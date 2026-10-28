// Package store is the Postgres layer: pool, embedded migrations and every
// query the service runs. One function per query, positional pgx args, and
// a transaction wherever more than one statement must land together.
package store

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/trick77/ship/kit/migrate"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// migrateLockID is the advisory-lock key held for the whole of Migrate.
// Fixed: pg_advisory_lock namespaces by value alone. From "alma".
const migrateLockID int64 = 0x616C6D61

func migrations() fs.FS {
	sub, err := fs.Sub(migrationFS, "migrations")
	if err != nil {
		panic(err) // unreachable: the embed pattern guarantees the directory
	}
	return sub
}

// Store owns the pool.
type Store struct {
	pool *pgxpool.Pool
	log  *slog.Logger
}

// Open connects with a pool of 2..10 and logs the server version.
func Open(ctx context.Context, dsn string, log *slog.Logger) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	cfg.MinConns = 2
	cfg.MaxConns = 10
	return openConfig(ctx, cfg, log)
}

func openConfig(ctx context.Context, cfg *pgxpool.Config, log *slog.Logger) (*Store, error) {
	cfg.ConnConfig.RuntimeParams["timezone"] = "UTC"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	var version string
	if err := pool.QueryRow(ctx, `SELECT version()`).Scan(&version); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect: %w", err)
	}
	log.Info("PostgreSQL connection pool created (" + version + ")")
	return &Store{pool: pool, log: log}, nil
}

// Close releases the pool.
func (s *Store) Close() {
	s.pool.Close()
	s.log.Info("PostgreSQL connection pool closed")
}

// Migrate applies every pending migration in file order, exactly once, each
// in its own transaction, serialised by an advisory lock. Idempotent.
func (s *Store) Migrate(ctx context.Context) error {
	db := stdlib.OpenDBFromPool(s.pool)
	defer func() { _ = db.Close() }()
	_, err := migrate.Up(ctx, db, migrations(), s.migrateOptions())
	return err
}

func (s *Store) migrateOptions() migrate.Options {
	return migrate.Options{Dialect: migrate.Postgres(migrateLockID), Log: s.log}
}

// Ping is the readiness check.
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

// SchemaCurrent reports whether every embedded migration has been applied,
// so a deploy that skipped the migrate init container fails at boot with a
// name, not at the first query.
func (s *Store) SchemaCurrent(ctx context.Context) error {
	db := stdlib.OpenDBFromPool(s.pool)
	defer func() { _ = db.Close() }()
	pending, err := migrate.Pending(ctx, db, migrations(), s.migrateOptions())
	if err != nil {
		return err
	}
	if len(pending) > 0 {
		return fmt.Errorf("database schema is behind, pending %v: run `almanach migrate` first", pending)
	}
	return nil
}
