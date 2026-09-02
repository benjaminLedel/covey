---
slug: teams
title: Microsoft Teams
description: 'From the demo setup to a real Teams bot through the Azure Bot Service: registration, endpoint, webhook secret and the limits of a single-tenant bot.'
---

A practical runbook for the step from the demo setup (`demo/faketeams`) to a
**real Teams bot** through the Azure Bot Service. The design background is
in [`../spec/15-teams-integration.md`](../../../spec/15-teams-integration.md); this
document says **what concretely has to be done** — and where the limits for
production use lie.

> Short version: the adapter is already Bot Framework compatible (a
> JWT-verified messaging endpoint, OAuth2 `client_credentials` against the bot
> connector, `/v3/conversations` paths). These are essentially **configuration
> steps in Azure + Teams**, not a rebuild.

---

## 1. Overview of the data flow

```
Teams  ──(user @mention/DM)──►  Azure Bot Service  ──(POST activity, JWT)──►  covey
                                                                                │  /api/webhooks/teams/<agent-slug>
                                                                                │  check JWT → intake filter → backlog task
                                                                                ▼
                                                                          agent (sandbox, Claude Code)
                                                                                │  actions through the action proxy
Teams  ◄──(bot connector /v3, OAuth2 token)───────────────────────────────────┘  send, reply, create_conversation
```

Two directions, two auth routes:

- **Inbound** (Bot Service → covey): the messaging endpoint, verified by a **JWT**
  (issuer `api.botframework.com`, audience = the bot app ID, RS256/JWKS). The
  expected app ID through `COVEY_TEAMS_WEBHOOK_SECRET`.
- **Outbound** (covey → bot connector): REST with a short-lived token exchanged
  by OAuth2 `client_credentials`. The app ID + app password come brokered
  (the secret `teams_token`) and are never persisted in the sandbox.

---

## 2. Setup — step by step

Order: **Azure first** (register the bot), **then Teams** (the app package, so
that users can write to the bot), **then covey** (secrets, target system, env).
Reckon with ~30 minutes if you have the necessary roles.

### 2.0 Which roles/rights do I need?

| Step | Necessary role |
|---|---|
| Create the Azure bot resource + app registration | **Application Administrator** (or Cloud Application Administrator) in Entra ID **and** *Contributor* on an Azure subscription/resource group |
| Upload (sideload) a custom Teams app | A user whose **Teams setup policy** allows "Upload custom apps" — enabled by the **Teams administrator** |
| Publish/approve the app org-wide | **Teams Administrator** (Teams Admin Center → *Teams apps → Manage apps*) |
| Configure covey (secrets, target system, env) | covey **org_admin**/**security** + the **agent owner** |

> **Important — no Graph, no admin consent for the MVP.** For pure
> sending/receiving **including file attachments in 1:1 chats** the bot needs
> **no** Microsoft Graph API permissions and **no** admin consent flow: the
> Bot Framework channel authenticates itself through app ID + secret
> (`client_credentials` against `botframework.com`). Graph permissions (and
> with them admin consent by a global/cloud app admin) only become relevant for
> later scenarios — reading channel messages *without* an @mention (RSC),
> proactively writing by AAD ID through Graph. Not for this integration.

### 2.1 Azure: create the bot registration + secret

1. Azure portal → create an **"Azure Bot"** (the resource "Azure Bot"). As the
   *Microsoft App ID* choose "Create new", type **Multi-Tenant** (the simplest
   start; single-tenant works too, then set `teams_url` tenant-specifically in
   covey — see 2.4).
2. After creation note the **Microsoft app ID** (the client ID of the
   associated app registration) — it is at once the bot ID, a credential
   component and the JWT audience.
3. Create a **client secret**: the app registration (Entra ID → App
   registrations → the app) → *Certificates & secrets* → *New client secret*.
   Copy the **value immediately** (it is shown only once) — that is the app
   password.
4. **Enable the Teams channel:** the bot resource → *Channels* → add
   **Microsoft Teams** (confirm the terms of use).

> An alternative to the portal: the **Teams Developer Portal**
> (`dev.teams.microsoft.com`) can do the bot registration, the messaging
> endpoint and the app manifest in one go. The roles from 2.0 still apply.

### 2.2 Azure: set the messaging endpoint

The bot resource → *Configuration* → **Messaging endpoint**:

```
https://covey.example.com/api/webhooks/teams/<agent-slug>
```

`<agent-slug>` = the slug of the responsible agent in covey; the message is
assigned to the agent through the URL. The **agent ID** is accepted as an
alternative (the UUID from the agent page's URL) — handy when the endpoint in
the third-party system cannot be changed afterwards. The slug is still the
better choice: it is readable and survives a rebuild of the agent. The base has
to be **publicly reachable**, otherwise the Bot Service cannot deliver (no
`localhost`; for local tests use a tunnel such as `ngrok`, or the `faketeams`
double from section 6).

### 2.3 Teams: build and upload the app package

So that users can see the bot in Teams and write to it, an **app package** is
needed: a `.zip` of `manifest.json` + two icons
(`color.png` 192×192, `outline.png` 32×32 transparent).

A minimal `manifest.json` (schema 1.17). Replace `<BOT-APP-ID>` with the app ID
from 2.1 and adjust the domains/names:

```json
{
  "$schema": "https://developer.microsoft.com/en-us/json-schemas/teams/v1.17/MicrosoftTeams.schema.json",
  "manifestVersion": "1.17",
  "version": "1.0.0",
  "id": "<BOT-APP-ID>",
  "developer": {
    "name": "My Company",
    "websiteUrl": "https://covey.example.com",
    "privacyUrl": "https://covey.example.com/privacy",
    "termsOfUseUrl": "https://covey.example.com/terms"
  },
  "name": { "short": "covey agent", "full": "covey AI agent" },
  "description": {
    "short": "AI agent in the Teams chat",
    "full": "A covey agent that works on messages and files in the Teams chat."
  },
  "icons": { "color": "color.png", "outline": "outline.png" },
  "accentColor": "#2A5B9E",
  "bots": [
    {
      "botId": "<BOT-APP-ID>",
      "scopes": ["personal", "team", "groupChat"],
      "supportsFiles": true,
      "isNotificationOnly": false
    }
  ],
  "permissions": ["identity", "messageTeamMembers"],
  "validDomains": ["covey.example.com"]
}
```

The important fields:

- **`bots[].botId`** = the app ID from 2.1 (do not confuse it — `id` at the top
  is the Teams app ID; it may be the same GUID).
- **`scopes`**: `personal` = 1:1 chat, `team` = @mention in channels,
  `groupChat` = group chats. Only include what you need.
- **`supportsFiles: true`** — **that is the switch for file attachments in
  1:1 chats.** Without it, Teams delivers no `file.download.info` attachments in
  direct messages, and `download_attachment` gets nothing to load.
  (Inline images and channel attachments arrive independently of it.)

**Uploading (sideloading)** — Teams → *Apps* → *Manage your apps* → *Upload an
app* → *Upload a custom app* → choose the `.zip` → *Add*. Prerequisite: custom
app upload is allowed for you (2.0). Afterwards open the bot as a 1:1 chat or
write `@covey agent …` in a channel where the app is installed.

**Rolling out org-wide** (instead of sideloading per user): Teams Admin Center →
*Teams apps → Manage apps* → *Upload new app*, then release it through
*Permission policies*. This needs the Teams admin role.

### 2.4 covey: deposit the secrets

Set per agent in the SecretStore (UI: agent page → Secrets, or via the API):

| Secret | Value | Purpose |
|---|---|---|
| `teams_token` | `<app-id>:<app-password>` | Outbound auth (OAuth2 client_credentials) |
| `teams_url` | *(optional)* the token endpoint | Default: `https://login.microsoftonline.com/botframework.com/oauth2/v2.0/token` (movable instance-wide through `COVEY_TEAMS_TOKEN_URL`); for **single-tenant** bots `https://login.microsoftonline.com/<tenant-id>/oauth2/v2.0/token` |
| `anthropic_api_key` *or* `claude_code_oauth_token` | An API key or `claude setup-token` | The runtime in the sandbox |

The app ID goes **before** the first `:`, the rest is the password (it may
contain `:`). Without one of the Claude values, tasks fail with
"Not logged in" — the sandbox has its own empty `HOME`.

### 2.5 covey: enable the target system + release access

The target system `teams` has to be **enabled** for the org (UI: Target systems
→ enable Microsoft Teams). If it is not active, the broker refuses every
credential release fail-closed and the webhook endpoint rejects the event.

In addition the agent has to be allowed to access `teams` according to its
`ACCESS.md` (`- system: teams scope: read,write`), and the guard rails must not
forbid `teams` / `teams:send` / `teams:reply` / `teams:create_conversation` /
`teams:download_attachment`.

### 2.6 covey: set the process env

```bash
COVEY_PUBLIC_URL=https://covey.example.com        # reachable from the Bot Service, NOT localhost
COVEY_TEAMS_WEBHOOK_SECRET=<bot-app-id>           # the expected JWT audience
# optional:
COVEY_TEAMS_INTAKE_TENANTS="<tenant-id>"          # empty = all tenants
COVEY_TEAMS_ATTACHMENT_MAX_MB=25                  # size limit per attachment (1-1024)
```

> **Important:** `COVEY_TEAMS_WEBHOOK_SECRET` does **not** carry an HMAC secret
> here but the **bot app ID** — it is the audience against which the incoming
> JWT is validated. An **empty** value disables the check (dev /
> `faketeams` only). Always set it in production.

### 2.7 Testing

1. Send the bot a **1:1 message** in Teams (or @mention it in a channel).
2. In covey: does a backlog task appear at the agent? → look at the recording.
3. If the agent replies, check: does the reply arrive in the Teams chat?
4. Attach a file: does the agent load it (`download_attachment`) and engage with
   its content?
5. On a follow-up question: does the agent go `blocked`? On the user's follow-up
   message → does the agent wake up again through the `conversation.id`?
   (section 4)

> **Where to look first when troubleshooting:** *Platform → Requests*. Every
> incoming webhook from the Bot Service is there — **including the rejected
> ones**, with status and response body ("signatur ungültig", "kein agent mit
> slug …", "zielsystem teams unbekannt oder deaktiviert") — as is every outgoing
> connector call by the agent including Microsoft's answer. Credentials are
> redacted in it, bodies truncated; retention 72 h
> (`COVEY_REQUEST_LOG_RETENTION`). If no incoming entry arrives at all, the path
> ends before covey: the messaging endpoint, `COVEY_PUBLIC_URL` or the reverse
> proxy.

### 2.8 Common stumbling blocks

| Symptom | Cause / fix |
|---|---|
| The bot does not appear in Teams | Custom app upload not allowed (2.0) or the app not installed. Roll out org-wide or adjust the setup policy. |
| The message arrives but covey does not react | The messaging endpoint is wrong (`<agent-slug>`?), `COVEY_PUBLIC_URL` is not public, or the target system `teams` is not enabled (2.5). |
| `signatur ungültig` / 401 at the webhook | `COVEY_TEAMS_WEBHOOK_SECRET` ≠ the bot app ID. Both have to be the app ID from 2.1. |
| The agent does not answer back | `teams_token` is wrong (`appId:appPassword`?), the secret has expired, or the egress blocks `login.microsoftonline.com` / the connector host (section 5). The recording states the cause in plain words — "Zugang zu teams verweigert" means the secret is missing or not assigned to the agent. |
| File attachments are missing in the 1:1 chat | `supportsFiles: true` forgotten in the manifest (2.3). |
| `download_attachment` fails | The URL has expired (load promptly), the egress blocks `*.sharepoint.com`, or the file is over the limit (`COVEY_TEAMS_ATTACHMENT_MAX_MB`). |
| A config change has no effect | The agent hangs `blocked` in a running conversation. A follow-up message continues the runtime session by `--resume` — **with the system prompt from back then**. A new config only takes hold with a new session: finish/cancel the task (backlog → *Clean up*), then it takes effect. |
| Sending a file does nothing | The consent card arrived but nobody clicked — the agent parks correctly. Without `supportsFiles: true` the card does not appear at all (2.3). |
| Consent given, but nothing arrives; the request log shows `ignored` | The associated task is no longer parked (cancelled, ended otherwise, delivery delayed). Consents deliberately create no new task — start the send again (3.1). |

---

## 3. Which messages does the agent take up?

`ShouldWake` in `github.com/benjaminLedel/covey-plugin-pack/teams/webhook.go` decides:

1. **A genuine user message:** `type == "message"`, with a sender and text. That
   way the bot's *own reply* does not trigger a new wake cycle.
2. **No echo:** `from.id != recipient.id` — the bot identity as a sender is
   ignored.
3. **A tenant allowlist** (optional):

   ```bash
   COVEY_TEAMS_INTAKE_TENANTS="11111111-2222-3333-4444-555555555555"
   ```

   Set → only messages from these Microsoft 365 tenants (case-insensitively).
   Empty/unset → no restriction.

Non-`message` activities (`conversationUpdate`, `typing`, …) do not wake.
A message **with an attachment only** (a file without text) wakes as well.

### Attachments

If a message carries files, covey lists them in the task body (name, content
type, a ready-made `download_attachment` call). The agent loads them through the
action `download_attachment {"url":"…","name":"…"}` into its sandbox
(`attachments/`) and reads them with the read tool (images by vision). Details:
spec/15, "Reading attachments". Relevant for operations:

- **Egress:** for shared files the agent loads from SharePoint/OneDrive
  (`*.sharepoint.com`), for inline images from the connector host. Both have to
  be on the allowlist when egress enforcement is on (see section 5).
- **Size limit:** `COVEY_TEAMS_ATTACHMENT_MAX_MB` (default 25, valid 1–1024)
  caps every download fail-closed. Values above 1024 are clamped to 1024,
  unreadable ones leave the default in place; both appear as `WARN` in the log.
- **Identical names do not collide:** if another file of that name already sits
  under `attachments/`, the new one gets a counter (`report-2.pdf`).
  With byte-identical content the existing path stays. Important, because
  Teams and email share **the same** `attachments/` of the same sandbox.
- **Short-lived URLs:** download URLs expire — the agent should load attachments
  promptly. If a `blocked` agent wakes up late, a URL may be invalid.

### 3.1 Sending files

A bot cannot simply attach a file in Teams — the recipient has to consent, and
only their click creates the upload location. For operations that means three
things:

- **There are always two runs.** The agent asks (`send_file`), parks
  `blocked`, is woken by the click and uploads (`upload_file`). In the recording
  you therefore see two sessions per file; that is not an error.
- **`supportsFiles: true`** in the app manifest is mandatory (section 2.3) —
  without the flag the consent card does not even appear in the 1:1 chat.
- **Egress:** the upload URL points at `*.sharepoint.com` or
  `*-my.sharepoint.com` (the recipient's OneDrive). Without that host on the
  allowlist the upload fails — the same line that incoming attachments need.

If the recipient clicks "Decline", the agent is woken just the same and
finishes its assignment; it does not hang on a consent that never comes. The
upload URL is short-lived — if the agent hangs for a long time (a queue, a
budget cap), it can expire, and then it has to ask again.

If the click comes when **nobody is parked any more** (the task was cancelled or
ended otherwise, or Teams delivers late), the event fizzles out: the request
log says `ignored`, and no task arises. That is intentional — a consent is the
continuation of work already started, not a new assignment.
The recipient then sees a ticked-off card without a file following; the agent
has to start the send again.

In **channels** this route does not exist: there, file storage goes through
Microsoft Graph and is not part of this integration.

### Mapping message → agent

The mapping runs **solely through the `<agent-slug>` in the messaging endpoint
URL**. For several bots/agents you create an Azure bot registration per agent
with that agent's slug URL.

---

## 4. `blocked` ↔ conversation

If the agent asks a follow-up question (`send`/`reply`), it goes `blocked`.
The correlation key is `teams:conversation:<conversation_id>`. The user's
follow-up message is delivered by the Bot Service as a new activity; covey
correlates via the `conversation.id` and continues the agent via
`claude -p --resume`.
Details: [`../spec/15-teams-integration.md`](../../../spec/15-teams-integration.md).

---

## 5. Env reference (Teams-relevant)

| Variable | Default | Meaning |
|---|---|---|
| `COVEY_PUBLIC_URL` | `http://localhost:8494` | The base URL at which the Bot Service reaches the messaging endpoint |
| `COVEY_TEAMS_WEBHOOK_SECRET` | *(empty = JWT check off)* | The expected bot app ID (the JWT audience) |
| `COVEY_TEAMS_INTAKE_TENANTS` | *(empty = all)* | An allowlist of Microsoft 365 tenants |
| `COVEY_TEAMS_TOKEN_URL` | The Bot Framework endpoint | The instance-wide token endpoint; still overridable per agent through the secret `teams_url` |
| `COVEY_TEAMS_ATTACHMENT_MAX_MB` | `25` | The size limit per attachment loaded into the sandbox (valid 1–1024; above that it is clamped) |
| `COVEY_DAEMON_TOKEN_TTL` | `15m` | The TTL of the credential passed into the sandbox |
| `COVEY_EGRESS_ENFORCE` | `false` | Switch on the egress allowlist proxy (only the `docker` provider) |
| `COVEY_EGRESS_ALLOW` | *(empty)* | Additional permitted egress hosts |
| `COVEY_REQUEST_LOG` | `true` | The request log (Platform → Requests): webhooks in, connector calls out |
| `COVEY_REQUEST_LOG_BODIES` | `true` | Record bodies too (truncated, redacted); `false` = metadata only |
| `COVEY_REQUEST_LOG_RETENTION` | `72h` | Retention of the log entries |

> **Egress:** with `COVEY_SANDBOX_PROVIDER=docker` and
> `COVEY_EGRESS_ENFORCE=true` the sandbox traffic runs through an allowlist
> proxy. For Teams the agent addresses two host families that have to be on the
> allowlist: `login.microsoftonline.com` (the token) and the regional connector
> hosts (`*.botframework.com` or `smba.trafficmanager.net`). Add them e.g. via:
>
> ```bash
> COVEY_EGRESS_ALLOW="login.microsoftonline.com, *.botframework.com, smba.trafficmanager.net, *.sharepoint.com"
> ```
>
> `*.sharepoint.com` is only necessary when agents load shared file attachments
> (`download_attachment`).
>
> Details and the hard network isolation mode as in the Zammad runbook
> ([`ops-zammad.md`](./zammad.md), section 6.1).

---

## 6. A local demo without Azure (`faketeams`)

`demo/faketeams` plays the **answering side** (bot connector + token endpoint) on
port 9998. That is how you test outbound without an Azure registration:

1. Start `go run ./demo/faketeams`.
2. Secret `teams_token = demo-app:demo-secret`, `teams_url = http://localhost:9998/token`.
3. Leave `COVEY_TEAMS_WEBHOOK_SECRET` **empty** (JWT check off).
4. Simulate an incoming message (inbound → wake covey):

   ```bash
   curl -X POST http://localhost:8494/api/webhooks/teams/<agent-slug> \
     -H 'Content-Type: application/json' -d '{
       "type":"message","id":"a1","text":"Hallo Agent",
       "serviceUrl":"http://localhost:9998","channelId":"msteams",
       "from":{"id":"29:kunde","name":"Kunde"},
       "recipient":{"id":"28:bot","name":"covey"},
       "conversation":{"id":"19:conv1","conversationType":"personal","tenantId":"t1"}}'
   ```

   `serviceUrl` points at `faketeams` — the agent's reply lands in its log.

---

## 7. Known limits / production checklist

- **JWT validation** checks the issuer, audience, expiry and signature against
  the Bot Framework JWKS (cached). Before production use, check the current
  issuer/JWKS endpoints (Microsoft changes them rarely, but with notice). An
  empty `COVEY_TEAMS_WEBHOOK_SECRET` = the check is off, dev only.
- **The app password is long-lived.** Like Zammad's token it falls into the
  "target system connected by key" case: the built-in `SecretStore` keeps it
  encrypted and passes it through short-lived; the runtime connector access then
  already runs through a short-lived exchanged token. Rotate the secret
  regularly.
- **Files in both directions, but only in chats.** Incoming attachments are read
  (`download_attachment`, section 3), outgoing ones run through the file consent
  flow (section 3.1). **Not** yet covered: adaptive cards, files into
  **channels** and channel/team administration through Microsoft Graph
  (spec/15, section "Scope").
- The general MVP limits (egress hardening, retry/reconnect, the budget cap,
  `webhook_events` retention) apply as in the Zammad runbook
  ([`ops-zammad.md`](./zammad.md), section 7).

---

## 8. Template: an access/provisioning ticket (EN)

A ready-made text to copy to the Azure/M365 admin when a bot is to be created in
a foreign tenant (e.g. for a pilot at another company). Fill in `<…>` before
sending.

```text
Subject: Azure Bot (Bot Framework) for AI-agent pilot — <Company / Tenant>

Context
We are evaluating how to roll out AI agents across the organization (platform:
covey — it manages AI agents like employees). We want to connect Microsoft Teams
as the human<->agent channel and therefore need an Azure Bot based on the
Microsoft Bot Framework in the <Company> Microsoft 365 tenant.

What we need — please pick ONE option
  Option A (I set it up myself): grant me, for the pilot,
    - role "Application Administrator" (or Cloud Application Administrator) in Entra ID,
    - "Contributor" on <subscription / resource group>,
    - a Teams setup policy that allows "Upload custom apps".
  Option B (you provision — preferred): create and hand over to me
    - an "Azure Bot" resource (Multi-Tenant) with the Microsoft Teams channel enabled,
    - its App Registration incl. a Client Secret,
    - and send me securely: Application (client) ID, Client Secret, Tenant ID.

Messaging endpoint (enter when creating the bot resource)
  https://<covey-host>/api/webhooks/teams/<agent-slug>
  (I will provide the final URL once the pilot's covey instance is up.)

Teams side
  Please either enable custom-app upload for me, or approve the app org-wide
  later (Teams Admin Center -> Manage apps).

No Graph / no admin consent required
  For sending/receiving (including 1:1 file attachments) the bot authenticates
  with App ID + Secret against the Bot Framework — it needs NO Microsoft Graph
  API permissions and NO tenant admin consent.

Pilot scope
  <e.g. 1 bot / 1 agent, 1:1 chat only, test user group X>.

Data protection
  The bot receives chat messages and attached files from the test users; these
  are processed by the AI agent. Data subjects / data types / retention:
  <fill in>. Happy to align with <DPO / Security> if needed.

Secret handling
  Please deliver the Client Secret via <secure channel>; rotation interval
  <e.g. 6 months>.
```

After provisioning, the covey side follows (a manifest with
`supportsFiles: true`, secrets, enabling the target system) according to
section 2.
