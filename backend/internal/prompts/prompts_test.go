package prompts

import (
	"regexp"
	"strings"
	"testing"
)

// Every prompt that reads code or notes says what the redaction marker is.
func TestPromptsStateTheRedactionMarker(t *testing.T) {
	for name, p := range map[string]string{"merge": Merge, "catalogue": Catalogue, "describe": Describe, "project": Project,
		"project departure": ProjectDeparture, "repo map": RepoMap, "restyle": Restyle, "project restyle": ProjectRestyle, "brief": Brief,
		"router": Router} {
		if !strings.Contains(p, Redacted) {
			t.Errorf("%s prompt lacks the redaction sentence %q", name, Redacted)
		}
	}
}

// Every prompt that asks for German states the one spelling rule and the
// one jargon rule, the diagram rules the jargon rule once, by the prompt
// they are rendered into.
func TestPromptsStateTheSpelling(t *testing.T) {
	for name, p := range map[string]string{"merge": Merge, "catalogue": Catalogue, "describe": Describe, "project": Project,
		"project departure": ProjectDeparture, "repo map": RepoMap, "diagrams": Diagrams, "restyle": Restyle, "project restyle": ProjectRestyle,
		"brief": Brief, "router": Router} {
		if !strings.Contains(p, Spelling) {
			t.Errorf("%s prompt lacks the spelling rule %q", name, Spelling)
		}
		if got := strings.Count(p, Jargon); name != "diagrams" && got != 1 || name == "diagrams" && got != 0 {
			t.Errorf("%s prompt states the jargon rule %d times", name, got)
		}
	}
}

// Every prompt that writes statements, or lays the page out, states the one
// rule for areas; every statement it asks for carries one.
func TestPromptsStateTheAreas(t *testing.T) {
	for name, p := range map[string]string{"merge": Merge, "describe": Describe, "project": Project, "repo map": RepoMap,
		"restyle": Restyle, "project restyle": ProjectRestyle} {
		if !strings.Contains(p, Areas) {
			t.Errorf("%s prompt lacks the area rule %q", name, Areas)
		}
	}
	for name, p := range map[string]string{"merge": Merge, "describe": Describe, "project": Project, "project departure": ProjectDeparture,
		"restyle": Restyle, "project restyle": ProjectRestyle} {
		if !strings.Contains(p, `"area": "<area>"`) {
			t.Errorf("%s prompt's statement has no area", name)
		}
	}
}

// Both restyles ask for the diff alone, in one wording: an area by the
// outline, never a statement returned for it.
func TestRestylesAskTheDiff(t *testing.T) {
	for name, p := range map[string]string{"restyle": Restyle, "project restyle": ProjectRestyle} {
		if !strings.Contains(p, RestyleEdits) {
			t.Errorf("%s prompt lacks the diff rule %q", name, RestyleEdits)
		}
	}
}

// Every prompt that writes statement text and a summary states the one
// layout rule and how the summary takes it; none bars the summary from it.
func TestPromptsStateTheLayout(t *testing.T) {
	for name, p := range map[string]string{"merge": Merge, "describe": Describe, "project": Project, "project departure": ProjectDeparture,
		"restyle": Restyle, "project restyle": ProjectRestyle} {
		if !strings.Contains(p, Layout) {
			t.Errorf("%s prompt lacks the layout rule %q", name, Layout)
		}
		if !strings.Contains(p, SummaryLayout) {
			t.Errorf("%s prompt lacks the summary layout %q", name, SummaryLayout)
		}
		if strings.Contains(p, "never in a summary") || strings.Contains(p, `"summary": "<prose>"`) {
			t.Errorf("%s prompt still keeps the summary one block of prose", name)
		}
		// The layout is the schema's blocks: an output format asking for a
		// text as one string is how a model's newlines got lost.
		if !strings.Contains(p, Block) || strings.Contains(p, `laid out as above>"`) {
			t.Errorf("%s prompt's output format does not ask for blocks", name)
		}
		// The order's parts are content, never a heading's title.
		if strings.Contains(strings.ToLower(p), "purpose and boundary") {
			t.Errorf("%s prompt still names a part \"purpose and boundary\"", name)
		}
	}
}

// A describe step names another repository's interface only where its
// code shows the call, never from the list alone; the project's summary
// groups the repositories' roles, never one flat list of them.
func TestPromptsReadTheOtherRepositories(t *testing.T) {
	if !strings.Contains(Describe, "{sibling_interfaces}") || !strings.Contains(Describe, "never from the list alone") {
		t.Error("describe prompt lacks the other repositories' interfaces or the rule that guards them")
	}
	if !strings.Contains(Project, "never one flat list of repositories") || !strings.Contains(Merge, "the repositories' roles, grouped by") ||
		!strings.Contains(ProjectRestyle, "never one flat list of repositories") {
		t.Error("project, merge or project restyle prompt still lists the repositories' roles flat")
	}
}

// A restyle reads no code and learns nothing: it says so, and every
// statement it adds names what it was split from, which the code carries
// the sources of (consolidate's restyle). A repository's drops nothing; the
// project's drops what is one repository's inside, never what spans
// repositories.
func TestRestylePromptsLearnNothing(t *testing.T) {
	for name, p := range map[string]string{"restyle": Restyle, "project restyle": ProjectRestyle} {
		for _, want := range []string{"you learn nothing", `"from": ["<id>" | "summary", ...]`, "`outline`"} {
			if !strings.Contains(p, want) {
				t.Errorf("%s prompt lacks %q", name, want)
			}
		}
		if strings.Contains(p, `"confirmed"`) {
			t.Errorf("%s prompt's output format offers a confirmation", name)
		}
	}
	if !strings.Contains(Restyle, "Nothing is dropped") || strings.Contains(Restyle, `"drop"`) {
		t.Error("repository restyle prompt drops")
	}
	for _, want := range []string{`"drop": ["<id>", ...]`, "how one repository works inside", "A statement pull requests taught about how repositories work together stays",
		"never a source of facts for the page"} {
		if !strings.Contains(ProjectRestyle, want) {
			t.Errorf("project restyle prompt lacks %q", want)
		}
	}
}

// Every project prompt that writes the page judges the project's shape the
// same way and holds a system's page to the wiring: no component, how a
// repository works inside its page's; the merge reads a collection's from
// its description's head (consolidate's collectionProject).
func TestProjectPromptsJudgeTheShape(t *testing.T) {
	judge := "A repository building on another's code (a common helper library) makes no system by itself"
	for name, p := range map[string]string{"project": Project, "project restyle": ProjectRestyle} {
		for _, want := range []string{"## The project's shape", judge, "{project_shape}", "{inner_edges}", `"shape": "system" | "collection"`,
			"## A collection", "No list of the repositories", "never how one works"} {
			if !strings.Contains(p, want) {
				t.Errorf("%s prompt lacks %q", name, want)
			}
		}
		if strings.Contains(p, `"kind": "component"`) {
			t.Errorf("%s prompt's output format offers a component", name)
		}
	}
	if !strings.Contains(Merge, "never how one works") || !strings.Contains(Merge, "never a `component` or an `extension`") {
		t.Error("merge prompt does not hold the project page to the wiring")
	}
	if strings.Contains(ProjectDeparture, `"kind": "component"`) {
		t.Error("departure prompt offers a component")
	}
}

// A flow is told in full once, in its `flow` statement: every prompt that
// writes statements defines the kind, and the summary only names the
// flows, never retells them end to end. That each output format offers it
// is inference's TestPromptKindsMatchTheSchema.
func TestPromptsStateTheFlowKind(t *testing.T) {
	for name, p := range map[string]string{"merge": Merge, "describe": Describe, "project": Project, "restyle": Restyle, "project restyle": ProjectRestyle} {
		if !strings.Contains(p, "- `flow`: a flow") {
			t.Errorf("%s prompt does not define the flow kind", name)
		}
		if !strings.Contains(p, "told in full in its `flow` statement") || strings.Contains(p, "flows, end to end") {
			t.Errorf("%s prompt's summary still tells the flows itself", name)
		}
	}
}

// A gateway caches a prompt's identical prefix only: before, every prompt
// named its repository a few kB in, and a call cached its first 1024
// tokens of 66-92k (2026-11-07). The instructions come first, byte for byte
// the same on every call, the call's values after the output format; only
// what a deployment fixes (the diagram rules and limits; the repository
// map's size, a constant) comes earlier. The call's own section stands
// before "Untrusted input", which the system message makes data.
// Catalogue (below one cached block) and brief (a button's rare call) are
// left as they are.
func TestPromptsPutTheCallLast(t *testing.T) {
	diagrams := map[string]bool{"{diagram_rules}": true, "{diagram_change}": true, "{diagram_field}": true}
	repoMap := map[string]bool{"{map_max_nodes}": true, "{map_max_edges}": true}
	placeholder := regexp.MustCompile(`\{[a-z_]+\}`)
	for name, p := range map[string]string{"merge": Merge, "describe": Describe, "project": Project, "project departure": ProjectDeparture,
		"repo map": RepoMap, "restyle": Restyle, "project restyle": ProjectRestyle, "router": Router} {
		format, untrusted := strings.Index(p, "## Output format"), strings.Index(p, "## Untrusted input")
		if format < 0 || untrusted < format {
			t.Fatalf("%s prompt has no output format, or its untrusted input before it", name)
		}
		for _, at := range placeholder.FindAllStringIndex(p, -1) {
			ph := p[at[0]:at[1]]
			if fixed := diagrams[ph] || name == "repo map" && repoMap[ph]; !fixed && at[0] < format {
				t.Errorf("%s prompt has %s before its output format, at byte %d", name, ph, at[0])
			}
		}
		if call := strings.Index(p, "## This "); call < format || call > untrusted {
			t.Errorf("%s prompt's call section is not between the output format and the untrusted input", name)
		}
	}
}

// The brief holds only what an agent cannot read from the code, short,
// in blocks, with every placeholder the worker fills.
func TestBriefPromptKeepsToTheUnderivable(t *testing.T) {
	for _, want := range []string{"only what the agent cannot learn from the code", "Never in the brief", "2500 characters",
		Block, "{repo}", "{project}", "{project_description}", "{dependencies}", "{repo_description}", `{"brief": [<block>, ...]}`} {
		if !strings.Contains(Brief, want) {
			t.Errorf("brief prompt lacks %q", want)
		}
	}
}
