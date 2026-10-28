-- A fold the gateway failed (no answer, an error) is retried a bounded
-- number of times before the note is taken off the queue for good; the
-- count lives on the note. A wrong key must not consume every note that
-- arrives before someone reads the log.
ALTER TABLE notes ADD COLUMN fold_attempts INTEGER NOT NULL DEFAULT 0;
