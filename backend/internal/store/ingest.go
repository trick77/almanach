package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
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
	accepted := false
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		ok, err := writeDelivery(ctx, tx, d, n, call, claims)
		accepted = ok
		return err
	})
	if err != nil {
		return false, err
	}
	return accepted, nil
}

// writeDelivery is Ingest inside its transaction.
func writeDelivery(ctx context.Context, tx *sql.Tx, d Delivery, n Note, call *NoteCall, claims []Claim) (bool, error) {
	now := nowStamp()
	var id string
	err := tx.QueryRowContext(ctx, `INSERT INTO deliveries (delivery_id, team, event_type, received_at, payload) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (delivery_id) DO NOTHING RETURNING delivery_id`, d.ID, d.Team, d.EventType, now, jsonText(d.Payload)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	// The structure: upsert, last_seen bumped.
	if _, err := tx.ExecContext(ctx, `INSERT INTO teams (slug, first_seen, last_seen) VALUES (?1, ?2, ?2)
		ON CONFLICT (slug) DO UPDATE SET last_seen = EXCLUDED.last_seen`, n.Team, now); err != nil {
		return false, err
	}
	// A project follows the team that last sent for it: a repo that changed
	// hands moves its project's team with it, as noergler's PR rows follow.
	// A name no project or repository ever had registers a new one; an old
	// name lands on the row that had it.
	pid, err := ensureProject(ctx, tx, n.ProjectKey)
	if err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE projects SET team = ?2, first_seen = COALESCE(first_seen, ?3), last_seen = ?3 WHERE id = ?1`,
		pid, n.Team, now); err != nil {
		return false, err
	}
	rid, err := ensureRepo(ctx, tx, pid, n.RepoSlug)
	if err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE repos SET team = ?2, first_seen = COALESCE(first_seen, ?3), last_seen = ?3 WHERE id = ?1`,
		rid, n.Team, now); err != nil {
		return false, err
	}
	// Claims are kept as noergler sent them, names and all, never joined
	// on: a claim of a repository Almanach has not met registers nothing
	// (a registered name would block the rename that later brings it).
	for _, c := range claims {
		var repo *string
		if c.RepoSlug != "" {
			repo = &c.RepoSlug
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO claims (team, project_key, repo_slug, seen_at) VALUES (?, ?, ?, ?) ON CONFLICT DO NOTHING`,
			n.Team, c.ProjectKey, repo, now); err != nil {
			return false, err
		}
	}

	// The note. A second note for the same PR (a reopened PR merged again)
	// replaces the first and queues again.
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO notes (repo_id, pr_id, team, pr_key, pr_title, ticket_key, jira_issue_type, change_kind,
		    bugfix, bugfix_area, bugfix_component, note, reviewed_commit, source_commit, merge_commit, merged_at, run_id,
		    model_label, issues_posted, suggestions_posted, delivery_id, received_at)
		VALUES (?, ?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''),
		    ?, NULLIF(?, ''), NULLIF(?, ''), ?, NULLIF(?, ''), ?, NULLIF(?, ''), ?, NULLIF(?, 0),
		    NULLIF(?, ''), ?, ?, ?, ?)
		ON CONFLICT (repo_id, pr_id) DO UPDATE SET
		    team = EXCLUDED.team, pr_key = EXCLUDED.pr_key, pr_title = EXCLUDED.pr_title, ticket_key = EXCLUDED.ticket_key,
		    jira_issue_type = EXCLUDED.jira_issue_type, change_kind = EXCLUDED.change_kind, bugfix = EXCLUDED.bugfix,
		    bugfix_area = EXCLUDED.bugfix_area, bugfix_component = EXCLUDED.bugfix_component, note = EXCLUDED.note,
		    reviewed_commit = EXCLUDED.reviewed_commit, source_commit = EXCLUDED.source_commit,
		    merge_commit = EXCLUDED.merge_commit, merged_at = EXCLUDED.merged_at, run_id = EXCLUDED.run_id,
		    model_label = EXCLUDED.model_label, issues_posted = EXCLUDED.issues_posted,
		    suggestions_posted = EXCLUDED.suggestions_posted, delivery_id = EXCLUDED.delivery_id,
		    received_at = EXCLUDED.received_at, consolidated_at = NULL, fold_attempts = 0`,
		rid, n.PRID, n.Team, n.PRKey, n.PRTitle, n.TicketKey, n.IssueType, n.ChangeKind,
		jsonOrNull(n.Bugfix), n.BugfixArea, n.BugfixComponent, jsonText(n.Body), n.ReviewedCommit, n.SourceCommit, n.MergeCommit,
		stamp(n.MergedAt), n.RunID, n.ModelLabel, n.IssuesPosted, n.Suggestions, d.ID, now); err != nil {
		return false, err
	}

	if call != nil {
		c := call
		warnings, err := jsonOf(orEmpty(c.Warnings))
		if err != nil {
			return false, err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO note_calls (delivery_id, team, repo_id, pr_id, run_id, outcome, note_status,
			    profile, model, deployment, reasoning_sent, finish_reason, call_id, litellm_version,
			    input_tokens, input_uncached_tokens, cache_read_tokens, cache_write_tokens, output_tokens, reasoning_tokens,
			    cost_nano_usd, cost_input_nano_usd, cost_cache_read_nano_usd, cost_cache_write_nano_usd,
			    cost_output_nano_usd, cost_reasoning_nano_usd, cost_tool_usage_nano_usd,
			    key_spend_nano_usd, headers_ms, total_ms, warnings,
			    rounds, rounds_input_tokens, rounds_output_tokens, rounds_cost_nano_usd, created_at)
			VALUES (?, ?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''),
			    NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''),
			    ?, ?, ?, ?, ?, ?,
			    ?, ?, ?, ?, ?, ?, ?,
			    ?, ?, ?, ?,
			    ?, ?, ?, ?, ?)`,
			d.ID, n.Team, rid, n.PRID, c.RunID, c.Outcome, c.NoteStatus,
			c.Profile, c.Model, c.Deployment, c.Reasoning, c.Finish, c.CallID, c.GatewayVer,
			c.InputTokens, c.InputUncached, c.CacheRead, c.CacheWrite, c.OutputTokens, c.ReasoningTokens,
			c.Cost, c.CostInput, c.CostCacheRead, c.CostCacheWrite, c.CostOutput, c.CostReasoning, c.CostTool,
			c.KeySpend, c.HeadersMS, c.TotalMS, warnings,
			c.Rounds, c.RoundsInputTokens, c.RoundsOutputTokens, c.RoundsCost, now); err != nil {
			return false, err
		}
	}
	return true, nil
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
