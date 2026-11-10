---
slug: teams-app
title: Microsoft Teams (covey app)
description: One Teams app for the whole installation — connect a Microsoft 365 tenant once, upload the app, and every agent with access can be written to and post in channels. No Azure registration per agent.
---

A runbook for the connector `teamsapp`. It is the second way into Teams beside
the [per-agent bot](teams.md): **one bot registration per covey
installation**, shared by every organisation and every agent on it. Design
and security reasoning: [`spec/15`](../../../spec/15-teams-integration.md),
section "The installation's Teams app".

| | `teamsapp` (this page) | `teams` (per-agent bot) |
|---|---|---|
| Azure registration | once per installation | once per agent |
| Teams app package | one, from the settings page | one per agent, hand-written |
| Sender in Teams | "covey", the agent's name in the card | the agent's own bot name |
| Posting into a channel unprompted | yes (`post`) | no |
| Files | not yet | yes |

## 1. Once per installation: the app registration

Whoever runs the installation does this once; organisations on it do not.

1. **Entra ID → App registrations → New registration.** Supported account
   types: *Accounts in any organizational directory (multitenant)*. Redirect
   URI (Web): `https://<your covey>/api/teamsapp/callback`.
2. **Certificates & secrets → New client secret.** Note the value.
3. **Manifest:** set `"groupMembershipClaims": "DirectoryRole"`. covey reads
   the signed-in person's directory roles from the ID token to make sure an
   administrator links the tenant; without this setting the token carries
   none and every link is refused.
4. **Azure Bot** resource (multi-tenant) with this app's ID. Messaging
   endpoint: `https://<your covey>/api/webhooks/teamsapp`. Under *Channels*,
   add **Microsoft Teams**.
5. On the control plane:

   ```sh
   COVEY_TEAMSAPP_APP_ID=<application (client) id>
   COVEY_TEAMSAPP_APP_PASSWORD=<client secret>
   COVEY_SITE_URL=https://<your covey>     # required: Microsoft returns only to registered addresses
   ```

   Restart. The log says `teams app active`, and the connector *Microsoft
   Teams (covey app)* appears in every organisation's target systems. The
   password stays in the control plane; no sandbox ever receives it.

## 2. Per organisation: connect and upload

1. **Target systems → Microsoft Teams → Connect Microsoft 365** (org admin).
   Sign in as a **Teams Administrator or Global Administrator** of your
   Microsoft tenant. The sign-in has to finish in the same browser that
   started it.
2. **Download app package**, then upload it: Teams admin centre → *Manage
   apps* → *Upload new app* (or Teams → *Apps* → *Upload a custom app*). Add
   it to the teams whose channels agents should use.
3. Switch on *Microsoft Teams (covey app)* in the target systems, and grant
   it in each agent's `ACCESS.md`:

   ```
   - system: teamsapp scope: read,write
   ```

   `read` lets the agent receive messages and list channels, `write` lets it
   reply and post.

## 3. In Teams

- **Direct chat:** open the covey app and write. With one agent in the
  organisation it takes every message; with several, start with the name:
  "Vera, can you check the invoice?". The chat then stays with Vera until
  another name opens a message.
- **Channel:** `@covey Vera, …`. Teams delivers channel messages only when
  the app is mentioned. Under *Channels* on the settings page a channel can
  get a default colleague for messages that name nobody.
- **Agents posting:** `list_channels` shows the channels; `post
  {"channel":"Sales/Leads","text":"…"}` starts a thread. Replies in that
  thread come back to the agent that posted.

A channel appears once the app has been added to its team and mentioned
there once (or reinstalled) — that is when Teams tells covey about the team.

## 4. Agents that work as the person writing

An agent that holds `teamsuser` in its ACCESS.md works with **the writer's
own Teams access**: Ada's message runs as Ada, Bo's next one as Bo. The first
time somebody writes, the bot asks them to sign in (silently through Teams'
single sign-on where it is set up) and handles their message afterwards.
Every message sent in their name ends with "— sent by <agent> (covey)".

Once per installation, on the app registration of section 1:

1. **Expose an API:** Application ID URI
   `api://<your covey host>/botid-<application id>`, a scope
   `access_as_user`, and as authorized client applications Teams'
   `1fec8e78-bce4-4aaf-ab1b-5451cc387264` (desktop, mobile) and
   `5e3ce6c0-2b1f-4285-8d4b-75ee78787346` (web).
2. **API permissions → Microsoft Graph → Delegated:** `offline_access`,
   `User.Read`, `Chat.ReadWrite`, `ChatMessage.Send`, `Team.ReadBasic.All`,
   `Channel.ReadBasic.All`, `ChannelMessage.Read.All`, `ChannelMessage.Send` —
   and *Grant admin consent* per tenant, so people are not asked one by one.
3. **Redirect URI** in addition: `https://<your covey>/api/teamsapp/person-callback`
   (the browser fallback).
4. **Azure Bot → Configuration → OAuth connection settings:** a connection
   named `covey` (or `COVEY_TEAMSAPP_SSO_CONNECTION`), provider *Azure Active
   Directory v2*, this app's ID and secret, token exchange URL = the
   Application ID URI from step 1.
5. Download the app package again and update it in Teams: it now carries
   the single sign-on entry (`webApplicationInfo`, version 1.1.0).

A run that no Teams message started — a heartbeat — never gets a person's
access.

## 5. What keeps organisations apart

- A message is routed by the sender's **tenant** to the organisation that
  linked it; an unlinked tenant reaches nobody.
- A tenant is linked by an administrator of that tenant, proven by
  Microsoft's ID token, and only from the browser that started the link. A
  tenant held by one organisation cannot be linked by another.
- Every agent action is checked in the control plane against the
  organisation's own conversations and teams. The connector host comes from
  covey's records, never from the agent.
- A personal chat belongs to the colleague it is bound to; colleagues of the
  same organisation share channel threads, as they share the channel.

## 6. Common stumbling blocks

| Symptom | Cause |
|---|---|
| "no site address configured" | `COVEY_SITE_URL` is not set. |
| "the sign-in carried no directory roles" | Step 1.3: `groupMembershipClaims` is missing in the manifest. |
| "only a Teams or Global Administrator …" | The account is not an administrator of that tenant. |
| "does not belong to a link started in this browser" | The sign-in finished in another browser, or took longer than 15 minutes. |
| The bot answers "not connected to covey yet" | The tenant has not been linked (section 2.1). |
| `list_channels` is empty | The app is not in a team yet, or has not been mentioned there since it was added. |
| Delivery refused (401) in the request log | The messaging endpoint's bot is not the app in `COVEY_TEAMSAPP_APP_ID`. |
| The bot always sends the browser sign-in | Single sign-on is not set up (section 4, steps 1 and 4) or admin consent is missing. |
