# Covey

> **Codename: Covey.** Ein „covey" ist ein kleiner, koordinierter Schwarm — eine abgestimmte Gruppe, die zusammen unterwegs ist. Genau das ist die Plattform: viele Agenten, zentral orchestriert.

Eine zentrale Plattform, die KI-Agenten wie Mitarbeiter behandelt — mit Identität, Arbeitsplatz, Zugängen, Backlog und Vorgesetztem — und dem IT-Admin die Werkzeuge gibt, sie zu führen und zu überwachen.

**Coveys Einheit ist die Organisation, nicht der einzelne Nutzer.** Das ist die tragende Abgrenzung zu den Single-User-„AI-Employee"-Apps: Covey ist die Plattform, die ein *Unternehmen* betreibt, um seine gesamte Agenten-Belegschaft zu verwalten und zu governen — mit vielen menschlichen Stakeholdern (IT, Team-Leads, Security/Compliance, Audit, Controlling), zentraler Governance und unternehmensweitem Org-Chart.

## Status

**MVP implementiert.** Das Repository enthält neben der **Spezifikation** den vollständigen MVP-Durchstich (Meilensteine M0–M7 aus [`spec/11-mvp-plan.md`](spec/11-mvp-plan.md)): das `covey`-Binary (Control Plane, API, eingebettete Admin-UI), den Sandbox-Daemon `coveyd`, den Claude-Code-Adapter, die Zammad-Integration und die Vertrauensschicht (Guard-Rails, Recording, Kill-Switch, Cost, RBAC). Die Abnahme-Checkliste läuft als Integrationstest-Suite (`internal/integration/`).

## Schnellstart mit Docker Compose (empfohlen zum Ausprobieren)

Covey in Minuten starten — **ohne Go, Node oder lokale Postgres**. Nur Docker nötig:

```bash
cp .env.example .env
echo "COVEY_MASTER_KEY=$(openssl rand -hex 32)" >> .env   # 32-Byte-Schlüssel
docker compose up -d --build                              # Postgres + Covey starten
```

Dann [http://localhost:8494](http://localhost:8494) öffnen — Login `admin@covey.local` / `covey-admin`.
Das mitgelieferte [`docker-compose.yml`](docker-compose.yml) bringt Postgres (pgvector) und das
covey-Binary mit eingebetteter Admin-UI; `bootstrap` legt Organisation, Admin und einen Demo-Agenten
an, Migrationen laufen automatisch. Vollständige Anleitung inkl. erstem Agenten und Produktions-Checkliste:
[`docs/schnellstart-docker.md`](docs/schnellstart-docker.md).

**Automatisches Deployment (main → Host):** Jeder Push auf `main` rollt Covey über die
GitLab-Pipeline (`test → build → deploy`) auf einen Zielhost aus — das gebaute Image auf den
Commit-Tag gepinnt, via [`docker-compose.deploy.yml`](docker-compose.deploy.yml) auf einem
Shell-Runner am Host gestartet. Einrichtung und Betrieb: [`docs/betrieb-deployment.md`](docs/betrieb-deployment.md).

## Schnellstart (Entwicklung)

```bash
make dev-db       # Postgres (pgvector) via Docker auf Port 5433
make bootstrap    # Frontend + Binaries bauen, migrieren, Org/Admin/Agent anlegen
make run          # covey serve auf http://localhost:8494
```

**Sandbox-Isolation:** Standardmäßig startet die Control Plane Sandboxen als lokale Subprozesse (`COVEY_SANDBOX_PROVIDER=local`) — ehrlich, aber nur Prozess-Isolation. Für echte Container-Isolation `make sandbox-image` (baut `covey-sandbox:latest` aus [`Dockerfile.sandbox`](Dockerfile.sandbox), coveyd + Claude Code) und mit `COVEY_SANDBOX_PROVIDER=docker` starten. Das persistente Agenten-Home wird als Volume gemountet; der Container erbt nichts von der Host-Umgebung. Image überschreibbar via `COVEY_SANDBOX_IMAGE`.

Login: `admin@covey.local` / `covey-admin` (überschreibbar via `COVEY_ADMIN_EMAIL`/`COVEY_ADMIN_PASSWORD` beim Bootstrap). Tests: `make test` (Unit) und `make test-integration` (voller Durchstich gegen die Dev-DB, mit Mock-Runtime und Fake-Zammad). Für Demos ohne echtes Zammad: `go run ./demo/fakezammad` und die Secrets `zammad_url` = `http://localhost:9999`, `zammad_token` beliebig setzen. Damit die Claude-Code-Runtime in der Sandbox arbeiten kann, das Secret `anthropic_api_key` (API-Key) oder alternativ `claude_code_oauth_token` (Abo-Account: Token einmalig mit `claude setup-token` erzeugen) hinterlegen — ohne eines der beiden scheitern Aufgaben mit „Not logged in · Please run /login", weil die Sandbox ein eigenes, leeres `HOME` hat und die lokale `claude`-Anmeldung dort nicht sichtbar ist.

**Anschluss an ein echtes Zammad:** Schritt-für-Schritt-Runbook (API-Token, Webhook + Trigger, Ticket-Auswahl steuern, kundensichtbare Antworten, Produktions-Checkliste) in [`docs/betrieb-zammad.md`](docs/betrieb-zammad.md).

## Die Leitmetapher

Die Plattform ist die **IT- und HR-Abteilung für KI-Agenten**. Fast jede Komponente hat ein Gegenstück im echten Unternehmen — daraus folgt der Bauplan:

| Im Unternehmen | Auf der Plattform |
|---|---|
| Identität / Active Directory | Agent-Identität (E-Mail optional) |
| Arbeitsplatz / PC | Isolierte, persistente Sandbox |
| Onboarding / Org-Chart | `SOUL.md` + Org-Struktur |
| Belegschaft & Abteilungen | Org-eigene Agenten, Teams, Cost-Center |
| Personalabteilung / IT-Verwaltung | Menschliche Rollen + RBAC + SSO |
| Passwort-Tresor / PAM | Secrets-Broker (kurzlebige Tokens) |
| Aufgabenliste / Ticket | Backlog (First-Class-Objekt) |
| Betriebshandbuch / Compliance | Zentrale Guard-Rails (plattform-erzwungen) |
| SIEM / EDR | Session-Recording + Alerts + Kill-Switch |

## Architektur in einem Absatz

Das System zerfällt in eine **Control Plane** (zustandsführend, immer aktiv: Scheduler, Agent-Registry, Backlog-Store, Identitäts- & Secrets-Broker, Guard-Rail-Engine, Observability) und eine **Data Plane** aus isolierten, ephemeren **Sandboxen** mit persistentem Home. In jeder Sandbox läuft ein schlanker **Daemon**, der ein einheitliches Protokoll spricht und die konkrete **Runtime** (Claude Code, OpenHands, …) über einen dünnen **Adapter** bootstrappt. Die Plattform managt die Sandbox, nicht das Framework — dadurch bleibt die Runtime austauschbar. Details in [`spec/01-architektur.md`](spec/01-architektur.md).

## Designprinzipien

1. **Organisation als Einheit, nicht der Nutzer.**
2. **Die Control Plane ist das Produkt.** Sandboxen sind Commodity, Runtimes austauschbar.
3. **Runtime-agnostisch.** Einheitlicher Daemon + dünne Adapter statt Framework-Lock-in.
4. **Immer erreichbar, Compute nur bei Bedarf.** Idle muss wirklich idle sein.
5. **Config-as-Code.** Agentenverhalten versioniert in Git, Änderung via PR/Review.
6. **Niemals langlebige Secrets in die Sandbox.** Zugriff wird zur Laufzeit gebrokert, kurzlebig, gescopt.
7. **Guard-Rails zentral und plattform-erzwungen.** Fail-closed, außerhalb der Runtime durchgesetzt.
8. **Trust by design.** Recording, Freigaben und Kill-Switch sind Grundvoraussetzung, kein Add-on.
9. **Seriell vor parallel.** Ein Agent, eine Aufgabe zur Zeit; Parallelität = mehr Agenten.
10. **Batteries included, but swappable.** Jede Fähigkeit hat einen simplen, DB-gestützten Built-in-Default und ein schmales Interface für einen externen Provider.

## Geplanter Stack

- **Backend:** ein einziges Go-Binary (Tendenz Go, siehe D10) — API/BFF + Orchestration-Core in einem Prozess, sauber getrennt.
- **Frontend:** TypeScript-SPA (React), Tailwind + shadcn/ui, TanStack Query, WebSocket/SSE für Live-Updates — ins Binary eingebettet.
- **Datenhaltung:** PostgreSQL als Anker — State, Backlog, RBAC, Job-Queue (`SKIP LOCKED`), Pub/Sub (`LISTEN/NOTIFY`), Memory (`pgvector`), verschlüsselte Secret-Spalten.
- **MVP-Default:** `builtin` überall — faktisch **Binary + Postgres + Sandbox-Infra, sonst nichts**. Keycloak/Vault/Redis/Langfuse/Graphiti sind optional zuschaltbar, nicht Voraussetzung.

Deployment-Ziel: eine Datei kopieren, `covey migrate up`, `covey serve` — kein separates Frontend-Hosting, kein nginx. Details in [`spec/10-architektur-stack.md`](spec/10-architektur-stack.md).

## MVP — der eine Durchstich

Ein **Support-Agent**, der ein **Zammad**-Ticket triagiert, selbst beantwortet oder eskaliert, bei einer Rückfrage sauber `blocked` geht, durch die eingehende Antwort korrekt wieder aufwacht und die Lösung ins Gedächtnis schreibt — **vollständig aufgezeichnet, durch zentrale Guard-Rails eingehegt und mit Kill-Switch**. Läuft dieser Durchstich, steht Coveys Kern. Abnahme-Checkliste in [`spec/11-mvp-plan.md`](spec/11-mvp-plan.md).

## Repository-Inhalt

| Pfad | Inhalt |
|---|---|
| [`spec/`](spec/) | Die vollständige Spezifikation (Einstieg: [`spec/README.md`](spec/README.md)) |
| `cmd/covey/` | Control-Plane-Binary: `serve`, `migrate`, `bootstrap`, `genkey` |
| `cmd/coveyd/` | Sandbox-Daemon (spricht das Daemon-Protokoll, bootstrappt die Runtime) |
| `internal/` | Orchestrator, Agents, Backlog, Identity/Secrets (builtin), Guard-Rails, Observability, Memory, Zielsystem-Plugins (`target/`, Zammad als erstes Built-in), HTTP-API |
| `migrations/` | Versionierte SQL-Migrationen (via `//go:embed` ins Binary gebacken) |
| `web/` | React/Vite/Tailwind-Admin-UI (dist/ wird ins Binary eingebettet) |
| `demo/fakezammad/` | Minimales Zammad-Double für lokale Demos |
| `mockup/covey-ui-mockup.html` | Statischer HTML-Mockup der Admin-Oberfläche |
| `praesentationen/` | Pitch- und Investor-Decks (`.pptx`) |

### Spec-Dokumente

| Datei | Inhalt |
|---|---|
| [`spec/01-architektur.md`](spec/01-architektur.md) | System-Übersicht, Control/Data Plane, Runtime-Abstraktion, Daemon-Protokoll |
| [`spec/02-agenten-modell.md`](spec/02-agenten-modell.md) | Der Agent als Entität: Identität, Sandbox, Zugänge, Config-as-Code, Org-Chart |
| [`spec/03-lifecycle-scheduling.md`](spec/03-lifecycle-scheduling.md) | Zustandsmaschine, Dispatch-Loop, Wake-Quellen, Backlog, Blocking, Korrelation |
| [`spec/04-identitaet-secrets.md`](spec/04-identitaet-secrets.md) | Keycloak, RFC 8693 Token Exchange, Secrets-Broker, Threat-Model |
| [`spec/05-gedaechtnis.md`](spec/05-gedaechtnis.md) | Memory-Schichten, Knowledge-Graph (Graphiti), persistentes Home |
| [`spec/06-observability-control.md`](spec/06-observability-control.md) | Guard-Rails, Session-Recording, Approval-Gates, Kill-Switch, Kosten, Supervisor |
| [`spec/07-offene-entscheidungen.md`](spec/07-offene-entscheidungen.md) | Offene Fragen, Build-vs-Buy, MVP-Scope |
| [`spec/08-marktumfeld.md`](spec/08-marktumfeld.md) | Marktrecherche: Konkurrenz, Open-Source-Bausteine, Build-vs-Adopt |
| [`spec/09-enterprise-modell.md`](spec/09-enterprise-modell.md) | Organisation als Einheit: Rollen & RBAC, SSO, Mandanten, Cost-Center, Compliance |
| [`spec/10-architektur-stack.md`](spec/10-architektur-stack.md) | Frontend, Backend-Sprache, „batteries included, but swappable", Postgres-Anker |
| [`spec/11-mvp-plan.md`](spec/11-mvp-plan.md) | Bau-Reihenfolge M0–M7, kritischer Pfad, Abnahme-Checkliste |
| [`spec/12-claude-code-adapter.md`](spec/12-claude-code-adapter.md) | Erster Runtime-Adapter: Claude Code headless via `claude -p` |
| [`spec/13-zammad-integration.md`](spec/13-zammad-integration.md) | MVP-Zielsystem Zammad: Wake via Webhook, REST-Aktionen, `blocked`↔`pending` |

## Mitwirken

Solange das Repository in der Design-Phase ist, gehen Änderungen an Konzept und Architektur über die Spec: Vorschläge als Merge Request gegen `spec/`, Diskussion offener Punkte in [`spec/07-offene-entscheidungen.md`](spec/07-offene-entscheidungen.md).
