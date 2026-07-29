# Betrieb: Covey an ein echtes Zammad anschließen

Praktisches Runbook für den Schritt vom Demo-Setup (`demo/fakezammad`) zu einer
**echten Zammad-Instanz**. Die Design-Hintergründe stehen in
[`../spec/13-zammad-integration.md`](../spec/13-zammad-integration.md); dieses
Dokument sagt, **was konkret zu tun ist** — und wo die Grenzen für den
Produktivbetrieb liegen.

> Kurzfassung: Der Adapter ist bereits Zammad-kompatibel (Token-Auth,
> HMAC-SHA1-Webhook, `/api/v1`-Pfade). Es sind im Wesentlichen
> **Konfigurationsschritte**, kein Umbau. Zwei Verhaltenspunkte
> (kundensichtbare Antwort, Ticket-Auswahl) solltest du bewusst einstellen.

---

## 1. Überblick des Datenflusses

```
Zammad  ──(Trigger + Webhook, HMAC-signiert)──►  Covey  /api/webhooks/zammad/<agent-slug>
                                                   │  Signatur prüfen → Intake-Filter → Backlog-Task
                                                   ▼
                                                 Agent (Sandbox, Claude Code)
                                                   │  Aktionen über Action-Proxy
Zammad  ◄──(REST /api/v1, Token-Auth)──────────────┘  get_ticket, reply, set_state, escalate
```

Zwei Richtungen, zwei Auth-Wege:

- **Inbound** (Zammad → Covey): Webhook, verifiziert per HMAC-SHA1-Signatur
  (`COVEY_ZAMMAD_WEBHOOK_SECRET`).
- **Outbound** (Covey → Zammad): REST mit einem gebrokerten API-Token
  (Secret `zammad_token`), das nie in der Sandbox persistiert wird.

---

## 2. Schritt-für-Schritt-Anleitung

### 2.1 In Zammad: API-Token + Rechte anlegen

1. Eine **Agenten-Rolle** mit Least-Privilege-Rechten anlegen: `ticket.agent`
   für **genau die Gruppe(n)**, die der Agent bearbeiten soll — nicht mehr.
2. Einen **User** (den „Covey-Agenten") mit dieser Rolle anlegen und ihm die
   Zielgruppe zuweisen.
3. Token-Zugriff aktivieren: *Admin → System → API →* „Token Access" einschalten.
4. Als dieser User ein **API-Token** erzeugen (*Profil → Token Access*) mit den
   Permissions `ticket.agent`. Token notieren (wird nur einmal angezeigt).

### 2.2 In Covey: Secrets hinterlegen

Pro Agent im SecretStore setzen (UI: Agenten-Seite → Secrets, oder via API):

| Secret | Wert | Zweck |
|---|---|---|
| `zammad_url` | `https://helpdesk.example.com` | **ohne** `/api/v1` — der Client hängt das an |
| `zammad_token` | das API-Token aus 2.1 | Outbound-Auth |
| `anthropic_api_key` *oder* `claude_code_oauth_token` | API-Key bzw. `claude setup-token` | die Runtime in der Sandbox |

Ohne einen der Claude-Werte scheitern Aufgaben mit „Not logged in" — die Sandbox
hat ein eigenes, leeres `HOME`.

### 2.3 In Covey: Zielsystem aktivieren

Das Zielsystem `zammad` muss für die Org **aktiviert** sein (UI: Zielsysteme →
Zammad aktivieren). Ist es nicht aktiv, verweigert der Broker fail-closed jede
Credential-Freigabe und der Webhook-Endpunkt weist das Event ab.

Zusätzlich muss der Agent laut seiner `ACCESS.md` auf `zammad` zugreifen dürfen,
und die Guard-Rails dürfen `zammad` / `zammad:reply_external` nicht verbieten.

### 2.4 In Covey: Prozess-Env setzen

```bash
COVEY_PUBLIC_URL=https://covey.example.com        # von Zammad erreichbar, NICHT localhost
COVEY_ZAMMAD_WEBHOOK_SECRET=<langes-zufalls-secret>
# optional, siehe Abschnitt 3 und 4:
COVEY_ZAMMAD_INTAKE_GROUPS="Support L1"
COVEY_ZAMMAD_REPLY_TYPE=email
```

`COVEY_PUBLIC_URL` muss öffentlich (bzw. für Zammad im Netz) auflösbar sein,
sonst kann Zammad den Webhook nicht zustellen.

### 2.5 In Zammad: Webhook + Trigger einrichten

1. **Webhook** anlegen (*Admin → Manage → Webhooks*):
   - Endpoint: `https://covey.example.com/api/webhooks/zammad/<agent-slug>`
     (`<agent-slug>` = der Slug des zuständigen Agenten in Covey; über die URL
     wird das Ticket dem Agenten zugeordnet — siehe Abschnitt 3.2. Ersatzweise
     wird auch die Agent-ID akzeptiert, die UUID aus der URL der Agenten-Seite.)
   - HMAC-Signatur-Token: **derselbe Wert** wie `COVEY_ZAMMAD_WEBHOOK_SECRET`.
   - Payload: Standard-Payload ist ok; sie muss `ticket` und `article` als
     Top-Level-Objekte enthalten. Wichtig: `article.sender` muss als String
     („Customer"/„Agent") ankommen, und für den Gruppen-Filter muss
     `ticket.group` (Gruppenname) enthalten sein.
2. **Trigger** anlegen (*Admin → Manage → Trigger*):
   - Bedingung z. B. „Aktion: Ticket erstellt/aktualisiert" **und**
     „Artikel-Absender: Kunde".
   - Aktion: „Webhook auslösen" → den Webhook aus Schritt 1.
   - Optional Bedingungen wie „Gruppe = Support L1" ergänzen (siehe 3.1).

### 2.6 Testen

1. In Zammad ein Ticket in der Zielgruppe anlegen (als Kunde antworten).
2. In Covey: erscheint eine Backlog-Aufgabe beim Agenten? → Recording ansehen.
3. Antwortet der Agent, prüfen: kommt die Antwort **kundensichtbar** an
   (Abschnitt 4)?
4. Bei einer Rückfrage: geht das Ticket auf `pending reminder` und der Agent auf
   `blocked`? Kundenantwort → wacht der Agent wieder auf? (Abschnitt 5)

---

## 3. Welche Tickets nimmt der Agent auf?

Das lässt sich auf **zwei Ebenen** steuern — beide zusammen ergeben die
Aufnahme-Entscheidung.

### 3.1 Ebene 1 — Zammad-seitig (Trigger-Bedingungen)

Der sauberste Filter sitzt an der Quelle: Der Zammad-**Trigger** feuert den
Webhook nur, wenn seine Bedingungen erfüllt sind (Gruppe, Priorität, Status,
Tag, Owner, …). Was der Trigger nicht durchlässt, erreicht Covey gar nicht.
Für „nur Tickets der Gruppe *Support L1*" genügt eine Trigger-Bedingung.

### 3.2 Ebene 2 — Covey-seitig (Intake-Filter)

Erreicht ein Webhook Covey, entscheidet der Adapter, ob daraus eine Aufgabe
wird. Zwei Kriterien, beide müssen zutreffen (`ShouldWake` in
`internal/target/zammad/webhook.go`):

1. **Kundennachricht:** `article.sender == "Customer"` und nicht intern. So löst
   die *eigene Antwort* des Agenten keinen neuen Wake-Zyklus aus.
2. **Gruppen-Allowlist** (neu, konfigurierbar):

   ```bash
   COVEY_ZAMMAD_INTAKE_GROUPS="Support L1, Beschwerden"
   ```

   Ist die Variable gesetzt, wird nur ein Ticket aus einer dieser Gruppen
   aufgenommen (case-insensitiv). Leer/ungesetzt → keine Einschränkung
   (Rückwärts-kompatibel). Voraussetzung: Der Webhook-Payload enthält
   `ticket.group` als Namen.

**Empfehlung:** Ebene 1 (Trigger) als primären Filter nutzen — spart
Netzwerk-Roundtrips und hält die Auswahl beim Fachbereich. Ebene 2 als
Sicherheitsnetz / zentrale Durchsetzung, falls der Trigger zu weit gefasst ist.

### 3.3 Zuordnung Ticket → Agent

Heute läuft die Zuordnung **allein über den `<agent-slug>` in der Webhook-URL**.
Für mehrere Support-Bereiche legt man pro Agent einen eigenen Zammad-Webhook
(mit passender Trigger-Bedingung) auf dessen Slug-URL an. Ein zentrales
Queue→Agent-Mapping in Covey gibt es (noch) nicht — siehe Abschnitt 7.

---

## 4. Kundensichtbare Antworten

Der Adapter unterscheidet:

- **intern** (`reply` mit `internal:true`) → Zammad-Artikel type `note`, nur für
  Agenten sichtbar.
- **extern** (`reply` mit `internal:false`) → type **`email`** (Default), geht
  als Mail an den Kunden raus.

> Wichtig: Eine externe *note* wäre im Ticket sichtbar, würde aber **keine Mail**
> auslösen. Deshalb ist der Default für externe Antworten `email`. Für
> Web-/Chat-basierte Instanzen den Typ umstellen:
>
> ```bash
> COVEY_ZAMMAD_REPLY_TYPE=web
> ```

---

## 5. `blocked` ↔ Zammad `pending`

Stellt der Agent eine Rückfrage, setzt er das Ticket auf `pending reminder`
(`set_state`) und geht selbst auf `blocked`. Korrelations-Key ist die
Ticket-`id`. Die Kundenantwort (ein neuer Customer-Artikel) feuert den Trigger,
Covey korreliert über die Ticket-`id` und setzt den Agenten via
`claude -p --resume` fort. Details: [`../spec/13-zammad-integration.md`](../spec/13-zammad-integration.md).

Achte darauf, dass der Trigger **auch für Ticket-Updates** (nicht nur „erstellt")
feuert — sonst wacht ein geblockter Agent nie wieder auf.

---

## 6. Env-Referenz (Zammad-relevant)

| Variable | Default | Bedeutung |
|---|---|---|
| `COVEY_PUBLIC_URL` | `http://localhost:8494` | Basis-URL, die Zammad für den Webhook erreicht |
| `COVEY_ZAMMAD_WEBHOOK_SECRET` | *(leer = Signaturprüfung aus)* | HMAC-SHA1-Secret, identisch zum Zammad-Webhook-Token |
| `COVEY_ZAMMAD_INTAKE_GROUPS` | *(leer = alle)* | Allowlist der Gruppen, die Tickets aufnehmen |
| `COVEY_ZAMMAD_REPLY_TYPE` | `email` | Artikel-Typ externer Antworten (`email`/`web`) |
| `COVEY_DAEMON_TOKEN_TTL` | `15m` | TTL des in die Sandbox gereichten Credentials |
| `COVEY_EGRESS_ENFORCE` | `false` | Egress-Allowlist-Proxy einschalten (nur `docker`-Provider) |
| `COVEY_EGRESS_ALLOW` | *(leer)* | zusätzliche erlaubte Egress-Hosts, z. B. der Zammad-Host (`*.suffix` erlaubt) |
| `COVEY_EGRESS_ISOLATION` | `proxy` | `proxy` (kooperativ) oder `network` (harte Isolation, siehe 6.1) |
| `COVEY_EGRESS_PROXY_ADDR` | `:8888` | Bind-Adresse des Proxy (network-Modus, im Container) |

> **Egress:** Mit `COVEY_SANDBOX_PROVIDER=docker` und `COVEY_EGRESS_ENFORCE=true`
> läuft der Sandbox-Verkehr über einen Allowlist-Proxy. Fest erlaubt ist
> `api.anthropic.com` (die Runtime); **den Zammad-Host musst du ergänzen**, sonst
> kann der Agent nicht antworten. Zwei Wege:
>
> Die Allowlist ist **pro Agent**: effektiv = fest erlaubte Hosts + zugewiesene
> Templates + agent-eigene Hosts.
> - **In der Oberfläche** (empfohlen): *Egress* (Seitenmenü) pflegt die
>   wiederverwendbaren **Templates** und zeigt das globale Monitoring; die
>   **Zuweisung pro Agent** (Templates + eigene Hosts) sitzt im Reiter *Egress*
>   der Agenten-Seite. Wirkt innerhalb ~15 s, kein Neustart. Rechte: `security`
>   oder `platform_admin`.
> - **Per ENV/Compose** (gilt für ALLE Agenten, nicht in der UI löschbar):
>   ```bash
>   COVEY_SANDBOX_PROVIDER=docker
>   COVEY_EGRESS_ENFORCE=true
>   COVEY_EGRESS_ALLOW="helpdesk.example.com"
>   ```
>
> Muster: exakter Host oder `*.suffix`. Standardmodus ist kooperativ
> (HTTP(S)_PROXY im Container) — verhindert naives Ausleiten, ist aber vom Agenten
> über direkte IPs umgehbar. Für harte Isolation siehe unten.

### 6.1 Harte Netz-Isolation (`COVEY_EGRESS_ISOLATION=network`)

Im network-Modus läuft die Sandbox auf einem **internen Docker-Netz ohne
Internet**; ein Proxy-Container ist der **einzige Ausgang** und erzwingt die
Allowlist. Ein direkter Bypass (direkte IP, `--noproxy`) ist damit unmöglich —
der Container hat schlicht keine Route nach draußen.

```bash
make egress-image          # baut covey-egress:latest (Proxy-Container)
COVEY_SANDBOX_PROVIDER=docker
COVEY_EGRESS_ENFORCE=true
COVEY_EGRESS_ISOLATION=network
COVEY_EGRESS_ALLOW="helpdesk.example.com"   # Zielsystem-Hosts (UI-Hosts kommen dazu)
```

Die Control Plane richtet Netz und Proxy-Container automatisch ein
(`covey-egress-internal`, `covey-egress-proxy`, Image `make egress-image`). Der
Proxy identifiziert den anfragenden Agenten über ein per-Sandbox-Token
(Proxy-Authorization, beim Wake gesetzt) und wendet dessen effektive Allowlist an
(DB + ENV + Default), Cache ~15 s.

**Voraussetzung:** Die Control Plane muss über **TLS/wss** erreichbar sein. Im
network-Modus läuft auch die Daemon↔Control-Plane-WebSocket per HTTP-CONNECT
durch den Proxy — das funktioniert nur mit `wss://` (TLS-Tunnel), nicht mit
Klartext-`ws://`. `host.docker.internal` ist dafür automatisch auf der Allowlist.

**Verifiziert** (echte Container): per-Agent-Durchsetzung (Agent A erreicht nur
seine Hosts, Agent B nur seine), gesperrter Host 403, falsches/fehlendes Token
407, direkter Bypass scheitert, Entscheidungs-Log korrekt pro Agent. **Nicht**
end-to-end im Repo verifiziert: der vollständige Agent-Lauf (coveyd-WS durch den
Proxy + Runtime) — das braucht das Sandbox-Image, eine wss-Control-Plane und
echte Creds; auf der Zielumgebung gegenchecken.

Secrets (pro Agent, im SecretStore, **nicht** als Env): `zammad_url`,
`zammad_token`, `anthropic_api_key`/`claude_code_oauth_token`.

> Signaturprüfung: Ein **leeres** `COVEY_ZAMMAD_WEBHOOK_SECRET` deaktiviert die
> Prüfung (nur Dev). Im Produktivbetrieb immer setzen.

---

## 7. Bekannte Grenzen / Produktions-Checkliste

Der MVP-Durchstich funktioniert, aber vor echtem Kundenverkehr sind diese Punkte
zu beachten (Details und Dateiverweise siehe unten). Priorisiert:

**Blocker für Produktivbetrieb mit echten Kundendaten:**

1. **Egress-Enforcement (umgesetzt, zwei Stufen).** Mit `docker`-Provider +
   `COVEY_EGRESS_ENFORCE=true` geht der Sandbox-Verkehr über einen fail-closed
   Allowlist-Proxy (`internal/egress`). Zwei Modi (`COVEY_EGRESS_ISOLATION`):
   - `proxy` (Default): kooperativ via HTTP(S)_PROXY — verhindert naives
     Ausleiten, aber vom Agenten über direkte IPs umgehbar.
   - `network`: **harte Isolation** — Sandbox auf internem Docker-Netz ohne
     Internet, Proxy-Container als einziger Ausgang. Direkter Bypass ist damit
     unmöglich (verifiziert). Setup siehe unten.

   **Noch offen:** (a) network-Modus erfordert die Control Plane über TLS/wss
   (die WS läuft per CONNECT durch den Proxy); (b) der LLM-Key wird weiterhin als
   Env-Var in die Sandbox gereicht — ihn stattdessen am Proxy zu injizieren (Key
   nie in der Sandbox) bleibt der nächste Härtungsschritt; (c) der `local`-Provider
   kann prinzipiell nicht isolieren.
2. **Verbindungsverlust = verlorenes Ticket.** Jeder Fehler (auch ein
   Netzwerk-Blip) setzt die Aufgabe hart auf `failed`; kein Retry/Backoff, kein
   Daemon-Reconnect, einseitiger Heartbeat ohne Timeout. → transiente Fehler auf
   `open`/`blocked` zurücksetzen, Reconnect + beidseitiger Heartbeat.
3. **Startup-Reconcile (umgesetzt).** Beim `serve`-Start werden verwaiste
   `in_progress`-Tasks (Sandbox mit dem letzten Prozess verschwunden) auf `open`
   zurückgesetzt, sodass sie nach Crash/Deploy sofort wieder greifen. Gilt für
   Single-Node; node-übergreifende Session-Liveness bleibt für echtes HA offen.
4. **Budget deckelt nur reaktiv.** Kosten kommen erst *nach* dem Lauf; eine
   entlaufene Aufgabe kann das Budget sprengen, gebremst wird erst die nächste. →
   Streaming-Cost + `MaxBudgetUSD` an die CLI durchreichen.

**Härtung kurz danach:**

5. **`blocked`-Mechanik ist prompt-abhängig.** Fehlt die `COVEY_STATUS`-Zeile,
   wird die Aufgabe still als `done` gewertet. → strukturierter Tool-Call statt
   geparster Textzeile, sicherer Default (ungültig → `blocked`+Eskalation).
6. **Signing-Key prozess-lokal + flüchtig**, In-Memory-Session-Maps → Control
   Plane faktisch single-node (obwohl DB-Bausteine HA suggerieren). → Key
   persistieren; entweder ehrlich single-node (Leader-Election) oder Sessions
   node-übergreifend.
7. **Memory ist ein `HashEmbedder`**, kein echtes Embedding → nur Keyword-, keine
   semantische Ähnlichkeit.
8. **Kein Rate-Limiting/Lockout** auf Login + Webhook-Endpunkten.
9. **`webhook_events` ohne Retention** — Tabelle wächst unbegrenzt, periodisches
   Cleanup einplanen.
10. **„Kurzlebig gebrokert" ist derzeit kosmetisch:** Das langlebige Zammad-Token
    wird mit rein informativer TTL durchgereicht und vom Daemon die ganze Session
    gecacht. Für den Built-in-Store laut Spec ok, aber die TTL erzwingt nichts.

---

## 8. Ausblick

- **Per-Org-Intake-Konfiguration in der DB** statt Env: mehrere Support-Queues
  auf mehreren Agenten, gepflegt über die UI (heute: Env + ein Webhook pro Agent).
- **Queue→Agent-Routing in Covey**, damit ein einziger Webhook Tickets anhand
  ihrer Gruppe auf verschiedene Agenten verteilt (heute: Zuordnung über
  Slug-URL).
- **Deklarativer Intake-Filter** wie bei den Manifest-Plugins
  (`Webhook.IgnoreWhen`) für das kompilierte Zammad-Plugin — feldbasierte Regeln
  über Priorität/Status/Tag, nicht nur Gruppe.
