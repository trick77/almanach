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
| `ALMANACH_MODEL` | The merge model as an llmwire **profile id**, never a gateway alias. Must be listed above and must support strict JSON-schema output, checked at startup off the profile; the gateway's model listing must carry the alias, checked at startup too. A gateway that does not answer at startup is a warning (folds retry); a refused key or a missing alias aborts. |

## Optional

| Variable | Default | Meaning |
| --- | --- | --- |
| `ALMANACH_REASONING_EFFORT` | the model's balanced level | A reasoning level the profile accepts; anything else is a startup fault. |
| `SERVER_HOST` | `0.0.0.0` | Listen address. |
| `SERVER_PORT` | `8080` | Listen port. |
| `ALMANACH_AUTH_MODE` | `none` | `none`: the read API names nobody. `proxy`: an OAuth sidecar in front sets `X-Forwarded-User`, logged as the reader; `serve` then refuses any `SERVER_HOST` but `127.0.0.1`, so the header can only come from the sidecar. The webhook and the ping never read the header in either mode. |
| `ALMANACH_REPO_MAX_STATEMENTS` | `40` | Statements a repository description may hold. |
| `ALMANACH_PROJECT_MAX_STATEMENTS` | `24` | Statements a project description may hold. |
| `ALMANACH_SUMMARY_MAX_CHARS` | `1500` | Runes a summary may hold. |
| `ALMANACH_STATEMENT_MAX_CHARS` | `300` | Runes a statement's text may hold. |
| `ALMANACH_PROBLEM_WINDOW_DAYS` | `90` | How far back the problem areas look by default; `?days=` overrides per request. |
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
nowhere to land. Everything under `/api/` goes through the login.

## Commands

| Command | What |
| --- | --- |
| `almanach serve` | Run the service. Refuses a schema that is behind. |
| `almanach migrate` | Apply pending migrations and exit. |
| `almanach version` | Print the version. |
| `almanach -healthcheck` | Probe `/healthz` on loopback, exit 0 on a 2xx. |
| `almanach -readycheck` | Probe `/readyz`: the database answers. |
