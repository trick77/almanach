// Package ingest reads one noergler payload into what the store keeps. The
// contract is ours on both sides, so it is strict: an unknown field or a
// missing required one is an error naming it, and the body is rejected
// with a 422 rather than half-stored. Optional strings accept "" as absent.
package ingest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/trick77/almanach/internal/store"
)

// EventType is the one event noergler sends.
const EventType = "pr_merged_note"

// Payload is the wire shape, every field the sender emits.
type Payload struct {
	EventType         string          `json:"event_type"`
	ProjectKey        string          `json:"project_key"`
	RepoSlug          string          `json:"repo_slug"`
	PRID              int             `json:"pr_id"`
	PRKey             string          `json:"pr_key"`
	PRTitle           string          `json:"pr_title"`
	TicketKey         *string         `json:"ticket_key"`
	JiraIssueType     *string         `json:"jira_issue_type"`
	ChangeKind        *string         `json:"change_kind"`
	ReviewedCommit    *string         `json:"reviewed_commit"`
	SourceCommitSHA   string          `json:"source_commit_sha"`
	MergeCommitSHA    *string         `json:"merge_commit_sha"`
	MergedAt          string          `json:"merged_at"`
	RunID             int64           `json:"run_id"`
	ModelLabel        *string         `json:"model_label"`
	IssuesPosted      int             `json:"issues_posted"`
	SuggestionsPosted int             `json:"suggestions_posted"`
	Note              json.RawMessage `json:"note"`
	Claims            []claim         `json:"claims"`
	NoteCall          *noteCall       `json:"note_call"`
	Rounds            rounds          `json:"rounds"`
	ReviewTotals      json.RawMessage `json:"review_totals"`
}

type claim struct {
	ProjectKey string  `json:"project_key"`
	RepoSlug   *string `json:"repo_slug"`
}

type rounds struct {
	Count        int     `json:"count"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	CostUSD      *string `json:"cost_usd"`
	CostNanoUSD  *int64  `json:"cost_nano_usd"`
}

type noteCall struct {
	Outcome        string   `json:"outcome"`
	NoteStatus     string   `json:"note_status"`
	Profile        *string  `json:"profile"`
	Model          *string  `json:"model"`
	Deployment     *string  `json:"deployment"`
	ReasoningSent  *string  `json:"reasoning_sent"`
	FinishReason   *string  `json:"finish_reason"`
	CallID         *string  `json:"call_id"`
	LiteLLMVersion *string  `json:"litellm_version"`
	InputTokens    *int64   `json:"input_tokens"`
	InputUncached  *int64   `json:"input_uncached_tokens"`
	CacheRead      *int64   `json:"cache_read_tokens"`
	CacheWrite     *int64   `json:"cache_write_tokens"`
	OutputTokens   *int64   `json:"output_tokens"`
	Reasoning      *int64   `json:"reasoning_tokens"`
	CostUSD        *string  `json:"cost_usd"`
	Cost           *int64   `json:"cost_nano_usd"`
	CostInput      *int64   `json:"cost_input_nano_usd"`
	CostCacheRead  *int64   `json:"cost_cache_read_nano_usd"`
	CostCacheWrite *int64   `json:"cost_cache_write_nano_usd"`
	CostOutput     *int64   `json:"cost_output_nano_usd"`
	CostReasoning  *int64   `json:"cost_reasoning_nano_usd"`
	CostTool       *int64   `json:"cost_tool_usage_nano_usd"`
	KeySpendUSD    *string  `json:"key_spend_usd"`
	KeySpend       *int64   `json:"key_spend_nano_usd"`
	HeadersMS      *int64   `json:"headers_ms"`
	TotalMS        *int64   `json:"total_ms"`
	Warnings       []string `json:"warnings"`
}

// noteShape is the note object as far as ingest reads it: the bugfix block
// and the component files the problem areas group by. The rest is kept
// verbatim.
type noteShape struct {
	ChangeKind string          `json:"change_kind"`
	Bugfix     json.RawMessage `json:"bugfix"`
	Components []struct {
		Name  string   `json:"name"`
		Files []string `json:"files"`
	} `json:"components"`
}

// Parsed is a payload lifted into the store's rows.
type Parsed struct {
	DeliveryID string
	EventType  string
	Note       store.Note
	Call       *store.NoteCall
	Claims     []store.Claim
}

// Parse reads and checks one body.
func Parse(raw []byte) (Parsed, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var p Payload
	if err := dec.Decode(&p); err != nil {
		return Parsed{}, fmt.Errorf("body: %w", err)
	}
	if p.EventType != EventType {
		return Parsed{}, fmt.Errorf("event_type: want %q, got %q", EventType, p.EventType)
	}
	// A slice, not a map: with two fields missing the 422 names the same
	// one every time.
	for _, f := range []struct{ name, v string }{{"project_key", p.ProjectKey}, {"repo_slug", p.RepoSlug}, {"pr_key", p.PRKey},
		{"pr_title", p.PRTitle}, {"source_commit_sha", p.SourceCommitSHA}, {"merged_at", p.MergedAt}} {
		if strings.TrimSpace(f.v) == "" {
			return Parsed{}, fmt.Errorf("%s: required", f.name)
		}
	}
	if p.PRID < 1 {
		return Parsed{}, errors.New("pr_id: must be positive")
	}
	if len(p.SourceCommitSHA) < 7 {
		return Parsed{}, errors.New("source_commit_sha: at least 7 characters")
	}
	mergedAt, err := time.Parse(time.RFC3339, p.MergedAt)
	if err != nil {
		return Parsed{}, fmt.Errorf("merged_at: %w", err)
	}
	if len(p.Note) == 0 || string(p.Note) == "null" {
		return Parsed{}, errors.New("note: required")
	}
	var shape noteShape
	if err := json.Unmarshal(p.Note, &shape); err != nil {
		return Parsed{}, fmt.Errorf("note: %w", err)
	}

	n := store.Note{
		ProjectKey: p.ProjectKey, RepoSlug: p.RepoSlug, PRID: p.PRID, PRKey: p.PRKey, PRTitle: p.PRTitle,
		TicketKey: deref(p.TicketKey), IssueType: deref(p.JiraIssueType), ChangeKind: deref(p.ChangeKind),
		Body: p.Note, ReviewedCommit: deref(p.ReviewedCommit), SourceCommit: strings.ToLower(p.SourceCommitSHA),
		MergeCommit: strings.ToLower(deref(p.MergeCommitSHA)), MergedAt: mergedAt.UTC(), RunID: p.RunID,
		ModelLabel: deref(p.ModelLabel), IssuesPosted: p.IssuesPosted, Suggestions: p.SuggestionsPosted,
	}
	if n.ChangeKind == "" {
		n.ChangeKind = shape.ChangeKind
	}
	if n.ChangeKind == "bugfix" && len(shape.Bugfix) > 0 && string(shape.Bugfix) != "null" {
		n.Bugfix = shape.Bugfix
		var files []string
		for _, c := range shape.Components {
			files = append(files, c.Files...)
		}
		n.BugfixArea = commonDir(files)
		if len(shape.Components) > 0 {
			n.BugfixComponent = shape.Components[0].Name
		}
	}

	claims := make([]store.Claim, 0, len(p.Claims))
	for _, c := range p.Claims {
		if strings.TrimSpace(c.ProjectKey) == "" {
			return Parsed{}, errors.New("claims: project_key required")
		}
		claims = append(claims, store.Claim{ProjectKey: c.ProjectKey, RepoSlug: deref(c.RepoSlug)})
	}

	var call *store.NoteCall
	if c := p.NoteCall; c != nil {
		call = &store.NoteCall{
			Outcome: c.Outcome, NoteStatus: c.NoteStatus, Profile: deref(c.Profile), Model: deref(c.Model),
			Deployment: deref(c.Deployment), Reasoning: deref(c.ReasoningSent), Finish: deref(c.FinishReason),
			CallID: deref(c.CallID), GatewayVer: deref(c.LiteLLMVersion),
			InputTokens: c.InputTokens, InputUncached: c.InputUncached, CacheRead: c.CacheRead, CacheWrite: c.CacheWrite,
			OutputTokens: c.OutputTokens, ReasoningTokens: c.Reasoning,
			Cost: c.Cost, CostInput: c.CostInput, CostCacheRead: c.CostCacheRead, CostCacheWrite: c.CostCacheWrite,
			CostOutput: c.CostOutput, CostReasoning: c.CostReasoning, CostTool: c.CostTool,
			KeySpend: c.KeySpend, HeadersMS: c.HeadersMS, TotalMS: c.TotalMS, Warnings: c.Warnings,
		}
		if p.RunID != 0 {
			id := p.RunID
			call.RunID = &id
		}
	} else {
		// No call recorded for the round: the round totals still count.
		call = &store.NoteCall{Outcome: "", NoteStatus: ""}
	}
	call.Rounds, call.RoundsInputTokens, call.RoundsOutputTokens, call.RoundsCost = p.Rounds.Count, p.Rounds.InputTokens, p.Rounds.OutputTokens, p.Rounds.CostNanoUSD

	return Parsed{
		DeliveryID: EventType + "#" + strings.ToLower(p.PRKey) + "#" + n.SourceCommit,
		EventType:  EventType,
		Note:       n,
		Call:       call,
		Claims:     claims,
	}, nil
}

// commonDir is the longest directory every path shares, "" when none or
// when they share nothing but the root. One file: its directory.
func commonDir(files []string) string {
	var dirs []string
	for _, f := range files {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		dirs = append(dirs, path.Dir(f))
	}
	if len(dirs) == 0 {
		return ""
	}
	common := strings.Split(dirs[0], "/")
	for _, d := range dirs[1:] {
		parts := strings.Split(d, "/")
		n := 0
		for n < len(common) && n < len(parts) && common[n] == parts[n] {
			n++
		}
		common = common[:n]
	}
	joined := strings.Join(common, "/")
	if joined == "." {
		return ""
	}
	return joined
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return strings.TrimSpace(*p)
}
