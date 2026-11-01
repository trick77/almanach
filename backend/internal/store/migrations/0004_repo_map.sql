-- The repository map: a repository page's picture of itself from inside
-- (where it is entered, its parts, what they talk to), drawn by the last
-- step of a baseline run, after the project step, and checked like the
-- project's system map (baseline/diagram.go). The same flow form as map
-- (ui/src/diagramIR.ts). Repository pages only: a project revision carries
-- map, never repo_map. NULL until a run's map step draws one; the map step
-- sets it on the page's current revision in place (no revision of its
-- own: the text did not change), and every new repository revision (a
-- describe step, a merge) carries the current one forward (store/wiki.go
-- insertRevision). 0001 to 0003 stand as applied.
ALTER TABLE revisions ADD COLUMN repo_map TEXT
    CHECK (json_valid(repo_map))
    CHECK (repo_map IS NULL OR scope = 'repository');

-- repo_map_basis is the description the map was drawn from, as a hash
-- (consolidate's mapBasis): carried with the map, so a page whose text
-- moved on since (a merge, a run whose map step was skipped) shows a basis
-- that no longer matches, and the next run draws again. NULL with no map.
ALTER TABLE revisions ADD COLUMN repo_map_basis TEXT
    CHECK (repo_map_basis IS NULL OR scope = 'repository');

-- The map step's calls in the ledger: purpose repo_map beside the run's
-- other steps. SQLite cannot widen a CHECK in place, so llm_calls is
-- rebuilt as 0003 left it with the one value added, rows, ids and indexes
-- kept. Nothing references llm_calls by foreign key (revisions.llm_call_id
-- and note_calls carry none); its own reference to repos is checked on the
-- copy, every row already holds, so the rebuild runs inside this
-- migration's transaction with the foreign keys on.
CREATE TABLE llm_calls_new (
    id INTEGER PRIMARY KEY,
    repo_id INTEGER NOT NULL REFERENCES repos(id),
    source_kind TEXT NOT NULL DEFAULT 'pr' CHECK (source_kind IN ('pr', 'baseline')),
    source_pr_key TEXT,
    source_commit TEXT,
    purpose TEXT NOT NULL DEFAULT 'merge' CHECK (purpose IN ('merge', 'catalogue', 'overview', 'subject', 'project', 'repo_map')),
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
SELECT id, repo_id, source_kind, source_pr_key, source_commit, purpose, baseline_run_id, outcome,
       model, input_tokens, cached_tokens, output_tokens, cost_nano_usd, call_id, created_at
  FROM llm_calls;
DROP TABLE llm_calls;
ALTER TABLE llm_calls_new RENAME TO llm_calls;
CREATE INDEX llm_calls_run_idx ON llm_calls (baseline_run_id) WHERE baseline_run_id IS NOT NULL;
CREATE INDEX llm_calls_repo_idx ON llm_calls (repo_id);
