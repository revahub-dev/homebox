package migrations_test

// Fork test: the order-import migrations apply cleanly on top of upstream's and
// match the ent schema well enough for a create/query round trip.
// Postgres runs only when HBOX_TEST_POSTGRES_DSN is set.

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	entdialect "entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"

	"github.com/sysadminsmedia/homebox/backend/internal/data/ent"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/ximportcandidate"
	"github.com/sysadminsmedia/homebox/backend/internal/data/migrations"
	_ "github.com/sysadminsmedia/homebox/backend/internal/data/migrations/postgres"
	_ "github.com/sysadminsmedia/homebox/backend/internal/data/migrations/sqlite3"
	"github.com/sysadminsmedia/homebox/backend/internal/sys/config"
	_ "github.com/sysadminsmedia/homebox/backend/pkgs/cgofreesqlite"
)

func roundTrip(t *testing.T, db *sql.DB, driver, entDialect string) {
	t.Helper()
	fsys, err := migrations.Migrations(driver)
	require.NoError(t, err)
	goose.SetBaseFS(fsys)
	require.NoError(t, goose.SetDialect(driver))
	require.NoError(t, goose.Up(db, driver))

	ctx := context.Background()
	c := ent.NewClient(ent.Driver(entsql.OpenDB(entDialect, db)))

	g, err := c.Group.Create().SetName("x-mig-" + uuid.NewString()[:8]).Save(ctx)
	require.NoError(t, err)

	_, err = c.XInboundAddress.Create().SetGroupID(g.ID).SetToken(uuid.NewString()).SetAllowedSenders("a@example.com").Save(ctx)
	require.NoError(t, err)

	now := time.Now().UTC().Truncate(time.Second)
	m, err := c.XInboundMessage.Create().
		SetGroupID(g.ID).SetChannel("eml").SetStatus("parsed").SetContentHash("h1").
		SetMessageID("<a@b>").SetStore("amazonjp").SetOrderNumber("250-1").
		SetOrderDate(now).SetOrderTotal(1234.5).SetRaw([]byte("raw")).Save(ctx)
	require.NoError(t, err)

	_, err = c.XImportCandidate.Create().
		SetGroupID(g.ID).SetInboundMessageID(m.ID).SetName("ケーブル").SetQuantity(2).
		SetUnitPrice(980).SetOrderDate(now).SetConfidence(0.95).SetLowConfidence(true).Save(ctx)
	require.NoError(t, err)

	got, err := c.XImportCandidate.Query().Where(ximportcandidate.GroupID(g.ID)).WithMessage().Only(ctx)
	require.NoError(t, err)
	require.Equal(t, "ケーブル", got.Name)
	require.Equal(t, ximportcandidate.StatusPending, got.Status)
	require.Equal(t, "250-1", got.Edges.Message.OrderNumber)

	// Unique (group_id, content_hash) is enforced by the migration.
	_, err = c.XInboundMessage.Create().SetGroupID(g.ID).SetChannel("eml").SetContentHash("h1").Save(ctx)
	require.Error(t, err)

	// Deleting the group cascades to the fork tables.
	_, err = db.ExecContext(ctx, rebind(entDialect, "DELETE FROM groups WHERE id = ?"), g.ID)
	require.NoError(t, err)
	n, err := c.XImportCandidate.Query().Where(ximportcandidate.GroupID(g.ID)).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, n)
}

func rebind(d, q string) string {
	if d == entdialect.Postgres {
		return "DELETE FROM groups WHERE id = $1"
	}
	return q
}

func TestXOrderImportMigrationsSQLite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.db")
	db, err := sql.Open("sqlite3", "file:"+path+"?_fk=1&_time_format=sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	roundTrip(t, db, config.DriverSqlite3, entdialect.SQLite)
}

func TestXOrderImportMigrationsPostgres(t *testing.T) {
	dsn := os.Getenv("HBOX_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("HBOX_TEST_POSTGRES_DSN not set")
	}
	db, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	roundTrip(t, db, config.DriverPostgres, entdialect.Postgres)
}
