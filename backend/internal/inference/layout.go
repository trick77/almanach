package inference

import (
	"encoding/json"
	"fmt"

	"github.com/trick77/almanach/internal/markup"
)

// blocks is a laid-out text's schema, a summary's or a statement's: its
// paragraphs, sub-headings and lists as items of an array, never newlines
// inside one JSON string. A model writing a whole text on one line (loom's
// summary, 2026-11-01) or breaking it with "<br>" (webfetch's) lost every
// heading and list; here the code writes the newlines (markup.FromBlocks).
func blocks() map[string]any {
	b := array(object(map[string]any{
		"kind":  enum(markup.BlockKinds...),
		"text":  str(),
		"items": array(str()),
	}))
	b["minItems"] = 1
	return b
}

// nullableBlocks is blocks or null: a patch's summary kept as it is.
func nullableBlocks() map[string]any {
	b := blocks()
	b["type"] = []any{"array", "null"}
	return b
}

// laid is a laid-out text as the model returned it, in the text form
// lang.Laid takes: blocks through markup.FromBlocks; a plain string (an
// answer of the schema before blocks, a test's) as it is; null as ok false.
func laid(raw json.RawMessage) (string, bool, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", false, nil
	}
	var s string
	if raw[0] == '"' {
		err := json.Unmarshal(raw, &s)
		return s, err == nil, err
	}
	var bs []markup.Block
	if err := json.Unmarshal(raw, &bs); err != nil {
		return "", false, fmt.Errorf("laid-out text: %w", err)
	}
	return markup.FromBlocks(bs), true, nil
}

// UnmarshalJSON reads a statement whose text comes as blocks.
func (s *Statement) UnmarshalJSON(b []byte) error {
	type plain Statement
	var aux struct {
		*plain
		Text json.RawMessage `json:"text"`
	}
	aux.plain = (*plain)(s)
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	text, _, err := laid(aux.Text)
	s.Text = text
	return err
}

// UnmarshalJSON reads a description whose summary comes as blocks.
func (d *Description) UnmarshalJSON(b []byte) error {
	type plain Description
	var aux struct {
		*plain
		Summary json.RawMessage `json:"summary"`
	}
	aux.plain = (*plain)(d)
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	summary, _, err := laid(aux.Summary)
	d.Summary = summary
	return err
}

// UnmarshalJSON reads a patch whose summary comes as blocks or null.
func (p *Patch) UnmarshalJSON(b []byte) error {
	type plain Patch
	var aux struct {
		*plain
		Summary json.RawMessage `json:"summary"`
	}
	aux.plain = (*plain)(p)
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	summary, ok, err := laid(aux.Summary)
	p.Summary = nil
	if ok {
		p.Summary = &summary
	}
	return err
}
