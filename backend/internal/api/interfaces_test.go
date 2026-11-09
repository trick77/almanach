package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/trick77/almanach/internal/endpoints"
	"github.com/trick77/almanach/internal/store"
)

// A repository's page lists the interfaces its code offers and those it
// only declares, each with every operation; the project page the offered
// ones of all its repositories; the exports say the same, whole. With the
// sources off: none.
func TestInterfaces_RepoProjectAndExport(t *testing.T) {
	st := depsStore()
	loom, rongo := store.RepoRef{ProjectKey: "apps", Slug: "loom"}, store.RepoRef{ProjectKey: "apps", Slug: "rongo"}
	ep := func(api, method, path, sum, dir string) endpoints.Endpoint {
		return endpoints.Endpoint{Kind: endpoints.HTTP, API: api, Method: method, Path: path, Summary: sum, Direction: dir, Files: []string{"api.yaml"}}
	}
	st.interfaces = map[store.RepoRef]endpoints.Read{
		loom: {
			APIs: []endpoints.API{
				{Kind: endpoints.HTTP, Name: "Jobs API", Version: "1.2", BasePath: "/v1", Direction: endpoints.Offers, Files: []string{"api.yaml", "Jobs.java"}},
				{Kind: endpoints.HTTP, Name: "Stock", Direction: endpoints.Declares, Files: []string{"stock.yaml"}},
			},
			Endpoints: []endpoints.Endpoint{
				ep("Jobs API", "GET", "/v1/jobs", "List jobs", endpoints.Offers),
				ep("Jobs API", "GET", "/v1/jobs/{id}", "", endpoints.Offers),
				ep("Stock", "GET", "/stock", "", endpoints.Declares),
			},
		},
		rongo: {
			APIs:      []endpoints.API{{Kind: endpoints.GRPC, Name: "rongo.Runner", Direction: endpoints.Offers, Files: []string{"r.proto"}}},
			Endpoints: []endpoints.Endpoint{{Kind: endpoints.GRPC, API: "rongo.Runner", Method: endpoints.RPC, Path: "/rongo.Runner/Run", Direction: endpoints.Offers}},
		},
	}
	h, _, _ := sourcesHandlerWith(t, newFakeSources(), st, &fakeWaker{}, false)
	code, body := do(t, h, http.MethodGet, "/api/projects/apps/repos/loom", "", "", nil)
	var repo []interfaceBody
	if err := json.Unmarshal(body["interfaces"], &repo); code != 200 || err != nil {
		t.Fatalf("repo: %d %v", code, err)
	}
	if len(repo) != 2 || repo[0].Name != "Jobs API" || len(repo[0].Endpoints) != 2 || repo[1].Direction != endpoints.Declares ||
		repo[0].Key == "" || repo[0].Commit != "c0ffee" || repo[0].Repo.Slug != "loom" {
		t.Errorf("loom interfaces = %+v", repo)
	}
	code, body = do(t, h, http.MethodGet, "/api/projects/apps", "", "", nil)
	var project []interfaceBody
	if err := json.Unmarshal(body["interfaces"], &project); code != 200 || err != nil {
		t.Fatalf("project: %d %v", code, err)
	}
	if len(project) != 2 || project[0].Name != "Jobs API" || project[1].Name != "rongo.Runner" ||
		project[1].Endpoints[0].Files == nil {
		t.Errorf("project interfaces = %+v, want the offered ones of both", project)
	}

	rec := getMD(h, "/api/projects/apps/repos/loom.md", "")
	want := "## Schnittstellen\n\n" +
		"- HTTP `Jobs API` `1.2`, Basis `/v1` (`api.yaml`, `Jobs.java`)\n  - `GET /v1/jobs`: List jobs\n  - `GET /v1/jobs/{id}`\n" +
		"- HTTP `Stock`, nur genannt (kein Server im Code) (`stock.yaml`)\n  - `GET /stock`\n"
	if !strings.Contains(rec.Body.String(), want) {
		t.Errorf("repo export: %q, want %q in it", rec.Body.String(), want)
	}
	rec = getMD(h, "/api/projects/apps.md", "")
	wantProject := "## Schnittstellen\n\n- `apps/loom`: HTTP `Jobs API`"
	if !strings.Contains(rec.Body.String(), wantProject) || !strings.Contains(rec.Body.String(), "- `apps/rongo`: gRPC `rongo.Runner` (`r.proto`)\n  - `RPC /rongo.Runner/Run`\n") ||
		strings.Contains(rec.Body.String(), "Stock") {
		t.Errorf("project export: %q", rec.Body.String())
	}
	if rec = getMD(h, "/api/projects/apps/repos/fetch.md", ""); strings.Contains(rec.Body.String(), "Schnittstellen") {
		t.Errorf("export without interfaces: %q", rec.Body.String())
	}

	st.graphErr = errors.New("must not be asked")
	off := handler(t, st, &fakeWaker{}, "")
	code, body = do(t, off, http.MethodGet, "/api/projects/libs/repos/wire", "", "", nil)
	if code != 200 || string(body["interfaces"]) != `[]` {
		t.Errorf("sources off: %d %s", code, body["interfaces"])
	}
}
