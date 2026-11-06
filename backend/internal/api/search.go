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

// phrase is the search's phrase, its spaces collapsed, folded once for
// every text to be matched against.
type phrase string

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
	p := phrase(strings.ToLower(q))
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
		if text, ok := snippet(s.Text, q); ok {
			hit.Snippet = text
		} else if !contains(s.Name, q) && !contains(s.Area, q) {
			// Not the name alone, which the page shows with the area
			// anyway: a file, or no hit.
			if hit.File = firstFile(s.Files, q); hit.File == "" {
				continue
			}
		}
		out.Statements = append(out.Statements, hit)
	}
	return out, out.Summary != "" || len(out.Statements) > 0
}

// snippet is the plain text around the phrase's first hit in a stored
// text, ok false when none: snippetRunes of it, or the hit whole when
// longer, the hit a third in, or further when the text ends sooner, an
// ellipsis for what is left out. The layout (headings, items, bolds,
// the code spans' backticks) is dropped first, as format.ts plain drops
// it for the page's own search: a search box shows words.
func snippet(text string, q phrase) (string, bool) {
	plain := strings.Join(strings.Fields(untick(markup.Plain(text))), " ")
	hit := runeIndex(plain, q)
	if hit < 0 {
		return "", false
	}
	runes := []rune(plain)
	n := utf8.RuneCountInString(string(q))
	width := max(snippetRunes, n)
	lead := min(snippetRunes/3, width-n)
	start := max(min(hit-lead, len(runes)-width), 0)
	end := min(start+width, len(runes))
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
	at := strings.Index(low, string(q))
	if at < 0 {
		return -1
	}
	return utf8.RuneCountInString(low[:at])
}

func contains(text string, q phrase) bool {
	return strings.Contains(strings.ToLower(text), string(q))
}

// untick is plain text without its code spans' backticks, line by line,
// as format.ts codeSpans cuts them (markup.SpanAt): a span's code with
// CommonMark's one blank of padding off, a run nothing closes left as
// written.
func untick(s string) string {
	if !strings.Contains(s, "`") {
		return s
	}
	lines := strings.Split(s, "\n")
	for n, l := range lines {
		var b strings.Builder
		for i := 0; i < len(l); {
			if l[i] != '`' {
				j := strings.IndexByte(l[i:], '`')
				if j < 0 {
					j = len(l) - i
				}
				b.WriteString(l[i : i+j])
				i += j
				continue
			}
			end, ok := markup.SpanAt(l, i)
			if !ok {
				b.WriteString(l[i:])
				break
			}
			tick := len(l[i:]) - len(strings.TrimLeft(l[i:], "`"))
			code := l[i+tick : end-tick]
			if len(code) > 2 && code[0] == ' ' && code[len(code)-1] == ' ' && strings.TrimSpace(code) != "" {
				code = code[1 : len(code)-1]
			}
			b.WriteString(code)
			i = end
		}
		lines[n] = b.String()
	}
	return strings.Join(lines, "\n")
}

// firstFile is the first file the phrase matches, "" for none.
func firstFile(files []string, q phrase) string {
	for _, f := range files {
		if contains(f, q) {
			return f
		}
	}
	return ""
}
