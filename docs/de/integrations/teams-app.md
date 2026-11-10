---
slug: teams-app
title: Microsoft Teams (Organisations-Bot)
description: Ein Teams-Bot für die ganze Organisation, eingerichtet in der covey-Oberfläche – jeder Agent mit Zugriff ist ansprechbar, schreibt in Kanäle und kann mit dem eigenen Teams-Zugriff der schreibenden Person arbeiten. Keine Azure-Registrierung pro Agent, keine Server-Konfiguration.
---

Ein Runbook für den Konnektor `teamsapp`. Er ist der zweite Weg nach Teams
neben dem [Bot pro Agent](../../en/integrations/teams.md): **ein Bot pro
Organisation**, geteilt von allen ihren Agenten, eingerichtet von einer
Org-Administration in covey. Entwurf und Sicherheitsbegründung:
[`spec/15`](../../../spec/15-teams-integration.md), Abschnitt „An
organisation's Teams bot".

| | `teamsapp` (diese Seite) | `teams` (Bot pro Agent) |
|---|---|---|
| Azure-Registrierung | eine pro Organisation | eine pro Agent |
| Eingerichtet | in der covey-Oberfläche | Secrets pro Agent |
| Teams-App-Paket | aus covey herunterladen | von Hand |
| Absender in Teams | Name Ihres Bots, der Agentenname in der Karte | der eigene Bot-Name des Agenten |
| Ungefragt in einen Kanal posten | ja (`post`) | nein |
| Mit dem Zugriff der schreibenden Person arbeiten | ja (`teamsuser`) | nein |
| Dateien | noch nicht | ja |

## 1. In Azure: App-Registrierung und Bot

1. **Entra ID → App-Registrierungen → Neue Registrierung.** Ein Mandant
   genügt. *Anwendungs-ID (Client)* und *Verzeichnis-ID (Mandant)* notieren.
2. **Zertifikate & Geheimnisse → Neuer geheimer Clientschlüssel.** Den Wert
   notieren.
3. **Azure-Bot**-Ressource mit der ID dieser App (Typ *Single Tenant*). Unter
   *Kanäle* **Microsoft Teams** hinzufügen. Den Messaging-Endpunkt gibt es in
   Schritt 3 unten.

## 2. In covey: den Bot eintragen

**Zielsysteme → Microsoft Teams → Bot einrichten** (Org-Administration):
Name, App-ID, Tenant-ID, Clientschlüssel. *Prüfen und speichern* fragt bei
Microsoft, ob die drei zusammenpassen; ein Tippfehler scheitert hier, mit
Microsofts Begründung. Das Secret wird versiegelt gespeichert und nie wieder
angezeigt. Es wirkt sofort.

Danach zeigt covey, was in Azure einzutragen ist:

3. **Messaging-Endpunkt** → *Konfiguration* des Azure-Bots.
4. **Umleitungs-URIs** → *Authentifizierung* (Web) der App-Registrierung –
   nötig für das Anmelden von Personen (Abschnitt 4).
5. **App-Paket herunterladen** und hochladen: Teams Admin Center → *Apps
   verwalten* → *Neue App hochladen* (oder Teams → *Apps* →
   *Benutzerdefinierte App hochladen*). Den Teams hinzufügen, in deren Kanälen
   Agenten arbeiten sollen.
6. *Microsoft Teams (organisation bot)* in den Zielsystemen einschalten und in
   der `ACCESS.md` jedes Agenten freigeben:

   ```
   - system: teamsapp scope: read,write
   ```

## 3. In Teams

- **Direkter Chat:** den Bot öffnen und schreiben. Mit einem Agenten nimmt
  er jede Nachricht; mit mehreren mit dem Namen beginnen: „Vera, kannst du die
  Rechnung prüfen?". Der Chat bleibt dann bei Vera, bis ein anderer Name eine
  Nachricht eröffnet.
- **Kanal:** `@covey Vera, …`. Teams liefert Kanalnachrichten nur, wenn der
  Bot erwähnt wird. Unter *Kanäle* in der Einstellungsseite bekommt ein Kanal
  einen Standard-Kollegen für Nachrichten, die niemanden nennen.
- **Agenten posten:** `list_channels` zeigt die Kanäle; `post
  {"channel":"Vertrieb/Leads","text":"…"}` beginnt einen Thread. Antworten
  darin gehen an den Agenten, der gepostet hat.

Ein Kanal taucht auf, sobald der Bot seinem Team hinzugefügt und dort einmal
erwähnt wurde – dann erfährt covey von dem Team.

## 4. Agenten, die als die schreibende Person arbeiten

Ein Agent, der zusätzlich `teamsuser` in der ACCESS.md hat, arbeitet mit **dem
eigenen Teams-Zugriff der schreibenden Person**: Adas Nachricht läuft als
Ada, Bos nächste als Bo. Wer zum ersten Mal schreibt, wird um eine Anmeldung
gebeten (still über das Single Sign-on von Teams, wo es eingerichtet ist),
danach wird die Nachricht abgearbeitet. Jede Nachricht in ihrem Namen endet
mit „— sent by <Agent> (covey)". Details: [Teams als Person](teams-user.md).

Delegierter Zugriff läuft über die App-Registrierung des Bots – oder, wenn
unter *Single Sign-on und delegierter Zugriff* eine eingetragen ist, über
eine eigene App-Registrierung. An dieser Registrierung:

1. **Eine API verfügbar machen:** Anwendungs-ID-URI = die *Anwendungs-ID-URI
   für Single Sign-on*, die covey anzeigt, ein Scope `access_as_user`, und
   als autorisierte Clientanwendungen die von Teams:
   `1fec8e78-bce4-4aaf-ab1b-5451cc387264` (Desktop, Mobil) und
   `5e3ce6c0-2b1f-4285-8d4b-75ee78787346` (Web).
2. **API-Berechtigungen → Microsoft Graph → Delegiert:** `offline_access`,
   `User.Read`, `Chat.ReadWrite`, `ChatMessage.Send`, `Team.ReadBasic.All`,
   `Channel.ReadBasic.All`, `ChannelMessage.Read.All`, `ChannelMessage.Send` –
   und *Administratorzustimmung erteilen*, damit nicht jede Person einzeln
   gefragt wird.
3. **Azure Bot → Konfiguration → OAuth-Verbindungseinstellungen:** eine
   Verbindung namens `covey` (oder der in covey eingetragene Name), Anbieter
   *Azure Active Directory v2*, ID und Geheimnis der Delegations-App,
   Tokenaustausch-URL = die Anwendungs-ID-URI aus Schritt 1.
4. Das App-Paket neu herunterladen und in Teams aktualisieren.

Ein Lauf, den keine Teams-Nachricht ausgelöst hat – ein Herzschlag –,
bekommt nie den Zugriff einer Person.

## 5. Was die Organisationen trennt

- Jeder Bot hat seinen eigenen Endpunkt, geprüft gegen seine eigene App-ID;
  was dort ankommt, erreicht nur seine Organisation, und nur aus ihrem Tenant.
- Eine App-ID kann nur eine Organisation eintragen.
- Jede Agentenaktion prüft die Control Plane gegen die eigenen Gespräche und
  Teams des Bots. Der Connector-Host kommt aus coveys Aufzeichnungen, nie vom
  Agenten.
- Ein persönlicher Chat gehört dem Kollegen, an den er gebunden ist; Threads
  im Kanal teilen sich die Kollegen einer Organisation, wie den Kanal selbst.

## 6. Häufige Stolpersteine

| Symptom | Ursache |
|---|---|
| „Microsoft did not accept app ID, secret and tenant together" | Einer der drei Werte stimmt nicht, oder das Secret ist abgelaufen. |
| „this app ID is configured by another organisation" | Die Registrierung ist in covey schon in Gebrauch. |
| Kein Endpunkt angezeigt | Für die Installation ist keine Website-Adresse eingestellt. |
| Der Bot antwortet „serves another Microsoft 365 organisation" | Der Tenant der schreibenden Person ist nicht der für den Bot eingetragene. |
| `list_channels` ist leer | Der Bot ist noch in keinem Team oder wurde dort seit dem Hinzufügen nicht erwähnt. |
| Zustellung abgelehnt (401) im Anfragelog | Die App des Azure-Bots ist nicht die in covey eingetragene. |
| Der Bot schickt immer die Anmeldung im Browser | Single Sign-on ist nicht eingerichtet (Abschnitt 4, Schritte 1 und 3) oder die Administratorzustimmung fehlt. |
