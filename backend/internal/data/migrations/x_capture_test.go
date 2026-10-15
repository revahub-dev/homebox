package migrations_test

// Fork addition (photo-first capture). See FORK_NOTES.md.

import (
	"context"
	"database/sql"
	"os"
	"testing"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"

	"github.com/sysadminsmedia/homebox/backend/internal/data/ent"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/xcaptureitem"
	"github.com/sysadminsmedia/homebox/backend/internal/data/migrations"
)

func captureRoundTrip(t *testing.T, db *sql.DB, driver, entDialect string) {
	t.Helper()
	fsys, err := migrations.Migrations(driver)
	require.NoError(t, err)
	goose.SetBaseFS(fsys)
	require.NoError(t, goose.SetDialect(driver))
	require.NoError(t, goose.Up(db, driver))

	ctx := context.Background()
	c := ent.NewClient(ent.Driver(entsql.OpenDB(entDialect, db)))
	g, err := c.Group.Create().SetName("x-cap-" + uuid.NewString()[:8]).Save(ctx)
	require.NoError(t, err)
	u, err := c.User.Create().SetName("u").SetEmail(uuid.NewString() + "@example.com").SetPassword("x").SetDefaultGroupID(g.ID).Save(ctx)
	require.NoError(t, err)

	s, err := c.XCaptureSession.Create().SetGroupID(g.ID).SetUserID(u.ID).SetMode("shelf").Save(ctx)
	require.NoError(t, err)
	p, err := c.XCapturePhoto.Create().SetGroupID(g.ID).SetSessionID(s.ID).SetContentHash("h1").SetImage([]byte("jpg")).Save(ctx)
	require.NoError(t, err)
	_, err = c.XCapturePhoto.Create().SetGroupID(g.ID).SetSessionID(s.ID).SetContentHash("h1").Save(ctx)
	require.Error(t, err, "unique (session, content hash)")
	_, err = c.XCaptureItem.Create().SetGroupID(g.ID).SetSessionID(s.ID).SetPhotoID(p.ID).SetName("Kettle").
		SetQuantity(2).SetConfidence(0.8).SetBbox(`{"x":0.1,"y":0.1,"w":0.3,"h":0.3}`).SetCrop([]byte("c")).Save(ctx)
	require.NoError(t, err)
	n, err := c.XCaptureItem.Query().Where(xcaptureitem.SessionID(s.ID), xcaptureitem.StateEQ(xcaptureitem.StatePending)).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n)

	// Deleting the group cascades through session → photos → items.
	_, err = db.ExecContext(ctx, rebindQ(entDialect, "DELETE FROM groups WHERE id = ?"), g.ID)
	require.NoError(t, err)
	n, err = c.XCaptureItem.Query().Where(xcaptureitem.SessionID(s.ID)).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, n)
}

func TestXCaptureMigrationsSQLite(t *testing.T) {
	db, err := sql.Open("sqlite3", "file:"+t.TempDir()+"/x.db?_fk=1&_time_format=sqlite")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	captureRoundTrip(t, db, "sqlite3", "sqlite3")
}

func TestXCaptureMigrationsPostgres(t *testing.T) {
	dsn := os.Getenv("HBOX_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("HBOX_TEST_POSTGRES_DSN not set")
	}
	db, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	captureRoundTrip(t, db, "postgres", "postgres")
}
