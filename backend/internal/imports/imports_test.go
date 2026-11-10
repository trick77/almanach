package imports

import (
	"reflect"
	"testing"
)

// A module at the root names its root package ".", as the build's parts
// do; pkg/<a> folds like internal/<a>; a file the build ignores is no
// part; Python reads a black-formatted import over lines and a private
// `_internal` package.
func TestParse_Layouts(t *testing.T) {
	got := Parse(map[string][]byte{
		"go.mod":               []byte("module example.org/svc\n"),
		"main.go":              []byte("package main\n\nimport \"example.org/svc/pkg/auth\"\n"),
		"cmd/tool/main.go":     []byte("package main\n\nimport \"example.org/svc/pkg/auth\"\n"),
		"pkg/auth/auth.go":     []byte("package auth\n\nimport \"example.org/svc/pkg/store\"\n"),
		"pkg/store/store.go":   []byte("package store\n"),
		"internal/gen/main.go": []byte("//go:build ignore\n\npackage main\n\nimport \"example.org/svc/pkg/store\"\n"),
		// A main deep in a fold (an example) makes no app of it.
		"pkg/store/example/main.go": []byte("package main\n"),
	})
	want := Read{
		Units: []Unit{
			{Dir: ".", Name: "svc", Kind: KindApp},
			{Dir: "cmd/tool", Name: "cmd/tool", Kind: KindApp},
			{Dir: "pkg/auth", Name: "pkg/auth"},
			{Dir: "pkg/store", Name: "pkg/store"},
		},
		Edges: []Edge{
			{From: ".", To: "pkg/auth"},
			{From: "cmd/tool", To: "pkg/auth"},
			{From: "pkg/auth", To: "pkg/store"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Go: Parse =\n%+v\nwant\n%+v", got, want)
	}

	files := map[string][]byte{
		"pyproject.toml":          []byte("[project]\nname = \"lib\"\n"),
		"lib/api.py":              []byte("from lib.core import (\n    model,  # the model\n    view,\n)\nfrom lib import (\n    _internal,\n)\n"),
		"lib/core/__init__.py":    []byte(""),
		"lib/_internal/helper.py": []byte("\"\"\"Usage:\n\nfrom lib import admin\n\"\"\"\nfrom ..core import \\\n    model\n"),
		"lib/admin.py":            []byte("import os\n"),
	}
	for p := range files {
		if !Wanted(p) {
			t.Fatalf("Wanted(%q) = false", p)
		}
	}
	got = Parse(files)
	want = Read{
		Units: []Unit{
			{Dir: "lib/_internal", Name: "lib._internal"},
			{Dir: "lib/api.py", Name: "lib.api"},
			{Dir: "lib/core", Name: "lib.core"},
		},
		Edges: []Edge{
			{From: "lib/_internal", To: "lib/core"},
			{From: "lib/api.py", To: "lib/_internal"},
			{From: "lib/api.py", To: "lib/core"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Python: Parse =\n%+v\nwant\n%+v", got, want)
	}
}

func TestWanted(t *testing.T) {
	for p, want := range map[string]bool{
		"backend/go.mod":                   true,
		"cmd/api/main.go":                  true,
		"internal/store/store_test.go":     false,
		"internal/conform/testdata/x/a.go": false,
		"vendor/example.org/x/x.go":        false,
		"_tools/gen.go":                    false,
		".claude/worktrees/a/main.go":      false,
		"src/pkg/core/model.py":            true,
		"src/pkg/tests/test_model.py":      false,
		"tests/conftest.py":                false,
		"pkg/model_test.py":                false,
		"pyproject.toml":                   true,
		"README.md":                        false,
	} {
		if got := Wanted(p); got != want {
			t.Errorf("Wanted(%q) = %v, want %v", p, got, want)
		}
	}
}

// Go: a module in a folder, its packages folded to cmd/<x>, internal/<a>
// and <a>; a main package is an app; a second module's import is an edge
// to its fold; outside imports and a package's own are none.
func TestParse_Go(t *testing.T) {
	got := Parse(map[string][]byte{
		"backend/go.mod": []byte("module example.org/tool\n\ngo 1.24\n"),
		"backend/cmd/api/main.go": []byte(`package main

import (
	"fmt"

	"example.org/tool/internal/store"
	"example.org/tool/internal/web/routes"
	kit "example.org/tool/kit"
)
`),
		"backend/cmd/worker/main.go":       []byte("package main\n\nimport \"example.org/tool/internal/store\"\n"),
		"backend/internal/store/store.go":  []byte("package store\n\nimport \"example.org/tool/internal/store/sqlx\"\n"),
		"backend/internal/store/sqlx/x.go": []byte("package sqlx\n"),
		"backend/internal/web/routes/r.go": []byte("package routes\n\nimport \"example.org/tool/internal/store\"\n"),
		"backend/kit/go.mod":               []byte("module example.org/tool/kit\n"),
		"backend/kit/kit.go":               []byte("package kit\n\nimport \"example.org/tool/kit/auth\"\n"),
		"backend/kit/auth/auth.go":         []byte("package auth\n"),
		"backend/broken.go":                []byte("package"),
	})
	want := Read{
		Units: []Unit{
			{Dir: "backend/cmd/api", Name: "cmd/api", Kind: KindApp},
			{Dir: "backend/cmd/worker", Name: "cmd/worker", Kind: KindApp},
			{Dir: "backend/internal/store", Name: "internal/store"},
			{Dir: "backend/internal/web", Name: "internal/web"},
			{Dir: "backend/kit", Name: "kit"},
			{Dir: "backend/kit/auth", Name: "auth"},
		},
		Edges: []Edge{
			{From: "backend/cmd/api", To: "backend/internal/store"},
			{From: "backend/cmd/api", To: "backend/internal/web"},
			{From: "backend/cmd/api", To: "backend/kit"},
			{From: "backend/cmd/worker", To: "backend/internal/store"},
			{From: "backend/internal/web", To: "backend/internal/store"},
			{From: "backend/kit", To: "backend/kit/auth"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse =\n%+v\nwant\n%+v", got, want)
	}
}

// Python: a distribution under src/, its top-level subpackages and
// modules the parts; absolute, relative and `from pkg import sub` imports
// are edges; a script's module and __main__ are apps.
func TestParse_Python(t *testing.T) {
	got := Parse(map[string][]byte{
		"pyproject.toml":               []byte("[project]\nname = \"my-tool\"\n[project.scripts]\ntool = \"my_tool.cli:main\"\n"),
		"src/my_tool/__init__.py":      []byte("from .core import model\n"),
		"src/my_tool/__main__.py":      []byte("from my_tool import cli\n"),
		"src/my_tool/cli.py":           []byte("import my_tool.core.model as m, os\nfrom .render import draw\n"),
		"src/my_tool/core/__init__.py": []byte(""),
		"src/my_tool/core/model.py":    []byte("from ..util import clamp\nfrom . import helpers\n"),
		"src/my_tool/core/helpers.py":  []byte("import json\n"),
		"src/my_tool/render/draw.py":   []byte("from my_tool.core import model\n"),
		"src/my_tool/util.py":          []byte("import math\n"),
	})
	want := Read{
		Units: []Unit{
			{Dir: "src/my_tool/__main__.py", Name: "my_tool.__main__", Kind: KindApp},
			{Dir: "src/my_tool/cli.py", Name: "my_tool.cli", Kind: KindApp},
			{Dir: "src/my_tool/core", Name: "my_tool.core"},
			{Dir: "src/my_tool/render", Name: "my_tool.render"},
			{Dir: "src/my_tool/util.py", Name: "my_tool.util"},
		},
		Edges: []Edge{
			{From: "src/my_tool/__main__.py", To: "src/my_tool/cli.py"},
			{From: "src/my_tool/cli.py", To: "src/my_tool/core"},
			{From: "src/my_tool/cli.py", To: "src/my_tool/render"},
			{From: "src/my_tool/core", To: "src/my_tool/util.py"},
			{From: "src/my_tool/render", To: "src/my_tool/core"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse =\n%+v\nwant\n%+v", got, want)
	}
}

// No module, or one package alone: nothing.
func TestParse_Nothing(t *testing.T) {
	for name, files := range map[string]map[string][]byte{
		"no go.mod": {"main.go": []byte("package main\n")},
		"one package": {
			"go.mod":  []byte("module example.org/one\n"),
			"main.go": []byte("package main\n\nimport \"fmt\"\n"),
		},
		"no package dir": {"pyproject.toml": []byte("[project]\nname = \"x\"\n"), "app.py": []byte("import os\n")},
	} {
		if got := Parse(files); len(got.Units) != 0 || len(got.Edges) != 0 {
			t.Errorf("%s: Parse = %+v, want none", name, got)
		}
	}
}
