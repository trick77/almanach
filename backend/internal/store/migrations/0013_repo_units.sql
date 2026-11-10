-- The parts inside a source repository and which uses which, as its builds
-- declare them (manifest.Units), read with its manifests by the same reader
-- at the same commit, so stored in their row. units: a JSON array of
-- {"dir", "name", "kind"}; unit_edges: of {"from", "to", "scope"}, by dir.
-- Both empty when no part uses another. A row from before reads as none
-- until its next sync (manifest.Version 7 reads every repository again).
ALTER TABLE repo_manifests ADD COLUMN units TEXT NOT NULL DEFAULT '[]'
    CHECK (json_valid(units) AND json_type(units) = 'array');
ALTER TABLE repo_manifests ADD COLUMN unit_edges TEXT NOT NULL DEFAULT '[]'
    CHECK (json_valid(unit_edges) AND json_type(unit_edges) = 'array');
