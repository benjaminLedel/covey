---
slug: teams-user
title: Microsoft Teams als Person
description: Eine Person lässt einen Agenten per delegiertem Zugriff in ihrem eigenen Teams arbeiten – er liest ihre Chats und Kanäle und schreibt in ihrem Namen, markiert als vom Agenten gesendet. Proof of Concept.
---

Ein Runbook für den Konnektor `teamsuser`. Mit dem [Organisations-Bot](teams-app.md)
arbeitet ein Agent, der ihn hat, als **die Person, die ihm schreibt**
(Abschnitt 4 dort). Diese Seite beschreibt den zweiten Weg: **Eine Person
überlässt einem Agenten ihr eigenes Teams** für Läufe, die keine
Teams-Nachricht ausgelöst hat (Herzschlag, geplante Zusammenfassung): Der
Agent liest die Chats und Kanäle, in denen diese Person ist, und antwortet
oder postet in ihrem Namen. Er sieht genau, was die Person sieht, nicht mehr.
Entwurf: [`spec/15`](../../../spec/15-teams-integration.md), Abschnitt „Teams
as a person".

## 1. Voraussetzung: der Bot der Organisation

Delegierter Zugriff läuft über den [Teams-Bot](teams-app.md) Ihrer
Organisation – seine App-Registrierung oder die daneben eingetragene
Delegations-App. Die delegierten Graph-Rechte und die Umleitungs-URI werden
dort eingerichtet, wie in Abschnitt 4 jener Seite beschrieben. Auf dem Server
wird nichts konfiguriert.

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
