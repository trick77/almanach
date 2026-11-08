package baseline

import (
	"fmt"
	"path"
	"slices"
	"strings"
)

// RouterMaxBytes is the router's listing ceiling, from the context: with
// the prompt's instructions and the system message, at 3 bytes a token (a
// listing measured 3.6: 293 kB, 82k tokens, 2026-11-08), it leaves
// inference.RouteReserve inside inference.RouteContext
// (TestRouterListingFitsTheContext); a prompt past it anyway is refused
// before the call (consolidate's routeFits). About 70k tokens a call, a
// full run's one router call, near twice the 40k it cost at first.
//
// 150 kB at first, the names half of it: the dry runs (user, 2026-11-08)
// found a thousand-file service's names alone at 149 kB, its directories
// folded, and half the files the router missed seen only in a folded line.
// A larger tree still folds, its lowest-scored directories first; the
// skeletons of a large service (774 kB measured) never fit whole.
const RouterMaxBytes = 250_000

// routerNamesThirds is the share of the ceiling, in thirds, the file names
// may take before directories fold (166 kB of 250): the rest is skeletons.
const routerNamesThirds = 2

// routerScore is how far up a file's skeleton goes in the router's
// listing when not all fit: an anchor first, then by use and change. A
// hint, never a cut: every file is still named.
func routerScore(p string, z StageZero) int {
	s := z.FanIn[p]*10 + z.Churn[p]*2
	if AnchorRank(p) > 0 {
		s += 1_000
	}
	return s
}

// routerItem is one line of the router's listing: a file, or a folded
// directory standing for files the router picks only by the directory's
// path ("dir/*" its own files, "dir/" everything under it).
type routerItem struct {
	key   string // a file's path, or the path the router picks it by
	first string // the first path it stands for, for the listing's order
	files []File
	best  int
	bytes int64
	line  string
	file  bool
}

// RouterListing is what the router reads of a tree: every file by path,
// size and hints, and as many skeletons as fit ceiling, the
// highest-scored first. When the names alone pass two thirds of the ceiling, the
// lowest-scored directories fold into one line each, their own files as
// "dir/*"; when that is not enough, whole subtrees fold as "dir/", the
// deepest first. Sizes in kB of 1024 bytes, as the budgets are given; a
// file read only as its head says so. In path order, so the listing reads
// as the tree does. Folded is the files the listing names only by a folded
// line: a dry run tells a pick the router passed over from one it never saw
// by name.
func RouterListing(files []File, z StageZero, ceiling int) (listing string, folded map[string]bool) {
	items := make([]*routerItem, 0, len(files))
	for _, f := range files {
		items = append(items, &routerItem{key: f.Path, first: f.Path, files: []File{f}, best: routerScore(f.Path, z), bytes: f.Size,
			line: routerLine(f, z), file: true})
	}
	budget := ceiling * routerNamesThirds / 3
	names := 0
	for _, it := range items {
		names += len(it.line)
	}
	// Each file's own directory first, then subtrees, deepest first.
	group := func(depth int) func(*routerItem) (string, bool) {
		if depth < 0 {
			return func(it *routerItem) (string, bool) {
				if !it.file {
					return "", false
				}
				return dirOf(it.key) + "*", true
			}
		}
		return func(it *routerItem) (string, bool) {
			parts := strings.Split(dirOf(it.first), "/")
			if len(parts) <= depth+1 {
				return "", false
			}
			return strings.Join(parts[:depth+1], "/") + "/", true
		}
	}
	maxDepth := 0
	for _, f := range files {
		maxDepth = max(maxDepth, strings.Count(f.Path, "/"))
	}
	for depth := -1; depth < maxDepth && names > budget; depth++ {
		key := group(depth)
		if depth >= 0 {
			// Subtrees: the deepest level that still holds several entries.
			key = group(maxDepth - 1 - depth)
		}
		items, names = foldItems(items, key, names, budget, 2)
	}
	// Still over: a top-level directory of one file or one fold is a line of
	// its own, which no group folds; it folds alone where its folded line is
	// the shorter. Past that only a tree of more top-level entries than the
	// ceiling holds names passes it (the caller refuses such a listing).
	if names > budget {
		items, names = foldItems(items, group(0), names, budget, 1)
	}
	// Skeletons into what is left, the highest score first.
	byScore := slices.Clone(items)
	slices.SortStableFunc(byScore, func(a, b *routerItem) int {
		if a.best != b.best {
			return b.best - a.best
		}
		return strings.Compare(a.key, b.key)
	})
	left := ceiling - names
	shown := map[string]string{}
	for _, it := range byScore {
		if !it.file {
			continue
		}
		s := z.Skeletons[it.key]
		if s == "" {
			continue
		}
		if in := indent(s); len(in) <= left {
			shown[it.key] = in
			left -= len(in)
		}
	}
	slices.SortFunc(items, func(a, b *routerItem) int { return strings.Compare(a.first, b.first) })
	var b strings.Builder
	folded = map[string]bool{}
	for _, it := range items {
		b.WriteString(it.line)
		b.WriteString(shown[it.key])
		if !it.file {
			for _, f := range it.files {
				folded[f.Path] = true
			}
		}
	}
	return b.String(), folded
}

// foldItems merges the items key groups of at least minGroup, the lowest
// best score first, into one folded line each, until names fits budget. An
// item key does not group stays; a group of one folds only where its folded
// line is shorter than its own.
func foldItems(items []*routerItem, key func(*routerItem) (string, bool), names, budget, minGroup int) ([]*routerItem, int) {
	groups := map[string][]*routerItem{}
	for _, it := range items {
		if k, ok := key(it); ok {
			groups[k] = append(groups[k], it)
		}
	}
	order := make([]string, 0, len(groups))
	for k, g := range groups {
		if len(g) >= minGroup && (len(g) > 1 || len(foldLine(k, len(g[0].files), g[0].bytes)) < len(g[0].line)) {
			order = append(order, k)
		}
	}
	// The lowest best score first; of a score, the fold that saves the most
	// bytes, so the fewest files go into folded lines.
	best, saved := make(map[string]int, len(order)), make(map[string]int, len(order))
	for _, k := range order {
		files, bytes := 0, int64(0)
		for _, it := range groups[k] {
			best[k] = max(best[k], it.best)
			saved[k] += len(it.line)
			files += len(it.files)
			bytes += it.bytes
		}
		saved[k] -= len(foldLine(k, files, bytes))
	}
	slices.SortFunc(order, func(a, b string) int {
		if best[a] != best[b] {
			return best[a] - best[b]
		}
		if saved[a] != saved[b] {
			return saved[b] - saved[a]
		}
		return strings.Compare(a, b)
	})
	gone := map[*routerItem]bool{}
	var folded []*routerItem
	for _, k := range order {
		if names <= budget {
			break
		}
		g := groups[k]
		it := &routerItem{key: k, first: g[0].first, best: best[k]}
		for _, x := range g {
			names -= len(x.line)
			gone[x] = true
			it.files = append(it.files, x.files...)
			it.bytes += x.bytes
			it.first = min(it.first, x.first)
		}
		it.line = foldLine(k, len(it.files), it.bytes)
		names += len(it.line)
		folded = append(folded, it)
	}
	out := make([]*routerItem, 0, len(items))
	for _, it := range items {
		if !gone[it] {
			out = append(out, it)
		}
	}
	return append(out, folded...), names
}

// dirOf is a path's directory as a pick names it: "" at the root, else
// with its slash.
func dirOf(p string) string {
	if d := path.Dir(p); d != "." {
		return d + "/"
	}
	return ""
}

// routerLine is a file's line: its path, size and hints.
func routerLine(f File, z StageZero) string {
	hints := []string{kb(f.Size)}
	// Files caps a big file at FileMaxBytes and marks it Cut: its head.
	if f.Cut || f.Size > FileMaxBytes {
		hints = append(hints, "head only")
	}
	if AnchorRank(f.Path) > 0 {
		hints = append(hints, "anchor")
	}
	if n := z.FanIn[f.Path]; n > 0 {
		hints = append(hints, fmt.Sprintf("in %d", n))
	}
	if n := z.Churn[f.Path]; n > 0 {
		hints = append(hints, fmt.Sprintf("churn %d", n))
	}
	return f.Path + " (" + strings.Join(hints, ", ") + ")\n"
}

// foldLine is a folded directory's one line, by the path the router picks
// it by.
func foldLine(key string, files int, bytes int64) string {
	return fmt.Sprintf("%s (folded: %d files, %s)\n", key, files, kb(bytes))
}

// indent sets a skeleton under its file's line.
func indent(s string) string {
	var b strings.Builder
	for _, l := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		b.WriteString("    ")
		b.WriteString(l)
		b.WriteByte('\n')
	}
	return b.String()
}

// KiB is a byte count in kB of 1024, rounded: one rounding for the
// router's listing, its prompt and the dry run's report.
func KiB(n int64) int64 { return (n + 512) / 1024 }

// kb is a size as the router reads it, in kB of 1024 bytes.
func kb(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%d kB", KiB(n))
}
