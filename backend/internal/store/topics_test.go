package store

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/trick77/almanach/internal/messaging"
)

func saveTopics(t *testing.T, s *Store, key, slug, commit string, read messaging.Read) {
	t.Helper()
	if err := s.SaveTopics(context.Background(), repoIDOf(t, s, key, slug), commit, read); err != nil {
		t.Fatal(err)
	}
}

func topic(name, dir string) messaging.Topic {
	return messaging.Topic{Name: name, Direction: dir, Files: []string{"f"}}
}

func TestTopics_SavedReplacedAndJoined(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	for _, k := range []string{"shop", "ops"} {
		if err := s.CreateSource(ctx, staticSource(k)); err != nil {
			t.Fatal(err)
		}
	}
	addRepos(t, s,
		SourceRepo{ProjectKey: "shop", Slug: "orders", CloneURL: "https://forge.example.invalid/x/orders"},
		SourceRepo{ProjectKey: "shop", Slug: "billing", CloneURL: "https://forge.example.invalid/x/billing"},
		SourceRepo{ProjectKey: "ops", Slug: "audit", CloneURL: "https://forge.example.invalid/x/audit"},
	)
	orders := repoIDOf(t, s, "shop", "orders")
	if _, ok, err := s.TopicsCommit(ctx, orders); ok || err != nil {
		t.Fatalf("TopicsCommit before a read = %v %v, want none", ok, err)
	}
	saveTopics(t, s, "shop", "orders", "c1", messaging.Read{Topics: []messaging.Topic{topic("old.v1", messaging.Produces)}})
	saveTopics(t, s, "shop", "orders", "c2", messaging.Read{
		Topics:  []messaging.Topic{topic("order.placed", messaging.Produces), topic("payment.done", messaging.Consumes)},
		Schemas: []messaging.Schema{{Name: "shop.OrderPlaced", Kind: "avro", File: "o.avsc"}},
	})
	if c, ok, err := s.TopicsCommit(ctx, orders); c != "c2" || !ok || err != nil {
		t.Fatalf("TopicsCommit = %q %v %v, want the second read's", c, ok, err)
	}
	saveTopics(t, s, "shop", "billing", "c1", messaging.Read{Topics: []messaging.Topic{
		topic("order.placed", messaging.Consumes), topic("payment.done", messaging.Produces), topic("ledger", messaging.Declares)}})
	saveTopics(t, s, "ops", "audit", "c1", messaging.Read{Topics: []messaging.Topic{topic("order.placed", messaging.Consumes)}})

	m, err := s.Messaging(ctx)
	if err != nil {
		t.Fatal(err)
	}
	o := RepoRef{ProjectKey: "shop", Slug: "orders"}
	b := RepoRef{ProjectKey: "shop", Slug: "billing"}
	a := RepoRef{ProjectKey: "ops", Slug: "audit"}
	if m.Commit(o) != "c2" || m.Commit(RepoRef{ProjectKey: "x", Slug: "y"}) != "" {
		t.Errorf("Commit(orders) = %q, want the second read's", m.Commit(o))
	}
	if got := m.Of(o); len(got.Topics) != 2 || len(got.Schemas) != 1 {
		t.Errorf("Of(orders) = %+v, want the second read alone", got)
	}
	rt := m.RepoTopics(o)
	if len(rt) != 2 || !reflect.DeepEqual(rt[0].Peers, []RepoRef{a, b}) || !reflect.DeepEqual(rt[1].Peers, []RepoRef{b}) {
		t.Errorf("RepoTopics(orders) = %+v, want the consumers of what it produces, the producer of what it consumes", rt)
	}
	if bt := m.RepoTopics(b); bt[2].Name != "ledger" || len(bt[2].Peers) != 0 {
		t.Errorf("a topic only named has no far side: %+v", bt[2])
	}
	flows := m.ProjectFlows("shop")
	if len(flows) != 3 || flows[1].Name != "order.placed" || !reflect.DeepEqual(flows[1].Producers, []RepoRef{o}) ||
		!reflect.DeepEqual(flows[1].Consumers, []RepoRef{a, b}) || !reflect.DeepEqual(flows[0].Declared, []RepoRef{b}) {
		t.Errorf("ProjectFlows(shop) = %+v", flows)
	}
	if none := m.Of(RepoRef{ProjectKey: "x", Slug: "y"}); none.Topics == nil || none.Schemas == nil {
		t.Error("a repository never read: want empty lists, not nil")
	}

	// Deactivated: its messages go with its manifests.
	if _, err := s.DeactivateSourceRepo(ctx, "ops", "audit"); err != nil {
		t.Fatal(err)
	}
	m, err = s.Messaging(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rt := m.RepoTopics(o); !reflect.DeepEqual(rt[0].Peers, []RepoRef{b}) {
		t.Errorf("after the deactivation: %+v, want billing alone", rt[0].Peers)
	}
	if err := s.SaveTopics(ctx, repoIDOf(t, s, "ops", "audit"), "c2", messaging.Read{}); !errors.Is(err, ErrNoRepo) {
		t.Errorf("save for a deactivated repository: %v, want ErrNoRepo", err)
	}
}
