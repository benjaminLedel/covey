# Upgrading

Migrations run by themselves: `covey serve` applies what is missing at startup,
under an advisory lock, so several instances against one database do not race.
An upgrade is therefore usually "pull the new binary, restart".

This page lists the upgrades where that is **not** enough — where something has
to be built, backed up or decided beforehand. Newest first.

## Ask your own installation first

```bash
covey doctor
```

Or, in the browser: **Platform → Diagnostics** — the same checks, plus which
agent configurations should catch up after the upgrade.

It reads and changes nothing, and it answers the questions this page can only
ask in general: which images **your** agents need and whether they are on this
host, what **your** database is about to migrate, where **your** blocks lie and
how big they are. It exits non-zero when something would stop an agent from
working, so it can stand in a deploy script before the restart.

The short version of an upgrade is therefore:

```bash
make upgrade     # binaries + both sandbox profiles
covey doctor     # what is still in the way here
```

---

## To the runner release (`spec/16`)

Six migrations (0051–0056), a split sandbox image, and a new store next to the
database. Two things need action before the first agent wakes.

### 1. Build the second sandbox image — otherwise no agent wakes

The sandbox image used to be one image with everything in it. It is now two
profiles ([`spec/16`](../spec/16-runner.md), "Sandbox images per agent"):

| Profile | Contents | For whom |
|---|---|---|
| `base` (`covey-sandbox:latest`) | coveyd, Node, git, chromium, ripgrep | support, mail, QA, research |
| `dev` (`covey-sandbox-dev:latest`) | + PHP, JDK, `fvm`, `uv`, node-gyp toolchain | developer agents |

**Migration 0052 puts every existing agent on `dev`** — deliberately, because
that is what the old image contained, and an upgrade must not pull a developer
agent's toolchain out from under it. But `covey-sandbox:latest` now builds the
*lean* base image, so the image your agents are pointed at does not exist yet:

```bash
make sandbox-images     # builds both: base, then dev on top of it
```

Without it every wake fails with *sandbox image "covey-sandbox-dev:latest" is
missing*. `covey doctor` names it before the restart, with how many agents are
waiting on it; the startup check and the agent overview say it afterwards.

Afterwards, move the agents that do not need a toolchain to `base` (agent →
settings → *workplace*). That is what the split is for: a mail agent should not
carry a JVM. It is a decision per agent and not one a migration makes.

### 2. Back up the new block store

The home store is **on by default**. After every job an agent's home is synced
into it as a whole and materialised from it on wake — that is what makes a home
survive the loss of its working copy.

The blocks live under `<COVEY_DATA_DIR>/blocks`.

> **This directory needs backup like the database.** For 99 % of its content
> that would only mean downloading toolchains and cloning repos again. For the
> rest it would not: of a measured 7.1 GB developer home, 48 MB exist nowhere
> else, and they lie scattered all over it. It is a cache in its *function*, not
> in its *need for protection*.

What it costs, so nothing is a surprise:

- **The first falling-asleep of each agent is slow.** The first sync of a grown
  home is a full pass — 7 GB. Everything after that is a delta of megabytes.
- **Disk.** The store holds what the runner also has. With a single agent that
  is close to a doubling; from the second developer agent onwards the
  deduplication of the identical toolchain caches turns it into a saving.
- `COVEY_HOME_STORE=false` turns it off. Then homes stay directories as before:
  no snapshots, no rollback, and unrecoverable when lost.
- `COVEY_HOME_EXCLUDES=".dartServer,…"` leaves paths out. The default is empty
  on purpose — the list is a cost question, not a prerequisite for correctness.

Retention (the last *N* per agent, a maximum age) is set under **Runners**, with
a preview before anything is removed.

### 3. What happens by itself

Nothing to do for these; they are listed so the log lines make sense.

- **Migrations 0051–0056** are applied at startup.
- **The egress proxy no longer gets the database URL.** It fetches its allowlist
  from the control plane with a runner token now — it is an enforcement point,
  not a database client. The control plane sets that up itself.
- **The internal network and the proxy container carry the runner's identity**
  (`covey-egress-internal-<runner>`). The instance-wide ones from before are
  removed at the next start; you will see a log line saying so. This also closes
  a gap that existed before: with more than one organisation, all sandboxes hung
  off the same internal segment, and `--internal` cuts the way out, not the way
  sideways.
- **Every sandbox now starts through the runner protocol**, on a built-in runner
  the control plane runs itself — one per organisation. Nothing to install, and
  the setup does not change.

### 4. Optional, and only if you want it

- **A second host**: `make runner`, then `covey-runner register` on that
  machine. See [`ops-runner.md`](ops-runner.md). Note that the built-in runner
  **stands down** as soon as an organisation registers a runner of its own —
  compute is then either on this machine or off it, never quietly on both.
  TLS becomes mandatory at that point: a `start_sandbox` carries daemon tokens.
- **Blocks in an object store**: `COVEY_BLOB_STORE=s3` (see
  [`ops-runner.md`](ops-runner.md)). Switching backends does **not** move the
  blocks — copy the directory over first, or accept that every home is rebuilt
  from scratch on its next wake.

### Rolling back past earlier releases

Everything before this release upgraded without a manual step: no environment
variable was ever removed or renamed, and every migration carried its own data
over. One boundary is worth knowing about anyway — **v0.3.0 cannot be rolled
back without loss.** Migration 0048 moved credential policy from the secret key
up to the runtime; going back restores the columns but not which runtime had set
which cap, because two runtimes can share a key. The migration says so itself.

### Rolling back

The migrations have `down` files, but rolling back past 0052 leaves the agents
on an image column that no longer exists while the *content* of
`covey-sandbox:latest` has changed. If you have to go back, rebuild the old
image from the previous tag as well.

The block store is unaffected by a rollback: it is content-addressed, and a
version that does not know it simply does not read it. It also does not clean it
up — the directory stays until a newer version sweeps it.
