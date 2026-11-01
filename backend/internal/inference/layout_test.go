package inference

import (
	"encoding/json"
	"testing"
)

// The schema carries the layout: an answer in blocks reads as text with
// its newlines, whatever the model wrote inside one string.
func TestLaidOutAnswers(t *testing.T) {
	var p Patch
	if err := json.Unmarshal([]byte(`{"summary": [
		{"kind": "paragraph", "text": "`+"`loom`"+` ist ein Chat.", "items": []},
		{"kind": "heading", "text": "Woraus es besteht", "items": []},
		{"kind": "list", "text": "", "items": ["`+"`backend/`"+`: die API", "`+"`ui/`"+`: die SPA"]}
	], "statements": [{"id": "s1", "kind": "rule", "name": "Eine Regel", "text": [
		{"kind": "paragraph", "text": "Erst dies.", "items": []},
		{"kind": "numbered", "text": "", "items": ["eins", "zwei"]}
	], "files": []}], "added": [], "drop": [], "confirmed": []}`), &p); err != nil {
		t.Fatal(err)
	}
	if p.Summary == nil || *p.Summary != "`loom` ist ein Chat.\n\n#### Woraus es besteht\n\n- `backend/`: die API\n- `ui/`: die SPA" {
		t.Errorf("summary = %v", p.Summary)
	}
	if len(p.Statements) != 1 || p.Statements[0].Text != "Erst dies.\n\n1. eins\n2. zwei" || *p.Statements[0].ID != "s1" || p.Statements[0].Name != "Eine Regel" {
		t.Errorf("statements = %+v", p.Statements)
	}

	var keep Patch
	if err := json.Unmarshal([]byte(`{"summary": null, "statements": [], "added": [], "drop": [], "confirmed": []}`), &keep); err != nil || keep.Summary != nil {
		t.Errorf("a null summary must keep the stored one: %v, %v", keep.Summary, err)
	}

	var d Description
	if err := json.Unmarshal([]byte(`{"summary": "Als Text.", "statements": [], "added": []}`), &d); err != nil || d.Summary != "Als Text." {
		t.Errorf("a string summary must read as it is: %q, %v", d.Summary, err)
	}
	if err := json.Unmarshal([]byte(`{"summary": 7}`), &d); err == nil {
		t.Error("a summary neither blocks nor text must not parse")
	}
}
