# Configuration

Environment variables, read once at startup. Every fault is collected and
reported together, and any one aborts startup. A variable set to blank
counts as unset. `.env.example` is the working reference: a live line is
required, a commented line is optional and shows its default.

Every variable is `BACKEND_*`, named for the process, never the product.
A name an earlier release read (`ALMANACH_*`, `SERVER_HOST`, `SERVER_PORT`)
is a startup fault that names its replacement. The page ceilings
(`BACKEND_REPO_MAX_STATEMENTS`, `BACKEND_PROJECT_MAX_STATEMENTS`,
`BACKEND_REPO_MAX_CHARS`, `BACKEND_PROJECT_MAX_CHARS`) are gone: a
description is system documentation, never cut to a size, and one still set
is a startup fault too.

## Required

| Variable | Meaning |
| --- | --- |
| `BACKEND_DB_PATH` | The SQLite database file, e.g. `/data/almanach.db`; created on first open if missing. Its folder must exist and be writable (the file's `-wal` and `-shm` land beside it), checked at startup by `serve` and `migrate` alike. A local disk on its own volume, never a shared filesystem: WAL needs the locks of one host. `migrate` needs nothing else. |
| `BACKEND_TEAM_KEYS_PATH` | Path of the team-keys file: JSON `{"<team>": "<token>"}`, one token per team. The token is the team's identity; the same value goes into the team's `almanach:` block in noergler. In oidc mode it also opens the two Markdown exports (`/api/projects/{key}.md`, `/api/projects/{key}/repos/{slug}.md`) read-only, for a coding agent; no other route. Re-read when the file's modification time changes, so a new team needs no restart; a malformed file keeps the last good table and logs. Two teams sharing a token, or an empty entry, is a startup fault. Never committed. compose mounts the folder `./conf` read-only at `/conf` and sets `/conf/team-keys.json` (`cp conf/team-keys.example.json conf/team-keys.json`): a folder, because Docker turns a missing bind-mounted file into a directory. |
| `LLMWIRE_LITELLM_BASE_URL` | Host of the OpenAI-compatible gateway. Read by llmwire. Gateway route only. |
| `LLMWIRE_LITELLM_API_KEY` | The gateway key. Read by llmwire. Gateway route only. |
| `LLMWIRE_LITELLM_MODELS` | `<profile>=<alias>,...`: which llmwire profiles the gateway serves, under which alias. A profile listed here goes through the gateway. |
| `LLMWIRE_<PROVIDER>_API_KEY` | Direct route: a profile not listed in `LLMWIRE_LITELLM_MODELS` goes straight to its provider with this key, e.g. `LLMWIRE_MIMO_API_KEY` for `mimo-v2.6-pro`. Unset, startup aborts naming the variable (`needs LLMWIRE_MIMO_API_KEY set in the environment`). Read by llmwire; one per provider the configured models use. |
| `BACKEND_MODEL` | The merge model as an llmwire **profile id**, never a gateway alias. Reached by one of the two routes above and must support strict JSON-schema output, checked at startup off the profile; on the gateway route the gateway's model listing must carry the alias, checked at startup too. A gateway that does not answer at startup (unreachable, 5xx) is a warning: notes are taken, folds wait for it; a refused key, a 4xx, a body that is no listing or a missing alias aborts. |

## Optional

| Variable | Default | Meaning |
| --- | --- | --- |
| `BACKEND_MODEL_REASONING_EFFORT` | the model's balanced level | A reasoning level the profile accepts, or `off` for no reasoning (a no-op on a model that never reasons, a startup fault on one that reasons and cannot be switched off); anything else is a startup fault. Thinking is most of a call's output: measured on a baseline subject step, about 1.5k of 22k output tokens were the patch. |
| `BACKEND_ADDR` | `0.0.0.0:8080` | Listen address, `host:port`; an empty host (`:8080`) is every interface. |
| `BACKEND_AUTH_MODE` | `none` | `none`: no sign-in, the read API and the source writes are open and name nobody; allowed on any `BACKEND_ADDR`, so keep it off networks others reach. `oidc`: readers sign in at an OpenID provider (see [Signing in](#signing-in)). The webhook and the ping are bearer only in either mode. `compose.yaml` fixes `oidc`, `compose.dev.yaml` `none`; `.env` sets neither. |
| `BACKEND_DIAGRAM_MAX_NODES` | `12` | Nodes one statement diagram may hold (participants of a sequence, tables of an entity model); the project's system map is held to the drawing's 16 instead. At most 16, the most the drawing lays out; a sequence and an entity model stop at 8 whatever this says. Past about a dozen a picture stops reading and should be two; the prompts say so. The node and edge caps hold a new or changed diagram: one stored under higher caps stays as it is. |
| `BACKEND_DIAGRAM_MAX_EDGES` | `20` | Edges one diagram may hold (messages and notes of a sequence, relationships of an entity model, which stop at 20 and 12). At most 24. |
| `BACKEND_DIAGRAM_MAX_PER_PAGE` | `6` | Statement diagrams one page may hold, repository or project page; the system map does not count. A diagram added past it is left out, its statement kept. `0`: no schema offers a statement diagram, the map is still drawn. A page already over a lowered cap keeps its diagrams but gains none. |
| `BACKEND_PROBLEM_WINDOW_DAYS` | `90` | How far back the problem areas look by default; `?days=` overrides per request. |
| `BACKEND_REPOS_DIR` | unset | The clones (the Quellen pages). Unset: the feature is off, no poller, the routes answer 404, the UI hides the entry. Set: the base folder every clone lands under as `<dir>/p<project id>/r<repo id>` (by ids, so a rename never moves one); must exist and be writable (checked with a probe file at startup), and `git` must be on PATH. compose gives its container `/app/repos` on the `repos` volume; a deployment needs a volume claim there, owned by uid 65532 (fsGroup 65532 or an init chown; a runAsUser, if set, 65532: the image has a passwd entry for no other uid, and git looks its own up). |
| `BACKEND_SOURCE_TOKEN_KEY` | unset | Base64 of 32 random bytes (`openssl rand -base64 32`). Seals the sources' tokens in the database with AES-GCM. Unset, public repositories still clone and saving a token is a 422. Changing it orphans every stored token: the affected sources fail with a line naming the key until the token is entered again. |
| `BACKEND_SYNC_INTERVAL` | `30m` | The poller's cycle: every Bitbucket source listed, every clone fetched. `0` means on demand only (the Synchronisieren buttons). The first cycle runs thirty seconds after start whatever the interval. |
| `BACKEND_BITBUCKET_HOST` | unset | The Bitbucket base URL a new Bitbucket source starts with on the Quellen page, e.g. `https://bitbucket.example.com`: https, no user info, query or fragment, a trailing slash trimmed; anything else is a startup fault. Only read, and checked, with `BACKEND_REPOS_DIR` set. Only prefills the field: a source may name another host, and the host stored on an existing source stays. Unset, the field starts empty. |
| `BACKEND_GIT_CA_FILE` | unset | A PEM bundle git trusts for https (`GIT_SSL_CAINFO`), for a forge behind an internal CA. The path as the process sees it: under compose the file goes into `./conf` and this reads `/conf/ca.pem`. |
| `BACKEND_BASELINE_CATALOGUE_MODEL` | `BACKEND_MODEL` | The baseline's catalogue step (which subjects a repository is made of, which files belong to each) as an llmwire profile id: a cheaper model may pick, the main one writes. Checked at startup like `BACKEND_MODEL`, at its balanced reasoning level. Only read with `BACKEND_REPOS_DIR` set. |
| `BACKEND_BASELINE_MAX_CALLS` | `16` | Model calls one baseline run may make: the catalogue, the overview, one per subject, the project, the repository map (kept back from the subjects, the first dropped when spent). At least 3. Checked before every call; past it the rest is skipped and the run ends partial. |
| `BACKEND_BASELINE_MAX_BYTES` | `4000000` | File content one baseline run may read: the docs its catalogue reads and every part's files. A part over what is left is skipped, and a smaller one after it may still be read. |
| `BACKEND_BASELINE_RECONCILE` | `720h` | How long after its last run a repository whose head moved off the documented commit is read again on its own (only the changes since). While it is on, a done run at the head that skipped parts or left the repository page without its map is also retried a day after it ended (the interval when shorter), at most three retries in a row: only the skipped parts and the map are read. `0`: only the buttons, but a failed run is still tried again. |
| `BACKEND_LOG_LEVEL` | `INFO` | `DEBUG`, `INFO`, `WARN`, `ERROR`. |
| `BACKEND_ENV` | `dev` | Environment name in every log line. |

A description has no cap: no setting limits its statements or its size,
and nothing is dropped for either. A statement diagram over a node or
edge cap, one that breaks a rule (an arrow to an undeclared node), or one
past the page's count never costs a rewrite: it is left out and logged,
its statement kept. The diagram the statement had stays while its name
and text stay, and goes when they changed. The system map never costs a
rewrite either: one that fails is not drawn and the stored one stays.

## Signing in

With `BACKEND_AUTH_MODE=oidc` every variable below is required, and each
is a startup fault under `none`, where it would look active and change
nothing. The provider's discovery document is fetched in the background
from startup on, each attempt bounded to 30 s; the listener never waits
for it. A provider that cannot be reached (no connection, a timeout, a
5xx) is a warning, not a fault: the webhook, the ping and the wiki's 401
keep working, the login lands on a page that says so
(`/?auth_error=provider_unavailable`), and discovery is retried (5 s,
doubling up to 5 min), each failure logged, until it answers. A provider
that answers but disagrees with the configuration (an issuer that does
not match, a 4xx, a page that is no discovery document) is a fault:
logged at error level once, never retried, the login landing on
`/?auth_error=provider_misconfigured` until the configuration is fixed
and the service restarted. A deep link survives the sign-in: the login carries the page in
`?next=` (a path on this site only) and the callback returns there.

| Variable | Meaning |
| --- | --- |
| `BACKEND_OIDC_ISSUER` | The provider's issuer exactly as the provider states it, path and trailing slash included (`https://auth.example.com`, `https://idp.example.com/realms/wiki`, `https://tenant.example.com/`): discovery compares it byte for byte. https; plain http only on a loopback host, for a local provider. No user info, query or fragment. |
| `BACKEND_OIDC_CLIENT_ID` | The client registered for Almanach at the provider. Scopes asked: `openid profile email`. |
| `BACKEND_OIDC_CLIENT_SECRET` | Its secret. |
| `BACKEND_OIDC_REDIRECT_URL` | The callback as the browser reaches it: exactly `https://<host>/api/auth/callback`, nothing after the path. The session and login cookies are always Secure: behind a TLS-terminating proxy the process sees plain HTTP. |
| `BACKEND_SESSION_SECRET` | Signs the session cookie `almanach_session`, at least 32 bytes (`openssl rand -base64 48`). There is no session table: rotating it signs everyone out. |

A session lasts 30 days and is not renewed. Every route under `/api/` but
`/api/auth/login` and `/api/auth/callback` needs it, the sources' write
routes included: whoever the provider lets in may configure the clones,
and the reader (preferred username, else email, else subject) is on the
log line of every read and write. A change (POST, PUT, PATCH, DELETE)
must come from Almanach's own pages, in every mode: `Sec-Fetch-Site`
same-origin or none, or, without it, an `Origin` naming the request's
host (or, in oidc mode, the redirect URL's origin); anything else is a
403. In oidc mode a change carrying neither header is refused too; in
none mode a client like curl still writes. Who may sign in at all is
the provider's policy. `/webhooks/noergler`, `/auth/ping`, `/healthz`,
`/readyz` and the UI shell are outside the sign-in.

## Compose

`compose.yaml` is the production stack: no host port, a Traefik router on
the external `traefik` network, `oidc` fixed. It reads `.env` whole
(`env_file`, optional), then sets what must not come from there: the
database, keys and clone paths, the listener, `HOME` and `TMPDIR` for git
on the read-only root filesystem, and the mode. The variables below it
checks itself (`${VAR:?}`): blank or missing stops `up` with the
variable's name. `.env.example` leaves them blank.

| Variable | Meaning |
| --- | --- |
| `PUBLIC_FQDN` | The hostname Traefik routes Almanach on. Read by compose only. |
| `BACKEND_OIDC_ISSUER`, `BACKEND_OIDC_CLIENT_ID`, `BACKEND_OIDC_CLIENT_SECRET`, `BACKEND_OIDC_REDIRECT_URL`, `BACKEND_SESSION_SECRET` | As in [Signing in](#signing-in). |

Checked means interpolated, and when Compose interpolates, a variable
exported in the shell beats `.env`: a stale export silently wins over the
file. Unset it, or keep the values in env files only. `docker compose
config` prints every value, the secrets included: never share its
output.

`compose.dev.yaml` on top (`make dev`, `make dev-down`) publishes
`127.0.0.1:8081`, sets `none`, blanks the OIDC variables in the container
and drops the router and the network. Compose checks the variables above
while it reads `compose.yaml`, before any override applies, so
`make dev` passes `compose.dev.env` after `.env` (`--env-file` twice, the
later one wins): stand-ins for those checks, never real values.

Paths in `.env` are the process's view. compose sets the database, keys
and clone paths itself; `BACKEND_GIT_CA_FILE` it leaves to `.env`, so
under compose the bundle sits in `./conf`, mounted read-only at `/conf`,
and the variable reads `/conf/ca.pem`.

`up` pulls the published image when it is missing, so a host needs only
`compose.yaml`, `.env` and `conf/`; a new release is `docker compose pull`
then `up -d`. In a checkout, `up -d --build` builds it instead. Needs
Docker Compose 2.24.4 or later.

The stop grace is 11 minutes: the worker waits up to the call timeout
plus 15 s for a paid call in flight before it cuts it off, and Docker's
default 10 s would kill it.

## Commands

| Command | What |
| --- | --- |
| `almanach serve` | Run the service. Refuses a schema that is behind. |
| `almanach migrate` | Apply pending migrations to `BACKEND_DB_PATH` and exit. |
| `almanach version` | Print the version. |
| `almanach -healthcheck` | Probe `/healthz` on loopback, exit 0 on a 2xx. |
| `almanach -readycheck` | Probe `/readyz`: the database answers. |
