# Betrieb: Covey an Microsoft Teams anschließen

Praktisches Runbook für den Schritt vom Demo-Setup (`demo/faketeams`) zu einem
**echten Teams-Bot** über den Azure Bot Service. Die Design-Hintergründe stehen
in [`../spec/15-teams-integration.md`](../spec/15-teams-integration.md); dieses
Dokument sagt, **was konkret zu tun ist** — und wo die Grenzen für den
Produktivbetrieb liegen.

> Kurzfassung: Der Adapter ist bereits Bot-Framework-kompatibel (JWT-verifizierter
> Messaging-Endpoint, OAuth2-`client_credentials` gegen den Bot Connector,
> `/v3/conversations`-Pfade). Es sind im Wesentlichen **Konfigurationsschritte in
> Azure + Teams**, kein Umbau.

---

## 1. Überblick des Datenflusses

```
Teams  ──(User @mention/DM)──►  Azure Bot Service  ──(POST Activity, JWT)──►  Covey
                                                                                │  /api/webhooks/teams/<agent-slug>
                                                                                │  JWT prüfen → Intake-Filter → Backlog-Task
                                                                                ▼
                                                                          Agent (Sandbox, Claude Code)
                                                                                │  Aktionen über Action-Proxy
Teams  ◄──(Bot Connector /v3, OAuth2-Token)───────────────────────────────────┘  send, reply, create_conversation
```

Zwei Richtungen, zwei Auth-Wege:

- **Inbound** (Bot Service → Covey): Messaging-Endpoint, verifiziert per **JWT**
  (Issuer `api.botframework.com`, Audience = Bot-App-ID, RS256/JWKS). Erwartete
  App-ID über `COVEY_TEAMS_WEBHOOK_SECRET`.
- **Outbound** (Covey → Bot Connector): REST mit einem kurzlebigen, per OAuth2
  `client_credentials` getauschten Token. App-ID + App-Passwort kommen gebrokert
  (Secret `teams_token`) und werden nie in der Sandbox persistiert.

---

## 2. Einrichtung — Schritt für Schritt

Reihenfolge: **erst Azure** (Bot registrieren), **dann Teams** (App-Paket, damit
Nutzer den Bot anschreiben können), **dann Covey** (Secrets, Zielsystem, Env).
Rechne mit ~30 Minuten, wenn du die nötigen Rollen hast.

### 2.0 Welche Rollen/Rechte brauche ich?

| Schritt | Nötige Rolle |
|---|---|
| Azure-Bot-Ressource + App-Registration anlegen | **Application Administrator** (oder Cloud Application Administrator) in Entra ID **und** *Contributor* auf einem Azure-Abo/einer Resource Group |
| Custom Teams-App hochladen (sideload) | Ein Nutzer, dessen **Teams-Setup-Policy** „Upload custom apps" erlaubt — freigeschaltet vom **Teams-Administrator** |
| App org-weit veröffentlichen/genehmigen | **Teams Administrator** (Teams Admin Center → *Teams apps → Manage apps*) |
| Covey konfigurieren (Secrets, Zielsystem, Env) | Covey **platform_admin**/**security** + der **Agent-Owner** |

> **Wichtig — kein Graph, kein Admin-Consent für den MVP.** Für reines
> Senden/Empfangen **inklusive Datei-Anhängen in 1:1-Chats** braucht der Bot
> **keine** Microsoft-Graph-API-Permissions und **keinen** Admin-Consent-Flow: Der
> Bot-Framework-Kanal authentifiziert sich selbst über App-ID + Secret
> (`client_credentials` gegen `botframework.com`). Graph-Permissions (und damit
> Admin Consent durch einen Global/Cloud-App-Admin) werden erst für spätere
> Szenarien relevant — Kanal-Nachrichten *ohne* @mention lesen (RSC), proaktives
> Anschreiben per AAD-ID über Graph. Nicht für diese Integration.

### 2.1 Azure: Bot-Registration + Secret anlegen

1. Azure Portal → **„Azure Bot"** anlegen (Ressource „Azure Bot"). Als
   *Microsoft App ID* „Create new" wählen, Typ **Multi-Tenant** (der einfachste
   Start; Single-Tenant geht auch, dann in Covey `teams_url` tenant-spezifisch
   setzen — siehe 2.4).
2. Nach dem Anlegen die **Microsoft-App-ID** (Client-ID der zugehörigen
   App-Registration) notieren — sie ist zugleich Bot-ID, Credential-Bestandteil
   und JWT-Audience.
3. **Client Secret** erzeugen: App-Registration (Entra ID → App registrations →
   die App) → *Certificates & secrets* → *New client secret*. Den **Value**
   **sofort** kopieren (wird nur einmal angezeigt) — das ist das App-Passwort.
4. **Teams-Kanal aktivieren:** Bot-Ressource → *Channels* → **Microsoft Teams**
   hinzufügen (Nutzungsbedingungen bestätigen).

> Alternative zum Portal: das **Teams Developer Portal**
> (`dev.teams.microsoft.com`) kann Bot-Registrierung, Messaging-Endpoint und
> App-Manifest in einem Rutsch erledigen. Die Rollen aus 2.0 gelten trotzdem.

### 2.2 Azure: Messaging-Endpoint setzen

Bot-Ressource → *Configuration* → **Messaging endpoint**:

```
https://covey.example.com/api/webhooks/teams/<agent-slug>
```

`<agent-slug>` = der Slug des zuständigen Agenten in Covey; über die URL wird die
Nachricht dem Agenten zugeordnet. Ersatzweise wird auch die **Agent-ID**
akzeptiert (die UUID aus der URL der Agenten-Seite) — praktisch, wenn der
Endpoint im Fremdsystem nachträglich nicht mehr änderbar ist. Der Slug ist
trotzdem die bessere Wahl: er ist lesbar und übersteht einen Neuaufbau des
Agenten. Die Basis muss **öffentlich erreichbar** sein,
sonst kann der Bot Service nicht zustellen (kein `localhost`; für lokale Tests
z. B. ein Tunnel wie `ngrok`, oder das `faketeams`-Double aus Abschnitt 6).

### 2.3 Teams: App-Paket bauen und hochladen

Damit Nutzer den Bot in Teams sehen und anschreiben können, braucht es ein
**App-Paket**: eine `.zip` aus `manifest.json` + zwei Icons
(`color.png` 192×192, `outline.png` 32×32 transparent).

Minimales `manifest.json` (Schema 1.17). Ersetze `<BOT-APP-ID>` durch die App-ID
aus 2.1 und die Domains/Namen:

```json
{
  "$schema": "https://developer.microsoft.com/en-us/json-schemas/teams/v1.17/MicrosoftTeams.schema.json",
  "manifestVersion": "1.17",
  "version": "1.0.0",
  "id": "<BOT-APP-ID>",
  "developer": {
    "name": "Meine Firma",
    "websiteUrl": "https://covey.example.com",
    "privacyUrl": "https://covey.example.com/privacy",
    "termsOfUseUrl": "https://covey.example.com/terms"
  },
  "name": { "short": "Covey-Agent", "full": "Covey KI-Agent" },
  "description": {
    "short": "KI-Agent im Teams-Chat",
    "full": "Ein Covey-Agent, der Nachrichten und Dateien im Teams-Chat bearbeitet."
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

Die wichtigen Felder:

- **`bots[].botId`** = die App-ID aus 2.1 (nicht verwechseln — `id` ganz oben ist
  die Teams-App-ID; sie darf dieselbe GUID sein).
- **`scopes`**: `personal` = 1:1-Chat, `team` = @mention in Kanälen, `groupChat`
  = Gruppen-Chats. Nur einbauen, was du brauchst.
- **`supportsFiles: true`** — **das ist der Schalter für Datei-Anhänge im
  1:1-Chat.** Ohne ihn liefert Teams in Direktnachrichten keine
  `file.download.info`-Anhänge, und `download_attachment` bekommt nichts zu laden.
  (Inline-Bilder und Kanal-Anhänge kommen unabhängig davon.)

**Hochladen (sideload)** — Teams → *Apps* → *Manage your apps* → *Upload an app*
→ *Upload a custom app* → die `.zip` wählen → *Add*. Voraussetzung: Custom-App-
Upload ist für dich erlaubt (2.0). Danach den Bot als 1:1-Chat öffnen oder in
einem Kanal, in dem die App installiert ist, `@Covey-Agent …` schreiben.

**Org-weit ausrollen** (statt pro Nutzer sideloaden): Teams Admin Center →
*Teams apps → Manage apps* → *Upload new app*, dann über *Permission policies*
freigeben. Braucht die Teams-Admin-Rolle.

### 2.4 Covey: Secrets hinterlegen

Pro Agent im SecretStore setzen (UI: Agenten-Seite → Secrets, oder via API):

| Secret | Wert | Zweck |
|---|---|---|
| `teams_token` | `<app-id>:<app-passwort>` | Outbound-Auth (OAuth2 client_credentials) |
| `teams_url` | *(optional)* Token-Endpoint | Default: `https://login.microsoftonline.com/botframework.com/oauth2/v2.0/token` (instanzweit per `COVEY_TEAMS_TOKEN_URL` verschiebbar); für **Single-Tenant**-Bots `https://login.microsoftonline.com/<tenant-id>/oauth2/v2.0/token` |
| `anthropic_api_key` *oder* `claude_code_oauth_token` | API-Key bzw. `claude setup-token` | die Runtime in der Sandbox |

App-ID steht **vor** dem ersten `:`, der Rest ist das Passwort (darf `:`
enthalten). Ohne einen der Claude-Werte scheitern Aufgaben mit „Not logged in" —
die Sandbox hat ein eigenes, leeres `HOME`.

### 2.5 Covey: Zielsystem aktivieren + Zugriff freigeben

Das Zielsystem `teams` muss für die Org **aktiviert** sein (UI: Zielsysteme →
Microsoft Teams aktivieren). Ist es nicht aktiv, verweigert der Broker
fail-closed jede Credential-Freigabe und der Webhook-Endpunkt weist das Event ab.

Zusätzlich muss der Agent laut seiner `ACCESS.md` auf `teams` zugreifen dürfen
(`- system: teams scope: read,write`), und die Guard-Rails dürfen `teams` /
`teams:send` / `teams:reply` / `teams:create_conversation` /
`teams:download_attachment` nicht verbieten.

### 2.6 Covey: Prozess-Env setzen

```bash
COVEY_PUBLIC_URL=https://covey.example.com        # vom Bot Service erreichbar, NICHT localhost
COVEY_TEAMS_WEBHOOK_SECRET=<bot-app-id>           # die erwartete JWT-Audience
# optional:
COVEY_TEAMS_INTAKE_TENANTS="<tenant-id>"          # leer = alle Tenants
COVEY_TEAMS_ATTACHMENT_MAX_MB=25                  # Größenlimit je Anhang (1-1024)
```

> **Wichtig:** `COVEY_TEAMS_WEBHOOK_SECRET` trägt hier **nicht** ein HMAC-Secret,
> sondern die **Bot-App-ID** — sie ist die Audience, gegen die das eingehende
> JWT validiert wird. Ein **leerer** Wert deaktiviert die Prüfung (nur Dev /
> `faketeams`). Im Produktivbetrieb immer setzen.

### 2.7 Testen

1. Dem Bot in Teams eine **1:1-Nachricht** schicken (oder ihn im Kanal
   @mentionen).
2. In Covey: erscheint eine Backlog-Aufgabe beim Agenten? → Recording ansehen.
3. Antwortet der Agent, prüfen: kommt die Antwort im Teams-Chat an?
4. Eine Datei anhängen: lädt der Agent sie (`download_attachment`) und geht
   inhaltlich darauf ein?
5. Bei einer Rückfrage: geht der Agent auf `blocked`? Folgenachricht des Nutzers
   → wacht der Agent über die `conversation.id` wieder auf? (Abschnitt 4)

> **Wo man beim Fehlersuchen zuerst hinschaut:** *Plattform → Requests*. Dort
> steht jeder eingehende Webhook des Bot Service — **auch der abgelehnte**, mit
> Status und Antwort-Body ("signatur ungültig", "kein agent mit slug …",
> "zielsystem teams unbekannt oder deaktiviert") — und jeder ausgehende
> Connector-Call des Agenten samt Antwort von Microsoft. Zugangsdaten sind
> darin redigiert, Bodies gekappt; Aufbewahrung 72 h
> (`COVEY_REQUEST_LOG_RETENTION`). Kommt gar kein eingehender Eintrag an, endet
> der Weg vor Covey: Messaging-Endpoint, `COVEY_PUBLIC_URL` oder Reverse-Proxy.

### 2.8 Häufige Stolpersteine

| Symptom | Ursache / Fix |
|---|---|
| Bot taucht in Teams nicht auf | Custom-App-Upload nicht erlaubt (2.0) oder App nicht installiert. Org-weit ausrollen oder Setup-Policy anpassen. |
| Nachricht kommt an, aber Covey reagiert nicht | Messaging-Endpoint falsch (`<agent-slug>`?), `COVEY_PUBLIC_URL` nicht öffentlich, oder Zielsystem `teams` nicht aktiviert (2.5). |
| `signatur ungültig` / 401 am Webhook | `COVEY_TEAMS_WEBHOOK_SECRET` ≠ Bot-App-ID. Beide müssen die App-ID aus 2.1 sein. |
| Agent antwortet nicht zurück | `teams_token` falsch (`appId:appPassword`?), Secret abgelaufen, oder Egress blockt `login.microsoftonline.com` / Connector-Host (Abschnitt 5). Im Recording steht die Ursache im Klartext — „Zugang zu teams verweigert" heißt Secret fehlt oder ist dem Agenten nicht zugewiesen. |
| Datei-Anhänge fehlen im 1:1-Chat | `supportsFiles: true` im Manifest vergessen (2.3). |
| `download_attachment` schlägt fehl | URL abgelaufen (zeitnah laden), Egress blockt `*.sharepoint.com`, oder Datei über dem Limit (`COVEY_TEAMS_ATTACHMENT_MAX_MB`). |
| Config-Änderung wirkt nicht | Der Agent hängt auf `blocked` in einer laufenden Unterhaltung. Eine Folgenachricht setzt die Runtime-Session per `--resume` fort — **mit dem System-Prompt von damals**. Neue Config greift erst bei einer neuen Session: Aufgabe abschließen/abbrechen (Backlog → *Aufräumen*), dann wirkt sie. |
| Datei senden passiert nichts | Zustimmungs-Karte kam an, aber niemand hat geklickt — der Agent parkt korrekt. Ohne `supportsFiles: true` erscheint die Karte gar nicht (2.3). |
| Zugestimmt, aber nichts kommt an; Request-Log zeigt `ignored` | Die zugehörige Aufgabe parkt nicht mehr (abgebrochen, anders beendet, verspätete Zustellung). Zustimmungen legen bewusst keine neue Aufgabe an — Versand neu anstoßen (3.1). |

---

## 3. Welche Nachrichten nimmt der Agent auf?

`ShouldWake` in `internal/target/teams/webhook.go` entscheidet:

1. **Echte Nutzer-Nachricht:** `type == "message"`, mit Absender und Text. So
   löst die *eigene Antwort* des Bots keinen neuen Wake-Zyklus aus.
2. **Kein Echo:** `from.id != recipient.id` — die Bot-Identität als Absender wird
   ignoriert.
3. **Tenant-Allowlist** (optional):

   ```bash
   COVEY_TEAMS_INTAKE_TENANTS="11111111-2222-3333-4444-555555555555"
   ```

   Gesetzt → nur Nachrichten aus diesen Microsoft-365-Tenants (case-insensitiv).
   Leer/ungesetzt → keine Einschränkung.

Nicht-`message`-Activities (`conversationUpdate`, `typing`, …) wecken nicht.
Eine Nachricht **nur mit Anhang** (Datei ohne Text) weckt ebenfalls.

### Anhänge

Führt eine Nachricht Dateien, listet Covey sie im Task-Body (Name, Content-Type,
fertiger `download_attachment`-Aufruf). Der Agent lädt sie über die Action
`download_attachment {"url":"…","name":"…"}` in seine Sandbox (`attachments/`) und
liest sie mit dem Read-Tool (Bilder per Vision). Details: spec/15, „Anhänge
lesen". Betriebsrelevant:

- **Egress:** Für geteilte Dateien lädt der Agent von SharePoint/OneDrive
  (`*.sharepoint.com`), für Inline-Bilder vom Connector-Host. Beide müssen bei
  aktiviertem Egress-Enforcement auf die Allowlist (siehe Abschnitt 5).
- **Größenlimit:** `COVEY_TEAMS_ATTACHMENT_MAX_MB` (Default 25, gültig 1–1024)
  deckelt jeden Download fail-closed. Werte über 1024 werden auf 1024 geklemmt,
  Unlesbares lässt den Default stehen; beides steht als `WARN` im Log.
- **Gleiche Namen kollidieren nicht:** Liegt unter `attachments/` schon eine
  andere Datei dieses Namens, bekommt die neue einen Zähler (`bericht-2.pdf`).
  Bei byte-gleichem Inhalt bleibt es beim bestehenden Pfad. Wichtig, weil sich
  Teams und E-Mail **dasselbe** `attachments/` derselben Sandbox teilen.
- **Kurzlebige URLs:** Download-URLs laufen ab — der Agent sollte Anhänge zeitnah
  laden. Wacht ein `blocked`-Agent spät auf, kann eine URL ungültig sein.

### 3.1 Dateien senden

Ein Bot kann in Teams keine Datei einfach anhängen — der Empfänger muss
zustimmen, und erst sein Klick erzeugt den Upload-Platz. Für den Betrieb heißt
das dreierlei:

- **Es sind immer zwei Läufe.** Der Agent fragt (`send_file`), parkt auf
  `blocked`, wird vom Klick geweckt und lädt hoch (`upload_file`). Im Recording
  sieht man deshalb zwei Sitzungen pro Datei; das ist kein Fehler.
- **`supportsFiles: true`** im App-Manifest ist Pflicht (Abschnitt 2.3) — ohne
  das Flag erscheint die Zustimmungs-Karte im 1:1-Chat gar nicht erst.
- **Egress:** die Upload-URL zeigt auf `*.sharepoint.com` bzw.
  `*-my.sharepoint.com` (das OneDrive des Empfängers). Ohne diesen Host auf der
  Allowlist scheitert der Upload — dieselbe Zeile, die auch eingehende
  Anhänge braucht.

Klickt der Empfänger „Ablehnen", wird der Agent ebenfalls geweckt und beendet
seinen Auftrag; er bleibt nicht auf einer Zustimmung hängen, die nie kommt. Die
Upload-URL ist kurzlebig — hängt der Agent lange (Warteschlange, Budget-Deckel),
kann sie ablaufen, dann muss er neu fragen.

Kommt der Klick, wenn **niemand mehr parkt** (die Aufgabe wurde abgebrochen oder
anders beendet, oder Teams stellt verspätet zu), verpufft das Event: im Request-
Log steht `ignored`, es entsteht keine Aufgabe. Das ist Absicht — eine
Zustimmung ist die Fortsetzung einer angefangenen Arbeit, kein neuer Auftrag.
Der Empfänger sieht dann eine abgehakte Karte ohne folgende Datei; der Agent
muss den Versand neu anstoßen.

In **Kanälen** gibt es diesen Weg nicht: dort führt Dateiablage über Microsoft
Graph und ist nicht Teil dieser Integration.

### Zuordnung Nachricht → Agent

Die Zuordnung läuft **allein über den `<agent-slug>` in der Messaging-Endpoint-
URL**. Für mehrere Bots/Agenten legt man je Agent eine eigene Azure-Bot-
Registration mit dessen Slug-URL an.

---

## 4. `blocked` ↔ Konversation

Stellt der Agent eine Rückfrage (`send`/`reply`), geht er auf `blocked`.
Korrelations-Key ist `teams:conversation:<conversation_id>`. Die Folgenachricht
des Nutzers stellt der Bot Service als neue Activity zu; Covey korreliert über
die `conversation.id` und setzt den Agenten via `claude -p --resume` fort.
Details: [`../spec/15-teams-integration.md`](../spec/15-teams-integration.md).

---

## 5. Env-Referenz (Teams-relevant)

| Variable | Default | Bedeutung |
|---|---|---|
| `COVEY_PUBLIC_URL` | `http://localhost:8494` | Basis-URL, die der Bot Service für den Messaging-Endpoint erreicht |
| `COVEY_TEAMS_WEBHOOK_SECRET` | *(leer = JWT-Prüfung aus)* | erwartete Bot-App-ID (JWT-Audience) |
| `COVEY_TEAMS_INTAKE_TENANTS` | *(leer = alle)* | Allowlist der Microsoft-365-Tenants |
| `COVEY_TEAMS_TOKEN_URL` | Bot-Framework-Endpoint | instanzweiter Token-Endpoint; pro Agent weiter per Secret `teams_url` überschreibbar |
| `COVEY_TEAMS_ATTACHMENT_MAX_MB` | `25` | Größenlimit je in die Sandbox geladenem Anhang (gültig 1–1024; darüber wird geklemmt) |
| `COVEY_DAEMON_TOKEN_TTL` | `15m` | TTL des in die Sandbox gereichten Credentials |
| `COVEY_EGRESS_ENFORCE` | `false` | Egress-Allowlist-Proxy einschalten (nur `docker`-Provider) |
| `COVEY_EGRESS_ALLOW` | *(leer)* | zusätzliche erlaubte Egress-Hosts |
| `COVEY_REQUEST_LOG` | `true` | Request-Log (Plattform → Requests): Webhooks rein, Connector-Calls raus |
| `COVEY_REQUEST_LOG_BODIES` | `true` | Bodies mitschreiben (gekappt, redigiert); `false` = nur Metadaten |
| `COVEY_REQUEST_LOG_RETENTION` | `72h` | Aufbewahrung der Log-Einträge |

> **Egress:** Mit `COVEY_SANDBOX_PROVIDER=docker` und `COVEY_EGRESS_ENFORCE=true`
> läuft der Sandbox-Verkehr über einen Allowlist-Proxy. Der Agent spricht für
> Teams zwei Host-Familien an, die auf die Allowlist müssen:
> `login.microsoftonline.com` (Token) und die regionalen Connector-Hosts
> (`*.botframework.com` bzw. `smba.trafficmanager.net`). Ergänzen z. B. via:
>
> ```bash
> COVEY_EGRESS_ALLOW="login.microsoftonline.com, *.botframework.com, smba.trafficmanager.net, *.sharepoint.com"
> ```
>
> `*.sharepoint.com` nur nötig, wenn Agenten geteilte Datei-Anhänge laden
> (`download_attachment`).
>
> Details und der harte network-Isolationsmodus wie im Zammad-Runbook
> ([`betrieb-zammad.md`](betrieb-zammad.md), Abschnitt 6.1).

---

## 6. Lokale Demo ohne Azure (`faketeams`)

`demo/faketeams` spielt die **Antwort-Seite** (Bot Connector + Token-Endpoint) auf
Port 9998. So testet man Outbound ohne Azure-Registration:

1. `go run ./demo/faketeams` starten.
2. Secret `teams_token = demo-app:demo-secret`, `teams_url = http://localhost:9998/token`.
3. `COVEY_TEAMS_WEBHOOK_SECRET` **leer** lassen (JWT-Prüfung aus).
4. Eine eingehende Nachricht simulieren (Inbound → Covey wecken):

   ```bash
   curl -X POST http://localhost:8494/api/webhooks/teams/<agent-slug> \
     -H 'Content-Type: application/json' -d '{
       "type":"message","id":"a1","text":"Hallo Agent",
       "serviceUrl":"http://localhost:9998","channelId":"msteams",
       "from":{"id":"29:kunde","name":"Kunde"},
       "recipient":{"id":"28:bot","name":"Covey"},
       "conversation":{"id":"19:conv1","conversationType":"personal","tenantId":"t1"}}'
   ```

   `serviceUrl` zeigt auf `faketeams` — die Antwort des Agenten landet dort im Log.

---

## 7. Bekannte Grenzen / Produktions-Checkliste

- **JWT-Validierung** prüft Issuer, Audience, Ablauf und Signatur gegen die
  Bot-Framework-JWKS (gecacht). Vor produktivem Betrieb die aktuellen
  Issuer/JWKS-Endpunkte gegenchecken (Microsoft ändert sie selten, aber
  angekündigt). Leeres `COVEY_TEAMS_WEBHOOK_SECRET` = Prüfung aus, nur Dev.
- **App-Passwort ist langlebig.** Wie Zammads Token fällt es in den „per Key
  angebundenes Zielsystem"-Fall: Der Built-in-`SecretStore` verwahrt es
  verschlüsselt und reicht es kurzlebig durch; der Laufzeit-Connector-Zugriff
  läuft dann bereits über ein kurzlebiges getauschtes Token. Secret regelmäßig
  rotieren.
- **Dateien in beide Richtungen, aber nur in Chats.** Eingehende Anhänge werden
  gelesen (`download_attachment`, Abschnitt 3), ausgehende laufen über den
  File-Consent-Flow (Abschnitt 3.1). Noch **nicht** abgedeckt: Adaptive Cards,
  Dateien in **Kanäle** und Kanal-/Team-Verwaltung über Microsoft Graph
  (spec/15, Abschnitt „Scope").
- Die allgemeinen MVP-Grenzen (Egress-Härtung, Retry/Reconnect, Budget-Deckel,
  `webhook_events`-Retention) gelten wie im Zammad-Runbook
  ([`betrieb-zammad.md`](betrieb-zammad.md), Abschnitt 7).

---

## 8. Vorlage: Zugriffs-/Provisionierungs-Ticket (EN)

Fertiger Text zum Kopieren an den Azure-/M365-Admin, wenn ein Bot in einem
fremden Tenant (z. B. für einen Pilot in einer anderen GmbH) angelegt werden
soll. `<…>` vor dem Senden ausfüllen. Bewusst auf Englisch, damit es 1:1 an
IT/Provider gehen kann.

```text
Subject: Azure Bot (Bot Framework) for AI-agent pilot — <Company / Tenant>

Context
We are evaluating how to roll out AI agents across the organization (platform:
Covey — it manages AI agents like employees). We want to connect Microsoft Teams
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
  (I will provide the final URL once the pilot's Covey instance is up.)

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

Nach der Bereitstellung folgt die Covey-Seite (Manifest mit
`supportsFiles: true`, Secrets, Zielsystem aktivieren) gemäß Abschnitt 2.
