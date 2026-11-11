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
| `BACKEND_DB_PATH` | The SQLite database file, e.g. `/data/almanach.db`; created on first open if missing. Its folder must exist and be writable (the file's `-wal` and `-shm` land beside it), checked at startup by `serve` and `migrate` alike. A local disk on its own volume, never a shared filesystem: WAL needs the locks of one host. `serve` also keeps a model answer cut short or off its schema in `cuts/` beside it, whole and redacted (`cut-<call-id>.json`, `unparseable-<call-id>.json`), the newest 20 of each, up to ~250 kB a file, each with its call's budget: `cap` (the answer's), `room` (reasoning), `wire_cap` (the max_tokens sent: both plus llmwire's reasoning allowance, held to `max_output`, the model's limit), `page_chars` (a restyle's page), `prompt_tokens_est` (an estimate, not the gateway's count), `out_tokens` and `reasoning_tokens` (the gateway's), `warnings` (llmwire's on the request); an unknown left out: the gateway stores none. `migrate` needs nothing else. |
| `BACKEND_TEAM_KEYS_PATH` | Path of the team-keys file: JSON `{"<team>": "<token>"}`, one token per team. The token is the team's identity; the same value goes into the team's `almanach:` block in noergler. An entry may be an object, `{"token": "<token>", "projects": ["<key>", ...]}`: the projects the team may write into though another team's notes own them (`BACKEND_TEAM_CLAIMS`), keys in any case; a field other than these two is a malformed file. In oidc mode it also opens the two Markdown exports (`/api/projects/{key}.md`, `/api/projects/{key}/repos/{slug}.md`) read-only, for a coding agent; no other route. Re-read when the file's modification time changes, so a new team needs no restart; a malformed file keeps the last good table and logs. Two teams sharing a token, or an empty entry, is a startup fault. Never committed. compose mounts the folder `./conf` read-only at `/conf` and sets `/conf/team-keys.json` (`cp conf/team-keys.example.json conf/team-keys.json`): a folder, because Docker turns a missing bind-mounted file into a directory. |
| `LLMWIRE_LITELLM_BASE_URL` | Host of the OpenAI-compatible gateway. Read by llmwire. Gateway route only. Every call streams, and a gateway puts a streamed call's cost only on its usage object: LiteLLM needs `litellm_settings.include_cost_in_streaming_usage: true`, or every call is recorded unpriced (logged "call unpriced" with the reason). |
| `LLMWIRE_LITELLM_API_KEY` | The gateway key. Read by llmwire. Gateway route only. |
| `LLMWIRE_LITELLM_MODELS` | `<profile>=<alias>,...`: which llmwire profiles the gateway serves, under which alias. A profile listed here goes through the gateway. |
| `LLMWIRE_<PROVIDER>_API_KEY` | Direct route: a profile not listed in `LLMWIRE_LITELLM_MODELS` goes straight to its provider with this key, e.g. `LLMWIRE_MIMO_API_KEY` for `mimo-v2.6-pro`. Unset, startup aborts naming the variable (`needs LLMWIRE_MIMO_API_KEY set in the environment`). Read by llmwire; one per provider the configured models use. |
| `BACKEND_MODEL` | The merge model as an llmwire **profile id**, never a gateway alias. Reached by one of the two routes above and must support strict JSON-schema output, checked at startup off the profile; on the gateway route the gateway's model listing must carry the alias, checked at startup too. A gateway that does not answer at startup (unreachable, 5xx) is a warning: notes are taken and wait for it; a refused key, a 4xx, a body that is no listing or a missing alias aborts. |

## Optional

| Variable | Default | Meaning |
| --- | --- | --- |
| `BACKEND_MODEL_REASONING_EFFORT` | the model's balanced level | A reasoning level the profile accepts, or `off` for no reasoning (a no-op on a model that never reasons, a startup fault on one that reasons and cannot be switched off); anything else is a startup fault. Thinking is most of a call's output: measured on a baseline subject step, about 1.5k of 22k output tokens were the patch. |
| `BACKEND_ADDR` | `0.0.0.0:8080` | Listen address, `host:port`; an empty host (`:8080`) is every interface. |
| `BACKEND_AUTH_MODE` | `none` | `none`: no sign-in, the read API and the source writes are open and name nobody; allowed on any `BACKEND_ADDR`, so keep it off networks others reach. `oidc`: readers sign in at an OpenID provider (see [Signing in](#signing-in)). The webhook and the ping are bearer only in either mode. `compose.yaml` fixes `oidc`, `compose.dev.yaml` `none`; `.env` sets neither. |
| `BACKEND_TEAM_CLAIMS` | `log` | What the webhook does with a note for a project another team owns (the bearer is the team; a project belongs to the first team whose note named it, and keeps it). `log`: stored as any note, with a warning line naming team, project and owner: the grace a rollout starts with, nothing lost, the log saying who writes where. `enforce`: a 403 the sender sees, nothing stored; noergler keeps the note pending and sends it again on the team's next merge, so it lands once the project is listed. Either way the team-keys file may list the project for the sender (see `BACKEND_TEAM_KEYS_PATH`): a repository that changed hands, two teams on one project. A listing names the project by any key it had, in any case, so a rename keeps it. A listing shares, it does not transfer: the owner stays, both teams write, and a listed team's note never makes it the owner of a project nobody owns yet. |
| `BACKEND_DIAGRAM_MAX_NODES` | `12` | Nodes one statement diagram may hold (participants of a sequence, tables of an entity model); a system map, the project's or a repository's, is held to its own 22 nodes and 33 edges instead. At most 16, the most the drawing lays out; a sequence and an entity model stop at 8 whatever this says. Past about a dozen a picture stops reading and should be two; the prompts say so. The node and edge caps hold a new or changed diagram: one stored under higher caps stays as it is. |
| `BACKEND_DIAGRAM_MAX_EDGES` | `20` | Edges one diagram may hold (messages and notes of a sequence, relationships of an entity model, which stop at 20 and 12). At most 24. |
| `BACKEND_DIAGRAM_MAX_PER_PAGE` | `6` | Statement diagrams one page may hold, repository or project page; the system map does not count. A diagram added past it is left out, its statement kept. `0`: no schema offers a statement diagram, the map is still drawn. A page already over a lowered cap keeps its diagrams but gains none. |
| `BACKEND_FOLD_DELAY` | `24h` | How long a repository's notes wait, from the oldest of them, before one merge call consolidates them all, in merge order, at most 240 kB of notes a call (the rest in the next). The page then lags the code by up to this long, and a day of pull requests costs one call instead of one each; a burst of notes delivered by mistake is consolidated in a few calls. `0` consolidates each note on its own as it comes. Negative is a startup fault. |
| `BACKEND_PROBLEM_WINDOW_DAYS` | `90` | How far back the problem areas look by default; `?days=` overrides per request. |
| `BACKEND_REPOS_DIR` | unset | The clones (the Quellen pages). Unset: the feature is off, no poller, the routes answer 404, the UI hides the entry. Set: the base folder every clone lands under as `<dir>/p<project id>/r<repo id>` (by ids, so a rename never moves one); must exist and be writable (checked with a probe file at startup), and `git` must be on PATH. compose gives its container `/app/repos` on the `repos` volume; a deployment needs a volume claim there, owned by uid 65532 (fsGroup 65532 or an init chown; a runAsUser, if set, 65532: the image has a passwd entry for no other uid, and git looks its own up). |
| `BACKEND_SOURCE_TOKEN_KEY` | unset | Base64 of 32 random bytes (`openssl rand -base64 32`). Seals the sources' tokens in the database with AES-GCM. Unset, public repositories still clone and saving a token is a 422. Changing it orphans every stored token: the affected sources fail with a line naming the key until the token is entered again. |
| `BACKEND_SYNC_INTERVAL` | `30m` | The poller's cycle: every Bitbucket source listed, every clone fetched. `0` means on demand only (the Synchronisieren buttons); otherwise at least `1m`, a startup fault below. The first cycle runs thirty seconds after start whatever the interval. |
| `BACKEND_BITBUCKET_HOST` | unset | The Bitbucket base URL a new Bitbucket source starts with on the Quellen page, e.g. `https://bitbucket.example.com`: https, no user info, query or fragment, a trailing slash trimmed; anything else is a startup fault. Only read, and checked, with `BACKEND_REPOS_DIR` set. Only prefills the project URL field as `<host>/projects/`, the key left to type: a source may name another host, and the host stored on an existing source stays. Unset, the field starts empty. |
| `BACKEND_GIT_CA_FILE` | unset | A PEM bundle git trusts for https (`GIT_SSL_CAINFO`), for a forge behind an internal CA; the Bitbucket listing trusts it too, as git does: the file alone, not the system's store, read again for each listing. A file that cannot be read or holds no certificate is a startup fault; a relative path is taken from the process's working directory. Not the sign-in: an OpenID provider behind an internal CA needs `SSL_CERT_FILE`. The path as the process sees it: under compose the file goes into `./conf` and this reads `/conf/ca.pem`. |
| `BACKEND_GIT_TIMEOUT` | `10m` | How long one git command that reaches a remote, a clone aside (a fetch, `ls-remote`, a brief's push), may run before it is cut and recorded as that repository's failure. The poller's cycle is sequential: without a bound one stalled fetch held every source, and every baseline step waiting for that clone, for good. A connection carrying nothing at all for five minutes is also cut by git's own stall guard, whatever this says (nothing, not slowly: a server preparing a large pack sends only keep-alives for minutes). A fetch that honestly needs longer (a huge push since the last cycle) fails every cycle saying `cut after 10m0s (BACKEND_GIT_TIMEOUT)`: raise it. Must be positive. Until 0.1.33 this bounded the clone too, at `1h`: a value raised for a big clone belongs in `BACKEND_GIT_CLONE_TIMEOUT` now. |
| `BACKEND_GIT_CLONE_TIMEOUT` | `1h` | The same bound for a clone, apart because a first clone of a big repository honestly takes long and a fetch never should. One that needs longer is cut and redone from nothing every cycle, its failure saying `cut after 1h0m0s (BACKEND_GIT_CLONE_TIMEOUT)`: raise it for such a repository. Must be positive. |
| `BACKEND_BASELINE_CATALOGUE_MODEL` | `BACKEND_MODEL` | The baseline's catalogue step (which subjects a repository is made of, which files belong to each) as an llmwire profile id: a cheaper model may pick, the main one writes. Checked at startup like `BACKEND_MODEL`, at its balanced reasoning level. Only read with `BACKEND_REPOS_DIR` set. |
| `BACKEND_BASELINE_MAX_CALLS` | `16` | Model calls one baseline run may make: the catalogue (the router in its place; a router call that fails or picks nothing counts on top), the overview, one per subject, the project, the repository map (the subjects keep a call back for the map, and one for the project when it will call; a refused map asked again, at most twice, counts too). At least 3; below 4 the map's call is kept only where the catalogue and one part leave room, best effort. Checked before every call; past it the rest is skipped and the run ends partial. |
| `BACKEND_BASELINE_MAX_BYTES` | `4000000` | File content one baseline run may read: the docs its catalogue reads and every part's files. A part over what is left is skipped, and a smaller one after it may still be read. At the default 16 calls a run reads at most about 3.1 MB (a step shows at most 240 kB), so the calls end it first; the bytes cap a run only once `BACKEND_BASELINE_MAX_CALLS` is raised. Below about 240 kB no subject fits. |
| `BACKEND_BASELINE_MAX_INPUT_TOKENS` | `1500000` | Prompt input one baseline run may send its catalogue and reading calls. The default is meant to leave a full run of a large repository whole and catch the outlier, a bloated page or listing. The bytes count only the files; a step's prompt also carries the page, the rules and the project, which grow as the run goes. Each prompt is estimated before its call at 3 bytes a token (held high) and counted as the gateway reports it, the estimate where it reports none. A part whose prompt would pass what is left is skipped, and a smaller one after it may still be read; a catalogue that would leave nothing fails the run before its call. The project and map steps come after the reading and are not counted. The router's prompt (`BACKEND_BASELINE_ROUTER`) takes at most a quarter of it, so the reading keeps three quarters: below about `350000` (`860000` with a 256k context) that, not the model's context, sizes the router's listing, and a tree too wide for it is catalogued. At least `100000`, one full reading step. |
| `BACKEND_BASELINE_ROUTER` | `true` | A full run's plan by the router instead of the catalogue: the catalogue model reads every readable file's skeleton and hints and picks, by subject, the files to read in full, with the questions a reader would ask of each (shown to its step). `false` plans by the catalogue. Its context is the `max_input_tokens` the gateway's model listing gives the catalogue model's alias at startup, capped at 256k tokens, 128k when it lists none; the listing it reads grows with it (244 kB of 1024 bytes at 128k, 494 kB at 256k), its prompt at most a quarter of `BACKEND_BASELINE_MAX_INPUT_TOKENS`; startup logs what it is held to. A tree too wide for its context or that share, a skeleton read that fails, a router call that fails or picks no readable file, or a catalogue model that cannot route, is catalogued as before, logged (a failed router call is paid on top). While it is on, a reconcile of a routed plan reads only the changes its subjects hold and files not readable before; a change to a file the router passed over waits for the next full run, unless its share of the readable tree, with the picked changes' share of the plan, passes a half, which reads it afresh. Turned off, a routed plan reconciles like any other. |
| `BACKEND_BASELINE_RECONCILE` | `720h` | How long after its last run a repository whose head moved off the documented commit is read again on its own (only the changes since). While it is on, a done run at the head that skipped parts or left the repository page without its map is also retried a day after it ended, at most three retries in a row: only the skipped parts and the map are read. `0`: only the buttons, but a failed run is still tried again. Otherwise at least `24h`, the retry's own wait: every moved head is a paid read, and an hourly one is twenty-four a day. |
| `BACKEND_DAILY_BUDGET_USD` | unset | What Almanach's own model calls may cost in one UTC day, a positive decimal amount (`25`, `12.50`), read as an integer of nano-USD; `0` is a startup fault, unset means no ceiling. Only read, and allowed, with `BACKEND_REPOS_DIR` set: without the sources there is no run to hold. Spent, no baseline run starts until the next day (the queue page says so on the run), but a departure run (a deactivated repository leaving its project page: one call, no code read, paid past the budget); one already started goes on to its end, so the day may overshoot by one run's calls (`BACKEND_BASELINE_MAX_CALLS` bounds that). A note's call goes on whatever it says, a note being a small delta already paid for in noergler's context. Counts priced calls only: a gateway that reports no cost spends nothing against it (the Kosten page shows the unpriced calls). |
| `BACKEND_LOG_LEVEL` | `INFO` | `DEBUG`, `INFO`, `WARN`, `ERROR`. At `INFO` the access log has a line per reader request and per webhook delivery; the UI's polled reads (the queue count, the queue page, a source page) and static files only at `DEBUG`. |
| `BACKEND_METRICS_TOKEN` | unset | A bearer that opens `/metrics` from anywhere, at least 32 bytes (`openssl rand -base64 48`); no team's token does. Unset, only a direct loopback caller (inside the container) reads it, as on the probes; never route `/metrics` through a proxy on the same host, which would arrive as loopback. The startup line says only whether it is set. |
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
not match, a 4xx other than a 408 or a 429, a page that is no discovery document) is a fault:
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

The stop grace is 11 minutes: the HTTP drain (15 s), then the worker
waits up to the call timeout plus 15 s for a paid call in flight before
it cuts it off, then 5 s for the planner's refresh (`PRAGMA optimize`);
Docker's default 10 s would kill the call.

## Commands

| Command | What |
| --- | --- |
| `almanach serve` | Run the service. Refuses a schema that is behind. |
| `almanach migrate` | Apply pending migrations to `BACKEND_DB_PATH` and exit. |
| `almanach backup <file>` | Write a consistent copy of `BACKEND_DB_PATH` to the file (`VACUUM INTO`, while `serve` runs on) and exit. The file must not exist. |
| `almanach version` | Print the version. |
| `almanach -healthcheck` | Probe `/healthz` on loopback, exit 0 on a 2xx. |
| `almanach -readycheck` | Probe `/readyz`: the database answers and its disk has 64 MB free. |
