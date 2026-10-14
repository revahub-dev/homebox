package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hay-kot/httpkit/errchain"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sysadminsmedia/homebox/backend/internal/core/services"
	"github.com/sysadminsmedia/homebox/backend/internal/core/services/reporting/eventbus"
	"github.com/sysadminsmedia/homebox/backend/internal/core/services/x/orderimport"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent"
	"github.com/sysadminsmedia/homebox/backend/internal/data/repo"
	"github.com/sysadminsmedia/homebox/backend/internal/sys/config"
	"github.com/sysadminsmedia/homebox/backend/internal/web/mid"
)

const fixtureDir = "../../../../internal/core/services/x/orderimport/testdata"

type importEnv struct {
	router *chi.Mux
	repos  *repo.AllRepos
	gid    uuid.UUID
}

func setupImport(t *testing.T) importEnv {
	t.Helper()
	ctx := context.Background()
	client, err := ent.Open("sqlite3", "file:imp_"+uuid.NewString()+"?mode=memory&cache=shared&_fk=1&_time_format=sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	require.NoError(t, client.Schema.Create(ctx))
	bus := eventbus.New()
	bctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	go func() { _ = bus.Run(bctx) }()
	repos := repo.New(client, bus, config.Storage{PrefixPath: "/", ConnString: "file://" + t.TempDir()}, "mem://{{ .Topic }}", config.Thumbnail{}, nil)
	g, err := repos.Groups.GroupCreate(ctx, "imp", uuid.Nil)
	require.NoError(t, err)

	ctrl := &V1Controller{repo: repos, maxParseMemory: 8, config: &config.Config{
		OrderImport: config.OrderImportConf{InboundDomain: "inbox.example.com"},
	}}
	chain := errchain.New(mid.Errors(zerolog.Nop()))
	// Stand-in for the auth middleware: put the tenant on the context.
	auth := func(next errchain.Handler) errchain.Handler {
		return errchain.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
			return next.ServeHTTP(w, r.WithContext(services.SetTenantCtx(r.Context(), g.ID)))
		})
	}
	r := chi.NewRouter()
	r.Post("/import/inbound/{token}", chain.ToHandlerFunc(ctrl.HandleImportInbound()))
	r.Get("/import/settings", chain.ToHandlerFunc(ctrl.HandleImportSettingsGet(), auth))
	r.Put("/import/settings", chain.ToHandlerFunc(ctrl.HandleImportSettingsUpdate(), auth))
	r.Post("/import/settings/rotate", chain.ToHandlerFunc(ctrl.HandleImportTokenRotate(), auth))
	r.Post("/import/eml", chain.ToHandlerFunc(ctrl.HandleImportEmlUpload(), auth))
	r.Post("/import/photo", chain.ToHandlerFunc(ctrl.HandleImportPhotoUpload(), auth))
	r.Get("/import/candidates", chain.ToHandlerFunc(ctrl.HandleImportCandidates(), auth))
	r.Post("/import/candidates/{id}/accept", chain.ToHandlerFunc(ctrl.HandleImportAccept(), auth))
	r.Post("/import/candidates/{id}/merge", chain.ToHandlerFunc(ctrl.HandleImportMerge(), auth))
	r.Post("/import/candidates/{id}/ignore", chain.ToHandlerFunc(ctrl.HandleImportIgnore(), auth))
	return importEnv{router: r, repos: repos, gid: g.ID}
}

func (e importEnv) do(t *testing.T, method, path string, body []byte, contentType string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

func (e importEnv) settings(t *testing.T) ImportSettings {
	t.Helper()
	rec := e.do(t, http.MethodGet, "/import/settings", nil, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var s ImportSettings
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &s))
	return s
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(fixtureDir, name))
	require.NoError(t, err)
	return b
}

func TestImportSettingsAndRotate(t *testing.T) {
	e := setupImport(t)
	s := e.settings(t)
	assert.GreaterOrEqual(t, len(s.Token), 32, "token carries >=160 bits")
	assert.Equal(t, "orders-"+s.Token+"@inbox.example.com", s.Address)
	assert.Equal(t, "/api/v1/import/inbound/"+s.Token, s.WebhookPath)
	assert.False(t, s.LLMEnabled)
	assert.Equal(t, s.Token, e.settings(t).Token, "stable across reads")

	rec := e.do(t, http.MethodPost, "/import/settings/rotate", nil, "")
	require.Equal(t, http.StatusOK, rec.Code)
	var rotated ImportSettings
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &rotated))
	assert.NotEqual(t, s.Token, rotated.Token)

	// Old token is dead, new one works.
	rec = e.do(t, http.MethodPost, "/import/inbound/"+s.Token, readFixture(t, "amazon_direct.eml"), "message/rfc822")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	rec = e.do(t, http.MethodPost, "/import/inbound/"+rotated.Token, readFixture(t, "amazon_direct.eml"), "message/rfc822")
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.NotNil(t, e.settings(t).LastReceivedAt)
}

func TestImportInboundRejections(t *testing.T) {
	e := setupImport(t)
	tok := e.settings(t).Token

	rec := e.do(t, http.MethodPost, "/import/inbound/"+strings.Repeat("a", 32), readFixture(t, "amazon_direct.eml"), "")
	assert.Equal(t, http.StatusNotFound, rec.Code, "unknown token")

	big := append([]byte("From: a@b.c\r\nSubject: x\r\n\r\n"), bytes.Repeat([]byte("x"), orderimport.MaxMessageBytes+10)...)
	rec = e.do(t, http.MethodPost, "/import/inbound/"+tok, big, "message/rfc822")
	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)

	rec = e.do(t, http.MethodPost, "/import/inbound/"+tok, readFixture(t, "not_an_email.eml"), "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	// Allow-list: the Amazon fixture comes straight from amazon.co.jp.
	body, _ := json.Marshal(ImportSettingsUpdate{AllowedSenders: []string{"jack@example.com", "@family.example"}})
	rec = e.do(t, http.MethodPut, "/import/settings", body, "application/json")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, []string{"jack@example.com", "@family.example"}, e.settings(t).AllowedSenders)

	rec = e.do(t, http.MethodPost, "/import/inbound/"+tok, readFixture(t, "amazon_direct.eml"), "message/rfc822")
	assert.Equal(t, http.StatusForbidden, rec.Code)
	rec = e.do(t, http.MethodPost, "/import/inbound/"+tok, readFixture(t, "amazon_forwarded_gmail.eml"), "message/rfc822")
	assert.Equal(t, http.StatusOK, rec.Code, "jack@example.com is allowed")

	cs, err := e.repos.XOrderImport.ListCandidates(context.Background(), e.gid, "", 0)
	require.NoError(t, err)
	assert.Len(t, cs, 2, "only the allowed forward was imported")
}

func TestImportInboundMultipartForm(t *testing.T) {
	e := setupImport(t)
	tok := e.settings(t).Token
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	require.NoError(t, mw.WriteField("recipient", "orders-"+tok+"@inbox.example.com"))
	require.NoError(t, mw.WriteField("body-mime", string(readFixture(t, "yodobashi_base64.eml"))))
	require.NoError(t, mw.Close())
	rec := e.do(t, http.MethodPost, "/import/inbound/"+tok, buf.Bytes(), mw.FormDataContentType())
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var res orderimport.Result
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &res))
	assert.Equal(t, orderimport.StatusImported, res.Status)
	assert.Equal(t, 2, res.Candidates)
}

func emlUpload(t *testing.T, files map[string][]byte) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for name, b := range files {
		w, err := mw.CreateFormFile("files", name)
		require.NoError(t, err)
		_, _ = w.Write(b)
	}
	require.NoError(t, mw.Close())
	return buf.Bytes(), mw.FormDataContentType()
}

func TestImportEmlUploadAndReviewFlow(t *testing.T) {
	e := setupImport(t)
	body, ct := emlUpload(t, map[string][]byte{
		"amazon.eml":     readFixture(t, "amazon_direct.eml"),
		"amazon-fwd.eml": readFixture(t, "amazon_forwarded_gmail.eml"),
		"rakuten.eml":    readFixture(t, "rakuten_iso2022jp.eml"),
		"notes.txt":      readFixture(t, "not_an_email.eml"),
	})
	rec := e.do(t, http.MethodPost, "/import/eml", body, ct)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var results []ImportFileResult
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &results))
	byName := map[string]ImportFileResult{}
	for _, r := range results {
		byName[r.FileName] = r
	}
	assert.Equal(t, "not a valid email (.eml) file", byName["notes.txt"].Error)
	assert.Equal(t, orderimport.StatusImported, byName["rakuten.eml"].Result.Status)
	statuses := []string{byName["amazon.eml"].Result.Status, byName["amazon-fwd.eml"].Result.Status}
	assert.ElementsMatch(t, []string{orderimport.StatusImported, orderimport.StatusDuplicate}, statuses)

	rec = e.do(t, http.MethodGet, "/import/candidates?status=pending", nil, "")
	require.Equal(t, http.StatusOK, rec.Code)
	var cands []repo.XImportCandidate
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &cands))
	require.Len(t, cands, 4)

	accept := cands[0].ID
	rec = e.do(t, http.MethodPost, "/import/candidates/"+accept.String()+"/accept", []byte(`{"quantity":5}`), "application/json")
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var created repo.EntityOut
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))
	assert.InDelta(t, 5, created.Quantity, 0.001)
	assert.NotEmpty(t, created.PurchaseFrom)

	rec = e.do(t, http.MethodPost, "/import/candidates/"+accept.String()+"/accept", nil, "application/json")
	assert.Equal(t, http.StatusConflict, rec.Code, "already accepted")

	merge := cands[1].ID
	rec = e.do(t, http.MethodPost, "/import/candidates/"+merge.String()+"/merge",
		[]byte(fmt.Sprintf(`{"entityId":%q}`, uuid.NewString())), "application/json")
	assert.Equal(t, http.StatusNotFound, rec.Code, "merge target outside group")
	rec = e.do(t, http.MethodPost, "/import/candidates/"+merge.String()+"/merge",
		[]byte(fmt.Sprintf(`{"entityId":%q}`, created.ID)), "application/json")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	rec = e.do(t, http.MethodPost, "/import/candidates/"+cands[2].ID.String()+"/ignore", nil, "")
	assert.Equal(t, http.StatusNoContent, rec.Code)
	rec = e.do(t, http.MethodPost, "/import/candidates/"+uuid.NewString()+"/ignore", nil, "")
	assert.Equal(t, http.StatusNotFound, rec.Code)

	rec = e.do(t, http.MethodGet, "/import/candidates?status=pending", nil, "")
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &cands))
	assert.Len(t, cands, 1)
	rec = e.do(t, http.MethodGet, "/import/candidates?status=bogus", nil, "")
	assert.NotEqual(t, http.StatusOK, rec.Code)
}

func TestImportEmlUploadLimits(t *testing.T) {
	e := setupImport(t)
	files := map[string][]byte{}
	for i := 0; i < 51; i++ {
		files[fmt.Sprintf("%d.eml", i)] = []byte("From: a@b.c\r\nSubject: s\r\n\r\nx")
	}
	body, ct := emlUpload(t, files)
	assert.Equal(t, http.StatusUnprocessableEntity, e.do(t, http.MethodPost, "/import/eml", body, ct).Code)
	assert.Equal(t, http.StatusBadRequest, e.do(t, http.MethodPost, "/import/eml", nil, "multipart/form-data; boundary=x").Code)
}

func TestImportPhotoWithoutLLM(t *testing.T) {
	e := setupImport(t)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	w, err := mw.CreateFormFile("file", "receipt.png")
	require.NoError(t, err)
	_, _ = w.Write(readFixture(t, "receipt.png"))
	require.NoError(t, mw.Close())
	rec := e.do(t, http.MethodPost, "/import/photo", buf.Bytes(), mw.FormDataContentType())
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code, rec.Body.String())
}
