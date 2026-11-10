---
slug: teams-app
title: Microsoft Teams (organisation bot)
description: One Teams bot for your whole organisation, set up in covey's interface — every agent with access can be written to and post in channels, and can work with the writer's own Teams access. No Azure registration per agent, no server configuration.
---

A runbook for the connector `teamsapp`. It is the second way into Teams beside
the [per-agent bot](teams.md): **one bot per organisation**, shared by all
its agents, configured by an org admin in covey. Design and security
reasoning: [`spec/15`](../../../spec/15-teams-integration.md), section "An
organisation's Teams bot".

| | `teamsapp` (this page) | `teams` (per-agent bot) |
|---|---|---|
| Azure registration | one per organisation | one per agent |
| Configured | in covey's interface | secrets per agent |
| Teams app package | downloaded from covey | hand-written |
| Sender in Teams | your bot's name, the agent's name in the card | the agent's own bot name |
| Posting into a channel unprompted | yes (`post`) | no |
| Acting with the writer's own access | yes (`teamsuser`) | no |
| Files | not yet | yes |

## 1. In Azure: the app registration and the bot

1. **Entra ID → App registrations → New registration.** Single tenant is
   enough. Note the *Application (client) ID* and the *Directory (tenant) ID*.
2. **Certificates & secrets → New client secret.** Note the value.
3. **Azure Bot** resource with this app's ID (type *Single Tenant*). Under
   *Channels*, add **Microsoft Teams**. The messaging endpoint follows in
   step 3 below.

## 2. In covey: enter the bot

**Target systems → Microsoft Teams → Set up bot** (org admin): name, app ID,
tenant ID, client secret. *Check and save* asks Microsoft whether the three
belong together; a typo fails here, with Microsoft's reason. The secret is
stored sealed and never shown again. It takes effect at once.

Afterwards covey shows what to enter in Azure:

3. **Messaging endpoint** → the Azure Bot's *Configuration*.
4. **Redirect URIs** → the app registration's *Authentication* (Web) — needed
   for signing people in (section 4).
5. **Download app package** and upload it: Teams admin centre → *Manage
   apps* → *Upload new app* (or Teams → *Apps* → *Upload a custom app*). Add
   it to the teams whose channels agents should use.
6. Switch on *Microsoft Teams (organisation bot)* in the target systems and
   grant it in each agent's `ACCESS.md`:

   ```
   - system: teamsapp scope: read,write
   ```

## 3. In Teams

- **Direct chat:** open the bot and write. With one agent it takes every
  message; with several, start with the name: "Vera, can you check the
  invoice?". The chat then stays with Vera until another name opens a
  message.
- **Channel:** `@covey Vera, …`. Teams delivers channel messages only when
  the bot is mentioned. Under *Channels* on the settings page a channel can
  get a default colleague for messages that name nobody.
- **Agents posting:** `list_channels` shows the channels; `post
  {"channel":"Sales/Leads","text":"…"}` starts a thread. Replies in that
  thread come back to the agent that posted.

A channel appears once the bot has been added to its team and mentioned
there once — that is when Teams tells covey about the team.

## 4. Agents that work as the person writing

An agent that also holds `teamsuser` in its ACCESS.md works with **the
writer's own Teams access**: Ada's message runs as Ada, Bo's next one as Bo.
The first time somebody writes, the bot asks them to sign in (silently
through Teams' single sign-on where it is set up) and handles their message
afterwards. Every message sent in their name ends with "— sent by <agent>
(covey)". Details: [Teams as a person](teams-user.md).

Delegated access runs through the bot's app registration — or, if you enter
one under *Single sign-on and delegated access*, through an app registration
of its own. On that registration:

1. **Expose an API:** Application ID URI = the *Application ID URI for
   single sign-on* covey shows, a scope `access_as_user`, and as authorized
   client applications Teams' `1fec8e78-bce4-4aaf-ab1b-5451cc387264`
   (desktop, mobile) and `5e3ce6c0-2b1f-4285-8d4b-75ee78787346` (web).
2. **API permissions → Microsoft Graph → Delegated:** `offline_access`,
   `User.Read`, `Chat.ReadWrite`, `ChatMessage.Send`, `Team.ReadBasic.All`,
   `Channel.ReadBasic.All`, `ChannelMessage.Read.All`, `ChannelMessage.Send`
   — and *Grant admin consent*, so people are not asked one by one.
3. **Azure Bot → Configuration → OAuth connection settings:** a connection
   named `covey` (or the name entered in covey), provider *Azure Active
   Directory v2*, the delegation app's ID and secret, token exchange URL =
   the Application ID URI from step 1.
4. Download the app package again and update it in Teams.

A run that no Teams message started — a heartbeat — never gets a person's
access.

## 5. What keeps organisations apart

- Each bot has its own endpoint, checked against its own app ID; what
  arrives there reaches only its organisation, and only from its tenant.
- An app ID can be configured by one organisation only.
- Every agent action is checked in the control plane against the bot's own
  conversations and teams. The connector host comes from covey's records,
  never from the agent.
- A personal chat belongs to the colleague it is bound to; colleagues of the
  same organisation share channel threads, as they share the channel.

## 6. Common stumbling blocks

| Symptom | Cause |
|---|---|
| "Microsoft did not accept app ID, secret and tenant together" | One of the three is wrong, or the secret expired. |
| "this app ID is configured by another organisation" | The registration is already in use in covey. |
| No endpoint shown | The installation has no site address configured. |
| The bot answers "serves another Microsoft 365 organisation" | The writer's tenant is not the one entered for the bot. |
| `list_channels` is empty | The bot is not in a team yet, or has not been mentioned there since it was added. |
| Delivery refused (401) in the request log | The Azure Bot's app is not the one entered in covey. |
| The bot always sends the browser sign-in | Single sign-on is not set up (section 4, steps 1 and 3) or admin consent is missing. |
