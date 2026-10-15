package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/sysadminsmedia/homebox/backend/internal/core/dupcheck"
	"github.com/sysadminsmedia/homebox/backend/internal/core/services"
	"github.com/sysadminsmedia/homebox/backend/internal/core/services/reporting/eventbus"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent"
	"github.com/sysadminsmedia/homebox/backend/internal/data/repo"
	"github.com/sysadminsmedia/homebox/backend/internal/sys/config"
	_ "github.com/sysadminsmedia/homebox/backend/pkgs/cgofreesqlite"
)

func setupDupCheck(t *testing.T) (*V1Controller, uuid.UUID, repo.EntityOut) {
	t.Helper()
	ctx := context.Background()
	client, err := ent.Open("sqlite3", "file:dupcheck_"+uuid.NewString()+"?mode=memory&cache=shared&_fk=1&_time_format=sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	require.NoError(t, client.Schema.Create(ctx))

	bus := eventbus.New()
	busCtx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	go func() { _ = bus.Run(busCtx) }()

	repos := repo.New(client, bus, config.Storage{PrefixPath: "/", ConnString: "file://" + t.TempDir()},
		"mem://{{ .Topic }}", config.Thumbnail{}, nil)

	g, err := repos.Groups.GroupCreate(ctx, "dupcheck", uuid.Nil)
	require.NoError(t, err)

	locType, err := repos.EntityTypes.GetDefault(ctx, g.ID, true)
	require.NoError(t, err)
	itemType, err := repos.EntityTypes.GetDefault(ctx, g.ID, false)
	require.NoError(t, err)

	garage, err := repos.Entities.Create(ctx, g.ID, repo.EntityCreate{Name: "Garage", EntityTypeID: locType.ID})
	require.NoError(t, err)
	drawer, err := repos.Entities.Create(ctx, g.ID, repo.EntityCreate{Name: "Drawer 3", EntityTypeID: locType.ID, ParentID: garage.ID})
	require.NoError(t, err)
	item, err := repos.Entities.Create(ctx, g.ID, repo.EntityCreate{
		Name: "ソニー ワイヤレスヘッドホン", EntityTypeID: itemType.ID, ParentID: drawer.ID, ModelNumber: "WH-1000XM5",
	})
	require.NoError(t, err)
	_, err = repos.Entities.Create(ctx, g.ID, repo.EntityCreate{Name: "Garden hose", EntityTypeID: itemType.ID, ParentID: garage.ID})
	require.NoError(t, err)

	return &V1Controller{repo: repos}, g.ID, item
}

func serveDupCheck(t *testing.T, ctrl *V1Controller, gid uuid.UUID, req *http.Request) (int, DuplicateCheckResult) {
	t.Helper()
	h := ctrl.HandleDuplicateCheckGet()
	if req.Method == http.MethodPost {
		h = ctrl.HandleDuplicateCheckPost()
	}
	req = req.WithContext(services.SetTenantCtx(req.Context(), gid))
	rec := httptest.NewRecorder()
	if err := h(rec, req); err != nil {
		return http.StatusBadRequest, DuplicateCheckResult{}
	}
	var out DuplicateCheckResult
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	return rec.Code, out
}

func TestHandleDuplicateCheck(t *testing.T) {
	ctrl, gid, item := setupDupCheck(t)

	t.Run("GET model number exact match with location path", func(t *testing.T) {
		q := url.Values{"name": {"Sony headphones"}, "modelNumber": {"wh1000xm5"}}
		code, out := serveDupCheck(t, ctrl, gid, httptest.NewRequest(http.MethodGet, "/?"+q.Encode(), nil))
		require.Equal(t, http.StatusOK, code)
		require.Len(t, out.Matches, 1)
		m := out.Matches[0]
		assert.Equal(t, item.ID, m.ID)
		assert.Equal(t, dupcheck.TierExact, m.Confidence)
		assert.Equal(t, dupcheck.ReasonModelNumber, m.MatchedOn)
		assert.Equal(t, "Garage > Drawer 3", m.LocationPath)
		require.Len(t, m.Location, 2)
	})

	t.Run("POST normalized name match (half-width katakana)", func(t *testing.T) {
		body := `{"name":"ｿﾆｰ　ﾜｲﾔﾚｽﾍｯﾄﾞﾎﾝ"}`
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		code, out := serveDupCheck(t, ctrl, gid, req)
		require.Equal(t, http.StatusOK, code)
		require.Len(t, out.Matches, 1)
		assert.Equal(t, dupcheck.TierLikely, out.Matches[0].Confidence)
	})

	t.Run("no match returns empty list", func(t *testing.T) {
		code, out := serveDupCheck(t, ctrl, gid, httptest.NewRequest(http.MethodGet, "/?name=toaster", nil))
		require.Equal(t, http.StatusOK, code)
		assert.NotNil(t, out.Matches)
		assert.Empty(t, out.Matches)
	})

	t.Run("other group sees nothing", func(t *testing.T) {
		code, out := serveDupCheck(t, ctrl, uuid.New(), httptest.NewRequest(http.MethodGet, "/?modelNumber=WH-1000XM5", nil))
		require.Equal(t, http.StatusOK, code)
		assert.Empty(t, out.Matches)
	})

	t.Run("empty query rejected", func(t *testing.T) {
		code, _ := serveDupCheck(t, ctrl, gid, httptest.NewRequest(http.MethodGet, "/", nil))
		assert.Equal(t, http.StatusBadRequest, code)
	})
}
