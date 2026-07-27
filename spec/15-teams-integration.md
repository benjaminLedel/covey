# 15 — Microsoft-Teams-Integration (Zielsystem)

Bindet **Microsoft Teams** als Zielsystem an, damit Agenten dort Nachrichten **empfangen und senden** — der Chat wird zum Kanal zwischen Mensch und Agent, analog zum Helpdesk in [`13-zammad-integration.md`](13-zammad-integration.md). Teams ist das zweite Webhook-getriebene Zielsystem (nach Zammad) und das erste mit **OAuth2/JWT** statt eines langlebigen API-Tokens.

Architektonisch ist Teams ein **kompiliertes Zielsystem-Plugin** (`internal/target/teams`, per Blank-Import eingebunden — siehe [`10-architektur-stack.md`](10-architektur-stack.md), „Zielsysteme als Plugins"): dieselbe `System`/`Webhooker`-Schnittstelle wie Zammad, derselbe Event-Router, dieselbe Dedup-/Korrelations-Mechanik. Neu ist nur die Auth-Fläche.

## Der Weg: Azure Bot Framework

Teams-Bots laufen nicht direkt zwischen Teams und Covey, sondern über den **Azure Bot Service** (Bot Framework). Das ist der kanonische, dokumentierte Weg für bidirektionale Bots (@mention in Kanälen, 1:1-Chat) und passt exakt ins Covey-Muster: eingehend ein Webhook, ausgehend ein REST-Call.

```
Teams-Client  ──(User schreibt @Agent oder DM)──►  Azure Bot Service
                                                         │  POST Activity (JWT)
                                                         ▼
                                          {public_url}/api/webhooks/teams/<agent-slug>
                                                         │  ParseWebhook → Wake
                                                         ▼
                                                   Backlog / blocked-Aufgabe
                                                         │  Agent antwortet (Action-Proxy)
                                                         ▼
                               POST {serviceUrl}/v3/conversations/…/activities  (Bot Connector)
```

## Drei Integrationsflächen

### 1. Inbound: Wake über den Messaging-Endpoint

Der Azure Bot Service stellt jede Nachricht als **Activity** (JSON) an den Messaging-Endpoint des Bots zu — bei Covey ist das `POST {public_url}/api/webhooks/teams/<agent-slug>`. Relevante Felder: `type` (`message`, `conversationUpdate`, …), `id` (Activity-ID), `text`, `from` (Absender), `recipient` (der Bot), `conversation` (`id`, `conversationType`, `tenantId`), `serviceUrl` (der Bot-Connector-Host für die Antwort).

- **Neue Nachricht / DM** → Activity → Event-Router → Agent wacht auf (Wake-Quelle).
- **Folgenachricht in derselben Konversation** → Korrelation über die `conversation.id` → geblockter Agent wacht auf.
- **Integrität:** Der Bot Service signiert jede Zustellung mit einem **JWT** im `Authorization: Bearer …`-Header (Issuer `https://api.botframework.com`, Audience = die Microsoft-App-ID des Bots, RS256, Schlüssel aus der Bot-Framework-JWKS). Covey validiert dieses Token, bevor es dem Event traut.
- **Idempotenz:** Der Bot Service wiederholt Zustellungen bei Fehlern. Der Event-Router dedupliziert über die Activity-`id` (`teams:activity:<id>`), sodass dieselbe Nachricht nur einen Wake auslöst.

### 2. Outbound: Antworten über den Bot Connector

Antworten gehen an den **Bot-Connector-REST-Dienst** unter der `serviceUrl` aus der auslösenden Activity (regional, z. B. `https://smba.trafficmanager.net/emea/`):

| Aktion | Aufruf |
|---|---|
| Nachricht senden | `POST {serviceUrl}/v3/conversations/{conversationId}/activities` |
| Auf Nachricht antworten | `POST {serviceUrl}/v3/conversations/{conversationId}/activities/{activityId}` |
| 1:1-Chat proaktiv eröffnen | `POST {serviceUrl}/v3/conversations` → dann Activity senden |

Der Body ist eine minimale Activity (`{"type":"message","text":"…"}`); Absender/Empfänger/Konversation leitet der Connector aus URL und Token ab.

### 3. Auth: der Broker gegen den Bot Connector

Anders als Zammads statisches Token nutzt der Bot Connector **OAuth2 `client_credentials`**: Der Bot tauscht seine **App-ID + App-Passwort** (Client Secret) gegen ein kurzlebiges Access-Token (`scope=https://api.botframework.com/.default`), das er als `Bearer` an den Connector reicht.

- Der **Secrets-Broker** hält App-ID und App-Passwort und injiziert sie dem Daemon zur Laufzeit — **nichts Langlebiges in der Sandbox** (siehe [`04-identitaet-secrets.md`](04-identitaet-secrets.md)). Konvention der gebrokerten Credentials:
  - `teams_token` = `"{appId}:{appPassword}"` (App-ID vor dem ersten `:`, Rest = Passwort — analog zum `user:pass` des E-Mail-Plugins),
  - `teams_url` = optionaler Token-Endpoint (Default: der Multi-Tenant-Bot-Framework-Endpoint).
- Das **kurzlebige Connector-Token** wird pro Daemon-Prozess gecacht und vor Ablauf erneuert — es verlässt die Sandbox nicht.

> **Ehrliche Grenze.** Das App-Passwort selbst ist ein langlebiges Client Secret (wie Zammads Token fällt es in den „per Key angebundenes Zielsystem"-Fall aus [`10-architektur-stack.md`](10-architektur-stack.md)). Der Built-in-`SecretStore` verwahrt es verschlüsselt und reicht es kurzlebig durch; der eigentliche Laufzeit-Zugriff auf den Connector läuft dann bereits über ein **kurzlebiges, per `client_credentials` getauschtes** Token. Echter End-to-End-RFC-8693-Exchange bis zum Nutzer-Kontext bleibt späteren, delegierten Graph-Szenarien vorbehalten.

## `blocked` ↔ Konversation (Korrelation)

Teams hat keinen Ticket-Zustand wie Zammads `pending`. Der natürliche Korrelations-Anker ist die **`conversation.id`**, die in jeder Activity mitkommt:

1. Agent stellt eine Rückfrage (`send`/`reply`) und parkt die Aufgabe → `blocked`, **Korrelations-Key = `teams:conversation:<conversation_id>`**, dazu die Runtime-`session_id` (siehe [`12-claude-code-adapter.md`](12-claude-code-adapter.md)).
2. Nutzer antwortet → Bot Service stellt die nächste Activity zu.
3. Covey korreliert über die `conversation.id`, weckt den Agenten und setzt fort.

Wie bei Zammad ist die Korrelation damit „geschenkt" — kein eigener Key muss durch die Kommunikation zurückgeschleift werden.

## Echo-Schutz

Der Bot darf seine eigenen Nachrichten nicht als Arbeit aufnehmen. Eine Activity, deren `from.id` gleich der `recipient.id` (die Bot-Identität) ist, wird registriert (Dedup), löst aber **keinen Wake** aus (`Wake=false`) — dieselbe Mechanik wie der `Sender=Agent`-Filter bei Zammad. Ebenso wecken Nicht-`message`-Activities (`conversationUpdate`, `typing`, …) nicht.

## Aufnahme-Filter

Optionaler Betriebs-Filter über ENV (12-Factor, wie bei Zammad):

- `COVEY_TEAMS_INTAKE_TENANTS="<tenant-id>, …"` — nur Nachrichten aus diesen Microsoft-365-Tenants lösen eine Aufgabe aus. Leer/ungesetzt → alle Tenants.

## Aktionen (Übersicht)

| Aktion | Params | Wirkung |
|---|---|---|
| `send` | `service_url`, `conversation_id`, `text` | Nachricht in eine bestehende Konversation. |
| `reply` | `service_url`, `conversation_id`, `reply_to_activity_id`, `text` | Antwort auf eine Nachricht (ohne `reply_to_activity_id` → `send`). |
| `create_conversation` | `service_url`, `tenant_id`, `user_id`, `text` | Proaktiver 1:1-Chat mit einem Nutzer. |

`service_url` und `conversation_id` stammen aus der auslösenden Nachricht (stehen im Task-Body). Alle drei sind eigene Guard-Rail-Subjekte (`teams:send`, `teams:reply`, `teams:create_conversation`).

## Scope dieser Integration

- **Ein Bot / ein Agent**, App-ID + App-Passwort über den Built-in-`SecretStore`.
- **Messaging-Endpoint** als Webhook, JWT-verifiziert, idempotent verarbeitet.
- **Aktionen:** senden, antworten, 1:1-Chat eröffnen.
- **`blocked`** über die Konversation, Korrelation über die `conversation.id`.

Später (nicht jetzt): Adaptive Cards statt Klartext, Kanal-/Team-Verwaltung über Microsoft Graph, delegierter Nutzer-Kontext (Graph mit On-Behalf-Of), Attachments, mehrere Bots pro Org.

## Hinweise

- Der Bot braucht eine **Azure-Bot-Registration** (Microsoft-App-ID + Client Secret) und den Messaging-Endpoint `{public_url}/api/webhooks/teams/<agent-slug>`; das Teams-Manifest bindet den Bot in Teams ein. Betriebsdetails: `docs/betrieb-teams.md`.
- JWT-Validierung geprüft gegen die Bot-Framework-Auth-Doku (Stand Juli 2026) — vor produktivem Betrieb die aktuellen Issuer/JWKS-Endpunkte gegenchecken. Bei leerem `COVEY_TEAMS_WEBHOOK_SECRET` ist die Validierung deaktiviert (nur für lokale Tests / das `faketeams`-Double).
