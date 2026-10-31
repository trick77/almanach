package baseline

import (
	"strings"
	"testing"

	ir "github.com/trick77/almanach/internal/diagram"
	"github.com/trick77/almanach/internal/inference"
	"github.com/trick77/almanach/internal/store"
)

var caps = DiagramCaps{MaxNodes: 4, MaxEdges: 3}

func flowOf(nodes ...string) *ir.Diagram {
	d := &ir.Diagram{Kind: ir.KindFlow, Direction: "TB"}
	for _, n := range nodes {
		d.Nodes = append(d.Nodes, ir.Node{ID: n, Label: strings.ToUpper(n), Shape: "step"})
	}
	return d
}

func edge(from, to, label string) ir.Edge {
	return ir.Edge{From: from, To: to, Label: label, Head: "end"}
}

func msg(from, to string) ir.Row {
	return ir.Row{Type: ir.RowMessage, From: from, To: to, Label: "ruft", Line: "solid", Head: "filled"}
}

func seqOf(rows ...ir.Row) *ir.Diagram {
	return &ir.Diagram{Kind: ir.KindSequence, Actors: []ir.Actor{{ID: "a", Label: "A"}, {ID: "b", Label: "B"}}, Rows: rows}
}

func erOf(rels ...ir.Rel) *ir.Diagram {
	return &ir.Diagram{Kind: ir.KindER, Direction: "LR", Entities: []ir.Entity{
		{ID: "order", Label: "Auftrag", Attrs: []ir.Attr{{Type: "uuid", Name: "id", Keys: []string{"PK"}}}},
		{ID: "line", Label: "Position", Attrs: []ir.Attr{{Type: "uuid", Name: "order_id", Keys: []string{"FK"}, Comment: "Gehört zum Auftrag"}}},
	}, Rels: rels}
}

func rel(from, to string) ir.Rel {
	return ir.Rel{From: from, To: to, Label: "hat", FromCard: "one", ToCard: "zeroOrMore"}
}

// A diagram that holds comes back as stored: labels trimmed and Swiss, a
// zone without a node gone, a start without text.
func TestCheckDiagramCleans(t *testing.T) {
	d := flowOf("a", "b")
	d.Nodes[0].Label = "  Grösse prüfen  "
	d.Nodes[1].Label = "Maß"
	d.Nodes = append(d.Nodes, ir.Node{ID: "s", Label: "Start", Shape: "start", Zone: "z"})
	d.Zones = []ir.Zone{{ID: "z", Title: "Zone"}, {ID: "empty", Title: "Leer"}}
	d.Edges = []ir.Edge{edge("s", "a", ""), edge("a", "b", " weiter ")}
	out, reason := CheckDiagram(d, caps)
	if reason != "" {
		t.Fatalf("reason = %q", reason)
	}
	if out.Nodes[0].Label != "Grösse prüfen" || out.Nodes[1].Label != "Mass" || out.Nodes[2].Label != "" || out.Edges[1].Label != "weiter" {
		t.Errorf("cleaned = %+v", out)
	}
	if len(out.Zones) != 1 || out.Zones[0].ID != "z" {
		t.Errorf("zones = %+v, want the empty one gone", out.Zones)
	}
	if d, reason := CheckDiagram(nil, caps); d != nil || reason != "" {
		t.Errorf("nil = %v %q", d, reason)
	}
	for _, good := range []*ir.Diagram{
		seqOf(msg("a", "b"), ir.Row{Type: ir.RowFrame, Keyword: "alt", Label: "ok"}, msg("b", "a"),
			ir.Row{Type: ir.RowDivider, Keyword: "else"}, ir.Row{Type: ir.RowNote, From: "a", To: "a", Side: "over", Label: "x"},
			ir.Row{Type: ir.RowEnd}, msg("a", "a")),
		erOf(rel("order", "line")),
	} {
		if _, reason := CheckDiagram(good, DiagramCaps{MaxNodes: 4, MaxEdges: 6}); reason != "" {
			t.Errorf("%s: reason = %q", good.Kind, reason)
		}
	}
}

func TestCheckDiagramRefuses(t *testing.T) {
	long := strings.Repeat("x", labelMaxChars+1)
	cases := map[string]struct {
		d    *ir.Diagram
		want string
	}{
		"unknown kind":   {&ir.Diagram{Kind: "gantt"}, "unknown diagram kind"},
		"flow direction": {func() *ir.Diagram { d := flowOf("a"); d.Direction = "BT"; return d }(), "direction"},
		"flow empty":     {flowOf(), "0 nodes"},
		"flow too many":  {flowOf("a", "b", "c", "d", "e"), "5 nodes, not 1 to 4"},
		"flow edges cap": {func() *ir.Diagram {
			d := flowOf("a", "b")
			d.Edges = []ir.Edge{edge("a", "b", "1"), edge("a", "b", "2"), edge("b", "a", "3"), edge("b", "a", "4")}
			return d
		}(), "4 edges"},
		"flow dangling":  {func() *ir.Diagram { d := flowOf("a"); d.Edges = []ir.Edge{edge("a", "ghost", "")}; return d }(), "no declared node"},
		"flow self edge": {func() *ir.Diagram { d := flowOf("a"); d.Edges = []ir.Edge{edge("a", "a", "")}; return d }(), "points at itself"},
		"flow duplicate": {func() *ir.Diagram {
			d := flowOf("a", "b")
			d.Edges = []ir.Edge{edge("a", "b", "x"), edge("a", "b", " x")}
			return d
		}(), "twice"},
		"flow head": {func() *ir.Diagram {
			d := flowOf("a", "b")
			d.Edges = []ir.Edge{{From: "a", To: "b", Head: "arrow"}}
			return d
		}(), "unknown head"},
		"node id twice":     {flowOf("a", "a"), `node id "a" twice`},
		"node id padded":    {flowOf(" a"), "empty or padded"},
		"node id long":      {flowOf(strings.Repeat("n", idMaxChars+1)), "over 64"},
		"node shape":        {func() *ir.Diagram { d := flowOf("a"); d.Nodes[0].Shape = "cloud"; return d }(), "unknown shape"},
		"node label empty":  {func() *ir.Diagram { d := flowOf("a"); d.Nodes[0].Label = " "; return d }(), "empty label"},
		"node label long":   {func() *ir.Diagram { d := flowOf("a"); d.Nodes[0].Label = long; return d }(), "label over 80"},
		"node zone":         {func() *ir.Diagram { d := flowOf("a"); d.Nodes[0].Zone = "nowhere"; return d }(), "undeclared zone"},
		"seq no actor":      {&ir.Diagram{Kind: ir.KindSequence, Rows: []ir.Row{msg("a", "b")}}, "0 participants"},
		"seq dangling":      {seqOf(msg("a", "c")), "no declared participant"},
		"seq note dangling": {seqOf(msg("a", "b"), ir.Row{Type: ir.RowNote, From: "c", To: "c", Side: "over"}), "no declared participant"},
		"seq no message":    {seqOf(), "without a message"},
		"seq line":          {seqOf(ir.Row{Type: ir.RowMessage, From: "a", To: "b", Line: "dotted", Head: "filled"}), "unknown line"},
		"seq side":          {seqOf(msg("a", "b"), ir.Row{Type: ir.RowNote, From: "a", To: "a", Side: "under"}), "unknown side"},
		"seq frame open":    {seqOf(ir.Row{Type: ir.RowFrame, Keyword: "loop"}, msg("a", "b")), "left open"},
		"seq frame word":    {seqOf(ir.Row{Type: ir.RowFrame, Keyword: "rect"}, msg("a", "b"), ir.Row{Type: ir.RowEnd}), "unknown frame"},
		"seq divider":       {seqOf(msg("a", "b"), ir.Row{Type: ir.RowDivider, Keyword: "else"}), "outside any frame"},
		"seq divider word":  {seqOf(ir.Row{Type: ir.RowFrame, Keyword: "alt"}, ir.Row{Type: ir.RowDivider, Keyword: "otherwise"}, msg("a", "b"), ir.Row{Type: ir.RowEnd}), "unknown divider"},
		"seq end":           {seqOf(msg("a", "b"), ir.Row{Type: ir.RowEnd}), "outside any frame"},
		"seq deep":          {seqOf(ir.Row{Type: ir.RowFrame, Keyword: "loop"}, ir.Row{Type: ir.RowFrame, Keyword: "loop"}, ir.Row{Type: ir.RowFrame, Keyword: "loop"}, ir.Row{Type: ir.RowFrame, Keyword: "loop"}), "deeper than 3"},
		"seq row type":      {seqOf(msg("a", "b"), ir.Row{Type: "activate"}), "unknown row type"},
		"seq rows cap":      {seqOf(msg("a", "b"), msg("b", "a"), msg("a", "b"), msg("b", "a")), "4 messages and notes, over the cap of 3"},
		"er dangling":       {erOf(rel("order", "invoice")), "no declared entity"},
		"er self":           {erOf(rel("order", "order")), "points at itself"},
		"er duplicate":      {erOf(rel("order", "line"), rel("order", "line")), "twice"},
		"er card":           {erOf(ir.Rel{From: "order", To: "line", FromCard: "many", ToCard: "one"}), "unknown cardinality"},
		"er direction":      {func() *ir.Diagram { d := erOf(); d.Direction = ""; return d }(), "direction"},
		"er field name":     {func() *ir.Diagram { d := erOf(); d.Entities[0].Attrs[0].Name = ""; return d }(), "without a name"},
		"er field key":      {func() *ir.Diagram { d := erOf(); d.Entities[0].Attrs[0].Keys = []string{"PK", "PK"}; return d }(), "unknown or twice"},
		"er field long":     {func() *ir.Diagram { d := erOf(); d.Entities[0].Attrs[0].Type = long; return d }(), "name or type over"},
		"er entity label":   {func() *ir.Diagram { d := erOf(); d.Entities[0].Label = ""; return d }(), "empty label"},
		"er too many":       {&ir.Diagram{Kind: ir.KindER, Direction: "TB"}, "0 entities"},
		"er rels cap":       {erOf(rel("order", "line"), rel("line", "order"), ir.Rel{From: "order", To: "line", Label: "b", FromCard: "one", ToCard: "one"}, ir.Rel{From: "order", To: "line", Label: "c", FromCard: "one", ToCard: "one"}), "4 relationships"},
	}
	for name, c := range cases {
		_, reason := CheckDiagram(c.d, caps)
		if !strings.Contains(reason, c.want) {
			t.Errorf("%s: reason = %q, want %q", name, reason, c.want)
		}
	}
}

// The drawing's own ceilings hold under a higher config cap: an entity
// model of nine tables is not drawn, so it is not stored.
func TestCheckDiagramDrawingCeilings(t *testing.T) {
	wide := DiagramCaps{MaxNodes: 16, MaxEdges: 24}
	d := erOf()
	for i := range 7 {
		d.Entities = append(d.Entities, ir.Entity{ID: "e" + string(rune('a'+i)), Label: "E", Attrs: []ir.Attr{}})
	}
	if _, reason := CheckDiagram(d, wide); !strings.Contains(reason, "9 entities, not 1 to 8") {
		t.Errorf("reason = %q", reason)
	}
	s := seqOf()
	for i := range 9 {
		s.Actors = append(s.Actors, ir.Actor{ID: "x" + string(rune('a'+i)), Label: "X"})
	}
	if _, reason := CheckDiagram(s, wide); !strings.Contains(reason, "11 participants, not 1 to 8") {
		t.Errorf("reason = %q", reason)
	}
	// Frames, dividers and ends take a row of the drawing too: 21 rows are
	// one too many though only five are messages.
	rows := []ir.Row{msg("a", "b")}
	for range 4 {
		rows = append(rows, ir.Row{Type: ir.RowFrame, Keyword: "alt"}, msg("a", "b"), ir.Row{Type: ir.RowDivider, Keyword: "else"},
			ir.Row{Type: ir.RowDivider, Keyword: "else"}, ir.Row{Type: ir.RowEnd})
	}
	if _, reason := CheckDiagram(seqOf(rows...), wide); !strings.Contains(reason, "21 rows, over the cap of 20") {
		t.Errorf("21 rows: reason = %q", reason)
	}
	if _, reason := CheckDiagram(seqOf(rows[:20]...), wide); reason != "a frame left open" {
		t.Errorf("20 rows: reason = %q, want only the open frame", reason)
	}
}

func mapOf(nodes ...ir.Node) *ir.Diagram {
	return &ir.Diagram{Kind: ir.KindFlow, Direction: "LR", Nodes: nodes,
		Zones: []ir.Zone{{ID: ir.ZoneRepositories, Title: "Repos"}, {ID: ir.ZoneStores, Title: "Speicher"}, {ID: ir.ZoneExternal, Title: "Extern"}}}
}

func node(id, zone string) ir.Node { return ir.Node{ID: id, Label: id, Shape: "step", Zone: zone} }

func TestCheckMap(t *testing.T) {
	slugs := []string{"api", "web"}
	good := mapOf(node("api", ir.ZoneRepositories), node("pg", ir.ZoneStores))
	good.Edges = []ir.Edge{edge("api", "pg", "DB")}
	out, reason := CheckMap(good, slugs, caps)
	if reason != "" || len(out.Zones) != 2 {
		t.Fatalf("map = %+v, reason %q; want held, the unused zone gone", out, reason)
	}
	// A zone without a title holds: the UI titles zones by id. A node
	// without a label does not.
	untitled := mapOf(node("api", ir.ZoneRepositories))
	untitled.Zones[0].Title = " "
	if out, reason := CheckMap(untitled, slugs, caps); reason != "" || out.Zones[0].Title != "" {
		t.Errorf("untitled zone: %+v, reason %q", out, reason)
	}
	unlabelled := mapOf(node("api", ir.ZoneRepositories), node("pg", ir.ZoneStores))
	unlabelled.Nodes[1].Label = ""
	if _, reason := CheckMap(unlabelled, slugs, caps); !strings.Contains(reason, "empty label") {
		t.Errorf("unlabelled node: reason %q", reason)
	}
	// A repository node is labelled by its slug, whatever was written, but
	// for a slug longer than a label; the map handed in stays as it was.
	long := strings.Repeat("r", labelMaxChars+1)
	renamed := mapOf(node("api", ir.ZoneRepositories), node(long, ir.ZoneRepositories), node("pg", ir.ZoneStores))
	renamed.Nodes[0].Label, renamed.Nodes[1].Label, renamed.Nodes[2].Label = "die API", "lang", "Datenbank"
	out = LabelRepos(renamed)
	if out.Nodes[0].Label != "api" || out.Nodes[1].Label != "lang" || out.Nodes[2].Label != "Datenbank" || renamed.Nodes[0].Label != "die API" {
		t.Errorf("labelled = %+v, input %+v", out.Nodes, renamed.Nodes)
	}
	if LabelRepos(nil) != nil {
		t.Error("LabelRepos(nil) is a map")
	}
	withEdge := func(d *ir.Diagram, e ir.Edge) *ir.Diagram { d.Edges = append(d.Edges, e); return d }
	cases := map[string]struct {
		d    *ir.Diagram
		want string
	}{
		"missing":          {nil, "no map"},
		"not a flow":       {seqOf(msg("a", "b")), "not a flow"},
		"zone not allowed": {func() *ir.Diagram { d := mapOf(node("api", ir.ZoneRepositories)); d.Zones[2].ID = "cloud"; return d }(), `zone "cloud" is none of`},
		"node in no zone":  {mapOf(node("api", ir.ZoneRepositories), node("pg", "")), "sits in no zone"},
		"invented slug":    {mapOf(node("api", ir.ZoneRepositories), node("billing", ir.ZoneRepositories)), `"billing" is no source repository`},
		"slug elsewhere":   {mapOf(node("api", ir.ZoneRepositories), node("web", ir.ZoneExternal)), `repository "web" sits in zone "external"`},
		"no repository":    {mapOf(node("pg", ir.ZoneStores)), "holds no repository"},
		"dangling edge":    {withEdge(mapOf(node("api", ir.ZoneRepositories)), edge("api", "ghost", "HTTP")), "no declared node"},
		"self edge":        {withEdge(mapOf(node("api", ir.ZoneRepositories)), edge("api", "api", "HTTP")), "points at itself"},
		"over the cap":     {mapOf(node("api", ir.ZoneRepositories), node("a", ir.ZoneExternal), node("b", ir.ZoneExternal), node("c", ir.ZoneExternal), node("d", ir.ZoneExternal)), "5 nodes"},
	}
	for name, c := range cases {
		_, reason := CheckMap(c.d, slugs, caps)
		if !strings.Contains(reason, c.want) {
			t.Errorf("%s: reason = %q, want %q", name, reason, c.want)
		}
	}
}

// A repository node whose slug left the project goes, with its edges; a
// node of another zone named like nothing stays for CheckMap to judge.
func TestDropDeparted(t *testing.T) {
	m := mapOf(node("api", ir.ZoneRepositories), node("old", ir.ZoneRepositories), node("pg", ir.ZoneStores))
	m.Edges = []ir.Edge{edge("api", "pg", "DB"), edge("old", "pg", "DB"), edge("api", "old", "HTTP")}
	out, gone := DropDeparted(m, []string{"api"})
	if len(gone) != 1 || gone[0] != "old" || len(out.Nodes) != 2 || len(out.Edges) != 1 || out.Edges[0].To != "pg" {
		t.Errorf("out = %+v, gone %v", out, gone)
	}
	if len(m.Nodes) != 3 || len(m.Edges) != 3 {
		t.Error("DropDeparted changed the caller's map")
	}
	if same, gone := DropDeparted(m, []string{"api", "old"}); same != m || gone != nil {
		t.Errorf("nothing departed: %+v %v", same, gone)
	}
	if d, gone := DropDeparted(nil, nil); d != nil || gone != nil {
		t.Error("nil map")
	}
}

// A statement the patch does not name keeps its diagram: the patch rebuilds
// it from the page.
func TestApplyPatchKeepsDiagrams(t *testing.T) {
	d := flowOf("a", "b")
	current := []store.Statement{{ID: "s1", Kind: "rule", Name: "R", Text: "T", Diagram: d}}
	applied, reason := ApplyPatch(current, inference.Patch{Statements: []inference.Statement{}}, func(string) bool { return true })
	if reason != "" || applied.Statements[0].Diagram != d {
		t.Errorf("applied = %+v, reason %q", applied, reason)
	}
}
