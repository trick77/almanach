# How Almanach works

Almanach receives one note per merged pull request from noergler and turns
the notes into two descriptions: one per repository, one per Bitbucket
project. This document starts with the shape of the system, then follows
a note from the webhook to the page, and ends with the storage model and
the decisions behind it.

## Architecture overview

Almanach is one Go process and one PostgreSQL database. The wiki feed calls
nothing but the LLM gateway; the sources feature (section 4a) reaches a
Bitbucket project's repository listing and git remotes with the operator's
token, and nothing else.

```
  noergler ──POST /webhooks/noergler (bearer)──▶ ┌──────────────────────┐
  noergler ──GET  /auth/ping          (bearer)──▶ │  HTTP (net/http)     │
                                                  │  api: webhook, ping, │
  browser  ──GET  /api/... (via oauth sidecar)──▶ │  read routes,        │
  browser  ──GET  /, /projekte/... (sidecar)   ──▶ │  embedded UI (web)   │
                                                  └──────────┬───────────┘
                                                             │ Ingest, reads
                                                             ▼
                                                  ┌──────────────────────┐
                                                  │  store (pgx)         │
                                                  │  deliveries, notes,  │
                                                  │  revisions, pages... │
                                                  └──────────┬───────────┘
                                                        ▲    │ NextQueued
                                        InsertRevision  │    ▼
                                                  ┌──────────────────────┐      ┌─────────────┐
                                                  │  consolidate worker  │─Chat─▶│ LLM gateway │
                                                  │  one goroutine,      │◀──────│ (LiteLLM)   │
                                                  │  serial, oldest first│      └─────────────┘
                                                  └──────────────────────┘
```

Three parts, three responsibilities:

- **The HTTP surface** (`internal/api`, `internal/httpapi`, `web`) takes
  notes in and serves the wiki out. The webhook is the only write path;
  the read routes and the embedded UI are the only things a person
  touches. The routes are thin: decode, call the store, encode. The UI is
  a static bundle (`ui/`, built into `backend/web/dist`) served from the
  binary; it reads `/api/...` and nothing else, and knows no team, no
  user and no secret.
- **The store** (`internal/store`) owns every query and every transaction.
  It is the one place SQL lives; the other packages see Go types. An
  accepted delivery and everything derived from it land in one
  transaction; a revision and its page pointer land in one transaction.
- **The consolidation worker** (`internal/consolidate`) is the only caller
  of the model. It takes notes off the queue one at a time, renders the
  merge prompt, checks the answer against rules the model cannot bend and
  writes a revision. It runs in the same process, started after the
  startup checks and stopped after the HTTP server.

The two surfaces have different trust models. Machine traffic (noergler)
authenticates with a bearer token that *is* the team: the token names the
team, nothing in a body does, and the webhook never reads a user header.
Human traffic reaches the read routes only through an OAuth sidecar in
production (`ALMANACH_AUTH_MODE=proxy`), which sets a user header that the
access log records and nothing else acts on; the wiki is read-only and
serves the same to everyone who can log in.

What flows between the parts is small and typed: `ingest.Parsed` from the
webhook into the store, `store.QueuedNote` and `store.Page` into the
worker, `inference.MergeResult` back, `store.NewRevision` into the store.
Money is nano-USD integers in the store and 3-decimal strings on the
wire, never a float on either side.

## 1. Where the notes come from

noergler reviews a pull request in rounds, one per push. After every
successful round it continues the same model conversation (the system
message, the review prompt, the model's review answer) with one more turn:
what this pull request teaches about this repository, as a strict JSON
object in German. Components, business rules, invariants, data entities; the
change kind (`bugfix`, `feature`, `refactor`, `chore`); for a bugfix the
symptom, the cause and its class. noergler validates it with blunt rules
(Swiss spelling, no secrets, a ceiling on the whole note) and keeps the latest round's
note per pull request. At `pr:merged` it stamps the note final and sends it
here with everything it knows beside it: the project key, the repository
slug, the pull request, the ticket, the commits, the counts of issues and
suggestions it posted, the team's project claims, and every figure the
gateway reported for the note call. That is the whole feed. The feed never
calls Bitbucket, Jira or noergler; the clones of section 4a are a separate
path with their own credentials.

Why at review time and not at merge: the review already paid for the files
and the diff in the model's context. The extra turn rides on that prompt,
and when the gateway's prefix cache holds, it costs a fraction of a fresh
call. noergler records the cache hits per call; the delivery carries them,
so the premise is measured, not assumed.

## 2. The webhook

`POST /webhooks/noergler` with a bearer token. The token is looked up in the
team-keys file in constant time across every entry; the team it names is
the note's team. Nothing in the body names a team, and the handler never
reads the sidecar's user header: machine traffic is bearer-only.

The body is decoded strictly: a body that is not JSON, an unknown field, a
missing required one, a malformed timestamp or a short commit hash is a 422
naming what is wrong. The
contract is ours on both sides, so an invalid body is a bug and must not be
half-stored.

Everything lands in one transaction:

- `deliveries`: the raw body, keyed by noergler's dedup id
  `pr_merged_note#<pr_key>#<source_commit>`. Append-only: a second delivery
  with the same id answers `deduped` and changes nothing.
- `teams`, `projects`, `repos`, `claims`: the Bitbucket structure as noergler
  knows it from its configuration, upserted from every delivery.
- `notes`: the note and its facts. `bugfix_area` is computed here, the
  longest directory every component file of the note shares.
- `note_calls`: every lane the gateway reported for the round's note call,
  plus the pull request's round totals, as columns.

A persist failure is a 500. noergler keeps the note pending and sends it
again on the team's next merge.

The handler logs one `webhook_processed` line for every request it read
with the outcome (`accepted`, `deduped`, `invalid`) and the delivery id,
then pokes the worker. The two exceptions log their own line instead: a
request without a known bearer is a 401 on the access log only, and a
delivery the database refused is a `webhook_persist_failed` error line
with the 500.

## 3. The consolidation worker

One worker, in process, serial, oldest note first. It wakes on a webhook
and polls every thirty seconds, so a note queued before a restart is folded
without a second delivery. Serial on purpose: two folds of the same project
would each rewrite the project description from a base the other is
replacing.

A fold reads the current repository and project descriptions, renders the
merge prompt (`backend/internal/prompts/almanach_merge.txt`, compiled in)
and makes one `Chat` call with a strict JSON schema. The model returns, for
each description, either `null` (no change) or a rewritten summary and
statements. A statement it keeps or edits carries its `id`; a new one has
`id: null`.

The answer is then held to rules the model cannot bend:

- Spelling: the sharp s becomes `ss` on every prose field, never on file paths.
- Provenance: a kept id takes its sources from the current page and gains
  this pull request only when its text or name changed; a new statement
  starts with this pull request, numbered past every id the page's history
  ever used, so an id dropped in one fold never names another fact later.
  An id the page does not have, or one used twice, rejects the rewrite.
- Ceilings: statement count per scope and runes per whole description
  (the summary and every statement's name and text), plus fixed caps on a
  statement's name and its file list. No statement has a length cap of its
  own: a description is documentation, each statement as long as its
  subject needs, and only the whole must not grow without bound. Over a
  ceiling the rewrite is rejected whole and the page stays as it was. Never
  truncated: a truncated description lies.
- An identical rewrite is no revision.

What passes is written as the next revision of the page and the page
repointed to it, in one transaction. History is kept; a rollback is a
repoint. Whatever the model answered, the note is then marked consolidated,
so a note that fails forever cannot block the queue; the log says why. The
same for a failure specific to the call: a 4xx (the note too large for the
context, a rejected format) or an answer of the wrong shape.

A failure of the gateway as a whole is different: a timeout, a 5xx or
transport failure, a refused key, a rate limit, a proxy page where the
answer should be. The next fold would meet the same gateway, so the worker
holds: no fold for thirty seconds, doubling on each further failure up to
ten minutes, or for as long as a rate limit asked. The note stays queued
with its attempt counted and is tried again after the hold; a call that
gets through clears the hold. After eight such failures of one note it is
given up, with a log line, so one note the gateway always fails cannot
hold the queue forever. A timed-out call may have been billed at the
gateway, so a gateway consistently slower than the call cap costs up to
eight merges per note before giving up; the ledger shows them. If the mark
itself fails (the database refusing writes), the drain pauses until the
next wake or poll rather than paying for the same fold again. A shutdown
lets the fold in flight finish, up to a grace of the call timeout plus
fifteen seconds for its writes, before it is cut off.

Every merge call leaves an `llm_calls` row: Almanach's own ledger, never
mixed with the figures noergler reports in `note_calls`.

## 4. The read side

`/api/...` serves the wiki as JSON, structs with stable field order:

- `/api/projects`: every project a note has arrived for, by key.
- `/api/projects/{key}`: the project description, the repositories with
  the first sentence of their description, the problem areas, the recent
  notes.
- `/api/projects/{key}/repos/{slug}`: the repository description, its
  revisions, its notes.
- `/api/projects/{key}/repos/{slug}/notes/{pr}`: one note with its facts
  and the recorded note call (tokens, cache hits, cost, rounds).
- `/api/projects/{key}/problems?days=90`: the problem areas of the window.
- `/api/sources` and below: the clones, read and written (section 4a).

Problem areas are bugfix notes grouped by repository and `bugfix_area`. The
grouping is by directory, never by the model's component name: three
spellings of one component would otherwise be three areas. Each area
reports the bugfix count, the most common cause class, the summed issue and
suggestion counts from the reviews, and the last fix.

The read API gates nothing. In proxy mode an OAuth sidecar in front decides
who reaches it and sets the user header, which only the access log reads;
`serve` then refuses to listen anywhere but loopback so the header can come
from nowhere else. Nothing is written through the read API; the wiki is
read-only by design.

## 4a. The sources

The descriptions above are built from deltas: what each merged pull request
taught. What they lack is the baseline, a description of the repository as
it stands, and the baseline needs the code itself. The sources feature is
the first half of that: Almanach clones every repository of a project into
a base folder and keeps the clones current. Reading them and writing the
baseline description with the model is the second half, a later step; the
clones already record the head commit each checkout holds, so that step can
diff since the last commit it described instead of re-reading everything.

A **source** is a project. Two kinds: a **Bitbucket project**, whose
repositories are listed from the Bitbucket Server REST API
(`/rest/api/1.0/projects/{key}/repos`, paged, archived ones skipped) on
every cycle; and a **static folder**, a project key the operator chooses
with repositories entered by hand as clone URLs, for example the
repositories of one GitHub organisation. Both land as
`<ALMANACH_REPOS_DIR>/<project_key>/<slug>`; nothing else is ever written
there. In compose that folder is the `repos` volume, in a deployment a
volume claim.

Git is the binary, after ../rongo's `gitrepo`, never a Go library: proven on
any repository size, and every rule here was paid for there.

- A clone is bare and full history: an object store, no working tree, so
  nothing on disk can go stale between the recorded head and the files,
  and phase 2 reads `git show <head>:<path>` as ../rongo's reader does. The
  default branch is asked of the remote (`ls-remote --symref`), never
  assumed; a fetch uses explicit refspecs with `--prune`; the head is the
  remote-tracking ref, and a branch the remote no longer has is a loud
  failure on the row, never a silent stop.
- The token reaches git for one command only, always as a header through
  `GIT_CONFIG_*` in the environment: `Authorization: Bearer` for `bearer`,
  `Authorization: Basic` of `user:token` for `basic`. Never in a URL: git
  persists a URL in the clone's config, prints it in its errors and shows
  it in the process list. The username defaults to `x-access-token`, which
  GitHub ignores and Bitbucket Data Center checks (project and repository
  tokens as `x-token-auth`, personal ones as the user's own name). git's
  stderr is still redacted of any URL credentials before it reaches a log
  or a row, the belt to that brace. A cancelled command is given ten
  seconds for its helpers before its pipes are cut, so a shutdown cannot
  hang on `git-remote-https`.
- The origin URL is the identity, the directory name a label: a clone
  whose origin differs from the row's clone URL is removed and cloned
  again, so a corrected URL never serves the old code under the new name.
  A clone is complete only when its `HEAD` resolves to a commit; git
  writes `HEAD` before any transfer, so a clone cut off by a shutdown has
  one and no refs, fails that check, and is removed and cloned again.
- A removal from the API goes through the poller's disk lock: never while
  a sync touches a clone, and a row removed mid-cycle is skipped by the
  cycle that listed it, so a deleted folder is not cloned back as an
  orphan.
- Keys and slugs are validated as single path segments (Bitbucket's
  charset, never `.` or `..`) and the resulting path checked with
  `filepath.Rel` against the base, both on the way in and again before any
  `RemoveAll`: the cost of being wrong is deleting outside the base.

One poller, in process, sequential: a stampede of parallel clones against
one forge is how a token gets rate-limited. The first cycle runs thirty
seconds after start (announced in the log, so "not yet" reads apart from
"broken"), then every `ALMANACH_SYNC_INTERVAL`, jittered, and at once on a
nudge from the API. A cycle lists every Bitbucket source (a new slug is a
pending row, a vanished one `missing` with its clone kept, a failed listing
leaves the rows alone and goes on the source), then syncs every repository
that is not missing. One repository's failure is recorded on its row with
the redacted error and the cycle continues; only the store not answering
stops a cycle. Cycle start and finish log their counts; an unchanged
repository is a debug line. A shutdown cuts the clone in flight; the next
cycle finds the half directory and redoes it.

The token at rest is sealed with AES-256-GCM under `ALMANACH_SECRET_KEY`,
a fresh nonce per seal, stdlib only. Without the key a public source still
works and saving a token is a 422 naming the variable; a stored token the
key cannot open (the key changed) fails that source's repositories with a
line saying so. The token is never returned by any route (`has_token` is
all a body carries), never logged, never in `.git/config`.

The routes under `/api/sources` are the first writes beside the webhook.
They are gated exactly like the read routes: by the sidecar's login in
proxy mode, by nobody in none; the forwarded user is logged on every write
and decides nothing. A write needs `Content-Type: application/json`, which
a cross-site form cannot send without a preflight. With
`ALMANACH_REPOS_DIR` unset the feature is off: no poller, the list route
answers `enabled: false`, every other route 404, and the UI hides the
entry. The UI section "Quellen" is the one read-write part of the SPA: the
sources list with a create form, a source page with its repositories, their
states, inline forms for the token and for adding a repository to a static
source, and inline confirms for removals that name the folder that goes.
It polls every three seconds while a clone runs and otherwise not at all.

## 5. Operations

- `almanach migrate` applies the embedded migrations under an advisory lock
  and exits: the init container's job. `almanach serve` refuses a schema
  that is behind, naming the pending files.
- `/healthz` is liveness, `/readyz` asks the database. Both answer loopback
  callers only, so probes are `exec` probes against the binary's
  `-healthcheck` and `-readycheck`.
- Startup runs every check (database, team keys, model profile, the
  gateway's model listing) before aborting, so the log names all the
  failures at once. A gateway that does not answer is a warning only: notes
  are still taken and folds retry; a gateway that refuses the key or does
  not list the alias is a fault.
- Logs are structured, one line per event. No secret, no DSN, no token ever
  appears in them; the startup config line prints addresses, model and caps.
- Shutdown order: the HTTP server drains first (new deliveries get a
  connection refused and noergler keeps them pending), then the worker is
  asked to stop and finishes the fold in flight, up to a grace of the call
  timeout plus fifteen seconds; only then does the pool close. Size the
  pod's termination grace above that sum plus the HTTP drain.

## 6. Package layout

Everything Go lives under `backend/`, one Go module; the UI is `ui/`, a
Vite and React bundle that `make fe-build` (and the image's node stage)
writes into `backend/web/dist` before `go build` embeds it. The dependency
direction
is strictly downward: `cmd` wires, and a package below depends on another
only for its types; where a test needs a fake, the consumer declares the
interface it needs and the test satisfies it. llmwire is reached in two
places: `inference` holds the client and the call, `consolidate` reads
llmwire's error classes to tell a gateway failure from a call-specific
one.

| Package | Role | Depends on |
| --- | --- | --- |
| `cmd/almanach` | The binary: `serve`, `migrate`, `version`, the two probes. Loads config, runs the startup checks, wires store, worker and routes, handles signals. | everything below |
| `internal/api` | The routes: `/auth/ping`, `/webhooks/noergler`, `/api/...`, and `/` handed to `web`. Request decoding, response structs, the one `webhook_processed` log line. Declares the `Store` and `Waker` interfaces it needs. | config, httpapi, ingest, store, teamkeys, web |
| `web` | The embedded UI: `go:embed` of `backend/web/dist`, kit's SPA handler with the client route allowlist (`/`, `/faq`, `/projekte/{key}`, `.../repos/{slug}`, `.../notizen/{pr}`); anything else is a 404, an unbuilt bundle a 503 with a placeholder. The allowlist mirrors `ui/src/routing.ts`. | kit/spa |
| `internal/httpapi` | The mux with kit's probes, access log and panic recovery. net/http only. | kit/httpx |
| `internal/ingest` | `Parse`: the strict reading of one noergler payload into store rows, and `commonDir` for the bugfix area. Pure, no I/O. | store (types only) |
| `internal/teamkeys` | The bearer table: file load, mtime hot reload, constant-time lookup. | nothing |
| `internal/store` | PostgreSQL: pool, embedded migrations, every query. `ingest.go` writes a delivery, `wiki.go` serves the worker, `read.go` serves the read routes. | pgx, kit/migrate |
| `internal/consolidate` | The worker: loop, hold, fold, the rules (`attach`, `check`), prompt rendering. Declares the `Store` and `Merger` interfaces it needs. | config, inference, lang, prompts, store, llmwire (error classes) |
| `internal/inference` | The llmwire client: startup checks, one `Merge` call with the strict schema, answer parsing, cost extraction. | llmwire |
| `internal/prompts` | `almanach_merge.txt`, compiled in with `go:embed`. Never configurable. | nothing |
| `internal/gitrepo` | The git binary: clone, default branch, fetch, head, origin check, removal with the path guard, credential redaction. | nothing |
| `internal/bitbucket` | The Bitbucket Server repository listing, paged, net/http only. | gitrepo (Access), store (types only) |
| `internal/clone` | The sync poller: cycles, nudge, listing, per-repository sync with the failure recorded on the row. Declares the `Store`, `Git` and `Lister` interfaces it needs. | gitrepo, secret, store |
| `internal/secret` | AES-GCM for the sources' tokens, key from the environment. | nothing |
| `internal/lang` | `SwissSS`: the sharp s to `ss`. | nothing |
| `internal/config` | The environment, read once through kit's reader, every fault collected. | kit/env |

The interfaces are declared where they are used (`api.Store`,
`consolidate.Store`, `consolidate.Merger`) and satisfied by `*store.Store`
and `*inference.Client`. That is what lets the api and consolidate tests
run against in-memory fakes while the store has its own tests against a
real PostgreSQL that ship starts for `make check`.

## 7. Storage model

Everything is in PostgreSQL, schema `backend/internal/store/migrations/`.
The database records what happened; the descriptions are derived from it
and rewritten by the merge call, revision by revision, never edited by
hand. Four groups of tables:

**What arrived.** `deliveries` keeps every webhook body whole, keyed by
noergler's dedup id `pr_merged_note#<pr_key>#<source_commit>`. Append-only:
a second delivery with the same id is a no-op, the first one stands. It is
the audit trail and the raw material; everything below can be rebuilt
from it.

**The structure.** `teams`, `projects`, `repos` and `claims` are the
Bitbucket structure as noergler knows it from its configuration, upserted
from every delivery. A team is created the first time its token delivers;
a project and a repo the first time a note names them; a claim (team owns
project, or team owns one repo of a project) from the `claims` array. This
is how the project page knows which repositories belong to it without
the feed ever calling Bitbucket.

**The sources.** `sources` and `source_repos` are the clone feature's,
apart from the feed's tables on purpose: a source has no team, a token, and
a folder on disk. `sources` is one row per project key with its kind
(`bitbucket` or `static`), the Bitbucket host, how a token travels
(`bearer` or `basic` with `token_user`), the token sealed with AES-GCM
(`token_enc`, NULL for a public source), and the last listing's time and
error. `source_repos` is one row per clone: the clone URL, who put it here
(`listed` by the API or `manual`), the branch (NULL until resolved from
the remote), the head commit the checkout holds, the poller's `state`
(`pending`, `syncing`, `ready`, `failed`, `missing`) and the last sync's
time and error. Phase 2 joins the two groups on `(project_key, slug)`.

**The notes.** `notes` is one row per merged pull request, replaced when
the same PR is delivered again (a reopen and re-merge) and then re-queued.
Beside the note object itself (`note`, JSONB, verbatim) sit the facts
noergler attached: ticket, change kind, commits, run id, the posted issue
and suggestion counts, and two columns computed at ingest for the problem
areas, `bugfix_area` (the longest directory every component file of a
bugfix note shares) and `bugfix_component` (the first component's name).
`consolidated_at` and `fold_attempts` are the worker's queue state: NULL
means queued, the partial index `notes_queue_idx` makes the oldest queued
note one cheap lookup. `note_calls` holds, per delivery, every lane the
gateway reported for the round's note call plus the PR's round totals, as
columns: the cache-hit rate and the cost per note are one query.

**The wiki.** `revisions` is every version of every description:
`scope` (repository or project), the keys, a per-page `revision` number,
the `summary` prose, the `statements` array as JSONB (each with its id,
kind, name, text, files and the sources code attached), the serialized
size, the PR whose note produced it and the merge call behind it. The
check `(scope = 'project') = (repo_slug IS NULL)` keeps the two scopes
honest. `pages` is the current pointer per page: one row, one
`current_revision_id`. Repointing it is a rollback; history is never
rewritten. `llm_calls` is Almanach's own ledger, one row per merge call,
never summed with `note_calls`.

What is derived and what is kept: `deliveries` and `notes` are kept as
they arrived. `bugfix_area`, the statements' `sources` and ids, the
revision numbers and the page pointers are code's. The `summary` and the
statements' text are the model's, as long as the subject needs under the
page's ceiling. The problem areas are not
stored at all: they are a query over bugfix notes within a window, grouped
by repository and `bugfix_area`.

## 8. Design decisions

Each of these was a choice with an alternative; the reasons are recorded
so the choice is not undone as a cleanup.

- **Notes are written at review time, not at merge.** The review already
  paid for the files and the diff in the model's context; the note is one
  more turn on that prompt. A merge-time call would read everything again.
  The cost of this is that noergler keeps a note per round and replaces
  it; Almanach only ever sees the last one.
- **The bearer token is the team.** noergler has a team slug, but a slug in
  a body is a claim anyone with the URL could make. The token is issued by
  the Almanach operator per team and compared in constant time; a team
  exists for Almanach the moment its token is in the keys file. Nothing
  else identifies a sender.
- **Deliveries are append-only and deduped on noergler's key.** noergler
  marks a note sent only after a 2xx and resends a pending one on the
  team's next merge, so a duplicate is the normal case after any blip. The
  dedup makes a resend harmless; a lost note would be the real cost.
- **One worker, serial.** Two folds of the same project would rewrite the
  project description from two different bases and one would overwrite
  the other's facts. The volume is one note per merged PR; a queue of
  minutes is fine, a wrong description is not. The serial worker is what
  keeps a fold's base current; the advisory lock in `InsertRevision` only
  keeps revision numbers and the page pointer consistent, it does not
  make concurrent folds safe. Parallelising the worker would need the
  base revision checked at write time.
- **Provenance is code's, never the model's.** The sources on a statement
  (which PRs it was learned from) are what make a wiki trustworthy, and a
  model asked to maintain them would invent PR numbers. So the model
  returns statements with their ids and code carries the sources forward,
  appending the current PR when the text changed. An id the page never
  had is rejected as invented; a dropped id is never reissued.
- **Caps are enforced in code, and a rewrite over a cap is rejected whole.**
  The prompt tells the model the numbers, but prose grows unless something
  refuses it. Truncating would produce a description that lies about the
  system; rejecting keeps the previous revision, and the next PR that
  gets it right corrects it.
- **Problem areas group by directory, not by name.** Three spellings of one
  component would be three areas. The shared directory of the bugfix's
  component files is computed once at ingest and does not depend on the
  model's wording.
- **A gateway failure holds the worker; a call-specific failure marks the
  note.** A dead gateway would otherwise consume every queued note within
  minutes. The hold doubles up to ten minutes and the note keeps its
  attempt count; after eight failures of one note it is given up so one
  bad note cannot hold the queue forever. A 4xx or an off-schema answer is
  about this note and is not retried: it would fail the same way.
- **German, Swiss spelling, generic masculine.** The readers are German
  speaking; the spelling is enforced in code (`lang.SwissSS`) on every
  prose field because the model drifts to the sharp s. File paths are never
  touched.
- **The UI is in the binary, and reads the wiki, writes only the sources.**
  It ships with the process so a deployment is one image and one sidecar,
  and it talks to `/api/...` alone (`backend/api/openapi.yaml`); nothing in
  it knows a team or a bearer, and nothing in the backend knows the UI
  beyond the route allowlist. It is German only, with no settings and no
  login of its own: the sidecar authenticates, the UI shows. The wiki
  pages are read-only; the Quellen section configures the clones, and is
  the only place a browser writes. Statement text is rendered as
  paragraphs split on blank lines, never as markdown, so a model that
  emits markup cannot change the page's shape.
- **Almanach clones, noergler reviews.** noergler is the pull request
  agent; it never indexes a repository, that would make no sense for a
  review tool. The code lives with Almanach, which is the one that will
  describe it (user, 2026-10-30). The premise "Almanach never calls
  Bitbucket" therefore holds for the feed only.
- **The git binary, not a Go library.** go-git was considered and
  rejected: memory and speed on large repositories, partial submodule and
  LFS support, a working-tree half with a bug history; the binary is
  proven on any size and every rule of ../rongo's `gitrepo` transfers. The
  price is a runtime image with a userland (debian-slim with git) instead
  of distroless.
- **The token is typed in the UI and sealed in the database**, not an
  environment variable name as in ../rongo's `repos.yaml`. A new token is a
  form, not a redeploy; `ALMANACH_SECRET_KEY` is what makes that safe, and
  without it no token is stored at all.
- **Origin is identity.** A checkout is named after its row, and a row's
  clone URL can be corrected; comparing `remote.origin.url` on every cycle
  is what keeps the old code from answering under the new name.

## 9. Rules and invariants

The behaviour that is not obvious from a route or a table, collected in
one place. Each is enforced in code and most have a test; they are the
things a change must not undo by accident.

### Identity and delivery

- **The bearer is the team.** The token is compared in constant time
  against every entry of the keys file, found or not, so timing reveals
  neither which team matched nor how many there are. Whitespace around a
  token is ignored. The file is re-read when its modification time changes
  and only then; a file that does not parse, has an empty team or token,
  two teams with one token, or no entries at all is refused and the last
  good table stays, with the error on the next request's log line.
- **The delivery id is noergler's**: `pr_merged_note#<pr_key, lowercased>#<source commit, lowercased>`.
  A second delivery with that id answers `deduped`, writes nothing and
  does not wake the worker. A delivery with the same PR but another
  source commit (a reopen and re-merge) is a new delivery: it replaces
  the note row, re-queues it and resets its fold attempts; the revisions
  the old note produced stay.
- **The envelope is strict, the note is verbatim.** An unknown key at the
  top level or inside `note_call`, `rounds` or a claim, a missing required
  field (`project_key`, `repo_slug`, `pr_key`, `pr_title`,
  `source_commit_sha`, `merged_at`), `pr_id` below 1, a source commit
  shorter than seven characters, a `merged_at` that is not RFC 3339, a
  `note` that is null or not an object, a claim without a project key, or
  a body that is not JSON at all: 422 naming what is wrong, in a fixed
  order, never half-stored. The `note` object and `review_totals` are
  kept as they came: a key noergler adds inside them is stored, not
  refused, which is how the note can grow without a release here. A body
  over one megabyte is 413.
- **One `webhook_processed` line for every request that was read**, with
  the outcome (`accepted`, `deduped`, `invalid`), the team and the
  delivery id when one could be built. A request without a known bearer
  is a 401 on the access log only; a delivery the database refused logs
  `webhook_persist_failed` instead, with the 500, so the sender keeps the
  note pending. Count the three lines together for a per-request total.
- **Timestamps are stored in UTC**; `merged_at` is parsed in whatever
  offset noergler sent and normalised.

### What ingest derives from a note

- `change_kind` comes from the payload; when the payload has none the
  note object's own `change_kind` is taken.
- The bugfix block is lifted only when the kind is `bugfix` *and* the
  block is present and not null. A bugfix note without a block has no
  `bugfix_area` and no cause: the problem areas still count it, under the
  root area with cause `other`, because it is a bugfix of that repository
  all the same.
- `bugfix_area` is the longest directory every file of every component
  shares: one file gives its directory, files that share nothing but the
  root give `""` (which still groups, as the area "root"), no files give
  `""`. `bugfix_component` is the first component's name, as the display
  name only.
- The `note_call` object is optional; without one an empty call row is
  still written so the PR's round totals (`rounds`) are kept. A reported
  lane the gateway did not send is NULL, never zero.
- `review_totals` is not lifted into columns; it stays in the delivery's
  raw payload.

### The queue and the worker

- The queue is `consolidated_at IS NULL`, oldest `received_at` first, then
  the lowest PR id. One note is folded at a time; there is never a second
  worker.
- The worker wakes on an accepted delivery and polls every thirty seconds;
  a wake that arrives while one is pending is the same wake.
- Both current pages are read *before* the model call, and the repository
  rewrite is applied before the project one. The project description is
  folded from the note, not from the repository description.
- A failure is classed, and the class decides everything:
  - the model's answer off the schema, an answer cut by the model's output
    limit (`too_long`: the description asked for does not fit one call), a
    4xx from the gateway (context overflow, rejected format), a 2xx of the
    wrong shape: specific to this call, the note is marked consolidated
    with a log line and never retried;
  - a timeout, a 5xx, a transport failure, a refused key, a rate limit, a
    proxy page where the answer should be: the gateway's, the worker holds
    (thirty seconds, doubling to ten minutes, or the rate limit's
    Retry-After when longer), the note keeps its place with its attempt
    counted, and is tried again after the hold. Any answer from the
    gateway, parsed or not, clears the hold. After eight gateway failures
    of one note it is given up and marked.
- A mark or a defer the store refuses pauses the drain until the next wake
  or poll rather than paying for the same fold again.
- Every model call leaves an `llm_calls` row, whatever its outcome, with
  the tokens and the cost the gateway reported (cost NULL when it reported
  none).
- Shutdown lets the fold in flight finish for up to the call timeout plus
  fifteen seconds, then cancels it. A fold cancelled this way leaves its
  note queued (nothing was marked) and is folded again after the restart.

### Rules of a rewrite

A rewrite the model returns passes all of these or is rejected whole, with
the reason in the log and the page unchanged:

- A statement's `kind` is one of `component`, `rule`, `invariant`,
  `entity`.
- `name` and `text` are trimmed and Swiss-spelled before anything is
  checked; an empty one rejects the rewrite.
- A kept `id` must exist on the current page and may appear once. A
  statement with no id is new and gets the next id past every id any
  revision of this page ever used: `s1`, `s2`, ... with no gaps reused.
- Sources: a kept statement whose name and text are unchanged keeps its
  sources exactly; one whose name or text changed gains the current PR
  once (never twice for the same PR); a new statement starts with the
  current PR. The model never writes a source.
- Ceilings, in this order after the spelling: the summary is not empty;
  the statement count is within the scope's cap
  (`ALMANACH_REPO_MAX_STATEMENTS`, `ALMANACH_PROJECT_MAX_STATEMENTS`); the
  prose of the whole description (summary plus every name and text) is
  within the scope's cap (`ALMANACH_REPO_MAX_CHARS`,
  `ALMANACH_PROJECT_MAX_CHARS`); each name within 80 runes; at most 5
  files per statement, each within 200 runes and without whitespace. A
  statement's text has no cap of its own. Empty file strings are dropped
  before counting.
- A rewrite identical to the current page (same summary, same statements
  in the same order with the same ids, kinds, names, texts and files)
  writes no revision.
- `null` for a description means "no change" and writes nothing; a
  rewrite of one scope can land while the other is rejected.
- The revision's `chars` is the summary's runes plus the serialized
  statements' runes, after the spelling.

### The read side

- The description of a page is `null` until its first revision exists.
- Projects list alphabetically by key; a project's repositories list
  alphabetically by slug; notes list newest merge first; revisions list
  newest first. The project page shows the twenty most recent notes of the
  project, a repository page its hundred most recent notes and fifty
  revisions.
- A repository's summary on the project page is its description's first
  sentence: cut at the first `.`, `!` or `?` followed by a space or the
  end, except after an abbreviation (`z.B.`, `bzw.`, `usw.`, ...) or a
  dotted name (`config.yaml`, `2.0`) unless a capital follows.
- A note's `items` is the count of its components, rules, invariants and
  entities; a list that is null or not an array counts as empty and never
  breaks a read.
- Problem areas: bugfix notes of the project whose `merged_at` is within
  the window (`?days=`, 1 to 3650, default `ALMANACH_PROBLEM_WINDOW_DAYS`),
  grouped by repository and `bugfix_area`. Per area: the count, the most
  frequent `cause_kind` (ties broken alphabetically; a missing kind counts
  as `other`), the component name and PR of the most recent fix, and the
  summed issue and suggestion counts noergler posted on those PRs. Ordered
  by count, then by the most recent fix.
- A note's detail shows the most recent `note_calls` row for the PR (the
  last delivery's). A delivery without a `note_call` object still has a
  row (empty outcome and status, the round totals filled), so on the wire
  `note_call` is an object with empty strings, not `null`; `null` only
  appears for a row that predates the delivery's call, which the current
  ingest never produces.
- Money: nano-USD integers in the store; on the wire a 3-decimal USD
  string rounded half up, or omitted when the gateway did not price the
  call. Never a float, never summed across `note_calls` and `llm_calls`.
- `files` and every array are `[]` on the wire, never `null`.
- In proxy mode the user header is written to the access log of every read
  route and nowhere else; it gates nothing, and the webhook and the ping
  never read it.

### The sources

- A key or slug is one path segment of Bitbucket's charset, never `.` or
  `..`; a clone URL is https without credentials; a Bitbucket source has a
  host (https, no query, no credentials), a static one has none.
  Checked on the way in (422 naming the field) and again before a removal.
- `token_auth` defaults to `bearer` for Bitbucket and `basic` for static;
  `token_user` goes with `basic` only. An omitted token on PATCH keeps the
  stored one, an empty one clears it. A token without `ALMANACH_SECRET_KEY`
  is a 422; a stored token the key cannot open fails the source's
  repositories with a line naming the key.
- The token is a header per command, never a URL; the clone's config never
  holds a credential. git's stderr is redacted of URL credentials before a
  log line or a row. The base folder is made absolute at startup: git
  resolves a clone's destination against the command's directory.
- A removal takes the folder first, then the row: a folder left behind by
  a failed row delete is re-cloned by the next cycle, harmless; a row gone
  with the folder still there would be an orphan.
- A listed repository the API no longer names is `missing`: not synced, its
  clone kept, removable by hand; named again, it is pending once more. A
  hand-entered row is never touched by a listing.
- `RequestSync` moves every row but a missing one to pending and nudges the
  poller; the cycle in flight finishes first, the next one serves it.
- The feature off (`ALMANACH_REPOS_DIR` unset): no poller, `GET /api/sources`
  answers `enabled: false`, every other sources route 404, the UI hides the
  entry. On: git on PATH, the folder present and writable (a probe file),
  and the key, if set, 32 bytes of base64, all checked at startup.

### Startup and operations

- Startup runs every check before aborting: the database answers and the
  schema is current (the pending migration files are named), the keys file
  loads with at least one team, the model profile supports strict JSON
  schema output and the configured reasoning level, and the gateway lists
  the model's alias. The listing is bounded to ten seconds. A gateway that
  is unreachable or answers 5xx is a warning; a refused key, a 4xx, a body
  that is not a listing, or an alias missing from it is a fault.
- `serve` never migrates; `migrate` needs only the DSN. A schema behind the
  binary refuses to start.
- `/healthz` and `/readyz` answer a loopback caller without forwarding
  headers only; through a router or the sidecar they are 404 by design.
- In proxy mode the listener must be loopback (`127.0.0.1` or
  `localhost`); any other `SERVER_HOST` is a startup fault.
- Caps and the problem window must be positive integers; a zero or
  negative value is a startup fault, not a disabled cap.
