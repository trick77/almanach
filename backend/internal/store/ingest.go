package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Delivery is one webhook body as received.
type Delivery struct {
	ID        string
	Team      string
	EventType string
	Payload   json.RawMessage
}

// Claim is one row of a team's ownership; RepoSlug empty is the whole
// project.
type Claim struct {
	ProjectKey string
	RepoSlug   string
}

// Note is one merged PR's note as the sender described it, lifted into
// columns beside the raw object.
type Note struct {
	ProjectKey string
	RepoSlug   string
	PRID       int
	Team       string
	PRKey      string
	PRTitle    string
	TicketKey  string
	IssueType  string
	ChangeKind string
	// Bugfix is the note's bugfix object, nil unless a bugfix; Area and
	// Component are what the problem areas group by, computed at ingest.
	Bugfix          json.RawMessage
	BugfixArea      string
	BugfixComponent string
	Body            json.RawMessage
	ReviewedCommit  string
	SourceCommit    string
	MergeCommit     string
	MergedAt        time.Time
	RunID           int64
	ModelLabel      string
	IssuesPosted    int
	Suggestions     int
}

// NoteCall is what the sender reported about the round's gateway call.
type NoteCall struct {
	RunID      *int64
	Outcome    string
	NoteStatus string
	Profile    string
	Model      string
	Deployment string
	Reasoning  string
	Finish     string
	CallID     string
	GatewayVer string

	InputTokens, InputUncached, CacheRead, CacheWrite, OutputTokens, ReasoningTokens    *int64
	Cost, CostInput, CostCacheRead, CostCacheWrite, CostOutput, CostReasoning, CostTool *int64
	KeySpend, HeadersMS, TotalMS                                                        *int64
	Warnings                                                                            []string

	Rounds             int
	RoundsInputTokens  int64
	RoundsOutputTokens int64
	RoundsCost         *int64
}

// Ingest writes one accepted delivery in a single transaction: the raw body,
// the structure it names, the note and the call. accepted is false when the
// delivery id was seen before (a retry): nothing is written, the first
// delivery stands, as riptide does it.
func (s *Store) Ingest(ctx context.Context, d Delivery, n Note, call *NoteCall, claims []Claim) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var id string
	err = tx.QueryRow(ctx, `INSERT INTO deliveries (delivery_id, team, event_type, payload) VALUES ($1, $2, $3, $4)
		ON CONFLICT (delivery_id) DO NOTHING RETURNING delivery_id`, d.ID, d.Team, d.EventType, []byte(d.Payload)).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	// The structure: upsert, last_seen bumped.
	if _, err := tx.Exec(ctx, `INSERT INTO teams (slug) VALUES ($1) ON CONFLICT (slug) DO UPDATE SET last_seen = now()`, n.Team); err != nil {
		return false, err
	}
	// A project follows the team that last sent for it: a repo that changed
	// hands moves its project's team with it, as noergler's PR rows follow.
	if _, err := tx.Exec(ctx, `INSERT INTO projects (key, team) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET team = EXCLUDED.team, last_seen = now()`, n.ProjectKey, n.Team); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO repos (project_key, slug, team) VALUES ($1, $2, $3)
		ON CONFLICT (project_key, slug) DO UPDATE SET team = EXCLUDED.team, last_seen = now()`, n.ProjectKey, n.RepoSlug, n.Team); err != nil {
		return false, err
	}
	for _, c := range claims {
		var repo *string
		if c.RepoSlug != "" {
			repo = &c.RepoSlug
		}
		if _, err := tx.Exec(ctx, `INSERT INTO claims (team, project_key, repo_slug) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
			n.Team, c.ProjectKey, repo); err != nil {
			return false, err
		}
	}

	// The note. A second note for the same PR (a reopened PR merged again)
	// replaces the first and queues again.
	if _, err := tx.Exec(ctx, `
		INSERT INTO notes (project_key, repo_slug, pr_id, team, pr_key, pr_title, ticket_key, jira_issue_type, change_kind,
		    bugfix, bugfix_area, bugfix_component, note, reviewed_commit, source_commit, merge_commit, merged_at, run_id,
		    model_label, issues_posted, suggestions_posted, delivery_id)
		VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''), NULLIF($8, ''), NULLIF($9, ''),
		    $10, NULLIF($11, ''), NULLIF($12, ''), $13, NULLIF($14, ''), $15, NULLIF($16, ''), $17, NULLIF($18, 0),
		    NULLIF($19, ''), $20, $21, $22)
		ON CONFLICT (project_key, repo_slug, pr_id) DO UPDATE SET
		    team = EXCLUDED.team, pr_key = EXCLUDED.pr_key, pr_title = EXCLUDED.pr_title, ticket_key = EXCLUDED.ticket_key,
		    jira_issue_type = EXCLUDED.jira_issue_type, change_kind = EXCLUDED.change_kind, bugfix = EXCLUDED.bugfix,
		    bugfix_area = EXCLUDED.bugfix_area, bugfix_component = EXCLUDED.bugfix_component, note = EXCLUDED.note,
		    reviewed_commit = EXCLUDED.reviewed_commit, source_commit = EXCLUDED.source_commit,
		    merge_commit = EXCLUDED.merge_commit, merged_at = EXCLUDED.merged_at, run_id = EXCLUDED.run_id,
		    model_label = EXCLUDED.model_label, issues_posted = EXCLUDED.issues_posted,
		    suggestions_posted = EXCLUDED.suggestions_posted, delivery_id = EXCLUDED.delivery_id,
		    received_at = now(), consolidated_at = NULL`,
		n.ProjectKey, n.RepoSlug, n.PRID, n.Team, n.PRKey, n.PRTitle, n.TicketKey, n.IssueType, n.ChangeKind,
		nullJSON(n.Bugfix), n.BugfixArea, n.BugfixComponent, []byte(n.Body), n.ReviewedCommit, n.SourceCommit, n.MergeCommit,
		n.MergedAt.UTC(), n.RunID, n.ModelLabel, n.IssuesPosted, n.Suggestions, d.ID); err != nil {
		return false, err
	}

	if call != nil {
		c := call
		if _, err := tx.Exec(ctx, `
			INSERT INTO note_calls (delivery_id, team, project_key, repo_slug, pr_id, run_id, outcome, note_status,
			    profile, model, deployment, reasoning_sent, finish_reason, call_id, litellm_version,
			    input_tokens, input_uncached_tokens, cache_read_tokens, cache_write_tokens, output_tokens, reasoning_tokens,
			    cost_nano_usd, cost_input_nano_usd, cost_cache_read_nano_usd, cost_cache_write_nano_usd,
			    cost_output_nano_usd, cost_reasoning_nano_usd, cost_tool_usage_nano_usd,
			    key_spend_nano_usd, headers_ms, total_ms, warnings,
			    rounds, rounds_input_tokens, rounds_output_tokens, rounds_cost_nano_usd)
			VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''), NULLIF($8, ''),
			    NULLIF($9, ''), NULLIF($10, ''), NULLIF($11, ''), NULLIF($12, ''), NULLIF($13, ''), NULLIF($14, ''), NULLIF($15, ''),
			    $16, $17, $18, $19, $20, $21,
			    $22, $23, $24, $25, $26, $27, $28,
			    $29, $30, $31, $32,
			    $33, $34, $35, $36)`,
			d.ID, n.Team, n.ProjectKey, n.RepoSlug, n.PRID, c.RunID, c.Outcome, c.NoteStatus,
			c.Profile, c.Model, c.Deployment, c.Reasoning, c.Finish, c.CallID, c.GatewayVer,
			c.InputTokens, c.InputUncached, c.CacheRead, c.CacheWrite, c.OutputTokens, c.ReasoningTokens,
			c.Cost, c.CostInput, c.CostCacheRead, c.CostCacheWrite, c.CostOutput, c.CostReasoning, c.CostTool,
			c.KeySpend, c.HeadersMS, c.TotalMS, orEmpty(c.Warnings),
			c.Rounds, c.RoundsInputTokens, c.RoundsOutputTokens, c.RoundsCost); err != nil {
			return false, err
		}
	}
	return true, tx.Commit(ctx)
}

func nullJSON(b json.RawMessage) any {
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	return []byte(b)
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
