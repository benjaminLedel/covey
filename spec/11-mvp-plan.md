# 11 — MVP-Plan

Übersetzt den MVP-Scope aus [`07-offene-entscheidungen.md`](07-offene-entscheidungen.md), die BUILD-Zeilen der Matrix aus [`08-marktumfeld.md`](08-marktumfeld.md) und den Stack aus [`10-architektur-stack.md`](10-architektur-stack.md) in eine konkrete Bau-Reihenfolge.

## Ziel (Definition of Done)

Ein **Support-Agent**, der ein Ticket triagiert, selbst beantwortet oder eskaliert, bei einer Rückfrage sauber `blocked` geht, durch die eingehende Antwort korrekt wieder aufwacht und die Lösung ins Gedächtnis schreibt — **vollständig aufgezeichnet, durch zentrale Guard-Rails eingehegt und mit Kill-Switch**. Wenn dieser eine Durchstich läuft, steht Coveys Kern.

## MVP-Prinzipien

- **Dünnster vertikaler Durchstich zuerst.** Nicht Layer für Layer horizontal, sondern früh eine end-to-end lauffähige (wenn auch triviale) Kette.
- **`builtin` überall.** Keine externen Schwergewichte im MVP (Identität/Secrets/Queue/Observability als Built-in, siehe [`10-architektur-stack.md`](10-architektur-stack.md)).
- **Genau eins von allem.** Ein Agenten-Typ (Support), eine Runtime (Claude Code via ACP), ein Zielsystem (Ticketsystem), seriell.
- **Den `blocked`-Loop früh entrisiken.** Er ist das Definierende *und* das Riskanteste — Design vor Bau (D1 klären).
- **Jeder Meilenstein ist demonstrierbar.** Kein Meilenstein ohne sichtbares Ergebnis in der UI oder im Log.

## Meilenstein-Übersicht

| # | Fokus | Größe | Risiko | Kernabhängigkeit |
|---|---|---|---|---|
| **M0** | Walking Skeleton (Binary + Postgres + UI-Shell + Migrations) | M | niedrig | — |
| **M1** | Sandbox + Daemon-Protokoll + eine Runtime | L | **hoch** | M0 |
| **M2** | Config-as-Code (`SOUL.md` → Prompt) | S | niedrig | M1 |
| **M3** | Backlog + State-Machine (seriell, ohne `blocked`) | M | mittel | M2 |
| **M4** | **`blocked`-Loop + Event-Korrelation** | M | **hoch** | M3, D1 |
| **M5** | Secrets-Broker + ein Zielsystem (Ticketsystem) | M | mittel | M2 |
| **M6** | Guard-Rails + Recording + Kill-Switch + Cost + RBAC | L | mittel | M3 |
| **M7** | Memory (pgvector: query@triage, ingest@done) | S | niedrig | M3 |

Reihenfolge ist nicht strikt linear: **M5** hängt an M2 (nicht an M4) und kann parallel zu M3/M4 laufen; **M6** und **M7** setzen auf M3 auf.

---

## M0 — Walking Skeleton

**Ziel:** Die deploybare Wirbelsäule steht, noch ohne alles Agentische.

- **Bauen:** Go-Binary mit `serve`/`migrate`/`bootstrap`; Postgres-Schema (Agent-Registry, Orgs, Rollen) via eingebettete Migrationen; eingebettete React/Tailwind-Admin-Shell, die Agenten listet/anlegt; Config über ENV; `/healthz` + `/readyz`.
- **Adoptieren:** —
- **Ergebnis:** `covey migrate up && covey serve` läuft; ein Agent lässt sich anlegen und erscheint in der UI. Beweist den Stack aus [`10-architektur-stack.md`](10-architektur-stack.md) end-to-end.

## M1 — Sandbox + Daemon-Protokoll + eine Runtime

**Ziel:** Die Data-Plane lebt; ein Agent kann in einer Sandbox denken.

- **Bauen:** Das bidirektionale **Daemon-Protokoll** (`wake`/`assign_task`/`event`/`sleep`, siehe [`01-architektur.md`](01-architektur.md)) in Minimalform; ein schlanker Daemon, der in der Sandbox läuft; **ein Adapter** für Claude Code, konkret über den **Headless-Modus `claude -p`** (Details in [`12-claude-code-adapter.md`](12-claude-code-adapter.md)).
- **Adoptieren:** Sandbox-Infra (E2B oder Beam, siehe [`08-marktumfeld.md`](08-marktumfeld.md)); Claude Code als Runtime. (ACP als generischer Multi-Runtime-Standard erst post-MVP für weitere Runtimes.)
- **Ergebnis:** Aus der UI „wecken" → Sandbox startet → Daemon verbindet → triviale Aufgabe zuweisen → Output/Events streamen zurück. **Höchstes Infrastruktur-Risiko — deshalb früh.**

## M2 — Config-as-Code

**Ziel:** Das Verhalten des Agenten kommt aus seiner Konfiguration.

- **Bauen:** `SOUL.md` + minimaler MD-Satz in DB/Git; Kompilierung zu System-Prompt + Runtime-Config; Injektion via `inject_config` beim Wake (siehe [`02-agenten-modell.md`](02-agenten-modell.md)).
- **Ergebnis:** `SOUL.md` ändern → geändertes Verhalten beim nächsten Wake. Versioniert, per Review.

## M3 — Backlog + State-Machine (seriell)

**Ziel:** Der Agent hat ein echtes Arbeitsleben — außer Blocken.

- **Bauen:** Backlog als First-Class-Postgres-Objekt (State/Priorität/Herkunft/Historie); die Zustandsmaschine `sleeping → triggered → triage → working → done`; der **Dispatch-Loop** (Postgres `SKIP LOCKED` + `LISTEN/NOTIFY`); Wake-Quellen Event (neues Ticket per **Zammad-Trigger → Webhook**, siehe [`13-zammad-integration.md`](13-zammad-integration.md)) + manuell; strikt seriell (siehe [`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md)).
- **Ergebnis:** Aufgabe ins Backlog → Agent wacht auf, arbeitet sie seriell ab, markiert `done`, schläft. Live-Status in der UI.

## M4 — Der `blocked`-Loop + Event-Korrelation

**Ziel:** Der Agent wird zum Angestellten — er parkt und wacht korrekt wieder auf. **Das Herz des MVP.**

- **Voraussetzung:** Entscheidung **D1** (Korrelations-Key vs. zentraler Event-Router vs. Hybrid) — *als Design-Spike vor dem Bau klären*, siehe [`07-offene-entscheidungen.md`](07-offene-entscheidungen.md).
- **Bauen:** `blocked`-Zustand mit Suspendierung; Korrelations-Key beim Parken; **ein** Wake-Kanal (Ticket-Update). Für Zammad ist der Korrelations-Key die **Ticket-`id`** (kommt im Webhook mit) und `blocked` bildet sich auf den Zammad-**`pending`-State** ab — siehe [`13-zammad-integration.md`](13-zammad-integration.md). Die Wiederaufnahme nutzt bei Claude Code die native **`--resume <session_id>`**-Mechanik (die `session_id` wird zur geparkten Aufgabe gespeichert) — siehe [`12-claude-code-adapter.md`](12-claude-code-adapter.md).
- **Ergebnis:** Agent stellt eine Rückfrage, geht `blocked`, Sandbox fährt runter; die eingehende Antwort korreliert und weckt ihn zum Fortsetzen. Kein Pollen, kein Halluzinieren.

## M5 — Secrets-Broker + ein Zielsystem

**Ziel:** Der Agent handelt in einem echten System — ohne Secrets in der Sandbox.

- **Bauen:** `IdentityProvider` + `SecretStore` in der **Built-in-Variante** (signierte JWTs, AES-GCM-Secrets in Postgres, siehe [`10-architektur-stack.md`](10-architektur-stack.md)); Broker verwahrt das **Zammad-API-Token** (permission-gescopte Rolle) und injiziert es kurzlebig; Least-Privilege-Anbindung an **Zammad** (Ticket lesen / antworten / Status setzen), siehe [`13-zammad-integration.md`](13-zammad-integration.md).
- **Adoptieren:** **Zammad** als Zielsystem (self-hosted, REST-API + Webhooks); optional Keycloak, falls vorhanden (sonst `builtin`).
- **Ergebnis:** Der Agent liest/schreibt echte Zammad-Tickets über gebrokerte Credentials — nichts Langlebiges in der Sandbox (siehe [`04-identitaet-secrets.md`](04-identitaet-secrets.md)).

## M6 — Guard-Rails + Recording + Kill-Switch + Cost

**Ziel:** Die Vertrauensschicht — ohne die es keine Adoption gibt.

- **Bauen:** minimaler **zentraler Guard-Rail-Satz** (Egress-Deny ohne Freigabe, Deny nicht-freigegebener Systeme/Tools, Approval-Pflicht für destruktive Aktionen, fail-closed); Session-Recording in Postgres; Kill-Switch (einzeln + flottenweit); simples Cost-Tracking pro Agent; **rollen-gescopte Sichten** (Basis-RBAC, siehe [`06-observability-control.md`](06-observability-control.md), [`09-enterprise-modell.md`](09-enterprise-modell.md)).
- **Ergebnis:** Alles, was der Agent tut, ist aufgezeichnet und inspizierbar; riskante Aktionen gaten; der Agent lässt sich sofort stoppen; Kosten pro Agent sichtbar.

## M7 — Memory

**Ziel:** Der Agent kennt den Laden.

- **Bauen:** Built-in-Memory über **pgvector**; Abfrage im `triage`-Schritt, Einspeisung im `done`-Schritt (siehe [`05-gedaechtnis.md`](05-gedaechtnis.md)). MVP-Baseline: flacher Schnipsel-Store; Weiterentwicklung zum **Wiki** (verlinkte Markdown-Seiten + pgvector-Index, Konsolidierungs-Pass) über dieselbe Naht.
- **Adoptieren:** Graphiti erst post-MVP, falls echtes temporales Reasoning über das Wiki hinaus gebraucht wird.
- **Ergebnis:** „Mit diesem Kunden hatte ich letzte Woche zu tun, die Lösung war Y."

---

## Kritischer Pfad

Zwei Meilensteine tragen das Risiko: **M1** (Sandbox/Daemon/Runtime — Infrastruktur) und **M4** (`blocked` + Korrelation — die definierende Mechanik). Beide früh angehen; M1 als erstes technisches Risiko, M4 mit einem Design-Spike (D1) *vor* dem Bau. Der Rest (M0, M2, M3, M5, M6, M7) ist vergleichsweise Standard-Engineering.

## Explizit später (nicht MVP)

Weitere Runtimes/Adapter · Supervisor-Agent (KI-Anomalie-Erkennung) · geteiltes Org-weites Gedächtnis · Inter-Agent-Kommunikation via A2A/MCP · voll ausgebautes Admin-Dashboard · Externalisierung auf Keycloak/Vault/Redis/Langfuse/Graphiti · Mehr-Mandanten-Fähigkeit (D9). Alle kommen über die bereits gezogenen Interfaces hinzu, ohne den Kern zu ändern.

## Abnahme-Checkliste (der eine Durchstich)

Ein neues **Zammad**-Ticket trifft ein und der Support-Agent durchläuft nachweislich:

- [ ] **Wake** durch den Zammad-Webhook (Trigger auf „Ticket erstellt", nicht durch Polling).
- [ ] **Triage:** Backlog + Memory geprüft, priorisiert.
- [ ] **Working:** Zugriff auf Zammad über das gebrokerte API-Token (kein Secret in der Sandbox).
- [ ] **Blocked:** Rückfrage an den Kunden gestellt, Ticket auf `pending` gesetzt, sauber `blocked` gegangen, Sandbox heruntergefahren.
- [ ] **Wake-on-correlation:** eingehende Kundenantwort korreliert über die Ticket-`id`, Agent setzt via `--resume` fort.
- [ ] **Done:** gelöst oder eskaliert; Lösung ins Memory geschrieben.
- [ ] **Guard-Rails:** eine riskante Aktion (z. B. externe Mail) wird gegated.
- [ ] **Recording:** die gesamte Session ist lückenlos aufgezeichnet und inspizierbar.
- [ ] **Kill-Switch:** der Agent lässt sich jederzeit sofort stoppen.
- [ ] **Cost:** verbrauchte Tokens/Compute pro Agent sichtbar.

Sind alle Punkte grün, ist die MVP-Leitfrage beantwortet.
