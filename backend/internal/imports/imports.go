// Package imports reads which packages inside a repository import which:
// Go packages by their import paths against the repository's own go.mod
// modules, Python packages by their import statements against the
// repository's own distributions. A Go module's packages fold to their
// top level (cmd/<x>, internal/<a>, <a>), a Python distribution's to its
// top-level subpackages and modules: the package graph itself is too fine
// to read (user, 2026-11-10). Pure: bytes in, parts and edges out, every
// rule a table test. Code, no model.
package imports

import (
	"go/parser"
	"go/token"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/trick77/almanach/internal/manifest"
)

// Version is this package's reading: raise it with any change to what
// Parse makes of a file, and every repository is read again at the next
// sync, not only those whose head moved.
const Version = 1

// Unit is one folded part, as the build's parts are (manifest.Unit): its
// folder in the repository ("." the root), its name (its path in its
// module or distribution: "cmd/api", "internal/store", "pkg.sub"), and
// whether it is an entry point (KindApp: a Go main package, a Python
// script's or __main__ module).
type Unit = manifest.Unit

// KindApp is an entry point.
const KindApp = manifest.KindApp

// Edge is one part importing another, by their folders.
type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Read is the parts and edges, each sorted; empty when no part imports
// another.
type Read struct {
	Units []Unit `json:"units"`
	Edges []Edge `json:"edges"`
}

// Wanted reports whether a file at p is read: a Go or Python source, a
// go.mod, a pyproject.toml; never a test, a test's data or a vendored
// copy.
func Wanted(p string) bool {
	b := path.Base(p)
	switch {
	case b == "go.mod" || path.Ext(b) == ".go" && !strings.HasSuffix(b, "_test.go"):
		return !skipped(p, true)
	case b == "pyproject.toml":
		return !skipped(p, false)
	case path.Ext(b) == ".py":
		return !strings.HasPrefix(b, "test_") && !strings.HasSuffix(b, "_test.py") && b != "conftest.py" && !skipped(p, false)
	}
	return false
}

// skipped reports whether a folder on p's path is none of the code's: a
// hidden one, a vendored copy, a test tree; for Go (goTree) also testdata
// and a `_` folder, which the go tool leaves out (a Python package's
// `_internal` is its code).
func skipped(p string, goTree bool) bool {
	for _, s := range strings.Split(path.Dir(p), "/") {
		switch {
		case s == "." || s == "":
		case strings.HasPrefix(s, ".") || s == "vendor" || s == "node_modules":
			return true
		case goTree && (s == "testdata" || strings.HasPrefix(s, "_")):
			return true
		case !goTree && (s == "tests" || s == "test"):
			return true
		}
	}
	return false
}

// Parse reads the files given (path to body, as Wanted picks them).
func Parse(files map[string][]byte) Read {
	g := newGraph()
	goModules(files, g)
	pyPackages(files, g)
	return g.read()
}

// graph collects units and edges by folder.
type graph struct {
	units map[string]*Unit
	edges map[[2]string]bool
}

func newGraph() *graph {
	return &graph{units: map[string]*Unit{}, edges: map[[2]string]bool{}}
}

func (g *graph) unit(dir, name string) *Unit {
	if g.units[dir] == nil {
		g.units[dir] = &Unit{Dir: dir, Name: name}
	}
	return g.units[dir]
}

func (g *graph) edge(from, to string) {
	if from != to {
		g.edges[[2]string{from, to}] = true
	}
}

// read is the units an edge touches and the edges, sorted, the root folder
// named as the build's parts name it (manifest.UnitDir).
func (g *graph) read() Read {
	out := Read{Units: []Unit{}, Edges: []Edge{}}
	touched := map[string]bool{}
	for k := range g.edges {
		if g.units[k[0]] == nil || g.units[k[1]] == nil {
			continue
		}
		out.Edges = append(out.Edges, Edge{From: manifest.UnitDir(k[0]), To: manifest.UnitDir(k[1])})
		touched[k[0]], touched[k[1]] = true, true
	}
	for d, u := range g.units {
		if touched[d] {
			v := *u
			v.Dir = manifest.UnitDir(d)
			out.Units = append(out.Units, v)
		}
	}
	sort.Slice(out.Units, func(i, j int) bool { return out.Units[i].Dir < out.Units[j].Dir })
	sort.Slice(out.Edges, func(i, j int) bool {
		a, b := out.Edges[i], out.Edges[j]
		if a.From != b.From {
			return a.From < b.From
		}
		return a.To < b.To
	})
	return out
}

// join is folder rel of folder d, "" the root.
func join(d, rel string) string {
	if d == "" {
		return rel
	}
	if rel == "" {
		return d
	}
	return d + "/" + rel
}

// dirOf is a path's folder, "" for the root.
func dirOf(p string) string {
	if d := path.Dir(p); d != "." {
		return d
	}
	return ""
}

// reIgnore is a build constraint that leaves the file out of every build:
// `//go:build ignore` (and its old `// +build ignore`), the tag alone.
var (
	reIgnore  = regexp.MustCompile(`(?m)^//\s*(?:go:build|\+build)[ \t]+ignore[ \t]*$`)
	rePackage = regexp.MustCompile(`(?m)^package[ \t]`)
)

// ignored reports whether a Go file's header says it is built never: the
// constraint before its package clause.
func ignored(body []byte) bool {
	head := body
	if i := rePackage.FindIndex(body); i != nil {
		head = body[:i[0]]
	}
	return reIgnore.Match(head)
}

// goModule is a go.mod's folder and module path.
type goModule struct{ dir, path string }

var reModule = regexp.MustCompile(`(?m)^\s*module\s+("?)([^\s"]+)("?)`)

// goModules reads the Go packages: each .go file belongs to the module of
// the nearest go.mod above it; its package folds to its top level there,
// and each import of an in-repository module's package (the longest
// module path that prefixes it) is an edge to that one's fold.
func goModules(files map[string][]byte, g *graph) {
	var mods []goModule
	for p, body := range files {
		if path.Base(p) != "go.mod" {
			continue
		}
		if m := reModule.FindSubmatch(body); m != nil {
			mods = append(mods, goModule{dir: dirOf(p), path: string(m[2])})
		}
	}
	if len(mods) == 0 {
		return
	}
	// Longest first: a nested module owns its tree, and its path wins.
	sort.Slice(mods, func(i, j int) bool { return len(mods[i].dir) > len(mods[j].dir) })
	byPath := append([]goModule(nil), mods...)
	sort.Slice(byPath, func(i, j int) bool { return len(byPath[i].path) > len(byPath[j].path) })
	owner := func(dir string) (goModule, string, bool) {
		for _, m := range mods {
			if m.dir == "" {
				return m, dir, true
			}
			if dir == m.dir {
				return m, "", true
			}
			if rel, ok := strings.CutPrefix(dir, m.dir+"/"); ok {
				return m, rel, true
			}
		}
		return goModule{}, "", false
	}
	fold := func(m goModule, rel string) (dir, name string) {
		segs := strings.Split(rel, "/")
		switch {
		case rel == "":
			return m.dir, path.Base(m.path)
		case (segs[0] == "cmd" || segs[0] == "internal" || segs[0] == "pkg") && len(segs) > 1:
			name = segs[0] + "/" + segs[1]
		default:
			name = segs[0]
		}
		return join(m.dir, name), name
	}
	fset := token.NewFileSet()
	for p, body := range files {
		if path.Ext(p) != ".go" {
			continue
		}
		m, rel, ok := owner(dirOf(p))
		// A file the build leaves out (`//go:build ignore`, a generator
		// run by go generate) is no part of the code.
		if !ok || ignored(body) {
			continue
		}
		f, err := parser.ParseFile(fset, p, body, parser.ImportsOnly)
		if err != nil {
			continue
		}
		dir, name := fold(m, rel)
		u := g.unit(dir, name)
		// The fold's own package is its entry point; a main deeper in it (an
		// example, a tool under internal/store/gen) makes no app of it.
		if f.Name.Name == "main" && dirOf(p) == dir {
			u.Kind = KindApp
		}
		for _, s := range f.Imports {
			ip, err := strconv.Unquote(s.Path.Value)
			if err != nil {
				continue
			}
			for _, to := range byPath {
				if ip != to.path && !strings.HasPrefix(ip, to.path+"/") {
					continue
				}
				tdir, tname := fold(to, strings.TrimPrefix(strings.TrimPrefix(ip, to.path), "/"))
				g.unit(tdir, tname)
				g.edge(dir, tdir)
				break
			}
		}
	}
}

// pyDist is a Python package root: the folder holding the package, its
// import name, and the modules its scripts start in.
type pyDist struct {
	root, name string
	scripts    []string
}

var (
	reImport = regexp.MustCompile(`(?m)^[ \t]*import[ \t]+([A-Za-z_][\w.]*(?:[ \t]+as[ \t]+\w+)?(?:[ \t]*,[ \t]*[A-Za-z_][\w.]*(?:[ \t]+as[ \t]+\w+)?)*)`)
	// The names after `import`: in parentheses, over lines (as black
	// writes a long one), group 3; else on the line, group 4.
	reFromImport = regexp.MustCompile(`(?m)^[ \t]*from[ \t]+(\.*)([A-Za-z_][\w.]*)?[ \t]+import[ \t]*(?:\(([^)]*)\)|([\w, \t*]+))`)
	reComment    = regexp.MustCompile(`#[^\n]*`)
	// A triple-quoted string (a docstring's usage line is no import) and a
	// line continued by a backslash.
	reTripleQuoted = regexp.MustCompile(`(?s)""".*?"""|'''.*?'''`)
	reContinued    = regexp.MustCompile(`\\[ \t]*\r?\n`)
)

// pyCode is a Python source as its imports are read: its triple-quoted
// strings out, its continued lines one.
func pyCode(body []byte) string {
	text := reTripleQuoted.ReplaceAllString(string(body), "")
	return reContinued.ReplaceAllString(text, " ")
}

// pyPackages reads the Python packages: each distribution a pyproject.toml
// names, its package at <dir>/src/<name> or <dir>/<name> (the name with
// "-" as "_"); its top-level subpackages and modules are its parts, and
// each import of one, absolute or relative, an edge to it.
func pyPackages(files map[string][]byte, g *graph) {
	// Each folder holding a .py file at any depth, once: asked per name of
	// every `from pkg import a, b`.
	pyDirs := map[string]bool{}
	for p := range files {
		if path.Ext(p) == ".py" {
			for d := dirOf(p); d != "" && !pyDirs[d]; d = dirOf(d) {
				pyDirs[d] = true
			}
		}
	}
	have := func(p string) bool { _, ok := files[p]; return ok }
	hasPkg := func(d string) bool { return pyDirs[d] }
	var dists []pyDist
	for p, body := range files {
		if path.Base(p) != "pyproject.toml" {
			continue
		}
		n, scripts, ok := manifest.PyProject(body)
		if !ok {
			continue
		}
		name := strings.ReplaceAll(strings.ToLower(n), "-", "_")
		d := dirOf(p)
		// The package under src/ or beside the pyproject.toml; a
		// distribution of one module, or one whose package is elsewhere,
		// has no parts to read.
		for _, c := range []string{join(d, "src"), d} {
			if hasPkg(join(c, name)) {
				dists = append(dists, pyDist{root: c, name: name, scripts: scripts})
				break
			}
		}
	}
	for _, dist := range dists {
		pkgDir := join(dist.root, dist.name)
		// partDir is a top-level part's path: its folder, or a module's file
		// (pkg/cli.py), so a reader finds it.
		partDir := func(name string) string {
			d := join(pkgDir, name)
			if !hasPkg(d) && have(d+".py") {
				return d + ".py"
			}
			return d
		}
		// part is the top-level part a dotted module path inside the
		// package names: pkg.sub.x is pkg/sub; pkg alone is none.
		part := func(dotted string) (string, string, bool) {
			segs := strings.Split(dotted, ".")
			if segs[0] != dist.name || len(segs) < 2 {
				return "", "", false
			}
			return partDir(segs[1]), dist.name + "." + segs[1], true
		}
		for _, s := range dist.scripts {
			if dir, name, ok := part(s); ok {
				g.unit(dir, name).Kind = KindApp
			}
		}
		for p, body := range files {
			rel, ok := strings.CutPrefix(p, pkgDir+"/")
			if !ok || path.Ext(p) != ".py" {
				continue
			}
			segs := strings.Split(strings.TrimSuffix(rel, ".py"), "/")
			if segs[0] == "__init__" {
				continue
			}
			from := partDir(segs[0])
			u := g.unit(from, dist.name+"."+segs[0])
			if segs[0] == "__main__" {
				u.Kind = KindApp
			}
			// The package the module is in, for its relative imports: a
			// module's and its package's __init__ alike.
			mod := append([]string{dist.name}, segs...)
			mod = mod[:len(mod)-1]
			text := pyCode(body)
			for _, m := range reImport.FindAllStringSubmatch(text, -1) {
				for _, item := range strings.Split(m[1], ",") {
					f := strings.Fields(item)
					if len(f) == 0 {
						continue
					}
					if dir, tname, ok := part(f[0]); ok {
						g.unit(dir, tname)
						g.edge(from, dir)
					}
				}
			}
			for _, m := range reFromImport.FindAllStringSubmatch(text, -1) {
				dots, base := len(m[1]), m[2]
				var dotted []string
				if dots > 0 {
					if dots-1 > len(mod) {
						continue
					}
					dotted = append(dotted, mod[:len(mod)-(dots-1)]...)
				}
				if base != "" {
					dotted = append(dotted, strings.Split(base, ".")...)
				}
				full := strings.Join(dotted, ".")
				if dir, tname, ok := part(full); ok {
					g.unit(dir, tname)
					g.edge(from, dir)
					continue
				}
				// `from pkg import sub`: each name may be a part.
				if full == dist.name {
					names := reComment.ReplaceAllString(m[3]+m[4], "")
					for _, item := range strings.Split(names, ",") {
						f := strings.Fields(item)
						if len(f) == 0 || f[0] == "*" {
							continue
						}
						name := f[0]
						if dir := partDir(name); have(dir) || hasPkg(dir) {
							g.unit(dir, dist.name+"."+name)
							g.edge(from, dir)
						}
					}
				}
			}
		}
	}
}
