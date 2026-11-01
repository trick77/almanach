package prompts

import (
	"strings"
	"testing"
)

// Every prompt that asks for German states the one spelling rule.
func TestPromptsStateTheSpelling(t *testing.T) {
	for name, p := range map[string]string{"merge": Merge, "catalogue": Catalogue, "describe": Describe, "project": Project, "repo map": RepoMap, "diagrams": Diagrams} {
		if !strings.Contains(p, Spelling) {
			t.Errorf("%s prompt lacks the spelling rule %q", name, Spelling)
		}
	}
}
