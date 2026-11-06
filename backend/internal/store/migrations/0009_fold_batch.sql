-- A fold takes a repository's queued notes together, once a day
-- (BACKEND_FOLD_DELAY): one merge call, one revision per page, for several
-- pull requests. The call's row and the revision keep naming the first of
-- them in source_pr_key, as every reader of a single note expects; the
-- whole set is batch_pr_keys, a JSON array of PR keys in merge order, NULL
-- for a fold of one note. Without it a revision would credit one pull
-- request for what several taught, and the queue page would show the
-- others as folded without a call.
ALTER TABLE llm_calls ADD COLUMN batch_pr_keys TEXT
    CHECK (batch_pr_keys IS NULL OR (json_valid(batch_pr_keys) AND json_type(batch_pr_keys) = 'array'));
ALTER TABLE revisions ADD COLUMN batch_pr_keys TEXT
    CHECK (batch_pr_keys IS NULL OR (json_valid(batch_pr_keys) AND json_type(batch_pr_keys) = 'array'));
