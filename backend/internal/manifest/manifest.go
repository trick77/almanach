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
// change to what Parse makes of one, and every repository's manifests are
// read again at the next sync, not only those whose head moved.
const Version = 5

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

// Norm is a stored scope as it counts: "" (a row read before scopes) is
// main, as everything was then.
func Norm(s string) string {
	if _, ok := scopeRank[s]; !ok {
		return ScopeMain
	}
	return s
}

// Dep is one coordinate a manifest requires, the version it names, as
// written ("" when none is), and what for.
type Dep struct {
	Coord   string `json:"coord"`
	Version string `json:"version"`
	Scope   string `json:"scope"`
}

// Read is what one repository's manifests say: the coordinates it provides
// and the ones it requires, each once, sorted. A coordinate it provides
// itself is no requirement (a monorepo's modules use each other).
type Read struct {
	Provides []string `json:"provides"`
	Requires []Dep    `json:"requires"`
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
	files := make(map[string][]byte, len(in))
	for p, body := range in {
		files[p] = trimBOM(body)
	}
	provides := map[string]bool{}
	requires := map[string]Dep{}
	// A coordinate named twice: the first version written, the strongest
	// scope.
	add := func(d Dep) {
		d.Scope = Norm(d.Scope)
		cur, ok := requires[d.Coord]
		if !ok {
			requires[d.Coord] = d
			return
		}
		if cur.Version == "" {
			cur.Version = d.Version
		}
		if Stronger(d.Scope, cur.Scope) {
			cur.Scope = d.Scope
		}
		requires[d.Coord] = cur
	}
	// By path, so the version two manifests name differently is always
	// the first one's.
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	// A Gradle build's files are read together: its requires come per
	// build file, in path order like the rest.
	gradleProv, gradleReqs := gradle(files)
	for _, c := range gradleProv {
		provides[c] = true
	}
	cargoWS := cargoWorkspaces(files)
	for _, p := range paths {
		body := files[p]
		var prov []string
		var reqs []Dep
		switch b := path.Base(p); b {
		case "go.mod":
			prov, reqs = goMod(body)
		case "go.work", "pnpm-workspace.yaml":
			// Members only (Modules), nothing provided or required.
		case "pom.xml":
			prov, reqs = pom(body)
		case "package.json":
			prov, reqs = packageJSON(body)
		case "pyproject.toml":
			prov, reqs = pyproject(body)
		case "Cargo.toml":
			prov, reqs = cargo(body, cargoWS.of(dirOf(p)))
		default:
			if scope, ok := requirementsScope(b); ok {
				reqs = requirements(body, scope)
			} else {
				reqs = gradleReqs[p]
			}
		}
		for _, c := range prov {
			provides[c] = true
		}
		for _, d := range reqs {
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
				reqs = append(reqs, Dep{Coord: Go + ":" + unquote(f[0]), Version: f[1], Scope: scope})
			}
		case f[0] == "module" && len(f) >= 2:
			prov = append(prov, Go+":"+unquote(f[1]))
		case f[0] == "require" && len(f) >= 2 && f[1] == "(":
			inBlock = true
		case f[0] == "require" && len(f) >= 3:
			reqs = append(reqs, Dep{Coord: Go + ":" + unquote(f[1]), Version: f[2], Scope: scope})
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
	Version    string `xml:"version"`
	Scope      string `xml:"scope"`
	Type       string `xml:"type"`
}

// version is the dependency's version as written, "" for a property.
func (d pomDep) version() string {
	v := strings.TrimSpace(d.Version)
	if strings.Contains(v, "${") {
		return ""
	}
	return v
}

// pom is a pom.xml's own coordinate (groupId from its parent when it has
// none) and its dependencies by scope: compile, runtime, provided and
// system Main, test Test. Its parent is Managed: build inheritance, what
// most services of one platform share, no use at runtime. So is a BOM its
// dependencyManagement imports (`<type>pom</type>`, `<scope>import</scope>`);
// any other managed entry only pins a version, required by nothing, and
// gives its version to a dependency naming none. A coordinate built of a
// property (`${...}`) is left out: unresolved, it would match nothing or
// the wrong thing.
func pom(body []byte) ([]string, []Dep) {
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
		reqs = append(reqs, Dep{Coord: c, Version: p.Parent.version(), Scope: ScopeManaged})
	}
	pinned := map[string]string{}
	for _, d := range p.Managed {
		c, ok := mavenCoord(d.GroupID, d.ArtifactID)
		if !ok {
			continue
		}
		if strings.TrimSpace(d.Scope) == "import" && strings.TrimSpace(d.Type) == "pom" {
			reqs = append(reqs, Dep{Coord: c, Version: d.version(), Scope: ScopeManaged})
		} else if _, seen := pinned[c]; !seen {
			pinned[c] = d.version()
		}
	}
	for _, d := range p.Dependencies {
		c, ok := mavenCoord(d.GroupID, d.ArtifactID)
		if !ok {
			continue
		}
		v := d.version()
		if v == "" && strings.TrimSpace(d.Version) == "" {
			v = pinned[c]
		}
		scope := ScopeMain
		switch strings.TrimSpace(d.Scope) {
		case "test":
			scope = ScopeTest
		case "import":
			scope = ScopeManaged
		}
		reqs = append(reqs, Dep{Coord: c, Version: v, Scope: scope})
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
// dependencies, peer and optional ones Main, devDependencies Dev.
func packageJSON(body []byte) ([]string, []Dep) {
	var p struct {
		Name     string            `json:"name"`
		Deps     map[string]string `json:"dependencies"`
		Dev      map[string]string `json:"devDependencies"`
		Peer     map[string]string `json:"peerDependencies"`
		Optional map[string]string `json:"optionalDependencies"`
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
		m     map[string]string
		scope string
	}{{p.Deps, ScopeMain}, {p.Peer, ScopeMain}, {p.Optional, ScopeMain}, {p.Dev, ScopeDev}} {
		for name, v := range s.m {
			if name = strings.TrimSpace(name); name != "" {
				reqs = append(reqs, Dep{Coord: NPM + ":" + name, Version: v, Scope: s.scope})
			}
		}
	}
	return prov, reqs
}
