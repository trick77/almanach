package baseline

import (
	"reflect"
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
	// At the cap the missing anchors join the last subject where they fit.
	joined := EnsureAnchors(p, files, nil, 2)
	if got := names(joined); !reflect.DeepEqual(got, []string{"Core:src/A.java", "Last:pom.xml,src/main/resources/application.yml,ev.avsc"}) {
		t.Errorf("at the cap, room in the last subject: %v", got)
	}
	if len(p.Subjects[1].Files) != 1 {
		t.Error("the plan passed in was changed")
	}
	// Where they do not, the last subject gives way; an anchor only it held
	// is taken in, unless the overview reads it (pom.xml, a manifest).
	big := Plan{Subjects: []Subject{p.Subjects[0], {Name: "Last", Files: []File{fl("src/main/resources/orders.yml", StepMaxBytes)}}}}
	full := EnsureAnchors(big, append(files, fl("src/main/resources/orders.yml", StepMaxBytes)), nil, 2)
	if got := names(full); !reflect.DeepEqual(got, []string{AnchorSubject + ":ev.avsc,src/main/resources/application.yml", "Core:src/A.java"}) {
		t.Errorf("at the cap, no room: %v", got)
	}
	// Nothing missing: the plan as it was.
	if same := EnsureAnchors(p, []File{fl("src/A.java", 100)}, nil, 5); len(same.Subjects) != 2 {
		t.Errorf("nothing to add: %v", names(same))
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
