package baseline

import (
	"reflect"
	"slices"
	"testing"

	"github.com/trick77/almanach/internal/gitrepo"
	"github.com/trick77/almanach/internal/inference"
)

func TestTestFile(t *testing.T) {
	for p, want := range map[string]bool{
		"apps/web/src/app/app.component.spec.ts":  true,
		"src/util.test.js":                        true,
		"internal/store/store_test.go":            true,
		"src/main/java/a/FooTest.java":            true, // no test set, but the name
		"src/test/java/a/Foo.java":                true,
		"svc/src/test/resources/x.json":           true,
		"lib/src/main/java/a/RepoIT.java":         true,
		"tests/test_api.py":                       true,
		"pkg/testdata/golden.json":                true,
		"e2e/fixtures/a.json":                     true,
		"e2e/playwright.config.ts":                false,
		"apps/shop-e2e/playwright.config.ts":      false,
		"apps/shop-e2e/test-utils.ts":             true,
		"libs/e2e/src/login.ts":                   true,
		"libs/e2e/src/page.po.ts":                 true,
		"apps/e2etools/src/main.ts":               false,
		"apps/shop-e2e/cypress.config.ts":         false, // a runner config, an anchor
		"apps/shop-e2e/project.json":              false, // a build file, an anchor
		"e2e/Playwright.config.mjs":               false,
		"E2E/helpers.ts":                          true,
		"e2e_tests/login.ts":                      true,
		"tests-e2e/stub.ts":                       true,
		"src/main/java/a/chat/e2e/Cipher.java":    false, // end-to-end encryption
		"internal/crypto/e2e/session.go":          false,
		"packages/e2e-encryption/src/cipher.ts":   false,
		"mypkg/e2e/keys.py":                       false,
		"apps/shop-e2e/src/main.ts":               true, // the suite's, no entry point of the product
		"test/helpers.js":                         true,
		"tests/conftest.py":                       true,
		"src/__tests__/a.ts":                      true,
		"src/main/java/a/Latest.java":             false,
		"src/main/java/a/TestDataController.java": false,
		"README.md":                               false,
	} {
		if got := TestFile(p); got != want {
			t.Errorf("TestFile(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestFilesLeavesTestsOutUnlessMostly(t *testing.T) {
	// Mostly code by count, though the recorded fixtures outweigh it.
	mostlyCode := []gitrepo.Entry{
		{Path: "src/app.ts", Mode: "100644", Size: 900},
		{Path: "src/routes.ts", Mode: "100644", Size: 400},
		{Path: "src/app.spec.ts", Mode: "100644", Size: 300},
		{Path: "e2e/fixtures/run.json", Mode: "100644", Size: 900_000},
		{Path: "src/main.ts", Mode: "100644", Size: 100},
	}
	if got := Files(mostlyCode); len(got) != 3 || TestFile(got[0].Path) || TestFile(got[1].Path) || TestFile(got[2].Path) {
		t.Errorf("Files = %v, want the test and the fixture left out", got)
	}
	mostlyTests := []gitrepo.Entry{
		{Path: "README.md", Mode: "100644", Size: 100},
		{Path: "tests/login.spec.ts", Mode: "100644", Size: 900},
	}
	if got := Files(mostlyTests); len(got) != 2 {
		t.Errorf("Files = %v, want a test suite's tests kept", got)
	}
	// Unit tests short of half, an end-to-end folder beside them: together
	// past half, but neither kind is most, so both go.
	app := []gitrepo.Entry{
		{Path: "src/a.ts", Mode: "100644", Size: 100},
		{Path: "src/b.ts", Mode: "100644", Size: 100},
		{Path: "src/a.spec.ts", Mode: "100644", Size: 100},
		{Path: "src/b.spec.ts", Mode: "100644", Size: 100},
		{Path: "src/c.spec.ts", Mode: "100644", Size: 100},
		{Path: "apps/shop-e2e/login.ts", Mode: "100644", Size: 100},
		{Path: "apps/shop-e2e/order.ts", Mode: "100644", Size: 100},
		{Path: "apps/shop-e2e/playwright.config.ts", Mode: "100644", Size: 100},
	}
	if got := Files(app); len(got) != 3 {
		t.Errorf("Files = %v, want the code and the runner config alone", got)
	}
	suite := []gitrepo.Entry{
		{Path: "README.md", Mode: "100644", Size: 100},
		{Path: "e2e/login.ts", Mode: "100644", Size: 100},
		{Path: "e2e/order.ts", Mode: "100644", Size: 100},
	}
	if got := Files(suite); len(got) != 3 {
		t.Errorf("Files = %v, want an end-to-end suite kept", got)
	}
	// A suite's specs are the suite's, never unit tests splitting its vote.
	specs := []gitrepo.Entry{
		{Path: "README.md", Mode: "100644", Size: 100},
		{Path: "package.json", Mode: "100644", Size: 100},
		{Path: "playwright.config.ts", Mode: "100644", Size: 100},
		{Path: "e2e/login.spec.ts", Mode: "100644", Size: 100},
		{Path: "e2e/order.spec.ts", Mode: "100644", Size: 100},
		{Path: "e2e/pages/login.po.ts", Mode: "100644", Size: 100},
		{Path: "e2e/support/helpers.ts", Mode: "100644", Size: 100},
	}
	if got := Files(specs); len(got) != 7 {
		t.Errorf("Files = %v, want the suite whole", got)
	}
	// End-to-end files most of the tree: they stay, the unit tests go.
	mostlyE2E := []gitrepo.Entry{
		{Path: "src/a.ts", Mode: "100644", Size: 100},
		{Path: "src/a.spec.ts", Mode: "100644", Size: 100},
		{Path: "e2e/one.ts", Mode: "100644", Size: 100},
		{Path: "e2e/two.ts", Mode: "100644", Size: 100},
		{Path: "e2e/three.ts", Mode: "100644", Size: 100},
	}
	got := Files(mostlyE2E)
	if len(got) != 4 || slices.ContainsFunc(got, func(f File) bool { return f.Path == "src/a.spec.ts" }) {
		t.Errorf("Files = %v, want the suite and the code, no unit test", got)
	}
}

func TestAnchorRank(t *testing.T) {
	for p, want := range map[string]int{
		"schemas/event.avsc":                                anchorContract,
		"api/openapi.yaml":                                  anchorContract,
		"service/x/src/main/resources/orders.yml":           anchorContract,
		"service/x/src/main/resources/application.yml":      anchorEntry,
		"service/x/src/main/resources/application-prod.yml": anchorEntry,
		"src/main/resources/workflow/main-process.bpmn":     anchorContract,
		"cmd/app/main.go":                                   anchorEntry,
		"src/main/java/a/ShopApplication.java":              anchorEntry,
		"apps/web/src/app/app.module.ts":                    anchorEntry,
		"web/src/app/app-routing.module.ts":                 anchorEntry,
		"src/main/java/a/config/WebSecurityConfig.java":     anchorEntry,
		"src/main/java/a/security/SecurityUtils.java":       0,
		"src/main/resources/messages.yml":                   0,
		"proxy.conf.json":                                   anchorEntry,
		"Jenkinsfile":                                       anchorEntry,
		"pom.xml":                                           anchorBuild,
		"service/x/build.gradle":                            anchorBuild,
		"a/b/c/pom.xml":                                     0,
		"release.sh":                                        anchorBuild,
		"scripts/dev.sh":                                    0,
		"deploy/live/kustomization.yaml":                    anchorBuild,
		"src/main/java/a/Service.java":                      0,
		"src/app.spec.ts":                                   0,
	} {
		if got := AnchorRank(p); got != want {
			t.Errorf("AnchorRank(%q) = %d, want %d", p, got, want)
		}
	}
}

func TestEnsureAnchors(t *testing.T) {
	files := []File{fl("pom.xml", 10), fl("src/A.java", 100), fl("src/main/resources/application.yml", 20), fl("ev.avsc", 5), fl("x.wsdl", 7)}
	p := Plan{Subjects: []Subject{{Name: "Core", Files: []File{fl("src/A.java", 100)}}, {Name: "Last", Files: []File{fl("pom.xml", 10)}}}}
	got := EnsureAnchors(p, files, map[string]bool{"x.wsdl": true}, 5)
	if len(got.Subjects) != 3 || got.Subjects[0].Name != AnchorSubject {
		t.Fatalf("subjects = %v, want the anchor subject first", names(got))
	}
	var paths []string
	for _, f := range got.Subjects[0].Files {
		paths = append(paths, f.Path)
	}
	// Contracts first, then entry points; pom.xml is held already.
	if !reflect.DeepEqual(paths, []string{"ev.avsc", "x.wsdl", "src/main/resources/application.yml"}) {
		t.Errorf("anchor files = %v", paths)
	}
	// At the cap the missing anchors join the subjects they fit best: the
	// one sharing a folder, else the smallest.
	joined := EnsureAnchors(p, files, nil, 2)
	if got := names(joined); !reflect.DeepEqual(got, []string{"Core:src/A.java,src/main/resources/application.yml", "Last:pom.xml,ev.avsc"}) {
		t.Errorf("at the cap, room in the subjects: %v", got)
	}
	if len(p.Subjects[0].Files) != 1 {
		t.Error("the plan passed in was changed")
	}
	if len(p.Subjects[1].Files) != 1 {
		t.Error("the plan passed in was changed")
	}
	// A joined last subject keeps the names it stands for.
	jp := Plan{Subjects: []Subject{p.Subjects[0], {Name: "A und B", Files: []File{fl("pom.xml", 10)}, Joined: []string{"A", "B"}}}}
	if got := EnsureAnchors(jp, files, nil, 2); !reflect.DeepEqual(got.Subjects[1].Joined, []string{"A", "B"}) {
		t.Errorf("joined last subject = %+v, want its names kept", got.Subjects[1])
	}
	// Where they do not, the last subject gives way; an anchor only it held
	// is taken in, unless the overview reads it (pom.xml, a manifest).
	big := Plan{Subjects: []Subject{{Name: "Core", Files: []File{fl("src/A.java", StepMaxBytes)}},
		{Name: "Last", Files: []File{fl("src/main/resources/orders.yml", StepMaxBytes)}}}}
	full := EnsureAnchors(big, append(files, fl("src/main/resources/orders.yml", StepMaxBytes)), nil, 2)
	if got := names(full); !reflect.DeepEqual(got, []string{AnchorSubject + ":ev.avsc,src/main/resources/application.yml", "Core:src/A.java"}) {
		t.Errorf("at the cap, no room: %v", got)
	}
	// The anchors that fit are placed; one that fits no subject has two
	// small subjects join for its slot, every subject's files still read.
	three := Plan{Subjects: []Subject{p.Subjects[0], {Name: "Small", Files: []File{fl("src/B.java", 50)}},
		{Name: "Last", Files: []File{fl("src/main/resources/orders.yml", StepMaxBytes)}}}}
	roomy := EnsureAnchors(three, append(files, fl("src/B.java", 50), fl("src/main/resources/orders.yml", StepMaxBytes),
		fl("api/openapi.yaml", StepMaxBytes-40), fl("api/huge.proto", StepMaxBytes+1)), nil, 3)
	if got := names(roomy); !reflect.DeepEqual(got, []string{AnchorSubject + ":api/openapi.yaml",
		"Core und Small:src/A.java,src/B.java,ev.avsc,src/main/resources/application.yml", "Last:src/main/resources/orders.yml"}) {
		t.Errorf("at the cap, two small subjects: %v", got)
	}
	// Nothing missing: the plan as it was.
	if same := EnsureAnchors(p, []File{fl("src/A.java", 100)}, nil, 5); len(same.Subjects) != 2 {
		t.Errorf("nothing to add: %v", names(same))
	}
}

// At the cap the build subject, last by the router's rule, takes the build
// files: a pipeline, a deploy manifest, a test runner's config. An entry
// point or a contract goes to its feature, by folder, else to the smallest
// feature, never to the build subject while a feature has room.
func TestEnsureAnchorsSparesTheBuildSubject(t *testing.T) {
	files := []File{
		fl("orders/src/OrderService.java", 300), fl("orders/src/openapi.yaml", 20), fl("billing/Invoice.java", 100),
		fl("ev.avsc", 5), fl("deploy/live/kustomization.yaml", 10), fl("Jenkinsfile", 8), fl("orders/playwright.config.ts", 4),
		fl(".gitlab-ci.yml", 30),
	}
	p := Plan{Subjects: []Subject{
		{Name: "Bestellungen", Files: []File{fl("orders/src/OrderService.java", 300)}},
		{Name: "Rechnungen", Files: []File{fl("billing/Invoice.java", 100)}},
		{Name: LastArea, Files: []File{fl(".gitlab-ci.yml", 30)}},
	}}
	got := EnsureAnchors(p, files, nil, 3)
	if want := []string{"Bestellungen:orders/src/OrderService.java,orders/src/openapi.yaml", "Rechnungen:billing/Invoice.java,ev.avsc",
		LastArea + ":.gitlab-ci.yml,Jenkinsfile,orders/playwright.config.ts,deploy/live/kustomization.yaml"}; !reflect.DeepEqual(names(got), want) {
		t.Errorf("subjects = %v, want %v", names(got), want)
	}
	// A build file with no room in the build subject goes to a feature, by
	// folder too.
	full := p
	full.Subjects = append([]Subject{}, p.Subjects...)
	full.Subjects[2] = Subject{Name: LastArea, Files: []File{fl(".gitlab-ci.yml", StepMaxBytes)}}
	got = EnsureAnchors(full, files, nil, 3)
	if want := []string{"Bestellungen:orders/src/OrderService.java,orders/src/openapi.yaml,orders/playwright.config.ts",
		"Rechnungen:billing/Invoice.java,ev.avsc,Jenkinsfile,deploy/live/kustomization.yaml",
		LastArea + ":.gitlab-ci.yml"}; !reflect.DeepEqual(names(got), want) {
		t.Errorf("no room in the build subject: %v, want %v", names(got), want)
	}
	// The build subject alone takes them all: no feature to take any.
	alone := EnsureAnchors(Plan{Subjects: p.Subjects[2:]}, []File{fl(".gitlab-ci.yml", 30), fl("ev.avsc", 5)}, nil, 1)
	if want := []string{LastArea + ":.gitlab-ci.yml,ev.avsc"}; !reflect.DeepEqual(names(alone), want) {
		t.Errorf("build subject alone: %v, want %v", names(alone), want)
	}
}

// Where the features have no room and no two join, the build subject takes
// an entry point rather than lose a subject; where it has no room either,
// it joins one; only where nothing joins does a subject give way, a
// feature before the build subject, wherever it stands.
func TestEnsureAnchorsLosesNoSubjectForTheBuildOne(t *testing.T) {
	a := Subject{Name: "A", Files: []File{fl("a/A.java", StepMaxBytes)}}
	b := Subject{Name: "B", Files: []File{fl("b/B.java", StepMaxBytes)}}
	build := Subject{Name: LastArea, Files: []File{fl("ci.yaml", 40), fl("helm/templates/deployment.yaml", 60)}}
	files := []File{a.Files[0], b.Files[0], build.Files[0], build.Files[1], fl("src/main/resources/application.yml", 10)}
	got := EnsureAnchors(Plan{Subjects: []Subject{a, b, build}}, files, nil, 3)
	if want := []string{"A:a/A.java", "B:b/B.java", LastArea + ":ci.yaml,helm/templates/deployment.yaml,src/main/resources/application.yml"}; !reflect.DeepEqual(names(got), want) {
		t.Errorf("features full: %v, want %v", names(got), want)
	}
	// No room in the build subject either: it joins the feature it fits.
	small := Subject{Name: "C", Files: []File{fl("c/C.java", 100)}}
	big := Subject{Name: LastArea, Files: []File{fl("ci.yaml", StepMaxBytes-200)}}
	got = EnsureAnchors(Plan{Subjects: []Subject{small, big}}, []File{small.Files[0], big.Files[0], fl("api/orders.proto", StepMaxBytes-50)}, nil, 2)
	if want := []string{AnchorSubject + ":api/orders.proto", LastArea + " und C:ci.yaml,c/C.java"}; !reflect.DeepEqual(names(got), want) {
		t.Errorf("build subject full: %v, want %v", names(got), want)
	}
	// Nothing joins: the feature gives way, though the build subject is last.
	fullBuild := Subject{Name: LastArea, Files: []File{fl("ci.yaml", StepMaxBytes)}}
	got = EnsureAnchors(Plan{Subjects: []Subject{a, fullBuild}}, []File{a.Files[0], fullBuild.Files[0], fl("api/orders.proto", 50)}, nil, 2)
	if want := []string{AnchorSubject + ":api/orders.proto", LastArea + ":ci.yaml"}; !reflect.DeepEqual(names(got), want) {
		t.Errorf("nothing joins: %v, want %v", names(got), want)
	}
}

func TestBuildAnchor(t *testing.T) {
	for p, want := range map[string]bool{
		"pom.xml": true, "Containerfile": true, "deploy/live/kustomization.yaml": true, "release.sh": true,
		"Jenkinsfile": true, "Jenkinsfile.release": true, "e2e/playwright.config.ts": true, "vite.config.ts": true,
		"main.go": false, "src/main/resources/application.yml": false, "api/openapi.yaml": false, "src/A.java": false,
	} {
		if got := buildAnchor(p); got != want {
			t.Errorf("buildAnchor(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestSchemasHeldToTheBuild(t *testing.T) {
	files := []File{
		fl("wsdl/Order.wsdl", 10), fl("wsdl/schema/Order.xsd", 10), fl("wsdl/Unused.wsdl", 10),
		fl("wsdl/codes.xsd", 900), fl("src/A.java", 10), fl("build.gradle", 10),
	}
	kept, contracts := Schemas(files, map[string][]byte{"build.gradle": []byte(`wsdls = ["Order.wsdl"]`)})
	var paths []string
	for _, f := range kept {
		paths = append(paths, f.Path)
	}
	if !reflect.DeepEqual(paths, []string{"wsdl/Order.wsdl", "wsdl/schema/Order.xsd", "src/A.java", "build.gradle"}) {
		t.Errorf("kept = %v", paths)
	}
	if !contracts["wsdl/Order.wsdl"] || !contracts["wsdl/schema/Order.xsd"] || len(contracts) != 2 {
		t.Errorf("contracts = %v", contracts)
	}
	// A longer name holding a shorter one names only itself.
	_, longer := Schemas(files, map[string][]byte{"build.gradle": []byte(`wsdls = ["CustomerOrder.wsdl", "x/Unused.wsdl"]`)})
	if longer["wsdl/Order.wsdl"] || !longer["wsdl/Unused.wsdl"] {
		t.Errorf("CustomerOrder.wsdl named: contracts %v, want Unused.wsdl alone", longer)
	}
	if _, lower := Schemas(files, map[string][]byte{"pom.xml": []byte("<wsdl>order.wsdl</wsdl>")}); !lower["wsdl/Order.wsdl"] || !lower["wsdl/schema/Order.xsd"] {
		t.Errorf("named in another case: contracts %v", lower)
	}
	if !HasSchemas(files) || HasSchemas(files[4:]) {
		t.Error("HasSchemas: want true with a WSDL, false without")
	}
	// No build names any: all stay, none a contract.
	all, none := Schemas(files, map[string][]byte{"build.gradle": []byte("plugins {}")})
	if len(all) != len(files) || len(none) != 0 {
		t.Errorf("unnamed: kept %d of %d, contracts %v", len(all), len(files), none)
	}
}

func TestBuildFile(t *testing.T) {
	for p, want := range map[string]bool{"pom.xml": true, "a/build.gradle": true, "build.gradle.kts": true, "settings.gradle": false, "package.json": false} {
		if BuildFile(p) != want {
			t.Errorf("BuildFile(%q) = %v", p, !want)
		}
	}
}

// A folder the catalogue names gives its anchors first, so the step's
// budget takes them before what sorts early.
func TestFolderGivesAnchorsFirst(t *testing.T) {
	files := []File{fl("app/a-helper.ts", 10), fl("app/app.module.ts", 10), fl("app/b.ts", 10)}
	p := BuildPlan([]inference.Subject{{Name: "App", Paths: []string{"app/"}}}, files, 5)
	if got := names(p); !reflect.DeepEqual(got, []string{"App:app/app.module.ts,app/a-helper.ts,app/b.ts"}) {
		t.Errorf("plan = %v", got)
	}
}
