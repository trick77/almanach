package consolidate

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/trick77/almanach/internal/inference"
	"github.com/trick77/almanach/internal/store"
)

// seqMerger answers each call with the next result, the last one again
// past the end, and records the prompts.
type seqMerger struct {
	results []inference.MergeResult
	prompts []string
}

func (m *seqMerger) Merge(_ context.Context, req inference.MergeRequest) inference.MergeResult {
	m.prompts = append(m.prompts, req.Prompt)
	return m.results[min(len(m.prompts), len(m.results))-1]
}

// batchNote is note n of PLAT/files, merged n minutes after mergedAt and
// received with it.
func batchNote(n int, kind string) store.QueuedNote {
	q := queued("PLAT/files#" + string(rune('0'+n)))
	q.PRID, q.ChangeKind = n, kind
	q.MergedAt = mergedAt.Add(time.Duration(n) * time.Minute)
	q.Body = json.RawMessage(`{"note":` + string(rune('0'+n)) + `}`)
	return q
}

// batchWorker is a worker under a day's fold delay at now, its queue the
// notes given.
func batchWorker(now time.Time, m Merger, notes ...store.QueuedNote) (*Worker, *fakeStore) {
	st := newFakeStore()
	st.pages[pageKey(store.ScopeRepository, "PLAT", "files")] = existingRepoPage()
	st.queue = notes
	w := newWorker(st, m).WithFoldDelay(24 * time.Hour)
	w.now = func() time.Time { return now }
	return w, st
}

func from(s inference.Statement, keys ...string) inference.Statement {
	s.FromPRs = keys
	return s
}

// A repository's notes wait the delay from its oldest; then one call folds
// them all, the notes in merge order (whatever order they arrived in),
// each under its PR key.
func TestBatch_WaitsThenOneCall(t *testing.T) {
	m := &fakeMerger{result: okResult(nil, nil)}
	w, st := batchWorker(mergedAt.Add(23*time.Hour), m, batchNote(2, "feature"), batchNote(1, "feature"), batchNote(3, "feature"))
	w.drain(context.Background())
	if len(m.prompts) != 0 || len(st.marked) != 0 {
		t.Fatalf("before the delay: %d calls, marked %v", len(m.prompts), st.marked)
	}
	w.now = func() time.Time { return mergedAt.Add(24 * time.Hour) }
	w.drain(context.Background())
	if len(m.prompts) != 1 {
		t.Fatalf("calls = %d, want one for the repository's three notes", len(m.prompts))
	}
	p := m.prompts[0]
	i1, i2, i3 := strings.Index(p, "### PR PLAT/files#1 ("), strings.Index(p, "### PR PLAT/files#2 ("), strings.Index(p, "### PR PLAT/files#3 (")
	if i1 < 0 || i2 < i1 || i3 < i2 || !strings.Contains(p, "(3 notes)") {
		t.Errorf("notes not in merge order, or missing: %d %d %d", i1, i2, i3)
	}
	if len(st.marked) != 3 {
		t.Errorf("marked = %v, want all three", st.marked)
	}
	if c := st.calls[0]; c.SourcePR != "PLAT/files#1" || !slices.Equal(c.BatchPRKeys, []string{"PLAT/files#1", "PLAT/files#2", "PLAT/files#3"}) {
		t.Errorf("call row = %+v, want the first PR and all three", c)
	}
}

// Each edit and new statement is credited with the pull requests it names,
// of the batch's; one naming none, or one the batch does not hold, is left
// out, the statement it edited kept as stored. The revision keeps every PR.
func TestBatch_CreditsWhatEachItemNames(t *testing.T) {
	edit := from(inference.Statement{ID: str("s1"), Kind: "component", Name: "Uploader", Text: "Nimmt Dateien an, auch grosse.",
		Files: []string{"internal/files/upload.go"}}, "PLAT/files#2")
	fresh := from(inference.Statement{Kind: "rule", Name: "Grenze", Text: "Höchstens 10 MB.", Files: []string{"internal/files/limit.go"}},
		"PLAT/files#1", "PLAT/files#3")
	unnamed := inference.Statement{Kind: "rule", Name: "Ohne", Text: "Nennt keinen PR."}
	foreign := from(inference.Statement{Kind: "rule", Name: "Fremd", Text: "Nennt einen fremden PR."}, "PLAT/files#9")
	res := okResult(nil, nil)
	res.Merge.Repository = &inference.Patch{Statements: []inference.Statement{edit}, Added: []inference.Statement{fresh, unnamed, foreign}}
	w, st := batchWorker(mergedAt.Add(25*time.Hour), &fakeMerger{result: res}, batchNote(1, "feature"), batchNote(2, "feature"), batchNote(3, "feature"))
	w.drain(context.Background())
	if len(st.revisions) != 1 {
		t.Fatalf("revisions = %d, want 1", len(st.revisions))
	}
	rev := st.revisions[0]
	if rev.SourcePR != "PLAT/files#1" || len(rev.BatchPRKeys) != 3 {
		t.Errorf("revision source %q keys %v", rev.SourcePR, rev.BatchPRKeys)
	}
	got := map[string][]string{}
	for _, s := range rev.Statements {
		for _, src := range s.Sources {
			got[s.Name] = append(got[s.Name], src.PRKey)
		}
	}
	if want := []string{"PLAT/files#1", "PLAT/files#2"}; !slices.Equal(got["Uploader"], want) {
		t.Errorf("Uploader sources = %v, want %v: its old PR and the one it names", got["Uploader"], want)
	}
	if want := []string{"PLAT/files#1", "PLAT/files#3"}; !slices.Equal(got["Grenze"], want) {
		t.Errorf("Grenze sources = %v, want %v", got["Grenze"], want)
	}
	if _, ok := got["Ohne"]; ok {
		t.Error("an item naming no PR was kept")
	}
	if _, ok := got["Fremd"]; ok {
		t.Error("an item naming a PR outside the batch was kept")
	}
	for _, s := range rev.Statements {
		if s.Name == "Grenze" && !s.Sources[0].MergedAt.Equal(mergedAt.Add(time.Minute)) {
			t.Errorf("Grenze's first source merged %v, want its own note's time", s.Sources[0].MergedAt)
		}
	}
}

// A new pitfall stands only on a bugfix note it names; the prompt shows the
// bugfix notes by area beside the area's history.
func TestBatch_PitfallsAndProblemAreas(t *testing.T) {
	fix := batchNote(2, "bugfix")
	fix.BugfixArea = "internal/files"
	backed := from(inference.Statement{Kind: "pitfall", Name: "Doppelt", Text: "Zweimal hochgeladen.", Files: []string{"internal/files/upload.go"}},
		"PLAT/files#2")
	bare := from(inference.Statement{Kind: "pitfall", Name: "Geraten", Text: "Ohne Beleg."}, "PLAT/files#1")
	res := okResult(nil, nil)
	res.Merge.Repository = &inference.Patch{Statements: []inference.Statement{}, Added: []inference.Statement{backed, bare}}
	m := &fakeMerger{result: res}
	w, st := batchWorker(mergedAt.Add(25*time.Hour), m, batchNote(1, "feature"), fix)
	st.repoProblems = []store.RepoProblem{{Area: "internal/files", Bugfixes: 3, TopCause: "logic", PRKeys: []string{"PLAT/files#2", "PLAT/files#0"}}}
	w.drain(context.Background())
	if want := "- `internal/files/`: bugfixes here PLAT/files#2; in all 3 bugfixes, cause seen most `logic`, newest fixes PLAT/files#2, PLAT/files#0"; !strings.Contains(m.prompts[0], want) {
		t.Errorf("prompt lacks the problem area:\n%s", want)
	}
	var names []string
	for _, s := range st.revisions[0].Statements {
		names = append(names, s.Name)
	}
	if !slices.Contains(names, "Doppelt") || slices.Contains(names, "Geraten") {
		t.Errorf("statements = %v, want the bugfix-backed pitfall alone", names)
	}
	// A history that cannot be read leaves the notes alone.
	st.repoProblemsErr = errors.New("boom")
	if got := w.batchProblems(context.Background(), []store.QueuedNote{fix}); got != "- `internal/files/`: bugfixes here PLAT/files#2\n" {
		t.Errorf("without the history: %q", got)
	}
	if got := w.batchProblems(context.Background(), []store.QueuedNote{batchNote(1, "feature")}); got != noProblems {
		t.Errorf("no bugfix: %q", got)
	}
}

// An answer that fails a batch folds each half on its own, down to one
// note, which fails as before batching; what passes is marked.
func TestBatch_SplitsOnABadAnswer(t *testing.T) {
	bad := inference.MergeResult{Call: inference.Call{Outcome: inference.OutcomeUnparseable}}
	m := &seqMerger{results: []inference.MergeResult{bad, okResult(nil, nil), bad, bad}}
	w, st := batchWorker(mergedAt.Add(25*time.Hour), m, batchNote(1, "feature"), batchNote(2, "feature"), batchNote(3, "feature"))
	w.drain(context.Background())
	// The three, then #1 alone (ok), then #2 and #3 (bad), then #2, #3.
	if len(m.prompts) != 5 {
		t.Fatalf("calls = %d, want 5", len(m.prompts))
	}
	for i, want := range [][]string{{"#1", "#2", "#3"}, {"#1"}, {"#2", "#3"}, {"#2"}, {"#3"}} {
		var in []string
		for _, n := range []string{"#1", "#2", "#3"} {
			if strings.Contains(m.prompts[i], "### PR PLAT/files"+n+" (") {
				in = append(in, n)
			}
		}
		if !slices.Equal(in, want) {
			t.Errorf("call %d folded %v, want %v", i, in, want)
		}
	}
	if !slices.Equal(st.marked, []string{"PLAT|files|1"}) {
		t.Errorf("marked = %v, want #1 alone", st.marked)
	}
	if len(st.failed) != 2 || st.failed["PLAT|files|2"] == "" || st.failed["PLAT|files|3"] == "" {
		t.Errorf("failed = %v, want #2 and #3", st.failed)
	}
}

// A gateway failure keeps every note of the batch queued, each with its
// attempt counted, and holds the worker.
func TestBatch_GatewayDefersEveryNote(t *testing.T) {
	m := &fakeMerger{result: inference.MergeResult{Call: inference.Call{Outcome: inference.OutcomeTimedOut}}}
	w, st := batchWorker(mergedAt.Add(25*time.Hour), m, batchNote(1, "feature"), batchNote(2, "feature"))
	w.drain(context.Background())
	if len(m.prompts) != 1 || len(st.marked) != 0 || len(st.failed) != 0 || !w.holding() {
		t.Fatalf("calls %d marked %v failed %v holding %v", len(m.prompts), st.marked, st.failed, w.holding())
	}
	if st.attempts["PLAT|files|1"] != 1 || st.attempts["PLAT|files|2"] != 1 {
		t.Errorf("attempts = %v, want one each", st.attempts)
	}
}

// A batch takes a repository's notes in merge order while they fit in
// FoldBatchBytes, at least one; without a delay a note folds alone.
func TestBatchOf_Bytes(t *testing.T) {
	big := func(n int) store.QueuedNote {
		q := batchNote(n, "feature")
		q.Body = json.RawMessage(`"` + strings.Repeat("x", FoldBatchBytes/2) + `"`)
		return q
	}
	w, st := batchWorker(mergedAt, &fakeMerger{}, big(2), big(3), batchNote(4, "feature"))
	st.popped = big(1)
	batch, err := w.batchOf(context.Background(), st.popped)
	if err != nil {
		t.Fatal(err)
	}
	if got := foldKeys(batch); !slices.Equal(got, []string{"PLAT/files#1"}) {
		t.Errorf("batch = %v, want the first note alone: two halves overrun the bound", got)
	}
	w.foldDelay = 0
	if batch, _ := w.batchOf(context.Background(), batchNote(5, "feature")); len(batch) != 1 {
		t.Errorf("no delay: batch of %d", len(batch))
	}
}
