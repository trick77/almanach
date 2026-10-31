# Configuration

Environment variables, read once at startup. Every fault is collected and
reported together, and any one aborts startup. A variable set to blank
counts as unset. `.env.example` is the working reference: a live line is
required, a commented line is optional and shows its default.

## Required

| Variable | Meaning |
| --- | --- |
| `DATABASE_URL` | PostgreSQL DSN. `migrate` needs nothing else. |
| `ALMANACH_TEAM_KEYS_PATH` | Path of the team-keys file: JSON `{"<team>": "<token>"}`, one token per team. The token is the team's identity; the same value goes into the team's `almanach:` block in noergler. Re-read when the file's modification time changes, so a new team needs no restart; a malformed file keeps the last good table and logs. Two teams sharing a token, or an empty entry, is a startup fault. Never committed. |
| `LLMWIRE_LITELLM_BASE_URL` | Host of the OpenAI-compatible gateway. Read by llmwire. |
| `LLMWIRE_LITELLM_API_KEY` | The gateway key. Read by llmwire. |
| `LLMWIRE_LITELLM_MODELS` | `<profile>=<alias>,...`: which llmwire profiles the gateway serves, under which alias. |
| `ALMANACH_MODEL` | The merge model as an llmwire **profile id**, never a gateway alias. Must be listed above and must support strict JSON-schema output, checked at startup off the profile; the gateway's model listing must carry the alias, checked at startup too. A gateway that does not answer at startup (unreachable, 5xx) is a warning: notes are taken, folds wait for it; a refused key, a 4xx, a body that is no listing or a missing alias aborts. |

## Optional

| Variable | Default | Meaning |
| --- | --- | --- |
| `ALMANACH_REASONING_EFFORT` | the model's balanced level | A reasoning level the profile accepts; anything else is a startup fault. |
| `SERVER_HOST` | `0.0.0.0` | Listen address. |
| `SERVER_PORT` | `8080` | Listen port. |
| `ALMANACH_AUTH_MODE` | `none` | `none`: no sign-in, the read API and the source writes are open and name nobody; allowed on any `SERVER_HOST`, so keep it off networks others reach. `oidc`: readers sign in at an OpenID provider (see [Signing in](#signing-in)). The webhook and the ping are bearer only in either mode. |
| `ALMANACH_REPO_MAX_STATEMENTS` | `40` | Statements a repository description may hold. |
| `ALMANACH_PROJECT_MAX_STATEMENTS` | `24` | Statements a project description may hold. |
| `ALMANACH_REPO_MAX_CHARS` | `60000` | Runes a whole repository description may hold: the summary and every statement's name and text together. No statement has a cap of its own: a description is documentation, as long as its subject needs; this is the ceiling against unbounded growth. |
| `ALMANACH_PROJECT_MAX_CHARS` | `40000` | The same for a project description. |
| `ALMANACH_DIAGRAM_MAX_NODES` | `12` | Nodes one statement diagram may hold (participants of a sequence, tables of an entity model); the project's system map is held to the drawing's 16 instead. At most 16, the most the drawing lays out; a sequence and an entity model stop at 8 whatever this says. Past about a dozen a picture stops reading and should be two; the prompts say so. The node and edge caps hold a new or changed diagram: one stored under higher caps stays as it is. |
| `ALMANACH_DIAGRAM_MAX_EDGES` | `20` | Edges one diagram may hold (messages and notes of a sequence, relationships of an entity model, which stop at 20 and 12). At most 24. |
| `ALMANACH_MAX_DIAGRAMS` | `6` | Statement diagrams one page may hold, repository or project page; the system map does not count. A diagram added past it is left out, its statement kept. `0`: no schema offers a statement diagram, the map is still drawn. A page already over a lowered cap keeps its diagrams but gains none. |
| `ALMANACH_PROBLEM_WINDOW_DAYS` | `90` | How far back the problem areas look by default; `?days=` overrides per request. |
| `ALMANACH_REPOS_DIR` | unset | The clones (the Quellen pages). Unset: the feature is off, no poller, the routes answer 404, the UI hides the entry. Set: the base folder every clone lands under as `<dir>/<project>/<slug>`; must exist and be writable (checked with a probe file at startup), and `git` must be on PATH. compose gives its container `/app/repos` on the `repos` volume; a deployment needs a volume claim there, owned by uid 1000 (fsGroup or an init chown). |
| `ALMANACH_SECRET_KEY` | unset | Base64 of 32 random bytes (`openssl rand -base64 32`). Seals the sources' tokens in the database with AES-GCM. Unset, public repositories still clone and saving a token is a 422. Changing it orphans every stored token: the affected sources fail with a line naming the key until the token is entered again. |
| `ALMANACH_SYNC_INTERVAL` | `30m` | The poller's cycle: every Bitbucket source listed, every clone fetched. `0` means on demand only (the Synchronisieren buttons). The first cycle runs thirty seconds after start whatever the interval. |
| `ALMANACH_GIT_CA_FILE` | unset | A PEM bundle git trusts for https (`GIT_SSL_CAINFO`), for a forge behind an internal CA. |
| `ALMANACH_CATALOGUE_MODEL` | `ALMANACH_MODEL` | The baseline's catalogue step (which subjects a repository is made of, which files belong to each) as an llmwire profile id: a cheaper model may pick, the main one writes. Checked at startup like `ALMANACH_MODEL`, at its balanced reasoning level. Only read with `ALMANACH_REPOS_DIR` set. |
| `ALMANACH_BASELINE_MAX_CALLS` | `16` | Model calls one baseline run may make: the catalogue, the overview, one per subject, the project. At least 3. Checked before every call; past it the rest is skipped and the run ends partial. |
| `ALMANACH_BASELINE_MAX_BYTES` | `2000000` | File content one baseline run may read: the docs its catalogue reads and every part's files. A part over what is left is skipped, and a smaller one after it may still be read. |
| `ALMANACH_BASELINE_RECONCILE` | `720h` | How long after its last run a repository whose head moved off the documented commit is read again on its own (only the changes since, and only with the source's automatic runs on). `0`: only the buttons. |
| `LOG_LEVEL` | `INFO` | `DEBUG`, `INFO`, `WARN`, `ERROR`. |
| `ALMANACH_ENV` | `dev` | Environment name in every log line. |

Over any cap the merge call's rewrite is rejected whole and the page stays
as it was; the caps are the one lever against endless growth, and the
prompt tells the model the numbers. A statement diagram over a node or
edge cap, or one that breaks a rule (an arrow to an undeclared node),
rejects the whole rewrite the same way; one past the page's count is left
out, its statement kept. The system map never costs a rewrite: one that
fails is not drawn and the stored one stays.

## Signing in

With `ALMANACH_AUTH_MODE=oidc` every variable below is required, and each
is a startup fault under `none`, where it would look active and change
nothing. The provider's discovery document is fetched at startup, each
attempt bounded to 30 s. A provider that does not answer is a warning,
not a fault: the webhook, the ping and the wiki's 401 keep working, the
login routes answer 503, and discovery is retried in the background
(5 s, doubling up to 5 min), each failure logged, until it answers.

| Variable | Meaning |
| --- | --- |
| `ALMANACH_OIDC_ISSUER` | The provider's issuer exactly as the provider states it, path and trailing slash included (`https://auth.example.com`, `https://idp.example.com/realms/wiki`, `https://tenant.example.com/`): discovery compares it byte for byte. https; plain http only on a loopback host, for a local provider. No user info, query or fragment. |
| `ALMANACH_OIDC_CLIENT_ID` | The client registered for Almanach at the provider. Scopes asked: `openid profile email`. |
| `ALMANACH_OIDC_CLIENT_SECRET` | Its secret. |
| `ALMANACH_OIDC_REDIRECT_URL` | The callback as the browser reaches it: exactly `https://<host>/api/auth/callback`, nothing after the path. The session and login cookies are always Secure: behind a TLS-terminating proxy the process sees plain HTTP. |
| `ALMANACH_SESSION_SECRET` | Signs the session cookie `almanach_session`, at least 32 bytes (`openssl rand -base64 48`). There is no session table: rotating it signs everyone out. |

A session lasts 30 days and is not renewed. Every route under `/api/` but
`/api/auth/login` and `/api/auth/callback` needs it, the sources' write
routes included: whoever the provider lets in may configure the clones,
and the reader (preferred username, else email, else subject) is on the
log line of every read and write. A change (POST, PUT, PATCH, DELETE)
must come from Almanach's own pages: `Sec-Fetch-Site` same-origin or
none, or, without it, an `Origin` naming the request's host or the
redirect URL's origin; anything else is a 403. Who may sign in at all is
the provider's policy. `/webhooks/noergler`, `/auth/ping`, `/healthz`,
`/readyz` and the UI shell are outside the sign-in.

## Commands

| Command | What |
| --- | --- |
| `almanach serve` | Run the service. Refuses a schema that is behind. |
| `almanach migrate` | Apply pending migrations and exit. |
| `almanach version` | Print the version. |
| `almanach -healthcheck` | Probe `/healthz` on loopback, exit 0 on a 2xx. |
| `almanach -readycheck` | Probe `/readyz`: the database answers. |
