package inference

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/trick77/toolkit/llmwire"
	"github.com/trick77/toolkit/llmwire/llmwiretest"
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

// Every call that writes blocks sends their keys kind, text, items, as the
// wire carries them (a re-encode on the way would sort them again), and the
// same bytes call after call (the gateway's prefix cache).
func TestBlockKeysOnTheWire(t *testing.T) {
	fake := llmwiretest.NewServer(t)
	var mu sync.Mutex
	var bodies [][]byte
	tap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, b)
		mu.Unlock()
		r.Body = io.NopCloser(bytes.NewReader(b))
		fake.Config.Handler.ServeHTTP(w, r)
	}))
	t.Cleanup(tap.Close)
	c := testClient(llmwire.New(llmwire.Config{BaseURL: tap.URL, APIKey: "k", Registry: llmwiretest.Registry(),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}))
	ctx := context.Background()
	paragraph := `"properties":{"kind":{"enum":["paragraph","heading"],"type":"string"},"text":{"minLength":1,"type":"string"}},"required":["kind","text"]`
	list := `"properties":{"kind":{"enum":["list","numbered"],"type":"string"},"text":{"type":"string"},"items":{"items":{"minLength":1,"type":"string"},"minItems":1,"type":"array"}},"required":["kind","text","items"]`
	for name, call := range map[string]func(){
		"restyle":  func() { c.Restyle(ctx, "p", 1, true) },
		"patch":    func() { c.Patch(ctx, "p") },
		"describe": func() { c.Describe(ctx, "p") },
		"merge":    func() { c.Merge(ctx, MergeRequest{Prompt: "p"}) },
		"brief":    func() { c.Brief(ctx, "p") },
	} {
		mu.Lock()
		bodies = nil
		mu.Unlock()
		call()
		call()
		mu.Lock()
		got := bodies
		mu.Unlock()
		if len(got) != 2 {
			t.Fatalf("%s: %d requests", name, len(got))
		}
		if !bytes.Equal(got[0], got[1]) {
			t.Errorf("%s: two calls, two schemas", name)
		}
		body := string(got[0])
		if !strings.Contains(body, paragraph) || !strings.Contains(body, list) {
			t.Errorf("%s: blocks not kind, text, items on the wire: %s", name, body)
		}
		if strings.Contains(body, `"properties":{"items"`) {
			t.Errorf("%s: a block opens with items: %s", name, body)
		}
	}
}

// An ordered object names each property once, or it is a programming error.
func TestOrderedObjectNamesEveryProperty(t *testing.T) {
	for name, order := range map[string][]string{"short": {"a"}, "unknown": {"a", "c"}} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: no panic", name)
				}
			}()
			orderedObject(order, map[string]any{"a": str(), "b": str()})
		}()
	}
}
