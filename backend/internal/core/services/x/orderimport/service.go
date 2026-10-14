// Package orderimport turns forwarded order emails, uploaded .eml files and
// receipt photos into review-inbox candidates. Nothing becomes an entity
// until the user accepts or merges it.
package orderimport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/sysadminsmedia/homebox/backend/internal/core/dupcheck"
	"github.com/sysadminsmedia/homebox/backend/internal/core/services/x/orderimport/llm"
	"github.com/sysadminsmedia/homebox/backend/internal/core/services/x/orderimport/mailmsg"
	"github.com/sysadminsmedia/homebox/backend/internal/core/services/x/orderimport/parsers"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/attachment"
	"github.com/sysadminsmedia/homebox/backend/internal/data/repo"
)

// Channels.
const (
	ChannelForward = "forward"
	ChannelEML     = "eml"
	ChannelPhoto   = "photo"
)

// Result statuses.
const (
	StatusImported  = "imported"  // candidates created
	StatusUnparsed  = "unparsed"  // stored, surfaced as a notice
	StatusDuplicate = "duplicate" // already imported; nothing new in the inbox
)

// Confidence by extractor.
const (
	confParser = 0.95
	confLLM    = 0.6
	confPhoto  = 0.4
)

// MaxMessageBytes is the hard cap for one email or photo.
const MaxMessageBytes = 10 << 20

var (
	// ErrInvalidEmail means the bytes are not an RFC 5322 message.
	ErrInvalidEmail = errors.New("not a valid email (.eml) file")
	// ErrVisionDisabled means photo import was requested without an LLM.
	ErrVisionDisabled = errors.New("photo import needs an LLM endpoint (HBOX_ORDER_IMPORT_LLM_*)")
	// ErrNotAnImage rejects non-image photo uploads.
	ErrNotAnImage = errors.New("photo must be a JPEG, PNG, WebP or GIF image")
)

// Service wires the pipeline. LLM may be nil (fallback disabled).
type Service struct {
	Repo        *repo.XOrderImportRepository
	Entities    *repo.EntityRepository
	Attachments *repo.AttachmentRepo
	LLM         llm.Extractor
}

// Result describes what happened to one ingested message.
type Result struct {
	MessageID   uuid.UUID `json:"messageId"`
	Status      string    `json:"status"      enums:"imported,unparsed,duplicate"`
	Store       string    `json:"store"`
	OrderNumber string    `json:"orderNumber"`
	Candidates  int       `json:"candidates"`
	Extractor   string    `json:"extractor"`
	Note        string    `json:"note"`
}

// DupMatch is the snapshot stored with each candidate.
type DupMatch struct {
	ID           uuid.UUID `json:"id"`
	Name         string    `json:"name"`
	Confidence   string    `json:"confidence"`
	Score        float64   `json:"score"`
	MatchedOn    string    `json:"matchedOn"`
	LocationPath string    `json:"locationPath"`
}

func hashOf(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// SenderAllowed checks the (outer) sender against an allow-list of addresses
// or "@domain" entries. An empty list allows everyone.
func SenderAllowed(allow []string, from string) bool {
	if len(allow) == 0 {
		return true
	}
	from = strings.ToLower(strings.TrimSpace(from))
	at := strings.LastIndexByte(from, '@')
	for _, a := range allow {
		a = strings.ToLower(strings.TrimSpace(a))
		switch {
		case a == from:
			return true
		case strings.HasPrefix(a, "@") && at >= 0 && from[at:] == a:
			return true
		}
	}
	return false
}

// SenderOf returns the outer From address of raw MIME (for allow-list checks).
func SenderOf(raw []byte) (string, error) {
	m, err := mailmsg.Parse(raw)
	if err != nil {
		return "", ErrInvalidEmail
	}
	return m.From, nil
}

// IngestEmail handles both the forwarding webhook and .eml uploads.
func (s *Service) IngestEmail(ctx context.Context, gid uuid.UUID, channel string, raw []byte) (Result, error) {
	if len(raw) > MaxMessageBytes {
		return Result{}, fmt.Errorf("message larger than %d bytes", MaxMessageBytes)
	}
	hash := hashOf(raw)
	if prev, err := s.Repo.FindMessageByHash(ctx, gid, hash); err != nil {
		return Result{}, err
	} else if prev != nil {
		return Result{MessageID: prev.ID, Status: StatusDuplicate, Store: prev.Store, OrderNumber: prev.OrderNumber, Note: "identical message already imported"}, nil
	}

	m, err := mailmsg.Parse(raw)
	if err != nil {
		return Result{}, ErrInvalidEmail
	}

	msg := repo.XMessageCreate{
		Channel: channel, ContentHash: hash, MessageID: m.MessageID,
		Sender: m.From, Subject: m.Subject, MimeType: "message/rfc822", Raw: raw,
	}

	if seen, err := s.Repo.MessageIDSeen(ctx, gid, m.MessageID); err != nil {
		return Result{}, err
	} else if seen {
		return s.storeDuplicate(ctx, gid, msg, "Message-ID already imported")
	}

	var order *parsers.Order
	var parseErr error
	if p := parsers.Detect(m); p != nil {
		order, parseErr = p.Parse(m)
		msg.Extractor = "parser:" + p.Store()
		if parseErr != nil {
			order = nil
		}
	}
	confidence := confParser
	if order == nil && s.LLM != nil && !parsers.IsForwardingConfirmation(m) {
		o, err := s.LLM.ExtractText(ctx, m.Subject, m.Text)
		if err != nil {
			log.Warn().Err(err).Msg("order import: llm fallback failed")
		} else if len(o.Items) > 0 {
			if o.Store == "" {
				o.Store = parsers.DetectStore(m)
			}
			order, confidence, msg.Extractor = o, confLLM, "llm"
		}
	}

	if order == nil {
		msg.Status = "unparsed"
		msg.Store = parsers.DetectStore(m)
		if parseErr != nil {
			msg.Error = parseErr.Error()
		}
		note := "Could not read an order from this email. Open the original and add items by hand, or ignore it."
		if parsers.IsForwardingConfirmation(m) {
			note = "Your mail provider sent a forwarding confirmation. Open it to confirm the forwarding address:\n\n" + truncateRunes(m.Text, 2000)
		}
		id, err := s.Repo.CreateMessage(ctx, gid, msg, []repo.XCandidateCreate{{
			Kind: "notice", Name: firstNonEmpty(m.Subject, "(no subject)"), Note: note,
			Store: msg.Store, Extractor: msg.Extractor, Quantity: 1,
		}})
		if err != nil {
			return Result{}, err
		}
		return Result{MessageID: id, Status: StatusUnparsed, Store: msg.Store, Candidates: 1, Note: note}, nil
	}

	msg.Store, msg.OrderNumber, msg.OrderDate, msg.OrderTotal = order.Store, order.OrderNumber, order.OrderDate, order.Total
	if seen, err := s.Repo.OrderSeen(ctx, gid, order.Store, order.OrderNumber); err != nil {
		return Result{}, err
	} else if seen {
		return s.storeDuplicate(ctx, gid, msg, "order "+order.OrderNumber+" already imported")
	}
	return s.storeOrder(ctx, gid, msg, order, confidence, false)
}

// IngestPhoto handles receipt / order-screenshot images via the vision LLM.
func (s *Service) IngestPhoto(ctx context.Context, gid uuid.UUID, mimeType string, data []byte) (Result, error) {
	switch mimeType {
	case "image/jpeg", "image/png", "image/webp", "image/gif":
	default:
		return Result{}, ErrNotAnImage
	}
	if len(data) > MaxMessageBytes {
		return Result{}, fmt.Errorf("photo larger than %d bytes", MaxMessageBytes)
	}
	hash := hashOf(data)
	if prev, err := s.Repo.FindMessageByHash(ctx, gid, hash); err != nil {
		return Result{}, err
	} else if prev != nil {
		return Result{MessageID: prev.ID, Status: StatusDuplicate, Note: "identical photo already imported"}, nil
	}
	if s.LLM == nil {
		return Result{}, ErrVisionDisabled
	}
	order, err := s.LLM.ExtractImage(ctx, mimeType, data)
	if err != nil {
		return Result{}, fmt.Errorf("vision extraction failed: %w", err)
	}
	msg := repo.XMessageCreate{
		Channel: ChannelPhoto, ContentHash: hash, MimeType: mimeType, Raw: data, Extractor: "llm-vision",
		Store: order.Store, OrderNumber: order.OrderNumber, OrderDate: order.OrderDate, OrderTotal: order.Total,
	}
	if seen, err := s.Repo.OrderSeen(ctx, gid, order.Store, order.OrderNumber); err != nil {
		return Result{}, err
	} else if seen {
		return s.storeDuplicate(ctx, gid, msg, "order "+order.OrderNumber+" already imported")
	}
	if seen, err := s.Repo.SimilarOrderSeen(ctx, gid, order.Store, order.OrderDate, order.Total); err != nil {
		return Result{}, err
	} else if seen {
		return s.storeDuplicate(ctx, gid, msg, "likely duplicate: same store, date and total as an imported order")
	}
	if len(order.Items) == 0 {
		msg.Status = "unparsed"
		id, err := s.Repo.CreateMessage(ctx, gid, msg, []repo.XCandidateCreate{{
			Kind: "notice", Name: "Receipt photo", Note: "No purchased items could be read from this photo.",
			Quantity: 1, LowConfidence: true, Extractor: msg.Extractor,
		}})
		if err != nil {
			return Result{}, err
		}
		return Result{MessageID: id, Status: StatusUnparsed, Candidates: 1}, nil
	}
	return s.storeOrder(ctx, gid, msg, order, confPhoto, true)
}

func (s *Service) storeDuplicate(ctx context.Context, gid uuid.UUID, msg repo.XMessageCreate, why string) (Result, error) {
	msg.Status = "duplicate"
	msg.Error = why
	msg.Raw = nil // keep the log row, not a second copy of the bytes
	id, err := s.Repo.CreateMessage(ctx, gid, msg, nil)
	if err != nil {
		return Result{}, err
	}
	return Result{MessageID: id, Status: StatusDuplicate, Store: msg.Store, OrderNumber: msg.OrderNumber, Note: why}, nil
}

func (s *Service) storeOrder(ctx context.Context, gid uuid.UUID, msg repo.XMessageCreate, o *parsers.Order, conf float64, low bool) (Result, error) {
	msg.Status = "parsed"
	pool, err := s.Entities.DupCheckCandidates(ctx, gid)
	if err != nil {
		return Result{}, err
	}
	cands := make([]repo.XCandidateCreate, 0, len(o.Items))
	for _, it := range o.Items {
		cands = append(cands, repo.XCandidateCreate{
			Kind: "item", Name: it.Name, Quantity: it.Quantity, UnitPrice: it.UnitPrice,
			Currency: o.Currency, Store: o.Store, OrderNumber: o.OrderNumber, OrderDate: o.OrderDate,
			ASIN: it.ASIN, JAN: it.JAN, ModelNumber: it.ModelNumber, URL: it.URL,
			Confidence: conf, LowConfidence: low, Extractor: msg.Extractor,
			DupMatches: s.dupSnapshot(ctx, gid, pool, it),
		})
	}
	id, err := s.Repo.CreateMessage(ctx, gid, msg, cands)
	if err != nil {
		return Result{}, err
	}
	return Result{MessageID: id, Status: StatusImported, Store: o.Store, OrderNumber: o.OrderNumber, Candidates: len(cands), Extractor: msg.Extractor}, nil
}

func (s *Service) dupSnapshot(ctx context.Context, gid uuid.UUID, pool []dupcheck.Candidate, it parsers.LineItem) string {
	ms, err := dupcheck.Rank(ctx, dupcheck.Query{Name: it.Name, ASIN: it.ASIN, JAN: it.JAN, ModelNumber: it.ModelNumber}, pool, dupcheck.Options{Limit: 3})
	if err != nil || len(ms) == 0 {
		return "[]"
	}
	out := make([]DupMatch, 0, len(ms))
	for _, m := range ms {
		d := DupMatch{ID: m.Candidate.ID, Name: m.Candidate.Name, Confidence: string(m.Tier), Score: m.Score, MatchedOn: string(m.Reason)}
		if p, err := s.Entities.PathForEntity(ctx, gid, m.Candidate.ID); err == nil {
			names := make([]string, 0, len(p))
			for _, e := range p {
				if e.ID != m.Candidate.ID {
					names = append(names, e.Name)
				}
			}
			d.LocationPath = strings.Join(names, " > ")
		}
		out = append(out, d)
	}
	b, _ := json.Marshal(out)
	return string(b)
}

// AcceptInput lets the reviewer edit before accepting. Zero values keep the
// extracted data.
type AcceptInput struct {
	Name         string    `json:"name"         validate:"max=255"`
	Quantity     float64   `json:"quantity"     validate:"min=0"`
	UnitPrice    float64   `json:"unitPrice"    validate:"min=0"`
	ParentID     uuid.UUID `json:"parentId"     extensions:"x-nullable"`
	EntityTypeID uuid.UUID `json:"entityTypeId" extensions:"x-nullable"`
}

// StoreLabel is what lands in the entity's "purchased from" field.
func StoreLabel(store string) string {
	switch store {
	case parsers.StoreAmazonJP:
		return "Amazon.co.jp"
	case parsers.StoreRakuten:
		return "楽天市場"
	case parsers.StoreYodobashi:
		return "ヨドバシ.com"
	}
	return store
}

// Accept creates an entity from a pending item candidate.
func (s *Service) Accept(ctx context.Context, gid, id uuid.UUID, in AcceptInput) (repo.EntityOut, error) {
	c, err := s.Repo.GetCandidate(ctx, gid, id)
	if err != nil {
		return repo.EntityOut{}, err
	}
	if err := s.Repo.ClaimCandidate(ctx, gid, id, "accepted", "item"); err != nil {
		return repo.EntityOut{}, err
	}
	release := func(err error) (repo.EntityOut, error) {
		_ = s.Repo.ReleaseCandidate(ctx, gid, id)
		return repo.EntityOut{}, err
	}

	name := firstNonEmpty(strings.TrimSpace(in.Name), c.Name)
	qty := c.Quantity
	if in.Quantity > 0 {
		qty = in.Quantity
	}
	price := c.UnitPrice
	if in.UnitPrice > 0 {
		price = in.UnitPrice
	}
	desc := "Imported from " + StoreLabel(c.Store)
	if c.OrderNumber != "" {
		desc += " order " + c.OrderNumber
	}
	created, err := s.Entities.Create(ctx, gid, repo.EntityCreate{
		Name: truncateRunes(name, 255), Quantity: qty, Description: truncateRunes(desc, 1000),
		ParentID: in.ParentID, EntityTypeID: in.EntityTypeID, ModelNumber: truncateRunes(c.ModelNumber, 255),
	})
	if err != nil {
		return release(err)
	}
	if err := s.Repo.ApplyPurchase(ctx, gid, created.ID, repo.XPurchaseInfo{
		Date: c.OrderDate, From: StoreLabel(c.Store), Price: price,
		ASIN: c.ASIN, JAN: c.JAN, Overwrite: true,
	}); err != nil {
		_ = s.Entities.DeleteByGroup(ctx, gid, created.ID)
		return release(err)
	}
	if c.Channel == ChannelPhoto && s.Attachments != nil {
		s.attachPhoto(ctx, gid, c.MessageID, created.ID)
	}
	if err := s.Repo.SetCandidateEntity(ctx, gid, id, created.ID); err != nil {
		return repo.EntityOut{}, err
	}
	return s.Entities.GetOneByGroup(ctx, gid, created.ID)
}

func (s *Service) attachPhoto(ctx context.Context, gid, messageID, entityID uuid.UUID) {
	raw, mimeType, err := s.Repo.MessageRaw(ctx, gid, messageID)
	if err != nil || len(raw) == 0 {
		return
	}
	ext := map[string]string{"image/jpeg": ".jpg", "image/png": ".png", "image/webp": ".webp", "image/gif": ".gif"}[mimeType]
	title := path.Clean("receipt" + ext)
	if _, err := s.Attachments.Create(ctx, entityID, repo.ItemCreateAttachment{Title: title, Content: bytes.NewReader(raw)}, attachment.TypeReceipt, false); err != nil {
		log.Warn().Err(err).Msg("order import: could not attach receipt photo")
	}
}

// Merge folds a pending item candidate into an existing entity: quantity is
// added, identifiers and empty purchase fields are filled in.
func (s *Service) Merge(ctx context.Context, gid, id, entityID uuid.UUID) (repo.EntityOut, error) {
	ok, err := s.Repo.EntityInGroup(ctx, gid, entityID)
	if err != nil {
		return repo.EntityOut{}, err
	}
	if !ok {
		return repo.EntityOut{}, ErrEntityNotFound
	}
	c, err := s.Repo.GetCandidate(ctx, gid, id)
	if err != nil {
		return repo.EntityOut{}, err
	}
	if err := s.Repo.ClaimCandidate(ctx, gid, id, "merged", "item"); err != nil {
		return repo.EntityOut{}, err
	}
	if err := s.Repo.ApplyPurchase(ctx, gid, entityID, repo.XPurchaseInfo{
		Date: c.OrderDate, From: StoreLabel(c.Store), Price: c.UnitPrice, ModelNumber: c.ModelNumber,
		ASIN: c.ASIN, JAN: c.JAN, AddQuantity: c.Quantity,
	}); err != nil {
		_ = s.Repo.ReleaseCandidate(ctx, gid, id)
		return repo.EntityOut{}, err
	}
	if err := s.Repo.SetCandidateEntity(ctx, gid, id, entityID); err != nil {
		return repo.EntityOut{}, err
	}
	return s.Entities.GetOneByGroup(ctx, gid, entityID)
}

// ErrEntityNotFound is returned when merging into an entity outside the group.
var ErrEntityNotFound = errors.New("entity not found")

// Ignore dismisses a pending candidate (items and notices).
func (s *Service) Ignore(ctx context.Context, gid, id uuid.UUID) error {
	return s.Repo.ClaimCandidate(ctx, gid, id, "ignored")
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
