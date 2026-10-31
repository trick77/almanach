# AGENTS.md

Public repo: no company names, hosts, team names, project keys anywhere.

## Commands

- Prompts compiled in, never configurable. UI: German only, `/api/...`
  only, routes mirrored in `web/embed.go`, statements as paragraphs, never
  markdown.
- Gate = `make check`; `make hooks` per clone. GitHub never first to find a
  failure. Store tests need docker.
- Commit subjects + PR titles: Go style `area: what`, gated by ship.
- Release = `gh workflow run Release`, GitHub-hosted runners only.
- ship, kit never hand-pinned.
- No web framework, ORM, logging lib beyond kit's.

## Everywhere

- Bearer token IS the team, no payload names one; webhook never reads
  `X-Forwarded-User`. Constant-time compare, every entry. Keys file
  hot-reloads on mtime; malformed = last good table kept, logged.
- `deliveries` append-only, keyed by noergler dedup id; retry = `deduped`,
  never update. Persist failure = 500, never swallowed.
- Envelope strict (`DisallowUnknownFields`, 422 names field;
  non-JSON 422, never 400); `note`, `review_totals` verbatim. One
  `webhook_processed` per request read; 401 access log only;
  `webhook_persist_failed`, `webhook_source_check_failed` both 500.
  openapi.yaml in step with api.go.
- German, Swiss spelling (`lang.SwissSS` on every prose field, never
  paths), generic masculine. No `ß` anywhere.
- Description = documentation, not one-liners: statement as long as subject
  needs, no sentence/paragraph quota (user, 2026-10-28). Ceilings in code,
  checked on stored Swiss text. Over: rewrite rejected whole, page stays,
  never truncated. Identical rewrite = no revision.
- Provenance is code's: `sources` carried by id, current PR appended on
  text change. Unknown id = invented, rejected. New id: `NextStatementID`
  over whole history, dropped id never reissued.
- Problem areas group by `bugfix_area` (shared dir, at ingest), never
  model's component name.
- One worker, serial, oldest first. Call-specific failure (off schema, 4xx,
  wrong shape): note marked, logged. Gateway-class (timeout, 5xx,
  transport, auth, rate limit, proxy page): whole worker holds, doubling
  backoff, note kept, attempt counted, given up after `maxFoldAttempts`: no
  note blocks queue forever. Failed mark pauses drain (else one paid call
  per lap). Shutdown: in-flight fold gets a grace, then cut.
- Startup model listing: unreachable/5xx warning; refused key, 4xx,
  non-listing body, unlisted alias fault.
- One `Chat` per fold, strict JSON schema, no streaming, no in-call retry
  (worker hold = only retry). Models = llmwire profile ids in config, never
  code.
- Money: nano-USD `BIGINT` in DB, 3-decimal string in JSON, never float.
  `llm_calls` (own), `note_calls` (noergler's) never summed.
- `serve` never migrates; never edit an applied migration. Probes exec,
  never httpGet.
- `ALMANACH_AUTH_MODE=proxy`: `SERVER_HOST` loopback only. Read API: user
  gates nothing, log only.
- Sources: Almanach clones, noergler never indexes. git binary (`os/exec`),
  never go-git (rejected 2026-10-30); image debian-slim + git. Clones only
  at `ALMANACH_REPOS_DIR/<project>/<slug>`; key, slug single path segments,
  `filepath.Rel`-checked before any `RemoveAll`. Clone URLs https, no
  credentials. Bare clones. Token: per-command header via `GIT_CONFIG_*`
  env, never in URL; stderr redacted; AES-GCM-sealed, never
  returned/logged. Origin URL = identity: mismatch re-clones; unresolvable
  HEAD = cut off, redone. One sequential poller; repo failure on its row.
  Removal: poller's disk lock, folder first, then row. Dir unset = feature
  off. UI copy: capital only if first char a letter ("Bereit", "7 bereit");
  "Slug", never "Schlüssel".
- Baseline: sources on = source is starting point; non-source repo's note =
  202 `ignored`, never stored (user, 2026-10-30). Runs = steps of the one
  worker, notes first, one call/step, commit pinned at start; repo's notes
  wait while its run runs. Step answers patch: unnamed ids kept, PR
  statement dropped only when its files left tree. Never `git checkout`,
  never parallel writer.
