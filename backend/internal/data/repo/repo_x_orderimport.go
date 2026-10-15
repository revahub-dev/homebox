package repo

// Fork addition (order import). See FORK_NOTES.md.

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/entity"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/entityfield"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/group"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/ximportcandidate"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/xinboundaddress"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/xinboundmessage"
)

// ErrCandidateNotPending is returned when a review action targets a candidate
// that was already handled (or is the wrong kind).
var ErrCandidateNotPending = errors.New("import candidate is not pending")

// XOrderImportRepository stores inbound addresses, raw messages and review
// candidates for the order-import feature.
type XOrderImportRepository struct {
	db *ent.Client
}

func NewXOrderImportRepository(db *ent.Client) *XOrderImportRepository {
	return &XOrderImportRepository{db: db}
}

type (
	XInboundAddress struct {
		ID             uuid.UUID  `json:"id"`
		GroupID        uuid.UUID  `json:"-"`
		Token          string     `json:"token"`
		AllowedSenders []string   `json:"allowedSenders"`
		LastReceivedAt *time.Time `json:"lastReceivedAt,omitempty" extensions:"x-nullable"`
	}

	XMessageCreate struct {
		Channel     string
		Status      string
		MessageID   string
		ContentHash string
		Store       string
		OrderNumber string
		OrderDate   *time.Time
		OrderTotal  float64
		Sender      string
		Subject     string
		MimeType    string
		Extractor   string
		Error       string
		Raw         []byte
	}

	XCandidateCreate struct {
		Kind          string
		Name          string
		Quantity      float64
		UnitPrice     float64
		Currency      string
		Store         string
		OrderNumber   string
		OrderDate     *time.Time
		ASIN          string
		JAN           string
		ModelNumber   string
		URL           string
		Note          string
		Confidence    float64
		LowConfidence bool
		Extractor     string
		DupMatches    string
	}

	XImportCandidate struct {
		ID            uuid.UUID  `json:"id"`
		CreatedAt     time.Time  `json:"createdAt"`
		Kind          string     `json:"kind"                enums:"item,notice"`
		Status        string     `json:"status"              enums:"pending,accepted,merged,ignored"`
		Name          string     `json:"name"`
		Quantity      float64    `json:"quantity"`
		UnitPrice     float64    `json:"unitPrice"`
		Currency      string     `json:"currency"`
		Store         string     `json:"store"`
		OrderNumber   string     `json:"orderNumber"`
		OrderDate     *time.Time `json:"orderDate,omitempty" extensions:"x-nullable"`
		ASIN          string     `json:"asin"`
		JAN           string     `json:"jan"`
		ModelNumber   string     `json:"modelNumber"`
		URL           string     `json:"url"`
		Note          string     `json:"note"`
		Confidence    float64    `json:"confidence"`
		LowConfidence bool       `json:"lowConfidence"`
		Extractor     string     `json:"extractor"`
		// JSON array of duplicate-check matches captured at import time.
		DupMatches string     `json:"dupMatches"`
		EntityID   *uuid.UUID `json:"entityId,omitempty" extensions:"x-nullable"`
		// Source message
		MessageID uuid.UUID `json:"messageId"`
		Channel   string    `json:"channel"   enums:"forward,eml,photo"`
		Subject   string    `json:"subject"`
	}

	// XPurchaseInfo is written onto an entity on accept/merge.
	XPurchaseInfo struct {
		Date        *time.Time
		From        string
		Price       float64
		ModelNumber string
		ASIN        string
		JAN         string
		// Overwrite replaces existing purchase fields (accept); otherwise only
		// empty fields are filled (merge).
		Overwrite bool
		// AddQuantity is added to the entity quantity (merge).
		AddQuantity float64
	}
)

// NewInboundToken returns 160 bits of randomness, base32 (lowercase, no padding).
func NewInboundToken() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)), nil
}

func mapAddress(a *ent.XInboundAddress) XInboundAddress {
	return XInboundAddress{
		ID: a.ID, GroupID: a.GroupID, Token: a.Token,
		AllowedSenders: SplitSenders(a.AllowedSenders), LastReceivedAt: a.LastReceivedAt,
	}
}

// SplitSenders parses a comma/newline separated allow-list.
func SplitSenders(s string) []string {
	out := []string{}
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '\n' || r == ';' || r == ' ' }) {
		if f = strings.ToLower(strings.TrimSpace(f)); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// GetOrCreateAddress returns the group's inbound address, creating it on first use.
func (r *XOrderImportRepository) GetOrCreateAddress(ctx context.Context, gid uuid.UUID) (XInboundAddress, error) {
	a, err := r.db.XInboundAddress.Query().Where(xinboundaddress.GroupID(gid)).Only(ctx)
	if err == nil {
		return mapAddress(a), nil
	}
	if !ent.IsNotFound(err) {
		return XInboundAddress{}, err
	}
	tok, err := NewInboundToken()
	if err != nil {
		return XInboundAddress{}, err
	}
	a, err = r.db.XInboundAddress.Create().SetGroupID(gid).SetToken(tok).Save(ctx)
	if ent.IsConstraintError(err) { // concurrent create
		a, err = r.db.XInboundAddress.Query().Where(xinboundaddress.GroupID(gid)).Only(ctx)
	}
	if err != nil {
		return XInboundAddress{}, err
	}
	return mapAddress(a), nil
}

// RotateToken replaces the group's token; the old one stops working at once.
func (r *XOrderImportRepository) RotateToken(ctx context.Context, gid uuid.UUID) (XInboundAddress, error) {
	if _, err := r.GetOrCreateAddress(ctx, gid); err != nil {
		return XInboundAddress{}, err
	}
	tok, err := NewInboundToken()
	if err != nil {
		return XInboundAddress{}, err
	}
	if _, err := r.db.XInboundAddress.Update().Where(xinboundaddress.GroupID(gid)).SetToken(tok).Save(ctx); err != nil {
		return XInboundAddress{}, err
	}
	return r.GetOrCreateAddress(ctx, gid)
}

// SetAllowedSenders replaces the sender allow-list.
func (r *XOrderImportRepository) SetAllowedSenders(ctx context.Context, gid uuid.UUID, senders []string) (XInboundAddress, error) {
	if _, err := r.GetOrCreateAddress(ctx, gid); err != nil {
		return XInboundAddress{}, err
	}
	clean := SplitSenders(strings.Join(senders, ","))
	if _, err := r.db.XInboundAddress.Update().Where(xinboundaddress.GroupID(gid)).
		SetAllowedSenders(strings.Join(clean, "\n")).Save(ctx); err != nil {
		return XInboundAddress{}, err
	}
	return r.GetOrCreateAddress(ctx, gid)
}

// AddressByToken resolves a webhook token. Returns an ent NotFound error for
// unknown or rotated tokens.
func (r *XOrderImportRepository) AddressByToken(ctx context.Context, token string) (XInboundAddress, error) {
	a, err := r.db.XInboundAddress.Query().Where(xinboundaddress.Token(token)).Only(ctx)
	if err != nil {
		return XInboundAddress{}, err
	}
	return mapAddress(a), nil
}

func (r *XOrderImportRepository) TouchAddress(ctx context.Context, id uuid.UUID) error {
	return r.db.XInboundAddress.UpdateOneID(id).SetLastReceivedAt(time.Now()).Exec(ctx)
}

// FindMessageByHash returns the message with identical raw bytes, if any.
func (r *XOrderImportRepository) FindMessageByHash(ctx context.Context, gid uuid.UUID, hash string) (*ent.XInboundMessage, error) {
	m, err := r.db.XInboundMessage.Query().
		Where(xinboundmessage.GroupID(gid), xinboundmessage.ContentHash(hash)).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, nil
	}
	return m, err
}

// MessageIDSeen reports whether a non-duplicate message with this Message-ID exists.
func (r *XOrderImportRepository) MessageIDSeen(ctx context.Context, gid uuid.UUID, messageID string) (bool, error) {
	if messageID == "" {
		return false, nil
	}
	return r.db.XInboundMessage.Query().Where(
		xinboundmessage.GroupID(gid), xinboundmessage.MessageID(messageID),
		xinboundmessage.StatusNEQ(xinboundmessage.StatusDuplicate),
	).Exist(ctx)
}

// OrderSeen reports whether this store order was already imported.
func (r *XOrderImportRepository) OrderSeen(ctx context.Context, gid uuid.UUID, store, orderNumber string) (bool, error) {
	if store == "" || orderNumber == "" {
		return false, nil
	}
	return r.db.XInboundMessage.Query().Where(
		xinboundmessage.GroupID(gid), xinboundmessage.Store(store), xinboundmessage.OrderNumber(orderNumber),
		xinboundmessage.StatusEQ(xinboundmessage.StatusParsed),
	).Exist(ctx)
}

// SimilarOrderSeen is the photo dedup rule: same store, same calendar day and
// same total as an already imported order.
func (r *XOrderImportRepository) SimilarOrderSeen(ctx context.Context, gid uuid.UUID, store string, date *time.Time, total float64) (bool, error) {
	if store == "" || date == nil || total <= 0 {
		return false, nil
	}
	start := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, date.Location())
	return r.db.XInboundMessage.Query().Where(
		xinboundmessage.GroupID(gid), xinboundmessage.Store(store),
		xinboundmessage.StatusEQ(xinboundmessage.StatusParsed),
		xinboundmessage.OrderDateGTE(start), xinboundmessage.OrderDateLT(start.AddDate(0, 0, 1)),
		xinboundmessage.OrderTotalGTE(total-0.5), xinboundmessage.OrderTotalLTE(total+0.5),
	).Exist(ctx)
}

// CreateMessage stores a raw message and its candidates atomically.
func (r *XOrderImportRepository) CreateMessage(ctx context.Context, gid uuid.UUID, m XMessageCreate, cands []XCandidateCreate) (uuid.UUID, error) {
	tx, err := r.db.Tx(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	msg, err := tx.XInboundMessage.Create().
		SetGroupID(gid).
		SetChannel(xinboundmessage.Channel(m.Channel)).
		SetStatus(xinboundmessage.Status(m.Status)).
		SetMessageID(truncate(m.MessageID, 998)).
		SetContentHash(m.ContentHash).
		SetStore(truncate(m.Store, 32)).
		SetOrderNumber(truncate(m.OrderNumber, 128)).
		SetNillableOrderDate(m.OrderDate).
		SetOrderTotal(m.OrderTotal).
		SetSender(truncate(m.Sender, 320)).
		SetSubject(truncate(m.Subject, 998)).
		SetMimeType(truncate(m.MimeType, 128)).
		SetExtractor(truncate(m.Extractor, 64)).
		SetError(truncate(m.Error, 1000)).
		SetRaw(m.Raw).
		Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return uuid.Nil, err
	}
	for _, c := range cands {
		currency := c.Currency
		if currency == "" {
			currency = "JPY"
		}
		dup := c.DupMatches
		if dup == "" {
			dup = "[]"
		}
		kind := c.Kind
		if kind == "" {
			kind = "item"
		}
		_, err := tx.XImportCandidate.Create().
			SetGroupID(gid).
			SetInboundMessageID(msg.ID).
			SetKind(ximportcandidate.Kind(kind)).
			SetName(truncate(c.Name, 500)).
			SetQuantity(c.Quantity).
			SetUnitPrice(c.UnitPrice).
			SetCurrency(truncate(currency, 8)).
			SetStore(truncate(c.Store, 32)).
			SetOrderNumber(truncate(c.OrderNumber, 128)).
			SetNillableOrderDate(c.OrderDate).
			SetAsin(truncate(c.ASIN, 64)).
			SetJan(truncate(c.JAN, 64)).
			SetModelNumber(truncate(c.ModelNumber, 255)).
			SetURL(truncate(c.URL, 2048)).
			SetNote(c.Note).
			SetConfidence(c.Confidence).
			SetLowConfidence(c.LowConfidence).
			SetExtractor(truncate(c.Extractor, 64)).
			SetDupMatches(dup).
			Save(ctx)
		if err != nil {
			_ = tx.Rollback()
			return uuid.Nil, err
		}
	}
	return msg.ID, tx.Commit()
}

func mapCandidate(c *ent.XImportCandidate) XImportCandidate {
	out := XImportCandidate{
		ID: c.ID, CreatedAt: c.CreatedAt, Kind: c.Kind.String(), Status: c.Status.String(),
		Name: c.Name, Quantity: c.Quantity, UnitPrice: c.UnitPrice, Currency: c.Currency,
		Store: c.Store, OrderNumber: c.OrderNumber, OrderDate: c.OrderDate,
		ASIN: c.Asin, JAN: c.Jan, ModelNumber: c.ModelNumber, URL: c.URL, Note: c.Note,
		Confidence: c.Confidence, LowConfidence: c.LowConfidence, Extractor: c.Extractor,
		DupMatches: c.DupMatches, EntityID: c.EntityID, MessageID: c.InboundMessageID,
	}
	if m := c.Edges.Message; m != nil {
		out.Channel = m.Channel.String()
		out.Subject = m.Subject
	}
	return out
}

// ListCandidates returns review-inbox entries, newest first. Empty status means all.
func (r *XOrderImportRepository) ListCandidates(ctx context.Context, gid uuid.UUID, status string, limit int) ([]XImportCandidate, error) {
	q := r.db.XImportCandidate.Query().Where(ximportcandidate.GroupID(gid))
	if status != "" {
		q = q.Where(ximportcandidate.StatusEQ(ximportcandidate.Status(status)))
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := q.WithMessage(func(mq *ent.XInboundMessageQuery) {
		mq.Select(xinboundmessage.FieldID, xinboundmessage.FieldChannel, xinboundmessage.FieldSubject)
	}).Order(ent.Desc(ximportcandidate.FieldCreatedAt)).Limit(limit).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]XImportCandidate, len(rows))
	for i, c := range rows {
		out[i] = mapCandidate(c)
	}
	return out, nil
}

// GetCandidate loads one candidate scoped to the group.
func (r *XOrderImportRepository) GetCandidate(ctx context.Context, gid, id uuid.UUID) (XImportCandidate, error) {
	c, err := r.db.XImportCandidate.Query().
		Where(ximportcandidate.ID(id), ximportcandidate.GroupID(gid)).
		WithMessage(func(mq *ent.XInboundMessageQuery) {
			mq.Select(xinboundmessage.FieldID, xinboundmessage.FieldChannel, xinboundmessage.FieldSubject)
		}).Only(ctx)
	if err != nil {
		return XImportCandidate{}, err
	}
	return mapCandidate(c), nil
}

// ClaimCandidate atomically moves a pending candidate to status. Only one
// concurrent caller can win; others get ErrCandidateNotPending.
func (r *XOrderImportRepository) ClaimCandidate(ctx context.Context, gid, id uuid.UUID, status string, kinds ...string) error {
	q := r.db.XImportCandidate.Update().Where(
		ximportcandidate.ID(id), ximportcandidate.GroupID(gid),
		ximportcandidate.StatusEQ(ximportcandidate.StatusPending),
	)
	if len(kinds) > 0 {
		ks := make([]ximportcandidate.Kind, len(kinds))
		for i, k := range kinds {
			ks[i] = ximportcandidate.Kind(k)
		}
		q = q.Where(ximportcandidate.KindIn(ks...))
	}
	n, err := q.SetStatus(ximportcandidate.Status(status)).Save(ctx)
	if err != nil {
		return err
	}
	if n == 0 {
		exists, err := r.db.XImportCandidate.Query().Where(ximportcandidate.ID(id), ximportcandidate.GroupID(gid)).Exist(ctx)
		if err != nil {
			return err
		}
		if !exists {
			return &ent.NotFoundError{}
		}
		return ErrCandidateNotPending
	}
	return nil
}

// ReleaseCandidate puts a claimed candidate back to pending (error path).
func (r *XOrderImportRepository) ReleaseCandidate(ctx context.Context, gid, id uuid.UUID) error {
	return r.db.XImportCandidate.Update().
		Where(ximportcandidate.ID(id), ximportcandidate.GroupID(gid)).
		SetStatus(ximportcandidate.StatusPending).ClearEntityID().Exec(ctx)
}

func (r *XOrderImportRepository) SetCandidateEntity(ctx context.Context, gid, id, entityID uuid.UUID) error {
	return r.db.XImportCandidate.Update().
		Where(ximportcandidate.ID(id), ximportcandidate.GroupID(gid)).
		SetEntityID(entityID).Exec(ctx)
}

// MessageRaw returns a message's raw bytes and MIME type.
func (r *XOrderImportRepository) MessageRaw(ctx context.Context, gid, id uuid.UUID) ([]byte, string, error) {
	m, err := r.db.XInboundMessage.Query().
		Where(xinboundmessage.ID(id), xinboundmessage.GroupID(gid)).
		Select(xinboundmessage.FieldRaw, xinboundmessage.FieldMimeType).Only(ctx)
	if err != nil {
		return nil, "", err
	}
	return m.Raw, m.MimeType, nil
}

// EntityInGroup reports whether entityID exists in the group.
func (r *XOrderImportRepository) EntityInGroup(ctx context.Context, gid, entityID uuid.UUID) (bool, error) {
	return r.db.Entity.Query().Where(entity.ID(entityID), entity.HasGroupWith(group.ID(gid))).Exist(ctx)
}

// Custom-field names written for identifiers. DupCheckCandidates reads them back.
const (
	XFieldASIN = "ASIN"
	XFieldJAN  = "JAN"
)

// ApplyPurchase writes purchase data and identifiers onto an entity.
func (r *XOrderImportRepository) ApplyPurchase(ctx context.Context, gid, entityID uuid.UUID, p XPurchaseInfo) error {
	tx, err := r.db.Tx(ctx)
	if err != nil {
		return err
	}
	rollback := func(err error) error { _ = tx.Rollback(); return err }

	e, err := tx.Entity.Query().Where(entity.ID(entityID), entity.HasGroupWith(group.ID(gid))).WithFields().Only(ctx)
	if err != nil {
		return rollback(err)
	}
	up := tx.Entity.UpdateOneID(entityID)
	if p.Date != nil && (p.Overwrite || e.PurchaseDate.IsZero()) {
		// Purchase dates are calendar dates; store UTC midnight of the order's
		// local date, matching how upstream round-trips types.Date.
		d := *p.Date
		up = up.SetPurchaseDate(time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC))
	}
	if p.From != "" && (p.Overwrite || e.PurchaseFrom == "") {
		up = up.SetPurchaseFrom(truncate(p.From, 255))
	}
	if p.Price > 0 && (p.Overwrite || e.PurchasePrice == 0) {
		up = up.SetPurchasePrice(p.Price)
	}
	if p.ModelNumber != "" && (p.Overwrite || e.ModelNumber == "") {
		up = up.SetModelNumber(truncate(p.ModelNumber, 255))
	}
	if p.AddQuantity > 0 {
		up = up.AddQuantity(p.AddQuantity)
	}
	if err := up.Exec(ctx); err != nil {
		return rollback(err)
	}

	addField := func(name, value string) error {
		if value == "" {
			return nil
		}
		for _, f := range e.Edges.Fields {
			if strings.EqualFold(strings.TrimSpace(f.Name), name) {
				if strings.EqualFold(strings.TrimSpace(f.TextValue), value) || !p.Overwrite {
					return nil
				}
				return tx.EntityField.UpdateOneID(f.ID).SetTextValue(value).Exec(ctx)
			}
		}
		return tx.EntityField.Create().SetEntityID(entityID).SetName(name).
			SetType(entityfield.TypeText).SetTextValue(value).Exec(ctx)
	}
	if err := addField(XFieldASIN, p.ASIN); err != nil {
		return rollback(err)
	}
	if err := addField(XFieldJAN, p.JAN); err != nil {
		return rollback(err)
	}
	return tx.Commit()
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
