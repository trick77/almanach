package prompts

import (
	"strings"
	"testing"
)

// Every prompt that reads code or notes says what the redaction marker is.
func TestPromptsStateTheRedactionMarker(t *testing.T) {
	for name, p := range map[string]string{"merge": Merge, "catalogue": Catalogue, "describe": Describe, "project": Project, "repo map": RepoMap} {
		if !strings.Contains(p, Redacted) {
			t.Errorf("%s prompt lacks the redaction sentence %q", name, Redacted)
		}
	}
}

// Every prompt that asks for German states the one spelling rule.
func TestPromptsStateTheSpelling(t *testing.T) {
	for name, p := range map[string]string{"merge": Merge, "catalogue": Catalogue, "describe": Describe, "project": Project, "repo map": RepoMap, "diagrams": Diagrams} {
		if !strings.Contains(p, Spelling) {
			t.Errorf("%s prompt lacks the spelling rule %q", name, Spelling)
		}
	}
}

// Every prompt that writes statement text states the one layout rule.
func TestPromptsStateTheLayout(t *testing.T) {
	for name, p := range map[string]string{"merge": Merge, "describe": Describe, "project": Project} {
		if !strings.Contains(p, Layout) {
			t.Errorf("%s prompt lacks the layout rule %q", name, Layout)
		}
	}
}
