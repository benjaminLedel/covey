# Runner — sandboxes on more than one host

The data plane does not have to live on the control plane's machine. A
**runner** is a process on an arbitrary host that registers with the control
plane and gets sandboxes assigned to it from there. The full model is in
[`spec/16-runner.md`](../spec/16-runner.md); this page is the operational part.

## You already have one

Every sandbox starts through the runner protocol, including on a single
machine: `covey serve` runs a **built-in runner** in its own process, one per
organisation. Nothing to install, no token, no configuration file — the normal
installation notices none of this.

`COVEY_RUNNER_LOCAL` is not a switch you need. What matters is the rule:

> **An organisation's built-in runner stands down as soon as it has a
> registered one — and steps back in when no connected runner fits.**

Whoever adds a runner has said that compute leaves this machine, and that is
what the first half is for. The second half exists because the first one alone
cost a production instance its whole data plane: the registered host claimed
`covey-sandbox:latest`, the agents needed the deploy image, and every wake
failed every 30 seconds for half an hour while the machine that held that image
stood idle. A mixed pool is a trade-off; a control plane that stops because
somebody added a host is not.

The fallback is not quiet. It writes `no connected runner fits — the built-in
one takes it` with the image and tags it was looking for, and it cannot smuggle
work past a requirement: **tags still exclude it.** An agent that asks for `gpu`
waits for a host with `gpu`.

`COVEY_BUILTIN_RUNNER=off` turns the fallback off for good — for an instance
whose compute must not touch the control plane's machine, said once and
deliberately rather than following from the existence of another host.

**Offline is not "no runner".** The rule counts registered runners, not
connected ones. A maintenance window on your only runner does not move the
workforce back onto the control plane's host; the agents wait instead.

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
Covey version so that any host can. Pulling it there in advance (`make
sandbox-image`, or from your registry) only makes the first wake faster.

The one case where that is not automatic: an image in a **private** registry.
The host needs credentials for it (`docker login`), otherwise the start fails
there — and the sandbox moves to the next runner rather than being lost.

`register` writes `/etc/covey-runner/config.toml` (overridable with
`--config`): the server address, the runner token, the working directory, and
what the host says about itself — `--tag arm64` says what it **is**, `--image`
which workplaces it provides. The token is what this host acts for its
organisation with; the file is written `0600` and belongs treated as such.

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
Description=Covey runner
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
- **offline** — the row stays. Offline is not "no runner", so the built-in one
  does not come back and the agents wait.

An agent asks for a host's capabilities through **Host requirements** in its
settings (`arm64`, `gpu`, a runner inside the target system's network). Empty is
the normal case and means any runner of the organisation.

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
- `COVEY_HOME_EXCLUDES=".dartServer,…"` leaves paths out. The default is empty
  on purpose — the list is a cost question, not a prerequisite for
  correctness. Only demonstrably derivable paths belong in it.
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
Hetzner Object Storage, Garage, MinIO, Ceph RadosGW and SeaweedFS — Covey does
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
plain HTTP), and the connection fails with **426**. The 426 comes from Covey
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
