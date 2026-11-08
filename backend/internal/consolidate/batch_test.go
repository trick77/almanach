package consolidate

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/trick77/almanach/internal/config"
	"github.com/trick77/almanach/internal/inference"
	"github.com/trick77/almanach/internal/store"
)

// Fold runs one note through the merge: a fold of one, as the worker
// makes it without a fold delay.
func (w *Worker) Fold(ctx context.Context, q store.QueuedNote) (next bool) {
	return w.fold(ctx, []store.QueuedNote{q}, 0)
}

// render is the merge prompt of one note without a bugfix.
func render(tmpl string, q store.QueuedNote, repoPage, projectPage store.Page, l config.Limits) string {
	return renderBatch(tmpl, []store.QueuedNote{q}, repoPage, describe(projectPage), noProblems, l)
}

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
	if i1 < 0 || i2 < i1 || i3 < i2 || !strings.Contains(p, "3 notes of repository `files`") {
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
	if want := "- `internal/files/`: bugfixes here PLAT/files#2; before these 3 bugfixes, cause seen most `logic`, newest fixes PLAT/files#2, PLAT/files#0"; !strings.Contains(m.prompts[0], want) {
		t.Errorf("prompt lacks the problem area:\n%s", want)
	}
	// The history ends before the fold's first merge: its fixes are not
	// counted twice.
	if want := mergedAt.Add(time.Minute - time.Nanosecond); !st.problemsUntil.Equal(want) {
		t.Errorf("history read until %v, want %v", st.problemsUntil, want)
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

// folded is which of notes #1..#n a prompt holds.
func folded(prompt string, n int) []string {
	var in []string
	for i := 1; i <= n; i++ {
		k := "#" + string(rune('0'+i))
		if strings.Contains(prompt, "### PR PLAT/files"+k+" (") {
			in = append(in, k)
		}
	}
	return in
}

// An answer that fails a batch folds each half on its own, the bad note
// found in about two calls a halving; what passes is marked. The split
// makes no more calls than the notes would have: the last half, #4, fails
// unfolded once they are spent.
func TestBatch_SplitsOnABadAnswer(t *testing.T) {
	bad := inference.MergeResult{Call: inference.Call{Outcome: inference.OutcomeUnparseable}}
	ok := okResult(nil, nil)
	// The four (bad), #1 #2 (ok), #3 #4 (bad), #3 (ok); #4 past the calls.
	m := &seqMerger{results: []inference.MergeResult{bad, ok, bad, ok}}
	w, st := batchWorker(mergedAt.Add(25*time.Hour), m, batchNote(1, "feature"), batchNote(2, "feature"), batchNote(3, "feature"),
		batchNote(4, "feature"))
	w.drain(context.Background())
	want := [][]string{{"#1", "#2", "#3", "#4"}, {"#1", "#2"}, {"#3", "#4"}, {"#3"}}
	if len(m.prompts) != len(want) {
		t.Fatalf("calls = %d, want %d", len(m.prompts), len(want))
	}
	for i := range want {
		if got := folded(m.prompts[i], 4); !slices.Equal(got, want[i]) {
			t.Errorf("call %d folded %v, want %v", i, got, want[i])
		}
	}
	if !slices.Equal(st.marked, []string{"PLAT|files|1", "PLAT|files|2", "PLAT|files|3"}) {
		t.Errorf("marked = %v, want #1 to #3", st.marked)
	}
	if len(st.failed) != 1 || st.failed["PLAT|files|4"] == "" {
		t.Errorf("failed = %v, want #4 alone", st.failed)
	}
}

// A failure every call meets costs a split no more calls than its notes
// would have one by one: past them a half fails unfolded, never lost.
func TestBatch_SplitBoundedByItsNotes(t *testing.T) {
	bad := inference.MergeResult{Call: inference.Call{Outcome: inference.OutcomeUnparseable}}
	m := &seqMerger{results: []inference.MergeResult{bad}}
	w, st := batchWorker(mergedAt.Add(25*time.Hour), m, batchNote(1, "feature"), batchNote(2, "feature"), batchNote(3, "feature"))
	w.drain(context.Background())
	// The three, then #1 alone, then #2 #3, past which the calls are spent.
	if len(m.prompts) != 3 {
		t.Fatalf("calls = %d, want 3, the notes' own count", len(m.prompts))
	}
	if len(st.failed) != 3 || !strings.Contains(st.failed["PLAT|files|3"], "the split's calls spent") {
		t.Errorf("failed = %v, want all three, the last two unfolded", st.failed)
	}
	if len(st.marked) != 0 {
		t.Errorf("marked = %v", st.marked)
	}
}

// A gateway failure keeps every note of the batch queued, only the
// anchor's attempt counted (the oldest received), as before batching the
// oldest's alone; a timeout halves the repository's next batches, and an
// answer at the half keeps them halved.
func TestBatch_GatewayCountsTheAnchor(t *testing.T) {
	m := &seqMerger{results: []inference.MergeResult{{Call: inference.Call{Outcome: inference.OutcomeTimedOut}}, okResult(nil, nil)}}
	w, st := batchWorker(mergedAt.Add(25*time.Hour), m, batchNote(1, "feature"), batchNote(2, "feature"), batchNote(3, "feature"))
	w.drain(context.Background())
	if len(m.prompts) != 1 || len(st.marked) != 0 || len(st.failed) != 0 || !w.holding() {
		t.Fatalf("calls %d marked %v failed %v holding %v", len(m.prompts), st.marked, st.failed, w.holding())
	}
	if st.attempts["PLAT|files|1"] != 1 || st.attempts["PLAT|files|2"] != 0 || st.attempts["PLAT|files|3"] != 0 {
		t.Errorf("attempts = %v, want the first note's alone", st.attempts)
	}
	if got := w.batchMax["plat/files"]; got != 1 {
		t.Fatalf("batchMax = %v, want half of 3", w.batchMax)
	}
	st.popped = batchNote(1, "feature")
	st.queue = []store.QueuedNote{batchNote(2, "feature"), batchNote(3, "feature")}
	batch, left, err := w.batchOf(context.Background(), st.popped, true)
	if err != nil || len(batch) != 1 || left != 2 {
		t.Fatalf("after the timeout: batch %d, left %d, %v; want one note, two left", len(batch), left, err)
	}
	w.fold(context.Background(), batch, 0)
	if got := w.batchMax["plat/files"]; got != 1 {
		t.Errorf("an answer at the half reset the cap: %v", w.batchMax)
	}

	// A note merged first but received last is not the anchor: the
	// oldest received keeps counting.
	late := batchNote(1, "feature")
	late.ReceivedAt = mergedAt.Add(2 * time.Hour)
	early := batchNote(2, "feature")
	m2 := &fakeMerger{result: inference.MergeResult{Call: inference.Call{Outcome: inference.OutcomeTimedOut}}}
	w2, st2 := batchWorker(mergedAt.Add(25*time.Hour), m2, early, late)
	w2.drain(context.Background())
	if st2.attempts["PLAT|files|2"] != 1 || st2.attempts["PLAT|files|1"] != 0 {
		t.Errorf("attempts = %v, want the oldest received's alone", st2.attempts)
	}
}

// A mark the store refused after the fold was written is retried before
// anything else, never folded again.
func TestBatch_RefusedMarkIsRetriedNotRefolded(t *testing.T) {
	m := &fakeMerger{result: okResult(nil, nil)}
	w, st := batchWorker(mergedAt.Add(25*time.Hour), m, batchNote(1, "feature"), batchNote(2, "feature"))
	st.markErr = errors.New("disk full")
	w.drain(context.Background())
	if len(m.prompts) != 1 || len(w.unmarked) != 2 || !w.holding() {
		t.Fatalf("calls %d unmarked %d holding %v", len(m.prompts), len(w.unmarked), w.holding())
	}
	// Paused meanwhile: the mark is a store write, no call, and is taken.
	st.markErr, w.retryAt = nil, time.Time{}
	st.pause = &store.Pause{At: mergedAt}
	w.drain(context.Background())
	if len(m.prompts) != 1 || len(st.marked) != 2 || len(w.unmarked) != 0 {
		t.Errorf("calls %d marked %v unmarked %d, want both marked without a second call", len(m.prompts), st.marked, len(w.unmarked))
	}
}

// A repository's notes are cut into batches in merge order, each within
// FoldBatchBytes and at least one note, and one drain folds them one after
// another; without a delay a note folds alone.
func TestBatchOf_Bytes(t *testing.T) {
	big := func(n int) store.QueuedNote {
		q := batchNote(n, "feature")
		q.Body = json.RawMessage(`"` + strings.Repeat("x", FoldBatchBytes/2) + `"`)
		return q
	}
	m := &fakeMerger{result: okResult(nil, nil)}
	w, st := batchWorker(mergedAt.Add(25*time.Hour), m, big(1), big(2), big(3), batchNote(4, "feature"))
	w.drain(context.Background())
	want := [][]string{{"#1"}, {"#2"}, {"#3", "#4"}}
	if len(m.prompts) != len(want) {
		t.Fatalf("calls = %d, want %d", len(m.prompts), len(want))
	}
	for i := range want {
		if got := folded(m.prompts[i], 4); !slices.Equal(got, want[i]) {
			t.Errorf("call %d folded %v, want %v", i, got, want[i])
		}
	}
	if len(st.marked) != 4 {
		t.Errorf("marked = %v, want all four in one drain", st.marked)
	}
	w.foldDelay = 0
	if batch, left, _ := w.batchOf(context.Background(), batchNote(5, "feature"), true); len(batch) != 1 || left != 0 {
		t.Errorf("no delay: %v, %d left", batch, left)
	}
}

// A pause during a repository's first batch stops the drain after it: the
// batches past it wait like any other item.
func TestBatchOf_PausedBetweenBatches(t *testing.T) {
	big := func(n int) store.QueuedNote {
		q := batchNote(n, "feature")
		q.Body = json.RawMessage(`"` + strings.Repeat("x", FoldBatchBytes/2) + `"`)
		return q
	}
	m := &fakeMerger{result: okResult(nil, nil)}
	w, st := batchWorker(mergedAt.Add(25*time.Hour), m, big(1), big(2), big(3))
	m.onCall = func() { st.pause = &store.Pause{At: mergedAt} }
	w.drain(context.Background())
	if len(m.prompts) != 1 || len(st.marked) != 1 {
		t.Errorf("calls %d marked %v, want the first batch alone", len(m.prompts), st.marked)
	}
}
