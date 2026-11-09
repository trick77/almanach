package baseline

import (
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// What a run must read is code's to decide where code can tell, never the
// catalogue's alone (2026-11-07): a catalogue names whole folders, their
// files go in path order into a step's budget, the unit tests beside each
// component take it and the entry points and routing go unread; and a
// schema the whole repository is about can sit in no subject at all.
// Three rules: tests are left out (TestFile, in Files), anchor files are
// read first and always (AnchorRank, EnsureAnchors), and schemas are
// contracts, a WSDL or XSD one only when a build file names it
// (Schemas).

// testDirs are folders whose files are tests or test data, wherever they
// sit.
var testDirs = map[string]bool{
	"test": true, "tests": true, "__tests__": true, "__snapshots__": true, "testdata": true, "fixtures": true, "__mocks__": true,
}

// testName is a file named as a test by the conventions in use; jvmTest a
// JVM test class, by case (Latest.java is none); e2eDir an end-to-end
// folder by its name (e2e, shop-e2e, e2e_tests, any case); runnerConfig the
// config that says how end-to-end tests run, an anchor (entryName).
var (
	testName     = regexp.MustCompile(`(?i)(\.(spec|test)\.[a-z]+$|_test\.go$|_test\.py$|^test_.*\.py$)`)
	jvmTest      = regexp.MustCompile(`^[A-Za-z0-9_]+(Test|Tests|IT)\.(java|kt|groovy)$`)
	e2eDir       = regexp.MustCompile(`(?i)^(e2e|.+[-_]e2e|e2e[-_]tests?)$`)
	runnerConfig = `(playwright|cypress|wdio|protractor)\.conf(ig)?\.[a-z]+`
	runnerName   = regexp.MustCompile(`(?i)^` + runnerConfig + `$`)
)

// workspaceDirs are a monorepo's project folders: an e2e folder right in
// one is a project of its own (Nx apps/shop-e2e, libs/e2e).
var workspaceDirs = map[string]bool{"apps": true, "libs": true, "packages": true, "projects": true}

// TestFile reports whether a path is a test or test data by its name: a
// unit test (unitTest) or end-to-end test infrastructure (e2eFile).
func TestFile(p string) bool {
	return unitTest(p) || e2eFile(p)
}

// unitTest is a test file, a Maven/Gradle test source set, a folder of
// tests, snapshots or fixtures.
func unitTest(p string) bool {
	if strings.HasPrefix(p, "src/test/") || strings.Contains(p, "/src/test/") {
		return true
	}
	for _, d := range strings.Split(path.Dir(p), "/") {
		if testDirs[d] {
			return true
		}
	}
	base := path.Base(p)
	return testName.MatchString(base) || jvmTest.MatchString(base)
}

// e2eFile is a file of an end-to-end project: an e2e folder at the root or
// right in a workspace folder. Page objects, logins, stubs, test accounts
// say nothing of the product, and a router given them can make a subject of
// them alone. Deeper, an e2e folder is a package of the product
// (end-to-end encryption), never a suite. The project's runner config and
// build files stay: how the tests run and are built is what a reader needs
// (anchors).
func e2eFile(p string) bool {
	parts := strings.Split(p, "/")
	at := -1
	switch {
	case len(parts) > 1 && e2eDir.MatchString(parts[0]):
		at = 0
	case len(parts) > 2 && workspaceDirs[parts[0]] && e2eDir.MatchString(parts[1]):
		at = 1
	}
	if at < 0 {
		return false
	}
	base := parts[len(parts)-1]
	return !runnerName.MatchString(base) && !buildNames[base]
}

// keepTests reports whether a kind of tests stays readable: when its files
// are most of the tree, the repository is about them (a smoketest, a
// recording suite) and leaving them out would leave it nothing. Unit and
// end-to-end tests each on their own: an app's end-to-end project neither
// tips its unit tests over half nor rides in with them. By count, never
// bytes: recorded fixtures can outweigh the code and would keep every unit
// test in.
func keepTests(tests, all int) bool {
	return all > 0 && tests*2 >= all
}

// testKind is a file's kind of test: an end-to-end project's file first,
// a spec in one is the suite's.
type testKind int

const (
	noTest testKind = iota
	unitKind
	e2eKind
)

func kindOf(p string) testKind {
	switch {
	case e2eFile(p):
		return e2eKind
	case unitTest(p):
		return unitKind
	}
	return noTest
}

// Anchor ranks: higher is read first.
const (
	anchorBuild    = 3
	anchorEntry    = 4
	anchorContract = 5
)

// buildNames are build, container and pipeline files: how the repository
// is built, run and released.
var buildNames = map[string]bool{
	"pom.xml": true, "build.gradle": true, "build.gradle.kts": true, "settings.gradle": true, "settings.gradle.kts": true,
	"gradle.properties": true, "package.json": true, "nx.json": true, "angular.json": true, "go.mod": true, "Makefile": true,
	"Cargo.toml": true, "pyproject.toml": true, "Containerfile": true, "Dockerfile": true, "compose.yaml": true, "compose.yml": true,
	"compose.dev.yaml": true, "docker-compose.yml": true, "docker-compose.yaml": true, "kustomization.yaml": true, "kustomization.yml": true,
	"Chart.yaml": true, "values.yaml": true, "ship.yaml": true, "project.json": true, ".env.example": true,
}

// devConfig is a pipeline, test runner or dev server configuration by its
// name: how the code is built, tested and run locally. An entry point's
// rank, read first, but the build subject's to hold (buildAnchor).
var devConfig = `proxy.*\.conf\.(js|json)|vite\.config\.ts|` + runnerConfig + `|Jenkinsfile.*`

// entryName is an entry point, routing, security or runtime configuration
// by its name, or a devConfig; devName a devConfig alone.
var (
	entryName = regexp.MustCompile(`(?i)^(main\.(go|ts|py|rs)|.*Application\.(java|kt)|app\.module\.ts|app\.config\.ts|app\.routes\.ts|.*[-.]routing\.module\.ts|.*Security(Config|Configuration)?\.(java|kt)|application(-[a-z0-9]+)?\.(properties|ya?ml)|bootstrap\.(properties|ya?ml)|` + devConfig + `)$`)
	devName   = regexp.MustCompile(`(?i)^(` + devConfig + `)$`)
)

// contractExt are schema files: what a repository sends, receives or
// stores, by definition.
var contractExt = map[string]bool{".avsc": true, ".avdl": true, ".proto": true, ".bpmn": true, ".dmn": true}

// contractName is an API or event contract by its name.
var contractName = regexp.MustCompile(`(?i)(openapi|swagger|asyncapi).*\.(ya?ml|json)$`)

// AnchorRank is how surely a file is one every page about its repository
// rests on, 0 for none: contracts, entry points and runtime configuration,
// build and deploy files. Docs are the overview's (Docs), tests none.
func AnchorRank(p string) int {
	if TestFile(p) {
		return 0
	}
	base := path.Base(p)
	depth := strings.Count(p, "/")
	switch {
	case contractExt[strings.ToLower(path.Ext(base))], contractName.MatchString(base), resourceContract(p):
		return anchorContract
	case entryName.MatchString(base):
		return anchorEntry
	case buildNames[base] && depth <= 2, strings.HasSuffix(base, ".sh") && depth == 0:
		return anchorBuild
	}
	return 0
}

// resourceContract is a YAML spec in a JVM resources folder that is no
// runtime configuration: an OpenAPI or AsyncAPI file by another name.
func resourceContract(p string) bool {
	if !strings.Contains(p, "src/main/resources/") || strings.Count(p[strings.Index(p, "src/main/resources/")+len("src/main/resources/"):], "/") > 0 {
		return false
	}
	base := strings.ToLower(path.Base(p))
	if !strings.HasSuffix(base, ".yml") && !strings.HasSuffix(base, ".yaml") {
		return false
	}
	for _, runtime := range []string{"application", "bootstrap", "logback", "log4j", "messages", "banner", "i18n"} {
		if strings.HasPrefix(base, runtime) {
			return false
		}
	}
	return true
}

// buildAnchor reports whether an anchor is the build subject's
// (devSubject): a build, container, pipeline or deploy file (buildNames, a
// root script) or a devConfig. A CI workflow folder (.github/workflows) or
// a Helm template is no anchor at all: the router picks it or it goes
// unread.
func buildAnchor(p string) bool {
	switch AnchorRank(p) {
	case anchorBuild:
		return true
	case anchorEntry:
		return devName.MatchString(path.Base(p))
	}
	return false
}

// byAnchor orders files anchors first, by rank, a file named in contracts
// a contract, the rest in their order. Each rank once: a comparator
// calling AnchorRank ran its patterns n log n times over a large folder.
func byAnchor(files []File, contracts map[string]bool) {
	rank := make(map[string]int, len(files))
	for _, f := range files {
		rank[f.Path] = AnchorRank(f.Path)
		if contracts[f.Path] {
			rank[f.Path] = anchorContract
		}
	}
	sort.SliceStable(files, func(i, j int) bool { return rank[files[i].Path] > rank[files[j].Path] })
}

// AnchorSubject is the subject EnsureAnchors opens for anchors the
// catalogue left out.
const (
	AnchorSubject = "Einstiegspunkte, Build und Verträge"
	anchorPurpose = "Einstiegspunkte, Routing, Sicherheit, Build-, Deploy- und Laufzeitkonfiguration sowie Schnittstellenverträge, auf denen jede Beschreibung des Repositorys aufbaut."
)

// EnsureAnchors puts every anchor file (AnchorRank, or named in contracts)
// the plan holds in no subject, and the overview does not read (Docs), into
// a subject of its own, first, within StepMaxBytes by rank. With the plan at
// maxSubjects, each missing anchor joins the subject it fits best where one
// has room (placeAnchors). A slot for the rest, short of losing a file: two
// subjects other than the build subject join (as past the cap, compact),
// else the build subject takes the rest, else joins one. Only then does a
// subject give way (dropTo), going first, so an anchor only it held is
// missing and taken in, never lost with it.
func EnsureAnchors(p Plan, files []File, contracts map[string]bool, maxSubjects int) Plan {
	if maxSubjects <= 0 {
		return p
	}
	docs := map[string]bool{}
	for _, f := range Docs(files, DocsMaxBytes) {
		docs[f.Path] = true
	}
	missingFrom := func(subjects []Subject) []File {
		held := map[string]bool{}
		for _, s := range subjects {
			for _, f := range s.Files {
				held[f.Path] = true
			}
		}
		var out []File
		for _, f := range files {
			// Past a step no subject reads a file (index.add): one would
			// only cost a join for nothing.
			if !held[f.Path] && !docs[f.Path] && f.Size <= StepMaxBytes && (AnchorRank(f.Path) > 0 || contracts[f.Path]) {
				out = append(out, f)
			}
		}
		return out
	}
	kept := p.Subjects
	if len(kept) >= maxSubjects {
		missing := missingFrom(kept)
		if len(missing) == 0 {
			return p
		}
		byAnchor(missing, contracts)
		var rest []File
		if kept, rest = placeAnchors(kept, missing, false); len(rest) == 0 {
			p.Subjects = kept
			return p
		}
		if joined, ok := joinTo(kept, 0, maxSubjects-1, false); ok {
			kept = joined
		} else if kept, rest = placeAnchors(kept, rest, true); len(rest) == 0 {
			p.Subjects = kept
			return p
		} else if joined, ok := joinTo(kept, 0, maxSubjects-1, true); ok {
			kept = joined
		} else {
			kept = dropTo(kept, 0, maxSubjects-1)
		}
	}
	missing := missingFrom(kept)
	if len(missing) == 0 {
		p.Subjects = kept
		return p
	}
	byAnchor(missing, contracts)
	s := Subject{Name: AnchorSubject, Purpose: anchorPurpose}
	var used int64
	for _, f := range missing {
		if used+f.Size > StepMaxBytes {
			continue
		}
		used += f.Size
		s.Files = append(s.Files, f)
	}
	if len(s.Files) == 0 {
		return p
	}
	p.Subjects = append([]Subject{s}, kept...)
	return p
}

// placeAnchors puts each anchor, in their order, into the subject it fits
// best that has room: a build file (buildAnchor) the build subject
// (devSubject) first, any other the subject sharing most of its folders,
// the smallest on a tie, the build subject only with anywhere. The plan's
// last subject took them all once, and with the router's build subject
// last an entry point or a contract was read as build, apart from its
// feature. rest is the anchors no subject had room for; the subjects
// passed in are never changed.
func placeAnchors(subjects []Subject, anchors []File, anywhere bool) (out []Subject, rest []File) {
	out = append([]Subject{}, subjects...)
	size := make([]int64, len(out))
	dev := make([]bool, len(out))
	dirs := make([]map[string]bool, len(out))
	for i, s := range out {
		size[i], dev[i], dirs[i] = s.Bytes(), devSubject(s), map[string]bool{}
		for _, f := range s.Files {
			addFolders(dirs[i], f.Path)
		}
	}
	copied := make([]bool, len(out))
	for _, f := range anchors {
		build := buildAnchor(f.Path)
		share := make([]int, len(out))
		var order []int
		for i := range out {
			if !dev[i] || build || anywhere {
				share[i] = sharedFolders(dirs[i], f.Path)
				order = append(order, i)
			}
		}
		sort.SliceStable(order, func(x, y int) bool {
			a, b := order[x], order[y]
			switch {
			case dev[a] != dev[b]:
				return dev[a] == build
			case share[a] != share[b]:
				return share[a] > share[b]
			}
			return size[a] < size[b]
		})
		at := slices.IndexFunc(order, func(i int) bool { return size[i]+f.Size <= StepMaxBytes })
		if at < 0 {
			rest = append(rest, f)
			continue
		}
		i := order[at]
		if !copied[i] {
			out[i].Files, copied[i] = append([]File{}, out[i].Files...), true
		}
		out[i].Files = append(out[i].Files, f)
		size[i] += f.Size
		addFolders(dirs[i], f.Path)
	}
	return out, rest
}

// addFolders adds p's leading folders to set, "a" and "a/b" for a/b/c.go;
// a folder in set has its parents there too.
func addFolders(set map[string]bool, p string) {
	for d := path.Dir(p); d != "." && d != "/" && !set[d]; d = path.Dir(d) {
		set[d] = true
	}
}

// sharedFolders is how many of p's leading folders set holds.
func sharedFolders(set map[string]bool, p string) int {
	for d := path.Dir(p); d != "." && d != "/"; d = path.Dir(d) {
		if set[d] {
			return strings.Count(d, "/") + 1
		}
	}
	return 0
}

// wsdlLike are the schema files a build turns into code: read only when a
// build file names them.
func wsdlLike(p string) bool {
	ext := strings.ToLower(path.Ext(p))
	return ext == ".wsdl" || ext == ".xsd"
}

// HasSchemas reports whether a tree holds a WSDL or XSD: without one there
// is nothing for Schemas to hold to the builds, and no build file to read.
func HasSchemas(files []File) bool {
	for _, f := range files {
		if wsdlLike(f.Path) {
			return true
		}
	}
	return false
}

// namedIn reports whether text names a file by its base name, standing on
// its own: CustomerOrder.wsdl names no Order.wsdl.
func namedIn(text, name string) bool {
	for i := 0; ; {
		j := strings.Index(text[i:], name)
		if j < 0 {
			return false
		}
		at := i + j
		if at == 0 || !nameByte(text[at-1]) {
			return true
		}
		i = at + 1
	}
}

// nameByte is a byte a file name continues with.
func nameByte(b byte) bool {
	return b == '_' || b == '-' || b == '.' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// BuildFile is a build file Schemas reads to learn which schemas the build
// generates code from.
func BuildFile(p string) bool {
	switch path.Base(p) {
	case "pom.xml", "build.gradle", "build.gradle.kts":
		return true
	}
	return false
}

// Schemas holds WSDL and XSD files to what the builds name: a vendor
// delivers hundreds, the build generates from a few, and the rest is bulk
// that fills a step's budget while the services the build uses go unread
// (2026-11-07). A WSDL or XSD whose name a
// build file holds, in any case, stays and is a contract, so is an XSD of
// the same base name as a named WSDL (its own schema); every other one
// leaves files.
// When no build names any, all stay: nothing tells them apart.
func Schemas(files []File, builds map[string][]byte) (kept []File, contracts map[string]bool) {
	contracts = map[string]bool{}
	var text strings.Builder
	for _, b := range builds {
		text.Write(b)
		text.WriteByte('\n')
	}
	all := strings.ToLower(text.String())
	named := map[string]bool{}
	for _, f := range files {
		if wsdlLike(f.Path) && namedIn(all, strings.ToLower(path.Base(f.Path))) {
			named[f.Path] = true
		}
	}
	if len(named) == 0 {
		return files, contracts
	}
	// Not the named WSDL's whole folder: a vendor's delivery is one tree,
	// and its folder would let the bulk back in.
	wsdlBases := map[string]bool{}
	for p := range named {
		if strings.EqualFold(path.Ext(p), ".wsdl") {
			wsdlBases[strings.ToLower(strings.TrimSuffix(path.Base(p), path.Ext(p)))] = true
		}
	}
	for _, f := range files {
		if !wsdlLike(f.Path) {
			kept = append(kept, f)
			continue
		}
		base := strings.ToLower(strings.TrimSuffix(path.Base(f.Path), path.Ext(f.Path)))
		if named[f.Path] || (strings.EqualFold(path.Ext(f.Path), ".xsd") && wsdlBases[base]) {
			contracts[f.Path] = true
			kept = append(kept, f)
		}
	}
	return kept, contracts
}
