package baseline

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/trick77/almanach/internal/gitrepo"
	"github.com/trick77/almanach/internal/inference"
	"github.com/trick77/almanach/internal/store"
)

func TestBlocked(t *testing.T) {
	for p, want := range map[string]bool{
		"main.go":                          false,
		"internal/api/api.go":              false,
		"docs/plans/x.html":                false,
		"vendor/github.com/x/y.go":         true,
		"ui/node_modules/react/index.js":   true,
		"dist/app.js":                      true,
		"internal/build/info.go":           false,
		"packages/web/dist/index.js":       true,
		"action/coverage/lcov.info":        true,
		"cmd/target/main.go":               false,
		"go.sum":                           true,
		"ui/package-lock.json":             true,
		"assets/logo.PNG":                  true,
		"ui/src/fonts/Sans.woff2":          true,
		"static/app.min.js":                true,
		"api/v1/service.pb.go":             true,
		"internal/mock_generated.go":       true,
		"src/__snapshots__/a.test.ts.snap": true,
		"build.gradle":                     false,
		"buildinfo/x.go":                   false,
		"deploy/certs/tls.key":             true,
		"certs/ca.PEM":                     true,
		"src/main/resources/store.jks":     true,
		"ops/.ssh/id_ed25519":              true,
		".aws/credentials":                 true,
		"internal/credentials/store.go":    false,
	} {
		if got := Blocked(p); got != want {
			t.Errorf("Blocked(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestFilesAndReadable(t *testing.T) {
	entries := []gitrepo.Entry{
		{Path: "z.go", Mode: "100644", Size: 10},
		{Path: "a.sh", Mode: "100755", Size: 5},
		{Path: "empty.go", Mode: "100644", Size: 0},
		{Path: "huge.json", Mode: "100644", Size: FileMaxBytes + 1},
		{Path: "max.go", Mode: "100644", Size: FileMaxBytes},
		{Path: "link", Mode: "120000", Size: 4},
		{Path: "sub", Mode: "160000"},
		{Path: "go.sum", Mode: "100644", Size: 50},
		{Path: "gone.go", Mode: "100644", Size: 3, Deleted: true},
	}
	got := Files(entries)
	want := []File{{"a.sh", 5}, {"max.go", FileMaxBytes}, {"z.go", 10}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Files = %v, want %v", got, want)
	}
	if Binary([]byte("plain text\n")) || !Binary([]byte("PK\x03\x04\x00zip")) || Binary(nil) {
		t.Error("Binary misjudges text, a NUL or nothing")
	}
	late := append([]byte(strings.Repeat("a", 9000)), 0)
	if Binary(late) {
		t.Error("a NUL past the first 8000 bytes made a text binary")
	}
}

func TestDocs_OrderAndLimit(t *testing.T) {
	files := []File{
		{"docs/guide.md", 30}, {"docs/img.txt", 5}, {"internal/x.go", 10}, {"AGENTS.md", 20}, {"README.md", 40},
		{"backend/go.mod", 8}, {"Makefile", 6}, {"HOW_IT_WORKS.md", 50}, {"backend/README.md", 12}, {"docs/deep/a.md", 7},
		{"backend/internal/README.md", 9}, {"compose.yaml", 4}, {"api/openapi.yaml", 11},
		{"src/Configuration.java", 3}, {"design.go", 2},
	}
	got := Docs(files, 1000)
	var paths []string
	for _, f := range got {
		paths = append(paths, f.Path)
	}
	want := []string{"README.md", "AGENTS.md", "HOW_IT_WORKS.md", "backend/README.md", "Makefile", "compose.yaml",
		"api/openapi.yaml", "backend/go.mod", "docs/guide.md", "docs/img.txt", "docs/deep/a.md"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("Docs order = %v\nwant %v", paths, want)
	}
	// 40 + 20 fit 65; HOW_IT_WORKS (50) does not and is skipped, the 4 of
	// compose.yaml still fits after.
	got = Docs(files, 65)
	paths = nil
	for _, f := range got {
		paths = append(paths, f.Path)
	}
	if !reflect.DeepEqual(paths, []string{"README.md", "AGENTS.md", "compose.yaml"}) {
		t.Fatalf("Docs under a limit = %v", paths)
	}
}

// Diagram sources at the root or under docs/ come after every prose doc;
// elsewhere, or as .drawio, they are not docs.
func TestDocs_DiagramSourcesLast(t *testing.T) {
	files := []File{
		{"docs/flow.puml", 3}, {"architecture.mmd", 4}, {"docs/zz.md", 5}, {"README.md", 6},
		{"src/seq.puml", 2}, {"docs/map.drawio", 2}, {"docs/c4.plantuml", 1},
	}
	var paths []string
	for _, f := range Docs(files, 1000) {
		paths = append(paths, f.Path)
	}
	want := []string{"README.md", "docs/zz.md", "architecture.mmd", "docs/c4.plantuml", "docs/flow.puml"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("Docs = %v\nwant %v", paths, want)
	}
}

// A README is the README whatever its markup.
func TestDocs_ReadmeInAnyMarkup(t *testing.T) {
	for _, name := range []string{"README", "README.markdown", "readme.org", "README.asciidoc", "README.textile", "README.rst"} {
		got := Docs([]File{{"main.go", 1}, {name, 5}}, 100)
		if len(got) != 1 || got[0].Path != name {
			t.Errorf("Docs with %s = %v, want it read", name, got)
		}
	}
	if got := Docs([]File{{"readme.go", 5}}, 100); len(got) != 0 {
		t.Errorf("readme.go read as a doc: %v", got)
	}
}

func TestListing_FilesThenDirectories(t *testing.T) {
	small := Listing([]File{{"a.go", 3}, {"x/b.go", 4}})
	if small != "a.go\t3\nx/b.go\t4\n" {
		t.Fatalf("small listing = %q", small)
	}
	var many []File
	many = append(many, File{"README.md", 9})
	for i := 0; i < ListMaxEntries; i++ {
		many = append(many, File{fmt.Sprintf("svc/handlers/h%d.go", i), 1}, File{fmt.Sprintf("svc/main%d.go", i), 2})
	}
	big := Listing(many)
	want := "README.md\t9\nsvc/\t4000 files\t8000 bytes\nsvc/handlers/\t4000 files\t4000 bytes\n"
	if big != want {
		t.Fatalf("big listing = %q\nwant %q", big, want)
	}
}

func subject(name string, paths ...string) inference.Subject {
	return inference.Subject{Name: name, Purpose: "  why " + name + "  ", Paths: paths}
}

func names(p Plan) []string {
	var out []string
	for _, s := range p.Subjects {
		var files []string
		for _, f := range s.Files {
			files = append(files, f.Path)
		}
		out = append(out, s.Name+":"+strings.Join(files, ","))
	}
	return out
}

func TestBuildPlan_HoldsTheCatalogueToTheTree(t *testing.T) {
	files := []File{
		{"auth/login.go", 100}, {"auth/session.go", 200}, {"auth/token/jwt.go", 50},
		{"billing/run.go", StepMaxBytes - 10}, {"billing/close.go", 30}, {"billing/tiny.go", 5}, {"main.go", 10},
	}
	p := BuildPlan([]inference.Subject{
		subject("Auth", "auth/", "auth/login.go", "invented.go"),
		subject("  ", "main.go"),
		subject("Billing", "billing/run.go", "billing/close.go", "billing/tiny.go"),
		subject("Nothing", "nope/", "vendor/x.go"),
		subject("auth", "main.go"),
		subject("Tokens", "auth/token"),
		subject("Over", "main.go"),
	}, files, 3)
	want := []string{
		// The directory in path order, the repeated file once, the invented
		// path gone; the second "auth" merged into the first.
		"Auth:auth/login.go,auth/session.go,auth/token/jwt.go,main.go",
		// close.go does not fit beside run.go; tiny.go still does.
		"Billing:billing/run.go,billing/tiny.go",
		// A directory named without its slash.
		"Tokens:auth/token/jwt.go",
	}
	if got := names(p); !reflect.DeepEqual(got, want) {
		t.Fatalf("plan = %v\nwant %v", got, want)
	}
	if p.Subjects[0].Purpose != "why Auth" {
		t.Errorf("purpose = %q, want trimmed", p.Subjects[0].Purpose)
	}
	if p.Bytes() != 100+200+50+10+StepMaxBytes-10+5+50 {
		t.Errorf("Bytes = %d", p.Bytes())
	}
	if empty := BuildPlan(nil, files, 3); empty.Subjects == nil || len(empty.Subjects) != 0 {
		t.Errorf("no proposal = %+v, want an empty, non-nil plan", empty)
	}
}

// A part is named once with the first reason that hit it; the overview and
// the project list without a name, and a run from before reasons has none.
func TestPlan_SkipKeepsTheFirstReason(t *testing.T) {
	var p Plan
	p.SkipStep(PartProject, SkipNoPage)
	p.SkipSubject("Auth", SkipFailed)
	p.SkipSubject("Auth", SkipCalls)
	p.SkipStep(PartOverview, SkipBytes)
	p.SkipStep(PartOverview, SkipCalls)
	// The run's order, whatever order they were marked in: overview,
	// subjects, project.
	want := []SkippedPart{
		{Part: "overview", Name: "", Reason: SkipBytes},
		{Part: "subject", Name: "Auth", Reason: SkipFailed},
		{Part: "project", Name: "", Reason: SkipNoPage},
	}
	if got := p.SkippedParts(); !reflect.DeepEqual(got, want) {
		t.Fatalf("parts = %+v\nwant %+v", got, want)
	}
	if !p.StepSkipped(PartOverview) || !p.StepSkipped(PartProject) || (Plan{}).StepSkipped(PartProject) {
		t.Errorf("StepSkipped wrong: %+v", p.Steps)
	}
	old := Plan{Skipped: []string{"Auth"}}
	if got := old.SkippedParts(); !reflect.DeepEqual(got, []SkippedPart{{Part: "subject", Name: "Auth", Reason: ""}}) {
		t.Errorf("old plan parts = %+v", got)
	}
	if got := (Plan{}).SkippedParts(); got == nil || len(got) != 0 {
		t.Errorf("no skips = %#v, want an empty list", got)
	}
}

// RunSkips reads a stored plan's skips the way the worker wrote them,
// through Plan's own JSON.
func TestRunSkips_ReadsAStoredPlan(t *testing.T) {
	var p Plan
	p.Subjects = []Subject{{Name: "Auth", Purpose: "p", Files: []File{{"auth/a.go", 1}}}}
	p.SkipSubject("Auth", SkipBytes)
	raw, _ := json.Marshal(p)
	got, err := RunSkips(raw)
	if err != nil || !reflect.DeepEqual(got, []SkippedPart{{Part: "subject", Name: "Auth", Reason: SkipBytes}}) {
		t.Fatalf("RunSkips = %+v, %v", got, err)
	}
	if got, err := RunSkips(nil); err != nil || len(got) != 0 {
		t.Errorf("no plan = %+v, %v", got, err)
	}
	if _, err := RunSkips(json.RawMessage(`{"skipped":7}`)); err == nil {
		t.Error("a malformed plan decoded")
	}
}

// A subject the model names like a step stays a subject: the steps are
// held by Part, never by name.
func TestPlan_SubjectNamedLikeAStepStaysASubject(t *testing.T) {
	p := Plan{Format: PlanFormat}
	p.SkipSubject("the project", SkipBytes)
	p.SkipSubject("the overview", SkipFailed)
	raw, _ := json.Marshal(p)
	got, err := RunSkips(raw)
	want := []SkippedPart{
		{Part: "subject", Name: "the project", Reason: SkipBytes},
		{Part: "subject", Name: "the overview", Reason: SkipFailed},
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("RunSkips = %+v, %v\nwant %+v", got, err, want)
	}
	if p.StepSkipped(PartProject) || p.StepSkipped(PartOverview) {
		t.Errorf("a subject marked a step: %+v", p.Steps)
	}
}

// A plan stored before Steps held the two steps by name in Skipped:
// Upgrade moves them, with their reasons, and leaves the subjects.
func TestPlan_UpgradeMovesTheLegacyStepNames(t *testing.T) {
	raw := json.RawMessage(`{"subjects":[],"skipped":["the overview","Auth","the project"],"why":{"the overview":"calls","Auth":"calls"}}`)
	var p Plan
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	p.Upgrade()
	if !reflect.DeepEqual(p.Skipped, []string{"Auth"}) || !reflect.DeepEqual(p.Why, map[string]Skip{"Auth": SkipCalls}) ||
		!reflect.DeepEqual(p.Steps, map[Part]Skip{PartOverview: SkipCalls, PartProject: ""}) {
		t.Fatalf("upgraded = %+v", p)
	}
	got, err := RunSkips(raw)
	want := []SkippedPart{
		{Part: "overview", Reason: SkipCalls},
		{Part: "subject", Name: "Auth", Reason: SkipCalls},
		{Part: "project", Reason: ""},
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("RunSkips = %+v, %v", got, err)
	}
	// A plan with nothing to move is left as it was.
	q := Plan{Skipped: []string{"Auth"}}
	q.Upgrade()
	if !reflect.DeepEqual(q.Skipped, []string{"Auth"}) || q.Steps != nil || q.Format != PlanFormat {
		t.Errorf("no legacy names: %+v", q)
	}
	// A current plan's subjects are never read as steps, whatever their name.
	c := Plan{Format: PlanFormat, Skipped: []string{"the project"}}
	c.Upgrade()
	if !reflect.DeepEqual(c.Skipped, []string{"the project"}) || c.Steps != nil {
		t.Errorf("current plan upgraded: %+v", c)
	}
}

func TestMergePlan_KeepsResizesAndPlaces(t *testing.T) {
	old := Plan{Subjects: []Subject{
		{Name: "Auth", Purpose: "p", Files: []File{{"auth/login.go", 100}, {"auth/gone.go", 10}}},
		{Name: "Old", Purpose: "p", Files: []File{{"old/x.go", 5}}},
	}, Skipped: []string{"Old"}}
	files := []File{{"auth/login.go", 120}, {"auth/new.go", 7}, {"jobs/run.go", 9}}
	p := MergePlan(old, []inference.Subject{subject("AUTH", "auth/new.go"), subject("Jobs", "jobs/"), subject("Third", "jobs/run.go")}, files, 2)
	want := []string{"Auth:auth/login.go,auth/new.go", "Jobs:jobs/run.go"}
	if got := names(p); !reflect.DeepEqual(got, want) {
		t.Fatalf("merged = %v\nwant %v", got, want)
	}
	if p.Subjects[0].Files[0].Size != 120 || p.Subjects[0].Name != "Auth" {
		t.Errorf("kept subject = %+v, want its name and the new size", p.Subjects[0])
	}
	if p.Skipped != nil {
		t.Errorf("Skipped = %v, want a new plan's own", p.Skipped)
	}
	// A subject named with umlauts where the old plan wrote them out is the
	// same subject, not a second one.
	spelled := Plan{Subjects: []Subject{{Name: "Datenueberpruefung", Purpose: "p", Files: []File{{"auth/login.go", 100}}}}}
	if got := names(MergePlan(spelled, []inference.Subject{subject("Datenüberprüfung", "auth/new.go")}, files, 2)); !reflect.DeepEqual(got, []string{"Datenueberpruefung:auth/login.go,auth/new.go"}) {
		t.Errorf("respelled subject = %v, want the old one with the new file", got)
	}
	// Files that grew are held to the step budget again, in their order.
	grown := Plan{Subjects: []Subject{{Name: "Big", Files: []File{{"a.go", 100}, {"b.go", 100}, {"c.go", 10}}}}}
	half := StepMaxBytes / 2
	now := []File{{"a.go", int64(half)}, {"b.go", int64(half) + 1}, {"c.go", 10}}
	if got := names(MergePlan(grown, nil, now, 5)); !reflect.DeepEqual(got, []string{"Big:a.go,c.go"}) {
		t.Errorf("grown subject = %v, want b.go past the budget left out", got)
	}
}

func TestReconcile_SiftTouchedResting(t *testing.T) {
	diff := []gitrepo.Entry{
		{Path: "auth/login.go", Mode: "100644", Size: 120},
		{Path: "auth/old.go", Deleted: true},
		{Path: "jobs/new.go", Mode: "100644", Size: 40},
		{Path: "go.sum", Mode: "100644", Size: 900},
		{Path: "link", Mode: "120000", Size: 3},
		{Path: "billing/huge.go", Mode: "100644", Size: FileMaxBytes + 1},
	}
	c := Sift(diff)
	if len(c.Changed) != 2 || !reflect.DeepEqual(c.Gone, []string{"auth/old.go", "go.sum", "link", "billing/huge.go"}) {
		t.Fatalf("Sift = %+v", c)
	}
	plan := Plan{Subjects: []Subject{
		{Name: "Auth", Files: []File{{"auth/old.go", 50}, {"auth/session.go", 400}}},
		{Name: "Billing", Files: []File{{"billing/run.go", 300}, {"billing/huge.go", 90}}},
		{Name: "Docs", Files: []File{{"README.md", 30}}},
		{Name: "Quiet", Files: []File{{"quiet.go", 10}}},
	}, Skipped: []string{"Docs"}}
	touched, unplaced := Touched(plan, c)
	if !reflect.DeepEqual(touched, []string{"Auth", "Billing", "Docs"}) {
		t.Errorf("touched = %v, want Auth (deleted), Billing (grown past the cap) and Docs (skipped last time)", touched)
	}
	if len(unplaced) != 2 || unplaced[0].Path != "auth/login.go" || unplaced[1].Path != "jobs/new.go" {
		t.Errorf("unplaced = %v", unplaced)
	}
	// 160 changed plus 140 catalogued gone, of 880.
	if FullInstead(plan, c) {
		t.Error("300 of 880 read as a full run")
	}
	if !FullInstead(plan, Changes{Changed: []File{{"x", 300}}, Gone: []string{"auth/session.go"}}) || !FullInstead(Plan{}, c) {
		t.Error("over half moved counting a deletion, or an empty plan, did not ask for a full run")
	}
	statements := []store.Statement{
		{ID: "s1", Files: []string{"auth/session.go"}},
		{ID: "s2", Files: []string{"billing/run.go", "auth/old.go"}},
		{ID: "s3", Files: []string{}},
		{ID: "s4", Files: []string{"jobs/new.go"}},
	}
	if got := Resting(statements, c.Paths()); !reflect.DeepEqual(got, []string{"s2", "s4"}) {
		t.Errorf("Resting = %v", got)
	}
}

func id(s string) *string { return &s }

func page() []store.Statement {
	pr := []store.Source{{PRKey: "PRJ/svc#1"}}
	base := []store.Source{{Kind: store.FromBaseline, Commit: "c0ffee"}}
	return []store.Statement{
		{ID: "s1", Kind: "component", Name: "Auth", Text: "Login.", Files: []string{"auth/login.go"}, Sources: pr},
		{ID: "s2", Kind: "rule", Name: "Retry", Text: "Thrice.", Files: []string{"jobs/retry.go"}, Sources: pr},
		{ID: "s3", Kind: "entity", Name: "Invoice", Text: "A bill.", Files: []string{"billing/invoice.go"}, Sources: base},
		{ID: "s4", Kind: "invariant", Name: "Once", Text: "Never twice.", Files: []string{}, Sources: pr},
	}
}

// inTree is Exists over a tree holding these files.
func inTree(paths ...string) func(string) bool {
	entries := make([]gitrepo.Entry, 0, len(paths))
	for _, p := range paths {
		entries = append(entries, gitrepo.Entry{Path: p, Mode: "100644", Size: 1})
	}
	return Exists(entries)
}

func TestExists_ForgivesSpellingKnowsDirectories(t *testing.T) {
	exists := Exists([]gitrepo.Entry{{Path: "internal/store/wiki.go"}, {Path: "go.sum"}, {Path: "old.go", Deleted: true}})
	for p, want := range map[string]bool{
		"internal/store/wiki.go": true, "./internal/store/wiki.go": true, "/internal/store/wiki.go": true,
		"internal/store/": true, "internal": true, " go.sum ": true,
		"internal/stor": false, "old.go": false, "": false, "./": false,
	} {
		if got := exists(p); got != want {
			t.Errorf("exists(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestApplyPatch_EditsDropsConfirmsAppends(t *testing.T) {
	summary := "Neu."
	p := inference.Patch{
		Summary:    &summary,
		Statements: []inference.Statement{{ID: id("s2"), Kind: "rule", Name: "Retry", Text: "Five times.", Files: []string{"jobs/retry.go"}}},
		// A new statement carries no id: one the answer holds is cleared.
		Added: []inference.Statement{{ID: id("s9"), Kind: "component", Name: "Jobs", Text: "Runs jobs."}},
		// s3 rests on the baseline alone; s2's PR file is gone, but s2 is
		// edited, not dropped.
		Drop:      []string{"s3"},
		Confirmed: []string{"s1"},
	}
	a := ApplyPatch(page(), p, inTree("auth/login.go", "jobs/retry.go", "billing/invoice.go"), anyKind)
	var got []string
	for i, st := range a.Statements {
		sid := "new"
		if st.ID != nil {
			sid = *st.ID
		}
		got = append(got, fmt.Sprintf("%s:%s:%v", sid, st.Text, a.Checked[i]))
	}
	want := []string{"s1:Login.:true", "s2:Five times.:true", "s4:Never twice.:false", "new:Runs jobs.:true"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("applied = %v\nwant %v", got, want)
	}
	if a.Summary == nil || *a.Summary != "Neu." {
		t.Errorf("Summary = %v", a.Summary)
	}
	blank := "  "
	if a := ApplyPatch(page(), inference.Patch{Summary: &blank}, inTree(), anyKind); a.Summary != nil || len(a.Statements) != 4 {
		t.Errorf("blank summary, empty patch = %+v, want the page kept whole", a)
	}
}

// anyKind takes every statement: the kind rules are consolidate's.
func anyKind(inference.Statement) string { return "" }

// A statement valid refuses costs only itself: an edit is left out, the
// statement as stored and unchecked, a drop of it beside the edit left out
// too (two answers keep it); a new one is left out.
func TestApplyPatch_InvalidItemsLeftOut(t *testing.T) {
	noTutorials := func(s inference.Statement) string {
		if s.Kind == "tutorial" {
			return "kind tutorial"
		}
		return ""
	}
	a := ApplyPatch(page(), inference.Patch{
		Statements: []inference.Statement{{ID: id("s3"), Kind: "tutorial", Name: "Anders", Text: "x"}},
		Added:      []inference.Statement{{Kind: "tutorial", Name: "Neu", Text: "x"}, {Kind: "rule", Name: "Gut", Text: "y"}},
		Drop:       []string{"s3"},
	}, inTree("auth/login.go", "jobs/retry.go", "billing/invoice.go"), noTutorials)
	if !reflect.DeepEqual(a.BadEdits, []string{"s3 (kind tutorial), kept as stored"}) ||
		!reflect.DeepEqual(a.BadNew, []string{`"Neu" (kind tutorial)`}) ||
		!reflect.DeepEqual(a.BadDrops, []string{"s3 (an edit of it left out too, kept as stored)"}) {
		t.Fatalf("left out: edits %q new %q drops %q", a.BadEdits, a.BadNew, a.BadDrops)
	}
	if len(a.Statements) != 5 || a.Statements[2].Name != "Invoice" || a.Checked[2] || a.Statements[4].Name != "Gut" {
		t.Errorf("statements = %+v checked %v; want s3 as stored and unchecked, Gut added", a.Statements, a.Checked)
	}
}

// What a PR taught goes only when the code shows its files gone. A patch is
// never refused whole: each item that breaks a rule is left out and named,
// the statement as stored, the rest of the patch applied (user,
// 2026-11-01).
func TestApplyPatch_ItemsCostOnlyThemselves(t *testing.T) {
	everything := inTree("auth/login.go", "jobs/retry.go", "billing/invoice.go")
	// names is the page's statements as id:name, in order.
	names := func(a Applied) []string {
		var out []string
		for _, s := range a.Statements {
			sid := "new"
			if s.ID != nil {
				sid = *s.ID
			}
			out = append(out, sid+":"+s.Name)
		}
		return out
	}
	stored := []string{"s1:Auth", "s2:Retry", "s3:Invoice", "s4:Once"}
	for name, c := range map[string]struct {
		p      inference.Patch
		exists func(string) bool
		edits  []string
		drops  []string
		want   []string
	}{
		"edit without an id":    {inference.Patch{Statements: []inference.Statement{{Name: "x", Text: "y"}}}, everything, []string{`"x" (no id)`}, nil, stored},
		"edit with an empty id": {inference.Patch{Statements: []inference.Statement{{ID: id(""), Name: "x", Text: "y"}}}, everything, []string{`"x" (no id)`}, nil, stored},
		"edited and dropped": {inference.Patch{Statements: []inference.Statement{{ID: id("s3"), Name: "Neu"}}, Drop: []string{"s3"}}, everything, nil,
			[]string{"s3 (edited too, the edit taken)"}, []string{"s1:Auth", "s2:Retry", "s3:Neu", "s4:Once"}},
		"PR fact, files there": {inference.Patch{Drop: []string{"s1"}}, everything, nil, []string{"s1 (taught by a pull request, its files still exist)"}, stored},
		"PR fact, no files":    {inference.Patch{Drop: []string{"s4"}}, inTree(), nil, []string{"s4 (taught by a pull request, its files still exist)"}, stored},
		"confirmed and gone":   {inference.Patch{Drop: []string{"s3"}, Confirmed: []string{"s3"}}, everything, nil, []string{"s3 (confirmed too, kept)"}, stored},
		"PR fact edited off its file": {inference.Patch{Statements: []inference.Statement{{ID: id("s1"), Kind: "component", Name: "Moved",
			Text: "Moved.", Files: []string{"elsewhere.go"}}}}, everything,
			[]string{"s1 (taught by a pull request, drops its file auth/login.go the tree still has)"}, nil, stored},
	} {
		a := ApplyPatch(page(), c.p, c.exists, anyKind)
		if !reflect.DeepEqual(a.BadEdits, c.edits) || !reflect.DeepEqual(a.BadDrops, c.drops) || !reflect.DeepEqual(names(a), c.want) {
			t.Errorf("%s: edits %q drops %q statements %v; want %q %q %v", name, a.BadEdits, a.BadDrops, names(a), c.edits, c.drops, c.want)
		}
	}
	// A good item beside a bad one is taken.
	a := ApplyPatch(page(), inference.Patch{Statements: []inference.Statement{{Name: "ohne", Text: "y"},
		{ID: id("s3"), Kind: "rule", Name: "Rechnung", Text: "Monatlich."}}, Drop: []string{"s1", "s2"}}, inTree("auth/login.go"), anyKind)
	if want := []string{"s1:Auth", "s3:Rechnung", "s4:Once"}; !reflect.DeepEqual(names(a), want) {
		t.Errorf("good beside bad = %v, want %v", names(a), want)
	}
	if a := ApplyPatch(page(), inference.Patch{Drop: []string{"s1"}}, inTree("jobs/retry.go"), anyKind); len(a.Statements) != 3 {
		t.Errorf("PR fact whose files are gone: %d statements, want it dropped", len(a.Statements))
	}
	// An unknown id to drop or confirm refers to nothing: left out, named,
	// and the rest of the patch applies.
	a = ApplyPatch(page(), inference.Patch{Drop: []string{"s9", "s3"}, Confirmed: []string{"8b0d8f4a", "s1"}}, everything, anyKind)
	if !reflect.DeepEqual(a.Unknown, []string{"s9", "8b0d8f4a"}) || len(a.Statements) != 3 || !a.Checked[0] {
		t.Errorf("unknown drop and confirm ids: unknown %v, %d statements, checked %v", a.Unknown, len(a.Statements), a.Checked)
	}
	// An edit of an id the page lacks, or of one already edited, costs only
	// itself: the first edit of s3 counts.
	a = ApplyPatch(page(), inference.Patch{Statements: []inference.Statement{
		{ID: id("s9"), Name: "x", Text: "y"}, {ID: id("s3"), Name: "Erste", Text: "y"}, {ID: id("s3"), Name: "Zweite", Text: "y"}}}, everything, anyKind)
	if !reflect.DeepEqual(a.BadEdits, []string{"s9 (unknown id)", "s3 (id used twice)"}) || len(a.Statements) != 4 || a.Statements[2].Name != "Erste" {
		t.Errorf("bad edits: left out %v, statements %+v", a.BadEdits, a.Statements)
	}
	// An edit may add files and correct the text, keeping the ones the tree
	// has, however spelled; a file that left the tree may go.
	kept := inference.Patch{Statements: []inference.Statement{{ID: id("s1"), Kind: "component", Name: "Auth", Text: "Login, two steps.",
		Files: []string{"./auth/login.go", "auth/otp.go"}}}}
	if a := ApplyPatch(page(), kept, everything, anyKind); len(a.BadEdits) != 0 || a.Statements[0].Text != "Login, two steps." {
		t.Errorf("edit keeping the file: left out %v", a.BadEdits)
	}
	moved := inference.Patch{Statements: []inference.Statement{{ID: id("s2"), Kind: "rule", Name: "Retry", Text: "Moved.",
		Files: []string{"jobs/backoff.go"}}}}
	if a := ApplyPatch(page(), moved, inTree("auth/login.go"), anyKind); len(a.BadEdits) != 0 || a.Statements[1].Text != "Moved." {
		t.Errorf("edit away from a file that left the tree: left out %v", a.BadEdits)
	}
	// A statement whose file is named as its directory still rests on code.
	dir := page()
	dir[0].Files = []string{"auth/"}
	if a := ApplyPatch(dir, inference.Patch{Drop: []string{"s1"}}, everything, anyKind); len(a.BadDrops) != 1 || len(a.Statements) != 4 {
		t.Errorf("drop of a PR fact named by its directory: left out %v, %d statements", a.BadDrops, len(a.Statements))
	}
}
