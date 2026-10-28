# AGENTS.md

Almanach is the living wiki noergler feeds: one note per merged PR in,
per-repository and per-project descriptions out, rewritten by a model
call revision by revision. Companion to noergler, like riptide. Public
repo: no company names, hosts, team names or project keys anywhere, docs
and tests included.

## Commands

- Module in `backend/` (go commands there); docs, `compose.yaml`, `make` at
  the root. The prompt is `backend/internal/prompts/almanach_merge.txt`,
  compiled in, never configurable.
- Gate = `make check` (ship master; `SHIP_CMD=<binary>` locally). `make
  hooks` per clone. GitHub is never first to find a failure.
- Store tests need docker (ship starts Postgres); plain `go test` skips them.
- Commit subjects + PR titles: Go style `area: what` (`store: …`, `api: …`,
  `consolidate: …`), gated by ship.
- Release = `gh workflow run Release`, GitHub-hosted runners only.
- ship and kit are never pinned by hand: `ship init` scaffolds, kit at
  `@latest`, Dependabot moves it.
- No web framework, no ORM, no logging lib beyond kit's. net/http, pgx.

## Everywhere

- The bearer token IS the team. Nothing in a payload names a team; the
  webhook never reads `X-Forwarded-User`. Tokens are compared in constant
  time across every entry. The keys file hot-reloads on mtime; a malformed
  file keeps the last good table and logs.
- `deliveries` is append-only, keyed by noergler's dedup id; a retry is
  `deduped`, never an update. A persist failure is a 500, never swallowed:
  noergler keeps the note pending and sends it again.
- Ingest is strict: `DisallowUnknownFields`, a 422 names the field; a body
  that is not JSON is a 422 too, never a 400. One `webhook_processed` line
  per request. The contract is `backend/api/openapi.yaml`, kept in step
  with `api.go`.
- German always, Swiss spelling (`lang.SwissSS` on every prose field, never
  on paths), generic masculine. No `ß` anywhere, docs included.
- Caps are code: statement counts and rune lengths from config, name and
  file caps as constants. Checked on the stored (Swiss-spelled) text. Over
  any cap the rewrite is rejected whole and the page stays; never
  truncated. An identical rewrite is no revision.
- Provenance is code's, never the model's: a statement's `sources` are
  carried forward by id and the current PR appended when its text changed.
  An id the page does not have is the model inventing one: rejected. A new
  id comes from `NextStatementID` over the page's whole history: a dropped
  id is never reissued.
- Problem areas group by the shared directory of the bugfix's component
  files (`bugfix_area`, computed at ingest), never by the model's component
  name.
- One consolidation worker, serial, oldest first. A fold that fails takes
  its note off the queue with a log line; a note that fails forever must not
  block the queue. A mark that fails pauses the drain (else one paid call
  per lap). Shutdown waits for the fold in flight up to a grace, then cuts
  it.
- One `Chat` per fold, strict JSON schema, no retry, never streaming. Models
  are llmwire profile ids in config, never in code.
- Money is nano-USD `BIGINT` in the DB and a 3-decimal string in JSON, never
  a float. Almanach's own calls are `llm_calls`, noergler's reported ones
  `note_calls`; never summed together.
- `serve` never migrates; never edit an applied migration. Probes are exec,
  never httpGet.
- `ALMANACH_AUTH_MODE=proxy` refuses any `SERVER_HOST` but loopback: the
  sidecar is the only caller, so the forwarded user header can come from
  nowhere else. The read API gates nothing on the user; the name is for the
  access log.
