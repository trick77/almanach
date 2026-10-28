# How Almanach works

Almanach receives one note per merged pull request from noergler and turns
the notes into two descriptions: one per repository, one per Bitbucket
project. This document follows a note from the webhook to the page.

## 1. Where the notes come from

noergler reviews a pull request in rounds, one per push. After every
successful round it continues the same model conversation (the system
message, the review prompt, the model's review answer) with one more turn:
what this pull request teaches about this repository, as a strict JSON
object in German. Components, business rules, invariants, data entities; the
change kind (`bugfix`, `feature`, `refactor`, `chore`); for a bugfix the
symptom, the cause and its class. noergler validates it with blunt rules
(Swiss spelling, no secrets, caps per field) and keeps the latest round's
note per pull request. At `pr:merged` it stamps the note final and sends it
here with everything it knows beside it: the project key, the repository
slug, the pull request, the ticket, the commits, the counts of issues and
suggestions it posted, the team's project claims, and every figure the
gateway reported for the note call. That is the whole feed. Almanach never
calls Bitbucket, Jira or noergler.

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

The body is decoded strictly: an unknown field, a missing required one, a
malformed timestamp or a short commit hash is a 422 naming the field. The
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

The handler logs exactly one `webhook_processed` line per request with the
outcome (`accepted`, `deduped`, `invalid`) and the delivery id, then pokes
the worker.

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

- Spelling: `ss` for `ß` on every prose field, never on file paths.
- Provenance: a kept id takes its sources from the current page and gains
  this pull request only when its text or name changed; a new statement
  starts with this pull request. An id the page does not have, or one used
  twice, rejects the rewrite.
- Caps: statement count per scope, runes per statement text, runes per
  summary. Over any cap the rewrite is rejected whole and the page stays as
  it was. Never truncated: a truncated description lies.
- An identical rewrite is no revision.

What passes is written as the next revision of the page and the page
repointed to it, in one transaction. History is kept; a rollback is a
repoint. Whatever happened, the note is marked consolidated, so a note that
fails forever cannot block the queue; the log says why.

Every merge call leaves an `llm_calls` row: Almanach's own ledger, never
mixed with the figures noergler reports in `note_calls`.

## 4. The read side

`/api/...` serves the wiki as JSON, structs with stable field order:

- `/api/projects`: every project with a note, newest merge first.
- `/api/projects/{key}`: the project description, the repositories with
  the first sentence of their description, the problem areas, the recent
  notes.
- `/api/projects/{key}/repos/{slug}`: the repository description, its
  revisions, its notes.
- `/api/projects/{key}/repos/{slug}/notes/{pr}`: one note with its facts
  and the recorded note call (tokens, cache hits, cost, rounds).
- `/api/projects/{key}/problems?days=90`: the problem areas of the window.

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

## 5. Operations

- `almanach migrate` applies the embedded migrations under an advisory lock
  and exits: the init container's job. `almanach serve` refuses a schema
  that is behind, naming the pending files.
- `/healthz` is liveness, `/readyz` asks the database. Both answer loopback
  callers only, so probes are `exec` probes against the binary's
  `-healthcheck` and `-readycheck`.
- Startup runs every check (database, team keys, model profile) before
  aborting, so the log names all the failures at once.
- Logs are structured, one line per event. No secret, no DSN, no token ever
  appears in them; the startup config line prints addresses, model and caps.
