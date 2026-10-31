package inference

// The diagrams' strict schemas: rongo's intermediate form (a flow, a
// sequence, an entity model) as JSON the model fills in. The schema keeps
// the shape; the rules no schema can say (ends declared, no self edges,
// caps, slugs) are baseline/diagram.go's.

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

func enum(values ...any) map[string]any {
	return map[string]any{"type": "string", "enum": values}
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
	zones := []any{"clients", "repositories", "stores", "external"}
	return flow(enum(zones...), enum(zones...))
}

func flow(nodeZone, zoneID map[string]any) map[string]any {
	return object(map[string]any{
		"kind":      enum("flow"),
		"direction": enum("TB", "LR"),
		"nodes": array(object(map[string]any{
			"id":    str(),
			"label": str(),
			"shape": enum("pill", "step", "decision", "start", "stop"),
			"zone":  nodeZone,
		})),
		"edges": array(object(map[string]any{
			"from":   str(),
			"to":     str(),
			"label":  str(),
			"dashed": map[string]any{"type": "boolean"},
			"head":   enum("end", "both", "none"),
		})),
		"zones": array(object(map[string]any{"id": zoneID, "title": str()})),
	})
}

// sequenceSchema is an exchange between parts: participants, then rows of
// messages, notes and frames top to bottom.
func sequenceSchema() map[string]any {
	return object(map[string]any{
		"kind":   enum("sequence"),
		"actors": array(object(map[string]any{"id": str(), "label": str()})),
		"rows": array(map[string]any{"anyOf": []any{
			object(map[string]any{
				"type": enum("message"), "from": str(), "to": str(), "label": str(),
				"line": enum("solid", "dashed"), "head": enum("filled", "open", "none", "cross", "both"),
			}),
			object(map[string]any{
				"type": enum("note"), "from": str(), "to": str(), "label": str(),
				"side": enum("over", "left", "right"),
			}),
			object(map[string]any{
				"type": enum("frame"), "keyword": enum("loop", "alt", "opt", "par", "critical", "break"), "label": str(),
			}),
			object(map[string]any{"type": enum("divider"), "keyword": enum("else", "and", "option"), "label": str()}),
			object(map[string]any{"type": enum("end")}),
		}}),
	})
}

// erSchema is entities with their fields and the relationships between
// them.
func erSchema() map[string]any {
	card := enum("one", "zeroOrOne", "oneOrMore", "zeroOrMore")
	return object(map[string]any{
		"kind":      enum("er"),
		"direction": enum("TB", "LR"),
		"entities": array(object(map[string]any{
			"id":    str(),
			"label": str(),
			"attrs": array(object(map[string]any{
				"type": str(), "name": str(), "comment": str(),
				"keys": array(enum("PK", "FK", "UK")),
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
		object(map[string]any{"kind": enum("none")}), map[string]any{"type": "null"}}}
}
