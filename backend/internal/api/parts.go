package api

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/trick77/almanach/internal/imports"
	"github.com/trick77/almanach/internal/manifest"
)

// The parts inside one repository and which uses which, by code: the
// parts its builds declare (manifest.Units, read with its manifests; main
// scope only, as every view but the dependency page) and the packages
// that import each other (imports, Go and Python folded to their top
// level). The table lists every part an edge touches; the graph the build
// parts whole, and of the packages only the entry points and the code two
// or more of them reach (user, 2026-11-10: a package graph of 40 boxes is
// a ball of wool).

type partBody struct {
	Dir  string `json:"dir"`
	Name string `json:"name"`
	// Kind is "app" or "lib" where the build or the code says so, else "".
	Kind string `json:"kind"`
}

type partEdgeBody struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// partsGraphBody is what the graph draws: parts by dir, and edges between
// them (a package's through those left out).
type partsGraphBody struct {
	Nodes []string       `json:"nodes"`
	Edges []partEdgeBody `json:"edges"`
}

type partsBody struct {
	Units []partBody     `json:"units"`
	Edges []partEdgeBody `json:"edges"`
	Graph partsGraphBody `json:"graph"`
}

// units is a repository's parts as its manifests and its code were read:
// none with the sources off, where nothing is cloned.
func (d Deps) units(ctx context.Context, key, slug string) (manifest.Inner, imports.Read, error) {
	if d.Sources == nil {
		return manifest.Inner{}, imports.Read{}, nil
	}
	return d.Store.RepoParts(ctx, key, slug)
}

// partsOf is a repository's parts on the wire: the build's main edges and
// the parts they touch, then the packages', each in the stored order.
func partsOf(in manifest.Inner, imp imports.Read) partsBody {
	out := partsBody{Units: []partBody{}, Edges: []partEdgeBody{},
		Graph: partsGraphBody{Nodes: []string{}, Edges: []partEdgeBody{}}}
	seen := map[string]bool{}
	touched := map[string]bool{}
	// An edge both the build and the imports say (a module requiring the
	// one its code imports) is one use.
	listed := map[partEdgeBody]bool{}
	drawn := map[partEdgeBody]bool{}
	list := func(e partEdgeBody) {
		if !listed[e] {
			listed[e] = true
			out.Edges = append(out.Edges, e)
		}
	}
	draw := func(e partEdgeBody) {
		if !drawn[e] {
			drawn[e] = true
			out.Graph.Edges = append(out.Graph.Edges, e)
		}
	}
	for _, e := range in.Edges {
		if manifest.Norm(e.Scope) != manifest.ScopeMain {
			continue
		}
		list(partEdgeBody{From: e.From, To: e.To})
		draw(partEdgeBody{From: e.From, To: e.To})
		touched[e.From], touched[e.To] = true, true
	}
	for _, u := range in.Units {
		if touched[u.Dir] && !seen[u.Dir] {
			seen[u.Dir] = true
			out.Units = append(out.Units, partBody{Dir: u.Dir, Name: u.Name, Kind: u.Kind})
			out.Graph.Nodes = append(out.Graph.Nodes, u.Dir)
		}
	}
	at := map[string]int{}
	for i, u := range out.Units {
		at[u.Dir] = i
	}
	for _, u := range imp.Units {
		if !seen[u.Dir] {
			seen[u.Dir] = true
			out.Units = append(out.Units, partBody{Dir: u.Dir, Name: u.Name, Kind: u.Kind})
			continue
		}
		// A build part whose code is an entry point (a go.mod at the root
		// beside its main package) is one, as shared counts it.
		if i, ok := at[u.Dir]; ok && u.Kind == imports.KindApp {
			out.Units[i].Kind = imports.KindApp
		}
	}
	for _, e := range imp.Edges {
		list(partEdgeBody{From: e.From, To: e.To})
	}
	nodes, edges := shared(imp)
	inGraph := map[string]bool{}
	for _, n := range out.Graph.Nodes {
		inGraph[n] = true
	}
	for _, n := range nodes {
		if !inGraph[n] {
			out.Graph.Nodes = append(out.Graph.Nodes, n)
		}
	}
	for _, e := range edges {
		draw(e)
	}
	return out
}

// shared is the packages' graph as drawn: the entry points and the
// packages two or more of them reach, an edge from one to another where
// the second is reached through none drawn between. Fewer than two entry
// points, or none sharing: nothing.
func shared(imp imports.Read) ([]string, []partEdgeBody) {
	out := map[string][]string{}
	for _, e := range imp.Edges {
		out[e.From] = append(out[e.From], e.To)
	}
	reach := func(from string, stop func(string) bool) map[string]bool {
		got := map[string]bool{}
		todo := append([]string(nil), out[from]...)
		for len(todo) > 0 {
			n := todo[len(todo)-1]
			todo = todo[:len(todo)-1]
			if got[n] || n == from {
				continue
			}
			got[n] = true
			if stop != nil && stop(n) {
				continue
			}
			todo = append(todo, out[n]...)
		}
		return got
	}
	var apps []string
	for _, u := range imp.Units {
		if u.Kind == imports.KindApp {
			apps = append(apps, u.Dir)
		}
	}
	if len(apps) < 2 {
		return nil, nil
	}
	keep := map[string]bool{}
	by := map[string]int{}
	for _, a := range apps {
		keep[a] = true
		for n := range reach(a, nil) {
			by[n]++
		}
	}
	sharedAny := false
	for n, c := range by {
		if c >= 2 && !keep[n] {
			keep[n] = true
			sharedAny = true
		}
	}
	if !sharedAny {
		return nil, nil
	}
	var nodes []string
	for _, u := range imp.Units {
		if keep[u.Dir] {
			nodes = append(nodes, u.Dir)
		}
	}
	var edges []partEdgeBody
	for _, n := range nodes {
		var to []string
		for m := range reach(n, func(x string) bool { return keep[x] }) {
			if keep[m] {
				to = append(to, m)
			}
		}
		sort.Strings(to)
		for _, m := range to {
			edges = append(edges, partEdgeBody{From: n, To: m})
		}
	}
	return nodes, edges
}

// markdownParts is the parts as the export's section, "" when there are
// none: each part, what it uses.
func markdownParts(p partsBody) string {
	if len(p.Edges) == 0 {
		return ""
	}
	uses := map[string][]string{}
	for _, e := range p.Edges {
		uses[e.From] = append(uses[e.From], e.To)
	}
	var b strings.Builder
	b.WriteString("\n## Innere Abhängigkeiten\n\n")
	for _, u := range p.Units {
		fmt.Fprintf(&b, "- `%s`", u.Dir)
		if to := uses[u.Dir]; len(to) > 0 {
			b.WriteString(" verwendet ")
			for i, t := range to {
				if i > 0 {
					b.WriteString(", ")
				}
				fmt.Fprintf(&b, "`%s`", t)
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}
