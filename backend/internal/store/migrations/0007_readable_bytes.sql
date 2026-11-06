-- readable_bytes is what a full run could have read, the readable files of
-- the tree at its commit; read_bytes the distinct files its folded parts
-- read, so a file in two subjects counts once and a skipped part not at
-- all (bytes is the budget's running count, every call's input). The
-- share of the two says how much of the code the page rests on, which a
-- run of twelve subjects over a large repository never said. A reconcile
-- carries the documented run's pair: the page's coverage is the last full
-- read's. 0 on a run from before, and on one that read no code (a
-- restyle, a departure).
ALTER TABLE baseline_runs ADD COLUMN readable_bytes INTEGER NOT NULL DEFAULT 0;
ALTER TABLE baseline_runs ADD COLUMN read_bytes INTEGER NOT NULL DEFAULT 0;
