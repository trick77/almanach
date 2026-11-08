package baseline

import (
	"strings"
	"testing"

	ir "github.com/trick77/almanach/internal/diagram"
)

// A label made English stays within the cap, so a stored map passes every
// later check: one the English would push over keeps its German word. A
// repository node labelled by its slug keeps the slug, an identifier.
func TestLabelsKeepTheCapAndTheSlugs(t *testing.T) {
	near := "Liest die Umgebungsvariable " + strings.Repeat("x", labelMaxChars-len([]rune("Liest die Umgebungsvariable ")))
	if got, why := label("node a", near, true); why != "" || got != near {
		t.Errorf("label at the cap = %q, %q; want it kept as written", got, why)
	}
	if got, _ := label("node a", "Der Dienst", true); got != "Der Service" {
		t.Errorf("label = %q, want the word in English", got)
	}
	m := &ir.Diagram{Kind: ir.KindFlow, Direction: "LR",
		Nodes: []ir.Node{{ID: "Nachricht", Label: "Nachricht", Shape: "step", Zone: ir.ZoneRepositories},
			{ID: "q", Label: "Warteschlange", Shape: "step", Zone: "stores"}},
		Zones: []ir.Zone{{ID: ir.ZoneRepositories}, {ID: "stores"}}}
	checked, why := CheckMap(m, []string{"Nachricht"}, DiagramCaps{MaxNodes: 12, MaxEdges: 20})
	if why != "" || checked.Nodes[0].Label != "Nachricht" || checked.Nodes[1].Label != "Queue" {
		t.Fatalf("CheckMap = %+v, %q; want the slug kept and the rest English", checked, why)
	}
	// Stored again: CheckMap passes it unchanged.
	if again, why := CheckMap(checked, []string{"Nachricht"}, DiagramCaps{MaxNodes: 12, MaxEdges: 20}); why != "" || !ir.Equal(again, checked) {
		t.Errorf("a checked map fails or moves when checked again: %q", why)
	}

	// Respeller: the same rule on a stored map, the slug kept, a change only
	// where a label moved.
	stored := &ir.Diagram{Kind: ir.KindFlow, Nodes: []ir.Node{{ID: "Nachricht", Label: "Nachricht", Zone: ir.ZoneRepositories},
		{ID: "q", Label: "Warteschlange", Zone: "stores"}, {ID: "n", Label: near, Zone: "stores"}}}
	if !(Respeller{}).Diagram(stored) || stored.Nodes[0].Label != "Nachricht" || stored.Nodes[1].Label != "Queue" || stored.Nodes[2].Label != near {
		t.Errorf("Respeller.Diagram = %+v", stored.Nodes)
	}
	if (Respeller{}).Diagram(stored) {
		t.Error("Respeller.Diagram changed a respelled map again")
	}
	slugOnly := &ir.Diagram{Kind: ir.KindFlow, Nodes: []ir.Node{{ID: "Nachricht", Label: "Nachricht", Zone: ir.ZoneRepositories}}}
	if (Respeller{}).Diagram(slugOnly) {
		t.Error("a map of slugs alone reads as changed")
	}
}
