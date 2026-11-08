package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// Respell applies f to the prose of every stored revision, the history's
// as well as the current: summary, each statement's name, text, area and
// diagram labels, and a page's maps. f is a spelling rule the write path
// applies too (lang.Terms), so text stored before it reads as text
// written after it, and the history compares like with like: respelling
// the current revision alone would show every statement changed against
// the one before. No model call, no new revision, provenance untouched.
// f must be idempotent; a revision it leaves as it is is not written.
// Returns the number of revisions rewritten. Run by migrate.
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
		changed, err := s.respellRevision(ctx, id, f)
		if err != nil {
			return n, fmt.Errorf("revision %d: %w", id, err)
		}
		if changed {
			n++
		}
	}
	return n, nil
}

// respellRevision is Respell on one revision, in a transaction of its own.
func (s *Store) respellRevision(ctx context.Context, id int64, f func(string) string) (bool, error) {
	changed := false
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var summary, basis string
		var statements, drawn, repoDrawn []byte
		if err := tx.QueryRowContext(ctx, `SELECT summary, statements, map, repo_map, COALESCE(repo_map_basis, '') FROM revisions WHERE id = ?`, id).
			Scan(&summary, &statements, &drawn, &repoDrawn, &basis); err != nil {
			return err
		}
		var st []Statement
		if err := json.Unmarshal(statements, &st); err != nil {
			return err
		}
		// A repository map drawn from this very text stays drawn from it
		// once respelled: its basis is a hash of the text, and a stale one
		// has the next run draw the map again, a model call for spelling.
		mapCurrent := basis != "" && basis == (Page{Summary: summary, Statements: st}).MapBasis()
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
			return err
		}
		rm, err := diagramOf(repoDrawn)
		if err != nil {
			return err
		}
		if m.Relabel(f) {
			changed = true
		}
		if rm.Relabel(f) {
			changed = true
		}
		if !changed {
			return nil
		}
		body, err := jsonOf(orEmptyStatements(st))
		if err != nil {
			return err
		}
		// A column stays as stored where it held nothing: NULL stays NULL.
		mapCol, repoMapCol := any(nil), any(nil)
		if m != nil {
			if mapCol, err = jsonOf(m); err != nil {
				return err
			}
		} else {
			mapCol = jsonOrNull(drawn)
		}
		if rm != nil {
			if repoMapCol, err = jsonOf(rm); err != nil {
				return err
			}
		} else {
			repoMapCol = jsonOrNull(repoDrawn)
		}
		if mapCurrent {
			basis = (Page{Summary: summary, Statements: st}).MapBasis()
		}
		_, err = tx.ExecContext(ctx, `UPDATE revisions SET summary = ?, statements = ?, map = ?, repo_map = ?, repo_map_basis = NULLIF(?, '') WHERE id = ?`,
			summary, body, mapCol, repoMapCol, basis, id)
		return err
	})
	return changed, err
}
