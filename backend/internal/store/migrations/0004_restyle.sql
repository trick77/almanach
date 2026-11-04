-- A restyle run applies the current prompts' layout, kind and area rules to
-- a stored page without reading code: one call over the page alone, the
-- repository's and, once per project, the project's. prompts.Epoch, bumped
-- by hand when a prompt change must reach stored pages, says which pages
-- are due: restyle_epoch is the epoch a page was last restyled at (a
-- repository's also by a full run done under it), 0 for every page written
-- before, so the first epoch restyles each once.
ALTER TABLE source_repos ADD COLUMN restyle_epoch INTEGER NOT NULL DEFAULT 0;
ALTER TABLE projects ADD COLUMN restyle_epoch INTEGER NOT NULL DEFAULT 0;

-- The run's mode restyle, its trigger epoch, and the epoch a full or
-- restyle run ran under (0 otherwise): the failed restyles at one epoch
-- bound its retries. SQLite cannot change a CHECK in place, so the runs are
-- rebuilt as 0003 made them, every row carried over, the id sequence with
-- them.
CREATE TABLE baseline_runs_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    repo_id INTEGER NOT NULL REFERENCES source_repos(repo_id) ON DELETE CASCADE,
    mode TEXT NOT NULL CHECK (mode IN ('full', 'reconcile', 'restyle')),
    trigger TEXT NOT NULL CHECK (trigger IN ('manual', 'first_ready', 'monthly', 'retry', 'departure', 'epoch')),
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
    epoch INTEGER NOT NULL DEFAULT 0,
    CHECK (mode = 'full' OR base_commit <> ''),
    CHECK ((mode = 'restyle') = (trigger = 'epoch'))
);
INSERT INTO baseline_runs_new (id, repo_id, mode, trigger, base_commit, "commit", state, step, steps, plan, attempts, calls,
                               bytes, skipped, error, queued_at, started_at, finished_at)
SELECT id, repo_id, mode, trigger, base_commit, "commit", state, step, steps, plan, attempts, calls,
       bytes, skipped, error, queued_at, started_at, finished_at
  FROM baseline_runs;
INSERT INTO sqlite_sequence (name, seq)
SELECT 'baseline_runs_new', seq FROM sqlite_sequence
 WHERE name = 'baseline_runs' AND NOT EXISTS (SELECT 1 FROM sqlite_sequence WHERE name = 'baseline_runs_new');
UPDATE sqlite_sequence SET seq = MAX(seq, COALESCE((SELECT seq FROM sqlite_sequence WHERE name = 'baseline_runs'), 0))
 WHERE name = 'baseline_runs_new';
DROP TABLE baseline_runs;
ALTER TABLE baseline_runs_new RENAME TO baseline_runs;
CREATE UNIQUE INDEX baseline_runs_open_idx ON baseline_runs (repo_id) WHERE state NOT IN ('done', 'failed');
CREATE INDEX baseline_runs_repo_idx ON baseline_runs (repo_id, id DESC);

-- A restyle's call is its own purpose, restyle: what a bump costs reads off
-- the ledger. The ledger is rebuilt as 0002 made it, the purpose added.
CREATE TABLE llm_calls_new (
    id INTEGER PRIMARY KEY,
    repo_id INTEGER NOT NULL REFERENCES repos(id),
    source_kind TEXT NOT NULL DEFAULT 'pr' CHECK (source_kind IN ('pr', 'baseline')),
    source_pr_key TEXT,
    source_commit TEXT,
    purpose TEXT NOT NULL DEFAULT 'merge' CHECK (purpose IN ('merge', 'catalogue', 'overview', 'subject', 'project', 'repo_map', 'project_map', 'restyle')),
    baseline_run_id INTEGER,
    step INTEGER,
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
INSERT INTO llm_calls_new (id, repo_id, source_kind, source_pr_key, source_commit, purpose, baseline_run_id, step, outcome, model,
                           input_tokens, cached_tokens, output_tokens, cost_nano_usd, call_id, created_at)
SELECT id, repo_id, source_kind, source_pr_key, source_commit, purpose, baseline_run_id, step, outcome, model,
       input_tokens, cached_tokens, output_tokens, cost_nano_usd, call_id, created_at
  FROM llm_calls;
DROP TABLE llm_calls;
ALTER TABLE llm_calls_new RENAME TO llm_calls;
CREATE INDEX llm_calls_run_idx ON llm_calls (baseline_run_id) WHERE baseline_run_id IS NOT NULL;
CREATE INDEX llm_calls_repo_idx ON llm_calls (repo_id);
CREATE INDEX llm_calls_created_idx ON llm_calls (created_at);
