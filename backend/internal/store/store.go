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
	"os"
	"path/filepath"

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

// Optimize runs PRAGMA optimize on every pooled connection: SQLite's
// refresh of the planner's statistics from what each connection queried
// (the pragma reads a connection's own history), kept in the file for
// the next run. serve's shutdown runs it, nothing else: it may ANALYZE
// under the write lock, which a backup or a migrate beside a running
// serve must not take. A failure is a log line.
func (s *Store) Optimize(ctx context.Context) {
	// Every connection held at once, so each is a different one.
	conns := make([]*sql.Conn, 0, maxConns)
	defer func() {
		for _, c := range conns {
			_ = c.Close()
		}
	}()
	for i := 0; i < maxConns; i++ {
		c, err := s.db.Conn(ctx)
		if err != nil {
			s.log.Warn("SQLite optimize: connection", "err", err.Error())
			return
		}
		conns = append(conns, c)
		if _, err := c.ExecContext(ctx, `PRAGMA optimize`); err != nil {
			s.log.Warn("SQLite optimize failed", "err", err.Error())
			return
		}
	}
}

// Backup writes a consistent copy of the database to dest with VACUUM
// INTO: one transaction's view of every table, compacted, no -wal or
// -shm beside it, taken while serve runs on (a reader, not a lock on
// writers). Copying the file and its WAL by hand under live writes is
// not consistent, which is why this exists. The copy lands in a file of
// its own name beside dest (CreateTemp: two backups at once never share
// one), is synced (VACUUM INTO never fsyncs its output) and linked to
// dest, which fails when dest exists meanwhile: no half copy and no
// overwrite under the name, the folder synced after. dest must not be
// the database or one of its sidecars. Returns the copy's size.
func (s *Store) Backup(ctx context.Context, dest string) (int64, error) {
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		if sameFile(dest, s.path+suffix) {
			return 0, fmt.Errorf("backup %s: is the database itself", dest)
		}
	}
	if _, err := os.Stat(dest); err == nil {
		return 0, fmt.Errorf("backup %s: exists", dest)
	}
	// A folder that does not exist, or takes no file, fails here.
	part, err := os.CreateTemp(filepath.Dir(dest), filepath.Base(dest)+".*.part")
	if err != nil {
		return 0, fmt.Errorf("backup %s: %w", dest, err)
	}
	tmp := part.Name()
	_ = part.Close()
	size, err := s.backupInto(ctx, tmp, dest)
	if err != nil {
		_ = os.Remove(tmp)
		return 0, fmt.Errorf("backup %s: %w", dest, err)
	}
	return size, nil
}

// backupInto fills tmp (an empty file, which VACUUM INTO accepts), syncs
// it, links it to dest and drops the temporary name; the caller removes
// tmp on failure.
func (s *Store) backupInto(ctx context.Context, tmp, dest string) (int64, error) {
	if _, err := s.db.ExecContext(ctx, `VACUUM INTO ?`, tmp); err != nil {
		return 0, err
	}
	if err := syncFile(tmp); err != nil {
		return 0, err
	}
	if err := os.Link(tmp, dest); err != nil {
		return 0, err
	}
	_ = os.Remove(tmp)
	if err := syncFile(filepath.Dir(dest)); err != nil {
		return 0, err
	}
	info, err := os.Stat(dest)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// syncFile fsyncs a file or a folder: the bytes, or the names in it.
func syncFile(path string) error {
	f, err := os.Open(path) //nolint:gosec // a path this package built
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return f.Sync()
}

// sameFile reports whether a and b name the same file: by the file system
// when both exist (a path given two ways, a link), else by their absolute
// cleaned paths, as b does not exist yet.
func sameFile(a, b string) bool {
	ia, errA := os.Stat(a)
	ib, errB := os.Stat(b)
	if errA == nil && errB == nil {
		return os.SameFile(ia, ib)
	}
	absA, _ := filepath.Abs(a)
	absB, _ := filepath.Abs(b)
	return absA == absB
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
