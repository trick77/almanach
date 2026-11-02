-- A source repository can be deactivated: the listing keeps it (a Bitbucket
-- project's next listing would add it back otherwise), but it is no source
-- anywhere: no clone, no sync, no run, its notes ignored, its page out of
-- the wiki and out of the project's. deactivated_at is when; NULL is
-- active. reactivated_at is the last time it came back, so a project page
-- written while it was off folds it in again. Both are beside state, which
-- stays the poller's (a deactivated repository still goes missing when
-- the listing drops it).
ALTER TABLE source_repos ADD COLUMN deactivated_at TEXT CHECK (deactivated_at GLOB '????-??-??T??:??:??.?????????Z');
ALTER TABLE source_repos ADD COLUMN reactivated_at TEXT CHECK (reactivated_at GLOB '????-??-??T??:??:??.?????????Z');

-- A deactivation queues a run of trigger departure on the repository that
-- left: no reading, only the project step, which takes the repository out
-- of the project page. SQLite cannot change a CHECK in place, so the runs
-- are rebuilt as 0001 made them, the trigger added, every row carried over.
-- No foreign key points at baseline_runs (the ledger and the revisions
-- keep the run id without one).
CREATE TABLE baseline_runs_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    repo_id INTEGER NOT NULL REFERENCES source_repos(repo_id) ON DELETE CASCADE,
    mode TEXT NOT NULL CHECK (mode IN ('full', 'reconcile')),
    trigger TEXT NOT NULL CHECK (trigger IN ('manual', 'first_ready', 'monthly', 'retry', 'departure')),
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
INSERT INTO baseline_runs_new (id, repo_id, mode, trigger, base_commit, "commit", state, step, steps, plan, attempts, calls,
                               bytes, skipped, error, queued_at, started_at, finished_at)
SELECT id, repo_id, mode, trigger, base_commit, "commit", state, step, steps, plan, attempts, calls,
       bytes, skipped, error, queued_at, started_at, finished_at
  FROM baseline_runs;
-- The id sequence goes on where it stood: a run removed with its
-- repository may have had the highest id, and a new run taking it again
-- would own that run's ledger rows.
INSERT INTO sqlite_sequence (name, seq)
SELECT 'baseline_runs_new', seq FROM sqlite_sequence
 WHERE name = 'baseline_runs' AND NOT EXISTS (SELECT 1 FROM sqlite_sequence WHERE name = 'baseline_runs_new');
UPDATE sqlite_sequence SET seq = MAX(seq, COALESCE((SELECT seq FROM sqlite_sequence WHERE name = 'baseline_runs'), 0))
 WHERE name = 'baseline_runs_new';
DROP TABLE baseline_runs;
ALTER TABLE baseline_runs_new RENAME TO baseline_runs;
CREATE UNIQUE INDEX baseline_runs_open_idx ON baseline_runs (repo_id) WHERE state NOT IN ('done', 'failed');
CREATE INDEX baseline_runs_repo_idx ON baseline_runs (repo_id, id DESC);
