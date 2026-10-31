package diagram

import (
	"encoding/json"
	"slices"
	"testing"
)

// On the wire a diagram has its kind's fields and no other, its lists never
// null, a row its type's fields: what the UI's types say.
func TestMarshalPerKind(t *testing.T) {
	for name, c := range map[string]struct {
		d    Diagram
		want string
	}{
		"flow": {Diagram{Kind: KindFlow, Direction: "TB", Nodes: []Node{{ID: "a", Label: "A", Shape: "step"}}},
			`{"kind":"flow","direction":"TB","nodes":[{"id":"a","label":"A","shape":"step"}],"edges":[],"zones":[]}`},
		"sequence": {Diagram{Kind: KindSequence, Actors: []Actor{{ID: "a", Label: "A"}}, Rows: []Row{
			{Type: RowMessage, From: "a", To: "a", Line: "solid", Head: "filled"},
			{Type: RowNote, From: "a", To: "a", Side: "over", Label: "n"},
			{Type: RowFrame, Keyword: "loop"}, {Type: RowDivider, Keyword: "and"}, {Type: RowEnd}}},
			`{"kind":"sequence","actors":[{"id":"a","label":"A"}],"rows":[` +
				`{"type":"message","from":"a","to":"a","label":"","line":"solid","head":"filled"},` +
				`{"type":"note","from":"a","to":"a","side":"over","label":"n"},` +
				`{"type":"frame","keyword":"loop","label":""},{"type":"divider","keyword":"and","label":""},{"type":"end"}]}`},
		"er": {Diagram{Kind: KindER, Direction: "LR", Entities: []Entity{{ID: "e", Label: "E", Attrs: []Attr{{Type: "int", Name: "id"}}}}},
			`{"kind":"er","direction":"LR","entities":[{"id":"e","label":"E","attrs":[{"type":"int","name":"id","keys":[],"comment":""}]}],"rels":[]}`},
		"unknown": {Diagram{Kind: "x"}, `{"kind":"x"}`},
	} {
		b, err := json.Marshal(c.d)
		if err != nil || string(b) != c.want {
			t.Errorf("%s: %s %v\nwant %s", name, b, err, c.want)
		}
		var back Diagram
		if err := json.Unmarshal(b, &back); err != nil {
			t.Errorf("%s: read back: %v", name, err)
		}
		if name != "unknown" && !Equal(&back, &c.d) {
			t.Errorf("%s: round trip = %+v", name, back)
		}
	}
}

func TestEqual(t *testing.T) {
	a := &Diagram{Kind: KindFlow, Direction: "TB", Nodes: []Node{{ID: "a", Label: "A", Shape: "step"}}}
	b := &Diagram{Kind: KindFlow, Direction: "TB", Nodes: []Node{{ID: "a", Label: "A", Shape: "step"}}, Edges: []Edge{}}
	if !Equal(a, b) || !Equal(nil, nil) || Equal(a, nil) || Equal(nil, b) {
		t.Error("Equal of the same diagram, nil, or one nil")
	}
	b.Nodes[0].Label = "B"
	if Equal(a, b) {
		t.Error("Equal of a changed label")
	}
}

// The order of nodes, zones, edges, tables and relationships says nothing;
// the order of a table's fields and of a sequence's rows does.
func TestEqualIgnoresOrderThatSaysNothing(t *testing.T) {
	flow := func(rev bool) *Diagram {
		d := &Diagram{Kind: KindFlow, Direction: "LR",
			Nodes: []Node{{ID: "a", Label: "A", Shape: "step", Zone: "z"}, {ID: "b", Label: "B", Shape: "step", Zone: "y"}},
			Zones: []Zone{{ID: "z", Title: "Z"}, {ID: "y", Title: "Y"}},
			Edges: []Edge{{From: "a", To: "b", Label: "x", Head: "end"}, {From: "a", To: "b", Label: "w", Head: "end"}}}
		if rev {
			slices.Reverse(d.Nodes)
			slices.Reverse(d.Zones)
			slices.Reverse(d.Edges)
		}
		return d
	}
	if !Equal(flow(false), flow(true)) {
		t.Error("a reordered flow counts as changed")
	}
	er := func(rev bool) *Diagram {
		d := &Diagram{Kind: KindER, Direction: "TB",
			Entities: []Entity{{ID: "o", Label: "O", Attrs: []Attr{{Name: "id"}, {Name: "x"}}}, {ID: "l", Label: "L"}},
			Rels:     []Rel{{From: "o", To: "l", Label: "hat", FromCard: "one", ToCard: "one"}, {From: "l", To: "o", Label: "in", FromCard: "one", ToCard: "one"}}}
		if rev {
			slices.Reverse(d.Entities)
			slices.Reverse(d.Rels)
		}
		return d
	}
	if !Equal(er(false), er(true)) {
		t.Error("a reordered entity model counts as changed")
	}
	fields := er(false)
	slices.Reverse(fields.Entities[0].Attrs)
	if Equal(er(false), fields) {
		t.Error("reordered fields count as the same")
	}
	seq := func(rev bool) *Diagram {
		d := &Diagram{Kind: KindSequence, Actors: []Actor{{ID: "a", Label: "A"}},
			Rows: []Row{{Type: RowMessage, From: "a", To: "a", Label: "1"}, {Type: RowMessage, From: "a", To: "a", Label: "2"}}}
		if rev {
			slices.Reverse(d.Rows)
		}
		return d
	}
	if Equal(seq(false), seq(true)) {
		t.Error("reordered rows count as the same")
	}
	// The caller's lists keep their order.
	d := flow(true)
	Equal(d, flow(false))
	if d.Nodes[0].ID != "b" {
		t.Error("Equal sorted the caller's nodes")
	}
}
