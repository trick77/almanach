package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/ncruces/go-sqlite3"
)

// How values are stored where SQLite has no type of its own: times, JSON
// and the constraint errors the store turns into its own.

// stampLayout is every stored time: UTC, RFC 3339 with all nine digits of
// the nanoseconds. Fixed width, so text order is time order, and the
// sub-second order of two merges or two run steps survives. Every time
// column's CHECK refuses any other shape.
//
// Written through stamp, not the driver's _timefmt: the driver formats a
// custom layout in the time's own zone (only its built-in layouts are
// turned to UTC first), so a local time.Time would land as its local clock
// with a literal Z, a wrong instant the CHECK cannot see. And an
// expression such as MAX(merged_at) has no declared type, so the driver
// would not decode it either; stampFormat reads every one.
const stampLayout = "2006-01-02T15:04:05.000000000Z"

// stampFormat reads a stored time in stampLayout: anything else is an
// error, never a zero time.
const stampFormat = sqlite3.TimeFormat(stampLayout)

// stamp is t as stored.
func stamp(t time.Time) string { return t.UTC().Format(stampLayout) }

// nowStamp is the current time as stored: Go is the only clock.
func nowStamp() string { return stamp(time.Now()) }

// stampOrNull is a nullable time as stored, nil as NULL.
func stampOrNull(t *time.Time) any {
	if t == nil {
		return nil
	}
	return stamp(*t)
}

// timeAt scans a NOT NULL time column into t.
func timeAt(t *time.Time) sql.Scanner { return stampFormat.Scanner(t) }

// timeOrNil scans a nullable time column into t: NULL is nil.
func timeOrNil(t **time.Time) sql.Scanner { return nullTime{t} }

type nullTime struct{ t **time.Time }

func (s nullTime) Scan(v any) error {
	if v == nil {
		*s.t = nil
		return nil
	}
	t, err := stampFormat.Decode(v)
	if err != nil {
		return err
	}
	*s.t = &t
	return nil
}

// ErrBusy is the error a write meets when the database's write lock stayed
// taken past the busy timeout: IsBusy reports it.
var ErrBusy error = sqlite3.BUSY

// IsBusy reports a write that waited out the busy timeout (or met a locked
// table): temporary, worth a hold and a retry, never a failure of the
// write's content.
func IsBusy(err error) bool {
	return errors.Is(err, sqlite3.BUSY) || errors.Is(err, sqlite3.LOCKED)
}

// jsonText is a JSON value as bound: text, never a blob, which json_valid
// and the JSON functions would not read as JSON.
func jsonText(b []byte) string { return string(b) }

// jsonOrNull is jsonText with an absent value (empty, or JSON null) as SQL
// NULL.
func jsonOrNull(b []byte) any {
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	return string(b)
}

// jsonOf marshals v for a JSON column.
func jsonOf(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}

// uniqueAsExists maps a duplicate key, primary or unique, to ErrExists.
func uniqueAsExists(err error) error {
	if errors.Is(err, sqlite3.CONSTRAINT_UNIQUE) || errors.Is(err, sqlite3.CONSTRAINT_PRIMARYKEY) {
		return ErrExists
	}
	return err
}

// isForeignKey reports a foreign key that names no row.
func isForeignKey(err error) bool { return errors.Is(err, sqlite3.CONSTRAINT_FOREIGNKEY) }

// inTx runs fn in one transaction, IMMEDIATE by the DSN: the write lock is
// taken at BEGIN, committed when fn returns nil, rolled back otherwise.
func (s *Store) inTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// affected is the row count of an UPDATE or DELETE.
func affected(res sql.Result, err error) (int64, error) {
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
