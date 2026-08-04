# 16 — Runner (the distributed data plane)

Today the data plane runs on **the control plane's host**: the `docker` provider calls the local Docker CLI, the persistent home sits as a directory next to it, the egress proxy is a container on the same machine. That carries one instance and a handful of agents, but it makes the size of the workforce a question of the size of *one* machine.

The **runner** solves this: a standalone process on an arbitrary host that **registers** with the control plane and gets sandboxes assigned to it from there — the same pattern as GitLab runners. The control plane remains the single point of truth; the runners are, like the sandboxes themselves, dumb and replaceable.

Why you want this: more agents than one machine carries; data residency per department or tenant (see [`09-enterprise-model.md`](09-enterprise-model.md)); hardware proximity (ARM builds, GPU, a runner inside the target system's network); and the clean separation of control plane and compute load that today exists only conceptually.

## What already carries

The most important property is already there: **the daemon dials out.** `coveyd` reads `COVEY_WS_URL` and `COVEY_DAEMON_TOKEN` from its environment and builds the WebSocket connection to the control plane; the control plane only waits for it. It **never** calls into a sandbox.

```
Sandbox (somewhere)  ──── WebSocket, outbound ────►  Control plane
                           COVEY_DAEMON_TOKEN
```

The direction reversal that makes remote execution practical in the first place is therefore already anchored in the daemon protocol ([`01-architecture.md`](01-architecture.md)) — a sandbox needs no inbound reachability, only a way out. The `SandboxProvider` port carries just as well: it has exactly one call site in the orchestrator. A provider that addresses a registered runner instead of the local Docker CLI changes nothing about the orchestrator.

What still sticks to the local host is correspondingly manageable:

| Local today | Why it breaks |
|---|---|
| `docker run` through the local CLI | The control plane has to sit on the Docker host |
| The home as the host path `<data>/homes/<agent-id>` | The file browser reads straight from the file system |
| The egress proxy as a container next to it | Reachable through `host.docker.internal` |
| A single provider without selection | No routing, no scheduling, no capacity |

## The model

```
                     ┌──────────────────────────────┐
                     │        Control plane         │
                     │  scheduler · registry · DB   │
                     └───────┬──────────────┬───────┘
        runner protocol      │              │      daemon protocol
        (long-lived, 1×/host)│              │      (per wake, 1×/sandbox)
                     ┌───────▼──────┐       │
                     │ covey-runner │       │
                     │  host A      │       │
                     │  ┌─────────┐ │       │
                     │  │ sandbox ├─┼───────┘
                     │  └─────────┘ │
                     │  homes/      │  ← working copy, disposable
                     └──────────────┘
```

**Two connections, two protocols.** The runner link is long-lived and exists per host; the daemon link arises per wake and per sandbox. That is deliberately not the same protocol: the runner has to be addressable even when **no** sandbox is running — for capacity reports, for file access to a sleeping home, for cleaning up orphaned containers. Binding the daemon protocol to a host instead of a sandbox would mix the two lifetimes.

The daemon's data path stays **untouched** by this: the sandbox still talks directly to the control plane, not through the runner. The runner starts and stops compute — it is not a proxy for the agent's work and does not see its events.

## Registration

As with GitLab, split into a **registration token** (org-wide, creatable and revocable in the UI) and a **runner token** derived from it (long-lived, per runner, stored only as a hash):

```
covey-runner register --url https://covey.example --token <registration-token> \
                      --tag php --tag arm64 --description "Build host Frankfurt"
```

`register` writes the received runner token into a **configuration file** (`/etc/covey-runner/config.toml`, overridable) — server address, token, tags, working directory. Deliberately a file and not just environment variables: the runner runs as a service on a machine that otherwise has nothing to do with Covey, and `register` has to be able to deposit its result somewhere.

After that `covey-runner run` holds a permanent WebSocket connection on `/api/runner/ws`, authenticated with the runner token. On connecting, the runner reports its **capabilities**:

- available sandbox images (see below),
- architecture (`amd64`/`arm64`), CPU/RAM, free disk space,
- the tags from registration,
- its own version, so that the control plane can make version drift visible,
- the **protocol version** it speaks (see "Delivery").

**One process, one instance.** A runner registers with exactly one Covey server. Whoever wants to serve two instances starts two processes — that is cheaper than the alternative: block storage shared across two organisations would be a channel between them, and distributing capacity between foreign tenants would be a policy nobody needs.

A runner without a connection is **offline**, not deleted — the distinction matters for operations (a maintenance window vs. decommissioning) but is inconsequential for the agents: they move to a different runner (see "The home"). A deleted runner takes only its local working copy with it, no platform state.

## Delivery

The runner is **installed individually**, on machines where nothing else of Covey lives. It is therefore a release artefact of its own with its own version number: a static binary per architecture, a Docker image, a systemd unit. An operator downloads it, calls `register` with a token and is done — they notice nothing of the rest of the project.

**The code nevertheless stays in the same repository** (`cmd/covey-runner/`), for a reason that has already proven itself here: `coveyd` is exactly the same case. It is a separate binary, runs on a different machine (in the sandbox), speaks a protocol to the control plane — and `internal/daemon` defines that protocol for **both** sides. That is exactly why a protocol change is a single commit instead of a compatibility dance across two repositories.

A separate repository modelled on `gitlab-runner` would remain possible once the protocol has settled. While it is in motion, the separation costs more than it brings — and for the operator it changes nothing anyway, because they see the artefact, not the repository.

### Protocol version

Because runner and server are delivered separately, different versions inevitably meet — someone updates Covey and forgets three runners. The handshake therefore names a protocol version, and the control plane decides:

- **matching** → normal assignment.
- **older, but supported** → the connection stands, marked as outdated in the runner view.
- **too old or newer than known** → the connection is refused, with a message saying *which* side needs updating.

Refusal is explicit with a reason, not silent: a runner that quietly fails to connect costs an evening of searching.

## Runner protocol

### Control plane → runner

| Message | Purpose |
|---|---|
| `start_sandbox` | Start a sandbox: agent ID, image, home identifier, env (`COVEY_WS_URL`, `COVEY_DAEMON_TOKEN`, egress token) |
| `stop_sandbox` | Shut compute down; the home stays |
| `sync_home` | Write the home into the store as a snapshot — regularly after the job, and enforceable besides (maintenance, decommissioning a runner) |
| `home_op` | File access to a home: list, read, write, delete (see file access) |
| `set_allowlist` | Update the egress allowlist for the runner's local proxy |
| `prune` | Clean up orphaned containers and the homes of deleted agents |

### Runner → control plane

| Message | Purpose |
|---|---|
| `registered` | Capabilities, tags, version — the first message after connecting |
| `sandbox_started` / `sandbox_failed` | The result of a `start_sandbox` (the `ready` proof still comes from the daemon itself) |
| `sandbox_exited` | The sandbox ended on its own (crash, OOM) — the control plane learns of it without waiting for the daemon timeout |
| `home_synced` | The snapshot is written: identifier, blocks transferred, total size — only afterwards may anything be cleaned up locally |
| `home_result` | The answer to a `home_op` |
| `capacity` | Running sandboxes, free space — the basis for scheduling and warnings |
| `heartbeat` | Sign of life |

`sandbox_exited` is the reason the runner has to observe the container state at all: today the control plane notices a crash only at the `ReadyTimeout` or at the breaking daemon link. With a runner that asks the local Docker daemon anyway, that becomes a reported fact instead of a guess.

## The home

Here the GitLab analogy appears to end: a CI runner may be chosen freely because it is **stateless** — a job clones anew, builds, throws everything away. Covey's sandbox, by contrast, has a persistent home, and that *is* part of the promise ("if a sandbox is lost, it is rebuilt from config + home", [`01-architecture.md`](01-architecture.md)).

The appearance deceives. A measured developer home (7.1 GB) consists almost entirely of things that **already have a source elsewhere**:

| Share | Size | Source of truth |
|---|---|---|
| `repos/` | 3.0 GB | the project's Git remote |
| `flutter`, `.pub-cache`, `.gradle`, `.npm`, `jdk`, `.dartServer` | 4.0 GB | derivable — pure cache |
| `wiki/`, `.claude/skills/` | a few MB | the control plane (already central) |
| **everything else** | **48 MB** | *nowhere* |

The table shows two things. First: **almost nothing in it is unique.** The 4 GB of toolchain caches are byte-for-byte the same on every developer home, and even checkouts overlap between agents on the same project. Second: the genuinely unique part is tiny at 48 MB — and it lies **scattered all over the home**. Alongside the 30 MB of session transcripts you find `useSevenAssistant.ts` (95 KB of extracted code), `panel.json`, `subagent-223.json`, `fix223/`, `verify-729/` — directly in the root directory, not in a folder provided for it. An agent puts its interim results, analyses and self-written helpers wherever seems sensible to it during the run. Its home is its workplace, not a form.

From this follows what does **not** work: a list. Neither a positive list ("save `work/` and `uploads/`") nor a negative one ("discard what the image profile knows as a cache") survives contact with an agent that creates itself a directory `analysis/` tomorrow. Every list is a rule that can be wrong, and its error costs work that has already been paid for.

### The central home store

**Decided: after every job the home is synced as a whole into a central store and materialised from there on wake.** No whitelisting, no negative list, no check whether a checkout is clean — the home goes in completely and comes out completely. The question "what is valuable?" is thereby never asked in the first place.

What makes this practical is the store's construction — **content-addressed and deduplicated block-wise**:

- Every file breaks into blocks, and the block hash is its key. The same content means **one** block, org-wide across all agents and all snapshots.
- After the job only the **new** blocks travel upwards. A typical run changes megabytes in a 7 GB home.
- On wake only the **missing** blocks come down; what the runner already has locally stays put.
- One snapshot per job. History and rollback ("the home from the day before yesterday") fall out as a by-product.

The effect on the measured figures is the actual point: the 4 GB of toolchain caches sit centrally **once**, not once per agent — deduplicated precisely because they are identical everywhere. And the 48 MB of unique work are backed up along with it, **without anyone having had to note anywhere that it exists**.

Three worries are thereby dealt with at once: the session transcripts travel along automatically (a parked agent keeps its `--resume` thread wherever it wakes up, [`12-claude-code-adapter.md`](12-claude-code-adapter.md)); unpublished changes in a checkout are no longer a special case; and self-written tools need no rule to protect them.

### A cache for the mass, the only copy for the rest

The store is called a "cache", and for 99 % of its content that is right: if it were lost, toolchains would be downloaded again and repos cloned again — annoying, not tragic. For the 48 MB it is **not** right. They exist nowhere else.

From this follows an operational rule that must not be skimmed over: **the home store requires backup like the database.** Treating it as a pure cache — deletable at will, no backup — would be exactly the data loss the whole construction is meant to prevent. It is a cache in its *function*, not in its *need for protection*.

### Exclusions are optimisation, not politics

Excluding individual paths from the sync stays sensible — pure analysis caches like `.dartServer` (317 MB) gain little from dedup and change constantly. The difference from the rejected list approach is the **role** of the list:

- Before, its completeness was a **prerequisite for correctness**. A forgotten path meant data loss.
- Now it is a **cost question**. The default is empty: without configuration everything is synced.

Exclusions therefore apply only to demonstrably derivable paths, and when in doubt it is synced. A wrongly set exclusion then still costs something — but someone has to have set it deliberately instead of forgetting it.

### What it costs

Worth naming honestly, because it is not free:

- **The first sync** of a grown developer home is a full pass — 7 GB. Deltas after that.
- **The sleep path gets longer.** The sync runs at real falling-asleep, not when a warm sandbox is parked — the warm session ([`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md)) stays untouched by it.
- **Storage for blocks.** Postgres is the wrong place for GB-sized binary data (see below).
- **Cleaning up.** Snapshots accumulate; without retention the store grows unbounded. Operable in the interface, not only through an environment variable — see "Interface".

### Where the blocks live

Another port following the pattern of `IdentityProvider` and `SecretStore` ([`10-architecture-stack.md`](10-architecture-stack.md), "batteries included, but swappable"): **`BlobStore`**, interface before implementation, two implementations.

| Backend | When | What it costs |
|---|---|---|
| `builtin` (default) | a directory next to `data/homes`, blocks as `blocks/<xx>/<hash>` | nothing — no extra service, no new operational part |
| `s3` | when durability, replication or separation from the control plane's disk is required | one more service to operate |

The default is deliberately the directory. For an installation on one machine — Covey's normal case — an object store is unnecessary operational surface, and the promise "one binary + Postgres" should not quietly become "one binary + Postgres + MinIO".

**If an object store, then S3-compatible — not "AWS S3".** The protocol is the common denominator of Hetzner Object Storage, Garage, MinIO, Ceph RadosGW and SeaweedFS; Covey does not prescribe a server but speaks the protocol. For the operator's choice: whatever the hoster offers anyway (on the Hetzner/Proxmox infrastructure named in [`10-architecture-stack.md`](10-architecture-stack.md) that means Hetzner Object Storage), otherwise **Garage** — lightweight and built for exactly such small, self-operated clusters. MinIO, if it is running in-house anyway.

**The client question stays open until stage 7** — deliberately, because it is smaller than it looks. A block store needs five operations: `PUT`, `GET`, `HEAD`, `DELETE` and the signing of short-lived URLs. Because blocks are small and immutable, the most laborious part of an S3 client falls away entirely: **multipart upload is never needed.**

Measured rather than estimated:

| Candidate | Cost | Note |
|---|---|---|
| `minio-go/v7` | **18 indirect modules, 41 compiled foreign packages** | Covey has 22 modules *in total* today. It brings `msgp`, `xxh3`, `crc64nvme`, `md5-simd`, `compress` for replication, ILM, notifications, S3 Select — none of which will ever be used |
| `aws-sdk-go-v2` | heavier still | Official and thorough, but more awkward against foreign endpoints |
| a minimal client of our own | no dependency | SigV4 over `crypto/hmac` + `crypto/sha256`; the presign variant is the simpler one (query-string auth) |

Building it ourselves is more defensible here than usual, because its **failure mode is loud and closed**: a wrong signature yields a `403`, immediately visible — not a silently weakened security property. That is the difference between "use crypto primitives" and "invent a crypto protocol": SigV4 is a signature recipe over HMAC-SHA256, not a handshake.

Against it stands what a matured library brings: the individual providers' idiosyncrasies (path style vs. virtual host, region determination, error parsing). Whoever serves only one provider notices none of it; whoever serves five notices it at every one.

This will be decided when stage 7 comes up. Until then all that counts is that `BlobStore` is a port and that `builtin` needs **no** dependency at all.

### How the blocks reach the runner

A runner **never gets the store's credentials** — that would be the same omission as the database URL in the egress proxy (see "Trust boundary"). Instead the control plane issues **short-lived, scoped URLs** per transfer, exactly following the secrets broker's pattern ([`04-identity-secrets.md`](04-identity-secrets.md)):

- With the `s3` backend as **pre-signed URLs** — the runner loads directly from the object store, the control plane never sees the bytes.
- With the `builtin` backend the control plane delivers equivalent short-lived URLs onto itself.

Both also prevent 7 GB from having to go through the runner WebSocket: the control channel stays narrow, the payload goes past it.

> **Why not Git as the transport?** An obvious thought, but Git only fits the curated text part. `repos/` are Git checkouts themselves (nested repos plus duplication of an existing remote), caches and SDKs have no business in a version history, and the transcripts are append-only JSONL on which Git bloats without a diff ever being read. What makes Git attractive here — dedup and history — the content-addressed store delivers anyway, and without those drawbacks. Underlaying the wiki *with* a Git history remains an independently attractive idea but is not decided along with this.

### Affinity as a preference, not a binding

If the home is restorable from the store, the binding to a runner turns from a *prerequisite* into an *optimisation*. The difference shows up at failure time:

- **Preference:** the scheduler prefers the runner the agent last ran on — its blocks already sit there locally, and materialising is almost free.
- **On a different runner** it fetches only the blocks that are missing there. That is considerably less than a cold start in the original sense: nothing is cloned anew or pulled from the internet, it comes from our own store — and everything other agents have already deposited there is already present.
- The worst case remains a **fresh** runner without a single block. Then it is a full transfer, and a Flutter agent needs minutes before it reads its first line. But that is **time, not data loss** — and it hits the first agent on a new machine, not every switch.

The home on a runner is thereby a **genuine working copy without special rules**: the runner may clear it away entirely when short of space (`prune`) as soon as the sync is through — there is nothing in it that is not in the store. The only hard rule is: **no `prune` before a successful sync.** A newly set-up runner needs no data migration.

The preference is **sticky, but without automatic return**: if a runner fails, its agents move to another one and *stay there*. When the old one comes back, nothing is migrated back — otherwise the failure wave would be followed by a second wave of cold starts. The balancing happens by itself at the next cold start that was due anyway.

## Warmup: the local block storage

Warmup needs **no mechanism of its own** — it falls out of the store as soon as the runner keeps the fetched blocks locally instead of throwing them away. A block belongs to no agent: it is determined by its hash, and whoever needs the same content gets the same block.

That hits exactly the waste that is unavoidable today. A sandbox currently has **a single mount**, its own home — every cache is private, even though not a byte of it is agent-specific:

| | Size in the measured home | agent-specific? |
|---|---|---|
| `.pub-cache` | 1.0 GB | no — the same packages |
| `.gradle` | 951 MB | no |
| `flutter` (SDK) | 1.0 GB | no — the version sits in the project |
| `.npm` | 402 MB | no |
| `jdk` | 346 MB | no |

Two developer agents on the same host today hold the same 4 GB twice; five hold 20 GB. With block-wise deduplication they sit **once** in the runner's local storage — and the second agent materialises its home from it without fetching a single byte over the wire. A *new* agent on a run-in runner therefore starts almost warm, even though it has never run there.

### Why that dissolves the isolation question

A shared cache is otherwise a **channel between agents** and therefore an isolation question ([`06-observability-control.md`](06-observability-control.md)) — whoever may write into a shared package cache can slip something to others. With content addressing the problem falls away **by construction**:

- A block is requested by the hash of **its content**. If the local storage delivers it, the content is by definition the same one the store would deliver.
- Which blocks make up an agent's home is written in **its own** snapshot. No other agent can influence that mapping.

There is therefore no promotion rule, no hash checklist and no read-only substrate someone would have to populate. Sharing is safe because there is no possibility of depositing something else under a foreign hash in the first place.

### What that means for a failure

If a runner fails, **all** its agents move to another one at once. Without shared blocks each of them would pull gigabytes for itself there, simultaneously. With them, the fallback runner fetches **every block exactly once**, no matter how many agents need it — and everything other agents have already deposited there is already present.

The cold start is therefore no longer a question of switching agents but only of the **fresh host**: it hits the first agent on a new machine and nobody after that.

## Scheduling

Deliberately simple, because no runner has to be "the right one" — only the cheapest:

1. Candidates: all **connected** runners whose tags satisfy the agent's `runner_tags` **and** that hold its image.
2. Of those, preferably the one the agent last ran on (`last_runner_id`) — its working copy is warm there.
3. Otherwise the one with the fewest running sandboxes. No bin packing, no resource modelling.
4. None suitable → the task stays put, with an explanatory state instead of an error message about a failed container start.

`last_runner_id` is explicitly a **hint, not a promise**: the scheduler may override it at any time, and nothing in the system may assume a home is still there. An agent whose preferred runner is missing wakes up on a different one — slower, but without a human doing anything.

## Sandbox images per agent

The runner makes a gap visible that hurts even without it: the sandbox image is instance-wide today (`COVEY_SANDBOX_IMAGE`). Every agent gets the same one — the mail agent carries the developer agent's JVM along.

The image therefore belongs **on the agent** (D11 in [`07-open-decisions.md`](07-open-decisions.md)), as a profile:

| Profile | Contents | For whom |
|---|---|---|
| `base` | coveyd, Node, git, chromium, ripgrep | support, mail, QA, research |
| `dev` | + PHP, JDK, `fvm`, `uv` | developer agents |
| org-owned | anything | special cases, tighter sandboxes |

The cut deliberately does **not** go along individual languages. A profile is a *union*: a developer agent legitimately works on a PHP and a Flutter project, and one image per language would bring back exactly the question that "version → home, toolchain → image" has already answered — which image do I start on wake, when it is not yet settled which ticket is coming?

For runners the image is at the same time a **capability**: a runner reports which images it holds and gets only matching agents. The price is known and bearable — the warm pool fragments per image.

## Trust boundary

With `start_sandbox` a runner receives an agent's `COVEY_DAEMON_TOKEN` and egress token in order to inject them into the container. It **can** therefore impersonate every agent it hosts. That is the same trust level as a CI runner that sees job tokens — but it has to be said out loud:

> **Runners are trusted infrastructure of the organisation, not foreign boxes.** A runner is not a way of bringing in untrusted compute capacity.

From this follow hard rules:

- **No database access and no store credentials.** A runner speaks exclusively the runner protocol; it fetches and writes blocks through short-lived, scoped URLs (see "How the blocks reach the runner"). Concretely this concerns the egress proxy today: in hard isolation mode it receives `COVEY_DATABASE_URL` and reads its allowlist from Postgres itself. On a remote runner that would mean distributing the Postgres credentials to every host — that tips the trust boundary over and is a **prerequisite** for everything else: the allowlist has to come through the authenticated control-plane API (see below).
- **No long-lived target-system secret.** The runner sees daemon and egress tokens, never the brokered credentials — those still go directly to the daemon ([`04-identity-secrets.md`](04-identity-secrets.md)).
- **TLS is mandatory.** The control plane has to be reachable from foreign hosts; `COVEY_PUBLIC_URL` over plaintext HTTP would be the disclosure of every daemon token.
- **Revocation and audit per runner.** A runner token is individually revocable; registration, connection, every sandbox assignment and every `home_op` are auditable events ([`06-observability-control.md`](06-observability-control.md)).

## Egress with distributed runners

Hard isolation mode (`network`) requires a proxy **in the sandbox's network segment** — that is, per runner, started and maintained by the runner. Two changes compared with today:

1. **The allowlist through the API instead of through the DB.** The proxy asks the control plane (authenticated with the runner token) and caches; the control plane pushes changes via `set_allowlist`. That is the prerequisite named above and **the right construction even without runners** — the proxy is an enforcement point, not a database client.
2. **The control plane is a genuine foreign host.** `host.docker.internal` becomes the public address; it has to be on the proxy's internal allowlist so that the daemon link gets through hard mode. The mechanism for that exists (`COVEY_EGRESS_ALLOW`), only the value is no longer automatically right.

## File access

`FileAccess` today delivers a **host path** that the file browser reads directly. With remote runners that becomes `home_op` over the runner link.

The reasoning why file access deliberately does **not** go through the daemon protocol is preserved in the process — it even becomes cleaner: the home has to be readable even when the sandbox is asleep, and asleep is the normal state. The runner link exists continuously, the daemon link only during a run. The runner is therefore exactly the right seam for this requirement; the daemon would have been the wrong one.

## Interface

A store that grows quietly in the background and whose content nobody can see is an operational risk — you notice it only when the disk is full. Both therefore belong in the UI and not only in environment variables.

### An agent's home

On the agent page, next to the existing file browser:

| Figure | Why it belongs there |
|---|---|
| The home's size, of which **occupied after dedup** | The difference is the actual statement: a 7.1 GB home, but perhaps 200 MB that only this agent holds |
| Last sync: time, duration, blocks transferred | Whether the sync runs at all, and whether it is expensive |
| Number of snapshots and their time span | What the retention currently leaves |
| The current or last-used runner | Where the working copy sits warm |
| The largest directories | Answers "why is this home so big?" without shell access — and reveals candidates for an exclusion |

Plus the snapshot list with two actions: **restore** (an earlier state becomes the current one — the rollback capability that falls out of the construction anyway) and **back up now** (force a sync, e.g. before maintenance).

Restoring is a modifying action on someone else's work and therefore needs the same treatment as other interventions: only with the appropriate role, with confirmation, and as an audit event ([`06-observability-control.md`](06-observability-control.md)). It is furthermore only permitted while the agent is **not** running — otherwise the running sandbox writes into a home that changes underneath it.

### Retention

An org-wide setting, with a button rather than only a variable:

- **Keep snapshots per agent:** the last *N* (default 10).
- **Maximum age:** remove snapshots older than *X* days (default 30).
- **Always keep:** every agent's most recent snapshot — even if both rules would catch it. A retention that takes an agent's last home away is a delete command by a detour.
- **Clean up now** as an explicit button, with a preview: what would fall away, how much space would be freed.

During cleanup a block is only removed when **no** remaining snapshot references it any more — that is the price of deduplication and the reason why "delete this snapshot" does not free space linearly. The preview therefore names the space actually freed, not the sum of the snapshot sizes; anything else would be a number that is never right.

The store's fill level additionally belongs on the dashboard: total size, growth, and a warning before the disk runs short — not after.

## Not in the first pass

Deliberately left out so that the first pass stays small:

- **Autoscaling** of runners (cloud API, spot instances).
- **Anticipatory warming** — mirroring blocks onto runners an agent has never run on as a precaution. The local block cache grows out of actual use; *anticipating* it is a later optimisation.
- **Migration back** after a runner failure (see "The home": the preference stays sticky).
- **Several simultaneous sandboxes per agent** — "serial before parallel" still applies.
- **The runner as a tenant boundary.** One runner per tenant is an obvious thought, but the isolation promises for it belong in [`09-enterprise-model.md`](09-enterprise-model.md) and are a decision of their own.
- **Non-Docker runners** (Firecracker, Kubernetes pods). The runner protocol is cut so that they fit behind it later — the Docker runner is built first.

## Build order

Every stage is useful in itself and can be accepted individually:

| Stage | Contents | Value on its own |
|---|---|---|
| 0 | Detach the egress proxy from the DB (allowlist through the API) | The right construction for the enforcement point, independently of runners |
| 1 | An image per agent (`sandbox_image`), profiles `base`/`dev` | The mail agent no longer carries a JVM |
| 2 | The home store: content-addressed block storage, sync after the job, materialising on wake, a local block cache | A lost home costs time instead of work, and the second agent on the same host starts warm — **both already with a single host** |
| 3 | `covey-runner` as a third binary: registration including a configuration file, the protocol handshake, `start_sandbox`/`stop_sandbox`, `RunnerPool` as a `SandboxProvider`; plus the release artefacts (a binary per architecture, a Docker image, a systemd unit) | Sandboxes run on a second host |
| 4 | `home_op` — the file browser over the runner link | The file browser works remotely too |
| 5 | Tags, capacity, a runner view in the UI | Operability from more than two runners onwards |
| 6 | Interface: home info, snapshot list, retention setting and button, fill level on the dashboard | The store is visible and operable instead of growing quietly |
| 7 | The `BlobStore` backend `s3` (the port exists from stage 2, `builtin` suffices up to here) | Durability and replication when the control plane's disk is not enough |

Stages 0 to 2 are independent of the runner and should run first — each is an improvement on the current state in its own right. Stage 0 because it otherwise becomes a security hole as soon as a runner runs remotely. Stage 1 because the image capability would otherwise have to be retrofitted in stage 3.

**Stage 2 carries the whole edifice.** It makes the home replaceable, and only then is a runner switch not data loss; without it the affinity would have to be a binding after all. It delivers the warmup along with it — the block cache *is* the shared holding. And it pays off before a single second host exists: today two developer agents on the same machine hold the same 4 GB twice, and a deleted home is unrecoverable.
