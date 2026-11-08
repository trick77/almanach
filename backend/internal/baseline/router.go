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

// RouterListing is what the router reads of a tree: every file by path,
// size and hints, and as many skeletons as fit ceiling, the
// highest-scored first. When the names alone pass half the ceiling, the
// directories of the lowest scores fold into one line each ("dir/ (40
// files, 120 kB)"), whose files the router can pick only as the
// directory. In path order, so the listing reads as the tree does.
func RouterListing(files []File, z StageZero, ceiling int) string {
	sorted := slices.Clone(files)
	slices.SortFunc(sorted, func(a, b File) int { return strings.Compare(a.Path, b.Path) })
	line := func(f File) string {
		hints := []string{kb(f.Size)}
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
	// Fold directories, the lowest best score first, until the names fit.
	type dir struct {
		files []File
		best  int
		bytes int64
	}
	dirs := map[string]*dir{}
	names := 0
	for _, f := range sorted {
		d := dirs[path.Dir(f.Path)]
		if d == nil {
			d = &dir{}
			dirs[path.Dir(f.Path)] = d
		}
		d.files = append(d.files, f)
		d.best = max(d.best, routerScore(f.Path, z))
		d.bytes += f.Size
		names += len(line(f))
	}
	order := make([]string, 0, len(dirs))
	for k := range dirs {
		order = append(order, k)
	}
	slices.SortFunc(order, func(a, b string) int {
		if dirs[a].best != dirs[b].best {
			return dirs[a].best - dirs[b].best
		}
		return strings.Compare(a, b)
	})
	folded := map[string]bool{}
	for _, k := range order {
		if names <= ceiling/routerNamesShare {
			break
		}
		d := dirs[k]
		if len(d.files) < 2 {
			continue
		}
		for _, f := range d.files {
			names -= len(line(f))
		}
		names += len(foldLine(k, d.files, d.bytes))
		folded[k] = true
	}
	// Skeletons into what is left, the highest score first.
	byScore := slices.Clone(sorted)
	slices.SortStableFunc(byScore, func(a, b File) int { return routerScore(b.Path, z) - routerScore(a.Path, z) })
	left := ceiling - names
	shown := map[string]bool{}
	for _, f := range byScore {
		s := z.Skeletons[f.Path]
		if folded[path.Dir(f.Path)] || s == "" {
			continue
		}
		if size := len(indent(s)); size <= left {
			shown[f.Path] = true
			left -= size
		}
	}
	var b strings.Builder
	done := map[string]bool{}
	for _, f := range sorted {
		k := path.Dir(f.Path)
		if folded[k] {
			if !done[k] {
				done[k] = true
				b.WriteString(foldLine(k, dirs[k].files, dirs[k].bytes))
			}
			continue
		}
		b.WriteString(line(f))
		if shown[f.Path] {
			b.WriteString(indent(z.Skeletons[f.Path]))
		}
	}
	return b.String()
}

// foldLine is a folded directory's one line.
func foldLine(dir string, files []File, bytes int64) string {
	if dir == "." {
		dir = ""
	}
	return fmt.Sprintf("%s/ (folded: %d files, %s)\n", dir, len(files), kb(bytes))
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

// kb is a size as the router reads it.
func kb(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%d kB", (n+512)/1024)
}
