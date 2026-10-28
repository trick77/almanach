# Almanach

[![CI](https://github.com/trick77/almanach/actions/workflows/ci.yaml/badge.svg)](https://github.com/trick77/almanach/actions/workflows/ci.yaml) ![Go 1.27](https://img.shields.io/badge/go-1.27-blue)

A living wiki, written by the code reviewer. Companion service to
[noergler](https://github.com/trick77/noergler), the way
[riptide](https://github.com/trick77/riptide) is for cost.

Every review noergler runs already paid for reading the files, the diff and
the ticket. Almanach keeps what the model learned on the way: after each
review round noergler asks the same conversation one more question, "what
does this pull request teach about this repository?", and at merge it sends
the answer here. Almanach folds every note into two short descriptions, one
per repository and one per Bitbucket project, under hard size caps, and
keeps a history of every revision. Nothing flows back into reviews.

The descriptions are German, Swiss spelling. Nobody edits them by hand: the
next pull request that gets a fact right corrects it.

## What you get

- **Repository description**: a short summary plus statements about the
  components, business rules, invariants and data entities of the code,
  each with the pull requests it was learned from.
- **Project description**: what spans the project's repositories, built by
  the model from the repository notes.
- **Problem areas**: bugfix notes grouped by the directory the fixes touched,
  with the most common cause class, so a team sees where it fixes the same
  thing over and over.
- **Notes**: every merged pull request's note, with the full gateway
  statistics of the call that wrote it (tokens, cache hits, cost).

A read-only JSON API serves all of it; the UI comes later. The contract is
[backend/api/openapi.yaml](backend/api/openapi.yaml).

## How it is built

One Go process and one PostgreSQL database. The process has three parts:
the HTTP surface (the bearer-authenticated webhook noergler calls, and the
read routes a browser reaches through an OAuth sidecar), the store (every
query and transaction, PostgreSQL via pgx, embedded migrations), and the
consolidation worker (one serial goroutine that folds each note into the
descriptions with one model call and rules the model cannot bend: caps,
spelling, provenance). Every delivery is kept whole and deduped on
noergler's key; the descriptions are revisions with a current pointer, so
history is never rewritten and a rollback is a repoint.

The architecture, the package layout, the storage model and the design
decisions with their reasons: [HOW_IT_WORKS.md](HOW_IT_WORKS.md). Every
knob: [CONFIGURATION.md](CONFIGURATION.md).

## Running it

Almanach needs PostgreSQL and an OpenAI-compatible gateway (a LiteLLM
proxy) for the merge model.

```sh
cp .env.example .env            # fill in the gateway
cp team-keys.example.json team-keys.json
docker compose up --build       # Postgres, migrate, almanach on :8081
```

One token per team in `team-keys.json`; the same token goes into that team's
`almanach:` block in noergler's `teams.yaml`. noergler checks it at startup:

```sh
curl -H "Authorization: Bearer <token>" http://localhost:8081/auth/ping
# {"status":"ok","team":"platform"}
```

Then merge a reviewed pull request and read the result:

```sh
curl http://localhost:8081/api/projects
curl http://localhost:8081/api/projects/<KEY>/repos/<slug>
```

In production an OAuth sidecar sits in front of the read API; see
`ALMANACH_AUTH_MODE` in [CONFIGURATION.md](CONFIGURATION.md).

## Development

```sh
make hooks     # once per clone: ship's pre-push gate
make check     # everything CI runs (needs docker for the store tests)
make lint
make build     # bin/almanach
```

The Go module lives in `backend/`. Releases are built on GitHub only:
`gh workflow run Release`.

## License

[MIT](LICENSE).
