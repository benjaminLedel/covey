# Quickstart with Docker Compose

Try Covey out locally in a few minutes — **without Go, Node or a local
Postgres installation**. Docker and Docker Compose suffice.

> For development (hot reload, tests) see the "Development" section in the
> [README](../README.md) instead.

---

## Prerequisites

- **Docker** with the Compose plugin (`docker compose version` ≥ 2.x).
- **OpenSSL** (for the master key; pre-installed on macOS/Linux).
- The repository checked out locally (the image is built from the `Dockerfile`).

---

## In four steps

```bash
# 1. Create the configuration
cp .env.example .env

# 2. Generate a master key and write it into .env (32 bytes / 64 hex characters)
echo "COVEY_MASTER_KEY=$(openssl rand -hex 32)" >> .env

# 3. Start (builds the image the first time)
docker compose up -d --build

# 4. Open
open http://localhost:8494        # or call it up in the browser
```

Login: **`admin@covey.local`** / **`covey-admin`**
(changeable in `.env` through `COVEY_ADMIN_EMAIL` / `COVEY_ADMIN_PASSWORD`).

That's it. `docker compose` starts three things:

| Service | Job |
|---|---|
| `db` | PostgreSQL with `pgvector` (persistent in the volume `covey-db`) |
| `bootstrap` | Creates the organisation, the admin login and a demo agent once, then the container exits (idempotent) |
| `covey` | The control plane: API + orchestrator + the embedded admin UI on port **8494**. Migrations run automatically at start |

---

## Useful commands

```bash
docker compose logs -f covey      # live logs of the control plane
docker compose ps                 # status of all services
docker compose restart covey      # restart only the control plane
docker compose down               # stop (data stays in the volumes)
docker compose down -v            # stop AND delete all data (a fresh start)
docker compose up -d --build      # rebuild & start after code changes
```

Run a command in the covey container (e.g. repeat the bootstrap manually):

```bash
docker compose run --rm bootstrap
```

---

## Your first agent

There is a **first steps** checklist on the agent overview for this:
deposit a runtime credential → create an agent → write `SOUL.md` → create a
task → watch it work. It reads the organisation's actual state, so it ticks
itself off and disappears once everything is done. The same steps are described
in more detail in the help (key `?`).

After logging in a **demo support agent** already exists. For it to actually be
able to work, it needs two things — both depositable in the admin UI under the
agent:

1. **Anthropic access** — the secret `anthropic_api_key` (an API key) *or*
   `claude_code_oauth_token` (a subscription account; generate the token once
   with `claude setup-token`). Without one of the two, tasks fail with
   "Not logged in", because the sandbox has its own empty `HOME`. Both names
   accept an `_suffix` (`claude_code_oauth_token_team_a`) if you want several
   credentials in one organisation and a different one per agent.
2. **A target system** — e.g. Zammad. For trying things out without a real
   Zammad there is the double `demo/fakezammad`; for connecting to a real
   instance see the runbook [`ops-zammad.md`](ops-zammad.md).

---

## Sandbox isolation

The Compose setup uses the default **`COVEY_SANDBOX_PROVIDER=local`**: sandboxes
run as subprocesses *inside* the covey container. That is honest and ideal for
trying things out, but it offers **process isolation only**.

For real container isolation per agent (`COVEY_SANDBOX_PROVIDER=docker`) the
covey container needs access to a Docker daemon (a socket mount or
Docker-in-Docker) and the sandbox image `covey-sandbox:latest`
([`Dockerfile.sandbox`](../Dockerfile.sandbox)). That is deliberately **not**
part of this beginner setup — details in
[`spec/01-architecture.md`](../spec/01-architecture.md) and the README.

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
