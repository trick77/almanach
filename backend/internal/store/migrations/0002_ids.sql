-- A project and a repository are ids; a key and a slug are names. Before,
-- every table carried (project_key, slug) as its key, so a rename rewrote
-- them all. From here, projects and repos hold the id and the one current
-- name; project_names and repo_names map every name ever used to its id, so
-- a note, a link or a listing entry under an old name lands on the same
-- row. A name is never given to another project or repository (user,
-- 2026-11-01). Every other table points at an id.
--
-- SQLite cannot change a primary key in place: each table is built anew as
-- <name>_new, filled from the old one, the old ones dropped children first
-- (a parent dropped first would cascade into its children), and the new
-- ones renamed, which rewrites the references between them. Foreign keys
-- are checked at commit; the migration runs in one transaction, so a
-- failure anywhere leaves the database on 0001.
--
-- Nothing in a text or JSON column is touched: statements, summaries, maps
-- and PR keys are copied byte for byte. A PR key keeps the name the PR was
-- merged under.
PRAGMA defer_foreign_keys = ON;

-- The project: id, current key, noergler's team and times. Team and times
-- are NULL for a project only a source knows.
CREATE TABLE projects_new (
    id INTEGER PRIMARY KEY,
    key TEXT NOT NULL UNIQUE,
    team TEXT REFERENCES teams(slug),
    first_seen TEXT CHECK (first_seen GLOB '????-??-??T??:??:??.?????????Z'),
    last_seen TEXT CHECK (last_seen GLOB '????-??-??T??:??:??.?????????Z')
);
INSERT INTO projects_new (key, team, first_seen, last_seen)
SELECT k.key, p.team, p.first_seen, p.last_seen
  FROM (SELECT key FROM projects
        UNION SELECT project_key FROM sources
        UNION SELECT project_key FROM source_repos
        UNION SELECT project_key FROM baseline_runs
        UNION SELECT project_key FROM repos
        UNION SELECT project_key FROM notes
        UNION SELECT project_key FROM note_calls
        UNION SELECT project_key FROM revisions
        UNION SELECT project_key FROM pages
        UNION SELECT project_key FROM llm_calls) k
  LEFT JOIN projects p ON p.key = k.key
 ORDER BY k.key;

-- Every key a project was ever known by, the current one included.
CREATE TABLE project_names (
    key TEXT PRIMARY KEY,
    project_id INTEGER NOT NULL REFERENCES projects_new(id)
);
CREATE INDEX project_names_project_idx ON project_names (project_id);
INSERT INTO project_names (key, project_id) SELECT key, id FROM projects_new;

-- The repository: id, project, current slug, the forge's own id (Bitbucket's
-- repository id, which survives a rename; NULL until a listing names it, and
-- for a hand-entered repository), noergler's team and times. A slug only the
-- ledgers know (a removed clone) keeps its id: the ledger outlives a removed
-- repository.
CREATE TABLE repos_new (
    id INTEGER PRIMARY KEY,
    project_id INTEGER NOT NULL REFERENCES projects_new(id),
    slug TEXT NOT NULL,
    forge_id INTEGER,
    team TEXT REFERENCES teams(slug),
    first_seen TEXT CHECK (first_seen GLOB '????-??-??T??:??:??.?????????Z'),
    last_seen TEXT CHECK (last_seen GLOB '????-??-??T??:??:??.?????????Z'),
    UNIQUE (project_id, slug),
    UNIQUE (project_id, forge_id)
);
INSERT INTO repos_new (project_id, slug, team, first_seen, last_seen)
SELECT p.id, k.slug, r.team, r.first_seen, r.last_seen
  FROM (SELECT project_key, slug FROM repos
        UNION SELECT project_key, slug FROM source_repos
        UNION SELECT project_key, slug FROM baseline_runs
        UNION SELECT project_key, repo_slug FROM notes
        UNION SELECT project_key, repo_slug FROM note_calls
        UNION SELECT project_key, repo_slug FROM revisions WHERE repo_slug IS NOT NULL
        UNION SELECT project_key, repo_slug FROM pages WHERE repo_slug <> ''
        UNION SELECT project_key, repo_slug FROM llm_calls) k
  JOIN projects_new p ON p.key = k.project_key
  LEFT JOIN repos r ON r.project_key = k.project_key AND r.slug = k.slug
 ORDER BY p.id, k.slug;

-- Every slug a repository was ever known by within its project, the current
-- one included.
CREATE TABLE repo_names (
    project_id INTEGER NOT NULL REFERENCES projects_new(id),
    slug TEXT NOT NULL,
    repo_id INTEGER NOT NULL REFERENCES repos_new(id),
    PRIMARY KEY (project_id, slug)
);
CREATE INDEX repo_names_repo_idx ON repo_names (repo_id);
INSERT INTO repo_names (project_id, slug, repo_id) SELECT project_id, slug, id FROM repos_new;

-- (project_key, slug) to repo id, for the copies below.
CREATE TEMP TABLE repo_map AS
SELECT p.key AS project_key, r.slug, r.id AS repo_id, p.id AS project_id
  FROM repos_new r JOIN projects_new p ON p.id = r.project_id;
CREATE UNIQUE INDEX temp.repo_map_idx ON repo_map (project_key, slug);

-- claims stay as they are: noergler's names as it sent them, like the
-- deliveries, never joined on. Kept by name, a claim of a repository
-- Almanach has not met registers nothing.

CREATE TABLE notes_new (
    repo_id INTEGER NOT NULL REFERENCES repos_new(id),
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
    merged_at TEXT NOT NULL CHECK (merged_at GLOB '????-??-??T??:??:??.?????????Z'),
    run_id INTEGER,
    model_label TEXT,
    issues_posted INTEGER NOT NULL DEFAULT 0,
    suggestions_posted INTEGER NOT NULL DEFAULT 0,
    delivery_id TEXT NOT NULL REFERENCES deliveries(delivery_id),
    received_at TEXT NOT NULL CHECK (received_at GLOB '????-??-??T??:??:??.?????????Z'),
    consolidated_at TEXT CHECK (consolidated_at GLOB '????-??-??T??:??:??.?????????Z'),
    fold_attempts INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (repo_id, pr_id)
);
INSERT INTO notes_new (repo_id, pr_id, team, pr_key, pr_title, ticket_key, jira_issue_type, change_kind, bugfix,
                       bugfix_area, bugfix_component, note, reviewed_commit, source_commit, merge_commit, merged_at,
                       run_id, model_label, issues_posted, suggestions_posted, delivery_id, received_at,
                       consolidated_at, fold_attempts)
SELECT m.repo_id, n.pr_id, n.team, n.pr_key, n.pr_title, n.ticket_key, n.jira_issue_type, n.change_kind, n.bugfix,
       n.bugfix_area, n.bugfix_component, n.note, n.reviewed_commit, n.source_commit, n.merge_commit, n.merged_at,
       n.run_id, n.model_label, n.issues_posted, n.suggestions_posted, n.delivery_id, n.received_at,
       n.consolidated_at, n.fold_attempts
  FROM notes n JOIN repo_map m ON m.project_key = n.project_key AND m.slug = n.repo_slug;

CREATE TABLE note_calls_new (
    id INTEGER PRIMARY KEY,
    delivery_id TEXT NOT NULL REFERENCES deliveries(delivery_id),
    team TEXT NOT NULL,
    repo_id INTEGER NOT NULL REFERENCES repos_new(id),
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
    created_at TEXT NOT NULL CHECK (created_at GLOB '????-??-??T??:??:??.?????????Z')
);
INSERT INTO note_calls_new (id, delivery_id, team, repo_id, pr_id, run_id, outcome, note_status, profile, model,
                            deployment, reasoning_sent, finish_reason, call_id, litellm_version, input_tokens,
                            input_uncached_tokens, cache_read_tokens, cache_write_tokens, output_tokens,
                            reasoning_tokens, cost_nano_usd, cost_input_nano_usd, cost_cache_read_nano_usd,
                            cost_cache_write_nano_usd, cost_output_nano_usd, cost_reasoning_nano_usd,
                            cost_tool_usage_nano_usd, key_spend_nano_usd, headers_ms, total_ms, warnings, rounds,
                            rounds_input_tokens, rounds_output_tokens, rounds_cost_nano_usd, created_at)
SELECT c.id, c.delivery_id, c.team, m.repo_id, c.pr_id, c.run_id, c.outcome, c.note_status, c.profile, c.model,
       c.deployment, c.reasoning_sent, c.finish_reason, c.call_id, c.litellm_version, c.input_tokens,
       c.input_uncached_tokens, c.cache_read_tokens, c.cache_write_tokens, c.output_tokens,
       c.reasoning_tokens, c.cost_nano_usd, c.cost_input_nano_usd, c.cost_cache_read_nano_usd,
       c.cost_cache_write_nano_usd, c.cost_output_nano_usd, c.cost_reasoning_nano_usd,
       c.cost_tool_usage_nano_usd, c.key_spend_nano_usd, c.headers_ms, c.total_ms, c.warnings, c.rounds,
       c.rounds_input_tokens, c.rounds_output_tokens, c.rounds_cost_nano_usd, c.created_at
  FROM note_calls c JOIN repo_map m ON m.project_key = c.project_key AND m.slug = c.repo_slug;

-- A project revision has project_id and no repo_id; a repository revision
-- both, the project its repository's.
CREATE TABLE revisions_new (
    id INTEGER PRIMARY KEY,
    scope TEXT NOT NULL CHECK (scope IN ('repository', 'project')),
    project_id INTEGER NOT NULL REFERENCES projects_new(id),
    repo_id INTEGER REFERENCES repos_new(id),
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
    created_at TEXT NOT NULL CHECK (created_at GLOB '????-??-??T??:??:??.?????????Z'),
    CHECK ((scope = 'project') = (repo_id IS NULL)),
    CONSTRAINT revisions_source_pr CHECK ((source_kind = 'pr') = (source_pr_key IS NOT NULL)),
    CONSTRAINT revisions_source_commit CHECK ((source_kind = 'baseline') = (source_commit IS NOT NULL)),
    CONSTRAINT revisions_map_scope CHECK (map IS NULL OR scope = 'project')
);
INSERT INTO revisions_new (id, scope, project_id, repo_id, revision, summary, statements, chars, source_kind,
                           source_pr_key, source_commit, llm_call_id, baseline_run_id, map, created_at)
SELECT v.id, v.scope, p.id, m.repo_id, v.revision, v.summary, v.statements, v.chars, v.source_kind,
       v.source_pr_key, v.source_commit, v.llm_call_id, v.baseline_run_id, v.map, v.created_at
  FROM revisions v JOIN projects_new p ON p.key = v.project_key
  LEFT JOIN repo_map m ON m.project_key = v.project_key AND m.slug = v.repo_slug;

-- The current revision per page. repo_id 0 is the project scope, so the key
-- is one primary key (a NULL would make every project row distinct).
CREATE TABLE pages_new (
    scope TEXT NOT NULL,
    project_id INTEGER NOT NULL REFERENCES projects_new(id),
    repo_id INTEGER NOT NULL DEFAULT 0,
    current_revision_id INTEGER NOT NULL REFERENCES revisions_new(id),
    updated_at TEXT NOT NULL CHECK (updated_at GLOB '????-??-??T??:??:??.?????????Z'),
    PRIMARY KEY (scope, project_id, repo_id)
);
INSERT INTO pages_new (scope, project_id, repo_id, current_revision_id, updated_at)
SELECT g.scope, p.id, COALESCE(m.repo_id, 0), g.current_revision_id, g.updated_at
  FROM pages g JOIN projects_new p ON p.key = g.project_key
  LEFT JOIN repo_map m ON m.project_key = g.project_key AND m.slug = g.repo_slug;

CREATE TABLE llm_calls_new (
    id INTEGER PRIMARY KEY,
    repo_id INTEGER NOT NULL REFERENCES repos_new(id),
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
    created_at TEXT NOT NULL CHECK (created_at GLOB '????-??-??T??:??:??.?????????Z'),
    CONSTRAINT llm_calls_source_pr CHECK ((source_kind = 'pr') = (source_pr_key IS NOT NULL)),
    CONSTRAINT llm_calls_source_commit CHECK ((source_kind = 'baseline') = (source_commit IS NOT NULL))
);
INSERT INTO llm_calls_new (id, repo_id, source_kind, source_pr_key, source_commit, purpose, baseline_run_id, outcome,
                           model, input_tokens, cached_tokens, output_tokens, cost_nano_usd, call_id, created_at)
SELECT c.id, m.repo_id, c.source_kind, c.source_pr_key, c.source_commit, c.purpose, c.baseline_run_id, c.outcome,
       c.model, c.input_tokens, c.cached_tokens, c.output_tokens, c.cost_nano_usd, c.call_id, c.created_at
  FROM llm_calls c JOIN repo_map m ON m.project_key = c.project_key AND m.slug = c.repo_slug;

CREATE TABLE sources_new (
    project_id INTEGER PRIMARY KEY REFERENCES projects_new(id),
    kind TEXT NOT NULL CHECK (kind IN ('bitbucket', 'static')),
    host TEXT,
    token_auth TEXT NOT NULL CHECK (token_auth IN ('bearer', 'basic')),
    token_user TEXT NOT NULL DEFAULT '',
    token_enc BLOB,
    created_at TEXT NOT NULL CHECK (created_at GLOB '????-??-??T??:??:??.?????????Z'),
    updated_at TEXT NOT NULL CHECK (updated_at GLOB '????-??-??T??:??:??.?????????Z'),
    last_listed_at TEXT CHECK (last_listed_at GLOB '????-??-??T??:??:??.?????????Z'),
    last_error TEXT NOT NULL DEFAULT '',
    auto_describe BOOLEAN NOT NULL DEFAULT 1 CHECK (auto_describe IN (0, 1))
);
INSERT INTO sources_new (project_id, kind, host, token_auth, token_user, token_enc, created_at, updated_at,
                         last_listed_at, last_error, auto_describe)
SELECT p.id, s.kind, s.host, s.token_auth, s.token_user, s.token_enc, s.created_at, s.updated_at,
       s.last_listed_at, s.last_error, s.auto_describe
  FROM sources s JOIN projects_new p ON p.key = s.project_key;

-- One row per clone; the repository's project is its source.
CREATE TABLE source_repos_new (
    repo_id INTEGER PRIMARY KEY REFERENCES repos_new(id),
    project_id INTEGER NOT NULL REFERENCES sources_new(project_id) ON DELETE CASCADE,
    clone_url TEXT NOT NULL,
    origin TEXT NOT NULL CHECK (origin IN ('listed', 'manual')),
    branch TEXT,
    head_commit TEXT NOT NULL DEFAULT '',
    state TEXT NOT NULL CHECK (state IN ('pending', 'syncing', 'ready', 'failed', 'missing')),
    last_synced_at TEXT CHECK (last_synced_at GLOB '????-??-??T??:??:??.?????????Z'),
    last_error TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL CHECK (created_at GLOB '????-??-??T??:??:??.?????????Z')
);
CREATE INDEX source_repos_project_idx ON source_repos_new (project_id);
INSERT INTO source_repos_new (repo_id, project_id, clone_url, origin, branch, head_commit, state, last_synced_at,
                              last_error, created_at)
SELECT m.repo_id, m.project_id, r.clone_url, r.origin, r.branch, r.head_commit, r.state, r.last_synced_at,
       r.last_error, r.created_at
  FROM source_repos r JOIN repo_map m ON m.project_key = r.project_key AND m.slug = r.slug;

CREATE TABLE baseline_runs_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    repo_id INTEGER NOT NULL REFERENCES source_repos_new(repo_id) ON DELETE CASCADE,
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
    queued_at TEXT NOT NULL CHECK (queued_at GLOB '????-??-??T??:??:??.?????????Z'),
    started_at TEXT CHECK (started_at GLOB '????-??-??T??:??:??.?????????Z'),
    finished_at TEXT CHECK (finished_at GLOB '????-??-??T??:??:??.?????????Z'),
    CHECK (mode = 'full' OR base_commit <> '')
);
INSERT INTO baseline_runs_new (id, repo_id, mode, trigger, base_commit, "commit", state, step, steps, plan, attempts,
                               calls, bytes, skipped, error, queued_at, started_at, finished_at)
SELECT b.id, m.repo_id, b.mode, b.trigger, b.base_commit, b."commit", b.state, b.step, b.steps, b.plan, b.attempts,
       b.calls, b.bytes, b.skipped, b.error, b.queued_at, b.started_at, b.finished_at
  FROM baseline_runs b JOIN repo_map m ON m.project_key = b.project_key AND m.slug = b.slug;
-- AUTOINCREMENT: a run id is never reused, so the counter carries over even
-- past rows already removed with their repository.
UPDATE sqlite_sequence SET seq = (SELECT seq FROM sqlite_sequence WHERE name = 'baseline_runs')
 WHERE name = 'baseline_runs_new' AND EXISTS (SELECT 1 FROM sqlite_sequence WHERE name = 'baseline_runs');
INSERT INTO sqlite_sequence (name, seq)
SELECT 'baseline_runs_new', seq FROM sqlite_sequence
 WHERE name = 'baseline_runs' AND NOT EXISTS (SELECT 1 FROM sqlite_sequence WHERE name = 'baseline_runs_new');

-- Every row carried over, or the migration fails: a row whose names found
-- no id would otherwise vanish without a word.
CREATE TEMP TABLE carried (what TEXT NOT NULL, ok INTEGER NOT NULL CHECK (ok = 1));
INSERT INTO carried VALUES ('notes', (SELECT COUNT(*) FROM notes) = (SELECT COUNT(*) FROM notes_new));
INSERT INTO carried VALUES ('note_calls', (SELECT COUNT(*) FROM note_calls) = (SELECT COUNT(*) FROM note_calls_new));
INSERT INTO carried VALUES ('revisions', (SELECT COUNT(*) FROM revisions) = (SELECT COUNT(*) FROM revisions_new));
INSERT INTO carried VALUES ('pages', (SELECT COUNT(*) FROM pages) = (SELECT COUNT(*) FROM pages_new));
INSERT INTO carried VALUES ('llm_calls', (SELECT COUNT(*) FROM llm_calls) = (SELECT COUNT(*) FROM llm_calls_new));
INSERT INTO carried VALUES ('sources', (SELECT COUNT(*) FROM sources) = (SELECT COUNT(*) FROM sources_new));
INSERT INTO carried VALUES ('source_repos', (SELECT COUNT(*) FROM source_repos) = (SELECT COUNT(*) FROM source_repos_new));
INSERT INTO carried VALUES ('baseline_runs', (SELECT COUNT(*) FROM baseline_runs) = (SELECT COUNT(*) FROM baseline_runs_new));
INSERT INTO carried VALUES ('repos', (SELECT COUNT(*) FROM repos) <= (SELECT COUNT(*) FROM repos_new));
INSERT INTO carried VALUES ('projects', (SELECT COUNT(*) FROM projects) <= (SELECT COUNT(*) FROM projects_new));
DROP TABLE carried;
DROP TABLE repo_map;

-- Children first: a parent dropped before its children would cascade.
DROP TABLE baseline_runs;
DROP TABLE source_repos;
DROP TABLE sources;
DROP TABLE pages;
DROP TABLE revisions;
DROP TABLE llm_calls;
DROP TABLE note_calls;
DROP TABLE notes;
DROP TABLE repos;
DROP TABLE projects;

ALTER TABLE projects_new RENAME TO projects;
ALTER TABLE repos_new RENAME TO repos;
ALTER TABLE notes_new RENAME TO notes;
ALTER TABLE note_calls_new RENAME TO note_calls;
ALTER TABLE revisions_new RENAME TO revisions;
ALTER TABLE pages_new RENAME TO pages;
ALTER TABLE llm_calls_new RENAME TO llm_calls;
ALTER TABLE sources_new RENAME TO sources;
ALTER TABLE source_repos_new RENAME TO source_repos;
ALTER TABLE baseline_runs_new RENAME TO baseline_runs;

CREATE INDEX repos_project_idx ON repos (project_id);
CREATE INDEX notes_queue_idx ON notes (received_at) WHERE consolidated_at IS NULL;
CREATE INDEX notes_repo_idx ON notes (repo_id, merged_at DESC);
CREATE INDEX note_calls_team_idx ON note_calls (team, created_at);
CREATE INDEX note_calls_note_idx ON note_calls (repo_id, pr_id);
CREATE UNIQUE INDEX revisions_key_idx ON revisions (scope, project_id, COALESCE(repo_id, 0), revision);
CREATE INDEX llm_calls_run_idx ON llm_calls (baseline_run_id) WHERE baseline_run_id IS NOT NULL;
CREATE INDEX llm_calls_repo_idx ON llm_calls (repo_id);
CREATE UNIQUE INDEX baseline_runs_open_idx ON baseline_runs (repo_id) WHERE state NOT IN ('done', 'failed');
CREATE INDEX baseline_runs_repo_idx ON baseline_runs (repo_id, id DESC);
