//go:build integration

package storetest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"example.com/app/internal/platform/store"
)

const (
	defaultImage = "postgres:17-alpine"
	dbName       = "app"
	dbUser       = "app"
	dbPassword   = "app"
	cleanupLimit = 30 * time.Second
)

type options struct {
	image         string
	migrationsDir string
	extra         []func(ctx context.Context, pool *pgxpool.Pool) error
}

// Option customizes Start.
type Option func(*options)

// WithImage selects the Postgres image.
func WithImage(image string) Option {
	return func(o *options) { o.image = image }
}

// WithMigrationsDir overrides the goose directory. By default it is
// db/migrations at the module root, found by walking up from the test's
// working directory.
func WithMigrationsDir(dir string) Option {
	return func(o *options) { o.migrationsDir = dir }
}

// WithMigration runs fn after the goose migrations. Packages that depend on
// River use it to apply River's own migrations.
func WithMigration(fn func(ctx context.Context, pool *pgxpool.Pool) error) Option {
	return func(o *options) { o.extra = append(o.extra, fn) }
}

// DB is a migrated Postgres test database.
type DB struct {
	pool      *pgxpool.Pool
	url       string
	container *postgres.PostgresContainer
}

// Start launches the container and applies every migration.
func Start(ctx context.Context, opts ...Option) (*DB, error) {
	o := options{image: defaultImage}
	for _, opt := range opts {
		opt(&o)
	}
	if o.migrationsDir == "" {
		dir, err := findMigrationsDir()
		if err != nil {
			return nil, err
		}
		o.migrationsDir = dir
	}

	ctr, err := postgres.Run(ctx, o.image,
		postgres.WithDatabase(dbName),
		postgres.WithUsername(dbUser),
		postgres.WithPassword(dbPassword),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		return nil, fmt.Errorf("storetest: start postgres: %w", err)
	}

	db, err := prepare(ctx, ctr, o)
	if err != nil {
		_ = ctr.Terminate(ctx)
		return nil, err
	}
	return db, nil
}

func prepare(ctx context.Context, ctr *postgres.PostgresContainer, o options) (*DB, error) {
	url, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return nil, fmt.Errorf("storetest: connection string: %w", err)
	}
	if err := applyMigrations(ctx, url, o.migrationsDir); err != nil {
		return nil, err
	}
	pool, err := store.NewPool(ctx, store.PoolConfig{URL: url, MaxConns: 8})
	if err != nil {
		return nil, fmt.Errorf("storetest: %w", err)
	}
	for _, extra := range o.extra {
		if err := extra(ctx, pool); err != nil {
			pool.Close()
			return nil, fmt.Errorf("storetest: extra migration: %w", err)
		}
	}
	return &DB{pool: pool, url: url, container: ctr}, nil
}

func applyMigrations(ctx context.Context, url, dir string) error {
	sqlDB, err := sql.Open("pgx", url)
	if err != nil {
		return fmt.Errorf("storetest: open database: %w", err)
	}
	defer func() { _ = sqlDB.Close() }()

	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, os.DirFS(dir))
	if err != nil {
		return fmt.Errorf("storetest: goose provider for %s: %w", dir, err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("storetest: apply migrations: %w", err)
	}
	return nil
}

func findMigrationsDir() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("storetest: working directory: %w", err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return filepath.Join(dir, "db", "migrations"), nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("storetest: go.mod not found above the working directory")
		}
		dir = parent
	}
}

// URL returns the connection string of the test database.
func (d *DB) URL() string { return d.url }

// Terminate closes the pool and removes the container.
func (d *DB) Terminate(ctx context.Context) error {
	d.pool.Close()
	if err := d.container.Terminate(ctx); err != nil {
		return fmt.Errorf("storetest: terminate container: %w", err)
	}
	return nil
}

// Truncate empties every application table, including River's, but keeps the
// migration bookkeeping tables so the schema stays applied.
func (d *DB) Truncate(ctx context.Context) error {
	rows, err := d.pool.Query(ctx, `
		SELECT quote_ident(tablename)
		FROM pg_tables
		WHERE schemaname = 'public'
		  AND tablename NOT IN ('goose_db_version', 'river_migration')`)
	if err != nil {
		return fmt.Errorf("storetest: list tables: %w", err)
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return fmt.Errorf("storetest: read table names: %w", err)
	}
	if len(names) == 0 {
		return nil
	}
	_, err = d.pool.Exec(ctx, "TRUNCATE TABLE "+strings.Join(names, ", ")+" RESTART IDENTITY CASCADE")
	if err != nil {
		return fmt.Errorf("storetest: truncate: %w", err)
	}
	return nil
}

// Pool returns the shared pool and registers a cleanup that truncates every
// table when the test ends, so tests never see each other's rows.
func (d *DB) Pool(t testing.TB) *pgxpool.Pool {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), cleanupLimit)
		defer cancel()
		if err := d.Truncate(ctx); err != nil {
			t.Errorf("truncate after test: %v", err)
		}
	})
	return d.pool
}

// Run starts one database for the whole test package, passes it to set, runs
// the tests, and tears everything down. Call it from TestMain:
//
//	func TestMain(m *testing.M) {
//		os.Exit(storetest.Run(m, func(db *storetest.DB) { testDB = db }))
//	}
func Run(m *testing.M, set func(*DB), opts ...Option) int {
	ctx := context.Background()
	db, err := Start(ctx, opts...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "storetest: %v\n", err)
		return 1
	}
	set(db)

	code := m.Run()

	termCtx, cancel := context.WithTimeout(ctx, cleanupLimit)
	defer cancel()
	if err := db.Terminate(termCtx); err != nil {
		fmt.Fprintf(os.Stderr, "storetest: %v\n", err)
	}
	return code
}
