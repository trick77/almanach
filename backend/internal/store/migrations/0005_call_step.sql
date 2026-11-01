-- The reading step a baseline call belongs to (a run's overview or subject,
-- by the run's step index), so the queue page shows each step's time and
-- cost. NULL for a merge, a catalogue, a project or a map call, and for
-- every call recorded before this column.
ALTER TABLE llm_calls ADD COLUMN step INTEGER;

-- The queue page sums the calls of the last 24 hours on every poll: by
-- time, not over the whole append-only table.
CREATE INDEX llm_calls_created_idx ON llm_calls (created_at);

-- And lists the notes folded in that window, newest first.
CREATE INDEX notes_folded_idx ON notes (consolidated_at) WHERE consolidated_at IS NOT NULL;
