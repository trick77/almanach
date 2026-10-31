# AGENTS.md

Public repo: no company names, hosts, team names, project keys anywhere.

## Commands

- Prompts compiled in, never configurable. UI: German only, copy only in
  `ui/src/strings.ts`, `/api/...` only, routes mirrored in `web/embed.go`.
  Wiki pages read-only; Quellen only browser write. Statements as
  paragraphs, never markdown.
- Gate = `make check` (`SHIP_CMD=<binary>` locally); `make hooks` per
  clone. GitHub never first to find a failure. Store tests need docker;
  plain `go test` skips them.
- Commit subjects + PR titles: Go style `area: what` (`store:`, `api:`,
  `consolidate:`), gated by ship.
- Release = `gh workflow run Release`, GitHub-hosted runners only.
- ship, kit never hand-pinned (`ship init`, kit `@latest`, Dependabot).
- No web framework, ORM, logging lib beyond kit's.

## Everywhere

- Bearer token IS the team, no payload names one; webhook never reads
  `X-Forwarded-User`. Constant-time compare, every entry. Keys file
  hot-reloads (mtime); malformed keeps last good, logs.
- `deliveries` append-only, keyed by noergler dedup id; retry = `deduped`,
  never update. Persist failure = 500, never swallowed.
- Envelope strict (`DisallowUnknownFields`, 422 names field; non-JSON 422,
  never 400); `note`, `review_totals` verbatim. `webhook_processed` once
  per request read, 500s excepted (own lines); 401 access log only.
  openapi.yaml in step with api.go.
- German, Swiss spelling (`lang.SwissSS` on prose, never paths), generic
  masculine. No `ß` anywhere.
- Description = documentation, not one-liners: statement as long as subject
  needs, no sentence/paragraph quota (user, 2026-10-28). Ceilings in code:
  statement counts, page runes from config; name, file caps constants;
  checked on stored Swiss text. Over: rewrite rejected whole, page stays,
  never truncated. Identical rewrite = no revision.
- Provenance is code's: `sources` carried by id, current PR appended on
  text change. Unknown id = invented, rejected. New id: `NextStatementID`
  over whole history, dropped id never reissued.
- Problem areas group by `bugfix_area` (at ingest), never model's
  component name.
- One worker, serial, oldest first. Call-specific failure: note marked,
  logged. Gateway-class: whole worker holds, doubling backoff, note kept,
  attempt counted, given up after `maxFoldAttempts`: none blocks queue.
  Failed mark pauses drain (else one paid call per lap). Shutdown: fold in
  flight gets a grace, then cut.
- Startup model listing: unreachable/5xx warning; refused key, 4xx,
  non-listing body, unlisted alias fault.
- One `Chat` per fold, strict schema, no streaming, no in-call retry
  (hold = only retry). Models = llmwire profile ids in config.
- Money: nano-USD `BIGINT`, JSON 3-decimal string, never float.
  `llm_calls` (own), `note_calls` (noergler's) never summed.
- `serve` never migrates; never edit an applied migration. Probes exec.
- `ALMANACH_AUTH_MODE=proxy`: `SERVER_HOST` loopback only. User gates
  nothing, reads or writes; log only.
- Sources: Almanach clones, noergler never indexes. git binary (`os/exec`),
  never go-git (rejected 2026-10-30). Clones only
  at `ALMANACH_REPOS_DIR/<project>/<slug>`; key, slug single path segments,
  `filepath.Rel`-checked before any `RemoveAll`. Clone URLs https, no
  credentials. Bare clones. Token: per-command header via `GIT_CONFIG_*`,
  never in URL; stderr redacted; AES-GCM-sealed, never returned/logged.
  Origin URL = identity: mismatch re-clones; unresolvable HEAD = redone.
  One sequential poller; repo failure on its row. Removal: disk lock,
  folder, then row. `ALMANACH_REPOS_DIR` unset = off. UI copy: capital only
  if first char a letter ("Bereit", "7 bereit"); "Slug", never "Schlüssel".
- Baseline: sources on = source is starting point; non-source repo's note =
  202 `ignored`, never stored (user, 2026-10-30). Runs = steps of the one
  worker, notes first, one call/step, commit pinned at start; repo's notes
  wait while it runs. Patch: unnamed ids kept, PR statement dropped only
  when its files left tree. Never `git checkout`, never parallel writer.
