package consolidate

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/trick77/almanach/internal/inference"
	"github.com/trick77/almanach/internal/messaging"
	"github.com/trick77/almanach/internal/store"
)

func lib(project, slug, summary string, users ...string) store.SharedRepo {
	l := store.SharedRepo{RepoRef: store.RepoRef{ProjectKey: project, Slug: slug}, Summary: summary}
	for _, u := range users {
		l.UsedBy = append(l.UsedBy, store.RepoRef{ProjectKey: "tools", Slug: u})
	}
	return l
}

// Every describe step reads the summaries of the libraries its repository
// uses; the project step those of other projects the project uses, with
// their users.
func TestRun_LibrariesAsContext(t *testing.T) {
	x := newWorld(t, 16)
	x.st.libs = map[string][]store.SharedRepo{
		"svc": {lib("libs", "wire", "Ein Client für Modelle.", "svc")},
		"":    {lib("libs", "wire", "Ein Client für Modelle.", "svc", "cli")},
	}
	x.queue(store.RunFull, "")
	x.models.catalogue = []inference.CatalogueResult{{Call: called, Catalogue: inference.Catalogue{Subjects: []inference.Subject{
		{Name: "Anmeldung", Purpose: "Login.", Paths: []string{"auth/"}},
	}}}}
	x.models.patches = []inference.PatchResult{
		{Call: called, Patch: inference.Patch{Summary: ref("Ein Service."), Statements: []inference.Statement{stmt(nil, "component", "Service", "Der Service.", "README.md")}}},
		{Call: called, Patch: inference.Patch{Statements: []inference.Statement{stmt(nil, "rule", "Login", "Zweistufig.", "auth/login.go")}}},
	}
	x.models.project = inference.DescriptionResult{Call: called}
	x.models.repoMap = inference.RepoMapResult{Call: called, Map: drawnRepoMap()}
	x.w.drain(context.Background())

	var describe, project int
	for _, p := range x.models.prompts {
		switch {
		case strings.Contains(p, "## Repositories this repository depends on"):
			describe++
			if !strings.Contains(p, "### libs/wire\n\nEin Client für Modelle.") {
				t.Errorf("describe prompt without the library's summary:\n%s", p)
			}
		case strings.Contains(p, "## Repositories of other projects"):
			project++
			if !strings.Contains(p, "### libs/wire (used by svc, cli)\n\nEin Client für Modelle.") {
				t.Errorf("project prompt without the library and its users:\n%s", p)
			}
		}
	}
	if describe != 2 || project != 1 {
		t.Fatalf("prompts with libraries: %d describe, %d project; want 2 and 1", describe, project)
	}
	if x.st.libReads["svc"] != 1 {
		t.Errorf("the repository's libraries read %d times, want once a run", x.st.libReads["svc"])
	}
}

// Every describe step reads what the repository's code sends and
// receives, with the far side, and the schemas it defines.
func TestTopicsShown(t *testing.T) {
	svc, cli := store.RepoRef{ProjectKey: "tools", Slug: "svc"}, store.RepoRef{ProjectKey: "tools", Slug: "cli"}
	tp := func(name, dir string) messaging.Topic { return messaging.Topic{Name: name, Direction: dir} }
	m := store.NewMessages(map[store.RepoRef]messaging.Read{
		svc: {Topics: []messaging.Topic{tp("jobs", messaging.Produces), tp("done", messaging.Consumes), tp("audit", messaging.Declares)},
			Schemas: []messaging.Schema{{Name: "ex.Job", Kind: "avro", File: "job.avsc"}}},
		cli: {Topics: []messaging.Topic{tp("jobs", messaging.Consumes)}},
	})
	want := "- sends to `jobs`, received by `tools/cli`\n- receives from `done`, sent by none of this wiki's repositories\n" +
		"- names `audit` (the code does not say which way)\n- defines the avro schema `ex.Job` (`job.avsc`)"
	if got := topicsShown(m, svc, "c1"); got != want {
		t.Errorf("topicsShown =\n%s\nwant\n%s", got, want)
	}
	if got := topicsShown(m, store.RepoRef{ProjectKey: "tools", Slug: "web"}, "c1"); got != "_None._" {
		t.Errorf("without topics: %q", got)
	}
	// Read at a commit the run is not pinned to: none shown.
	later := store.NewMessagesAt(map[store.RepoRef]messaging.Read{svc: {Topics: []messaging.Topic{tp("jobs", messaging.Produces)}}},
		map[store.RepoRef]string{svc: "c2"})
	if got := topicsShown(later, svc, "c1"); !strings.HasPrefix(got, "_Not read at this run's commit") {
		t.Errorf("another commit's topics: %q", got)
	}

	x := newWorld(t, 16)
	x.st.messages = map[store.RepoRef]messaging.Read{svc: {Topics: []messaging.Topic{tp("jobs", messaging.Produces)}}}
	x.queue(store.RunFull, "")
	x.models.catalogue = []inference.CatalogueResult{{Call: called, Catalogue: inference.Catalogue{Subjects: []inference.Subject{
		{Name: "Anmeldung", Purpose: "Login.", Paths: []string{"auth/"}},
	}}}}
	x.models.patches = []inference.PatchResult{
		{Call: called, Patch: inference.Patch{Summary: ref("Ein Service."), Statements: []inference.Statement{stmt(nil, "component", "Service", "Der Service.", "README.md")}}},
		{Call: called, Patch: inference.Patch{Statements: []inference.Statement{stmt(nil, "rule", "Login", "Zweistufig.", "auth/login.go")}}},
	}
	x.models.project = inference.DescriptionResult{Call: called}
	x.models.repoMap = inference.RepoMapResult{Call: called, Map: drawnRepoMap()}
	x.w.drain(context.Background())
	shown := 0
	for _, p := range x.models.prompts {
		if strings.Contains(p, "## Topics this repository's code sends and receives\n\n- sends to `jobs`, received by none of this wiki's repositories") {
			shown++
		}
	}
	if shown != 2 {
		t.Errorf("describe prompts with the topics: %d, want both steps", shown)
	}
}

// The model is left room on the map for what code adds: a node per
// library, an edge per user; never less than the repositories need.
func TestMapRoom(t *testing.T) {
	nodes, edges := mapRoom([]store.SharedRepo{lib("libs", "a", "", "x", "y"), lib("libs", "b", "", "x")}, 3)
	if nodes != 20 || edges != 30 {
		t.Errorf("room = %d nodes, %d edges; want 20 and 30", nodes, edges)
	}
	if nodes, edges := mapRoom(make([]store.SharedRepo, 30), 5); nodes != 5 || edges != 33 {
		t.Errorf("room with many libraries = %d, %d; want the repositories' 5 and all edges", nodes, edges)
	}
}

// A store that cannot name the libraries pauses the drain: no step is
// paid without its context.
func TestRun_LibrariesReadFailurePauses(t *testing.T) {
	x := newWorld(t, 16)
	x.st.libsErr = errors.New("database is locked")
	x.queue(store.RunFull, "")
	x.models.catalogue = []inference.CatalogueResult{{Call: called, Catalogue: inference.Catalogue{Subjects: []inference.Subject{
		{Name: "Anmeldung", Purpose: "Login.", Paths: []string{"auth/"}},
	}}}}
	x.w.drain(context.Background())
	for _, c := range x.st.calls {
		if c.Purpose != store.PurposeCatalogue {
			t.Fatalf("a %s call although the libraries could not be read", c.Purpose)
		}
	}
	if r := x.run(); r.State == store.RunDone {
		t.Fatalf("run = %+v, want it left for the next drain", r)
	}
}

func TestLibrariesRender(t *testing.T) {
	if got := sharedRepos(nil, false); got != "_None._" {
		t.Errorf("no libraries = %q", got)
	}
	got := sharedRepos([]store.SharedRepo{lib("libs", "wire", "Kurz."), lib("libs", "fetch", "")}, false)
	if got != "### libs/wire\n\nKurz.\n\nAlso depended on, without a summary here: libs/fetch.\n" {
		t.Errorf("render = %q", got)
	}
}
