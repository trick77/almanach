package inference

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/trick77/toolkit/llmwire"
)

// cutBudget is a call's allowance and use, named beside every cut or
// unparseable answer, in its error, its log line and its file: two restyles
// cut at the output limit (2026-11-11) left no way to tell a think past the
// room from an honest answer past the cap or a runaway once the log was gone.
//
// Cap is the answer's own, Room the reasoning's on top (none with thinking
// off). WireCap is the max_tokens sent: llmwire adds its reasoning allowance
// for the level to Cap+Room and holds the sum to the model's MaxOutput,
// silently (wireCap; a 32k cap with its room on a 32k model goes out as 32k).
// Warnings are llmwire's on the request, a cap above the model's limit among
// them. PageChars is the restyled page's, PromptTokens the prompt's estimate
// (PromptTokens). The token counts are the gateway's, nil when it reported
// none (a cancelled stream). Unknown or none is left out, never a zero.
type cutBudget struct {
	Cap             int      `json:"cap"`
	Room            int      `json:"room,omitempty"`
	WireCap         int      `json:"wire_cap,omitempty"`
	MaxOutput       int64    `json:"max_output,omitempty"`
	PageChars       int      `json:"page_chars,omitempty"`
	PromptTokens    int      `json:"prompt_tokens_est,omitempty"`
	OutTokens       *int64   `json:"out_tokens,omitempty"`
	ReasoningTokens *int64   `json:"reasoning_tokens,omitempty"`
	Warnings        []string `json:"warnings,omitempty"`
}

// String is the budget as an error's tail: the tokens used and of them the
// reasoning where the gateway counted them, then the caps and the page.
func (b cutBudget) String() string {
	var s strings.Builder
	if b.OutTokens != nil {
		fmt.Fprintf(&s, "%d tokens out", *b.OutTokens)
		if b.ReasoningTokens != nil {
			fmt.Fprintf(&s, ", %d of them reasoning", *b.ReasoningTokens)
		}
		s.WriteString("; ")
	}
	caps := fmt.Sprintf("answer %d", b.Cap)
	if b.Room > 0 {
		caps += fmt.Sprintf(", reasoning room %d", b.Room)
	}
	if b.MaxOutput > 0 {
		caps += fmt.Sprintf(", model limit %d", b.MaxOutput)
	}
	if b.WireCap > 0 {
		fmt.Fprintf(&s, "cap %d (%s)", b.WireCap, caps)
	} else {
		fmt.Fprintf(&s, "cap: %s", caps)
	}
	if b.PageChars > 0 {
		fmt.Fprintf(&s, ", page %d chars", b.PageChars)
	}
	return s.String()
}

// LogValue is the budget as one log group, the same fields as the file.
func (b cutBudget) LogValue() slog.Value {
	attrs := []slog.Attr{slog.Int("cap", b.Cap)}
	add := func(k string, v int64) {
		if v > 0 {
			attrs = append(attrs, slog.Int64(k, v))
		}
	}
	add("room", int64(b.Room))
	add("wire_cap", int64(b.WireCap))
	add("max_output", b.MaxOutput)
	add("page_chars", int64(b.PageChars))
	add("prompt_tokens_est", int64(b.PromptTokens))
	if b.OutTokens != nil {
		attrs = append(attrs, slog.Int64("out_tokens", *b.OutTokens))
	}
	if b.ReasoningTokens != nil {
		attrs = append(attrs, slog.Int64("reasoning_tokens", *b.ReasoningTokens))
	}
	if len(b.Warnings) > 0 {
		attrs = append(attrs, slog.String("warnings", strings.Join(b.Warnings, "; ")))
	}
	return slog.GroupValue(attrs...)
}

// wireCap is the max_tokens llmwire sends for an answer budget of asked
// (cap and room) and the model's output limit, 0 unknown: asked plus the
// reasoning allowance of the level this client resolves to, held to the
// limit, as llmwire's checkAnswerBudget reckons it. A profile not found
// names neither.
func (c *Client) wireCap(asked int) (int, int64) {
	if c.wire == nil {
		return 0, 0
	}
	p, err := c.wire.Registry().Lookup(c.model)
	if err != nil {
		return 0, 0
	}
	wire := int64(asked) + int64(c.allowance(p.Reasoning))
	if limit := p.Limits.MaxOutput; limit > 0 {
		wire = min(wire, limit)
	}
	return int(wire), p.Limits.MaxOutput
}

// answerCeiling is the most an answer may take of the model's output limit,
// as llmwire's profile gives it, beside room (the call's reasoning room,
// none with thinking off) and llmwire's allowance for the level; 0 when
// llmwire knows no limit for the model.
func (c *Client) answerCeiling(room int) int {
	if c.wire == nil {
		return 0
	}
	p, err := c.wire.Registry().Lookup(c.model)
	if err != nil || p.Limits.MaxOutput <= 0 {
		return 0
	}
	if c.effort == EffortOff {
		room = 0
	}
	return max(int(p.Limits.MaxOutput)-room-c.allowance(p.Reasoning), 1)
}

// allowance is the reasoning tokens llmwire adds to an answer budget for
// this client's level (llmwire's reasoningOverhead): the profile's measured
// overhead for the level, else llmwire's default; none where the model
// cannot think, nor with thinking off unless measured.
func (c *Client) allowance(r llmwire.Reasoning) int {
	if !r.Supported {
		return 0
	}
	or := func(level string, def int) int {
		if n, ok := r.Overhead[level]; ok {
			return n
		}
		return def
	}
	switch level := c.effort; {
	case level == EffortOff || level == "none":
		return or("off", 0)
	case level == "" && r.Balanced != "":
		return or(r.Balanced, llmwire.DefaultReasoningOverhead)
	case level == "":
		// Balanced sends no knob here: the model's own default.
		if !r.EnabledByDefault {
			return 0
		}
		if n, ok := r.Overhead[r.DefaultEffort]; ok && r.DefaultEffort != "" {
			return n
		}
		return or("default", llmwire.DefaultReasoningOverhead)
	default:
		return or(level, llmwire.DefaultReasoningOverhead)
	}
}

// warningTexts is llmwire's warnings as the budget keeps them.
func warningTexts(ws []llmwire.Warning) []string {
	out := make([]string, 0, len(ws))
	for _, w := range ws {
		out = append(out, w.String())
	}
	return out
}
