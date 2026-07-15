# 13 — Zammad-Integration (MVP-Zielsystem)

Konkretisiert das „eine Zielsystem" aus M5 in [`11-mvp-plan.md`](11-mvp-plan.md) als **Zammad** (Open-Source-Helpdesk, self-hostbar, REST/JSON-API, Trigger + Webhooks). Zammad berührt drei MVP-Meilensteine: den Event-Wake (M3), die Event-Korrelation (M4) und Broker + API-Aktionen (M5).

Passt gut, weil Zammad im DACH-Raum verbreitet und wie Covey self-hostbar ist — beides läuft auf derselben Infra.

## Drei Integrationsflächen

### 1. Inbound: Wake über Trigger + Webhooks (M3/M4)

Zammad kennt **Triggers** (reagieren auf Ticket-Lifecycle-Events: erstellt, Status geändert, neue Nachricht) und **Webhooks** (POST an eine externe URL). Ein Trigger feuert den Webhook an Coveys Event-Router.

- **Neues Ticket** → Trigger → Webhook → Event-Router → Support-Agent wacht auf (M3-Wake-Quelle).
- **Kundenantwort / Ticket-Update** → Trigger → Webhook → Korrelation → geblockter Agent wacht auf (M4).
- Der Webhook-Payload ist JSON und enthält das Ticket (inkl. **`id`** und `article_ids`), den Artikel, Gruppe und User; die Integrität ist per **HMAC-SHA1-Signatur** im Header verifizierbar (optionaler Signatur-Token) — Covey prüft die Signatur, bevor es dem Event traut.
- Betriebs-Realität: Webhooks kommen **nicht garantiert sofort** (gleiche Priorität/Reihenfolge wie E-Mail-Trigger) und werden bei Fehlern bis zu viermal wiederholt. Der Event-Router muss also idempotent sein (dasselbe Ticket-Update darf nicht zwei Wakes auslösen).

### 2. Outbound: Aktionen über die REST-API (M5)

Base `/api/v1/`, `Content-Type: application/json`. Die Aktionen, die der Support-Agent im MVP braucht:

| Aktion | Aufruf |
|---|---|
| Ticket lesen | `GET /tickets/{id}` |
| Verlauf lesen (Artikel) | `GET /ticket_articles/by_ticket/{ticket_id}` |
| Antwort/Kommentar schreiben | `POST /ticket_articles` (bzw. `article`-Objekt im Ticket-Update) |
| Status/Owner/Priorität setzen | `PUT /tickets/{id}` |

Bei Artikeln steuert `internal: true|false` die Sichtbarkeit (interne Notiz vs. für den Kunden sichtbar) und `type` die Art (note, email, …). Damit deckt der Agent triagieren, antworten, intern kommentieren und eskalieren (Gruppe/Owner ändern) ab.

### 3. Auth: der Broker gegen Zammad (M5)

Zammad unterstützt drei Auth-Methoden: HTTP-Basic, **Token-Access** (permission-gescopte API-Tokens) und OAuth2. Für Covey:

- **Token-Access** ist der Weg: ein **eigener Rolle** mit exakt den nötigen Rechten (z. B. `ticket.agent` für bestimmte Gruppen — Least Privilege), Token in der Admin-Oberfläche unter *System → API* („Token access allowed").
- Der **Secrets-Broker** hält dieses Token und injiziert es dem Daemon zur Laufzeit — **nichts Langlebiges in der Sandbox** (siehe [`04-identitaet-secrets.md`](04-identitaet-secrets.md)).

> **Ehrliche Grenze.** Zammads API-Token ist ein permission-gescoptes, aber **langlebiges** Token — **kein** kurzlebiges, per RFC-8693 getauschtes Token. Zammad fällt damit in den „simpel per API-Key angebundenes Zielsystem"-Fall aus [`10-architektur-stack.md`](10-architektur-stack.md): Der Built-in-`SecretStore` verwahrt das Zammad-Token verschlüsselt und reicht es kurzlebig durch; echter Token-Exchange greift erst bei OAuth-fähigen Zielsystemen. Für den MVP ist genau das ausreichend und ehrlich abgegrenzt.

## `blocked` ↔ Zammad `pending`-State (M4)

Zammad hat einen **`pending reminder`/`pending close`**-State mit `pending_time` — das bildet Coveys `blocked` nativ ab und hält zugleich die **menschliche Sicht konsistent**: Das Ticket steht sichtbar auf „warten auf Kunde", nicht offen und nicht geschlossen.

Ablauf:

1. Agent stellt eine Rückfrage an den Kunden (`POST /ticket_articles`, `internal: false`) und setzt das Ticket auf `pending reminder` (`PUT /tickets/{id}`).
2. Covey parkt die Aufgabe → `blocked`, **Korrelations-Key = Zammad-Ticket-`id`**, dazu die Claude-Code-`session_id` (siehe [`12-claude-code-adapter.md`](12-claude-code-adapter.md)).
3. Kunde antwortet → Zammad-Trigger feuert den Webhook (Ticket-Update, `sender: Customer`).
4. Covey korreliert über die Ticket-`id`, weckt den Agenten und setzt via `claude -p --resume <session_id>` fort.

## Korrelation — für Zammad quasi geschenkt

Die offene Entscheidung D1 (Event-Korrelation, siehe [`07-offene-entscheidungen.md`](07-offene-entscheidungen.md)) ist für Zammad einfach: Die **Ticket-`id` ist ein stabiler, natürlicher Korrelations-Key**, der in jedem Webhook-Payload mitkommt. Es braucht keinen eigenen Key, der durch die Kundenkommunikation zurückgeschleift wird — der **zentrale Event-Router** mappt schlicht `ticket.id` → geparkte Aufgabe. Das ist der pragmatische Startpunkt; der allgemeine, kanalunabhängige Mechanismus bleibt für spätere Zielsysteme relevant.

## MVP-Scope dieser Integration

- **Eine Gruppe / ein Support-Agent**, Token-Auth über den Built-in-`SecretStore`.
- **Trigger + Webhook** auf „Ticket erstellt" und „Kundenantwort", HMAC-verifiziert, idempotent verarbeitet.
- **Aktionen:** lesen, antworten (intern/extern), Status/Owner setzen.
- **`blocked`** über den Zammad-`pending`-State, Korrelation über die Ticket-`id`.

Später (nicht MVP): OAuth2 statt Token, mehrere Gruppen/Agenten, Attachments (Webhook liefert nur Links, Auth nötig), weitere Zielsysteme über dasselbe Broker-Interface.

## Hinweise

- Zammad-Webhooks gibt es seit 3.6, frei anpassbare Payloads seit 6.0. Geprüft gegen die Zammad-Doku (`docs.zammad.org`, `admin-docs.zammad.org`, Stand Juli 2026) — vor Bau kurz gegenchecken.
- Beide Systeme (Covey und Zammad) laufen self-hosted; der Webhook-Weg Zammad → Covey und der API-Weg Covey → Zammad bleiben intern, kein Umweg über Dritte.
