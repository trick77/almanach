package inference

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/trick77/toolkit/llmwire"
)

// para and numbered are blocks as the 2026-11-10 runaways streamed them,
// pretty-printed.
func para(s string) string {
	return "        {\n          \"kind\": \"paragraph\",\n          \"text\": \"" + s + "\"\n        },\n"
}

func numbered(lead string, items ...string) string {
	return `{"kind": "numbered", "text": "` + lead + `", "items": ["` + strings.Join(items, `", "`) + `"]}, `
}

// statementOpen starts a statement and its text array.
func statementOpen(id string) string {
	return `{"id": "` + id + `", "kind": "flow", "name": "Ablauf", "text": [` + "\n"
}

// runaway is a statement's text as the cut answers ended: one-sentence
// paragraphs, a lead-in with a colon every few, never the list it
// announces.
func runaway(n int) string {
	var b strings.Builder
	leads := []string{"Die nummerierte Liste ist:", "Der Ablauf lautet:", "Die vollständige Reihenfolge lautet:"}
	for i := range n {
		b.WriteString(para(leads[i%len(leads)]))
		b.WriteString(para(fmt.Sprintf("Die Verarbeitung erreicht Schritt %d.", i)))
		b.WriteString(para("Die Oberfläche erreicht Danke nach erfolgreicher Abgabe."))
	}
	return b.String()
}

func feed(g *listGuard, s string) bool {
	for len(s) > 0 {
		n := min(97, len(s))
		if g.add(s[:n]) {
			return true
		}
		s = s[n:]
	}
	g.scan()
	return g.cut
}

// The runaway is cut within a few kB; a lead-in followed by its list, or a
// couple without, is not.
func TestListGuard(t *testing.T) {
	var g listGuard
	answer := `{"outline": null, "summary": null, "statements": [` + statementOpen("s1") + runaway(400)
	cutAt := -1
	for i := 0; i < len(answer); i += 97 {
		if g.add(answer[i:min(i+97, len(answer))]) {
			cutAt = i
			break
		}
	}
	if cutAt < 0 || cutAt > 8_000 {
		t.Fatalf("runaway cut at %d, want within 8k chars", cutAt)
	}
	if g.count() < listOrphans {
		t.Errorf("count %d", g.count())
	}

	var honest strings.Builder
	honest.WriteString(`{"statements": [`)
	for i := range 60 {
		honest.WriteString(statementOpen(fmt.Sprintf("s%d", i)))
		honest.WriteString(para("Der Ablauf lautet:"))
		honest.WriteString(numbered("", "Erfassen.", "Prüfen.", "Speichern."))
		honest.WriteString(para("Wichtig ist Folgendes:"))
		honest.WriteString(para("Ein Fall wird nie doppelt gespeichert."))
		honest.WriteString(`]}, `)
	}
	if feed(&listGuard{}, honest.String()) {
		t.Error("an honest answer was cut")
	}
}

// A block's keys in any order: a list whose items come before its text
// still clears the lead-in, a paragraph whose text comes first is still
// one; a guard that is off never cuts.
func TestListGuardKeyOrder(t *testing.T) {
	itemsFirst := `{"kind": "list", "items": ["a", "b"], "text": ""}, `
	textFirst := func(s string) string { return `{"text": "` + s + `", "kind": "paragraph"}, ` }
	var honest, runaway strings.Builder
	honest.WriteString(`{"statements": [`)
	runaway.WriteString(`{"statements": [` + statementOpen("s1"))
	for i := range 40 {
		honest.WriteString(statementOpen(fmt.Sprintf("s%d", i)) + textFirst("Folgende Werte:") + itemsFirst + textFirst("Danach endet es.") + `]}, `)
		runaway.WriteString(textFirst("Die Liste lautet:") + textFirst("Ein Satz."))
	}
	if feed(&listGuard{}, honest.String()) {
		t.Error("lists with their items first were not seen")
	}
	if !feed(&listGuard{}, runaway.String()) {
		t.Error("text-first paragraphs were not seen")
	}
	if feed(&listGuard{off: true}, runaway.String()) {
		t.Error("a guard that is off cut")
	}
}

// A lead-in is seen past escaped newlines and blanks; a stretch without a
// block keeps the buffer bounded.
func TestListGuardEscapesAndBound(t *testing.T) {
	for text, want := range map[string]bool{`Der Ablauf lautet:`: true, `Der Ablauf lautet:\n`: true, `lautet: \n\t `: true,
		`Ein Satz.`: false, `Zeit: 10 Uhr`: false} {
		if got := leadIn(text); got != want {
			t.Errorf("leadIn(%q) = %v", text, got)
		}
	}
	g := listGuard{}
	feed(&g, `{"statements": [{"id": "s1", "diagram": {"x": "`+strings.Repeat("a", 3*listKeep))
	if len(g.buf) > listKeep+listEvery {
		t.Errorf("buffer %d chars, want at most %d", len(g.buf), listKeep+listEvery)
	}
}

// A runaway spread over statements, a few lead-ins to each, is cut by the
// answer's count since its last list.
func TestListGuardAcrossStatements(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"statements": [`)
	for i := range 40 {
		b.WriteString(statementOpen(fmt.Sprintf("s%d", i)))
		b.WriteString(runaway(2))
		b.WriteString(`]}, `)
	}
	g := listGuard{}
	if !feed(&g, b.String()) {
		t.Fatal("not cut")
	}
	if g.run < listRunOrphans {
		t.Errorf("run %d", g.run)
	}
}

// streamedRestyle streams answer in small deltas, then hangs until the
// call is cut, and returns the restyle.
func streamedRestyle(t *testing.T, answer string, logs *strings.Builder) RestyleResult {
	t.Helper()
	var sent atomic.Int64
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for i := 0; i < len(answer) && r.Context().Err() == nil; i += 64 {
			delta, _ := json.Marshal(answer[i:min(i+64, len(answer))])
			sent.Store(int64(i))
			_, _ = io.WriteString(w, "data: "+`{"model": "sol", "choices": [{"index": 0, "delta": {"content": `+string(delta)+`}, "finish_reason": null}]}`+"\n\n")
			w.(http.Flusher).Flush()
		}
		<-r.Context().Done()
	}))
	t.Cleanup(gw.Close)
	c := gatewayClient(t, gw.URL, llmwire.Config{CallTimeout: time.Minute, IdleTimeout: time.Minute}, slog.New(slog.NewTextHandler(logs, nil)))
	res := c.Restyle(context.Background(), "p", 65_000, false)
	if n := sent.Load(); n >= int64(len(answer))-64 {
		t.Errorf("the whole answer was sent (%d chars): want it cut", n)
	}
	return res
}

// A call whose answer runs away so is cut: too long, the guard named.
func TestListGuardCutsTheCall(t *testing.T) {
	answer := `{"outline": null, "summary": null, "statements": [` + statementOpen("s1") + runaway(400)
	var logs strings.Builder
	res := streamedRestyle(t, answer, &logs)
	if res.Outcome != OutcomeTooLong || !strings.Contains(res.Err.Error(), "announced a list") {
		t.Fatalf("Outcome = %s, err %v", res.Outcome, res.Err)
	}
	if got := logs.String(); !strings.Contains(got, "answer cut") || !strings.Contains(got, "answer shape") {
		t.Errorf("log = %q", got)
	}
}
