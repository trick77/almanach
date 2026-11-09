package manifest

import (
	"reflect"
	"testing"
)

func TestIs_Ecosystems(t *testing.T) {
	for p, want := range map[string]bool{
		"pyproject.toml":           true,
		"svc/pyproject.toml":       true,
		"requirements.txt":         true,
		"requirements-dev.txt":     true,
		"dev-requirements.txt":     true,
		"test-requirements.txt":    true,
		"requirements/base.txt":    false,
		"requirements-lock.txt":    false,
		"Cargo.toml":               true,
		"crates/Cargo.toml":        true,
		"Cargo.lock":               false,
		"go.work":                  true,
		"pnpm-workspace.yaml":      true,
		"a/b/pyproject.toml":       false,
		"vendor/x/y/Cargo.toml":    false,
		"pnpm-lock.yaml":           false,
		"setup.py":                 false,
		"web/pnpm-workspace.yaml":  true,
		"tools/x/requirements.txt": false,
	} {
		if got := Is(p); got != want {
			t.Errorf("Is(%q) = %v, want %v", p, got, want)
		}
	}
}

// Gradle configurations by what they are for: a test source set's, an
// annotation processor's, a platform's apart from the uses.
func TestParse_GradleScopes(t *testing.T) {
	got := Parse(map[string][]byte{
		"build.gradle.kts": []byte(`dependencies {
    implementation("a:main:1")
    compileOnly("a:provided:1")
    debugImplementation("a:variant:1")
    testImplementation("a:test:1")
    integrationTestRuntimeOnly("a:it:1")
    androidTestImplementation("a:android-test:1")
    testFixturesApi("a:fixtures:1")
    implementation(testFixtures("a:wrapped-fixtures:1"))
    annotationProcessor("a:processor:1")
    kapt("a:kapt:1")
    kaptTest("a:kapt-test:1")
    developmentOnly("a:devtools:1")
    implementation(enforcedPlatform("a:bom:1"))
    latestImplementation("a:latest:1")
}
`),
	})
	want := []Dep{
		{Coord: "maven:a:android-test", Scope: ScopeTest},
		{Coord: "maven:a:bom", Scope: ScopeManaged},
		{Coord: "maven:a:devtools", Scope: ScopeDev},
		{Coord: "maven:a:fixtures", Scope: ScopeTest},
		{Coord: "maven:a:it", Scope: ScopeTest},
		{Coord: "maven:a:kapt", Scope: ScopeDev},
		{Coord: "maven:a:kapt-test", Scope: ScopeTest},
		// A source set called latest is no test one.
		{Coord: "maven:a:latest", Scope: ScopeMain},
		{Coord: "maven:a:main", Scope: ScopeMain},
		{Coord: "maven:a:processor", Scope: ScopeDev},
		// Never on the runtime classpath.
		{Coord: "maven:a:provided", Scope: ScopeDev},
		{Coord: "maven:a:test", Scope: ScopeTest},
		{Coord: "maven:a:variant", Scope: ScopeMain},
		{Coord: "maven:a:wrapped-fixtures", Scope: ScopeTest},
	}
	if !reflect.DeepEqual(got.Requires, want) {
		t.Fatalf("Requires = %+v\nwant %+v", got.Requires, want)
	}
}

// A multi-module build pins scopes in the root POM; a module naming none
// takes them, its own pin first, as Maven inherits them.
func TestParse_PomInheritedPins(t *testing.T) {
	got := Parse(map[string][]byte{
		"pom.xml": []byte(`<project><groupId>com.acme</groupId><artifactId>parent</artifactId>
  <dependencyManagement><dependencies>
    <dependency><groupId>com.acme</groupId><artifactId>lib</artifactId><version>1.0</version></dependency>
    <dependency><groupId>com.acme</groupId><artifactId>testkit</artifactId><version>2.0</version><scope>test</scope></dependency>
    <dependency><groupId>com.acme</groupId><artifactId>wire</artifactId><version>3.0</version></dependency>
  </dependencies></dependencyManagement>
</project>`),
		"svc/pom.xml": []byte(`<project><parent><groupId>com.acme</groupId><artifactId>parent</artifactId></parent><artifactId>svc</artifactId>
  <dependencyManagement><dependencies>
    <dependency><groupId>com.acme</groupId><artifactId>wire</artifactId><version>3.1</version></dependency>
  </dependencies></dependencyManagement>
  <dependencies>
    <dependency><groupId>com.acme</groupId><artifactId>lib</artifactId></dependency>
    <dependency><groupId>com.acme</groupId><artifactId>testkit</artifactId></dependency>
    <dependency><groupId>com.acme</groupId><artifactId>wire</artifactId></dependency>
  </dependencies>
</project>`),
	})
	want := []Dep{
		{Coord: "maven:com.acme:lib", Scope: ScopeMain},
		{Coord: "maven:com.acme:testkit", Scope: ScopeTest},
		{Coord: "maven:com.acme:wire", Scope: ScopeMain},
	}
	if !reflect.DeepEqual(got.Requires, want) {
		t.Fatalf("Requires = %+v\nwant %+v", got.Requires, want)
	}

	// A module whose parent is no POM of this repository inherits nothing
	// from the folder above.
	got = Parse(map[string][]byte{
		"pom.xml": []byte(`<project><groupId>com.acme</groupId><artifactId>aggregator</artifactId>
  <dependencyManagement><dependencies>
    <dependency><groupId>com.acme</groupId><artifactId>testkit</artifactId><version>2.0</version><scope>test</scope></dependency>
  </dependencies></dependencyManagement>
</project>`),
		"ext/pom.xml": []byte(`<project><parent><groupId>org.other</groupId><artifactId>base</artifactId><version>1</version></parent>
  <groupId>com.acme</groupId><artifactId>ext</artifactId>
  <dependencies><dependency><groupId>com.acme</groupId><artifactId>testkit</artifactId></dependency></dependencies>
</project>`),
	})
	want = []Dep{
		{Coord: "maven:com.acme:testkit", Scope: ScopeMain},
		{Coord: "maven:org.other:base", Scope: ScopeManaged},
	}
	if !reflect.DeepEqual(got.Requires, want) {
		t.Fatalf("external parent: Requires = %+v\nwant %+v", got.Requires, want)
	}
}

func TestParse_Pyproject(t *testing.T) {
	got := Parse(map[string][]byte{
		"pyproject.toml": []byte(`[project]
name = "Acme_Wire.Client"
dependencies = [
  "requests[socks]>=2.31,<3; python_version >= '3.9'",
  "acme-core (==1.2)",
  "vendored @ https://example.invalid/vendored.tar.gz",
]

[project.optional-dependencies]
postgres = ["psycopg>=3"]
test = ["pytest>=8"]

[dependency-groups]
lint = ["ruff==0.6"]
tests = ["coverage", {include-group = "lint"}]
`),
		"tool/pyproject.toml": []byte(`[tool.poetry]
name = "acme-tool"

[tool.poetry.dependencies]
python = "^3.11"
Acme_Wire_Client = "^1.0"
click = { version = "^8.1", optional = true }

[tool.poetry.group.dev.dependencies]
mypy = "^1.0"

[tool.poetry.group.test.dependencies]
pytest = "^8"
`),
	})
	want := Read{
		// acme-tool requires the root's own distribution: no requirement of
		// itself.
		Provides: []string{"py:acme-tool", "py:acme-wire-client"},
		Requires: []Dep{
			{Coord: "py:acme-core", Scope: ScopeMain},
			// Optional: an extra's, as in PEP 621.
			{Coord: "py:click", Scope: ScopeDev},
			{Coord: "py:coverage", Scope: ScopeTest},
			{Coord: "py:mypy", Scope: ScopeDev},
			{Coord: "py:psycopg", Scope: ScopeDev},
			{Coord: "py:pytest", Scope: ScopeTest},
			{Coord: "py:requests", Scope: ScopeMain},
			{Coord: "py:ruff", Scope: ScopeDev},
			{Coord: "py:vendored", Scope: ScopeMain},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse = %+v\nwant  %+v", got, want)
	}
}

func TestParse_Requirements(t *testing.T) {
	got := Parse(map[string][]byte{
		"requirements.txt": []byte(`# pinned
-r base.txt
--index-url https://pypi.example.invalid/simple
-e ./local
Django==5.0 \
    --hash=sha256:abc
acme.core>=1.2  # ours
git+https://example.invalid/x.git#egg=x
./wheels/thing.whl
acme-wire[async] @ git+https://git.example.invalid/acme/wire.git@v1
`),
		"requirements-dev.txt": []byte("pytest\nDjango\n"),
		// A site builder's, no runtime's.
		"docs/requirements.txt": []byte("sphinx-acme-theme\n"),
	})
	want := []Dep{
		{Coord: "py:acme-core", Scope: ScopeMain},
		// A direct reference names its distribution.
		{Coord: "py:acme-wire", Scope: ScopeMain},
		// A per-requirement option (--hash) is no part of the name.
		{Coord: "py:django", Scope: ScopeMain},
		{Coord: "py:pytest", Scope: ScopeDev},
		{Coord: "py:sphinx-acme-theme", Scope: ScopeDev},
	}
	if !reflect.DeepEqual(got.Requires, want) {
		t.Fatalf("Requires = %+v\nwant %+v", got.Requires, want)
	}
}

func TestParse_Cargo(t *testing.T) {
	got := Parse(map[string][]byte{
		"Cargo.toml": []byte(`[workspace]
members = ["crates/*"]

[workspace.dependencies]
serde = { version = "1.0.200", features = ["derive"] }
wire = { package = "acme_wire", version = "0.3" }
`),
		"crates/app/Cargo.toml": []byte(`[package]
name = "acme-app"
version.workspace = true

[dependencies]
serde = { workspace = true }
wire.workspace = true
tokio = "1.38"
core-lib = { path = "../core-lib" }
rand_x = { package = "rand", version = "0.8" }
metrics = { version = "0.23", optional = true }

[dev-dependencies]
proptest = "1"

[build-dependencies]
cc = "1.0"

[target.'cfg(unix)'.dependencies]
libc = "0.2"
`),
		"crates/core-lib/Cargo.toml": []byte("[package]\nname = \"core_lib\"\n"),
	})
	want := Read{
		Provides: []string{"cargo:acme-app", "cargo:core-lib"},
		Requires: []Dep{
			{Coord: "cargo:acme-wire", Scope: ScopeMain},
			{Coord: "cargo:cc", Scope: ScopeDev},
			{Coord: "cargo:libc", Scope: ScopeMain},
			// A feature's, so no part of every build.
			{Coord: "cargo:metrics", Scope: ScopeDev},
			{Coord: "cargo:proptest", Scope: ScopeTest},
			{Coord: "cargo:rand", Scope: ScopeMain},
			{Coord: "cargo:serde", Scope: ScopeMain},
			{Coord: "cargo:tokio", Scope: ScopeMain},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse = %+v\nwant  %+v", got, want)
	}
}

// A workspace's members, each tool's: patterns matched against the tree's
// folders, `*` one name, `**` any depth, exclusions out; a plain folder as
// it stands; the root, a folder outside and node_modules never.
func TestModules_Workspaces(t *testing.T) {
	dirs := []string{
		"packages/ui", "packages/web", "packages/old", "packages/web/node_modules/x",
		"apps/shop/front", "apps/shop/back", "tools/cli", "crates/a", "crates/b", "crates/skip",
		"py/one", "py/two", "mod/api",
	}
	got := Modules(map[string][]byte{
		"package.json":        []byte(`{"workspaces": ["packages/*", "!packages/old", "."]}`),
		"pnpm-workspace.yaml": []byte("# pnpm\npackages:\n  - \"apps/**\" # all of them\n  - '!apps/shop/back'\nonlyBuiltDependencies:\n  - esbuild\n"),
		"tools/package.json":  []byte(`{"workspaces": {"packages": ["cli"]}}`),
		"go.work":             []byte("go 1.23\n\nuse (\n\t./mod/api // the api\n\t.\n)\nuse ../outside\n"),
		"Cargo.toml":          []byte("[workspace]\nmembers = [\"crates/*\"]\nexclude = [\"crates/skip\"]\n"),
		"pyproject.toml":      []byte("[tool.uv.workspace]\nmembers = [\"py/*\"]\nexclude = [\"py/two\"]\n"),
	}, dirs)
	want := []Module{
		{Dir: "apps/shop/front", NPM: true},
		{Dir: "crates/a", Cargo: true},
		{Dir: "crates/b", Cargo: true},
		{Dir: "mod/api", Go: true},
		{Dir: "packages/ui", NPM: true},
		{Dir: "packages/web", NPM: true},
		{Dir: "py/one", Py: true},
		{Dir: "tools/cli", NPM: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Modules = %+v\nwant  %+v", got, want)
	}
	for _, c := range []struct {
		m    Module
		base string
		want bool
	}{
		{Module{NPM: true}, "package.json", true}, {Module{NPM: true}, "go.mod", false},
		{Module{Go: true}, "go.mod", true}, {Module{Cargo: true}, "Cargo.toml", true},
		{Module{Py: true}, "pyproject.toml", true}, {Module{Py: true}, "requirements.txt", false},
	} {
		if got := c.m.Reads(c.base); got != c.want {
			t.Errorf("%+v.Reads(%q) = %v, want %v", c.m, c.base, got, c.want)
		}
	}
}

func TestPnpmFlowList(t *testing.T) {
	inc, exc := pnpmPackages([]byte("packages: ['a/*', \"!a/x\"]\n"))
	if !reflect.DeepEqual(inc, []string{"a/*"}) || !reflect.DeepEqual(exc, []string{"a/x"}) {
		t.Fatalf("pnpmPackages = %v %v", inc, exc)
	}
}
