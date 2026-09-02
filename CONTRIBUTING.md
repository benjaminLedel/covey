# Contributing to covey

Thanks for looking. covey is a young project with a small maintainer circle —
issues, questions and pull requests are all welcome, and so is the observation
that something in here does not make sense.

## Before you write code

**Concept and architecture changes go through the spec first.** covey is
specified in [`spec/`](spec/) (16 documents, start at
[`spec/README.md`](spec/README.md)). If your change alters how the system
behaves rather than how it is implemented, open an issue or a pull request
against the responsible spec document and let us settle the shape there. That is
cheaper for both sides than a large branch that turns out to contradict the
model.

Larger, not-yet-settled proposals live in
[`feature-requests/`](feature-requests/) as numbered documents: motivation,
trigger condition, how it embeds into the existing architecture, non-goals,
acceptance criteria. Accepted content moves into `spec/`; the request stays as a
record.

For bug fixes and self-contained improvements, just open a pull request.

## Development setup

You need Go 1.26+, Node 20+ and Docker (for the Postgres with `pgvector`).

```bash
make dev-db        # Postgres with pgvector on port 5433
make bootstrap     # master key, migrations, organisation + admin + demo agent
make run           # builds web/dist + both binaries, then starts the server
```

Then open <http://localhost:8494>. `make bootstrap` prints the login it created.

If you would rather not install a toolchain, the Docker path from the
[README](README.md#start-in-two-minutes) works for development too — it is just
slower to iterate on.

**A build is not a restart.** `go build ./...` only type-checks; it does not
rewrite the `./covey` binary. To see a change live: `make build`, stop the
running `covey serve`, start it again. Migrations run automatically at startup.

## Tests

```bash
make test              # go vet + the unit tests, no database needed
make test-integration  # the acceptance suite, needs the dev database
cd web && npm test     # frontend tests
```

The integration suite in [`internal/integration/`](internal/integration/) is the
acceptance checklist from [`spec/11-mvp-plan.md`](spec/11-mvp-plan.md) turned
into code: a real Postgres on port 5433, the daemon in-process over a real
WebSocket, a mock runtime and a fake Zammad. It skips itself when port 5433 is
unreachable, so a missing database looks like a pass — check that it actually
ran before you trust it.

CI runs format checks, `go vet`, both test suites, `govulncheck` and CodeQL on
every push and pull request.

## Conventions that will trip you up

- **Migrations are append-only.** Never edit an existing file in
  [`migrations/`](migrations/) — add a new numbered pair. They are embedded via
  `//go:embed` and run with an advisory lock at startup.
- **`web/dist` must exist before the Go build.** `//go:embed` pulls the built
  frontend into the binary. `make build` does this for you.
- **The UI is bilingual.** Every new user-facing string goes into *both*
  `web/src/locales/de.json` and `web/src/locales/en.json`.
- **Runtimes and target systems are plugins.** They register themselves in the
  daemon and target registries. Do not add a hardcoded list or a special case in
  the core — if the core needs to know a plugin's name, the design is wrong.
- **Interface before implementation.** The pluggable ports (`identity/`,
  `secrets/`) keep the interface in the package root and implementations in
  subpackages, even where only `builtin` exists.
- **Never put long-lived secrets in the sandbox.** Access is brokered at
  runtime, short-lived and scoped. Guard rails are enforced centrally, outside
  the runtime, fail-closed — never by asking the agent's prompt nicely.
- **`spec/` and `docs/` are English**, in the tone of the documents already
  there: plain, precise, no marketing. Strings that come *out of the program* —
  error messages, log lines, UI labels, config syntax a parser reads — are never
  translated in documentation; they are quoted as they appear.
- **`README.md` is English, `README.de.md` is its German translation.** Change
  both together; they are translations of each other, not separate documents.

## Commits and pull requests

The existing history is in German, and maintainer commits stay German. If you
write German, match that style — a short imperative summary line describing the
change, not the activity. **If you do not write German, English commit messages
are fine**; we would rather have the contribution.

Keep a pull request to one coherent change and say in the description what it
does and why. If it changes behaviour, point at the spec document that covers
it — or explain why none does.

Note that `main` lives in two places: GitLab (`gitlab.lapco.legal`, where the
pipeline and the deploy to [covey.work](https://covey.work) run) and GitHub
(`benjaminLedel/covey`, the public copy). Pull requests on GitHub are the right
place for outside contributions; maintainers mirror them across.

## Security

Please do not open a public issue for a vulnerability. See
[`SECURITY.md`](SECURITY.md).

## Licence

covey is licensed under the [AGPL-3.0](LICENSE). By contributing you agree that
your contribution is licensed under the same terms.
