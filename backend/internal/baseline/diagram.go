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
// invented. Anything off is left out, never stored: a statement keeps its
// text, and the diagram it had while its name and text stay (consolidate's
// statementDiagram); a map that fails is not drawn, the stored one
// staying. One bad drawing never costs the text around it.
//
// The checks are the ones rongo's toIR made of a mermaid parse (ui/src/
// diagramIR.ts): what passes here is what rongo's copied layout was written
// for.

// DiagramCaps are the per-diagram ceilings from config.
type DiagramCaps struct {
	MaxNodes int
	MaxEdges int
}

// MapCaps is what both maps, the system map and a repository's, are held
// to: diagram.MapMax*, not the statement diagrams' caps from config.
var MapCaps = DiagramCaps{MaxNodes: ir.MapMaxNodes, MaxEdges: ir.MapMaxEdges}

// Fixed caps on a diagram's text, like a statement name's.
const (
	labelMaxChars = 80
	idMaxChars    = 64
)

// The vocabularies are internal/diagram's, shared with the schema.
var (
	directions = ir.Directions
	shapes     = ir.Shapes
	heads      = ir.Heads
	layers     = ir.Layers
	stages     = ir.Stages
	seqLines   = ir.SeqLines
	seqHeads   = ir.SeqHeads
	sides      = ir.Sides
	frames     = ir.Frames
	dividers   = ir.Dividers
	cards      = ir.Cards
	attrKeys   = ir.KeyMarks
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
		return checkFlow(d, caps, false)
	case ir.KindSequence:
		return checkSequence(d, caps)
	case ir.KindER:
		return checkER(d, caps)
	}
	return nil, fmt.Sprintf("unknown diagram kind %q", d.Kind)
}

// CheckMap holds the project's system map to the rules of a flow and to its
// own: the five zones only, every node in one, a node in the repositories
// zone named by a source repository's slug, and a slug nowhere else. A map
// without a repository is no map of the project.
func CheckMap(d *ir.Diagram, slugs []string, caps DiagramCaps) (*ir.Diagram, string) {
	if why := checkZoned(d, ir.MapZones); why != "" {
		return nil, why
	}
	repos := 0
	for _, n := range d.Nodes {
		isSlug := slices.Contains(slugs, n.ID)
		switch {
		case n.Zone == ir.ZoneRepositories && !isSlug:
			return nil, fmt.Sprintf("repository node %q is no source repository of the project", n.ID)
		case n.Zone != ir.ZoneRepositories && isSlug:
			return nil, fmt.Sprintf("repository %q sits in zone %q, not %s", n.ID, n.Zone, ir.ZoneRepositories)
		case n.Zone == ir.ZoneRepositories:
			repos++
		}
	}
	if repos == 0 {
		return nil, "the map holds no repository"
	}
	return checkFlow(d, caps, true)
}

// CheckNewMap is CheckMap on a newly drawn system map, every tag required
// (CheckTagged); CheckMap alone is for a stored one.
func CheckNewMap(d *ir.Diagram, slugs []string, caps DiagramCaps) (*ir.Diagram, string) {
	if why := CheckTagged(d); why != "" {
		return nil, why
	}
	return CheckMap(d, slugs, caps)
}

// CheckRepoMap holds a repository's map to the rules of a flow and to its
// own: the four zones only, every node in one. No slug rule: its nodes are
// the repository's parts, not repositories. Its tags carried, as the
// system map's: RepoRuntimeOnly filters it. Only ever a new map: every tag
// is required (CheckTagged).
func CheckRepoMap(d *ir.Diagram, caps DiagramCaps) (*ir.Diagram, string) {
	if why := checkZoned(d, ir.RepoMapZones); why != "" {
		return nil, why
	}
	if why := CheckTagged(d); why != "" {
		return nil, why
	}
	return checkFlow(d, caps, true)
}

// CheckTagged refuses a newly drawn map with a node of no stage or an edge
// of no layer: the strict schema requires both, but a provider that does
// not hold to it would store a map read as one from before the tags
// (diagram.Untagged), never standing, redrawn on every retry. A stored map
// is not held to it: one from before the tags carries none.
func CheckTagged(d *ir.Diagram) string {
	if d == nil {
		return ""
	}
	for _, n := range d.Nodes {
		if n.Stage == "" {
			return fmt.Sprintf("node %q has no stage", n.ID)
		}
	}
	for _, e := range d.Edges {
		if e.Layer == "" {
			return fmt.Sprintf("edge %s → %s has no layer", e.From, e.To)
		}
	}
	return ""
}

// checkZoned is the rules both maps share before their own: a flow, its
// zones only the ones given, every node in one. checkFlow does the rest.
func checkZoned(d *ir.Diagram, zones []string) string {
	if d == nil {
		return "no map"
	}
	if d.Kind != ir.KindFlow {
		return fmt.Sprintf("the map is a %q, not a flow", d.Kind)
	}
	for _, z := range d.Zones {
		if !slices.Contains(zones, z.ID) {
			return fmt.Sprintf("zone %q is none of %s", z.ID, strings.Join(zones, ", "))
		}
	}
	for _, n := range d.Nodes {
		if n.Zone == "" {
			return fmt.Sprintf("node %q sits in no zone", n.ID)
		}
	}
	return ""
}

// RepairEdges takes out of a proposed flow, the map or a statement's, the
// edges that say nothing: one pointing at its own node, and one repeating
// an edge before it (same ends, same label). One such edge refused a whole
// map once ("almanach → almanach"); every other rule still refuses it in
// CheckMap and CheckDiagram. A copy, with the edges taken out named; nil
// stays nil.
func RepairEdges(d *ir.Diagram) (*ir.Diagram, []string) {
	if d == nil {
		return nil, nil
	}
	out := *d
	out.Edges = nil
	var dropped []string
	seen := map[[4]string]bool{}
	for _, e := range d.Edges {
		// The label as checkFlow compares it: trimmed and Swiss. And whether
		// the map keeps it: a build arrow repeating a runtime one must
		// not take its place, RuntimeOnly takes it out after.
		kept := ""
		if e.Layer == ir.LayerBuild || e.Layer == ir.LayerDeploy || e.Layer == ir.LayerMonitoring {
			kept = e.Layer
		}
		key := [4]string{e.From, e.To, lang.Prose(e.Label), kept}
		if e.From == e.To || seen[key] {
			dropped = append(dropped, e.From+" → "+e.To)
			continue
		}
		seen[key] = true
		out.Edges = append(out.Edges, e)
	}
	return &out, dropped
}

// LabelRepos labels each repository node of a proposed map by its id, the
// slug, whatever the model wrote ("rongos Zwischenform" once): the reader
// finds the repository by it. A slug longer than a label may be keeps the
// model's label. A copy; nil stays nil. Only a new map: a stored one is
// carried as drawn, so no revision is written for a label alone.
func LabelRepos(d *ir.Diagram) *ir.Diagram {
	if d == nil {
		return nil
	}
	out := *d
	out.Nodes = slices.Clone(d.Nodes)
	for i, n := range out.Nodes {
		if n.Zone == ir.ZoneRepositories && utf8.RuneCountInString(n.ID) <= labelMaxChars {
			out.Nodes[i].Label = n.ID
		}
	}
	return &out
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
	for _, n := range d.Nodes {
		if n.Zone == ir.ZoneRepositories && !slices.Contains(slugs, n.ID) {
			gone[n.ID] = true
		}
	}
	if len(gone) == 0 {
		return d, nil
	}
	out := *d
	out.Nodes, out.Edges = nil, nil
	for _, n := range d.Nodes {
		if !gone[n.ID] {
			out.Nodes = append(out.Nodes, n)
		}
	}
	for _, e := range d.Edges {
		if !gone[e.From] && !gone[e.To] {
			out.Edges = append(out.Edges, e)
		}
	}
	return &out, slices.Sorted(maps.Keys(gone))
}

// RuntimeOnly takes out of a proposed system map what does not run in
// production, by the model's tags, never by a name: a box outside the
// repositories tagged with another stage, with its arrows (the model's word
// that the system's production box is on the map: code cannot tell two
// boxes are one system), an arrow of the build, the deployment or the
// monitoring, and then every box outside the repositories left without an
// arrow: the map shows only what the system calls at runtime, so a box with
// no arrow says nothing (a build server drawn without its edge). A
// repository stays, arrows or not; an untagged arrow stays; an unknown tag
// is checkFlow's to refuse. Run after the departed repositories are out,
// so a box they left alone goes too. What went is named, "label (why)". A
// copy; d itself when nothing goes; nil stays nil.
func RuntimeOnly(d *ir.Diagram) (*ir.Diagram, []string) { return runtimeOnly(d, false) }

// RepoRuntimeOnly is RuntimeOnly on a repository map (user, 2026-11-02:
// build, CI and CD are no architecture overview), but it judges a box by
// the arrows it had: one whose every arrow went goes with them (a CI
// pipeline drawn as a part, its job, its workspace), one drawn with no
// arrow stays (a library's parts and entry point may link to nothing on
// the map). So a map it empties had arrows, all of them the tags' to take.
func RepoRuntimeOnly(d *ir.Diagram) (*ir.Diagram, []string) { return runtimeOnly(d, true) }

func runtimeOnly(d *ir.Diagram, repoMap bool) (*ir.Diagram, []string) {
	if d == nil {
		return nil, nil
	}
	// Only a box in a zone other than the repositories may go: one in no
	// zone, or an arrow to an undeclared box, is CheckMap's to refuse, and
	// the refusal goes back to the model.
	label := map[string]string{}
	other := map[string]bool{}
	for _, n := range d.Nodes {
		label[n.ID] = n.Label
		other[n.ID] = droppable(n) && n.Stage == ir.StageOther
	}
	var dropped []string
	var edges []ir.Edge
	linked, drawn := map[string]bool{}, map[string]bool{}
	for _, e := range d.Edges {
		_, from := label[e.From]
		_, to := label[e.To]
		drawn[e.From], drawn[e.To] = true, true
		why := ""
		switch {
		case !from || !to:
		case other[e.From] || other[e.To]:
			why = "to another stage"
		case e.Layer == ir.LayerBuild || e.Layer == ir.LayerDeploy || e.Layer == ir.LayerMonitoring:
			why = e.Layer
		}
		if why != "" {
			dropped = append(dropped, label[e.From]+" → "+label[e.To]+" ("+why+")")
			continue
		}
		edges = append(edges, e)
		linked[e.From], linked[e.To] = true, true
	}
	var nodes []ir.Node
	for _, n := range d.Nodes {
		switch {
		case other[n.ID]:
			dropped = append(dropped, n.Label+" ("+ir.StageOther+" stage)")
		case repoMap && !drawn[n.ID]:
			nodes = append(nodes, n)
		case droppable(n) && !linked[n.ID]:
			dropped = append(dropped, n.Label+" (no runtime arrow)")
		default:
			nodes = append(nodes, n)
		}
	}
	if len(dropped) == 0 {
		return d, nil
	}
	out := *d
	out.Nodes, out.Edges = nodes, edges
	return &out, dropped
}

// droppable is a box RuntimeOnly may take out: one in a zone, not a
// repository.
func droppable(n ir.Node) bool { return n.Zone != "" && n.Zone != ir.ZoneRepositories }

// checkFlow holds a flow to the rules. tagged: a map, the system's or a
// repository's, whose stage and layer are carried; every other flow drops
// them, so no tag reaches a diagram RuntimeOnly never filters.
func checkFlow(d *ir.Diagram, caps DiagramCaps, tagged bool) (*ir.Diagram, string) {
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
		// A title may be empty: the UI titles zones from strings.ts by id.
		title, reason := label("zone "+z.ID, z.Title, false)
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
		if n.Stage != "" && !slices.Contains(stages, n.Stage) {
			return nil, fmt.Sprintf("node %q: unknown stage %q", n.ID, n.Stage)
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
		stage := ""
		if tagged {
			stage = n.Stage
		}
		out.Nodes = append(out.Nodes, ir.Node{ID: n.ID, Label: text, Shape: n.Shape, Zone: n.Zone, Stage: stage})
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
		if e.Layer != "" && !slices.Contains(layers, e.Layer) {
			return nil, fmt.Sprintf("edge %s → %s: unknown layer %q", e.From, e.To, e.Layer)
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
		layer := ""
		if tagged {
			layer = e.Layer
		}
		out.Edges = append(out.Edges, ir.Edge{From: e.From, To: e.To, Label: text, Dashed: e.Dashed, Head: e.Head, Layer: layer})
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
	s = lang.Prose(s)
	if required && s == "" {
		return "", at + ": empty label"
	}
	if utf8.RuneCountInString(s) > labelMaxChars {
		return "", fmt.Sprintf("%s: label over %d characters", at, labelMaxChars)
	}
	return s, ""
}
