-- retry: the poller queues a failed run again, in the failed run's mode. Its
-- own trigger, so the log and the source page tell a retry from a first
-- ready or a monthly reconcile.
ALTER TABLE baseline_runs DROP CONSTRAINT baseline_runs_trigger_check;
ALTER TABLE baseline_runs ADD CONSTRAINT baseline_runs_trigger_check
    CHECK (trigger IN ('manual', 'first_ready', 'monthly', 'retry'));
