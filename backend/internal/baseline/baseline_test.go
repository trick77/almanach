package baseline

import (
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
		"backend/web/dist/app.js":          true,
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
}

func TestReconcile_SiftTouchedResting(t *testing.T) {
	diff := []gitrepo.Entry{
		{Path: "auth/login.go", Mode: "100644", Size: 120},
		{Path: "auth/old.go", Deleted: true},
		{Path: "jobs/new.go", Mode: "100644", Size: 40},
		{Path: "go.sum", Mode: "100644", Size: 900},
		{Path: "link", Mode: "120000", Size: 3},
	}
	c := Sift(diff)
	if len(c.Changed) != 2 || c.Deleted[0] != "auth/old.go" || c.Bytes() != 160 {
		t.Fatalf("Sift = %+v", c)
	}
	plan := Plan{Subjects: []Subject{
		{Name: "Auth", Files: []File{{"auth/old.go", 50}, {"auth/session.go", 400}}},
		{Name: "Billing", Files: []File{{"billing/run.go", 300}}},
		{Name: "Docs", Files: []File{{"README.md", 30}}},
	}, Skipped: []string{"Docs"}}
	touched, unplaced := Touched(plan, c)
	if !reflect.DeepEqual(touched, []int{0, 2}) {
		t.Errorf("touched = %v, want Auth (a deleted file) and Docs (skipped last time)", touched)
	}
	if len(unplaced) != 2 || unplaced[0].Path != "auth/login.go" || unplaced[1].Path != "jobs/new.go" {
		t.Errorf("unplaced = %v", unplaced)
	}
	if FullInstead(plan, c) {
		t.Error("160 changed of 780 catalogued read as a full run")
	}
	if !FullInstead(plan, Changes{Changed: []File{{"x", 391}}}) || !FullInstead(Plan{}, c) {
		t.Error("over half changed, or an empty plan, did not ask for a full run")
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

func inTree(paths ...string) func(string) bool {
	set := map[string]bool{}
	for _, p := range paths {
		set[p] = true
	}
	return func(p string) bool { return set[p] }
}

func TestApplyPatch_EditsDropsConfirmsAppends(t *testing.T) {
	summary := "Neu."
	p := inference.Patch{
		Summary:    &summary,
		Statements: []inference.Statement{{ID: id("s2"), Kind: "rule", Name: "Retry", Text: "Five times.", Files: []string{"jobs/retry.go"}}, {Kind: "component", Name: "Jobs", Text: "Runs jobs."}},
		// s3 rests on the baseline alone; s2's PR file is gone, but s2 is
		// edited, not dropped.
		Drop:      []string{"s3"},
		Confirmed: []string{"s1"},
	}
	a, reason := ApplyPatch(page(), p, inTree("auth/login.go", "jobs/retry.go", "billing/invoice.go"))
	if reason != "" {
		t.Fatal(reason)
	}
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
	if a, _ := ApplyPatch(page(), inference.Patch{Summary: &blank}, inTree()); a.Summary != nil || len(a.Statements) != 4 {
		t.Errorf("blank summary, empty patch = %+v, want the page kept whole", a)
	}
}

// What a PR taught goes only when the code shows its files gone; every
// other wrong answer rejects the patch whole.
func TestApplyPatch_Rejections(t *testing.T) {
	everything := inTree("auth/login.go", "jobs/retry.go", "billing/invoice.go")
	for name, c := range map[string]struct {
		p      inference.Patch
		exists func(string) bool
		reason string
	}{
		"invented id":          {inference.Patch{Statements: []inference.Statement{{ID: id("s9"), Name: "x", Text: "y"}}}, everything, "unknown statement id s9"},
		"edited twice":         {inference.Patch{Statements: []inference.Statement{{ID: id("s1")}, {ID: id("s1")}}}, everything, "used twice s1"},
		"drop of an invented":  {inference.Patch{Drop: []string{"s9"}}, everything, "drop of unknown statement id s9"},
		"edited and dropped":   {inference.Patch{Statements: []inference.Statement{{ID: id("s3")}}, Drop: []string{"s3"}}, everything, "both edited and dropped"},
		"PR fact, files there": {inference.Patch{Drop: []string{"s1"}}, everything, "drop of s1, which a pull request taught"},
		"PR fact, no files":    {inference.Patch{Drop: []string{"s4"}}, inTree(), "drop of s4"},
		"confirm invented":     {inference.Patch{Confirmed: []string{"s7"}}, everything, "confirmation of unknown statement id s7"},
		"confirmed and gone":   {inference.Patch{Drop: []string{"s3"}, Confirmed: []string{"s3"}}, everything, "both confirmed and dropped"},
	} {
		if _, reason := ApplyPatch(page(), c.p, c.exists); !strings.Contains(reason, c.reason) {
			t.Errorf("%s: reason = %q, want %q", name, reason, c.reason)
		}
	}
	a, reason := ApplyPatch(page(), inference.Patch{Drop: []string{"s1"}}, inTree("jobs/retry.go"))
	if reason != "" || len(a.Statements) != 3 {
		t.Errorf("PR fact whose files are gone: reason %q, %d statements, want it dropped", reason, len(a.Statements))
	}
}
