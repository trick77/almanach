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
| `ALMANACH_AUTH_MODE` | `none` | `none`: the read API names nobody. `proxy`: an OAuth sidecar in front sets `X-Forwarded-User`, logged as the reader; `serve` then refuses any `SERVER_HOST` but `127.0.0.1`, so the header can only come from the sidecar. The webhook and the ping never read the header in either mode. |
| `ALMANACH_REPO_MAX_STATEMENTS` | `40` | Statements a repository description may hold. |
| `ALMANACH_PROJECT_MAX_STATEMENTS` | `24` | Statements a project description may hold. |
| `ALMANACH_REPO_MAX_CHARS` | `60000` | Runes a whole repository description may hold: the summary and every statement's name and text together. No statement has a cap of its own: a description is documentation, as long as its subject needs; this is the ceiling against unbounded growth. |
| `ALMANACH_PROJECT_MAX_CHARS` | `40000` | The same for a project description. |
| `ALMANACH_PROBLEM_WINDOW_DAYS` | `90` | How far back the problem areas look by default; `?days=` overrides per request. |
| `ALMANACH_REPOS_DIR` | unset | The clones (the Quellen pages). Unset: the feature is off, no poller, the routes answer 404, the UI hides the entry. Set: the base folder every clone lands under as `<dir>/<project>/<slug>`; must exist and be writable (checked with a probe file at startup), and `git` must be on PATH. compose gives its container `/app/repos` on the `repos` volume; a deployment needs a volume claim there, owned by uid 1000 (fsGroup or an init chown). |
| `ALMANACH_SECRET_KEY` | unset | Base64 of 32 random bytes (`openssl rand -base64 32`). Seals the sources' tokens in the database with AES-GCM. Unset, public repositories still clone and saving a token is a 422. Changing it orphans every stored token: the affected sources fail with a line naming the key until the token is entered again. |
| `ALMANACH_SYNC_INTERVAL` | `30m` | The poller's cycle: every Bitbucket source listed, every clone fetched. `0` means on demand only (the Synchronisieren buttons). The first cycle runs thirty seconds after start whatever the interval. |
| `ALMANACH_GIT_CA_FILE` | unset | A PEM bundle git trusts for https (`GIT_SSL_CAINFO`), for a forge behind an internal CA. |
| `LOG_LEVEL` | `INFO` | `DEBUG`, `INFO`, `WARN`, `ERROR`. |
| `ALMANACH_ENV` | `dev` | Environment name in every log line. |

Over any cap the merge call's rewrite is rejected whole and the page stays
as it was; the caps are the one lever against endless growth, and the
prompt tells the model the numbers.

## The sidecar

In `proxy` mode, machine traffic must still reach the webhook and the ping
without a browser login. The sidecar skips authentication for exactly
`/healthz`, `/auth/ping` and `/webhooks/noergler`; those routes are bearer
only and read no user header, so a client header on a skipped path has
nowhere to land. Everything under `/api/` goes through the login, the
sources' write routes included: whoever the sidecar lets in may configure
the clones, and the forwarded user is on the log line of every write.

## Commands

| Command | What |
| --- | --- |
| `almanach serve` | Run the service. Refuses a schema that is behind. |
| `almanach migrate` | Apply pending migrations and exit. |
| `almanach version` | Print the version. |
| `almanach -healthcheck` | Probe `/healthz` on loopback, exit 0 on a 2xx. |
| `almanach -readycheck` | Probe `/readyz`: the database answers. |
