// Package diagram is the shape of a diagram as the model returns it, the
// store keeps it and the UI draws it: rongo's intermediate form
// (ui/src/diagramIR.ts), a flow, a sequence or an entity model. Data only;
// baseline/diagram.go validates it, rongo's copied code draws it. No
// diagram language anywhere: the JSON is the diagram.
package diagram

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
)

// The kinds. KindNone is no diagram: a statement's way to say its diagram
// is gone, never stored.
const (
	KindFlow     = "flow"
	KindSequence = "sequence"
	KindER       = "er"
	KindNone     = "none"
)

// The most rongo's drawing lays out (ui/src/diagramIR.ts fits, which names
// these): the one place the backend keeps them. A flow, the system map
// included, holds DrawMaxNodes and DrawMaxEdges; a sequence and an entity
// model are wider per item and stop sooner.
const (
	DrawMaxNodes    = 16
	DrawMaxEdges    = 24
	DrawMaxActors   = 8
	DrawMaxRows     = 20
	DrawMaxEntities = 8
	DrawMaxRels     = 12
	DrawMaxAttrs    = 12
)

// The vocabularies, once: the schema the model fills in (inference) and the
// validator (baseline) both read them. openapi.yaml is written by hand.
var (
	Directions = []string{"TB", "LR"}
	Shapes     = []string{"pill", "step", "decision", "start", "stop"}
	Heads      = []string{"end", "both", "none"}
	SeqLines   = []string{"solid", "dashed"}
	SeqHeads   = []string{"filled", "open", "none", "cross", "both"}
	Sides      = []string{"over", "left", "right"}
	Frames     = []string{"loop", "alt", "opt", "par", "critical", "break"}
	Dividers   = []string{"else", "and", "option"}
	Cards      = []string{"one", "zeroOrOne", "oneOrMore", "zeroOrMore"}
	KeyMarks   = []string{"PK", "FK", "UK"}
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

// Sentinel is the map a project revision carries when a map was due and
// none was accepted: asked for, not drawn. The API and the UI read it as
// no map; the baseline does not run a step again to ask for one.
func Sentinel() *Diagram { return &Diagram{Kind: KindNone} }

// IsSentinel reports whether d is the sentinel.
func IsSentinel(d *Diagram) bool { return d != nil && d.Kind == KindNone }

// MapFits reports whether a project of this many source repositories can
// have a system map: every repository a node, and room for at least one
// node beside them. A larger project gets none.
func MapFits(repos int) bool { return repos <= DrawMaxNodes-1 }

// Diagram is one of the three kinds, told apart by Kind; only that kind's
// fields are set, and only they go on the wire.
type Diagram struct {
	Kind string `json:"kind"`
	// Direction: TB or LR, a flow's and an entity model's.
	Direction string `json:"direction,omitempty"`
	// A flow: its nodes, the arrows between them and the zones they sit in.
	Nodes []Node `json:"nodes,omitempty"`
	Edges []Edge `json:"edges,omitempty"`
	Zones []Zone `json:"zones,omitempty"`
	// A sequence: its participants and its rows, top to bottom.
	Actors []Actor `json:"actors,omitempty"`
	Rows   []Row   `json:"rows,omitempty"`
	// An entity model: its tables and their relationships.
	Entities []Entity `json:"entities,omitempty"`
	Rels     []Rel    `json:"rels,omitempty"`
}

// Node is a flow's box. Shape: pill, step, decision, start, stop. Zone ""
// is none.
type Node struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Shape string `json:"shape"`
	Zone  string `json:"zone,omitempty"`
}

// Edge is a flow's arrow. Head: end, both, none.
type Edge struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Label  string `json:"label"`
	Dashed bool   `json:"dashed"`
	Head   string `json:"head"`
}

// Zone is a titled frame around some of a flow's nodes.
type Zone struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// Actor is a sequence's participant.
type Actor struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// Row is one row of a sequence, by Type: a message (From, To, Label, Line,
// Head), a note (From, To, Side, Label), a frame opening or a divider
// (Keyword, Label), or the end of a frame.
type Row struct {
	Type    string `json:"type"`
	From    string `json:"from,omitempty"`
	To      string `json:"to,omitempty"`
	Label   string `json:"label,omitempty"`
	Line    string `json:"line,omitempty"`
	Head    string `json:"head,omitempty"`
	Side    string `json:"side,omitempty"`
	Keyword string `json:"keyword,omitempty"`
}

// The row types.
const (
	RowMessage = "message"
	RowNote    = "note"
	RowFrame   = "frame"
	RowDivider = "divider"
	RowEnd     = "end"
)

// Entity is an entity model's table.
type Entity struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Attrs []Attr `json:"attrs"`
}

// Attr is one field of a table. Keys: PK, FK, UK.
type Attr struct {
	Type    string   `json:"type"`
	Name    string   `json:"name"`
	Keys    []string `json:"keys"`
	Comment string   `json:"comment"`
}

// Rel is a relationship: the cardinality at each end (one, zeroOrOne,
// oneOrMore, zeroOrMore), fromCard at From's box.
type Rel struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Label    string `json:"label"`
	FromCard string `json:"fromCard"`
	ToCard   string `json:"toCard"`
	Dashed   bool   `json:"dashed"`
}

// MarshalJSON writes the kind's fields and no other, its lists never null:
// the UI's types say a flow has zones, an empty list when none.
func (d Diagram) MarshalJSON() ([]byte, error) {
	switch d.Kind {
	case KindFlow:
		return marshalPlain(struct {
			Kind      string `json:"kind"`
			Direction string `json:"direction"`
			Nodes     []Node `json:"nodes"`
			Edges     []Edge `json:"edges"`
			Zones     []Zone `json:"zones"`
		}{d.Kind, d.Direction, orEmpty(d.Nodes), orEmpty(d.Edges), orEmpty(d.Zones)})
	case KindSequence:
		rows := make([]json.RawMessage, 0, len(d.Rows))
		for _, r := range d.Rows {
			b, err := r.marshal()
			if err != nil {
				return nil, err
			}
			rows = append(rows, b)
		}
		return marshalPlain(struct {
			Kind   string            `json:"kind"`
			Actors []Actor           `json:"actors"`
			Rows   []json.RawMessage `json:"rows"`
		}{d.Kind, orEmpty(d.Actors), rows})
	case KindER:
		// A copy: the caller's diagram keeps its nil lists.
		entities := make([]Entity, len(d.Entities))
		for i, e := range d.Entities {
			attrs := make([]Attr, len(e.Attrs))
			for j, a := range e.Attrs {
				a.Keys = orEmpty(a.Keys)
				attrs[j] = a
			}
			e.Attrs = attrs
			entities[i] = e
		}
		return marshalPlain(struct {
			Kind      string   `json:"kind"`
			Direction string   `json:"direction"`
			Entities  []Entity `json:"entities"`
			Rels      []Rel    `json:"rels"`
		}{d.Kind, d.Direction, entities, orEmpty(d.Rels)})
	}
	type plain Diagram
	return marshalPlain(plain(d))
}

// marshal writes a row with its type's fields, the empty label included.
func (r Row) marshal() ([]byte, error) {
	switch r.Type {
	case RowMessage:
		return marshalPlain(struct {
			Type  string `json:"type"`
			From  string `json:"from"`
			To    string `json:"to"`
			Label string `json:"label"`
			Line  string `json:"line"`
			Head  string `json:"head"`
		}{r.Type, r.From, r.To, r.Label, r.Line, r.Head})
	case RowNote:
		return marshalPlain(struct {
			Type  string `json:"type"`
			From  string `json:"from"`
			To    string `json:"to"`
			Side  string `json:"side"`
			Label string `json:"label"`
		}{r.Type, r.From, r.To, r.Side, r.Label})
	case RowFrame, RowDivider:
		return marshalPlain(struct {
			Type    string `json:"type"`
			Keyword string `json:"keyword"`
			Label   string `json:"label"`
		}{r.Type, r.Keyword, r.Label})
	}
	return marshalPlain(struct {
		Type string `json:"type"`
	}{r.Type})
}

// marshalPlain is json.Marshal leaving < > & as written: a label reaches
// the Markdown export as the model wrote it. An encoder that escapes for
// HTML (the API's) still does so over these bytes.
func marshalPlain(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), nil
}

func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// Equal reports whether two diagrams say the same, nil included: a
// rewrite whose only change is its diagram is a change, one that only
// lists the same nodes or arrows in another order is none.
func Equal(a, b *Diagram) bool {
	if a == nil || b == nil {
		return a == b
	}
	ja, errA := json.Marshal(canonical(*a))
	jb, errB := json.Marshal(canonical(*b))
	return errA == nil && errB == nil && bytes.Equal(ja, jb)
}

// canonical is a diagram with every list whose order says nothing sorted:
// a flow's nodes and zones by id, its edges by their ends and label, an
// entity model's tables by id and relationships likewise. A table's
// fields and a sequence's rows keep their order: it is what they say.
func canonical(d Diagram) Diagram {
	d.Nodes = sorted(d.Nodes, func(n Node) string { return n.ID })
	d.Zones = sorted(d.Zones, func(z Zone) string { return z.ID })
	d.Edges = sorted(d.Edges, func(e Edge) string { return e.From + "\x00" + e.To + "\x00" + e.Label })
	d.Entities = sorted(d.Entities, func(e Entity) string { return e.ID })
	d.Rels = sorted(d.Rels, func(r Rel) string { return r.From + "\x00" + r.To + "\x00" + r.Label })
	return d
}

// sorted is a sorted copy, the caller's slice left as it is.
func sorted[T any](s []T, key func(T) string) []T {
	out := slices.Clone(s)
	slices.SortStableFunc(out, func(a, b T) int { return strings.Compare(key(a), key(b)) })
	return out
}
