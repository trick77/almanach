package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/trick77/almanach/internal/diagram"
	"github.com/trick77/almanach/internal/store"
	"github.com/trick77/ship/kit/httpx"
)

// The Markdown export: a page as one file an agent pulls into its context,
// the way it reads a repository's agent notes. Markdown is the frame only
// (headings, file and PR lines, fenced diagrams); a statement's text goes
// in as the paragraphs it is, never rewritten into Markdown, only a line
// that would read as structure escaped (prose).

// markdownKinds is the export's order: what must not break and why first,
// where to change and the parts after. Order and headings are the UI's
// (Description.tsx KINDS, strings.ts S.kinds), held to them by a test.
var markdownKinds = []struct{ kind, heading string }{
	{"invariant", "Invarianten"},
	{"rule", "Regeln"},
	{"decision", "Entscheidungen"},
	{"pitfall", "Fallstricke"},
	{"extension", "Erweiterungen"},
	{"component", "Komponenten"},
	{"entity", "Entitäten"},
}

const markdownType = "text/markdown; charset=utf-8"

// projectMarkdown serves GET /api/projects/{key}.md: 404 for an unknown
// project, as the JSON route.
func (d Deps) projectMarkdown(w http.ResponseWriter, r *http.Request, key string) {
	ctx := r.Context()
	_, ok, err := d.Store.ProjectExists(ctx, key, d.Sources != nil)
	if err != nil {
		httpx.ServerError(w, r, err, "project")
		return
	}
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "unknown project")
		return
	}
	page, found, err := d.Store.CurrentPage(ctx, store.ScopeProject, key, "")
	if err != nil {
		httpx.ServerError(w, r, err, "project page")
		return
	}
	writeMarkdown(w, renderMarkdown("Projekt "+key, page, found))
}

// repoMarkdown serves GET /api/projects/{key}/repos/{slug}.md: 404 for an
// unknown repository, as the JSON route.
func (d Deps) repoMarkdown(w http.ResponseWriter, r *http.Request, key, slug string) {
	ctx := r.Context()
	ok, err := d.Store.RepoExists(ctx, key, slug, d.Sources != nil)
	if err != nil {
		httpx.ServerError(w, r, err, "repo")
		return
	}
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "unknown repository")
		return
	}
	page, found, err := d.Store.CurrentPage(ctx, store.ScopeRepository, key, slug)
	if err != nil {
		httpx.ServerError(w, r, err, "repo page")
		return
	}
	writeMarkdown(w, renderMarkdown("Repository "+slug+" (Projekt "+key+")", page, found))
}

func writeMarkdown(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", markdownType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(body))
}

// renderMarkdown is a page's current revision as Markdown: the title, the
// revision, the summary, the project's map, then the statements grouped by
// kind in markdownKinds' order, each with its files, its PR keys and its
// diagram. A kind without statements has no heading. A page without a
// revision yet says so under its title.
func renderMarkdown(title string, p store.Page, found bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", title)
	if !found {
		b.WriteString("_Noch keine Beschreibung._\n")
		return b.String()
	}
	fmt.Fprintf(&b, "Revision %d, Stand %s.\n\n", p.Revision, p.UpdatedAt.UTC().Format("2006-01-02"))
	if s := prose(p.Summary); s != "" {
		b.WriteString(s + "\n\n")
	}
	// The sentinel is no map: asked for, not drawn.
	if p.Map != nil && !diagram.IsSentinel(p.Map) {
		b.WriteString("## Systemkarte\n\n")
		writeJSONBlock(&b, p.Map)
	}
	for _, k := range markdownKinds {
		var list []store.Statement
		for _, s := range p.Statements {
			if s.Kind == k.kind {
				list = append(list, s)
			}
		}
		if len(list) == 0 {
			continue
		}
		fmt.Fprintf(&b, "## %s\n\n", k.heading)
		for _, s := range list {
			writeStatement(&b, s)
		}
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

func writeStatement(b *strings.Builder, s store.Statement) {
	fmt.Fprintf(b, "### %s\n\n%s\n\n", strings.Join(strings.Fields(s.Name), " "), prose(s.Text))
	if len(s.Files) > 0 {
		files := make([]string, 0, len(s.Files))
		for _, f := range s.Files {
			files = append(files, codeSpan(f))
		}
		fmt.Fprintf(b, "Dateien: %s\n", strings.Join(files, ", "))
	}
	var prs []string
	for _, src := range s.Sources {
		if !src.IsBaseline() && src.PRKey != "" {
			prs = append(prs, src.PRKey)
		}
	}
	if len(prs) > 0 {
		fmt.Fprintf(b, "PRs: %s\n", strings.Join(prs, ", "))
	}
	if len(s.Files) > 0 || len(prs) > 0 {
		b.WriteString("\n")
	}
	if s.Diagram != nil {
		writeJSONBlock(b, s.Diagram)
	}
}

// writeJSONBlock writes a diagram as the fenced JSON it is stored as, the
// fence longer than any run of backticks in a label.
func writeJSONBlock(b *strings.Builder, d *diagram.Diagram) {
	j, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return
	}
	fence := strings.Repeat("`", max(3, longestRun(string(j), '`')+1))
	b.WriteString(fence + "json\n" + string(j) + "\n" + fence + "\n\n")
}

// prose is model-written text as text, never as the export's structure.
// Each line loses its indent (no code block), and one whose first character
// could open a block (a heading, quote, fence, list, rule, underline or
// HTML) gets a backslash before it, as does the dot or parenthesis after a
// leading number (no ordered list): a backslashed ASCII punctuation mark
// reads as itself.
func prose(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i, l := range lines {
		t := strings.TrimLeft(l, " \t")
		switch {
		case t != "" && strings.ContainsRune("#>`~-+*_=<|", rune(t[0])):
			t = `\` + t
		default:
			if n := len(t) - len(strings.TrimLeft(t, "0123456789")); n > 0 && n < len(t) && (t[n] == '.' || t[n] == ')') {
				t = t[:n] + `\` + t[n:]
			}
		}
		lines[i] = t
	}
	return strings.Join(lines, "\n")
}

// codeSpan is s as inline code, its delimiter longer than any run of
// backticks in s.
func codeSpan(s string) string {
	n := longestRun(s, '`')
	if n == 0 {
		return "`" + s + "`"
	}
	tick := strings.Repeat("`", n+1)
	return tick + " " + s + " " + tick
}

func longestRun(s string, c byte) int {
	best, run := 0, 0
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			run++
			best = max(best, run)
		} else {
			run = 0
		}
	}
	return best
}
