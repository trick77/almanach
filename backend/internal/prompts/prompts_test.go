package prompts

import (
	"strings"
	"testing"
)

// Every prompt that reads code or notes says what the redaction marker is.
func TestPromptsStateTheRedactionMarker(t *testing.T) {
	for name, p := range map[string]string{"merge": Merge, "catalogue": Catalogue, "describe": Describe, "project": Project,
		"project departure": ProjectDeparture, "repo map": RepoMap} {
		if !strings.Contains(p, Redacted) {
			t.Errorf("%s prompt lacks the redaction sentence %q", name, Redacted)
		}
	}
}

// Every prompt that asks for German states the one spelling rule.
func TestPromptsStateTheSpelling(t *testing.T) {
	for name, p := range map[string]string{"merge": Merge, "catalogue": Catalogue, "describe": Describe, "project": Project,
		"project departure": ProjectDeparture, "repo map": RepoMap, "diagrams": Diagrams} {
		if !strings.Contains(p, Spelling) {
			t.Errorf("%s prompt lacks the spelling rule %q", name, Spelling)
		}
	}
}

// Every prompt that writes statements, or lays the page out, states the one
// rule for areas; every statement it asks for carries one.
func TestPromptsStateTheAreas(t *testing.T) {
	for name, p := range map[string]string{"merge": Merge, "describe": Describe, "project": Project, "repo map": RepoMap} {
		if !strings.Contains(p, Areas) {
			t.Errorf("%s prompt lacks the area rule %q", name, Areas)
		}
	}
	for name, p := range map[string]string{"merge": Merge, "describe": Describe, "project": Project, "project departure": ProjectDeparture} {
		if !strings.Contains(p, `"area": "<area>"`) {
			t.Errorf("%s prompt's statement has no area", name)
		}
	}
}

// Every prompt that writes statement text and a summary states the one
// layout rule and how the summary takes it; none bars the summary from it.
func TestPromptsStateTheLayout(t *testing.T) {
	for name, p := range map[string]string{"merge": Merge, "describe": Describe, "project": Project, "project departure": ProjectDeparture} {
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
	if !strings.Contains(Project, "never one flat list of repositories") || !strings.Contains(Merge, "the repositories' roles, grouped by") {
		t.Error("project or merge prompt still lists the repositories' roles flat")
	}
}

// A flow is told in full once, in its `flow` statement: every prompt that
// writes statements defines the kind, and the summary only names the
// flows, never retells them end to end. That each output format offers it
// is inference's TestPromptKindsMatchTheSchema.
func TestPromptsStateTheFlowKind(t *testing.T) {
	for name, p := range map[string]string{"merge": Merge, "describe": Describe, "project": Project} {
		if !strings.Contains(p, "- `flow`: a flow") {
			t.Errorf("%s prompt does not define the flow kind", name)
		}
		if !strings.Contains(p, "told in full in its `flow` statement") || strings.Contains(p, "flows, end to end") {
			t.Errorf("%s prompt's summary still tells the flows itself", name)
		}
	}
}
