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

> **An organisation has the built-in runner exactly as long as it has no
> registered one.** Registering the first runner ends it; deleting the last one
> brings it back.

Whoever adds a runner has said that compute leaves this machine. The
alternative would be a mixed pool — some agents on the registered hosts, some
on the control plane's, decided by a scheduling preference nobody remembers
making.

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

The host also needs the sandbox image the agents work in — `make sandbox-image`
there, or pull it from your registry. A runner reports which images it holds and
gets only matching agents, so a host without the image is simply not a candidate
rather than a failure at wake time.

`register` writes `/etc/covey-runner/config.toml` (overridable with
`--config`): the server address, the runner token, the working directory, and
the tags and images this host holds. Both are capability statements —
`--tag arm64` says what this host is, `--image` says which workplaces it can
provide, and the scheduler assigns only agents that fit both. An agent asks for
tags through `PATCH /api/v1/agents/{id}/runner-tags`; empty is the normal case
and means any runner of the organisation. The token is what this host acts for its
organisation with — the file is written `0600` and belongs treated as such.

`register` checks the connection before it reports success — the WebSocket, not
just the HTTP call that created the runner. If that fails, the registration
still stands and the configuration is written; fix the connection and start it
with `covey-runner run`.

Once running, the runner reports in every 30 seconds. After three missed beats
the control plane closes the connection, and the runner shows as **offline**
rather than as a host that is there but hears nothing — the state in which every
wake would sit out its timeout before failing.

As a service:

```ini
[Unit]
Description=Covey runner
After=network-online.target docker.service

[Service]
ExecStart=/usr/local/bin/covey-runner run
Restart=always
RestartSec=5

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

Snapshots accumulate. Retention is set per organisation under **Runners** — the
last *N* per agent and a maximum age, with every agent's most recent snapshot
always kept. *Preview* measures what a cleanup would free before anything
happens.

That number is the space **actually** freed and not the sum of the snapshot
sizes: a block belongs to no single snapshot, so removing one frees only what
nothing else references. Any other figure would be one that is never right.

The rules are enforced **without anybody pressing anything**: the control plane
runs the same pass every six hours, for every organisation. That matters because
the store also grows in ways no retention rule describes — a deleted agent takes
its snapshot rows with it and leaves its blocks behind — and an installation
whose admin never opens the page is exactly the one that runs out of disk.

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
now* forces a sync; a snapshot in the list can be restored, but only while the
agent is asleep — otherwise the running sandbox would write into a home that
changes underneath it.

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
| *no runner holds this image* | build the image on the runner, or change the agent's profile |
| *every runner is offline* | the host is down or cannot reach the control plane |
| *426 to the WebSocket handshake* | a proxy in front of the instance is not forwarding the upgrade — see above |

There is deliberately **no fallback** onto the built-in runner when a
registered one does not fit: that would restore the mixed pool through the back
door. Register an `arm64` build host and nothing else, and your developer
agents have candidates on paper and none in fact — the message says so.

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
