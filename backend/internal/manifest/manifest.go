// Package manifest reads what a repository's build manifests say it is and
// what it pulls in: a go.mod's module and requires, a pom.xml's
// coordinates and dependencies, a package.json's name and dependencies, a
// Gradle build's projects and dependencies (gradle.go), a pyproject.toml's
// or requirements file's (python.go), a Cargo.toml's (cargo.go), and the
// folders a workspace declares (workspace.go). Pure: bytes in, coordinates
// out, so every rule is a table test. Code derives the dependency graph
// from these, never a model.
package manifest

import (
	"bufio"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"io"
	"path"
	"sort"
	"strings"
)

// Version is this package's reading of the manifests: raise it with any
// change to what Parse or Units makes of one, and every repository's
// manifests are read again at the next sync, not only those whose head
// moved. 6: no version read, so no row keeps one. 7: the parts inside a
// repository (Units).
const Version = 7

// Ecosystems: the prefix of a coordinate.
const (
	Go     = "go"
	Maven  = "maven"
	NPM    = "npm"
	Python = "py"
	Cargo  = "cargo"
)

// Scopes: what a requirement is for, as its manifest says. Only Main is a
// use at runtime, what the maps and the prompts draw; the others are kept
// for the dependency page. Managed pins versions (a BOM import, a Gradle
// platform, a parent POM), Dev builds or checks (an annotation processor,
// a devDependency, a Cargo build-dependency), Test only runs in tests,
// Indirect is a go.mod's `// indirect`: a dependency's dependency.
const (
	ScopeMain     = "main"
	ScopeManaged  = "managed"
	ScopeDev      = "dev"
	ScopeTest     = "test"
	ScopeIndirect = "indirect"
)

// scopeRank orders the scopes, strongest first: a coordinate named twice
// counts as its strongest use (a library in dependencies and in
// devDependencies is used).
var scopeRank = map[string]int{ScopeMain: 0, ScopeManaged: 1, ScopeDev: 2, ScopeTest: 3, ScopeIndirect: 4}

// Stronger reports whether scope a outranks scope b.
func Stronger(a, b string) bool { return scopeRank[Norm(a)] < scopeRank[Norm(b)] }

// Norm is a scope as it counts, for ranking and the main filter: one
// unknown is main, every parser naming its own.
//
// TRANSITIONAL(unscoped-requires): a stored row read before scopes
// (reader < 5) has none, main as everything was then. Remove this
// paragraph, not Norm, when no repo_manifests row has reader < 5 (each is
// read again at its repository's next sync).
func Norm(s string) string {
	if _, ok := scopeRank[s]; !ok {
		return ScopeMain
	}
	return s
}

// Dep is one coordinate a manifest requires, and what for. No version:
// none is shown, the relation is what counts (user, 2026-11-09).
type Dep struct {
	Coord string `json:"coord"`
	Scope string `json:"scope"`
}

// Read is what one repository's manifests say: the coordinates it provides
// and the ones it requires, each once, sorted. A coordinate it provides
// itself is no requirement (a monorepo's modules use each other). Units is
// the parts inside it (Units), read beside, never by Parse.
type Read struct {
	Provides []string `json:"provides"`
	Requires []Dep    `json:"requires"`
	Units    Inner    `json:"-"`
}

// Is reports whether a path names a manifest this package reads without
// being told, at the root or one level down: a deeper one is a copy (a
// checked-out corpus, a fixture) and would make the repository provide
// what it only carries, unless a build declares its folder (Modules).
func Is(p string) bool {
	// A build's catalog sits in its gradle/ folder: the root's, or a build's
	// one down (read only for a folder that is a build, see gradle).
	if p == gradleCatalog || strings.Count(p, "/") == 2 && strings.HasSuffix(p, "/"+gradleCatalog) {
		return true
	}
	if strings.Count(p, "/") > 1 {
		return false
	}
	switch b := path.Base(p); b {
	case "go.mod", "go.work", "pom.xml", "package.json", "pnpm-workspace.yaml", "gradle.properties", "pyproject.toml",
		"Cargo.toml":
		return true
	default:
		_, req := requirementsScope(b)
		return req || gradleBuild(b) || gradleSettings(b)
	}
}

// Module is a folder a build among the manifests declares as its own, and
// which tool declares it.
type Module struct {
	Dir                               string
	Gradle, Maven, NPM, Go, Cargo, Py bool
}

// Reads reports whether the module's file of this name is read: the
// declaring tool's build file (a Gradle project's gradle.properties too),
// never the other tool's (a stale or generated pom.xml in a Gradle
// project would provide what the build does not publish). A settings file
// or a catalog there makes no build of its own.
func (m Module) Reads(base string) bool {
	return m.Maven && base == "pom.xml" || m.Gradle && (base == "gradle.properties" || gradleBuild(base)) ||
		m.NPM && base == "package.json" || m.Go && base == "go.mod" || m.Cargo && base == "Cargo.toml" ||
		m.Py && base == "pyproject.toml"
}

// Modules is the folders the builds among files declare as theirs, by
// Dir: a Gradle settings file's includes (gradleProjects), a pom.xml's
// modules, its profiles' too, and a workspace's members (workspaces: npm,
// pnpm, go.work, Cargo, uv), whose patterns are matched against dirs, the
// tree's folders. A declared folder is part of the build at any depth, no
// copy: its manifests are read too (a multi-project build keeps its
// projects in lib/<x>, service/<x>, and only there what it requires).
func Modules(files map[string][]byte, dirs []string) []Module {
	logic := buildLogic(files)
	byDir := map[string]*Module{}
	at := func(d string) *Module {
		if byDir[d] == nil {
			byDir[d] = &Module{Dir: d}
		}
		return byDir[d]
	}
	projects := gradleProjects(files, logic)
	for d := range projects {
		at(d).Gradle = true
	}
	for p, body := range files {
		// A pom.xml in a Gradle project's folder is stale or generated: its
		// modules are no build's.
		if _, gradle := projects[dirOf(p)]; path.Base(p) != "pom.xml" || gradle {
			continue
		}
		x, ok := decodePom(body)
		if !ok {
			continue
		}
		for _, m := range append(x.Modules, x.ProfileModules...) {
			// A module may name its pom.xml instead of its folder; one naming
			// another file is left out, only pom.xml is read.
			m = strings.ReplaceAll(strings.TrimSpace(m), `\`, "/")
			if path.Base(m) == "pom.xml" {
				m = path.Dir(m)
			} else if strings.HasSuffix(m, ".xml") {
				continue
			}
			if d, ok := inRepo(dirOf(p), m); ok {
				at(d).Maven = true
			}
		}
	}
	for _, w := range workspaces(files, dirs) {
		m := at(w.dir)
		switch w.tool {
		case NPM:
			m.NPM = true
		case Go:
			m.Go = true
		case Cargo:
			m.Cargo = true
		case Python:
			m.Py = true
		}
	}
	out := make([]Module, 0, len(byDir))
	for _, m := range byDir {
		out = append(out, *m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Dir < out[j].Dir })
	return out
}

// trimBOM drops a UTF-8 byte order mark (an editor's on Windows): it would
// fail JSON, TOML and XML at the first byte; the tools themselves skip it.
func trimBOM(body []byte) []byte { return bytes.TrimPrefix(body, []byte("\xef\xbb\xbf")) }

// Parse reads the manifests given (path to body). A manifest that does not
// parse is left out; the rest still count.
func Parse(in map[string][]byte) Read {
	return parse(read(in))
}

func parse(files map[string][]byte, g gradleRead, each map[string]fileRead) Read {
	provides := map[string]bool{}
	requires := map[string]Dep{}
	// A coordinate named twice: the strongest scope, whatever the order.
	add := func(d Dep) {
		d.Scope = Norm(d.Scope)
		cur, ok := requires[d.Coord]
		if !ok || Stronger(d.Scope, cur.Scope) {
			requires[d.Coord] = d
		}
	}
	for _, c := range g.provides() {
		provides[c] = true
	}
	for p := range files {
		for _, c := range each[p].prov {
			provides[c] = true
		}
		for _, d := range each[p].reqs {
			add(d)
		}
	}
	out := Read{Provides: []string{}, Requires: []Dep{}}
	for c := range provides {
		out.Provides = append(out.Provides, c)
	}
	for c, d := range requires {
		if !provides[c] {
			out.Requires = append(out.Requires, d)
		}
	}
	sort.Strings(out.Provides)
	sort.Slice(out.Requires, func(i, j int) bool { return out.Requires[i].Coord < out.Requires[j].Coord })
	return out
}

// fileRead is what one manifest provides and requires.
type fileRead struct {
	prov []string
	reqs []Dep
}

// read is the manifests' bodies, a byte order mark off, what the Gradle
// builds among them say (read together: a build file alone does not say
// what its project is called), and each manifest's own read, a Gradle
// build file's requires among them (its provides are the build's, by
// folder). A manifest that does not parse reads as nothing.
func read(in map[string][]byte) (map[string][]byte, gradleRead, map[string]fileRead) {
	files := make(map[string][]byte, len(in))
	for p, body := range in {
		files[p] = trimBOM(body)
	}
	g := gradle(files)
	cargoWS := cargoWorkspaces(files)
	pins := pomPins(files)
	each := make(map[string]fileRead, len(files))
	for p, body := range files {
		var r fileRead
		switch b := path.Base(p); b {
		case "go.mod":
			r.prov, r.reqs = goMod(body)
		case "go.work", "pnpm-workspace.yaml":
			// Members only (Modules), nothing provided or required.
		case "pom.xml":
			r.prov, r.reqs = pom(body, pins.of(dirOf(p)))
		case "package.json":
			r.prov, r.reqs = packageJSON(body)
		case "pyproject.toml":
			r.prov, r.reqs = pyproject(body)
		case "Cargo.toml":
			r.prov, r.reqs = cargo(body, cargoWS.of(dirOf(p)))
		default:
			if scope, ok := requirementsScope(b); ok {
				r.reqs = requirements(body, toolingScope(dirOf(p), scope))
			} else {
				r.reqs = g.reqs[p]
			}
		}
		each[p] = r
	}
	return files, g, each
}

// goMod is a go.mod's module and its requires, line by line: the module
// directive, require lines and require blocks. Replace and exclude are
// left alone; a require marked `// indirect` is a dependency's dependency,
// Indirect.
func goMod(body []byte) ([]string, []Dep) {
	var prov []string
	var reqs []Dep
	inBlock := false
	sc := bufio.NewScanner(bytes.NewReader(body))
	for sc.Scan() {
		line := sc.Text()
		scope := ScopeMain
		if i := strings.Index(line, "//"); i >= 0 {
			if strings.HasPrefix(strings.TrimSpace(line[i+2:]), "indirect") {
				scope = ScopeIndirect
			}
			line = line[:i]
		}
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		switch {
		case inBlock && f[0] == ")":
			inBlock = false
		case inBlock:
			if len(f) >= 2 {
				reqs = append(reqs, Dep{Coord: Go + ":" + unquote(f[0]), Scope: scope})
			}
		case f[0] == "module" && len(f) >= 2:
			prov = append(prov, Go+":"+unquote(f[1]))
		case f[0] == "require" && len(f) >= 2 && f[1] == "(":
			inBlock = true
		case f[0] == "require" && len(f) >= 3:
			reqs = append(reqs, Dep{Coord: Go + ":" + unquote(f[1]), Scope: scope})
		}
	}
	return prov, reqs
}

func unquote(s string) string { return strings.Trim(s, "\"`") }

// pomXML is the part of a pom.xml read here.
type pomXML struct {
	GroupID        string   `xml:"groupId"`
	ArtifactID     string   `xml:"artifactId"`
	Parent         pomDep   `xml:"parent"`
	Dependencies   []pomDep `xml:"dependencies>dependency"`
	Managed        []pomDep `xml:"dependencyManagement>dependencies>dependency"`
	Modules        []string `xml:"modules>module"`
	ProfileModules []string `xml:"profiles>profile>modules>module"`
}

// decodePom is a pom.xml read; false when it does not parse.
func decodePom(body []byte) (pomXML, bool) {
	var p pomXML
	dec := xml.NewDecoder(bytes.NewReader(trimBOM(body)))
	dec.CharsetReader = charsetReader
	return p, dec.Decode(&p) == nil
}

type pomDep struct {
	GroupID    string `xml:"groupId"`
	ArtifactID string `xml:"artifactId"`
	Scope      string `xml:"scope"`
	Type       string `xml:"type"`
}

// pomPinned is one pom.xml's own coordinate, its parent's, and its managed
// entries (BOM imports aside): the scope each gives a dependency naming
// none of its own (as written, "" for none).
type pomPinned struct {
	self, parent string
	pins         map[string]string
}

// pomPinsByDir is each pom.xml's pomPinned by its folder.
type pomPinsByDir map[string]pomPinned

func pomPins(files map[string][]byte) pomPinsByDir {
	out := pomPinsByDir{}
	for p, body := range files {
		if path.Base(p) != "pom.xml" {
			continue
		}
		x, ok := decodePom(body)
		if !ok {
			continue
		}
		group := strings.TrimSpace(x.GroupID)
		if group == "" {
			group = strings.TrimSpace(x.Parent.GroupID)
		}
		self, _ := mavenCoord(group, x.ArtifactID)
		parent, _ := mavenCoord(x.Parent.GroupID, x.Parent.ArtifactID)
		pins := map[string]string{}
		for _, d := range x.Managed {
			c, ok := mavenCoord(d.GroupID, d.ArtifactID)
			if _, seen := pins[c]; !ok || seen || d.bomImport() {
				continue
			}
			pins[c] = strings.TrimSpace(d.Scope)
		}
		out[dirOf(p)] = pomPinned{self: self, parent: parent, pins: pins}
	}
	return out
}

// of is the pinned scope of a coordinate for the pom.xml in folder d: its
// own first, then its parent's up the chain, as Maven inherits them, each
// parent the pom.xml of this repository with that coordinate (the nearest
// folder above first); a parent from elsewhere ends the chain. False for
// none.
func (b pomPinsByDir) of(d string) func(coord string) (string, bool) {
	return func(coord string) (string, bool) {
		seen := map[string]bool{}
		for at, ok := d, true; ok && !seen[at]; {
			seen[at] = true
			cur := b[at]
			if pin, found := cur.pins[coord]; found {
				return pin, true
			}
			at, ok = b.parentOf(at, cur.parent)
		}
		return "", false
	}
}

// parentOf is the folder of the pom.xml whose coordinate is parent: the
// nearest folder above d holding it, else any in path order; false for
// none.
func (b pomPinsByDir) parentOf(d, parent string) (string, bool) {
	if parent == "" {
		return "", false
	}
	for at := d; at != ""; {
		at = dirOf(at)
		if b[at].self == parent {
			return at, true
		}
	}
	var dirs []string
	for dir, p := range b {
		if p.self == parent && dir != d {
			dirs = append(dirs, dir)
		}
	}
	if len(dirs) == 0 {
		return "", false
	}
	sort.Strings(dirs)
	return dirs[0], true
}

func (d pomDep) bomImport() bool {
	return strings.TrimSpace(d.Scope) == "import" && strings.TrimSpace(d.Type) == "pom"
}

// pomScope is a Maven scope as a Dep's: compile, runtime, provided, system
// (and none) Main, test Test, import Managed.
func pomScope(s string) string {
	switch strings.TrimSpace(s) {
	case "test":
		return ScopeTest
	case "import":
		return ScopeManaged
	}
	return ScopeMain
}

// pom is a pom.xml's own coordinate (groupId from its parent when it has
// none) and its dependencies by scope (pomScope). Its parent is Managed:
// build inheritance, what most services of one platform share, no use at
// runtime. So is a BOM its dependencyManagement imports
// (`<type>pom</type>`, `<scope>import</scope>`); any other managed entry
// is required by nothing and gives its scope to a dependency
// naming none, its own or its parent chain's within the repository
// (pinned), as Maven inherits them. A coordinate built of a property
// (`${...}`) is left out: unresolved, it would match nothing or the wrong
// thing.
func pom(body []byte, pinned func(coord string) (string, bool)) ([]string, []Dep) {
	p, ok := decodePom(body)
	if !ok {
		return nil, nil
	}
	var prov []string
	group := strings.TrimSpace(p.GroupID)
	if group == "" {
		group = strings.TrimSpace(p.Parent.GroupID)
	}
	if c, ok := mavenCoord(group, p.ArtifactID); ok {
		prov = append(prov, c)
	}
	var reqs []Dep
	if c, ok := mavenCoord(p.Parent.GroupID, p.Parent.ArtifactID); ok {
		reqs = append(reqs, Dep{Coord: c, Scope: ScopeManaged})
	}
	for _, d := range p.Managed {
		if c, ok := mavenCoord(d.GroupID, d.ArtifactID); ok && d.bomImport() {
			reqs = append(reqs, Dep{Coord: c, Scope: ScopeManaged})
		}
	}
	for _, d := range p.Dependencies {
		c, ok := mavenCoord(d.GroupID, d.ArtifactID)
		if !ok {
			continue
		}
		scope := strings.TrimSpace(d.Scope)
		if scope == "" {
			scope, _ = pinned(c)
		}
		reqs = append(reqs, Dep{Coord: c, Scope: pomScope(scope)})
	}
	return prov, reqs
}

// charsetReader reads a pom.xml that declares another encoding than
// UTF-8, as older Maven projects do: ISO-8859-1 and windows-1252 byte by
// byte to their code points (the coordinates are ASCII either way), any
// other label as it stands.
func charsetReader(label string, in io.Reader) (io.Reader, error) {
	switch strings.ToLower(label) {
	case "iso-8859-1", "iso8859-1", "latin1", "latin-1", "windows-1252", "cp1252":
		b, err := io.ReadAll(in)
		if err != nil {
			return nil, err
		}
		r := make([]rune, len(b))
		for i, c := range b {
			r[i] = rune(c)
		}
		return strings.NewReader(string(r)), nil
	}
	return in, nil
}

func mavenCoord(group, artifact string) (string, bool) {
	group, artifact = strings.TrimSpace(group), strings.TrimSpace(artifact)
	if group == "" || artifact == "" || strings.Contains(group+artifact, "${") {
		return "", false
	}
	return Maven + ":" + group + ":" + artifact, true
}

// packageJSON is a package.json's name and the keys of its dependencies:
// dependencies and peer ones Main; devDependencies Dev, and
// optionalDependencies, as an extra elsewhere: none every install has.
func packageJSON(body []byte) ([]string, []Dep) {
	// Raw values: only the names count, so a value of another shape costs
	// the file nothing.
	var p struct {
		Name     string                     `json:"name"`
		Deps     map[string]json.RawMessage `json:"dependencies"`
		Dev      map[string]json.RawMessage `json:"devDependencies"`
		Peer     map[string]json.RawMessage `json:"peerDependencies"`
		Optional map[string]json.RawMessage `json:"optionalDependencies"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, nil
	}
	var prov []string
	if n := strings.TrimSpace(p.Name); n != "" {
		prov = append(prov, NPM+":"+n)
	}
	var reqs []Dep
	for _, s := range []struct {
		m     map[string]json.RawMessage
		scope string
	}{{p.Deps, ScopeMain}, {p.Peer, ScopeMain}, {p.Optional, ScopeDev}, {p.Dev, ScopeDev}} {
		for name := range s.m {
			if name = strings.TrimSpace(name); name != "" {
				reqs = append(reqs, Dep{Coord: NPM + ":" + name, Scope: s.scope})
			}
		}
	}
	return prov, reqs
}
