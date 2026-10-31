package baseline

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode/utf8"

	// ir, not diagram: docs.go's diagram() reads diagram sources.
	ir "github.com/trick77/almanach/internal/diagram"
	"github.com/trick77/almanach/internal/lang"
)

// A diagram is a claim like a statement: the model's structure, held to
// code's rules before anything stores it. Every end of an arrow or a
// message is a declared node, nothing points at itself, nothing is said
// twice, every node has an id, a known shape and a label, labels are Swiss
// and capped, and the size is capped from config. On the system map a
// repository node is a source repository of the project: an unknown slug is
// invented. Anything off rejects the rewrite whole, like an invented
// statement id; the page stays.
//
// The checks are the ones rongo's toIR made of a mermaid parse (ui/src/
// diagramIR.ts): what passes here is what rongo's copied layout was written
// for.

// DiagramCaps are the per-diagram ceilings from config.
type DiagramCaps struct {
	MaxNodes int
	MaxEdges int
}

// DrawCaps is the most rongo's drawing lays out, the bound config's caps may
// not pass: what the system map and an unchanged stored diagram are held
// to. The sequence and entity limits are diagram.DrawMax* too.
var DrawCaps = DiagramCaps{MaxNodes: ir.DrawMaxNodes, MaxEdges: ir.DrawMaxEdges}

// Fixed caps on a diagram's text, like a statement name's.
const (
	labelMaxChars = 80
	idMaxChars    = 64
)

// The system map's zones, the only four it may have.
const (
	ZoneClients      = "clients"
	ZoneRepositories = "repositories"
	ZoneStores       = "stores"
	ZoneExternal     = "external"
)

// MapZones is the four in the order the prompt names them.
var MapZones = []string{ZoneClients, ZoneRepositories, ZoneStores, ZoneExternal}

var (
	directions = []string{"TB", "LR"}
	shapes     = []string{"pill", "step", "decision", "start", "stop"}
	heads      = []string{"end", "both", "none"}
	seqLines   = []string{"solid", "dashed"}
	seqHeads   = []string{"filled", "open", "none", "cross", "both"}
	sides      = []string{"over", "left", "right"}
	frames     = []string{"loop", "alt", "opt", "par", "critical", "break"}
	dividers   = []string{"else", "and", "option"}
	cards      = []string{"one", "zeroOrOne", "oneOrMore", "zeroOrMore"}
	attrKeys   = []string{"PK", "FK", "UK"}
)

// maxFrameDepth is how deep a sequence's frames may sit in one another.
const maxFrameDepth = 3

// CheckDiagram holds a statement's diagram to the rules and returns it as
// stored: labels trimmed and in Swiss spelling. nil is no diagram. The
// reason is "" when it holds.
func CheckDiagram(d *ir.Diagram, caps DiagramCaps) (*ir.Diagram, string) {
	if d == nil {
		return nil, ""
	}
	switch d.Kind {
	case ir.KindFlow:
		return checkFlow(d, caps)
	case ir.KindSequence:
		return checkSequence(d, caps)
	case ir.KindER:
		return checkER(d, caps)
	}
	return nil, fmt.Sprintf("unknown diagram kind %q", d.Kind)
}

// CheckMap holds the project's system map to the rules of a flow and to its
// own: the four zones only, every node in one, a node in the repositories
// zone named by a source repository's slug, and a slug nowhere else. A map
// without a repository is no map of the project.
func CheckMap(d *ir.Diagram, slugs []string, caps DiagramCaps) (*ir.Diagram, string) {
	if d == nil {
		return nil, "no map"
	}
	if d.Kind != ir.KindFlow {
		return nil, fmt.Sprintf("the map is a %q, not a flow", d.Kind)
	}
	for _, z := range d.Zones {
		if !slices.Contains(MapZones, z.ID) {
			return nil, fmt.Sprintf("zone %q is none of %s", z.ID, strings.Join(MapZones, ", "))
		}
	}
	repos := 0
	for _, n := range d.Nodes {
		isSlug := slices.Contains(slugs, n.ID)
		switch {
		case n.Zone == "":
			return nil, fmt.Sprintf("node %q sits in no zone", n.ID)
		case n.Zone == ZoneRepositories && !isSlug:
			return nil, fmt.Sprintf("repository node %q is no source repository of the project", n.ID)
		case n.Zone != ZoneRepositories && isSlug:
			return nil, fmt.Sprintf("repository %q sits in zone %q, not %s", n.ID, n.Zone, ZoneRepositories)
		case n.Zone == ZoneRepositories:
			repos++
		}
	}
	if repos == 0 {
		return nil, "the map holds no repository"
	}
	return checkFlow(d, caps)
}

// DropDeparted takes out of a map the repository nodes whose slug is no
// source repository of the project any more, with their edges, and names
// them: a repository removed since the map was drawn is history, not an
// invention, and must not cost the map. A copy; nil stays nil.
func DropDeparted(d *ir.Diagram, slugs []string) (*ir.Diagram, []string) {
	if d == nil {
		return nil, nil
	}
	gone := map[string]bool{}
	out := *d
	out.Nodes = nil
	for _, n := range d.Nodes {
		if n.Zone == ZoneRepositories && !slices.Contains(slugs, n.ID) {
			gone[n.ID] = true
			continue
		}
		out.Nodes = append(out.Nodes, n)
	}
	if len(gone) == 0 {
		return d, nil
	}
	out.Edges = nil
	for _, e := range d.Edges {
		if !gone[e.From] && !gone[e.To] {
			out.Edges = append(out.Edges, e)
		}
	}
	names := slices.Sorted(maps.Keys(gone))
	return &out, names
}

func checkFlow(d *ir.Diagram, caps DiagramCaps) (*ir.Diagram, string) {
	if !slices.Contains(directions, d.Direction) {
		return nil, fmt.Sprintf("direction %q is neither TB nor LR", d.Direction)
	}
	if len(d.Nodes) == 0 || len(d.Nodes) > caps.MaxNodes {
		return nil, fmt.Sprintf("%d nodes, not 1 to %d", len(d.Nodes), caps.MaxNodes)
	}
	if len(d.Edges) > caps.MaxEdges {
		return nil, fmt.Sprintf("%d edges, over the cap of %d", len(d.Edges), caps.MaxEdges)
	}
	out := &ir.Diagram{Kind: d.Kind, Direction: d.Direction}
	zones := map[string]bool{}
	for _, z := range d.Zones {
		if reason := checkID("zone", z.ID, zones); reason != "" {
			return nil, reason
		}
		title, reason := label("zone "+z.ID, z.Title, true)
		if reason != "" {
			return nil, reason
		}
		out.Zones = append(out.Zones, ir.Zone{ID: z.ID, Title: title})
	}
	nodes := map[string]bool{}
	used := map[string]bool{}
	for _, n := range d.Nodes {
		if reason := checkID("node", n.ID, nodes); reason != "" {
			return nil, reason
		}
		if !slices.Contains(shapes, n.Shape) {
			return nil, fmt.Sprintf("node %q: unknown shape %q", n.ID, n.Shape)
		}
		if n.Zone != "" && !zones[n.Zone] {
			return nil, fmt.Sprintf("node %q: undeclared zone %q", n.ID, n.Zone)
		}
		used[n.Zone] = true
		// A start or a stop is a dot: it carries no text.
		text := ""
		if n.Shape != "start" && n.Shape != "stop" {
			var reason string
			if text, reason = label("node "+n.ID, n.Label, true); reason != "" {
				return nil, reason
			}
		}
		out.Nodes = append(out.Nodes, ir.Node{ID: n.ID, Label: text, Shape: n.Shape, Zone: n.Zone})
	}
	// A zone holding no node is no zone: drawn it is an empty frame, and it
	// would keep the flow from the layouts built for it.
	out.Zones = slices.DeleteFunc(out.Zones, func(z ir.Zone) bool { return !used[z.ID] })
	seen := map[[3]string]bool{}
	for _, e := range d.Edges {
		if !nodes[e.From] || !nodes[e.To] {
			return nil, fmt.Sprintf("edge %s → %s: an end is no declared node", e.From, e.To)
		}
		if e.From == e.To {
			return nil, fmt.Sprintf("edge %s → %s points at itself", e.From, e.To)
		}
		if !slices.Contains(heads, e.Head) {
			return nil, fmt.Sprintf("edge %s → %s: unknown head %q", e.From, e.To, e.Head)
		}
		text, reason := label("edge "+e.From+" → "+e.To, e.Label, false)
		if reason != "" {
			return nil, reason
		}
		key := [3]string{e.From, e.To, text}
		if seen[key] {
			return nil, fmt.Sprintf("edge %s → %s %q twice", e.From, e.To, text)
		}
		seen[key] = true
		out.Edges = append(out.Edges, ir.Edge{From: e.From, To: e.To, Label: text, Dashed: e.Dashed, Head: e.Head})
	}
	return out, ""
}

func checkSequence(d *ir.Diagram, caps DiagramCaps) (*ir.Diagram, string) {
	maxActors := min(caps.MaxNodes, ir.DrawMaxActors)
	if len(d.Actors) == 0 || len(d.Actors) > maxActors {
		return nil, fmt.Sprintf("%d participants, not 1 to %d", len(d.Actors), maxActors)
	}
	out := &ir.Diagram{Kind: d.Kind}
	actors := map[string]bool{}
	for _, a := range d.Actors {
		if reason := checkID("participant", a.ID, actors); reason != "" {
			return nil, reason
		}
		text, reason := label("participant "+a.ID, a.Label, true)
		if reason != "" {
			return nil, reason
		}
		out.Actors = append(out.Actors, ir.Actor{ID: a.ID, Label: text})
	}
	// Every row takes a line of the drawing, a frame's opening, divider and
	// end as much as a message: all of them count against rongo's rows.
	if len(d.Rows) > ir.DrawMaxRows {
		return nil, fmt.Sprintf("%d rows, over the cap of %d", len(d.Rows), ir.DrawMaxRows)
	}
	maxRows := min(caps.MaxEdges, ir.DrawMaxRows)
	depth, drawn, messages := 0, 0, 0
	for i, r := range d.Rows {
		at := fmt.Sprintf("row %d", i+1)
		row := ir.Row{Type: r.Type}
		var reason string
		switch r.Type {
		case ir.RowMessage, ir.RowNote:
			drawn++
			if !actors[r.From] || !actors[r.To] {
				return nil, fmt.Sprintf("%s %s → %s: an end is no declared participant", at, r.From, r.To)
			}
			row.From, row.To = r.From, r.To
			if r.Type == ir.RowMessage {
				messages++
				if !slices.Contains(seqLines, r.Line) || !slices.Contains(seqHeads, r.Head) {
					return nil, fmt.Sprintf("%s: unknown line %q or head %q", at, r.Line, r.Head)
				}
				row.Line, row.Head = r.Line, r.Head
			} else {
				if !slices.Contains(sides, r.Side) {
					return nil, fmt.Sprintf("%s: unknown side %q", at, r.Side)
				}
				row.Side = r.Side
			}
		case ir.RowFrame:
			if depth++; depth > maxFrameDepth {
				return nil, fmt.Sprintf("%s: frames deeper than %d", at, maxFrameDepth)
			}
			if !slices.Contains(frames, r.Keyword) {
				return nil, fmt.Sprintf("%s: unknown frame %q", at, r.Keyword)
			}
			row.Keyword = r.Keyword
		case ir.RowDivider:
			if depth == 0 {
				return nil, at + ": a divider outside any frame"
			}
			if !slices.Contains(dividers, r.Keyword) {
				return nil, fmt.Sprintf("%s: unknown divider %q", at, r.Keyword)
			}
			row.Keyword = r.Keyword
		case ir.RowEnd:
			if depth == 0 {
				return nil, at + ": an end outside any frame"
			}
			depth--
		default:
			return nil, fmt.Sprintf("%s: unknown row type %q", at, r.Type)
		}
		if r.Type != ir.RowEnd {
			if row.Label, reason = label(at, r.Label, false); reason != "" {
				return nil, reason
			}
		}
		out.Rows = append(out.Rows, row)
	}
	if depth != 0 {
		return nil, "a frame left open"
	}
	if messages == 0 {
		return nil, "a sequence without a message"
	}
	if drawn > maxRows {
		return nil, fmt.Sprintf("%d messages and notes, over the cap of %d", drawn, maxRows)
	}
	return out, ""
}

func checkER(d *ir.Diagram, caps DiagramCaps) (*ir.Diagram, string) {
	if !slices.Contains(directions, d.Direction) {
		return nil, fmt.Sprintf("direction %q is neither TB nor LR", d.Direction)
	}
	maxEntities := min(caps.MaxNodes, ir.DrawMaxEntities)
	if len(d.Entities) == 0 || len(d.Entities) > maxEntities {
		return nil, fmt.Sprintf("%d entities, not 1 to %d", len(d.Entities), maxEntities)
	}
	maxRels := min(caps.MaxEdges, ir.DrawMaxRels)
	if len(d.Rels) > maxRels {
		return nil, fmt.Sprintf("%d relationships, over the cap of %d", len(d.Rels), maxRels)
	}
	out := &ir.Diagram{Kind: d.Kind, Direction: d.Direction}
	entities := map[string]bool{}
	for _, e := range d.Entities {
		if reason := checkID("entity", e.ID, entities); reason != "" {
			return nil, reason
		}
		text, reason := label("entity "+e.ID, e.Label, true)
		if reason != "" {
			return nil, reason
		}
		if len(e.Attrs) > ir.DrawMaxAttrs {
			return nil, fmt.Sprintf("entity %q: %d fields, over the cap of %d", e.ID, len(e.Attrs), ir.DrawMaxAttrs)
		}
		entity := ir.Entity{ID: e.ID, Label: text, Attrs: []ir.Attr{}}
		for _, a := range e.Attrs {
			attr, reason := checkAttr(e.ID, a)
			if reason != "" {
				return nil, reason
			}
			entity.Attrs = append(entity.Attrs, attr)
		}
		out.Entities = append(out.Entities, entity)
	}
	seen := map[[3]string]bool{}
	for _, r := range d.Rels {
		if !entities[r.From] || !entities[r.To] {
			return nil, fmt.Sprintf("relationship %s → %s: an end is no declared entity", r.From, r.To)
		}
		if r.From == r.To {
			return nil, fmt.Sprintf("relationship %s → %s points at itself", r.From, r.To)
		}
		if !slices.Contains(cards, r.FromCard) || !slices.Contains(cards, r.ToCard) {
			return nil, fmt.Sprintf("relationship %s → %s: unknown cardinality %q or %q", r.From, r.To, r.FromCard, r.ToCard)
		}
		text, reason := label("relationship "+r.From+" → "+r.To, r.Label, false)
		if reason != "" {
			return nil, reason
		}
		key := [3]string{r.From, r.To, text}
		if seen[key] {
			return nil, fmt.Sprintf("relationship %s → %s %q twice", r.From, r.To, text)
		}
		seen[key] = true
		out.Rels = append(out.Rels, ir.Rel{From: r.From, To: r.To, Label: text, FromCard: r.FromCard, ToCard: r.ToCard, Dashed: r.Dashed})
	}
	return out, ""
}

// checkAttr holds one field: a name, at most the three key marks once each.
// Name and type are code, kept as written; the comment is prose.
func checkAttr(entity string, a ir.Attr) (ir.Attr, string) {
	name, typ := strings.TrimSpace(a.Name), strings.TrimSpace(a.Type)
	at := fmt.Sprintf("entity %q field %q", entity, name)
	if name == "" {
		return ir.Attr{}, fmt.Sprintf("entity %q: a field without a name", entity)
	}
	if utf8.RuneCountInString(name) > labelMaxChars || utf8.RuneCountInString(typ) > labelMaxChars {
		return ir.Attr{}, fmt.Sprintf("%s: name or type over %d characters", at, labelMaxChars)
	}
	keys := []string{}
	for _, k := range a.Keys {
		if !slices.Contains(attrKeys, k) || slices.Contains(keys, k) {
			return ir.Attr{}, fmt.Sprintf("%s: key %q unknown or twice", at, k)
		}
		keys = append(keys, k)
	}
	comment, reason := label(at, a.Comment, false)
	if reason != "" {
		return ir.Attr{}, reason
	}
	return ir.Attr{Type: typ, Name: name, Keys: keys, Comment: comment}, ""
}

// checkID holds an id: present, without surrounding space, short, and
// unique among its kind's.
func checkID(what, id string, seen map[string]bool) string {
	switch {
	case id == "" || strings.TrimSpace(id) != id:
		return fmt.Sprintf("%s id %q empty or padded", what, id)
	case utf8.RuneCountInString(id) > idMaxChars:
		return fmt.Sprintf("%s id %q over %d characters", what, id, idMaxChars)
	case seen[id]:
		return fmt.Sprintf("%s id %q twice", what, id)
	}
	seen[id] = true
	return ""
}

// label is a piece of a diagram's prose as stored: trimmed, Swiss, capped,
// and present when it must be.
func label(at, s string, required bool) (string, string) {
	s = lang.SwissSS(strings.TrimSpace(s))
	if required && s == "" {
		return "", at + ": empty label"
	}
	if utf8.RuneCountInString(s) > labelMaxChars {
		return "", fmt.Sprintf("%s: label over %d characters", at, labelMaxChars)
	}
	return s, ""
}
