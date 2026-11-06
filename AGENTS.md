# AGENTS.md

Public repo: no company/team names, hosts, project keys (docs, tests too).

## Commands

- Prompts compiled in, never configurable; a rule meant for stored pages bumps its scope's epoch.
  UI: German, copy only in `strings.ts`, `/api/...` only;
  `routing.ts` mirrored in `backend/web/embed.go`. Wiki read-only; Quellen (`/api/sources*`) only
  browser write. Text, summary: schema blocks, never newlines in a JSON string (models drop
  them), `lang.Laid` subset, UI `blocks` mirrors; notes plain. Diagrams only validated JSON, by
  rongo's code; maps by `mapLayout.ts`, banded, grow down (ELK holds no rows in zones).
- Gate = `make check`. GitHub never first to find failure.
- Commit subjects + PR titles: Go style `area: what`, ship-gated.
- Fresh clone: `ship hooks` before the first push (check, conform and init install it too); without the pre-push hook a push skips the gate.
- Release: `gh workflow run Release`, GitHub-hosted runners only.
- ship, kit never hand-pinned: `ship init`, kit `@latest`, Dependabot.
- No web framework, ORM, logging lib beyond kit's.

## Everywhere

- Bearer token IS the team, no payload names one; webhook: bearer only. Constant-time compare,
  each entry.
- `deliveries` append-only, keyed by dedup id; retry = `deduped`, never update. Persist failure =
  500, never swallowed.
- Envelope strict; `note`, `review_totals` verbatim. Each 500 its own line; 401 access log only.
  openapi.yaml = api.go.
- Swiss German (`lang.Prose` on model prose, never paths), generic masculine, no Eszett.
- Credentials never leave, by regex, not prompt: `redact.File` in `ReadFiles`, note text per store
  read (rows verbatim), `lang.Prose`/`Laid`. Shapes need a value's mark, prose stays. New shape → test.
- Description = documentation, no one-liners: statement as long as subject needs, no
  sentence/paragraph quota (user, 2026-10-28). Nothing dropped for size, no page/name/file cap
  (user, 2026-11-01); bad path: off file list.
- Provenance is code's: `sources` carried by id, current PR/run appended on text change; departure
  run neither credits nor adds (removal teaches nothing; user, 2026-11-02), restyle never credits. Bad item
  (unknown/repeated id, bad kind, empty): left out of patch, logged, stored kept (user, 2026-11-01).
  New statements in id-less `added`; ids never reissued.
- Problem areas: `bugfix_area` (shared dir, at ingest), never model's component. Pitfall: bugfix
  evidence only. `.md` export: bearer opens only it.
- One worker, serial, oldest first. Merge = patch. Repo revision written = note done (refold dups).
  Code drop: files gone at head, never merge tree. Call failure: note failed, never consolidated.
  `gatewayClass`: hold, note kept. Refused write: hold, note kept, retry counts once before its
  call (a pause repaid the call each poll). All ≤ `maxFoldAttempts`: none blocks queue forever.
- One `ChatStream` per call (gateway cuts non-streamed at 300 s; user, 2026-11-02), strict
  schema, no in-call retry.
- Money: nano-USD INTEGER, no float. `llm_calls` (own), `note_calls` (noergler's) never summed.
  Daily budget holds runs, never notes.
- SQLite: `serve` never migrates; never edit applied migration (0001 squashed once, DB dropped,
  2026-11-01). Probes exec.
- oidc gates `/api` but login+callback; proxy gone (user, 2026-10-31). Reader's name: log only,
  gates nothing.
- Project, repo = id; names only in `projects`, `repos`, `*_names`; never join on one. Name once
  used, any case, never to another; own may change case (user, 2026-11-01). Carry-over:
  `TRANSITIONAL(<topic>): Remove when <cond>.`, tests too.
- Sources: Almanach clones, noergler never indexes. git binary (`os/exec`), never go-git (rejected
  2026-10-30). Clone paths `filepath.Rel`-checked before `RemoveAll`; removal: lock, folder, row. Token never in URL,
  stderr, response or log; sealed at rest; one origin only (static folder = one host).
  Origin URL = identity: mismatch re-clones; unresolvable HEAD = cut off, redone. One sequential
  poller; repo failure on its row. Run due = read from rows each cycle, never from an event.
  UI copy: capital only if first char letter ("Bereit", "7 bereit"); "Slug", never "Schlüssel" (Bitbucket's "Projektschlüssel" only). Bitbucket
  source key: from project URL, case-only rename.
- Listing folds where files are, never 2 fixed levels (Maven = one line). Big file = head, never left
  out (god class is one); redacted whole, then cut.
- Baseline: sources on: source = starting point; non-source repo's note = 202 `ignored`, never
  stored (user, 2026-10-30). Gate: key, slug any case (user, 2026-11-03). Runs = worker steps, notes first, one call/step, commit pinned at
  start; repo's notes wait out its run. Step answers a patch: unnamed ids kept; repo page: PR statement
  dropped only when all its files left tree; edit keeps rest. No `git checkout`, no parallel writer.
  1 source repo = project of one: no project call/page. Project = system (page: wiring, no
  `component`) or collection (summary alone); model judges, no switch (user, 2026-11-04); its
  drops the model's word, PR-taught too.
- Deps code's: edges joined at read, never stored; ambiguous = no edge. Shared repo derived, no
  kind, not "library"; map zone UI-drawn. Maps, prompts, uses: main scope only (test, dev, pins
  dashed on Abhängigkeiten alone); parent POM a pin.
- Both maps runtime+prod only, no build/CI/CD (user, 2026-11-02, twice): model's tags, `RuntimeOnly`.
