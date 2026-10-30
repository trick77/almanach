package baseline

import (
	"github.com/trick77/almanach/internal/gitrepo"
	"github.com/trick77/almanach/internal/store"
)

// A reconcile reads what changed in the code since the documented commit,
// whatever the notes said about it: the changed paths map onto the
// catalogue's subjects, and onto the statements resting on them.

// Changes is a diff seen through the same filter as the tree: the readable
// files that were added or changed, and every path that was deleted (a
// statement may rest on any of them).
type Changes struct {
	Changed []File
	Deleted []string
}

// Sift keeps what a reconcile looks at of a diff.
func Sift(diff []gitrepo.Entry) Changes {
	var c Changes
	for _, e := range diff {
		switch {
		case e.Deleted:
			c.Deleted = append(c.Deleted, e.Path)
		case Readable(e):
			c.Changed = append(c.Changed, File{Path: e.Path, Size: e.Size})
		}
	}
	return c
}

// Paths is every path the changes name.
func (c Changes) Paths() map[string]bool {
	out := make(map[string]bool, len(c.Changed)+len(c.Deleted))
	for _, f := range c.Changed {
		out[f.Path] = true
	}
	for _, p := range c.Deleted {
		out[p] = true
	}
	return out
}

// Bytes is what the changed files hold.
func (c Changes) Bytes() int64 {
	var n int64
	for _, f := range c.Changed {
		n += f.Size
	}
	return n
}

// FullInstead reports whether so much changed that reading the touched
// subjects one by one costs more than reading the repository afresh: the
// changed bytes over half of what the plan catalogued, or a plan with
// nothing in it.
func FullInstead(p Plan, c Changes) bool {
	catalogued := p.Bytes()
	return catalogued == 0 || c.Bytes()*2 > catalogued
}

// Touched is the subjects a reconcile reads again, in plan order: those
// holding a changed or deleted path, and those the last run skipped. Unplaced
// is the changed files no subject holds, for the catalogue step to place.
func Touched(p Plan, c Changes) (touched []int, unplaced []File) {
	changed := c.Paths()
	skipped := map[string]bool{}
	for _, name := range p.Skipped {
		skipped[name] = true
	}
	held := map[string]bool{}
	for i, s := range p.Subjects {
		hit := skipped[s.Name]
		for _, f := range s.Files {
			held[f.Path] = true
			hit = hit || changed[f.Path]
		}
		if hit {
			touched = append(touched, i)
		}
	}
	for _, f := range c.Changed {
		if !held[f.Path] {
			unplaced = append(unplaced, f)
		}
	}
	return touched, unplaced
}

// Resting is the ids of the statements resting on any of the paths, in page
// order: what a reconcile's step must check against the new code.
func Resting(statements []store.Statement, paths map[string]bool) []string {
	var ids []string
	for _, s := range statements {
		for _, f := range s.Files {
			if paths[f] {
				ids = append(ids, s.ID)
				break
			}
		}
	}
	return ids
}
