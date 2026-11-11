package inference

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A cut restyle answer: the statement that ran away named first, open, its
// blocks counted; the finished ones closed.
func TestAnswerShapeNamesTheRunaway(t *testing.T) {
	answer := "Hier:\n" + `{"outline": null, "summary": null, "statements": [` +
		`{"id": "s1", "kind": "rule", "name": "Kurz", "text": [{"kind": "paragraph", "text": "Ein Satz."}], "files": [], "area": "A"}, ` +
		statementOpen("s88") + runaway(50)
	sh := answerShape(answer)
	if len(sh.elems) != 2 {
		t.Fatalf("elems = %v", sh.elems)
	}
	run := sh.elems[0]
	if run.id != "s88" || run.kind != "flow" || run.name != "Ablauf" || !run.open || run.path != "statements[1]" {
		t.Errorf("largest = %s", run)
	}
	if run.blocks["paragraph"] != 150 || run.blocks["numbered"] != 0 {
		t.Errorf("blocks = %v", run.blocks)
	}
	if run.end != len(answer) || !strings.HasPrefix(answer[run.start:], `{"id": "s88"`) {
		t.Errorf("span %d..%d of %d: %q", run.start, run.end, len(answer), answer[run.start:min(run.start+20, len(answer))])
	}
	if done := sh.elems[1]; done.id != "s1" || done.open || done.blocks["paragraph"] != 1 {
		t.Errorf("finished = %s", done)
	}
	if got := strings.Join(sh.top, "; "); got != "outline null; summary null; statements 2, open" {
		t.Errorf("top = %q", got)
	}
}

// A merge answer's statements stand a level down; a whole answer is
// closed throughout.
func TestAnswerShapeMerge(t *testing.T) {
	sh := answerShape(`{"repository": {"summary": null, "statements": [{"id": "s4", "kind": "rule", "name": "R", "text": [], "files": []}], "added": []}, "project": null}`)
	if len(sh.elems) != 1 || sh.elems[0].path != "repository.statements[0]" || sh.elems[0].open {
		t.Fatalf("elems = %v", sh.elems)
	}
	if got := strings.Join(sh.top, "; "); got != "repository; project null" {
		t.Errorf("top = %q", got)
	}
	if sh := answerShape("not json"); len(sh.top) != 0 || len(sh.elems) != 0 {
		t.Errorf("no JSON: %+v", sh)
	}
}

// The shape line names the runaway and keeps its head redacted; a
// diagram's nodes are no statements.
func TestLogShape(t *testing.T) {
	var logs strings.Builder
	c := &Client{model: "m", log: slog.New(slog.NewTextHandler(&logs, nil))}
	secret := "Authorization: Bearer abcdefghijKLMNOPQRSTuvwxyz0123456789"
	c.logCut("id", nil, cutBudget{}, answerPart(`{"statements": [{"id": "s1", "kind": "rule", "name": "R", "text": [], "diagram": {"nodes": [{"id": "n1", "label": "A"}]}}, `+
		statementOpen("s9")+para(secret)+runaway(20)))
	got := logs.String()
	if strings.Contains(got, "nodes[0]") || !strings.Contains(got, "statements=2") {
		t.Errorf("diagram nodes counted: %q", got)
	}
	if !strings.Contains(got, "answer shape") || !strings.Contains(got, `largest="statements[1] s9 flow \"Ablauf\"`) || !strings.Contains(got, "open") {
		t.Errorf("log = %q", got)
	}
	if strings.Contains(got, "0123456789") {
		t.Errorf("credential logged: %q", got)
	}
	logs.Reset()
	c.logShape("id", "no json here", "")
	if logs.Len() != 0 {
		t.Errorf("a line for no JSON: %q", logs.String())
	}
}

// An answer that arrived whole logs its blocks, summary's too, the
// statement with the longest stretch of paragraphs without a list (a
// heading does not end it, a list does) and the guard's lead-ins; an answer
// without blocks logs nothing.
func TestLogBlocks(t *testing.T) {
	var logs strings.Builder
	c := &Client{model: "m", log: slog.New(slog.NewTextHandler(&logs, nil))}
	p := func(s string) string { return `{"kind": "paragraph", "text": "` + s + `"}` }
	text := func(bs ...string) string { return `"text": [` + strings.Join(bs, ", ") + `]` }
	answer := `{"summary": [` + p("Was es ist.") + `], "statements": [` +
		`{"id": "s1", "kind": "flow", "name": "Ablauf", ` + text(p("Die Schritte:"), `{"kind": "numbered", "text": "", "items": ["a", "b"]}`, p("Danach.")) + `}, ` +
		`{"id": "s2", "kind": "rule", "name": "Lang", ` + text(p("Eins."), p("Zwei."), `{"kind": "heading", "text": "Teil"}`, p("Drei."),
		`{"kind": "list", "text": "", "items": ["x"]}`, p("Vier.")) + `}]}`
	c.logBlocks("id", answer, 3)
	got := logs.String()
	for _, want := range []string{"answer blocks", `blocks="10 blocks (7 paragraph, 1 heading, 1 list, 1 numbered)"`, "statements=2",
		"lead_ins_without_list=3", `longest_run="statements[1] s2 rule \"Lang\"`, "at most 3 paragraphs without a list"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
	logs.Reset()
	c.logBlocks("id", `{"subjects": [{"name": "a", "paths": ["x"]}]}`, -1)
	if logs.Len() != 0 {
		t.Errorf("a line for an answer without blocks: %q", logs.String())
	}
	if strings.Contains(got, "lead_ins_without_list=-1") {
		t.Errorf("a guard that is off logged: %q", got)
	}
}

// A cut call is kept whole in the folder, redacted, the newest cutKeep of
// its kind; blanks, or no folder, keep none; a folder that cannot be
// written is a warning only.
func TestKeepCut(t *testing.T) {
	dir := t.TempDir()
	var logs strings.Builder
	c := &Client{model: "m", log: slog.New(slog.NewTextHandler(&logs, nil)), cutDir: dir}
	secret := "Authorization: Bearer abcdefghijKLMNOPQRSTuvwxyz0123456789"
	middle := strings.Repeat("x ", 12_288) + secret + strings.Repeat(" y", 12_288)
	c.logCut("call/1", os.ErrDeadlineExceeded, cutBudget{}, answerPart(middle), reasoningPart("thought"))
	path := filepath.Join(dir, "cut-call_1.json")
	if !strings.Contains(logs.String(), "file="+path) {
		t.Fatalf("log = %.500q", logs.String())
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "0123456789") || !strings.Contains(string(b), "thought") || !strings.Contains(string(b), " y y y") {
		t.Errorf("file = %.300q", b)
	}
	// The oldest go past cutKeep.
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	// An unparseable answer has a pool of its own: it never pushes a cut out.
	unparseable := c.keepCut(cutKindUnparseable, "u", nil, cutBudget{}, answerPart("a"))
	if err := os.Chtimes(unparseable, old.Add(-time.Hour), old.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	for i := range cutKeep {
		c.keepCut(cutKindCut, "n"+string(rune('a'+i)), nil, cutBudget{}, answerPart("a"))
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != cutKeep+1 {
		t.Errorf("%d files, want %d cuts and the unparseable one", len(entries), cutKeep)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the oldest was kept: %v", err)
	}
	if _, err := os.Stat(unparseable); err != nil {
		t.Errorf("the unparseable one went with the cuts: %v", err)
	}
	if got := c.keepCut(cutKindCut, "blank", nil, cutBudget{}, answerPart("\n\n"), reasoningPart("")); got != "" {
		t.Errorf("blanks kept: %q", got)
	}
	if got := (&Client{log: c.log}).keepCut(cutKindCut, "id", nil, cutBudget{}, answerPart("a")); got != "" {
		t.Errorf("no folder: %q", got)
	}
	blocked := filepath.Join(dir, "file")
	if err := os.WriteFile(blocked, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	logs.Reset()
	if got := (&Client{log: c.log, cutDir: filepath.Join(blocked, "cuts")}).keepCut(cutKindCut, "id", nil, cutBudget{}, answerPart("a")); got != "" ||
		!strings.Contains(logs.String(), "cut answer not kept") {
		t.Errorf("unwritable: %q, log %q", got, logs.String())
	}
}
