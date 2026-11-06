package manifest

import (
	"bufio"
	"bytes"
	"regexp"
	"sort"
	"strings"
)

// Python: a pyproject.toml names the distribution (PEP 621 `[project]`, or
// Poetry's `[tool.poetry]`) and what it requires; a requirements file only
// requires. A name is compared as PEP 503 normalises it (lower case, a run
// of `-`, `_` and `.` one `-`), so `Foo_Bar` and `foo-bar` meet:
// py:<name>.
//
// Scopes: `[project].dependencies` and Poetry's `dependencies` are Main;
// an extra (`optional-dependencies`, a Poetry `optional = true`), a PEP 735
// `[dependency-groups]` group and a Poetry group are Dev, Test when the
// group's name says test. An
// extra may be a runtime feature, but none is what every install pulls in:
// the maps stay with what surely runs.

// pyName is the start of a PEP 508 requirement: the distribution's name.
var pyName = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?`)

var pyNormalise = regexp.MustCompile(`[-_.]+`)

// pyCoord is a distribution's coordinate, its name normalised.
func pyCoord(name string) string {
	return Python + ":" + strings.ToLower(pyNormalise.ReplaceAllString(strings.TrimSpace(name), "-"))
}

// pep508 reads one requirement string ("requests[socks]>=2.31; python_version>'3.8'"):
// its coordinate and its version specifier as written, without extras and
// markers; a direct reference (`name @ url`) names no version. False when
// it names no distribution.
func pep508(s string) (Dep, bool) {
	s = strings.TrimSpace(s)
	name := pyName.FindString(s)
	if name == "" {
		return Dep{}, false
	}
	rest := s[len(name):]
	if i := strings.IndexByte(rest, ';'); i >= 0 {
		rest = rest[:i]
	}
	rest = strings.TrimSpace(rest)
	if strings.HasPrefix(rest, "[") {
		if i := strings.IndexByte(rest, ']'); i >= 0 {
			rest = strings.TrimSpace(rest[i+1:])
		}
	}
	if strings.HasPrefix(rest, "@") {
		rest = ""
	}
	rest = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(rest, "("), ")"))
	return Dep{Coord: pyCoord(name), Version: rest}, true
}

// groupScope is a dependency group's scope by its name: Test when it says
// test (test, tests, testing, unit-test), else Dev (dev, lint, docs).
func groupScope(name string) string {
	if strings.Contains(strings.ToLower(name), "test") {
		return ScopeTest
	}
	return ScopeDev
}

// pyproject is a pyproject.toml's distribution and requirements.
func pyproject(body []byte) ([]string, []Dep) {
	doc, err := parseTOML(body)
	if err != nil {
		return nil, nil
	}
	var prov []string
	var reqs []Dep
	// A dependency group may include another ({include-group = "x"}): that
	// group's entries are read where it stands.
	strs := func(v any, scope string) {
		for _, s := range tomlStrings(v) {
			if d, ok := pep508(s); ok {
				d.Scope = scope
				reqs = append(reqs, d)
			}
		}
	}
	project, _ := doc["project"].(map[string]any)
	if n, _ := project["name"].(string); strings.TrimSpace(n) != "" {
		prov = append(prov, pyCoord(n))
	}
	strs(project["dependencies"], ScopeMain)
	extras, _ := project["optional-dependencies"].(map[string]any)
	for _, g := range sortedKeys(extras) {
		strs(extras[g], groupScope(g))
	}
	groups, _ := doc["dependency-groups"].(map[string]any)
	for _, g := range sortedKeys(groups) {
		strs(groups[g], groupScope(g))
	}
	tool, _ := doc["tool"].(map[string]any)
	poetry, _ := tool["poetry"].(map[string]any)
	if n, _ := poetry["name"].(string); strings.TrimSpace(n) != "" && len(prov) == 0 {
		prov = append(prov, pyCoord(n))
	}
	table := func(v any, scope string) {
		m, _ := v.(map[string]any)
		for _, name := range sortedKeys(m) {
			spec := m[name]
			if strings.EqualFold(name, "python") || pyName.FindString(name) != name {
				continue
			}
			version, s := "", scope
			switch t := spec.(type) {
			case string:
				version = t
			case map[string]any:
				version, _ = t["version"].(string)
				// An optional one is an extra's, as in PEP 621.
				if o, _ := t["optional"].(string); o == "true" && s == ScopeMain {
					s = ScopeDev
				}
			}
			reqs = append(reqs, Dep{Coord: pyCoord(name), Version: strings.TrimSpace(version), Scope: s})
		}
	}
	table(poetry["dependencies"], ScopeMain)
	table(poetry["dev-dependencies"], ScopeDev)
	pgroups, _ := poetry["group"].(map[string]any)
	for _, g := range sortedKeys(pgroups) {
		if t, ok := pgroups[g].(map[string]any); ok {
			table(t["dependencies"], groupScope(g))
		}
	}
	return prov, reqs
}

// sortedKeys is a table's keys in order: a coordinate two groups name gets
// the same version every read.
func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// requirementsScope is the scope of a requirements file by its name:
// requirements.txt Main, requirements-dev.txt and dev-requirements.txt
// Dev, requirements-test.txt and test-requirements.txt Test. False for any
// other name: a requirements/*.txt tree or a lock file is no such file.
func requirementsScope(base string) (string, bool) {
	switch base {
	case "requirements.txt":
		return ScopeMain, true
	case "requirements-dev.txt", "dev-requirements.txt":
		return ScopeDev, true
	case "requirements-test.txt", "test-requirements.txt":
		return ScopeTest, true
	}
	return "", false
}

// directReference reports whether a requirement is PEP 508's `name @ url`
// (name[extras] first, then `@`): a name, unlike a bare URL.
func directReference(s string) bool {
	name := pyName.FindString(s)
	if name == "" {
		return false
	}
	rest := strings.TrimSpace(s[len(name):])
	if strings.HasPrefix(rest, "[") {
		if i := strings.IndexByte(rest, ']'); i >= 0 {
			rest = strings.TrimSpace(rest[i+1:])
		}
	}
	return strings.HasPrefix(rest, "@")
}

// requirements is a requirements file's requirements, one per line (a
// trailing `\` joins the next). Comments, options (`-r`, `-e`,
// `--index-url`) and a path or URL in place of a name are left out; a
// `name @ url` is the name's, no version.
func requirements(body []byte, scope string) []Dep {
	var reqs []Dep
	var line strings.Builder
	flush := func() {
		s := line.String()
		line.Reset()
		// A comment starts the line or follows a blank (pip's rule): a `#`
		// in a URL fragment is none.
		for i := 0; i < len(s); i++ {
			if s[i] == '#' && (i == 0 || s[i-1] == ' ' || s[i-1] == '\t') {
				s = s[:i]
				break
			}
		}
		// A per-requirement option (`--hash=...`) ends the requirement.
		if i := strings.Index(s, " -"); i >= 0 {
			s = s[:i]
		}
		if i := strings.Index(s, "\t-"); i >= 0 {
			s = s[:i]
		}
		s = strings.TrimSpace(s)
		if s == "" || strings.HasPrefix(s, "#") || strings.HasPrefix(s, "-") || strings.HasPrefix(s, ".") ||
			strings.HasPrefix(s, "/") || strings.Contains(s, "://") && !directReference(s) {
			return
		}
		if d, ok := pep508(s); ok {
			d.Scope = scope
			reqs = append(reqs, d)
		}
	}
	sc := bufio.NewScanner(bytes.NewReader(body))
	for sc.Scan() {
		t := sc.Text()
		if strings.HasSuffix(t, `\`) {
			line.WriteString(strings.TrimSuffix(t, `\`))
			line.WriteByte(' ')
			continue
		}
		line.WriteString(t)
		flush()
	}
	flush()
	return reqs
}
