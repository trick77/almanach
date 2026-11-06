package inference

import (
	"context"
	"testing"

	"github.com/trick77/toolkit/llmwire/llmwiretest"
)

// A brief comes as blocks and leaves as the stored layout; an empty or
// missing one is no answer.
func TestBrief(t *testing.T) {
	srv := llmwiretest.NewServer(t)
	srv.SetReply(`{"brief": [{"kind": "paragraph", "text": "Der Dienst rechnet ab."},
		{"kind": "heading", "text": "Was gelten muss"},
		{"kind": "list", "text": "", "items": ["Beträge in Rappen.", "Nie doppelt buchen."]}]}`)
	res := testClient(srv.Client()).Brief(context.Background(), "brief this")
	if res.Outcome != OutcomeOK {
		t.Fatalf("Outcome = %s err %v", res.Outcome, res.Err)
	}
	want := "Der Dienst rechnet ab.\n\n#### Was gelten muss\n\n- Beträge in Rappen.\n- Nie doppelt buchen."
	if res.Text != want {
		t.Fatalf("Text = %q, want %q", res.Text, want)
	}
	if name := schemaName(t, srv); name != BriefSchemaName {
		t.Errorf("schema = %q", name)
	}
	for _, reply := range []string{"no json", `{"brief": null}`, `{}`, "null", `{"brief": "  "}`} {
		srv.SetReply(reply)
		if res := testClient(srv.Client()).Brief(context.Background(), "p"); res.Outcome != OutcomeUnparseable || res.Text != "" {
			t.Errorf("reply %q: Outcome = %s text %q, want unparseable", reply, res.Outcome, res.Text)
		}
	}
}
