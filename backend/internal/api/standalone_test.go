package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/trick77/almanach/internal/store"
)

// A static folder is one system unless it says otherwise; a Bitbucket
// project always is. The switch comes alone in its PATCH.
func TestSources_System(t *testing.T) {
	fs := newFakeSources()
	h, _, _ := sourcesHandler(t, fs, false)

	code, body := do(t, h, http.MethodPost, "/api/sources", "", `{"project_key":"tools","kind":"static","system":false}`, jsonHdr)
	if code != 201 || string(body["system"]) != "false" || !fs.sources["tools"].Standalone {
		t.Fatalf("create standalone folder: %d %v", code, body)
	}
	code, body = do(t, h, http.MethodPost, "/api/sources", "", `{"project_key":"apps","kind":"static"}`, jsonHdr)
	if code != 201 || string(body["system"]) != "true" {
		t.Fatalf("create folder without system: %d %v, want a system", code, body)
	}
	code, body = do(t, h, http.MethodPost, "/api/sources", "",
		`{"project_key":"SHOP","kind":"bitbucket","host":"https://forge.example.invalid","system":false}`, jsonHdr)
	if code != 422 || !strings.Contains(field(t, body, "error"), "system") {
		t.Fatalf("Bitbucket standing alone: %d %v, want 422 naming system", code, body)
	}

	code, body = do(t, h, http.MethodPatch, "/api/sources/tools", "", `{"system":true}`, jsonHdr)
	if code != 200 || string(body["system"]) != "true" || fs.sources["tools"].Standalone {
		t.Fatalf("switch on: %d %v", code, body)
	}
	code, body = do(t, h, http.MethodPatch, "/api/sources/tools", "", `{"system":false,"token_user":"me"}`, jsonHdr)
	if code != 422 || !strings.Contains(field(t, body, "error"), "system") {
		t.Fatalf("switch with another field: %d %v, want 422", code, body)
	}
	code, body = do(t, h, http.MethodPatch, "/api/sources/tools", "", `{"project_key":"tools2","system":false}`, jsonHdr)
	if code != 422 || !strings.Contains(field(t, body, "error"), "project_key") {
		t.Fatalf("switch with a rename: %d %v, want 422", code, body)
	}

	if code, _ := do(t, h, http.MethodPost, "/api/sources", "",
		`{"project_key":"SHOP","kind":"bitbucket","host":"https://forge.example.invalid"}`, jsonHdr); code != 201 {
		t.Fatal("create Bitbucket")
	}
	code, body = do(t, h, http.MethodPatch, "/api/sources/SHOP", "", `{"system":false}`, jsonHdr)
	if code != 422 || !strings.Contains(field(t, body, "error"), "Bitbucket") {
		t.Fatalf("Bitbucket switched off: %d %v, want 422", code, body)
	}
}

// Repositories that stand alone are listed one by one, and their project
// has no page on the wire, not even one written while it was a system.
func TestProjects_Standalone(t *testing.T) {
	st := newFakeStore()
	st.projects = []store.ProjectRow{{Key: "SHOP", Repos: 1}, {Key: "tools", Repos: 2, Standalone: true}}
	st.teams["tools"] = ""
	st.repos["tools"] = []store.RepoRow{{Slug: "llmwire", Revision: 3, Summary: "Ein Client. Mehr."}, {Slug: "webfetch"}}
	st.pages[store.ScopeProject+"|tools|"] = store.Page{RevisionID: 1, Revision: 1, Summary: "Ein erfundenes System."}
	st.standalone = map[string]bool{"tools": true}
	h, _, _ := sourcesHandlerWith(t, newFakeSources(), st, &fakeWaker{}, false)

	code, body := do(t, h, http.MethodGet, "/api/projects", "", "", nil)
	if code != 200 {
		t.Fatalf("projects: %d", code)
	}
	var list []struct {
		Key          string `json:"key"`
		System       bool   `json:"system"`
		Repositories []struct {
			Slug    string `json:"slug"`
			Summary string `json:"summary"`
		} `json:"repositories"`
	}
	if err := json.Unmarshal(body["projects"], &list); err != nil || len(list) != 2 {
		t.Fatalf("projects body: %v %s", err, body["projects"])
	}
	if !list[0].System || len(list[0].Repositories) != 0 {
		t.Errorf("SHOP: %+v, want a system without its repositories listed", list[0])
	}
	if list[1].System || len(list[1].Repositories) != 2 || list[1].Repositories[0].Summary != "Ein Client." {
		t.Errorf("tools: %+v, want standalone with both repositories", list[1])
	}

	code, body = do(t, h, http.MethodGet, "/api/projects/tools", "", "", nil)
	if code != 200 || string(body["system"]) != "false" || string(body["description"]) != "null" ||
		string(body["map_expected"]) != "false" {
		t.Fatalf("standalone project: %d system %s description %s map_expected %s", code, body["system"], body["description"], body["map_expected"])
	}
	code, body = do(t, h, http.MethodGet, "/api/projects/tools.md", "", "", nil)
	if code != 404 || !strings.Contains(field(t, body, "error"), "stand alone") {
		t.Fatalf("standalone export: %d %v, want 404", code, body)
	}
}
