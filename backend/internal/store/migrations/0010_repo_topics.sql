-- What a source repository's code says of its messages, read by the poller
-- beside its manifests (internal/messaging): the topics it produces to,
-- consumes from or names, and the schemas it defines. Who talks to whom
-- over a topic is joined from these at read time, never stored, like the
-- dependency graph. topics: a JSON array of {"name", "direction",
-- "files"}; schemas: of {"name", "kind", "file"}. reader: the messaging
-- package's Version that read them; a newer one reads every repository
-- again, its head moved or not.
CREATE TABLE repo_topics (
    repo_id INTEGER PRIMARY KEY REFERENCES source_repos(repo_id) ON DELETE CASCADE,
    "commit" TEXT NOT NULL,
    topics TEXT NOT NULL CHECK (json_valid(topics) AND json_type(topics) = 'array'),
    schemas TEXT NOT NULL CHECK (json_valid(schemas) AND json_type(schemas) = 'array'),
    reader INTEGER NOT NULL,
    read_at TEXT NOT NULL CHECK (read_at GLOB '????-??-??T??:??:??.?????????Z')
);
