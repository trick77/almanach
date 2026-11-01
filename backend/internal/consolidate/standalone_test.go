package consolidate

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/trick77/almanach/internal/baseline"
	"github.com/trick77/almanach/internal/gitrepo"
	"github.com/trick77/almanach/internal/inference"
	"github.com/trick77/almanach/internal/store"
)

// A folder whose repositories stand alone has no project step: no call, no
// project revision, nothing skipped; the repository map is still drawn.
func TestRun_StandaloneLeavesTheProjectOut(t *testing.T) {
	x := newWorld(t, 16)
	x.st.standalone = map[string]bool{"tools": true}
	x.queue(store.RunFull, "")
	x.models.catalogue = []inference.CatalogueResult{{Call: called, Catalogue: inference.Catalogue{Subjects: []inference.Subject{
		{Name: "Anmeldung", Purpose: "Login.", Paths: []string{"auth/"}},
	}}}}
	x.models.patches = []inference.PatchResult{
		{Call: called, Patch: inference.Patch{Summary: ref("Ein Dienst für Werkzeuge."), Statements: []inference.Statement{stmt(nil, "component", "Dienst", "Der Dienst.", "README.md")}}},
		{Call: called, Patch: inference.Patch{Statements: []inference.Statement{stmt(nil, "rule", "Login", "Zweistufig.", "auth/login.go")}}},
	}
	x.models.project = inference.DescriptionResult{Call: called, Description: &inference.Description{Summary: "Erfunden.", Statements: []inference.Statement{}}}
	x.models.repoMap = inference.RepoMapResult{Call: called, Map: drawnRepoMap()}
	x.w.drain(context.Background())

	r := x.run()
	if r.State != store.RunDone || r.Skipped != 0 {
		t.Fatalf("run = %+v, want done with nothing skipped", r)
	}
	var purposes []string
	for _, c := range x.st.calls {
		purposes = append(purposes, c.Purpose)
	}
	if !reflect.DeepEqual(purposes, []string{"catalogue", "overview", "subject", "repo_map"}) {
		t.Errorf("ledger = %v, want no project call", purposes)
	}
	for _, rev := range x.st.revisions {
		if rev.Scope == store.ScopeProject {
			t.Fatalf("a project revision for a standalone folder: %+v", rev)
		}
	}
	if !x.plan(t).ProjectDone {
		t.Error("the project step is not marked over")
	}
}

// A database that cannot say whether the folder stands alone pauses the
// drain: no project call is paid on a guess.
func TestRun_StandaloneReadFailurePauses(t *testing.T) {
	x := newWorld(t, 16)
	x.st.standaloneErr = errors.New("database is locked")
	x.queue(store.RunFull, "")
	x.models.catalogue = []inference.CatalogueResult{{Call: called, Catalogue: inference.Catalogue{Subjects: []inference.Subject{
		{Name: "Anmeldung", Purpose: "Login.", Paths: []string{"auth/"}},
	}}}}
	x.models.patches = []inference.PatchResult{
		{Call: called, Patch: inference.Patch{Summary: ref("Ein Dienst."), Statements: []inference.Statement{stmt(nil, "component", "Dienst", "Der Dienst.", "README.md")}}},
		{Call: called, Patch: inference.Patch{Statements: []inference.Statement{stmt(nil, "rule", "Login", "Zweistufig.", "auth/login.go")}}},
	}
	x.models.project = inference.DescriptionResult{Call: called, Description: &inference.Description{Summary: "P.", Statements: []inference.Statement{}}}
	x.w.drain(context.Background())
	for _, c := range x.st.calls {
		if c.Purpose == store.PurposeProject {
			t.Fatal("a project call although the source could not be read")
		}
	}
	if r := x.run(); r.State == store.RunDone {
		t.Fatalf("run = %+v, want it left for the next drain", r)
	}
}

// A note of a folder whose repositories stand alone folds into the
// repository alone: the prompt shows no project page and asks for null,
// and a project rewrite that comes anyway is not written.
func TestFoldStandaloneLeavesTheProjectOut(t *testing.T) {
	st := newFakeStore()
	st.standalone = map[string]bool{"PLAT": true}
	st.pages[pageKey(store.ScopeProject, "PLAT", "")] = store.Page{Scope: store.ScopeProject, ProjectKey: "PLAT", RevisionID: 9, Revision: 1,
		Summary: "Ein früheres System."}
	m := &fakeMerger{result: okResult(
		&inference.Description{Summary: "Der Dienst speichert Dateien.", Statements: []inference.Statement{{Kind: "component", Name: "Uploader", Text: "Nimmt Dateien an."}}},
		&inference.Description{Summary: "Erfunden.", Statements: []inference.Statement{}},
	)}
	newWorker(st, m).WithBaseline(Baseline{Store: &runStore{fakeStore: st}}).Fold(context.Background(), queued("PLAT/files#42"))
	if len(m.prompts) != 1 || !strings.Contains(m.prompts[0], standaloneProject) || strings.Contains(m.prompts[0], "Ein früheres System.") {
		t.Fatalf("prompt does not show the standalone line in place of the page: %q", m.prompts)
	}
	if len(st.revisions) != 1 || st.revisions[0].Scope != store.ScopeRepository {
		t.Fatalf("revisions = %+v, want the repository's alone", st.revisions)
	}
	if len(st.marked) != 1 {
		t.Errorf("marked = %v, want the note marked", st.marked)
	}
}

// With the sources off a folder's old word counts for nothing: every
// project is a system again, its page folded.
func TestFoldStandaloneNeedsTheSources(t *testing.T) {
	st := newFakeStore()
	st.standalone = map[string]bool{"PLAT": true}
	m := &fakeMerger{result: okResult(nil, &inference.Description{Summary: "Zwei Repositories.", Statements: []inference.Statement{}})}
	newWorker(st, m).Fold(context.Background(), queued("PLAT/files#42"))
	if len(st.revisions) != 1 || st.revisions[0].Scope != store.ScopeProject {
		t.Fatalf("revisions = %+v, want the project's", st.revisions)
	}
}

// A folder that became a system again is folded by the next run even
// where a reconcile would skip the project, and the mark is cleared once
// the step got its answer.
func TestRun_SystemAgainFoldsTheProject(t *testing.T) {
	x := newWorld(t, 16)
	x.st.due = map[string]bool{"tools": true}
	raw, _ := json.Marshal(runPlan{Plan: baseline.Plan{Subjects: []baseline.Subject{{Name: "A", Files: []baseline.File{{Path: "auth/login.go", Size: 30}}}}}})
	x.st.documented = &store.BaselineRun{Commit: older40, Plan: raw, State: store.RunDone}
	// Nothing a subject holds changed: without the mark a quiet reconcile
	// that makes no call at all.
	x.st.pages[pageKey(store.ScopeRepository, "tools", "svc")] = store.Page{RevisionID: 2, Revision: 1, Summary: "Ein Dienst.", Statements: []store.Statement{}}
	x.code.diff = []gitrepo.Entry{{Path: "go.sum", Mode: "100644", Size: 6}}
	x.queue(store.RunReconcile, older40)
	x.models.project = inference.DescriptionResult{Call: called}
	x.models.repoMap = inference.RepoMapResult{Call: called, Map: drawnRepoMap()}
	x.w.drain(context.Background())
	var project bool
	for _, c := range x.st.calls {
		project = project || c.Purpose == store.PurposeProject
	}
	if !project {
		t.Fatalf("ledger = %+v, want the project folded; run %+v saved %v", x.st.calls, x.run(), x.st.saved)
	}
	if !reflect.DeepEqual(x.st.folded, []string{"tools"}) || x.st.due["tools"] {
		t.Errorf("folded = %v, due = %v, want the mark cleared", x.st.folded, x.st.due)
	}
}
