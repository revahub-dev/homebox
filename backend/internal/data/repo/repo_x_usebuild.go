package repo

// Fork addition (use & build: idle-item nudges, "What can I build?" projects).
// See FORK_NOTES.md.

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/sysadminsmedia/homebox/backend/internal/data/ent"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/entity"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/group"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/tag"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/xentityactivity"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/xidledigest"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/xproject"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/xprojectpart"
)

// ErrNotAnItem is returned when an action targets a location or another
// collection's entity.
var ErrNotAnItem = errors.New("entity is not an item in this collection")

type XUseBuildRepository struct {
	db *ent.Client
}

func NewXUseBuildRepository(db *ent.Client) *XUseBuildRepository {
	return &XUseBuildRepository{db: db}
}

// ---------------------------------------------------------------------------
// Inventory snapshot

// XInvItem is one owned, non-archived item with the data idle detection and
// the build prompt need.
type XInvItem struct {
	ID            uuid.UUID  `json:"id"`
	Name          string     `json:"name"`
	Description   string     `json:"description"`
	Quantity      float64    `json:"quantity"`
	LocationPath  string     `json:"locationPath"`
	PurchasePrice float64    `json:"purchasePrice"`
	PurchaseDate  *time.Time `json:"purchaseDate,omitempty" extensions:"x-nullable"`
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`
	LastUsedAt    *time.Time `json:"lastUsedAt,omitempty"   extensions:"x-nullable"`
	UseCount      int        `json:"useCount"`
	SnoozedUntil  *time.Time `json:"snoozedUntil,omitempty" extensions:"x-nullable"`
	ForSale       bool       `json:"forSale"`
	SaleDraft     string     `json:"saleDraft"`
	// LastTouchedAt is the latest of created, edited and used.
	LastTouchedAt time.Time `json:"lastTouchedAt"`
	// ReservedQuantity is the total reserved by saved projects.
	ReservedQuantity float64 `json:"reservedQuantity"`

	ancestors []uuid.UUID
}

// XInventoryScope narrows Inventory. Zero value = whole collection.
type XInventoryScope struct {
	TagID      uuid.UUID
	LocationID uuid.UUID
}

// Inventory returns every non-archived, non-location entity in the group,
// joined with its activity row, its location path and reservations.
func (r *XUseBuildRepository) Inventory(ctx context.Context, gid uuid.UUID, scope XInventoryScope) ([]XInvItem, error) {
	all, err := r.db.Entity.Query().
		Where(entity.HasGroupWith(group.ID(gid))).
		WithEntityType().
		WithParent(func(q *ent.EntityQuery) { q.Select(entity.FieldID) }).
		WithLocation(func(q *ent.EntityQuery) { q.Select(entity.FieldID) }).
		All(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[uuid.UUID]*ent.Entity, len(all))
	for _, e := range all {
		byID[e.ID] = e
	}
	parentOf := func(e *ent.Entity) *ent.Entity {
		if e.Edges.Location != nil {
			return byID[e.Edges.Location.ID]
		}
		if e.Edges.Parent != nil {
			return byID[e.Edges.Parent.ID]
		}
		return nil
	}

	var tagged map[uuid.UUID]bool
	if scope.TagID != uuid.Nil {
		ids, err := r.db.Entity.Query().
			Where(entity.HasGroupWith(group.ID(gid)), entity.HasTagWith(tag.ID(scope.TagID))).
			IDs(ctx)
		if err != nil {
			return nil, err
		}
		tagged = make(map[uuid.UUID]bool, len(ids))
		for _, id := range ids {
			tagged[id] = true
		}
	}

	acts, err := r.db.XEntityActivity.Query().Where(xentityactivity.GroupID(gid)).All(ctx)
	if err != nil {
		return nil, err
	}
	actBy := make(map[uuid.UUID]*ent.XEntityActivity, len(acts))
	for _, a := range acts {
		actBy[a.EntityID] = a
	}
	reserved, err := r.reservedByEntity(ctx, gid)
	if err != nil {
		return nil, err
	}

	out := make([]XInvItem, 0, len(all))
	for _, e := range all {
		if e.Archived || (e.Edges.EntityType != nil && e.Edges.EntityType.IsLocation) {
			continue
		}
		if tagged != nil && !tagged[e.ID] {
			continue
		}
		it := XInvItem{
			ID: e.ID, Name: e.Name, Description: e.Description, Quantity: e.Quantity,
			PurchasePrice: e.PurchasePrice, CreatedAt: e.CreatedAt, UpdatedAt: e.UpdatedAt,
			ReservedQuantity: reserved[e.ID],
		}
		if !e.PurchaseDate.IsZero() {
			d := e.PurchaseDate
			it.PurchaseDate = &d
		}
		var names []string
		seen := map[uuid.UUID]bool{e.ID: true}
		for p := parentOf(e); p != nil && !seen[p.ID]; p = parentOf(p) {
			seen[p.ID] = true
			it.ancestors = append(it.ancestors, p.ID)
			names = append([]string{p.Name}, names...)
		}
		it.LocationPath = strings.Join(names, " > ")
		if scope.LocationID != uuid.Nil && !containsID(it.ancestors, scope.LocationID) {
			continue
		}
		it.LastTouchedAt = latest(e.CreatedAt, e.UpdatedAt)
		if a := actBy[e.ID]; a != nil {
			it.LastUsedAt, it.UseCount, it.SnoozedUntil = a.LastUsedAt, a.UseCount, a.SnoozedUntil
			it.ForSale, it.SaleDraft = a.ForSale, a.SaleDraft
			if a.LastUsedAt != nil {
				it.LastTouchedAt = latest(it.LastTouchedAt, *a.LastUsedAt)
			}
		}
		out = append(out, it)
	}
	return out, nil
}

func containsID(ids []uuid.UUID, id uuid.UUID) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

func latest(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// IdleItems returns items last touched before cutoff that are not snoozed
// (as of now) or already marked for sale, oldest first, up to limit
// (limit <= 0 means no limit).
func (r *XUseBuildRepository) IdleItems(ctx context.Context, gid uuid.UUID, cutoff, now time.Time, limit int) ([]XInvItem, error) {
	inv, err := r.Inventory(ctx, gid, XInventoryScope{})
	if err != nil {
		return nil, err
	}
	out := inv[:0]
	for _, it := range inv {
		if !it.LastTouchedAt.Before(cutoff) || it.ForSale {
			continue
		}
		if it.SnoozedUntil != nil && it.SnoozedUntil.After(now) {
			continue
		}
		out = append(out, it)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].LastTouchedAt.Before(out[j].LastTouchedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ForSaleItems lists items marked for sale.
func (r *XUseBuildRepository) ForSaleItems(ctx context.Context, gid uuid.UUID) ([]XInvItem, error) {
	inv, err := r.Inventory(ctx, gid, XInventoryScope{})
	if err != nil {
		return nil, err
	}
	out := inv[:0]
	for _, it := range inv {
		if it.ForSale {
			out = append(out, it)
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Activity

func (r *XUseBuildRepository) ensureItem(ctx context.Context, gid, eid uuid.UUID) (*ent.Entity, error) {
	e, err := r.db.Entity.Query().
		Where(entity.ID(eid), entity.HasGroupWith(group.ID(gid))).
		WithEntityType().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrNotAnItem
		}
		return nil, err
	}
	if e.Edges.EntityType != nil && e.Edges.EntityType.IsLocation {
		return nil, ErrNotAnItem
	}
	return e, nil
}

// upsertActivity applies fn to the entity's activity row, creating it first
// if needed.
func (r *XUseBuildRepository) upsertActivity(ctx context.Context, gid, eid uuid.UUID, fn func(*ent.XEntityActivityUpdateOne)) error {
	if _, err := r.ensureItem(ctx, gid, eid); err != nil {
		return err
	}
	a, err := r.db.XEntityActivity.Query().Where(xentityactivity.EntityID(eid)).Only(ctx)
	if ent.IsNotFound(err) {
		a, err = r.db.XEntityActivity.Create().SetGroupID(gid).SetEntityID(eid).Save(ctx)
		if ent.IsConstraintError(err) { // concurrent create
			a, err = r.db.XEntityActivity.Query().Where(xentityactivity.EntityID(eid)).Only(ctx)
		}
	}
	if err != nil {
		return err
	}
	u := a.Update()
	fn(u)
	return u.Exec(ctx)
}

// MarkUsed records a use: bumps last_used_at and use_count and clears any
// snooze and for-sale flag.
func (r *XUseBuildRepository) MarkUsed(ctx context.Context, gid, eid uuid.UUID, at time.Time) error {
	return r.upsertActivity(ctx, gid, eid, func(u *ent.XEntityActivityUpdateOne) {
		u.SetLastUsedAt(at).AddUseCount(1).ClearSnoozedUntil().SetForSale(false)
	})
}

// Snooze hides the item from idle lists until the given time.
func (r *XUseBuildRepository) Snooze(ctx context.Context, gid, eid uuid.UUID, until time.Time) error {
	return r.upsertActivity(ctx, gid, eid, func(u *ent.XEntityActivityUpdateOne) {
		u.SetSnoozedUntil(until)
	})
}

// SetForSale marks (or unmarks) the item for sale with a listing draft.
func (r *XUseBuildRepository) SetForSale(ctx context.Context, gid, eid uuid.UUID, forSale bool, draft string) error {
	return r.upsertActivity(ctx, gid, eid, func(u *ent.XEntityActivityUpdateOne) {
		u.SetForSale(forSale).SetSaleDraft(draft)
	})
}

// Archive sets the upstream archived flag on the item.
func (r *XUseBuildRepository) Archive(ctx context.Context, gid, eid uuid.UUID) error {
	if _, err := r.ensureItem(ctx, gid, eid); err != nil {
		return err
	}
	return r.db.Entity.UpdateOneID(eid).SetArchived(true).Exec(ctx)
}

// MarkNudged stamps last_nudged_at on the items included in a digest.
func (r *XUseBuildRepository) MarkNudged(ctx context.Context, gid uuid.UUID, ids []uuid.UUID, at time.Time) error {
	for _, id := range ids {
		if err := r.upsertActivity(ctx, gid, id, func(u *ent.XEntityActivityUpdateOne) { u.SetLastNudgedAt(at) }); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Digest bookkeeping

// LastDigest returns when the group last got an idle digest (nil if never).
func (r *XUseBuildRepository) LastDigest(ctx context.Context, gid uuid.UUID) (*time.Time, error) {
	d, err := r.db.XIdleDigest.Query().Where(xidledigest.GroupID(gid)).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &d.LastSentAt, nil
}

// RecordDigest stores the time of the latest digest for the group.
func (r *XUseBuildRepository) RecordDigest(ctx context.Context, gid uuid.UUID, at time.Time, count int) error {
	n, err := r.db.XIdleDigest.Update().Where(xidledigest.GroupID(gid)).SetLastSentAt(at).SetItemCount(count).Save(ctx)
	if err != nil {
		return err
	}
	if n == 0 {
		return r.db.XIdleDigest.Create().SetGroupID(gid).SetLastSentAt(at).SetItemCount(count).Exec(ctx)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Projects

type XProjectPartIn struct {
	EntityID uuid.UUID `json:"entityId" validate:"required"`
	Quantity float64   `json:"quantity"`
	Role     string    `json:"role"     validate:"max=255"`
}

type XMissingPart struct {
	Name     string  `json:"name"`
	Quantity float64 `json:"quantity"`
	Note     string  `json:"note"`
}

type XProjectIn struct {
	Name         string           `json:"name"         validate:"required,max=255"`
	Description  string           `json:"description"`
	BuildStatus  string           `json:"buildStatus"  validate:"omitempty,oneof=buildable_now missing_parts"`
	Steps        []string         `json:"steps"`
	OwnedParts   []XProjectPartIn `json:"ownedParts"`
	MissingParts []XMissingPart   `json:"missingParts"`
}

type XProjectPartOut struct {
	EntityID uuid.UUID `json:"entityId"`
	Name     string    `json:"name"`
	Quantity float64   `json:"quantity"`
	Role     string    `json:"role"`
}

type XProjectOut struct {
	ID           uuid.UUID         `json:"id"`
	CreatedAt    time.Time         `json:"createdAt"`
	Name         string            `json:"name"`
	Description  string            `json:"description"`
	BuildStatus  string            `json:"buildStatus"`
	Steps        []string          `json:"steps"`
	Parts        []XProjectPartOut `json:"parts"`
	MissingParts []XMissingPart    `json:"missingParts"`
}

// CreateProject saves a project and reserves its owned parts. Parts must be
// items in the same group; duplicates are summed.
func (r *XUseBuildRepository) CreateProject(ctx context.Context, gid uuid.UUID, in XProjectIn) (XProjectOut, error) {
	qty := map[uuid.UUID]float64{}
	role := map[uuid.UUID]string{}
	var order []uuid.UUID
	for _, p := range in.OwnedParts {
		if _, err := r.ensureItem(ctx, gid, p.EntityID); err != nil {
			return XProjectOut{}, err
		}
		q := p.Quantity
		if q <= 0 {
			q = 1
		}
		if _, ok := qty[p.EntityID]; !ok {
			order = append(order, p.EntityID)
			role[p.EntityID] = p.Role
		}
		qty[p.EntityID] += q
	}
	status := in.BuildStatus
	if status == "" {
		status = string(xproject.BuildStatusMissingParts)
	}
	steps, _ := json.Marshal(nonNil(in.Steps))
	missing, _ := json.Marshal(nonNilMissing(in.MissingParts))

	tx, err := r.db.Tx(ctx)
	if err != nil {
		return XProjectOut{}, err
	}
	p, err := tx.XProject.Create().SetGroupID(gid).SetName(in.Name).SetDescription(in.Description).
		SetBuildStatus(xproject.BuildStatus(status)).SetSteps(string(steps)).SetMissingParts(string(missing)).
		Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return XProjectOut{}, err
	}
	for _, id := range order {
		if err := tx.XProjectPart.Create().SetProjectID(p.ID).SetEntityID(id).SetQuantity(qty[id]).SetRole(role[id]).Exec(ctx); err != nil {
			_ = tx.Rollback()
			return XProjectOut{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return XProjectOut{}, err
	}
	return r.GetProject(ctx, gid, p.ID)
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func nonNilMissing(s []XMissingPart) []XMissingPart {
	if s == nil {
		return []XMissingPart{}
	}
	return s
}

func (r *XUseBuildRepository) projectOut(ctx context.Context, p *ent.XProject) XProjectOut {
	out := XProjectOut{
		ID: p.ID, CreatedAt: p.CreatedAt, Name: p.Name, Description: p.Description,
		BuildStatus: string(p.BuildStatus), Steps: []string{}, Parts: []XProjectPartOut{}, MissingParts: []XMissingPart{},
	}
	_ = json.Unmarshal([]byte(p.Steps), &out.Steps)
	_ = json.Unmarshal([]byte(p.MissingParts), &out.MissingParts)
	ids := make([]uuid.UUID, 0, len(p.Edges.Parts))
	for _, pp := range p.Edges.Parts {
		ids = append(ids, pp.EntityID)
	}
	names := map[uuid.UUID]string{}
	if len(ids) > 0 {
		es, err := r.db.Entity.Query().Where(entity.IDIn(ids...)).Select(entity.FieldID, entity.FieldName).All(ctx)
		if err == nil {
			for _, e := range es {
				names[e.ID] = e.Name
			}
		}
	}
	for _, pp := range p.Edges.Parts {
		out.Parts = append(out.Parts, XProjectPartOut{EntityID: pp.EntityID, Name: names[pp.EntityID], Quantity: pp.Quantity, Role: pp.Role})
	}
	return out
}

// GetProject returns one project of the group.
func (r *XUseBuildRepository) GetProject(ctx context.Context, gid, id uuid.UUID) (XProjectOut, error) {
	p, err := r.db.XProject.Query().Where(xproject.ID(id), xproject.GroupID(gid)).WithParts().Only(ctx)
	if err != nil {
		return XProjectOut{}, err
	}
	return r.projectOut(ctx, p), nil
}

// ListProjects returns the group's projects, newest first.
func (r *XUseBuildRepository) ListProjects(ctx context.Context, gid uuid.UUID) ([]XProjectOut, error) {
	ps, err := r.db.XProject.Query().Where(xproject.GroupID(gid)).WithParts().Order(ent.Desc(xproject.FieldCreatedAt)).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]XProjectOut, 0, len(ps))
	for _, p := range ps {
		out = append(out, r.projectOut(ctx, p))
	}
	return out, nil
}

// DeleteProject removes a project and releases its reservations.
func (r *XUseBuildRepository) DeleteProject(ctx context.Context, gid, id uuid.UUID) error {
	n, err := r.db.XProject.Delete().Where(xproject.ID(id), xproject.GroupID(gid)).Exec(ctx)
	if err != nil {
		return err
	}
	if n == 0 {
		return &ent.NotFoundError{}
	}
	return nil
}

func (r *XUseBuildRepository) reservedByEntity(ctx context.Context, gid uuid.UUID) (map[uuid.UUID]float64, error) {
	parts, err := r.db.XProjectPart.Query().Where(xprojectpart.HasProjectWith(xproject.GroupID(gid))).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]float64, len(parts))
	for _, p := range parts {
		out[p.EntityID] += p.Quantity
	}
	return out, nil
}

// Client exposes the ent client for scope lookups in handlers.
func (r *XUseBuildRepository) Client() *ent.Client { return r.db }
