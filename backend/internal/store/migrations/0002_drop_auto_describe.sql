-- Runs start on their own for every source (a clone's first ready, the
-- reconcile interval, a failed run's retries); the per-source switch is gone.
ALTER TABLE sources DROP COLUMN auto_describe;
