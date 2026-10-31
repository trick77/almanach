-- Almanach's schema. Everything noergler sends is kept raw (deliveries),
-- lifted into rows the wiki is built from (teams, projects, repos, claims,
-- notes, note_calls), and consolidated into descriptions (revisions, pages).
-- The database records what happened; the descriptions are rewritten by the
-- merge call, revision by revision, never edited by hand.
--
-- SQLite, one file. Every time is TEXT, UTC, RFC 3339 with all nine digits
-- of the nanoseconds (2026-10-01T12:00:00.000000000Z): fixed width, so text
-- order is time order. The store writes every one; the schema has no clock
-- (no DEFAULT on a time column). JSON is TEXT that json_valid accepts; a
-- NULL passes a CHECK. Money is nano-USD in an INTEGER (64-bit).

-- Every webhook body, whole, keyed by the sender's dedup key. Append-only:
-- a retry is a no-op, never an update.
CREATE TABLE deliveries (
    delivery_id TEXT PRIMARY KEY,
    team TEXT NOT NULL,
    event_type TEXT NOT NULL,
    received_at TEXT NOT NULL,
    payload TEXT NOT NULL CHECK (json_valid(payload))
);
CREATE INDEX deliveries_team_idx ON deliveries (team, received_at);

-- The Bitbucket structure as noergler knows it, rebuilt from every payload:
-- the team the bearer named, the project and repo the note belongs to, and
-- the team's claims on the project (a NULL repo_slug is the whole project).
CREATE TABLE teams (
    slug TEXT PRIMARY KEY,
    first_seen TEXT NOT NULL,
    last_seen TEXT NOT NULL
);
CREATE TABLE projects (
    key TEXT PRIMARY KEY,
    team TEXT NOT NULL REFERENCES teams(slug),
    first_seen TEXT NOT NULL,
    last_seen TEXT NOT NULL
);
CREATE TABLE repos (
    project_key TEXT NOT NULL REFERENCES projects(key),
    slug TEXT NOT NULL,
    team TEXT NOT NULL REFERENCES teams(slug),
    first_seen TEXT NOT NULL,
    last_seen TEXT NOT NULL,
    PRIMARY KEY (project_key, slug)
);
CREATE TABLE claims (
    team TEXT NOT NULL REFERENCES teams(slug),
    project_key TEXT NOT NULL,
    repo_slug TEXT,
    seen_at TEXT NOT NULL
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
    bugfix TEXT CHECK (json_valid(bugfix)),
    bugfix_area TEXT,
    bugfix_component TEXT,
    note TEXT NOT NULL CHECK (json_valid(note)),
    reviewed_commit TEXT,
    source_commit TEXT NOT NULL,
    merge_commit TEXT,
    merged_at TEXT NOT NULL,
    run_id INTEGER,
    model_label TEXT,
    issues_posted INTEGER NOT NULL DEFAULT 0,
    suggestions_posted INTEGER NOT NULL DEFAULT 0,
    delivery_id TEXT NOT NULL REFERENCES deliveries(delivery_id),
    received_at TEXT NOT NULL,
    -- Set by the worker once the note is folded into the descriptions (or
    -- the fold was refused and logged); NULL = still queued.
    consolidated_at TEXT,
    -- A fold the gateway failed (no answer, an error) is retried a bounded
    -- number of times before the note is taken off the queue for good. A
    -- wrong key must not consume every note that arrives before someone
    -- reads the log.
    fold_attempts INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (project_key, repo_slug, pr_id)
);
CREATE INDEX notes_queue_idx ON notes (received_at) WHERE consolidated_at IS NULL;
CREATE INDEX notes_repo_idx ON notes (project_key, repo_slug, merged_at DESC);
CREATE INDEX notes_project_idx ON notes (project_key, merged_at DESC);

-- What noergler reported about the round's note call, every lane, plus the
-- PR's round totals: the cache-hit rate and the cost per note are one query.
-- warnings is a JSON array of strings.
CREATE TABLE note_calls (
    id INTEGER PRIMARY KEY,
    delivery_id TEXT NOT NULL REFERENCES deliveries(delivery_id),
    team TEXT NOT NULL,
    project_key TEXT NOT NULL,
    repo_slug TEXT NOT NULL,
    pr_id INTEGER NOT NULL,
    run_id INTEGER,
    outcome TEXT,
    note_status TEXT,
    profile TEXT,
    model TEXT,
    deployment TEXT,
    reasoning_sent TEXT,
    finish_reason TEXT,
    call_id TEXT,
    litellm_version TEXT,
    input_tokens INTEGER,
    input_uncached_tokens INTEGER,
    cache_read_tokens INTEGER,
    cache_write_tokens INTEGER,
    output_tokens INTEGER,
    reasoning_tokens INTEGER,
    cost_nano_usd INTEGER,
    cost_input_nano_usd INTEGER,
    cost_cache_read_nano_usd INTEGER,
    cost_cache_write_nano_usd INTEGER,
    cost_output_nano_usd INTEGER,
    cost_reasoning_nano_usd INTEGER,
    cost_tool_usage_nano_usd INTEGER,
    key_spend_nano_usd INTEGER,
    headers_ms INTEGER,
    total_ms INTEGER,
    warnings TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(warnings)),
    rounds INTEGER NOT NULL DEFAULT 0,
    rounds_input_tokens INTEGER NOT NULL DEFAULT 0,
    rounds_output_tokens INTEGER NOT NULL DEFAULT 0,
    rounds_cost_nano_usd INTEGER,
    created_at TEXT NOT NULL
);
CREATE INDEX note_calls_team_idx ON note_calls (team, created_at);

-- The descriptions, one revision per fold: a short summary and the
-- statements (components, rules, invariants, entities), each with the
-- sources code attached. History is kept; rollback is repointing the page.
--
-- Provenance has a kind: a revision comes from a PR's note or from a
-- baseline run at a commit; exactly one of source_pr_key and source_commit
-- is set. baseline_run_id carries no foreign key, like llm_call_id: the
-- ledger outlives a removed repository.
--
-- map is the project's system map: rongo's flow form (ui/src/diagramIR.ts)
-- as the baseline project step returned it and baseline/diagram.go
-- validated it. Project pages only; a statement's diagram lives inside its
-- statement. NULL until a baseline project step writes one; the merge path
-- carries it forward.
CREATE TABLE revisions (
    id INTEGER PRIMARY KEY,
    scope TEXT NOT NULL CHECK (scope IN ('repository', 'project')),
    project_key TEXT NOT NULL,
    repo_slug TEXT,
    revision INTEGER NOT NULL,
    summary TEXT NOT NULL,
    statements TEXT NOT NULL CHECK (json_valid(statements)),
    chars INTEGER NOT NULL,
    source_kind TEXT NOT NULL DEFAULT 'pr' CHECK (source_kind IN ('pr', 'baseline')),
    source_pr_key TEXT,
    source_commit TEXT,
    llm_call_id INTEGER,
    baseline_run_id INTEGER,
    map TEXT CHECK (json_valid(map)),
    created_at TEXT NOT NULL,
    CHECK ((scope = 'project') = (repo_slug IS NULL)),
    CONSTRAINT revisions_source_pr CHECK ((source_kind = 'pr') = (source_pr_key IS NOT NULL)),
    CONSTRAINT revisions_source_commit CHECK ((source_kind = 'baseline') = (source_commit IS NOT NULL)),
    CONSTRAINT revisions_map_scope CHECK (map IS NULL OR scope = 'project')
);
CREATE UNIQUE INDEX revisions_key_idx ON revisions (scope, project_key, COALESCE(repo_slug, ''), revision);

-- The current revision per page. '' is the project scope's repo_slug, so
-- the key is one PK.
CREATE TABLE pages (
    scope TEXT NOT NULL,
    project_key TEXT NOT NULL,
    repo_slug TEXT NOT NULL DEFAULT '',
    current_revision_id INTEGER NOT NULL REFERENCES revisions(id),
    updated_at TEXT NOT NULL,
    PRIMARY KEY (scope, project_key, repo_slug)
);

-- Almanach's own gateway calls: a note's merge or a baseline run's step.
-- Its ledger, nobody else's. purpose says which step of which flow made the
-- call; the source is a PR or a run's commit, as on a revision.
CREATE TABLE llm_calls (
    id INTEGER PRIMARY KEY,
    project_key TEXT NOT NULL,
    repo_slug TEXT NOT NULL,
    source_kind TEXT NOT NULL DEFAULT 'pr' CHECK (source_kind IN ('pr', 'baseline')),
    source_pr_key TEXT,
    source_commit TEXT,
    purpose TEXT NOT NULL DEFAULT 'merge' CHECK (purpose IN ('merge', 'catalogue', 'overview', 'subject', 'project')),
    baseline_run_id INTEGER,
    outcome TEXT NOT NULL,
    model TEXT,
    input_tokens INTEGER,
    cached_tokens INTEGER,
    output_tokens INTEGER,
    cost_nano_usd INTEGER,
    call_id TEXT,
    created_at TEXT NOT NULL,
    CONSTRAINT llm_calls_source_pr CHECK ((source_kind = 'pr') = (source_pr_key IS NOT NULL)),
    CONSTRAINT llm_calls_source_commit CHECK ((source_kind = 'baseline') = (source_commit IS NOT NULL))
);
CREATE INDEX llm_calls_run_idx ON llm_calls (baseline_run_id) WHERE baseline_run_id IS NOT NULL;

-- The sources: the projects whose code Almanach clones itself, apart from
-- the noergler-fed structure (projects, repos carry a team and are upserted
-- from deliveries). A source is a Bitbucket project, whose repositories the
-- REST API lists, or a static parent folder, whose repositories the operator
-- enters by hand. Either way a clone lands at <base>/<project_key>/<slug>.
-- The token is encrypted at rest (AES-GCM under ALMANACH_SECRET_KEY) and
-- never leaves the store in clear except to the git process.
-- auto_describe: the first run on a clone's first ready and the monthly
-- reconcile happen on their own; off, only the buttons start a run.
CREATE TABLE sources (
    project_key TEXT PRIMARY KEY,
    kind TEXT NOT NULL CHECK (kind IN ('bitbucket', 'static')),
    host TEXT,
    token_auth TEXT NOT NULL CHECK (token_auth IN ('bearer', 'basic')),
    token_user TEXT NOT NULL DEFAULT '',
    token_enc BLOB,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    last_listed_at TEXT,
    last_error TEXT NOT NULL DEFAULT '',
    auto_describe BOOLEAN NOT NULL DEFAULT 1 CHECK (auto_describe IN (0, 1))
);

-- One row per clone (bare: an object store, no working tree). origin says
-- who put it here (the listing or the operator); state is the poller's:
-- pending (queued for the next cycle), syncing, ready (the clone's
-- remote-tracking ref of branch is head_commit), failed
-- (last_error says why, retried next cycle), missing (the listing no longer
-- names it; the checkout stays until the operator removes it). A NULL
-- branch is resolved from the remote's HEAD and written back.
CREATE TABLE source_repos (
    project_key TEXT NOT NULL REFERENCES sources(project_key) ON DELETE CASCADE,
    slug TEXT NOT NULL,
    clone_url TEXT NOT NULL,
    origin TEXT NOT NULL CHECK (origin IN ('listed', 'manual')),
    branch TEXT,
    head_commit TEXT NOT NULL DEFAULT '',
    state TEXT NOT NULL CHECK (state IN ('pending', 'syncing', 'ready', 'failed', 'missing')),
    last_synced_at TEXT,
    last_error TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    PRIMARY KEY (project_key, slug)
);

-- The baseline: Almanach reads a cloned repository at a commit and writes
-- the description it should have had from the start; the PR notes refine it
-- from then on. A run documents one repository at one commit, pinned when
-- the run starts, one model call per step, so the worker can interleave
-- steps with notes, hold after a gateway failure and resume after a
-- restart.
--
-- mode: full reads the repository with a fresh catalogue, reconcile reads
-- what changed since base_commit (the documented commit). commit is the
-- head when the run was queued until the run starts and pins it. plan is the
-- catalogue: the subjects with their files and sizes. attempts counts the
-- gateway failures of the current step; calls and bytes are what the run
-- spent against its caps; skipped counts the subjects it could not fold.
-- trigger retry: the poller queues a failed run again, in the failed run's
-- mode, so the log and the source page tell a retry from a first ready or a
-- monthly reconcile.
--
-- "commit" is quoted wherever it is named: a keyword in SQLite.
--
-- AUTOINCREMENT: a run's id is never reused, not even after its repository
-- (and so the newest run row) is removed. llm_calls and revisions keep the
-- id without a foreign key; a reused one would hand a new run the old one's
-- calls and cost.
CREATE TABLE baseline_runs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    project_key TEXT NOT NULL,
    slug TEXT NOT NULL,
    mode TEXT NOT NULL CHECK (mode IN ('full', 'reconcile')),
    trigger TEXT NOT NULL CHECK (trigger IN ('manual', 'first_ready', 'monthly', 'retry')),
    base_commit TEXT NOT NULL DEFAULT '',
    "commit" TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('queued', 'cataloguing', 'describing', 'project', 'done', 'failed')),
    step INTEGER NOT NULL DEFAULT 0,
    steps INTEGER NOT NULL DEFAULT 0,
    plan TEXT CHECK (json_valid(plan)),
    attempts INTEGER NOT NULL DEFAULT 0,
    calls INTEGER NOT NULL DEFAULT 0,
    bytes INTEGER NOT NULL DEFAULT 0,
    skipped INTEGER NOT NULL DEFAULT 0,
    error TEXT NOT NULL DEFAULT '',
    queued_at TEXT NOT NULL,
    started_at TEXT,
    finished_at TEXT,
    FOREIGN KEY (project_key, slug) REFERENCES source_repos (project_key, slug) ON DELETE CASCADE,
    CHECK (mode = 'full' OR base_commit <> '')
);
-- One open run per repository: a second click is a conflict, not a second
-- writer of the same page.
CREATE UNIQUE INDEX baseline_runs_open_idx ON baseline_runs (project_key, slug) WHERE state NOT IN ('done', 'failed');
CREATE INDEX baseline_runs_repo_idx ON baseline_runs (project_key, slug, id DESC);
