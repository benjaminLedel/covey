# FR-002 — covey as a platform: self-registration with a waitlist code

Status: **Proposed** · As of: 2026-08-11

> Feature requests are proposals, not yet settled spec. If a request is accepted,
> its content moves into the responsible `spec/` document and this document is
> set to *accepted* / *rejected*.

## In short

Today a covey installation is opened by hand: `covey bootstrap` creates one
organisation with one `platform_admin`, and every further human is created by
that admin under *Users*. Whoever wants to try covey has to install it.

This request describes what has to change so that **a person can sign up on the
public website**, gated by a **waitlist code**, and then either **join an
existing organisation** or **create their own**. The organisation stays the unit
([`09-enterprise-model.md`](../spec/09-enterprise-model.md)) — what is new is
that a *person* now exists before their organisation does, and can belong to
more than one.

The waitlist code is deliberately the first gate: it keeps the tenant set small
and manually approved while the parts that a hosted platform actually needs
(mailer, quotas, an isolated data plane) grow in behind it.

Whether an instance accepts registrations at all, and how it sends mail, is
**instance configuration and belongs in the product**: a new `system_settings`
table plus a *System* page for the system admin — not an environment variable
that only whoever can restart the process may change.

## Motivation

Three things are currently impossible, and each of them costs a real
conversation:

1. **Nobody can try covey without installing it.** The public website
   ([`web/src/public/`](../web/src/public)) explains the product and then offers
   a login form for an account that a visitor does not have. The only path from
   "interested" to "seeing it" runs through Docker and a terminal.
2. **A person cannot belong to two organisations.** `humans.email` is globally
   unique and `humans.org_id` is `NOT NULL` (`migrations/0001_init.up.sql`) —
   the same person in a second organisation is a second, unrelated login with a
   second password. That already bites internally (someone who supports two
   tenants) and blocks every "join my org" flow.
3. **A hosted instance has no system-admin level.** `POST /api/v1/orgs`,
   `DELETE /api/v1/orgs/{id}` and `GET /api/v1/orgs` are guarded by `adminOnly`,
   i.e. `platform_admin` — a role that *every* organisation has
   (`internal/httpapi/server.go:403-406`, `internal/httpapi/admin.go`). On a
   single-tenant install that is correct and harmless. The moment strangers can
   create organisations, the first person who signs up becomes the
   `platform_admin` of their org — and can list, rename and **delete every other
   tenant on the instance**. This is the one change that is not optional.

## Today's state (what the code assumes)

| Assumption | Where it lives | Holds after this change? |
|---|---|---|
| Login identity = membership. One `humans` row carries e-mail, password hash, org and RBAC role. | `migrations/0001_init.up.sql`, `internal/identity/builtin` | no — split |
| A session resolves to exactly one human, and therefore to exactly one org. | `internal/httpapi/sessions.go:42` | no — session gains an *active* membership |
| Every authenticated request has an `OrgID`. | `identity.Principal`, `principalFrom`, all handlers | yes — kept deliberately |
| `platform_admin` is simultaneously the instance operator. | `internal/org/org.go:330` (comment), `server.go:403` | no — split off |
| Humans are created by an admin, never by themselves. | `internal/httpapi/admin.go` | no — plus self-registration |
| The control plane never sends e-mail. SMTP exists only as an agent target plugin. | `github.com/benjaminLedel/covey-plugin-pack/email/` | no — needs a mailer |
| Every instance-level setting is an environment variable, read once at start. | `internal/config/config.go` | no — the ones an admin operates move into `system_settings` |
| Tenant isolation on the read/write paths is enforced by middleware. | `agentScoped` / `taskScoped` / `stageScoped` / `pageScoped`, `server.go:552-627` | mostly — seven gaps, see [`003`](003-mandantentrennung.md) |
| All sandboxes are Docker containers on the control-plane host; runners ([`16-runner.md`](../spec/16-runner.md)) are spec, not code. | `cmd/covey/main.go:649-688` | **this is the gating risk**, see *Capacity and abuse* |

The last-but-one row is the load-bearing one: the org boundary is largely a
middleware you have to pass through, not a line 67 handlers must remember. The
work below therefore mostly does not have to re-secure the application — it has
to introduce a person above the organisation, a system admin above the instance,
and the instance's own configuration somewhere an admin can actually reach it.

"Largely", because an audit of the running code found seven places where the
boundary does not in fact hold — a live event bus that broadcasts across
tenants, webhooks that resolve an agent by slug across organisations, and four
smaller gaps. They are written up separately in
[`003-mandantentrennung.md`](003-mandantentrennung.md), and that request is a
**prerequisite** for this one: self-registration on an instance where those
stand would hand every new tenant a view into all the others.

## The target flow

1. A visitor opens `/registrieren` (`/en/sign-up`) and enters **waitlist code,
   e-mail, name, password**.
2. The control plane checks the code (hash lookup, uses left, not expired, an
   optional e-mail pattern matches), creates an **account** in state
   *unverified*, records the redemption and sends a verification mail.
3. The link signs the account in and marks it verified. From here on there is a
   session **without an active organisation** — a state that does not exist
   today.
4. The SPA sees "no organisation" and shows the **org gate** instead of the
   dashboard. It offers exactly what is available to this account:
   - **open invitations** addressed to this e-mail,
   - **organisations that match** (discoverable, or the e-mail domain is on
     their join list) → *request to join*,
   - **create your own organisation** (name; the creator becomes its
     `platform_admin`).
5. Creating an organisation runs the existing path — `org.CreateOrg` already
   creates the first admin and seeds the egress allowlist — and then lands in
   the setup that already exists ([`20-hiring-and-setup.md`](../spec/20-hiring-and-setup.md)):
   credential, company description, People department.
6. A join request appears for the target organisation's admins under *Users*;
   on approval the account gets a `humans` row there with the role the admin
   picks. Approval, not automatic entry, is the default — an organisation
   decides who works in it.
7. An account with more than one membership gets an **org switcher** in the app
   shell. Switching rewrites the active membership on the session; nothing in
   the API below `p.OrgID` notices.

## Data model

New migrations from `0052` upwards (existing migrations are never edited).

### The account, next to the human

The cheap and honest cut: **`humans` stays exactly what it is — the membership
record**. Ten foreign keys point at it (`agents.owner_id`,
`agent_config_versions.created_by`, `audit_log.actor_id`, `department_leads`,
`manager_id`, …); a person is not what they reference, a *seat in an
organisation* is. What moves out is only the login.

```sql
-- 0052_accounts.up.sql
CREATE TABLE accounts (
    id                UUID PRIMARY KEY,
    email             TEXT NOT NULL UNIQUE,          -- lower-cased
    password_hash     TEXT NOT NULL,
    display_name      TEXT NOT NULL DEFAULT '',
    email_verified_at TIMESTAMPTZ,
    -- The instance level, deliberately NOT an org role (see RBAC below).
    platform_role     TEXT NOT NULL DEFAULT 'user'
                      CHECK (platform_role IN ('user','system_admin')),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_login_at     TIMESTAMPTZ
);

ALTER TABLE humans ADD COLUMN account_id UUID REFERENCES accounts(id) ON DELETE CASCADE;
-- Backfill: one account per existing human, e-mail and hash carried over,
-- verified (they were created by an admin), the bootstrap admin as system_admin.
ALTER TABLE humans DROP CONSTRAINT humans_email_key;
CREATE UNIQUE INDEX humans_email_per_org   ON humans (org_id, lower(email));
CREATE UNIQUE INDEX humans_account_per_org ON humans (org_id, account_id);
```

`humans.email` and `humans.password_hash` stay for one release as the source of
the backfill and are then dropped in a follow-up migration — authentication
reads `accounts` from P1 on.

### The session gains an active membership

```sql
-- 0053_session_account.up.sql
ALTER TABLE http_sessions ADD COLUMN account_id UUID REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE http_sessions ALTER COLUMN human_id DROP NOT NULL;  -- NULL = signed in, no org chosen
```

`human_id NULL` is the whole "signed in without an organisation" state. No second
session mechanism, no second cookie.

### Waitlist codes

*Gebaut* als `migrations/0058_accounts_waitlist.up.sql` — zusammen mit
`accounts`, weil die Einlösung auf ein Konto zeigt und beide in derselben
Transaktion entstehen müssen. Das Format des Codes steht in
`internal/waitlist/code.go`: `COVEY-4K7MQ-P2D9X`, Crockford-Base32, 50 Bit.

```sql
-- 0058_accounts_waitlist.up.sql (hier als 0054 skizziert)
CREATE TABLE waitlist_codes (
    code_hash     TEXT PRIMARY KEY,        -- only the hash, like http_sessions
    label         TEXT NOT NULL DEFAULT '',-- "batch march", "conference X"
    max_uses      INTEGER NOT NULL DEFAULT 1,
    used_count    INTEGER NOT NULL DEFAULT 0,
    expires_at    TIMESTAMPTZ,
    -- Optional binding: this code may only join THIS organisation …
    org_id        UUID REFERENCES organizations(id) ON DELETE CASCADE,
    -- … and/or only from this e-mail domain.
    email_pattern TEXT NOT NULL DEFAULT '',
    created_by    UUID REFERENCES accounts(id) ON DELETE SET NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at    TIMESTAMPTZ
);

CREATE TABLE waitlist_redemptions (
    code_hash   TEXT NOT NULL REFERENCES waitlist_codes(code_hash) ON DELETE CASCADE,
    account_id  UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    redeemed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (code_hash, account_id)
);
```

Storing only the hash follows the rule the session store already applies
(`sessions.go:13-20`): whoever reads the database does not get usable codes out
of it. `used_count` is incremented in the same transaction as the account
insert, with `UPDATE … WHERE used_count < max_uses` — a code with one use cannot
be redeemed twice in parallel.

### Tokens, invitations, join requests

```sql
-- 0055_account_tokens.up.sql
CREATE TABLE account_tokens (
    token_hash TEXT PRIMARY KEY,
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    purpose    TEXT NOT NULL CHECK (purpose IN ('verify_email','reset_password')),
    expires_at TIMESTAMPTZ NOT NULL,
    used_at    TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 0056_org_join.up.sql
ALTER TABLE organizations
    ADD COLUMN join_policy  TEXT NOT NULL DEFAULT 'invite'
        CHECK (join_policy IN ('invite','request','domain')),
    ADD COLUMN join_domains TEXT[] NOT NULL DEFAULT '{}',
    ADD COLUMN discoverable BOOLEAN NOT NULL DEFAULT FALSE;

CREATE TABLE org_invites (
    token_hash TEXT PRIMARY KEY,
    org_id     UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    email      TEXT NOT NULL,
    role       TEXT NOT NULL,   -- the RBAC role the invitee gets
    invited_by UUID REFERENCES humans(id) ON DELETE SET NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    accepted_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE org_join_requests (
    id         UUID PRIMARY KEY,
    org_id     UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    message    TEXT NOT NULL DEFAULT '',
    state      TEXT NOT NULL DEFAULT 'open'
               CHECK (state IN ('open','approved','rejected','withdrawn')),
    decided_by UUID REFERENCES humans(id) ON DELETE SET NULL,
    decided_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX org_join_requests_open_uq
    ON org_join_requests (org_id, account_id) WHERE state = 'open';
```

`join_policy` defaults to `invite` — the conservative value. An existing
organisation does not become joinable by migrating.

### Per-organisation quotas

```sql
-- 0057_org_quota.up.sql
ALTER TABLE organizations
    ADD COLUMN max_agents      INTEGER NOT NULL DEFAULT 0,  -- 0 = no cap
    ADD COLUMN max_sandboxes   INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN budget_usd      NUMERIC(12,4) NOT NULL DEFAULT 0;
```

Today only agents carry a budget (`agents.budget_usd`). A self-service tenant
needs a ceiling one level up, and the dispatcher needs a reason to refuse — see
*Capacity and abuse*.

### System settings

The instance's own configuration — one row per setting, no wide table that
needs a migration for every new switch.

*Gebaut* als `migrations/0057_system_settings.up.sql` — die Nummern in diesem
Dokument sind Reihenfolge, nicht Vergabe: 0051–0056 liegen in den offenen
Zweigen (Runner, Aufgaben-Wiederholung), und eine doppelte Nummer gilt als
längst angewandt und wird still übersprungen.

```sql
-- 0057_system_settings.up.sql
CREATE TABLE system_settings (
    key        TEXT PRIMARY KEY,
    -- Plain value. NULL for secret settings, which live in the two columns below.
    value      TEXT,
    -- Secret value, AES-GCM sealed with the master key (SMTP password).
    nonce      BYTEA,
    ciphertext BYTEA,
    updated_by UUID REFERENCES accounts(id) ON DELETE SET NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

Sealing reuses the primitive the built-in `SecretStore` already has
(`internal/secrets/builtin/builtin.go`, `seal`/`open`, AES-GCM with the
`COVEY_MASTER_KEY`) — extracted into a shared helper so there is one
implementation, with `"system:"+key` as the associated data. The `secrets` table
itself cannot carry these: its primary key is `(org_id, key)` with a foreign key
to `organizations`, and an instance-level value belongs to no organisation.

Defined keys, all with a compiled-in default so a fresh database needs no seed:

| Key | Type | Default | Meaning |
|---|---|---|---|
| `signup.mode` | `off` \| `waitlist` \| `open` | `off` | whether the instance accepts registrations |
| `signup.org_quota` | int | `1` | organisations one account may create |
| `signup.new_org_sandboxes` | bool | `false` | may a self-registered organisation run sandboxes before a system admin approves it |
| `signup.default_join_policy` | `invite` \| `request` \| `domain` | `invite` | what a newly created organisation starts with |
| `mail.smtp_host` / `mail.smtp_port` | text / int | — / `587` | the mailer |
| `mail.smtp_user` | text | — | |
| `mail.smtp_password` | **secret** | — | sealed, never returned to the browser |
| `mail.from` / `mail.from_name` | text | — | envelope and display sender |
| `mail.security` | `starttls` \| `tls` \| `none` | `starttls` | built as an enum rather than the boolean `mail.starttls` this document first proposed: implicit TLS on port 465 is half the installed base, and `none` is what makes a local mail double usable at all |
| `mail.last_test_at` / `mail.last_test_error` | timestamp / text | — | written by the test mail; the gate for `signup.mode` reads it |
| `site.name` | text | `covey` | what the mails and the sign-up page call this instance |

Reads go through a small in-process cache in front of the table; invalidation
uses the `LISTEN/NOTIFY` channel that the stack already uses for pub/sub
(`spec/10`), so a second process picks up a changed setting without a restart.

**`off` stays the default**, and now for a stronger reason than an unset
environment variable: a fresh row is simply absent, and an absent row means the
compiled-in default. An existing installation that upgrades gets `off` because
that is what the code says, not because someone remembered to leave a variable
unset.

## Code changes

### `internal/identity` — authenticate the account, not the human

`Provider.AuthenticateHuman` becomes account-based; `identity.Principal` gains
`AccountID` and `PlatformRole` and **keeps `OrgID` and `Role`**, now filled from
the active membership. That is the point of the whole cut: every handler that
reads `p.OrgID` today stays untouched, and there are hundreds of them.

New on the port: `Memberships(ctx, accountID)` and the org-less principal.

### `internal/httpapi` — one new middleware state

`s.auth` today rejects anything without a resolvable human. It has to accept a
session whose `human_id` is `NULL` and produce an account-only principal.
`s.rbac` then rejects that principal with a **distinct, machine-readable
error** (`409` + `{"error":"no_organization"}`) so the SPA routes to the org
gate instead of bouncing back to the login form. `agentScoped` and friends
inherit it unchanged, because they run through `rbac`.

New handler files, following the existing split:

- `signup.go` — the unauthenticated public endpoints (code check, signup,
  verify, password reset).
- `membership.go` — memberships, org switch, the org gate's offers, join
  requests, invitations.
- `platform.go` — the system-admin endpoints (system settings, waitlist codes,
  tenant list), moved out of `admin.go`.

### `internal/settings` — the instance's own configuration

A small store over `system_settings`: typed getters with compiled-in defaults
(`Mode()`, `OrgQuota()`, `Mail()`), a `Set` that writes, audits and notifies,
and a cache invalidated over `LISTEN/NOTIFY`. Secret keys are sealed with the
master key and are **write-only from the outside**: the API returns whether a
value is set, never the value — the same rule the `SecretStore` previews already
follow (`internal/secrets/secrets.go`, `KeyPreview`).

Validation lives in the store, not in the handler, so the CLI and the API cannot
disagree: `mail.from` has to parse as an address, and switching `signup.mode`
away from `off` fails until a **test mail has actually succeeded** — a filled-in
host is not evidence, a delivered message is.

### `internal/mail` — the control plane learns to send

The RFC-5322 builder and the PLAIN/STARTTLS delivery already exist, but as part
of an agent target plugin (`github.com/benjaminLedel/covey-plugin-pack/email/smtp.go`).

*Built* (#167), with two corrections to what stood here:

- The builder is **not shared**. The dependency graph is acyclic and nothing
  depends on covey — the pack cannot import `internal/mail`. Sharing would mean
  moving the builder into the plugin SDK and making the message format part of
  the contract third parties build against; for two senders with different
  requirements (an agent's threaded reply into a foreign mailbox versus a short
  transactional message from the instance) that price is too high. The
  duplication is deliberate and the package comment says so.
- There is **no `log` sender**. One that "succeeded" would satisfy the gate
  below and open registration on an instance that sends nothing. A development
  instance points at the mail double in `demo/fakemail` instead, where the
  message can actually be looked at.

The sender reads its configuration from `internal/settings`, not from the
process environment, and re-reads it on change — an admin who fixes a typo in
the SMTP host must not have to restart the instance. Fail-closed twice over:
signup cannot be switched on without a mailer (see above), and if the mailer
breaks afterwards, `POST /public/signup` answers with a plain "registration is
currently unavailable" instead of creating accounts nobody can verify.

### `internal/org` — memberships instead of one-shot humans

`CreateOrg` keeps its shape but takes an existing `account_id` instead of
e-mail/name/hash (the admin already exists when self-service creates an org).
New: `AddMembership`, `RemoveMembership`, `ListMemberships(accountID)`. The
`ErrLastAdmin` guard has to hold on the membership level too — leaving an
organisation must not remove its last admin.

### API surface

| Method | Path | Auth | Purpose |
|---|---|---|---|
| `POST` | `/api/v1/public/signup` | none, rate-limited | code + e-mail + name + password → unverified account + mail |
| `POST` | `/api/v1/public/verify` | none | token → verified + session |
| `POST` | `/api/v1/public/password-reset` | none, rate-limited | request the mail |
| `POST` | `/api/v1/public/password-reset/confirm` | none | token + new password |
| `POST` | `/api/v1/public/invites/accept` | none / session | accept an invitation |
| `GET` | `/api/v1/auth/memberships` | session | the account's organisations + the active one |
| `POST` | `/api/v1/auth/switch-org` | session | set the active membership |
| `GET` | `/api/v1/auth/join-options` | session, org-less allowed | invitations, matching orgs, open requests |
| `POST` | `/api/v1/orgs` | session, verified, quota | create an organisation (creator = `platform_admin`) |
| `POST` | `/api/v1/orgs/{id}/join-requests` | session | ask to join |
| `GET`/`POST` | `/api/v1/org/join-requests`, `…/{id}/approve\|reject` | `platform_admin` | decide |
| `GET`/`POST`/`DELETE` | `/api/v1/org/invites` | `platform_admin` | invite by e-mail |
| `PATCH` | `/api/v1/org/join-policy` | `platform_admin` | invite / request / domain, domain list, discoverable |
| `GET`/`PUT` | `/api/v1/platform/settings` | **system admin** | read / write the system settings |
| `POST` | `/api/v1/platform/settings/test-mail` | **system admin** | send a test mail to the caller |
| `GET`/`POST`/`DELETE` | `/api/v1/platform/waitlist-codes` | **system admin** | generate, list, revoke |
| `GET`/`PATCH`/`DELETE` | `/api/v1/platform/orgs[/{id}]` | **system admin** | today's `/api/v1/orgs`, plus approve |
| `GET` | `/api/v1/public/signup-state` | none | is signup open, is a code required, what is this instance called |

`POST /api/v1/orgs` changes meaning: from "an admin creates a tenant including
its first admin" to "a signed-in account opens its own organisation". The old
behaviour lives on under `/api/v1/platform/orgs`.

`GET /api/v1/platform/settings` returns secret settings as `{"set": true}`, never
as a value. `PUT` takes a partial object — an absent key stays unchanged, so a
form that does not know a newly added setting cannot wipe it.

## RBAC: the missing installation level

covey's five roles are **org roles** — they answer "what may this person do
inside their organisation". Nothing in that set answers "what may this person do
to the *instance*", because until now the two questions had the same answer.

`accounts.platform_role` splits them:

- `user` — the normal case, including every `platform_admin` of every
  organisation. Sees exactly their own organisations.
- `system_admin` — the person who runs the installation. System settings,
  waitlist codes, the tenant list, tenant approval and deletion, instance-wide
  diagnostics. Set by `covey bootstrap` on the bootstrap admin and by an
  explicit CLI command afterwards (`covey system-admin add <email>`) — never
  through a self-service path, and never by a role an organisation can hand out
  to itself.

For a single-tenant self-hosted install nothing visibly changes: the bootstrap
admin is `platform_admin` *and* `system_admin`, and sees the same screens as
today plus the *System* page.

## Web

New public pages, registered in `web/src/public/seo.ts` (the single source for
routing, prerendering, `Head` and `sitemap.xml`) with both language paths:

| id | de | en | indexable |
|---|---|---|---|
| `registrieren` | `/registrieren` | `/en/sign-up` | yes — it is marketing surface |
| `bestaetigen` | `/registrieren/bestaetigen` | `/en/sign-up/confirm` | no |
| `passwort` | `/passwort-vergessen` | `/en/forgot-password` | no |
| `passwort-neu` | `/passwort-neu` | `/en/reset-password` | no |
| `einladung` | `/einladung` | `/en/invitation` | no |

In the application:

- **`OrgGate`** — the screen for a session without an active organisation:
  invitations, matching organisations, "create your own". `App.tsx` currently
  goes straight from `me` to the dashboard; the gate sits in between.
- **Org switcher** in the shell, rendered only when the account has more than
  one membership.
- **Users** gains two tabs: *join requests* and *invitations*; **Org** gains the
  join policy.
- **`System`** (`web/src/pages/System.tsx`) — the system admin's page, see
  below. **Waitlist** (codes, uses, revoke) sits next to it, and the existing
  `Organizations.tsx` moves behind the same role.
- Every string in `web/src/locales/de.json` **and** `en.json`.

The `LoginCard` keeps its localhost-only demo login and gains one line: "No
account yet? → sign up" — rendered from `GET /api/v1/public/signup-state`, so a
self-hosted install with signup off shows nothing.

## The System page

One page, three cards, visible only to `system_admin` and reachable from the
navigation only for that role. It is the counterpart of the setup wizard
([`20-hiring-and-setup.md`](../spec/20-hiring-and-setup.md)) one level up: the
setup configures an *organisation*, this configures the *instance*.

1. **Registration** — the mode (`off` / `waitlist` / `open`) as three explicit
   choices with a sentence each on what they mean, the org quota per account,
   the default join policy for new organisations, and whether a freshly
   registered organisation may run sandboxes before approval. Switching away
   from `off` stays disabled until the e-mail card below reports a successful
   test — with that sentence written on the card, instead of a switch that
   silently fails.
2. **E-mail** — SMTP host, port, STARTTLS, user, password (write-only; the field
   shows "set" and can be replaced or cleared), sender address and name, plus a
   **Send test mail** button that delivers to the signed-in admin and reports
   the SMTP error verbatim on failure. That button is the whole reason this
   belongs in the UI: a wrong mail configuration is otherwise discovered by the
   first person who never receives their verification link.

   The test takes **the same path as a verification mail** — the same
   `internal/mail` sender, the same settings read, the same from-address, only
   the body differs. Not a connection the handler opens for itself: a test that
   travels a different route can pass while the real mail fails, and then it has
   certified exactly nothing. For the same reason the test sends the settings
   **as stored**, not the values in the unsaved form — save first, then test,
   so that what was proven is what will run.

   The result is recorded (`mail.last_test_at`, `mail.last_test_error`) and shown
   on the card. Whoever switches registration on a week later should be able to
   see when this last worked, rather than having to remember.
3. **Instance** — the name used in mails and on the sign-up page, plus a
   read-only block of the facts that stay in the process environment (version,
   public URL, sandbox provider, database) so nobody hunts for a setting that is
   deliberately not editable here.

Everything on the page is audited (`audit_log`, migration 0044) with the actor,
the key and the old/new value — secret values as "changed", never as text.

**Why a table and not an environment variable.** Operating tooling belongs in
the binary, because covey is installed from GitHub by third parties
([`README.md`](../README.md)) who do not have our deployment pipeline: a setting
that only exists as an environment variable can be changed only by whoever can
edit the unit file and restart the process — which on a hosted instance is
nobody who is awake at the time. A table plus a page is also what makes "send a
test mail" possible at all.

**`off` stays the default** for the same reason as before: an upgrade that
silently opens a public registration form on someone's internal instance would
be a security incident, not a feature. `waitlist` is what covey.work would run;
`open` stays refused by the store until the capacity question below is answered.

The process environment keeps exactly what has to be readable *before* the
database is: `COVEY_DATABASE_URL`, `COVEY_MASTER_KEY`, `COVEY_LISTEN_ADDR`,
`COVEY_PUBLIC_URL`, the sandbox provider. The line is deliberately drawn there —
whatever an admin operates goes into the table, whatever is needed to reach the
table stays in the environment.

## Capacity and abuse

- **Rate limits.** The existing `loginLimiter` (in-process, keyed by IP+e-mail,
  `internal/httpapi/ratelimit.go`) is the right shape and the wrong scope: signup,
  verification, reset and code checks each need one, keyed by IP *and* by code.
  In-process is acceptable for the single-binary topology, as documented there —
  but a code with 50 uses deserves a database counter, which it has
  (`used_count`).
- **Enumeration.** Password reset and signup must answer identically for a known
  and an unknown e-mail. The waitlist code is the deliberate exception: telling
  someone their code is wrong is the whole point of the field.
- **Quotas.** `max_agents` / `max_sandboxes` / `budget_usd` per organisation,
  enforced where the money is spent — agent creation (`handleCreateAgent`) and
  dispatch (`internal/orchestrator`), next to the existing `fleet_killed` check.
- **Credentials stay BYOK.** An organisation brings its own Claude credential
  (`internal/runtimes`, [`18-runtimes-capacity.md`](../spec/18-runtimes-capacity.md)).
  In the waitlist phase the platform provides **no** shared credential — a new
  tenant therefore cannot spend the instance's money, only their own.
- **The data plane is the real gate.** Every sandbox today is a Docker container
  on the control-plane host (`cmd/covey/main.go`; only the `docker` provider is
  implemented, runners from [`16-runner.md`](../spec/16-runner.md) are spec
  only). Self-service registration means strangers running code in containers on
  one shared host. The waitlist is what makes that tolerable *for now*: a small,
  manually approved tenant set with a name attached to each code. Before signup
  goes beyond `waitlist`, one of these has to exist:
  1. **runners** per [`16-runner.md`](../spec/16-runner.md) — the organisation
     brings its own capacity, which is exactly how that document already frames
     a runner ("trusted infrastructure of the organisation"), or
  2. a **hardened sandbox runtime** (gVisor/Kata) plus a per-tenant network
     namespace, or
  3. **no sandbox at all** for unapproved tenants — they can configure agents
     and see the platform, but nothing runs until a system admin approves them
     on the tenant list.

  Option 3 is the cheapest and worth doing first: it makes the waitlist phase
  safe without new infrastructure, and it turns "approved" into a state the
  platform can express. It is the setting `signup.new_org_sandboxes`, which is
  exactly why that switch is in the table.

## Build order

Each step is releasable on its own and leaves the single-tenant installation
working.

> **Stand.** P1, P2 and P3 are built. The stores and the public sign-up exist
> (`internal/settings`, `internal/waitlist`, `internal/accounts`), and the
> switches and the codes are administered under *Platform*
> (`/api/v1/platform/settings`, `/api/v1/platform/waitlist-codes`,
> `…/accounts`). The **mailer** followed with #167: `internal/mail`, the sealed
> SMTP password in `system_settings`, the *E-mail* card with its test mail, and
> the gate that keeps `signup.mode` on `off` until a mail has demonstrably gone
> out.
>
> What P4 still lacks is the **mails themselves** (#168) — confirmation link
> and password reset; until they exist, registration marks addresses as
> verified straight away. P5 (joining, org switcher) and P6 (quotas) are open,
> and notification mails are #169.

- **P1 — accounts and sessions.** Migrations 0052/0053, backfill, identity and
  session refactor, org-less principal. *Nothing user-visible changes.*
  Acceptance: the integration suite (`make test-integration`) is green, login,
  logout, session list and every org-scoped route behave exactly as before.
- **P2 — the system-admin level.** `platform_role`, `/api/v1/platform/…`, the
  tenant endpoints move, `covey system-admin add`. Acceptance: a
  `platform_admin` of org A gets a 403 on every route that touches org B; the
  bootstrap admin does not notice the change.
- **P3 — system settings and mail.** *Built* (migration 0057,
  `internal/settings`, `internal/mail`, the *E-mail* page). Acceptance: an admin
  configures SMTP in the UI, the test mail arrives, password reset works end to
  end against a local SMTP double (`demo/` has the pattern for a mail double),
  switching `signup.mode` is refused while no mailer works, and every change
  shows up in the audit trail.
- **P4 — waitlist and signup.** Migrations 0054/0055, the public endpoints, the
  public pages, the org gate with "create your own organisation". Acceptance: a
  fresh account with a valid code reaches its own dashboard and the setup
  ([`20-hiring-and-setup.md`](../spec/20-hiring-and-setup.md)) without a system
  admin touching anything; a used, expired or wrong code is refused; a code with
  one use cannot be redeemed twice in parallel; and with `signup.mode = off` the
  public endpoints answer 404, not 403 — a closed instance does not advertise
  that it could be opened.
- **P5 — joining.** Migration 0056, invitations, join requests, domain matching,
  the org switcher. Acceptance: one account works in two organisations,
  switching changes what every API answer contains, and no cross-org data
  appears in either.
- **P6 — quotas and audit.** Migration 0057, enforcement in creation and
  dispatch, audit events for signup, join, approval, org creation and code
  redemption. Acceptance: an organisation at its cap gets a clear refusal, not
  a failed run; every step of a registration is reconstructable from
  `/api/v1/audit`.

## Non-goals

- **Billing, plans, payment.** A waitlist code is not a subscription. Cost
  visibility already exists per agent and organisation; charging for it is a
  separate decision.
- **Self-service SSO/OIDC.** The `IdentityProvider` port stays as it is; an
  organisation that wants Entra or Keycloak configures it at the instance level,
  as today.
- **A public agent marketplace.** Templates and skill bundles stay
  organisation-internal.
- **Custom domains per tenant.** One instance, one address.
- **Account deletion / data export on request.** Needed before any real public
  launch, deliberately not designed here — see the open decisions.

## Open decisions

- **D1 — one role per membership, or per account?** The proposal puts the RBAC
  role on the membership (`humans.role`, unchanged), which is almost certainly
  right, but it means an account can be `auditor` in one organisation and
  `platform_admin` in another. Confirm that the UI shows this clearly enough.
- **D2 — is e-mail verification mandatory before the org gate?** Making it
  mandatory costs one round trip and removes a class of abuse. The proposal
  assumes yes.
- **D3 — does the code check leak?** A separate `POST /public/waitlist/check`
  gives better UX and a free oracle for guessing codes. The proposal has no
  separate check; the code is validated with the signup.
- **D4 — may an account create more than one organisation?**
  `signup.org_quota` defaults to 1; the honest answer depends on whether
  agencies are a target group.
- **D5 — what happens to an organisation whose last member leaves?** Either
  refuse the last departure (like `ErrLastAdmin`) or let the system admin
  garbage collect it.
- **D6 — GDPR: account deletion.** An account spans organisations, but its
  `humans` rows carry authorship in agent config versions and the audit trail.
  Deleting the account must not erase the audit history — the pattern
  (`ON DELETE SET NULL` plus a retained `actor_email`) already exists in
  `migrations/0044_audit_log.up.sql` and should be the model.
- **D7 — do existing environment variables migrate into the table?** Nothing in
  today's `Config` overlaps with the settings proposed here, so the answer is
  "nothing to migrate". The question is whether *further* settings should follow
  (`COVEY_DREAM_AT`, `COVEY_BOARD_RETENTION`, `COVEY_EGRESS_ALLOW` are the
  candidates an admin would plausibly want to operate). The proposal keeps them
  where they are and leaves the door open; if they move, ENV becomes the seed
  for the first start and the table the authority afterwards — never a
  precedence rule that changes with the wind.

## Effect on the specification

If this request is accepted, the content moves into:

- [`09-enterprise-model.md`](../spec/09-enterprise-model.md) — the tenant-model
  paragraph currently reads "primarily single-org self-hosted, multi-tenancy is
  a later expansion stage". This request *is* that expansion stage; the account
  vs. membership distinction and the `system_admin` level belong there.
- [`04-identity-secrets.md`](../spec/04-identity-secrets.md) — the human
  identity layer gains self-registration and the account/membership split.
- [`10-architecture-stack.md`](../spec/10-architecture-stack.md) — the Postgres
  anchor gains one more thing it carries: the instance's own configuration, and
  the line between what stays in the environment and what does not.
- [`16-runner.md`](../spec/16-runner.md) — gains the sentence that
  self-service tenants are exactly the case runners solve.
- [`07-open-decisions.md`](../spec/07-open-decisions.md) — D1–D7 above, promoted
  to instance-wide D-numbers.
- [`20-hiring-and-setup.md`](../spec/20-hiring-and-setup.md) — the setup no
  longer begins at `covey bootstrap` but at the first login of a
  self-registered organisation.
