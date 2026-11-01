package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/trick77/almanach/internal/diagram"
	"github.com/trick77/almanach/internal/markup"
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

// projectEntityHeading is the entity kind's heading on a project's export:
// there the kind holds the entities that cross repositories, their system
// of record and how their ids travel. The UI's label (strings.ts
// S.projectKinds), held to it by a test.
const projectEntityHeading = "Geteilte Daten"

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
	writeMarkdown(w, renderMarkdown("Projekt "+key, store.ScopeProject, page, found))
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
	writeMarkdown(w, renderMarkdown("Repository "+slug+" (Projekt "+key+")", store.ScopeRepository, page, found))
}

func writeMarkdown(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", markdownType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(body))
}

// renderMarkdown is a page's current revision as Markdown: the title, the
// revision, the summary, the page's map (the project's system map, a
// repository's Aufbau), then the statements grouped by kind in
// markdownKinds' order, each with its files, its PR keys and its diagram.
// A kind without statements has no heading; a project's entities are
// headed projectEntityHeading. A page without a revision yet says so under
// its title.
func renderMarkdown(title, scope string, p store.Page, found bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", title)
	if !found {
		b.WriteString("_Noch keine Beschreibung._\n")
		return b.String()
	}
	fmt.Fprintf(&b, "Revision %d, Stand %s.\n\n", p.Revision, p.UpdatedAt.UTC().Format("2006-01-02"))
	if s := prose(p.Summary, false); s != "" {
		b.WriteString(s + "\n\n")
	}
	// The sentinel is no map: asked for, not drawn.
	if p.Map != nil && !diagram.IsSentinel(p.Map) {
		b.WriteString("## Systemkarte\n\n")
		writeJSONBlock(&b, p.Map)
	}
	if p.RepoMap != nil {
		b.WriteString("## Aufbau\n\n")
		writeJSONBlock(&b, p.RepoMap)
	}
	for _, k := range markdownKinds {
		heading := k.heading
		if k.kind == "entity" && scope == store.ScopeProject {
			heading = projectEntityHeading
		}
		var list []store.Statement
		for _, s := range p.Statements {
			if s.Kind == k.kind {
				list = append(list, s)
			}
		}
		if len(list) == 0 {
			continue
		}
		fmt.Fprintf(&b, "## %s\n\n", heading)
		for _, s := range list {
			writeStatement(&b, s)
		}
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

func writeStatement(b *strings.Builder, s store.Statement) {
	fmt.Fprintf(b, "### %s\n\n%s\n\n", strings.Join(strings.Fields(s.Name), " "), prose(s.Text, true))
	if len(s.Files) > 0 {
		files := make([]string, 0, len(s.Files))
		for _, f := range s.Files {
			files = append(files, markup.CodeSpan(f))
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
// fence longer than any run of backticks in a label. A label's < > & stay
// as written, never escaped for HTML: the reader is an agent.
func writeJSONBlock(b *strings.Builder, d *diagram.Diagram) {
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(d); err != nil {
		return
	}
	j := strings.TrimSuffix(buf.String(), "\n")
	fence := strings.Repeat("`", max(3, markup.LongestRun(j, '`')+1))
	b.WriteString(fence + "json\n" + j + "\n" + fence + "\n\n")
}

// prose is model-written text in its stored layout (package markup) and
// nothing more, never the export's structure. Each line loses its indent
// (no code block). A "- " or "N. " list item and, where headings is set (a
// statement's text), a "#### " sub-heading under the ### statement name
// stay as they are; any other line that would open a block as CommonMark
// reads it (a heading of another level, quote, fence, * or + item, rule,
// underline or HTML) gets a backslash before the marker: a backslashed
// ASCII punctuation mark reads as itself. Text that opens nothing, "-5" or
// "1.5 Sekunden", stays as written, for the agent reads the raw file.
func prose(s string, headings bool) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i, l := range lines {
		t := strings.TrimLeft(l, " \t")
		if _, ok := markup.StoredHeading(t); ok && headings {
			lines[i] = t
			continue
		}
		// An item's text is text again: "- # x" or "1. > y" opens nothing
		// inside the item either.
		if marker, text, ok := markup.StoredItem(t); ok {
			lines[i] = marker + escapeBlock(text)
			continue
		}
		lines[i] = escapeBlock(t)
	}
	return strings.Join(lines, "\n")
}

// escapeBlock is a line, its indent dropped, with a backslash before the
// marker of any block it would open.
func escapeBlock(t string) string {
	if blockStart(t) {
		return `\` + t
	}
	if n := len(t) - len(strings.TrimLeft(t, "0123456789")); n > 0 && n <= 9 && n < len(t) &&
		(t[n] == '.' || t[n] == ')') && markerEnds(t[n+1:]) {
		return t[:n] + `\` + t[n:]
	}
	return t
}

// blockStart reports whether a line, its indent dropped, opens a block
// with its first character.
func blockStart(t string) bool {
	if t == "" {
		return false
	}
	switch t[0] {
	case '>':
		return true
	case '#':
		n := len(t) - len(strings.TrimLeft(t, "#"))
		return n <= 6 && markerEnds(t[n:])
	case '`', '~':
		// markup's fence rule: a code span Normalize wrote around a line with
		// ``` in it opens no fence and keeps no backslash.
		return markup.FenceOf(t) != ""
	case '<':
		return len(t) > 1 && (t[1] >= 'a' && t[1] <= 'z' || t[1] >= 'A' && t[1] <= 'Z' || strings.ContainsRune("/!?", rune(t[1])))
	case '-', '+', '*':
		if markerEnds(t[1:]) {
			return true
		}
	}
	// A rule or a heading's underline: nothing but -, *, _ or = and blanks.
	return strings.Trim(t, "-*_= \t") == ""
}

// markerEnds reports whether what follows a block marker lets it open the
// block: nothing, or a blank.
func markerEnds(rest string) bool {
	return rest == "" || rest[0] == ' ' || rest[0] == '\t'
}
