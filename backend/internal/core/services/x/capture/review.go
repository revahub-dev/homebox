package capture

import (
	"bytes"
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/sysadminsmedia/homebox/backend/internal/data/ent"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/attachment"
	"github.com/sysadminsmedia/homebox/backend/internal/data/repo"
)

// EditInput is a partial edit of a pending item. Nil fields are unchanged.
type EditInput struct {
	Name             *string    `json:"name"             validate:"omitempty,min=1,max=255"`
	Brand            *string    `json:"brand"            validate:"omitempty,max=255"`
	ModelNumber      *string    `json:"modelNumber"      validate:"omitempty,max=255"`
	Category         *string    `json:"category"         validate:"omitempty,max=255"`
	Quantity         *float64   `json:"quantity"         validate:"omitempty,min=0.001,max=999999"`
	TargetLocationID *uuid.UUID `json:"targetLocationId" extensions:"x-nullable"`
}

// Edit changes a pending item and refreshes its duplicate banner.
func (s *Service) Edit(ctx context.Context, gid, id uuid.UUID, in EditInput) (ItemOut, error) {
	patch := repo.XCaptureItemPatch{Brand: in.Brand, ModelNumber: in.ModelNumber, Category: in.Category, Quantity: in.Quantity}
	if in.Name != nil {
		n := truncate(strings.TrimSpace(*in.Name), 255)
		if n == "" {
			return ItemOut{}, ErrBadItemInput
		}
		patch.Name = &n
	}
	if in.TargetLocationID != nil {
		ok, err := s.Repo.IsLocation(ctx, gid, *in.TargetLocationID)
		if err != nil {
			return ItemOut{}, err
		}
		if !ok {
			return ItemOut{}, ErrBadLocation
		}
		patch.TargetLocationID = in.TargetLocationID
	}
	if err := s.Repo.PatchItem(ctx, gid, id, patch); err != nil {
		return ItemOut{}, err
	}
	it, err := s.Repo.GetItem(ctx, gid, id)
	if err != nil {
		return ItemOut{}, err
	}
	if in.Name != nil || in.ModelNumber != nil {
		if err := s.refreshDups(ctx, gid, it); err != nil {
			log.Warn().Err(err).Msg("capture: refresh duplicates failed")
		} else if it, err = s.Repo.GetItem(ctx, gid, id); err != nil {
			return ItemOut{}, err
		}
	}
	return s.itemOut(it), nil
}

// AcceptResult is returned by accept and merge.
type AcceptResult struct {
	Item     ItemOut   `json:"item"`
	EntityID uuid.UUID `json:"entityId"`
}

// Accept creates an inventory item from a pending detection.
func (s *Service) Accept(ctx context.Context, gid, id uuid.UUID) (AcceptResult, error) {
	it, err := s.Repo.GetItem(ctx, gid, id)
	if err != nil {
		return AcceptResult{}, err
	}
	if it.State != statePending {
		return AcceptResult{}, repo.ErrCaptureItemNotPending
	}
	if err := s.Repo.ClaimItem(ctx, gid, id, "accepted", s.now()); err != nil {
		return AcceptResult{}, err
	}
	fail := func(err error) (AcceptResult, error) {
		_ = s.Repo.ReleaseItem(context.WithoutCancel(ctx), gid, id)
		return AcceptResult{}, err
	}

	parent, err := s.targetLocation(ctx, gid, it)
	if err != nil {
		return fail(err)
	}
	qty := it.Quantity
	if qty <= 0 {
		qty = 1
	}
	create := repo.EntityCreate{
		Name: truncate(it.Name, 255), Quantity: qty, Description: "Added from a photo.",
		ParentID: parent, ModelNumber: truncate(it.ModelNumber, 255), Manufacturer: truncate(it.Brand, 255),
	}
	if tag := s.matchTag(ctx, gid, it.Category); tag != uuid.Nil {
		create.TagIDs = []uuid.UUID{tag}
	}
	created, err := s.Entities.Create(ctx, gid, create)
	if err != nil {
		return fail(err)
	}
	attID := s.attachCrop(ctx, gid, id, created.ID, true)
	if err := s.Repo.SetItemResult(ctx, id, created.ID, attID, qty); err != nil {
		_ = s.Entities.DeleteByGroup(ctx, gid, created.ID)
		return fail(err)
	}
	return s.result(ctx, gid, id, created.ID)
}

// Merge adds a pending detection's quantity to an existing item.
func (s *Service) Merge(ctx context.Context, gid, id, entityID uuid.UUID) (AcceptResult, error) {
	ok, err := s.Repo.EntityInGroup(ctx, gid, entityID)
	if err != nil {
		return AcceptResult{}, err
	}
	isLoc, err := s.Repo.IsLocation(ctx, gid, entityID)
	if err != nil {
		return AcceptResult{}, err
	}
	if !ok || isLoc {
		return AcceptResult{}, ErrBadTarget
	}
	it, err := s.Repo.GetItem(ctx, gid, id)
	if err != nil {
		return AcceptResult{}, err
	}
	if it.State != statePending {
		return AcceptResult{}, repo.ErrCaptureItemNotPending
	}
	if err := s.Repo.ClaimItem(ctx, gid, id, "merged", s.now()); err != nil {
		return AcceptResult{}, err
	}
	qty := it.Quantity
	if qty <= 0 {
		qty = 1
	}
	applied, err := s.Repo.AddQuantity(ctx, gid, entityID, qty)
	if err != nil {
		_ = s.Repo.ReleaseItem(context.WithoutCancel(ctx), gid, id)
		return AcceptResult{}, err
	}
	var attID *uuid.UUID
	if has, err := s.Repo.EntityHasPrimaryPhoto(ctx, entityID); err == nil && !has {
		attID = s.attachCrop(ctx, gid, id, entityID, true)
	}
	if err := s.Repo.SetItemResult(ctx, id, entityID, attID, applied); err != nil {
		_, _ = s.Repo.AddQuantity(ctx, gid, entityID, -applied)
		_ = s.Repo.ReleaseItem(context.WithoutCancel(ctx), gid, id)
		return AcceptResult{}, err
	}
	return s.result(ctx, gid, id, entityID)
}

// Ignore dismisses a pending detection.
func (s *Service) Ignore(ctx context.Context, gid, id uuid.UUID) (ItemOut, error) {
	if err := s.Repo.ClaimItem(ctx, gid, id, "ignored", s.now()); err != nil {
		return ItemOut{}, err
	}
	it, err := s.Repo.GetItem(ctx, gid, id)
	if err != nil {
		return ItemOut{}, err
	}
	return s.itemOut(it), nil
}

// Undo reverses accept, merge or ignore within UndoWindow.
func (s *Service) Undo(ctx context.Context, gid, id uuid.UUID) (ItemOut, error) {
	it, err := s.Repo.GetItem(ctx, gid, id)
	if err != nil {
		return ItemOut{}, err
	}
	if it.State == statePending || it.HandledAt == nil || s.now().Sub(*it.HandledAt) > UndoWindow {
		return ItemOut{}, ErrNotUndoable
	}
	// Claim first so a double tap cannot reverse twice.
	if err := s.Repo.ReopenItem(ctx, gid, id); err != nil {
		if errors.Is(err, repo.ErrCaptureItemNotHandled) {
			return ItemOut{}, ErrNotUndoable
		}
		return ItemOut{}, err
	}
	bg := context.WithoutCancel(ctx)
	switch it.State {
	case "accepted":
		if it.EntityID != nil {
			if err := s.Entities.DeleteByGroup(bg, gid, *it.EntityID); err != nil && !ent.IsNotFound(err) {
				log.Warn().Err(err).Msg("capture undo: could not delete created item")
			}
		}
	case "merged":
		if it.EntityID != nil {
			if it.AppliedQuantity != 0 {
				if _, err := s.Repo.AddQuantity(bg, gid, *it.EntityID, -it.AppliedQuantity); err != nil && !ent.IsNotFound(err) {
					log.Warn().Err(err).Msg("capture undo: could not restore quantity")
				}
			}
			if it.AttachmentID != nil {
				if err := s.Attachments.Delete(bg, gid, *it.AttachmentID); err != nil && !ent.IsNotFound(err) {
					log.Warn().Err(err).Msg("capture undo: could not remove photo")
				}
			}
		}
	}
	it, err = s.Repo.GetItem(ctx, gid, id)
	if err != nil {
		return ItemOut{}, err
	}
	return s.itemOut(it), nil
}

// BulkResult summarises AcceptReady.
type BulkResult struct {
	Accepted int         `json:"accepted"`
	Skipped  int         `json:"skipped"`
	ItemIDs  []uuid.UUID `json:"itemIds"`
}

// AcceptReady accepts every pending item in the session that is in the
// "ready" bucket and has no strong duplicate match.
func (s *Service) AcceptReady(ctx context.Context, gid, sessionID uuid.UUID) (BulkResult, error) {
	items, err := s.Repo.SessionItems(ctx, gid, sessionID)
	if err != nil {
		return BulkResult{}, err
	}
	res := BulkResult{ItemIDs: []uuid.UUID{}}
	for _, it := range items {
		o := s.itemOut(it)
		if !o.Ready {
			continue
		}
		if _, err := s.Accept(ctx, gid, it.ID); err != nil {
			if errors.Is(err, repo.ErrCaptureItemNotPending) {
				continue
			}
			log.Warn().Err(err).Str("item", it.ID.String()).Msg("capture: bulk accept skipped an item")
			res.Skipped++
			continue
		}
		res.Accepted++
		res.ItemIDs = append(res.ItemIDs, it.ID)
	}
	return res, nil
}

// ------------------------------------------------------------------ helpers

func (s *Service) result(ctx context.Context, gid, id, entityID uuid.UUID) (AcceptResult, error) {
	it, err := s.Repo.GetItem(ctx, gid, id)
	if err != nil {
		return AcceptResult{}, err
	}
	return AcceptResult{Item: s.itemOut(it), EntityID: entityID}, nil
}

// targetLocation resolves where a new item goes: the item's own location,
// else the session's, else the group's "Unsorted" location (created on demand).
func (s *Service) targetLocation(ctx context.Context, gid uuid.UUID, it repo.XCaptureItem) (uuid.UUID, error) {
	if it.TargetLocationID != nil {
		if ok, err := s.Repo.IsLocation(ctx, gid, *it.TargetLocationID); err != nil {
			return uuid.Nil, err
		} else if ok {
			return *it.TargetLocationID, nil
		}
	}
	if sess, err := s.Repo.GetSession(ctx, gid, it.SessionID); err == nil && sess.LocationID != nil {
		if ok, err := s.Repo.IsLocation(ctx, gid, *sess.LocationID); err != nil {
			return uuid.Nil, err
		} else if ok {
			return *sess.LocationID, nil
		}
	}
	id, err := s.Repo.FindLocationByName(ctx, gid, UnsortedName)
	if err != nil {
		return uuid.Nil, err
	}
	if id != uuid.Nil {
		return id, nil
	}
	et, err := s.EntityTypes.GetDefault(ctx, gid, true)
	if err != nil {
		return uuid.Nil, err
	}
	loc, err := s.Entities.Create(ctx, gid, repo.EntityCreate{Name: UnsortedName, Description: "Items added from photos that had no location yet.", EntityTypeID: et.ID})
	if err != nil {
		return uuid.Nil, err
	}
	return loc.ID, nil
}

// matchTag finds an existing tag whose name equals the model's category.
// Tags are never created automatically.
func (s *Service) matchTag(ctx context.Context, gid uuid.UUID, category string) uuid.UUID {
	category = strings.TrimSpace(category)
	if category == "" || s.Tags == nil {
		return uuid.Nil
	}
	tags, err := s.Tags.GetAll(ctx, gid)
	if err != nil {
		return uuid.Nil
	}
	for _, t := range tags {
		if strings.EqualFold(t.Name, category) {
			return t.ID
		}
	}
	return uuid.Nil
}

// attachCrop stores the item's crop on an entity as its photo. Failures are
// logged, not fatal: the item itself is more important than its picture.
func (s *Service) attachCrop(ctx context.Context, gid, itemID, entityID uuid.UUID, primary bool) *uuid.UUID {
	if s.Attachments == nil {
		return nil
	}
	crop, err := s.Repo.ItemCrop(ctx, gid, itemID)
	if err != nil || len(crop) == 0 {
		return nil
	}
	a, err := s.Attachments.Create(ctx, entityID, repo.ItemCreateAttachment{Title: "photo.jpg", Content: bytes.NewReader(crop)}, attachment.TypePhoto, primary)
	if err != nil {
		log.Warn().Err(err).Msg("capture: could not attach photo")
		return nil
	}
	id := a.ID
	return &id
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
