# 26 — Engine catalogue

*What belongs here: where an agent runtime's binary comes from, who decides it, and what has to hold before a sandbox may run on it.*

---

## The problem this removes

An agent runtime is a CLI inside the sandbox. Until now the only way for it to get there was to be **part of the image** — which meant an engine per workplace profile, and a build for every combination that might be wanted:

| | `base` | `dev` | `node` | `python` |
|---|---|---|---|---|
| claude-code | built | built | built | built |
| codex | built | built | built | built |
| educa-ai | built | built | built | built |
| sevencode | — | — | — | to be built |

Four profiles times four engines is sixteen images that mostly contain the same thing, and the last row is the honest one: an engine the project does not publish could not be had at all without building images locally — which is exactly the sentence the workplace catalogue ([`16-runners-and-workplaces.md`](16-runners-and-workplaces.md)) was written to remove, and which `make sandbox-images-pull` still says about every engine today.

This document splits the two concerns that were welded together:

- **the workplace** — the OS, the tools, what the sandbox is built on: a catalogue *image*, decided per agent, published by the project ([`16`](16-runners-and-workplaces.md)),
- **the engine** — one CLI, the same whatever the workplace: a catalogue *layer*, decided per agent's runtime, installed on the host that starts the sandbox.

## The shape

One JSON document behind one URL, fetched with a cache, pinned by digest, deciding nothing on its own. That is `marketplace.Feed` — the mechanism the plugin catalogue ([`22`](22-plugin-marketplace.md)) and the workplace catalogue ([`16`](16-runners-and-workplaces.md)) are built on, with the same four properties: one plain GET so GitHub raw, an S3 bucket, an internal nginx and a `file://` path are one case; the last good copy survives a restart; stale is served immediately and refreshed behind the page; a failed refresh is reported alongside the copy, never instead of it.

Implementation: `internal/engines` (`catalogue.go`, `store.go`, `fetch.go`, `env.go`), wired into the docker provider on both sides of the seam — `cmd/covey` (built-in runner, `COVEY_ENGINE_CATALOG_URL`) and `cmd/covey-runner` (its own environment).

```json
{
  "schema": 1,
  "generated_at": "2026-09-05T09:00:00Z",
  "engines": [
    { "name": "sevencode", "versions": [
      { "version": "1.0.7", "kind": "npm", "package": "sevencode",
        "binary_env": "COVEY_SEVENCODE_BIN",
        "env": ["SEVENCODE_DISABLE_AUTOUPDATE=1"],
        "requires": ["node>=22"],
        "notes": "headless via -p --output-format stream-json --session-id" }
    ]},
    { "name": "claude-code", "versions": [
      { "version": "2.1.0", "kind": "npm", "package": "@anthropic-ai/claude-code",
        "binary_env": "COVEY_CLAUDE_BIN" }
    ]}
  ]}
```

`name` is the runtime name as the daemon registry knows it ([`01`](01-architecture.md)). An entry a build does not register is carried and ignored, never refused — a newer catalogue must not break an older covey. `versions` is append-only, newest last; the last entry is what an unpinned instance gets. A release is `kind: npm` (`package`, optional `registry`) or `kind: tarball` (`url` **plus** `integrity`), and says which variable the adapter reads (`binary_env`, convention `COVEY_<NAME>_BIN` — spelled out because `claude-code` reads `COVEY_CLAUDE_BIN` and nothing derives one from the other).

A document that describes something nothing can install is refused at parse: a tarball without `integrity`, an npm release without `package`, a release without a version, an unknown kind.

## Where a layer lives, and how it gets in

The layer sits on the **runner**, at `<DataDir>/engines/<engine>/<version>/`, and is bind-mounted **read-only** into the sandbox at `/opt/engines/<engine>/<version>`. The fixed container path is what lets one catalogue entry be right on a laptop and on a fleet: the store directory is the operator's business, the path a run reads must not be. Only the one engine's directory is mounted, never the store — a directory of runtime binaries visible to a sandbox is a runtime the platform did not account for, and cost accounting plus credential pools ([`18`](18-provider-abstraction.md)) exist precisely to keep that gap shut.

The path reaches the adapter the way it already did: the runner sets the adapter's own environment variable (`COVEY_SEVENCODE_BIN`, `COVEY_CLAUDE_BIN`, …) on the container. No protocol field for a binary path, no adapter change — the adapters have read that variable from the start, which is the only reason this was a small change and not a new seam. The protocol does carry one new optional field, `StartSandbox.engine`, for the same reason `image_hint` does: only the control plane knows what the agent is configured with, and a runner must be told what to install before it can install it.

A layer is written to a temporary directory and renamed into place; the marker file (`.covey-engine.json`, storing engine, version, kind, digest and the executable **relative to the layer**) is written last. A directory without a marker is a crashed install, not an engine. A tarball is unpacked with a traversal guard — these bytes come off a network and into a directory that is mounted into someone else's sandbox — and an entry that links or writes outside the layer refuses the whole archive.

**Install on first use, not at boot.** An engine nobody uses must not appear on every host, and a boot-time install turns a catalogue host's outage into a host that will not start. The install happens at the one moment its result is needed — the sandbox start — which is also the moment a failure can be attached to the run that needs the reason.

## Digest

`integrity` is hex sha256 over the artefact bytes, required for `kind: tarball`, verified before anything is unpacked. A mismatch names both sides — the promised digest and the one computed — because "it did not match" sends an operator to compare two values by hand.

`kind: npm` pins the exact version in the install request and then **reads back** the version that actually landed (`package.json`); a registry that answers a pinned request with something else fails the start rather than being believed. A publisher who wants a digest for an npm release puts the registry's tarball integrity in the same field.

Lifecycle scripts are disabled (`--ignore-scripts`) unless an entry sets `allow_scripts: true`. That is not a default, it is a boundary: an npm postinstall is code the runner executes as the runner, outside every sandbox boundary this platform maintains, so an entry that needs a build step is a decision by whoever publishes the catalogue and is written down as one.

## Precedence

Lowest to highest:

1. the compiled default — the image carries the engine,
2. the catalogue entry for the agent's engine,
3. an explicit path on the host: `COVEY_SEVENCODE_BIN` etc. set in the runner's or control plane's own environment,
4. what the control plane sends for the sandbox itself.

So the catalogue takes an engine off the image without taking the last word away from the operator, in the same spirit as `COVEY_SANDBOX_IMAGE_<PROFILE>` outranking the workplace catalogue ([`16`](16-runners-and-workplaces.md)).

**Silence is not failure.** The catalogue says nothing in four cases — no URL configured, the start names no engine, the catalogue does not list this engine, the operator named a path — and each leaves the old behaviour standing. What *is* a failure: the catalogue names this engine and the layer cannot be produced. Then the start fails and the task records why. Falling back to whatever binary the image happens to hold would record a run against version 1.0.7 while it ran on the 0.9 in the image — a wrong record, not a degraded one.

## Trust

**Off by default, one variable to turn it on.** `COVEY_ENGINE_CATALOG_URL` is an operator setting, read at process start, and nothing on an agent, a bundle or a task can point it somewhere else. Unlike the workplace catalogue it has no compiled default, because no such document is published yet — a default pointing at a missing file would put a failed fetch on every wake. `engines.DefaultCatalogURL()` is the address the project will publish at, derived from the source address (`buildinfo.SourceRepo`), and it is what an operator sets the variable to; an installation with its own registry or mirror sets its own and is right without editing this repository.

**The document fetch is bounded, the download is not.** The catalogue is read on the wake path, so `internal/runner` gives it five seconds (`catalogueBudget`) and treats a timeout as silence — the image's own engine stands. It has to be a timeout and not a skipped attempt: nothing negative-caches a failed fetch, because a document that did not arrive once is exactly the one worth asking for again next time. The artefact download that follows is unbounded on purpose; an engine archive is large, and an interrupted download fails with a reason rather than being mistaken for a network fault.

**No secret is ever catalogue content.** `env` entries carry endpoints, not credentials: brokered secrets arrive per run through the path in [`04-identity-secrets.md`](04-identity-secrets.md), and a document behind a public URL that contained one would leak it to everyone the catalogue is fetched by. Anything a release needs that is a secret is declared as a credential the platform brokers, not written into `env`.

**The runner does not fetch engines over a socket.** `covey-runner` reads its catalogue URL from its own environment — the control plane does not push binaries at a runner. That keeps the trust boundary of [`16`](16-runners-and-workplaces.md) intact: a compromised control plane can place a sandbox on a runner, but it cannot make that runner download code without the runner operator's configuration.

**The sandbox user can read the layer and must not write it.** Read-only mount, directories `0755`, files as the archive carried them, layer root owned by the runner's user. A writable engine directory would be a way for an agent to change what the next run executes.

## Status, and what comes next

Built and covered by unit tests: catalogue format and parse, resolution (newest last, pinned, unknown), digest verification, npm version read-back, tar traversal refusal, marker round trip, offline copy still serving, the runner's four-case decision and its mount/variable wiring, fail-loud when a promised artefact is missing. The e2e (an agent on an engine that is in no image) needs Docker and was not run in this environment.

Deliberately not built yet, each with the reason:

- **Recording the engine version per run.** The layer knows its version; `RunResult` does not carry one yet, so the transcript cannot say which engine answered. The recording seam is `RunResult` ([`01`](01-architecture.md)) and the figure belongs beside the model for the same reason cost does.
- **A catalogue screen in the UI, and `covey doctor`.** A published list an operator cannot see is a file, not a setting. The workplace catalogue set the pattern: one screen, showing the copy, its fetch time and the refresh error beside it.
- **Version pinning per instance.** `Catalog.Release` already takes a pin; no caller passes one. A per-engine `COVEY_ENGINE_VERSION_<NAME>` would, and it should arrive with the screen that shows what is currently chosen.
- **Signatures over the document.** The digest pins an artefact to a byte sequence, and it does not say who wrote the document. The other two catalogues are in the same position, so this is a shared step, not an engine-specific one.
- **Migration of the sandbox images.** Four engine-install blocks remain in `docker/sandbox/Dockerfile.*`. They stay until the catalogue is published and mirrored locally — removing them first would break every existing installation, and this repository's rule is that an upgrade path exists before the old road is closed.
