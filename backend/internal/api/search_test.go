package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/trick77/almanach/internal/config"
	"github.com/trick77/almanach/internal/store"
)

func searchPages() []store.PageText {
	return []store.PageText{
		{Scope: store.ScopeProject, ProjectKey: "PAY", Summary: "Das Projekt Payments.\n\nEin Auftrag durchläuft Zustände.",
			Statements: []store.StatementText{{ID: "s1", Kind: "flow", Name: "Upload", Text: "Vom Auftrag zur Datei.", Area: "Aufträge"}}},
		{Scope: store.ScopeRepository, ProjectKey: "PAY", RepoSlug: "pay-api", Summary: "Der Zahlungsdienst.",
			Statements: []store.StatementText{
				{ID: "s1", Kind: "component", Name: "Auftragsannahme", Text: "#### Eingang\n\nNimmt **Aufträge** an, jeden Auftrag einzeln.\n\n- Der Mandant kommt aus der Kopfzeile.", Files: []string{"internal/orders/intake.go"}, Area: "Aufträge"},
				{ID: "s2", Kind: "rule", Name: "Gebühren", Text: "Die kleinere Grenze gilt.", Files: []string{"internal/fees/limit.go"}},
			}},
		{Scope: store.ScopeRepository, ProjectKey: "ALPHA", RepoSlug: "lib", Summary: "Eine Bibliothek.",
			Statements: []store.StatementText{{ID: "s1", Kind: "entity", Name: "Datei", Text: "Eine Datei im Eingang."}}},
	}
}

// The search folds case the Unicode way, answers the pages in the store's
// order with a snippet around the first hit of the text, the name alone
// when only name, area or file matched, and the summary's snippet when it
// matched; a short phrase answers no pages.
func TestSearch(t *testing.T) {
	st := newFakeStore()
	st.allPages = searchPages()
	h := handler(t, st, nil, config.AuthNone)
	for _, q := range []string{"", "%20", "a", "%20a%20"} {
		code, body := do(t, h, http.MethodGet, "/api/search?q="+q, "", "", nil)
		if code != 200 || string(body["pages"]) != "[]" || string(body["more"]) != "0" {
			t.Errorf("q=%q: %d %s", q, code, body["pages"])
		}
	}
	if len(st.sourcedAsked) != 0 {
		t.Errorf("a short phrase read the pages: %v", st.sourcedAsked)
	}

	code, body := do(t, h, http.MethodGet, "/api/search?q=auftrag", "", "", nil)
	if code != 200 || field(t, body, "query") != "auftrag" || string(body["more"]) != "0" {
		t.Fatalf("%d %v", code, body)
	}
	var pages []searchPageBody
	if err := json.Unmarshal(body["pages"], &pages); err != nil {
		t.Fatal(err)
	}
	if len(pages) != 2 {
		t.Fatalf("pages = %+v", pages)
	}
	if p := pages[0]; p.Scope != "project" || p.ProjectKey != "PAY" || p.RepoSlug != "" || p.Single ||
		p.Summary != "Das Projekt Payments. Ein Auftrag durchläuft Zustände." ||
		len(p.Statements) != 1 || p.Statements[0].Snippet != "Vom Auftrag zur Datei." || p.Statements[0].Area != "Aufträge" {
		t.Errorf("project page = %+v", p)
	}
	if p := pages[1]; p.Scope != "repository" || p.RepoSlug != "pay-api" || p.Summary != "" || len(p.Statements) != 1 ||
		p.Statements[0].ID != "s1" || p.Statements[0].Kind != "component" ||
		// The layout is dropped: no heading mark, no bold, no item dash.
		p.Statements[0].Snippet != "Eingang Nimmt Aufträge an, jeden Auftrag einzeln. Der Mandant kommt aus der Kopfzeile." {
		t.Errorf("repository page = %+v", p)
	}
	if len(st.sourcedAsked) != 1 || st.sourcedAsked[0] {
		t.Errorf("CurrentPages asked with sourced %v, want false without the sources", st.sourcedAsked)
	}

	// Umlauts fold: GEBÜHREN finds Gebühren, in the name alone (no
	// snippet); a file path matches too.
	_, body = do(t, h, http.MethodGet, "/api/search?q=GEB%C3%9CHREN", "", "", nil)
	if err := json.Unmarshal(body["pages"], &pages); err != nil {
		t.Fatal(err)
	}
	if len(pages) != 1 || len(pages[0].Statements) != 1 || pages[0].Statements[0].Name != "Gebühren" || pages[0].Statements[0].Snippet != "" {
		t.Errorf("umlaut search = %+v", pages)
	}
	_, body = do(t, h, http.MethodGet, "/api/search?q=fees%2Flimit", "", "", nil)
	if err := json.Unmarshal(body["pages"], &pages); err != nil {
		t.Fatal(err)
	}
	if len(pages) != 1 || len(pages[0].Statements) != 1 || pages[0].Statements[0].ID != "s2" || pages[0].Statements[0].File != "internal/fees/limit.go" {
		t.Errorf("file search = %+v", pages)
	}
	// Two spaces in the phrase find the text with one; the query is
	// answered collapsed. A phrase past the bound answers nothing.
	_, body = do(t, h, http.MethodGet, "/api/search?q=Auftrag%20%20zur", "", "", nil)
	if err := json.Unmarshal(body["pages"], &pages); err != nil {
		t.Fatal(err)
	}
	if field(t, body, "query") != "Auftrag zur" || len(pages) != 1 || pages[0].Statements[0].Snippet != "Vom Auftrag zur Datei." {
		t.Errorf("collapsed phrase = %q %+v", body["query"], pages)
	}
	_, body = do(t, h, http.MethodGet, "/api/search?q="+strings.Repeat("a", searchMaxRunes+1), "", "", nil)
	if string(body["pages"]) != "[]" {
		t.Errorf("phrase past the bound = %s", body["pages"])
	}
	_, body = do(t, h, http.MethodGet, "/api/search?q=nirgends", "", "", nil)
	if string(body["pages"]) != "[]" {
		t.Errorf("no hit = %s", body["pages"])
	}

	st.pagesErr = errors.New("boom")
	if code, _ := do(t, h, http.MethodGet, "/api/search?q=auftrag", "", "", nil); code != 500 {
		t.Errorf("store failure = %d, want 500", code)
	}
}

// With the sources on, a project of one has no project page in the
// answer and its repository's page says single; the store is asked for
// served pages; past searchMaxPages the rest are counted.
func TestSearch_SourcesAndBound(t *testing.T) {
	st := newFakeStore()
	st.allPages = searchPages()
	st.single = map[string]store.RepoRow{"PAY": {Slug: "pay-api"}}
	h, _, _ := sourcesHandlerWith(t, newFakeSources(), st, &fakeWaker{}, false)
	_, body := do(t, h, http.MethodGet, "/api/search?q=auftrag", "", "", nil)
	var pages []searchPageBody
	if err := json.Unmarshal(body["pages"], &pages); err != nil {
		t.Fatal(err)
	}
	if len(pages) != 1 || pages[0].Scope != "repository" || !pages[0].Single {
		t.Errorf("project of one = %+v", pages)
	}
	if len(st.sourcedAsked) != 1 || !st.sourcedAsked[0] {
		t.Errorf("CurrentPages asked with sourced %v, want true", st.sourcedAsked)
	}

	st.single = nil
	st.allPages = nil
	for i := 0; i < searchMaxPages+3; i++ {
		st.allPages = append(st.allPages, store.PageText{Scope: store.ScopeRepository, ProjectKey: "P", RepoSlug: fmt.Sprintf("r%03d", i), Summary: "Treffer hier."})
	}
	_, body = do(t, h, http.MethodGet, "/api/search?q=treffer", "", "", nil)
	if err := json.Unmarshal(body["pages"], &pages); err != nil {
		t.Fatal(err)
	}
	if len(pages) != searchMaxPages || string(body["more"]) != "3" || pages[0].RepoSlug != "r000" {
		t.Errorf("bound: %d pages, more %s, first %s", len(pages), body["more"], pages[0].RepoSlug)
	}
}

// A snippet is the plain text around the first hit, bounded, cut at the
// ends only with an ellipsis for what is left out.
func TestSnippet(t *testing.T) {
	q := func(s string) phrase { return phrase(strings.ToLower(s)) }
	long := strings.Repeat("Wort ", 60) + "Treffer " + strings.Repeat("Wort ", 60)
	s, ok := snippet(long, q("treffer"))
	if !ok || !strings.HasPrefix(s, "… ") || !strings.HasSuffix(s, " …") || !strings.Contains(s, "Treffer") || len([]rune(s)) > snippetRunes+4 {
		t.Errorf("long snippet = %q (%d runes)", s, len([]rune(s)))
	}
	if s, ok := snippet("Kurz und gut.", q("gut")); !ok || s != "Kurz und gut." {
		t.Errorf("short snippet = %q %v", s, ok)
	}
	if s, ok := snippet("Nichts.", q("x")); ok || s != "" {
		t.Errorf("no hit = %q %v", s, ok)
	}
	// The hit itself is never cut: at the text's end it is still whole.
	s, ok = snippet(strings.Repeat("Wort ", 60)+"Schlussläufer", q("schlussläufer"))
	if !ok || !strings.HasSuffix(s, "Schlussläufer") {
		t.Errorf("hit at the end = %q", s)
	}
	// A rune whose lower case is longer in bytes (Ⱥ, 2 bytes, to ⱥ, 3)
	// before the hit: the offset is counted in runes of the folded text,
	// so the window still holds the hit, and nothing panics.
	s, ok = snippet(strings.Repeat("Ⱥ", 10)+" Treffer", q("treffer"))
	if !ok || s != strings.Repeat("Ⱥ", 10)+" Treffer" {
		t.Errorf("snippet after a growing rune = %q %v", s, ok)
	}
	s, ok = snippet(strings.Repeat("K", 10)+" Treffer", q("treffer"))
	if !ok || !strings.HasSuffix(s, " Treffer") {
		t.Errorf("snippet after a shrinking rune = %q %v", s, ok)
	}
	// A phrase longer than the window's tail after the hit, up to the
	// longest searched, is still whole.
	for _, n := range []int{snippetRunes - 10, searchMaxRunes} {
		hit := strings.TrimSpace(strings.Repeat("satz ", n/5+1))[:n]
		s, ok = snippet(strings.Repeat("Wort ", 60)+hit+strings.Repeat(" Wort", 60), q(hit))
		if !ok || !strings.Contains(s, hit) {
			t.Errorf("long phrase (%d runes) cut: %q", n, s)
		}
	}
	// A hit near the end shows what comes before it, a full window.
	s, ok = snippet(strings.Repeat("Wort ", 60)+"Treffer am Ende.", q("treffer"))
	if !ok || len([]rune(s)) != snippetRunes+2 || !strings.HasSuffix(s, "Treffer am Ende.") {
		t.Errorf("hit near the end = %q (%d runes)", s, len([]rune(s)))
	}
	// Code spans lose their backticks, the padding of one holding a
	// backtick too; a run nothing closes stays as written.
	s, ok = snippet("Liest `config.yaml` und ``a ` b`` beim Start, `offen", q("config"))
	if !ok || s != "Liest config.yaml und a ` b beim Start, `offen" {
		t.Errorf("code spans = %q", s)
	}
}
