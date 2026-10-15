package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hay-kot/httpkit/errchain"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sysadminsmedia/homebox/backend/internal/core/services"
	"github.com/sysadminsmedia/homebox/backend/internal/core/services/reporting/eventbus"
	"github.com/sysadminsmedia/homebox/backend/internal/core/services/x/capture"
	"github.com/sysadminsmedia/homebox/backend/internal/core/services/x/vision"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent"
	"github.com/sysadminsmedia/homebox/backend/internal/data/repo"
	"github.com/sysadminsmedia/homebox/backend/internal/sys/config"
	"github.com/sysadminsmedia/homebox/backend/internal/web/mid"
)

const (
	keyLocationID = "locationId"
	keyEntityID   = "entityId"
)

type capEnv struct {
	router *chi.Mux
	repos  *repo.AllRepos
	svc    *capture.Service
	fake   *vision.Fake
	gid    uuid.UUID
	locID  uuid.UUID
	locT   uuid.UUID
	itemT  uuid.UUID
}

func setupCapture(t *testing.T, withProvider bool) capEnv {
	t.Helper()
	ctx := context.Background()
	client, err := ent.Open("sqlite3", "file:cap_"+uuid.NewString()+"?mode=memory&cache=shared&_fk=1&_time_format=sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	require.NoError(t, client.Schema.Create(ctx))
	bus := eventbus.New()
	bctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	go func() { _ = bus.Run(bctx) }()
	repos := repo.New(client, bus, config.Storage{PrefixPath: "/", ConnString: "file://" + t.TempDir()}, "mem://{{ .Topic }}", config.Thumbnail{}, nil)
	g, err := repos.Groups.GroupCreate(ctx, "cap", uuid.Nil)
	require.NoError(t, err)
	it, err := repos.EntityTypes.GetDefault(ctx, g.ID, false)
	require.NoError(t, err)
	lt, err := repos.EntityTypes.GetDefault(ctx, g.ID, true)
	require.NoError(t, err)
	loc, err := repos.Entities.Create(ctx, g.ID, repo.EntityCreate{Name: "Garage shelf", EntityTypeID: lt.ID})
	require.NoError(t, err)

	fake := &vision.Fake{}
	svc := &capture.Service{
		Repo: repos.XCapture, Entities: repos.Entities, EntityTypes: repos.EntityTypes, Tags: repos.Tags,
		Attachments: repos.Attachments, Cfg: capture.Config{Workers: 0, RetryDelay: time.Millisecond, MaxPhotoBytes: 1 << 20, MaxPhotosPerDay: 50},
	}
	if withProvider {
		svc.Provider = fake
	}
	ctrl := &V1Controller{repo: repos, maxParseMemory: 8, config: &config.Config{}, capture: svc}
	chain := errchain.New(mid.Errors(zerolog.Nop()))
	tenant := g.ID
	auth := func(next errchain.Handler) errchain.Handler {
		return errchain.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
			gid := tenant
			if h := r.Header.Get("X-Test-Group"); h != "" {
				gid = uuid.MustParse(h)
			}
			return next.ServeHTTP(w, r.WithContext(services.SetTenantCtx(r.Context(), gid)))
		})
	}
	r := chi.NewRouter()
	r.Get("/capture/status", chain.ToHandlerFunc(ctrl.HandleCaptureStatus(), auth))
	r.Post("/capture/sessions", chain.ToHandlerFunc(ctrl.HandleCaptureSessionCreate(), auth))
	r.Get("/capture/sessions/{id}", chain.ToHandlerFunc(ctrl.HandleCaptureSessionGet(), auth))
	r.Post("/capture/sessions/{id}/photos", chain.ToHandlerFunc(ctrl.HandleCapturePhotoUpload(), auth))
	r.Post("/capture/sessions/{id}/accept", chain.ToHandlerFunc(ctrl.HandleCaptureSessionAccept(), auth))
	r.Post("/capture/sessions/{id}/finish", chain.ToHandlerFunc(ctrl.HandleCaptureSessionFinish(), auth))
	r.Post("/capture/photos/{id}/retry", chain.ToHandlerFunc(ctrl.HandleCapturePhotoRetry(), auth))
	r.Get("/capture/items/{id}/crop", chain.ToHandlerFunc(ctrl.HandleCaptureItemCrop(), auth))
	r.Patch("/capture/items/{id}", chain.ToHandlerFunc(ctrl.HandleCaptureItemEdit(), auth))
	r.Post("/capture/items/{id}/accept", chain.ToHandlerFunc(ctrl.HandleCaptureItemAccept(), auth))
	r.Post("/capture/items/{id}/merge", chain.ToHandlerFunc(ctrl.HandleCaptureItemMerge(), auth))
	r.Post("/capture/items/{id}/ignore", chain.ToHandlerFunc(ctrl.HandleCaptureItemIgnore(), auth))
	r.Post("/capture/items/{id}/undo", chain.ToHandlerFunc(ctrl.HandleCaptureItemUndo(), auth))
	return capEnv{router: r, repos: repos, svc: svc, fake: fake, gid: g.ID, locID: loc.ID, locT: lt.ID, itemT: it.ID}
}

func (e capEnv) do(t *testing.T, method, path string, body any, hdr ...string) *httptest.ResponseRecorder {
	t.Helper()
	var b []byte
	if body != nil {
		var err error
		b, err = json.Marshal(body)
		require.NoError(t, err)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

func (e capEnv) upload(t *testing.T, sessionID uuid.UUID, data []byte) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "shot.jpg")
	require.NoError(t, err)
	_, _ = fw.Write(data)
	require.NoError(t, mw.Close())
	req := httptest.NewRequest(http.MethodPost, "/capture/sessions/"+sessionID.String()+"/photos", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

func testJPEG(t *testing.T, shade uint8) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 320, 240))
	for y := 0; y < 240; y++ {
		for x := 0; x < 320; x++ {
			img.Set(x, y, color.RGBA{R: shade, G: uint8(x), B: uint8(y), A: 255})
		}
	}
	var b bytes.Buffer
	require.NoError(t, jpeg.Encode(&b, img, nil))
	return b.Bytes()
}

func decodeInto[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &v), rec.Body.String())
	return v
}

func (e capEnv) newSession(t *testing.T, mode string, loc *uuid.UUID) capture.SessionOut {
	t.Helper()
	rec := e.do(t, http.MethodPost, "/capture/sessions", map[string]any{"mode": mode, keyLocationID: loc})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	return decodeInto[capture.SessionOut](t, rec)
}

func (e capEnv) session(t *testing.T, id uuid.UUID) capture.SessionOut {
	t.Helper()
	rec := e.do(t, http.MethodGet, "/capture/sessions/"+id.String(), nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	return decodeInto[capture.SessionOut](t, rec)
}

func box(x, y, w, h float64) *vision.BBox { return &vision.BBox{X: x, Y: y, W: w, H: h} }

func itemByName(t *testing.T, s capture.SessionOut, name string) capture.ItemOut {
	t.Helper()
	for _, it := range s.Items {
		if it.Name == name {
			return it
		}
	}
	t.Fatalf("item %q not in session: %+v", name, s.Items)
	return capture.ItemOut{}
}

func (e capEnv) entity(t *testing.T, id uuid.UUID) repo.EntityOut {
	t.Helper()
	out, err := e.repos.Entities.GetOneByGroup(context.Background(), e.gid, id)
	require.NoError(t, err)
	return out
}

// shelfFake returns the detections used by most tests: photo 1 has an
// existing-inventory duplicate, a clean item and a weak guess; photo 2 sees
// the scissors again (higher count) plus a new item.
func shelfFake(f *vision.Fake) {
	n := 0
	f.Fn = func(vision.DetectRequest) (vision.DetectResult, error) {
		n++
		if n == 1 {
			return vision.DetectResult{Detections: []vision.Detection{
				{Name: "Anker USB-C Cable", Quantity: 2, Confidence: 0.9, BBox: box(0.05, 0.05, 0.3, 0.3)},
				{Name: "Scissors", Brand: "Fiskars", Category: "Tools", Quantity: 1, Confidence: 0.92, BBox: box(0.4, 0.1, 0.3, 0.3)},
				{Name: "Blurry thing", Quantity: 1, Confidence: 0.3, BBox: box(0.1, 0.6, 0.2, 0.2)},
			}}, nil
		}
		return vision.DetectResult{Detections: []vision.Detection{
			{Name: "Scissors", Quantity: 3, Confidence: 0.9, BBox: box(0.4, 0.1, 0.3, 0.3)},
			{Name: "Duct tape", Quantity: 1, Confidence: 0.88},
		}}, nil
	}
}

func TestCapture_DisabledWithoutProvider(t *testing.T) {
	e := setupCapture(t, false)
	st := decodeInto[capture.Status](t, e.do(t, http.MethodGet, "/capture/status", nil))
	assert.False(t, st.Enabled)
	s := e.newSession(t, "item", nil)
	rec := e.upload(t, s.ID, testJPEG(t, 1))
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Body.String(), "HBOX_AI_BASE_URL")
}

func TestCapture_StatusReportsProvider(t *testing.T) {
	e := setupCapture(t, true)
	e.svc.Cfg.BaseURL = "http://192.168.1.20:11434/v1"
	e.svc.Cfg.Model = "qwen2.5vl"
	st := decodeInto[capture.Status](t, e.do(t, http.MethodGet, "/capture/status", nil))
	assert.True(t, st.Enabled)
	assert.True(t, st.Local)
	assert.Equal(t, "qwen2.5vl", st.Model)
	assert.NotContains(t, e.do(t, http.MethodGet, "/capture/status", nil).Body.String(), "11434")
}

func TestCapture_UploadValidation(t *testing.T) {
	e := setupCapture(t, true)
	s := e.newSession(t, "item", nil)

	rec := e.upload(t, s.ID, []byte("this is not an image"))
	assert.Equal(t, http.StatusUnsupportedMediaType, rec.Code)

	big := bytes.Repeat([]byte{0xFF}, (1<<20)+10)
	assert.Equal(t, http.StatusRequestEntityTooLarge, e.upload(t, s.ID, big).Code)

	// unknown session / other group's session
	assert.Equal(t, http.StatusNotFound, e.upload(t, uuid.New(), testJPEG(t, 2)).Code)
	assert.Equal(t, http.StatusNotFound,
		e.do(t, http.MethodGet, "/capture/sessions/"+s.ID.String(), nil, "X-Test-Group", uuid.NewString()).Code)

	// bad mode / bad location
	assert.Equal(t, http.StatusUnprocessableEntity, e.do(t, http.MethodPost, "/capture/sessions", map[string]any{"mode": "room"}).Code)
	bad := uuid.New()
	assert.Equal(t, http.StatusUnprocessableEntity,
		e.do(t, http.MethodPost, "/capture/sessions", map[string]any{"mode": "item", keyLocationID: bad}).Code)

	ok := e.upload(t, s.ID, testJPEG(t, 3))
	require.Equal(t, http.StatusAccepted, ok.Code, ok.Body.String())
	// same bytes again → same photo, not queued twice
	again := e.upload(t, s.ID, testJPEG(t, 3))
	require.Equal(t, http.StatusAccepted, again.Code)
	assert.Equal(t, decodeInto[capture.PhotoOut](t, ok).ID, decodeInto[capture.PhotoOut](t, again).ID)
	assert.Len(t, e.session(t, s.ID).Photos, 1)
}

func TestCapture_FullFlow(t *testing.T) {
	e := setupCapture(t, true)
	shelfFake(e.fake)
	ctx := context.Background()

	existing, err := e.repos.Entities.Create(ctx, e.gid, repo.EntityCreate{Name: "Anker USB-C Cable", Quantity: 1, ParentID: e.locID})
	require.NoError(t, err)

	s := e.newSession(t, "shelf", &e.locID)
	require.Equal(t, http.StatusAccepted, e.upload(t, s.ID, testJPEG(t, 10)).Code)
	require.Equal(t, http.StatusAccepted, e.upload(t, s.ID, testJPEG(t, 200)).Code)
	got := e.session(t, s.ID)
	assert.Equal(t, 2, got.Processing)
	assert.Empty(t, got.Items)

	require.Equal(t, 2, e.svc.Drain(ctx))
	got = e.session(t, s.ID)
	assert.Equal(t, 0, got.Processing)
	require.Len(t, got.Items, 4, "scissors from photo 2 must merge into photo 1's row")
	assert.Equal(t, 4, got.Pending)

	scissors := itemByName(t, got, "Scissors")
	assert.InDelta(t, 3.0, scissors.Quantity, 1e-9, "cross-photo merge keeps the larger count")
	assert.Equal(t, "ready", scissors.Bucket)
	assert.True(t, scissors.Ready)
	assert.True(t, scissors.HasCrop)
	assert.NotNil(t, scissors.BBox)

	cable := itemByName(t, got, "Anker USB-C Cable")
	require.NotEmpty(t, cable.DupMatches)
	assert.Equal(t, existing.ID, cable.DupMatches[0].ID)
	assert.Equal(t, "likely", cable.DupMatches[0].Confidence)
	assert.Equal(t, "Garage shelf", cable.DupMatches[0].LocationPath)
	assert.False(t, cable.Ready, "a likely duplicate is never bulk-accepted")

	blurry := itemByName(t, got, "Blurry thing")
	assert.Equal(t, "maybe", blurry.Bucket)

	tape := itemByName(t, got, "Duct tape")
	assert.False(t, tape.HasCrop, "no box in shelf mode with several detections → no crop")

	// crop is a JPEG
	rec := e.do(t, http.MethodGet, "/capture/items/"+scissors.ID.String()+"/crop", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "image/jpeg", rec.Header().Get("Content-Type"))
	_, err = jpeg.Decode(rec.Body)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound,
		e.do(t, http.MethodGet, "/capture/items/"+scissors.ID.String()+"/crop", nil, "X-Test-Group", uuid.NewString()).Code)

	// accept: creates the item in the session location with the crop as photo
	rec = e.do(t, http.MethodPost, "/capture/items/"+scissors.ID.String()+"/accept", nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	acc := decodeInto[capture.AcceptResult](t, rec)
	ent1 := e.entity(t, acc.EntityID)
	assert.Equal(t, "Scissors", ent1.Name)
	assert.InDelta(t, 3.0, ent1.Quantity, 1e-9)
	assert.Equal(t, "Fiskars", ent1.Manufacturer)
	require.NotNil(t, ent1.Parent)
	assert.Equal(t, e.locID, ent1.Parent.ID)
	has, err := e.repos.XCapture.EntityHasPrimaryPhoto(ctx, acc.EntityID)
	require.NoError(t, err)
	assert.True(t, has)
	assert.True(t, acc.Item.CanUndo)
	// a second accept conflicts
	assert.Equal(t, http.StatusConflict, e.do(t, http.MethodPost, "/capture/items/"+scissors.ID.String()+"/accept", nil).Code)

	// undo removes the created item and reopens the row
	rec = e.do(t, http.MethodPost, "/capture/items/"+scissors.ID.String()+"/undo", nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "pending", decodeInto[capture.ItemOut](t, rec).State)
	_, err = e.repos.Entities.GetOneByGroup(ctx, e.gid, acc.EntityID)
	assert.True(t, ent.IsNotFound(err), "entity should be gone after undo, got %v", err)
	assert.Equal(t, http.StatusConflict, e.do(t, http.MethodPost, "/capture/items/"+scissors.ID.String()+"/undo", nil).Code)

	// merge into the existing cable: quantity grows and the cable gets a photo
	rec = e.do(t, http.MethodPost, "/capture/items/"+cable.ID.String()+"/merge", map[string]any{keyEntityID: existing.ID})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.InDelta(t, 3.0, e.entity(t, existing.ID).Quantity, 1e-9)
	has, _ = e.repos.XCapture.EntityHasPrimaryPhoto(ctx, existing.ID)
	assert.True(t, has)
	// merging into a location or a foreign id is refused
	assert.Equal(t, http.StatusNotFound, e.do(t, http.MethodPost, "/capture/items/"+blurry.ID.String()+"/merge", map[string]any{keyEntityID: e.locID}).Code)
	assert.Equal(t, http.StatusNotFound, e.do(t, http.MethodPost, "/capture/items/"+blurry.ID.String()+"/merge", map[string]any{keyEntityID: uuid.New()}).Code)

	// undo the merge restores quantity and removes the added photo
	require.Equal(t, http.StatusOK, e.do(t, http.MethodPost, "/capture/items/"+cable.ID.String()+"/undo", nil).Code)
	assert.InDelta(t, 1.0, e.entity(t, existing.ID).Quantity, 1e-9)
	has, _ = e.repos.XCapture.EntityHasPrimaryPhoto(ctx, existing.ID)
	assert.False(t, has)

	// ignore + undo
	rec = e.do(t, http.MethodPost, "/capture/items/"+blurry.ID.String()+"/ignore", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "ignored", decodeInto[capture.ItemOut](t, rec).State)
	require.Equal(t, http.StatusOK, e.do(t, http.MethodPost, "/capture/items/"+blurry.ID.String()+"/undo", nil).Code)

	// bulk accept: only ready, non-duplicate items (scissors, tape)
	got = e.session(t, s.ID)
	assert.Equal(t, 2, got.ReadyCount)
	rec = e.do(t, http.MethodPost, "/capture/sessions/"+s.ID.String()+"/accept", nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, 2, decodeInto[capture.BulkResult](t, rec).Accepted)
	got = e.session(t, s.ID)
	assert.Equal(t, 2, got.Pending) // cable + blurry stay for manual review
	assert.Equal(t, "accepted", itemByName(t, got, "Duct tape").State)

	// the whole session can be closed
	assert.Equal(t, http.StatusNoContent, e.do(t, http.MethodPost, "/capture/sessions/"+s.ID.String()+"/finish", nil).Code)
	assert.Equal(t, http.StatusConflict, e.upload(t, s.ID, testJPEG(t, 99)).Code)
}

func TestCapture_EditRefreshesDuplicateBanner(t *testing.T) {
	e := setupCapture(t, true)
	ctx := context.Background()
	e.fake.Result = vision.DetectResult{Detections: []vision.Detection{{Name: "Cable thing", Quantity: 1, Confidence: 0.9}}}
	existing, err := e.repos.Entities.Create(ctx, e.gid, repo.EntityCreate{Name: "Hammer", Quantity: 1, ParentID: e.locID})
	require.NoError(t, err)

	s := e.newSession(t, "item", nil)
	require.Equal(t, http.StatusAccepted, e.upload(t, s.ID, testJPEG(t, 5)).Code)
	e.svc.Drain(ctx)
	it := e.session(t, s.ID).Items[0]
	assert.Empty(t, it.DupMatches)
	assert.True(t, it.HasCrop, "item mode without a box falls back to the whole photo")

	rec := e.do(t, http.MethodPatch, "/capture/items/"+it.ID.String(), map[string]any{"name": "hammer", "quantity": 2, "targetLocationId": e.locID})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	out := decodeInto[capture.ItemOut](t, rec)
	assert.Equal(t, "hammer", out.Name)
	assert.InDelta(t, 2.0, out.Quantity, 1e-9)
	require.NotEmpty(t, out.DupMatches)
	assert.Equal(t, existing.ID, out.DupMatches[0].ID)

	assert.Equal(t, http.StatusUnprocessableEntity, e.do(t, http.MethodPatch, "/capture/items/"+it.ID.String(), map[string]any{"targetLocationId": existing.ID}).Code)
	assert.Equal(t, http.StatusUnprocessableEntity, e.do(t, http.MethodPatch, "/capture/items/"+it.ID.String(), map[string]any{"quantity": -3}).Code)

	// edits to handled items conflict
	require.Equal(t, http.StatusOK, e.do(t, http.MethodPost, "/capture/items/"+it.ID.String()+"/ignore", nil).Code)
	assert.Equal(t, http.StatusConflict, e.do(t, http.MethodPatch, "/capture/items/"+it.ID.String(), map[string]any{"name": "x"}).Code)
}

func TestCapture_AcceptWithoutLocationUsesUnsorted(t *testing.T) {
	e := setupCapture(t, true)
	ctx := context.Background()
	e.fake.Result = vision.DetectResult{Detections: []vision.Detection{{Name: "Mystery box", Quantity: 1, Confidence: 0.9}}}
	s := e.newSession(t, "item", nil)
	require.Equal(t, http.StatusAccepted, e.upload(t, s.ID, testJPEG(t, 6)).Code)
	e.svc.Drain(ctx)
	it := e.session(t, s.ID).Items[0]
	a := decodeInto[capture.AcceptResult](t, e.do(t, http.MethodPost, "/capture/items/"+it.ID.String()+"/accept", nil))
	en := e.entity(t, a.EntityID)
	require.NotNil(t, en.Parent)
	assert.Equal(t, capture.UnsortedName, en.Parent.Name)

	// a second item reuses the same Unsorted location
	e.fake.Result = vision.DetectResult{Detections: []vision.Detection{{Name: "Other box", Quantity: 1, Confidence: 0.9}}}
	s2 := e.newSession(t, "item", nil)
	require.Equal(t, http.StatusAccepted, e.upload(t, s2.ID, testJPEG(t, 7)).Code)
	e.svc.Drain(ctx)
	it2 := e.session(t, s2.ID).Items[0]
	a2 := decodeInto[capture.AcceptResult](t, e.do(t, http.MethodPost, "/capture/items/"+it2.ID.String()+"/accept", nil))
	assert.Equal(t, en.Parent.ID, e.entity(t, a2.EntityID).Parent.ID)
}

func TestCapture_FailureRetriesThenFails(t *testing.T) {
	e := setupCapture(t, true)
	ctx := context.Background()
	e.fake.Err = errors.New("dial tcp: connection refused")
	s := e.newSession(t, "item", nil)
	require.Equal(t, http.StatusAccepted, e.upload(t, s.ID, testJPEG(t, 8)).Code)

	e.svc.Drain(ctx)
	got := e.session(t, s.ID)
	require.Len(t, got.Photos, 1)
	assert.Equal(t, "failed", got.Photos[0].Status)
	assert.Equal(t, capture.MaxAttempts, got.Photos[0].Attempts)
	assert.Contains(t, got.Photos[0].Error, "HBOX_AI_BASE_URL")
	assert.Equal(t, 1, got.Failed)
	assert.Equal(t, capture.MaxAttempts, e.fake.Calls)

	// the user fixes the endpoint and retries
	e.fake.Err = nil
	e.fake.Result = vision.DetectResult{Detections: []vision.Detection{{Name: "Lamp", Quantity: 1, Confidence: 0.9}}}
	require.Equal(t, http.StatusNoContent, e.do(t, http.MethodPost, "/capture/photos/"+got.Photos[0].ID.String()+"/retry", nil).Code)
	e.svc.Drain(ctx)
	got = e.session(t, s.ID)
	assert.Equal(t, "done", got.Photos[0].Status)
	assert.Len(t, got.Items, 1)
	// only failed photos can be retried
	assert.Equal(t, http.StatusNotFound, e.do(t, http.MethodPost, "/capture/photos/"+got.Photos[0].ID.String()+"/retry", nil).Code)
}

func TestCapture_DailyCap(t *testing.T) {
	e := setupCapture(t, true)
	e.svc.Cfg.MaxPhotosPerDay = 2
	s := e.newSession(t, "item", nil)
	assert.Equal(t, http.StatusAccepted, e.upload(t, s.ID, testJPEG(t, 20)).Code)
	assert.Equal(t, http.StatusAccepted, e.upload(t, s.ID, testJPEG(t, 21)).Code)
	assert.Equal(t, http.StatusTooManyRequests, e.upload(t, s.ID, testJPEG(t, 22)).Code)
}

func TestCapture_ConcurrentAcceptCreatesOneItem(t *testing.T) {
	e := setupCapture(t, true)
	ctx := context.Background()
	e.fake.Result = vision.DetectResult{Detections: []vision.Detection{{Name: "Race lamp", Quantity: 1, Confidence: 0.9}}}
	s := e.newSession(t, "item", &e.locID)
	require.Equal(t, http.StatusAccepted, e.upload(t, s.ID, testJPEG(t, 30)).Code)
	e.svc.Drain(ctx)
	it := e.session(t, s.ID).Items[0]

	var wg sync.WaitGroup
	var wins int32
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := e.svc.Accept(ctx, e.gid, it.ID); err == nil {
				atomic.AddInt32(&wins, 1)
			}
		}()
	}
	wg.Wait()
	assert.EqualValues(t, 1, wins)
	n, err := e.repos.XUseBuild.Client().Entity.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, n, "location + exactly one created item")
}

func TestCapture_WorkerClaimsOncePerPhoto(t *testing.T) {
	e := setupCapture(t, true)
	ctx := context.Background()
	e.fake.Result = vision.DetectResult{Detections: []vision.Detection{{Name: "Solo", Quantity: 1, Confidence: 0.9}}}
	s := e.newSession(t, "item", nil)
	for i := 0; i < 5; i++ {
		require.Equal(t, http.StatusAccepted, e.upload(t, s.ID, testJPEG(t, uint8(40+i))).Code)
	}
	var wg sync.WaitGroup
	var handled int32
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				ok, err := e.svc.RunOnce(ctx)
				if err != nil || !ok {
					return
				}
				atomic.AddInt32(&handled, 1)
			}
		}()
	}
	wg.Wait()
	assert.EqualValues(t, 5, handled)
	got := e.session(t, s.ID)
	assert.Equal(t, 0, got.Processing)
	for _, p := range got.Photos {
		assert.Equal(t, "done", p.Status)
		assert.Equal(t, 1, p.Attempts)
	}
}

func TestCapture_StaleRunningPhotoIsRecovered(t *testing.T) {
	e := setupCapture(t, true)
	ctx := context.Background()
	e.fake.Result = vision.DetectResult{Detections: []vision.Detection{{Name: "Recovered", Quantity: 1, Confidence: 0.9}}}
	s := e.newSession(t, "item", nil)
	require.Equal(t, http.StatusAccepted, e.upload(t, s.ID, testJPEG(t, 60)).Code)
	// a worker claims the photo, then "crashes"
	p, err := e.repos.XCapture.ClaimNextPhoto(ctx, time.Now().Add(-time.Hour))
	require.NoError(t, err)
	require.NotNil(t, p)
	ok, err := e.svc.RunOnce(ctx)
	require.NoError(t, err)
	assert.False(t, ok, "a running photo is not claimable")

	require.NoError(t, e.repos.XCapture.RequeueStale(ctx, time.Now().Add(-5*time.Minute), capture.MaxAttempts))
	assert.Equal(t, 1, e.svc.Drain(ctx))
	got := e.session(t, s.ID)
	assert.Equal(t, "done", got.Photos[0].Status)
	assert.Equal(t, 2, got.Photos[0].Attempts)
	assert.Len(t, got.Items, 1)
}

func TestCapture_PurgeDropsOldBlobs(t *testing.T) {
	e := setupCapture(t, true)
	ctx := context.Background()
	e.fake.Result = vision.DetectResult{Detections: []vision.Detection{{Name: "Old thing", Quantity: 1, Confidence: 0.9}}}
	s := e.newSession(t, "item", nil)
	require.Equal(t, http.StatusAccepted, e.upload(t, s.ID, testJPEG(t, 70)).Code)
	e.svc.Drain(ctx)
	it := e.session(t, s.ID).Items[0]
	require.True(t, it.HasCrop)

	require.NoError(t, e.repos.XCapture.PurgeBefore(ctx, time.Now().Add(time.Hour), time.Now().Add(-time.Hour)))
	got := e.session(t, s.ID)
	assert.False(t, got.Items[0].HasCrop)
	assert.Equal(t, http.StatusNotFound, e.do(t, http.MethodGet, "/capture/items/"+it.ID.String()+"/crop", nil).Code)
	// sessions older than the second cutoff are deleted entirely
	require.NoError(t, e.repos.XCapture.PurgeBefore(ctx, time.Now().Add(time.Hour), time.Now().Add(time.Hour)))
	assert.Equal(t, http.StatusNotFound, e.do(t, http.MethodGet, "/capture/sessions/"+s.ID.String(), nil).Code)
}

func TestCapture_IsLocalEndpoint(t *testing.T) {
	for url, want := range map[string]bool{
		"http://localhost:11434/v1":        true,
		"http://127.0.0.1:1234/v1":         true,
		"http://192.168.0.5:11434/v1":      true,
		"http://10.0.0.2/v1":               true,
		"http://host.docker.internal:1234": true,
		"http://ollama:11434/v1":           true,
		"http://gpu-box.local:11434/v1":    true,
		"https://api.openai.com/v1":        false,
		"https://openrouter.ai/api/v1":     false,
		"http://8.8.8.8/v1":                false,
		"":                                 false,
	} {
		assert.Equal(t, want, capture.IsLocalEndpoint(url), url)
	}
}
