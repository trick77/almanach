// Package store is the SQLite layer: one database file, embedded migrations
// and every query the service runs. One function per query, positional ?
// args, and a transaction wherever more than one statement must land
// together.
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"

	// Registers the "sqlite3" database/sql driver: SQLite as WASM, no cgo.
	_ "github.com/ncruces/go-sqlite3/driver"
	"github.com/trick77/ship/kit/migrate"
	"github.com/trick77/ship/kit/sqlitex"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

func migrations() fs.FS {
	sub, err := fs.Sub(migrationFS, "migrations")
	if err != nil {
		panic(err) // unreachable: the embed pattern guarantees the directory
	}
	return sub
}

// Store owns the database handle.
type Store struct {
	db   *sql.DB
	path string
	log  *slog.Logger
}

// maxConns caps the pool: each connection is a WASM instance of SQLite with
// its own memory and page cache, and there is one writer at a time anyway.
// Four leave readers (UI, API) room beside the worker's write.
const maxConns = 4

// Open opens (creating if needed) the database file at path and logs the
// SQLite version. Callers run Migrate separately; serve never does.
//
// The DSN is kit's: a 10 s busy timeout, WAL (readers never wait for the
// writer, the writer never waits for readers) and foreign keys on. Every
// writing transaction begins IMMEDIATE. In WAL a deferred transaction that
// reads before its first write holds a snapshot, and once a connection is
// in a read transaction SQLite no longer runs the busy handler for the
// write lock: another connection's commit in between fails the write at
// once with "database is locked". Taking the write lock at BEGIN puts
// every read of the transaction behind it, and the other writer is the one
// that waits, up to the busy timeout. One that runs out is an error like
// any failed write: the webhook answers 500. A single statement outside a
// transaction takes the lock it needs itself, with the same wait.
func Open(ctx context.Context, path string, log *slog.Logger) (*Store, error) {
	return open(ctx, path, log)
}

// open is Open with further DSN parameters after kit's, which a test uses
// to shorten the busy timeout.
func open(ctx context.Context, path string, log *slog.Logger, extra ...string) (*Store, error) {
	db, err := sql.Open("sqlite3", sqlitex.DSN(path, append([]string{"_txlock=immediate"}, extra...)...))
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	db.SetMaxOpenConns(maxConns)
	db.SetMaxIdleConns(maxConns)
	var version string
	if err := db.QueryRowContext(ctx, `SELECT sqlite_version()`).Scan(&version); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	log.Info("SQLite database opened (" + version + ")")
	return &Store{db: db, path: path, log: log}, nil
}

// Close releases the database.
func (s *Store) Close() {
	_ = s.db.Close()
	s.log.Info("SQLite database closed")
}

// Migrate applies every pending migration in file order, exactly once, each
// in its own transaction. Idempotent.
//
// The whole run holds a lock on a file beside the database
// (<path>-migrate.lock), so a second migrator waits for the first to end.
// kit's migrate reads the applied list before its first write lock: two
// runs at once would both see a file pending and the second fail on
// "table already exists". The write lock cannot serve: the run's own
// transactions need it, on the connection that would hold it.
func (s *Store) Migrate(ctx context.Context) error {
	unlock, err := lockFile(ctx, s.path+"-migrate.lock")
	if err != nil {
		return fmt.Errorf("migration lock: %w", err)
	}
	defer unlock()
	_, err = migrate.Up(ctx, s.db, migrations(), s.migrateOptions())
	return err
}

func (s *Store) migrateOptions() migrate.Options {
	return migrate.Options{Dialect: migrate.SQLite, Log: s.log}
}

// Ping is the readiness check: a read of a real table, so a volume gone or
// a file no longer a database turns readiness red.
func (s *Store) Ping(ctx context.Context) error {
	var n int
	return s.db.QueryRowContext(ctx, `SELECT count(*) FROM `+migrate.Table).Scan(&n)
}

// SchemaCurrent reports whether every embedded migration has been applied,
// so a deploy that skipped the migrate step fails at boot with a name, not
// at the first query.
func (s *Store) SchemaCurrent(ctx context.Context) error {
	pending, err := migrate.Pending(ctx, s.db, migrations(), s.migrateOptions())
	if err != nil {
		return err
	}
	if len(pending) > 0 {
		return fmt.Errorf("database schema is behind, pending %v: run `almanach migrate` first", pending)
	}
	return nil
}
