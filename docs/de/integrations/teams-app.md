---
slug: teams-app
title: Microsoft Teams (covey-App)
description: Eine Teams-App für die ganze Installation – Microsoft-365-Tenant einmal verbinden, App hochladen, und jeder Agent mit Zugriff ist ansprechbar und schreibt in Kanäle. Keine Azure-Registrierung pro Agent.
---

Ein Runbook für den Konnektor `teamsapp`. Er ist der zweite Weg nach Teams
neben dem [Bot pro Agent](../../en/integrations/teams.md): **eine
Bot-Registrierung pro covey-Installation**, geteilt von allen Organisationen
und Agenten darauf. Entwurf und Sicherheitsbegründung:
[`spec/15`](../../../spec/15-teams-integration.md), Abschnitt „The
installation's Teams app".

| | `teamsapp` (diese Seite) | `teams` (Bot pro Agent) |
|---|---|---|
| Azure-Registrierung | einmal pro Installation | einmal pro Agent |
| Teams-App-Paket | eines, aus der Einstellungsseite | eines pro Agent, von Hand |
| Absender in Teams | „covey", der Agentenname in der Karte | der eigene Bot-Name des Agenten |
| Ungefragt in einen Kanal posten | ja (`post`) | nein |
| Dateien | noch nicht | ja |

## 1. Einmal pro Installation: die App-Registrierung

Das macht, wer die Installation betreibt – einmal, nicht jede Organisation.

1. **Entra ID → App-Registrierungen → Neue Registrierung.** Unterstützte
   Kontotypen: *Konten in einem beliebigen Organisationsverzeichnis
   (mehrinstanzenfähig)*. Umleitungs-URI (Web):
   `https://<ihr covey>/api/teamsapp/callback`.
2. **Zertifikate & Geheimnisse → Neuer geheimer Clientschlüssel.** Den Wert
   notieren.
3. **Manifest:** `"groupMembershipClaims": "DirectoryRole"` setzen. covey liest
   die Verzeichnisrollen der angemeldeten Person aus dem ID-Token, damit eine
   Administration den Tenant verknüpft; ohne die Einstellung trägt das Token
   keine Rollen, und jede Verknüpfung wird abgelehnt.
4. **Azure-Bot**-Ressource (mehrinstanzenfähig) mit der ID dieser App.
   Messaging-Endpunkt: `https://<ihr covey>/api/webhooks/teamsapp`. Unter
   *Kanäle* **Microsoft Teams** hinzufügen.
5. Auf der Control Plane:

   ```sh
   COVEY_TEAMSAPP_APP_ID=<Anwendungs-ID (Client)>
   COVEY_TEAMSAPP_APP_PASSWORD=<geheimer Clientschlüssel>
   COVEY_SITE_URL=https://<ihr covey>     # Pflicht: Microsoft leitet nur an registrierte Adressen zurück
   ```

   Neu starten. Das Log meldet `teams app active`, und der Konnektor
   *Microsoft Teams (covey app)* erscheint in den Zielsystemen jeder
   Organisation. Das Passwort bleibt in der Control Plane; keine Sandbox
   bekommt es je.

## 2. Pro Organisation: verbinden und hochladen

1. **Zielsysteme → Microsoft Teams → Microsoft 365 verbinden**
   (Org-Administration). Als **Teams-Administrator oder globaler
   Administrator** des eigenen Microsoft-Tenants anmelden. Die Anmeldung muss
   im selben Browser enden, in dem sie begonnen hat.
2. **App-Paket herunterladen** und hochladen: Teams Admin Center → *Apps
   verwalten* → *Neue App hochladen* (oder Teams → *Apps* → *Benutzerdefinierte
   App hochladen*). Den Teams hinzufügen, in deren Kanälen Agenten arbeiten
   sollen.
3. *Microsoft Teams (covey app)* in den Zielsystemen einschalten und in der
   `ACCESS.md` jedes Agenten freigeben:

   ```
   - system: teamsapp scope: read,write
   ```

   `read` lässt den Agenten Nachrichten empfangen und Kanäle auflisten,
   `write` antworten und posten.

## 3. In Teams

- **Direkter Chat:** die covey-App öffnen und schreiben. Mit einem Agenten in
  der Organisation nimmt er jede Nachricht; mit mehreren mit dem Namen
  beginnen: „Vera, kannst du die Rechnung prüfen?". Der Chat bleibt dann bei
  Vera, bis ein anderer Name eine Nachricht eröffnet.
- **Kanal:** `@covey Vera, …`. Teams liefert Kanalnachrichten nur, wenn die App
  erwähnt wird. Unter *Kanäle* in der Einstellungsseite bekommt ein Kanal
  einen Standard-Kollegen für Nachrichten, die niemanden nennen.
- **Agenten posten:** `list_channels` zeigt die Kanäle; `post
  {"channel":"Vertrieb/Leads","text":"…"}` beginnt einen Thread. Antworten
  darin gehen an den Agenten, der gepostet hat.

Ein Kanal taucht auf, sobald die App seinem Team hinzugefügt und dort einmal
erwähnt (oder neu installiert) wurde – dann erfährt covey von dem Team.

## 4. Agenten, die als die schreibende Person arbeiten

Ein Agent mit `teamsuser` in der ACCESS.md arbeitet mit **dem eigenen
Teams-Zugriff der Person, die schreibt**: Adas Nachricht läuft als Ada, Bos
nächste als Bo. Wer zum ersten Mal schreibt, wird um eine Anmeldung gebeten
(still über das Single Sign-on von Teams, wo es eingerichtet ist), danach
wird die Nachricht abgearbeitet. Jede Nachricht in ihrem Namen endet mit
„— sent by <Agent> (covey)".

Einmal pro Installation, an der App-Registrierung aus Abschnitt 1:

1. **Eine API verfügbar machen:** Anwendungs-ID-URI
   `api://<Host Ihres covey>/botid-<Anwendungs-ID>`, ein Scope
   `access_as_user`, und als autorisierte Clientanwendungen die von Teams:
   `1fec8e78-bce4-4aaf-ab1b-5451cc387264` (Desktop, Mobil) und
   `5e3ce6c0-2b1f-4285-8d4b-75ee78787346` (Web).
2. **API-Berechtigungen → Microsoft Graph → Delegiert:** `offline_access`,
   `User.Read`, `Chat.ReadWrite`, `ChatMessage.Send`, `Team.ReadBasic.All`,
   `Channel.ReadBasic.All`, `ChannelMessage.Read.All`, `ChannelMessage.Send` –
   und pro Tenant *Administratorzustimmung erteilen*, damit nicht jede Person
   einzeln gefragt wird.
3. **Umleitungs-URI** zusätzlich: `https://<ihr covey>/api/teamsapp/person-callback`
   (der Rückfall über den Browser).
4. **Azure Bot → Konfiguration → OAuth-Verbindungseinstellungen:** eine
   Verbindung namens `covey` (oder `COVEY_TEAMSAPP_SSO_CONNECTION`), Anbieter
   *Azure Active Directory v2*, ID und Geheimnis dieser App,
   Tokenaustausch-URL = die Anwendungs-ID-URI aus Schritt 1.
5. Das App-Paket neu herunterladen und in Teams aktualisieren: Es trägt jetzt
   den Single-Sign-on-Eintrag (`webApplicationInfo`, Version 1.1.0).

Ein Lauf, den keine Teams-Nachricht ausgelöst hat – ein Herzschlag –,
bekommt nie den Zugriff einer Person.

## 5. Was die Organisationen trennt

- Eine Nachricht geht über den **Tenant** des Absenders an die Organisation,
  die ihn verknüpft hat; ein unverknüpfter Tenant erreicht niemanden.
- Verknüpft wird ein Tenant von einer Administration dieses Tenants,
  belegt durch das ID-Token von Microsoft, und nur aus dem Browser, der die
  Verknüpfung begonnen hat. Ein Tenant, den eine Organisation hält, kann keine
  andere verknüpfen.
- Jede Agentenaktion prüft die Control Plane gegen die eigenen Gespräche und
  Teams der Organisation. Der Connector-Host kommt aus coveys Aufzeichnungen,
  nie vom Agenten.
- Ein persönlicher Chat gehört dem Kollegen, an den er gebunden ist; Threads
  im Kanal teilen sich die Kollegen einer Organisation, wie den Kanal selbst.

## 6. Häufige Stolpersteine

| Symptom | Ursache |
|---|---|
| „no site address configured" | `COVEY_SITE_URL` ist nicht gesetzt. |
| „the sign-in carried no directory roles" | Schritt 1.3: `groupMembershipClaims` fehlt im Manifest. |
| „only a Teams or Global Administrator …" | Das Konto ist keine Administration dieses Tenants. |
| „does not belong to a link started in this browser" | Die Anmeldung endete in einem anderen Browser oder dauerte länger als 15 Minuten. |
| Der Bot antwortet „not connected to covey yet" | Der Tenant ist nicht verknüpft (Abschnitt 2.1). |
| `list_channels` ist leer | Die App ist noch in keinem Team oder wurde dort seit dem Hinzufügen nicht erwähnt. |
| Zustellung abgelehnt (401) im Anfragelog | Der Bot des Messaging-Endpunkts ist nicht die App aus `COVEY_TEAMSAPP_APP_ID`. |
| Der Bot schickt immer die Anmeldung im Browser | Single Sign-on ist nicht eingerichtet (Abschnitt 4, Schritte 1 und 4) oder die Administratorzustimmung fehlt. |
