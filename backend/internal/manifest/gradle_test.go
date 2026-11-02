package manifest

import (
	"reflect"
	"testing"
)

func TestIs_Gradle(t *testing.T) {
	for p, want := range map[string]bool{
		"build.gradle":                             true,
		"api/build.gradle.kts":                     true,
		"settings.gradle":                          true,
		"service/settings.gradle.kts":              true,
		"gradle.properties":                        true,
		"gradle/libs.versions.toml":                true,
		"libs.versions.toml":                       false,
		"service/gradle/libs.versions.toml":        true,
		"a/b/gradle/libs.versions.toml":            false,
		"service/conf/libs.versions.toml":          false,
		"a/b/build.gradle":                         false,
		"gradle/wrapper/gradle-wrapper.properties": false,
	} {
		if got := Is(p); got != want {
			t.Errorf("Is(%q) = %v, want %v", p, got, want)
		}
	}
}

// A Groovy multi-project build: settings names and includes the projects,
// gradle.properties gives the group and a version, a project's own group
// wins, comments and project dependencies count for nothing.
func TestParse_GradleGroovy(t *testing.T) {
	got := Parse(map[string][]byte{
		"settings.gradle": []byte(`rootProject.name = 'platform'
include ':api', ':core'
include 'tools' // a third
// include ':gone'
`),
		"gradle.properties": []byte("group=com.acme\nlibVersion = 1.2\n"),
		"build.gradle": []byte(`plugins { id 'java' }
ext {
    slf4jVersion = '2.0.9'
}
dependencies {
    implementation "org.slf4j:slf4j-api:${slf4jVersion}"
    /* implementation 'com.acme:old:1.0' */
}
`),
		"api/build.gradle": []byte(`dependencies {
    implementation project(':core')
    api "com.acme:commons:$libVersion"
    implementation group: 'org.example', name: 'util', version: '2.0'
    testImplementation 'org.junit.jupiter:junit-jupiter:5.10.0'
    exclude group: 'org.ignored', module: 'thing'
    implementation "com.acme:${unknownName}:1.0"
    runtimeOnly 'com.acme:native:1.0:linux@so'
    // implementation 'com.acme:commented:1.0'
    implementation 'com.acme:core:1.0'
}
`),
		"core/build.gradle": []byte("group = 'com.acme.core'\n"),
	})
	want := Read{
		Provides: []string{"maven:com.acme.core:core", "maven:com.acme:api", "maven:com.acme:platform", "maven:com.acme:tools"},
		Requires: []Dep{
			{Coord: "maven:com.acme:commons", Version: "1.2"},
			{Coord: "maven:com.acme:core", Version: "1.0"},
			{Coord: "maven:com.acme:native", Version: "1.0"},
			{Coord: "maven:org.example:util", Version: "2.0"},
			{Coord: "maven:org.junit.jupiter:junit-jupiter", Version: "5.10.0"},
			{Coord: "maven:org.slf4j:slf4j-api", Version: "2.0.9"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse = %+v\nwant  %+v", got, want)
	}
}

// A Kotlin DSL build with a version catalog: accessors, bundles, platform
// and `.get()` resolve; plugins do not count; `${property("x")}` and a
// `val` resolve; a dependency over several lines is read.
func TestParse_GradleKotlinCatalog(t *testing.T) {
	got := Parse(map[string][]byte{
		"settings.gradle.kts": []byte(`rootProject.name = "checkout"
include(
    ":app",
)
`),
		"gradle.properties": []byte("xVersion=3.0\n"),
		"gradle/libs.versions.toml": []byte(`[versions]
acme = "4.1"   # the platform
junit = { strictly = "5.10.0" }

[libraries]
acme-commons = { module = "com.acme:commons", version.ref = "acme" }
acme-bom = { group = "com.acme", name = "bom", version.ref = "acme" }
"junit-jupiter" = { module = "org.junit.jupiter:junit-jupiter", version.ref = "junit" }
ktor-server = 'io.ktor:ktor-server-core:2.3.0'
ktor-server-netty = { module = "io.ktor:ktor-server-netty" }
unused = "org.unused:unused:1.0"

[bundles]
web = [
  "ktor-server",
  "ktor_server_netty", # same accessor
]

[plugins]
spring = { id = "org.springframework.boot", version = "3.2.0" }
`),
		"build.gradle.kts": []byte(`plugins {
    alias(libs.plugins.spring)
    kotlin("jvm") version "2.0.0"
}
group = "com.acme"
val ktorVersion = "2.3.0"
dependencies {
    implementation(platform(libs.acme.bom))
    implementation(libs.acme.commons)
    implementation(libs.bundles.web)
    testImplementation(libs.junit.jupiter.get())
    implementation(kotlin("stdlib"))
    implementation("io.ktor:ktor-client-core:$ktorVersion")
    implementation("com.acme:x:${property("xVersion")}")
    implementation(
        "com.acme:multi:1.0"
    )
}
`),
		"app/build.gradle.kts": []byte(`dependencies {
    implementation(project(":lib"))
    implementation(libs.acme.commons)
}
`),
	})
	want := Read{
		// The root's top-level group is the root project's only: app has
		// none, so provides nothing.
		Provides: []string{"maven:com.acme:checkout"},
		Requires: []Dep{
			{Coord: "maven:com.acme:bom", Version: "4.1"},
			{Coord: "maven:com.acme:commons", Version: "4.1"},
			{Coord: "maven:com.acme:multi", Version: "1.0"},
			{Coord: "maven:com.acme:x", Version: "3.0"},
			{Coord: "maven:io.ktor:ktor-client-core", Version: "2.3.0"},
			{Coord: "maven:io.ktor:ktor-server-core", Version: "2.3.0"},
			{Coord: "maven:io.ktor:ktor-server-netty", Version: ""},
			{Coord: "maven:org.junit.jupiter:junit-jupiter", Version: "5.10.0"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse = %+v\nwant  %+v", got, want)
	}
}

// A build with no settings file names nothing it provides (its name would
// be the clone's folder); a folder with its own settings file is its own
// build, with its own properties, and the root catalog is not its.
func TestParse_GradleNestedBuild(t *testing.T) {
	got := Parse(map[string][]byte{
		"build.gradle": []byte("group = 'com.acme'\ndependencies { implementation libs.acme.commons }\n"),
		"gradle/libs.versions.toml": []byte(`[libraries]
acme-commons = "com.acme:commons:1.0"
`),
		"service/settings.gradle.kts": []byte(`rootProject.name = "service"`),
		"service/gradle.properties":   []byte("group=org.other\nv=9\n"),
		"service/build.gradle.kts":    []byte("dependencies {\n implementation(libs.acme.commons)\n api(\"org.lib:core:$v\")\n}\n"),
	})
	want := Read{
		Provides: []string{"maven:org.other:service"},
		Requires: []Dep{
			{Coord: "maven:com.acme:commons", Version: "1.0"},
			{Coord: "maven:org.lib:core", Version: "9"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse = %+v\nwant  %+v", got, want)
	}
}

// Without a group the projects provide nothing; a catalog that does not
// parse resolves nothing, the literal dependencies still count.
func TestParse_GradleNoGroupBadCatalog(t *testing.T) {
	got := Parse(map[string][]byte{
		"settings.gradle":           []byte("rootProject.name = 'x'\n"),
		"gradle/libs.versions.toml": []byte("[libraries\n"),
		"build.gradle":              []byte("dependencies {\n implementation libs.a.b\n implementation 'org.a:b:1'\n}\n"),
	})
	want := Read{Provides: []string{}, Requires: []Dep{{Coord: "maven:org.a:b", Version: "1"}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse = %+v\nwant  %+v", got, want)
	}
}

// A Gradle project and a Maven module of one repository meet on the same
// coordinate: no requirement of itself.
func TestParse_GradleMeetsMaven(t *testing.T) {
	got := Parse(map[string][]byte{
		"settings.gradle": []byte("rootProject.name = 'commons'\n"),
		"build.gradle":    []byte("group = 'com.acme'\n"),
		"demo/pom.xml":    []byte(`<project><groupId>com.acme</groupId><artifactId>demo</artifactId><dependencies><dependency><groupId>com.acme</groupId><artifactId>commons</artifactId></dependency></dependencies></project>`),
	})
	want := Read{Provides: []string{"maven:com.acme:commons", "maven:com.acme:demo"}, Requires: []Dep{}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse = %+v\nwant  %+v", got, want)
	}
}

func TestStripComments(t *testing.T) {
	in := "a // x\nb \"http://h/p\" /* y\nz */ c '//' \"${f(\"//\")}\""
	want := "a \nb \"http://h/p\" \n  c '//' \"${f(\"//\")}\""
	if got := stripComments([]byte(in)); got != want {
		t.Fatalf("stripComments = %q, want %q", got, want)
	}
}

func TestProperties(t *testing.T) {
	got := properties([]byte("# comment\n! also\n  group = com.acme\nname:x\nspaced value here\nlong = a\\\n    b\nesc=\\u0041\\=\\\\\nempty\ngroup=last\n"))
	want := map[string]string{"group": "last", "name": "x", "spaced": "value here", "long": "ab", "esc": `A=\`, "empty": ""}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("properties = %#v\nwant %#v", got, want)
	}
}

func TestParseTOML(t *testing.T) {
	got, err := parseTOML([]byte(`top = "t" # c
[a.b]
c = 'lit\n'
d = """
multi\
   line"""
e = 1_000
"q.k" = { x.y = "z", arr = [1, "two", [3]] }
[[list]]
n = "1"
[[list]]
n = "2"
`))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"top": "t",
		"a": map[string]any{"b": map[string]any{
			"c":   `lit\n`,
			"d":   "multiline",
			"e":   "1_000",
			"q.k": map[string]any{"x": map[string]any{"y": "z"}, "arr": []any{"1", "two", []any{"3"}}},
		}},
		"list": []any{map[string]any{"n": "1"}, map[string]any{"n": "2"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseTOML = %#v\nwant %#v", got, want)
	}
	for _, bad := range []string{"a = ", "[a", "a = \"x", "a = 1\na = 2", "a = { b = 1", "a = [1 2]", "a = 1 b = 2"} {
		if _, err := parseTOML([]byte(bad)); err == nil {
			t.Errorf("parseTOML(%q) = nil error, want one", bad)
		}
	}
}

// A catalog library that could not be read is nothing, never a shorter
// alias sharing its prefix; a byte order mark does not fail the catalog.
func TestParse_GradleCatalogExact(t *testing.T) {
	got := Parse(map[string][]byte{
		"gradle/libs.versions.toml": []byte("\xef\xbb\xbf[libraries]\naws-sdk = \"org.aws:core:2.0\"\naws-sdk-extra = { group = \"${g}\", name = \"extra\" }\n"),
		"build.gradle":              []byte("dependencies {\n implementation libs.aws.sdk.extra\n implementation libs.aws.sdk.asProvider()\n}\n"),
	})
	want := Read{Provides: []string{}, Requires: []Dep{{Coord: "maven:org.aws:core", Version: "2.0"}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse = %+v\nwant  %+v", got, want)
	}
}

// Groups as Gradle evaluates them: subprojects { } is not the root's, a
// root allprojects { } beats a subproject's gradle.properties, a
// subproject's own top-level group beats both.
func TestParse_GradleGroupScopes(t *testing.T) {
	got := Parse(map[string][]byte{
		"settings.gradle":       []byte("rootProject.name = 'parent'\ninclude 'app', 'lib', 'own'\n"),
		"build.gradle":          []byte("allprojects {\n    group = 'com.all'\n}\nsubprojects {\n    group = 'com.sub'\n}\n"),
		"app/gradle.properties": []byte("group=com.other\n"),
		"own/build.gradle":      []byte("group = 'com.own'\n"),
	})
	want := []string{"maven:com.all:parent", "maven:com.own:own", "maven:com.sub:app", "maven:com.sub:lib"}
	if !reflect.DeepEqual(got.Provides, want) {
		t.Fatalf("Provides = %v, want %v", got.Provides, want)
	}

	got = Parse(map[string][]byte{
		"settings.gradle":       []byte("rootProject.name = 'parent'\ninclude 'app'\n"),
		"build.gradle":          []byte("allprojects {\n    group = 'com.acme'\n}\n"),
		"app/gradle.properties": []byte("group=com.other\n"),
	})
	want = []string{"maven:com.acme:app", "maven:com.acme:parent"}
	if !reflect.DeepEqual(got.Provides, want) {
		t.Fatalf("Provides = %v, want %v", got.Provides, want)
	}
}

// Several coordinates in one Groovy call, map entries in any order.
func TestParse_GradleListsAndMapOrder(t *testing.T) {
	got := Parse(map[string][]byte{
		"build.gradle": []byte("dependencies {\n implementation 'a.b:one:1', 'a.b:two:2'\n compile name: 'n', group: 'g.h', version: '3'\n implementation(version = \"4\", name = \"k\", group = \"g.h\")\n}\n"),
	})
	want := []Dep{
		{Coord: "maven:a.b:one", Version: "1"},
		{Coord: "maven:a.b:two", Version: "2"},
		{Coord: "maven:g.h:k", Version: "4"},
		{Coord: "maven:g.h:n", Version: "3"},
	}
	if !reflect.DeepEqual(got.Requires, want) {
		t.Fatalf("Requires = %+v, want %+v", got.Requires, want)
	}
}

// buildSrc and a build pluginManagement includes are build logic: their
// dependencies are a plugin's classpath, not the repository's; a build
// included outside pluginManagement is code and counts.
func TestParse_GradleBuildLogic(t *testing.T) {
	got := Parse(map[string][]byte{
		"settings.gradle.kts":             []byte("pluginManagement {\n    includeBuild(\"build-logic\")\n}\nincludeBuild(\"shared\")\nrootProject.name = \"svc\"\n"),
		"build.gradle.kts":                []byte("group = \"com.acme\"\n"),
		"buildSrc/build.gradle.kts":       []byte("dependencies { implementation(\"org.boot:boot-plugin:3.2.0\") }\n"),
		"build-logic/settings.gradle.kts": []byte("rootProject.name = \"build-logic\"\n"),
		"build-logic/build.gradle.kts":    []byte("group = \"com.acme\"\ndependencies { implementation(\"org.kotlin:kotlin-plugin:2.0\") }\n"),
		"shared/settings.gradle.kts":      []byte("rootProject.name = \"shared\"\n"),
		"shared/build.gradle.kts":         []byte("group = \"com.acme\"\ndependencies { implementation(\"org.lib:util:1\") }\n"),
	})
	want := Read{
		Provides: []string{"maven:com.acme:shared", "maven:com.acme:svc"},
		Requires: []Dep{{Coord: "maven:org.lib:util", Version: "1"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse = %+v\nwant  %+v", got, want)
	}
}

// buildscript { } is the build's own classpath and constraints { } only pin
// versions: neither is required. A Kotlin-quoted configuration counts.
func TestParse_GradleExcludedBlocks(t *testing.T) {
	got := Parse(map[string][]byte{
		"build.gradle.kts": []byte(`buildscript {
    dependencies { classpath("com.android.tools.build:gradle:8.0.0") }
}
dependencies {
    constraints {
        implementation("a.b:pinned:9")
    }
    "implementation"("a.b:quoted:1")
    implementation("a.b:real:2")
}
`),
	})
	want := []Dep{{Coord: "maven:a.b:quoted", Version: "1"}, {Coord: "maven:a.b:real", Version: "2"}}
	if !reflect.DeepEqual(got.Requires, want) {
		t.Fatalf("Requires = %+v, want %+v", got.Requires, want)
	}
}

// A group set in a one-line block counts; of allprojects and subprojects
// the later one wins for a subproject, of own and allprojects for the
// root.
func TestParse_GradleGroupOrder(t *testing.T) {
	got := Parse(map[string][]byte{
		"settings.gradle": []byte("rootProject.name = 'svc'\ninclude 'app'\n"),
		"build.gradle":    []byte("group = 'com.early'\nsubprojects { group = 'old' }\nallprojects { group = 'com.x'; version = '1' }\n"),
	})
	want := []string{"maven:com.x:app", "maven:com.x:svc"}
	if !reflect.DeepEqual(got.Provides, want) {
		t.Fatalf("Provides = %v, want %v", got.Provides, want)
	}
}

// A property under a secret key is never resolved into a version; a
// version-named one is.
func TestParse_GradleSecretProperty(t *testing.T) {
	got := Parse(map[string][]byte{
		"gradle.properties": []byte("repoToken=s3cr3tvalue\ntokenLibVersion=1.2.3\n"),
		"build.gradle":      []byte("dependencies {\n implementation \"a.b:c:$repoToken\"\n implementation \"a.b:token-lib:$tokenLibVersion\"\n}\n"),
	})
	want := []Dep{{Coord: "maven:a.b:c", Version: ""}, {Coord: "maven:a.b:token-lib", Version: "1.2.3"}}
	if !reflect.DeepEqual(got.Requires, want) {
		t.Fatalf("Requires = %+v, want %+v", got.Requires, want)
	}
}

// Of a Groovy and a Kotlin build file in one folder the Groovy one counts,
// every time; a nested build reads its own catalog; a rich version shows
// prefer.
func TestParse_GradleOneFileNestedCatalog(t *testing.T) {
	files := map[string][]byte{
		"build.gradle":                  []byte("ext.v = '1'\ndependencies { implementation \"a.b:c:$v\" }\n"),
		"build.gradle.kts":              []byte("val v = \"2\"\ndependencies { implementation(\"a.b:kts:$v\") }\n"),
		"jvm/settings.gradle.kts":       []byte("rootProject.name = \"jvm\"\n"),
		"jvm/gradle/libs.versions.toml": []byte("[libraries]\nwire = { module = \"a.b:wire\", version = { strictly = \"[1.0,2.0[\", prefer = \"1.5\" } }\n"),
		"jvm/build.gradle.kts":          []byte("dependencies { implementation(libs.wire) }\n"),
	}
	want := []Dep{{Coord: "maven:a.b:c", Version: "1"}, {Coord: "maven:a.b:wire", Version: "1.5"}}
	for range 20 {
		if got := Parse(files); !reflect.DeepEqual(got.Requires, want) {
			t.Fatalf("Requires = %+v, want %+v", got.Requires, want)
		}
	}
}

// A package.json with a byte order mark parses too.
func TestParse_BOM(t *testing.T) {
	got := Parse(map[string][]byte{"package.json": []byte("\xef\xbb\xbf{\"name\": \"x\"}")})
	if !reflect.DeepEqual(got.Provides, []string{"npm:x"}) {
		t.Fatalf("Provides = %v, want [npm:x]", got.Provides)
	}
}
