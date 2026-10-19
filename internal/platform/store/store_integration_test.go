//go:build integration

package store_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"example.com/app/internal/platform/store"
	"example.com/app/internal/platform/store/storetest"
)

// testDB is set once by TestMain and shared by every test in this package.
var testDB *storetest.DB

func TestMain(m *testing.M) {
	os.Exit(storetest.Run(m, func(db *storetest.DB) { testDB = db }))
}

const (
	insertUser  = `INSERT INTO users (email, password_hash, role) VALUES ($1, 'hash', $2)`
	insertEvent = `INSERT INTO inbound_events (source, external_id, payload)
		VALUES ('acme', $1, '{}') ON CONFLICT (source, external_id) DO NOTHING`
)

func countRows(t *testing.T, pool *pgxpool.Pool, table string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func TestMigrationsCreateCoreTables(t *testing.T) {
	pool := testDB.Pool(t)
	for _, table := range []string{"users", "refresh_tokens", "idempotency_keys", "inbound_events"} {
		var exists bool
		err := pool.QueryRow(context.Background(),
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables
				WHERE table_schema = 'public' AND table_name = $1)`, table).Scan(&exists)
		if err != nil {
			t.Fatalf("check %s: %v", table, err)
		}
		if !exists {
			t.Errorf("table %s does not exist after migrations", table)
		}
	}
}

func TestUserEmailIsUniqueIgnoringCase(t *testing.T) {
	pool := testDB.Pool(t)
	ctx := context.Background()

	if _, err := pool.Exec(ctx, insertUser, "Alice@Example.com", "member"); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	_, err := pool.Exec(ctx, insertUser, "alice@example.COM", "member")

	constraint, ok := store.UniqueViolation(err)
	if !ok || constraint != "users_email_lower_key" {
		t.Fatalf("UniqueViolation(%v) = (%q, %v), want users_email_lower_key", err, constraint, ok)
	}
}

func TestUserRoleIsConstrained(t *testing.T) {
	pool := testDB.Pool(t)

	_, err := pool.Exec(context.Background(), insertUser, "bob@example.com", "superuser")

	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Fatalf("err = %v, want a check violation (23514)", err)
	}
}

func TestRefreshTokensCascadeWithUser(t *testing.T) {
	pool := testDB.Pool(t)
	ctx := context.Background()

	var userID string
	err := pool.QueryRow(ctx, insertUser+` RETURNING id::text`, "carol@example.com", "member").Scan(&userID)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO refresh_tokens (user_id, family_id, token_hash, expires_at)
		VALUES ($1, gen_random_uuid(), '\x01'::bytea, now() + interval '1 hour')`, userID)
	if err != nil {
		t.Fatalf("insert token: %v", err)
	}
	if got := countRows(t, pool, "refresh_tokens"); got != 1 {
		t.Fatalf("tokens = %d, want 1", got)
	}

	if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	if got := countRows(t, pool, "refresh_tokens"); got != 0 {
		t.Errorf("tokens after user delete = %d, want 0", got)
	}
}

func TestRefreshTokenRequiresExistingUser(t *testing.T) {
	pool := testDB.Pool(t)

	_, err := pool.Exec(context.Background(), `
		INSERT INTO refresh_tokens (user_id, family_id, token_hash, expires_at)
		VALUES (gen_random_uuid(), gen_random_uuid(), '\x02'::bytea, now() + interval '1 hour')`)

	if _, ok := store.ForeignKeyViolation(err); !ok {
		t.Fatalf("err = %v, want a foreign key violation", err)
	}
}

func TestInboundEventsDeduplicate(t *testing.T) {
	pool := testDB.Pool(t)
	ctx := context.Background()

	first, err := pool.Exec(ctx, insertEvent, "evt-1")
	if err != nil {
		t.Fatalf("first insert: %v", err)
	}
	second, err := pool.Exec(ctx, insertEvent, "evt-1")
	if err != nil {
		t.Fatalf("second insert: %v", err)
	}
	if first.RowsAffected() != 1 || second.RowsAffected() != 0 {
		t.Errorf("rows affected = %d then %d, want 1 then 0", first.RowsAffected(), second.RowsAffected())
	}
}

func TestInTxCommitsOnSuccess(t *testing.T) {
	pool := testDB.Pool(t)
	ctx := context.Background()

	err := store.InTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, insertEvent, "evt-commit")
		return err
	})
	if err != nil {
		t.Fatalf("InTx: %v", err)
	}
	if got := countRows(t, pool, "inbound_events"); got != 1 {
		t.Errorf("rows = %d, want 1", got)
	}
}

func TestInTxRollsBackOnError(t *testing.T) {
	pool := testDB.Pool(t)
	ctx := context.Background()
	errBoom := errors.New("boom")

	err := store.InTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, insertEvent, "evt-rollback"); err != nil {
			return err
		}
		return errBoom
	})
	if !errors.Is(err, errBoom) {
		t.Fatalf("InTx err = %v, want boom", err)
	}
	if got := countRows(t, pool, "inbound_events"); got != 0 {
		t.Errorf("rows = %d, want 0 after rollback", got)
	}
}

func TestInTxOptionsAppliesIsolation(t *testing.T) {
	pool := testDB.Pool(t)

	var level string
	err := store.InTxOptions(context.Background(), pool, pgx.TxOptions{IsoLevel: pgx.Serializable},
		func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SHOW transaction_isolation`).Scan(&level)
		})
	if err != nil {
		t.Fatalf("InTxOptions: %v", err)
	}
	if level != "serializable" {
		t.Errorf("isolation = %q, want serializable", level)
	}
}

func TestInTenantTxEnforcesRLSAndClearsOnCommit(t *testing.T) {
	pool := testDB.Pool(t)
	ctx := context.Background()

	_, err := pool.Exec(ctx, `
		CREATE TABLE rls_probe (
			id        uuid PRIMARY KEY DEFAULT gen_random_uuid(),
			tenant_id uuid NOT NULL,
			label     text NOT NULL
		);
		ALTER TABLE rls_probe ENABLE ROW LEVEL SECURITY;
		ALTER TABLE rls_probe FORCE ROW LEVEL SECURITY;
		CREATE POLICY rls_probe_tenant_isolation ON rls_probe
			USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
			WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
		DO $$
		BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'rls_probe_app') THEN
				CREATE ROLE rls_probe_app NOSUPERUSER NOBYPASSRLS;
			END IF;
		END $$;
		GRANT ALL ON TABLE rls_probe TO rls_probe_app;
	`)
	if err != nil {
		t.Fatalf("setup RLS table: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DROP TABLE IF EXISTS rls_probe`)
	})

	tenantA := uuid.New()
	tenantB := uuid.New()

	// Insert one row for tenantA and one row for tenantB under the non-superuser role.
	for _, tc := range []struct {
		tenant uuid.UUID
		label  string
	}{
		{tenantA, "alpha"},
		{tenantB, "bravo"},
	} {
		err := store.InTenantTx(ctx, pool, tc.tenant, func(ctx context.Context, tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `SET LOCAL ROLE rls_probe_app`); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `INSERT INTO rls_probe (tenant_id, label) VALUES ($1, $2)`, tc.tenant, tc.label)
			return err
		})
		if err != nil {
			t.Fatalf("insert for %s: %v", tc.label, err)
		}
	}

	// Tenant A must see only its own row even with an unqualified SELECT.
	var labels []string
	err = store.InTenantTxOptions(ctx, pool, tenantA, pgx.TxOptions{IsoLevel: pgx.RepeatableRead}, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SET LOCAL ROLE rls_probe_app`); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT label FROM rls_probe`)
		if err != nil {
			return err
		}
		labels, err = pgx.CollectRows(rows, pgx.RowTo[string])
		return err
	})
	if err != nil {
		t.Fatalf("query for tenantA: %v", err)
	}
	if len(labels) != 1 || labels[0] != "alpha" {
		t.Fatalf("tenantA visible labels = %v, want [alpha]", labels)
	}

	// Cross-tenant INSERT (tenantA writing a row tagged with tenantB) must be blocked by RLS (42501).
	err = store.InTenantTx(ctx, pool, tenantA, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SET LOCAL ROLE rls_probe_app`); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO rls_probe (tenant_id, label) VALUES ($1, 'spoof')`, tenantB)
		return err
	})
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
		t.Fatalf("cross-tenant insert err = %v, want SQLSTATE 42501 (insufficient_privilege)", err)
	}

	// After InTenantTx completes, app.tenant_id must not leak onto the pooled connection.
	var leaked string
	if err := pool.QueryRow(ctx, `SELECT COALESCE(current_setting('app.tenant_id', true), '')`).Scan(&leaked); err != nil {
		t.Fatalf("check leaked setting: %v", err)
	}
	if leaked != "" {
		t.Errorf("app.tenant_id after commit = %q, want empty", leaked)
	}
}

func TestTablesAreTruncatedBetweenTests(t *testing.T) {
	t.Run("writes a row", func(t *testing.T) {
		pool := testDB.Pool(t)
		if _, err := pool.Exec(context.Background(), insertEvent, "evt-left-behind"); err != nil {
			t.Fatalf("insert: %v", err)
		}
	})
	t.Run("sees an empty table", func(t *testing.T) {
		pool := testDB.Pool(t)
		if got := countRows(t, pool, "inbound_events"); got != 0 {
			t.Errorf("rows = %d, want 0: the previous test's cleanup did not run", got)
		}
	})
}

func TestPingSucceedsAgainstLiveDatabase(t *testing.T) {
	pool := testDB.Pool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := store.Pinger(pool)(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}
