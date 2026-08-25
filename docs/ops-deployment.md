# Operations: automatic deployment (main → host)

Every push to `main` rolls Covey out onto a target host automatically. This
runbook describes the one-off setup and what happens in ongoing operation.

> For trying things out locally without CI see
> [`quickstart-docker.md`](quickstart-docker.md) instead.

---

## How it works

The pipeline (`.gitlab-ci.yml`) has three stages: `test → build → deploy`.

1. **build** builds **one** Docker image and pushes it into the registry, among
   others onto the **immutable commit tag** `:$CI_COMMIT_SHORT_SHA` — that is
   the "special tag" the deployment pins to: `…/covey`, the control plane
   ([`Dockerfile`](../Dockerfile)).

   The sandbox images are **not** built here any more. They are built and
   published by [`../.github/workflows/sandbox-images.yml`](../.github/workflows/sandbox-images.yml)
   on every push to `main` and for every tag — public on ghcr, listed in the
   workplace catalogue and pinned by digest. Two jobs and some twenty minutes
   per pipeline went with them.
2. **deploy** runs on a **shell runner directly on the target host**
   (runner tag `covey-deploy`). The job:
   - copies [`docker-compose.deploy.yml`](../docker-compose.deploy.yml) to
     `$DEPLOY_DIR/docker-compose.yml` (default `/opt/covey`),
   - creates, on the **first** deploy, a one-off `.env` with a random
     master key + passwords (never touched again afterwards),
   - sets `COVEY_IMAGE` to `…/covey:$CI_COMMIT_SHORT_SHA` — and **nothing**
     about the workplaces: their images come from the published catalogue
     ([`../spec/16-runner.md`](../spec/16-runner.md)), pinned by digest per
     Covey version and public on ghcr, and a runner fetches what it is
     missing. The job used to pull both sandbox images and pin them through
     `COVEY_SANDBOX_IMAGE` / `COVEY_SANDBOX_IMAGE_DEV`, because the covey
     container has no credentials for the private registry. With the first
     runner of your own that stopped being a shortcut and became an exclusion:
     that host has no credentials either, and should not get any. The two
     variables still win where somebody sets them — a fork, an installation
     without internet — they are simply no longer set here, and the deploy
     removes leftovers of them from the `.env`,
   - `docker compose pull && docker compose up -d`.

### Sandbox isolation in the deployment

The deployment uses the **docker SandboxProvider**: for every agent wake the
control plane starts a sibling container from the sandbox image. For that, the
host's Docker socket is mounted into the covey container in the Compose file,
and the data directory (`COVEY_DATA_DIR`, default `/opt/covey/data`) sits as a
bind mount at an **identical path** on the host and in the container — only
that way are the `-v` paths of the agent homes, which the covey container hands
to the host daemon, correct. The sandbox image is pulled in the deploy job
(that is where the registry login exists); the control plane itself never
pulls.

Migrations run automatically at `serve` start (an advisory lock). The
`bootstrap` service creates the organisation/admin idempotently and blocks the
control plane's start until it is through.

Deploy Compose vs. local Compose:

| File | Purpose | Image |
|---|---|---|
| `docker-compose.yml` | trying things out locally | `build: .` (builds locally) |
| `docker-compose.deploy.yml` | host deployment via CI | `image: ${COVEY_IMAGE}` (from the registry) |

### What belongs in the sandbox image — and what does not

A developer agent works on several projects with different technologies and
versions. The sandbox, however, hangs off the **agent**, not the project: the
container starts on wake, before it is settled which ticket from which project
comes up. One image per project is therefore not a viable route.
Instead:

> **Version → home, toolchain → image.**

| Layer | Contents | Lifetime |
|---|---|---|
| Image | System packages (PHP, JDK) and the version managers themselves | per build |
| Home `/home/agent` | The SDK versions the project pins | persistent per agent |
| Checkout | `vendor/`, `node_modules/`, the Gradle cache | persistent per agent |

The image therefore brings `fvm` (Flutter/Dart) and `uv` (Python) along, but
**no SDKs**: every agent fetches those into its home when first needed, steered
by the version written in the project repo (`.fvmrc`,
`.python-version`, `gradle-wrapper.properties`). Because the home is
persistent, that happens once and not on every run.

**When extending the image, note:** the control plane mounts the home over
`/home/agent`. Anything an installer writes there at build time is masked and
invisible at runtime — tools belong in a system path
(`/usr/local/bin`, `/opt`), their caches in the home. For the same reason
SDKMAN cannot be pre-installed; by construction it lives in `$HOME/.sdkman` and
is installed by the agent itself when needed (and then stays there).

**Installing at runtime is not a route.** The agent runs as non-root, and a
package manager on the egress allowlist would be a generic code-execution
channel. The reasoning is **D11** in
[`spec/07-open-decisions.md`](../spec/07-open-decisions.md).

### Egress for developer agents

In isolation mode `network` the sandbox reaches only what is on the allowlist —
without the right templates, `composer install`, `fvm install` and `gradlew`
fail. The built-in catalogue (`internal/egress/builtin.go`) holds ready-made
host sets for that; a developer agent is usually assigned:

| Template | What for |
|---|---|
| GitHub | Git clones and release downloads. **Practically always needed:** `fvm`, Composer VCS packages, `uv`'s CPython, the Gradle wrapper and the JDK toolchains all end up on GitHub Releases |
| PHP / Composer | `packagist.org` (the dist archives come via GitHub) |
| Dart / Flutter | `pub.dev` and the SDK artefacts |
| Maven / Gradle | Maven Central, the plugin portal, wrapper distributions, JDK toolchains |
| Android / Google Maven | Android dependencies — for Gradle **and** Flutter builds |
| Node.js / npm | `registry.npmjs.org` |

Plus your own GitLab or Zammad as an org-owned template — those deliberately do
not sit in the catalogue.

Two things that regularly cost time when tailoring the allowlist:

- **The proxy follows no redirect.** It sees only the CONNECT host of the
  respective connection, and it is fail-closed. If a service redirects to
  another host, the target has to be on the list too — that is why
  `plugins-artifacts.gradle.org` sits next to `plugins.gradle.org`, and why
  the GitHub template is practically mandatory.
- **`storage.googleapis.com` in the Flutter template is broad.** The proxy does
  not terminate TLS and therefore cannot filter on paths — the entry opens
  every publicly readable GCS bucket, not just the Flutter artefacts. For
  agents where that is not acceptable, the route goes through a mirror of your
  own and `FLUTTER_STORAGE_BASE_URL` in the sandbox rather than through this
  template.

---

## One-off setup

### 1. A shell runner on the target host

A GitLab runner with **executor `shell`** has to run on the target host and be
registered with the tag `covey-deploy`. The host must have installed:

- **Docker** + the **Compose plugin** (`docker compose version`),
- the runner user must be allowed to run Docker (the `docker` group),
- **OpenSSL** (for the one-off `.env` generation).

```bash
# example registration (on the host):
gitlab-runner register \
  --url https://gitlab.lapco.legal \
  --registration-token <TOKEN> \
  --executor shell \
  --tag-list covey-deploy \
  --description "covey-host"
```

### 2. CI/CD variables (optional)

In GitLab under **Settings → CI/CD → Variables**:

| Variable | Default | Purpose |
|---|---|---|
| `DEPLOY_DIR` | `/opt/covey` | The target folder on the host |
| `COVEY_PUBLIC_URL` | `http://localhost:8494` | The public URL — written into the `.env` only on the **first** deploy |

> **`COVEY_PUBLIC_URL` is an operational address, not a marketing address.**
> The `COVEY_WS_URL` with which every sandbox connects back to the control
> plane is built from it. With the docker provider a loopback host is rewritten
> to `host.docker.internal` in the process — it does not become a public name.
> If you enter the website's domain here, the sandboxes dial back over the open
> network, where the egress allowlist stops them: all agents then fail with
> `daemon hat sich nicht verbunden (timeout 1m0s)`.
> The value has to be reachable **from the sandbox**, nothing else.

Everything this instance addresses **outward**, by contrast, hangs off
`COVEY_SITE_URL`: `canonical`, `hreflang`, `sitemap.xml`, `robots.txt`, the
webhook and trigger URLs for copying, and the target URL in the downloadable
skill. **Leaving it empty is the normal case** — the server then derives the
address from the request (the host header plus `X-Forwarded-Proto`). Since the
interface is called under the right domain, the URLs displayed are then correct
by themselves. Set it only when the reverse proxy does not pass the origin
through and `http://` or an internal name would otherwise land in the sitemap.

In short: `COVEY_PUBLIC_URL` points **inward** (to the sandboxes),
`COVEY_SITE_URL` points **outward** (to visitors and third-party systems).

### `COVEY_TRUSTED_PROXIES` — who a request comes from

Behind a reverse proxy every request arrives from the same address: the proxy's.
Whatever is counted per client then lands in one shared bucket — the sign-up
limit throttles the whole installation at once, the login limit loses its
per-address half, and the audit log records the proxy as the actor of every
administrative action.

`COVEY_TRUSTED_PROXIES` names the addresses whose `X-Forwarded-For` may be
believed: comma-separated, as a CIDR (`10.0.0.0/8`) or a single address
(`10.0.0.2`), and `private` as shorthand for loopback plus the private ranges —
the usual case, because a proxy in the same docker network gets its address
assigned rather than chosen.

```
COVEY_TRUSTED_PROXIES=private
```

**Leaving it empty is the safe default**, and it is the right setting for an
instance reachable directly: the header is written by whoever sends the request,
so believing it unconditionally would let every attacker pick their own bucket.
Configured, the server reads the chain from the right — the proxy appends the
peer it actually saw, so an invented `X-Forwarded-For: 1.2.3.4` ends up to the
left of the real address and is passed over.

The registry login runs automatically through the built-in `$CI_REGISTRY_*`.

### 3. The first push to main

On the first deploy the job creates `$DEPLOY_DIR/.env` with a random master
key and Postgres and admin passwords. **Back this `.env` up immediately** — the
master key en/decrypts all deposited secrets; if it is lost, all brokered
credentials are unreadable. The generated admin password is in there too:

```bash
sudo cat /opt/covey/.env      # read off / back up the admin password + master key
```

---

## Ongoing operation

```bash
cd /opt/covey
docker compose ps                 # status
docker compose logs -f covey      # live logs
docker compose down               # stop (data stays in the volumes)
```

Every further push to main pulls the new images and restarts. The DB volume
(`covey-db`), the agent homes (`/opt/covey/data/homes/…`) and the `.env` are
preserved.

### Checking configurations after an upgrade

The platform's share of an agent's system prompt is compiled at dispatch time,
so every agent gets a new platform contract with the deploy. What a human wrote
— `SOUL.md`, `PLAYBOOKS.md`, `HEARTBEAT.md` — stays as it is, and it can fall out
of step with it. `covey config lint` names the agents that need catching up,
across all organizations, and exits with `1` when there are findings — enough
for an upgrade script:

```bash
docker compose exec covey /covey config lint          # readable
docker compose exec covey /covey config lint --json   # for a pipeline
```

The same findings stand on each agent's own page, above the tabs — that is where
they are read when somebody looks after an agent because something is wrong with
it.

### Turning the MCP route on for target actions

By default an agent issues a target action as a shell command (`curl` against
the local action proxy). The proxy additionally speaks MCP, and on that route
the same action is a typed tool call — no JSON inside a quoted shell string,
which is where a good part of the wasted turns come from.

It is **opt-in**, because a failed handshake would take all target actions with
it: set `COVEY_ACTION_MCP=1` in the `.env`, restart, and watch one agent's
recording for a run (the actions appear as `mcp__covey` tool calls instead of
`Bash`). The shell route stays open either way, so an agent config that
describes it keeps working.

### Emergency password reset

You change your own password in the UI (account settings); other people's are
reset by the org admin under *Administration → Members & roles*. If the **admin
themselves** is locked out, `covey passwd` directly on the host helps — it sets
the password anew in the DB and invalidates all the user's running sessions:

```bash
cd /opt/covey
docker compose run --rm covey passwd admin@covey.local
# → asks for the new password interactively (without echo);
#   non-interactively: echo 'new-password' | docker compose run --rm -T covey passwd admin@covey.local
```

### Rollback

Back to an earlier commit tag (the tags are in the registry):

```bash
cd /opt/covey
COVEY_IMAGE=<registry>/covey:<older-sha> docker compose up -d
```

---

## Wiki memory: choosing an embedding

The agents' wiki search (see [`spec/05`](../spec/05-memory.md)) sits on a
vector index. Which embedding fills it is decided by
`COVEY_EMBEDDING_PROVIDER` in the deploy folder's `.env`.

The default `builtin` needs nothing and can do little: it measures word
overlap, not meaning. "The pipeline is red" and "The CI build is failing" have
nothing to do with each other as far as it is concerned. An agent therefore
does not find its own page again as soon as it phrases things differently — and
creates a second one. For real operation that is not enough.

**Run it yourself** (recommended — the wiki contents do not leave the house):

```bash
# in /opt/covey/.env
COMPOSE_PROFILES=embeddings
COVEY_EMBEDDING_PROVIDER=ollama
```

That starts two additional containers: an embedding server and a one-off job
that loads the model. The default is EmbeddingGemma — 308M parameters,
multilingual, runs on the CPU, no key, no egress to third parties. A different
model through `COVEY_EMBEDDING_MODEL`; it has to be able to deliver the 256
dimensions the schema expects (Matryoshka-trained models do; otherwise it is
truncated and re-normalised).

**Use a third-party service:**

```bash
COVEY_EMBEDDING_PROVIDER=voyage      # or openai
COVEY_EMBEDDING_API_KEY=…
```

Then `docker compose up -d`. At start the control plane re-embeds the existing
corpus automatically — vectors from different models are not comparable, which
is why every page carries its model's fingerprint.
In the log:

```
wiki-embedding aktiv                      modell=ollama:embeddinggemma:256
wiki-embedding: Bestand wird nachgezogen  seiten=52
wiki-embedding: Bestand nachgezogen       seiten=52
```

Until that has run through, the search does not find the affected pages; they
are not lost, though. If the embedding service is not ready yet at start (it
loads the model the first time), the control plane retries every minute.

### The cleanup heartbeat

`COVEY_WIKI_CLEANUP` (default `03:00`) creates a maintenance task for every
agent daily: merge similar pages, fix dead `[[references]]`, smooth out
contradictions. That costs **one LLM run per agent per day** — a noticeable item
with many agents. Switch it off with an empty value, use a different cadence
through `HH:MM` or an interval like `12h`. Individual agents override the entry
through an item of the same name in their `HEARTBEAT.md`.

---

## What a run costs — and where the tokens go

The bill of an agent platform is not written by what the agents say. It is
written by what they **read, on every turn**: the system prompt with all tool
schemas, the target-system docs and the transcript so far go into the model
again with each step. Measured on a live instance over 24 hours: 3.2 million
output tokens against **112 million** cached input tokens.

Two consequences follow, and both are worth knowing before tuning anything:

- **A long run is disproportionately expensive.** The prefix is re-read per
  turn, and the transcript grows on top. Measured on one run: turn 1 read 44.000
  tokens, turn 70 read 155.000. Raising `max_turns` removes aborted runs, but it
  does not make the runs cheaper — it makes them longer.
- **The static prefix is the biggest single item**, because it is paid for on
  every turn. `COVEY_RUNTIME_TOOLS` controls it (see below).

### `COVEY_RUNTIME_TOOLS`

The built-in tool scope of a run, comma-separated; empty = the default. The
default deliberately carries only what a Covey agent uses — file, shell, search,
web and task tools. The runtime's full built-in set costs 20.811 prompt tokens,
the default 11.045.

The list decides not only what a run **may** use but what **exists** for it at
all. That distinction matters when extending it: a built-in tool an agent is to
use has to be named here, not merely permitted. Target-system actions are not
affected — they reach the run over their own route and follow `ACCESS.md`.

```bash
COVEY_RUNTIME_TOOLS="Bash,Read,Write,Edit,Glob,Grep,Task,WebFetch"
```

The `Skill` tool is added per run and only when skills have been materialized
for the agent — it pulls the descriptions of all built-in skills into the prompt
(+2.454 tokens) and is dead weight without skills of its own.

### Where the money went

Two views answer that, both under **Costs**:

- **Cost types** shows the split across cached read, cache write, fresh input
  and output. Whoever optimizes output length without this view optimizes the
  wrong 3 %.
- **Most expensive runs** ranks the individual runs (`GET /api/v1/cost/runs`,
  per agent `GET /api/v1/agents/{id}/cost/runs`). The **Actions** column carries
  the decisive signal: a run with `0` touched no target system — it read,
  thought and went back to sleep. In every aggregate such a run looks exactly
  like one that fixed three bugs.

---

## What to back up

Two things, and both are needed. The database holds the *manifests* of every
home snapshot; `data/blocks` holds their *content*. Back up one without the
other and you are left with a directory nothing points at, or an index of
things that are gone.

The share that matters is smaller than it looks and impossible to reconstruct:
measured on a real developer home of 7 GB, **48 MB existed nowhere else** — the
agent's own notes, extracted code and interim results, scattered across the home
([`spec/16`](../spec/16-runner.md)). The rest is toolchain caches and checkouts,
which download again. `covey doctor` and Administration → Diagnostics name the
directory and say this out loud, because it is the one operational obligation
the platform added that nobody would guess.

```bash
docker compose exec -T db pg_dump -U covey covey | gzip > db.sql.gz
tar -czf blocks.tar.gz -C /opt/covey/data blocks
```

**The deploy takes both by itself**, into `$DEPLOY_DIR/backups`, right before
`docker compose up -d` — which is the moment the migrations run, and the one
moment somebody would want a backup. Three of each are kept; the job never
fails on a failed backup, because a deploy that stops halfway leaves an
instance in a state nobody chose. What went wrong stands in the job log.

That is a pre-upgrade snapshot on the same host, not a backup strategy: it
survives a bad migration, not a dead disk. Whoever operates this instance still
copies both off the machine, or points the block store at an object store
(`COVEY_BLOB_STORE=s3`) whose bucket is backed up — then the blocks are no
longer on this disk at all, and `covey doctor` says so instead of warning.

## Before real production use

The setup is deliberately lean. For real operation, additionally (cf.
[`quickstart-docker.md`](quickstart-docker.md#for-production-use)):

- **HTTPS in front:** a reverse proxy with TLS termination, `COVEY_PUBLIC_URL`
  on `https://…` — the secure cookie then switches itself on automatically.
- **DB TLS:** `sslmode=require` in the DB URL (then in
  `docker-compose.deploy.yml` or via a DB instance of your own).
- **Egress:** `COVEY_EGRESS_ENFORCE=true` (the docker sandbox provider is
  already active) so that sandboxes reach only allowlist hosts.
- Replace the admin password from the generated `.env` with one of your own.
- **Session lifetime:** `COVEY_SESSION_TTL` (default `168h`, i.e. seven days).
  The session slides — every request in the second half of that window pushes
  the end back, so only an *unused* session runs out. Installations on shared
  machines can shorten it (`COVEY_SESSION_TTL=8h`); what protects an unattended
  browser is still the screen lock, not the hour count.
