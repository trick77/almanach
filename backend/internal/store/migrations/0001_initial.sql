-- Almanach's schema. Everything noergler sends is kept raw (deliveries),
-- lifted into rows the wiki is built from (teams, projects, repos, claims,
-- notes, note_calls), and consolidated into descriptions (revisions, pages).
-- The database records what happened; the descriptions are rewritten by the
-- merge call, revision by revision, never edited by hand.

-- Every webhook body, whole, keyed by the sender's dedup key. Append-only:
-- a retry is a no-op, never an update.
CREATE TABLE deliveries (
    delivery_id TEXT PRIMARY KEY,
    team TEXT NOT NULL,
    event_type TEXT NOT NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    payload JSONB NOT NULL
);
CREATE INDEX deliveries_team_idx ON deliveries (team, received_at);

-- The Bitbucket structure as noergler knows it, rebuilt from every payload:
-- the team the bearer named, the project and repo the note belongs to, and
-- the team's claims on the project (a NULL repo_slug is the whole project).
CREATE TABLE teams (
    slug TEXT PRIMARY KEY,
    first_seen TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE projects (
    key TEXT PRIMARY KEY,
    team TEXT NOT NULL REFERENCES teams(slug),
    first_seen TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE repos (
    project_key TEXT NOT NULL REFERENCES projects(key),
    slug TEXT NOT NULL,
    team TEXT NOT NULL REFERENCES teams(slug),
    first_seen TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (project_key, slug)
);
CREATE TABLE claims (
    team TEXT NOT NULL REFERENCES teams(slug),
    project_key TEXT NOT NULL,
    repo_slug TEXT,
    seen_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX claims_repo_idx ON claims (team, project_key, repo_slug) WHERE repo_slug IS NOT NULL;
CREATE UNIQUE INDEX claims_project_idx ON claims (team, project_key) WHERE repo_slug IS NULL;

-- One note per merged PR, as noergler's last review round wrote it. The
-- note object is the model's; the columns beside it are facts. bugfix_area
-- is the longest common directory of the bugfix's component files, computed
-- at ingest: the problem areas group by it, never by the model's wording.
CREATE TABLE notes (
    project_key TEXT NOT NULL,
    repo_slug TEXT NOT NULL,
    pr_id INTEGER NOT NULL,
    team TEXT NOT NULL REFERENCES teams(slug),
    pr_key TEXT NOT NULL,
    pr_title TEXT NOT NULL,
    ticket_key TEXT,
    jira_issue_type TEXT,
    change_kind TEXT,
    bugfix JSONB,
    bugfix_area TEXT,
    bugfix_component TEXT,
    note JSONB NOT NULL,
    reviewed_commit TEXT,
    source_commit TEXT NOT NULL,
    merge_commit TEXT,
    merged_at TIMESTAMPTZ NOT NULL,
    run_id BIGINT,
    model_label TEXT,
    issues_posted INTEGER NOT NULL DEFAULT 0,
    suggestions_posted INTEGER NOT NULL DEFAULT 0,
    delivery_id TEXT NOT NULL REFERENCES deliveries(delivery_id),
    received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Set by the worker once the note is folded into the descriptions (or
    -- the fold was refused and logged); NULL = still queued.
    consolidated_at TIMESTAMPTZ,
    PRIMARY KEY (project_key, repo_slug, pr_id)
);
CREATE INDEX notes_queue_idx ON notes (received_at) WHERE consolidated_at IS NULL;
CREATE INDEX notes_repo_idx ON notes (project_key, repo_slug, merged_at DESC);
CREATE INDEX notes_project_idx ON notes (project_key, merged_at DESC);

-- What noergler reported about the round's note call, every lane, plus the
-- PR's round totals: the cache-hit rate and the cost per note are one query.
CREATE TABLE note_calls (
    id BIGSERIAL PRIMARY KEY,
    delivery_id TEXT NOT NULL REFERENCES deliveries(delivery_id),
    team TEXT NOT NULL,
    project_key TEXT NOT NULL,
    repo_slug TEXT NOT NULL,
    pr_id INTEGER NOT NULL,
    run_id BIGINT,
    outcome TEXT,
    note_status TEXT,
    profile TEXT,
    model TEXT,
    deployment TEXT,
    reasoning_sent TEXT,
    finish_reason TEXT,
    call_id TEXT,
    litellm_version TEXT,
    input_tokens BIGINT,
    input_uncached_tokens BIGINT,
    cache_read_tokens BIGINT,
    cache_write_tokens BIGINT,
    output_tokens BIGINT,
    reasoning_tokens BIGINT,
    cost_nano_usd BIGINT,
    cost_input_nano_usd BIGINT,
    cost_cache_read_nano_usd BIGINT,
    cost_cache_write_nano_usd BIGINT,
    cost_output_nano_usd BIGINT,
    cost_reasoning_nano_usd BIGINT,
    cost_tool_usage_nano_usd BIGINT,
    key_spend_nano_usd BIGINT,
    headers_ms BIGINT,
    total_ms BIGINT,
    warnings TEXT[] NOT NULL DEFAULT '{}',
    rounds INTEGER NOT NULL DEFAULT 0,
    rounds_input_tokens BIGINT NOT NULL DEFAULT 0,
    rounds_output_tokens BIGINT NOT NULL DEFAULT 0,
    rounds_cost_nano_usd BIGINT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX note_calls_team_idx ON note_calls (team, created_at);

-- The descriptions, one revision per fold: a short summary and the
-- statements (components, rules, invariants, entities), each with the
-- sources code attached. History is kept; rollback is repointing the page.
CREATE TABLE revisions (
    id BIGSERIAL PRIMARY KEY,
    scope TEXT NOT NULL CHECK (scope IN ('repository', 'project')),
    project_key TEXT NOT NULL,
    repo_slug TEXT,
    revision INTEGER NOT NULL,
    summary TEXT NOT NULL,
    statements JSONB NOT NULL,
    chars INTEGER NOT NULL,
    source_pr_key TEXT NOT NULL,
    llm_call_id BIGINT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((scope = 'project') = (repo_slug IS NULL))
);
CREATE UNIQUE INDEX revisions_key_idx ON revisions (scope, project_key, COALESCE(repo_slug, ''), revision);

-- The current revision per page. '' is the project scope's repo_slug, so
-- the key is one PK.
CREATE TABLE pages (
    scope TEXT NOT NULL,
    project_key TEXT NOT NULL,
    repo_slug TEXT NOT NULL DEFAULT '',
    current_revision_id BIGINT NOT NULL REFERENCES revisions(id),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (scope, project_key, repo_slug)
);

-- Almanach's own gateway calls: the merge per note. Its ledger, nobody
-- else's.
CREATE TABLE llm_calls (
    id BIGSERIAL PRIMARY KEY,
    project_key TEXT NOT NULL,
    repo_slug TEXT NOT NULL,
    source_pr_key TEXT NOT NULL,
    outcome TEXT NOT NULL,
    model TEXT,
    input_tokens BIGINT,
    cached_tokens BIGINT,
    output_tokens BIGINT,
    cost_nano_usd BIGINT,
    call_id TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
