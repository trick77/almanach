package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"unicode/utf8"
)

// Respell applies f to the prose of every stored revision, the history's
// as well as the current: summary, each statement's name, text, area and
// diagram labels, and a page's maps. f is a spelling rule the write path
// applies too (lang.Terms), so text stored before it reads as text
// written after it, and the history compares like with like: respelling
// the current revision alone would show every statement changed against
// the one before. No model call, no new revision, provenance untouched.
// f must be idempotent. Each revision is read without a lock; only one f
// changes takes the write lock, reads it again inside and writes it, so a
// migrate with nothing left to do never blocks a running serve. Returns
// the number of revisions rewritten. Run by migrate.
func (s *Store) Respell(ctx context.Context, f func(string) string) (int, error) {
	var ids []int64
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM revisions ORDER BY id`)
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	n := 0
	for _, id := range ids {
		r, err := respelled(ctx, s.db, id, f)
		if err != nil {
			return n, fmt.Errorf("revision %d: %w", id, err)
		}
		if r == nil {
			continue
		}
		err = s.inTx(ctx, func(tx *sql.Tx) error {
			// Read again under the lock: the map step sets a repository map
			// on the current revision in place.
			r, err := respelled(ctx, tx, id, f)
			if err != nil || r == nil {
				return err
			}
			return r.write(ctx, tx, id)
		})
		if err != nil {
			return n, fmt.Errorf("revision %d: %w", id, err)
		}
		n++
	}
	return n, nil
}

// respelling is one revision's columns as f leaves them.
type respelling struct {
	summary, statements string
	chars               int
	drawn, repoDrawn    any
	basis               string
}

// respelled is revision id as f leaves it, nil when f changes nothing.
func respelled(ctx context.Context, q rowQuerier, id int64, f func(string) string) (*respelling, error) {
	var summary, basis string
	var chars int
	var statements, drawn, repoDrawn []byte
	if err := q.QueryRowContext(ctx, `SELECT summary, statements, chars, map, repo_map, COALESCE(repo_map_basis, '') FROM revisions WHERE id = ?`, id).
		Scan(&summary, &statements, &chars, &drawn, &repoDrawn, &basis); err != nil {
		return nil, err
	}
	var st []Statement
	if err := json.Unmarshal(statements, &st); err != nil {
		return nil, err
	}
	// A repository map drawn from this very text stays drawn from it once
	// respelled: its basis is a hash of the text, and a stale one has the
	// next run draw the map again, a model call for spelling.
	mapCurrent := basis != "" && basis == (Page{Summary: summary, Statements: st}).MapBasis()
	before := proseChars(summary, st)
	changed := false
	set := func(p *string) {
		if v := f(*p); v != *p {
			*p, changed = v, true
		}
	}
	set(&summary)
	for i := range st {
		set(&st[i].Name)
		set(&st[i].Text)
		set(&st[i].Area)
		if st[i].Diagram.Relabel(f) {
			changed = true
		}
	}
	m, err := diagramOf(drawn)
	if err != nil {
		return nil, err
	}
	rm, err := diagramOf(repoDrawn)
	if err != nil {
		return nil, err
	}
	if m.Relabel(f) {
		changed = true
	}
	if rm.Relabel(f) {
		changed = true
	}
	if !changed {
		return nil, nil
	}
	r := &respelling{summary: summary, drawn: jsonOrNull(drawn), repoDrawn: jsonOrNull(repoDrawn), basis: basis}
	// The stored size moves by what f added or took: the writer counts the
	// summary and every statement's name and text (consolidate's prose).
	r.chars = chars + proseChars(summary, st) - before
	if r.statements, err = jsonOf(orEmptyStatements(st)); err != nil {
		return nil, err
	}
	if m != nil {
		if r.drawn, err = jsonOf(m); err != nil {
			return nil, err
		}
	}
	if rm != nil {
		if r.repoDrawn, err = jsonOf(rm); err != nil {
			return nil, err
		}
	}
	if mapCurrent {
		r.basis = (Page{Summary: summary, Statements: st}).MapBasis()
	}
	return r, nil
}

func (r *respelling) write(ctx context.Context, q execer, id int64) error {
	_, err := q.ExecContext(ctx, `UPDATE revisions SET summary = ?, statements = ?, chars = ?, map = ?, repo_map = ?, repo_map_basis = NULLIF(?, '') WHERE id = ?`,
		r.summary, r.statements, r.chars, r.drawn, r.repoDrawn, r.basis, id)
	return err
}

// proseChars is a revision's size as its writer stores it: the summary and
// every statement's name and text, in runes.
func proseChars(summary string, st []Statement) int {
	n := utf8.RuneCountInString(summary)
	for _, s := range st {
		n += utf8.RuneCountInString(s.Name) + utf8.RuneCountInString(s.Text)
	}
	return n
}
