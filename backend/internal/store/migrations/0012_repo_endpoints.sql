-- What a source repository's code says of the interfaces it offers, read by
-- the poller beside its topics (internal/endpoints): HTTP APIs (specs and
-- the routes its code declares), gRPC and SOAP services, each offered or
-- only declared (a contract kept for a client). Who offers what in a
-- project is read from these, never stored twice. apis: a JSON array of
-- {"kind", "name", "version", "base_path", "direction", "files"};
-- endpoints: of {"kind", "api", "method", "path", "summary", "direction",
-- "files"}. reader: the endpoints package's Version that read them; a
-- newer one reads every repository again, its head moved or not.
CREATE TABLE repo_endpoints (
    repo_id INTEGER PRIMARY KEY REFERENCES source_repos(repo_id) ON DELETE CASCADE,
    "commit" TEXT NOT NULL,
    apis TEXT NOT NULL CHECK (json_valid(apis) AND json_type(apis) = 'array'),
    endpoints TEXT NOT NULL CHECK (json_valid(endpoints) AND json_type(endpoints) = 'array'),
    reader INTEGER NOT NULL,
    read_at TEXT NOT NULL CHECK (read_at GLOB '????-??-??T??:??:??.?????????Z')
);
