-- Almanach's schema. Everything noergler sends is kept raw (deliveries),
-- lifted into rows the wiki is built from (teams, projects, repos, claims,
-- notes, note_calls), and consolidated into descriptions (revisions, pages).
-- The sources are what Almanach clones itself (sources, source_repos), the
-- baseline runs describe them (baseline_runs), the manifests say who uses
-- whom (repo_manifests). The database records what happened; the
-- descriptions are rewritten by the model, revision by revision, never
-- edited by hand.
--
-- SQLite, one file. Every time is TEXT, UTC, RFC 3339 with all nine digits
-- of the nanoseconds (2026-10-01T12:00:00.000000000Z): fixed width, so text
-- order is time order. The store writes every one; the schema has no clock
-- (no DEFAULT on a time column), and every time column's CHECK refuses any
-- other shape, a variable-width or zoned one above all. JSON is TEXT that
-- json_valid accepts; a NULL passes a CHECK. Money is nano-USD in an
-- INTEGER (64-bit).
--
-- A project and a repository are ids; a key and a slug are names. projects
-- and repos hold the id and the one current name; project_names and
-- repo_names map every name ever used to its id, so a note, a link or a
-- listing entry under an old name lands on the same row. A name is never
-- given to another project or repository. Every other table points at an id.
--
-- One file, squashed on a dropped database (user, 2026-11-01): what came
-- before it never ran against this schema.

-- Every webhook body, whole, keyed by the sender's dedup key. Append-only:
-- a retry is a no-op, never an update.
CREATE TABLE deliveries (
    delivery_id TEXT PRIMARY KEY,
    team TEXT NOT NULL,
    event_type TEXT NOT NULL,
    received_at TEXT NOT NULL CHECK (received_at GLOB '????-??-??T??:??:??.?????????Z'),
    payload TEXT NOT NULL CHECK (json_valid(payload))
);
CREATE INDEX deliveries_team_idx ON deliveries (team, received_at);

-- The team a bearer named, and its claims on projects as noergler sent
-- them (a NULL repo_slug is the whole project). Claims stay noergler's
-- names, like the deliveries, never joined on.
CREATE TABLE teams (
    slug TEXT PRIMARY KEY,
    first_seen TEXT NOT NULL CHECK (first_seen GLOB '????-??-??T??:??:??.?????????Z'),
    last_seen TEXT NOT NULL CHECK (last_seen GLOB '????-??-??T??:??:??.?????????Z')
);
CREATE TABLE claims (
    team TEXT NOT NULL REFERENCES teams(slug),
    project_key TEXT NOT NULL,
    repo_slug TEXT,
    seen_at TEXT NOT NULL CHECK (seen_at GLOB '????-??-??T??:??:??.?????????Z')
);
CREATE UNIQUE INDEX claims_repo_idx ON claims (team, project_key, repo_slug) WHERE repo_slug IS NOT NULL;
CREATE UNIQUE INDEX claims_project_idx ON claims (team, project_key) WHERE repo_slug IS NULL;

-- The project: id, current key, noergler's team and times. Team and times
-- are NULL for a project only a source knows.
CREATE TABLE projects (
    id INTEGER PRIMARY KEY,
    key TEXT NOT NULL UNIQUE,
    team TEXT REFERENCES teams(slug),
    first_seen TEXT CHECK (first_seen GLOB '????-??-??T??:??:??.?????????Z'),
    last_seen TEXT CHECK (last_seen GLOB '????-??-??T??:??:??.?????????Z')
);

-- Every key a project was ever known by, the current one included.
CREATE TABLE project_names (
    key TEXT PRIMARY KEY,
    project_id INTEGER NOT NULL REFERENCES projects(id)
);
CREATE INDEX project_names_project_idx ON project_names (project_id);

-- The repository: id, project, current slug, the forge's own id (Bitbucket's
-- repository id, which survives a rename; NULL for a hand-entered
-- repository), noergler's team and times. A slug only the ledgers know (a
-- removed clone) keeps its id: the ledger outlives a removed repository.
CREATE TABLE repos (
    id INTEGER PRIMARY KEY,
    project_id INTEGER NOT NULL REFERENCES projects(id),
    slug TEXT NOT NULL,
    forge_id INTEGER,
    team TEXT REFERENCES teams(slug),
    first_seen TEXT CHECK (first_seen GLOB '????-??-??T??:??:??.?????????Z'),
    last_seen TEXT CHECK (last_seen GLOB '????-??-??T??:??:??.?????????Z'),
    UNIQUE (project_id, slug),
    UNIQUE (project_id, forge_id)
);
CREATE INDEX repos_project_idx ON repos (project_id);

-- Every slug a repository was ever known by within its project, the current
-- one included.
CREATE TABLE repo_names (
    project_id INTEGER NOT NULL REFERENCES projects(id),
    slug TEXT NOT NULL,
    repo_id INTEGER NOT NULL REFERENCES repos(id),
    PRIMARY KEY (project_id, slug)
);
CREATE INDEX repo_names_repo_idx ON repo_names (repo_id);

-- One note per merged PR, as noergler's last review round wrote it. The
-- note object is the model's; the columns beside it are facts. bugfix_area
-- is the longest common directory of the bugfix's component files, computed
-- at ingest: the problem areas group by it, never by the model's wording.
CREATE TABLE notes (
    repo_id INTEGER NOT NULL REFERENCES repos(id),
    pr_id INTEGER NOT NULL,
    team TEXT NOT NULL REFERENCES teams(slug),
    pr_key TEXT NOT NULL,
    pr_title TEXT NOT NULL,
    ticket_key TEXT,
    jira_issue_type TEXT,
    change_kind TEXT,
    bugfix TEXT CHECK (json_valid(bugfix)),
    bugfix_area TEXT,
    bugfix_component TEXT,
    note TEXT NOT NULL CHECK (json_valid(note)),
    reviewed_commit TEXT,
    source_commit TEXT NOT NULL,
    merge_commit TEXT,
    merged_at TEXT NOT NULL CHECK (merged_at GLOB '????-??-??T??:??:??.?????????Z'),
    run_id INTEGER,
    model_label TEXT,
    issues_posted INTEGER NOT NULL DEFAULT 0,
    suggestions_posted INTEGER NOT NULL DEFAULT 0,
    delivery_id TEXT NOT NULL REFERENCES deliveries(delivery_id),
    received_at TEXT NOT NULL CHECK (received_at GLOB '????-??-??T??:??:??.?????????Z'),
    -- Set by the worker once the note is folded into the descriptions (or
    -- the fold was refused and logged); NULL = still queued.
    consolidated_at TEXT CHECK (consolidated_at GLOB '????-??-??T??:??:??.?????????Z'),
    -- A fold the gateway failed (no answer, an error) is retried a bounded
    -- number of times before the note is taken off the queue for good. A
    -- wrong key must not consume every note that arrives before someone
    -- reads the log.
    fold_attempts INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (repo_id, pr_id)
);
CREATE INDEX notes_queue_idx ON notes (received_at) WHERE consolidated_at IS NULL;
CREATE INDEX notes_repo_idx ON notes (repo_id, merged_at DESC);
-- The queue page lists the notes folded in the last 24 hours, newest first.
CREATE INDEX notes_folded_idx ON notes (consolidated_at) WHERE consolidated_at IS NOT NULL;

-- What noergler reported about the round's note call, every lane, plus the
-- PR's round totals: the cache-hit rate and the cost per note are one query.
-- warnings is a JSON array of strings.
CREATE TABLE note_calls (
    id INTEGER PRIMARY KEY,
    delivery_id TEXT NOT NULL REFERENCES deliveries(delivery_id),
    team TEXT NOT NULL,
    repo_id INTEGER NOT NULL REFERENCES repos(id),
    pr_id INTEGER NOT NULL,
    run_id INTEGER,
    outcome TEXT,
    note_status TEXT,
    profile TEXT,
    model TEXT,
    deployment TEXT,
    reasoning_sent TEXT,
    finish_reason TEXT,
    call_id TEXT,
    litellm_version TEXT,
    input_tokens INTEGER,
    input_uncached_tokens INTEGER,
    cache_read_tokens INTEGER,
    cache_write_tokens INTEGER,
    output_tokens INTEGER,
    reasoning_tokens INTEGER,
    cost_nano_usd INTEGER,
    cost_input_nano_usd INTEGER,
    cost_cache_read_nano_usd INTEGER,
    cost_cache_write_nano_usd INTEGER,
    cost_output_nano_usd INTEGER,
    cost_reasoning_nano_usd INTEGER,
    cost_tool_usage_nano_usd INTEGER,
    key_spend_nano_usd INTEGER,
    headers_ms INTEGER,
    total_ms INTEGER,
    warnings TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(warnings)),
    rounds INTEGER NOT NULL DEFAULT 0,
    rounds_input_tokens INTEGER NOT NULL DEFAULT 0,
    rounds_output_tokens INTEGER NOT NULL DEFAULT 0,
    rounds_cost_nano_usd INTEGER,
    created_at TEXT NOT NULL CHECK (created_at GLOB '????-??-??T??:??:??.?????????Z')
);
CREATE INDEX note_calls_team_idx ON note_calls (team, created_at);
CREATE INDEX note_calls_note_idx ON note_calls (repo_id, pr_id);

-- The descriptions, one revision per fold: a short summary and the
-- statements, each with the sources code attached. History is kept;
-- rollback is repointing the page. A project revision has project_id and no
-- repo_id; a repository revision both, the project its repository's.
--
-- Provenance has a kind: a revision comes from a PR's note or from a
-- baseline run at a commit; exactly one of source_pr_key and source_commit
-- is set. baseline_run_id and llm_call_id carry no foreign key: the ledger
-- outlives a removed repository.
--
-- map is the project's architecture overview, rongo's flow form
-- (ui/src/diagramIR.ts) as the baseline project step returned it and
-- baseline/diagram.go validated it: project pages only, NULL until a project
-- step writes one; the merge path carries it forward. A statement's diagram
-- lives inside its statement.
--
-- repo_map is a repository page's picture of itself from inside (where it
-- is entered, its parts, what they talk to), drawn by the last step of a
-- baseline run and checked the same way: repository pages only. The map
-- step sets it on the page's current revision in place (no revision of its
-- own: the text did not change), and every new repository revision carries
-- the current one forward. repo_map_basis is the description it was drawn
-- from, as a hash (consolidate's mapBasis): a page whose text moved on shows
-- a basis that no longer matches, and the next run draws again.
CREATE TABLE revisions (
    id INTEGER PRIMARY KEY,
    scope TEXT NOT NULL CHECK (scope IN ('repository', 'project')),
    project_id INTEGER NOT NULL REFERENCES projects(id),
    repo_id INTEGER REFERENCES repos(id),
    revision INTEGER NOT NULL,
    summary TEXT NOT NULL,
    statements TEXT NOT NULL CHECK (json_valid(statements)),
    chars INTEGER NOT NULL,
    source_kind TEXT NOT NULL DEFAULT 'pr' CHECK (source_kind IN ('pr', 'baseline')),
    source_pr_key TEXT,
    source_commit TEXT,
    llm_call_id INTEGER,
    baseline_run_id INTEGER,
    map TEXT CHECK (json_valid(map)),
    repo_map TEXT CHECK (json_valid(repo_map)),
    repo_map_basis TEXT,
    created_at TEXT NOT NULL CHECK (created_at GLOB '????-??-??T??:??:??.?????????Z'),
    CHECK ((scope = 'project') = (repo_id IS NULL)),
    CONSTRAINT revisions_source_pr CHECK ((source_kind = 'pr') = (source_pr_key IS NOT NULL)),
    CONSTRAINT revisions_source_commit CHECK ((source_kind = 'baseline') = (source_commit IS NOT NULL)),
    CONSTRAINT revisions_map_scope CHECK (map IS NULL OR scope = 'project'),
    CONSTRAINT revisions_repo_map_scope CHECK (repo_map IS NULL OR scope = 'repository'),
    CONSTRAINT revisions_repo_map_basis_scope CHECK (repo_map_basis IS NULL OR scope = 'repository')
);
CREATE UNIQUE INDEX revisions_key_idx ON revisions (scope, project_id, COALESCE(repo_id, 0), revision);

-- The current revision per page. repo_id 0 is the project scope, so the key
-- is one primary key (a NULL would make every project row distinct).
CREATE TABLE pages (
    scope TEXT NOT NULL,
    project_id INTEGER NOT NULL REFERENCES projects(id),
    repo_id INTEGER NOT NULL DEFAULT 0,
    current_revision_id INTEGER NOT NULL REFERENCES revisions(id),
    updated_at TEXT NOT NULL CHECK (updated_at GLOB '????-??-??T??:??:??.?????????Z'),
    PRIMARY KEY (scope, project_id, repo_id)
);

-- The sources: the projects whose code Almanach clones itself. A source is
-- a Bitbucket project, whose repositories the REST API lists, or a static
-- folder, whose repositories the operator enters by hand. Its repositories
-- are the project: one repository, a project of one. A clone lands at
-- <base>/p<project id>/r<repo id>. The token is sealed at rest (AES-GCM) and
-- never leaves the store in clear except to the git process.
CREATE TABLE sources (
    project_id INTEGER PRIMARY KEY REFERENCES projects(id),
    kind TEXT NOT NULL CHECK (kind IN ('bitbucket', 'static')),
    host TEXT,
    token_auth TEXT NOT NULL CHECK (token_auth IN ('bearer', 'basic')),
    token_user TEXT NOT NULL DEFAULT '',
    token_enc BLOB,
    created_at TEXT NOT NULL CHECK (created_at GLOB '????-??-??T??:??:??.?????????Z'),
    updated_at TEXT NOT NULL CHECK (updated_at GLOB '????-??-??T??:??:??.?????????Z'),
    last_listed_at TEXT CHECK (last_listed_at GLOB '????-??-??T??:??:??.?????????Z'),
    last_error TEXT NOT NULL DEFAULT ''
);

-- One row per clone (bare: an object store, no working tree); the
-- repository's project is its source. origin says who put it here (the
-- listing or the operator); state is the poller's: pending (queued for the
-- next cycle), syncing, ready (the clone's remote-tracking ref of branch is
-- head_commit), failed (last_error says why, retried next cycle), missing
-- (the listing no longer names it; the clone stays until the operator
-- removes it). A NULL branch is resolved from the remote's HEAD and written
-- back.
CREATE TABLE source_repos (
    repo_id INTEGER PRIMARY KEY REFERENCES repos(id),
    project_id INTEGER NOT NULL REFERENCES sources(project_id) ON DELETE CASCADE,
    clone_url TEXT NOT NULL,
    origin TEXT NOT NULL CHECK (origin IN ('listed', 'manual')),
    branch TEXT,
    head_commit TEXT NOT NULL DEFAULT '',
    state TEXT NOT NULL CHECK (state IN ('pending', 'syncing', 'ready', 'failed', 'missing')),
    last_synced_at TEXT CHECK (last_synced_at GLOB '????-??-??T??:??:??.?????????Z'),
    last_error TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL CHECK (created_at GLOB '????-??-??T??:??:??.?????????Z')
);
CREATE INDEX source_repos_project_idx ON source_repos (project_id);

-- The baseline: Almanach reads a cloned repository at a commit and writes
-- the description it should have had from the start; the PR notes refine it
-- from then on. A run documents one repository at one commit, pinned when
-- the run starts, one model call per step, so the worker can interleave
-- steps with notes, hold after a gateway failure and resume after a
-- restart.
--
-- mode: full reads the repository with a fresh catalogue, reconcile reads
-- what changed since base_commit (the documented commit). commit is the
-- head when the run was queued until the run starts and pins it. plan is the
-- catalogue: the subjects with their files and sizes. attempts counts the
-- gateway failures of the current step; calls and bytes are what the run
-- spent against its caps; skipped counts the parts it could not fold.
-- trigger retry: the poller queues a failed run again, in the failed run's
-- mode, so the log and the source page tell a retry from a first ready or a
-- reconcile.
--
-- "commit" is quoted wherever it is named: a keyword in SQLite.
--
-- AUTOINCREMENT: a run's id is never reused, not even after its repository
-- (and so the newest run row) is removed. llm_calls and revisions keep the
-- id without a foreign key; a reused one would hand a new run the old one's
-- calls and cost.
CREATE TABLE baseline_runs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    repo_id INTEGER NOT NULL REFERENCES source_repos(repo_id) ON DELETE CASCADE,
    mode TEXT NOT NULL CHECK (mode IN ('full', 'reconcile')),
    trigger TEXT NOT NULL CHECK (trigger IN ('manual', 'first_ready', 'monthly', 'retry')),
    base_commit TEXT NOT NULL DEFAULT '',
    "commit" TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('queued', 'cataloguing', 'describing', 'project', 'done', 'failed')),
    step INTEGER NOT NULL DEFAULT 0,
    steps INTEGER NOT NULL DEFAULT 0,
    plan TEXT CHECK (json_valid(plan)),
    attempts INTEGER NOT NULL DEFAULT 0,
    calls INTEGER NOT NULL DEFAULT 0,
    bytes INTEGER NOT NULL DEFAULT 0,
    skipped INTEGER NOT NULL DEFAULT 0,
    error TEXT NOT NULL DEFAULT '',
    queued_at TEXT NOT NULL CHECK (queued_at GLOB '????-??-??T??:??:??.?????????Z'),
    started_at TEXT CHECK (started_at GLOB '????-??-??T??:??:??.?????????Z'),
    finished_at TEXT CHECK (finished_at GLOB '????-??-??T??:??:??.?????????Z'),
    CHECK (mode = 'full' OR base_commit <> '')
);
-- One open run per repository: a second click is a conflict, not a second
-- writer of the same page.
CREATE UNIQUE INDEX baseline_runs_open_idx ON baseline_runs (repo_id) WHERE state NOT IN ('done', 'failed');
CREATE INDEX baseline_runs_repo_idx ON baseline_runs (repo_id, id DESC);

-- Almanach's own gateway calls: a note's merge or a baseline run's step.
-- Its ledger, nobody else's. purpose says which step of which flow made the
-- call; the source is a PR or a run's commit, as on a revision. step is the
-- reading step a baseline call belongs to (a run's overview or subject, by
-- the run's step index), so the queue page shows each step's time and cost;
-- NULL for a merge, a catalogue, a project or a map call.
CREATE TABLE llm_calls (
    id INTEGER PRIMARY KEY,
    repo_id INTEGER NOT NULL REFERENCES repos(id),
    source_kind TEXT NOT NULL DEFAULT 'pr' CHECK (source_kind IN ('pr', 'baseline')),
    source_pr_key TEXT,
    source_commit TEXT,
    purpose TEXT NOT NULL DEFAULT 'merge' CHECK (purpose IN ('merge', 'catalogue', 'overview', 'subject', 'project', 'repo_map')),
    baseline_run_id INTEGER,
    step INTEGER,
    outcome TEXT NOT NULL,
    model TEXT,
    input_tokens INTEGER,
    cached_tokens INTEGER,
    output_tokens INTEGER,
    cost_nano_usd INTEGER,
    call_id TEXT,
    created_at TEXT NOT NULL CHECK (created_at GLOB '????-??-??T??:??:??.?????????Z'),
    CONSTRAINT llm_calls_source_pr CHECK ((source_kind = 'pr') = (source_pr_key IS NOT NULL)),
    CONSTRAINT llm_calls_source_commit CHECK ((source_kind = 'baseline') = (source_commit IS NOT NULL))
);
CREATE INDEX llm_calls_run_idx ON llm_calls (baseline_run_id) WHERE baseline_run_id IS NOT NULL;
CREATE INDEX llm_calls_repo_idx ON llm_calls (repo_id);
-- The queue page sums the calls of the last 24 hours on every poll: by
-- time, not over the whole append-only table.
CREATE INDEX llm_calls_created_idx ON llm_calls (created_at);

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
-- {"coord", "version"}, the version as written, "" when none is. reader:
-- the manifest package's Version that read them; a newer one reads every
-- repository again, its head moved or not.
CREATE TABLE repo_manifests (
    repo_id INTEGER PRIMARY KEY REFERENCES source_repos(repo_id) ON DELETE CASCADE,
    "commit" TEXT NOT NULL,
    provides TEXT NOT NULL CHECK (json_valid(provides) AND json_type(provides) = 'array'),
    requires TEXT NOT NULL CHECK (json_valid(requires) AND json_type(requires) = 'array'),
    reader INTEGER NOT NULL,
    read_at TEXT NOT NULL CHECK (read_at GLOB '????-??-??T??:??:??.?????????Z')
);
