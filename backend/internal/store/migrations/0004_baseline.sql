-- The baseline: Almanach reads a cloned repository at a commit and writes
-- the description it should have had from the start; the PR notes refine it
-- from then on. A run documents one repository at one commit, pinned when
-- it is queued, one model call per step, so the worker can interleave steps
-- with notes, hold after a gateway failure and resume after a restart.

-- auto_describe: the first run on a clone's first ready and the monthly
-- reconcile happen on their own; off, only the buttons start a run.
ALTER TABLE sources ADD COLUMN auto_describe BOOLEAN NOT NULL DEFAULT true;

-- mode: full reads the repository with a fresh catalogue, reconcile reads
-- what changed since base_commit (the documented commit). plan is the
-- catalogue: the subjects with their files and sizes. attempts counts the
-- gateway failures of the current step; calls and bytes are what the run
-- spent against its caps; skipped counts the subjects it could not fold.
CREATE TABLE baseline_runs (
    id BIGSERIAL PRIMARY KEY,
    project_key TEXT NOT NULL,
    slug TEXT NOT NULL,
    mode TEXT NOT NULL CHECK (mode IN ('full', 'reconcile')),
    trigger TEXT NOT NULL CHECK (trigger IN ('manual', 'first_ready', 'monthly')),
    base_commit TEXT NOT NULL DEFAULT '',
    commit TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('queued', 'cataloguing', 'describing', 'project', 'done', 'failed')),
    step INTEGER NOT NULL DEFAULT 0,
    steps INTEGER NOT NULL DEFAULT 0,
    plan JSONB,
    attempts INTEGER NOT NULL DEFAULT 0,
    calls INTEGER NOT NULL DEFAULT 0,
    bytes BIGINT NOT NULL DEFAULT 0,
    skipped INTEGER NOT NULL DEFAULT 0,
    error TEXT NOT NULL DEFAULT '',
    queued_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    FOREIGN KEY (project_key, slug) REFERENCES source_repos (project_key, slug) ON DELETE CASCADE
);
-- One open run per repository: a second click is a conflict, not a second
-- writer of the same page.
CREATE UNIQUE INDEX baseline_runs_open_idx ON baseline_runs (project_key, slug) WHERE state NOT IN ('done', 'failed');
CREATE INDEX baseline_runs_repo_idx ON baseline_runs (project_key, slug, id DESC);

-- Provenance gains a kind: a revision or a call comes from a PR's note or
-- from a baseline run at a commit. The PR key goes nullable; exactly one of
-- it and the commit is set. baseline_run_id carries no foreign key, like
-- llm_call_id: the ledger outlives a removed repository.
ALTER TABLE revisions ALTER COLUMN source_pr_key DROP NOT NULL,
    ADD COLUMN source_kind TEXT NOT NULL DEFAULT 'pr' CHECK (source_kind IN ('pr', 'baseline')),
    ADD COLUMN source_commit TEXT,
    ADD COLUMN baseline_run_id BIGINT,
    ADD CONSTRAINT revisions_source_pr CHECK ((source_kind = 'pr') = (source_pr_key IS NOT NULL)),
    ADD CONSTRAINT revisions_source_commit CHECK ((source_kind = 'baseline') = (source_commit IS NOT NULL));

-- purpose: which step of which flow made the call. merge is a note's fold.
ALTER TABLE llm_calls ALTER COLUMN source_pr_key DROP NOT NULL,
    ADD COLUMN source_kind TEXT NOT NULL DEFAULT 'pr' CHECK (source_kind IN ('pr', 'baseline')),
    ADD COLUMN source_commit TEXT,
    ADD COLUMN purpose TEXT NOT NULL DEFAULT 'merge' CHECK (purpose IN ('merge', 'catalogue', 'overview', 'subject', 'project')),
    ADD COLUMN baseline_run_id BIGINT,
    ADD CONSTRAINT llm_calls_source_pr CHECK ((source_kind = 'pr') = (source_pr_key IS NOT NULL)),
    ADD CONSTRAINT llm_calls_source_commit CHECK ((source_kind = 'baseline') = (source_commit IS NOT NULL));
CREATE INDEX llm_calls_run_idx ON llm_calls (baseline_run_id) WHERE baseline_run_id IS NOT NULL;
