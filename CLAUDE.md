# CLAUDE.md

Orientierung für Claude Code in diesem Repository.

## Was ist Covey

Enterprise-Plattform, die **KI-Agenten wie Mitarbeiter** behandelt: Identität, isolierte Sandbox, gebrokerte Zugänge, Backlog, Org-Chart, zentrale Governance. Einheit ist die **Organisation**, nicht der einzelne Nutzer. Leitmetapher: die **IT- und HR-Abteilung für KI-Agenten**.

## Aktueller Zustand

**Deutlich über den MVP hinaus.** Der MVP-Durchstich (M0–M7 aus `spec/11-mvp-plan.md`) steht längst; darauf aufgesetzt sind u. a. Org-Chart & Abteilungen, Mitarbeiter-Profile, weitere Zielsystem-Plugins (GitLab, E-Mail/IMAP, MCP), Docker-Sandboxen, Egress-Kontrolle, ein QA-Agent-Bundle und das **Wiki-Gedächtnis** (verlinkte Markdown-Seiten + pgvector-Index statt flacher Schnipsel, spec/05). „M0–M7" markiert also die Grundlinie, nicht den aktuellen Umfang. Das Repo enthält:

- `spec/` — die vollständige Spezifikation (13 Dokumente, **deutschsprachig**). Einstieg: `spec/README.md`.
- Den Code gemäß dem Layout aus `spec/10-architektur-stack.md`: `cmd/covey` (Control Plane), `cmd/coveyd` (Sandbox-Daemon), `internal/…`, `web/` (React-SPA, eingebettet), `migrations/` (eingebettet).
- `internal/integration/` — die Abnahme-Checkliste aus `spec/11-mvp-plan.md` als Integrationstest-Suite (echtes Postgres auf Port 5433, In-Process-Daemon über echten WebSocket, Mock-Runtime, Fake-Zammad).
- `demo/fakezammad/` — Zammad-Double für lokale Demos.
- `mockup/covey-ui-mockup.html` — statischer HTML-Mockup; die React-UI übernimmt seine Design-Sprache (CSS-Variablen, Inter/Lora).
- `.claude/skills/covey-agent/` — Claude-Code-Skill zum **Bauen/Designen/Aktualisieren von Covey-Agenten**: erzeugt ein `covey.agent-config`-Bundle nach den Repo-Konventionen (SOUL/PLAYBOOKS/ACCESS/HEARTBEAT, Loop-Schutz, warm_sandbox) und legt den Agenten optional per API an. Einstieg: die `SKILL.md` dort.
- `praesentationen/*.pptx` — Pitch-/Investor-Material (nicht editieren, sofern nicht ausdrücklich gewünscht).

Entwicklungs-Workflow: `make dev-db && make bootstrap && make run` (siehe README). Tests: `make test`, Integrationstests: `make test-integration` (brauchen die Dev-DB; sie skippen, wenn Port 5433 nicht erreichbar ist). Vor dem Go-Build muss `web/dist` existieren (`cd web && npm run build`) — `//go:embed` zieht es ins Binary.

**Server nach Änderungen neu bauen und durchstarten.** `go build ./...` ist nur ein Kompilier-Check — es schreibt das `./covey`-Binary *nicht* neu. Damit Änderungen (Backend wie Web-UI) live sind: `make build` (baut `web/dist` + `covey` + `coveyd`), dann den laufenden `covey serve`-Prozess beenden (`pgrep -fl "covey serve"`) und via `make run` bzw. `COVEY_MASTER_KEY=$(cat .covey.key) ./covey serve` neu starten. Die Data Plane läuft über den **docker-Provider** (Default); nach coveyd-relevanten Änderungen `make sandbox-image` neu bauen, damit die Sandbox das neue Binary + chromium hat. Migrationen laufen beim `serve`-Start automatisch (Auto-Migrate mit advisory lock).

## Sprache & Konventionen

- **Spec und Doku sind auf Deutsch.** Schreibe Spec/Runbooks/Kommentare, die in dieses Repo gehen, auf Deutsch, im Ton der bestehenden Dokumente (nüchtern, präzise, Fachbegriffe englisch belassen: *Control Plane*, *Guard-Rail*, *Daemon*, *Backlog*, *blocked*).
- **Ausnahme README:** `README.md` ist **englisch** — es ist die Visitenkarte für Dritte, die Covey von GitHub installieren. Die deutsche Fassung liegt daneben in `README.de.md`. Beide beim Ändern gleichziehen; sie sind Übersetzungen voneinander, keine getrennten Dokumente.
- Spec-Dokumente verlinken sich gegenseitig relativ (`[`04-…`](04-identitaet-secrets.md)`). Diese Verlinkung beim Ändern konsistent halten.
- Jede Datei in `spec/` hat einen klaren Zuständigkeitsbereich (siehe Tabelle in `spec/README.md`) — Inhalt in die richtige Datei schreiben, nicht duplizieren.

## Kernarchitektur (zum schnellen Nachschlagen)

- **Control Plane** (zustandsführend, immer aktiv): Scheduler/Dispatcher, Agent-Registry & Org-Chart, Backlog-Store, Identitäts- & Secrets-Broker, Guard-Rail-/Policy-Engine, Observability, Config-Sync.
- **Data Plane**: isolierte, ephemere Sandboxen mit persistentem `/home`. „Dumm und ersetzbar" — geht eine Sandbox verloren, wird sie aus Config + Home neu gebaut.
- **Daemon-Protokoll**: bidirektional (WebSocket/gRPC) zwischen Control Plane und Sandbox-Daemon. Stabile Naht — Runtimes ändern sich, das Protokoll bleibt. Nachrichten in `spec/01-architektur.md`.
- **Runtime-Adapter**: dünn, übersetzen zwischen Daemon-Protokoll und Runtime-Spezifika. Erster Adapter: Claude Code headless via `claude -p` (`spec/12-claude-code-adapter.md`).

## Geplanter Stack (Referenz)

- **Backend:** ein Go-Binary (Tendenz Go, endgültige Sprachwahl offen — D10). API/BFF + Orchestration-Core, ein Prozess, sauber getrennt.
- **Frontend:** React-SPA + Tailwind + shadcn/ui + TanStack Query, WebSocket/SSE, **ins Binary eingebettet** (`//go:embed`).
- **Postgres als Anker:** State, Backlog, RBAC, Queue (`SELECT … FOR UPDATE SKIP LOCKED`), Pub/Sub (`LISTEN/NOTIFY`), Memory (`pgvector`), AES-GCM-Secret-Spalten.
- **Migrationen:** versionierte SQL unter `migrations/` (up/down), eingebettet via `//go:embed`, ausgeführt per `covey migrate up` mit `pg_advisory_lock`. Bestehende Migrationen nie editieren — immer neue anlegen.
- **„Batteries included, but swappable":** zwei Ports tragen das Muster — `IdentityProvider` (builtin JWT/Argon2id ↔ `oidc`) und `SecretStore` (builtin AES-GCM ↔ `vault`). Interface vor Implementierung ziehen, auch wenn nur `builtin` existiert.

## Wo der Code liegen soll

Single-Binary-Go-Projekt mit **`go.mod` im Repo-Wurzelverzeichnis** — der Code liegt **neben `spec/`**, nicht in einem Unterordner. Frontend und Migrationen werden via `//go:embed` mitkompiliert und müssen deshalb im selben Modulbaum liegen. Layout aus `spec/10-architektur-stack.md`:

```
covey/                    ← Repo-Wurzel = Go-Modul-Wurzel (go.mod hier)
  cmd/covey/              main.go — Wiring, Flags, Subcommands (serve, migrate, bootstrap)
  internal/
    orchestrator/         Dispatcher, State-Machine, Daemon-Verbindungen (Control-Plane-Seite)
    agents/               Registry, Config-Kompilierung (SOUL.md → Prompt)
    backlog/              Backlog-Store, Zustandsübergänge
    identity/             IdentityProvider — builtin/ (JWT/Argon2id) + oidc/
    secrets/              SecretStore — builtin/ (AES-GCM) + vault/
    target/               Zielsystem-Plugins — Registry + Manifest-Engine, zammad/ als erstes Built-in
    guardrails/           Policy-Engine, Enforcement-Punkte
    observability/        Recording, Cost, Alerts
    http/                 API/BFF-Handler, RBAC-Middleware
  web/                    React/Vite-Frontend (dist/ via //go:embed eingebettet)
  migrations/             SQL-Migrationen (up/down, via //go:embed eingebettet)
  go.mod
  ─────────────────────── (existierend, bleibt daneben)
  spec/  mockup/  praesentationen/
```

Begründung: ein Binary → `go.mod` in der Wurzel, `web/` + `migrations/` als Geschwister von `cmd/` (sonst greift `//go:embed` nicht). `internal/` hält die Pakete privat. Bei den Pluggable-Ports (`identity/`, `secrets/`) steht das Interface im Paket-Wurzel, die Implementierungen in Unterpackages — „Interface vor Implementierung".

**Offen / im Layout der Spec noch nicht vorgesehen:** Der **Sandbox-Daemon** (läuft *in* der Sandbox, spricht das Daemon-Protokoll, bootstrappt die Runtime) ist ein zweites, kleines Binary — voraussichtlich `cmd/coveyd/` mit `internal/daemon/` (Protokoll-Client + Runtime-Adapter). Das `orchestrator/`-Paket ist nur die Control-Plane-Seite dieser Verbindung.

## Wenn mit dem Bau begonnen wird

- **Dünnster vertikaler Durchstich zuerst** (end-to-end statt Layer für Layer), `builtin` überall, genau eins von allem (ein Agenten-Typ Support, eine Runtime Claude Code, ein Zielsystem Zammad, seriell).
- **Zwei Risiko-Meilensteine früh:** M1 (Sandbox/Daemon/Runtime) und M4 (`blocked`-Loop + Event-Korrelation, mit Design-Spike zu D1 *vor* dem Bau).
- Definition of Done des MVP = die Abnahme-Checkliste am Ende von `spec/11-mvp-plan.md`.

## Leitplanken (aus den Designprinzipien)

- **Niemals langlebige Secrets in die Sandbox** — Zugriff zur Laufzeit brokern, kurzlebig, gescopt.
- **Guard-Rails zentral erzwingen**, außerhalb der Runtime, fail-closed — nicht dem Agenten-Prompt überlassen.
- **Krypto-Primitive ja, Krypto-Protokolle nein:** JWTs signieren / AES-GCM / Argon2id mit bewährten Libs; keinen eigenen OAuth/OIDC-Server nachbauen — das übernimmt der externe Provider.
- **Config-as-Code:** Agentenverhalten (`SOUL.md`) versioniert, Änderung via PR/Review, nicht via Deploy.

## Git

- Branch `main`, Remote ist GitLab (`gitlab.lapco.legal`).
- **Regelmäßig committen:** abgeschlossene, zusammengehörige Arbeitsschritte (Feature fertig, Tests grün) als eigenen Commit festhalten — nicht alles in einem Riesen-Commit sammeln. Commit-Messages auf Deutsch, im Stil der bestehenden Historie.
- Nicht auf `main` committen ohne Rückfrage — vorher Branch anlegen. Push weiterhin nur auf ausdrückliche Bitte.
