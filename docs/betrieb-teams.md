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

## 2. Schritt-für-Schritt-Anleitung

### 2.1 In Azure: Bot-Registration + Secret anlegen

1. Eine **Azure-Bot-Ressource** anlegen (Azure Portal → „Azure Bot"). Bot-Typ
   „Multi Tenant" ist der einfachste Start.
2. Die **Microsoft-App-ID** (Client-ID) der zugehörigen App-Registration notieren.
3. Ein **Client Secret** (App-Passwort) erzeugen (App-Registration → Zertifikate
   & Geheimnisse). Wert **sofort** notieren (wird nur einmal angezeigt).
4. Den Kanal **Microsoft Teams** aktivieren (Bot-Ressource → Kanäle → Teams).

### 2.2 In Azure: Messaging-Endpoint setzen

Bot-Ressource → Konfiguration → **Messaging endpoint**:

```
https://covey.example.com/api/webhooks/teams/<agent-slug>
```

`<agent-slug>` = der Slug des zuständigen Agenten in Covey; über die URL wird die
Nachricht dem Agenten zugeordnet. Die Basis muss **öffentlich erreichbar** sein,
sonst kann der Bot Service nicht zustellen (kein `localhost`).

### 2.3 In Covey: Secrets hinterlegen

Pro Agent im SecretStore setzen (UI: Agenten-Seite → Secrets, oder via API):

| Secret | Wert | Zweck |
|---|---|---|
| `teams_token` | `<app-id>:<app-passwort>` | Outbound-Auth (OAuth2 client_credentials) |
| `teams_url` | *(optional)* Token-Endpoint | Default: `https://login.microsoftonline.com/botframework.com/oauth2/v2.0/token`; für Single-Tenant-Bots der tenant-spezifische Endpoint |
| `anthropic_api_key` *oder* `claude_code_oauth_token` | API-Key bzw. `claude setup-token` | die Runtime in der Sandbox |

App-ID steht **vor** dem ersten `:`, der Rest ist das Passwort (darf `:`
enthalten). Ohne einen der Claude-Werte scheitern Aufgaben mit „Not logged in" —
die Sandbox hat ein eigenes, leeres `HOME`.

### 2.4 In Covey: Zielsystem aktivieren

Das Zielsystem `teams` muss für die Org **aktiviert** sein (UI: Zielsysteme →
Microsoft Teams aktivieren). Ist es nicht aktiv, verweigert der Broker
fail-closed jede Credential-Freigabe und der Webhook-Endpunkt weist das Event ab.

Zusätzlich muss der Agent laut seiner `ACCESS.md` auf `teams` zugreifen dürfen
(`- system: teams scope: read,write`), und die Guard-Rails dürfen `teams` /
`teams:send` / `teams:reply` / `teams:create_conversation` nicht verbieten.

### 2.5 In Covey: Prozess-Env setzen

```bash
COVEY_PUBLIC_URL=https://covey.example.com        # vom Bot Service erreichbar, NICHT localhost
COVEY_TEAMS_WEBHOOK_SECRET=<bot-app-id>           # die erwartete JWT-Audience
# optional:
COVEY_TEAMS_INTAKE_TENANTS="<tenant-id>"          # leer = alle Tenants
```

> **Wichtig:** `COVEY_TEAMS_WEBHOOK_SECRET` trägt hier **nicht** ein HMAC-Secret,
> sondern die **Bot-App-ID** — sie ist die Audience, gegen die das eingehende
> JWT validiert wird. Ein **leerer** Wert deaktiviert die Prüfung (nur Dev /
> `faketeams`). Im Produktivbetrieb immer setzen.

### 2.6 In Teams: App-Manifest hochladen

Ein Teams-App-Manifest (`manifest.json` + Icons, als `.zip`) mit der **App-ID**
als `bots[].botId` erstellen und in Teams **hochladen/seitwärts laden** (Teams →
Apps → „Manage your apps" → „Upload an app"), damit Nutzer den Agenten
anschreiben können. Für Kanal-@mentions den `scopes`-Eintrag `team` ergänzen.

### 2.7 Testen

1. Dem Bot in Teams eine **1:1-Nachricht** schicken (oder ihn im Kanal
   @mentionen).
2. In Covey: erscheint eine Backlog-Aufgabe beim Agenten? → Recording ansehen.
3. Antwortet der Agent, prüfen: kommt die Antwort im Teams-Chat an?
4. Bei einer Rückfrage: geht der Agent auf `blocked`? Folgenachricht des Nutzers
   → wacht der Agent über die `conversation.id` wieder auf? (Abschnitt 4)

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
- **Größenlimit:** `COVEY_TEAMS_ATTACHMENT_MAX_MB` (Default 25) deckelt jeden
  Download fail-closed.
- **Kurzlebige URLs:** Download-URLs laufen ab — der Agent sollte Anhänge zeitnah
  laden. Wacht ein `blocked`-Agent spät auf, kann eine URL ungültig sein.

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
| `COVEY_TEAMS_ATTACHMENT_MAX_MB` | `25` | Größenlimit je in die Sandbox geladenem Anhang |
| `COVEY_DAEMON_TOKEN_TTL` | `15m` | TTL des in die Sandbox gereichten Credentials |
| `COVEY_EGRESS_ENFORCE` | `false` | Egress-Allowlist-Proxy einschalten (nur `docker`-Provider) |
| `COVEY_EGRESS_ALLOW` | *(leer)* | zusätzliche erlaubte Egress-Hosts |

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
- **Nur Klartext-Nachrichten.** Adaptive Cards, Attachments und Kanal-/Team-
  Verwaltung über Microsoft Graph sind bewusst noch nicht abgedeckt (spec/15,
  Abschnitt „Scope").
- Die allgemeinen MVP-Grenzen (Egress-Härtung, Retry/Reconnect, Budget-Deckel,
  `webhook_events`-Retention) gelten wie im Zammad-Runbook
  ([`betrieb-zammad.md`](betrieb-zammad.md), Abschnitt 7).
