# 15 — Microsoft Teams integration (target system)

Connects **Microsoft Teams** as a target system so that agents can **receive and send** messages there — the chat becomes the channel between human and agent, analogous to the helpdesk in [`13-zammad-integration.md`](13-zammad-integration.md). The Teams chat is a **target system**, not covey's own conversations ([`28-team-surface.md`](28-team-surface.md)): a message in Teams wakes the agent like a ticket does, and nothing is mirrored between the two in either direction. Teams is the second webhook-driven target system (after Zammad) and the first with **OAuth2/JWT** instead of a long-lived API token.

Architecturally Teams is a **compiled target-system plugin** (`github.com/benjaminLedel/covey-plugin-pack/teams`, in the plugin pack rather than in covey's repository, pulled in by blank import — see [`10-architecture-stack.md`](10-architecture-stack.md), "Target systems as plugins"): the same `System`/`Webhooker` interface as Zammad, the same event router, the same dedup/correlation mechanics. Only the auth surface is new.

## The route: Azure Bot Framework

Teams bots do not run directly between Teams and covey but through the **Azure Bot Service** (Bot Framework). That is the canonical, documented route for bidirectional bots (@mention in channels, 1:1 chat) and fits covey's pattern exactly: a webhook inbound, a REST call outbound.

```
Teams client  ──(user writes @agent or a DM)──►  Azure Bot Service
                                                         │  POST activity (JWT)
                                                         ▼
                                          {public_url}/api/webhooks/teams/<agent-slug>
                                                         │  ParseWebhook → wake
                                                         ▼
                                                   backlog / blocked task
                                                         │  agent replies (action proxy)
                                                         ▼
                               POST {serviceUrl}/v3/conversations/…/activities  (bot connector)
```

## Three integration surfaces

### 1. Inbound: wake via the messaging endpoint

The Azure Bot Service delivers every message as an **activity** (JSON) to the bot's messaging endpoint — in covey that is `POST {public_url}/api/webhooks/teams/<agent-slug>`. Relevant fields: `type` (`message`, `conversationUpdate`, …), `id` (the activity ID), `text`, `from` (the sender), `recipient` (the bot), `conversation` (`id`, `conversationType`, `tenantId`), `serviceUrl` (the bot connector host for the reply).

- **New message / DM** → activity → event router → the agent wakes up (a wake source).
- **A follow-up message in the same conversation** → correlation via the `conversation.id` → the blocked agent wakes up.
- **Integrity:** the Bot Service signs every delivery with a **JWT** in the `Authorization: Bearer …` header (issuer `https://api.botframework.com`, audience = the bot's Microsoft app ID, RS256, keys from the Bot Framework JWKS). covey validates this token before trusting the event.
- **Idempotency:** the Bot Service retries deliveries on failure. The event router deduplicates via the activity `id` (`teams:activity:<id>`), so that the same message triggers only one wake.

### 2. Outbound: replying through the bot connector

Replies go to the **bot connector REST service** at the `serviceUrl` from the triggering activity (regional, e.g. `https://smba.trafficmanager.net/emea/`):

| Action | Call |
|---|---|
| Send a message | `POST {serviceUrl}/v3/conversations/{conversationId}/activities` |
| Reply to a message | `POST {serviceUrl}/v3/conversations/{conversationId}/activities/{activityId}` |
| Proactively open a 1:1 chat | `POST {serviceUrl}/v3/conversations` → then send an activity |

The body is a minimal activity (`{"type":"message","text":"…"}`); the connector derives sender/recipient/conversation from the URL and the token.

### 3. Auth: the broker against the bot connector

Unlike Zammad's static token, the bot connector uses **OAuth2 `client_credentials`**: the bot exchanges its **app ID + app password** (client secret) for a short-lived access token (`scope=https://api.botframework.com/.default`) that it passes to the connector as a `Bearer`.

- The **secrets broker** holds the app ID and app password and injects them into the daemon at runtime — **nothing long-lived in the sandbox** (see [`04-identity-secrets.md`](04-identity-secrets.md)). The convention for the brokered credentials:
  - `teams_token` = `"{appId}:{appPassword}"` (app ID before the first `:`, the rest = the password — analogous to the email plugin's `user:pass`),
  - `teams_url` = an optional token endpoint (default: the multi-tenant Bot Framework endpoint).
- The **short-lived connector token** is cached per daemon process and renewed before expiry — it does not leave the sandbox.

> **An honest limit.** The app password itself is a long-lived client secret (like Zammad's token it falls into the "target system connected by key" case from [`10-architecture-stack.md`](10-architecture-stack.md)). The built-in `SecretStore` keeps it encrypted and passes it through short-lived; the actual runtime access to the connector then already runs through a **short-lived token exchanged via `client_credentials`**. Real end-to-end RFC 8693 exchange down to the user context remains reserved for later delegated Graph scenarios.

## `blocked` ↔ conversation (correlation)

Teams has no ticket state like Zammad's `pending`. The natural correlation anchor is the **`conversation.id`** that comes along in every activity:

1. The agent asks a follow-up question (`send`/`reply`) and parks the task → `blocked`, **correlation key = `teams:conversation:<conversation_id>`**, plus the runtime `session_id` (see [`12-claude-code-adapter.md`](12-claude-code-adapter.md)).
2. The user replies → the Bot Service delivers the next activity.
3. covey correlates via the `conversation.id`, wakes the agent and continues.

As with Zammad, correlation is therefore "free" — no key of its own has to be looped back through the communication.

## Echo protection

The bot must not take up its own messages as work. An activity whose `from.id` equals the `recipient.id` (the bot identity) is registered (dedup) but triggers **no wake** (`Wake=false`) — the same mechanic as the `Sender=Agent` filter at Zammad. Likewise, non-`message` activities (`conversationUpdate`, `typing`, …) do not wake.

## Intake filter

An optional operational filter through ENV (12-factor, as with Zammad):

- `COVEY_TEAMS_INTAKE_TENANTS="<tenant-id>, …"` — only messages from these Microsoft 365 tenants trigger a task. Empty/unset → all tenants.

## Actions (overview)

| Action | Params | Effect |
|---|---|---|
| `send` | `service_url`, `conversation_id`, `text` | A message into an existing conversation. |
| `reply` | `service_url`, `conversation_id`, `reply_to_activity_id`, `text` | A reply to a message (without `reply_to_activity_id` → `send`). |
| `create_conversation` | `service_url`, `tenant_id`, `user_id`, `text` | A proactive 1:1 chat with a user. |
| `download_attachment` | `url`, `name` | Loads a file attachment of the message into the sandbox. |
| `send_file` | `service_url`, `conversation_id`, `path`, `description` | Asks the recipient by card whether they want to accept a file. |
| `upload_file` | `upload_url`, `path`, `service_url`, `conversation_id`, … | Uploads the bytes after consent has been given. |

`service_url` and `conversation_id` come from the triggering message (they sit in the task body). All of them are guard-rail subjects of their own (`teams:send`, `teams:reply`, `teams:create_conversation`, `teams:download_attachment`, `teams:send_file`, `teams:upload_file`).

## Reading attachments

Messages often carry files — screenshots, PDFs, logs. An incoming activity lists them in the field `attachments` (name, `contentType`, download URL). covey does **not pass the bytes through the control plane** but follows the established pattern from GitLab (`download_upload`): the event router lists the attachments as text in the task body (name, type, a ready-made `download_attachment` call), and the agent fetches the file into its sandbox when needed.

- **Materialisation into the sandbox:** `download_attachment` loads the bytes brokered (the connector token stays in the daemon) into `<workspace>/attachments/<file>` and returns the path + content type. The agent then reads the file with the read tool — images by vision. Path traversal is reduced to the basename, and a size limit (`COVEY_TEAMS_ATTACHMENT_MAX_MB`, default 25 MB) applies fail-closed.
- **Two kinds of URL:** shared files arrive as `file.download.info` with a **pre-authorised** `content.downloadUrl` (no token); inline images as `image/*` with a `contentUrl` on the connector host (**a bearer token is needed**). `download_attachment` first tries without auth and, on `401/403`, retries once with the connector token — both kinds work this way without a special case in the prompt.
- **Short-lived URLs:** Teams download URLs expire. The task body instructs the agent to fetch attachments promptly; if a `blocked` agent only wakes up late, a URL may be invalid — the agent then requests the file again.
- Pure attachment messages (a file without text) also trigger a wake.

## Sending files (the file consent flow)

Teams does **not** let a bot simply attach files. The route goes through the
recipient's consent, and only that creates the upload location — this is a
platform requirement and cannot be short-cut:

1. The agent calls `send_file` with the path of a file in its working directory.
   covey posts a card (`…card.file.consent`) with name, description and
   size; the `context.key` carries the **requested path** through both
   directions (not just the file name — otherwise it would point into the void
   for a file in a subfolder).
2. The agent ends the run with `blocked` on `teams:conversation:<id>` — exactly
   the correlation key that carries normal follow-up questions too.
3. The recipient clicks "Accept". Teams sends an **`invoke` activity**
   (`fileConsent/invoke`) with `uploadInfo.uploadUrl` — a SharePoint/
   OneDrive upload session in *their* storage.
4. The event router wakes the parked agent with the **complete**
   `upload_file` call — `path` from the returned `context.key`, the
   rest from `uploadInfo`. The agent has nothing to add and nothing to guess.
5. The agent calls `upload_file`: a PUT of the bytes with `Content-Range` to the
   upload URL — **without** the connector token, the URL authorises itself —
   followed by the completion card (`…card.file.info`) that shows the file in
   the history.

Two design decisions behind this:

- **The bytes stay in the sandbox.** Minutes can pass between the card and the
  click; the file waits in the agent's **persistent `/home`**, not in the
  control plane. That way the flow needs no intermediate storage and no new
  protocol message — it is the same `blocked` cycle as any follow-up question,
  and the agent finishes its own work.
- **A refusal wakes it just the same.** A "no" is a result, not an absence:
  without a wake the agent would hang on a consent that never comes.
- **Only correlate, never create anew.** The answer to a consent card is the
  continuation of work already started. If nobody is parked on it — the task
  finished long ago, the delivery arrives late — the event fizzles out
  (`CorrelateOnly`). Otherwise a task would arise asking an
  unsuspecting agent to upload a file it knows nothing about.

Limits: `path` is resolved against the working directory (no `..`, no
absolute paths leading out), the same size limit as for incoming
attachments applies, and the flow carries only **1:1 and group chats**. In channels there is
no consent card — there the route goes through Graph into the team's file
storage and stays outside this integration.

## The installation's Teams app (`teamsapp`)

The connector above is **one bot per agent**: an Azure registration, a secret and an app package for each. That is right where an agent should appear in Teams under a name of its own, and too much everywhere else — five agents in an organisation's channels were five registrations its Teams admin had to approve, and a hosted installation made every customer repeat the Azure setup. So there is a second connector beside it, `teamsapp`: **one bot registration per installation**, multi-tenant, serving every organisation on it (#710). The per-agent `teams` connector stays as it is.

**Why the credential stays in the control plane.** A Bot Framework credential is not scoped to a tenant: whoever holds it posts into every conversation the bot is part of, in every Microsoft tenant that installed it. Brokered into a sandbox the way `teams_token` is, it would let any organisation's agent write into another organisation's Teams. So the app password is instance configuration (`COVEY_TEAMSAPP_APP_ID` / `COVEY_TEAMSAPP_APP_PASSWORD`), never brokered, and `teamsapp` is the first target system whose actions **run in the control plane**: the daemon sees the system's `RunsInControlPlane` and hands the call over (`request_platform_action`) instead of executing it.

**Tenant → organisation.** The tenant routes every message, so it is proven, not typed. An org admin signs in with Microsoft through the installation's app (authorization code flow); the tenant comes from the ID token the token endpoint returns, never from a redirect parameter; the signed-in account must hold a Teams or Global Administrator role in that tenant (`wids` claim). The `state` is signed and names the organisation, and its nonce must match a cookie in the browser of the covey admin who started the link — a link somebody else started and sent to a tenant's administrator ends without that cookie and is refused, rather than handing the tenant to the sender's organisation. A tenant belongs to at most one organisation; linking it elsewhere is refused, never moved. Unlinking forgets everything the bot knew of the tenant.

**Inbound.** One messaging endpoint, `POST /api/webhooks/teamsapp`. The token is checked against the installation's app ID *and* its `serviceurl` claim against the body's `serviceUrl` — that host is stored and later receives the bot's credential, so a body naming another host than the token was issued for would be a way to collect it. Then the tenant gives the organisation (unlinked: the bot says so, nothing else), and the organisation's candidates are its agents with `teamsapp` in ACCESS.md. Among them, in this order: the colleague named at the start of the message (display name, first name or slug — that is how one switches); the colleague the conversation is already bound to; the channel's default, set by the organisation; the only candidate, if there is one; otherwise the bot asks who is meant. The chosen agent gets the message as a webhook event exactly as from any other system — dedup, correlation on `teamsapp:conversation:<id>`, wake.

**Outbound.** Every action is checked again in the control plane — the system is switched on for the organisation, ACCESS.md grants it with the action's scope, the guard rails allow `teamsapp:<action>` (an approval is redeemed once) — and then resolved against the organisation's own records. The connector host always comes from those records, never from the agent's parameters.

| Action | Scope | Does |
|---|---|---|
| `reply` | write | Into a conversation of the organisation. A personal or group chat only by the colleague it is bound to; a channel thread by any colleague of the organisation. |
| `list_channels` | read | The channels of the teams the bot is installed in for the organisation's tenants (fresh from Teams). |
| `post` | write | A new thread in one of those channels, by ID, `Team/Channel` or a unique name; the thread is bound to the poster, so replies come back to it. |

Teams shows the bot as the sender of every message, so each one is a card whose first line names the agent.

**Not (yet) in `teamsapp`:** files in either direction (attachments are named in the task, not fetched), a proactive 1:1 chat with a person who never wrote to the bot, the Teams Store listing (the package is uploaded per tenant).

## Teams as a person (`teamsuser`, proof of concept)

Both bots speak as a bot and see only conversations the bot is part of. The third connector lets a **person hand an agent their own Teams** (#712): delegated Microsoft Graph access, with an app registration of its own beside the installation's bot (`COVEY_TEAMSUSER_APP_ID` / `COVEY_TEAMSUSER_APP_PASSWORD`).

- **Grant.** A person signs in with Microsoft from covey and picks the agent (the same signed state and browser binding as the tenant link, `internal/msauth`). Scopes: `offline_access User.Read Chat.ReadWrite ChatMessage.Send Team.ReadBasic.All Channel.ReadBasic.All ChannelMessage.Read.All ChannelMessage.Send`. The refresh token is sealed with the master key under the agent's id and stored per agent — one person per agent in the proof of concept. Microsoft rotates refresh tokens on use; the new one is sealed and stored at every refresh. Revoking (the person, or an org admin) deletes it.
- **Use.** Every action runs in the control plane with that person's token, for that agent only, never brokered into a sandbox: `list_chats`, `read_chat`, `send_chat`, `list_channels`, `read_channel`, `post_channel`. What the agent reaches is exactly what the person reaches in Teams. Without a delegation, or when it no longer refreshes, the action says so; it never falls back to another identity.
- **Authorship.** A message sent in the person's name ends with a line naming the agent ("— sent by Vera (covey)"), so readers can tell the person from their assistant. A guard rail on `teamsuser:send_chat` / `teamsuser:post_channel` makes the person approve each one.

Not in the proof of concept: delegation for other Microsoft 365 services, several people per agent, files.

### The person who wrote (#713)

Bound to an agent, a delegation answers everybody with one person's view. In a chat with the installation's app the right identity is the **writer of the message**: Ada's message runs with Ada's access, Bo's next one with Bo's.

- **Sign-in in the chat.** When a message reaches an agent that holds `teamsuser` and covey has no token for the writer (`from.aadObjectId` in the activity's tenant), no work is created: the message is kept and the bot sends an OAuth card naming the app's single sign-on resource (`api://<host>/botid-<app id>`, also the package's `webApplicationInfo`). Teams answers with `signin/tokenExchange`; covey trades that token through the on-behalf-of grant of the installation's app, checks with Graph's `/me` that it is the writer, seals the refresh token per Microsoft user and hands the kept message to the agent. A failed exchange is answered 412 and the bot offers a browser sign-in link instead; that link names the writer and ends only for the same Microsoft user (`tid` and `oid` of the ID token).
- **Whose task.** Every task a Teams message creates or wakes records the writer before it is dispatched; a later message by somebody else in the same conversation moves it to them. A `teamsuser` action of such a task uses that person's token and no other — a missing one is an error, never a fallback to the agent's own delegation. A task no Teams message started (a heartbeat) uses only the agent's delegation, if any.
- The tokens go with the tenant link: unlinking the tenant forgets them.

## Scope of this integration

- **One bot / one agent**, app ID + app password through the built-in `SecretStore` — or the installation's shared app above.
- **The messaging endpoint** as a webhook, JWT-verified, processed idempotently.
- **Actions:** send, reply, open a 1:1 chat, load an attachment into the sandbox, send a file (the consent flow).
- **Reading attachments** through `download_attachment` (bytes into the sandbox, not into the control plane).
- **Sending attachments** through `send_file`/`upload_file` in chats — bytes likewise from the sandbox.
- **`blocked`** through the Teams conversation, correlation through the `conversation.id` — for follow-up questions as for file consents.

Later (not now): files into **channels** (Graph), channel/team administration through Microsoft Graph, delegated user context (Graph with on-behalf-of).

## Notes

- The bot needs an **Azure bot registration** (a Microsoft app ID + client secret) and the messaging endpoint `{public_url}/api/webhooks/teams/<agent-slug>`; the Teams manifest embeds the bot into Teams. Operational details: `docs/en/integrations/teams.md`.
- JWT validation checked against the Bot Framework auth documentation (as of July 2026) — check the current issuer/JWKS endpoints before production use. The expected app ID comes from the organisation secret `teams_webhook_secret`, falling back to `COVEY_TEAMS_WEBHOOK_SECRET`; with neither the endpoint refuses calls, and only `COVEY_UNSIGNED_WEBHOOKS=teams` disables validation (local tests / the `faketeams` double).
