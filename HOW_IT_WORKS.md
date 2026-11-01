# How Almanach works

Almanach receives one note per merged pull request from noergler and turns
the notes into two descriptions: one per repository, one per Bitbucket
project. This document starts with the shape of the system, then follows
a note from the webhook to the page, and ends with the storage model and
the decisions behind it.

## Architecture overview

Almanach is one Go process and one SQLite database file on its own
volume. The wiki feed calls
nothing but the LLM gateway; the sources feature (section 4a) reaches a
Bitbucket project's repository listing and git remotes with the operator's
token, and nothing else.

```
  noergler ──POST /webhooks/noergler (bearer)──▶ ┌──────────────────────┐
  noergler ──GET  /auth/ping          (bearer)──▶ │  HTTP (net/http)     │
                                                  │  api: webhook, ping, │
  browser  ──GET  /api/...    (session cookie)──▶ │  read routes,        │
  agent    ──GET  /api/...md  (bearer)        ──▶ │  Markdown export,    │
  browser  ──GET  /, /projekte/... (open)     ──▶ │  embedded UI (web)   │
                                                  └──────────┬───────────┘
                                                             │ Ingest, reads
                                                             ▼
                                                  ┌──────────────────────┐
                                                  │  store (SQLite)      │
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
  writes a revision. With the sources on it also steps the baseline runs
  (4b), reading the clones through the poller's lock. It runs in the same
  process, started after the startup checks and stopped after the HTTP
  server.

The two surfaces have different trust models. Machine traffic (noergler)
authenticates with a bearer token that *is* the team: the token names the
team, nothing in a body does, and the webhook knows no session: bearer
only. Human traffic in production signs in over OIDC
(`BACKEND_AUTH_MODE=oidc`): every `/api` route but the login's own two
needs Almanach's signed session cookie, whose reader the access log
records and nothing else acts on; the wiki is read-only and serves the
same to everyone who can sign in. A coding agent pulls a page's Markdown
export with a team's bearer, which opens those two GET routes and no
other.

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
the note's team. Nothing in the body names a team, and the route sits
outside the sign-in: machine traffic is bearer-only.

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
statements in two lists: `statements`, every statement it keeps or edits,
each with its `id` (the schema requires the string), and `added`, the new
ones, which have no id field at all. A model asked to write `null` for a
new statement's id numbered it anyway (`s1` on an empty page, `s36` on one
up to `s35`) and lost the rewrite to the invented-id rule; with no field to
write, it cannot. The code numbers a new statement. On a page without
statements the model still put new ones in `statements` with ids (an
eval's project step, `s2`); there is nothing to edit there, so on every
path such edits are taken as new statements, logged, before any rule.

The answer is then held to rules the model cannot bend:

- Spelling: the sharp s becomes `ss` on every prose field, never on file paths.
- Provenance: a kept id takes its sources from the current page and gains
  this pull request only when its text or name changed; a new statement
  starts with this pull request, numbered past every id the page's history
  ever used, so an id dropped in one fold never names another fact later.
  An id the page does not have, or one used twice, rejects the rewrite:
  the answer is the whole page, so leaving such an edit out would delete
  the statement it meant to edit (a baseline step's patch keeps every id
  it does not name, and leaves only that edit out).
- No ceiling: a description is system documentation, each statement as
  long as its subject needs, the page as long as its statements. No count,
  no size, no cap on a name or the file count; nothing is dropped or
  truncated for its size. A path over 200 runes or with whitespace is no
  path of a tree: it leaves the statement's file list, logged, the
  statement kept.
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

With the sources on, the same worker drains the baseline runs (4b): notes
first, and when no note is due one step of the oldest open run, then the
notes again. A note of a repository whose run is running waits for it
(the store skips it) and is folded after it; a run still queued holds
nothing, so a long queue of runs never holds the notes for hours. The run
pins the clone's last synced head, so a note merged after that fetch may
have been folded before the run started: a statement such a pull request
taught is marked read-only in the run's prompts and a patch touching it is
refused, since the code the run reads cannot show it. Git decides first: a
merge commit the run's commit holds is in it, whatever the clocks say (a
rebase merge's head can be older than the PR's merge time); one it does
not hold counts as newer unless merged more than a day before the commit,
so a committer clock a day off does not fool it and a merge commit that
never reaches the branch (a release branch, a rewritten history) soon
stops counting. A PR source from before the merge commit was kept goes by
its merge time with two minutes of slack. A git read that fails holds the
step like a busy clone, before its files are read or a call made, for up to
two minutes from that commit's first failure in the run; past that a
statement whose merge commit git cannot place is read-only for the step
(a patch's edit, drop or confirmation of it is struck, the rest of the
patch taken, the log says why, and the part is marked to be read again)
and asked again in the next. A statement another PR already marks newer holds nothing. Git's
answers are kept for the run. Runs among
themselves go oldest first, one to its end before the next begins.

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
- `/api/projects/{key}.md` and `/api/projects/{key}/repos/{slug}.md`: a
  page as Markdown, for a coding agent (below).
- `/api/sources` and below: the clones, read and written (section 4a).
- `/api/queue`: the worker's queue, the page Warteschlange. The note or
  run step in flight, then the rest in the order the worker takes it (the
  notes it may fold, the started run's next step, the notes waiting for
  that run, the queued runs). Why the next item waits sits on that item:
  a hold with its cause (the model not answering, rate limited, the key
  refused, the database busy) and its end, or a run step waiting for its
  clone. A run lists its steps with their time and cost, from the plan
  and the calls (`llm_calls.step`). Below, what finished in the last 24
  hours and what the worker's calls cost. The worker's state is in
  memory; the queue itself is the store's, so a restart shows the same
  queue. A hold's end wakes the worker, not the next poll.

### The page model

A page is written for two readers in a different order: first a coding
agent about to change the repository (what must not break, where to make
a change, why things are as they are, what broke before, how to check its
work), then a developer in his first weeks (purpose, structure, flows,
words). The repository summary keeps a fixed order: purpose and boundary,
structure, key flows, and last and short, working on it (the gate and test
commands, the conventions, what the repository's agent notes insist on).
An agent jumps to the end, a newcomer reads from the top. Statements are
of eight kinds:

- `component`, `entity`: the parts and the things the code works with.
- `interface`: a public interface a component offers others (an HTTP API,
  events, a library's exported API, a CLI), never the component itself:
  who offers it, what it holds, its contract file where there is one, who
  uses it and what counts as a breaking change. An important artifact of
  its own, so it has its own section, after the invariants: it is what
  others rely on not breaking.
- `rule`, `invariant`: what the code enforces and what must always hold.
- `decision`: a choice the code embodies, its reason and what was given
  up. Only with a stated reason (a PR's note, the docs, a code comment that
  says why), never guessed from code: it keeps an agent from "fixing" what
  is deliberate.
- `extension`: where and how a kind of change is made (a route, a step, a
  setting): which files, in which order, where to register. A change
  recipe naming every file, each in the tree. A repository's alone: on the
  project page an extension rejects the rewrite like an unknown kind, and
  the project schemas do not offer it.
- `pitfall`: something that broke and how not to break it again, only from
  evidence. On the merge path a note of kind `bugfix` is that evidence:
  the prompt routes its symptom and cause to a pitfall, new or deepening
  one on the same files. On a baseline run a subject step is told the
  problem areas its files sit in (section 4b) and writes a pitfall only
  where one backs it. Code holds the prompts to it: a new pitfall from a
  note that is no bugfix, or from a step shown no problem area, is left
  out and logged by name, the rest of the answer kept.

Left out: technical debt (code does not show the debt that matters, and
TODO comments would paint the few written down as all there is: facts
about the system, never about its quality), tutorials, changelogs. A
pitfall carries its sources like any statement: the PR on the merge path,
the run's commit on a baseline run, whose prompt shows the problem area's
PR keys as evidence only.

### The Markdown export

An agent reads files, not JSON. `GET /api/projects/{key}.md` and
`GET /api/projects/{key}/repos/{slug}.md` answer the current revision as
`text/markdown; charset=utf-8`: the title and revision, the summary, the
project's system map as a fenced `json` block, then the statements
grouped by kind in the order invariants, interfaces, rules, decisions,
pitfalls, extensions, components, entities (a kind without statements has no
heading), each with its files, its PR keys and its diagram as a fenced
`json` block. Markdown is the frame; a statement's text and the summary
go in in their stored layout (paragraphs, `- ` and `N. ` items, bold,
code spans, a statement's `####` sub-headings under its `###` name),
each line's indent dropped and any other line that would read as
structure (a heading, quote, fence, list, rule, underline, HTML) escaped
with a backslash, and a fence or code span
longer than any backtick run inside it. An unknown project or repository
is a 404, as on the JSON routes; a page without a revision yet is its
title and a line saying so. The mux takes no wildcard inside a segment, so
the export arrives at the JSON routes' handlers and is told apart by its
`.md` suffix: a key or slug that itself ends in `.md` and is listed keeps
its JSON route, its export is `<name>.md.md`.

An agent outside a browser has no session. In oidc mode a team's bearer
token, the one the webhook takes, checked the same way (constant time,
any team), opens these two GET (and HEAD) routes read-only and nothing
else, and is always served the export, never a JSON page: every
other route under `/api`, the JSON of the same page included, still needs
the session, a wrong bearer gets the session's 401, and the team goes to
the access log. The gate matches the export by the escaped path, a key
and a slug as plain segments, so no spelling the mux reads otherwise is
opened. In none mode the exports are open like the rest.

Problem areas are bugfix notes grouped by repository and `bugfix_area`. The
grouping is by directory, never by the model's component name: three
spellings of one component would otherwise be three areas. Each area
reports the bugfix count, the most common cause class, the summed issue and
suggestion counts from the reviews, and the last fix.

The read API gates nothing on who reads. In oidc mode the sign-in decides
who reaches it at all, and the identity provider's policy decides who may
sign in; the reader (preferred username, else email, else subject) goes
to the access log and nowhere else. Nothing is written through the read API; the wiki is
read-only by design.

### Signing in

`BACKEND_AUTH_MODE=oidc` is kit's authorization code flow
(`ship/kit/auth`), without a users table. The UI asks
`GET /api/me` before it renders anything; a 401 sends the browser by full
navigation to `GET /api/auth/login?next=<the page>`, which redirects to the
provider with state, nonce and a PKCE verifier in short-lived
`almanach_oidc_*` cookies, the page in `almanach_next` (a path on this site
only, else `/`). The provider returns to `GET /api/auth/callback`; kit
checks state, PKCE, the ID token and its nonce, the claims become the
signed session cookie `almanach_session` (HMAC over the claims, 30 days,
Secure, HttpOnly, SameSite Lax), and the browser goes back to the page.
A `/api/me` that cannot be reached or read halts with a retry button: the
UI never assumes a mode the server did not state. Nothing is stored server-side: `POST /api/auth/logout`
clears the cookie, and rotating `BACKEND_SESSION_SECRET` signs everyone
out. A failed callback lands on `/?auth_error=…` and a sign-out on
`/?signed_out=1`; when `/api/me` then answers 401 the UI halts with a
button instead of redirecting, because a live provider session would
answer the next login unseen and loop. With a session, or in none mode,
the markers mean nothing and are stripped from the address. A 401 from
any later API call in oidc mode navigates to the login the same way; an
automatic sign-in within 30 s of the last one halts instead, so a
session the browser keeps dropping cannot loop either. The gate covers
every path under `/api` but the login and its callback, so a route added
later is covered without anyone remembering; the SPA shell, the webhook,
the ping and the probes are outside it. Its one other door: a GET of the
two Markdown exports passes on a team bearer (The Markdown export, above). A change through the gate (POST,
PUT, PATCH, DELETE) must come from Almanach's own pages: Go's
CrossOriginProtection decides (`Sec-Fetch-Site` same-origin or none, else
an `Origin` naming the request's host; the redirect URL's origin is
trusted too), and a write carrying neither header is refused as well;
else 403. SameSite Lax alone would let a sibling subdomain post. The
origin check runs in none mode too, without the neither-header rule, so
a script with curl still writes there and a page on another site cannot.

Discovery runs in the background from startup on, each attempt bounded to
30 s; boot never waits for it. A provider that cannot be reached (no
connection, a timeout, a 5xx) is a warning, like an unreachable gateway:
noergler's webhook and ping do not depend on it. The login and the
callback land on `/?auth_error=provider_unavailable`, where the UI halts
with a button to try again, and the wiki stays behind its 401 until a
retry (5 s, doubling up to 5 min, each failure logged) reaches the
provider. An answer that is configuration, not reachability (an issuer
that does not match, a 4xx, a 200 that is no discovery document; the
discovery's transport records the status, since the discovery reports
every failure as text), is a fault: logged at error level once, never retried, the login
landing on `/?auth_error=provider_misconfigured` until a restart. In
none mode there is no sign-in: `/api/me` answers
`{"mode": "none", "user": ""}` and the login routes 503.

## 4a. The sources

The descriptions above are built from deltas: what each merged pull request
taught. What they lack is the baseline, a description of the repository as
it stands, and the baseline needs the code itself. The sources feature is
the first half of that: Almanach clones every repository of a project into
a base folder and keeps the clones current. Reading them and writing the
baseline description with the model is the second half, section 4b; the
clones record the head commit each checkout holds, so a later run diffs
since the commit it last described instead of re-reading everything.

A **source** is a project. Two kinds: a **Bitbucket project**, whose
repositories are listed from the Bitbucket Server REST API
(`/rest/api/1.0/projects/{key}/repos`, paged, archived ones skipped) on
every cycle; and a **static folder**, a project key the operator chooses
with repositories entered by hand as clone URLs, for example the
repositories of one GitHub organisation. Both land as
`<BACKEND_REPOS_DIR>/p<project id>/r<repo id>`, named by ids, never by the
key or slug; nothing else is ever written there. In compose that folder is
the `repos` volume, in a deployment a volume claim.

A source's repositories form **one system** by default: a project page
and a system map, written by every run's project step (4b), the way a
Bitbucket project is one product. A static folder can say otherwise
("Bildet ein System" off, `system: false`): a folder of repositories that
merely share a host and a token, a GitHub account's libraries and tools
side by side, is no system, and a project page would describe one that
does not exist. Its repositories then stand alone: each keeps its page and
its repository map, the run makes no project call, a note's fold shows the
model no project page and asks for null (whatever comes is not written),
the wiki lists the repositories one by one under "Einzelne Repositories"
and serves the project without a description, map or Markdown export. A
project page written while the folder was a system stays in the store with
its history; switched back on, the folder is marked due (`project_due`)
and the next run of any of its repositories folds the project whatever a
reconcile would skip, the mark cleared once that step got its answer, so
the kept page catches up with repositories that came, went or changed
meanwhile. With the sources off the word counts for nothing: every project
is a system again. A Bitbucket project is always one system: the switch is
a 422 there.

A project and a repository are ids; the key and the slug are names. Every
name either ever had stays a name of it, so a renamed project or repository
keeps its pages, notes, runs and clone, and a URL, a note or a listing
entry under the old name still lands on it. A project is renamed on its
source page (a change of case included), a hand-entered repository on its
row. A Bitbucket repository follows its name in Bitbucket: the listing
knows it by Bitbucket's own repository id, so a rename there changes the
slug on the next sync instead of adding a second repository. A name once
used is never given to another project or repository, in any case. PR keys
keep the name the PR was merged under.

Git is the binary, after ../rongo's `gitrepo`, never a Go library: proven on
any repository size, and every rule here was paid for there.

- A clone is bare and full history: an object store, no working tree, so
  nothing on disk can go stale between the recorded head and the files,
  and a baseline run reads the object store at a commit (4b). The
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
- A token reaches one host: a URL's origin as a browser writes it (scheme
  and host, case aside, the default port dropped; one normaliser, the auth
  gate's too). A Bitbucket source's is its host; a static folder holds one
  host, checked with the insert in one write transaction when a repository
  is added, token or not. A folder from before that rule that spans
  several gets no new token, and no change of a stored one's auth or user.
  The header value comes from one function, given the target URL, so
  neither git nor the Bitbucket listing can send it elsewhere. A
  repository off its source's host (a listed clone URL off the Bitbucket
  host, any repository of a mixed folder) syncs without the token: a
  public one keeps working, a failing one names why the token was
  withheld. API and poller derive a folder's host from the same rows, all
  of them.
- The origin URL is the identity, the directory name a label: a clone
  whose origin differs from the row's clone URL is removed and cloned
  again, so a corrected URL never serves the old code under the new name.
  A clone is complete only when its `HEAD` resolves to a commit; git
  writes `HEAD` before any transfer, so a clone cut off by a shutdown has
  one and no refs, fails that check, and is removed and cloned again.
- Work on a clone (a sync, a run's read, a removal from the API) holds
  that repository's lock, so a run reading one repository never waits for
  the sync of another; a removal of a whole project folder holds them
  all. A removal never runs while a sync touches its clone, and a row
  removed mid-cycle is skipped by the cycle that listed it, so a deleted
  folder is not cloned back as an orphan.
- Keys and slugs are validated as single path segments (Bitbucket's
  charset, never `.` or `..`) and the resulting path checked with
  `filepath.Rel` against the base, both on the way in and again before any
  `RemoveAll`: the cost of being wrong is deleting outside the base.

One poller, in process, sequential: a stampede of parallel clones against
one forge is how a token gets rate-limited. The first cycle runs thirty
seconds after start (announced in the log, so "not yet" reads apart from
"broken"), then every `BACKEND_SYNC_INTERVAL`, jittered, and at once on a
nudge from the API. A cycle lists every Bitbucket source (a new slug is a
pending row, a vanished one `missing` with its clone kept, a failed listing
leaves the rows alone and goes on the source), then syncs every repository
that is not missing. One repository's failure is recorded on its row with
the redacted error and the cycle continues; only the store not answering
stops a cycle. Cycle start and finish log their counts; an unchanged
repository is a debug line. A shutdown cuts the clone in flight; the next
cycle finds the half directory and redoes it.

The token at rest is sealed with AES-256-GCM under `BACKEND_SOURCE_TOKEN_KEY`,
a fresh nonce per seal, stdlib only. Without the key a public source still
works and saving a token is a 422 naming the variable; a stored token the
key cannot open (the key changed) fails that source's repositories with a
line saying so. The token is never returned by any route (`has_token` is
all a body carries), never logged, never in `.git/config`.

The routes under `/api/sources` are the first writes beside the webhook.
They are gated exactly like the read routes: by the session in oidc mode,
by nobody in none; the reader is logged on every write and decides
nothing. A write needs `Content-Type: application/json`, which
a cross-site form cannot send without a preflight. With
`BACKEND_REPOS_DIR` unset the feature is off: no poller, the list route
answers `enabled: false`, every other route 404, and the UI hides the
entry. The UI section "Quellen" is the one read-write part of the SPA: the
sources list with a create form, a source page with its repositories, their
states, inline forms for the token and for adding a repository to a static
source, and inline confirms for removals that name the folder that goes.
It polls every three seconds while a clone or a baseline run is under way
and otherwise not at all.

### Dependencies between repositories

One repository is cloned once, whatever uses it. A clone URL is one source
row's across every source (`store.CloneKey`: the origin, the path lower
case without a trailing slash or `.git`): a second add, a rename onto it
or a listing naming it is refused (409 naming the row) or left out (the
source's listing line says so). A row gone `missing` holds nothing, so a
repository moved from a Bitbucket project to a folder is taken there.

Who uses whom is code's, never a model's. After a sync, still under the
clone's lock, the poller reads the build manifests at the head when they
were last read at another commit: `go.mod`, `pom.xml`, `package.json`, at
the root or one level down (deeper ones are copies, a checked-out corpus
or a fixture). `internal/manifest` turns them into coordinates: what the
repository provides (`go:<module>`, `maven:<group>:<artifact>`,
`npm:<name>`) and what it requires, with the version as written; a POM's
parent counts as required, and an ISO-8859-1 POM is read. A `${...}`
coordinate is left out, a `${...}` version is empty. Every body
passes `redact.File` first, since a version is shown. One row per source
repository in `repo_manifests`, replaced on a new head, gone with the row.

The edges are never stored. `store.Dependencies` joins requires against
provides at read time over the current source rows: a repository added,
removed or moved changes the graph with nothing to keep in step. A use of
itself (a monorepo's modules) is no edge; a coordinate two repositories
provide (a fork, a copy) gives no edge to either and is listed as
ambiguous; when the providers share one clone URL (two sources cloned it
before the rule) it says so, and removing all but one row gives the edges
back. A repository's body carries `uses` (with the library's other
users, `also`) and `used_by`, the `.md` export an
`Abhängigkeiten` section, and `GET /api/dependencies` the whole graph,
drawn on the page "Abhängigkeiten" as a flow from users to libraries. With
the sources off every list is empty.

A library is described once, on its own page, and read as context where
it is used. Every describe step gets the summaries of the libraries its
repository uses (`store.Libraries`, fitted to `LibrariesMaxBytes` like the
project's other repositories), the project step those of other projects
the project's repositories use; both prompts ask how the repository or
the system uses them, never what they are. A project's body carries those
libraries as `libraries`, and the UI draws them on the system map in a
zone of its own ("Bibliotheken") with an edge from each user, replacing a
node the model drew for one; the stored map never holds them, and the
model is told not to draw them. A standalone folder has no map, so none.

## 4b. The baseline

A **run** reads one repository's clone at one commit and folds what the
code teaches into its page: the description the repository should have had
before the first note. From then on the notes are the deltas, and a
monthly reconcile reads what changed in the code since, PRs or not.

**The source is the starting point.** With the sources on, a repository
exists for Almanach once it is a source repository. A note for any other
is answered 202 `ignored`, logged with its key and slug, and never stored:
noergler marks it sent and does not resend. A note still queued for one
(from before the sources, or for a repository removed since) is taken off
the queue unfolded. The wiki lists the sources then, with noergler's team
where it has delivered for the key; a source key that differs from a key
noergler delivers only by case is refused at creation, since every note
of the project would be ignored. With the sources off, nothing changes:
every note is taken and the wiki lists noergler's projects.

**Triggers.** Read from the rows after every sync, never from an event:
the poller queues a full run (trigger `first_ready`) for a ready clone that
has no run at all, so a run whose queueing was lost (a failed write, a
restart between the sync and the queue, the baseline off at the time) is
queued by the next cycle. It queues a reconcile when the head left the
documented commit and the last run ended more than
`BACKEND_BASELINE_RECONCILE` ago. A failed run, first, reconcile or a
button's, is queued again in its own mode (trigger `retry`) a day after it
ended, or after the interval when that is shorter, three times in a row,
whatever the head and even with reconciles off; past that the repository
is back on the interval, so a failure that is permanent costs a call a
month, not a day. The buttons on the source page queue a reconcile
(Aktualisieren, a full run before the first) or a full run with a fresh
catalogue (Neu beschreiben).

**A run, step by step.** Each step is at most one model call, so a step is
what the worker interleaves with notes, holds after a gateway failure and
resumes after a restart. The run pins the clone's head when it starts.

1. *Catalogue.* The tree at the commit (`ls-tree`, modes and sizes), held
   to what a run may read: regular files, within 100 kB, not vendored,
   built, locked, binary or generated. A full run asks the catalogue model
   (`BACKEND_BASELINE_CATALOGUE_MODEL`, else `BACKEND_MODEL`) for the
   repository's subjects (a flow, a feature, a part) with their paths,
   from the listing (directories over 4000 files) and the docs. Code holds
   the answer to the tree: invented paths dropped, directories expanded,
   each subject within 240 kB, at most `BACKEND_BASELINE_MAX_CALLS` less
   four (the catalogue, the overview, the project, the repository map). A
   reconcile starts from the documented run's plan instead: the
   diff since its commit (`diff-tree`, files that left the readable set
   counted as gone) maps onto the subjects, and only files no subject
   holds go to the catalogue model to be placed; files it cannot place
   stay on the plan for the next reconcile. Over half the catalogued bytes
   changed, a documented commit no longer in the history, or no plan: a
   full run instead.
2. *Overview.* The docs and manifests, chosen by code (README, agent
   notes, architecture docs, manifests, `docs/`, then diagram sources
   such as `.puml` and `.mmd` at the root or under `docs/`, last so a
   large generated one never crowds out prose), write the summary and
   what spans the repository. A reconcile reads them only when one changed.
   Docs are claims: to the overview a statement resting on code alone, no
   doc among its files, is read-only (struck from its patch, the rest
   applied), and a subject step whose code contradicts a statement
   corrects it from the code, whatever it was learned from.
3. *Subjects.* One step per subject the run reads: its files through one
   `cat-file --batch`, the current page beside them, and for a reconcile
   the ids of the statements resting on changed files. Beside the files,
   as untrusted data, the repository's problem areas the subject's files
   sit in (`RepoProblems`, read once a run: the whole history up to the
   pinned commit's time plus `newerSlack`, the head's own PR being
   recorded a moment later; not the dashboard's window: what broke does
   not expire, and a fix merged later is no evidence about the code
   read): an area whose
   `bugfix_area` is one of the files' directories, holds one or lies
   inside one, with its bugfix count, the cause seen most and the newest
   three fixes' PR keys; `_None._` when there is none, and always for the
   overview. The root area backs no subject: ingest files a fix there
   whose files share no directory, or that named none. The prompt's rule: a
   pitfall only where a problem area or a bugfix note backs it; without
   one, none. The answer is a patch (edits by id, new statements in
   `added` without one, drops, confirmations, a summary or null), applied
   under the rules below and then folded like a note: `attach`, a summary
   required, identical means no revision. The revision and
   the run's progress land in one transaction. No prompt names a budget
   or a ceiling: the page has none, and every part writes what its code
   teaches. The growth after the fold is logged.
4. *Project.* The repository's new description folded into the project
   page, on a full run always, on a reconcile when the summary changed;
   never for a folder whose repositories stand alone (4a), which ends the
   step without a call and with nothing skipped.
   Beside it the step sees the summaries of the project's other source
   repositories (80 kB with their headings; past that an even share each,
   a long summary cut at a paragraph or sentence and marked; those without
   a summary, or past hundreds of repositories without room for one, by
   slug only after them, and the page keeps what it says of them and of a
   cut one), so the page is written as one system: purpose and boundary,
   each repository's role, the flows across them and the runtime in the
   summary; shared components, shared entities and cross-repository rules
   as statements. The summary is rewritten whole; the statements are a patch
   like a subject step's (edits by id, `added`, `drop`), a statement not
   named kept: as a full rewrite, one answer naming none emptied a page
   (an eval's second repository wiped the first one's eleven). No tree
   shows a project statement's files gone, so the step never drops a
   PR-taught one (a note's fold may rewrite it): that drop is left out
   and logged. Every new statement is kept: the page has no ceiling. Its
   revision and the step's
   end (`project_done` in the run's plan) land in one transaction, so a
   restart never pays the project call twice.
5. *Repository map.* Last, after the project step, so a tight budget
   drops the map before the project page: one call reads the repository's
   finished description (summary and statements, no code) and its current
   map, and returns the repository from inside as a flow (`{"map": …}`,
   null keeps the stored one). The map's basis (`repo_map_basis`, a hash of
   the summary and each statement's kind, name, text and files, never its
   sources or check) says which text it was drawn from: when it matches
   the page as it stands the step makes no call, whatever the run did;
   when the map is missing or the text moved on (this run's steps, a merge
   since, a map step skipped or refused before) a run draws it, a quiet
   reconcile included, the project's call still skipped. What the step
   keeps, a new map or the stored one confirmed, is set with its basis on
   the page's current revision in place, with the run's end in one
   transaction: no revision of its own, the text did not change. A
   refused map, a failed call or spent calls write nothing, the basis
   stays stale for the next run. The map is optional: a database busy past
   its attempts ends the run done without it, never failed. None drawn is
   an answer too: recorded with its basis, no map, so the same text is not
   asked again. A page with no answer for its text (described before maps
   existed, a map step skipped) is never stranded at an unchanged head: the
   poller queues the retry below for it.

**Diagrams.** The model never writes a diagram language: it returns
rongo's intermediate form as strict JSON (`internal/diagram`), a flow, a
sequence or an entity model, and `baseline/diagram.go` holds it to code's
rules like a statement: every arrow and message end declared, no self edge
in a flow or an entity model (a sequence may message itself, rongo draws
it), nothing twice (a statement's flow, like the map, first loses its self
and repeated edges, logged: they say nothing), unique ids, known shapes, labels present, Swiss and
capped, the nodes and edges per diagram from config, within the drawing's
own limits (`diagram.DrawMax*`). A statement diagram that breaks a rule,
or one added past the page's count (`BACKEND_DIAGRAM_MAX_PER_PAGE`), is left out
and logged with its reason (once the rewrite is taken), on the
merge and every baseline step alike: its statement is kept. On the merge
and the project step a statement nothing else changed keeps its sources
and last check; on a describe step a returned statement is checked
against the commit read, like any it returns. One bad drawing never
costs a part's text, and the reading goes on. With the count at 0 no
schema offers a diagram. Code carries a statement's diagram by id like
provenance: `diagram: null` keeps it while the statement's name and text
stay, `{"kind": "none"}` removes it, a new one replaces it, the stored
one returned keeps it; the prompts show it as JSON to correct. Whenever
no returned diagram is taken (null, or one left out), a stored diagram
beside a changed name or text goes, logged: it would picture what is no
longer said. A changed diagram is a changed statement (reordering is
none), and only a new or changed one meets today's caps, so lowering a
cap never freezes a page; a page over the count may keep it, not grow.
Every project with no more source repositories than a map holds (15,
room left for one more node) carries a system map, the project step's alone
(`revisions.map`): zones `clients`, `repositories`, `stores`, `external`,
a repository node named and labelled by a source repository's slug (code
sets the label on a new map, whatever the model wrote, unless the slug is
longer than a label), an edge labelled
by how the parts talk (HTTP, event, DB, library, file), held to the
drawing's limits. A larger project gets none, and the prompt asks for
null. The map never costs the description: an edge pointing at its own
node or repeating one before it is taken out (one "almanach → almanach"
edge refused a whole map once), a repository that left the
project is taken out of it first, and a map that still breaks a rule, or
none returned, keeps the stored one (its departed repositories out too)
with the reason logged. Where a map was due and none is stored, the
revision carries the sentinel `{"kind": "none"}`: asked for, not drawn.
A map is due only while `revisions.map` is NULL, so a quiet reconcile
folds such a project once, not forever; a run that folds it anyway asks
again. A zone needs no title, the UI titles zones by id. A note's fold
carries the map forward unchanged. The API serves the sentinel as no map
with `map_declined`, and the project page shows its placeholder only
where `map_expected` holds and no map was declined. The UI draws both
with rongo's copied layout, drawing and theme, no Mermaid; the map's
repository nodes link to their pages, in the full view too.
Every repository page carries a map of its own, the repository map step's
alone (`revisions.repo_map`, repository revisions only): the repository
from inside, zones `entry`, `parts`, `stores`, `external`, an edge
labelled HTTP, event, DB, library, file or call, the same drawing limits
and the same repairs as the system map. No statement diagram, it counts
toward none. A map that breaks a rule, or none returned, keeps the stored
one, logged; there is no sentinel, and the page shows no placeholder.
Every new repository revision (a describe step's, a note's fold) takes
the page's current map and basis in the store, so neither a merge nor the
next run's first step drops it; nothing removes one. Its nodes are parts,
not pages: none links.

**PR facts are checked, not erased.** A patch never drops an id it does
not name. A statement a pull request taught may be dropped only when none
of its files is in the tree any more (`./` and directories forgiven), and
an edit of it keeps every file of it the tree still has. A step's patch
is never refused whole (user, 2026-11-01): an item that breaks a rule
costs only itself, logged. An edit without an id, of an id the page lacks
or already edited, of a PR fact shedding a file the tree has, or of a kind
the page cannot hold or empty, is left out, the statement as stored and not
marked checked; a new statement of such a kind or empty is left out; a drop
of a statement the patch also edits, tries to edit or confirms, or of a PR
fact whose files remain, is left out, the statement kept: two answers about
one statement keep it. An empty summary keeps the stored one; a page with
none yet takes the statements without one. A statement left alone as newer
than the commit read has its part, or the overview, read again by the next
reconcile (`again` in the run's plan), though it was no skip. A note's
merge, a whole page, stays strict.
An edit keeps the statement's PR sources and adds the run's commit
(`Basis`, with the repository's slug); a confirmed statement gets the
commit as `checked`, which a later change of its text voids.

**Failures and caps.** A gateway-class failure holds the worker as for a
note, with the step's attempt counted on the run; eight fail the run. A
full run's catalogue that fails on its own fails the run: there is no plan
to read. A part's call that fails on its own skips that part, the parts
after it still read; a project step refused (a first page without a
summary) or unpaid is skipped too. No part is skipped for the page's size. The calls
(`BACKEND_BASELINE_MAX_CALLS`) are checked before each call, and when
they are spent the rest is skipped. The bytes
(`BACKEND_BASELINE_MAX_BYTES`, the catalogue's docs and every part's
files) are checked per part: a part over what is left is skipped, a
smaller one after it may still be read. A
run that ends with skipped parts is shown as partial and still records
its commit; the next reconcile reads the skipped parts again, the docs and
the project included, a reconcile at the documented commit too. That next
run is never left to a head that may not move: a done run at the head
that skipped a part worth reading again (`baseline.LeftParts`: not a
subject gone, nor a project left unchanged without a page), or left
the repository page without a map answer for its text, gets a reconcile
at the same commit (trigger `retry`) from the poller a day after it ended
(the reconcile interval when shorter), at most three retries in a row,
a failed run's retries counted, and only with the interval on; it reads
the skipped parts and the map, nothing else. A head that moved waits for
the interval, which reads the skipped parts with the changes. The poller
reads a run's page for that once, not every cycle. Past the retries, or
with the interval off, the button is the way, kept open at a documented
head while a part is left. The source page names each skip's reason, an
error as one, and says whether the parts are read again on their own
(`auto_retry`) or with the next run, sooner by the button; the server
sends the rule's answer (`left_parts`), the page has no copy of it. A clone
the poller holds (a long clone or fetch of this repository) defers the
step to the next drain rather than holding the worker behind it; the reads
take the clone's lock for the read only, never across a model call.

## 5. Operations

- `almanach migrate` applies the embedded migrations to the file
  `BACKEND_DB_PATH` names, each in its own transaction, and exits: the
  init container's job. The whole run holds a lock on
  `<BACKEND_DB_PATH>-migrate.lock` beside the database, so a second
  migrator waits for the first instead of applying the same file again.
  `almanach serve` refuses a schema that is behind, naming the pending
  files. Both check first that the file's folder exists and takes a file.
- The database is one file (with its `-wal` and `-shm` beside it) on a
  host folder of its own, apart from the clones: the clones can be wiped
  and refilled from the forge, the database cannot, and a backup is its
  folder. A local disk on one node (ReadWriteOnce), never a shared
  filesystem: SQLite in WAL needs POSIX locks of one host. One replica.
- `/healthz` is liveness, `/readyz` asks the database. Both answer loopback
  callers only, so probes are `exec` probes against the binary's
  `-healthcheck` and `-readycheck`.
- Startup runs every check (database, team keys, model profile, the
  gateway's model listing) before aborting, so the log names all the
  failures at once. A gateway that does not answer is a warning only: notes
  are still taken and folds retry; a gateway that refuses the key or does
  not list the alias is a fault.
- Logs are structured, one line per event. No secret, no token ever
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
| `internal/store` | SQLite: the database file, embedded migrations, every query. `ingest.go` writes a delivery, `wiki.go` serves the worker, `read.go` serves the read routes, `values.go` holds how times and JSON are stored. | ncruces/go-sqlite3 (database/sql), kit/sqlitex, kit/migrate |
| `internal/consolidate` | The worker: loop, hold, fold, the rules (`attach`, `check`), prompt rendering. Declares the `Store` and `Merger` interfaces it needs. | config, inference, lang, prompts, store, llmwire (error classes) |
| `internal/inference` | The llmwire client: startup checks, one `Merge` call with the strict schema, answer parsing, cost extraction. | llmwire |
| `internal/prompts` | `almanach_merge.txt`, compiled in with `go:embed`. Never configurable. | nothing |
| `internal/gitrepo` | The git binary: clone, default branch, fetch, head, origin check, removal with the path guard, credential redaction. | nothing |
| `internal/bitbucket` | The Bitbucket Server repository listing, paged, net/http only. | gitrepo (Access), store (types only) |
| `internal/clone` | The sync poller: cycles, nudge, listing, per-repository sync with the failure recorded on the row, the manifests read after it. Declares the `Store`, `Git`, `Lister` and `ManifestStore` interfaces it needs. | gitrepo, manifest, secret, store |
| `internal/manifest` | `Parse`: a repository's go.mod, pom.xml and package.json as the coordinates it provides and requires. Pure, no I/O. | nothing |
| `internal/secret` | AES-GCM for the sources' tokens, key from the environment. | nothing |
| `internal/lang` | `SwissSS`: the sharp s to `ss`. | nothing |
| `internal/diagram` | A diagram's shape (rongo's intermediate form), data only; `baseline/diagram.go` validates it. | nothing |
| `internal/config` | The environment, read once through kit's reader, every fault collected. | kit/env |

The interfaces are declared where they are used (`api.Store`,
`consolidate.Store`, `consolidate.Merger`) and satisfied by `*store.Store`
and `*inference.Client`. That is what lets the api and consolidate tests
run against in-memory fakes while the store has its own tests against a
real, migrated SQLite file in each test's temporary folder: no server, no
docker, a plain `go test` runs them.

## 7. Storage model

Everything is in one SQLite file, schema
`backend/internal/store/migrations/`. The database records what happened;
the descriptions are derived from it and rewritten by the merge call,
revision by revision, never edited by hand.

How SQLite holds what it has no type for:

- **Times** are `TEXT`, UTC, RFC 3339 with all nine digits of the
  nanoseconds (`2026-10-01T12:00:00.000000000Z`). Fixed width, so text
  order is time order and two merges a second apart keep their order. Go
  writes every one through the store's one helper, in UTC; the schema has
  no clock of its own (no `datetime('now')`, no default on a time column),
  every time column's `CHECK (... GLOB '????-??-??T??:??:??.?????????Z')`
  refuses any other shape, and one read in another shape is an error,
  never a zero time. Not the driver's `_timefmt`: it formats a custom
  layout in the time's own zone, so a local time would land as its local
  clock with a `Z`.
- **JSON** (the note, the bugfix, a revision's statements and map, a run's
  plan, a call's warnings, the delivery body) is `TEXT` with a
  `CHECK (json_valid(...))`, read with `json_each`, `json_array_length`,
  `json_type` and `->>`. Kept verbatim: SQLite does not normalise it.
- **Money** is nano-USD in an `INTEGER` (64-bit), never a float.
- **Ids** are `INTEGER PRIMARY KEY`; a baseline run's is `AUTOINCREMENT`,
  never reused after its repository is removed, because the ledger keeps
  it without a foreign key.

**Writers.** The webhook, the worker, the poller and the Quellen routes
write, each in short transactions; none spans a model call or a git
command. Every writing transaction begins `IMMEDIATE` (`_txlock=immediate`
on the DSN): it takes the database's one write lock at `BEGIN`, so it never
holds a read snapshot and then fails its first write with "database is
locked". A second writer waits up to the busy timeout (10 s, kit's
`sqlitex.DSN`, with WAL and foreign keys); one that runs out is an error
like any failed write, and the webhook answers 500. In the worker it is
temporary (`store.IsBusy`), like a gateway failure: the worker holds, the
note stays queued with its attempt counted, a run's step is taken again
after the hold, both up to `maxFoldAttempts`. Readers never wait in WAL:
the UI and the API read while the worker writes. The pool holds at most
four connections, each a WASM instance of SQLite with its own memory.
`/readyz` reads `schema_migrations`, so a volume gone or a file that is no
database turns it red.

Four groups of tables:

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
time and error. The two groups meet on
`(project_key, slug)`: with the sources on, a note is taken only for a
source repository, and the wiki lists the sources (4b).

**The baseline.** `baseline_runs` is one row per run: the repository, the
mode (`full` or `reconcile` from `base_commit`), the trigger (`manual`,
`first_ready`, `monthly`, or `retry`: a failed run queued again in its own
mode, up to three times in a row, a day apart or after the reconcile
interval when that is shorter), the commit (the head when queued, pinned when
the run starts), the state (`queued`, `cataloguing`, `describing`,
`project`, `done`, `failed`), the step and the steps, the plan (the
catalogue as JSON: subjects with their files and sizes, what this run
reads, what it skipped, files it could not place), the attempts of the
current step, the calls and bytes spent, the skipped parts, the error and
the times. A partial unique index keeps one open run per repository; the
rows go with their repository (`ON DELETE CASCADE`). The documented commit
of a repository is the commit of its newest `done` run.

**The notes.** `notes` is one row per merged pull request, replaced when
the same PR is delivered again (a reopen and re-merge) and then re-queued.
Beside the note object itself (`note`, JSON, verbatim) sit the facts
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
the `summary` prose, the `statements` array as JSON (each with its id,
kind, name, text, files, the sources code attached and its diagram, if
any), a project revision's system `map` as JSON, the serialized
size, what produced it (`source_kind`: the PR whose note it was, or the
baseline run's commit, exactly one of `source_pr_key` and `source_commit`
set) and the call behind it. A statement's source carries the same kind;
a source stored before baselines has none and is a PR. The
check `(scope = 'project') = (repo_slug IS NULL)` keeps the two scopes
honest. `pages` is the current pointer per page: one row, one
`current_revision_id`. Repointing it is a rollback; history is never
rewritten. `llm_calls` is Almanach's own ledger, one row per model call
with its `purpose` (`merge`, `catalogue`, `overview`, `subject`,
`project`) and, for a run's, the run's id; never summed with `note_calls`.

What is derived and what is kept: `deliveries` and `notes` are kept as
they arrived. `bugfix_area`, the statements' `sources` and ids, the
revision numbers and the page pointers are code's. The `summary` and the
statements' text are the model's, as long as the subject needs. The
problem areas are not
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
  keeps a fold's base current; `InsertRevision`'s transaction, which
  holds SQLite's one write lock from its `BEGIN`, only keeps revision
  numbers and the page pointer consistent, it does not make concurrent
  folds safe. Parallelising the worker would need the
  base revision checked at write time.
- **Provenance is code's, never the model's.** The sources on a statement
  (which PRs it was learned from) are what make a wiki trustworthy, and a
  model asked to maintain them would invent PR numbers. So the model
  returns statements with their ids and code carries the sources forward,
  appending the current PR when the text changed. An edited id the page
  never had is invented and never accepted: a merge is rejected, a step's
  patch loses that edit; new statements come in a list with no id field,
  numbered by code; a dropped id is never reissued.
- **No ceiling on a description.** Pages were once held to a statement
  count and a size, a rewrite over one rejected whole, and a step given a
  budget of what it might add. On real repositories that threw away whole
  subjects' work: a late part of a run found the page full and lost
  everything it read. A description is system documentation; dropping
  what the code teaches because of where the page stands is a hole in the
  documentation, not a limit on bloat. Truncating would lie, and so does
  leaving out. What keeps a page from repeating itself is the prompt's
  rule that a fact an existing statement covers is an edit of it, never a
  second statement.
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
  It ships with the process so a deployment is one image,
  and it talks to `/api/...` alone (`backend/api/openapi.yaml`); nothing in
  it knows a team or a bearer, and nothing in the backend knows the UI
  beyond the route allowlist. It is German only, with no settings; its
  login is the backend's OIDC flow, the UI only gates on `/api/me`, names
  the reader and offers Abmelden in oidc mode. The wiki
  pages are read-only; the Quellen section configures the clones, and is
  the only place a browser writes. Statement text and summaries carry a
  layout for the human reader: paragraphs, flat lists, bold terms, code
  spans and, in a statement, one sub-heading level. The backend
  (`lang.Laid`, `internal/markup`) puts what the model writes into that
  stored form after redaction (a key's PEM shape holds only as written)
  and before the Swiss rule reads it: any heading level becomes `####`
  (dropped in a summary), `*` bullets `-`, a closed fence code spans. The UI
  (`format.ts` `blocks`) reads only that form, so markup beyond it (a
  quote, a table, HTML) stays text and cannot change the page's shape.
  Every store read of a page's text and summary passes `lang.Relaid`, so
  a revision written before the layout reads as a new one does, to the
  UI, the export and the fold alike. A text only laid out anew says
  nothing new and credits no source. Note text is noergler's and stays
  paragraphs.
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
  form, not a redeploy; `BACKEND_SOURCE_TOKEN_KEY` is what makes that safe, and
  without it no token is stored at all.
- **Origin is identity.** A checkout is named after its row, and a row's
  clone URL can be corrected; comparing `remote.origin.url` on every cycle
  is what keeps the old code from answering under the new name.
- **The source is the starting point** (user, 2026-10-30). A note for a
  repository that is no source is ignored and never stored, not held
  until a source appears: the baseline reads the code that PR produced,
  so the fact is not lost, its PR chip is. Storing ignored notes would put
  into `deliveries` what must never be rebuilt into the wiki.
- **Runs live in the one worker, notes first.** A run is a write to the
  same pages the notes write; a second writer would need the base checked
  at write time. A note is a small, timely delta and a run minutes of
  calls, so the notes go first and a run steps when none is due; a
  repository's notes wait only while its own run is running.
- **Read by subject, answer with a patch.** A catalogue of subjects keeps a
  flow that spans directories in one step; a directory group would show
  the model a third of it per call. A patch over the page rather than a
  rewritten page: a step sees part of the code, and a full rewrite per
  step would pay for the whole page's output each time and risk losing
  statements about parts it never read.
- **A run checks what the PRs taught, it does not erase it.** A PR fact
  carries knowledge the code does not show, such as why a rule exists; a
  run may correct it but drop it only when its files left the tree. Every
  note is folded, a note whose PR the run already read included.
- **A cheaper model picks, the strong one writes.** The catalogue is a
  listing and the docs, a selection task; the steps that write are the
  expensive ones. `BACKEND_BASELINE_CATALOGUE_MODEL` is optional.
- **A page is cut for the agent, not from an architecture template.** The
  classic architecture-documentation templates were written for people
  explaining a system to people. Their building blocks and runtime flows
  carry over as the summary's order, their architecture decisions as
  `decision`, narrowed to a stated reason. Goals, stakeholders and quality
  scenarios an agent about to change the code hardly needs; what it needs
  most, where to change what and what broke before, they have no place
  for: hence `extension` and `pitfall`, and the gate and test commands as
  the summary's last part. `component`, `rule`, `invariant` and `entity`
  mirror the lists a noergler note carries (`components`, `rules`,
  `invariants`, `entities`). The kinds, their order and what is left out
  are in the page model. A section taken from a template (quality goals,
  cross-cutting concepts) has to say which question it answers for an
  agent about to edit the code, and which evidence feeds it rather than
  the model's guess.

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
  the outcome (`accepted`, `deduped`, `ignored`, `invalid`), the team and
  the delivery id when one could be built. A request without a known bearer
  is a 401 on the access log only; a delivery the database refused logs
  `webhook_persist_failed` instead, a failed source check
  `webhook_source_check_failed`, each with the 500, so the sender keeps
  the note pending. Count the lines together for a per-request total.
- **With the sources on, the source is the starting point.** A note whose
  `(project_key, repo_slug)` is no source repository is answered 202
  `ignored` after the envelope is checked, and nothing is written.
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

A note's rewrite (the merge, a whole page) passes all of these or is
rejected whole, with the reason in the log and the page unchanged. A
baseline step's patch never is: its bad items are left out one by one (see
"PR facts are checked, not erased"):

- A statement's `kind` is one of `component`, `interface`, `rule`,
  `invariant`, `entity`, `decision`, `extension`, `pitfall`; on the
  project page not `extension`. Any other kind rejects the rewrite.
- `name` and `text` are trimmed and Swiss-spelled before anything is
  checked; an empty one rejects the rewrite. A statement returned exactly
  as stored is never held to the kind or emptiness rule: one written
  before a rule stays.
- A kept `id` must exist on the current page and may appear once; an edit
  without one rejects the rewrite. A statement in `added` is new (any id
  an answer slips in there is cleared) and gets the next id past every id
  any revision of this page ever used: `s1`, `s2`, ... with no gaps reused.
- Sources: a kept statement whose name and text are unchanged keeps its
  sources exactly; one whose name or text changed gains the current PR
  once (never twice for the same PR); a new statement starts with the
  current PR. The model never writes a source.
- The summary is not empty, after the spelling. There is no ceiling: no
  statement count, no size of the whole, no cap on a name or the number
  of files (user, 2026-11-01; earlier caps lost whole parts of real
  runs). The old `BACKEND_*_MAX_STATEMENTS` and `BACKEND_*_MAX_CHARS`
  set are a startup fault.
- A path over 200 runes or with whitespace is no path of a tree: it
  leaves the statement's file list, logged, the statement kept (empty
  file strings are dropped first). The prompts state that rule.
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
- Both pages have two tabs and a rail beside them. The project page opens
  on Übersicht (system map, repositories, problem areas, recent notes),
  the hub's job, then Beschreibung; a repository page opens on
  Beschreibung (its map, the summary, its own problem areas, the
  statements), its content, then Verlauf (notes, revisions). The hash
  holds the tab (`#beschreibung`, `#verlauf`, none for the start tab),
  replaced on a switch, never pushed. The rail lists both tabs' sections
  and, on a repository page, the project's repositories; its search
  filters this page's description (names, texts, files) in the browser.
  A repository page's problem areas and repository list come from the
  project endpoint, filtered by `repo_slug`; without it the page goes
  without them. The project page names its entities „Geteilte Daten“:
  they are the ones that cross repositories.
- A repository's summary on the project page is its description's first
  sentence: cut at the first `.`, `!` or `?` followed by a space or the
  end, except after an abbreviation (`z.B.`, `bzw.`, `usw.`, ...) or a
  dotted name (`config.yaml`, `2.0`) unless a capital follows.
- A note's `items` is the count of its components, rules, invariants and
  entities; a list that is null or not an array counts as empty and never
  breaks a read.
- Problem areas: bugfix notes of the project whose `merged_at` is within
  the window (`?days=`, 1 to 3650, default `BACKEND_PROBLEM_WINDOW_DAYS`),
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
- In oidc mode the reader (kit's username: preferred username, else
  email, else subject) is written to the access
  log of every read route and nowhere else; it gates nothing, and the
  webhook and the ping never pass the sign-in.
- The Markdown exports take a team bearer in oidc mode, GET only; a
  bearer opens no other route. A statement's text is never rewritten into
  Markdown; the export's order is the UI's (invariants, interfaces,
  rules, decisions, pitfalls, extensions, components, entities), held to
  it by a test.

### The sources

- A key or slug is one path segment of Bitbucket's charset, never `.` or
  `..`; a clone URL is https without credentials; a Bitbucket source has a
  host (https, no query, no credentials), a static one has none.
  Checked on the way in (422 naming the field) and again before a removal.
- `BACKEND_BITBUCKET_HOST` only prefills the create form's host, checked
  at startup by the same rule (`gitrepo.ValidHost`); the host stored on
  the source is the one used.
- `token_auth` defaults to `bearer` for Bitbucket and `basic` for static;
  `token_user` goes with `basic` only. An omitted token on PATCH keeps the
  stored one, an empty one clears it. A token without `BACKEND_SOURCE_TOKEN_KEY`
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
- One clone URL, one source row, across sources (`CloneKey`), checked in
  the transaction that writes it; a missing row holds none.
- Manifests at the root or one level down only, read by the poller at the
  head under the clone's lock, redacted first; a failed read or save is a
  log line, never the repository's state. Edges joined at read, never
  stored; a coordinate two repositories provide is no edge.
- The feature off (`BACKEND_REPOS_DIR` unset): no poller, `GET /api/sources`
  answers `enabled: false`, every other sources route 404, the UI hides the
  entry. On: git on PATH, the folder present and writable (a probe file),
  and the key, if set, 32 bytes of base64, all checked at startup.

### The baseline

- One open run per repository (partial unique index); a second describe
  is a 409. A run's commit is pinned when it starts; every read is at that
  commit, never a working tree.
- A repository's notes wait while its run is past `queued`; afterwards
  every waiting note is folded.
- Each step makes at most one model call; the caps are checked before it.
  A step's revision and the run's progress land in one transaction, the
  project step's with the run's end.
- Paths a model names are held to the tree, `./` and directories forgiven:
  a catalogue path the tree does not have is dropped, a statement a step
  writes or edits keeps only the files the tree has, and the drop and edit
  rules for a PR statement read the whole tree.
- A statement taught by a PR whose merge commit the run's commit does not
  hold is read-only for that run.
- A patch: an unnamed id stays; an edit without an id, edited and
  dropped, or confirmed and dropped rejects it. An edit of an id the page
  lacks (invented) or of one an earlier edit took is left out with a log
  line ("edit left out: s23 (unknown id), s3 (id used twice)"), the
  statement as stored or as first edited; an unknown id to drop or confirm
  refers to nothing and is left out too ("unknown ids left out: ..."); the
  rest applies. Nothing unknown is ever accepted, so the invented-id rule
  holds; only its blast radius is narrower: one made-up id cost a whole
  part twice in one eval (user, 2026-11-01). A PR
  statement is dropped only when its files left the tree, and an edit of
  it keeps the files the tree still has.
- A run that ends with no revision still records its commit as documented.
- A failed or refused part is named on the plan and read by the next
  reconcile; files a reconcile could not place are too.
- Reads take the clone's lock for the read only; a lock held past two
  seconds (its own repository syncing) defers the step to the next drain,
  never fails the run.

### Startup and operations

- Startup runs every check before aborting: the database answers and the
  schema is current (the pending migration files are named), the keys file
  loads with at least one team, the model profile supports strict JSON
  schema output and the configured reasoning level, and the gateway lists
  the model's alias; with the sources on and a catalogue model of its own,
  the same for it at its balanced level. The listing is bounded to ten
  seconds. A gateway that
  is unreachable or answers 5xx is a warning; a refused key, a 4xx, a body
  that is not a listing, or an alias missing from it is a fault.
- `serve` never migrates; `migrate` needs only `BACKEND_DB_PATH`. A schema
  behind the binary refuses to start.
- `/healthz` and `/readyz` answer a loopback caller without forwarding
  headers only; through a router they are 404 by design.
- In oidc mode the issuer (as written: https, http only on loopback), client
  id and secret, the redirect URL `https://<host>/api/auth/callback` and a
  session secret of at least 32 bytes are required, a missing or malformed
  one is a startup fault, and each is a fault under none. A provider that
  does not answer discovery (30 s an attempt) is a warning, retried in the
  background, never awaited; the login lands on a halting page meanwhile. `none` is allowed on any
  `BACKEND_ADDR`.
- Caps and the problem window must be positive integers; a zero or
  negative value is a startup fault, not a disabled cap.
