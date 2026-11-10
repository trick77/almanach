# AGENTS.md

Public repo: no company/team names, hosts, project keys (docs, tests too).

## Commands

- Gate = `make check`. GitHub never first to find failure.
- Commit subjects + PR titles: Go style `area: what`, ship-gated.
- Fresh clone: `ship hooks` before the first push (check, conform and init install it too); without the pre-push hook a push skips the gate.
- ship, kit never hand-pinned (`ship init`, kit `@latest`, Dependabot). No web framework, ORM, logging lib
  beyond kit's.

## UI

- German, IT words English (user: "No one ever says Repositorium"); copy only in `strings.ts`; capital only if
  first char letter ("Bereit", "7 bereit"); "Slug" (Bitbucket's "Projektschlüssel" only).
- `/api/...` only. Wiki read-only; browser writes only Quellen (`/api/sources*`) and the queue pause
  (operations; user, 2026-11-07).
- Maps by `mapLayout.ts`, banded, grow down (ELK holds no rows in zones).

## Everywhere

- Prompts compiled in, never configurable; a rule meant for stored pages bumps its scope's epoch. Text,
  summary: schema blocks, never newlines in a JSON string (models drop them); notes plain.
- Bearer token IS the team, no payload names one. Project = first team whose note named it, set once.
  `claims` never trusted.
- Persist failure = 500, never swallowed; each 500 its own line, 401 access log only.
- Money: integer nano-USD, never a float.
- IT words English by code (`lang.Terms`), never a prompt list (user). But Abhängigkeit, UI and pages
  (user, 2026-11-10).
- Credentials never leave, by regex, not prompt (`redact`): a shape needs a value's mark, so prose stays.
  New shape → test.
- Description = documentation: a statement as long as its subject needs (user, 2026-10-28).
  No learned fact dropped for size, no page/name cap (user, 2026-11-01); unread file still named by
  skeleton (router narrowed "no file cap", user, 2026-11-08); bad path: off file list.
- Provenance is code's: `sources` by id, current PR/run appended on text change; departure run neither
  credits nor adds (removal teaches nothing; user, 2026-11-02); restyle never credits. Bad item: left out,
  logged, stored kept (user, 2026-11-01).
- Problem areas: `bugfix_area`, never model's component. Pitfall: bugfix evidence only.
- One worker, serial. Repo's notes = one call a day (docs a day behind ok; user, 2026-11-06); credit =
  model's `from_prs`, held to the call's PRs. Code drop = files gone at head, never merge tree. Call
  failure: note failed, never consolidated. Gateway hold, refused write: note kept, retry counted once
  before its call (a pause repaid the call each poll).
- One `ChatStream` per call (gateway cuts non-streamed at 300 s; user, 2026-11-02), strict schema, no in-call
  retry. Every call capped; cap hit while streaming = `too_long`, the call's, never a gateway hold
  (2026-11-07: runaway restyles, 10 min, ~$0.9 each, 8×, unpriced).
- `llm_calls` (own), `note_calls` (noergler's) never summed. Daily budget holds runs, never notes.
- Never edit an applied migration (0001 squashed once, DB dropped, 2026-11-01).
- Schema: read `docs/schema.md` first (regen: its header). `TestSchemaDoc` sees no table-bound type's
  fields, checks none against a column.
- Proxy auth gone (user, 2026-10-31).
- Project, repo = id; names only in `projects`, `repos`, `*_names`, never joined on. A name once used, any
  case, never another's; own may change case (user, 2026-11-01). Carry-over: `TRANSITIONAL(<topic>): Remove
  when <cond>.`, tests too.

## Sources and runs

- Almanach clones, noergler never indexes. git binary, never go-git (rejected 2026-10-30). Removal: lock,
  folder, row. Token never in URL, stderr, response or log; one origin only.
- One forge write: the brief, only what code can't tell, size-capped (user: "reasonable in size"); refused
  push = text to copy (user, 2026-11-06).
- A run is due by the rows, read each cycle, never by an event.
- What a run reads is code's where code can tell, never the catalogue's alone (it read tests, not routing;
  user, 2026-11-07): tests out unless most of the tree; migrations out, model from code (user, 2026-11-08);
  anchors always; WSDL/XSD only build-named; generated and own `.agents/PROJECT.md` never. Listing opens where
  files are (Maven was one line).
- Sources on: a non-source repo's note = 202 `ignored`, never stored (user, 2026-10-30); gate key/slug any
  case (user, 2026-11-03). No `git checkout`, no parallel writer. Project = system (wiring, no `component`)
  or collection (summary alone, PR-taught too); model judges, no switch (user, 2026-11-04).
- Deps code's. Shared repo derived, not "library". Maps, prompts, uses: main scope only (others dashed on
  Dependencies alone); parent POM a pin. Topics likewise (user, 2026-11-07): code-read, a name code can't
  resolve left out, never guessed. Interfaces too (user, 2026-11-09): a spec/WSDL offers only on code's
  evidence, else declares (often a client's copy); contracts read whole, generated mark ignored (spec
  exports are big and marked).
- Both maps runtime+prod only, no build/CI/CD (user, 2026-11-02, twice).
