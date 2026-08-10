# Quickstart with Docker Compose

Try Covey out locally in a few minutes — **without Go, Node or a local
Postgres installation**. Docker and Docker Compose suffice.

> For development (hot reload, tests) see the "Development" section in the
> [README](../README.md) instead.

---

## Prerequisites

- **Docker** with the Compose plugin (`docker compose version` ≥ 2.x).
- **OpenSSL** (for the master key; pre-installed on macOS/Linux).
- The repository checked out locally (the images are built from the
  `Dockerfile`s).

---

## In five steps

```bash
# 1. Create the configuration
cp .env.example .env

# 2. Generate a master key and write it into .env (32 bytes / 64 hex characters)
echo "COVEY_MASTER_KEY=$(openssl rand -hex 32)" >> .env

# 3. Build the sandbox image agents work in (once, a few minutes)
docker build -f Dockerfile.sandbox -t covey-sandbox:latest .

# 4. Start (builds the control plane image the first time)
docker compose up -d --build

# 5. Open
open http://localhost:8494        # or call it up in the browser
```

Login: **`admin@covey.local`** / **`covey-admin`**
(changeable in `.env` through `COVEY_ADMIN_EMAIL` / `COVEY_ADMIN_PASSWORD`).

That's it. `docker compose` starts three things:

| Service | Job |
|---|---|
| `db` | PostgreSQL with `pgvector` (persistent in the volume `covey-db`) |
| `bootstrap` | Creates the organisation, the admin login, a demo agent and its workplace once, then the container exits (idempotent) |
| `covey` | The control plane: API + orchestrator + the embedded admin UI on port **8494**. Migrations run automatically at start |

### Why step 3 is a step of its own

An agent works inside its own container, and that image is not part of the
compose build. It is deliberately separate: it carries chromium, a JDK and a
Node toolchain and takes minutes, while everything else here takes seconds.

Skipping it costs nothing at first — the platform starts, the interface works,
agents and configs can be created. Only the first wake fails, with
`sandbox image "covey-sandbox:latest" is missing`. Covey checks for this at
`serve` start and says so in the log, and the **first steps** on the agent
overview say so too, so the answer is not buried in the recording of a task
that never ran.

---

## Useful commands

```bash
docker compose logs -f covey      # live logs of the control plane
docker compose ps                 # status of all services
docker compose restart covey      # restart only the control plane
docker compose down               # stop (data stays: DB volume + ./data)
docker compose down -v            # stop AND drop the database (the agent homes
                                  # live in ./data — delete that too for a truly
                                  # fresh start)
docker compose up -d --build      # rebuild & start after code changes
```

Run a command in the covey container (e.g. repeat the bootstrap manually):

```bash
docker compose run --rm bootstrap
```

---

## Setup

After the first login the **setup** page (`/setup`) asks three questions, each
of which may be skipped:

1. **Engine and credential.** Which engine your agents think on, and the
   credential for it — for Claude Code an API key or a subscription token
   (generate the latter once with `claude setup-token`); for Codex an API key or
   the contents of `~/.codex/auth.json`. The value is checked against the
   provider before it is stored, and the workplace (runtime) is created around
   it. Without this every task fails with "Not logged in", because the sandbox
   has its own empty `HOME`.
2. **What your company does.** Three to five sentences. They stay on the
   organisation and go into every hiring brief, into the configuration of newly
   drafted agents and into the config assistant's system prompt.
3. **Your People department.** An agent whose job is drafting the others: from a
   description of a job it writes a complete agent — character, remit,
   procedures, access — and leaves it as a **draft** that you hire.

Everything here can also be done by hand later (Secrets, Runtimes, the template
library). What the setup buys is the order: the credential first, because
without it nothing the interface offers can actually run.

## Your first agent

There is a **first steps** checklist on the agent overview: deposit a runtime
credential → create an agent → write `SOUL.md` → create a task → watch it work.
It reads the organisation's actual state, so it ticks itself off and disappears
once everything is done. The same steps are described in more detail in the help
(key `?`).

*New agent* offers four ways in. The **brief** is the shortest: describe in a few
sentences what the new colleague should do, and the People department drafts the
agent from it — asking back if the description is too thin to work from. Next to
it stay the ready-made **templates**, the **manual** form for whoever knows
exactly what they want, and the **bundle import**.

Whatever the way, what comes out is a **draft**: it exists, it can be looked at
and changed, and it does not work until you hire it. A summary of role, access,
supervisor and budget comes up before you do.

After logging in a **demo agent** already exists, with a workplace and a board —
hired, so it works as soon as the credential from step 1 is there.

The demo agent has **no target system** on purpose — it works on the task text
and writes down what it found, which is enough to see a whole run: dispatch,
recording, cost, memory. Give it a real job by connecting a target system
([`ops-zammad.md`](ops-zammad.md), [`ops-github.md`](ops-github.md),
[`ops-email.md`](ops-email.md) …) or by importing one of the ready-made bundles
from [`examples/`](../examples/). For trying out Zammad without a real instance
there is the double `demo/fakezammad`.

---

## Sandbox isolation

Every agent gets its own container — real isolation at the namespace level, and
there is no weaker mode to fall back to. Two things in the compose setup carry
that, and both are easy to lose when adapting the file:

- **The Docker socket** is mounted into the covey container
  (`/var/run/docker.sock`). The control plane starts each sandbox as a *sibling*
  container through the host's daemon. Without the socket every wake fails with
  `no Docker daemon reachable`.
- **The data directory has the same path** on the host and in the container
  (`COVEY_DATA_DIR`, by default `$PWD/data`). The agent homes are mounted into
  the sandbox with `-v`, and that path is resolved by the *host's* daemon — a
  named volume or a differing path would mount something that exists nowhere,
  and every agent would find an empty home on each wake.

The sandbox image itself is [`Dockerfile.sandbox`](../Dockerfile.sandbox)
(coveyd + Claude Code + chromium for the browser plugin). Extending it follows
one rule: **version → home, toolchain → image**. Details in
[`spec/01-architecture.md`](../spec/01-architecture.md) and
[`ops-deployment.md`](ops-deployment.md).

---

## For production use

The example is optimised for "try it out quickly". Before real operation, at
least:

- **Change passwords/keys:** `COVEY_ADMIN_PASSWORD`, the Postgres password and
  a fresh `COVEY_MASTER_KEY` (the key en/decrypts all secrets — losing it means
  every deposited credential is unreadable; keep it safe).
- **HTTPS in front:** set `COVEY_PUBLIC_URL` to `https://…` and put a
  reverse proxy (TLS termination) in front. The secure cookie then switches
  itself on automatically.
- **DB TLS:** `sslmode=require` (or higher) in `COVEY_DATABASE_URL`.
- **Egress & isolation:** the docker provider + `COVEY_EGRESS_ENFORCE=true`.

Covey also prints these points as warnings itself at `serve` start, as soon as
it is not bound purely locally (`localhost`).
