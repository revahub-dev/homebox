// Package capture is the photo-first intake pipeline: photos are queued in
// the database, analysed by a vision provider, cropped and checked against
// the inventory, then shown to the user as a confirm grid. Nothing becomes an
// inventory entity until the user accepts it.
package capture

// Fork addition (photo-first capture). See FORK_NOTES.md.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/sysadminsmedia/homebox/backend/internal/core/services/x/vision"
	"github.com/sysadminsmedia/homebox/backend/internal/data/repo"
)

// Errors mapped to HTTP statuses by the handlers.
var (
	ErrDisabled     = vision.ErrDisabled
	ErrTooLarge     = errors.New("photo is too large")
	ErrNotImage     = errors.New("file is not a supported image (JPEG, PNG, WebP, HEIC)")
	ErrDailyCap     = errors.New("daily photo limit reached (HBOX_CAPTURE_MAX_PHOTOS_PER_DAY)")
	ErrBadLocation  = errors.New("location not found")
	ErrBadTarget    = errors.New("target item not found")
	ErrBadMode      = errors.New("mode must be item or shelf")
	ErrNotUndoable  = errors.New("this item can no longer be undone")
	ErrBadItemInput = errors.New("invalid item")
)

const statePending = "pending"

// Tuning constants.
const (
	// UndoWindow is how long after a decision it can still be reversed.
	UndoWindow = 30 * time.Minute
	// MaxAttempts is how many times a photo is analysed before it fails.
	MaxAttempts = 3
	// ReadyConfidence is the threshold for the "ready" bucket.
	ReadyConfidence = 0.80
	// ReviewConfidence is the threshold for the "review" bucket.
	ReviewConfidence = 0.50
	// UnsortedName is the fallback location for items with no location.
	UnsortedName = "Unsorted"
)

// Config tunes the service.
type Config struct {
	// Workers is how many photos are analysed at once. 0 disables the
	// automatic workers (tests call Drain themselves).
	Workers         int
	MaxPhotosPerDay int // 0 = unlimited
	MaxPhotoBytes   int // 0 = 8 MiB
	RetentionDays   int // 0 = 7
	// StaleAfter is when a running photo is considered abandoned (0 = 5 min).
	StaleAfter time.Duration
	// RetryDelay is the pause before a failed photo is retried (0 = 1s).
	RetryDelay time.Duration
	// Language is a name-language hint passed to the model ("ja", "en").
	Language string
	// Model and BaseURL are shown in the status endpoint (never the key).
	Model   string
	BaseURL string
}

// Service owns the capture pipeline.
type Service struct {
	Repo        *repo.XCaptureRepository
	Entities    *repo.EntityRepository
	EntityTypes *repo.EntityTypeRepository
	Tags        *repo.TagRepository
	Attachments *repo.AttachmentRepo
	// Provider is nil when no AI endpoint is configured.
	Provider vision.Provider
	Cfg      Config
	// Now is replaceable in tests.
	Now func() time.Time

	mu     sync.Mutex
	base   context.Context
	active int32
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) maxPhotoBytes() int {
	if s.Cfg.MaxPhotoBytes > 0 {
		return s.Cfg.MaxPhotoBytes
	}
	return 8 << 20
}

func (s *Service) staleAfter() time.Duration {
	if s.Cfg.StaleAfter > 0 {
		return s.Cfg.StaleAfter
	}
	return 5 * time.Minute
}

func (s *Service) retention() time.Duration {
	d := s.Cfg.RetentionDays
	if d <= 0 {
		d = 7
	}
	return time.Duration(d) * 24 * time.Hour
}

// Enabled reports whether a vision provider is configured.
func (s *Service) Enabled() bool { return s.Provider != nil }

// ---------------------------------------------------------------- outputs

// Status is the capture capability summary shown on the snap screen.
type Status struct {
	Enabled         bool   `json:"enabled"`
	Provider        string `json:"provider"`
	Model           string `json:"model"`
	Local           bool   `json:"local"`
	QueueDepth      int    `json:"queueDepth"`
	MaxPhotosPerDay int    `json:"maxPhotosPerDay"`
	MaxPhotoMB      int    `json:"maxPhotoMb"`
}

// Status describes the configured provider and queue.
func (s *Service) Status(ctx context.Context) Status {
	st := Status{Enabled: s.Enabled(), Model: s.Cfg.Model, MaxPhotosPerDay: s.Cfg.MaxPhotosPerDay, MaxPhotoMB: s.maxPhotoBytes() >> 20}
	if s.Provider != nil {
		st.Provider = s.Provider.Name()
		st.Local = IsLocalEndpoint(s.Cfg.BaseURL)
	}
	if d, err := s.Repo.QueueDepth(ctx); err == nil {
		st.QueueDepth = d
	}
	return st
}

// IsLocalEndpoint guesses whether an endpoint runs on the user's own
// network (loopback, private or link-local IP, single-label docker host,
// *.local, host.docker.internal) as opposed to a cloud API. It is only a UI
// hint ("your photos stay on your network").
func IsLocalEndpoint(base string) bool {
	u, err := url.Parse(strings.TrimSpace(base))
	if err != nil || u.Hostname() == "" {
		return false
	}
	h := strings.ToLower(u.Hostname())
	if h == "localhost" || h == "host.docker.internal" || strings.HasSuffix(h, ".local") || strings.HasSuffix(h, ".lan") || strings.HasSuffix(h, ".internal") {
		return true
	}
	if ip := net.ParseIP(h); ip != nil {
		return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
	}
	return !strings.Contains(h, ".") // docker-compose service name, e.g. "ollama"
}

// DupMatch is a possible duplicate in the inventory.
type DupMatch struct {
	ID           uuid.UUID `json:"id"`
	Name         string    `json:"name"`
	Confidence   string    `json:"confidence"   enums:"exact,likely,similar"`
	Score        float64   `json:"score"`
	MatchedOn    string    `json:"matchedOn"`
	LocationPath string    `json:"locationPath"`
}

// ItemOut is one detection in the confirm grid.
type ItemOut struct {
	ID               uuid.UUID    `json:"id"`
	PhotoID          uuid.UUID    `json:"photoId"`
	State            string       `json:"state"                      enums:"pending,accepted,merged,ignored"`
	Name             string       `json:"name"`
	Brand            string       `json:"brand"`
	ModelNumber      string       `json:"modelNumber"`
	Category         string       `json:"category"`
	Quantity         float64      `json:"quantity"`
	Confidence       float64      `json:"confidence"`
	Bucket           string       `json:"bucket"                     enums:"ready,review,maybe"`
	Ready            bool         `json:"ready"`
	BBox             *vision.BBox `json:"bbox,omitempty"             extensions:"x-nullable,x-omitempty"`
	HasCrop          bool         `json:"hasCrop"`
	DupMatches       []DupMatch   `json:"dupMatches"`
	TargetLocationID *uuid.UUID   `json:"targetLocationId,omitempty" extensions:"x-nullable,x-omitempty"`
	EntityID         *uuid.UUID   `json:"entityId,omitempty"         extensions:"x-nullable,x-omitempty"`
	CanUndo          bool         `json:"canUndo"`
}

// PhotoOut is the processing state of one uploaded photo.
type PhotoOut struct {
	ID         uuid.UUID `json:"id"`
	Status     string    `json:"status"     enums:"queued,running,done,failed"`
	Error      string    `json:"error"`
	Detections int       `json:"detections"`
	Attempts   int       `json:"attempts"`
}

// SessionOut is the polled snapshot of a capture session.
type SessionOut struct {
	ID         uuid.UUID  `json:"id"`
	Mode       string     `json:"mode"                 enums:"item,shelf"`
	LocationID *uuid.UUID `json:"locationId,omitempty" extensions:"x-nullable,x-omitempty"`
	Status     string     `json:"status"               enums:"open,done"`
	Photos     []PhotoOut `json:"photos"`
	Items      []ItemOut  `json:"items"`
	// Processing counts photos still queued or running.
	Processing int `json:"processing"`
	Failed     int `json:"failed"`
	// Pending counts items awaiting a decision; ReadyCount those eligible for bulk accept.
	Pending    int `json:"pending"`
	ReadyCount int `json:"readyCount"`
}

func bucketOf(conf float64, low bool) string {
	switch {
	case conf >= ReadyConfidence && !low:
		return "ready"
	case conf >= ReviewConfidence:
		return "review"
	}
	return "maybe"
}

func (s *Service) itemOut(i repo.XCaptureItem) ItemOut {
	out := ItemOut{
		ID: i.ID, PhotoID: i.PhotoID, State: i.State, Name: i.Name, Brand: i.Brand, ModelNumber: i.ModelNumber,
		Category: i.Category, Quantity: i.Quantity, Confidence: i.Confidence, HasCrop: i.HasCrop,
		TargetLocationID: i.TargetLocationID, EntityID: i.EntityID, DupMatches: []DupMatch{},
	}
	out.Bucket = bucketOf(i.Confidence, i.LowConfidence)
	if i.BBox != "" && i.BBox != "null" {
		var b vision.BBox
		if json.Unmarshal([]byte(i.BBox), &b) == nil && b.W > 0 && b.H > 0 {
			out.BBox = &b
		}
	}
	if i.DupMatches != "" {
		_ = json.Unmarshal([]byte(i.DupMatches), &out.DupMatches)
		if out.DupMatches == nil {
			out.DupMatches = []DupMatch{}
		}
	}
	out.Ready = i.State == statePending && out.Bucket == "ready" && !hasStrongDup(out.DupMatches)
	if i.State != statePending && i.HandledAt != nil && s.now().Sub(*i.HandledAt) <= UndoWindow {
		out.CanUndo = true
	}
	return out
}

// hasStrongDup is true when an identifier or identical name matched, i.e.
// accepting would very likely create a duplicate.
func hasStrongDup(d []DupMatch) bool {
	for _, m := range d {
		if m.Confidence == "exact" || m.Confidence == "likely" {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------- sessions

// StartSession opens a capture session. location may be nil (items without a
// location go to "Unsorted").
func (s *Service) StartSession(ctx context.Context, gid, uid uuid.UUID, mode string, location *uuid.UUID) (SessionOut, error) {
	if !vision.Mode(mode).Valid() {
		return SessionOut{}, ErrBadMode
	}
	if location != nil {
		ok, err := s.Repo.IsLocation(ctx, gid, *location)
		if err != nil {
			return SessionOut{}, err
		}
		if !ok {
			return SessionOut{}, ErrBadLocation
		}
	}
	sess, err := s.Repo.CreateSession(ctx, gid, uid, mode, location)
	if err != nil {
		return SessionOut{}, err
	}
	return SessionOut{ID: sess.ID, Mode: sess.Mode, LocationID: sess.LocationID, Status: sess.Status, Photos: []PhotoOut{}, Items: []ItemOut{}}, nil
}

// Session returns the current state of a session (polled by the client).
func (s *Service) Session(ctx context.Context, gid, id uuid.UUID) (SessionOut, error) {
	sess, err := s.Repo.GetSession(ctx, gid, id)
	if err != nil {
		return SessionOut{}, err
	}
	photos, err := s.Repo.SessionPhotos(ctx, gid, id)
	if err != nil {
		return SessionOut{}, err
	}
	items, err := s.Repo.SessionItems(ctx, gid, id)
	if err != nil {
		return SessionOut{}, err
	}
	out := SessionOut{ID: sess.ID, Mode: sess.Mode, LocationID: sess.LocationID, Status: sess.Status, Photos: make([]PhotoOut, 0, len(photos)), Items: make([]ItemOut, 0, len(items))}
	for _, p := range photos {
		out.Photos = append(out.Photos, PhotoOut{ID: p.ID, Status: p.Status, Error: p.Error, Detections: p.Detections, Attempts: p.Attempts})
		switch p.Status {
		case "queued", "running":
			out.Processing++
		case "failed":
			out.Failed++
		}
	}
	for _, it := range items {
		o := s.itemOut(it)
		out.Items = append(out.Items, o)
		if it.State == statePending {
			out.Pending++
			if o.Ready {
				out.ReadyCount++
			}
		}
	}
	return out, nil
}

// FinishSession marks a session done (the review screen was closed).
func (s *Service) FinishSession(ctx context.Context, gid, id uuid.UUID) error {
	return s.Repo.FinishSession(ctx, gid, id)
}

// AddPhoto validates, normalises and queues one photo. created is false when
// an identical photo is already in the session.
func (s *Service) AddPhoto(ctx context.Context, gid, sessionID uuid.UUID, data []byte) (PhotoOut, bool, error) {
	if !s.Enabled() {
		return PhotoOut{}, false, ErrDisabled
	}
	if len(data) > s.maxPhotoBytes() {
		return PhotoOut{}, false, ErrTooLarge
	}
	sess, err := s.Repo.GetSession(ctx, gid, sessionID)
	if err != nil {
		return PhotoOut{}, false, err
	}
	jpg, w, h, err := vision.Normalise(data)
	if err != nil {
		return PhotoOut{}, false, ErrNotImage
	}
	if s.Cfg.MaxPhotosPerDay > 0 {
		n, err := s.Repo.PhotosSince(ctx, gid, s.now().Add(-24*time.Hour))
		if err != nil {
			return PhotoOut{}, false, err
		}
		if n >= s.Cfg.MaxPhotosPerDay {
			return PhotoOut{}, false, ErrDailyCap
		}
	}
	sum := sha256.Sum256(jpg)
	p, created, err := s.Repo.AddPhoto(ctx, sess, hex.EncodeToString(sum[:]), "image/jpeg", w, h, jpg)
	if err != nil {
		return PhotoOut{}, false, err
	}
	if created {
		s.Kick()
	}
	return PhotoOut{ID: p.ID, Status: p.Status, Error: p.Error, Detections: p.Detections, Attempts: p.Attempts}, created, nil
}

// RetryPhoto re-queues a failed photo.
func (s *Service) RetryPhoto(ctx context.Context, gid, id uuid.UUID) error {
	if !s.Enabled() {
		return ErrDisabled
	}
	if err := s.Repo.RetryPhoto(ctx, gid, id); err != nil {
		return err
	}
	s.Kick()
	return nil
}

// ItemCrop returns the JPEG crop of an item.
func (s *Service) ItemCrop(ctx context.Context, gid, id uuid.UUID) ([]byte, error) {
	return s.Repo.ItemCrop(ctx, gid, id)
}

// ----------------------------------------------------------------- workers

// Run starts the periodic sweep (crash recovery, retention) and blocks until
// ctx is done. Photos are normally picked up immediately by Kick; the sweep
// catches anything queued while the process was down.
func (s *Service) Run(ctx context.Context) error {
	s.mu.Lock()
	s.base = ctx
	s.mu.Unlock()
	s.sweep(ctx)
	s.Kick()
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	lastPurge := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
			s.sweep(ctx)
			if s.now().Sub(lastPurge) > time.Hour {
				lastPurge = s.now()
				cutoff := s.now().Add(-s.retention())
				if err := s.Repo.PurgeBefore(ctx, cutoff, s.now().Add(-90*24*time.Hour)); err != nil {
					log.Warn().Err(err).Msg("capture: purge failed")
				}
			}
			s.Kick()
		}
	}
}

func (s *Service) sweep(ctx context.Context) {
	if err := s.Repo.RequeueStale(ctx, s.now().Add(-s.staleAfter()), MaxAttempts); err != nil {
		log.Warn().Err(err).Msg("capture: requeue stale photos failed")
	}
}

// Kick starts background workers (up to Cfg.Workers) if photos are waiting.
func (s *Service) Kick() {
	if s.Cfg.Workers <= 0 || !s.Enabled() {
		return
	}
	for {
		cur := atomic.LoadInt32(&s.active)
		if int(cur) >= s.Cfg.Workers {
			return
		}
		if atomic.CompareAndSwapInt32(&s.active, cur, cur+1) {
			break
		}
	}
	s.mu.Lock()
	ctx := s.base
	s.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	go func() {
		defer atomic.AddInt32(&s.active, -1)
		s.Drain(ctx)
	}()
}
