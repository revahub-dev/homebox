package repo

// Fork addition (photo-first capture). See FORK_NOTES.md.

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/sysadminsmedia/homebox/backend/internal/data/ent"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/attachment"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/entity"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/entitytype"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/group"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/xcaptureitem"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/xcapturephoto"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/xcapturesession"
)

// ErrCaptureItemNotPending is returned when a review action targets an item
// that was already handled.
var ErrCaptureItemNotPending = errors.New("capture item is not pending")

// ErrCaptureSessionClosed is returned when photos are added to a finished session.
var ErrCaptureSessionClosed = errors.New("capture session is finished")

type XCaptureRepository struct {
	db *ent.Client
}

func NewXCaptureRepository(db *ent.Client) *XCaptureRepository { return &XCaptureRepository{db: db} }

type (
	XCaptureSession struct {
		ID         uuid.UUID
		GroupID    uuid.UUID
		UserID     uuid.UUID
		Mode       string
		LocationID *uuid.UUID
		Status     string
		CreatedAt  time.Time
	}

	XCapturePhoto struct {
		ID         uuid.UUID
		GroupID    uuid.UUID
		SessionID  uuid.UUID
		Status     string
		Attempts   int
		Error      string
		MIME       string
		Width      int
		Height     int
		Hash       string
		Image      []byte // only set by ClaimNextPhoto
		Provider   string
		LatencyMS  int
		Detections int
		CreatedAt  time.Time
	}

	XCaptureItem struct {
		ID               uuid.UUID
		GroupID          uuid.UUID
		SessionID        uuid.UUID
		PhotoID          uuid.UUID
		State            string
		Name             string
		Brand            string
		ModelNumber      string
		Category         string
		Quantity         float64
		Confidence       float64
		LowConfidence    bool
		BBox             string
		HasCrop          bool
		DupMatches       string
		TargetLocationID *uuid.UUID
		EntityID         *uuid.UUID
		AttachmentID     *uuid.UUID
		AppliedQuantity  float64
		HandledAt        *time.Time
		CreatedAt        time.Time
	}

	// XCaptureItemCreate is one detection to store.
	XCaptureItemCreate struct {
		Name          string
		Brand         string
		ModelNumber   string
		Category      string
		Quantity      float64
		Confidence    float64
		LowConfidence bool
		BBox          string
		Crop          []byte
		DupMatches    string
	}

	// XCaptureItemPatch edits a pending item. Nil fields are left alone.
	XCaptureItemPatch struct {
		Name             *string
		Brand            *string
		ModelNumber      *string
		Category         *string
		Quantity         *float64
		TargetLocationID *uuid.UUID
	}
)

func toSession(s *ent.XCaptureSession) XCaptureSession {
	return XCaptureSession{ID: s.ID, GroupID: s.GroupID, UserID: s.UserID, Mode: string(s.Mode), LocationID: s.LocationID, Status: string(s.Status), CreatedAt: s.CreatedAt}
}

func toPhoto(p *ent.XCapturePhoto) XCapturePhoto {
	return XCapturePhoto{
		ID: p.ID, GroupID: p.GroupID, SessionID: p.SessionID, Status: string(p.Status), Attempts: p.Attempts, Error: p.Error,
		MIME: p.MimeType, Width: p.Width, Height: p.Height, Hash: p.ContentHash, Image: p.Image, Provider: p.Provider,
		LatencyMS: p.LatencyMs, Detections: p.Detections, CreatedAt: p.CreatedAt,
	}
}

func toItem(i *ent.XCaptureItem) XCaptureItem {
	return XCaptureItem{
		ID: i.ID, GroupID: i.GroupID, SessionID: i.SessionID, PhotoID: i.PhotoID, State: string(i.State), Name: i.Name,
		Brand: i.Brand, ModelNumber: i.ModelNumber, Category: i.Category, Quantity: i.Quantity, Confidence: i.Confidence,
		LowConfidence: i.LowConfidence, BBox: i.Bbox, HasCrop: i.HasCrop, DupMatches: i.DupMatches,
		TargetLocationID: i.TargetLocationID, EntityID: i.EntityID, AttachmentID: i.AttachmentID,
		AppliedQuantity: i.AppliedQuantity, HandledAt: i.HandledAt, CreatedAt: i.CreatedAt,
	}
}

// itemFields lists every item column except the crop blob.
var itemFields = []string{
	xcaptureitem.FieldID, xcaptureitem.FieldCreatedAt, xcaptureitem.FieldGroupID, xcaptureitem.FieldSessionID,
	xcaptureitem.FieldPhotoID, xcaptureitem.FieldState, xcaptureitem.FieldName, xcaptureitem.FieldBrand,
	xcaptureitem.FieldModelNumber, xcaptureitem.FieldCategory, xcaptureitem.FieldQuantity, xcaptureitem.FieldConfidence,
	xcaptureitem.FieldLowConfidence, xcaptureitem.FieldBbox, xcaptureitem.FieldHasCrop, xcaptureitem.FieldDupMatches,
	xcaptureitem.FieldTargetLocationID, xcaptureitem.FieldEntityID, xcaptureitem.FieldAttachmentID,
	xcaptureitem.FieldAppliedQuantity, xcaptureitem.FieldHandledAt,
}

// photoFields lists every photo column except the image blob.
var photoFields = []string{
	xcapturephoto.FieldID, xcapturephoto.FieldCreatedAt, xcapturephoto.FieldGroupID, xcapturephoto.FieldSessionID,
	xcapturephoto.FieldStatus, xcapturephoto.FieldAttempts, xcapturephoto.FieldError, xcapturephoto.FieldMimeType,
	xcapturephoto.FieldWidth, xcapturephoto.FieldHeight, xcapturephoto.FieldContentHash, xcapturephoto.FieldProvider,
	xcapturephoto.FieldLatencyMs, xcapturephoto.FieldDetections,
}

// ---------------------------------------------------------------- sessions

func (r *XCaptureRepository) CreateSession(ctx context.Context, gid, uid uuid.UUID, mode string, location *uuid.UUID) (XCaptureSession, error) {
	s, err := r.db.XCaptureSession.Create().SetGroupID(gid).SetUserID(uid).SetMode(xcapturesession.Mode(mode)).
		SetNillableLocationID(location).Save(ctx)
	if err != nil {
		return XCaptureSession{}, err
	}
	return toSession(s), nil
}

func (r *XCaptureRepository) GetSession(ctx context.Context, gid, id uuid.UUID) (XCaptureSession, error) {
	s, err := r.db.XCaptureSession.Query().Where(xcapturesession.ID(id), xcapturesession.GroupID(gid)).Only(ctx)
	if err != nil {
		return XCaptureSession{}, err
	}
	return toSession(s), nil
}

func (r *XCaptureRepository) FinishSession(ctx context.Context, gid, id uuid.UUID) error {
	n, err := r.db.XCaptureSession.Update().Where(xcapturesession.ID(id), xcapturesession.GroupID(gid)).
		SetStatus(xcapturesession.StatusDone).Save(ctx)
	if err != nil {
		return err
	}
	if n == 0 {
		return &ent.NotFoundError{}
	}
	return nil
}

// ------------------------------------------------------------------ photos

// AddPhoto queues a photo. An identical photo (same hash) already in the
// session is returned with created=false.
func (r *XCaptureRepository) AddPhoto(ctx context.Context, s XCaptureSession, hash, mime string, w, h int, image []byte) (XCapturePhoto, bool, error) {
	if s.Status != "open" {
		return XCapturePhoto{}, false, ErrCaptureSessionClosed
	}
	existing, err := r.db.XCapturePhoto.Query().Where(xcapturephoto.SessionID(s.ID), xcapturephoto.ContentHash(hash)).
		Select(photoFields...).Only(ctx)
	if err == nil {
		return toPhoto(existing), false, nil
	}
	if !ent.IsNotFound(err) {
		return XCapturePhoto{}, false, err
	}
	p, err := r.db.XCapturePhoto.Create().SetGroupID(s.GroupID).SetSessionID(s.ID).SetContentHash(hash).
		SetMimeType(mime).SetWidth(w).SetHeight(h).SetImage(image).Save(ctx)
	if err != nil {
		if ent.IsConstraintError(err) { // lost a race with an identical upload
			if again, e2 := r.db.XCapturePhoto.Query().Where(xcapturephoto.SessionID(s.ID), xcapturephoto.ContentHash(hash)).
				Select(photoFields...).Only(ctx); e2 == nil {
				return toPhoto(again), false, nil
			}
		}
		return XCapturePhoto{}, false, err
	}
	p.Image = nil
	return toPhoto(p), true, nil
}

// PhotosToday counts photos queued by the group since the given time.
func (r *XCaptureRepository) PhotosSince(ctx context.Context, gid uuid.UUID, since time.Time) (int, error) {
	return r.db.XCapturePhoto.Query().Where(xcapturephoto.GroupID(gid), xcapturephoto.CreatedAtGTE(since)).Count(ctx)
}

func (r *XCaptureRepository) SessionPhotos(ctx context.Context, gid, sessionID uuid.UUID) ([]XCapturePhoto, error) {
	ps, err := r.db.XCapturePhoto.Query().Where(xcapturephoto.GroupID(gid), xcapturephoto.SessionID(sessionID)).
		Select(photoFields...).Order(ent.Asc(xcapturephoto.FieldCreatedAt)).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]XCapturePhoto, len(ps))
	for i, p := range ps {
		out[i] = toPhoto(p)
	}
	return out, nil
}

// QueueDepth counts queued and running photos (all groups).
func (r *XCaptureRepository) QueueDepth(ctx context.Context) (int, error) {
	return r.db.XCapturePhoto.Query().Where(xcapturephoto.StatusIn(xcapturephoto.StatusQueued, xcapturephoto.StatusRunning)).Count(ctx)
}

// RequeueStale puts photos whose worker died (running, lock older than
// staleBefore) back in the queue, or fails them after maxAttempts.
func (r *XCaptureRepository) RequeueStale(ctx context.Context, staleBefore time.Time, maxAttempts int) error {
	if _, err := r.db.XCapturePhoto.Update().Where(
		xcapturephoto.StatusEQ(xcapturephoto.StatusRunning), xcapturephoto.LockedAtLT(staleBefore),
		xcapturephoto.AttemptsLT(maxAttempts),
	).SetStatus(xcapturephoto.StatusQueued).ClearLockedAt().Save(ctx); err != nil {
		return err
	}
	_, err := r.db.XCapturePhoto.Update().Where(
		xcapturephoto.StatusEQ(xcapturephoto.StatusRunning), xcapturephoto.LockedAtLT(staleBefore),
		xcapturephoto.AttemptsGTE(maxAttempts),
	).SetStatus(xcapturephoto.StatusFailed).SetError("analysis timed out").ClearLockedAt().Save(ctx)
	return err
}

// ClaimNextPhoto atomically takes the oldest queued photo. Returns nil when
// the queue is empty. Safe with several workers or several processes: the
// UPDATE only succeeds for one claimant.
func (r *XCaptureRepository) ClaimNextPhoto(ctx context.Context, now time.Time) (*XCapturePhoto, error) {
	for i := 0; i < 5; i++ {
		cand, err := r.db.XCapturePhoto.Query().Where(xcapturephoto.StatusEQ(xcapturephoto.StatusQueued)).
			Order(ent.Asc(xcapturephoto.FieldCreatedAt)).Select(xcapturephoto.FieldID, xcapturephoto.FieldAttempts).First(ctx)
		if ent.IsNotFound(err) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		n, err := r.db.XCapturePhoto.Update().Where(xcapturephoto.ID(cand.ID), xcapturephoto.StatusEQ(xcapturephoto.StatusQueued)).
			SetStatus(xcapturephoto.StatusRunning).SetLockedAt(now).AddAttempts(1).Save(ctx)
		if err != nil {
			return nil, err
		}
		if n == 0 {
			continue // someone else got it
		}
		p, err := r.db.XCapturePhoto.Get(ctx, cand.ID)
		if err != nil {
			return nil, err
		}
		out := toPhoto(p)
		return &out, nil
	}
	return nil, nil
}

// CompletePhoto stores the detections of a photo and marks it done, in one transaction.
func (r *XCaptureRepository) CompletePhoto(ctx context.Context, p XCapturePhoto, targetLocation *uuid.UUID, provider string, latency time.Duration, items []XCaptureItemCreate) error {
	tx, err := r.db.Tx(ctx)
	if err != nil {
		return err
	}
	for _, it := range items {
		c := tx.XCaptureItem.Create().SetGroupID(p.GroupID).SetSessionID(p.SessionID).SetPhotoID(p.ID).
			SetName(it.Name).SetBrand(it.Brand).SetModelNumber(it.ModelNumber).SetCategory(it.Category).
			SetQuantity(it.Quantity).SetConfidence(it.Confidence).SetLowConfidence(it.LowConfidence).
			SetBbox(it.BBox).SetDupMatches(it.DupMatches).SetNillableTargetLocationID(targetLocation)
		if len(it.Crop) > 0 {
			c = c.SetCrop(it.Crop).SetHasCrop(true)
		}
		if err := c.Exec(ctx); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	if err := tx.XCapturePhoto.UpdateOneID(p.ID).SetStatus(xcapturephoto.StatusDone).SetProvider(provider).
		SetLatencyMs(int(latency.Milliseconds())).SetDetections(len(items)).SetError("").ClearLockedAt().Exec(ctx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// FailPhoto records a failure. With retry the photo goes back to the queue
// (the caller decides based on attempts).
func (r *XCaptureRepository) FailPhoto(ctx context.Context, id uuid.UUID, msg string, retry bool) error {
	if len(msg) > 900 {
		msg = msg[:900]
	}
	st := xcapturephoto.StatusFailed
	if retry {
		st = xcapturephoto.StatusQueued
	}
	return r.db.XCapturePhoto.UpdateOneID(id).SetStatus(st).SetError(msg).ClearLockedAt().Exec(ctx)
}

// RetryPhoto re-queues a failed photo owned by the group.
func (r *XCaptureRepository) RetryPhoto(ctx context.Context, gid, id uuid.UUID) error {
	n, err := r.db.XCapturePhoto.Update().Where(xcapturephoto.ID(id), xcapturephoto.GroupID(gid), xcapturephoto.StatusEQ(xcapturephoto.StatusFailed)).
		SetStatus(xcapturephoto.StatusQueued).SetError("").Save(ctx)
	if err != nil {
		return err
	}
	if n == 0 {
		return &ent.NotFoundError{}
	}
	return nil
}

// PurgeBefore drops stored photo bytes and unreviewed crops older than the
// cutoff, and deletes whole sessions older than sessionCutoff.
func (r *XCaptureRepository) PurgeBefore(ctx context.Context, cutoff, sessionCutoff time.Time) error {
	if _, err := r.db.XCapturePhoto.Update().Where(xcapturephoto.CreatedAtLT(cutoff)).ClearImage().Save(ctx); err != nil {
		return err
	}
	if _, err := r.db.XCaptureItem.Update().Where(
		xcaptureitem.CreatedAtLT(cutoff), xcaptureitem.StateIn(xcaptureitem.StatePending, xcaptureitem.StateIgnored),
	).ClearCrop().SetHasCrop(false).Save(ctx); err != nil {
		return err
	}
	_, err := r.db.XCaptureSession.Delete().Where(xcapturesession.CreatedAtLT(sessionCutoff)).Exec(ctx)
	return err
}

// ------------------------------------------------------------------- items

func (r *XCaptureRepository) SessionItems(ctx context.Context, gid, sessionID uuid.UUID) ([]XCaptureItem, error) {
	is, err := r.db.XCaptureItem.Query().Where(xcaptureitem.GroupID(gid), xcaptureitem.SessionID(sessionID)).
		Select(itemFields...).Order(ent.Desc(xcaptureitem.FieldConfidence), ent.Asc(xcaptureitem.FieldCreatedAt)).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]XCaptureItem, len(is))
	for i, it := range is {
		out[i] = toItem(it)
	}
	return out, nil
}

func (r *XCaptureRepository) GetItem(ctx context.Context, gid, id uuid.UUID) (XCaptureItem, error) {
	i, err := r.db.XCaptureItem.Query().Where(xcaptureitem.ID(id), xcaptureitem.GroupID(gid)).Select(itemFields...).Only(ctx)
	if err != nil {
		return XCaptureItem{}, err
	}
	return toItem(i), nil
}

// ItemCrop returns the JPEG crop of an item in the group.
func (r *XCaptureRepository) ItemCrop(ctx context.Context, gid, id uuid.UUID) ([]byte, error) {
	i, err := r.db.XCaptureItem.Query().Where(xcaptureitem.ID(id), xcaptureitem.GroupID(gid)).
		Select(xcaptureitem.FieldCrop).Only(ctx)
	if err != nil {
		return nil, err
	}
	if len(i.Crop) == 0 {
		return nil, &ent.NotFoundError{}
	}
	return i.Crop, nil
}

// PatchItem edits a pending item.
func (r *XCaptureRepository) PatchItem(ctx context.Context, gid, id uuid.UUID, p XCaptureItemPatch) error {
	u := r.db.XCaptureItem.Update().Where(xcaptureitem.ID(id), xcaptureitem.GroupID(gid), xcaptureitem.StateEQ(xcaptureitem.StatePending))
	if p.Name != nil {
		u = u.SetName(*p.Name)
	}
	if p.Brand != nil {
		u = u.SetBrand(*p.Brand)
	}
	if p.ModelNumber != nil {
		u = u.SetModelNumber(*p.ModelNumber)
	}
	if p.Category != nil {
		u = u.SetCategory(*p.Category)
	}
	if p.Quantity != nil {
		u = u.SetQuantity(*p.Quantity)
	}
	if p.TargetLocationID != nil {
		u = u.SetTargetLocationID(*p.TargetLocationID)
	}
	n, err := u.Save(ctx)
	if err != nil {
		return err
	}
	if n == 0 {
		return r.notPendingOrMissing(ctx, gid, id)
	}
	return nil
}

func (r *XCaptureRepository) notPendingOrMissing(ctx context.Context, gid, id uuid.UUID) error {
	ok, err := r.db.XCaptureItem.Query().Where(xcaptureitem.ID(id), xcaptureitem.GroupID(gid)).Exist(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return &ent.NotFoundError{}
	}
	return ErrCaptureItemNotPending
}

// ClaimItem atomically moves a pending item to state (accepted, merged or
// ignored). Exactly one concurrent caller wins; others get ErrCaptureItemNotPending.
func (r *XCaptureRepository) ClaimItem(ctx context.Context, gid, id uuid.UUID, state string, at time.Time) error {
	n, err := r.db.XCaptureItem.Update().Where(xcaptureitem.ID(id), xcaptureitem.GroupID(gid), xcaptureitem.StateEQ(xcaptureitem.StatePending)).
		SetState(xcaptureitem.State(state)).SetHandledAt(at).Save(ctx)
	if err != nil {
		return err
	}
	if n == 0 {
		return r.notPendingOrMissing(ctx, gid, id)
	}
	return nil
}

// ReleaseItem puts a claimed item back to pending (error path and undo).
func (r *XCaptureRepository) ReleaseItem(ctx context.Context, gid, id uuid.UUID) error {
	return r.db.XCaptureItem.Update().Where(xcaptureitem.ID(id), xcaptureitem.GroupID(gid)).
		SetState(xcaptureitem.StatePending).ClearHandledAt().ClearEntityID().ClearAttachmentID().SetAppliedQuantity(0).Exec(ctx)
}

// SetItemResult records what accept/merge created so Undo can reverse it.
func (r *XCaptureRepository) SetItemResult(ctx context.Context, id, entityID uuid.UUID, attachmentID *uuid.UUID, appliedQty float64) error {
	return r.db.XCaptureItem.UpdateOneID(id).SetEntityID(entityID).SetNillableAttachmentID(attachmentID).SetAppliedQuantity(appliedQty).Exec(ctx)
}

// ----------------------------------------------------------------- helpers

// EntityInGroup reports whether the entity exists in the group.
func (r *XCaptureRepository) EntityInGroup(ctx context.Context, gid, id uuid.UUID) (bool, error) {
	return r.db.Entity.Query().Where(entity.ID(id), entity.HasGroupWith(group.ID(gid))).Exist(ctx)
}

// IsLocation reports whether id is a location entity of the group.
func (r *XCaptureRepository) IsLocation(ctx context.Context, gid, id uuid.UUID) (bool, error) {
	e, err := r.db.Entity.Query().Where(entity.ID(id), entity.HasGroupWith(group.ID(gid))).WithEntityType().Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return e.Edges.EntityType != nil && e.Edges.EntityType.IsLocation, nil
}

// AddQuantity changes an entity's quantity by delta (never below 0) and
// returns the applied delta.
func (r *XCaptureRepository) AddQuantity(ctx context.Context, gid, id uuid.UUID, delta float64) (float64, error) {
	e, err := r.db.Entity.Query().Where(entity.ID(id), entity.HasGroupWith(group.ID(gid))).Select(entity.FieldID, entity.FieldQuantity).Only(ctx)
	if err != nil {
		return 0, err
	}
	next := e.Quantity + delta
	if next < 0 {
		next = 0
	}
	if err := r.db.Entity.UpdateOneID(id).SetQuantity(next).Exec(ctx); err != nil {
		return 0, err
	}
	return next - e.Quantity, nil
}

// EntityHasPrimaryPhoto reports whether the entity already has a primary photo.
func (r *XCaptureRepository) EntityHasPrimaryPhoto(ctx context.Context, id uuid.UUID) (bool, error) {
	return r.db.Attachment.Query().Where(attachment.HasEntityWith(entity.ID(id)), attachment.Primary(true)).Exist(ctx)
}

// FindUnsortedLocation returns the group's "Unsorted" location by name (uuid.Nil if none).
func (r *XCaptureRepository) FindLocationByName(ctx context.Context, gid uuid.UUID, name string) (uuid.UUID, error) {
	e, err := r.db.Entity.Query().Where(entity.HasGroupWith(group.ID(gid)), entity.Name(name), entity.Archived(false),
		entity.HasEntityTypeWith(entitytype.IsLocation(true))).First(ctx)
	if ent.IsNotFound(err) {
		return uuid.Nil, nil
	}
	if err != nil {
		return uuid.Nil, err
	}
	return e.ID, nil
}

// ErrCaptureItemNotHandled is returned by ReopenItem when the item is pending.
var ErrCaptureItemNotHandled = errors.New("capture item has not been handled")

// ReopenItem atomically moves a handled item back to pending and clears its
// result. Exactly one concurrent caller wins.
func (r *XCaptureRepository) ReopenItem(ctx context.Context, gid, id uuid.UUID) error {
	n, err := r.db.XCaptureItem.Update().Where(
		xcaptureitem.ID(id), xcaptureitem.GroupID(gid), xcaptureitem.StateNEQ(xcaptureitem.StatePending),
	).SetState(xcaptureitem.StatePending).ClearHandledAt().ClearEntityID().ClearAttachmentID().SetAppliedQuantity(0).Save(ctx)
	if err != nil {
		return err
	}
	if n == 0 {
		ok, err := r.db.XCaptureItem.Query().Where(xcaptureitem.ID(id), xcaptureitem.GroupID(gid)).Exist(ctx)
		if err != nil {
			return err
		}
		if !ok {
			return &ent.NotFoundError{}
		}
		return ErrCaptureItemNotHandled
	}
	return nil
}

// SetDupMatches replaces the stored duplicate snapshot of an item.
func (r *XCaptureRepository) SetDupMatches(ctx context.Context, gid, id uuid.UUID, dupJSON string) error {
	return r.db.XCaptureItem.Update().Where(xcaptureitem.ID(id), xcaptureitem.GroupID(gid)).SetDupMatches(dupJSON).Exec(ctx)
}
