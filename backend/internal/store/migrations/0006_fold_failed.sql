-- A note the merge call failed on (an answer off the schema, cut by the
-- output limit, a 4xx; a patch refused whole) leaves the queue as failed,
-- not consolidated: fold_failed_at says when, fold_failed why. Its
-- knowledge is late, not lost: the repository's next run puts it back on
-- the queue (both NULL again) while fold_attempts is under the worker's
-- bound; a note delivered again resets it with the rest. Before, such a
-- note was marked consolidated with a log line and its pull request's
-- knowledge was gone.
ALTER TABLE notes ADD COLUMN fold_failed_at TEXT
    CHECK (fold_failed_at IS NULL OR fold_failed_at GLOB '????-??-??T??:??:??.?????????Z');
ALTER TABLE notes ADD COLUMN fold_failed TEXT
    CHECK (fold_failed IS NULL OR fold_failed <> '');
CREATE INDEX notes_failed_idx ON notes (fold_failed_at) WHERE fold_failed_at IS NOT NULL;
