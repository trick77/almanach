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
	code, body := do(t, h, http.MethodPost, "/api/sources", "", `{"kind":"static","clone_url":"https://forge.example.invalid/acme/gateway.git"}`, jsonHdr)
	if code != 201 || field(t, body, "project_key") != "gateway" {
		t.Fatalf("create with a URL alone: %d %v", code, body)
	}
	if rs := fs.repos["gateway"]; len(rs) != 1 || rs[0].Slug != "gateway" || rs[0].CloneURL != "https://forge.example.invalid/acme/gateway.git" {
		t.Fatalf("first repository = %+v", rs)
	}
	if nudger.nudges == 0 {
		t.Error("no sync nudged")
	}
	code, body = do(t, h, http.MethodPost, "/api/sources", "",
		`{"project_key":"acme","kind":"static","clone_url":"https://forge.example.invalid/acme/core","slug":"kern"}`, jsonHdr)
	if code != 201 || fs.repos["acme"][0].Slug != "kern" {
		t.Fatalf("create with key and slug: %d %v %+v", code, body, fs.repos["acme"])
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
	// The first repository's slug taken names the slug, not the key.
	fs.taken["portal/shop"] = true
	code, body = do(t, h, http.MethodPost, "/api/sources", "",
		`{"project_key":"portal","kind":"static","clone_url":"https://forge.example.invalid/x/shop","slug":"shop"}`, jsonHdr)
	if code != 409 || !strings.HasPrefix(field(t, body, "error"), "slug:") {
		t.Fatalf("first slug taken: %d %v, want 409 naming the slug", code, body)
	}
	// A wrong kind is named as the kind, a URL with it or not.
	code, body = do(t, h, http.MethodPost, "/api/sources", "", `{"kind":"gitlab","clone_url":"https://forge.example.invalid/x/a"}`, jsonHdr)
	if code != 422 || !strings.HasPrefix(field(t, body, "error"), "kind:") {
		t.Fatalf("unknown kind with a URL: %d %v", code, body)
	}
}

// Adding a repository without a slug takes its name from the URL.
func TestSources_AddRepoSlugFromURL(t *testing.T) {
	fs := newFakeSources()
	h, _, _ := sourcesHandler(t, fs, false)
	if code, _ := do(t, h, http.MethodPost, "/api/sources", "", `{"project_key":"acme","kind":"static"}`, jsonHdr); code != 201 {
		t.Fatal("create")
	}
	code, body := do(t, h, http.MethodPost, "/api/sources/acme/repos", "", `{"clone_url":"https://forge.example.invalid/acme/console.git"}`, jsonHdr)
	if code != 201 || len(fs.repos["acme"]) != 1 || fs.repos["acme"][0].Slug != "console" {
		t.Fatalf("add without a slug: %d %v %+v", code, body, fs.repos["acme"])
	}
	// A bad URL is named as the URL, not as the slug it yields none of.
	code, body = do(t, h, http.MethodPost, "/api/sources/acme/repos", "", `{"clone_url":"forge.example.invalid/x/r"}`, jsonHdr)
	if code != 422 || !strings.HasPrefix(field(t, body, "error"), "clone_url:") {
		t.Fatalf("bad URL without a slug: %d %v", code, body)
	}
}

// A project of one is listed with its repository, has no project page on
// the wire, and exports as its repository.
func TestProjects_Single(t *testing.T) {
	st := newFakeStore()
	st.projects = []store.ProjectRow{{Key: "acme", Repos: 2}, {Key: "loom", Repos: 1}}
	st.teams["loom"], st.teams["acme"] = "", ""
	st.repos["loom"] = []store.RepoRow{{Slug: "loom", Revision: 3, Summary: "Ein Agent."}}
	st.repos["acme"] = []store.RepoRow{{Slug: "core"}, {Slug: "portal"}}
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
	if list[0].Key != "acme" || list[0].Repo != nil || list[1].Repo == nil || list[1].Repo.Summary != "Ein Agent." {
		t.Fatalf("projects = %+v, want loom with its repository, acme without", list)
	}

	code, body := do(t, h, http.MethodGet, "/api/projects/loom", "", "", nil)
	if code != 200 || string(body["single"]) != "true" || string(body["description"]) != "null" || string(body["map_expected"]) != "false" {
		t.Fatalf("loom: %d single %s description %s expected %s", code, body["single"], body["description"], body["map_expected"])
	}
	if code, body = do(t, h, http.MethodGet, "/api/projects/acme", "", "", nil); code != 200 || string(body["single"]) != "false" {
		t.Fatalf("acme: %d single %s", code, body["single"])
	}

	rec := getMD(h, "/api/projects/loom.md", "")
	if rec.Code != 200 || !strings.HasPrefix(rec.Body.String(), "# Repository loom (Projekt loom)") {
		t.Fatalf("loom export: %d %q, want the repository's", rec.Code, rec.Body.String())
	}
}
