package consolidate

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/trick77/almanach/internal/inference"
	"github.com/trick77/almanach/internal/store"
)

// briefRow is one repository's brief as the fake store holds it.
type briefRow struct {
	repoID       int64
	key, slug    string
	requested    bool
	attempts     int
	failed       string
	markdown     string
	pageRevision int
	pushState    string
	pushErr      string
	pushedCommit string
}

// fakeBriefs is the briefs' store, model and pusher in one.
type fakeBriefs struct {
	rows    []*briefRow
	graph   store.Graph
	answers []inference.BriefResult
	prompts []string
	// pushErr fails every push; unchanged answers nothing to push.
	pushErr   error
	unchanged bool
	pushed    []string
	// writeErr and deferErr fail WriteBrief and DeferBrief.
	writeErr, deferErr error
}

func (f *fakeBriefs) SharedIn(_ context.Context, g store.Graph, key, slug string) ([]store.SharedRepo, error) {
	var out []store.SharedRepo
	for _, e := range g.Edges {
		if e.From == (store.RepoRef{ProjectKey: key, Slug: slug}) {
			out = append(out, store.SharedRepo{RepoRef: e.To, Summary: "Geld in Rappen."})
		}
	}
	return out, nil
}

func (f *fakeBriefs) NextBrief(context.Context) (store.BriefJob, bool, error) {
	for _, r := range f.rows {
		if r.pushState == store.PushDue {
			return store.BriefJob{RepoID: r.repoID, ProjectKey: r.key, Slug: r.slug, Markdown: r.markdown, PushedCommit: r.pushedCommit}, true, nil
		}
	}
	for _, r := range f.rows {
		if r.requested {
			return store.BriefJob{RepoID: r.repoID, ProjectKey: r.key, Slug: r.slug, Write: true}, true, nil
		}
	}
	return store.BriefJob{}, false, nil
}

func (f *fakeBriefs) row(id int64) *briefRow {
	for _, r := range f.rows {
		if r.repoID == id {
			return r
		}
	}
	panic("no row")
}

func (f *fakeBriefs) WriteBrief(_ context.Context, id, _ int64, md string, rev int) error {
	if f.writeErr != nil {
		return f.writeErr
	}
	r := f.row(id)
	r.markdown, r.pageRevision, r.requested, r.attempts, r.failed, r.pushState = md, rev, false, 0, "", store.PushDue
	return nil
}

func (f *fakeBriefs) DeferBrief(_ context.Context, id int64) (int, error) {
	if errors.Is(f.deferErr, store.ErrNoRepo) {
		// The row went with its repository, as the store's cascade does.
		f.rows = slices.DeleteFunc(f.rows, func(r *briefRow) bool { return r.repoID == id })
	}
	if f.deferErr != nil {
		return 0, f.deferErr
	}
	r := f.row(id)
	r.attempts++
	return r.attempts, nil
}

func (f *fakeBriefs) FailBrief(_ context.Context, id, _ int64, reason string) error {
	r := f.row(id)
	r.requested, r.failed = false, reason
	return nil
}

func (f *fakeBriefs) PushedBrief(_ context.Context, id int64, state, commit, pushErr string) error {
	r := f.row(id)
	r.pushState, r.pushedCommit, r.pushErr = state, commit, pushErr
	return nil
}

func (f *fakeBriefs) Dependencies(context.Context, bool) (store.Graph, error) { return f.graph, nil }

func (f *fakeBriefs) Brief(_ context.Context, prompt string) inference.BriefResult {
	f.prompts = append(f.prompts, prompt)
	res := f.answers[0]
	f.answers = f.answers[1:]
	return res
}

func (f *fakeBriefs) PushFile(_ context.Context, key, slug, branch, owned, path string, content []byte, _ string) (string, bool, error) {
	if f.pushErr != nil {
		return "", false, f.pushErr
	}
	if f.unchanged {
		return "", false, nil
	}
	f.pushed = append(f.pushed, key+"/"+slug+"@"+branch+"("+owned+"):"+path+"="+string(content))
	return "c0ffee", true, nil
}

// briefWorld is a worker with one source repository's page and a brief
// requested for it.
func briefWorld(t *testing.T) (world, *fakeBriefs) {
	t.Helper()
	x := newWorld(t, 16)
	x.st.pages[pageKey(store.ScopeRepository, "tools", "svc")] = store.Page{RevisionID: 9, Revision: 3,
		Summary: "Der Dienst rechnet ab.", Statements: []store.Statement{{ID: "s1", Kind: "decision", Name: "Kein ORM",
			Text: "Abgelehnt, weil die Abfragen von Hand schneller sind.", Sources: []store.Source{{Kind: store.FromPR, PRKey: "tools/svc#4"}}}}}
	b := &fakeBriefs{rows: []*briefRow{{repoID: 7, key: "tools", slug: "svc", requested: true}},
		graph: store.Graph{Edges: []store.Edge{
			{From: store.RepoRef{ProjectKey: "tools", Slug: "web"}, To: store.RepoRef{ProjectKey: "tools", Slug: "svc"}},
			{From: store.RepoRef{ProjectKey: "tools", Slug: "svc"}, To: store.RepoRef{ProjectKey: "libs", Slug: "money"}},
		}}}
	x.w.base.Briefs = &Briefs{Store: b, Model: b, Push: b}
	return x, b
}

func briefAnswer(text string) inference.BriefResult {
	return inference.BriefResult{Call: called, Text: text}
}

// A request is one call over the page, the project and the edges; the
// file is stored with its push due and pushed in the same drain, the call
// on the ledger as a brief's.
func TestBrief_WriteThenPush(t *testing.T) {
	x, b := briefWorld(t)
	b.answers = []inference.BriefResult{briefAnswer("Der Dienst rechnet ab, die Webseite ruft ihn.\n\n#### Was gelten muss\n\n- Beträge heißen Rappen.")}
	x.w.drain(context.Background())
	if len(b.prompts) != 1 {
		t.Fatalf("calls = %d, want 1", len(b.prompts))
	}
	p := b.prompts[0]
	for _, want := range []string{"Kein ORM", "Learned from pull requests.", "Used by: `tools/web`", "Uses `libs/money`:\n\nGeld in Rappen.",
		"Werkzeuge.", "repository `svc` of project `tools`"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
	// Read-only: no diagram, no word asking for a write.
	for _, not := range []string{"Diagram:", noneYet, noStatementsYet} {
		if strings.Contains(p, not) {
			t.Errorf("prompt carries %q", not)
		}
	}
	r := b.rows[0]
	want := "# tools/svc\n\nDer Dienst rechnet ab, die Webseite ruft ihn.\n\n## Was gelten muss\n\n- Beträge heissen Rappen.\n"
	if r.markdown != want || r.pageRevision != 3 {
		t.Errorf("stored = %q (revision %d), want %q", r.markdown, r.pageRevision, want)
	}
	if r.pushState != store.PushPushed || r.pushedCommit != "c0ffee" || len(b.pushed) != 1 ||
		b.pushed[0] != "tools/svc@"+BriefBranch+"():"+BriefPath+"="+want {
		t.Errorf("push = %+v %v", r, b.pushed)
	}
	if n := len(x.st.calls); n != 1 || x.st.calls[0].Purpose != store.PurposeBrief || x.st.calls[0].SourceKind != store.FromPage {
		t.Errorf("ledger = %+v", x.st.calls)
	}
}

// A push the forge refuses is the push's end, its reason kept for the
// reader, who copies the text; the drain goes on. One with nothing to
// change says so.
func TestBrief_PushRefusedOrUnchanged(t *testing.T) {
	x, b := briefWorld(t)
	b.answers = []inference.BriefResult{briefAnswer("Text.")}
	b.pushErr = errors.New("git push: exit status 128: remote: You are not allowed to push")
	x.w.drain(context.Background())
	if r := b.rows[0]; r.pushState != store.PushFailed || !strings.Contains(r.pushErr, "not allowed") || r.markdown == "" {
		t.Errorf("refused push = %+v", r)
	}
	b.rows[0].pushState, b.pushErr, b.unchanged = store.PushDue, nil, true
	x.w.drain(context.Background())
	if r := b.rows[0]; r.pushState != store.PushUnchanged || r.pushedCommit != "" {
		t.Errorf("unchanged push = %+v", r)
	}
}

// A push error is redacted before the cut: a token the cut would leave too
// short for its shape is still taken out.
func TestBrief_PushErrorRedactedBeforeTheCut(t *testing.T) {
	x, b := briefWorld(t)
	b.rows[0].requested, b.rows[0].pushState, b.rows[0].markdown = false, store.PushDue, "## Projekt\n\nx\n"
	token := "abcdef0123456789abcdef0123456789abcdef01"
	pad := strings.Repeat("x", reasonMaxRunes-len("git push: Authorization: Bearer ")-10)
	b.pushErr = fmt.Errorf("git push: %sAuthorization: Bearer %s", pad, token)
	x.w.drain(context.Background())
	if r := b.rows[0]; r.pushState != store.PushFailed || strings.Contains(r.pushErr, token[:8]) {
		t.Errorf("push error with a token cut short stored as %q", r.pushErr)
	}
}

// A clone the poller holds leaves the push due for the next drain, leased
// to the commit pushed last; the runs go on meanwhile.
func TestBrief_CloneBusyWaits(t *testing.T) {
	x, b := briefWorld(t)
	b.rows[0].requested, b.rows[0].pushState, b.rows[0].markdown, b.rows[0].pushedCommit = false, store.PushDue, "# x\n", "c1"
	b.pushErr = fmt.Errorf("lock: %w", ErrCloneBusy)
	x.queue(store.RunFull, "")
	x.w.drain(context.Background())
	if r := b.rows[0]; r.pushState != store.PushDue {
		t.Errorf("busy clone: push state %q, want still due", r.pushState)
	}
	if r := x.run(); r.State == store.RunQueued {
		t.Errorf("a brief's busy clone held the run: %+v", r)
	}
	b.pushErr = nil
	x.w.drain(context.Background())
	if len(b.pushed) != 1 || !strings.Contains(b.pushed[0], "("+"c1"+")") {
		t.Errorf("pushed %v, want leased to the commit pushed last", b.pushed)
	}
}

// A request whose repository went between NextBrief and its count is
// dropped without a hold or a call.
func TestBrief_RepositoryGone(t *testing.T) {
	x, b := briefWorld(t)
	b.deferErr = store.ErrNoRepo
	x.w.drain(context.Background())
	if len(b.prompts) != 0 || x.w.holding() {
		t.Errorf("gone repository: calls %d holding %v", len(b.prompts), x.w.holding())
	}
}

// A count or a write the store refuses holds the worker: the request
// stays, and no lap pays for it again before the hold ends.
func TestBrief_RefusedWriteHolds(t *testing.T) {
	x, b := briefWorld(t)
	b.deferErr = store.ErrFull
	x.w.drain(context.Background())
	if len(b.prompts) != 0 || !x.w.holding() || !b.rows[0].requested {
		t.Fatalf("refused count: calls %d holding %v row %+v", len(b.prompts), x.w.holding(), b.rows[0])
	}
	x.w.clearHold()
	x.w.retryAt = x.w.now()
	b.deferErr, b.writeErr = nil, store.ErrFull
	b.answers = []inference.BriefResult{briefAnswer("Text.")}
	x.w.drain(context.Background())
	x.w.drain(context.Background())
	if len(b.prompts) != 1 || !x.w.holding() || !b.rows[0].requested || b.rows[0].attempts != 1 {
		t.Errorf("refused write: calls %d holding %v row %+v, want one call, held, the attempt counted", len(b.prompts), x.w.holding(), b.rows[0])
	}
}

// An answer off the schema fails the request; a page not written yet fails
// it without a call.
func TestBrief_RequestFails(t *testing.T) {
	x, b := briefWorld(t)
	b.answers = []inference.BriefResult{{Call: inference.Call{Outcome: inference.OutcomeUnparseable}}}
	x.w.drain(context.Background())
	if r := b.rows[0]; r.requested || r.failed != "unparseable" || r.pushState != "" {
		t.Errorf("unparseable answer: %+v", r)
	}

	x, b = briefWorld(t)
	delete(x.st.pages, pageKey(store.ScopeRepository, "tools", "svc"))
	x.w.drain(context.Background())
	if r := b.rows[0]; r.requested || !strings.Contains(r.failed, "no description") || len(b.prompts) != 0 {
		t.Errorf("no page: %+v, calls %d", r, len(b.prompts))
	}
}

// A gateway-class failure holds the worker with the request kept and its
// attempt counted; at maxFoldAttempts the request is given up.
func TestBrief_GatewayHolds(t *testing.T) {
	x, b := briefWorld(t)
	down := inference.BriefResult{Call: inference.Call{Outcome: inference.OutcomeError, Err: errors.New("503")}}
	b.answers = []inference.BriefResult{down}
	x.w.drain(context.Background())
	if r := b.rows[0]; !r.requested || r.attempts != 1 || !x.w.holding() {
		t.Fatalf("after a gateway failure: %+v holding %v", r, x.w.holding())
	}
	x.w.clearHold()
	b.rows[0].attempts = maxFoldAttempts - 1
	b.answers = []inference.BriefResult{down}
	x.w.drain(context.Background())
	if r := b.rows[0]; r.requested || !strings.Contains(r.failed, "given up") {
		t.Errorf("at the bound: %+v", r)
	}
}

// The day's budget spent, a request waits for the next day; a due push,
// which costs no call, goes on.
func TestBrief_BudgetHoldsTheRequest(t *testing.T) {
	x, b := briefWorld(t)
	x.w.base.Limits.DailyBudgetNanoUSD = 1
	x.st.callAt = mergedAt
	x.st.calls = []store.LLMCall{{CostNanoUSD: i64(5)}}
	x.w.drain(context.Background())
	if len(b.prompts) != 0 || !b.rows[0].requested {
		t.Fatalf("over budget: calls %d, row %+v", len(b.prompts), b.rows[0])
	}
	b.rows = append(b.rows, &briefRow{repoID: 8, key: "tools", slug: "web", pushState: store.PushDue, markdown: "## Projekt\n\nx\n"})
	x.w.drain(context.Background())
	if r := b.rows[1]; r.pushState != store.PushPushed {
		t.Errorf("over budget, a due push: %+v", r)
	}
}
