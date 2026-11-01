# AGENTS.md

Public repo: no company/team names, hosts, project keys (docs, tests too).

## Commands

- Prompts compiled in, never configurable. UI: German, copy only in `strings.ts`, `/api/...` only;
  `routing.ts` mirrored in `backend/web/embed.go`. Wiki read-only; Quellen (`/api/sources*`) only
  browser write. Text, summary: `lang.Laid` subset (user, 2026-11-01), UI `blocks` mirrors, rest
  literal; notes plain. Diagrams only validated JSON, drawn by rongo's code.
- Gate = `make check` (`SHIP_CMD=<binary>` locally). GitHub never first to find failure.
- Commit subjects + PR titles: Go style `area: what`, ship-gated.
- Fresh clone: `ship hooks` before the first push (check, conform and init install it too); without the pre-push hook a push skips the gate.
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
- Swiss German (`lang.Prose` on model prose, never paths), generic masculine, no Eszett.
- Credentials never leave, by regex, not prompt: `redact.File` in `ReadFiles`, note text per store
  read (rows verbatim), `lang.Prose`/`Laid`. Shapes need a value's mark, prose stays. New shape → test.
- Description = documentation, no one-liners: statement as long as subject needs, no
  sentence/paragraph quota (user, 2026-10-28). Nothing dropped for size, no page/name/file cap
  (user, 2026-11-01); bad path: off file list. Same rewrite = no revision. Flow:
  self/repeated edges dropped first.
- Provenance is code's: `sources` carried by id, current PR appended on text change. Bad item
  (unknown/repeated id, bad kind, empty): merge (whole page) rejected; step's patch never, item
  left out, logged, stored kept (user, 2026-11-01). Page without statements: edits taken as new.
  New statements in id-less `added`; id from `NextStatementID` over whole history, never reissued.
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
- Project, repo = id; names only in `projects`, `repos`, `*_names`; never join on one. Name once
  used, any case, never to another; own may change case (user, 2026-11-01). Carry-over:
  `TRANSITIONAL(<topic>): Remove when <cond>.`, tests too.
- Sources: Almanach clones, noergler never indexes. git binary (`os/exec`), never go-git (rejected
  2026-10-30). Clones only at `BACKEND_REPOS_DIR/p<id>/r<id>`; segments `filepath.Rel`-checked
  before `RemoveAll`. Clone URLs https, no creds; bare. Token: per-command `GIT_CONFIG_*` header,
  never in URL; stderr redacted; AES-GCM-sealed, not returned/logged; one origin only (static
  folder = one host, header bound to it).
  Origin URL = identity: mismatch re-clones; unresolvable HEAD = cut off, redone. One sequential
  poller; repo failure on its row. Removal: poller's disk lock, folder, then row.
  `BACKEND_REPOS_DIR` unset = off. UI copy: capital only if first char letter ("Bereit",
  "7 bereit"); "Slug", never "Schlüssel".
- Baseline: sources on: source = starting point; non-source repo's note = 202 `ignored`, never
  stored (user, 2026-10-30). Runs = worker steps, notes first, one call/step, commit pinned at
  start; repo's notes wait out its run. Step answers a patch: unnamed ids kept, PR statement dropped
  only when all its files left tree; edit keeps rest. No `git checkout`, no parallel writer.
  Left part/no map at head: ≤3 auto retries, rule `LeftParts`.
