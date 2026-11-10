package manifest

import (
	"reflect"
	"testing"
)

// A root build configuring other projects says nothing of its own uses; a
// path with no leading colon is the file's own project's child.
func TestUnits_GradleOthers(t *testing.T) {
	got := Units(map[string][]byte{
		"settings.gradle": []byte("rootProject.name = 'x'\ninclude ':common', ':a', ':a:b', ':web'\n"),
		"build.gradle": []byte(`subprojects {
    dependencies { implementation project(':common') }
}
project(':web') {
    dependencies { implementation project(':a') }
}
`),
		"common/build.gradle": []byte("plugins { id 'java-library' }\n"),
		"a/build.gradle":      []byte("dependencies { implementation project('b') }\n"),
		"a/b/build.gradle":    []byte("plugins { id 'java-library' }\n"),
		"web/build.gradle":    []byte("plugins { id 'java' }\n"),
	})
	want := []InnerEdge{{From: "a", To: "a/b", Scope: ScopeMain}}
	if !reflect.DeepEqual(got.Edges, want) {
		t.Fatalf("edges = %+v, want %+v", got.Edges, want)
	}
}

// An app by a version catalog alias or the war plugin; a dependency on
// Spring Boot is none.
func TestUnits_GradleKinds(t *testing.T) {
	got := Units(map[string][]byte{
		"settings.gradle.kts": []byte("rootProject.name = \"x\"\ninclude(\"web\", \"api\", \"core\")\n"),
		"web/build.gradle":    []byte("plugins {\n    war\n}\ndependencies { implementation project(':core') }\n"),
		"api/build.gradle.kts": []byte(`plugins {
    alias(libs.plugins.spring.boot)
}
dependencies { implementation(project(":core")) }
`),
		"core/build.gradle": []byte("dependencies { implementation 'org.springframework.boot:spring-boot-starter:3.0.0' }\n"),
	})
	kinds := map[string]string{}
	for _, u := range got.Units {
		kinds[u.Dir] = u.Kind
	}
	if want := map[string]string{"api": KindApp, "core": "", "web": KindApp}; !reflect.DeepEqual(kinds, want) {
		t.Fatalf("kinds = %v, want %v", kinds, want)
	}
	if got := ParseAll(map[string][]byte{"web/package.json": []byte(`{"name":"w","dependencies":{"k":"1"}}`),
		"kit/package.json": []byte(`{"name":"k"}`)}); len(got.Units.Edges) != 1 || len(got.Requires) != 0 {
		t.Fatalf("ParseAll = %+v, want one part's edge and nothing required outside", got)
	}
}

// A Groovy multi-project build of libraries and services: project
// dependencies with their scope, a type-safe accessor, a coordinate a
// sibling provides; a stale pom.xml in a project's folder counts for
// nothing; plugins tell an app from a library.
func TestUnits_Gradle(t *testing.T) {
	got := Units(map[string][]byte{
		"settings.gradle": []byte(`rootProject.name = 'platform'
include ':lib:core', ':lib:render-kit', ':service:api', ':service:worker'
`),
		"gradle.properties": []byte("group=org.example\n"),
		"build.gradle":      []byte("plugins { id 'base' }\n"),
		"lib/core/build.gradle": []byte(`plugins {
    id 'java-library'
}
`),
		"lib/render-kit/build.gradle": []byte(`plugins { id 'java-library' }
dependencies {
    api project(':lib:core')
}
`),
		"service/api/build.gradle": []byte(`plugins {
    id 'org.springframework.boot'
}
dependencies {
    implementation project(':lib:core')
    implementation(project(path: ":lib:render-kit"))
    testImplementation testFixtures(project(':service:worker'))
    implementation 'org.slf4j:slf4j-api:2.0.9'
}
`),
		"service/api/pom.xml": []byte(`<project><groupId>org.example</groupId><artifactId>api</artifactId>
<dependencies><dependency><groupId>org.example</groupId><artifactId>stale</artifactId></dependency></dependencies></project>`),
		"service/worker/build.gradle.kts": []byte(`plugins {
    application
}
dependencies {
    implementation(projects.lib.renderKit)
    implementation("org.example:core:1.0")
}
`),
	})
	want := Inner{
		Units: []Unit{
			{Dir: ".", Name: "platform"},
			{Dir: "lib/core", Name: "core", Kind: KindLib},
			{Dir: "lib/render-kit", Name: "render-kit", Kind: KindLib},
			{Dir: "service/api", Name: "api", Kind: KindApp},
			{Dir: "service/worker", Name: "worker", Kind: KindApp},
		},
		Edges: []InnerEdge{
			{From: "lib/render-kit", To: "lib/core", Scope: ScopeMain},
			{From: "service/api", To: "lib/core", Scope: ScopeMain},
			{From: "service/api", To: "lib/render-kit", Scope: ScopeMain},
			{From: "service/api", To: "service/worker", Scope: ScopeTest},
			{From: "service/worker", To: "lib/core", Scope: ScopeMain},
			{From: "service/worker", To: "lib/render-kit", Scope: ScopeMain},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Units =\n%+v\nwant\n%+v", got, want)
	}
}

// A Maven build: modules use each other by coordinate; the parent is a
// pin, no use; a Spring Boot plugin makes an app.
func TestUnits_Maven(t *testing.T) {
	got := Units(map[string][]byte{
		"pom.xml": []byte(`<project><groupId>org.example</groupId><artifactId>parent</artifactId>
<packaging>pom</packaging><modules><module>core</module><module>app</module></modules></project>`),
		"core/pom.xml": []byte(`<project><parent><groupId>org.example</groupId><artifactId>parent</artifactId></parent>
<artifactId>core</artifactId></project>`),
		"app/pom.xml": []byte(`<project><parent><groupId>org.example</groupId><artifactId>parent</artifactId></parent>
<artifactId>app</artifactId>
<dependencies><dependency><groupId>org.example</groupId><artifactId>core</artifactId></dependency></dependencies>
<build><plugins><plugin><groupId>org.springframework.boot</groupId><artifactId>spring-boot-maven-plugin</artifactId></plugin></plugins></build>
</project>`),
	})
	want := Inner{
		Units: []Unit{
			{Dir: ".", Name: "parent"},
			{Dir: "app", Name: "app", Kind: KindApp},
			{Dir: "core", Name: "core"},
		},
		Edges: []InnerEdge{
			{From: "app", To: ".", Scope: ScopeManaged},
			{From: "app", To: "core", Scope: ScopeMain},
			{From: "core", To: ".", Scope: ScopeManaged},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Units =\n%+v\nwant\n%+v", got, want)
	}
}

// Workspaces and modules side by side: npm by package name, uv by
// distribution, two go.mod by module path; scripts and a bin make an app.
func TestUnits_Workspaces(t *testing.T) {
	got := Units(map[string][]byte{
		"package.json":              []byte(`{"name": "root", "workspaces": ["packages/*"]}`),
		"packages/ui/package.json":  []byte(`{"name": "@x/ui", "dependencies": {"@x/kit": "workspace:*", "react": "18"}}`),
		"packages/kit/package.json": []byte(`{"name": "@x/kit", "bin": {"kit": "cli.js"}}`),
		"pyproject.toml":            []byte("[tool.uv.workspace]\nmembers = [\"py/*\"]\n"),
		"py/core/pyproject.toml":    []byte("[project]\nname = \"x-core\"\n"),
		"py/cli/pyproject.toml":     []byte("[project]\nname = \"x-cli\"\ndependencies = [\"x_core\", \"click\"]\n[project.scripts]\nx = \"x_cli:main\"\n"),
		"go.mod":                    []byte("module example.org/tool\n\nrequire example.org/tool/kit v0.0.0\n"),
		"kit/go.mod":                []byte("module example.org/tool/kit\n"),
	})
	want := Inner{
		Units: []Unit{
			{Dir: ".", Name: "tool"},
			{Dir: "kit", Name: "kit"},
			{Dir: "packages/kit", Name: "@x/kit", Kind: KindApp},
			{Dir: "packages/ui", Name: "@x/ui"},
			{Dir: "py/cli", Name: "x-cli", Kind: KindApp},
			{Dir: "py/core", Name: "x-core"},
		},
		Edges: []InnerEdge{
			{From: ".", To: "kit", Scope: ScopeMain},
			{From: "packages/ui", To: "packages/kit", Scope: ScopeMain},
			{From: "py/cli", To: "py/core", Scope: ScopeMain},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Units =\n%+v\nwant\n%+v", got, want)
	}
}

// One part, or parts that use none of each other: nothing to show. Two
// parts providing one coordinate: no edge to either.
func TestUnits_Nothing(t *testing.T) {
	for name, files := range map[string]map[string][]byte{
		"one part": {"go.mod": []byte("module example.org/one\n")},
		"no use": {
			"backend/go.mod":  []byte("module example.org/b\n"),
			"ui/package.json": []byte(`{"name": "ui"}`),
		},
		"ambiguous": {
			"a/package.json": []byte(`{"name": "x"}`),
			"b/package.json": []byte(`{"name": "x"}`),
			"c/package.json": []byte(`{"name": "c", "dependencies": {"x": "1"}}`),
		},
	} {
		if got := Units(files); len(got.Units) != 0 || len(got.Edges) != 0 {
			t.Errorf("%s: Units = %+v, want none", name, got)
		}
	}
}
