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
// that would read as structure escaped (markup.Export).

// markdownKinds is the export's order: what must not break and why first
// (a public interface is what others rely on not breaking), where to
// change and the parts after. The export is the agent's view, by kind,
// an agent reading every invariant at once; the page is the reader's, in
// chapters by area. Headings are the kinds' plural names (strings.ts
// S.kinds), held to them by a test.
var markdownKinds = []struct{ kind, heading string }{
	{"invariant", "Invarianten"},
	{"interface", "Schnittstellen"},
	{"rule", "Regeln"},
	{"decision", "Entscheidungen"},
	{"pitfall", "Fallstricke"},
	{"extension", "Erweiterungen"},
	{"component", "Komponenten"},
	{"flow", "Abläufe"},
	{"entity", "Entitäten"},
}

// projectEntityHeading is the entity kind's heading on a project's export:
// there the kind holds the entities that cross repositories, their system
// of record and how their ids travel. The plural of the UI's label of one
// such statement (strings.ts S.projectStatementKind), held to it by a test.
const projectEntityHeading = "Geteilte Entitäten"

// summaryHeading heads the summary on the export, as the UI's label
// (strings.ts S.rail.summary) over it.
const summaryHeading = "Überblick"

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
	alone, err := d.single(ctx, key)
	if err != nil {
		httpx.ServerError(w, r, err, "project source")
		return
	}
	if alone {
		// A project of one: its repository's export is the project's.
		repos, err := d.Store.Repos(ctx, key, true)
		if err != nil {
			httpx.ServerError(w, r, err, "repos")
			return
		}
		if len(repos) == 1 {
			d.repoMarkdown(w, r, key, repos[0].Slug)
			return
		}
		// Its repositories changed between the two reads: no page to serve,
		// never the project page it no longer has.
		httpx.WriteError(w, http.StatusNotFound, "no project page: a project of one, export its repository")
		return
	}
	page, found, err := d.Store.CurrentPage(ctx, store.ScopeProject, key, "")
	if err != nil {
		httpx.ServerError(w, r, err, "project page")
		return
	}
	// The flows name repositories by the project's current key.
	ck, _, err := d.Store.ProjectKeyOf(ctx, key)
	if err != nil {
		httpx.ServerError(w, r, err, "project key")
		return
	}
	msgs, err := d.messages(ctx)
	if err != nil {
		httpx.ServerError(w, r, err, "messaging")
		return
	}
	writeMarkdown(w, renderMarkdown("Projekt "+key, store.ScopeProject, page, found)+markdownFlows(projectFlows(msgs, ck)))
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
	// The graph names repositories by their current names.
	ck, cs, _, err := d.Store.RepoNamesOf(ctx, key, slug)
	if err != nil {
		httpx.ServerError(w, r, err, "repo names")
		return
	}
	g, err := d.graph(ctx, false)
	if err != nil {
		httpx.ServerError(w, r, err, "dependencies")
		return
	}
	uses, usedBy := usesOf(g, store.RepoRef{ProjectKey: ck, Slug: cs})
	msgs, err := d.messages(ctx)
	if err != nil {
		httpx.ServerError(w, r, err, "messaging")
		return
	}
	writeMarkdown(w, renderMarkdown("Repository "+slug+" (Projekt "+key+")", store.ScopeRepository, page, found)+
		markdownMessaging(repoMessaging(msgs, store.RepoRef{ProjectKey: ck, Slug: cs}))+markdownDeps(uses, usedBy))
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
	// The summary under its own heading, its parts one level down, as the
	// statements under their kinds: an outline that never skips a level.
	if s := markup.Export(p.Summary, "### "); s != "" {
		b.WriteString("## " + summaryHeading + "\n\n" + s + "\n\n")
	}
	// The sentinel is no map: asked for, not drawn.
	if p.Map != nil && !diagram.IsSentinel(p.Map) {
		b.WriteString("## Systemkarte\n\n")
		writeJSONBlock(&b, p.Map)
	}
	if p.RepoMap != nil && !diagram.IsSentinel(p.RepoMap) {
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
		// A kind spanning areas is headed by them, as the page is: its
		// statements one level down, the outline never skipping one.
		if !spansAreas(list) {
			for _, s := range list {
				writeStatement(&b, s, "### ", markup.Heading)
			}
			continue
		}
		// Each area once, where its first statement stands, as the UI's
		// areaGroups: a page not laid out yet reads as one that is.
		var order []string
		byArea := map[string][]store.Statement{}
		for _, s := range list {
			if _, seen := byArea[s.Area]; !seen {
				order = append(order, s.Area)
			}
			byArea[s.Area] = append(byArea[s.Area], s)
		}
		for _, area := range order {
			fmt.Fprintf(&b, "### %s\n\n", areaHeading(area))
			for _, s := range byArea[area] {
				writeStatement(&b, s, "#### ", "##### ")
			}
		}
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

// spansAreas reports whether a kind's statements stand in more than one
// area: one area, or none, needs no heading of its own.
func spansAreas(list []store.Statement) bool {
	for _, s := range list[1:] {
		if s.Area != list[0].Area {
			return true
		}
	}
	return false
}

// areaHeading is an area as a heading, the page's word for statements laid
// out under none.
func areaHeading(area string) string {
	if area == "" {
		return otherArea
	}
	return area
}

// otherArea heads the statements of a kind that stand in no area: the UI's
// S.areas.otherArea, held to it by a test.
const otherArea = "Weiteres"

// writeStatement writes one statement, its name a level heading and its
// text's sub-headings sub.
func writeStatement(b *strings.Builder, s store.Statement, level, sub string) {
	fmt.Fprintf(b, "%s%s\n\n%s\n\n", level, strings.Join(strings.Fields(s.Name), " "), markup.Export(s.Text, sub))
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
