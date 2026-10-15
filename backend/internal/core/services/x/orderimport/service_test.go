package orderimport_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sysadminsmedia/homebox/backend/internal/core/services/reporting/eventbus"
	"github.com/sysadminsmedia/homebox/backend/internal/core/services/x/orderimport"
	"github.com/sysadminsmedia/homebox/backend/internal/core/services/x/orderimport/parsers"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent"
	"github.com/sysadminsmedia/homebox/backend/internal/data/repo"
	"github.com/sysadminsmedia/homebox/backend/internal/sys/config"
	_ "github.com/sysadminsmedia/homebox/backend/pkgs/cgofreesqlite"
)

type env struct {
	svc   *orderimport.Service
	repos *repo.AllRepos
	gid   uuid.UUID
	ctx   context.Context
}

func setup(t *testing.T, extractor *fakeLLM) env {
	t.Helper()
	ctx := context.Background()
	client, err := ent.Open("sqlite3", "file:oi_"+uuid.NewString()+"?mode=memory&cache=shared&_fk=1&_time_format=sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	require.NoError(t, client.Schema.Create(ctx))
	bus := eventbus.New()
	bctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	go func() { _ = bus.Run(bctx) }()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "homebox"), 0o755))
	repos := repo.New(client, bus, config.Storage{PrefixPath: "/", ConnString: "file://" + dir}, "mem://{{ .Topic }}", config.Thumbnail{}, nil)
	g, err := repos.Groups.GroupCreate(ctx, "oi", uuid.Nil)
	require.NoError(t, err)
	svc := &orderimport.Service{Repo: repos.XOrderImport, Entities: repos.Entities, Attachments: repos.Attachments}
	if extractor != nil {
		svc.LLM = extractor
	}
	return env{svc: svc, repos: repos, gid: g.ID, ctx: ctx}
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return b
}

func (e env) pending(t *testing.T) []repo.XImportCandidate {
	t.Helper()
	cs, err := e.repos.XOrderImport.ListCandidates(e.ctx, e.gid, "pending", 0)
	require.NoError(t, err)
	return cs
}

type fakeLLM struct {
	text, image *parsers.Order
	calls       int
}

func (f *fakeLLM) ExtractText(context.Context, string, string) (*parsers.Order, error) {
	f.calls++
	if f.text == nil {
		return &parsers.Order{}, nil
	}
	return f.text, nil
}

func (f *fakeLLM) ExtractImage(context.Context, string, []byte) (*parsers.Order, error) {
	f.calls++
	return f.image, nil
}

func TestEmailDedupAcrossChannels(t *testing.T) {
	e := setup(t, nil)

	r, err := e.svc.IngestEmail(e.ctx, e.gid, orderimport.ChannelEML, fixture(t, "amazon_direct.eml"))
	require.NoError(t, err)
	assert.Equal(t, orderimport.StatusImported, r.Status)
	assert.Equal(t, 2, r.Candidates)
	assert.Equal(t, "parser:amazonjp", r.Extractor)

	// Same bytes again (re-upload).
	r, err = e.svc.IngestEmail(e.ctx, e.gid, orderimport.ChannelEML, fixture(t, "amazon_direct.eml"))
	require.NoError(t, err)
	assert.Equal(t, orderimport.StatusDuplicate, r.Status)

	// Forwarded copy (new Message-ID, same order number) via the webhook.
	r, err = e.svc.IngestEmail(e.ctx, e.gid, orderimport.ChannelForward, fixture(t, "amazon_forwarded_gmail.eml"))
	require.NoError(t, err)
	assert.Equal(t, orderimport.StatusDuplicate, r.Status)
	assert.Contains(t, r.Note, "250-1234567-7654321")

	// Rakuten: direct, then forwarded as attachment (same original Message-ID).
	r, err = e.svc.IngestEmail(e.ctx, e.gid, orderimport.ChannelForward, fixture(t, "rakuten_iso2022jp.eml"))
	require.NoError(t, err)
	assert.Equal(t, orderimport.StatusImported, r.Status)
	r, err = e.svc.IngestEmail(e.ctx, e.gid, orderimport.ChannelEML, fixture(t, "rakuten_forwarded_attachment.eml"))
	require.NoError(t, err)
	assert.Equal(t, orderimport.StatusDuplicate, r.Status)

	assert.Len(t, e.pending(t), 4, "2 Amazon + 2 Rakuten, no duplicates")
}

func TestAllFixturesWithoutLLM(t *testing.T) {
	e := setup(t, nil)
	want := map[string]string{
		"amazon_direct.eml":                  orderimport.StatusImported,
		"rakuten_iso2022jp.eml":              orderimport.StatusImported,
		"yodobashi_base64.eml":               orderimport.StatusImported,
		"yodobashi_forwarded_outlook_ja.eml": orderimport.StatusImported,
		"gmail_forwarding_confirmation.eml":  orderimport.StatusUnparsed,
		"unknown_store.eml":                  orderimport.StatusUnparsed,
	}
	for name, status := range want {
		r, err := e.svc.IngestEmail(e.ctx, e.gid, orderimport.ChannelEML, fixture(t, name))
		require.NoError(t, err, name)
		assert.Equal(t, status, r.Status, name)
	}
	_, err := e.svc.IngestEmail(e.ctx, e.gid, orderimport.ChannelEML, fixture(t, "not_an_email.eml"))
	require.ErrorIs(t, err, orderimport.ErrInvalidEmail)

	var notices int
	for _, c := range e.pending(t) {
		if c.Kind == "notice" {
			notices++
			if c.Channel == "eml" && c.Store == "" && len(c.Note) > 0 && c.Name != "" {
				assert.NotEmpty(t, c.Note)
			}
		}
	}
	assert.Equal(t, 2, notices)
}

func TestForwardingConfirmationSurfacesLink(t *testing.T) {
	e := setup(t, nil)
	_, err := e.svc.IngestEmail(e.ctx, e.gid, orderimport.ChannelForward, fixture(t, "gmail_forwarding_confirmation.eml"))
	require.NoError(t, err)
	cs := e.pending(t)
	require.Len(t, cs, 1)
	assert.Equal(t, "notice", cs[0].Kind)
	assert.Contains(t, cs[0].Note, "https://mail-settings.google.com/mail/vf-example")
	require.NoError(t, e.svc.Ignore(e.ctx, e.gid, cs[0].ID))
	assert.Empty(t, e.pending(t))
}

func TestLLMFallbackForUnknownStore(t *testing.T) {
	f := &fakeLLM{text: &parsers.Order{Store: "example-shop", OrderNumber: "A-0001", Currency: "JPY",
		Items: []parsers.LineItem{{Name: "無印良品 アクリル仕切りスタンド 3仕切り", Quantity: 1, UnitPrice: 1290}}}}
	e := setup(t, f)
	r, err := e.svc.IngestEmail(e.ctx, e.gid, orderimport.ChannelEML, fixture(t, "unknown_store.eml"))
	require.NoError(t, err)
	assert.Equal(t, orderimport.StatusImported, r.Status)
	assert.Equal(t, "llm", r.Extractor)
	cs := e.pending(t)
	require.Len(t, cs, 1)
	assert.InDelta(t, 0.6, cs[0].Confidence, 0.001)

	// Parsers win when they can; LLM is not called for known stores or confirmations.
	f.calls = 0
	_, err = e.svc.IngestEmail(e.ctx, e.gid, orderimport.ChannelEML, fixture(t, "yodobashi_base64.eml"))
	require.NoError(t, err)
	_, err = e.svc.IngestEmail(e.ctx, e.gid, orderimport.ChannelEML, fixture(t, "gmail_forwarding_confirmation.eml"))
	require.NoError(t, err)
	assert.Zero(t, f.calls)
}

func TestDupCheckAnnotationAcceptAndMerge(t *testing.T) {
	e := setup(t, nil)
	// An item the user already owns, with the Anker ASIN stored as a custom field.
	itemType, err := e.repos.EntityTypes.GetDefault(e.ctx, e.gid, false)
	require.NoError(t, err)
	locType, err := e.repos.EntityTypes.GetDefault(e.ctx, e.gid, true)
	require.NoError(t, err)
	drawer, err := e.repos.Entities.Create(e.ctx, e.gid, repo.EntityCreate{Name: "Drawer 3", EntityTypeID: locType.ID})
	require.NoError(t, err)
	owned, err := e.repos.Entities.Create(e.ctx, e.gid, repo.EntityCreate{Name: "Battery pack", Quantity: 1, EntityTypeID: itemType.ID, ParentID: drawer.ID})
	require.NoError(t, err)
	require.NoError(t, e.repos.XOrderImport.ApplyPurchase(e.ctx, e.gid, owned.ID, repo.XPurchaseInfo{ASIN: "B019GNUT0C"}))

	_, err = e.svc.IngestEmail(e.ctx, e.gid, orderimport.ChannelEML, fixture(t, "amazon_direct.eml"))
	require.NoError(t, err)
	byName := map[string]repo.XImportCandidate{}
	for _, c := range e.pending(t) {
		byName[c.Name] = c
	}
	anker := byName["Anker PowerCore 10000 (モバイルバッテリー 10000mAh)"]
	var dm []orderimport.DupMatch
	require.NoError(t, json.Unmarshal([]byte(anker.DupMatches), &dm))
	require.NotEmpty(t, dm)
	assert.Equal(t, owned.ID, dm[0].ID)
	assert.Equal(t, "exact", dm[0].Confidence)
	assert.Equal(t, "Drawer 3", dm[0].LocationPath)

	// Merge the Anker line into the owned item: qty 1 + 2.
	merged, err := e.svc.Merge(e.ctx, e.gid, anker.ID, owned.ID)
	require.NoError(t, err)
	assert.InDelta(t, 3, merged.Quantity, 0.001)
	assert.InDelta(t, 2990, merged.PurchasePrice, 0.001)
	assert.Equal(t, "Amazon.co.jp", merged.PurchaseFrom)

	// Accept the Sony line with an edited name, into the drawer.
	sony := byName["ソニー ワイヤレスノイズキャンセリングヘッドホン WH-1000XM5 ブラック"]
	out, err := e.svc.Accept(e.ctx, e.gid, sony.ID, orderimport.AcceptInput{Name: "Sony WH-1000XM5", ParentID: drawer.ID})
	require.NoError(t, err)
	assert.Equal(t, "Sony WH-1000XM5", out.Name)
	assert.InDelta(t, 44000, out.PurchasePrice, 0.001)
	assert.Equal(t, "2026-10-10", time.Time(out.PurchaseDate).Format("2006-01-02"))
	assert.Equal(t, "Amazon.co.jp", out.PurchaseFrom)
	var asin string
	for _, f := range out.Fields {
		if f.Name == "ASIN" {
			asin = f.TextValue
		}
	}
	assert.Equal(t, "B09Y2MYL5C", asin)

	// Second accept is refused; the inbox is empty.
	_, err = e.svc.Accept(e.ctx, e.gid, sony.ID, orderimport.AcceptInput{})
	require.ErrorIs(t, err, repo.ErrCandidateNotPending)
	assert.Empty(t, e.pending(t))

	// The accepted item is now found by the duplicate check via its ASIN.
	pool, err := e.repos.Entities.DupCheckCandidates(e.ctx, e.gid)
	require.NoError(t, err)
	found := false
	for _, p := range pool {
		if p.ID == out.ID && len(p.ASINs) == 1 && p.ASINs[0] == "B09Y2MYL5C" {
			found = true
		}
	}
	assert.True(t, found)

	// Merge into an entity of another group is rejected.
	_, err = e.svc.IngestEmail(e.ctx, e.gid, orderimport.ChannelEML, fixture(t, "yodobashi_base64.eml"))
	require.NoError(t, err)
	_, err = e.svc.Merge(e.ctx, e.gid, e.pending(t)[0].ID, uuid.New())
	require.ErrorIs(t, err, orderimport.ErrEntityNotFound)
}

func TestPhotoImport(t *testing.T) {
	e := setup(t, nil)
	_, err := e.svc.IngestPhoto(e.ctx, e.gid, "image/png", fixture(t, "receipt.png"))
	require.ErrorIs(t, err, orderimport.ErrVisionDisabled)
	_, err = e.svc.IngestPhoto(e.ctx, e.gid, "text/plain", []byte("x"))
	require.ErrorIs(t, err, orderimport.ErrNotAnImage)

	d := time.Date(2026, 10, 14, 0, 0, 0, 0, time.FixedZone("JST", 9*3600))
	f := &fakeLLM{image: &parsers.Order{Store: "yodobashi", OrderDate: &d, Total: 5480, Currency: "JPY",
		Items: []parsers.LineItem{{Name: "ブラザー ラベルライター P-touch PT-P300BT", Quantity: 1, UnitPrice: 5480}}}}
	e = setup(t, f)
	r, err := e.svc.IngestPhoto(e.ctx, e.gid, "image/png", fixture(t, "receipt.png"))
	require.NoError(t, err)
	assert.Equal(t, orderimport.StatusImported, r.Status)
	cs := e.pending(t)
	require.Len(t, cs, 1)
	assert.True(t, cs[0].LowConfidence)
	assert.Equal(t, "photo", cs[0].Channel)

	// A different photo of the same receipt: same store, day and total.
	r, err = e.svc.IngestPhoto(e.ctx, e.gid, "image/jpeg", []byte("\xff\xd8 another shot"))
	require.NoError(t, err)
	assert.Equal(t, orderimport.StatusDuplicate, r.Status)
	assert.Len(t, e.pending(t), 1)

	// Accepting keeps the photo as a receipt attachment.
	out, err := e.svc.Accept(e.ctx, e.gid, cs[0].ID, orderimport.AcceptInput{})
	require.NoError(t, err)
	require.Len(t, out.Attachments, 1)
	assert.Equal(t, "receipt", out.Attachments[0].Type)
}

func TestSenderAllowed(t *testing.T) {
	assert.True(t, orderimport.SenderAllowed(nil, "a@b.c"))
	assert.True(t, orderimport.SenderAllowed([]string{"jack@example.com"}, "Jack@Example.com"))
	assert.True(t, orderimport.SenderAllowed([]string{"@example.com"}, "x@example.com"))
	assert.False(t, orderimport.SenderAllowed([]string{"@example.com"}, "x@evil-example.com"))
	assert.False(t, orderimport.SenderAllowed([]string{"jack@example.com"}, "eve@example.com"))
}
