package inference

import "github.com/trick77/almanach/internal/diagram"

// The diagrams' strict schemas: rongo's intermediate form (a flow, a
// sequence, an entity model) as JSON the model fills in. The schema keeps
// the shape, its vocabularies internal/diagram's; the rules no schema can
// say (ends declared, no self edges, caps, slugs) are baseline/diagram.go's.

// object is a strict object, the one builder of every schema here: every
// property required, nothing else allowed, the required list in one order
// on the wire.
func object(properties map[string]any) map[string]any {
	required := make([]any, 0, len(properties))
	for _, k := range sortedKeys(properties) {
		required = append(required, k)
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             required,
		"properties":           properties,
	}
}

func enum(values ...string) map[string]any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = v
	}
	return map[string]any{"type": "string", "enum": out}
}

func str() map[string]any { return map[string]any{"type": "string"} }

func array(items map[string]any) map[string]any {
	return map[string]any{"type": "array", "items": items}
}

// flowSchema is a flow: a flowchart or a state machine. zone is a node's
// zone id, null for none.
func flowSchema() map[string]any {
	return flow(map[string]any{"type": []any{"string", "null"}}, str())
}

// mapSchema is the project's system map: a flow whose every node sits in
// one of the four zones.
func mapSchema() map[string]any {
	return flow(enum(diagram.MapZones...), enum(diagram.MapZones...))
}

// repoMapSchema is a repository's map: a flow whose every node sits in one
// of its four zones.
func repoMapSchema() map[string]any {
	return flow(enum(diagram.RepoMapZones...), enum(diagram.RepoMapZones...))
}

// flow is a flow's schema. No maxItems: a strict schema takes no array
// bound past minItems 0 or 1 on every provider; the caps are the prompt's
// and the check's, a map over them asked again.
func flow(nodeZone, zoneID map[string]any) map[string]any {
	return object(map[string]any{
		"kind":      enum(diagram.KindFlow),
		"direction": enum(diagram.Directions...),
		"nodes": array(object(map[string]any{
			"id":    str(),
			"label": str(),
			"shape": enum(diagram.Shapes...),
			"zone":  nodeZone,
		})),
		"edges": array(object(map[string]any{
			"from":   str(),
			"to":     str(),
			"label":  str(),
			"dashed": map[string]any{"type": "boolean"},
			"head":   enum(diagram.Heads...),
		})),
		"zones": array(object(map[string]any{"id": zoneID, "title": str()})),
	})
}

// sequenceSchema is an exchange between parts: participants, then rows of
// messages, notes and frames top to bottom.
func sequenceSchema() map[string]any {
	return object(map[string]any{
		"kind":   enum(diagram.KindSequence),
		"actors": array(object(map[string]any{"id": str(), "label": str()})),
		"rows": array(map[string]any{"anyOf": []any{
			object(map[string]any{
				"type": enum(diagram.RowMessage), "from": str(), "to": str(), "label": str(),
				"line": enum(diagram.SeqLines...), "head": enum(diagram.SeqHeads...),
			}),
			object(map[string]any{
				"type": enum(diagram.RowNote), "from": str(), "to": str(), "label": str(),
				"side": enum(diagram.Sides...),
			}),
			object(map[string]any{"type": enum(diagram.RowFrame), "keyword": enum(diagram.Frames...), "label": str()}),
			object(map[string]any{"type": enum(diagram.RowDivider), "keyword": enum(diagram.Dividers...), "label": str()}),
			object(map[string]any{"type": enum(diagram.RowEnd)}),
		}}),
	})
}

// erSchema is entities with their fields and the relationships between
// them.
func erSchema() map[string]any {
	card := enum(diagram.Cards...)
	return object(map[string]any{
		"kind":      enum(diagram.KindER),
		"direction": enum(diagram.Directions...),
		"entities": array(object(map[string]any{
			"id":    str(),
			"label": str(),
			"attrs": array(object(map[string]any{
				"type": str(), "name": str(), "comment": str(),
				"keys": array(enum(diagram.KeyMarks...)),
			})),
		})),
		"rels": array(object(map[string]any{
			"from": str(), "to": str(), "label": str(), "fromCard": card, "toCard": card,
			"dashed": map[string]any{"type": "boolean"},
		})),
	})
}

// statementDiagram is a statement's picture: one of the three kinds to add
// or replace it, {"kind": "none"} to remove it, null to keep what it has.
func statementDiagram() map[string]any {
	return map[string]any{"anyOf": []any{flowSchema(), sequenceSchema(), erSchema(),
		object(map[string]any{"kind": enum(diagram.KindNone)}), map[string]any{"type": "null"}}}
}
