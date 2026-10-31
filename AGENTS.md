# AGENTS.md

Public repo: no company names, hosts, team names, project keys anywhere,
docs and tests included.

## Commands

- Prompts compiled in, never configurable. UI: German, copy only in
  `strings.ts`, `/api/...` only; `routing.ts` mirrored in
  `backend/web/embed.go`.
  Wiki read-only; Quellen (`/api/sources*`) only browser write. Statements
  as paragraphs, never markdown.
- Gate = `make check` (`SHIP_CMD=<binary>` locally); `make hooks` per
  clone. GitHub never first to find failure. Store tests need docker;
  plain `go test` skips them.
- Commit subjects + PR titles: Go style `area: what`, gated by ship.
- Release: `gh workflow run Release`, GitHub-hosted runners only.
- ship, kit never hand-pinned: `ship init`, kit `@latest`, Dependabot.
- No web framework, ORM, logging lib beyond kit's.

## Everywhere

- Bearer token IS the team, no payload names one; webhook never reads
  `X-Forwarded-User`. Constant-time compare, every entry. Keys file
  hot-reloads (mtime); malformed keeps last good, logs.
- `deliveries` append-only, keyed by dedup id; retry = `deduped`,
  never update. Persist failure = 500, never swallowed.
- Envelope strict (`DisallowUnknownFields`, 422 names field; non-JSON 422,
  never 400); `note`, `review_totals` verbatim. `webhook_processed` once
  per request read; each 500 its own line; 401 access log only.
  openapi.yaml in step with api.go.
- German, Swiss spelling (`lang.SwissSS` on every prose field, never
  paths), generic masculine. No Eszett in prose or docs.
- Description = documentation, not one-liners: statement as long as subject
  needs, no sentence/paragraph quota (user, 2026-10-28). Ceilings:
  statement counts, page runes from config; name, file caps constants;
  checked on stored Swiss text. Over: rewrite rejected whole, page stays,
  never truncated. Identical rewrite = no revision.
- Provenance is code's: `sources` carried by id, current PR appended on
  text change. Unknown id = invented, rejected. New id from
  `NextStatementID` over whole history: never reissued.
- Problem areas: `bugfix_area` (shared dir, at ingest), never model's
  component.
- One worker, serial, oldest first. Call-specific failure: note marked,
  logged. Gateway-class (`gatewayClass`): worker holds, doubling backoff,
  note kept, attempts counted up to `maxFoldAttempts`: none blocks queue
  forever. Failed mark pauses drain (else one paid call per lap).
  Shutdown: in-flight fold gets grace, then cut.
- Startup listing: unreachable = warning, refused key = fault.
- One `Chat` per fold, strict schema, no streaming, no in-call retry.
  Models = llmwire profile ids in config, never code.
- Money: nano-USD `BIGINT` in DB, JSON 3-decimal string, never float.
  `llm_calls` (own), `note_calls` (noergler's) never summed.
- `serve` never migrates; never edit an applied migration. Probes exec,
  never httpGet.
- Reader's name gates nothing (reads, writes), log only.
- Sources: Almanach clones, noergler never indexes. git binary (`os/exec`;
  image has git), never go-git (rejected 2026-10-30). Clones only at
  `ALMANACH_REPOS_DIR/<project>/<slug>`; key, slug single path segments,
  `filepath.Rel`-checked before any `RemoveAll`. Clone URLs https, no
  credentials. Bare clones. Token: per-command header via `GIT_CONFIG_*`,
  never in URL; stderr redacted; AES-GCM-sealed, never returned/logged.
  Origin URL = identity: mismatch re-clones; unresolvable HEAD = cut off,
  redone. One sequential poller; repo failure on its row. Removal:
  poller's disk lock, folder, then row. `ALMANACH_REPOS_DIR` unset = off.
  UI copy: capital only if first char a letter ("Bereit", "7 bereit");
  "Slug", never "Schlüssel".
- Baseline: sources on: source = starting point; non-source repo's note =
  202 `ignored`, never stored (user, 2026-10-30). Runs = steps of the one
  worker, notes first, one call/step, commit pinned at start; repo's notes
  wait while its run runs. Step answers a patch: unnamed ids kept, PR
  statement dropped only when all its files left tree; edit keeps the
  rest. Never `git checkout`, never parallel writer.
