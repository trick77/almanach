package baseline

import (
	"github.com/trick77/almanach/internal/gitrepo"
	"github.com/trick77/almanach/internal/store"
)

// A reconcile reads what changed in the code since the documented commit,
// whatever the notes said about it: the changed paths map onto the
// catalogue's subjects, and onto the statements resting on them.

// Changes is a diff seen through the same filter as the tree: the readable
// files that were added or changed, and every path that left the readable
// set, deleted or no longer readable (grown past the cap, emptied, made a
// link). A statement may rest on either.
type Changes struct {
	Changed []File
	Gone    []string
}

// Sift keeps what a reconcile looks at of a diff.
func Sift(diff []gitrepo.Entry) Changes {
	var c Changes
	for _, e := range diff {
		if Readable(e) {
			c.Changed = append(c.Changed, File{Path: e.Path, Size: e.Size})
			continue
		}
		c.Gone = append(c.Gone, e.Path)
	}
	return c
}

// Paths is every path the changes name.
func (c Changes) Paths() map[string]bool {
	out := make(map[string]bool, len(c.Changed)+len(c.Gone))
	for _, f := range c.Changed {
		out[f.Path] = true
	}
	for _, p := range c.Gone {
		out[p] = true
	}
	return out
}

// FullInstead reports whether so much changed that reading the touched
// subjects one by one costs more than reading the repository afresh: the
// changed bytes plus the catalogued bytes that left, over half of what the
// plan catalogued; or a plan with nothing in it.
func FullInstead(p Plan, c Changes) bool {
	catalogued := p.Bytes()
	if catalogued == 0 {
		return true
	}
	var moved int64
	for _, f := range c.Changed {
		moved += f.Size
	}
	gone := map[string]bool{}
	for _, g := range c.Gone {
		gone[g] = true
	}
	for _, s := range p.Subjects {
		for _, f := range s.Files {
			if gone[f.Path] {
				moved += f.Size
			}
		}
	}
	return moved*2 > catalogued
}

// Touched is the subjects a reconcile reads again, by name in plan order:
// those holding a changed or gone path, and those the last run skipped. By
// name, because the merged plan the steps read drops subjects whose files
// are all gone and appends new ones. Unplaced is the changed files no
// subject holds, for the catalogue step to place.
func Touched(p Plan, c Changes) (touched []string, unplaced []File) {
	changed := c.Paths()
	skipped := map[string]bool{}
	for _, name := range p.Skipped {
		skipped[name] = true
	}
	held := map[string]bool{}
	for _, s := range p.Subjects {
		hit := skipped[s.Name]
		for _, f := range s.Files {
			held[f.Path] = true
			hit = hit || changed[f.Path]
		}
		if hit {
			touched = append(touched, s.Name)
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
