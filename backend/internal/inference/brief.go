package inference

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/trick77/toolkit/llmwire"
)

// BriefSchemaName is a brief's schema on the wire.
const BriefSchemaName = "almanach_brief"

// BriefResult is one brief call: the text in the stored layout (blocks
// joined by markup.FromBlocks), never empty on OutcomeOK.
type BriefResult struct {
	Call
	Text string
}

// Brief asks for a repository's brief, written from its stored page.
func (c *Client) Brief(ctx context.Context, prompt string) BriefResult {
	content, call := c.call(ctx, prompt, briefFormat(), capBrief)
	out := BriefResult{Call: call}
	if call.Outcome != OutcomeOK {
		return out
	}
	var answer *struct {
		Brief json.RawMessage `json:"brief"`
	}
	if !parse(content, &answer) || answer == nil {
		out.Outcome = c.unparseable(out.Call, content)
		return out
	}
	text, ok, err := laid(answer.Brief)
	if err != nil || !ok || strings.TrimSpace(text) == "" {
		out.Outcome = c.unparseable(out.Call, content)
		return out
	}
	out.Text = text
	return out
}

func briefFormat() llmwire.ResponseFormat {
	return strict(BriefSchemaName, map[string]any{"brief": blocks()})
}
