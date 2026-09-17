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
      { "version": "1.0.27", "kind": "file",
        "url": "https://cli.example.org/api/v1/cli/latest",
        "integrity": "sha256:26b78c035e543ac7222d81da3143ae76de1bef55ccce676107adbd4e364870f2",
        "binary": "bin/sevencode", "binary_env": "COVEY_SEVENCODE_BIN",
        "auth_header": "Authorization", "auth_secret": "COVEY_SEVENCODE_DOWNLOAD_TOKEN",
        "auth_env": "COVEY_SEVENCODE_DOWNLOAD_TOKEN",
        "requires": ["node>=22.13"],
        "notes": "headless via `-p … --json`; the CLI is one Node bundle and is served as that one file, so the kind is `file` and `binary` says where the runner writes it. Its source is not public, so the artefact sits behind a login: the entry names the header and where its token comes from — a secret of the organisation, or a variable on the host — never the token. The document stays publishable and the digest remains what decides whether a layer may run. `latest` is a mutable address, which is why the version and the digest stand beside it — a new release is a new entry, not an edit." }
    ]},
    { "name": "claude-code", "versions": [
      { "version": "2.1.0", "kind": "npm", "package": "@anthropic-ai/claude-code",
        "binary_env": "COVEY_CLAUDE_BIN" }
    ]}
  ]}
```

`name` is the runtime name as the daemon registry knows it ([`01`](01-architecture.md)). An entry a build does not register is carried and ignored, never refused — a newer catalogue must not break an older covey. `versions` is append-only, newest last; the last entry is what an unpinned instance gets. A release is `kind: npm` (`package`, optional `registry`), `kind: tarball` (`url` **plus** `integrity`) or `kind: file` (`url` **plus** `integrity`, and `binary` says where it goes). The third kind is there because a CLI that is one Node bundle is served as one file, and wrapping it in an archive of our own would put a byte sequence we produced under the digest instead of the publisher's ([`#288`](https://github.com/benjaminLedel/covey/issues/288)). Every kind says which variable the adapter reads (`binary_env`, convention `COVEY_<NAME>_BIN` — spelled out because `claude-code` reads `COVEY_CLAUDE_BIN` and nothing derives one from the other).

An artefact behind a login is named, never opened, by the document. `auth_header` says which header to send; its value comes from one of two references, and a document that carries neither is refused:

- **`auth_secret`** names a key in the organisation's secret store. The control plane resolves it for the agent whose engine this is — that agent's own value first, an org-wide one assigned to it after — and hands the value to the runner with the single start request that needs it. Nothing of it is stored on the host: not in the layer, not in the runner's environment, not in a log. It is the same brokered secret that an agent's `{{secret:…}}` placeholder already is, asked by the platform one moment earlier, because the download happens before the sandbox exists ([`#289`](https://github.com/benjaminLedel/covey/issues/289)).
- **`auth_env`** names a variable on the host that runs the runner. This is the way for a mirror or a registry whose token belongs to the machine rather than to an agent, and it stays as the fallback: an organisation that has put nothing into covey still has a door. It is read only when no secret answered, so a configured value is never shadowed by an old export.

A name, in both cases, and never a value — the document remains something that can be published on its own. Which of the two a start uses is invisible to the catalogue: `Settled()` is true when either is present, and what the secret of an agent really is cannot be seen from the host a runner runs on. The refusal of an artefact that cannot be opened therefore names **both** places a token could come from, in the order it looked; an operator who reads "not set on this host" on a machine where the token was never meant to sit stops reading there. Half a pair is refused at parse, and so is either reference on `kind: npm` — that kind is installed by the package manager, which would read a header by nothing.

One more thing happens to the value on the way out, because `Authorization` is the one header that insists on a scheme in front of the token: `authHeaderValue` supplies `Bearer` when the value brings no scheme of its own. That is not generosity but arithmetic — the token a vendor hands out is `sc_…`, the prefix belongs to the secret, the word in front does not, and whatever lands in a secret field looks like the thing the vendor gave. Sent verbatim it answers 401, and the wake then blames the catalogue while the state is one missing word. A value that carries a scheme (`Bearer x`, `Token x`, `Basic x`) goes out byte for byte as stored, and no other header is touched at all: a registry's `PRIVATE-TOKEN` carries none by design, and completing one there would be the same mistake in the other direction.

Whether the agent actually has the secret is answered where agent and store are both in reach, not on the data plane: assigning an engine resolves the named key once through the same store the start will use, and a missing one comes back as a warning beside the assignment, naming the key. Nothing refuses it — a secret can be set a minute later, and the agent can be moved back.

A document that describes something nothing can install is refused at parse: a fetched kind without `integrity`, an npm release without `package`, a release without a version, an unknown kind.

## Where a layer lives, and how it gets in

The layer sits on the **runner**, at `<DataDir>/engines/<engine>/<version>/`, and is bind-mounted **read-only** into the sandbox at `/opt/engines/<engine>/<version>`. The fixed container path is what lets one catalogue entry be right on a laptop and on a fleet: the store directory is the operator's business, the path a run reads must not be. Only the one engine's directory is mounted, never the store — a directory of runtime binaries visible to a sandbox is a runtime the platform did not account for, and cost accounting plus credential pools ([`18`](18-provider-abstraction.md)) exist precisely to keep that gap shut.

The path reaches the adapter the way it already did: the runner sets the adapter's own environment variable (`COVEY_SEVENCODE_BIN`, `COVEY_CLAUDE_BIN`, …) on the container. No protocol field for a binary path, no adapter change — the adapters have read that variable from the start, which is the only reason this was a small change and not a new seam. The protocol does carry one new optional field, `StartSandbox.engine`, for the same reason `image_hint` does: only the control plane knows what the agent is configured with, and a runner must be told what to install before it can install it. One further field, `EngineWatch`, is explicitly not part of the wire (`json:"-"`): it is the host's own progress callback, set by the node before it calls `Start`. A function could not survive JSON anyway, but the field is marked for the reason beside it — anything that would let the control plane name a host directory to mount is not something this struct should carry, and the install therefore stays inside `Start` rather than becoming a step a caller could skip.

A layer is written to a temporary directory and renamed into place; the marker file (`.covey-engine.json`, storing engine, version, kind, digest and the executable **relative to the layer**) is written last. A directory without a marker is a crashed install, not an engine. A tarball is unpacked with a traversal guard — these bytes come off a network and into a directory that is mounted into someone else's sandbox — and an entry that links or writes outside the layer refuses the whole archive. A `file` release needs the same thought at the one place it can be wrong: `binary` names where the bytes are written, and unlike the other kinds nothing else reads that field literally (a tarball answers with its own executable), so an entry naming `../escape` is refused rather than written one directory up.

**Install on first use, not at boot.** An engine nobody uses must not appear on every host, and a boot-time install turns a catalogue host's outage into a host that will not start. The install happens at the one moment its result is needed — the sandbox start — which is also the moment a failure can be attached to the run that needs the reason.

## Digest

`integrity` is hex sha256 over the artefact bytes, required for both fetched kinds (`tarball` and `file`), verified before anything is unpacked or written. A mismatch names both sides — the promised digest and the one computed — because "it did not match" sends an operator to compare two values by hand.

`kind: npm` pins the exact version in the install request and then **reads back** the version that actually landed (`package.json`); a registry that answers a pinned request with something else fails the start rather than being believed. A publisher who wants a digest for an npm release puts the registry's tarball integrity in the same field.

Lifecycle scripts are disabled (`--ignore-scripts`) unless an entry sets `allow_scripts: true`. That is not a default, it is a boundary: an npm postinstall is code the runner executes as the runner, outside every sandbox boundary this platform maintains, so an entry that needs a build step is a decision by whoever publishes the catalogue and is written down as one.

## Precedence

Lowest to highest:

1. the compiled default — the image carries the engine,
2. the catalogue entry for the agent's engine,
3. an explicit path on the host: `COVEY_SEVENCODE_BIN` etc. set in the runner's or control plane's own environment,
4. what the control plane sends for the sandbox itself.

So the catalogue takes an engine off the image without taking the last word away from the operator, in the same spirit as `COVEY_SANDBOX_IMAGE_<PROFILE>` outranking the workplace catalogue ([`16`](16-runners-and-workplaces.md)).

**Silence is not failure.** The catalogue says nothing in four cases — no URL configured, the start names no engine, the catalogue does not list this engine, the operator named a path — and each leaves the old behaviour standing. What *is* a failure: the catalogue names this engine and the layer cannot be produced. Then the start fails and the task records why. Falling back to whatever binary the image happens to hold would record a run against the version the catalogue names while it ran on whatever the image holds — a wrong record, not a degraded one.

## Trust

**On by default since the document stands.** `COVEY_ENGINE_CATALOG_URL` remains an operator setting, read at process start, and nothing on an agent, a bundle or a task can point it somewhere else. What changed is where an unset variable lands: `engines.DefaultCatalogURL()`, derived from the source address (`buildinfo.SourceRepo`), is consulted the way the workplace catalogue is. An instance therefore reads the document its project publishes without anybody configuring it, and a fork that publishes its own engines carries its own. A build whose source is not on GitHub gets the empty string from there and stays off — the state before this mechanism existed; whoever wants none on a GitHub build points the variable at a document of their own, and a `file://` path is one that reaches no network. An installation behind its own registry or mirror sets its own address and is right without editing this repository.

**The document is maintained in main, not in the pipeline.** `internal/engines/engine-catalog.json` is the file, and the `catalog` branch only carries the copy that is served (`.github/workflows/engine-catalog.yml`). The sandbox catalogue is generated because only CI knows the digests of the images it built; whether a version of a CLI is one this platform vouches for is a decision, and a digest of a hand-built artefact is measured. Two tests hold the file to what reads it: `TestShippedCatalogueIsInstallable` runs the runner's own parse over it, `TestShippedCatalogueNamesEnginesThisBuildRegisters` checks every name and every `binary_env` against the daemon registry that has to answer for it.

**The document fetch is bounded, the download is not.** The catalogue is read on the wake path, so `internal/runner` gives it five seconds (`catalogueBudget`) and treats a timeout as silence — the image's own engine stands. It has to be a timeout and not a skipped attempt: nothing negative-caches a failed fetch, because a document that did not arrive once is exactly the one worth asking for again next time. The artefact download that follows is unbounded on purpose; an engine archive is large, and an interrupted download fails with a reason rather than being mistaken for a network fault.

**No secret is ever catalogue content.** `env` entries carry endpoints, not credentials: brokered secrets arrive per run through the path in [`04-identity-secrets.md`](04-identity-secrets.md), and a document behind a public URL that contained one would leak it to everyone the catalogue is fetched by. Anything a release needs that is a secret is declared as a credential the platform brokers, not written into `env`.

**The runner does not fetch engines over a socket.** `covey-runner` reads its catalogue URL from its own environment — the control plane does not push binaries at a runner. That keeps the trust boundary of [`16`](16-runners-and-workplaces.md) intact: a compromised control plane can place a sandbox on a runner, but it cannot make that runner download code without the runner operator's configuration.

**The sandbox user can read the layer and must not write it.** Read-only mount, directories `0755`, files as the archive carried them, layer root owned by the runner's user. A writable engine directory would be a way for an agent to change what the next run executes.

## Status, and what comes next

Built and covered by unit tests: catalogue format and parse, resolution (newest last, pinned, unknown), digest verification, npm version read-back, tar traversal refusal, marker round trip, offline copy still serving, the runner's four-case decision and its mount/variable wiring, fail-loud when a promised artefact is missing, and the install reporting itself as a phase of its own (`PhaseEngine`) with byte figures the way an image pull reports its own — the first wake after somebody names an engine waits for a hundred and fifty megabytes, and "starting" is not an answer to give for a minute. The e2e (an agent on an engine that is in no image) has since been run against a real container: `internal/runner/engineslive_test.go` materialises a layer from a catalogue document through the store's own code, starts a container with the two arguments that come out, and asks it what the variable names, whether the file at that path runs, and whether it can be written to — plus the same image with nothing mounted, so "absent from the image" is shown rather than assumed. It skips where Docker is absent.

Published since, and consulted by default: `internal/engines/engine-catalog.json` is the document, the `catalog` branch serves the copy (§ Trust), and the address an unset `COVEY_ENGINE_CATALOG_URL` falls back to is that one. The release the document carries today sits behind a login, and the document names `sevencode_api_token` to open it: the key an agent has in covey's own secret store, resolved for that agent at its start and carried to the one install that needs it (#289). `COVEY_SEVENCODE_DOWNLOAD_TOKEN` stays as the second way, for a host that holds a token of its own and has nothing in covey behind it. Before that secret existed as a possibility the engine was installable only on a runner somebody had logged a token into — which is what an organisation hit when it had put the token into covey, seen it in the interface, and been told by the runner that the variable was not set on the machine.

Deliberately not built yet, each with the reason:

- **Recording the engine version per run.** The layer knows its version; `RunResult` does not carry one yet, so the transcript cannot say which engine answered. The recording seam is `RunResult` ([`01`](01-architecture.md)) and the figure belongs beside the model for the same reason cost does.
- **A catalogue screen in the UI.** A published list an operator cannot see is a file, not a setting. The workplace catalogue set the pattern: one screen, showing the copy, its fetch time and the refresh error beside it. `covey doctor` no longer waits for it: it names, per engine an agent is set to, where that engine's CLI comes from — a path on the host, a catalogue release with its version, or nothing, in which case the workplace image has to carry it. That last case is a warning and not a refusal, because the doctor cannot look inside an image without starting a container, and it carries the sentence the first task would otherwise produce (`executable file not found in $PATH`). The same answer stands beside the engine picker when an agent is put on an engine. It also says when a catalogue release's artefact sits behind a login and the host variable naming its token is not set here — a state that ends by setting the variable or by moving the agent, which is why it is a warning with two ways out and not a permanent mark. What the doctor cannot see is the runner's own environment when the runner is a different machine; there it speaks from its own host and says so. An entry that names a **secret of the organisation** instead (`auth_secret`) settles the question here without a warning: the value belongs to an agent, is resolved per start, and this process has no business reading the store to guess whether it stands. Whether this agent has it is asked once, by the assignment handler, through the same store the start will use — the only place both sides are in reach.
- **Version pinning per instance.** `Catalog.Release` already takes a pin; no caller passes one. A per-engine `COVEY_ENGINE_VERSION_<NAME>` would, and it should arrive with the screen that shows what is currently chosen.
- **Signatures over the document.** The digest pins an artefact to a byte sequence, and it does not say who wrote the document. The other two catalogues are in the same position, so this is a shared step, not an engine-specific one.
- **Migration of the sandbox images.** The images carry exactly one engine: `RUN npm install -g @anthropic-ai/claude-code` (`Dockerfile.sandbox:72`), which every dev variant inherits through `FROM ${BASE_IMAGE}`. Nothing installs codex, educa-ai or sevencode into an image, so on the docker provider those three have always needed either this catalogue or an explicit `COVEY_<NAME>_BIN` on the host — the multiplication this document describes was never four engines deep, it was one engine welded to one base image. `educa-ai` rides on the same CLI and therefore worked. That line stays while the published document names no `claude-code`: removing it first would break the one engine every existing installation does have, and this repository's rule is that an upgrade path exists before the old road is closed. What the document does cover is the engine no image ever carried — `sevencode`, whose CLI arrives on a layer of its own.
