package baseline

import (
	"github.com/trick77/almanach/internal/gitrepo"
	"github.com/trick77/almanach/internal/lang"
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

// Sift keeps what a reconcile looks at of a diff: a change to a file of
// files, the tree's readable set at the new commit (Files, Schemas), so a
// file the run never reads (a test, an unnamed schema) is never read for
// its change either. Such a file is not gone: it is still in the tree, and
// a plan from before it was left out (one that read tests) would count its
// bytes as left and read the repository afresh.
func Sift(diff []gitrepo.Entry, files []File) Changes {
	readable := make(map[string]bool, len(files))
	for _, f := range files {
		readable[f.Path] = true
	}
	var c Changes
	for _, e := range diff {
		if Readable(e) {
			if readable[e.Path] {
				c.Changed = append(c.Changed, FileOf(e))
			}
			continue
		}
		c.Gone = append(c.Gone, e.Path)
	}
	return c
}

// Picked is the changes a reconcile of a routed plan reads: a changed file
// a subject holds, or one the router never saw, not readable at the
// documented commit (seen: that commit's readable paths), added since or
// become readable (shrunk under the cap, a link made a file). A routed
// plan holds the files its router picked, not every readable one, so a
// change to a file it passed over is no file to place (placed, it would
// slip back in what the router left out, a little more each reconcile) and
// no step's to read. It weighs toward a fresh read only against the tree
// (RoutedFullInstead), never against the plan. Of the gone paths only the
// held count: one it passed over is no step's.
func (c Changes) Picked(p Plan, seen map[string]bool) Changes {
	held := p.Held()
	var out Changes
	for _, f := range c.Changed {
		if held[f.Path] || !seen[f.Path] {
			out.Changed = append(out.Changed, f)
		}
	}
	for _, g := range c.Gone {
		if held[g] {
			out.Gone = append(out.Gone, g)
		}
	}
	return out
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
	return movedOf(p, c)*2 > catalogued
}

// movedOf is the changed bytes plus the catalogued bytes that left.
func movedOf(p Plan, c Changes) int64 {
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
	return moved
}

// RoutedFullInstead is FullInstead for a routed plan: the picked changes
// weigh against the plan, the ones the router passed over (all but picked:
// changed at their new size, gone at their size before, from before)
// against the readable tree they touched (files, plus what left it), and a
// fresh read when the two shares together pass a half. A file touched
// counts whole, as in FullInstead: the diff tells which files changed, not
// how much (a header added everywhere reads the repository afresh, once).
// The passed-over changes weigh against the tree, never the plan: a routed
// plan holds a minority of the tree, so against it a month's ordinary
// churn outside the picks would read the repository afresh at nearly every
// reconcile and the router would save nothing. Not weighed at all, a
// skipped file that grew important would wait for a button or an epoch.
func RoutedFullInstead(p Plan, all, picked Changes, before map[string]int64, files []File) bool {
	catalogued := p.Bytes()
	if catalogued == 0 {
		return true
	}
	held := picked.Paths()
	var skipped, tree int64
	for _, f := range all.Changed {
		if !held[f.Path] {
			skipped += f.Size
		}
	}
	for _, g := range all.Gone {
		if !held[g] {
			skipped += before[g]
			tree += before[g]
		}
	}
	for _, f := range files {
		tree += f.Size
	}
	moved := movedOf(p, picked)
	if tree == 0 {
		return moved*2 > catalogued
	}
	// moved/catalogued + skipped/tree > 1/2, in integers.
	return 2*(moved*tree+skipped*catalogued) > catalogued*tree
}

// Touched is the subjects a reconcile reads again, by name in plan order:
// those holding a changed or gone path, and those the last run skipped. By
// name, because the merged plan the steps read drops subjects whose files
// are all gone and appends new ones. Unplaced is the changed files no
// subject holds, for the catalogue step to place.
func Touched(p Plan, c Changes) (touched []string, unplaced []File) {
	changed := c.Paths()
	// A name stored before the word rule (lang.Terms) is the same subject
	// as its English one.
	skipped := map[string]bool{}
	for _, name := range p.Skipped {
		skipped[lang.Terms(name)] = true
	}
	held := map[string]bool{}
	for _, s := range p.Subjects {
		hit := skipped[lang.Terms(s.Name)]
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
