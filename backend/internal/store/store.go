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
	"strings"

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

// OpenExisting is Open of a database that is already there and holds
// Almanach's schema, one migration applied at least (not SchemaCurrent: a
// backup before a migrate must work). A path that names no file is
// refused by SQLite itself (mode=rw), never created; an empty or foreign
// database, as a mistyped path once made, is refused too: a backup of it
// would read as success.
func OpenExisting(ctx context.Context, path string, log *slog.Logger) (*Store, error) {
	s, err := open(ctx, path, log, "mode=rw")
	if err != nil {
		return nil, err
	}
	var applied int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM `+migrate.Table).Scan(&applied); err != nil || applied == 0 {
		s.Close()
		return nil, fmt.Errorf("open %s: not an Almanach database, no migration applied", path)
	}
	return s, nil
}

// Optimize runs PRAGMA optimize on each idle connection of the pool:
// SQLite's refresh of the planner's statistics from what that
// connection queried (the pragma reads a connection's own history, so a
// fresh one would learn nothing), kept in the file for the next run.
// serve's shutdown runs it, nothing else: it may ANALYZE under the write
// lock, which a backup or a migrate beside a running serve must not
// take. A failure is one log line; it reports whether there was none.
func (s *Store) Optimize(ctx context.Context) bool {
	// The idle ones held at once, so each is a different one; the pool has
	// no lifetime and idles as many as it opens. One still in use (a
	// handler past the drain) is left alone: waiting for it would spend
	// the grace on nothing.
	n := s.db.Stats().Idle
	conns := make([]*sql.Conn, 0, n)
	defer func() {
		for _, c := range conns {
			_ = c.Close()
		}
	}()
	failed := 0
	var last error
	for i := 0; i < n; i++ {
		c, err := s.db.Conn(ctx)
		if err != nil {
			// ctx done or the pool closed: the rest would fail the same.
			failed, last = failed+n-i, err
			break
		}
		conns = append(conns, c)
		if _, err := c.ExecContext(ctx, `PRAGMA optimize`); err != nil {
			failed, last = failed+1, err
		}
	}
	if last != nil {
		s.log.Warn("SQLite optimize failed", "connections", failed, "of", n, "err", last.Error())
		return false
	}
	return true
}

// Backup writes a consistent copy of the database to dest with VACUUM
// INTO: one transaction's view of every table, compacted, no -wal or
// -shm beside it, taken while serve runs on (a reader, not a lock on
// writers). Copying the file and its WAL by hand under live writes is
// not consistent, which is why this exists. dest is taken first, empty,
// created exclusively (two backups to one name: the second is refused
// at once); the copy is written to a part file of its own name beside
// it, synced (VACUUM INTO never fsyncs its output) and renamed over the
// placeholder, the folder synced after. A failure or a cancelled ctx
// removes both. A kill or crash mid-copy leaves the empty placeholder,
// which the next run refuses as existing, and the part file beside it:
// visible, never a half database under the name. dest must not be the
// database or one of its sidecars. Returns the copy's size.
func (s *Store) Backup(ctx context.Context, dest string) (int64, error) {
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		if sameFile(dest, s.path+suffix) {
			return 0, fmt.Errorf("backup %s: is the database itself", dest)
		}
	}
	// A folder that does not exist, or takes no file, fails here too.
	place, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // the operator's target
	if err != nil {
		return 0, fmt.Errorf("backup %s: %w", dest, err)
	}
	_ = place.Close()
	part, err := os.CreateTemp(filepath.Dir(dest), filepath.Base(dest)+".*.part")
	if err != nil {
		_ = os.Remove(dest)
		return 0, fmt.Errorf("backup %s: %w", dest, err)
	}
	tmp := part.Name()
	_ = part.Close()
	size, err := s.backupInto(ctx, tmp, dest)
	if err != nil {
		_ = os.Remove(tmp)
		_ = os.Remove(dest)
		return 0, fmt.Errorf("backup %s: %w", dest, err)
	}
	return size, nil
}

// backupInto fills tmp (an empty file, which VACUUM INTO accepts), syncs
// it and renames it over dest, the placeholder of the caller's; a
// failure before the rename leaves both for the caller to remove. Once
// renamed the copy is whole: a folder sync that fails is a warning, not
// a failed backup.
func (s *Store) backupInto(ctx context.Context, tmp, dest string) (int64, error) {
	if _, err := s.db.ExecContext(ctx, `VACUUM INTO ?`, tmp); err != nil {
		return 0, err
	}
	if err := syncFile(tmp); err != nil {
		return 0, err
	}
	info, err := os.Stat(tmp)
	if err != nil {
		return 0, err
	}
	if err := os.Rename(tmp, dest); err != nil {
		return 0, err
	}
	if err := syncFile(filepath.Dir(dest)); err != nil {
		s.log.Warn("backup written, the folder's sync failed", "file", dest, "err", err.Error())
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
// when both exist (a path given two ways, a link), else by their folders
// resolved (a symlinked folder) and their cleaned names, as b need not
// exist yet. The names compare in any case: on a case-insensitive file
// system (APFS, NTFS) another spelling of a sidecar is the sidecar, and
// on any other refusing that name costs nothing.
func sameFile(a, b string) bool {
	ia, errA := os.Stat(a)
	ib, errB := os.Stat(b)
	if errA == nil && errB == nil {
		return os.SameFile(ia, ib)
	}
	return strings.EqualFold(resolved(a), resolved(b))
}

// resolved is a path with its folder's symlinks followed and the whole
// made absolute and clean; a folder that does not resolve stays as given.
func resolved(path string) string {
	dir, base := filepath.Split(path)
	if dir == "" {
		dir = "."
	}
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	abs, err := filepath.Abs(filepath.Join(dir, base))
	if err != nil {
		return filepath.Clean(path)
	}
	return abs
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
