package api

import (
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/trick77/almanach/internal/markup"
	"github.com/trick77/almanach/internal/store"
	"github.com/trick77/ship/kit/httpx"
)

// The search: one phrase over every served page's summary and statements
// (name, text, area, files), matched in Go. SQLite's LIKE and lower()
// fold ASCII alone, so "Gebühren" would not find "gebühren" there; the
// pages are few hundred at most and read in one query. No index to keep
// in step with the current revisions, nothing stored.

// searchMinRunes is the shortest phrase searched: one letter would mark
// every page.
const searchMinRunes = 2

// searchMaxPages bounds the pages answered; the rest are counted (more).
const searchMaxPages = 50

// snippetRunes is how much of a text surrounds the first hit.
const snippetRunes = 160

type searchBody struct {
	Query string           `json:"query"`
	Pages []searchPageBody `json:"pages"`
	// More is how many further pages matched past the first searchMaxPages.
	More int `json:"more"`
}

// searchPageBody is one page with hits: its names, whether the project is
// one of one (its repository's page is the project's, at its path), the
// summary's snippet when the summary matched, and the statements that
// did, in the page's order.
type searchPageBody struct {
	Scope      string          `json:"scope"`
	ProjectKey string          `json:"project_key"`
	RepoSlug   string          `json:"repo_slug,omitempty"`
	Single     bool            `json:"single"`
	Summary    string          `json:"summary,omitempty"`
	Statements []searchHitBody `json:"statements"`
}

// searchHitBody is one statement that matched: enough to name and link
// it, and a snippet of where it matched (the name alone when only the
// name, an area or a file did).
type searchHitBody struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Area    string `json:"area"`
	Snippet string `json:"snippet"`
}

// search answers GET /api/search?q=: the pages whose text holds the
// phrase, case folded. A phrase under searchMinRunes answers no pages,
// not an error: the box is typed into.
func (d Deps) search(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	d.reader(r)
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	out := searchBody{Query: q, Pages: []searchPageBody{}}
	if utf8.RuneCountInString(q) < searchMinRunes {
		httpx.WriteJSON(w, http.StatusOK, out)
		return
	}
	pages, err := d.Store.CurrentPages(ctx, d.Sources != nil)
	if err != nil {
		httpx.ServerError(w, r, err, "pages")
		return
	}
	// A project of one has no project page on the wire, and its
	// repository's page is at the project's path.
	single := map[string]store.RepoRow{}
	if d.Sources != nil {
		if single, err = d.Store.SingleRepos(ctx); err != nil {
			httpx.ServerError(w, r, err, "repos")
			return
		}
	}
	for _, p := range pages {
		_, alone := single[p.ProjectKey]
		if alone && p.Scope == store.ScopeProject {
			continue
		}
		hit, ok := searchPage(p, q)
		if !ok {
			continue
		}
		hit.Single = alone
		if len(out.Pages) == searchMaxPages {
			out.More++
			continue
		}
		out.Pages = append(out.Pages, hit)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// searchPage is a page's hits for the phrase, ok false for none.
func searchPage(p store.Page, q string) (searchPageBody, bool) {
	out := searchPageBody{Scope: p.Scope, ProjectKey: p.ProjectKey, RepoSlug: p.RepoSlug, Statements: []searchHitBody{}}
	if s, ok := snippet(p.Summary, q); ok {
		out.Summary = s
	}
	for _, s := range p.Statements {
		hit := searchHitBody{ID: s.ID, Kind: s.Kind, Name: s.Name, Area: s.Area}
		switch text, ok := snippet(s.Text, q); {
		case ok:
			hit.Snippet = text
		case contains(s.Name, q), contains(s.Area, q), containsAny(s.Files, q):
			// The name alone: the page shows name, area and files anyway.
		default:
			continue
		}
		out.Statements = append(out.Statements, hit)
	}
	return out, out.Summary != "" || len(out.Statements) > 0
}

// snippet is the plain text around the phrase's first hit in a stored
// text, ok false when none: up to snippetRunes, the hit near its middle,
// cut at the text's ends only, an ellipsis for what is left out. The
// layout (headings, items, bolds) is dropped first: a search box shows
// words.
func snippet(text, q string) (string, bool) {
	plain := strings.Join(strings.Fields(markup.Plain(text)), " ")
	at := index(plain, q)
	if at < 0 {
		return "", false
	}
	runes := []rune(plain)
	hit := utf8.RuneCountInString(plain[:at])
	start := max(hit-snippetRunes/3, 0)
	end := min(start+snippetRunes, len(runes))
	s := string(runes[start:end])
	if start > 0 {
		s = "… " + s
	}
	if end < len(runes) {
		s += " …"
	}
	return s, true
}

// index is the byte offset of the phrase in text, case folded the Unicode
// way (strings.ToLower, not SQLite's ASCII lower), -1 for none. The
// folded text keeps its byte lengths for the letters that matter here,
// so the offset holds for the original's; a letter whose case changes
// its length (rare, outside German) shifts a snippet's window, never
// its hit.
func index(text, q string) int {
	return strings.Index(strings.ToLower(text), strings.ToLower(q))
}

func contains(text, q string) bool { return index(text, q) >= 0 }

func containsAny(texts []string, q string) bool {
	for _, t := range texts {
		if contains(t, q) {
			return true
		}
	}
	return false
}
