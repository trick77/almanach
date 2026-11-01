package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/trick77/almanach/internal/store"
)

// A static folder comes with its first repository; without a slug the
// repository's name from the URL, without a project key the slug.
func TestSources_CreateWithFirstRepo(t *testing.T) {
	fs := newFakeSources()
	h, nudger, _ := sourcesHandler(t, fs, false)
	code, body := do(t, h, http.MethodPost, "/api/sources", "", `{"kind":"static","clone_url":"https://forge.example.invalid/ipverse/lens-gateway.git"}`, jsonHdr)
	if code != 201 || field(t, body, "project_key") != "lens-gateway" {
		t.Fatalf("create with a URL alone: %d %v", code, body)
	}
	if rs := fs.repos["lens-gateway"]; len(rs) != 1 || rs[0].Slug != "lens-gateway" || rs[0].CloneURL != "https://forge.example.invalid/ipverse/lens-gateway.git" {
		t.Fatalf("first repository = %+v", rs)
	}
	if nudger.nudges == 0 {
		t.Error("no sync nudged")
	}
	code, body = do(t, h, http.MethodPost, "/api/sources", "",
		`{"project_key":"ipverse","kind":"static","clone_url":"https://forge.example.invalid/ipverse/core","slug":"kern"}`, jsonHdr)
	if code != 201 || fs.repos["ipverse"][0].Slug != "kern" {
		t.Fatalf("create with key and slug: %d %v %+v", code, body, fs.repos["ipverse"])
	}
	// A Bitbucket project lists its repositories itself.
	code, body = do(t, h, http.MethodPost, "/api/sources", "",
		`{"project_key":"SHOP","kind":"bitbucket","host":"https://forge.example.invalid","clone_url":"https://forge.example.invalid/x/a"}`, jsonHdr)
	if code != 422 || !strings.Contains(field(t, body, "error"), "clone_url") {
		t.Fatalf("Bitbucket with a clone URL: %d %v", code, body)
	}
	// A URL with no repository name in it needs a slug.
	code, body = do(t, h, http.MethodPost, "/api/sources", "", `{"project_key":"x","kind":"static","clone_url":"https://forge.example.invalid/"}`, jsonHdr)
	if code != 422 || !strings.Contains(field(t, body, "error"), "slug") {
		t.Fatalf("URL without a name: %d %v", code, body)
	}
	// Cloned already elsewhere: 409, no folder.
	fs.cloned = map[string]*store.ClonedElsewhereError{"https://forge.example.invalid/x/wire": {ProjectKey: "libs", Slug: "wire"}}
	code, body = do(t, h, http.MethodPost, "/api/sources", "", `{"kind":"static","clone_url":"https://forge.example.invalid/x/wire"}`, jsonHdr)
	if code != 409 || !strings.Contains(field(t, body, "error"), "libs/wire") {
		t.Fatalf("cloned elsewhere: %d %v", code, body)
	}
	if _, ok := fs.sources["wire"]; ok {
		t.Error("the folder was created although its repository was refused")
	}
}

// Adding a repository without a slug takes its name from the URL.
func TestSources_AddRepoSlugFromURL(t *testing.T) {
	fs := newFakeSources()
	h, _, _ := sourcesHandler(t, fs, false)
	if code, _ := do(t, h, http.MethodPost, "/api/sources", "", `{"project_key":"ipverse","kind":"static"}`, jsonHdr); code != 201 {
		t.Fatal("create")
	}
	code, body := do(t, h, http.MethodPost, "/api/sources/ipverse/repos", "", `{"clone_url":"https://forge.example.invalid/ipverse/lens-console.git"}`, jsonHdr)
	if code != 201 || len(fs.repos["ipverse"]) != 1 || fs.repos["ipverse"][0].Slug != "lens-console" {
		t.Fatalf("add without a slug: %d %v %+v", code, body, fs.repos["ipverse"])
	}
}

// A project of one is listed with its repository, has no project page on
// the wire, and exports as its repository.
func TestProjects_Single(t *testing.T) {
	st := newFakeStore()
	st.projects = []store.ProjectRow{{Key: "ipverse", Repos: 2}, {Key: "loom", Repos: 1}}
	st.teams["loom"], st.teams["ipverse"] = "", ""
	st.repos["loom"] = []store.RepoRow{{Slug: "loom", Revision: 3, Summary: "Ein Agent."}}
	st.repos["ipverse"] = []store.RepoRow{{Slug: "core"}, {Slug: "lens"}}
	st.single = map[string]store.RepoRow{"loom": st.repos["loom"][0]}
	st.pages[store.ScopeProject+"|loom|"] = store.Page{RevisionID: 1, Revision: 1, Summary: "Von früher."}
	st.pages[store.ScopeRepository+"|loom|loom"] = store.Page{RevisionID: 2, Revision: 3, Summary: "Ein Agent."}
	h, _, _ := sourcesHandlerWith(t, newFakeSources(), st, &fakeWaker{}, false)

	_, body := do(t, h, http.MethodGet, "/api/projects", "", "", nil)
	var list []struct {
		Key  string `json:"key"`
		Repo *struct {
			Slug    string `json:"slug"`
			Summary string `json:"summary"`
		} `json:"repo"`
	}
	if err := json.Unmarshal(body["projects"], &list); err != nil || len(list) != 2 {
		t.Fatalf("projects: %v %s", err, body["projects"])
	}
	if list[0].Key != "ipverse" || list[0].Repo != nil || list[1].Repo == nil || list[1].Repo.Summary != "Ein Agent." {
		t.Fatalf("projects = %+v, want loom with its repository, ipverse without", list)
	}

	code, body := do(t, h, http.MethodGet, "/api/projects/loom", "", "", nil)
	if code != 200 || string(body["single"]) != "true" || string(body["description"]) != "null" || string(body["map_expected"]) != "false" {
		t.Fatalf("loom: %d single %s description %s expected %s", code, body["single"], body["description"], body["map_expected"])
	}
	if code, body = do(t, h, http.MethodGet, "/api/projects/ipverse", "", "", nil); code != 200 || string(body["single"]) != "false" {
		t.Fatalf("ipverse: %d single %s", code, body["single"])
	}

	rec := getMD(h, "/api/projects/loom.md", "")
	if rec.Code != 200 || !strings.HasPrefix(rec.Body.String(), "# Repository loom (Projekt loom)") {
		t.Fatalf("loom export: %d %q, want the repository's", rec.Code, rec.Body.String())
	}
}
