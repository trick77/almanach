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
// in as the paragraphs it is, never rewritten into Markdown.

// markdownKinds is the export's order: what must not break and why first,
// where to change and the parts after. The headings are the UI's labels.
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
	if s := strings.TrimSpace(p.Summary); s != "" {
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
	fmt.Fprintf(b, "### %s\n\n%s\n\n", s.Name, strings.TrimSpace(s.Text))
	if len(s.Files) > 0 {
		files := make([]string, 0, len(s.Files))
		for _, f := range s.Files {
			files = append(files, "`"+f+"`")
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

// writeJSONBlock writes a diagram as the fenced JSON it is stored as.
func writeJSONBlock(b *strings.Builder, d *diagram.Diagram) {
	j, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return
	}
	b.WriteString("```json\n" + string(j) + "\n```\n\n")
}
