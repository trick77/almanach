-- What a source repository's build manifests (go.mod, pom.xml,
-- package.json, at the root or one level down) say at a commit: the
-- coordinates it provides and the ones it requires, as the poller read
-- them after a sync, by code, never by a model. One row per source
-- repository, replaced when the head moves; gone with the row.
--
-- The edges between repositories are never stored: they are joined at
-- read time, requires against provides, over the current source rows, so
-- a repository added, removed or moved changes them with nothing to keep
-- in step.
--
-- provides: a JSON array of coordinates (`go:<module>`,
-- `maven:<group>:<artifact>`, `npm:<name>`). requires: a JSON array of
-- {"coord", "version"}, the version as written, "" when none is.
CREATE TABLE repo_manifests (
    repo_id INTEGER PRIMARY KEY REFERENCES source_repos(repo_id) ON DELETE CASCADE,
    "commit" TEXT NOT NULL,
    provides TEXT NOT NULL CHECK (json_valid(provides) AND json_type(provides) = 'array'),
    requires TEXT NOT NULL CHECK (json_valid(requires) AND json_type(requires) = 'array'),
    read_at TEXT NOT NULL CHECK (read_at GLOB '????-??-??T??:??:??.?????????Z')
);
