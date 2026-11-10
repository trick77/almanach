package manifest

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"path"
	"regexp"
	"sort"
	"strings"
)

// The parts inside one repository and which uses which, as its builds
// declare them: a Gradle build's projects (`project(':x')`, `projects.x`, or
// a coordinate another project of the repository provides), a Maven
// build's modules, a workspace's members, several go.mod, pyproject.toml or
// Cargo.toml side by side. A part is a folder whose manifest provides a
// coordinate, or a Gradle project; a use is a requirement only one other
// part provides (two: ambiguous, no edge). What a build file alone cannot
// say (a project renamed in settings, a convention plugin's dependencies)
// is left out, never guessed. Code, no model.

// Unit is one part: its folder ("." the root), its name as its build calls
// it, and what it is when its build says so: KindApp (a Spring Boot or
// application plugin, a war, a package with a bin, a Python distribution
// with scripts, a crate with a [[bin]]) or KindLib (a java-library), else
// "".
type Unit struct {
	Dir  string `json:"dir"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// Unit kinds.
const (
	KindApp = "app"
	KindLib = "lib"
)

// InnerEdge is one part using another, by their folders, and what for: the
// strongest scope it is named with.
type InnerEdge struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Scope string `json:"scope"`
}

// Inner is the parts of a repository and its edges, each sorted. Empty
// when no part uses another: a repository of one part has nothing to show.
type Inner struct {
	Units []Unit      `json:"units"`
	Edges []InnerEdge `json:"edges"`
}

// UnitDir is a folder as a Unit names it: "." the root (""), any other as
// it stands. Every reader of parts names the root so (imports too).
func UnitDir(d string) string {
	if d == "" {
		return "."
	}
	return d
}

// Units reads the manifests given (path to body), as Parse does, for the
// parts inside the repository and their edges.
func Units(in map[string][]byte) Inner {
	return units(read(in))
}

// ParseAll is Parse with Units beside, the manifests read once.
func ParseAll(in map[string][]byte) Read {
	files, g, each := read(in)
	out := parse(files, g, each)
	out.Units = units(files, g, each)
	return out
}

func units(files map[string][]byte, g gradleRead, each map[string]fileRead) Inner {
	// A folder a Gradle build holds is that build's: a pom.xml there is
	// stale or generated (as Module.Reads has it).
	gradleDir := map[string]bool{}
	for d := range g.projects {
		gradleDir[d] = true
	}
	for p := range files {
		if b := path.Base(p); gradleBuild(b) || gradleSettings(b) {
			gradleDir[dirOf(p)] = true
		}
	}
	counts := func(p string) bool { return path.Base(p) != "pom.xml" || !gradleDir[dirOf(p)] }

	units := map[string]*Unit{}
	unit := func(d, name string) {
		if units[d] == nil {
			units[d] = &Unit{Dir: UnitDir(d)}
		}
		if units[d].Name == "" {
			units[d].Name = name
		}
	}
	providers := map[string]map[string]bool{}
	provide := func(d, c string) {
		if providers[c] == nil {
			providers[c] = map[string]bool{}
		}
		providers[c][d] = true
		unit(d, coordName(c))
	}
	for d, name := range g.projects {
		unit(d, name)
	}
	for d, cs := range g.prov {
		for _, c := range cs {
			provide(d, c)
		}
	}
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		if !counts(p) {
			continue
		}
		for _, c := range each[p].prov {
			provide(dirOf(p), c)
		}
	}

	edges := map[[2]string]string{}
	edge := func(from, to, scope string) {
		if from == to || units[from] == nil || units[to] == nil {
			return
		}
		scope = Norm(scope)
		k := [2]string{from, to}
		if cur, ok := edges[k]; !ok || Stronger(scope, cur) {
			edges[k] = scope
		}
	}
	for _, p := range paths {
		if !counts(p) {
			continue
		}
		from := dirOf(p)
		for _, d := range each[p].reqs {
			if to, ok := only(providers[d.Coord], from); ok {
				edge(from, to, d.Scope)
			}
		}
		for _, r := range g.refs[p] {
			edge(from, r.Dir, r.Scope)
		}
	}
	if len(edges) == 0 {
		return Inner{Units: []Unit{}, Edges: []InnerEdge{}}
	}

	out := Inner{Units: []Unit{}, Edges: []InnerEdge{}}
	for d, u := range units {
		u.Kind = kindOf(files, d, gradleDir[d])
		if u.Name == "" {
			u.Name = path.Base(UnitDir(d))
		}
		out.Units = append(out.Units, *u)
	}
	for k, s := range edges {
		out.Edges = append(out.Edges, InnerEdge{From: UnitDir(k[0]), To: UnitDir(k[1]), Scope: s})
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

// only is the one folder among dirs other than from; false for none or
// more than one (two parts providing one coordinate: which is meant, the
// code cannot tell).
func only(dirs map[string]bool, from string) (string, bool) {
	var to string
	n := 0
	for d := range dirs {
		if d != from {
			to = d
			n++
		}
	}
	return to, n == 1
}

// coordName is a coordinate's name as its build calls the part: a Maven
// artifactId, a package's or a crate's name, a Go module path's last
// element.
func coordName(c string) string {
	_, rest, _ := strings.Cut(c, ":")
	if i := strings.LastIndexByte(rest, ':'); i >= 0 {
		rest = rest[i+1:]
	}
	if strings.HasPrefix(c, Go+":") {
		rest = path.Base(rest)
	}
	return rest
}

var (
	// A plugin applied by id: id 'x', id("x"), id "x", apply plugin: 'x'.
	reGradlePluginID = regexp.MustCompile(`\b(?:id|plugin)[ \t]*(?:\(|:)?[ \t]*["']([A-Za-z0-9_.\-]+)["']`)
	// A core plugin by name in plugins { }: application, war, `java-library`.
	reGradleCorePlugin = regexp.MustCompile("(?m)^[ \\t]*(?:`([a-z\\-]+)`|(application|java|war))[ \\t]*$")
	// A plugin by its version catalog alias: alias(libs.plugins.spring.boot).
	reGradlePluginAlias = regexp.MustCompile(`\balias[ \t]*\([ \t]*libs\.plugins\.([A-Za-z0-9_.]+)`)
	// A Spring Boot application's own blocks.
	reGradleBoot = regexp.MustCompile(`\b(?:springBoot|bootJar|bootRun)[ \t]*\{`)
)

// bootAlias reports whether a catalog plugin alias names Spring Boot's
// plugin ("spring.boot", "springBoot", "spring-boot" as an accessor).
func bootAlias(acc string) bool {
	a := strings.ToLower(strings.ReplaceAll(acc, ".", ""))
	return a == "springboot" || strings.HasSuffix(a, "springboot")
}

// kindOf is what the part in folder d is, as its build says (Unit): its
// Gradle build file when gradle, else its manifests'.
func kindOf(files map[string][]byte, d string, gradle bool) string {
	at := func(base string) ([]byte, bool) {
		body, ok := files[path.Join(d, base)]
		return body, ok
	}
	if gradle {
		body, ok := at("build.gradle")
		if !ok {
			body, ok = at("build.gradle.kts")
		}
		if !ok {
			return ""
		}
		text := stripComments(body)
		ids := map[string]bool{}
		for _, m := range reGradlePluginID.FindAllStringSubmatch(text, -1) {
			ids[m[1]] = true
		}
		for _, m := range reGradleCorePlugin.FindAllStringSubmatch(text, -1) {
			ids[m[1]+m[2]] = true
		}
		for _, m := range reGradlePluginAlias.FindAllStringSubmatch(text, -1) {
			if bootAlias(m[1]) {
				ids["org.springframework.boot"] = true
			}
		}
		switch {
		case ids["org.springframework.boot"] || ids["application"] || ids["war"] || reGradleBoot.MatchString(text):
			return KindApp
		case ids["java-library"]:
			return KindLib
		}
		return ""
	}
	if body, ok := at("pom.xml"); ok {
		var p struct {
			Packaging string `xml:"packaging"`
			Plugins   []struct {
				ArtifactID string `xml:"artifactId"`
			} `xml:"build>plugins>plugin"`
		}
		dec := xml.NewDecoder(bytes.NewReader(body))
		dec.CharsetReader = charsetReader
		if dec.Decode(&p) == nil {
			if strings.TrimSpace(p.Packaging) == "war" {
				return KindApp
			}
			for _, pl := range p.Plugins {
				if strings.TrimSpace(pl.ArtifactID) == "spring-boot-maven-plugin" {
					return KindApp
				}
			}
		}
	}
	if body, ok := at("package.json"); ok {
		var p struct {
			Bin json.RawMessage `json:"bin"`
		}
		if json.Unmarshal(body, &p) == nil && len(p.Bin) > 0 && string(p.Bin) != "null" {
			return KindApp
		}
	}
	if body, ok := at("pyproject.toml"); ok {
		if doc, err := parseTOML(body); err == nil {
			project, _ := doc["project"].(map[string]any)
			if s, _ := project["scripts"].(map[string]any); len(s) > 0 {
				return KindApp
			}
		}
	}
	if body, ok := at("Cargo.toml"); ok {
		if doc, err := parseTOML(body); err == nil {
			if _, bin := doc["bin"]; bin {
				return KindApp
			}
		}
	}
	return ""
}
