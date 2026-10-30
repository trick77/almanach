# AGENTS.md

Almanach is the living wiki noergler feeds: one note per merged PR in,
per-repository and per-project descriptions out, rewritten by a model
call revision by revision. Companion to noergler, like riptide. Public
repo: no company names, hosts, team names or project keys anywhere, docs
and tests included.

## Commands

- Module in `backend/` (go commands there); `ui/` (npm commands there),
  docs, `compose.yaml`, `make` at the root. Prompts in
  `backend/internal/prompts/`, compiled in, never configurable. `make fe-build` writes `backend/web/dist`, which `go
  build` embeds; the UI is German only (`ui/src/strings.ts`), read-only,
  reads `/api/...` and nothing else; its routes are mirrored in
  `backend/web/embed.go`, statements render as paragraphs, never markdown.
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
- Ingest is strict on the envelope (`DisallowUnknownFields`, a 422 names
  the field; a body that is not JSON is a 422 too, never a 400); the
  `note` object and `review_totals` are kept verbatim. One
  `webhook_processed` line per request read; a 401 is on the access log
  only, a persist failure is `webhook_persist_failed`. The contract is
  `backend/api/openapi.yaml`, kept in step with `api.go`.
- German always, Swiss spelling (`lang.SwissSS` on every prose field, never
  on paths), generic masculine. No `ß` anywhere, docs included.
- A description is documentation, never a list of one-liners: a statement
  is as long as its subject needs, no sentence or paragraph quota anywhere
  (user, 2026-10-28). Ceilings are code: statement counts and whole-page
  runes from config, name and file caps as constants, checked on the
  stored (Swiss-spelled) text. Over a ceiling the rewrite is rejected
  whole and the page stays; never truncated. An identical rewrite is no
  revision.
- Provenance is code's, never the model's: a statement's `sources` are
  carried forward by id and the current PR appended when its text changed.
  An id the page does not have is the model inventing one: rejected. A new
  id comes from `NextStatementID` over the page's whole history: a dropped
  id is never reissued.
- Problem areas group by the shared directory of the bugfix's component
  files (`bugfix_area`, computed at ingest), never by the model's component
  name.
- One consolidation worker, serial, oldest first. A failure specific to
  the call (model off schema, 4xx, wrong shape) marks the note with a log
  line. A gateway-class failure (timeout, 5xx, transport, auth, rate
  limit, proxy page: `gatewayClass`) holds the whole worker with doubling
  backoff, keeps the note with its attempt counted, gives up after
  `maxFoldAttempts`. A note that fails forever must not block the queue.
  A mark that fails pauses the drain (else one paid call per lap).
  Shutdown waits for the fold in flight up to a grace, then cuts it.
- Startup lists the gateway's models: unreachable or 5xx = warning;
  refused key, 4xx, non-listing body or unlisted alias = fault.
- One `Chat` per fold, strict JSON schema, never streaming; no retry
  inside a call, the worker's hold is the only retry. Models
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
- Sources (`/api/sources`, UI "Quellen"): Almanach clones, noergler never
  indexes. Git binary via `os/exec` (`internal/gitrepo`, after ../rongo),
  never go-git (rejected 2026-10-30); image is debian-slim with git. Clones
  only at `ALMANACH_REPOS_DIR/<project>/<slug>`; key and slug single path
  segments, `filepath.Rel`-checked before any `RemoveAll`. Clone URLs https,
  no credentials. Clones are bare (runs read the object store). Token
  to git per command as a header via `GIT_CONFIG_*` env (Bearer or Basic),
  never in a URL; stderr redacted; sealed AES-GCM under
  `ALMANACH_SECRET_KEY`, never returned or logged. Origin URL is identity:
  mismatch re-clones; a clone whose HEAD resolves to nothing is cut off and
  redone. One sequential poller; a repo's failure goes on its row;
  removals take the poller's disk lock. Unset
  `ALMANACH_REPOS_DIR` = feature off (list `enabled: false`, rest 404).
  Removal: folder first, then row. UI copy: capital only when the first
  char is a letter ("Bereit", "7 bereit"); "Slug", never "Schlüssel".
- Baseline (HOW_IT_WORKS 4b): sources on = the source is the starting
  point; a note for a non-source repo is 202 `ignored`, never stored (user,
  2026-10-30). Runs are steps of the one worker, notes first, one call per
  step, commit pinned at start; a repo's notes wait while its run runs. A
  step answers a patch: unnamed ids kept, a PR statement dropped only when
  its files left the tree. Never `git checkout`, never a parallel writer.
