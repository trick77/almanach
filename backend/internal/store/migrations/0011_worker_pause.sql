-- The queue's pause: a reader stops the worker taking new items (notes,
-- run steps, restyles, briefs) until resumed; the item in flight finishes,
-- it is paid for. A row, not memory: a pause that ended with a redeploy
-- would spend again unasked. Who paused is the log's, never stored: the
-- reader's name is log only. One row at most.
CREATE TABLE worker_pause (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    paused_at TEXT NOT NULL CHECK (paused_at GLOB '????-??-??T??:??:??.?????????Z')
);

-- Why a run was queued, written by whoever queued it: a JSON object the
-- queue page words (kind, and per kind the facts: the page levels a
-- restyle lifts, the attempt a retry is, the commits a reconcile spans).
-- NULL for a run from before.
ALTER TABLE baseline_runs ADD COLUMN reason TEXT CHECK (reason IS NULL OR (json_valid(reason) AND json_type(reason) = 'object'));

-- A call whose answer (or reasoning) had begun to arrive: billed whatever
-- its outcome, so one without a price is counted as unpriced, never as
-- free (a stream cut mid-answer reports no usage). 0 for a call from
-- before; those are told by their outcome.
ALTER TABLE llm_calls ADD COLUMN streamed INTEGER NOT NULL DEFAULT 0 CHECK (streamed IN (0, 1));
