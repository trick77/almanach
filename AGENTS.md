# AGENTS.md

Public repo: no company/team names, hosts, project keys (docs, tests too).

## Commands

- Prompts compiled in, never configurable. UI: German, copy only in `strings.ts`, `/api/...` only;
  `routing.ts` mirrored in `backend/web/embed.go`. Wiki read-only; Quellen (`/api/sources*`) only
  browser write. Statements paragraphs, no markdown; diagrams only validated JSON, drawn by rongo's
  code.
- Gate = `make check` (`SHIP_CMD=<binary>` locally); `make hooks` per clone. GitHub never first to
  find failure.
- Commit subjects + PR titles: Go style `area: what`, ship-gated.
- Release: `gh workflow run Release`, GitHub-hosted runners only.
- ship, kit never hand-pinned: `ship init`, kit `@latest`, Dependabot.
- No web framework, ORM, logging lib beyond kit's.

## Everywhere

- Bearer token IS the team, no payload names one; webhook: bearer only. Constant-time compare, each
  entry. Keys file hot-reloads (mtime); malformed keeps last good, logs.
- `deliveries` append-only, keyed by dedup id; retry = `deduped`, never update. Persist failure =
  500, never swallowed.
- Envelope strict (`DisallowUnknownFields`, 422 names field; non-JSON 422, not 400); `note`,
  `review_totals` verbatim. `webhook_processed` once per request read; each 500 its own line; 401
  access log only. openapi.yaml = api.go.
- Swiss German (`lang.SwissSS` on every prose field, never paths), generic masculine, no Eszett in
  prose, docs.
- Description = documentation, no one-liners: statement as long as subject needs, no
  sentence/paragraph quota (user, 2026-10-28). Ceilings: statement counts, page runes from config;
  name, file caps constants; checked on stored Swiss text. Statement over a field cap: left out,
  an edit kept as stored, logged (user, 2026-10-31). Page over a ceiling: rewrite rejected whole,
  page stays, never truncated; project step keeps the new statements that fit. Identical rewrite =
  no revision. Map: self/repeated edges dropped before its check.
- Provenance is code's: `sources` carried by id, current PR appended on text change. Unknown id on
  an edit = invented, rejected (page without statements: edits taken as new); on a step's
  drop/confirmed skipped, logged. New statements in
  id-less `added`; id from `NextStatementID` over whole history, never reissued.
- Problem areas: `bugfix_area` (shared dir, at ingest), never model's component. Pitfall: bugfix
  evidence only. `.md` export: bearer opens only it.
- One worker, serial, oldest first. Call-specific failure: note marked+logged. `gatewayClass`:
  worker holds, doubling backoff, note kept, attempts ≤ `maxFoldAttempts`: none blocks queue
  forever. Failed mark pauses drain (else one paid call per lap). Shutdown: in-flight fold gets
  grace, then cut.
- Startup listing: unreachable = warning, refused key = fault.
- One `Chat` per fold, strict schema, no streaming, no in-call retry. Models = llmwire profile ids
  in config, not code.
- Money: nano-USD INTEGER, JSON 3-decimal string, no float. `llm_calls` (own), `note_calls`
  (noergler's) never summed.
- SQLite: one file, own volume, WAL, writes BEGIN IMMEDIATE; no shared FS. `serve` never migrates;
  never edit applied migration. Probes exec, not httpGet.
- oidc gates `/api` but login+callback; proxy gone (user, 2026-10-31). Reader's name: log only,
  gates nothing.
- Sources: Almanach clones, noergler never indexes. git binary (`os/exec`; image has git), never
  go-git (rejected 2026-10-30). Clones only at `ALMANACH_REPOS_DIR/<project>/<slug>`; key, slug
  single path segments, `filepath.Rel`-checked before `RemoveAll`. Clone URLs https, no creds. Bare
  clones. Token: per-command `GIT_CONFIG_*` header, never in URL; stderr redacted; AES-GCM-sealed,
  not returned/logged. Origin URL = identity: mismatch re-clones; unresolvable HEAD = cut off,
  redone. One sequential poller; repo failure on its row. Removal: poller's disk lock, folder, then
  row. `ALMANACH_REPOS_DIR` unset = off. UI copy: capital only if first char letter ("Bereit",
  "7 bereit"); "Slug", never "Schlüssel".
- Baseline: sources on: source = starting point; non-source repo's note = 202 `ignored`, never
  stored (user, 2026-10-30). Runs = worker steps, notes first, one call/step, commit pinned at
  start; repo's notes wait out its run. Step answers a patch: unnamed ids kept, PR statement dropped
  only when all its files left tree; edit keeps rest. No `git checkout`, no parallel writer.
