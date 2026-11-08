package baseline

import (
	"path"
	"regexp"
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

// entryName is an entry point, routing, security or runtime configuration
// by its name.
var entryName = regexp.MustCompile(`(?i)^(main\.(go|ts|py|rs)|.*Application\.(java|kt)|app\.module\.ts|app\.config\.ts|app\.routes\.ts|.*[-.]routing\.module\.ts|.*Security(Config|Configuration)?\.(java|kt)|application(-[a-z0-9]+)?\.(properties|ya?ml)|bootstrap\.(properties|ya?ml)|proxy.*\.conf\.(js|json)|vite\.config\.ts|` + runnerConfig + `|Jenkinsfile.*)$`)

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

// byAnchor orders files anchors first, by rank, the rest in their order.
// Each rank once: a comparator calling AnchorRank ran its patterns
// n log n times over a large folder.
func byAnchor(files []File) {
	rank := make(map[string]int, len(files))
	for _, f := range files {
		rank[f.Path] = AnchorRank(f.Path)
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
// maxSubjects, the missing anchors join its last subject where they fit;
// where not, that subject, the catalogue's least important, gives way.
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
			if !held[f.Path] && !docs[f.Path] && (AnchorRank(f.Path) > 0 || contracts[f.Path]) {
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
		last := kept[len(kept)-1]
		used, add := int64(0), int64(0)
		for _, f := range last.Files {
			used += f.Size
		}
		for _, f := range missing {
			add += f.Size
		}
		if used+add <= StepMaxBytes {
			p.Subjects = append(append([]Subject{}, kept[:len(kept)-1]...), Subject{Name: last.Name, Purpose: last.Purpose,
				Files: append(append([]File{}, last.Files...), missing...)})
			return p
		}
		// The subject that gives way goes first, so an anchor only it held
		// is missing and taken in, never lost with it.
		kept = kept[:maxSubjects-1]
	}
	missing := missingFrom(kept)
	if len(missing) == 0 {
		return p
	}
	rank := make(map[string]int, len(missing))
	for _, f := range missing {
		rank[f.Path] = AnchorRank(f.Path)
		if contracts[f.Path] {
			rank[f.Path] = anchorContract
		}
	}
	sort.SliceStable(missing, func(i, j int) bool { return rank[missing[i].Path] > rank[missing[j].Path] })
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
