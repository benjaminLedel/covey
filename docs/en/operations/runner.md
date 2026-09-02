---
slug: runner
title: Runner
description: 'Sandboxes on more than one host: the built-in runner, registering a remote one, how work is assigned and what pausing a runner does.'
---

The data plane does not have to live on the control plane's machine. A
**runner** is a process on an arbitrary host that registers with the control
plane and gets sandboxes assigned to it from there. The full model is in
[`spec/16-runner.md`](../../../spec/16-runner.md); this page is the operational part.

## You already have one

Every sandbox starts through the runner protocol, including on a single
machine: `covey serve` runs a **built-in runner** in its own process, one per
organisation. Nothing to install, no token, no configuration file — the normal
installation notices none of this.

There is no switch you need to reach for. What matters is the rule:

> **The built-in runner runs beside your own hosts, is asked last of all, and
> takes over when none of them can. Whoever does not want it pauses it.**

Pausing is the everyday form of "not here", and it lives in the runner view.
`COVEY_BUILTIN_RUNNER=off` is the same statement for an installation whose
compute must never touch the control plane's machine: `covey serve` then
refuses to start a sandbox on it at all, rather than falling back to one.

It used to stand down by itself as soon as an organisation had a registered
runner, and that rule inferred an intention from a fact: somebody added a host,
therefore they want nothing on this machine. The inference was wrong twice in
one afternoon on a production instance — first because the registered host
claimed `covey-sandbox:latest` while the agents needed the deploy image, so
nobody was a candidate; then, a night later, because that host was connected and
answered nothing while the machine that could have run the work stood idle.

So a second runner is a second runner now, not a replacement. What used to be a
rule nobody could see is a state everybody can: **Pause** in the runner view.

The intention behind the old rule survives as an ordering rather than as a
switch: **the built-in runner is the last candidate.** As long as a registered
host can carry the work, it carries it — no mixed pool where half the agents
quietly run on the control plane. Only when none can does the built-in one step
in, which is the difference between a slow afternoon and an organisation that
does nothing.

The fallback is not quiet. It writes `no connected runner fits — the built-in
one takes it` with the image and tags it was looking for, and it cannot smuggle
work past a requirement: **tags still exclude it.** An agent that asks for `gpu`
waits for a host with `gpu`.

## Updating a host

The runner's page has **Update to the newest version**. The host then fetches
its own binary from the same release `install.sh` installs from, checks the
checksum before it replaces anything, and starts again — same command line,
same configuration file, no reinstall. A systemd unit is not needed for it: the
process replaces itself in place.

Runner and control plane are delivered separately on purpose; nobody should
have to touch ten machines to upgrade one server. The price used to be that a
fix in the data plane meant an SSH session per host, and the hosts nobody logs
into kept their bugs. This is that price paid back.

Three things it will not do:

- **Update while the host is carrying sandboxes.** It refuses and says how
  many. The containers would survive the restart, the watchers would not.
- **Update a host that is not connected.** A machine that hears nothing does
  not update itself; start it, then press the button.
- **Update the built-in runner.** That one is the control plane's own process
  and is updated by updating covey.

The version installed is the server's own when the server runs a released
version, and otherwise the newest published release — an instance built from
`main` is ahead of every release. `COVEY_RUNNER_DOWNLOAD_BASE` points the
download elsewhere: an installation that does not reach GitHub, or one that
publishes its own builds. Whatever it points at has to hold `SHA256SUMS` and
the archives named as the release names them
(`covey-runner_<version>_<os>_<arch>.tar.gz`).

If the runner runs as a user who may not write to its own path (`/usr/local/bin`
belongs to root), the update says exactly that and changes nothing.

**A new binary is not a new process.** The button takes care of that itself —
the runner replaces itself in place. The script does too when it finds a running
`covey-runner.service`: it restarts it and says so. Everywhere else (a runner in
a terminal, in tmux, under a different unit name) the restart is yours, and
until it happens nothing has changed — the old image keeps running and the
version in the runner view stays what it was.

**The first time is still by hand.** A runner built before this feature existed
does not know the message and would simply not answer it — so it says at
registration what it can do, and the button answers immediately with *install it
once by hand*. After that installation the host updates itself.

## Pausing a host

**Pause** on the runner's page takes it out of service: it gets no new
sandboxes, and everything else stays — token, tags, assigned images, and above
all the working copies of the homes that make this host worth keeping. What is
running there finishes; nothing is killed. Resuming makes it selectable again
at once, with no restart of the runner.

That is the maintenance window (kernel update, disk swap, a machine somebody
wants quiet), and on the **built-in runner** it is the sentence *no compute on
the control plane's machine* — said once, by a person, visible in the interface
and reversible in the same place. It survives a restart of the runner and of the
control plane: the pause is on the row, not on the connection.

`COVEY_BUILTIN_RUNNER=off` is the same statement for an installation whose
configuration, not whose database, should carry it — it now means "never",
independently of whether any other runner exists.

If everything is paused, a wake says so: *every runner of this organisation is
paused*. That is a sentence about a decision, and deliberately not the same one
as "no runner answers".

**Offline is not "no runner" any more.** It used to be: the agents waited for
their host to come back rather than moving onto the control plane's machine.
Now the built-in runner takes over — during a maintenance window too. That is
the price of the same fallback that keeps an organisation working when its host
is wedged, and it is one decision away: pause the built-in runner for the
window as well, and the agents wait exactly as they used to.

## Adding a host

Under **Runners** in the interface: *Create a registration token*. It is shown
once and the page prints the command to run on the new host. The same through
the API, if you prefer:

```bash
curl -X POST https://covey.example/api/v1/runners/registration-tokens \
     -H 'Content-Type: application/json' --cookie "$SESSION" \
     -d '{"description":"Build host Frankfurt"}'
```

On the new host. The runner view prints these three lines with the token
already in them — the installation script comes from your own instance, so the
runner matches the server it will talk to:

```bash
curl -fsSL https://covey.example/install.sh | sh -s -- --runner
covey-runner register --url https://covey.example --token <registration token> \
                      --description "Build host Frankfurt" \
                      --tag arm64 --image covey-sandbox:latest
covey-runner run
```

The host does **not** need the sandbox image up front. `docker run` fetches what
is missing, and the workplace images are published and pinned by digest per
covey version so that any host can. Pulling it there in advance (`make
sandbox-image`, or from your registry) only makes the first wake faster.

The one case where that is not automatic: an image in a **private** registry.
The host needs credentials for it (`docker login`), otherwise the start fails
there — and the sandbox moves to the next runner rather than being lost.

`register` writes `/etc/covey-runner/config.toml` (overridable with
`--config`): the server address, the runner token, the working directory, and
what the host says about itself — `--tag arm64` says what it **is**, `--image`
which workplaces it provides. The token is what this host acts for its
organisation with; the file is written `0600` and belongs treated as such.

### How many sandboxes this host carries

A runner takes as many sandboxes at once as it is asked for — one per agent,
several agents in parallel — and until now that was unbounded. Docker will
happily start the twentieth container, and the machine finds out afterwards.

The cap belongs to the host and is set in its configuration:

```
max_sandboxes = 4
```

`--max-sandboxes 4` at `register` writes it; afterwards you edit the file and
restart the service. `0` — the default — means no limit.

It is deliberately **not** in the runner view, unlike tags and images. Those
are steering decisions somebody makes about a fleet; this is a statement about
the iron. The scheduler can rank hosts, but it has no way of knowing that one
of them is a laptop, and a number an operator can type into a browser about a
machine they cannot see is a number that will be wrong.

Both sides hold it:

- The **scheduler** stops choosing a host that is at its limit. The runner
  view shows `3 of 4` instead of `3`, and the reason a wake waits reads
  *"every runner of this organisation is at its sandbox limit"* — a working
  data plane with a queue, not a fault to go looking for.
- The **host** refuses a start beyond its limit anyway. Not belt and braces:
  the scheduler works from a count it keeps itself, and between its decision
  and the start a second one can arrive. Only the host knows what is actually
  running on it, and only the host falls over when the number is wrong. The
  refusal is a line in its log.

Restarting an agent that already runs on a full host is allowed — it replaces
that agent's sandbox rather than adding one. Otherwise a host at its limit
could not restart its own agents, which is exactly when one usually needs
restarting.

**Both are editable in the runner view, and that is the normal way.** They were
properties of this file for a while, sent once at connect, and changing them
meant editing it on the machine and restarting the runner — a capability
statement that can only be corrected by an SSH session is one that stays wrong.
`PATCH /api/v1/runners/{id}` carries the same thing, and a connected runner
learns it immediately rather than at the next reconnect.

The two halves behave differently on purpose, and only one of them steers:

- **Tags are the steering.** They are added to what the host reports — a
  machine does not stop being `arm64` because somebody labelled it `build` —
  and they are the **only** thing that excludes a host. A tag says what a
  machine *is*, and no other machine can stand in for that.
- **Images are a statement about cost.** The assigned list replaces the one the
  host reports, and the scheduler prefers a host that already holds the image —
  but it sends the work there anyway when none does, because `docker run`
  fetches what is missing. It used to exclude, and that is what cost a
  production instance its whole data plane: `register` wrote the claim
  `covey-sandbox:latest` when no `--image` was given, a sentence about a host
  that had just been installed and held nothing at all, and from then on the
  agents that needed another workplace had no candidate. `register` writes no
  claim now, and a claim no longer refuses.

An agent asks for tags through **Host requirements** in its settings
(`PATCH /api/v1/agents/{id}/runner-tags`); empty is the normal case and means
any runner of the organisation. A tag is a requirement, not a preference: if
nobody carries it, the agent waits and the message names the tag.

`register` checks the connection before it reports success — the WebSocket, not
just the HTTP call that created the runner. If that fails, the registration
still stands and the configuration is written; fix the connection and start it
with `covey-runner run`.

**The first start on a new host is a download.** `docker run` fetches the
workplace image if it is missing, and those are several gigabytes — so one
sandbox start may legitimately take minutes. The bound for it is
`COVEY_SANDBOX_START_TIMEOUT` on the control plane, **60 minutes** by default.
That sounds enormous until you separate the two things it is not about: a
runner that is *dead* is caught by the heartbeat below (three missed beats,
about 90 seconds) no matter what a start is waiting for. This bound is about
slowness alone, and on covey.work two minutes turned the first wake on a fresh
host into `did not answer start_sandbox: context deadline exceeded` while the
pull was still running. Whoever wants it tighter sets the variable; the button
on the runner page (*fetch images in advance*) makes the wait happen while
somebody is watching instead.

**"Connected" and "answering" are two states.** The heartbeat below proves the
process lives; it says nothing about whether the runner still reads its
messages. A host stuck inside a long `docker run` keeps beating and answers
nothing, and the control plane notices that separately: it asks every connected
host for its capacity once a beat, and after three unanswered questions the host
shows as **not answering** and is no longer given new sandboxes. Whatever is
already running there may finish. If that is the only runner, the built-in one
steps in — which is why an agent keeps working instead of waiting out the start
timeout on a machine that reads nothing.

Once running, the runner reports in every 30 seconds. After three missed beats
the control plane closes the connection, and the runner shows as **offline**
rather than as a host that is there but hears nothing — the state in which every
wake would sit out its timeout before failing.

As a service — and this is not an optional finish. A runner started by hand
runs in the shell it was started from; the SSH session ends and the host shows
as **offline**, the state in which its agents wait rather than fail. So the
binary does it itself:

```
covey-runner install-service        # writes the unit, enables and starts it
covey-runner remove-service         # undoes it, registration and config stay
```

On a systemd host, run as root, `register` already does it — the registration
ends with a running service rather than with a command to type. `--no-service`
leaves it out. `install-service` takes `--config` (a configuration elsewhere),
`--user` (a user of its own; it is added to the `docker` group, which is
practically the same power with a name in front of it), `--no-start` and
`--print`.

`--print` is the way out for a host without systemd: it writes the unit and
changes nothing, so the text can be adapted for another init system. That is
also what the command does on its own when it finds no systemd — a runner on a
machine that runs something else needs the text, not a refusal.

```ini
[Unit]
Description=covey runner
After=network-online.target docker.service
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/covey-runner run --config /etc/covey-runner/config.toml
Restart=always
RestartSec=5
TimeoutStopSec=60

[Install]
WantedBy=multi-user.target
```

## Services beside a sandbox

An agent may declare what has to run **beside** its sandbox: the database a test
suite needs, the queue an application talks to. The runner brings those
containers up with the sandbox, on a network belonging to that sandbox alone,
and the agent reaches each one under its name — `db:5432`, exactly as the
project's own `docker-compose.yml` writes it.

**Which images may run is your decision, once.** The organisation keeps an
allowlist under *Infrastructure → Workplaces → Services beside the sandbox*, and
it governs every path — what a manager types as much as what an agent derives
from a project's compose file. Patterns are an exact reference (`postgres:16`)
or a star bound to a separator (`postgres:*`, `ghcr.io/acme/*`); `*` alone
allows everything. An empty list allows nothing, which is the safe default for a
fresh installation — the upgrade seeded what your agents already declared, so
nothing that ran yesterday stopped.

A refusal names the pattern that would let the image run, so answering it is one
line rather than a trip to this page. It is checked when the declaration is
saved and again at the wake: the second one refuses the whole wake rather than
starting a sandbox with two of its three services.

Set in the agent's settings under **Services**, or through the API:

```bash
curl -X PATCH https://covey.example.com/api/v1/agents/$AGENT/services \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"services":[{"name":"db","image":"postgres:16",
                    "env":{"POSTGRES_PASSWORD":"test"}}]}'
```

Three things are worth knowing before you use it:

- **It costs memory on the host, and nothing counts it.** The capacity figure
  above counts sandboxes, not the containers standing beside them. A Postgres
  and a Redis per agent are real memory on that runner, and an agent set to
  three services is three containers per wake — plan the host for it rather than
  discovering it under load. It also costs one docker network per sandbox, and
  the daemon's default address pool holds only a few dozen of those: a host
  carrying many concurrent sandboxes with services wants
  `default-address-pools` widened in `daemon.json` before it runs out, because
  the error when it does names the network and not the cause.
- **The services have no way out.** Their network is `--internal`: no internet,
  by construction and not by allowlist. An image that wants to fetch something
  at startup will not work, and that is deliberate — a test database has no
  business outside. The sandbox's own way out is unaffected; it keeps the route
  the egress configuration gives it.
- **Their state ends with the sandbox.** Every path that ends a sandbox ends
  them — the clean stop, a crash, and the start of the next sandbox. That is the
  point rather than a limitation: what an agent keeps belongs in its home, and a
  database that survived a run would hand the next one a state nobody wrote
  down. Anything you would miss does not belong in a service.

**An agent can bring up its project's own services.** Give it
`- system: covey scope: services:write` in its ACCESS.md and it may send the
content of a repository's `docker-compose.yml` and get what stands in it — the
database, the cache, the queue. It still chooses only among the images your
allowlist permits, so this grants no new reach: the privileged act stays
extending the list. Without that scope the agent neither reads about the action
in its prompt nor may call it.

Useful to know when you read such a run: the project's own application is
normally *skipped*, because it is built from the source the agent has and
belongs inside its sandbox rather than beside it. And a service the allowlist
refuses is reported to the agent by name, so it can tell you instead of quietly
producing a result that means nothing.

**What actually ran is in the recording, per job.** The host reports which image
each service started from — the image id, not only the tag it was configured
with — and that is recorded twice: once at the wake (what came up, or what was
refused), and once at the start of every job (what this run worked against). The
second one exists because a warm sandbox serves job after job on services that
came up hours earlier; without it, a run whose result nobody can reproduce would
point at a waking phase that has long scrolled off.

A service that cannot start **ends the wake** and takes the ones already up with
it, with the reason in the agent's recording. Half a workplace is the state in
which an agent reports the wrong defect — it finds the queue missing and writes
that into a merge request, while the fault was a typo in an image reference.

The images are the project's, not covey's: they come from wherever the reference
points, so a host behind a proxy or without access to that registry fails here
with docker's own words. The declaration itself is checked when it is saved — a
name has to be a host name, because that is what it becomes.

## The runner view

**Runners** shows, per host: whether it is connected, which tags and images it
carries, how many sandboxes are running on it, and how full the file system
holding its working copies is. The built-in runner is in that list too — visible
so the model is comprehensible, but with no token to revoke and no delete
button.

Two things it says that are easy to miss:

- **outdated protocol** — the runner speaks an older one than this control
  plane. Runner and server are delivered separately, so version drift is
  normal; it is named rather than merely tolerated.
- **offline** — the row stays, and so does everything on it: token, tags,
  working copies. The agents move to the next host that fits, and to the
  built-in one if none does.
- **not answering** — the line is open and the host reacts to nothing. It gets
  no new sandboxes until it answers again; what runs there may finish. This is
  the state a stuck read loop produces, and it used to read as *connected*. A
  start that is already on its way to such a host is taken back rather than
  waited out: the agent moves to the next host within about 90 seconds instead
  of standing still for the start timeout.
- **paused** — somebody took it out of service. The only one of these states
  that is a decision, which is why it is named separately from all of them.

An agent asks for a host's capabilities through **Host requirements** in its
settings (`arm64`, `gpu`, a runner inside the target system's network). Empty is
the normal case and means any runner of the organisation.

## The log

A runner writes to its own stderr, and under systemd that means journald on a
machine somebody has to have a shell on. For the one component that
deliberately stands on a host the control plane does not own, that is the wrong
place: "why did that host stop taking sandboxes at three in the morning" was a
question only an SSH session could answer, and only for as long as the journal
kept it.

The same lines now go two ways. The host's own log is unchanged — whoever
debugs there keeps what they had. Beside it the runner buffers and ships them
up the link it already holds, and the **Log** section on the runner's page is
where you read them.

### Two levels, and they are not the same one

| Control | What it decides | Where it acts |
|---|---|---|
| **The host reports** | what the runner SENDS | on the runner, over the protocol |
| **Shown** | what this page DISPLAYS | in the browser |

Confusing the two is how you end up switching a host to debug and still seeing
nothing — the filter above the list was left at info. Both are on the page next
to each other for exactly that reason.

Normal is `info`. `debug` adds one line per protocol message and the intermediate
steps of a start; it is what you switch on while you are looking at a problem on
one particular host, and off again afterwards, because it writes hundreds of
lines per start.

The level lives on the runner's row, not only in the message. A host somebody
switched to debug that drops out for a minute comes back at debug — otherwise
the switch would be showing a state that is not the world's.

### What ends up in it

- The connection: registered, reconnected, the protocol version refused.
- Every sandbox start with its image, and the line that explains an hour —
  *image not present, docker will fetch it.*
- The home: materialised with bytes and milliseconds, synced with block counts.
- Every failure with the words the underlying tool used. A wrapped "start
  failed" hides whether it was "no such image", "port in use" or "no space left
  on device", and those three call for three different people.
- A sandbox that ended without being asked to, with the reason, while the
  container's own words still exist.
- The progress of a start (image, home) — the same lines that go into the
  agent's recording. They belong in both places: the recording answers "what
  happened to this agent", the runner's log answers "what is this host doing",
  and a twenty-minute image pull is a question of the second kind.

### Limits

The buffer on the runner is a ring. A host that loses its connection keeps
working and keeps logging, and an unbounded buffer would turn a network problem
into an out-of-memory one on the machine that is currently running somebody's
sandboxes. What overflows is the oldest line, and how many were lost travels
with the next batch — a number, once, instead of a silence that reads like a
quiet host.

In the database the log is capped twice: by age (14 days) and by count per
runner (50,000 lines). Either alone has a hole. Age alone lets a host that logs
in a loop fill the disk inside its window; count alone keeps a quiet runner's
lines from three months ago and calls that a log. Whichever bites first is the
right one.

## What a runner needs, and what it must never have

It needs Docker on its host and a way out to the control plane. That is all.

- **No database access, no object store credentials.** A runner speaks
  exclusively the runner protocol and fetches its blocks through the control
  plane's runner API, authenticated with its own token.
- **It dials out.** No inbound reachability, no port to open — the direction is
  what makes this practical in the first place.
- **TLS is mandatory** as soon as a runner is not on the control plane's own
  machine: `COVEY_PUBLIC_URL` over plaintext HTTP would be the disclosure of
  every daemon token that travels in a `start_sandbox`.
- **It is trusted infrastructure of the organisation.** With a sandbox it
  receives that agent's daemon and egress tokens, so it *can* impersonate every
  agent it hosts — the same trust level as a CI runner that sees job tokens. A
  runner is not a way of bringing in untrusted compute capacity.

A runner belongs to exactly one organisation, inherited from the registration
token and unchangeable. Whoever wants to serve two starts two processes.

## The home store

After every job an agent's home is synced into a central, content-addressed
store as a whole and materialised from it on wake. That is what makes a runner
switch cost time instead of work.

- `COVEY_HOME_STORE=false` turns it off. Then homes stay directories on the
  runner: no snapshots, no rollback, and unrecoverable when lost.
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
- The blocks live under `<COVEY_DATA_DIR>/blocks`. **This directory needs
  backup like the database.** 99 % of it would only have to be downloaded
  again; the rest exists nowhere else. It is a cache in its function, not in
  its need for protection.

### Blocks in an object store

`COVEY_BLOB_STORE=s3` puts the blocks into an S3-compatible store instead —
for durability, replication, or simply to keep them off the control plane's
disk. The default stays the directory: for an installation on one machine an
object store is unnecessary operational surface.

```bash
COVEY_BLOB_STORE=s3
COVEY_S3_ENDPOINT=https://s3.eu-central-1.example.com
COVEY_S3_BUCKET=covey-blocks
COVEY_S3_ACCESS_KEY=…
COVEY_S3_SECRET_KEY=…
COVEY_S3_REGION=eu-central-1     # servers without regions still want one signed
COVEY_S3_PREFIX=covey            # optional, for a bucket that holds more
COVEY_S3_PATH_STYLE=true         # default; false addresses the bucket in the host
```

**S3-compatible, not "AWS S3".** The protocol is the common denominator of
Hetzner Object Storage, Garage, MinIO, Ceph RadosGW and SeaweedFS — covey does
not prescribe a server. Whatever your hoster offers is usually the right answer;
otherwise Garage, which is built for exactly this kind of small self-operated
cluster.

At startup the store is probed once (write, read, delete). A wrong key or a
missing bucket therefore says so in the log at start, not inside the recording
of the first agent that tried to fall asleep. It is a warning and not an abort:
everything that is not a run keeps working, and an object store that comes back
in two minutes is a normal case.

Switching backends does **not** move the blocks. Point a running installation at
an empty bucket and the snapshots stay in the database while their content is
gone — copy the block directory over first, or accept that every home is
rebuilt from scratch on its next wake.

There is **one snapshot per agent**, replaced by every sync. There is nothing to
configure about that and nothing to choose from: the store answers "where is
this home now", not "where was it on Thursday".

That is what makes the cleanup a permanent job rather than an occasional tidy-up.
Every sync replaces a manifest, and the blocks only that one still referenced are
garbage from that moment — so the store grows with how often the agents work,
not with a history somebody kept. The same goes for a deleted agent, whose row
went with it and whose blocks stayed. Measured on one installation: 151,898 of
152,178 blocks were unreferenced, 9.3 of 9.8 GB.

So the control plane sweeps **by itself**, every six hours, for every
organisation. An installation whose admin never opens the page is exactly the one
that runs out of disk.

*Preview* measures what a cleanup would free before anything happens. That number
is the space **actually** freed and not the size of what is removed: a block
belongs to no single home, so it goes only when nothing references it. Any other
figure would be one that is never right.

Whoever needs the space now, or has no browser at hand, runs the same pass from
the command line. It reports per organisation and deletes nothing without
`--apply`:

```bash
covey home-store cleanup            # preview: what would be freed
covey home-store cleanup --apply    # and now for real
```

Per agent, next to the file browser, the **home store** panel shows how big the
home is, how much of it only that agent holds (the figure that says whether a
loss costs time or work), what the last sync cost, and the largest directories —
which is also where you find the candidates for `COVEY_HOME_EXCLUDES`. *Back up
now* forces a sync — before a maintenance window, or simply because somebody
wants the current state safe.

There is no restore, because there is nothing to pick from. The way back after
an agent has wrecked its own home and synced that state is the backup of the
block directory on the host — the one the deploy writes before it brings the new
version up. That is a deliberate trade, and it puts weight on that backup: check
that it exists and is not empty.

The store's fill level is on the dashboard, so it is seen before the disk runs
short rather than after.

## Hard egress isolation with runners

In `COVEY_EGRESS_ISOLATION=network` the internal network and the proxy
container exist **once per runner** (`covey-egress-internal-<runner>`,
`covey-egress-proxy-<runner>`). Since a runner serves one organisation, two
tenants never share a network segment — `--internal` cuts the way out, not the
way sideways.

The proxy fetches its allowlist from the control plane with the runner token.
It is renewed once per control-plane start: the built-in runner's token is
rolled at every start, and a leftover container would carry the old one and —
fail-closed, correctly — block everything.

## Behind a reverse proxy

The runner dials **out** — one WebSocket to `/api/runner/ws`, held open. That is
what makes a remote host practical: it needs no inbound reachability, only a way
out. But the connection is an HTTP **upgrade**, and a proxy that does not
forward it turns the runner into a host that registers and then never connects.

The symptom is unambiguous once you know it: registration succeeds (that is
plain HTTP), and the connection fails with **426**. The 426 comes from covey
itself — the request reached it without the upgrade headers, so something in
between dropped them.

For nginx, two lines are usually missing:

```nginx
map $http_upgrade $connection_upgrade { default upgrade; '' close; }   # once, in http { }

location / {
    proxy_pass http://127.0.0.1:8494;
    proxy_http_version 1.1;
    proxy_set_header Upgrade    $http_upgrade;
    proxy_set_header Connection $connection_upgrade;
    proxy_set_header Host       $host;
    proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_read_timeout 3600s;   # the connection is long-lived by design
}
```

`proxy_read_timeout` matters as much as the upgrade itself: a runner that is
idle for a while is not a runner that is gone, and a proxy that closes the
connection after 60 seconds produces a host that reconnects all day for no
reason. Traefik and Caddy forward the upgrade by themselves; there the timeout
is the only thing to check.

This concerns the **remote** runner only. The sandbox daemon connects to
`COVEY_WS_URL`, which points inward at the control plane and normally does not
pass a proxy at all.

## When nothing runs

The message says which of the three it is, because they call for different
things:

| Message | What to do |
|---|---|
| *this organisation has no runner* | the built-in one is switched off and none is registered |
| *no runner carries the tags …* | give a host that tag (runner view), or loosen the agent's host requirements |
| *every runner is offline* | the host is down or cannot reach the control plane |
| *426 to the WebSocket handshake* | a proxy in front of the instance is not forwarding the upgrade — see above |

When no connected runner fits, the built-in one steps in and the log says so
(`COVEY_BUILTIN_RUNNER=off` prevents it). What it does **not** do is satisfy a
tag: register an `arm64` build host and give an agent the requirement `arm64`,
and a control plane that is not `arm64` remains no candidate — the message
names the tag. A tag says what a host **is**, and no substitute machine changes
that.

## The file browser

An agent's home is reachable from the interface no matter where it lies. Three
cases, and the interface says which one it is in:

| Situation | What you get |
|---|---|
| The runner holding the home is connected | the live working copy: read and write |
| It is not connected | the last snapshot: **read-only**, marked as such in the listing |
| No runner and no snapshot | "no reachable home" — an agent that has never run |

The second case is the one worth knowing about, because it is when you most
want it: looking at the work of an agent whose host is down. Writing is refused
there with a 409 and a reason. That is not caution — a change to a snapshot
would be a second state beside the working copy that is coming back, and
nothing could then say which of the two is the home.

**What you write is synced.** An upload lands in the runner's working copy, but
the next wake materialises the snapshot — and on another runner the snapshot is
all there is. So a change through the browser triggers a sync: debounced by a
few seconds, so a fifty-file drag-and-drop is one sync and not fifty, and
forced before the next start of the sandbox and at shutdown of the control
plane. If you ever see an upload survive in the browser but not in the agent's
next run, that chain is where to look.
