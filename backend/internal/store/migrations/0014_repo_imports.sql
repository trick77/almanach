-- Which packages inside a source repository import which, read by the
-- poller beside its manifests (internal/imports): Go packages folded to
-- cmd/<x>, internal/<a>, <a>; Python distributions' top-level subpackages.
-- units: a JSON array of {"dir", "name", "kind"}; edges: of {"from",
-- "to"}, by dir. reader: the imports package's Version that read them; a
-- newer one reads every repository again, its head moved or not.
CREATE TABLE repo_imports (
    repo_id INTEGER PRIMARY KEY REFERENCES source_repos(repo_id) ON DELETE CASCADE,
    "commit" TEXT NOT NULL,
    units TEXT NOT NULL CHECK (json_valid(units) AND json_type(units) = 'array'),
    edges TEXT NOT NULL CHECK (json_valid(edges) AND json_type(edges) = 'array'),
    reader INTEGER NOT NULL,
    read_at TEXT NOT NULL CHECK (read_at GLOB '????-??-??T??:??:??.?????????Z')
);
