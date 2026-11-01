package api

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/trick77/almanach/internal/auth"
	"github.com/trick77/almanach/internal/config"
	"github.com/trick77/almanach/internal/diagram"
	"github.com/trick77/almanach/internal/httpapi"
	"github.com/trick77/almanach/internal/inference"
	"github.com/trick77/almanach/internal/store"
	kitauth "github.com/trick77/ship/kit/auth"
)

// exportStore is a project PAY with one repository, both pages written:
// every kind once on the repository's, out of the export's order.
func exportStore() *fakeStore {
	st := newFakeStore()
	st.teams["PAY"] = "platform"
	st.repos["PAY"] = []store.RepoRow{{Slug: "pay-api"}, {Slug: "pay-docs"}}
	pr := []store.Source{{PRKey: "PAY/pay-api#12"}, {Kind: store.FromBaseline, Commit: "c0ffee"}, {PRKey: "PAY/pay-api#40"}}
	flow := &diagram.Diagram{Kind: diagram.KindFlow, Direction: "LR", Nodes: []diagram.Node{{ID: "a", Label: "A", Shape: "step"}}}
	st.pages[store.ScopeRepository+"|PAY|pay-api"] = store.Page{RevisionID: 9, Revision: 4, UpdatedAt: mergedAt,
		Summary: "Die API nimmt Zahlungen an.\n\nArbeiten daran: make check.",
		Statements: []store.Statement{
			{ID: "s1", Kind: "entity", Name: "Zahlung", Text: "Ein Auftrag.", Files: []string{"pay.go"}},
			{ID: "s2", Kind: "component", Name: "Annahme", Text: "Nimmt an.\n\nPrüft.", Files: []string{"intake.go"}, Diagram: flow},
			{ID: "s3", Kind: "extension", Name: "Neue Route", Text: "api.go, dann openapi.yaml.", Files: []string{"api.go", "openapi.yaml"}},
			{ID: "s4", Kind: "pitfall", Name: "Doppelte Buchung", Text: "Brach ohne Sperre.", Files: []string{"book.go"}, Sources: pr},
			{ID: "s5", Kind: "decision", Name: "Ein Worker", Text: "Seriell, weil.", Files: []string{"worker.go"}},
			{ID: "s6", Kind: "rule", Name: "Limit", Text: "Höchstens 10.", Files: nil},
			{ID: "s7", Kind: "invariant", Name: "Einmal", Text: "Nie zweimal.", Files: []string{"book.go"}},
		},
		RepoMap: &diagram.Diagram{Kind: diagram.KindFlow, Direction: "LR", Nodes: []diagram.Node{{ID: "api", Label: "HTTP-API", Shape: "step", Zone: "entry"}},
			Zones: []diagram.Zone{{ID: "entry", Title: "Eingang"}}}}
	st.pages[store.ScopeProject+"|PAY|"] = store.Page{RevisionID: 3, Revision: 2, UpdatedAt: mergedAt, Summary: "Das Projekt.",
		Map: &diagram.Diagram{Kind: diagram.KindFlow, Direction: "LR", Nodes: []diagram.Node{{ID: "pay-api", Label: "pay-api", Shape: "step", Zone: "repositories"}},
			Zones: []diagram.Zone{{ID: "repositories", Title: "Repositories"}}},
		Statements: []store.Statement{{ID: "s1", Kind: "pitfall", Name: "Quer", Text: "Brach.", Files: []string{"x.go"}},
			{ID: "s2", Kind: "entity", Name: "Zahlung", Text: "Gehört pay-api; die anderen sehen die ID.", Files: []string{"pay.go"}}}}
	return st
}

// oidcExport is the API in oidc mode over st, its log, the access log's
// lines included, written to buf.
func oidcExport(t *testing.T, st *fakeStore, sessions *kitauth.Sessions, buf *bytes.Buffer) http.Handler {
	t.Helper()
	log := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	srv := httpapi.New(func(context.Context) error { return nil }, log)
	Register(srv, Deps{Store: st, Keys: keysFile(t), Worker: &fakeWaker{}, AuthMode: config.AuthOIDC, ProblemWindow: 90,
		OIDC: &fakeOIDC{}, Sessions: sessions, RedirectURL: "https://almanach.example.com/api/auth/callback", Log: log})
	return srv.Handler()
}

func getMD(h http.Handler, path, bearer string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// The repository's export: the frame in order, the kinds in the export's
// order whatever the page's, files and PR keys under each statement, the
// statement's paragraphs as they are, its diagram fenced.
func TestRepoMarkdown(t *testing.T) {
	h := handler(t, exportStore(), &fakeWaker{}, config.AuthNone)
	rec := getMD(h, "/api/projects/PAY/repos/pay-api.md", "")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "text/markdown; charset=utf-8" {
		t.Fatalf("export = %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	md := rec.Body.String()
	order := []string{"# Repository pay-api (Projekt PAY)\n", "Revision 4, Stand 2026-10-20.", "Die API nimmt Zahlungen an.\n\nArbeiten daran: make check.",
		"## Aufbau\n\n```json\n{", `"zone": "entry"`, "## Invarianten", "### Einmal", "## Regeln", "### Limit", "## Entscheidungen", "### Ein Worker", "## Fallstricke", "### Doppelte Buchung",
		"## Erweiterungen", "### Neue Route", "## Komponenten", "### Annahme", "Nimmt an.\n\nPrüft.", "```json\n{", "## Entitäten", "### Zahlung"}
	at := 0
	for _, want := range order {
		i := strings.Index(md[at:], want)
		if i < 0 {
			t.Fatalf("export lacks %q after byte %d:\n%s", want, at, md)
		}
		at += i + len(want)
	}
	for _, want := range []string{"Dateien: `api.go`, `openapi.yaml`\n", "Dateien: `book.go`\nPRs: PAY/pay-api#12, PAY/pay-api#40\n"} {
		if !strings.Contains(md, want) {
			t.Errorf("export lacks %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, "c0ffee") || strings.Contains(md, "Systemkarte") {
		t.Errorf("export shows a baseline commit or a map on a repository page:\n%s", md)
	}
	// A page with no revision yet says so; an unknown repository is a 404.
	if rec := getMD(h, "/api/projects/PAY/repos/pay-docs.md", ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "_Noch keine Beschreibung._") {
		t.Errorf("export before the first revision = %d %q", rec.Code, rec.Body.String())
	}
	if rec := getMD(h, "/api/projects/PAY/repos/nope.md", ""); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "unknown repository") {
		t.Errorf("unknown repository = %d %q", rec.Code, rec.Body.String())
	}
	// The JSON route beside it is untouched.
	if rec := getMD(h, "/api/projects/PAY/repos/pay-api", ""); rec.Code != http.StatusOK || !strings.HasPrefix(rec.Body.String(), "{") {
		t.Errorf("JSON route = %d", rec.Code)
	}
}

// The project's export carries its map fenced, then its statements; no
// heading for a kind it lacks.
func TestProjectMarkdown(t *testing.T) {
	h := handler(t, exportStore(), &fakeWaker{}, config.AuthNone)
	rec := getMD(h, "/api/projects/PAY.md", "")
	md := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.HasPrefix(md, "# Projekt PAY\n\nRevision 2, Stand 2026-10-20.\n\nDas Projekt.\n\n## Systemkarte\n\n```json\n") {
		t.Fatalf("project export = %d:\n%s", rec.Code, md)
	}
	if !strings.Contains(md, "## Fallstricke\n\n### Quer\n\nBrach.\n\nDateien: `x.go`\n") || strings.Contains(md, "## Regeln") {
		t.Errorf("project statements:\n%s", md)
	}
	// A project's entities are the data its repositories share.
	if !strings.Contains(md, "## Geteilte Daten\n\n### Zahlung") || strings.Contains(md, "## Entitäten") || strings.Contains(md, "## Aufbau") {
		t.Errorf("project entities or a repository map:\n%s", md)
	}
	// A project leads with its parts: the shared data before the pitfall
	// stored ahead of it.
	if strings.Index(md, "## Geteilte Daten") > strings.Index(md, "## Fallstricke") {
		t.Errorf("project kinds out of order:\n%s", md)
	}
	if rec := getMD(h, "/api/projects/NOPE.md", ""); rec.Code != http.StatusNotFound {
		t.Errorf("unknown project = %d", rec.Code)
	}
	st := exportStore()
	page := st.pages[store.ScopeProject+"|PAY|"]
	page.Map = diagram.Sentinel()
	st.pages[store.ScopeProject+"|PAY|"] = page
	if md := getMD(handler(t, st, &fakeWaker{}, config.AuthNone), "/api/projects/PAY.md", "").Body.String(); strings.Contains(md, "Systemkarte") {
		t.Errorf("the sentinel drawn as a map:\n%s", md)
	}
}

// In oidc mode a team's bearer opens the two exports and nothing else; no
// credential and a wrong bearer get the 401, a session reads them too.
func TestMarkdownBearerInOIDC(t *testing.T) {
	var buf bytes.Buffer
	s := testSessions(t)
	h := oidcExport(t, exportStore(), s, &buf)
	for _, p := range []string{"/api/projects/PAY.md", "/api/projects/PAY/repos/pay-api.md"} {
		if rec := getMD(h, p, "tok-billing"); rec.Code != http.StatusOK || !strings.HasPrefix(rec.Body.String(), "# ") {
			t.Errorf("%s with a team bearer = %d %q", p, rec.Code, rec.Body.String())
		}
		for _, bearer := range []string{"", "tok-wrong"} {
			if rec := getMD(h, p, bearer); rec.Code != http.StatusUnauthorized {
				t.Errorf("%s with bearer %q = %d, want 401", p, bearer, rec.Code)
			}
		}
	}
	for _, p := range []string{"/api/projects/PAY", "/api/projects/PAY/repos/pay-api", "/api/projects", "/api/me", "/api/sources"} {
		if rec := getMD(h, p, "tok-platform"); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s with a team bearer = %d, want 401", p, rec.Code)
		}
	}
	if !strings.Contains(buf.String(), "team=billing") {
		t.Errorf("the access log lacks the team:\n%s", buf.String())
	}
	ck := sessionFor(t, s, auth.Claims{Subject: "sub-1", Email: "jan@example.com"})
	if rec := getMD(h, "/api/projects/PAY/repos/pay-api.md", "", ck); rec.Code != http.StatusOK {
		t.Errorf("export with a session = %d", rec.Code)
	}
}

// Model text never becomes the export's structure beyond its stored layout:
// a "- " or "N. " item and a statement's "#### " stay; a heading of another
// level, a #### in a summary, a quote, fence, * item, 1) item or underline
// line is escaped, a fence in a diagram label outlasted, a backtick in a
// path kept inside its code span.
func TestMarkdownEscapesModelText(t *testing.T) {
	label := &diagram.Diagram{Kind: diagram.KindFlow, Direction: "LR", Nodes: []diagram.Node{{ID: "a", Label: "x ```` y < z", Shape: "step"}}}
	md := renderMarkdown("T", store.ScopeRepository, store.Page{Revision: 1, UpdatedAt: mergedAt, Summary: "# Kein Titel\n#### Auch keiner\n- Punkt",
		Statements: []store.Statement{{Kind: "rule", Name: "Zwei\nZeilen",
			Text:  "Satz.\n## Regeln\n#### Ablauf\n```\n> zitiert\n---\n\n    eingerückt\n***\n1. Punkt\n1) Klammer\n* Stern\n<div>\n- Liste\n2026 kam\n1.5 Sekunden\n-5 ist der Default\n#3 offen\n< 10",
			Files: []string{"a`b.go"}, Diagram: label}}}, true)
	for _, want := range []string{"\\# Kein Titel\n\\#### Auch keiner\n- Punkt\n", "### Zwei Zeilen\n",
		"Satz.\n\\## Regeln\n#### Ablauf\n\\```\n\\> zitiert\n\\---\n\neingerückt\n\\***\n1. Punkt\n1\\) Klammer\n\\* Stern\n\\<div>\n- Liste\n2026 kam\n1.5 Sekunden\n-5 ist der Default\n#3 offen\n< 10\n",
		"Dateien: `` a`b.go ``\n", "`````json\n", `"label": "x ` + "````" + ` y < z"`} {
		if !strings.Contains(md, want) {
			t.Errorf("export lacks %q:\n%s", want, md)
		}
	}
	if strings.Count(md, "\n## ") != 1 {
		t.Errorf("model text opened a heading:\n%s", md)
	}
}

// A list item's text opens no block inside the item.
func TestMarkdownEscapesItemText(t *testing.T) {
	md := renderMarkdown("T", store.ScopeRepository, store.Page{Revision: 1, UpdatedAt: mergedAt,
		Statements: []store.Statement{{Kind: "rule", Name: "R", Text: "- # Titel\n- > Zitat\n1. ```go\n2. - -"}}}, true)
	if want := "- \\# Titel\n- \\> Zitat\n1. \\```go\n2. \\- -\n"; !strings.Contains(md, want) {
		t.Errorf("export lacks %q:\n%s", want, md)
	}
}

// The export's kinds are the UI's: the same order as Description.tsx's
// KINDS, each heading the label strings.ts gives the kind.
func TestMarkdownKindsMatchTheUI(t *testing.T) {
	read := func(p string) string {
		b, err := os.ReadFile(filepath.Join("..", "..", "..", "ui", "src", p))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	desc, copyTS := read("Description.tsx"), read("strings.ts")
	uiList := func(name string) []string {
		_, list, ok := strings.Cut(desc, "const "+name+": Kind[] = [")
		if !ok {
			t.Fatalf("Description.tsx lacks %s", name)
		}
		list, _, _ = strings.Cut(list, "]")
		var out []string
		for _, f := range strings.Split(list, ",") {
			if f = strings.Trim(strings.TrimSpace(f), `"`); f != "" {
				out = append(out, f)
			}
		}
		return out
	}
	ui := uiList("KINDS")
	var project []string
	for _, k := range kindOrder(store.ScopeProject) {
		project = append(project, k.kind)
	}
	if want := uiList("PROJECT_KINDS"); !slices.Equal(project, want) || !slices.Equal(projectKindOrder, want) {
		t.Errorf("project export order %v, UI order %v", project, want)
	}
	if a, b := slices.Sorted(slices.Values(project)), slices.Sorted(slices.Values(inference.ProjectKinds)); !slices.Equal(a, b) {
		t.Errorf("project export kinds %v, schema kinds %v", a, b)
	}
	var got []string
	for _, k := range markdownKinds {
		got = append(got, k.kind)
		if !strings.Contains(copyTS, fmt.Sprintf("    %s: %q,\n", k.kind, k.heading)) {
			t.Errorf("strings.ts labels %s otherwise than %q", k.kind, k.heading)
		}
	}
	if !slices.Equal(got, ui) {
		t.Errorf("export order %v, UI order %v", got, ui)
	}
	// The project page's own label for the entity kind.
	if !strings.Contains(copyTS, fmt.Sprintf("    entity: %q,\n", projectEntityHeading)) {
		t.Errorf("strings.ts lacks the project's entity label %q", projectEntityHeading)
	}
	if a, b := slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values(inference.RepoKinds)); !slices.Equal(a, b) {
		t.Errorf("export kinds %v, schema kinds %v", a, b)
	}
}

// A key or slug that itself ends in .md keeps its JSON route; a bearer,
// opened for the export alone, still gets the export.
func TestMarkdownSuffixOfARealName(t *testing.T) {
	st := exportStore()
	st.teams["DOC.md"] = "platform"
	st.repos["PAY"] = append(st.repos["PAY"], store.RepoRow{Slug: "notes.md"})
	h := handler(t, st, &fakeWaker{}, config.AuthNone)
	for _, p := range []string{"/api/projects/PAY/repos/notes.md", "/api/projects/DOC.md"} {
		if rec := getMD(h, p, ""); rec.Code != http.StatusOK || !strings.HasPrefix(rec.Body.String(), "{") {
			t.Errorf("%s = %d %q, want its JSON", p, rec.Code, rec.Body.String())
		}
	}
	if rec := getMD(h, "/api/projects/PAY/repos/notes.md.md", ""); rec.Code != http.StatusOK || !strings.HasPrefix(rec.Body.String(), "# Repository notes.md") {
		t.Errorf("export of notes.md = %d %q", rec.Code, rec.Body.String())
	}
	var buf bytes.Buffer
	o := oidcExport(t, st, testSessions(t), &buf)
	if rec := getMD(o, "/api/projects/PAY/repos/notes.md", "tok-billing"); rec.Code != http.StatusNotFound || strings.HasPrefix(rec.Body.String(), "{\"project_key\"") {
		t.Errorf("bearer on notes.md = %d %q, want the export of notes, unknown", rec.Code, rec.Body.String())
	}
}
