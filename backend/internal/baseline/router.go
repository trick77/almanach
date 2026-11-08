package baseline

import (
	"fmt"
	"path"
	"slices"
	"strings"
)

// RouterMaxBytes is the router's listing ceiling: about 40k tokens, a cent
// or two a call (user, 2026-11-08: 150 kB, revisited after the dry run).
// Without it the router would be the next expensive call: the skeletons
// of a large service run past 100k tokens.
const RouterMaxBytes = 150_000

// routerNamesShare is the share of the ceiling the file names may take
// before directories fold: the rest is skeletons.
const routerNamesShare = 2

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
// highest-scored first. When the names alone pass half the ceiling, the
// lowest-scored directories fold into one line each, their own files as
// "dir/*"; when that is not enough, whole subtrees fold as "dir/", the
// deepest first. Sizes in kB of 1024 bytes, as the budgets are given; a
// file read only as its head says so. In path order, so the listing reads
// as the tree does.
func RouterListing(files []File, z StageZero, ceiling int) string {
	items := make([]*routerItem, 0, len(files))
	for _, f := range files {
		items = append(items, &routerItem{key: f.Path, first: f.Path, files: []File{f}, best: routerScore(f.Path, z), bytes: f.Size,
			line: routerLine(f, z), file: true})
	}
	budget := ceiling / routerNamesShare
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
		items, names = foldItems(items, key, names, budget)
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
	for _, it := range items {
		b.WriteString(it.line)
		b.WriteString(shown[it.key])
	}
	return b.String()
}

// foldItems merges the items key groups, the lowest best score first, into
// one folded line each, until names fits budget. An item key does not
// group stays; a group of one is no fold.
func foldItems(items []*routerItem, key func(*routerItem) (string, bool), names, budget int) ([]*routerItem, int) {
	groups := map[string][]*routerItem{}
	for _, it := range items {
		if k, ok := key(it); ok {
			groups[k] = append(groups[k], it)
		}
	}
	order := make([]string, 0, len(groups))
	for k, g := range groups {
		if len(g) > 1 {
			order = append(order, k)
		}
	}
	best := func(g []*routerItem) int {
		b := 0
		for _, it := range g {
			b = max(b, it.best)
		}
		return b
	}
	slices.SortFunc(order, func(a, b string) int {
		if x, y := best(groups[a]), best(groups[b]); x != y {
			return x - y
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
		it := &routerItem{key: k, first: g[0].first, best: best(g)}
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
	if f.Size > FileMaxBytes {
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

// kb is a size as the router reads it, in kB of 1024 bytes.
func kb(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%d kB", (n+512)/1024)
}
