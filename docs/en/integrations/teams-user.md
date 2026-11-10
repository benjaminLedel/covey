---
slug: teams-user
title: Microsoft Teams as a person
description: A person lets an agent work in their own Teams through delegated access — it reads their chats and channels and writes in their name, marked as sent by the agent. Proof of concept.
---

A runbook for the connector `teamsuser` (proof of concept). Where the
[covey app](teams-app.md) is a bot people write to, this one lets **a person
hand an agent their own Teams**: the agent reads the chats and channels that
person is in and answers or posts in their name. It sees exactly what the
person sees, nothing more. Design: [`spec/15`](../../../spec/15-teams-integration.md),
section "Teams as a person".

## 1. Once per installation: the app registration

A registration of its own, not the Teams bot's.

1. **Entra ID → App registrations → New registration**, multitenant.
   Redirect URI (Web): `https://<your covey>/api/teamsuser/callback`.
2. **API permissions → Microsoft Graph → Delegated:** `offline_access`,
   `User.Read`, `Chat.ReadWrite`, `ChatMessage.Send`, `Team.ReadBasic.All`,
   `Channel.ReadBasic.All`, `ChannelMessage.Read.All`, `ChannelMessage.Send`.
   `ChannelMessage.Read.All` needs an administrator's consent in each tenant.
3. **Certificates & secrets → New client secret.**
4. On the control plane:

   ```sh
   COVEY_TEAMSUSER_APP_ID=<application (client) id>
   COVEY_TEAMSUSER_APP_PASSWORD=<client secret>
   COVEY_SITE_URL=https://<your covey>
   ```

   The tokens are sealed with the master key; without one the connector is
   not offered.

## 2. Per person: connect

1. **Target systems → Microsoft Teams as a person:** choose the agent,
   **Connect my Teams**, sign in with your own Microsoft account — in the
   same browser.
2. Switch on *Microsoft Teams as a person* and grant it in the agent's
   `ACCESS.md`:

   ```
   - system: teamsuser scope: read,write
   ```

3. Optional, recommended at first: a guard rail *require approval* on
   `teamsuser:send_chat` and `teamsuser:post_channel`, so you approve every
   message before it goes out.

## 3. What the agent can do

| Action | Scope | |
|---|---|---|
| `list_chats` | read | your chats with topic and members |
| `read_chat` | read | the latest messages of a chat |
| `send_chat` | write | a message into a chat, in your name |
| `list_channels` | read | the teams and channels you belong to |
| `read_channel` | read | the latest messages of a channel |
| `post_channel` | write | a post or a reply in a channel, in your name |

Every message the agent sends ends with "— sent by <agent> (covey)".

## 4. Limits of the proof of concept

- One person per agent; connecting again replaces the earlier person.
- Teams only — no mail, calendar or files through this delegation.
- If Microsoft stops refreshing the token (password change, revoked
  consent), the agent's actions fail with "connect your Teams again" — it
  never acts as anyone else.
- Disconnect on the same page (you, or an org admin).
