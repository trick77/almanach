-- A repository's brief: what an agent working in it cannot read from its
-- code, for the repository's .agents/PROJECT.md, written on request from
-- the stored page (one model call, no code read) and pushed to a branch of
-- the repository with the source's token. A source repository's only: the
-- row goes with it.
--
-- requested_at: a request waits for the worker; attempts counts the calls
-- tried for it, each counted before it is made. request_seq counts the
-- requests: the worker ends the one it took only while no other came
-- meanwhile. failed_at and failed: the last request failed, the brief
-- before it (if any) kept. markdown is the file as pushed, written from
-- page revision page_revision at written_at.
--
-- push_state: due (written, the push waits for the worker), pushed (the
-- branch holds pushed_commit), unchanged (the default branch already holds
-- the file), failed (push_error says why; the reader copies the text
-- instead). push_at is when it last changed. pushed_commit is the commit
-- last pushed, kept through the other states: a push replaces the branch
-- only where it still stands there (a lease), never commits of anyone else.
CREATE TABLE repo_briefs (
    repo_id INTEGER PRIMARY KEY REFERENCES source_repos(repo_id) ON DELETE CASCADE,
    markdown TEXT CHECK (markdown IS NULL OR markdown <> ''),
    page_revision INTEGER,
    written_at TEXT CHECK (written_at IS NULL OR written_at GLOB '????-??-??T??:??:??.?????????Z'),
    requested_at TEXT CHECK (requested_at IS NULL OR requested_at GLOB '????-??-??T??:??:??.?????????Z'),
    attempts INTEGER NOT NULL DEFAULT 0,
    request_seq INTEGER NOT NULL DEFAULT 0,
    failed_at TEXT CHECK (failed_at IS NULL OR failed_at GLOB '????-??-??T??:??:??.?????????Z'),
    failed TEXT CHECK (failed IS NULL OR failed <> ''),
    push_state TEXT CHECK (push_state IS NULL OR push_state IN ('due', 'pushed', 'unchanged', 'failed')),
    push_error TEXT CHECK (push_error IS NULL OR push_error <> ''),
    pushed_commit TEXT,
    push_at TEXT CHECK (push_at IS NULL OR push_at GLOB '????-??-??T??:??:??.?????????Z'),
    CHECK ((markdown IS NULL) = (written_at IS NULL)),
    CHECK ((markdown IS NULL) = (page_revision IS NULL)),
    CHECK (push_state IS NULL OR markdown IS NOT NULL),
    CHECK ((failed_at IS NULL) = (failed IS NULL)),
    CHECK ((push_state = 'failed') = (push_error IS NOT NULL))
);
CREATE INDEX repo_briefs_requested_idx ON repo_briefs (requested_at) WHERE requested_at IS NOT NULL;
CREATE INDEX repo_briefs_due_idx ON repo_briefs (push_at) WHERE push_state = 'due';

-- A brief's call is its own purpose, brief, its source the page (no pull
-- request, no commit). The ledger is rebuilt as 0004 made it, the purpose
-- and the source kind added.
CREATE TABLE llm_calls_new (
    id INTEGER PRIMARY KEY,
    repo_id INTEGER NOT NULL REFERENCES repos(id),
    source_kind TEXT NOT NULL DEFAULT 'pr' CHECK (source_kind IN ('pr', 'baseline', 'page')),
    source_pr_key TEXT,
    source_commit TEXT,
    purpose TEXT NOT NULL DEFAULT 'merge' CHECK (purpose IN ('merge', 'catalogue', 'overview', 'subject', 'project', 'repo_map', 'project_map', 'restyle', 'brief')),
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
    CONSTRAINT llm_calls_source_commit CHECK ((source_kind = 'baseline') = (source_commit IS NOT NULL)),
    CONSTRAINT llm_calls_source_page CHECK ((source_kind = 'page') = (purpose = 'brief'))
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
