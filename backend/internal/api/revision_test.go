package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/trick77/almanach/internal/config"
	"github.com/trick77/almanach/internal/diagram"
	"github.com/trick77/almanach/internal/store"
)

func st(id, name, text string, files ...string) store.Statement {
	return store.Statement{ID: id, Kind: "rule", Name: name, Text: text, Files: files}
}

// A revision answers as it stood with its diff against the one before:
// added, changed (name, text, files, kind or diagram) and dropped by id,
// the summary's change flagged; the first revision has everything added
// and no previous; an unknown number, page or project is a 404, and so is
// a number the client routes never write (a sign, a leading zero).
func TestRepoRevision(t *testing.T) {
	s := newFakeStore()
	s.teams["PLAT"] = "platform"
	s.repos["PLAT"] = []store.RepoRow{{Slug: "files"}}
	// The current number is the newest listed revision's.
	s.revisions = []store.RevisionRow{{Revision: 3}, {Revision: 2}, {Revision: 1}}
	s.revisionPages[store.ScopeRepository+"|PLAT|files|1"] = store.Page{ProjectKey: "PLAT", RepoSlug: "files", Revision: 1, Summary: "Anfang.", SourcePR: "PLAT/files#1",
		Statements: []store.Statement{st("s1", "Eins", "a"), st("s2", "Zwei", "b", "x.go")}}
	s.revisionPages[store.ScopeRepository+"|PLAT|files|2"] = store.Page{ProjectKey: "PLAT", RepoSlug: "files", Revision: 2, Summary: "Anfang.", SourcePR: "PLAT/files#2", UpdatedAt: mergedAt,
		Statements: []store.Statement{st("s1", "Eins", "a"), st("s2", "Zwei", "b", "x.go", "y.go"), st("s3", "Drei", "c")}}
	s.revisionPages[store.ScopeRepository+"|PLAT|files|3"] = store.Page{ProjectKey: "PLAT", RepoSlug: "files", Revision: 3, Summary: "Jetzt.", SourceKind: store.FromBaseline, SourceCommit: "abc",
		Statements: []store.Statement{st("s1", "Eins neu", "a"), st("s3", "Drei", "c")}}
	h := handler(t, s, nil, config.AuthNone)

	for _, p := range []string{"/api/projects/PLAT/repos/nope/revisions/1", "/api/projects/PLAT/repos/files/revisions/9",
		"/api/projects/PLAT/repos/files/revisions/0", "/api/projects/PLAT/repos/files/revisions/x",
		"/api/projects/PLAT/repos/files/revisions/02", "/api/projects/PLAT/repos/files/revisions/+2",
		"/api/projects/PLAT/repos/files/revisions/-0"} {
		if code, _ := do(t, h, http.MethodGet, p, "", "", nil); code != 404 {
			t.Errorf("%s = %d, want 404", p, code)
		}
	}
	code, body := do(t, h, http.MethodGet, "/api/projects/PLAT/repos/files/revisions/2", "", "", nil)
	if code != 200 || field(t, body, "scope") != "repository" || field(t, body, "project_key") != "PLAT" || field(t, body, "repo_slug") != "files" ||
		string(body["revision"]) != "2" || string(body["current"]) != "3" || string(body["previous"]) != "1" || string(body["summary_changed"]) != "false" ||
		field(t, body, "created_at") != "2026-10-20T10:00:00Z" {
		t.Fatalf("%d %v", code, body)
	}
	if string(body["added"]) != `[{"id":"s3","kind":"rule","name":"Drei"}]` || string(body["changed"]) != `[{"id":"s2","kind":"rule","name":"Zwei"}]` ||
		string(body["dropped"]) != "[]" || string(body["rechecked"]) != "[]" {
		t.Errorf("diff = %s %s %s %s", body["added"], body["changed"], body["dropped"], body["rechecked"])
	}
	// A repository page's map is set in place, no revision of its own.
	if string(body["map_changed"]) != "false" || string(body["shape_changed"]) != "false" {
		t.Errorf("map, shape changed = %s %s on a repository revision", body["map_changed"], body["shape_changed"])
	}
	var desc map[string]json.RawMessage
	if err := json.Unmarshal(body["description"], &desc); err != nil || string(desc["revision"]) != "2" || field(t, desc, "source_pr_key") != "PLAT/files#2" {
		t.Errorf("description = %s", body["description"])
	}

	_, body = do(t, h, http.MethodGet, "/api/projects/PLAT/repos/files/revisions/3", "", "", nil)
	if string(body["summary_changed"]) != "true" || string(body["changed"]) != `[{"id":"s1","kind":"rule","name":"Eins neu"}]` ||
		string(body["dropped"]) != `[{"id":"s2","kind":"rule","name":"Zwei"}]` || string(body["added"]) != "[]" {
		t.Errorf("revision 3 = %v", body)
	}
	_, body = do(t, h, http.MethodGet, "/api/projects/PLAT/repos/files/revisions/1", "", "", nil)
	if string(body["previous"]) != "0" || string(body["summary_changed"]) != "true" || string(body["dropped"]) != "[]" ||
		string(body["added"]) != `[{"id":"s1","kind":"rule","name":"Eins"},{"id":"s2","kind":"rule","name":"Zwei"}]` {
		t.Errorf("revision 1 = %v", body)
	}

	// No revision listed at all: nothing current, a 404, never "von 0".
	s.revisions = nil
	if code, _ := do(t, h, http.MethodGet, "/api/projects/PLAT/repos/files/revisions/2", "", "", nil); code != 404 {
		t.Errorf("no current revision = %d, want 404", code)
	}
	s.revisions = []store.RevisionRow{{Revision: 3}}
	s.revisionErr = errors.New("boom")
	if code, _ := do(t, h, http.MethodGet, "/api/projects/PLAT/repos/files/revisions/2", "", "", nil); code != 500 {
		t.Errorf("store failure = %d, want 500", code)
	}
}

// A project's revisions come on its page and one by one; a project of one
// has neither on the wire.
func TestProjectRevision(t *testing.T) {
	s := newFakeStore()
	s.teams["PLAT"] = "platform"
	s.repos["PLAT"] = []store.RepoRow{{Slug: "files"}, {Slug: "web"}}
	s.revisions = []store.RevisionRow{{Revision: 1, SourcePR: "PLAT/files#1", Statements: 1}}
	s.revisionPages[store.ScopeProject+"|PLAT||1"] = store.Page{ProjectKey: "PLAT", Revision: 1, Summary: "P.", SourcePR: "PLAT/files#1",
		Statements: []store.Statement{st("s1", "Eins", "a")}}
	h := handler(t, s, nil, config.AuthNone)
	_, body := do(t, h, http.MethodGet, "/api/projects/PLAT", "", "", nil)
	var revs []map[string]any
	if err := json.Unmarshal(body["revisions"], &revs); err != nil || len(revs) != 1 || revs[0]["revision"] != 1.0 {
		t.Errorf("project revisions = %s", body["revisions"])
	}
	code, body := do(t, h, http.MethodGet, "/api/projects/PLAT/revisions/1", "", "", nil)
	if code != 200 || field(t, body, "scope") != "project" || field(t, body, "project_key") != "PLAT" {
		t.Fatalf("%d %v", code, body)
	}
	if _, ok := body["repo_slug"]; ok {
		t.Error("repo_slug present on a project revision")
	}
	if string(body["map_changed"]) != "false" || string(body["shape_changed"]) != "false" {
		t.Errorf("first revision without a map: map, shape changed = %s %s", body["map_changed"], body["shape_changed"])
	}
	if code, _ := do(t, h, http.MethodGet, "/api/projects/NOPE/revisions/1", "", "", nil); code != 404 {
		t.Errorf("unknown project = %d, want 404", code)
	}

	// A run writes a revision for a map, a shape or a check alone: the view
	// names what moved, never "nothing changed". A system judged on a page
	// from before shapes moves nothing.
	m := &diagram.Diagram{Kind: diagram.KindFlow, Direction: "LR", Nodes: []diagram.Node{{ID: "a", Label: "A", Shape: "step"}}}
	one := s.revisionPages[store.ScopeProject+"|PLAT||1"]
	drawn, judged, system, checked := one, one, one, one
	drawn.Revision, drawn.Map = 2, m
	judged.Revision, judged.Map, judged.Shape = 3, m, store.ShapeCollection
	system.Revision, system.Shape = 2, store.ShapeSystem
	checked.Revision, checked.Map, checked.Shape = 4, m, store.ShapeCollection
	checked.Statements = []store.Statement{st("s1", "Eins", "a")}
	checked.Statements[0].Checked = "abc"
	s.revisionPages[store.ScopeProject+"|PLAT||2"] = drawn
	s.revisionPages[store.ScopeProject+"|PLAT||3"] = judged
	s.revisionPages[store.ScopeProject+"|PLAT||4"] = checked
	s.revisions = []store.RevisionRow{{Revision: 4}, {Revision: 3}, {Revision: 2}, {Revision: 1}}
	for _, c := range []struct {
		n                 string
		mapMoved, shape   string
		rechecked, others string
	}{
		{"2", "true", "false", "[]", "[]"},
		{"3", "false", "true", "[]", "[]"},
		{"4", "false", "false", `[{"id":"s1","kind":"rule","name":"Eins"}]`, "[]"},
	} {
		_, body := do(t, h, http.MethodGet, "/api/projects/PLAT/revisions/"+c.n, "", "", nil)
		if string(body["map_changed"]) != c.mapMoved || string(body["shape_changed"]) != c.shape || string(body["rechecked"]) != c.rechecked ||
			string(body["changed"]) != c.others || string(body["summary_changed"]) != "false" {
			t.Errorf("revision %s = %v", c.n, body)
		}
	}
	s.revisionPages[store.ScopeProject+"|PLAT||2"] = system
	if _, body := do(t, h, http.MethodGet, "/api/projects/PLAT/revisions/2", "", "", nil); string(body["shape_changed"]) != "false" {
		t.Errorf("a system judged on a page from before shapes: shape_changed = %s, want false", body["shape_changed"])
	}

	// A project of one: no revisions listed, the route a 404.
	s.teams["loom"] = ""
	s.repos["loom"] = []store.RepoRow{{Slug: "loom"}}
	s.single = map[string]store.RepoRow{"loom": {Slug: "loom"}}
	s.revisionPages[store.ScopeProject+"|loom||1"] = store.Page{ProjectKey: "loom", Revision: 1}
	h, _, _ = sourcesHandlerWith(t, newFakeSources(), s, &fakeWaker{}, false)
	_, body = do(t, h, http.MethodGet, "/api/projects/loom", "", "", nil)
	if string(body["revisions"]) != "[]" {
		t.Errorf("project of one's revisions = %s, want none", body["revisions"])
	}
	if code, body := do(t, h, http.MethodGet, "/api/projects/loom/revisions/1", "", "", nil); code != 404 || field(t, body, "error") != "unknown project" {
		t.Errorf("project of one's revision = %d %v, want 404", code, body)
	}
}

func TestDiffStatements(t *testing.T) {
	e := diffStatements(nil, nil)
	if len(e.added)+len(e.changed)+len(e.dropped)+len(e.rechecked) != 0 || e.added == nil || e.changed == nil || e.dropped == nil || e.rechecked == nil {
		t.Errorf("empty diff = %+v, want empty lists", e)
	}
	kind := st("s4", "Vier", "d")
	kind.Kind = "pitfall"
	// A diagram drawn beside an unchanged text is a change too: the fold
	// writes a revision for it.
	drawn := st("s6", "Sechs", "f")
	drawn.Diagram = &diagram.Diagram{Kind: diagram.KindFlow, Direction: "LR", Nodes: []diagram.Node{{ID: "a", Label: "A", Shape: "step"}}}
	// A check stamp alone is a recheck; with the text it is a change.
	stamped := st("s7", "Sieben", "g")
	stamped.Checked = "abc"
	both := st("s1", "A", "t2")
	both.Checked = "abc"
	d := diffStatements(
		[]store.Statement{st("s1", "A", "t"), st("s2", "B", "t"), st("s3", "C", "t"), st("s4", "Vier", "d"), st("s6", "Sechs", "f"), st("s7", "Sieben", "g")},
		[]store.Statement{st("s5", "E", "t"), st("s2", "B", "t"), both, kind, drawn, stamped})
	a, c, gone, re := d.added, d.changed, d.dropped, d.rechecked
	if len(a) != 1 || a[0].ID != "s5" || len(c) != 3 || c[0].ID != "s1" || c[1].ID != "s4" || c[2].ID != "s6" || len(gone) != 1 || gone[0].ID != "s3" ||
		len(re) != 1 || re[0].ID != "s7" {
		t.Errorf("diff = %+v", d)
	}
}
