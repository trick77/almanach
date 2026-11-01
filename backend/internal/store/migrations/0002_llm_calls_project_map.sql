-- The system map asked again alone is its own purpose, project_map: the
-- project step's description call stays 'project'. SQLite cannot change a
-- CHECK in place, so the ledger is rebuilt as 0001 made it, the purpose
-- added, every row carried over.
CREATE TABLE llm_calls_new (
    id INTEGER PRIMARY KEY,
    repo_id INTEGER NOT NULL REFERENCES repos(id),
    source_kind TEXT NOT NULL DEFAULT 'pr' CHECK (source_kind IN ('pr', 'baseline')),
    source_pr_key TEXT,
    source_commit TEXT,
    purpose TEXT NOT NULL DEFAULT 'merge' CHECK (purpose IN ('merge', 'catalogue', 'overview', 'subject', 'project', 'repo_map', 'project_map')),
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
