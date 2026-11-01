-- Whether a source's repositories stand alone. Off (every source so far),
-- they form one system: a project page and a system map, written by every
-- run's project step. On, each repository stands for itself: no project
-- step, no project page in the wiki (a page written before stays in the
-- store with its history). A Bitbucket project is always a system.
ALTER TABLE sources ADD COLUMN standalone BOOLEAN NOT NULL DEFAULT 0 CHECK (standalone IN (0, 1));
