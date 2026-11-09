package store

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/trick77/almanach/internal/endpoints"
	"github.com/trick77/almanach/internal/messaging"
)

func httpAPI(name, base, dir string, routes ...string) endpoints.Read {
	r := endpoints.Read{APIs: []endpoints.API{{Kind: endpoints.HTTP, Name: name, BasePath: base, Direction: dir, Files: []string{"api.yaml"}}}}
	for i := 0; i+1 < len(routes); i += 2 {
		r.Endpoints = append(r.Endpoints, endpoints.Endpoint{Kind: endpoints.HTTP, API: name, Method: routes[i], Path: routes[i+1],
			Direction: dir, Files: []string{"api.yaml"}})
	}
	return r
}

func TestInterfaces_SavedQueriedServed(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	for _, k := range []string{"shop", "ops"} {
		if err := s.CreateSource(ctx, staticSource(k)); err != nil {
			t.Fatal(err)
		}
	}
	addRepos(t, s,
		SourceRepo{ProjectKey: "shop", Slug: "orders", CloneURL: "https://forge.example.invalid/x/orders"},
		SourceRepo{ProjectKey: "shop", Slug: "web", CloneURL: "https://forge.example.invalid/x/web"},
		SourceRepo{ProjectKey: "ops", Slug: "audit", CloneURL: "https://forge.example.invalid/x/audit"},
	)
	orders := repoIDOf(t, s, "shop", "orders")
	if _, ok, err := s.EndpointsCommit(ctx, orders); ok || err != nil {
		t.Fatalf("EndpointsCommit before a read = %v %v, want none", ok, err)
	}
	save := func(slug, key, commit string, r endpoints.Read) {
		t.Helper()
		if err := s.SaveEndpoints(ctx, repoIDOf(t, s, key, slug), commit, r); err != nil {
			t.Fatal(err)
		}
	}
	save("orders", "shop", "c1", httpAPI("Old", "", endpoints.Offers, "GET", "/old"))
	save("orders", "shop", "c2", httpAPI("Orders API", "/v1", endpoints.Offers, "GET", "/v1/orders", "GET", "/v1/orders/{id}", "*", "/v1/orders/{id}/notes"))
	save("web", "shop", "c1", httpAPI("Orders API", "/v1", endpoints.Declares, "GET", "/v1/orders"))
	save("audit", "ops", "c1", httpAPI("Audit", "", endpoints.Offers, "GET", "/v1/orders/{id}"))
	if err := s.SaveTopics(ctx, orders, "c2", messaging.Read{Topics: []messaging.Topic{{Name: "order.placed", Direction: messaging.Produces, Files: []string{"S.java"}}}}); err != nil {
		t.Fatal(err)
	}
	if c, ok, err := s.EndpointsCommit(ctx, orders); c != "c2" || !ok || err != nil {
		t.Fatalf("EndpointsCommit = %q %v %v", c, ok, err)
	}

	in, err := s.Interfaces(ctx)
	if err != nil {
		t.Fatal(err)
	}
	o := RepoRef{ProjectKey: "shop", Slug: "orders"}
	if got := in.Of(o); len(got.APIs) != 1 || got.APIs[0].Name != "Orders API" || len(got.Endpoints) != 3 || in.Commit(o) != "c2" {
		t.Fatalf("Of(orders) = %+v at %q, want the second read alone", got, in.Commit(o))
	}
	if none := in.Of(RepoRef{ProjectKey: "x", Slug: "y"}); none.APIs == nil || none.Endpoints == nil {
		t.Error("never read: want empty lists, not nil")
	}

	shop := in.Query(InterfaceFilter{Project: "shop"})
	if len(shop) != 2 || shop[0].Repo != o || shop[1].Repo.Slug != "web" {
		t.Fatalf("Query(shop) = %+v", shop)
	}
	if want := fmt.Sprintf("%d:http:Orders API", orders); shop[0].Key != want || shop[0].Commit != "c2" || len(shop[0].Endpoints) != 3 {
		t.Errorf("key, commit, endpoints = %q %q %d, want %q", shop[0].Key, shop[0].Commit, len(shop[0].Endpoints), want)
	}
	if got := in.Query(InterfaceFilter{Direction: endpoints.Offers, API: "orders api"}); len(got) != 1 || got[0].Repo != o {
		t.Errorf("Query(offers, by name any case) = %+v", got)
	}
	if got := in.Query(InterfaceFilter{Kind: endpoints.GRPC}); len(got) != 0 {
		t.Errorf("Query(grpc) = %+v, want none", got)
	}
	if k := EndpointKey(shop[0].Key, shop[0].Endpoints[1]); k != shop[0].Key+":http GET /v1/orders/{}" {
		t.Errorf("EndpointKey = %q", k)
	}

	sys := in.System(mustMessages(t, s), "shop")
	if len(sys) != 3 || sys[2].Kind != TopicKind || sys[2].Name != "order.placed" || sys[2].Direction != messaging.Produces ||
		sys[2].Repo != o || sys[2].Commit != "c2" {
		t.Errorf("System(shop) = %+v, want the two APIs, then the topic", sys)
	}

	// A concrete call: with or without the base path; ambiguous kept whole.
	hit := in.Serving(InterfaceFilter{Project: "shop"}, "get", "/v1/orders/42")
	if len(hit) != 1 || hit[0].Endpoint.Path != "/v1/orders/{id}" || hit[0].Repo != o {
		t.Errorf("Serving(GET /v1/orders/42) = %+v", hit)
	}
	if hit := in.Serving(InterfaceFilter{Project: "shop"}, "GET", "/orders/42"); len(hit) != 1 {
		t.Errorf("without the base path: %+v", hit)
	}
	if hit := in.Serving(InterfaceFilter{}, "GET", "/v1/orders/42"); len(hit) != 2 {
		t.Errorf("two repositories serve it: %+v, want both", hit)
	}
	if hit := in.Serving(InterfaceFilter{Project: "shop"}, "DELETE", "/v1/orders/7/notes"); len(hit) != 1 {
		t.Errorf("a route for every method: %+v", hit)
	}
	if hit := in.Serving(InterfaceFilter{Project: "shop"}, "POST", "/v1/orders"); len(hit) != 0 {
		t.Errorf("no route for POST, and web only declares: %+v", hit)
	}
	// A base path ends at a segment: /v1 is no prefix of /v1x.
	odd := NewInterfaces(map[RepoRef]endpoints.Read{o: httpAPI("X", "/v1", endpoints.Offers, "GET", "/v1x/a")}, nil, nil)
	if hit := odd.Serving(InterfaceFilter{}, "GET", "x/a"); len(hit) != 0 {
		t.Errorf("base path cut mid-segment: %+v", hit)
	}
	// A version in the name endpoints added leaves the key as the first
	// version's was.
	if apiKey(7, "http", "Orders 1.0", "1.0") != apiKey(7, "http", "Orders", "1.0") || apiKey(7, "http", "Orders", "") != "7:http:Orders" {
		t.Error("key moved with the name's version")
	}

	if _, err := s.DeactivateSourceRepo(ctx, "ops", "audit"); err != nil {
		t.Fatal(err)
	}
	if in, err = s.Interfaces(ctx); err != nil {
		t.Fatal(err)
	}
	if got := in.Query(InterfaceFilter{Project: "ops"}); len(got) != 0 {
		t.Errorf("after the deactivation: %+v", got)
	}
	if err := s.SaveEndpoints(ctx, repoIDOf(t, s, "ops", "audit"), "c2", endpoints.Read{}); !errors.Is(err, ErrNoRepo) {
		t.Errorf("save for a deactivated repository: %v, want ErrNoRepo", err)
	}
}

func mustMessages(t *testing.T, s *Store) Messages {
	t.Helper()
	m, err := s.Messaging(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return m
}
