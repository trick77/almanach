-- The queue page's finished runs, newest first, read on every poll of an
-- open queue page: without it a scan of every run and a sort.
CREATE INDEX baseline_runs_finished_idx ON baseline_runs (finished_at) WHERE finished_at IS NOT NULL;
