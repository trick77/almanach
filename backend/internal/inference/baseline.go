package inference

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/trick77/almanach/internal/diagram"
	"github.com/trick77/toolkit/llmwire"
)

// The calls of a baseline run, one per step, each with its own strict
// schema: the catalogue (what to read), the patch (what one part of the
// code teaches the repository page), the project description and the
// repository's map. Same
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
// a rewrite's Map is nil only for a project too large to draw. Outline is
// the project page's layout, nil to keep it, given with a change or
// without one.
type DescriptionResult struct {
	Call
	Description *Description
	Outline     []OutlineArea
}

// RepoMapResult is one map call, a repository's map or the system map
// asked again: Map nil means keep the map the page has. Outline is the
// repository map step's layout of the page, nil to keep it (and always nil
// on the system map's call).
type RepoMapResult struct {
	Call
	Map     *diagram.Diagram
	Outline []OutlineArea
}

// RestyleResult is one restyle call: a patch over the page (edits, new
// statements naming what they were split from, a summary or null; the
// schema offers no drop nor confirmation, and the worker takes none) and
// the page's layout, nil to keep it.
type RestyleResult struct {
	Call
	Patch   Patch
	Outline []OutlineArea
}

// Schema names on the wire.
const (
	CatalogueSchemaName   = "almanach_catalogue"
	PatchSchemaName       = "almanach_patch"
	DescriptionSchemaName = "almanach_description"
	RepoMapSchemaName     = "almanach_repo_map"
	ProjectMapSchemaName  = "almanach_project_map"
	RestyleSchemaName     = "almanach_restyle"
)

// Restyle asks for a stored page brought to the rules of today, the page
// alone in the prompt: the repository's, or with project the project's,
// whose kinds it takes.
func (c *Client) Restyle(ctx context.Context, prompt string, project bool) RestyleResult {
	kinds := RepoKinds
	if project {
		kinds = ProjectKinds
	}
	content, call := c.call(ctx, prompt, restyleFormat(c.diagrams, kinds))
	out := RestyleResult{Call: call}
	if call.Outcome != OutcomeOK {
		return out
	}
	var outline struct {
		Outline json.RawMessage `json:"outline"`
	}
	if !parse(content, &out.Patch) || out.Patch.Statements == nil || !parse(content, &outline) {
		out.Outcome, out.Patch = OutcomeUnparseable, Patch{}
		return out
	}
	out.Outline = parseOutline(outline.Outline)
	return out
}

// restyleFormat is a restyle's answer: the patch's summary and edits, new
// statements each with its origin, and the outline.
func restyleFormat(diagrams bool, kinds []string) llmwire.ResponseFormat {
	props := statementProps(diagrams, kinds)
	props["from"] = array(str())
	return strict(RestyleSchemaName, map[string]any{
		"summary":    nullableBlocks(),
		"statements": statements(diagrams, kinds),
		"added":      array(object(props)),
		"outline":    outlineSchema(),
	})
}

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
		Outline     json.RawMessage `json:"outline"`
	}
	if !parse(content, &answer) || answer == nil || answer.Description == nil {
		out.Outcome = OutcomeUnparseable
		return out
	}
	out.Outline = parseOutline(answer.Outline)
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

// RepoMap asks for the repository's map drawn from its description, or
// null to keep the one it has.
func (c *Client) RepoMap(ctx context.Context, prompt string) RepoMapResult {
	return c.mapCall(ctx, prompt, repoMapFormat(), true)
}

// ProjectMap asks the project step's prompt again for its system map alone,
// after the one it returned was refused; null keeps the stored one.
func (c *Client) ProjectMap(ctx context.Context, prompt string) RepoMapResult {
	return c.mapCall(ctx, prompt, strict(ProjectMapSchemaName, map[string]any{"map": nullable(mapSchema())}), false)
}

// mapCall is one map call; outline, the repository map step's, whose
// answer lays the page out too. An outline that does not parse costs
// only itself: the map is the call's answer.
func (c *Client) mapCall(ctx context.Context, prompt string, format llmwire.ResponseFormat, outline bool) RepoMapResult {
	content, call := c.call(ctx, prompt, format)
	out := RepoMapResult{Call: call}
	if call.Outcome != OutcomeOK {
		return out
	}
	// The key must be there: {} is no answer, not "keep".
	var answer *struct {
		Map     json.RawMessage `json:"map"`
		Outline json.RawMessage `json:"outline"`
	}
	if !parse(content, &answer) || answer == nil || answer.Map == nil {
		out.Outcome = OutcomeUnparseable
		return out
	}
	if outline {
		out.Outline = parseOutline(answer.Outline)
	}
	if string(answer.Map) == "null" {
		return out
	}
	var d diagram.Diagram
	if json.Unmarshal(answer.Map, &d) != nil {
		out.Outcome = OutcomeUnparseable
		return out
	}
	out.Map = &d
	return out
}

func repoMapFormat() llmwire.ResponseFormat {
	return strict(RepoMapSchemaName, map[string]any{"map": nullable(repoMapSchema()), "outline": outlineSchema()})
}

// outlineSchema is a page's layout, its areas in order with their
// statements by id; null keeps the page as laid out.
func outlineSchema() map[string]any {
	outline := array(object(map[string]any{"area": str(), "ids": array(str())}))
	outline["type"] = []any{"array", "null"}
	return outline
}

// parseOutline reads an answer's outline: nil for none, null or one off
// its shape, which costs only itself.
func parseOutline(raw json.RawMessage) []OutlineArea {
	if raw == nil || string(raw) == "null" {
		return nil
	}
	var o []OutlineArea
	if json.Unmarshal(raw, &o) != nil {
		return nil
	}
	return o
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
		"summary":    nullableBlocks(),
		"statements": statements(diagrams, RepoKinds),
		"added":      added(diagrams, RepoKinds),
		"drop":       ids,
		"confirmed":  ids,
	})
}

func descriptionFormat(diagrams bool) llmwire.ResponseFormat {
	return strict(DescriptionSchemaName, map[string]any{"description": projectDescription(diagrams), "outline": outlineSchema()})
}
