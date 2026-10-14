package migrations_test

// Fork addition (use & build). See FORK_NOTES.md.

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"

	"github.com/sysadminsmedia/homebox/backend/internal/data/ent"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/xentityactivity"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/xprojectpart"
	"github.com/sysadminsmedia/homebox/backend/internal/data/migrations"
)

func useAndBuildRoundTrip(t *testing.T, db *sql.DB, driver, entDialect string) {
	t.Helper()
	fsys, err := migrations.Migrations(driver)
	require.NoError(t, err)
	goose.SetBaseFS(fsys)
	require.NoError(t, goose.SetDialect(driver))
	require.NoError(t, goose.Up(db, driver))

	ctx := context.Background()
	c := ent.NewClient(ent.Driver(entsql.OpenDB(entDialect, db)))

	g, err := c.Group.Create().SetName("x-ub-" + uuid.NewString()[:8]).Save(ctx)
	require.NoError(t, err)
	et, err := c.EntityType.Create().SetName("Item").SetGroupID(g.ID).Save(ctx)
	require.NoError(t, err)
	e, err := c.Entity.Create().SetName("drill").SetGroupID(g.ID).SetEntityTypeID(et.ID).Save(ctx)
	require.NoError(t, err)

	now := time.Now().UTC().Truncate(time.Second)
	_, err = c.XEntityActivity.Create().SetGroupID(g.ID).SetEntityID(e.ID).
		SetLastUsedAt(now).SetUseCount(1).SetForSale(true).SetSaleDraft("draft").Save(ctx)
	require.NoError(t, err)
	// one activity row per entity
	_, err = c.XEntityActivity.Create().SetGroupID(g.ID).SetEntityID(e.ID).Save(ctx)
	require.Error(t, err)

	_, err = c.XIdleDigest.Create().SetGroupID(g.ID).SetLastSentAt(now).SetItemCount(3).Save(ctx)
	require.NoError(t, err)

	p, err := c.XProject.Create().SetGroupID(g.ID).SetName("shelf").SetBuildStatus("buildable_now").
		SetSteps(`["cut"]`).Save(ctx)
	require.NoError(t, err)
	_, err = c.XProjectPart.Create().SetProjectID(p.ID).SetEntityID(e.ID).SetQuantity(2).Save(ctx)
	require.NoError(t, err)

	// deleting the entity cascades to activity and reservations
	_, err = db.ExecContext(ctx, rebindQ(entDialect, "DELETE FROM entities WHERE id = ?"), e.ID)
	require.NoError(t, err)
	n, err := c.XEntityActivity.Query().Where(xentityactivity.EntityID(e.ID)).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, n)
	n, err = c.XProjectPart.Query().Where(xprojectpart.EntityID(e.ID)).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, n)

	require.NoError(t, c.XProject.DeleteOneID(p.ID).Exec(ctx))
	_, err = db.ExecContext(ctx, rebindQ(entDialect, "DELETE FROM groups WHERE id = ?"), g.ID)
	require.NoError(t, err)
}

func rebindQ(d, q string) string {
	if d == "postgres" {
		return q[:len(q)-1] + "$1"
	}
	return q
}

func TestXUseAndBuildMigrationsSQLite(t *testing.T) {
	db, err := sql.Open("sqlite3", "file:"+t.TempDir()+"/x.db?_fk=1&_time_format=sqlite")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	useAndBuildRoundTrip(t, db, "sqlite3", "sqlite3")
}

func TestXUseAndBuildMigrationsPostgres(t *testing.T) {
	dsn := os.Getenv("HBOX_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("HBOX_TEST_POSTGRES_DSN not set")
	}
	db, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	useAndBuildRoundTrip(t, db, "postgres", "postgres")
}
