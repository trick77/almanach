package manifest

import (
	"path"
	"regexp"
	"sort"
	"strings"
)

// Gradle: a build's files read together, since a build file alone does
// not say what its project is called. settings.gradle(.kts) names the root
// project (`rootProject.name`) and includes the others; a project's group
// is its build file's `group = "..."`, else gradle.properties' `group`,
// else the root project's. Its coordinate is maven:<group>:<name>, as
// Gradle publishes it, so a Gradle build and a Maven one meet; a
// publication renaming its artifactId is not seen. A build with no
// settings file, or no group, provides nothing: its name would be the
// clone's folder, its coordinate a guess.
//
// Requires are what a dependency configuration names: a literal
// "group:artifact[:version]", the map notation (group, name, version), or
// a version catalog accessor (`libs.x.y`, `libs.bundles.z`) resolved in
// gradle/libs.versions.toml. `$x` and `${x}` are resolved from
// gradle.properties and the build files' plain assignments (`ext`, `val`,
// `set(...)`, `extra[...]`). Build files are scripts: what only running one
// would tell (a coordinate built of an unresolved variable, a convention
// plugin's dependencies) is left out, never guessed.

// gradleCatalog is the version catalog Gradle reads without being told.
const gradleCatalog = "gradle/libs.versions.toml"

func gradleBuild(base string) bool { return base == "build.gradle" || base == "build.gradle.kts" }
func gradleSettings(base string) bool {
	return base == "settings.gradle" || base == "settings.gradle.kts"
}

var (
	// A Groovy or Kotlin string: double quoted with `${...}` taken whole
	// (it may hold quotes of its own), or single quoted.
	gradleStr = `(?:"((?:\$\{[^}\n]*\}|[^"\n])*)"|'([^'\n]*)')`
	// A configuration name, then (optionally) `(` and platform(...).
	gradleConf = `\b([A-Za-z_][A-Za-z0-9_]*)[ \t]*\(?\s*(?:(?:platform|enforcedPlatform|testFixtures)[ \t]*\(\s*)?`

	reGradleCoord   = regexp.MustCompile(gradleConf + gradleStr)
	reGradleMap     = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]*)[ \t]*\(?\s*group[ \t]*[:=][ \t]*` + gradleStr + `[ \t]*,\s*name[ \t]*[:=][ \t]*` + gradleStr + `(?:[ \t]*,\s*version[ \t]*[:=][ \t]*` + gradleStr + `)?`)
	reGradleCatalog = regexp.MustCompile(gradleConf + `libs\.([A-Za-z0-9_.]+)`)
	// A project's own group: the whole line, so a named argument
	// (`group = "x",`) inside a dependency is not one.
	reGradleGroup = regexp.MustCompile(`(?m)^[ \t]*(?:project\.)?group[ \t]*=?[ \t]*` + gradleStr + `[ \t]*;?[ \t]*$`)
	reRootName    = regexp.MustCompile(`rootProject\.name[ \t]*=[ \t]*` + gradleStr)
	reInclude     = regexp.MustCompile(`\binclude\b[ \t]*\(?((?:\s*` + gradleStr + `[ \t]*,?)+)`)
	reQuoted      = regexp.MustCompile(gradleStr)
	// Plain assignments a version may be taken from.
	reAssign = regexp.MustCompile(`(?m)^[ \t]*(?:(?:val|var|def|ext\.|project\.ext\.)[ \t]*)?([A-Za-z_][A-Za-z0-9_]*)(?:[ \t]*:[ \t]*String)?[ \t]*=[ \t]*` + gradleStr + `[ \t]*;?[ \t]*$`)
	reSet    = regexp.MustCompile(`\bset\([ \t]*` + gradleStr + `[ \t]*,[ \t]*` + gradleStr + `[ \t]*\)`)
	reExtra  = regexp.MustCompile(`\bextra\[[ \t]*` + gradleStr + `[ \t]*\][ \t]*=[ \t]*` + gradleStr)
	// `${property("x")}` and its kin, `${x}`, `$x`.
	reRef       = regexp.MustCompile(`\$\{[ \t]*(?:project\.|rootProject\.)?(?:property|findProperty)\([ \t]*"([^"]+)"[ \t]*\)(?:[ \t]*as[ \t]+String\??)?[ \t]*\}|\$\{[ \t]*([A-Za-z_][A-Za-z0-9_.]*)[ \t]*\}|\$([A-Za-z_][A-Za-z0-9_]*)`)
	reCoordPart = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.\-]*$`)
)

// gradleConfs are the dependency configurations by name; any name ending
// in one of gradleConfSuffixes is one too (testImplementation,
// integrationTestRuntimeOnly, testFixturesApi).
var gradleConfs = map[string]bool{
	"implementation": true, "api": true, "compileOnly": true, "runtimeOnly": true,
	"annotationProcessor": true, "compile": true, "runtime": true, "testCompile": true,
	"testRuntime": true, "providedCompile": true, "providedRuntime": true, "classpath": true,
	"developmentOnly": true, "kapt": true, "ksp": true, "mavenBom": true,
}

var gradleConfSuffixes = []string{"Implementation", "Api", "CompileOnly", "RuntimeOnly", "AnnotationProcessor"}

func gradleConfName(name string) bool {
	if gradleConfs[name] {
		return true
	}
	for _, s := range gradleConfSuffixes {
		if strings.HasSuffix(name, s) && len(name) > len(s) {
			return true
		}
	}
	for _, p := range []string{"kapt", "ksp"} {
		if strings.HasPrefix(name, p) && len(name) > len(p) && name[len(p)] >= 'A' && name[len(p)] <= 'Z' {
			return true
		}
	}
	return false
}

// strMatch is the content of the string a gradleStr group pair matched.
func strMatch(m []string, i int) string {
	if m[i] != "" {
		return m[i]
	}
	return m[i+1]
}

type gradleDir struct {
	props    map[string]string // gradle.properties
	vars     map[string]string // the build file's plain assignments
	build    string            // the build file, comments out
	settings string            // the settings file, comments out
	hasSet   bool
}

// gradle reads the Gradle files among files: the coordinates their builds
// provide, and per build file what it requires.
func gradle(files map[string][]byte) ([]string, map[string][]Dep) {
	dirs := map[string]*gradleDir{}
	dir := func(d string) *gradleDir {
		if dirs[d] == nil {
			dirs[d] = &gradleDir{props: map[string]string{}, vars: map[string]string{}}
		}
		return dirs[d]
	}
	var cat *catalog
	builds := map[string]string{}
	for p, body := range files {
		d := path.Dir(p)
		if d == "." {
			d = ""
		}
		switch b := path.Base(p); {
		case p == gradleCatalog:
			cat = readCatalog(body)
		case b == "gradle.properties":
			dir(d).props = properties(body)
		case gradleBuild(b):
			text := stripComments(body)
			builds[p] = text
			g := dir(d)
			g.build += text + "\n"
			for k, v := range assignments(text) {
				g.vars[k] = v
			}
		case gradleSettings(b):
			g := dir(d)
			g.settings += stripComments(body) + "\n"
			g.hasSet = true
		}
	}
	if len(dirs) == 0 {
		return nil, nil
	}
	// A folder one down is its own build when it has a settings file;
	// otherwise it is (at most) a project of the root build.
	rootOf := func(d string) string {
		if g := dirs[d]; d != "" && g != nil && g.hasSet {
			return d
		}
		return ""
	}
	// What `$x` resolves to in folder d: gradle.properties of the build
	// root, then the folder's, then the build root's assignments, then the
	// folder's; a later one wins.
	propsOf := func(d string) map[string]string {
		out := map[string]string{}
		r := rootOf(d)
		for _, src := range []string{r, d} {
			if g := dirs[src]; g != nil {
				for k, v := range g.props {
					out[k] = v
				}
			}
		}
		for _, src := range []string{r, d} {
			if g := dirs[src]; g != nil {
				for k, v := range g.vars {
					out[k] = v
				}
			}
		}
		return out
	}

	var prov []string
	for d, g := range dirs {
		if !g.hasSet {
			continue
		}
		props := propsOf(d)
		rootGroup := buildGroup(g.build, props)
		if rootGroup == "" {
			rootGroup = g.props["group"]
		}
		if c, ok := gradleCoord(rootGroup, firstStr(reRootName, g.settings)); ok {
			prov = append(prov, c)
		}
		for _, inc := range includes(g.settings) {
			segs := strings.FieldsFunc(inc, func(r rune) bool { return r == ':' })
			if len(segs) == 0 {
				continue
			}
			group := ""
			pd := path.Join(d, strings.Join(segs, "/"))
			if sub := dirs[pd]; sub != nil && rootOf(pd) == d {
				group = buildGroup(sub.build, propsOf(pd))
				if group == "" {
					group = sub.props["group"]
				}
			}
			if group == "" {
				group = rootGroup
			}
			if c, ok := gradleCoord(group, segs[len(segs)-1]); ok {
				prov = append(prov, c)
			}
		}
	}
	reqs := map[string][]Dep{}
	for p, text := range builds {
		d := path.Dir(p)
		if d == "." {
			d = ""
		}
		var c *catalog
		if rootOf(d) == "" {
			c = cat
		}
		reqs[p] = buildDeps(text, propsOf(d), c)
	}
	sort.Strings(prov)
	return prov, reqs
}

// buildDeps is what one build file's dependency configurations name, in
// the order written.
func buildDeps(text string, props map[string]string, cat *catalog) []Dep {
	type hit struct {
		at  int
		dep Dep
	}
	var hits []hit
	for _, m := range reGradleCoord.FindAllStringSubmatchIndex(text, -1) {
		s := subs(text, m)
		if !gradleConfName(s[1]) {
			continue
		}
		parts := strings.Split(resolve(strMatch(s, 2), props), ":")
		if len(parts) < 2 || len(parts) > 4 {
			continue
		}
		// An artifact type (`@aar`) ends the last part.
		last := len(parts) - 1
		if i := strings.IndexByte(parts[last], '@'); i >= 0 {
			parts[last] = parts[last][:i]
		}
		version := ""
		if len(parts) >= 3 {
			version = parts[2]
		}
		if c, ok := gradleCoord(parts[0], parts[1]); ok {
			hits = append(hits, hit{m[0], Dep{Coord: c, Version: cleanVersion(version)}})
		}
	}
	for _, m := range reGradleMap.FindAllStringSubmatchIndex(text, -1) {
		s := subs(text, m)
		if !gradleConfName(s[1]) {
			continue
		}
		if c, ok := gradleCoord(resolve(strMatch(s, 2), props), resolve(strMatch(s, 4), props)); ok {
			hits = append(hits, hit{m[0], Dep{Coord: c, Version: cleanVersion(resolve(strMatch(s, 6), props))}})
		}
	}
	if cat != nil {
		for _, m := range reGradleCatalog.FindAllStringSubmatchIndex(text, -1) {
			s := subs(text, m)
			if !gradleConfName(s[1]) {
				continue
			}
			for _, d := range cat.lookup(s[2]) {
				hits = append(hits, hit{m[0], d})
			}
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].at < hits[j].at })
	out := make([]Dep, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.dep)
	}
	return out
}

// subs is the submatches of one FindAllStringSubmatchIndex hit, "" for a
// group that took no part.
func subs(text string, m []int) []string {
	out := make([]string, len(m)/2)
	for i := range out {
		if m[2*i] >= 0 {
			out[i] = text[m[2*i]:m[2*i+1]]
		}
	}
	return out
}

// gradleCoord is a Maven coordinate of literal parts only: a part still
// holding a `$` (unresolved) or anything a coordinate cannot is no
// coordinate.
func gradleCoord(group, artifact string) (string, bool) {
	group, artifact = strings.TrimSpace(group), strings.TrimSpace(artifact)
	if !reCoordPart.MatchString(group) || !reCoordPart.MatchString(artifact) {
		return "", false
	}
	return mavenCoord(group, artifact)
}

// cleanVersion is a version as written, "" when part of it is unresolved.
func cleanVersion(v string) string {
	v = strings.TrimSpace(v)
	if strings.Contains(v, "$") {
		return ""
	}
	return v
}

// buildGroup is a build file's own `group = "..."`, resolved; "" when it
// names none or one not resolved.
func buildGroup(text string, props map[string]string) string {
	m := reGradleGroup.FindStringSubmatch(text)
	if m == nil {
		return ""
	}
	g := strings.TrimSpace(resolve(strMatch(m, 1), props))
	if strings.Contains(g, "$") {
		return ""
	}
	return g
}

// includes is every project path a settings file includes.
func includes(settings string) []string {
	var out []string
	for _, m := range reInclude.FindAllStringSubmatch(settings, -1) {
		for _, q := range reQuoted.FindAllStringSubmatch(m[1], -1) {
			if s := strings.TrimSpace(strMatch(q, 1)); s != "" && !strings.Contains(s, "$") {
				out = append(out, s)
			}
		}
	}
	return out
}

func firstStr(re *regexp.Regexp, text string) string {
	m := re.FindStringSubmatch(text)
	if m == nil {
		return ""
	}
	return strMatch(m, 1)
}

// assignments is a build file's plain string assignments: `x = "1.0"`,
// `val x = "1.0"`, `ext.x = '1.0'`, `set("x", "1.0")`, `extra["x"] = "1.0"`.
// One that interpolates is no plain value and left out.
func assignments(text string) map[string]string {
	out := map[string]string{}
	put := func(k, v string) {
		if k != "" && !strings.Contains(v, "$") {
			out[k] = v
		}
	}
	for _, m := range reAssign.FindAllStringSubmatch(text, -1) {
		put(m[1], strMatch(m, 2))
	}
	for _, re := range []*regexp.Regexp{reSet, reExtra} {
		for _, m := range re.FindAllStringSubmatch(text, -1) {
			put(strMatch(m, 1), strMatch(m, 3))
		}
	}
	return out
}

// resolve replaces each `$x`, `${x}` and `${property("x")}` it finds in
// props; one it does not find stays as written.
func resolve(s string, props map[string]string) string {
	if !strings.Contains(s, "$") {
		return s
	}
	return reRef.ReplaceAllStringFunc(s, func(ref string) string {
		m := reRef.FindStringSubmatch(ref)
		name := m[1]
		if name == "" {
			name = m[2]
		}
		if name == "" {
			name = m[3]
		}
		name = strings.TrimPrefix(strings.TrimPrefix(name, "project."), "rootProject.")
		if v, ok := props[name]; ok {
			return v
		}
		return ref
	})
}

// stripComments is a Groovy or Kotlin script with its `//` and `/* */`
// comments blanked, newlines kept, strings left whole: a dependency
// commented out is none.
func stripComments(src []byte) string {
	s := string(src)
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		switch {
		case strings.HasPrefix(s[i:], "//"):
			for i < len(s) && s[i] != '\n' {
				i++
			}
		case strings.HasPrefix(s[i:], "/*"):
			end := strings.Index(s[i+2:], "*/")
			stop := len(s)
			if end >= 0 {
				stop = i + 2 + end + 2
			}
			b.WriteString(strings.Repeat("\n", strings.Count(s[i:stop], "\n")))
			b.WriteByte(' ')
			i = stop
		case s[i] == '"' || s[i] == '\'':
			j := stringEnd(s, i)
			b.WriteString(s[i:j])
			i = j
		default:
			b.WriteByte(s[i])
			i++
		}
	}
	return b.String()
}

// stringEnd is the index just past the string literal opening at i: one
// quote or three, backslash escapes, and in a double-quoted one `${...}`
// taken whole. Unclosed: the end of s.
func stringEnd(s string, i int) int {
	q := s[i]
	delim := string(q)
	if strings.HasPrefix(s[i:], strings.Repeat(delim, 3)) {
		delim = strings.Repeat(delim, 3)
	}
	j := i + len(delim)
	for j < len(s) {
		switch {
		case s[j] == '\\':
			j += 2
		case len(delim) == 1 && s[j] == '\n':
			return j
		case q == '"' && strings.HasPrefix(s[j:], "${"):
			depth := 0
			for j < len(s) {
				if s[j] == '{' {
					depth++
				} else if s[j] == '}' {
					depth--
					if depth == 0 {
						j++
						break
					}
				} else if s[j] == '\n' && len(delim) == 1 {
					return j
				}
				j++
			}
		case strings.HasPrefix(s[j:], delim):
			return j + len(delim)
		default:
			j++
		}
	}
	return len(s)
}

// catalog is a version catalog's libraries and bundles by accessor: the
// alias with `-` and `_` as `.`, as a build file writes it after `libs.`.
type catalog struct {
	libs    map[string]Dep
	bundles map[string][]string
}

func catalogAccessor(alias string) string {
	return strings.NewReplacer("-", ".", "_", ".").Replace(alias)
}

// readCatalog reads gradle/libs.versions.toml; nil when it does not parse.
// A library is "group:artifact[:version]" or a table of module (or group
// and name) and version: a string, a `ref` into [versions], or a rich
// version (require, strictly, prefer).
func readCatalog(body []byte) *catalog {
	doc, err := parseTOML(body)
	if err != nil {
		return nil
	}
	versions, _ := doc["versions"].(map[string]any)
	var versionOf func(v any, depth int) string
	versionOf = func(v any, depth int) string {
		switch t := v.(type) {
		case string:
			return t
		case map[string]any:
			if ref, ok := t["ref"].(string); ok && depth == 0 {
				return versionOf(versions[ref], depth+1)
			}
			for _, k := range []string{"require", "strictly", "prefer"} {
				if s, ok := t[k].(string); ok {
					return s
				}
			}
		}
		return ""
	}
	c := &catalog{libs: map[string]Dep{}, bundles: map[string][]string{}}
	libs, _ := doc["libraries"].(map[string]any)
	for alias, v := range libs {
		var group, artifact, version string
		switch t := v.(type) {
		case string:
			parts := strings.Split(t, ":")
			if len(parts) < 2 {
				continue
			}
			group, artifact = parts[0], parts[1]
			if len(parts) >= 3 {
				version = parts[2]
			}
		case map[string]any:
			if mod, ok := t["module"].(string); ok {
				group, artifact, _ = strings.Cut(mod, ":")
			} else {
				group, _ = t["group"].(string)
				artifact, _ = t["name"].(string)
			}
			version = versionOf(t["version"], 0)
		}
		if coord, ok := gradleCoord(group, artifact); ok {
			c.libs[catalogAccessor(alias)] = Dep{Coord: coord, Version: cleanVersion(version)}
		}
	}
	bundles, _ := doc["bundles"].(map[string]any)
	for alias, v := range bundles {
		list, _ := v.([]any)
		for _, e := range list {
			if s, ok := e.(string); ok {
				c.bundles[catalogAccessor(alias)] = append(c.bundles[catalogAccessor(alias)], catalogAccessor(s))
			}
		}
	}
	return c
}

// lookup is what an accessor after `libs.` stands for: a library, or a
// bundle's libraries. The longest prefix that names one counts, so a
// trailing `.get()` or a longer alias sharing a prefix reads right.
// Plugins and versions are no dependency.
func (c *catalog) lookup(acc string) []Dep {
	segs := strings.Split(strings.Trim(acc, "."), ".")
	if len(segs) == 0 {
		return nil
	}
	switch segs[0] {
	case "plugins", "versions":
		return nil
	case "bundles":
		for n := len(segs); n > 1; n-- {
			if libs, ok := c.bundles[strings.Join(segs[1:n], ".")]; ok {
				var out []Dep
				for _, l := range libs {
					if d, ok := c.libs[l]; ok {
						out = append(out, d)
					}
				}
				return out
			}
		}
		return nil
	}
	for n := len(segs); n > 0; n-- {
		if d, ok := c.libs[strings.Join(segs[:n], ".")]; ok {
			return []Dep{d}
		}
	}
	return nil
}
