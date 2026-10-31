# Almanach

[![CI](https://github.com/trick77/almanach/actions/workflows/ci.yaml/badge.svg)](https://github.com/trick77/almanach/actions/workflows/ci.yaml) ![Go 1.27](https://img.shields.io/badge/go-1.27-blue)

Living documentation of your code, written from the code. Almanach clones
the repositories of a Bitbucket project, reads them and writes what a new
developer needs to know: what each repository is for and how it is built,
which business rules and invariants the code enforces and why, which
entities it works with, and how the repositories together make one system.
It keeps that current as the code moves. Companion service to
[noergler](https://github.com/trick77/noergler), the way
[riptide](https://github.com/trick77/riptide) is for cost.

Two sources feed it:

- **The code.** A baseline run reads a clone the way a careful reader
  would: a catalogue of the subjects the repository is made of, an
  overview from its docs and manifests, then one step per subject over its
  files, then the project as a whole from every repository's summary. Each
  step is one model call that answers a patch; code holds every patch to
  rules the model cannot bend. When a repository's head moved, a
  reconcile (monthly by default) reads only what changed, or everything
  again when most of it did. The docs a repository keeps about itself are
  claims: where its code disagrees, the code wins.
- **The reviews.** noergler already paid to read every pull request's diff
  and ticket. At merge it sends what the pull request taught, and Almanach
  folds it in: the knowledge code cannot show, such as why a rule exists.
  With the sources on, the code is the starting point: a note for a
  repository Almanach does not clone is ignored.

The documentation is German, Swiss spelling. Nobody edits it by hand.
Every revision is kept, with the commit or pull request that wrote it.
Nothing flows back into reviews.

## What you get

- **Repository page**: the repository's purpose and structure, then its
  components, rules, invariants and entities, each as long as its subject
  needs, each naming the files it rests on and the pull requests it was
  learned from.
- **Project page**: the system as a whole: purpose and boundary, each
  repository's role, the flows across them, what runs where; then the
  interfaces between repositories and who owns them, the entities that
  cross them and their system of record, the rules that span them.
- **Problem areas**: bugfix notes grouped by the directory the fixes
  touched, with the most common cause, so a team sees where it fixes the
  same thing over and over.
- **Notes**: every merged pull request's note, with the gateway statistics
  of the call that wrote it.
- **Quellen** (sources): the projects Almanach clones, listed from
  Bitbucket's API or entered by hand, kept current by a poller, each
  repository with its baseline state and a button to read it again.

Every page is capped as a whole, so nothing bloats: a rewrite over a
ceiling is refused, never truncated. A JSON API serves all of it
([backend/api/openapi.yaml](backend/api/openapi.yaml)); a German web UI,
compiled into the binary, shows it. The wiki is read-only; the sources
are the one thing the UI configures.

In the works: a system map on every project page and diagrams where a
picture says more than a paragraph, a login of its own (OIDC), and a
compose stack behind Traefik.

## How it is built

One Go process and one SQLite database file on its own volume. The
process holds the HTTP surface (noergler's bearer-authenticated webhook,
the read API, the UI), the store (SQLite in WAL, embedded migrations),
the poller that clones and fetches
with the git binary, and one serial worker that folds notes and runs the
baseline's steps, so a page never has two writers. Descriptions are
revisions behind a current pointer: history is never rewritten.

The architecture, the storage model and the design decisions with their
reasons: [HOW_IT_WORKS.md](HOW_IT_WORKS.md). Every knob:
[CONFIGURATION.md](CONFIGURATION.md).

## Running it

Almanach needs an OpenAI-compatible gateway (a LiteLLM proxy) for the
model; its database is a file it keeps itself.

```sh
cp .env.example .env            # fill in the gateway and the model; for private
                                # repositories ALMANACH_SECRET_KEY (openssl rand -base64 32)
cp team-keys.example.json team-keys.json
docker compose up --build       # migrate, then almanach on :8081
```

The database lives on the `data` volume (`/data/almanach.db`), the clones
on the `repos` volume: two volumes, the database alone on its own. A
backup is the data volume; the clones refill from the forge.

Open http://localhost:8081/quellen and add a Bitbucket project, or a
static folder with its repositories entered by hand; each repository's
baseline runs once its clone is there. A private repository's token is
sealed with `ALMANACH_SECRET_KEY`; without it only public ones clone.
Then read the result:

```sh
curl http://localhost:8081/api/projects
curl http://localhost:8081/api/projects/<KEY>/repos/<slug>
```

For noergler's notes: one token per team in `team-keys.json`, the same
token in that team's `almanach:` block in noergler's `teams.yaml`.
noergler checks it at startup:

```sh
curl -H "Authorization: Bearer <token>" http://localhost:8081/auth/ping
# {"status":"ok","team":"platform"}
```

The read API, the UI and the source writes are open as this compose runs
them (`ALMANACH_AUTH_MODE=none`): keep it off any network others reach.
In production an OAuth sidecar sits in front; see `ALMANACH_AUTH_MODE` in
[CONFIGURATION.md](CONFIGURATION.md).

## Development

```sh
make hooks     # once per clone: ship's pre-push gate
make check     # everything CI runs; the store tests use a temporary file
make lint      # quick lint while editing
make build     # bin/almanach
make prompt-eval EVAL_ENV=<file>   # full baselines through a real model, paid
```

The Go module lives in `backend/`. Releases are built on GitHub only:
`gh workflow run Release`.

## License

[MIT](LICENSE).
