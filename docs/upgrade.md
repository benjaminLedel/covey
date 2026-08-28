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

## To 0.6.0 — three plugins move to the catalogue

`zammad`, `vulndb` and `k8s` are no longer compiled into the binary. They are
WebAssembly modules in the [plugin catalogue](https://github.com/benjaminLedel/covey-plugins)
now, on the same footing as anybody else's plugin.

**If you use none of the three, there is nothing to do.** The other nine target
systems are unchanged.

### 1. Install the ones you use — the agents keep their access

Store → Catalogue → *the plugin* → Install. Do it once per installation, after
the upgrade.

What survives the move on its own: the plugin row, its enablement per
organisation, the stored secrets, and every agent's `ACCESS.md`. Only the code
now arrives from the catalogue. An agent whose `ACCESS.md` names `zammad` finds
it again as soon as the module is installed, with the credentials it always had.

Between the upgrade and that install, an agent that needs one of the three
cannot use it. Do the install in the same maintenance window rather than the
next one.

> Installing needs the catalogue to be reachable. An air-gapped installation
> should fetch the three `.wasm` files from the
> [pack's v0.7.0 release](https://github.com/benjaminLedel/covey-plugin-pack/releases/tag/v0.7.0)
> beforehand and install them from file — the digests are in the release notes,
> and Covey checks them either way.

### 2. Two settings are gone, and one of them silently

| Was | Now |
|---|---|
| `COVEY_ZAMMAD_REPLY_TYPE=web` | the `reply_type` parameter on the `reply` action |
| `COVEY_ZAMMAD_INTAKE_GROUPS="Support L1"` | a **group condition on the Zammad trigger** ([`ops-zammad.md`](ops-zammad.md)) |
| `vulndb_token` (an NVD API key) | nothing. NVD answers at its anonymous rate limit |

The middle one is the one to act on. A module gets no process environment, so an
allowlist that used to be honoured is not merely ignored — **every group's
tickets now arrive**. If you relied on it, put the condition in the trigger
before the upgrade, not after: the filter belongs there anyway, because a
trigger that does not fire costs nothing while a webhook Covey discards has
already been sent, signed and verified.

`vulndb_token` is dead configuration and can be deleted. It used to raise NVD's
limit from 5 requests per 30 seconds to 50; a module is never handed the
credential for a host it merely declared, and all six of vulndb's sources are
declared ones. `advisory` reports a rate-limited NVD as a note beside an
otherwise complete result, the same as any source that does not answer.

### 3. Kubernetes: the cluster CA moves out of the action

`k8s` used to take the certificate as an action parameter,
`{{secret:k8s_ca}}` in `ca_pem`. That parameter is gone. Store the certificate
as the secret **`k8s_ca`** and Covey builds the trust store from it.

This is a fix as much as a move: a certificate passed per action travelled
through the model's context, the guard-rail subject and the recording of every
single call.

### 4. Rolling back

Going back to 0.5.x restores the three as compiled plugins, so nothing is lost —
the installed modules simply stop being reached, because a compiled plugin and a
stored one share a name and the compiled one wins. Uninstall them from the store
if you intend to stay on 0.5.x, or the store will show a plugin that is present
and not the one being used.

An 0.5.x instance cannot install the modules in the first place: the catalogue
entries declare `covey_min_version: 0.6.0` and the install is refused with that
sentence.

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
make sandbox-images-pull   # pulls both from the published package (minutes)
make sandbox-images        # or builds them here: base, then dev on top of it
```

The project publishes every workplace as one package on every push and every
release — `ghcr.io/benjaminledel/covey-sandbox`, the variants as a tag prefix
(`base-latest`, `dev-latest`, `dev-flutter-latest`, `base-v0.4.0`, …). Take the tag your binary is:
the image carries the `coveyd` that talks to this control plane.

Without it every wake fails with *sandbox image "covey-sandbox-dev:latest" is
missing*. `covey doctor` names it before the restart, with how many agents are
waiting on it; the startup check and the agent overview say it afterwards.

**Running Covey as a container?** Then there is no checkout and no `make`, and
the image has to come from somewhere else. Two ways, both a line in the
deployment's `.env`:

```bash
# a) the published image — nothing to build, the host pulls it on the first wake
COVEY_SANDBOX_IMAGE=ghcr.io/benjaminledel/covey-sandbox:base-latest
COVEY_SANDBOX_IMAGE_DEV=ghcr.io/benjaminledel/covey-sandbox:dev-latest

# b) the image you already have. The one from before the split carried PHP, JDK,
#    fvm and uv — it IS the dev workplace, whatever it is tagged.
COVEY_SANDBOX_IMAGE_DEV=covey-sandbox:latest

# c) your own registry (the deploy pipeline pushes both images there and pins
#    them; this is what an instance deployed from CI gets)
COVEY_SANDBOX_IMAGE_DEV=registry.example.com/covey/sandbox-dev:<tag>
```

`docker compose up -d` afterwards. The variable exists for every profile —
`COVEY_SANDBOX_IMAGE_<PROFILE>` — and the startup check names the one that fits
the missing image.

Afterwards, move the agents that do not need a toolchain to `base` (agent →
settings → *workplace*). That is what the split is for: a mail agent should not
carry a JVM. It is a decision per agent and not one a migration makes.

Since then there are **role workplaces** beside `dev` — `dev-flutter`,
`dev-php`, `dev-web` — for agents whose field is settled, and `dev-full` for an
installation that would rather not split at all. Nothing moves an agent there
either; `dev` keeps working. [`ops-workplaces.md`](ops-workplaces.md) says
what each contains and what one costs.

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
- `COVEY_HOME_EXCLUDES=".dartServer,…"` leaves paths out, and it now has a
  considered default instead of an empty one: the scrap class — `__pycache__`,
  `.dartServer`, `*.pyc`, `*.tmp`, the pip and Playwright caches, an agent's
  hand-built apt tree. Nothing but the tool reading them recreates those, so
  leaving them out costs nothing. Three kinds of pattern are understood: a path
  from the home root (`repos/scratch`), a NAME at any depth (`__pycache__`), and
  a glob on the file name (`*.pyc`).

  Package caches (`.npm`, `.gradle`, `.pub-cache`) are deliberately NOT in the
  default. Leaving them out saves store and scan time and costs a re-download on
  the next host — a good trade for an agent that moves between runners, a bad
  one for an agent that always lands on the same. That decision is yours;
  setting the variable replaces the default entirely, and `COVEY_HOME_EXCLUDES=none`
  switches it off (everything is synced, as before).
- `COVEY_HOME_TIDY_ABOVE_GB=5` is the size above which the platform asks an
  agent to tidy its own home — a backlog task with the figures in it, not a
  sweep. What is scratch and what is memory is known only by the agent that
  created it. `0` switches it off.

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
  machine. See [`ops-runner.md`](ops-runner.md). The built-in runner keeps
  running beside it and takes over when the new host cannot — whoever wants no
  compute on the control plane's machine **pauses** the built-in runner in the
  runner view. (It used to stand down by itself as soon as a runner was
  registered; that rule left more than one instance with a host on paper and no
  data plane in fact.) TLS becomes mandatory at that point: a `start_sandbox`
  carries daemon tokens.
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
