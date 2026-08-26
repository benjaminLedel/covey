# 16 — Runner (the distributed data plane)

Today the data plane runs on **the control plane's host**: the `docker` provider calls the local Docker CLI, the persistent home sits as a directory next to it, the egress proxy is a container on the same machine. That carries one instance and a handful of agents, but it makes the size of the workforce a question of the size of *one* machine.

The **runner** solves this: a standalone process on an arbitrary host that **registers** with the control plane and gets sandboxes assigned to it from there — the same pattern as GitLab runners. The control plane remains the single point of truth; the runners are, like the sandboxes themselves, dumb and replaceable.

Why you want this: more agents than one machine carries; data residency per department or tenant (see [`09-enterprise-model.md`](09-enterprise-model.md)); hardware proximity (ARM builds, GPU, a runner inside the target system's network); and the clean separation of control plane and compute load that today exists only conceptually.

But the runner is **not an add-on that has to be set up**. The control plane starts every sandbox through a runner, and in the normal case through one it runs itself — see "The built-in runner". Whoever installs Covey on one machine notices none of this and installs nothing extra. Whoever registers a runner has thereby said that compute leaves this machine, and the built-in one gives way to it.

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
                     │        covey serve           │
                     │  scheduler · registry · DB   │
   runner protocol,  │  ┌────────────────────────┐  │
   in process ───────┼─►│ built-in runner        │  │
                     │  │  1× per organisation   │  │
                     │  │ sandbox · homes/ blocks│  │
                     │  └────────────────────────┘  │
                     └───────▲──────────────▲───────┘
    runner protocol          │              │  daemon protocol
    (long-lived, 1×/runner)  │              │  (per wake, 1×/sandbox)
                     ┌───────┴──────┐       │
                     │ covey-runner │       │
                     │  host A      │       │
                     │  ┌─────────┐ │       │
                     │  │ sandbox ├─┼───────┘
                     │  └─────────┘ │
                     │ homes/blocks │  ← working copy, disposable
                     └──────────────┘
```

**Two connections, two protocols.** The runner link is long-lived and exists per runner; the daemon link arises per wake and per sandbox. That is deliberately not the same protocol: the runner has to be addressable even when **no** sandbox is running — for capacity reports, for file access to a sleeping home, for cleaning up orphaned containers. Binding the daemon protocol to a host instead of a sandbox would mix the two lifetimes.

The daemon's data path stays **untouched** by this: the sandbox still talks directly to the control plane, not through the runner. The runner starts and stops compute — it is not a proxy for the agent's work and does not see its events. That holds for the built-in runner too: a sandbox it starts dials the control plane like any other, not its parent process.

## Out of service: pause

A runner can be **paused**. It keeps its registration, its token, its tags and
its working copies, and gets no new sandboxes; what is running on it finishes.
Resuming needs no restart — the pause lives on the row and is carried to the
connection, so it applies to the next wake and survives a reconnect of either
side.

It exists because the alternative was a rule that guessed. The built-in runner
used to stand down as soon as an organisation had a registered one: an intention
inferred from a fact, and the inference cost a production instance its data
plane twice in one afternoon and its night's work once. A pause says the same
thing without inferring anything — it is set by a person, it is visible, and it
is taken back in the place it was set. The built-in runner is therefore an
ordinary runner in this respect too: whoever wants no compute on the control
plane's machine pauses it (or says so in the configuration, which is what
`COVEY_BUILTIN_RUNNER=off` is for).

Paused is named separately from every other reason a host is unavailable — it is
the only one somebody *chose*, and the answer to "why is nothing running" has to
be that choice rather than a guess about the network.

## The built-in runner

A distributed data plane that has to be assembled before anything runs at all would be the wrong trade for an installation on one machine — which is Covey's normal case. Hence: **the runner protocol is the only way a sandbox starts, and by default the control plane speaks it with a runner of its own**, running inside the `covey serve` process.

For whoever operates it, that means the setup does not change at all. No token, no registration, no configuration file, no second artefact. The built-in runner appears in the interface as *this host* — visible, so that the model is comprehensible, but it is not administered: it has no token to revoke and no delete button. It exists, or the rule below says it does not. `COVEY_RUNNER_LOCAL=off` switches it off from the outset, for a control plane that is meant to feed foreign runners only.

For the code it means something more important: **the default path and the remote path are the same code.** What differs is a transport — a channel pair in one case, a WebSocket in the other:

```go
type Transport interface {
    Send(context.Context, Message) error
    Receive(context.Context) (Message, error)
}
```

That is the same seam `internal/daemon` already draws between the control plane and the sandbox, and it is drawn here for the same reason. A built-in runner that took a shortcut past the protocol — calling the Docker CLI directly because it happens to be in the same process — would defeat the whole point: the remote path would then be exercised only by whoever actually operates two hosts, and would rot everywhere else. Every message in the table below is handled by one implementation, and the normal installation runs it every day.

Two things stay genuinely cheaper in process, and both are transport details rather than exceptions: blocks are read and written through the `BlobStore` directly instead of over a signed URL, and file access reads the home from the local file system instead of through `home_op`.

### It runs beside the others, and a pause is what switches it off

**The built-in runner is an ordinary runner.** It exists per organisation, it is a candidate like any other host, and whoever does not want it pauses it.

That is the second answer to this question. The first one was: *an organisation has a built-in runner exactly as long as it has no registered one* — registering the first host ended it, deleting the last one brought it back. The reasoning was the mixed pool: some agents on the registered hosts, some on the control plane's machine, the assignment decided by a scheduling preference nobody remembers making. Whoever adds a runner is saying that compute leaves this machine.

The reasoning was right and the rule was wrong, because it inferred that sentence from an event instead of hearing it from a person. What it actually produced, three times on a production instance:

1. The registered host claimed `covey-sandbox:latest` while the agents needed the deploy image. Candidates on paper, none in fact — and nothing fell back, because falling back would have restored the mixed pool through the back door. Half an hour of `no runner holds the image` every 30 seconds, next to an idle machine that held it.
2. After the fallback was added, the same rule sat a second time in the place that brings the built-in runner up, and refused there.
3. A night later the registered host was *connected* and answered nothing — its read loop was stuck inside an image pull. Now there WAS a candidate, so the fallback did not fire; every wake went to it and waited out its start timeout, an hour at a time.

A pause says what the rule was guessing, and says it where somebody can read and revoke it: it is set on a runner, it is visible in the runner view, it survives restarts on both sides, and it excludes that host from scheduling with a message that names the decision. The mixed pool is thereby a choice one makes rather than a state one falls into — and the case where the pool has candidates on paper and none in fact no longer ends in an organisation that cannot work.

**The built-in runner is the last candidate.** That is what is left of the old rule, and it is the half that was right: whoever adds a machine wants the compute *there*, and a pool where half the agents quietly run on the control plane is precisely the surprise the rule was trying to prevent. As long as a registered host can carry the work it carries it; the built-in one steps in when none can — a fallback the log line says out loud (`no connected runner fits — the built-in one takes it`), and tags still exclude it like any other host.

**"Offline is not no runner" therefore no longer holds**, and the change is worth naming: a maintenance window on the only registered host used to mean the agents waited, and now it means the built-in runner carries them. That is the same fallback that keeps an organisation working when its host is wedged — one cannot have it in the emergency and not in the maintenance window, because the platform cannot tell the two apart. What it can offer is the decision: pause the built-in runner for the window too, and the agents wait exactly as before.

Handing work over **drains, it does not cut**: a paused runner takes no new sandboxes, lets the running ones finish, and syncs every home it holds into the store. Agents then wake elsewhere and materialise from the store — a full transfer for the first one, which is time and not data loss ("Affinity as a preference").

That the sync has to work before this can happen is the reason the home store comes **before** the remote runner in the build order: at the point where a runner can be registered at all, the store exists. Without it, pausing the built-in runner would mean handing over a home that only exists on this host.

## Registration

Registration concerns the *added* runner. The built-in one is created by the platform itself — one per organisation, at bootstrap and whenever an organisation is added — needs neither token nor configuration file, and stands beside the registered runners of its organisation (see "It runs beside the others, and a pause is what switches it off").

For everything else, as with GitLab, split into a **registration token** (org-wide, creatable and revocable in the UI) and a **runner token** derived from it (long-lived, per runner, stored only as a hash):

```
covey-runner register --url https://covey.example --token <registration-token> \
                      --tag php --tag arm64 --description "Build host Frankfurt"
```

`register` then opens the connection once and lets it go again. Registering is an HTTP call and running is a WebSocket, and the two fail differently: a reverse proxy that does not upgrade, a TLS chain this host does not trust, a firewall that lets 443 through but not the upgrade. Finding that out while somebody is standing at that shell is worth the two seconds; finding it out later means reading a log on a machine nobody logged into again.

`register` writes the received runner token into a **configuration file** (`/etc/covey-runner/config.toml`, overridable) — server address, token, tags, working directory. Deliberately a file and not just environment variables: the runner runs as a service on a machine that otherwise has nothing to do with Covey, and `register` has to be able to deposit its result somewhere.

After that `covey-runner run` holds a permanent WebSocket connection on `/api/runner/ws`, authenticated with the runner token. On connecting, the runner reports its **capabilities**:

- available sandbox images (see below),
- architecture (`amd64`/`arm64`), CPU/RAM, free disk space,
- the tags from registration,
- its own version, so that the control plane can make version drift visible,
- the **protocol version** it speaks (see "Delivery").

### One runner, one organisation

A runner registers with exactly one Covey server **and belongs there to exactly one organisation**. It inherits that organisation from the registration token and cannot change it. Whoever wants to serve two instances or two organisations starts two processes.

The reason is the one this document already gives for the block store: shared block storage across two organisations would be a channel between them, and distributing capacity between foreign tenants would be a policy nobody needs. The same argument that makes an instance boundary out of it makes an organisation boundary out of it — a runner holds homes and daemon tokens, and both are the property of exactly one tenant. `runtimes` carries the identical reasoning for the identical reason (`org_id NOT NULL`, migration 0048): whatever holds the material of an organisation is bound to it in the schema, not by a query someone has to remember to write.

**Also for the built-in runner.** It is not the exception to this, it is one runner per organisation, each with its own working directory and its own local block storage:

```
<data>/runners/<org-id>/homes/    working copies
<data>/runners/<org-id>/blocks/   local block storage
```

The price is worth naming: with several organisations on one machine the toolchain caches sit once per organisation rather than once in total. That is exactly what the boundary costs, and for the normal installation with one organisation it costs nothing.

Deleting an organisation therefore takes its runner rows with it — and `prune` has to clear away the working directory and blocks belonging to it. Otherwise what stays behind is precisely the part that exists nowhere else.

A runner without a connection is **offline**, not deleted — the distinction matters for operations (a maintenance window vs. decommissioning) but is inconsequential for the agents: they move to a different runner (see "The home"). A deleted runner takes only its local working copy with it, no platform state.

## Delivery

The runner is **installed individually**, on machines where nothing else of Covey lives. It is therefore a release artefact of its own with its own version number: a static binary per architecture, a Docker image, a systemd unit. An operator downloads it, calls `register` with a token and is done — they notice nothing of the rest of the project.

**A binary of its own, not a subcommand of `covey`.** The tempting simplification would be to spare the extra artefact and let the same binary serve as the runner. It is the wrong one: on a runner host `serve`, `migrate` and `bootstrap` should not exist at all, and the trust boundary below ("no database access") reads badly when the database code is compiled in beside it. The easy path is bought by the built-in runner, which requires no artefact whatsoever — not by merging two that have different jobs.

**The code nevertheless stays in the same repository** (`cmd/covey-runner/`), for a reason that has already proven itself here: `coveyd` is exactly the same case. It is a separate binary, runs on a different machine (in the sandbox), speaks a protocol to the control plane — and `internal/daemon` defines that protocol for **both** sides. That is exactly why a protocol change is a single commit instead of a compatibility dance across two repositories.

A separate repository modelled on `gitlab-runner` would remain possible once the protocol has settled. While it is in motion, the separation costs more than it brings — and for the operator it changes nothing anyway, because they see the artefact, not the repository.

### Updating a runner

Version drift is not a fault to be avoided — separate delivery is the point, and nobody should have to touch ten machines to upgrade one server. What was missing was the remedy in the place where the drift is named: the runner view marks a host as *outdated*, and the answer to that used to be an SSH session per host, which means the hosts nobody logs into keep their bugs.

`update` therefore tells a runner to replace its own binary. It does what `installer/install.sh` does, deliberately and to the letter — fetch the release's checksums, fetch the archive for this platform, compare, unpack, replace, start again — because that is the path every installation exercises anyway. The checksum is not decoration: this downloads a program and then runs it, and "over HTTPS" is a statement about the transport, not about the file.

Three rules make it something one can press:

- **Not while it is carrying anything.** The containers would survive the restart — they belong to Docker — but the watchers would not, and a sandbox nobody watches any more is worse than an update that waits. The host refuses and names the number.
- **The answer comes before the restart.** Afterwards there is nothing left to ask: without it, a successful update and a host that fell over look exactly alike.
- **The built-in runner is not updated this way.** It is the control plane's own process, and it is updated by updating the control plane.
- **A runner that predates the feature is told, not waited for.** It would ignore the message, and the caller would wait out the whole timeout for an answer that is never coming — ending in "does not answer", which is this platform's sentence for a broken host. So `registered` carries a `features` list: a new optional field rather than a new protocol version, because the handshake demands an exact version match and raising it would disconnect precisely the population that needs to be told something.

Which version: the server's own when that is a released one, otherwise the newest published release — an instance built from `main` is ahead of every release, and prescribing its own version would send the host looking for an artefact that does not exist. `COVEY_RUNNER_DOWNLOAD_BASE` points the download somewhere else for an installation that does not reach GitHub, or one that publishes builds of its own.

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
| `update` | Replace your own binary and start again — how version drift is closed without an SSH session per host |

### Runner → control plane

| Message | Purpose |
|---|---|
| `registered` | Capabilities, tags, version — the first message after connecting |
| `sandbox_started` / `sandbox_failed` | The result of a `start_sandbox` (the `ready` proof still comes from the daemon itself) |
| `sandbox_exited` | The sandbox ended on its own (crash, OOM) — the control plane learns of it without waiting for the daemon timeout |
| `home_synced` | The snapshot is written: identifier, blocks transferred, total size — only afterwards may anything be cleaned up locally |
| `home_result` | The answer to a `home_op` |
| `capacity` | Running sandboxes, free space — the basis for scheduling and warnings |
| `update_result` | What became of an `update`: the versions before and after, or why nothing was replaced |
| `heartbeat` | Sign of life |

The heartbeat is not decoration. A TCP connection can be dead without either side noticing — a NAT that dropped the entry, a network partition, a host that went to sleep — and a runner in that state stays in the pool as *connected*. Every wake would then be assigned to a runner that hears nothing and would sit out its start timeout before failing, instead of going to one that works. So: a sign of life every 30 seconds, and after three missed ones the control plane closes the connection itself. Any message counts, not only a heartbeat; traffic is proof of life, and a runner busy answering need not say so twice.

It is also what makes "last seen" mean anything. Without it the figure would be the moment a runner **connected** — which is the one thing nobody wants to know about a runner that has since gone away.

**And it is not enough on its own.** The heartbeat goes out from a goroutine of its own, so it keeps beating while the runner's read loop is stuck — inside a `docker run` that is fetching a workplace image, for instance, for which the bound is an hour. Such a host reports itself as connected and answers nothing, and a scheduler that only knows "connected" sends it every wake and waits each one out. Measured on covey.work: three wakes, three hours, nothing done, the built-in runner idle throughout, because stepping in requires there to be *no* candidate.

So the second signal, and it costs no extra message: the control plane asks every connected host for its capacity once a beat anyway (see [Interface](#interface)). Whether an answer comes back is a statement about the **read loop** rather than about the socket. Three missed answers — the same tolerance the heartbeat gets — and the host stops being a candidate and is shown as *not answering* rather than as *connected*. It is not thrown out: what it is doing may be legitimate and long, and a start that is running has to be allowed to finish. It gets no new work while it cannot say a word.

And the same signal is listened to **while a start is outstanding**. "Answering" is a statement about the last 90 seconds, a start may take an hour because it may be a multi-gigabyte pull, and a host can go deaf in the second after it was picked — measured: a runner answered 19 seconds before the wake, took the start, went into its pull and left the agent standing for the full hour without a single message. So a start whose host stops answering is given up early and the next candidate asked. The abandoned start is **taken back** with a `stop_sandbox`: the message lies in front of the stuck loop rather than being lost, and when the pull finishes, the host would otherwise start a container for an agent that woke somewhere else long ago. Per-agent ordering is what makes that work — the stop is handled after the start it cancels.

A host that is genuinely reading stays unaffected however long its pull takes, because capacity is answered beside the loop. Not answering while a start is outstanding therefore means the loop is stuck.

The runner's side of the same lesson: **the read loop hands off**. `start_sandbox`, `stop_sandbox` and `sync_home` are worked on beside it, in the order they arrived per agent (start, stop and sync of one agent describe one working copy, so their order is part of their meaning; a mutex would not do it, since it hands the turn to whoever happens to be waiting). `capacity` and `check` answer beside it too. That way one agent's image pull costs that agent time, not the host.

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

What makes this practical is the store's construction — **content-addressed and deduplicated** (at what granularity: see below):

- Content is addressed by its hash. The same content means **one** block, across all agents and all snapshots of **one organisation** — the block namespace is keyed by `(org_id, hash)` and the boundary is structural, not a filter someone has to remember. A namespace shared across tenants would be an existence oracle over hashes: whoever may ask whether a block is already there learns whether somebody else holds that exact content.
- After the job only the **new** blocks travel upwards. A typical run changes megabytes in a 7 GB home.
- On wake only the **missing** blocks come down; what the runner already has locally stays put.
- **One snapshot per agent**, replaced by every sync. The store answers "where is this home now", not "where was it on Thursday". A history would fall out of the construction for free, and is deliberately not kept: the purpose is that a lost runner costs time instead of work, and for that exactly one state is needed. What a kept history would additionally buy — winding a home back after an agent wrecked it — is covered by the backup on the host, and paying for it with a second concept in the product is not worth it.

The effect on the measured figures is the actual point: the 4 GB of toolchain caches sit centrally **once**, not once per agent — deduplicated precisely because they are identical everywhere. And the 48 MB of unique work are backed up along with it, **without anyone having had to note anywhere that it exists**.

Three worries are thereby dealt with at once: the session transcripts travel along automatically (a parked agent keeps its `--resume` thread wherever it wakes up, [`12-claude-code-adapter.md`](12-claude-code-adapter.md)); unpublished changes in a checkout are no longer a special case; and self-written tools need no rule to protect them.

### Granularity: whole files, chunks only for the large ones

"Block-wise" leaves open how big a block is, and the answer decides how much machinery this needs. A home consists overwhelmingly of **many small files** — package caches, `node_modules`, SDK trees. For those, the hash of the whole file collects practically the entire dedup benefit: they are identical or they are new, and a file that changes at all is usually rewritten wholesale by the tool that owns it.

So: **whole-file addressing up to a threshold (8 MB), fixed-size blocks (4 MB) above it.** The chunking then applies to exactly the cases where it pays — archives, SDK tarballs, and above all the append-only JSONL transcripts. Whoever chunks everything pays for it over gigabytes of small files and receives, for that, a few per cent.

Fixed-size and not content-defined, which is the unusual half of this: content-defined chunking exists to survive an **insertion in the middle**, where every following byte shifts and a fixed grid would re-cut everything after it. What large files in a home actually do is **append** — a transcript grows at the end, and every preceding block stays byte-identical because no offset moves. For that case a rolling hash buys nothing and costs a pass over every byte. If a home ever produces large files that are edited in the middle, this is the decision to revisit; the manifest format does not have to change for it, only how the block boundaries are found.

### Materialising: reflink, otherwise a copy

Handing a file from the local block storage into the working copy must not be a **hard link**: it shares the inode, and the first agent that writes into the file corrupts the store for everyone who references that block. A store whose content changes behind its own hash is worse than no store.

The right instrument is a copy-on-write clone (`clonefile` on APFS, `FICLONE` on btrfs and XFS) with a plain copy as the fallback. On a file system without reflink support the local block storage therefore costs real disk space a second time — which is why keeping blocks is a **setting with an LRU cap**, not an assumption. Two things bound the cost: only missing or changed files are ever materialised, so the full pass hits a fresh runner and nothing else, and a working copy that already matches the target snapshot is left completely untouched.

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
- **Space next to the working copy.** The store holds what the runner also has on disk. With a *single* agent that is close to a doubling; from the second developer agent onwards the dedup of the 4 GB of toolchain caches turns it into a saving. `COVEY_HOME_STORE=off` exists for the smallest installations — then homes stay as they are today: directories, without snapshots, and unrecoverable when lost.
- **Cleaning up.** Keeping one snapshot does not make this smaller, it makes it constant: every sync replaces the previous manifest, and the blocks only that one still referenced become garbage on the spot. Without a sweep the store therefore grows with every single job — not with the history somebody chose to keep. It has to run by itself; a cleanup that depends on somebody pressing a button is one that happens on the installations that need it least — see "Cleaning up" under "Interface".

### Where the blocks live

Another port following the pattern of `IdentityProvider` and `SecretStore` ([`10-architecture-stack.md`](10-architecture-stack.md), "batteries included, but swappable"): **`BlobStore`**, interface before implementation, two implementations.

| Backend | When | What it costs |
|---|---|---|
| `builtin` (default) | a directory next to `data/homes`, blocks as `blocks/<org-id>/<xx>/<hash>` | nothing — no extra service, no new operational part |
| `s3` | when durability, replication or separation from the control plane's disk is required | one more service to operate |

The organisation is part of the key in both backends — the first path element with `builtin`, the object prefix with `s3`. Garbage collection walks per organisation for the same reason: a block is released when no remaining snapshot of **this** organisation references it any more.

The default is deliberately the directory. For an installation on one machine — Covey's normal case — an object store is unnecessary operational surface, and the promise "one binary + Postgres" should not quietly become "one binary + Postgres + MinIO".

**If an object store, then S3-compatible — not "AWS S3".** The protocol is the common denominator of Hetzner Object Storage, Garage, MinIO, Ceph RadosGW and SeaweedFS; Covey does not prescribe a server but speaks the protocol. For the operator's choice: whatever the hoster offers anyway (on the Hetzner/Proxmox infrastructure named in [`10-architecture-stack.md`](10-architecture-stack.md) that means Hetzner Object Storage), otherwise **Garage** — lightweight and built for exactly such small, self-operated clusters. MinIO, if it is running in-house anyway.

**Decided in stage 8: a minimal client of our own** — the row below that costs no dependency. The reasoning it was measured against held: a block store needs five operations, and it is smaller than it looks. A block store needs five operations: `PUT`, `GET`, `HEAD`, `DELETE` and the signing of short-lived URLs. Because blocks are small and immutable, the most laborious part of an S3 client falls away entirely: **multipart upload is never needed.**

Measured rather than estimated:

| Candidate | Cost | Note |
|---|---|---|
| `minio-go/v7` | **18 indirect modules, 41 compiled foreign packages** | Covey has 22 modules *in total* today. It brings `msgp`, `xxh3`, `crc64nvme`, `md5-simd`, `compress` for replication, ILM, notifications, S3 Select — none of which will ever be used |
| `aws-sdk-go-v2` | heavier still | Official and thorough, but more awkward against foreign endpoints |
| a minimal client of our own | no dependency | SigV4 over `crypto/hmac` + `crypto/sha256`; the presign variant is the simpler one (query-string auth) |

Building it ourselves is more defensible here than usual, because its **failure mode is loud and closed**: a wrong signature yields a `403`, immediately visible — not a silently weakened security property. That is the difference between "use crypto primitives" and "invent a crypto protocol": SigV4 is a signature recipe over HMAC-SHA256, not a handshake.

Against it stands what a matured library brings: the individual providers' idiosyncrasies (path style vs. virtual host, region determination, error parsing). Whoever serves only one provider notices none of it; whoever serves five notices it at every one.

What tipped it was that the counter-argument shrinks in exactly this case. The idiosyncrasies a matured library absorbs are those of *several* providers; an installation speaks to one, and the two knobs that differ — path style versus virtual host, and the region that servers without regions still want in the signature — are one field each. What remains is SigV4, and that is checked against the documented AWS test vector: if the implementation reproduces a signature somebody else computed, the canonical request, the signing key chain and the header handling are all right at once.

`builtin` still needs **no** dependency at all, and `s3` needs none either.

### How the blocks reach the runner

A runner **never gets the store's credentials** — that would be the same omission as the database URL in the egress proxy (see "Trust boundary"). Instead the control plane issues **short-lived, scoped URLs** per transfer, exactly following the secrets broker's pattern ([`04-identity-secrets.md`](04-identity-secrets.md)):

- With the `s3` backend as **pre-signed URLs** — the runner loads directly from the object store, the control plane never sees the bytes.
- With the `builtin` backend the control plane hands out the bytes itself, over the runner API (`/api/runner/v1/blocks/<hash>`, authenticated with the runner token). A signed URL onto itself would be the same thing with one indirection more; what matters is the property, and that is: the runner is a client with a token, not a participant in the storage layer.

Both also prevent 7 GB from having to go through the runner WebSocket: the control channel stays narrow, the payload goes past it. The built-in runner skips the detour and calls the `BlobStore` directly — it sits in the process that owns it. That is a transport detail; the sync and materialisation logic above it does not know the difference.

> **Why not Git as the transport?** An obvious thought, but Git only fits the curated text part. `repos/` are Git checkouts themselves (nested repos plus duplication of an existing remote), caches and SDKs have no business in a version history, and the transcripts are append-only JSONL on which Git bloats without a diff ever being read. What makes Git attractive here — dedup and history — the content-addressed store delivers anyway, and without those drawbacks. Underlaying the wiki *with* a Git history remains an independently attractive idea but is not decided along with this.

### Affinity as a preference, not a binding

If the home is restorable from the store, the binding to a runner turns from a *prerequisite* into an *optimisation*. The difference shows up at failure time:

- **Preference:** the scheduler prefers the runner the agent last ran on — its blocks already sit there locally, and materialising is almost free.
- **On a different runner** it fetches only the blocks that are missing there. That is considerably less than a cold start in the original sense: nothing is cloned anew or pulled from the internet, it comes from our own store — and everything other agents have already deposited there is already present.
- The worst case remains a **fresh** runner without a single block. Then it is a full transfer, and a Flutter agent needs minutes before it reads its first line. But that is **time, not data loss** — and it hits the first agent on a new machine, not every switch.

The home on a runner is thereby a **genuine working copy without special rules**: the runner may clear it away entirely when short of space (`prune`) as soon as the sync is through — there is nothing in it that is not in the store. The only hard rule is: **no `prune` before a successful sync.** A newly set-up runner needs no data migration.

The preference is **sticky, but without automatic return**: if a runner fails, its agents move to another one and *stay there*. When the old one comes back, nothing is migrated back — otherwise the failure wave would be followed by a second wave of cold starts. The balancing happens by itself at the next cold start that was due anyway.

## Warmup: the local block storage

Warmup needs **no mechanism of its own** — it falls out of the store as soon as the runner keeps the fetched blocks locally instead of throwing them away. A block belongs to no agent: it is determined by its hash, and whoever needs the same content gets the same block. It does belong to an organisation, and that needs no enforcement of its own either — a runner serves exactly one, so its local storage holds exactly one organisation's blocks.

That hits exactly the waste that is unavoidable today. A sandbox currently has **a single mount**, its own home — every cache is private, even though not a byte of it is agent-specific:

| | Size in the measured home | agent-specific? |
|---|---|---|
| `.pub-cache` | 1.0 GB | no — the same packages |
| `.gradle` | 951 MB | no |
| `flutter` (SDK) | 1.0 GB | no — the version sits in the project |
| `.npm` | 402 MB | no |
| `jdk` | 346 MB | no |

Two developer agents on the same host today hold the same 4 GB twice; five hold 20 GB. With content-addressed deduplication they sit **once** in the runner's local storage — and the second agent materialises its home from it without fetching a single byte over the wire. A *new* agent on a run-in runner therefore starts almost warm, even though it has never run there.

### Why that dissolves the isolation question

A shared cache is otherwise a **channel between agents** and therefore an isolation question ([`06-observability-control.md`](06-observability-control.md)) — whoever may write into a shared package cache can slip something to others. With content addressing the problem falls away **by construction**:

- A block is requested by the hash of **its content**. If the local storage delivers it, the content is by definition the same one the store would deliver.
- Which blocks make up an agent's home is written in **its own** snapshot. No other agent can influence that mapping.
- The sharing never reaches across the tenant boundary, because there is nothing on the runner to reach across to.

There is therefore no promotion rule, no hash checklist and no read-only substrate someone would have to populate. Sharing is safe because there is no possibility of depositing something else under a foreign hash in the first place.

### What that means for a failure

If a runner fails, **all** its agents move to another one at once. Without shared blocks each of them would pull gigabytes for itself there, simultaneously. With them, the fallback runner fetches **every block exactly once**, no matter how many agents need it — and everything other agents have already deposited there is already present.

The cold start is therefore no longer a question of switching agents but only of the **fresh host**: it hits the first agent on a new machine and nobody after that.

## Scheduling

Deliberately simple, because no runner has to be "the right one" — only the cheapest:

1. Candidates: the **connected** runners of the agent's **organisation** whose tags satisfy its `runner_tags`. The organisation comes first and is not a filter among others: a runner of a foreign tenant is not a worse candidate, it is none. And the tags are the **only** filter — see below.
2. Of those, preferably the one the agent last ran on (`last_runner_id`) — its working copy is warm there.
3. Then the one that already holds the image: a start that has nothing to fetch is the cheaper one.
4. Otherwise the one with the fewest running sandboxes. No bin packing, no resource modelling.
5. None suitable → the task stays put, with an explanatory state instead of an error message about a failed container start.

**Only the tags exclude, and that is a correction paid for in production.** The image used to be a filter too: a runner reported which images it held and got only matching agents. On 25 August 2026 one registered host — claiming `covey-sandbox:latest`, which is what `register` wrote when nobody passed `--image` — left an organisation whose agents run the deploy image with no candidate at all. Every wake failed for half an hour, every thirty seconds, while the machine that could have run it stood idle.

The rule that replaced it says what a host *is* versus what it *happens to have*: a **tag** is a property nothing can stand in for — `arm64`, `gpu`, inside the target system's network — so an agent that asks for one waits until a host carries it. An **image** is a state that repairs itself: `docker run` fetches what the host does not have, and the workplace images are published, digest-pinned per Covey version, precisely so that any host can. Where the fetch cannot work — a private registry the host has no credentials for, a full disk — the start fails **on that host**, and the sandbox moves to the next candidate rather than being lost with the first.

What remains of the "nothing fits" case therefore has two causes, and each still gets its own wording: *this organisation has no connected runner* (none registered, or every one offline) reads differently from *no runner carries the tags …*. The second names the tag, because the remedy is a decision — give a host that tag, or loosen the agent's `runner_tags`.

`last_runner_id` is explicitly a **hint, not a promise**: the scheduler may override it at any time, and nothing in the system may assume a home is still there. An agent whose preferred runner is missing wakes up on a different one — slower, but without a human doing anything.

## Sandbox images per agent

The runner made a gap visible that hurt even without it: the sandbox image used to be instance-wide (`COVEY_SANDBOX_IMAGE`). Every agent got the same one — the mail agent carried the developer agent's JVM along.

The image therefore belongs **on the agent** (D11 in [`07-open-decisions.md`](07-open-decisions.md)), as a profile:

| Profile | Contents | For whom |
|---|---|---|
| `base` | coveyd, Node, git, chromium, ripgrep | support, mail, QA, research |
| `dev` | + PHP, JDK, `fvm`, `uv` | developer agents |
| org-owned | anything | special cases, tighter sandboxes |

The cut deliberately does **not** go along individual languages. A profile is a *union*: a developer agent legitimately works on a PHP and a Flutter project, and one image per language would bring back exactly the question that "version → home, toolchain → image" has already answered — which image do I start on wake, when it is not yet settled which ticket is coming?

A runner reports which images it holds, and an operator may assign that list in the interface instead (migration 0075: `extra_tags` add to what the host reports, `assigned_images` replace it, and an empty list is the decision "no claim"). It is a **statement about cost, not about permission**: the scheduler prefers a host that already has the image and sends the work there anyway when none does. What used to make it a capability — "gets only matching agents" — is the rule that cost an organisation its data plane, see "Scheduling".

The profiles are a **catalogue in the code** (`internal/sandbox`), not a list in each place that needs one — the same shape the target-system plugins have. A profile declares its name, what is inside it, the image it defaults to, and the command that builds it. The last part is the reason the catalogue exists: without it, "how do I get this image?" was answered four times over, once by a `strings.Contains(image, "sandbox-dev")` that guessed the build command from the image name, and guessed wrongly for anyone who had configured an image of their own.

Two consequences follow, and they are what a third profile is worth:

- The instance overrides a profile's image with `COVEY_SANDBOX_IMAGE_<PROFILE>` — derived from the name, so a new profile needs no new configuration code. `COVEY_SANDBOX_IMAGE` remains valid for the default profile; it is the name from before the split and it is set in existing installations.
- The interface offers what is registered (`GET /api/v1/workplaces`) instead of a list of its own, together with the answer to whether the image lies ready. That answer comes from the runner and not from the control plane: an image is available where the sandbox starts. Asked, not warned about — a fresh installation has no `dev` image and needs none, but the choice may say so before somebody picks it and finds out at the next wake.

An image that no profile knows is somebody's own, and about those the repository knows nothing. It says so rather than naming a `make` target that would build something else.

### An image of your own is registered, not typed

The third row of the table — "org-owned: anything" — used to be a **free-text field on the agent**: type a reference, done. It cost three things, and none of them showed up until later:

- **It was invisible.** An image that lives in a field on one agent appears in no overview. "Which images of our own do we actually run?" meant opening every agent.
- **It was undescribed.** A registry reference does not say what is inside it. The next agent got the same string typed again, and whether the two meant the same thing was known only to whoever typed them.
- **A typo failed late.** Not at the keyboard, but at the next wake, in the recording of a run.

An own workplace is therefore **created once** (`org_workplaces`: name, label, description, image) and chosen afterwards like any published profile. The agent carries a *name* in both cases — which image is behind it stays one decision in one place, and that is the property the catalogue was introduced for in the first place.

Two rules where the two sources meet:

- **A catalogue name is taken**, even for an organisation that has never used it. Otherwise `dev` would mean two things and which one applied would depend on the order of a loop.
- **Deleting is refused while agents work in it.** They would be left pointing at a name that resolves to nothing, and would find out at the next wake. The overview therefore names the agents per workplace rather than counting them: whoever is about to change or delete one wants to know whom it concerns.

### Where the image itself comes from

The catalogue in the code says *which* profiles exist. It cannot say what a profile's image **is** on a given installation, and for a while it pretended to: the compiled default was `covey-sandbox-dev:latest`, a name that exists only on a machine that has built it. Every container installation therefore had to be told where its own workplace lived, through `COVEY_SANDBOX_IMAGE_<PROFILE>` — and an upgrade that moved existing agents onto a new profile broke every instance that had not been told, with an error whose remedy (`make sandbox-image-dev`) it could not run.

The image is therefore **published, and listed in a catalogue of its own** — the same shape the plugin marketplace has ([`22-plugin-marketplace.md`](22-plugin-marketplace.md)): one JSON file behind one URL, fetched with a cache, deciding nothing on its own.

```json
{ "schema": 1, "generated_at": "…", "workplaces": [
  { "name": "dev", "label": "dev", "description": "…", "images": [
    { "covey_version": "main",   "ref": "ghcr.io/…/covey-sandbox@sha256:…" },
    { "covey_version": "v0.4.0", "ref": "ghcr.io/…/covey-sandbox@sha256:…" }
  ]}]}
```

Three properties carry it, and each answers something that went wrong without it:

- **Pinned by digest, per Covey version.** The image carries the `coveyd` that speaks to this control plane, so "which image" is not a question about the newest one but about *this build*. A tag would be a moving target; the digest is not, and docker refuses a mismatch without anybody here writing a check — the same hinge the marketplace's `sha256` is.
- **Written by the side that builds.** The images and the catalogue come from the same pipeline ([`.github/workflows/sandbox-images.yml`](../.github/workflows/sandbox-images.yml)), because the digests exist nowhere else at that moment. Entries for older versions stay: an installation on `v0.4.0` still finds its own after `v0.5.0` is published.
- **Three sources, in one order.** `COVEY_SANDBOX_IMAGE_<PROFILE>` beats the catalogue beats the compiled default. Whoever names an image on their own host has the last word — a remote file that could overrule it would decide what runs on somebody else's machine. An air-gapped installation sets the variable, points `COVEY_SANDBOX_CATALOG_URL` at a `file://` path, or leaves both alone; none of the three is a special case in the code.

The catalogue URL defaults to what the project publishes and is derived from the source address (`buildinfo.SourceRepo`), so a fork that builds its own images serves its own catalogue without a line changed. The fetching itself is not a second mechanism: `marketplace.Feed` holds the part that is not about plugins — one GET, the cache that survives a restart, the stale copy served immediately while the refresh runs behind it, and the error reported *next to* that copy rather than instead of it.

What this removes is the class of failure, not one instance of it: a fresh installation needs no image name, an upgrade needs no second build, and "the image is missing" stops being a thing an operator has to fix by hand — the host pulls what the catalogue names.

## Trust boundary

With `start_sandbox` a runner receives an agent's `COVEY_DAEMON_TOKEN` and egress token in order to inject them into the container. It **can** therefore impersonate every agent it hosts. That is the same trust level as a CI runner that sees job tokens — but it has to be said out loud:

> **Runners are trusted infrastructure of the organisation, not foreign boxes.** A runner is not a way of bringing in untrusted compute capacity.

From this follow hard rules:

- **No database access and no store credentials.** A runner speaks exclusively the runner protocol; it fetches and writes blocks through short-lived, scoped URLs (see "How the blocks reach the runner"). Concretely this concerns the egress proxy today: in hard isolation mode it receives `COVEY_DATABASE_URL` and reads its allowlist from Postgres itself. On a remote runner that would mean distributing the Postgres credentials to every host — that tips the trust boundary over and is a **prerequisite** for everything else: the allowlist has to come through the authenticated control-plane API (see below). For the built-in runner no process boundary enforces this — it lives in the process that holds the pool. The rule holds for it all the same, and as a rule about the code: it reaches for the runner protocol, not for the database. Whoever weakens that here has quietly written a second implementation, which is the one thing the built-in runner exists to avoid.
- **No long-lived target-system secret.** The runner sees daemon and egress tokens, never the brokered credentials — those still go directly to the daemon ([`04-identity-secrets.md`](04-identity-secrets.md)).
- **The organisation is a property of the runner, not of the request.** Every message it receives and every answer it may ask for is scoped to its own organisation; a runner that asks after a foreign agent gets a "not found", not an allowlist. That the trust is limited to one tenant is the reason this trust level is bearable at all.
- **TLS is mandatory.** The control plane has to be reachable from foreign hosts; `COVEY_PUBLIC_URL` over plaintext HTTP would be the disclosure of every daemon token.
- **Revocation and audit per runner.** A runner token is individually revocable; registration, connection, every sandbox assignment and every `home_op` are auditable events ([`06-observability-control.md`](06-observability-control.md)).

## Egress with distributed runners

Hard isolation mode (`network`) requires a proxy **in the sandbox's network segment** — that is, per runner, started and maintained by the runner. Three changes compared with today:

1. **The segment belongs to the runner.** Today the internal network and the proxy container are instance-wide singletons (`covey-egress-internal`, `covey-egress-proxy`), so every sandbox of every organisation hangs off the same segment. The allowlist still applies per agent — the proxy identifies the agent by its per-sandbox token — but sandbox-to-sandbox traffic never reaches the proxy at all: `--internal` cuts the way out, not the way sideways. Both objects therefore carry the runner's identity in their name, one segment per runner. Since a runner serves one organisation, the tenant boundary falls out of it. **This is a gap that exists today**, independently of any runner, and it can be closed on its own before the rest of this.
2. **The allowlist through the API instead of through the DB.** The proxy asks the control plane (authenticated with the runner token) and caches; the control plane pushes changes via `set_allowlist`. That is the prerequisite named above and **the right construction even without runners** — the proxy is an enforcement point, not a database client.
3. **The control plane is a genuine foreign host.** `host.docker.internal` becomes the public address; it has to be on the proxy's internal allowlist so that the daemon link gets through hard mode. The mechanism for that exists (`COVEY_EGRESS_ALLOW`), only the value is no longer automatically right.

## File access

`FileAccess` today delivers a **host path** that the file browser reads directly. With remote runners that becomes `home_op` over the runner link.

The reasoning why file access deliberately does **not** go through the daemon protocol is preserved in the process — it even becomes cleaner: the home has to be readable even when the sandbox is asleep, and asleep is the normal state. The runner link exists continuously, the daemon link only during a run. The runner is therefore exactly the right seam for this requirement; the daemon would have been the wrong one.

So there are three places a home can be read from, behind one interface: the **connected runner** (the live truth, and the only one that can be written to), the **last snapshot** when that runner is not connected, and nothing at all when there is neither — which is answered as "no reachable home" rather than as an empty listing that reads like an empty home. The built-in runner takes the same route; in its case `home_op` is a channel send and not a round trip.

Reading from the snapshot is **read-only**, and not out of caution: a snapshot is a state that was, and writing into it would produce a second state beside the working copy that is coming back, with nothing to say which of the two is the home. The interface names that state in the listing itself and not only when a write fails — whoever is about to upload something should learn it beforehand.

### What the browser writes has to be synced

A change made through the browser lands in the runner's working copy. That is right — it is the live truth, and while the agent sleeps it is the only place it can land. But the snapshot is what the next wake materialises, and **on another runner it is the only thing there is**. Between the two lies a window in which an upload disappears without anyone having deleted it.

So a change through the browser marks the home and a sync follows. Two things make that reliable rather than merely likely:

- **Debounced, not per operation.** A sync walks the whole home; doing it per file would turn a fifty-file drag-and-drop into minutes of scanning. A short settling period turns it into one sync.
- **Flushed before every start, and at shutdown.** The settling period is an optimisation, not the guarantee. The guarantee is that whatever it has not yet carried out is carried out at the moment where getting it wrong actually costs something — immediately before the home is materialised over the working copy.

## Interface

A store that grows quietly in the background and whose content nobody can see is an operational risk — you notice it only when the disk is full. Both therefore belong in the UI and not only in environment variables.

**What the interface shows about a host, it does not fetch while somebody waits.** A runner answers `capacity` out of the same read loop a `start_sandbox` occupies — and a start may be a multi-gigabyte pull, which is why its bound is an hour. Asked at the moment the page is opened, one host in that state set the wait for the whole view, and the view is polled. So the connection asks by itself, at the heartbeat's tempo, and remembers the answer with the moment it was taken; the page reads what is remembered. The age travels with the figure and is shown once it is more than a couple of minutes — a remembered number without an age is the kind that reassures right up to the moment the disk is full. The running sandbox count does not come from there at all: the pool counts it itself and knows it exactly. The same holds for the data-plane check behind the warning banner — the first one is fetched while somebody waits, because there is nothing to show yet; every later one runs beside the request.

### An agent's home

On the agent page, next to the existing file browser:

| Figure | Why it belongs there |
|---|---|
| The home's size, of which **occupied after dedup** | The difference is the actual statement: a 7.1 GB home, but perhaps 200 MB that only this agent holds |
| Last sync: time, duration, blocks transferred | Whether the sync runs at all, and whether it is expensive |
| When the home was last written | Whether the one state is current, and how old it is |
| The current or last-used runner | Where the working copy sits warm |
| The largest directories | Answers "why is this home so big?" without shell access — and reveals candidates for an exclusion |

Plus one action: **back up now** (force a sync, e.g. before maintenance). There is no list to pick from and no restore, because there is nothing to pick: the snapshot is the state the next wake materialises, and the sync replaces it. What is offered here is the last write, not a choice between several.

The rollback that a kept history would allow lives one level down, in the backup of the block store on the host, and belongs to whoever operates the installation rather than to whoever clicks in it. That is a deliberate trade and it has a price worth naming: **an agent that wrecks its own home and then falls asleep has overwritten the only state Covey holds.** Whoever cannot accept that operates the block store as a backed-up directory — or points it at an object store whose bucket is.

### Cleaning up

No rules to configure, because there is nothing to weigh up: what no manifest references any more is gone.

- **By itself**, periodically, for every organisation. A cleanup that only runs when an admin presses a button does not run on the installation whose store is quietly filling the disk — and that is the only one it was for.
- **By hand** besides: a button with a preview, and the same pass as a subcommand for the machine where nobody wants to open a browser.
- **Never the last state.** An agent's current snapshot is what its next wake materialises. Removing it is a delete command by a detour.

A block goes only when **no** manifest references it any more — that is the price of deduplication, and the reason a preview has to name the space actually freed rather than the size of what is being removed. Anything else would be a number that is never right.

The figure this produces is larger than it sounds. With one state per agent, each sync leaves the previous manifest's exclusive blocks behind, so the garbage is proportional to how often agents work — not to what anybody chose to keep.

The store's fill level additionally belongs on the dashboard: total size, growth, and a warning before the disk runs short — not after.

## Not in the first pass

Deliberately left out so that the first pass stays small:

- **Autoscaling** of runners (cloud API, spot instances).
- **Anticipatory warming** — mirroring blocks onto runners an agent has never run on as a precaution. The local block cache grows out of actual use; *anticipating* it is a later optimisation.
- **Migration back** after a runner failure (see "The home": the preference stays sticky).
- **Several simultaneous sandboxes per agent** — "serial before parallel" still applies.
- **The runner as a promise of dedicated hardware.** That a runner belongs to exactly one organisation is decided (see "One runner, one organisation") — its state, its blocks and its network segment are the property of one tenant. What is *not* promised is that no second organisation's runner runs on the same machine: two built-in runners share a host by construction. Whether Covey ever guarantees a tenant its own iron is a question of the offering, not of this protocol, and it belongs in [`09-enterprise-model.md`](09-enterprise-model.md).
- **Non-Docker runners** (Firecracker, Kubernetes pods). The runner protocol is cut so that they fit behind it later — the Docker runner is built first.

## Build order

Every stage is useful in itself and can be accepted individually:

| Stage | Contents | Value on its own |
|---|---|---|
| 0 | Detach the egress proxy from the DB (allowlist through the API), the `runners` table with one built-in entry per organisation as the identity the proxy authenticates with, network segment and proxy container per runner | The right construction for the enforcement point, and the shared internal segment closed — both independently of runners |
| 1 | An image per agent (`sandbox_image`), profiles `base`/`dev` | The mail agent no longer carries a JVM |
| 2 | The seam: runner protocol, `Transport` with the in-process implementation, the built-in runner, the Docker provider moved behind it, `RunnerPool` as `SandboxProvider` | A crashed sandbox is reported instead of running into the `ReadyTimeout`, and what stands in the way of a wake becomes visible per host |
| 3 | The home store: content-addressed storage, sync after the job, materialising on wake, local block storage | A lost home costs time instead of work, and the second agent on the same host starts warm — **both already with a single host** |
| 4 | The remote runner: the WebSocket transport, `register` including a configuration file, the protocol handshake and version, `covey-runner` as a third binary plus its release artefacts (a binary per architecture, a Docker image, a systemd unit); and with it the pause that takes a host out of service | Sandboxes run on a second host |
| 5 | `home_op` — the file browser over the runner link, reading from the snapshot while a runner is offline, and the sync of what the browser writes | The file browser works remotely too, does not fail when a host does, and no longer loses an upload when the agent moves |
| 6 | Tags, capacity, a runner view in the UI | Operability from more than two runners onwards |
| 7 | Interface: home info, the periodic cleanup plus a button and a subcommand for it, fill level on the dashboard | The store is visible and operable instead of growing quietly |
| 8 | The `BlobStore` backend `s3` (the port exists from stage 3, `builtin` suffices up to here) | Durability and replication when the control plane's disk is not enough |

Stages 0 and 1 are independent of the runner and should run first — each is an improvement on the current state in its own right. Stage 0 because it otherwise becomes a security hole as soon as a runner runs remotely, and because the shared internal segment is one already. Stage 1 because the image capability would otherwise have to be retrofitted in stage 2.

**The seam comes before the store.** That is the opposite of the obvious order, and the reason is the built-in runner: sync and materialising are the runner's job, and whoever builds them into the local provider first writes them a second time as soon as the runner arrives. With the seam standing, the store is written once — for a runner that happens to be in the same process.

Stage 2 is at bottom a refactoring, and that is worth paying for anyway, because it does not come away empty-handed: a runner that watches the container state reports a crash or an OOM instead of leaving the control plane to infer it from a `ReadyTimeout` — and the provider's self-check (missing image, no Docker socket) becomes a per-host statement instead of an instance-wide one.

**Stage 3 carries the whole edifice.** It makes the home replaceable, and only then is a runner switch not data loss; without it the affinity would have to be a binding after all. It delivers the warmup along with it — the local block storage *is* the shared holding. And it pays off before a single second host exists: today two developer agents on the same machine hold the same 4 GB twice, and a deleted home is unrecoverable.
