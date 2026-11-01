-- Whether a source's repositories stand alone. Off (every source so far),
-- they form one system: a project page and a system map, written by every
-- run's project step. On, each repository stands for itself: no project
-- step, no project page in the wiki (a page written before stays in the
-- store with its history). A Bitbucket project is always a system.
ALTER TABLE sources ADD COLUMN standalone BOOLEAN NOT NULL DEFAULT 0 CHECK (standalone IN (0, 1));

-- Set when a folder becomes a system again: the project page it kept was
-- written before repositories came, went or changed while it stood alone,
-- so the next run folds the project whatever a reconcile would skip.
-- Cleared once a project step got its answer.
ALTER TABLE sources ADD COLUMN project_due BOOLEAN NOT NULL DEFAULT 0 CHECK (project_due IN (0, 1));
