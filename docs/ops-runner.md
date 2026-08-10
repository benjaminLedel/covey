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

On the control plane, create a registration token for the organisation
(currently through the API; the runner view in the UI follows):

```bash
curl -X POST https://covey.example/api/v1/runners/registration-tokens \
     -H 'Content-Type: application/json' --cookie "$SESSION" \
     -d '{"description":"Build host Frankfurt"}'
```

On the new host — Docker, the sandbox image, and the runner binary:

```bash
make sandbox-image                       # or pull it; the runner needs the image locally
covey-runner register --url https://covey.example --token <registration token> \
                      --description "Build host Frankfurt" --tag arm64
covey-runner run
```

`register` writes `/etc/covey-runner/config.toml` (overridable with
`--config`): the server address, the runner token, the working directory and
the image this host holds. The token is what this host acts for its
organisation with — the file is written `0600` and belongs treated as such.

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

Snapshots accumulate. Retention is applied per organisation (the last *N* per
agent, a maximum age, and always the most recent one); the blocks are freed by
a sweep over the surviving manifests — which is why deleting a snapshot does
not free space linearly.

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

## When nothing runs

The message says which of the three it is, because they call for different
things:

| Message | What to do |
|---|---|
| *this organisation has no runner* | the built-in one is switched off and none is registered |
| *no runner holds this image* | build the image on the runner, or change the agent's profile |
| *every runner is offline* | the host is down or cannot reach the control plane |

There is deliberately **no fallback** onto the built-in runner when a
registered one does not fit: that would restore the mixed pool through the back
door. Register an `arm64` build host and nothing else, and your developer
agents have candidates on paper and none in fact — the message says so.
