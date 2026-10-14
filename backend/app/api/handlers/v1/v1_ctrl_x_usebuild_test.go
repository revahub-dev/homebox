package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	"github.com/sysadminsmedia/homebox/backend/internal/core/services/x/usebuild"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent"
	"github.com/sysadminsmedia/homebox/backend/internal/data/repo"
	"github.com/sysadminsmedia/homebox/backend/internal/sys/config"
	"github.com/sysadminsmedia/homebox/backend/internal/web/mid"
)

type ubEnv struct {
	router *chi.Mux
	client *ent.Client
	repos  *repo.AllRepos
	gid    uuid.UUID
	itemT  uuid.UUID
	locT   uuid.UUID
}

func setupUseBuild(t *testing.T) ubEnv {
	t.Helper()
	ctx := context.Background()
	client, err := ent.Open("sqlite3", "file:ub_"+uuid.NewString()+"?mode=memory&cache=shared&_fk=1&_time_format=sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	require.NoError(t, client.Schema.Create(ctx))
	bus := eventbus.New()
	bctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	go func() { _ = bus.Run(bctx) }()
	repos := repo.New(client, bus, config.Storage{PrefixPath: "/", ConnString: "file://" + t.TempDir()}, "mem://{{ .Topic }}", config.Thumbnail{}, nil)
	g, err := repos.Groups.GroupCreate(ctx, "ub", uuid.Nil)
	require.NoError(t, err)
	it, err := repos.EntityTypes.GetDefault(ctx, g.ID, false)
	require.NoError(t, err)
	lt, err := repos.EntityTypes.GetDefault(ctx, g.ID, true)
	require.NoError(t, err)

	ctrl := &V1Controller{repo: repos, maxParseMemory: 8, config: &config.Config{Idle: config.IdleConf{Enabled: true}}}
	chain := errchain.New(mid.Errors(zerolog.Nop()))
	auth := func(next errchain.Handler) errchain.Handler {
		return errchain.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
			return next.ServeHTTP(w, r.WithContext(services.SetTenantCtx(r.Context(), g.ID)))
		})
	}
	r := chi.NewRouter()
	r.Get("/idle", chain.ToHandlerFunc(ctrl.HandleIdleList(), auth))
	r.Post("/idle/{id}/use", chain.ToHandlerFunc(ctrl.HandleItemUse(), auth))
	r.Post("/idle/{id}/keep", chain.ToHandlerFunc(ctrl.HandleItemKeep(), auth))
	r.Post("/idle/{id}/sell", chain.ToHandlerFunc(ctrl.HandleItemSell(), auth))
	r.Post("/idle/{id}/unsell", chain.ToHandlerFunc(ctrl.HandleItemUnsell(), auth))
	r.Post("/idle/{id}/archive", chain.ToHandlerFunc(ctrl.HandleItemArchive(), auth))
	r.Get("/build/status", chain.ToHandlerFunc(ctrl.HandleBuildStatus(), auth))
	r.Post("/build/suggest", chain.ToHandlerFunc(ctrl.HandleBuildSuggest(), auth))
	r.Get("/build/projects", chain.ToHandlerFunc(ctrl.HandleProjectsList(), auth))
	r.Post("/build/projects", chain.ToHandlerFunc(ctrl.HandleProjectCreate(), auth))
	r.Delete("/build/projects/{id}", chain.ToHandlerFunc(ctrl.HandleProjectDelete(), auth))
	return ubEnv{router: r, client: client, repos: repos, gid: g.ID, itemT: it.ID, locT: lt.ID}
}

func (e ubEnv) do(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var b []byte
	if body != nil {
		var err error
		b, err = json.Marshal(body)
		require.NoError(t, err)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

func (e ubEnv) mk(t *testing.T, name string, typ, parent uuid.UUID) uuid.UUID {
	t.Helper()
	out, err := e.repos.Entities.Create(context.Background(), e.gid, repo.EntityCreate{Name: name, Quantity: 1, EntityTypeID: typ, ParentID: parent})
	require.NoError(t, err)
	return out.ID
}

type fakeChat struct{ reply string }

func (f fakeChat) Chat(context.Context, string, string, bool) (string, error) { return f.reply, nil }

func TestBuildSuggestHandler(t *testing.T) {
	e := setupUseBuild(t)
	loc := e.mk(t, "Workbench", e.locT, uuid.Nil)
	a := e.mk(t, "Raspberry Pi", e.itemT, loc)
	b := e.mk(t, "Servo", e.itemT, uuid.Nil)

	// no LLM configured → 503
	aiChatOverride = nil
	rec := e.do(t, http.MethodPost, "/build/suggest", map[string]any{})
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code, rec.Body.String())
	rec = e.do(t, http.MethodGet, "/build/status", nil)
	assert.JSONEq(t, `{"aiEnabled":false}`, rec.Body.String())

	aiChatOverride = fakeChat{reply: `{"ideas":[
	  {"title":"Pan-tilt camera","steps":["mount"],"owned_parts":[{"ref":"i1"},{"ref":"i2"},{"ref":"i7"}],
	   "missing_parts":[{"name":"Camera module"}]},
	  {"title":"Ghost","owned_parts":[{"id":"` + uuid.NewString() + `"}]}]}`}
	t.Cleanup(func() { aiChatOverride = nil })

	rec = e.do(t, http.MethodPost, "/build/suggest", map[string]any{})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var res usebuild.BuildResult
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &res))
	require.Len(t, res.Ideas, 1)
	assert.Equal(t, 2, res.ItemsSent)
	assert.Equal(t, usebuild.StatusMissing, res.Ideas[0].Status)
	assert.Equal(t, 1, res.Ideas[0].DroppedRefs)
	ids := []uuid.UUID{}
	for _, p := range res.Ideas[0].OwnedParts {
		ids = append(ids, p.EntityID)
	}
	assert.ElementsMatch(t, []uuid.UUID{a, b}, ids)

	// location scope
	rec = e.do(t, http.MethodPost, "/build/suggest", map[string]any{"locationId": loc})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &res))
	assert.Equal(t, 1, res.ItemsSent)
	// unknown scope → 404; item as location → 404
	rec = e.do(t, http.MethodPost, "/build/suggest", map[string]any{"locationId": uuid.New()})
	assert.Equal(t, http.StatusNotFound, rec.Code)
	rec = e.do(t, http.MethodPost, "/build/suggest", map[string]any{"locationId": a})
	assert.Equal(t, http.StatusNotFound, rec.Code)
	rec = e.do(t, http.MethodPost, "/build/suggest", map[string]any{"tagId": uuid.New()})
	assert.Equal(t, http.StatusNotFound, rec.Code)

	// save as project → reserves parts
	idea := res.Ideas[0]
	parts := []map[string]any{}
	for _, p := range idea.OwnedParts {
		parts = append(parts, map[string]any{"entityId": p.EntityID, "quantity": p.Quantity, "role": p.Role})
	}
	rec = e.do(t, http.MethodPost, "/build/projects", map[string]any{
		"name": idea.Title, "buildStatus": idea.Status, "steps": idea.Steps,
		"ownedParts": parts, "missingParts": idea.MissingParts,
	})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var p repo.XProjectOut
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	assert.Len(t, p.Parts, 1)
	// a location can't be reserved
	rec = e.do(t, http.MethodPost, "/build/projects", map[string]any{"name": "x", "ownedParts": []map[string]any{{"entityId": loc}}})
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)

	rec = e.do(t, http.MethodGet, "/build/projects", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var list []repo.XProjectOut
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	require.Len(t, list, 1)
	rec = e.do(t, http.MethodDelete, "/build/projects/"+p.ID.String(), nil)
	assert.Equal(t, http.StatusNoContent, rec.Code)
	rec = e.do(t, http.MethodDelete, "/build/projects/"+p.ID.String(), nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestIdleHandlers(t *testing.T) {
	e := setupUseBuild(t)
	ctx := context.Background()
	old := e.mk(t, "Old camera", e.itemT, uuid.Nil)
	other := e.mk(t, "Old bike", e.itemT, uuid.Nil)
	_ = e.mk(t, "New phone", e.itemT, uuid.Nil)
	list := func() IdleList {
		rec := e.do(t, http.MethodGet, "/idle", nil)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var l IdleList
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &l))
		return l
	}
	l := list()
	assert.Equal(t, 180, l.Days)
	assert.Equal(t, 90, l.SnoozeDays)
	assert.Empty(t, l.Items, "fresh items are not idle")

	// actions work on any item
	assert.Equal(t, http.StatusNoContent, e.do(t, http.MethodPost, "/idle/"+old.String()+"/use", nil).Code)
	assert.Equal(t, http.StatusNoContent, e.do(t, http.MethodPost, "/idle/"+old.String()+"/keep", nil).Code)
	rec := e.do(t, http.MethodPost, "/idle/"+other.String()+"/sell", nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var d SaleDraftOut
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &d))
	assert.Contains(t, d.Draft, "タイトル: Old bike")
	assert.False(t, d.Generated)
	l = list()
	require.Len(t, l.ForSale, 1)
	assert.Equal(t, other, l.ForSale[0].ID)
	assert.Equal(t, http.StatusNoContent, e.do(t, http.MethodPost, "/idle/"+other.String()+"/unsell", nil).Code)
	assert.Empty(t, list().ForSale)

	assert.Equal(t, http.StatusNoContent, e.do(t, http.MethodPost, "/idle/"+other.String()+"/archive", nil).Code)
	got, err := e.client.Entity.Get(ctx, other)
	require.NoError(t, err)
	assert.True(t, got.Archived)

	act, err := e.repos.XUseBuild.Inventory(ctx, e.gid, repo.XInventoryScope{})
	require.NoError(t, err)
	for _, it := range act {
		if it.ID == old {
			assert.Equal(t, 1, it.UseCount)
			require.NotNil(t, it.SnoozedUntil)
			assert.WithinDuration(t, time.Now().Add(90*24*time.Hour), *it.SnoozedUntil, time.Minute)
		}
	}

	// unknown / foreign entity → 404
	assert.Equal(t, http.StatusNotFound, e.do(t, http.MethodPost, "/idle/"+uuid.NewString()+"/use", nil).Code)
	assert.Equal(t, http.StatusNotFound, e.do(t, http.MethodPost, "/idle/"+uuid.NewString()+"/sell", nil).Code)
}
