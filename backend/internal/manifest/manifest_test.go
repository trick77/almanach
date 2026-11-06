package manifest

import (
	"reflect"
	"testing"
)

func TestIs(t *testing.T) {
	for p, want := range map[string]bool{
		"go.mod":                         true,
		"backend/go.mod":                 true,
		"ui/package.json":                true,
		"pom.xml":                        true,
		"eval-corpus/checkouts/x/go.mod": false,
		"eval-corpus/checkouts/go.mod":   false,
		"go.sum":                         false,
		"backend/package-lock.json":      false,
		"README.md":                      false,
	} {
		if got := Is(p); got != want {
			t.Errorf("Is(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestParse_GoMod(t *testing.T) {
	got := Parse(map[string][]byte{"backend/go.mod": []byte(`module github.com/trick77/rongo // the app

go 1.27

require github.com/trick77/llmwire v0.1.9-0.20261026072811-61ed84c8533a

require (
	github.com/ncruces/go-sqlite3 v0.36.0
	golang.org/x/sys v0.49.0 // indirect
)

replace github.com/trick77/webfetch => ../webfetch
`)})
	want := Read{
		Provides: []string{"go:github.com/trick77/rongo"},
		Requires: []Dep{
			{Coord: "go:github.com/ncruces/go-sqlite3", Version: "v0.36.0", Scope: ScopeMain},
			{Coord: "go:github.com/trick77/llmwire", Version: "v0.1.9-0.20261026072811-61ed84c8533a", Scope: ScopeMain},
			// A dependency's dependency.
			{Coord: "go:golang.org/x/sys", Version: "v0.49.0", Scope: ScopeIndirect},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse = %+v\nwant  %+v", got, want)
	}
}

func TestParse_Pom(t *testing.T) {
	got := Parse(map[string][]byte{"pom.xml": []byte(`<?xml version="1.0"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <parent><groupId>com.acme</groupId><artifactId>parent</artifactId><version>3.1</version></parent>
  <artifactId>checkout</artifactId>
  <dependencyManagement><dependencies>
    <dependency><groupId>com.acme</groupId><artifactId>bom</artifactId><version>2.0</version><type>pom</type><scope>import</scope></dependency>
    <dependency><groupId>com.acme</groupId><artifactId>pinned</artifactId><version>7.0</version></dependency>
    <dependency><groupId>org.junit</groupId><artifactId>junit</artifactId><version>5.10</version></dependency>
  </dependencies></dependencyManagement>
  <dependencies>
    <dependency><groupId>com.acme</groupId><artifactId>commons</artifactId><version>${commons.version}</version></dependency>
    <dependency><groupId>${project.groupId}</groupId><artifactId>sibling</artifactId></dependency>
    <dependency><groupId>org.slf4j</groupId><artifactId>slf4j-api</artifactId><version>2.0.9</version><scope>runtime</scope></dependency>
    <dependency><groupId>org.junit</groupId><artifactId>junit</artifactId><scope>test</scope></dependency>
  </dependencies>
</project>`)})
	want := Read{
		Provides: []string{"maven:com.acme:checkout"},
		Requires: []Dep{
			// The imported BOM and the parent only pin versions and share
			// build settings; a managed entry nothing names is no requirement,
			// one a dependency names gives it its version.
			{Coord: "maven:com.acme:bom", Version: "2.0", Scope: ScopeManaged},
			{Coord: "maven:com.acme:commons", Version: "", Scope: ScopeMain},
			{Coord: "maven:com.acme:parent", Version: "3.1", Scope: ScopeManaged},
			{Coord: "maven:org.junit:junit", Version: "5.10", Scope: ScopeTest},
			{Coord: "maven:org.slf4j:slf4j-api", Version: "2.0.9", Scope: ScopeMain},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse = %+v\nwant  %+v", got, want)
	}
}

// An older pom declares ISO-8859-1; it is read, its non-ASCII text
// included, not left out.
func TestParse_PomLatin1(t *testing.T) {
	body := append([]byte(`<?xml version="1.0" encoding="ISO-8859-1"?>
<project><groupId>com.acme</groupId><artifactId>legacy</artifactId><name>Gr`), 0xfc)
	body = append(body, []byte(`n</name>
  <dependencies><dependency><groupId>com.acme</groupId><artifactId>commons</artifactId><version>1.0</version></dependency></dependencies>
</project>`)...)
	got := Parse(map[string][]byte{"pom.xml": body})
	want := Read{Provides: []string{"maven:com.acme:legacy"}, Requires: []Dep{{Coord: "maven:com.acme:commons", Version: "1.0", Scope: ScopeMain}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse = %+v\nwant  %+v", got, want)
	}
}

// A multi-module repository's modules name its own root POM as parent:
// no requirement of itself.
func TestParse_PomOwnParent(t *testing.T) {
	got := Parse(map[string][]byte{
		"pom.xml":     []byte(`<project><groupId>com.acme</groupId><artifactId>platform</artifactId></project>`),
		"api/pom.xml": []byte(`<project><parent><groupId>com.acme</groupId><artifactId>platform</artifactId></parent><artifactId>api</artifactId></project>`),
	})
	if len(got.Requires) != 0 || len(got.Provides) != 2 {
		t.Fatalf("Parse = %+v, want two provided, none required", got)
	}
}

func TestParse_PackageJSONAndMonorepo(t *testing.T) {
	got := Parse(map[string][]byte{
		"package.json":     []byte(`{"name": "@acme/web", "dependencies": {"react": "^19.0.0", "@acme/ui": "workspace:*"}, "devDependencies": {"vitest": "4.0.0", "zod": "3"}, "optionalDependencies": {"fsevents": "2"}}`),
		"ui/package.json":  []byte(`{"name": "@acme/ui", "peerDependencies": {"react": ">=18"}, "dependencies": {"zod": "^3.2"}}`),
		"bad/package.json": []byte(`{not json`),
	})
	want := Read{
		Provides: []string{"npm:@acme/ui", "npm:@acme/web"},
		Requires: []Dep{
			// Optional: an extra's, none every install has.
			{Coord: "npm:fsevents", Version: "2", Scope: ScopeDev},
			{Coord: "npm:react", Version: "^19.0.0", Scope: ScopeMain},
			{Coord: "npm:vitest", Version: "4.0.0", Scope: ScopeDev},
			// Dev at the root, used by ui: the strongest counts, the first
			// version written.
			{Coord: "npm:zod", Version: "3", Scope: ScopeMain},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse = %+v\nwant  %+v", got, want)
	}
}

// A build's declared folders, at any depth, and by which tool: a Gradle
// include from the root build (each folder on its path a project too) and
// from one a folder down, a Maven module, one of its own, one in a profile.
// Build logic's projects, a folder outside the repository and a module
// naming a file are none.
func TestModules(t *testing.T) {
	got := Modules(map[string][]byte{
		"settings.gradle":          []byte("rootProject.name = 'svc'\ninclude 'lib:core', ':service:api'\n// include 'old:gone'\n"),
		"jvm/settings.gradle.kts":  []byte("include(\"tools:cli\")\n"),
		"buildSrc/settings.gradle": []byte("include 'conventions'\n"),
		"pom.xml":                  []byte(`<project><modules><module>parent/mod</module><module>../sibling</module><module>alt/pom-x.xml</module><module>lib/core</module><module>web/app/pom.xml</module></modules></project>`),
		"parent/mod/pom.xml":       []byte(`<project><modules><module>sub</module></modules><profiles><profile><modules><module>it</module></modules></profile></profiles></project>`),
		// A Gradle project's leftover pom declares no build's modules.
		"service/pom.xml": []byte(`<project><modules><module>old/stale</module></modules></project>`),
	}, nil)
	want := []Module{
		{Dir: "jvm/tools", Gradle: true},
		{Dir: "jvm/tools/cli", Gradle: true},
		{Dir: "lib", Gradle: true},
		{Dir: "lib/core", Gradle: true, Maven: true},
		{Dir: "parent/mod", Maven: true},
		{Dir: "parent/mod/it", Maven: true},
		{Dir: "parent/mod/sub", Maven: true},
		{Dir: "service", Gradle: true},
		{Dir: "service/api", Gradle: true},
		{Dir: "web/app", Maven: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Modules = %+v\nwant  %+v", got, want)
	}
}

// A module's files are its declaring tool's, never the other's.
func TestModuleReads(t *testing.T) {
	gradle, maven := Module{Gradle: true}, Module{Maven: true}
	for _, c := range []struct {
		m    Module
		base string
		want bool
	}{
		{gradle, "build.gradle", true}, {gradle, "build.gradle.kts", true}, {gradle, "gradle.properties", true},
		{gradle, "pom.xml", false}, {gradle, "settings.gradle", false}, {gradle, "package.json", false},
		{maven, "pom.xml", true}, {maven, "build.gradle", false}, {maven, "gradle.properties", false},
	} {
		if got := c.m.Reads(c.base); got != c.want {
			t.Errorf("%+v.Reads(%q) = %v, want %v", c.m, c.base, got, c.want)
		}
	}
}

// A multi-project build whose projects sit two folders down and whose root
// build file names no dependency: what it requires comes from the
// projects' build files, the group from the root's gradle.properties; a
// project dependency on a sibling is none.
func TestParse_GradleProjectsTwoDown(t *testing.T) {
	got := Parse(map[string][]byte{
		"settings.gradle":       []byte("rootProject.name = 'svc'\ninclude 'lib:core'\ninclude 'service:api'\n"),
		"gradle.properties":     []byte("group=example.svc\nversion=1.0.0-SNAPSHOT\n"),
		"build.gradle":          []byte("plugins {\n  id 'example-conventions'\n}\n"),
		"lib/core/build.gradle": []byte("dependencies {\n  implementation 'org.example:store:3.1'\n}\n"),
		"service/api/build.gradle": []byte(`dependencies {
    implementation project(':lib:core')
    implementation('example.flow:flow-engine:1.2:api') {
        transitive = false
    }
    implementation 'example.wire:wire-core:2.0'
}
`),
	})
	want := Read{
		Provides: []string{"maven:example.svc:api", "maven:example.svc:core", "maven:example.svc:svc"},
		Requires: []Dep{
			{Coord: "maven:example.flow:flow-engine", Version: "1.2", Scope: ScopeMain},
			{Coord: "maven:example.wire:wire-core", Version: "2.0", Scope: ScopeMain},
			{Coord: "maven:org.example:store", Version: "3.1", Scope: ScopeMain},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse = %+v\nwant  %+v", got, want)
	}
}

// A folder on an include's path is a project too: provided when it has a
// build file of its own, not when it only holds others.
func TestParse_GradleIntermediateProject(t *testing.T) {
	got := Parse(map[string][]byte{
		"settings.gradle":       []byte("rootProject.name = 'svc'\ninclude 'platform:bom', 'lib:core'\n"),
		"gradle.properties":     []byte("group=example.svc\n"),
		"platform/build.gradle": []byte("dependencies {\n  api platform('org.example:base-bom:2.0')\n}\n"),
	})
	want := Read{
		Provides: []string{"maven:example.svc:bom", "maven:example.svc:core", "maven:example.svc:platform", "maven:example.svc:svc"},
		Requires: []Dep{{Coord: "maven:org.example:base-bom", Version: "2.0", Scope: ScopeManaged}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse = %+v\nwant  %+v", got, want)
	}
}

// A build a folder down owns the projects it includes: their group and
// properties come from its folder, not the root's, and the root including
// the same folder provides it no second time.
func TestParse_GradleNestedBuildOwnsItsProjects(t *testing.T) {
	got := Parse(map[string][]byte{
		"settings.gradle":            []byte("include 'jvm:tools:cli'\n"),
		"gradle.properties":          []byte("group=example.root\n"),
		"jvm/settings.gradle":        []byte("rootProject.name = 'jvm'\ninclude 'tools:cli'\n"),
		"jvm/gradle.properties":      []byte("group=example.jvm\nwireVersion=2.1\n"),
		"jvm/tools/cli/build.gradle": []byte("dependencies {\n  implementation \"example.wire:wire-core:$wireVersion\"\n}\n"),
	})
	want := Read{
		Provides: []string{"maven:example.jvm:cli", "maven:example.jvm:jvm"},
		Requires: []Dep{{Coord: "maven:example.wire:wire-core", Version: "2.1", Scope: ScopeMain}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse = %+v\nwant  %+v", got, want)
	}
}
