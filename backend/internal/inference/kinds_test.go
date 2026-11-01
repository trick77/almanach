package inference

import (
	"strings"
	"testing"

	"github.com/trick77/almanach/internal/prompts"
)

// Each prompt's output format offers exactly the kinds its schema takes, in
// the schema's order: a kind added to RepoKinds but missed in a prompt
// would be accepted and never asked for.
func TestPromptKindsMatchTheSchema(t *testing.T) {
	union := func(kinds []string) string {
		return `"kind": "` + strings.Join(kinds, `" | "`) + `",`
	}
	for name, tc := range map[string]struct {
		prompt string
		kinds  []string
	}{
		"describe": {prompts.Describe, RepoKinds},
		"merge":    {prompts.Merge, RepoKinds},
		"project":  {prompts.Project, ProjectKinds},
	} {
		if want := union(tc.kinds); !strings.Contains(tc.prompt, want) {
			t.Errorf("%s prompt lacks %s", name, want)
		}
	}
}
