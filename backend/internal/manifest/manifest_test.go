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
			{Coord: "go:github.com/ncruces/go-sqlite3", Version: "v0.36.0"},
			{Coord: "go:github.com/trick77/llmwire", Version: "v0.1.9-0.20261026072811-61ed84c8533a"},
			{Coord: "go:golang.org/x/sys", Version: "v0.49.0"},
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
    <dependency><groupId>com.acme</groupId><artifactId>bom</artifactId><version>2.0</version></dependency>
  </dependencies></dependencyManagement>
  <dependencies>
    <dependency><groupId>com.acme</groupId><artifactId>commons</artifactId><version>${commons.version}</version></dependency>
    <dependency><groupId>${project.groupId}</groupId><artifactId>sibling</artifactId></dependency>
    <dependency><groupId>org.slf4j</groupId><artifactId>slf4j-api</artifactId><version>2.0.9</version></dependency>
  </dependencies>
</project>`)})
	want := Read{
		Provides: []string{"maven:com.acme:checkout"},
		Requires: []Dep{
			{Coord: "maven:com.acme:bom", Version: "2.0"},
			{Coord: "maven:com.acme:commons", Version: ""},
			// The parent POM is used too.
			{Coord: "maven:com.acme:parent", Version: "3.1"},
			{Coord: "maven:org.slf4j:slf4j-api", Version: "2.0.9"},
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
	want := Read{Provides: []string{"maven:com.acme:legacy"}, Requires: []Dep{{Coord: "maven:com.acme:commons", Version: "1.0"}}}
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
		"package.json":     []byte(`{"name": "@acme/web", "dependencies": {"react": "^19.0.0", "@acme/ui": "workspace:*"}, "devDependencies": {"vitest": "4.0.0"}}`),
		"ui/package.json":  []byte(`{"name": "@acme/ui", "peerDependencies": {"react": ">=18"}}`),
		"bad/package.json": []byte(`{not json`),
	})
	want := Read{
		Provides: []string{"npm:@acme/ui", "npm:@acme/web"},
		Requires: []Dep{
			{Coord: "npm:react", Version: "^19.0.0"},
			{Coord: "npm:vitest", Version: "4.0.0"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse = %+v\nwant  %+v", got, want)
	}
}
