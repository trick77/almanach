package consolidate

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/trick77/almanach/internal/baseline"
	"github.com/trick77/almanach/internal/gitrepo"
	"github.com/trick77/almanach/internal/inference"
	"github.com/trick77/almanach/internal/store"
)

// A project of one has no project step: no call, no project revision,
// nothing skipped; the repository map is still drawn.
func TestRun_SingleLeavesTheProjectOut(t *testing.T) {
	x := newWorld(t, 16)
	x.st.others = []store.RepoSummary{{Slug: "svc"}}
	x.queue(store.RunFull, "")
	x.models.catalogue = []inference.CatalogueResult{{Call: called, Catalogue: inference.Catalogue{Subjects: []inference.Subject{
		{Name: "Anmeldung", Purpose: "Login.", Paths: []string{"auth/"}},
	}}}}
	x.models.patches = []inference.PatchResult{
		{Call: called, Patch: inference.Patch{Summary: ref("Ein Dienst."), Statements: []inference.Statement{stmt(nil, "component", "Dienst", "Der Dienst.", "README.md")}}},
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
			t.Fatalf("a project revision for a project of one: %+v", rev)
		}
	}
}

// A project whose second repository just came has no project page yet: the
// next run folds it even where a quiet reconcile would skip the project.
func TestRun_SecondRepoFoldsTheProject(t *testing.T) {
	x := newWorld(t, 16)
	delete(x.st.pages, pageKey(store.ScopeProject, "tools", ""))
	raw, _ := json.Marshal(runPlan{Plan: baseline.Plan{Subjects: []baseline.Subject{{Name: "A", Files: []baseline.File{{Path: "auth/login.go", Size: 30}}}}}})
	x.st.documented = &store.BaselineRun{Commit: older40, Plan: raw, State: store.RunDone}
	x.st.pages[pageKey(store.ScopeRepository, "tools", "svc")] = store.Page{RevisionID: 2, Revision: 1, Summary: "Ein Dienst.", Statements: []store.Statement{}}
	x.code.diff = []gitrepo.Entry{{Path: "go.sum", Mode: "100644", Size: 6}}
	x.queue(store.RunReconcile, older40)
	x.models.project = inference.DescriptionResult{Call: called, Description: &inference.Description{Summary: "Zwei Dienste.",
		Statements: []inference.Statement{}, Map: drawnMap("svc", "web")}}
	x.models.repoMap = inference.RepoMapResult{Call: called, Map: drawnRepoMap()}
	x.w.drain(context.Background())
	var project bool
	for _, c := range x.st.calls {
		project = project || c.Purpose == store.PurposeProject
	}
	if !project {
		t.Fatalf("ledger = %+v, want the project folded; run %+v", x.st.calls, x.run())
	}
}

// A note of a project of one folds into the repository alone: the prompt
// shows no project page and asks for null, and a project rewrite that comes
// anyway is not written.
func TestFoldSingleLeavesTheProjectOut(t *testing.T) {
	st := newFakeStore()
	st.repoCount = map[string]int{"PLAT": 1}
	st.pages[pageKey(store.ScopeProject, "PLAT", "")] = store.Page{Scope: store.ScopeProject, ProjectKey: "PLAT", RevisionID: 9, Revision: 1,
		Summary: "Eine frühere Projektseite."}
	m := &fakeMerger{result: okResult(
		&inference.Description{Summary: "Der Dienst speichert Dateien.", Statements: []inference.Statement{{Kind: "component", Name: "Uploader", Text: "Nimmt Dateien an."}}},
		&inference.Description{Summary: "Erfunden.", Statements: []inference.Statement{}},
	)}
	newWorker(st, m).WithBaseline(Baseline{Store: &runStore{fakeStore: st}}).Fold(context.Background(), queued("PLAT/files#42"))
	if len(m.prompts) != 1 || !strings.Contains(m.prompts[0], singleProject) || strings.Contains(m.prompts[0], "Eine frühere Projektseite.") {
		t.Fatalf("prompt does not show the single line in place of the page: %q", m.prompts)
	}
	if len(st.revisions) != 1 || st.revisions[0].Scope != store.ScopeRepository {
		t.Fatalf("revisions = %+v, want the repository's alone", st.revisions)
	}
}

// With the sources off there are no projects of one: every project keeps
// its page.
func TestFoldSingleNeedsTheSources(t *testing.T) {
	st := newFakeStore()
	st.repoCount = map[string]int{"PLAT": 1}
	m := &fakeMerger{result: okResult(nil, &inference.Description{Summary: "Zwei Repositories.", Statements: []inference.Statement{}})}
	newWorker(st, m).Fold(context.Background(), queued("PLAT/files#42"))
	if len(st.revisions) != 1 || st.revisions[0].Scope != store.ScopeProject {
		t.Fatalf("revisions = %+v, want the project's", st.revisions)
	}
}
