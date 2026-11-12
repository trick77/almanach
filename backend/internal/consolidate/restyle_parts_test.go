package consolidate

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/trick77/almanach/internal/inference"
	"github.com/trick77/almanach/internal/prompts"
	"github.com/trick77/almanach/internal/store"
)

// words is a text of about n chars, one word again and again: a statement
// as large as a test needs, its word telling which it is.
func words(word string, n int) string {
	return strings.Repeat(word+" ", n/(len(word)+1))
}

// bigWorld is restyleWorld with a page too large for one restyle answer:
// two statements in Kern, one a page from before areas, one in Zugriff,
// each about 25k chars, so two parts of two.
func bigWorld(t *testing.T) world {
	t.Helper()
	x := restyleWorld(t)
	x.st.projectEpoch = prompts.ProjectEpoch
	key := pageKey(store.ScopeRepository, "tools", "svc")
	page := x.st.pages[key]
	page.Summary = "svc rechnet ab."
	page.Statements = []store.Statement{
		{ID: "s1", Kind: "component", Name: "Kern", Text: words("Kernwort", 25_000), Sources: []store.Source{svcRun}, Area: "Kern"},
		{ID: "s2", Kind: "rule", Name: "Regel", Text: words("Regelwort", 25_000), Sources: []store.Source{svcRun}, Area: "Kern"},
		{ID: "s3", Kind: "flow", Name: "Ablauf", Text: words("Ablaufwort", 25_000), Sources: []store.Source{svcRun}},
		{ID: "s4", Kind: "rule", Name: "Zugriff", Text: words("Zugriffwort", 25_000), Sources: []store.Source{svcRun}, Area: "Zugriff"},
	}
	x.st.pages[key] = page
	return x
}

// part1 and part2 answer bigWorld's two parts: the second also edits a
// statement of the first, rewrites the summary and lays out one of the
// first's, all of which are the first part's to say.
var (
	part1 = inference.RestyleResult{Call: called, Patch: inference.Patch{Summary: ref("svc rechnet ab, kurz."),
		Statements: []inference.Statement{{ID: ref("s1"), Kind: "component", Name: "Kern", Text: "Kern neu.", Area: "Kern"}}}}
	part2 = inference.RestyleResult{Call: called, Patch: inference.Patch{Summary: ref("Nicht seine."),
		Statements: []inference.Statement{
			{ID: ref("s4"), Kind: "rule", Name: "Zugriff", Text: "Zugriff neu.", Area: "Zugriff"},
			{ID: ref("s1"), Kind: "component", Name: "Kern", Text: "Fremd.", Area: "Kern"},
			{ID: ref("s3"), Kind: "flow", Name: "Ablauf", Text: "Ablauf gekürzt.", Area: "Ablauf"},
		},
		Added: []inference.Statement{{Kind: "flow", Name: "Doppelt", Text: "Aus s3 und s1.", Area: "Ablauf", From: []string{"s3", "s1"}}}},
		Outline: []inference.OutlineArea{{Area: "Ablauf", IDs: []string{"s3", "s1"}}}}
)

// A repository page too large for one answer is restyled in parts, one
// call a step: each prompt shows its own statements in full and the others
// by name, the summary only in the first. The answers make one revision:
// what a part returns of another's is left out, the outlines laid out
// together, the page at the epoch, the parts gone from the plan.
func TestRestyle_LargePageInParts(t *testing.T) {
	x := bigWorld(t)
	x.models.restyles = []inference.RestyleResult{part1, part2}
	x.w.drain(context.Background())

	if r := x.run(); r.State != store.RunDone {
		t.Fatalf("run = %+v, want done", r)
	}
	if !slices.Equal(x.models.restyledProject, []bool{false, false}) {
		t.Fatalf("restyle calls for the project = %v, want two parts of the repository's", x.models.restyledProject)
	}
	for i, n := range x.models.restyleChars {
		if most := inference.DefaultRestyleLimits.PartChars; n < 45_000 || n > most {
			t.Errorf("part %d sized %d chars, want its own statements alone, within %d", i+1, n, most)
		}
	}
	first, second := x.models.prompts[0], x.models.prompts[1]
	if !strings.Contains(first, "part 1 of 2") || !strings.Contains(first, "Kernwort Kernwort") || !strings.Contains(first, "svc rechnet ab.") ||
		strings.Contains(first, "Ablaufwort Ablaufwort") || !strings.Contains(first, "[s3] flow: Ablauf") {
		t.Errorf("first part's prompt: want s1 and s2 in full, the summary, s3 by name")
	}
	if !strings.Contains(second, "part 2 of 2") || !strings.Contains(second, "Ablaufwort Ablaufwort") || strings.Contains(second, "Kernwort Kernwort") ||
		strings.Contains(second, "svc rechnet ab.") || !strings.Contains(second, "[s1] component: Kern (Kern)") {
		t.Errorf("second part's prompt: want s3 and s4 in full, s1 by name and area, no summary")
	}

	if len(x.st.revisions) != 1 {
		t.Fatalf("revisions = %d, want one for all parts", len(x.st.revisions))
	}
	page := x.repoPage()
	if page.Summary != "svc rechnet ab, kurz." {
		t.Errorf("summary = %q, want the first part's", page.Summary)
	}
	if s := statementNamed(t, page, "Kern"); s.Text != "Kern neu." || s.Area != "Kern" {
		t.Errorf("s1 = %q in %q, want the first part's edit in its area", s.Text, s.Area)
	}
	if s := statementNamed(t, page, "Zugriff"); s.Text != "Zugriff neu." {
		t.Errorf("s4 = %q, want the second part's edit", s.Text)
	}
	if s := statementNamed(t, page, "Ablauf"); s.Area != "Ablauf" || !strings.HasPrefix(s.Text, "Ablaufwort") {
		t.Errorf("s3 = %.20q in %q, want it as stored, laid out by the second part's outline: its split took from another part", s.Text, s.Area)
	}
	// The first part kept its layout: its chapter still opens the page.
	if got := ids(page.Statements); !slices.Equal(got, []string{"s1", "s2", "s3", "s4"}) {
		t.Errorf("order = %v, want the parts' chapters in the page's order", got)
	}
	if x.st.repoEpoch != prompts.RepoEpoch {
		t.Errorf("repository epoch = %d", x.st.repoEpoch)
	}
	if p := x.plan(t); p.Restyle == nil || p.Restyle.Parts != nil {
		t.Errorf("plan = %+v, want the parts gone once written", p.Restyle)
	}
}

// bigCut is bigWorld's parts.
var bigCut = [][]string{{"s1", "s2"}, {"s3", "s4"}}

// partsPlan is a restyle plan with the parts answered so far, cut from rev
// as cut says.
func partsPlan(t *testing.T, rev int64, cut [][]string, answers ...restyleAnswer) []byte {
	t.Helper()
	var r store.BaselineRun
	if err := encodeInto(&r, runPlan{Restyle: &restylePlan{Parts: &restyleParts{Revision: rev, Cut: cut, Answers: answers}}}); err != nil {
		t.Fatal(err)
	}
	return r.Plan
}

// The parts follow the model's limits: one whose output limit leaves less
// beside the reasoning cuts the same page into more, smaller parts.
func TestRestyle_PartsByTheModel(t *testing.T) {
	x := bigWorld(t)
	x.models.restyleLimits = &inference.RestyleLimits{PartChars: 30_000, OneCallMax: 60_000}
	x.models.restyles = []inference.RestyleResult{{Call: called, Patch: inference.Patch{Statements: []inference.Statement{}}}}
	x.w.drain(context.Background())
	if len(x.models.restyleChars) == 0 || !strings.Contains(x.models.prompts[0], "part 1 of 4") {
		t.Fatalf("restyle calls %d: want the page in four parts of one statement", len(x.models.restyleChars))
	}
	for i, n := range x.models.restyleChars {
		if n > 30_000 {
			t.Errorf("part %d sized %d chars, want within the model's 30000", i+1, n)
		}
	}
}

// A part answered is kept: after a restart the run asks the next part,
// and a run after a failed try takes the parts that try paid for, never
// asking the first again. Answers kept for another revision or another
// cut start over.
func TestRestyle_PartsResume(t *testing.T) {
	old := restyleAnswer{Patch: inference.Patch{Statements: []inference.Statement{
		{ID: ref("s1"), Kind: "component", Name: "Kern", Text: "Alt.", Area: "Kern"}}}}
	for name, c := range map[string]struct {
		run, failed []byte
		calls       int
	}{
		"restart":         {run: partsPlan(t, 5, bigCut, restyleAnswer{Patch: part1.Patch}), calls: 1},
		"after a failure": {failed: partsPlan(t, 5, bigCut, restyleAnswer{Patch: part1.Patch}), calls: 1},
		"page moved":      {run: partsPlan(t, 4, bigCut, old), calls: 2},
		"cut moved":       {failed: partsPlan(t, 5, [][]string{{"s1"}, {"s2", "s3", "s4"}}, old), calls: 2},
	} {
		t.Run(name, func(t *testing.T) {
			x := bigWorld(t)
			if c.run != nil {
				x.st.runs[0].Plan = c.run
			}
			x.st.failedPlan = c.failed
			x.models.restyles = []inference.RestyleResult{part1, part2}
			if c.calls == 1 {
				x.models.restyles = x.models.restyles[1:]
			}
			x.w.drain(context.Background())
			if len(x.models.restyleChars) != c.calls || !strings.Contains(x.models.prompts[0], fmt.Sprintf("part %d of 2", 3-c.calls)) {
				t.Fatalf("restyle calls %d, want %d", len(x.models.restyleChars), c.calls)
			}
			if s := statementNamed(t, x.repoPage(), "Kern"); s.Text != "Kern neu." {
				t.Errorf("s1 = %q, want the first part's answer for this page", s.Text)
			}
		})
	}
}

// A part that fails fails the run: nothing written, the page still due.
// Parts that need more calls than the run has left fail it before any.
func TestRestyle_PartsFail(t *testing.T) {
	x := bigWorld(t)
	x.models.restyles = []inference.RestyleResult{part1, {Call: inference.Call{Outcome: inference.OutcomeTooLong}}}
	x.w.drain(context.Background())
	if r := x.run(); r.State != store.RunFailed || !strings.Contains(r.Error, "part 2 of 2") {
		t.Fatalf("run = %+v, want failed on the second part", r)
	}
	if len(x.st.revisions) != 0 || x.st.repoEpoch == prompts.RepoEpoch {
		t.Errorf("revisions %d, epoch %d: want nothing written", len(x.st.revisions), x.st.repoEpoch)
	}

	if p := x.plan(t); p.Restyle.Parts == nil || len(p.Restyle.Parts.Answers) != 1 {
		t.Errorf("failed run's plan = %+v, want the first part's answer kept for the next try", p.Restyle.Parts)
	}

	// Taken from a failed try, the answers are kept with this run at once:
	// its own part failing, the try after it takes them still.
	w := bigWorld(t)
	w.st.failedPlan = partsPlan(t, 5, bigCut, restyleAnswer{Patch: part1.Patch})
	w.models.restyles = []inference.RestyleResult{{Call: inference.Call{Outcome: inference.OutcomeTooLong}}}
	w.w.drain(context.Background())
	if p := w.plan(t); w.run().State != store.RunFailed || p.Restyle.Parts == nil || len(p.Restyle.Parts.Answers) != 1 {
		t.Errorf("run %s, plan %+v: want failed, the carried answer kept", w.run().State, p.Restyle.Parts)
	}

	// The run's calls spent mid-way: failed, the answers so far kept, the
	// next try going on from there.
	y := bigWorld(t)
	y.w.base.Limits.MaxCalls = 1
	y.models.restyles = []inference.RestyleResult{part1}
	y.w.drain(context.Background())
	if r := y.run(); r.State != store.RunFailed || !strings.Contains(r.Error, "spent after 1 of 2 parts") || len(y.models.restyleChars) != 1 {
		t.Fatalf("run = %+v after %d calls, want failed after the first part", r, len(y.models.restyleChars))
	}
	if p := y.plan(t); p.Restyle.Parts == nil || len(p.Restyle.Parts.Answers) != 1 {
		t.Errorf("plan = %+v, want the first part's answer kept", p.Restyle.Parts)
	}
	// Spent by the last part, the merge asks no call and writes.
	u := bigWorld(t)
	u.w.base.Limits.MaxCalls = 2
	u.models.restyles = []inference.RestyleResult{part1, part2}
	u.w.drain(context.Background())
	if u.st.repoEpoch != prompts.RepoEpoch {
		t.Errorf("run = %+v: want the page written with the parts' calls spent", u.run())
	}

	// A part that keeps its layout while it holds a statement from before
	// areas leaves it under none: refused, as one call's answer is, and the
	// answers go with the failure: the next try asks again, never refusing
	// the same answers for free.
	z := bigWorld(t)
	z.models.restyles = []inference.RestyleResult{part1, {Call: called, Patch: inference.Patch{Statements: []inference.Statement{}}}}
	z.w.drain(context.Background())
	if r := z.run(); r.State != store.RunFailed || !strings.Contains(r.Error, "part 2 of 2 gave no outline") {
		t.Fatalf("run = %+v, want refused", r)
	}
	if p := z.plan(t); p.Restyle.Parts != nil {
		t.Errorf("plan = %+v, want the refused answers gone", p.Restyle.Parts)
	}

	// A part no answer can hold is never asked by the poller, nor the
	// parts before it.
	v := bigWorld(t)
	key := pageKey(store.ScopeRepository, "tools", "svc")
	page := v.st.pages[key]
	page.Statements[3].Text = words("Zugriffwort", inference.DefaultRestyleLimits.OneCallMax+1_000)
	v.st.pages[key] = page
	v.w.drain(context.Background())
	if r := v.run(); r.State != store.RunFailed || !strings.Contains(r.Error, "too large") || len(v.models.prompts) != 0 {
		t.Fatalf("run = %+v after %d calls, want failed before any", r, len(v.models.prompts))
	}
}

// The parts: whole areas while they fit, the summary in the first; an area
// too large split by its statements, one statement too large a part of its
// own; a page that fits is no parts.
func TestRestyleCut(t *testing.T) {
	st := func(id, area string, n int) store.Statement {
		return store.Statement{ID: id, Kind: "rule", Name: id, Text: words("w", n), Area: area}
	}
	cut := func(statements ...store.Statement) [][]string {
		parts, _ := restyleCut(store.Page{RevisionID: 1, Summary: "kurz", Statements: statements}, inference.DefaultRestyleLimits.PartChars)
		if len(parts) > 0 && !parts[0].summary {
			t.Errorf("first part without the summary")
		}
		return cutIDs(parts)
	}
	for name, c := range map[string]struct {
		page []store.Statement
		want [][]string
	}{
		"fits":                                {[]store.Statement{st("s1", "A", 20_000), st("s2", "B", 20_000)}, nil},
		"areas whole":                         {[]store.Statement{st("s1", "A", 20_000), st("s2", "A", 20_000), st("s3", "B", 25_000), st("s4", "C", 25_000)}, [][]string{{"s1", "s2"}, {"s3", "s4"}}},
		"area split":                          {[]store.Statement{st("s1", "A", 25_000), st("s2", "A", 25_000), st("s3", "A", 25_000)}, [][]string{{"s1", "s2"}, {"s3"}}},
		"one too long":                        {[]store.Statement{st("s1", "A", 10_000), st("s2", "A", 70_000), st("s3", "B", 10_000)}, [][]string{{"s1"}, {"s2"}, {"s3"}}},
		"first too long, the summary with it": {[]store.Statement{st("s1", "A", 70_000), st("s2", "B", 10_000)}, [][]string{{"s1"}, {"s2"}}},
		"one statement alone":                 {[]store.Statement{st("s1", "A", 70_000)}, nil},
	} {
		t.Run(name, func(t *testing.T) {
			if got := cut(c.page...); !slices.EqualFunc(got, c.want, slices.Equal[[]string]) {
				t.Errorf("parts = %v, want %v", got, c.want)
			}
		})
	}
}

// A project page no one answer can restyle is not asked for by the
// poller: the step fails without a call, saying why. A button's restyle
// asks all the same.
func TestRestyle_ProjectPageTooLarge(t *testing.T) {
	for plan, call := range map[string]bool{`{"restyle":{}}`: false, `{"restyle":{"manual":true}}`: true} {
		x := restyleWorld(t)
		x.st.repoEpoch = prompts.RepoEpoch
		key := pageKey(store.ScopeProject, "tools", "")
		page := x.st.pages[key]
		page.Summary = words("Werkzeug", inference.DefaultRestyleLimits.OneCallMax+1_000)
		x.st.pages[key] = page
		x.st.runs[0].Plan = []byte(plan)
		x.w.drain(context.Background())
		if got := slices.Contains(x.models.restyledProject, true); got != call {
			t.Errorf("%s: project page asked %v, want %v", plan, got, call)
		}
		if r := x.run(); !call && (r.State != store.RunFailed || !strings.Contains(r.Error, "too large")) {
			t.Errorf("%s: run = %+v, want failed, too large", plan, r)
		}
	}
}
