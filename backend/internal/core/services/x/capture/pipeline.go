package capture

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/sysadminsmedia/homebox/backend/internal/core/dupcheck"
	"github.com/sysadminsmedia/homebox/backend/internal/core/services/x/vision"
	"github.com/sysadminsmedia/homebox/backend/internal/data/repo"
)

// Drain processes queued photos until the queue is empty or ctx is done.
// Returns the number of photos handled.
func (s *Service) Drain(ctx context.Context) int {
	n := 0
	for ctx.Err() == nil {
		ok, err := s.RunOnce(ctx)
		if err != nil {
			log.Warn().Err(err).Msg("capture: worker error")
			select {
			case <-ctx.Done():
			case <-time.After(2 * time.Second):
			}
			continue
		}
		if !ok {
			return n
		}
		n++
	}
	return n
}

// RunOnce claims and processes one photo. It returns false when the queue is empty.
func (s *Service) RunOnce(ctx context.Context) (bool, error) {
	if !s.Enabled() {
		return false, nil
	}
	p, err := s.Repo.ClaimNextPhoto(ctx, s.now())
	if err != nil || p == nil {
		return false, err
	}
	start := time.Now()
	items, err := s.process(ctx, *p)
	if err != nil {
		retry := p.Attempts < MaxAttempts && !errors.Is(err, errBadImage) && ctx.Err() == nil
		msg := friendlyError(err)
		log.Warn().Err(err).Str("photo", p.ID.String()).Int("attempt", p.Attempts).Bool("retry", retry).Msg("capture: photo analysis failed")
		if ferr := s.Repo.FailPhoto(context.WithoutCancel(ctx), p.ID, msg, retry); ferr != nil {
			return true, ferr
		}
		if retry {
			select { // brief backoff so a down endpoint is not hammered
			case <-ctx.Done():
			case <-time.After(s.retryDelay()):
			}
		}
		return true, nil
	}
	tgt := s.sessionLocation(ctx, *p)
	if err := s.Repo.CompletePhoto(ctx, *p, tgt, s.Provider.Name(), time.Since(start), items); err != nil {
		return true, err
	}
	return true, nil
}

var errBadImage = errors.New("stored image could not be decoded")

func friendlyError(err error) string {
	msg := err.Error()
	switch {
	case errors.Is(err, errBadImage):
		return "The photo could not be read."
	case strings.Contains(msg, "context deadline exceeded") || strings.Contains(msg, "Client.Timeout"):
		return "The AI model took too long to answer. Try a smaller model or raise HBOX_AI_TIMEOUT_SEC."
	case strings.Contains(msg, "connection refused") || strings.Contains(msg, "no such host"):
		return "The AI server could not be reached. Check HBOX_AI_BASE_URL."
	}
	return "Analysis failed: " + msg
}

func (s *Service) sessionLocation(ctx context.Context, p repo.XCapturePhoto) *uuid.UUID {
	sess, err := s.Repo.GetSession(ctx, p.GroupID, p.SessionID)
	if err != nil {
		return nil
	}
	return sess.LocationID
}

// process runs detection, cropping and duplicate checks for one photo and
// returns the rows to store (already merged with pending items of the same
// session).
func (s *Service) process(ctx context.Context, p repo.XCapturePhoto) ([]repo.XCaptureItemCreate, error) {
	sess, err := s.Repo.GetSession(ctx, p.GroupID, p.SessionID)
	if err != nil {
		return nil, err
	}
	img, err := vision.Decode(p.Image)
	if err != nil {
		return nil, errBadImage
	}
	res, err := s.Provider.Detect(ctx, vision.DetectRequest{
		Image: p.Image, MIME: p.MIME, Mode: vision.Mode(sess.Mode), Width: p.Width, Height: p.Height, Language: s.Cfg.Language,
	})
	if err != nil {
		return nil, err
	}

	var pool []dupcheck.Candidate
	if len(res.Detections) > 0 {
		if pool, err = s.Entities.DupCheckCandidates(ctx, p.GroupID); err != nil {
			return nil, err
		}
	}
	existing, err := s.Repo.SessionItems(ctx, p.GroupID, p.SessionID)
	if err != nil {
		return nil, err
	}
	pending := map[string]repo.XCaptureItem{}
	for _, it := range existing {
		if it.State == statePending {
			pending[dupcheck.NormalizeName(it.Name)] = it
		}
	}

	out := make([]repo.XCaptureItemCreate, 0, len(res.Detections))
	for _, d := range res.Detections {
		key := dupcheck.NormalizeName(d.Name)
		if prev, ok := pending[key]; ok && key != "" {
			// Same product seen in an earlier photo of this session: keep the
			// larger count instead of listing it twice.
			if d.Quantity > prev.Quantity {
				q := d.Quantity
				_ = s.Repo.PatchItem(ctx, p.GroupID, prev.ID, repo.XCaptureItemPatch{Quantity: &q})
			}
			continue
		}
		row := repo.XCaptureItemCreate{
			Name: d.Name, Brand: d.Brand, ModelNumber: d.ModelNumber, Category: d.Category, Quantity: d.Quantity,
			Confidence: d.Confidence, LowConfidence: d.Confidence < ReviewConfidence, BBox: "",
			DupMatches: s.dupSnapshot(ctx, p.GroupID, pool, d.Name, d.ModelNumber),
		}
		crop := d.BBox
		if d.BBox != nil {
			b, _ := json.Marshal(d.BBox)
			row.BBox = string(b)
		} else if sess.Mode == string(vision.ModeItem) || len(res.Detections) == 1 {
			crop = &vision.BBox{X: 0, Y: 0, W: 1, H: 1} // no box: show the whole photo
		}
		if crop != nil {
			if jpg, err := vision.Crop(img, *crop); err == nil {
				row.Crop = jpg
			}
		}
		out = append(out, row)
		pending[key] = repo.XCaptureItem{}
	}
	return out, nil
}

// dupSnapshot ranks inventory entities against a detection and stores the top
// three with their location paths (same shape as the order-import snapshot).
func (s *Service) dupSnapshot(ctx context.Context, gid uuid.UUID, pool []dupcheck.Candidate, name, model string) string {
	if len(pool) == 0 {
		return "[]"
	}
	ms, err := dupcheck.Rank(ctx, dupcheck.Query{Name: name, ModelNumber: model}, pool, dupcheck.Options{Limit: 3})
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

// refreshDups recomputes the duplicate banner after an edit.
func (s *Service) refreshDups(ctx context.Context, gid uuid.UUID, it repo.XCaptureItem) error {
	pool, err := s.Entities.DupCheckCandidates(ctx, gid)
	if err != nil {
		return fmt.Errorf("dup check: %w", err)
	}
	return s.Repo.SetDupMatches(ctx, gid, it.ID, s.dupSnapshot(ctx, gid, pool, it.Name, it.ModelNumber))
}

func (s *Service) retryDelay() time.Duration {
	if s.Cfg.RetryDelay > 0 {
		return s.Cfg.RetryDelay
	}
	return time.Second
}
