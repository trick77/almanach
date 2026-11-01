// Package manifest reads what a repository's build manifests say it is and
// what it pulls in: a go.mod's module and requires, a pom.xml's
// coordinates and dependencies, a package.json's name and dependencies.
// Pure: bytes in, coordinates out, so every rule is a table test. Code
// derives the dependency graph from these, never a model.
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

// Ecosystems: the prefix of a coordinate.
const (
	Go    = "go"
	Maven = "maven"
	NPM   = "npm"
)

// Dep is one coordinate a manifest requires and the version it names, as
// written ("" when none is).
type Dep struct {
	Coord   string `json:"coord"`
	Version string `json:"version"`
}

// Read is what one repository's manifests say: the coordinates it provides
// and the ones it requires, each once, sorted. A coordinate it provides
// itself is no requirement (a monorepo's modules use each other).
type Read struct {
	Provides []string `json:"provides"`
	Requires []Dep    `json:"requires"`
}

// Is reports whether a path names a manifest this package reads, at the
// root or one level down: deeper ones are copies (a checked-out corpus, a
// fixture) and would make the repository provide what it only carries.
func Is(p string) bool {
	if strings.Count(p, "/") > 1 {
		return false
	}
	switch path.Base(p) {
	case "go.mod", "pom.xml", "package.json":
		return true
	}
	return false
}

// Parse reads the manifests given (path to body). A manifest that does not
// parse is left out; the rest still count.
func Parse(files map[string][]byte) Read {
	provides := map[string]bool{}
	requires := map[string]string{}
	add := func(coord, version string) {
		if v, ok := requires[coord]; !ok || v == "" {
			requires[coord] = version
		}
	}
	// By path, so the version two manifests name differently is always
	// the first one's.
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		body := files[p]
		var prov []string
		var reqs []Dep
		switch path.Base(p) {
		case "go.mod":
			prov, reqs = goMod(body)
		case "pom.xml":
			prov, reqs = pom(body)
		case "package.json":
			prov, reqs = packageJSON(body)
		}
		for _, c := range prov {
			provides[c] = true
		}
		for _, d := range reqs {
			add(d.Coord, d.Version)
		}
	}
	out := Read{Provides: []string{}, Requires: []Dep{}}
	for c := range provides {
		out.Provides = append(out.Provides, c)
	}
	for c, v := range requires {
		if !provides[c] {
			out.Requires = append(out.Requires, Dep{Coord: c, Version: v})
		}
	}
	sort.Strings(out.Provides)
	sort.Slice(out.Requires, func(i, j int) bool { return out.Requires[i].Coord < out.Requires[j].Coord })
	return out
}

// goMod is a go.mod's module and its requires, line by line: the module
// directive, require lines and require blocks. Comments, replace and
// exclude are left alone; an indirect require counts like any.
func goMod(body []byte) ([]string, []Dep) {
	var prov []string
	var reqs []Dep
	inBlock := false
	sc := bufio.NewScanner(bytes.NewReader(body))
	for sc.Scan() {
		line := sc.Text()
		if i := strings.Index(line, "//"); i >= 0 {
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
				reqs = append(reqs, Dep{Coord: Go + ":" + unquote(f[0]), Version: f[1]})
			}
		case f[0] == "module" && len(f) >= 2:
			prov = append(prov, Go+":"+unquote(f[1]))
		case f[0] == "require" && len(f) >= 2 && f[1] == "(":
			inBlock = true
		case f[0] == "require" && len(f) >= 3:
			reqs = append(reqs, Dep{Coord: Go + ":" + unquote(f[1]), Version: f[2]})
		}
	}
	return prov, reqs
}

func unquote(s string) string { return strings.Trim(s, "\"`") }

// pomXML is the part of a pom.xml read here.
type pomXML struct {
	GroupID      string   `xml:"groupId"`
	ArtifactID   string   `xml:"artifactId"`
	Parent       pomDep   `xml:"parent"`
	Dependencies []pomDep `xml:"dependencies>dependency"`
	Managed      []pomDep `xml:"dependencyManagement>dependencies>dependency"`
}

type pomDep struct {
	GroupID    string `xml:"groupId"`
	ArtifactID string `xml:"artifactId"`
	Version    string `xml:"version"`
}

// pom is a pom.xml's own coordinate (groupId from its parent when it has
// none) and its dependencies, managed ones included, and its parent: a
// parent POM is what most services of one platform share. A coordinate
// built of a property (`${...}`) is left out: unresolved, it would match
// nothing or the wrong thing.
func pom(body []byte) ([]string, []Dep) {
	var p pomXML
	dec := xml.NewDecoder(bytes.NewReader(body))
	dec.CharsetReader = charsetReader
	if err := dec.Decode(&p); err != nil {
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
	for _, d := range append(append([]pomDep{p.Parent}, p.Dependencies...), p.Managed...) {
		if c, ok := mavenCoord(d.GroupID, d.ArtifactID); ok {
			v := strings.TrimSpace(d.Version)
			if strings.Contains(v, "${") {
				v = ""
			}
			reqs = append(reqs, Dep{Coord: c, Version: v})
		}
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

// packageJSON is a package.json's name and the keys of its dependencies,
// dev and peer ones included.
func packageJSON(body []byte) ([]string, []Dep) {
	var p struct {
		Name string            `json:"name"`
		Deps map[string]string `json:"dependencies"`
		Dev  map[string]string `json:"devDependencies"`
		Peer map[string]string `json:"peerDependencies"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, nil
	}
	var prov []string
	if n := strings.TrimSpace(p.Name); n != "" {
		prov = append(prov, NPM+":"+n)
	}
	var reqs []Dep
	for _, m := range []map[string]string{p.Deps, p.Dev, p.Peer} {
		for name, v := range m {
			if name = strings.TrimSpace(name); name != "" {
				reqs = append(reqs, Dep{Coord: NPM + ":" + name, Version: v})
			}
		}
	}
	return prov, reqs
}
