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
// every page. searchMaxRunes is the longest: a phrase past it is nobody's
// question, and every text would be folded against it.
const (
	searchMinRunes = 2
	searchMaxRunes = 200
)

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
// it, and where it matched: a snippet of its text, or the file that did
// when the text did not (the name and the area the page shows anyway).
type searchHitBody struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Area    string `json:"area"`
	Snippet string `json:"snippet"`
	File    string `json:"file,omitempty"`
}

// phrase is the search's phrase as typed, its spaces collapsed, and
// folded once for every text to be matched against.
type phrase struct {
	raw, low string
}

// search answers GET /api/search?q=: the pages whose text holds the
// phrase, case folded. A phrase under searchMinRunes, or past
// searchMaxRunes, answers no pages, not an error: the box is typed into.
func (d Deps) search(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	d.reader(r)
	// Spaces collapsed, as the texts' are on the way to a snippet: a
	// pasted phrase with two spaces finds the text with one.
	q := strings.Join(strings.Fields(r.URL.Query().Get("q")), " ")
	out := searchBody{Query: q, Pages: []searchPageBody{}}
	if n := utf8.RuneCountInString(q); n < searchMinRunes || n > searchMaxRunes {
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
	single, err := d.singles(ctx)
	if err != nil {
		httpx.ServerError(w, r, err, "repos")
		return
	}
	p := phrase{raw: q, low: strings.ToLower(q)}
	for _, page := range pages {
		_, alone := single[page.ProjectKey]
		if alone && page.Scope == store.ScopeProject {
			continue
		}
		hit, ok := searchPage(page, p)
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
func searchPage(p store.PageText, q phrase) (searchPageBody, bool) {
	out := searchPageBody{Scope: p.Scope, ProjectKey: p.ProjectKey, RepoSlug: p.RepoSlug, Statements: []searchHitBody{}}
	if s, ok := snippet(p.Summary, q); ok {
		out.Summary = s
	}
	for _, s := range p.Statements {
		hit := searchHitBody{ID: s.ID, Kind: s.Kind, Name: s.Name, Area: s.Area}
		switch text, ok := snippet(s.Text, q); {
		case ok:
			hit.Snippet = text
		case contains(s.Name, q), contains(s.Area, q):
			// The name alone: the page shows name and area anyway.
		case firstFile(s.Files, q) != "":
			hit.File = firstFile(s.Files, q)
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
func snippet(text string, q phrase) (string, bool) {
	plain := strings.Join(strings.Fields(markup.Plain(text)), " ")
	hit := runeIndex(plain, q)
	if hit < 0 {
		return "", false
	}
	runes := []rune(plain)
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

// runeIndex is the rune offset of the phrase's first hit in text, case
// folded the Unicode way (strings.ToLower, not SQLite's ASCII lower), -1
// for none. Counted in the folded text: ToLower maps rune to rune, so
// the rune offset holds for the original where a byte offset would not
// (a rune whose lower case is longer or shorter in bytes).
func runeIndex(text string, q phrase) int {
	low := strings.ToLower(text)
	at := strings.Index(low, q.low)
	if at < 0 {
		return -1
	}
	return utf8.RuneCountInString(low[:at])
}

func contains(text string, q phrase) bool { return strings.Contains(strings.ToLower(text), q.low) }

// firstFile is the first file the phrase matches, "" for none.
func firstFile(files []string, q phrase) string {
	for _, f := range files {
		if contains(f, q) {
			return f
		}
	}
	return ""
}
