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
// off), both as fit held them to the output limit. WireCap is the
// max_tokens sent: Cap+Room and llmwire's reasoning allowance for the level,
// within MaxOutput (the profile's, or the gateway's where lower) as fit
// leaves them.
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
// limit (maxOutput: the profile's, or the gateway's where lower), as
// llmwire's checkAnswerBudget reckons it. A profile not found names
// neither.
func (c *Client) wireCap(asked int) (int, int64) {
	if c.wire == nil {
		return 0, 0
	}
	p, err := c.wire.Registry().Lookup(c.model)
	if err != nil {
		return 0, 0
	}
	limit := c.maxOutput(p)
	wire := int64(asked) + int64(c.allowance(p.Reasoning))
	if limit > 0 {
		wire = min(wire, limit)
	}
	return int(wire), limit
}

// fit holds a call's answer cap and reasoning room to the model's output
// limit (maxOutput: llmwire's profile, or the gateway's listing where
// lower), less llmwire's allowance for the level: both as asked while they
// fit; else the room what the answer leaves, at least a quarter of what the
// limit leaves, the answer the rest. A fixed room under a low limit would
// leave the answer nothing (a gateway's 16k beside patch's 16k room left it
// 1 token), a squeezed room still lets the model think. No limit known:
// both as asked. A limit at or under the allowance leaves nothing to fit:
// Startup warns of it.
func (c *Client) fit(answer, room int) (int, int) {
	if c.wire == nil {
		return answer, room
	}
	p, err := c.wire.Registry().Lookup(c.model)
	if err != nil {
		return answer, room
	}
	limit := c.maxOutput(p)
	if limit <= 0 {
		return answer, room
	}
	left := int(limit) - c.allowance(p.Reasoning)
	if answer+room <= left {
		return answer, room
	}
	left = max(left, 0)
	room = min(room, max(left-answer, left/4))
	return max(min(answer, left-room), 1), room
}

// restyleCeiling is the most a restyle's answer may take beside its
// reasoning room (restyleRoom, none with thinking off), as fit holds them;
// 0 when no limit is known.
func (c *Client) restyleCeiling() int {
	room := restyleRoom
	if c.effort == EffortOff {
		room = 0
	}
	const unbounded = 1 << 30
	answer, _ := c.fit(unbounded, room)
	if answer == unbounded {
		return 0
	}
	return answer
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
