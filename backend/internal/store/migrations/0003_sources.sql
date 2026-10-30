-- The sources: the projects whose code Almanach clones itself, apart from
-- the noergler-fed structure (projects, repos carry a team and are upserted
-- from deliveries). A source is a Bitbucket project, whose repositories the
-- REST API lists, or a static parent folder, whose repositories the operator
-- enters by hand. Either way a clone lands at <base>/<project_key>/<slug>.
-- The token is encrypted at rest (AES-GCM under ALMANACH_SECRET_KEY) and
-- never leaves the store in clear except to the git process.
CREATE TABLE sources (
    project_key TEXT PRIMARY KEY,
    kind TEXT NOT NULL CHECK (kind IN ('bitbucket', 'static')),
    host TEXT,
    token_auth TEXT NOT NULL CHECK (token_auth IN ('bearer', 'basic')),
    token_user TEXT NOT NULL DEFAULT '',
    token_enc BYTEA,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_listed_at TIMESTAMPTZ,
    last_error TEXT NOT NULL DEFAULT ''
);

-- One row per clone. origin says who put it here (the listing or the
-- operator); state is the poller's: pending (queued for the next cycle),
-- syncing, ready (the checkout holds head_commit of branch), failed
-- (last_error says why, retried next cycle), missing (the listing no longer
-- names it; the checkout stays until the operator removes it). A NULL
-- branch is resolved from the remote's HEAD and written back.
CREATE TABLE source_repos (
    project_key TEXT NOT NULL REFERENCES sources(project_key) ON DELETE CASCADE,
    slug TEXT NOT NULL,
    clone_url TEXT NOT NULL,
    origin TEXT NOT NULL CHECK (origin IN ('listed', 'manual')),
    branch TEXT,
    head_commit TEXT NOT NULL DEFAULT '',
    state TEXT NOT NULL CHECK (state IN ('pending', 'syncing', 'ready', 'failed', 'missing')),
    last_synced_at TIMESTAMPTZ,
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (project_key, slug)
);
