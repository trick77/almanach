package consolidate

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/trick77/almanach/internal/baseline"
	"github.com/trick77/almanach/internal/endpoints"
	"github.com/trick77/almanach/internal/inference"
	"github.com/trick77/almanach/internal/store"
)

func jobsAPI(n int) endpoints.Read {
	r := endpoints.Read{APIs: []endpoints.API{
		{Kind: endpoints.HTTP, Name: "Jobs", Version: "1.0", BasePath: "/v1", Direction: endpoints.Offers, Files: []string{"api.yaml", "Jobs.java"}},
		{Kind: endpoints.GRPC, Name: "stock.Stock", Direction: endpoints.Declares, Files: []string{"stock.proto"}},
	}}
	for i := 0; i < n; i++ {
		seg := []string{"jobs", "runs"}[i%2]
		r.Endpoints = append(r.Endpoints, endpoints.Endpoint{Kind: endpoints.HTTP, API: "Jobs", Method: "GET",
			Path: fmt.Sprintf("/v1/%s/%d", seg, i), Summary: "Eins", Direction: endpoints.Offers})
	}
	r.Endpoints = append(r.Endpoints, endpoints.Endpoint{Kind: endpoints.GRPC, API: "stock.Stock", Method: endpoints.RPC,
		Path: "/stock.Stock/Check", Direction: endpoints.Declares})
	return r
}

// A describe step reads the interfaces the repository's code offers or
// declares, every operation while they fit, folded by path past it; a
// project step those its repositories offer.
func TestInterfacesShown(t *testing.T) {
	svc := store.RepoRef{ProjectKey: "tools", Slug: "svc"}
	in := store.NewInterfaces(map[store.RepoRef]endpoints.Read{svc: jobsAPI(2)}, nil, nil)
	want := "- HTTP API `Jobs` 1.0, base path `/v1` (`api.yaml`, `Jobs.java`)\n  - `GET /v1/jobs/0`: Eins\n  - `GET /v1/runs/1`: Eins\n" +
		"- gRPC service `stock.Stock`, declared only: its contract is here, no code here serves it (likely another service's, for a client) (`stock.proto`)\n" +
		"  - `RPC /stock.Stock/Check`"
	if got := interfacesShown(in, svc, "c1"); got != want {
		t.Errorf("interfacesShown =\n%s\nwant\n%s", got, want)
	}
	web := store.RepoRef{ProjectKey: "tools", Slug: "web"}
	if got := interfacesShown(in, web, "c1"); !strings.HasPrefix(got, "_Not read from the code yet") {
		t.Errorf("never read: %q, want it said, not none", got)
	}
	empty := store.NewInterfaces(map[store.RepoRef]endpoints.Read{web: {}}, nil, nil)
	if got := interfacesShown(empty, web, "c1"); got != "_None._" {
		t.Errorf("read, none offered: %q", got)
	}
	later := store.NewInterfaces(map[store.RepoRef]endpoints.Read{svc: jobsAPI(1)}, nil, map[store.RepoRef]string{svc: "c2"})
	if got := interfacesShown(later, svc, "c1"); !strings.HasPrefix(got, "_Not read at this run's commit") {
		t.Errorf("another commit's interfaces: %q", got)
	}
	big := store.NewInterfaces(map[store.RepoRef]endpoints.Read{svc: jobsAPI(2000)}, nil, nil)
	got := interfacesShown(big, svc, "c1")
	if len(got) > baseline.InterfacesMaxBytes || !strings.Contains(got, "  - `/jobs…`: 1000 operations (GET)\n  - `/runs…`: 1000 operations (GET)") ||
		!strings.Contains(got, "`stock.Stock`") || strings.Contains(got, "more interfaces") {
		t.Errorf("folded, none left out: %d bytes\n%s", len(got), got)
	}

	sys := systemInterfacesShown(store.NewInterfaces(map[store.RepoRef]endpoints.Read{svc: jobsAPI(3)}, nil, nil), "tools")
	if sys != "- `svc`: HTTP API `Jobs` 1.0, base path `/v1` (`api.yaml`, `Jobs.java`), 3 operations" {
		t.Errorf("systemInterfacesShown = %q, want the offered one alone", sys)
	}
	if got := systemInterfacesShown(store.Interfaces{}, "tools"); got != "_None._" {
		t.Errorf("none: %q", got)
	}

	x := newWorld(t, 16)
	x.st.interfaces = map[store.RepoRef]endpoints.Read{svc: jobsAPI(1)}
	x.queue(store.RunFull, "")
	x.models.catalogue = []inference.CatalogueResult{{Call: called, Catalogue: inference.Catalogue{Subjects: []inference.Subject{
		{Name: "Anmeldung", Purpose: "Login.", Paths: []string{"auth/"}},
	}}}}
	x.models.patches = []inference.PatchResult{
		{Call: called, Patch: inference.Patch{Summary: ref("Ein Service."), Statements: []inference.Statement{stmt(nil, "component", "Service", "Der Service.", "README.md")}}},
		{Call: called, Patch: inference.Patch{Statements: []inference.Statement{stmt(nil, "rule", "Login", "Zweistufig.", "auth/login.go")}}},
	}
	x.models.project = inference.DescriptionResult{Call: called}
	x.models.repoMap = inference.RepoMapResult{Call: called, Map: drawnRepoMap()}
	x.w.drain(context.Background())
	described, project := 0, 0
	for _, p := range x.models.prompts {
		if strings.Contains(p, "## Interfaces this repository's code offers or declares\n\n- HTTP API `Jobs` 1.0") {
			described++
		}
		if strings.Contains(p, "read by code\n\n") && strings.Contains(p, "- `svc`: HTTP API `Jobs` 1.0, base path `/v1` (`api.yaml`, `Jobs.java`), 1 operation") {
			project++
		}
	}
	if described != 2 {
		t.Errorf("describe prompts with the interfaces: %d, want both steps", described)
	}
	if project != 1 {
		t.Errorf("project prompts with the system's interfaces: %d, want one", project)
	}
}
