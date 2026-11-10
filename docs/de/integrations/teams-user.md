---
slug: teams-user
title: Microsoft Teams als Person
description: Eine Person lässt einen Agenten per delegiertem Zugriff in ihrem eigenen Teams arbeiten – er liest ihre Chats und Kanäle und schreibt in ihrem Namen, markiert als vom Agenten gesendet. Proof of Concept.
---

Ein Runbook für den Konnektor `teamsuser` (Proof of Concept). Während die
[covey-App](teams-app.md) ein Bot ist, dem man schreibt, lässt dieser
Konnektor **eine Person einem Agenten ihr eigenes Teams überlassen**: Der
Agent liest die Chats und Kanäle, in denen diese Person ist, und antwortet
oder postet in ihrem Namen. Er sieht genau, was die Person sieht, nicht mehr.
Entwurf: [`spec/15`](../../../spec/15-teams-integration.md), Abschnitt „Teams
as a person".

## 1. Einmal pro Installation: die App-Registrierung

Eine eigene Registrierung, nicht die des Teams-Bots.

1. **Entra ID → App-Registrierungen → Neue Registrierung**,
   mehrinstanzenfähig. Umleitungs-URI (Web):
   `https://<ihr covey>/api/teamsuser/callback`.
2. **API-Berechtigungen → Microsoft Graph → Delegiert:** `offline_access`,
   `User.Read`, `Chat.ReadWrite`, `ChatMessage.Send`, `Team.ReadBasic.All`,
   `Channel.ReadBasic.All`, `ChannelMessage.Read.All`, `ChannelMessage.Send`.
   `ChannelMessage.Read.All` braucht in jedem Tenant die Zustimmung einer
   Administration.
3. **Zertifikate & Geheimnisse → Neuer geheimer Clientschlüssel.**
4. Auf der Control Plane:

   ```sh
   COVEY_TEAMSUSER_APP_ID=<Anwendungs-ID (Client)>
   COVEY_TEAMSUSER_APP_PASSWORD=<geheimer Clientschlüssel>
   COVEY_SITE_URL=https://<ihr covey>
   ```

   Die Tokens werden mit dem Master-Key versiegelt; ohne ihn wird der
   Konnektor nicht angeboten.

## 2. Pro Person: verbinden

1. **Zielsysteme → Microsoft Teams als Person:** den Agenten wählen, **Mein
   Teams verbinden**, mit dem eigenen Microsoft-Konto anmelden – im selben
   Browser.
2. *Microsoft Teams as a person* einschalten und in der `ACCESS.md` des
   Agenten freigeben:

   ```
   - system: teamsuser scope: read,write
   ```

3. Optional, für den Anfang empfohlen: eine Guard-Rail *Freigabe nötig* auf
   `teamsuser:send_chat` und `teamsuser:post_channel`, damit Sie jede
   Nachricht freigeben, bevor sie rausgeht.

## 3. Was der Agent kann

| Aktion | Scope | |
|---|---|---|
| `list_chats` | read | Ihre Chats mit Thema und Mitgliedern |
| `read_chat` | read | die letzten Nachrichten eines Chats |
| `send_chat` | write | eine Nachricht in einen Chat, in Ihrem Namen |
| `list_channels` | read | die Teams und Kanäle, in denen Sie sind |
| `read_channel` | read | die letzten Nachrichten eines Kanals |
| `post_channel` | write | ein Beitrag oder eine Antwort im Kanal, in Ihrem Namen |

Jede Nachricht des Agenten endet mit „— sent by <Agent> (covey)".

## 4. Grenzen des Proof of Concept

- Eine Person pro Agent; erneutes Verbinden ersetzt die frühere Person.
- Nur Teams – keine Mail, kein Kalender, keine Dateien über diese Delegation.
- Erneuert Microsoft das Token nicht mehr (Passwortwechsel, widerrufene
  Zustimmung), scheitern die Aktionen mit „Teams neu verbinden" – der Agent
  handelt nie als jemand anderes.
- Trennen auf derselben Seite (Sie selbst oder eine Org-Administration).
