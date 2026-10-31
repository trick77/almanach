package inference

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/trick77/llmwire"
)

// The calls of a baseline run, one per step, each with its own strict
// schema: the catalogue (what to read), the patch (what one part of the
// code teaches the repository page) and the project description. Same
// rules as the merge: one Chat, never streaming, no retry inside a call.

// Subject is one piece of a repository as the catalogue names it: a flow
// or a part ("the import pipeline"), what it is for, and the paths that
// make it up, most important first. A path ending in / is a directory.
type Subject struct {
	Name    string   `json:"name"`
	Purpose string   `json:"purpose"`
	Paths   []string `json:"paths"`
}

// Catalogue is the catalogue model's answer.
type Catalogue struct {
	Subjects []Subject `json:"subjects"`
}

// CatalogueResult is one catalogue call.
type CatalogueResult struct {
	Call
	Catalogue Catalogue
}

// Patch is what one step teaches the repository page: edits by id, new
// statements in a list of their own without one, ids to drop, ids
// confirmed unchanged, and a new summary or null to keep it. An id it does
// not name stays as it is.
type Patch struct {
	Summary    *string     `json:"summary"`
	Statements []Statement `json:"statements"`
	Added      []Statement `json:"added"`
	Drop       []string    `json:"drop"`
	Confirmed  []string    `json:"confirmed"`
}

// PatchResult is one overview or subject call.
type PatchResult struct {
	Call
	Patch Patch
}

// DescriptionResult is one project call: Description nil means no change;
// a rewrite's Map is nil only for a project too large to draw.
type DescriptionResult struct {
	Call
	Description *Description
}

// Schema names on the wire.
const (
	CatalogueSchemaName   = "almanach_catalogue"
	PatchSchemaName       = "almanach_patch"
	DescriptionSchemaName = "almanach_description"
)

// Catalogue asks which subjects a repository is made of.
func (c *Client) Catalogue(ctx context.Context, prompt string) CatalogueResult {
	content, call := c.call(ctx, prompt, catalogueFormat())
	out := CatalogueResult{Call: call}
	if call.Outcome == OutcomeOK && (!parse(content, &out.Catalogue) || out.Catalogue.Subjects == nil) {
		out.Outcome, out.Catalogue = OutcomeUnparseable, Catalogue{}
	}
	return out
}

// Patch asks what one part of the code teaches the repository page.
func (c *Client) Patch(ctx context.Context, prompt string) PatchResult {
	content, call := c.call(ctx, prompt, patchFormat(c.diagrams))
	out := PatchResult{Call: call}
	if call.Outcome == OutcomeOK && (!parse(content, &out.Patch) || out.Patch.Statements == nil) {
		out.Outcome, out.Patch = OutcomeUnparseable, Patch{}
	}
	return out
}

// Describe asks for the project description rewritten, or null.
func (c *Client) Describe(ctx context.Context, prompt string) DescriptionResult {
	content, call := c.call(ctx, prompt, descriptionFormat(c.diagrams))
	out := DescriptionResult{Call: call}
	if call.Outcome != OutcomeOK {
		return out
	}
	// The key must be there: {} is no answer, not "no change". A rewrite
	// must carry its statements, or the page would lose them all.
	var answer *struct {
		Description json.RawMessage `json:"description"`
	}
	if !parse(content, &answer) || answer == nil || answer.Description == nil {
		out.Outcome = OutcomeUnparseable
		return out
	}
	if string(answer.Description) == "null" {
		return out
	}
	// The map may be null (a project too large to draw); whether it should
	// be is the worker's to judge, and a missing one never sinks the text.
	var d Description
	if json.Unmarshal(answer.Description, &d) != nil || d.Statements == nil {
		out.Outcome = OutcomeUnparseable
		return out
	}
	out.Description = &d
	return out
}

func strict(name string, properties map[string]any) llmwire.ResponseFormat {
	return llmwire.ResponseFormat{Kind: llmwire.FormatJSONSchema, Name: name, Schema: object(properties), Strict: true}
}

// sortedKeys keeps the required list in one order on the wire, call after
// call: the gateway's prefix cache keys on the bytes.
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func catalogueFormat() llmwire.ResponseFormat {
	return strict(CatalogueSchemaName, map[string]any{
		"subjects": array(object(map[string]any{
			"name":    map[string]any{"type": "string", "minLength": 1},
			"purpose": map[string]any{"type": "string", "minLength": 1},
			"paths":   map[string]any{"type": "array", "minItems": 1, "items": map[string]any{"type": "string", "minLength": 1}},
		})),
	})
}

func patchFormat(diagrams bool) llmwire.ResponseFormat {
	ids := map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	return strict(PatchSchemaName, map[string]any{
		"summary":    map[string]any{"type": []any{"string", "null"}, "minLength": 1},
		"statements": statements(diagrams, RepoKinds),
		"added":      added(diagrams, RepoKinds),
		"drop":       ids,
		"confirmed":  ids,
	})
}

func descriptionFormat(diagrams bool) llmwire.ResponseFormat {
	return strict(DescriptionSchemaName, map[string]any{"description": projectDescription(diagrams)})
}
