package store

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Projects and Repos count what was ingested and carry the last merge and
// the current revision.
func TestProjectsAndRepos(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	ingest(t, s, noteFor("PRJ", "svc", 1))
	ingest(t, s, noteFor("PRJ", "svc", 2))
	ingest(t, s, noteFor("PRJ", "lib", 3))
	ingest(t, s, noteFor("ZED", "z", 4))
	if _, err := s.InsertRevision(ctx, NewRevision{Scope: ScopeProject, ProjectKey: "PRJ", Summary: "project", SourcePR: "PRJ/svc#2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertRevision(ctx, NewRevision{Scope: ScopeRepository, ProjectKey: "PRJ", RepoSlug: "svc", Summary: "svc summary", SourcePR: "PRJ/svc#2"}); err != nil {
		t.Fatal(err)
	}

	ps, err := s.Projects(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 || ps[0].Key != "PRJ" || ps[1].Key != "ZED" {
		t.Fatalf("Projects = %+v, want PRJ, ZED", ps)
	}
	p := ps[0]
	if p.Team != "alpha" || p.Repos != 2 || p.Notes != 3 || p.Revision != 1 {
		t.Errorf("PRJ = %+v, want team alpha, 2 repos, 3 notes, revision 1", p)
	}
	if p.LastMerge == nil || !p.LastMerge.Equal(mergedBase.Add(3*time.Minute)) {
		t.Errorf("PRJ last merge = %v, want PR 3's", p.LastMerge)
	}
	if ps[1].Revision != 0 || ps[1].Repos != 1 || ps[1].Notes != 1 {
		t.Errorf("ZED = %+v, want revision 0, 1 repo, 1 note", ps[1])
	}

	rs, err := s.Repos(ctx, "PRJ", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 2 || rs[0].Slug != "lib" || rs[1].Slug != "svc" {
		t.Fatalf("Repos = %+v, want lib, svc", rs)
	}
	if rs[0].Revision != 0 || rs[0].Summary != "" || rs[0].Notes != 1 || rs[0].LastMerge == nil || !rs[0].LastMerge.Equal(mergedBase.Add(3*time.Minute)) {
		t.Errorf("lib = %+v", rs[0])
	}
	if rs[1].Revision != 1 || rs[1].Summary != "svc summary" || rs[1].Notes != 2 || rs[1].LastMerge == nil || !rs[1].LastMerge.Equal(mergedBase.Add(2*time.Minute)) {
		t.Errorf("svc = %+v", rs[1])
	}
	if rs, err := s.Repos(ctx, "NONE", false); err != nil || len(rs) != 0 {
		t.Errorf("Repos(NONE) = %v, %v, want empty", rs, err)
	}
}

func TestExists(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	ingest(t, s, noteFor("PRJ", "svc", 1))
	team, ok, err := s.ProjectExists(ctx, "PRJ", false)
	if err != nil || !ok || team != "alpha" {
		t.Errorf("ProjectExists(PRJ) = %q, %v, %v; want alpha, true", team, ok, err)
	}
	if team, ok, err := s.ProjectExists(ctx, "NONE", false); err != nil || ok || team != "" {
		t.Errorf("ProjectExists(NONE) = %q, %v, %v; want false", team, ok, err)
	}
	if ok, err := s.RepoExists(ctx, "PRJ", "svc", false); err != nil || !ok {
		t.Errorf("RepoExists(PRJ, svc) = %v, %v; want true", ok, err)
	}
	if ok, err := s.RepoExists(ctx, "PRJ", "none", false); err != nil || ok {
		t.Errorf("RepoExists(PRJ, none) = %v, %v; want false", ok, err)
	}
	if ok, err := s.RepoExists(ctx, "NONE", "svc", false); err != nil || ok {
		t.Errorf("RepoExists(NONE, svc) = %v, %v; want false", ok, err)
	}
}

// With the sources on, the wiki is the sources: a project noergler named
// without a source is not listed, a source no note ever named is, with the
// team only where noergler delivered.
func TestSourcedLists(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	ingest(t, s, noteFor("PRJ", "svc", 1))
	ingest(t, s, noteFor("OLD", "gone", 2))
	sourceRepo(t, s, "PRJ", "svc")
	sourceRepo(t, s, "PRJ", "web")
	sourceRepo(t, s, "tools", "cli")

	ps, err := s.Projects(ctx, true)
	if err != nil || len(ps) != 2 {
		t.Fatalf("Projects = %+v %v", ps, err)
	}
	if ps[0].Key != "PRJ" || ps[0].Team != "alpha" || ps[0].Repos != 2 || ps[0].Notes != 1 || ps[1].Key != "tools" || ps[1].Team != "" || ps[1].Repos != 1 {
		t.Fatalf("Projects = %+v", ps)
	}
	rs, err := s.Repos(ctx, "PRJ", true)
	if err != nil || len(rs) != 2 || rs[0].Slug != "svc" || rs[0].Notes != 1 || rs[1].Slug != "web" {
		t.Fatalf("Repos = %+v %v", rs, err)
	}
	if _, err := s.InsertRevision(ctx, NewRevision{Scope: ScopeRepository, ProjectKey: "PRJ", RepoSlug: "web", Summary: "Absatz eins.\n\nAbsatz zwei.", SourcePR: "PRJ/web#1"}); err != nil {
		t.Fatal(err)
	}
	sums, err := s.RepoSummaries(ctx, "PRJ")
	if want := []RepoSummary{{"svc", ""}, {"web", "Absatz eins.\n\nAbsatz zwei."}}; err != nil || !reflect.DeepEqual(sums, want) {
		t.Errorf("RepoSummaries = %+v %v, want %+v", sums, err, want)
	}
	if team, ok, err := s.ProjectExists(ctx, "tools", true); err != nil || !ok || team != "" {
		t.Errorf("ProjectExists(tools) = %q %v %v", team, ok, err)
	}
	if _, ok, err := s.ProjectExists(ctx, "OLD", true); err != nil || ok {
		t.Errorf("ProjectExists(OLD) = %v %v, want unlisted without a source", ok, err)
	}
	if ok, err := s.RepoExists(ctx, "tools", "cli", true); err != nil || !ok {
		t.Errorf("RepoExists(tools, cli) = %v %v", ok, err)
	}
	if ok, err := s.RepoExists(ctx, "OLD", "gone", true); err != nil || ok {
		t.Errorf("RepoExists(OLD, gone) = %v %v, want unlisted", ok, err)
	}
}

// Revisions is newest first and honours the limit.
func TestRevisions_NewestFirstLimited(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	for i := 1; i <= 3; i++ {
		rev := NewRevision{Scope: ScopeRepository, ProjectKey: "PRJ", RepoSlug: "svc", Summary: "s", Chars: i * 10, SourcePR: "PRJ/svc#" + itoa(int64(i))}
		if i == 2 {
			rev.Statements = fixtureStatements()
		}
		if _, err := s.InsertRevision(ctx, rev); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.InsertRevision(ctx, NewRevision{Scope: ScopeProject, ProjectKey: "PRJ", Summary: "p", SourcePR: "PRJ/svc#9"}); err != nil {
		t.Fatal(err)
	}

	rs, err := s.Revisions(ctx, ScopeRepository, "PRJ", "svc", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 3 {
		t.Fatalf("Revisions = %+v, want 3", rs)
	}
	for i, want := range []int{3, 2, 1} {
		if rs[i].Revision != want {
			t.Errorf("rs[%d].Revision = %d, want %d", i, rs[i].Revision, want)
		}
	}
	if rs[1].Statements != 2 || rs[0].Statements != 0 || rs[1].Chars != 20 || rs[1].SourcePR != "PRJ/svc#2" || rs[0].CreatedAt.IsZero() {
		t.Errorf("rows = %+v", rs)
	}
	rs, err = s.Revisions(ctx, ScopeRepository, "PRJ", "svc", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 2 || rs[0].Revision != 3 || rs[1].Revision != 2 {
		t.Errorf("limit 2 = %+v, want 3, 2", rs)
	}
	rs, err = s.Revisions(ctx, ScopeProject, "PRJ", "", 10)
	if err != nil || len(rs) != 1 || rs[0].SourcePR != "PRJ/svc#9" {
		t.Errorf("project revisions = %+v, %v; want the one project revision", rs, err)
	}
}

// Notes lists newest first by project or by repo, with the item count.
func TestNotes_ByProjectAndRepo(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	ingest(t, s, noteFor("PRJ", "svc", 1))
	ingest(t, s, noteFor("PRJ", "lib", 2))
	three := noteFor("PRJ", "svc", 3)
	three.Body = []byte(`{"components":[{"name":"a"}],"entities":[{"name":"e"},{"name":"f"}]}`)
	three.TicketKey = ""
	three.ChangeKind = ""
	ingest(t, s, three)
	ingest(t, s, noteFor("ZED", "z", 4))
	// A list that is JSON null, or a scalar, counts as empty and breaks no
	// read: the sender's note object may carry null for an empty list.
	odd := noteFor("PRJ", "lib", 6)
	odd.Body = []byte(`{"components":null,"rules":"x","invariants":[{"name":"i"}]}`)
	odd.MergedAt = mergedBase.Add(-time.Hour)
	ingest(t, s, odd)

	ns, err := s.Notes(ctx, "PRJ", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ns) != 4 || ns[0].PRID != 3 || ns[1].PRID != 2 || ns[2].PRID != 1 || ns[3].PRID != 6 {
		t.Fatalf("Notes(PRJ) = %+v, want PRs 3, 2, 1, 6", ns)
	}
	if ns[3].Items != 1 {
		t.Errorf("PR 6 items = %d, want 1 (null and scalar lists count as empty)", ns[3].Items)
	}
	if d, ok, err := s.NoteByKey(ctx, "PRJ", "lib", 6); err != nil || !ok || d.Items != 1 {
		t.Errorf("NoteByKey(PR 6) items = %d ok=%v err=%v, want 1", d.Items, ok, err)
	}
	if ns[0].Items != 3 || ns[0].RepoSlug != "svc" || ns[0].TicketKey != "" || ns[0].ChangeKind != "" {
		t.Errorf("PR 3 = %+v, want 3 items, repo svc, empty ticket and kind", ns[0])
	}
	if ns[2].Items != 3 || ns[2].PRKey != "PRJ/svc#1" || ns[2].PRTitle != "PR 1" || ns[2].TicketKey != "ABC-1" ||
		ns[2].ChangeKind != "feature" || ns[2].Issues != 1 || ns[2].Suggestions != 2 || !ns[2].MergedAt.Equal(mergedBase.Add(time.Minute)) {
		t.Errorf("PR 1 = %+v", ns[2])
	}
	ns, err = s.Notes(ctx, "PRJ", "svc", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ns) != 2 || ns[0].PRID != 3 || ns[1].PRID != 1 {
		t.Errorf("Notes(PRJ, svc) = %+v, want PRs 3, 1", ns)
	}
	ns, err = s.Notes(ctx, "PRJ", "", 1)
	if err != nil || len(ns) != 1 || ns[0].PRID != 3 {
		t.Errorf("Notes(PRJ, limit 1) = %+v, %v; want PR 3", ns, err)
	}
}

// NoteByKey carries the note and its latest call; a missing key is ok=false.
func TestNoteByKey(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	n := bugfixFor("PRJ", "svc", 1, "internal/files", "logic", mergedBase)
	ingest(t, s, n)
	if _, ok, err := s.NoteByKey(ctx, "PRJ", "svc", 2); err != nil || ok {
		t.Errorf("missing note: ok=%v err=%v", ok, err)
	}
	d, ok, err := s.NoteByKey(ctx, "PRJ", "svc", 1)
	if err != nil || !ok {
		t.Fatalf("NoteByKey: ok=%v err=%v", ok, err)
	}
	if d.PRKey != n.PRKey || d.Team != "alpha" || d.ChangeKind != "bugfix" || d.BugfixArea != "internal/files" ||
		d.BugfixComponent != "files" || d.TicketKey != "ABC-1" || d.IssueType != "Story" || d.RunID != 101 ||
		d.SourceCommit != "abc123" || d.MergeCommit != "def456" || d.ModelLabel != "m" || !d.MergedAt.Equal(mergedBase) {
		t.Errorf("note = %+v", d.Note)
	}
	var bf struct {
		Symptom   string `json:"symptom"`
		Cause     string `json:"cause"`
		CauseKind string `json:"cause_kind"`
	}
	if err := json.Unmarshal(d.Bugfix, &bf); err != nil || bf.Symptom != "it broke" || bf.Cause != "because" || bf.CauseKind != "logic" {
		t.Errorf("Bugfix = %s (%v)", d.Bugfix, err)
	}
	if d.ReceivedAt.IsZero() || d.ConsolidatedAt != nil {
		t.Errorf("received %v consolidated %v, want set and nil", d.ReceivedAt, d.ConsolidatedAt)
	}
	if d.Call == nil {
		t.Fatal("Call = nil, want the ingested call")
	}
	c := d.Call
	if c.Outcome != "ok" || c.NoteStatus != "written" || c.Model != "test-model" || c.Rounds != 2 ||
		c.InputTokens == nil || *c.InputTokens != 1000 || c.CacheReadTokens == nil || *c.CacheReadTokens != 400 ||
		c.OutputTokens == nil || *c.OutputTokens != 50 || c.CostNanoUSD == nil || *c.CostNanoUSD != 1_500_000 ||
		c.RoundsCost == nil || *c.RoundsCost != 3_000_000 {
		t.Errorf("call = %+v", c)
	}

	// Without a call the detail has none.
	plain := noteFor("PRJ", "svc", 5)
	if ok, err := s.Ingest(ctx, deliveryFor(plain), plain, nil, nil); err != nil || !ok {
		t.Fatalf("ingest without call: ok=%v err=%v", ok, err)
	}
	d, ok, err = s.NoteByKey(ctx, "PRJ", "svc", 5)
	if err != nil || !ok || d.Call != nil {
		t.Errorf("note without call: ok=%v err=%v call=%+v", ok, err, d.Call)
	}
}

// Problems clusters bugfixes by area within the window: three fixes in one
// area with causes logic, logic, config; one stale fix and one feature
// are left out.
func TestProblems(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	ingest(t, s, bugfixFor("PRJ", "svc", 1, "internal/files", "logic", now.Add(-72*time.Hour)))
	ingest(t, s, bugfixFor("PRJ", "svc", 2, "internal/files", "logic", now.Add(-48*time.Hour)))
	ingest(t, s, bugfixFor("PRJ", "svc", 3, "internal/files", "config", now.Add(-24*time.Hour)))
	stale := bugfixFor("PRJ", "svc", 4, "internal/files", "config", now.Add(-40*24*time.Hour))
	ingest(t, s, stale)
	feature := noteFor("PRJ", "svc", 5)
	feature.MergedAt = now.Add(-time.Hour)
	ingest(t, s, feature)
	ingest(t, s, bugfixFor("OTHER", "svc", 6, "internal/files", "logic", now.Add(-time.Hour)))

	ps, err := s.Problems(ctx, "PRJ", 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 1 {
		t.Fatalf("Problems = %+v, want one area", ps)
	}
	p := ps[0]
	if p.RepoSlug != "svc" || p.Area != "internal/files" || p.Component != "files" {
		t.Errorf("area = %+v", p)
	}
	if p.Bugfixes != 3 || p.TopCause != "logic" || p.TopCauseN != 2 {
		t.Errorf("bugfixes %d top %q n %d, want 3 logic 2", p.Bugfixes, p.TopCause, p.TopCauseN)
	}
	if p.Issues != 3 || p.Suggestions != 6 {
		t.Errorf("issues %d suggestions %d, want 3 and 6", p.Issues, p.Suggestions)
	}
	if p.LastFixPR != "PRJ/svc#3" || !p.LastFixAt.Equal(now.Add(-24*time.Hour)) {
		t.Errorf("last fix = %q at %v, want PR 3", p.LastFixPR, p.LastFixAt)
	}

	// A wider window takes the stale fix in and config ties logic; the tie
	// breaks on the cause name.
	ps, err = s.Problems(ctx, "PRJ", 60)
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 1 || ps[0].Bugfixes != 4 || ps[0].TopCause != "config" || ps[0].TopCauseN != 2 {
		t.Errorf("60 days = %+v, want 4 bugfixes, config 2", ps)
	}
	if ps, err := s.Problems(ctx, "NONE", 30); err != nil || len(ps) != 0 {
		t.Errorf("Problems(NONE) = %v, %v; want empty", ps, err)
	}
}

// RepoProblems is one repository's areas over its whole history: the
// stale fix counts, another repository's and a feature do not; the newest
// three fixes' keys, newest first; most bugfixes first.
func TestRepoProblems(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	ingest(t, s, bugfixFor("PRJ", "svc", 1, "internal/files", "logic", now.Add(-400*24*time.Hour)))
	ingest(t, s, bugfixFor("PRJ", "svc", 2, "internal/files", "config", now.Add(-72*time.Hour)))
	ingest(t, s, bugfixFor("PRJ", "svc", 3, "internal/files", "config", now.Add(-48*time.Hour)))
	ingest(t, s, bugfixFor("PRJ", "svc", 4, "internal/files", "logic", now.Add(-24*time.Hour)))
	ingest(t, s, bugfixFor("PRJ", "svc", 5, "internal/api", "logic", now.Add(-time.Hour)))
	ingest(t, s, bugfixFor("PRJ", "other", 6, "internal/files", "logic", now.Add(-time.Hour)))
	feature := noteFor("PRJ", "svc", 7)
	feature.MergedAt = now
	ingest(t, s, feature)
	// Merged after the commit read: no evidence about it.
	ingest(t, s, bugfixFor("PRJ", "svc", 8, "internal/later", "logic", now.Add(time.Hour)))

	ps, err := s.RepoProblems(ctx, "PRJ", "svc", now)
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 {
		t.Fatalf("RepoProblems = %+v, want two areas", ps)
	}
	files := ps[0]
	// logic and config tie at two; the tie breaks on the name.
	if files.Area != "internal/files" || files.Bugfixes != 4 || files.TopCause != "config" ||
		strings.Join(files.PRKeys, ",") != "PRJ/svc#4,PRJ/svc#3,PRJ/svc#2" {
		t.Errorf("files = %+v", files)
	}
	if api := ps[1]; api.Area != "internal/api" || api.Bugfixes != 1 || api.TopCause != "logic" || strings.Join(api.PRKeys, ",") != "PRJ/svc#5" {
		t.Errorf("api = %+v", api)
	}
	if ps, err := s.RepoProblems(ctx, "PRJ", "none", now); err != nil || len(ps) != 0 {
		t.Errorf("RepoProblems(none) = %v, %v; want empty", ps, err)
	}
}
