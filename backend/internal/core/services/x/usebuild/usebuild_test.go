package usebuild_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sysadminsmedia/homebox/backend/internal/core/services/reporting/eventbus"
	"github.com/sysadminsmedia/homebox/backend/internal/core/services/x/aichat"
	"github.com/sysadminsmedia/homebox/backend/internal/core/services/x/usebuild"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent"
	"github.com/sysadminsmedia/homebox/backend/internal/data/repo"
	"github.com/sysadminsmedia/homebox/backend/internal/sys/config"
	_ "github.com/sysadminsmedia/homebox/backend/pkgs/cgofreesqlite"
)

type env struct {
	ctx    context.Context
	db     *sql.DB
	client *ent.Client
	repos  *repo.AllRepos
	gid    uuid.UUID
	item   uuid.UUID // entity type for items
	loc    uuid.UUID // entity type for locations
}

func setup(t *testing.T) env {
	t.Helper()
	ctx := context.Background()
	db, err := sql.Open("sqlite3", "file:ub_"+uuid.NewString()+"?mode=memory&cache=shared&_fk=1&_time_format=sqlite")
	require.NoError(t, err)
	client := ent.NewClient(ent.Driver(entsql.OpenDB("sqlite3", db)))
	t.Cleanup(func() { _ = client.Close() })
	require.NoError(t, client.Schema.Create(ctx))
	bus := eventbus.New()
	bctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	go func() { _ = bus.Run(bctx) }()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "homebox"), 0o755))
	repos := repo.New(client, bus, config.Storage{PrefixPath: "/", ConnString: "file://" + dir}, "mem://{{ .Topic }}", config.Thumbnail{}, nil)
	g, err := repos.Groups.GroupCreate(ctx, "ub", uuid.Nil)
	require.NoError(t, err)
	it, err := repos.EntityTypes.GetDefault(ctx, g.ID, false)
	require.NoError(t, err)
	lt, err := repos.EntityTypes.GetDefault(ctx, g.ID, true)
	require.NoError(t, err)
	return env{ctx: ctx, db: db, client: client, repos: repos, gid: g.ID, item: it.ID, loc: lt.ID}
}

// mk creates an item and backdates its created/updated time.
func (e env) mk(t *testing.T, name string, parent uuid.UUID, age time.Duration) uuid.UUID {
	t.Helper()
	out, err := e.repos.Entities.Create(e.ctx, e.gid, repo.EntityCreate{Name: name, Quantity: 2, EntityTypeID: e.item, ParentID: parent})
	require.NoError(t, err)
	at := time.Now().Add(-age)
	require.NoError(t, e.client.Entity.UpdateOneID(out.ID).SetPurchasePrice(4000).Exec(e.ctx))
	_, err = e.db.ExecContext(e.ctx, "UPDATE entities SET created_at = ?, updated_at = ? WHERE id = ?", at, at, out.ID)
	require.NoError(t, err)
	return out.ID
}

const day = 24 * time.Hour

func TestIdleItemsAndActions(t *testing.T) {
	e := setup(t)
	now := time.Now()
	shelf, err := e.repos.Entities.Create(e.ctx, e.gid, repo.EntityCreate{Name: "Shelf", EntityTypeID: e.loc})
	require.NoError(t, err)
	old1 := e.mk(t, "Old drill", shelf.ID, 400*day)
	old2 := e.mk(t, "Old tent", uuid.Nil, 300*day)
	old3 := e.mk(t, "Old lamp", uuid.Nil, 200*day)
	_ = e.mk(t, "Fresh mouse", uuid.Nil, 10*day)
	r := e.repos.XUseBuild
	cutoff := now.Add(-180 * day)

	items, err := r.IdleItems(e.ctx, e.gid, cutoff, now, 10)
	require.NoError(t, err)
	require.Len(t, items, 3)
	assert.Equal(t, old1, items[0].ID, "oldest first")
	assert.Equal(t, "Shelf", items[0].LocationPath)

	// limit
	items, err = r.IdleItems(e.ctx, e.gid, cutoff, now, 2)
	require.NoError(t, err)
	assert.Len(t, items, 2)

	// use → no longer idle
	require.NoError(t, r.MarkUsed(e.ctx, e.gid, old1, now))
	// keep → snoozed for 90 days
	require.NoError(t, r.Snooze(e.ctx, e.gid, old2, now.Add(90*day)))
	// sell → hidden from idle, listed for sale
	require.NoError(t, r.SetForSale(e.ctx, e.gid, old3, true, "draft"))
	items, err = r.IdleItems(e.ctx, e.gid, cutoff, now, 10)
	require.NoError(t, err)
	assert.Empty(t, items)
	sale, err := r.ForSaleItems(e.ctx, e.gid)
	require.NoError(t, err)
	require.Len(t, sale, 1)
	assert.Equal(t, "draft", sale[0].SaleDraft)

	// snooze expires
	items, err = r.IdleItems(e.ctx, e.gid, cutoff, now.Add(91*day), 10)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, old2, items[0].ID)

	// archive → excluded everywhere
	require.NoError(t, r.Archive(e.ctx, e.gid, old2))
	items, err = r.IdleItems(e.ctx, e.gid, cutoff, now.Add(91*day), 10)
	require.NoError(t, err)
	assert.Empty(t, items)

	// locations and foreign entities are rejected
	require.ErrorIs(t, r.MarkUsed(e.ctx, e.gid, shelf.ID, now), repo.ErrNotAnItem)
	require.ErrorIs(t, r.MarkUsed(e.ctx, uuid.New(), old1, now), repo.ErrNotAnItem)
}

func TestDigestJob(t *testing.T) {
	e := setup(t)
	now := time.Now()
	for i := 0; i < 12; i++ {
		e.mk(t, "Idle "+string(rune('A'+i)), uuid.Nil, time.Duration(200+i)*day)
	}
	usr, err := e.repos.Users.Create(e.ctx, repo.UserCreate{Name: "u", Email: "u@example.com", DefaultGroupID: e.gid, IsOwner: true})
	require.NoError(t, err)
	_, err = e.client.Notifier.Create().SetGroupID(e.gid).SetUserID(usr.ID).SetName("n").SetURL("generic://x").SetIsActive(true).Save(e.ctx)
	require.NoError(t, err)

	var sent []string
	job := &usebuild.DigestJob{
		Repos: e.repos, Conf: config.IdleConf{Enabled: true},
		Notify:  func(_, msg string) error { sent = append(sent, msg); return nil },
		BaseURL: "https://hb.example",
	}
	require.NoError(t, job.Run(e.ctx, now))
	require.Len(t, sent, 1)
	assert.Equal(t, 10, strings.Count(sent[0], "\n - "), "≤10 items")
	assert.Contains(t, sent[0], "untouched for 180+ days")
	assert.Contains(t, sent[0], "https://hb.example/x/idle")

	// same week → nothing
	require.NoError(t, job.Run(e.ctx, now.Add(3*day)))
	assert.Len(t, sent, 1)
	// next week → sent again
	require.NoError(t, job.Run(e.ctx, now.Add(8*day)))
	assert.Len(t, sent, 2)

	// disabled
	job.Conf.Enabled = false
	require.NoError(t, job.Run(e.ctx, now.Add(30*day)))
	assert.Len(t, sent, 2)
}

type fakeChat struct {
	reply string
	err   error
	user  string
}

func (f *fakeChat) Chat(_ context.Context, _, user string, _ bool) (string, error) {
	f.user = user
	return f.reply, f.err
}

func TestSaleDraft(t *testing.T) {
	it := repo.XInvItem{Name: "電動ドリル", PurchasePrice: 8000}
	tpl := usebuild.SaleDraft(context.Background(), nil, it)
	assert.Contains(t, tpl, "タイトル: 電動ドリル")
	assert.Contains(t, tpl, "希望価格: ¥4000")

	fc := &fakeChat{reply: "タイトル: AI draft"}
	assert.Equal(t, "タイトル: AI draft", usebuild.SaleDraft(context.Background(), fc, it))
	assert.Contains(t, fc.user, "電動ドリル")

	// LLM failure falls back to the template
	fc = &fakeChat{err: errors.New("boom")}
	assert.Contains(t, usebuild.SaleDraft(context.Background(), fc, it), "タイトル: 電動ドリル")

	assert.Equal(t, 0, usebuild.SuggestedPrice(0))
	assert.Equal(t, 300, usebuild.SuggestedPrice(100))
}

func TestSuggestValidatesRefs(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	items := []repo.XInvItem{
		{ID: a, Name: "Arduino Uno", Quantity: 1, Description: "microcontroller"},
		{ID: b, Name: "LED strip", Quantity: 3},
	}
	reply := "```json\n" + `{"ideas":[
	 {"title":"Night light","status":"missing_parts","steps":["wire","flash"],
	  "owned_parts":[{"ref":"i1","quantity":1,"role":"brain"},{"ref":"i2","quantity":9},{"ref":"i99"}],
	  "missing_parts":[]},
	 {"title":"Weather station","status":"buildable_now",
	  "owned_parts":[{"id":"` + a.String() + `"},{"id":"` + uuid.NewString() + `"}],
	  "missing_parts":[{"name":"BME280 sensor","quantity":1}]},
	 {"title":"Hallucinated only","owned_parts":[{"ref":"i42"}],"missing_parts":["x"]},
	 {"title":"","owned_parts":[{"ref":"i1"}]}
	]}` + "\n```"
	fc := &fakeChat{reply: reply}
	res, err := usebuild.Suggest(context.Background(), fc, items, "Tag: electronics")
	require.NoError(t, err)
	assert.Contains(t, fc.user, "i1 | Arduino Uno | 1 | microcontroller")
	assert.Contains(t, fc.user, "Scope: Tag: electronics")
	require.Len(t, res.Ideas, 2, "ideas without real owned parts are dropped")

	n := res.Ideas[0]
	assert.Equal(t, usebuild.StatusBuildable, n.Status, "status recomputed: no missing parts")
	require.Len(t, n.OwnedParts, 2)
	assert.Equal(t, a, n.OwnedParts[0].EntityID)
	assert.Equal(t, "Arduino Uno", n.OwnedParts[0].Name)
	assert.InDelta(t, 3, n.OwnedParts[1].Quantity, 0.001, "clamped to owned qty")
	assert.Equal(t, 1, n.DroppedRefs)

	w := res.Ideas[1]
	assert.Equal(t, usebuild.StatusMissing, w.Status, "status recomputed: has missing parts")
	assert.Equal(t, 1, w.DroppedRefs)
	assert.Equal(t, "BME280 sensor", w.MissingParts[0].Name)

	for _, idea := range res.Ideas {
		for _, p := range idea.OwnedParts {
			assert.Contains(t, []uuid.UUID{a, b}, p.EntityID)
		}
	}
}

func TestSuggestErrors(t *testing.T) {
	items := []repo.XInvItem{{ID: uuid.New(), Name: "x", Quantity: 1}}
	_, err := usebuild.Suggest(context.Background(), nil, items, "")
	require.ErrorIs(t, err, aichat.ErrDisabled)
	_, err = usebuild.Suggest(context.Background(), &fakeChat{reply: "{}"}, nil, "")
	require.ErrorIs(t, err, usebuild.ErrEmptyInventory)
	_, err = usebuild.Suggest(context.Background(), &fakeChat{reply: "sorry"}, items, "")
	require.ErrorIs(t, err, usebuild.ErrBadModelReply)
	_, err = usebuild.Suggest(context.Background(), &fakeChat{reply: `{"ideas":[{"title":"t","owned_parts":[{"ref":"i9"}]}]}`}, items, "")
	require.ErrorIs(t, err, usebuild.ErrBadModelReply)
}

func TestInventoryScopeAndProjects(t *testing.T) {
	e := setup(t)
	room, err := e.repos.Entities.Create(e.ctx, e.gid, repo.EntityCreate{Name: "Room", EntityTypeID: e.loc})
	require.NoError(t, err)
	box, err := e.repos.Entities.Create(e.ctx, e.gid, repo.EntityCreate{Name: "Box", EntityTypeID: e.loc, ParentID: room.ID})
	require.NoError(t, err)
	inBox := e.mk(t, "Solder", box.ID, day)
	outside := e.mk(t, "Kettle", uuid.Nil, day)
	tg, err := e.repos.Tags.Create(e.ctx, e.gid, repo.TagCreate{Name: "electronics"})
	require.NoError(t, err)
	require.NoError(t, e.client.Entity.UpdateOneID(outside).AddTagIDs(tg.ID).Exec(e.ctx))

	r := e.repos.XUseBuild
	inv, err := r.Inventory(e.ctx, e.gid, repo.XInventoryScope{LocationID: room.ID})
	require.NoError(t, err)
	require.Len(t, inv, 1, "nested location included")
	assert.Equal(t, inBox, inv[0].ID)
	assert.Equal(t, "Room > Box", inv[0].LocationPath)

	inv, err = r.Inventory(e.ctx, e.gid, repo.XInventoryScope{TagID: tg.ID})
	require.NoError(t, err)
	require.Len(t, inv, 1)
	assert.Equal(t, outside, inv[0].ID)

	p, err := r.CreateProject(e.ctx, e.gid, repo.XProjectIn{
		Name: "Lamp", BuildStatus: "missing_parts", Steps: []string{"a"},
		OwnedParts:   []repo.XProjectPartIn{{EntityID: inBox, Quantity: 1}, {EntityID: inBox, Quantity: 1}},
		MissingParts: []repo.XMissingPart{{Name: "bulb", Quantity: 1}},
	})
	require.NoError(t, err)
	require.Len(t, p.Parts, 1)
	assert.InDelta(t, 2, p.Parts[0].Quantity, 0.001)
	assert.Equal(t, "Solder", p.Parts[0].Name)

	inv, err = r.Inventory(e.ctx, e.gid, repo.XInventoryScope{})
	require.NoError(t, err)
	for _, it := range inv {
		if it.ID == inBox {
			assert.InDelta(t, 2, it.ReservedQuantity, 0.001)
		}
	}

	// foreign / location part is rejected
	_, err = r.CreateProject(e.ctx, e.gid, repo.XProjectIn{Name: "Bad", OwnedParts: []repo.XProjectPartIn{{EntityID: box.ID}}})
	require.ErrorIs(t, err, repo.ErrNotAnItem)

	list, err := r.ListProjects(e.ctx, e.gid)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.NoError(t, r.DeleteProject(e.ctx, e.gid, p.ID))
	require.Error(t, r.DeleteProject(e.ctx, e.gid, p.ID))
	inv, err = r.Inventory(e.ctx, e.gid, repo.XInventoryScope{LocationID: room.ID})
	require.NoError(t, err)
	assert.Zero(t, inv[0].ReservedQuantity, "reservation released")
}
