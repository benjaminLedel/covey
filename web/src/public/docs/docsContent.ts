/* Inhalt des Docs-Bereichs der öffentlichen Website — zweisprachig (de/en).
   Rendering über die Markdown-Komponente (components/Markdown.tsx). Neue Seiten
   hier ergänzen; die Navigation baut sich aus DOC_SECTIONS auf. Titel und Body
   liegen je Sprache vor; Docs.tsx wählt anhand der aktiven i18n-Sprache. */

export type Lang = "de" | "en";
export type Localized = Record<Lang, string>;
export type DocPage = { slug: string; title: Localized; body: Localized };
export type DocSection = { id: string; title: Localized; pages: DocPage[] };

export const DOC_SECTIONS: DocSection[] = [
  {
    id: "einfuehrung",
    title: { de: "Einführung", en: "Introduction" },
    pages: [
      {
        slug: "was-ist-covey",
        title: { de: "Was ist Covey?", en: "What is Covey?" },
        body: {
          de: `# Was ist Covey?

Covey ist eine Enterprise-Plattform, die KI-Agenten wie Mitarbeiter behandelt — mit eigener Identität, isoliertem Arbeitsplatz, gebrokerten Zugängen, einem Backlog und einem Platz im Org-Chart. Die Leitmetapher: die Plattform ist die IT- und HR-Abteilung für KI-Agenten.

## Die Organisation ist die Einheit

Coveys Einheit ist die Organisation, nicht der einzelne Nutzer. Das unterscheidet Covey von Single-User-„AI-Employee"-Apps. Ein Unternehmen betreibt Covey, um seine gesamte Agenten-Belegschaft zu verwalten und zu governen — mit mehreren menschlichen Rollen (IT, Team-Leads, Security/Compliance, Auditor, Controlling), zentraler Governance und einem unternehmensweiten Org-Chart. Agenten sind organisationseigene Ressourcen, keine persönlichen Assistenten.

## Entsprechungen im Unternehmen

Die meisten Komponenten haben ein Gegenstück im Unternehmen:

- Identität / Active Directory → Agent-Identität
- Arbeitsplatz / PC → isolierte, persistente Sandbox
- Onboarding / Org-Chart → \`SOUL.md\` + Org-Struktur
- Passwort-Tresor / PAM → Secrets-Broker (kurzlebige Tokens)
- Aufgabenliste / Ticket → Backlog als First-Class-Objekt
- Betriebshandbuch / Compliance → zentrale Guard-Rails
- SIEM / EDR → Session-Recording + Alerts + Kill-Switch`,
          en: `# What is Covey?

Covey is an enterprise platform that treats AI agents like employees — with their own identity, an isolated workspace, brokered access, a backlog and a place in the org chart. The guiding metaphor: the platform is the IT and HR department for AI agents.

## The organization is the unit

Covey's unit is the organization, not the individual user. That is what sets Covey apart from single-user "AI employee" apps. A company runs Covey to manage and govern its entire agent workforce — with several human roles (IT, team leads, security/compliance, auditor, controlling), central governance and a company-wide org chart. Agents are organization-owned resources, not personal assistants.

## Counterparts in the company

Most components have a counterpart in the company:

- Identity / Active Directory → agent identity
- Workstation / PC → isolated, persistent sandbox
- Onboarding / org chart → \`SOUL.md\` + org structure
- Password vault / PAM → secrets broker (short-lived tokens)
- Task list / ticket → backlog as a first-class object
- Operations manual / compliance → central guard-rails
- SIEM / EDR → session recording + alerts + kill switch`,
        },
      },
      {
        slug: "kernkonzepte",
        title: { de: "Kernkonzepte", en: "Core concepts" },
        body: {
          de: `# Kernkonzepte

Ein kompaktes Glossar der Begriffe, die in der ganzen Plattform wiederkehren.

## Agent

Eine konfigurierte, persistente Entität mit Identität, Sandbox, Zugängen und Backlog — das Gegenstück zum Mitarbeiter. Eine eigene E-Mail-Adresse ist optional.

## Control Plane

Der zentrale, zustandsführende Dienst: Scheduler/Dispatcher, Identitäts- & Secrets-Broker, Backlog-Store, Guard-Rail-Engine, Observability. Kennt den Zustand aller Agenten. Die Control Plane ist das Produkt; Sandboxen sind Commodity, Runtimes austauschbar.

## Data Plane

Die Gesamtheit der isolierten, ephemeren Sandboxen, in denen Agenten arbeiten. Geht eine Sandbox verloren, wird sie aus Config und Home neu gebaut.

## Runtime & Daemon

Die Runtime ist das Agent-Framework, das die LLM-Schleife fährt (erster Adapter: Claude Code headless). Der Daemon ist der schlanke Prozess in der Sandbox, der das einheitliche Plattform-Protokoll spricht und die Runtime bootstrappt.

## Backlog

Die persistente, priorisierte Aufgabenliste eines Agenten. Aufgaben haben Zustände; blockierte Aufgaben stellen Rückfragen an Menschen.

## Guard-Rail

Eine zentral definierte, plattformseitig erzwungene Grenze für Agentenverhalten (Egress-Regel, verbotenes Tool, Approval-Pflicht). Greift außerhalb der Runtime und ist vom Agenten nicht umgehbar — fail-closed.

## Secrets-Broker

Der Dienst, der Agenten kurzlebige, gescopte Access-Tokens für Zielsysteme ausstellt. Keine langlebigen Secrets in der Sandbox.`,
          en: `# Core concepts

A compact glossary of the terms that recur throughout the platform.

## Agent

A configured, persistent entity with identity, sandbox, access and backlog — the counterpart to an employee. A dedicated email address is optional.

## Control plane

The central, stateful service: scheduler/dispatcher, identity & secrets broker, backlog store, guard-rail engine, observability. It knows the state of every agent. The control plane is the product; sandboxes are commodity, runtimes are swappable.

## Data plane

The totality of isolated, ephemeral sandboxes in which agents work. If a sandbox is lost, it is rebuilt from config and home.

## Runtime & daemon

The runtime is the agent framework that drives the LLM loop (first adapter: Claude Code headless). The daemon is the lean process in the sandbox that speaks the unified platform protocol and bootstraps the runtime.

## Backlog

An agent's persistent, prioritized task list. Tasks have states; blocked tasks ask people questions.

## Guard-rail

A centrally defined, platform-enforced boundary on agent behavior (egress rule, forbidden tool, approval requirement). It applies outside the runtime and cannot be bypassed by the agent — fail-closed.

## Secrets broker

The service that issues agents short-lived, scoped access tokens for target systems. No long-lived secrets in the sandbox.`,
        },
      },
      {
        slug: "architektur",
        title: { de: "Architektur-Überblick", en: "Architecture overview" },
        body: {
          de: `# Architektur-Überblick

Covey trennt zwischen der Control Plane (zustandsführend, immer aktiv) und der Data Plane (ephemere Sandboxen).

## Control Plane

Ein einzelnes Go-Binary mit Postgres als Anker. Es vereint Scheduler/Dispatcher, Agent-Registry & Org-Chart, Backlog-Store, Identitäts- & Secrets-Broker, Guard-Rail-/Policy-Engine und Observability.

## Data Plane

Isolierte, ephemere Sandboxen mit persistentem Home. Der Zustand liegt in der Control Plane, die sie orchestriert.

## Daemon-Protokoll

Zwischen Control Plane und Sandbox läuft ein bidirektionales Protokoll (WebSocket). Es ist die stabile Naht der Architektur: Runtimes dahinter sind austauschbar (OpenHands, Harness, Claude Code …), das Protokoll bleibt gleich.

## Postgres als Anker

Ein Datenspeicher trägt fast alles: State, Backlog, RBAC, Queue (\`SELECT … FOR UPDATE SKIP LOCKED\`), Pub/Sub (\`LISTEN/NOTIFY\`), Memory (\`pgvector\`) und AES-GCM-verschlüsselte Secret-Spalten.

## Batteries included, but swappable

Jede Plattform-Fähigkeit hat eine DB-gestützte Built-in-Default und ein schmales Interface für einen externen Provider (z. B. \`IdentityProvider\`: builtin ↔ OIDC; \`SecretStore\`: builtin ↔ Vault). Der Betrieb läuft mit \`builtin\` überall.`,
          en: `# Architecture overview

Covey separates the control plane (stateful, always on) from the data plane (ephemeral sandboxes).

## Control plane

A single Go binary with Postgres as its anchor. It unites scheduler/dispatcher, agent registry & org chart, backlog store, identity & secrets broker, guard-rail/policy engine and observability.

## Data plane

Isolated, ephemeral sandboxes with a persistent home. The state lives in the control plane that orchestrates them.

## Daemon protocol

Between control plane and sandbox runs a bidirectional protocol (WebSocket). It is the stable seam of the architecture: runtimes behind it are swappable (OpenHands, Harness, Claude Code …), the protocol stays the same.

## Postgres as the anchor

One data store carries almost everything: state, backlog, RBAC, queue (\`SELECT … FOR UPDATE SKIP LOCKED\`), pub/sub (\`LISTEN/NOTIFY\`), memory (\`pgvector\`) and AES-GCM-encrypted secret columns.

## Batteries included, but swappable

Every platform capability has a DB-backed built-in default and a thin interface for an external provider (e.g. \`IdentityProvider\`: builtin ↔ OIDC; \`SecretStore\`: builtin ↔ Vault). Operations run with \`builtin\` everywhere.`,
        },
      },
    ],
  },
  {
    id: "erste-schritte",
    title: { de: "Erste Schritte", en: "Getting started" },
    pages: [
      {
        slug: "schnellstart",
        title: { de: "Schnellstart (Docker)", en: "Quick start (Docker)" },
        body: {
          de: `# Schnellstart (Docker)

Covey läuft als einzelnes Binary neben einer Postgres-Datenbank. Für einen lokalen Durchstich genügen Docker und wenige Schritte.

## Voraussetzungen

- Docker (mit Docker-Daemon)
- Ein Master-Key für die Secret-Verschlüsselung

## Ablauf

1. **Datenbank starten** — ein Postgres-Container mit aktivierter \`pgvector\`-Extension.
2. **Migrationen laufen automatisch** beim \`serve\`-Start (Auto-Migrate mit Advisory-Lock).
3. **Bootstrap** legt die erste Organisation und einen Admin-Benutzer an.
4. **Server starten** — die Control Plane bedient API und eingebettete Web-UI auf demselben Port.

\`\`\`
make dev-db      # Postgres bereitstellen
make bootstrap   # Org + Admin anlegen
make run         # Control Plane starten
\`\`\`

## Anmelden

Die Web-UI ist ins Binary eingebettet (kein separater Web-Server). Nach dem Start meldet man sich mit den Bootstrap-Zugangsdaten an und legt den ersten Agenten aus einem Template an.`,
          en: `# Quick start (Docker)

Covey runs as a single binary next to a Postgres database. For a local end-to-end slice, Docker and a few steps are enough.

## Prerequisites

- Docker (with the Docker daemon)
- A master key for secret encryption

## Steps

1. **Start the database** — a Postgres container with the \`pgvector\` extension enabled.
2. **Migrations run automatically** at \`serve\` start (auto-migrate with an advisory lock).
3. **Bootstrap** creates the first organization and an admin user.
4. **Start the server** — the control plane serves the API and the embedded web UI on the same port.

\`\`\`
make dev-db      # provision Postgres
make bootstrap   # create org + admin
make run         # start the control plane
\`\`\`

## Sign in

The web UI is embedded in the binary (no separate web server). After starting, sign in with the bootstrap credentials and create your first agent from a template.`,
        },
      },
      {
        slug: "ersten-agenten",
        title: { de: "Den ersten Agenten anlegen", en: "Create your first agent" },
        body: {
          de: `# Den ersten Agenten anlegen

Einen Agenten anzulegen entspricht dem Onboarding eines Mitarbeiters: Identität, Rolle, Konfiguration und ein Platz im Org-Chart.

## Aus einem Template

Templates bündeln eine Grundkonfiguration (Runtime, Rolle, Werkzeuge). Aus einem Template entsteht mit wenigen Angaben ein einsatzfähiger Agent.

## SOUL.md — Config-as-Code

Das Verhalten eines Agenten ist in seiner \`SOUL.md\` versioniert: Rolle, Auftrag, Leitplanken, Ton. Änderungen laufen über PR/Review, nicht über einen Deploy; der Audit-Trail entsteht dabei automatisch.

## Platz im Org-Chart

Jeder Agent bekommt einen Vorgesetzten (einen Menschen) und optional eine Abteilung. Der Org-Chart strukturiert Eskalation und Delegation.

## Zugänge & Zielsysteme

Statt Secrets in die Sandbox zu legen, weist man dem Agenten gebrokerte Zugänge zu Zielsystemen zu. Die kurzlebigen Tokens stellt der Secrets-Broker zur Laufzeit aus.

## Arbeiten lassen

Der Agent zieht Aufgaben aus dem Backlog und arbeitet sie seriell ab. Wecken/Fortsetzen steuert seinen Lauf; Guard-Rails und Freigaben greifen im Hintergrund.`,
          en: `# Create your first agent

Creating an agent corresponds to onboarding an employee: identity, role, configuration and a place in the org chart.

## From a template

Templates bundle a base configuration (runtime, role, tools). From a template, a ready-to-run agent is created with just a few inputs.

## SOUL.md — config as code

An agent's behavior is versioned in its \`SOUL.md\`: role, mission, guard-rails, tone. Changes go through PR/review, not a deploy; the audit trail is produced automatically.

## A place in the org chart

Every agent gets a supervisor (a human) and optionally a department. The org chart structures escalation and delegation.

## Access & target systems

Instead of putting secrets into the sandbox, you assign the agent brokered access to target systems. The short-lived tokens are issued by the secrets broker at runtime.

## Let it work

The agent pulls tasks from the backlog and works them serially. Wake/resume controls its run; guard-rails and approvals apply in the background.`,
        },
      },
    ],
  },
  {
    id: "konzepte",
    title: { de: "Konzepte", en: "Concepts" },
    pages: [
      {
        slug: "agenten-modell",
        title: { de: "Das Agenten-Modell", en: "The agent model" },
        body: {
          de: `# Das Agenten-Modell

Ein Agent ist mehr als ein Prompt: eine persistente Entität mit Identität, Arbeitsplatz und Verhalten-als-Code.

## Identität

Jeder Agent hat eine eigene Identität in der Plattform. Sie ist die Basis für Zugänge, Audit und die Zuordnung im Org-Chart.

## Sandbox mit persistentem Home

Der Agent arbeitet in einer isolierten, ephemeren Sandbox. Dateien und Arbeitsnotizen liegen im persistenten Home; die Rechenumgebung selbst ist ersetzbar.

## Config-as-Code

Das Verhalten steckt in versionierter Konfiguration (\`SOUL.md\`). Jede Verhaltensänderung ist nachvollziehbar, reviewbar und rückrollbar — wie Code.

## Seriell vor parallel

Ein Agent bearbeitet eine Aufgabe zur Zeit. Parallelität entsteht durch mehr Agenten, nicht durch Nebenläufigkeit innerhalb eines Agenten.

## Immer erreichbar, Compute nur bei Bedarf

„Always-on" ist eine UX-Eigenschaft: ein billiger Dispatch-Loop hält den Agenten erreichbar, teure Compute läuft nur, wenn Arbeit anliegt.`,
          en: `# The agent model

An agent is more than a prompt: a persistent entity with identity, workspace and behavior-as-code.

## Identity

Every agent has its own identity in the platform. It is the basis for access, audit and its position in the org chart.

## Sandbox with a persistent home

The agent works in an isolated, ephemeral sandbox. Files and working notes live in the persistent home; the compute environment itself is replaceable.

## Config as code

Behavior lives in versioned configuration (\`SOUL.md\`). Every behavior change is traceable, reviewable and revertible — like code.

## Serial before parallel

An agent handles one task at a time. Parallelism comes from more agents, not from concurrency within a single agent.

## Always reachable, compute only on demand

"Always-on" is a UX property: a cheap dispatch loop keeps the agent reachable, expensive compute runs only when there is work.`,
        },
      },
      {
        slug: "gedaechtnis",
        title: { de: "Das Wiki-Gedächtnis", en: "The wiki memory" },
        body: {
          de: `# Das Wiki-Gedächtnis

Ein Agent braucht Gedächtnis über Aufgaben hinweg. Covey pflegt es als LLM-gepflegtes Wiki aus verlinkten Markdown-Seiten mit \`pgvector\`-Index.

## Wiki statt Schnipsel-Store

Was einen Agenten kompetent macht, sind die Beziehungen zwischen Entitäten (Kunde → Ticket → Lösung, Kunde → Kollege), nicht bloße Textähnlichkeit. Statt loser Freitext-Schnipsel pflegt der Agent verlinkte Seiten — eine pro Entität — mit \`[[Wikilinks]]\`. Die Verlinkung bildet den Graphen.

## Drei Operationen

- **Ingest** (im \`done\`-Schritt): Erkenntnisse der richtigen Seite zuordnen.
- **Query** (im \`triage\`-Schritt): Vektorsuche liefert relevante Seiten; dazu der kompakte Index des gesamten Wikis.
- **Konsolidierung**: ein getakteter Wartungs-Job mergt Duplikate, sodass sich das Wissen verdichtet statt nur zu wachsen.

## Hybride Speicherung

Die Seiten leben zweifach: als \`.md\`-Dateien im Sandbox-Home und als Quelle der Wahrheit in der Control Plane.

## Auch für Menschen

Dasselbe Modell trägt das Gedächtnis menschlicher Mitarbeiter: die Companion-App gibt dem Menschen denselben Apparat, nur mit \`human_id\` statt \`agent_id\` als Owner.`,
          en: `# The wiki memory

An agent needs memory across tasks. Covey keeps it as an LLM-maintained wiki of linked Markdown pages with a \`pgvector\` index.

## Wiki instead of a snippet store

What makes an agent competent are the relationships between entities (customer → ticket → solution, customer → colleague), not mere text similarity. Instead of loose free-text snippets, the agent maintains linked pages — one per entity — with \`[[wikilinks]]\`. The links form the graph.

## Three operations

- **Ingest** (in the \`done\` step): file insights onto the right page.
- **Query** (in the \`triage\` step): vector search returns relevant pages, plus the compact index of the whole wiki.
- **Consolidation**: a scheduled maintenance job merges duplicates, so knowledge condenses instead of only growing.

## Hybrid storage

Pages live twice: as \`.md\` files in the sandbox home and as the source of truth in the control plane.

## For humans too

The same model carries the memory of human employees: the Companion app gives a person the same apparatus, only with \`human_id\` instead of \`agent_id\` as the owner.`,
        },
      },
      {
        slug: "guard-rails",
        title: { de: "Guard-Rails & Kontrolle", en: "Guard-rails & control" },
        body: {
          de: `# Guard-Rails & Kontrolle

Nachvollziehbarkeit, Freigaben und ein Kill-Switch sind Voraussetzung für die Adoption. Observability ist kein Add-on, sondern Grundvoraussetzung.

## Zentral erzwungen, fail-closed

Harte Grenzen werden nicht dem Agenten-Prompt überlassen (ein Prompt lässt sich umgehen oder injizieren), sondern zentral definiert und außerhalb der Runtime durchgesetzt. Im Zweifel wird blockiert.

## Enforcement-Punkte

- **Secrets-Broker** — welche Zugänge, wie lange, für welche Aufgabe
- **Egress** — wohin die Sandbox überhaupt sprechen darf
- **Tool-Layer** — welche Aktionen erlaubt sind und welche eine Freigabe brauchen

## Freigaben (Approval-Gates)

Kritische Aktionen warten auf eine menschliche Freigabe, bevor sie ausgeführt werden. Der Mensch bleibt in der Schleife.

## Recording, Kosten, Kill-Switch

Jede Sitzung wird aufgezeichnet — Schritt für Schritt, mit Kosten. Anomalien lösen Alerts aus; im Ernstfall stoppt ein Kill-Switch den Agenten sofort.`,
          en: `# Guard-rails & control

Traceability, approvals and a kill switch are a precondition for adoption. Observability is not an add-on but a precondition.

## Enforced centrally, fail-closed

Hard limits are not left to the agent prompt (a prompt can be bypassed or injected), but defined centrally and enforced outside the runtime. When in doubt, it blocks.

## Enforcement points

- **Secrets broker** — which access, for how long, for which task
- **Egress** — where the sandbox may talk at all
- **Tool layer** — which actions are allowed and which need approval

## Approvals (approval gates)

Critical actions wait for a human approval before they run. The human stays in the loop.

## Recording, cost, kill switch

Every session is recorded — step by step, with cost. Anomalies raise alerts; in an emergency a kill switch stops the agent immediately.`,
        },
      },
      {
        slug: "identitaet-secrets",
        title: { de: "Identität & Secrets", en: "Identity & secrets" },
        body: {
          de: `# Identität & Secrets

Eine der tragenden Leitplanken: keine langlebigen Secrets in der Sandbox.

## Zugriff zur Laufzeit brokern

Statt einem Agenten ein dauerhaftes Passwort mitzugeben, stellt der Secrets-Broker kurzlebige, gescopte Access-Tokens aus — für die Aufgabe, für ihre Dauer.

## Krypto-Primitive ja, Protokolle nein

Covey signiert JWTs, verschlüsselt mit AES-GCM und hasht Passwörter mit Argon2id — mit bewährten Libraries. Es baut aber keinen eigenen OAuth/OIDC-Server nach; das übernimmt ein externer Provider.

## Pluggable Ports

- **IdentityProvider** — builtin (JWT/Argon2id) ↔ OIDC
- **SecretStore** — builtin (AES-GCM-Spalten in Postgres) ↔ Vault

## Menschliche Rollen & RBAC

Menschen authentifizieren sich (per SSO möglich) und tragen fein gescopte Rollen: Platform-Admin, Agent-Owner, Security/Compliance, Auditor, Controlling. Jede Aktion ist org-scoped.`,
          en: `# Identity & secrets

One of the load-bearing guard-rails: no long-lived secrets in the sandbox.

## Broker access at runtime

Instead of giving an agent a permanent password, the secrets broker issues short-lived, scoped access tokens — for the task, for its duration.

## Crypto primitives yes, protocols no

Covey signs JWTs, encrypts with AES-GCM and hashes passwords with Argon2id — using proven libraries. But it does not rebuild an OAuth/OIDC server; an external provider does that.

## Pluggable ports

- **IdentityProvider** — builtin (JWT/Argon2id) ↔ OIDC
- **SecretStore** — builtin (AES-GCM columns in Postgres) ↔ Vault

## Human roles & RBAC

People authenticate (SSO possible) and carry finely scoped roles: platform admin, agent owner, security/compliance, auditor, controlling. Every action is org-scoped.`,
        },
      },
      {
        slug: "backlog-lifecycle",
        title: { de: "Backlog & Lifecycle", en: "Backlog & lifecycle" },
        body: {
          de: `# Backlog & Lifecycle

Agenten arbeiten wie ein Team: sie ziehen Aufgaben aus einem priorisierten Backlog und durchlaufen dabei eine klare Zustandsmaschine.

## Backlog als First-Class-Objekt

Das Backlog ist ein persistenter Speicher mit Prioritäten und Zuständen. Aufgaben entstehen aus verschiedenen Quellen (manuell, Heartbeat/Tick, Webhook eines Zielsystems).

## Der Dispatch-Loop

Ein billiger, dauerhaft laufender Orchestrierungs-Loop (kein LLM) verarbeitet Wake-Events pro Agent. Ein **Tick** ist ein periodischer „Was liegt an?"-Impuls.

## Triage → Working → Done

- **triage** — Kontext sammeln, Gedächtnis abfragen
- **working** — die Aufgabe bearbeiten, Werkzeuge nutzen
- **done** — Ergebnis festhalten, Gelerntes ins Wiki

## Blocked & Rückfragen

Fehlt eine Information oder eine Freigabe, geht die Aufgabe in **blocked** — mit einer Rückfrage an einen Menschen. Kommt die Antwort, läuft der Agent an derselben Stelle weiter (Event-Korrelation).`,
          en: `# Backlog & lifecycle

Agents work like a team: they pull tasks from a prioritized backlog and pass through a clear state machine.

## Backlog as a first-class object

The backlog is a persistent store with priorities and states. Tasks arise from various sources (manual, heartbeat/tick, a target system's webhook).

## The dispatch loop

A cheap, always-running orchestration loop (no LLM) processes wake events per agent. A **tick** is a periodic "what's up?" impulse.

## Triage → working → done

- **triage** — gather context, query memory
- **working** — do the task, use tools
- **done** — record the result, learnings into the wiki

## Blocked & follow-up questions

If information or an approval is missing, the task goes **blocked** — with a question to a human. When the answer arrives, the agent continues at the same point (event correlation).`,
        },
      },
    ],
  },
  {
    id: "integrationen",
    title: { de: "Integrationen & Betrieb", en: "Integrations & operations" },
    pages: [
      {
        slug: "zielsysteme",
        title: { de: "Zielsysteme & Plugins", en: "Target systems & plugins" },
        body: {
          de: `# Zielsysteme & Plugins

Zielsysteme sind die Systeme, in denen Agenten arbeiten — Ticketsysteme, Code-Hosting, Postfächer. Sie docken als selbstregistrierende Plugins an, nicht als hartkodierte Sonderfälle.

## Das Plugin-Muster

Jedes Zielsystem bringt ein Manifest mit: welche Aktionen es anbietet, welche Zugänge es braucht, wie es Agenten weckt (Trigger/Webhook). Neue Integrationen kommen hinzu, ohne den Kern zu ändern.

## Verfügbare Zielsysteme

- **Zammad** — das erste Built-in: Wecken via Trigger/Webhook, REST-Aktionen, \`blocked\` ↔ \`pending\`, Korrelation über die Ticket-ID.
- **GitLab** — Repository-Arbeit: Branches, Merge Requests, Pipelines.
- **E-Mail / IMAP** — Postfächer als Zielsystem: Mails lesen und beantworten.
- **MCP** — Anbindung von Werkzeugen über das Model-Context-Protocol.

## Broker-Token statt Dauer-Login

Der Zugriff auf ein Zielsystem läuft immer über den Secrets-Broker: kurzlebig und auf die Aufgabe gescopt.`,
          en: `# Target systems & plugins

Target systems are the systems agents work in — ticket systems, code hosting, mailboxes. They dock in as self-registering plugins, not hard-coded special cases.

## The plugin pattern

Every target system brings a manifest: which actions it offers, which access it needs, how it wakes agents (trigger/webhook). New integrations are added without touching the core.

## Available target systems

- **Zammad** — the first built-in: waking via trigger/webhook, REST actions, \`blocked\` ↔ \`pending\`, correlation via the ticket ID.
- **GitLab** — repository work: branches, merge requests, pipelines.
- **Email / IMAP** — mailboxes as a target system: read and answer mail.
- **MCP** — connecting tools via the Model Context Protocol.

## Broker token instead of a permanent login

Access to a target system always goes through the secrets broker: short-lived and scoped to the task.`,
        },
      },
      {
        slug: "betrieb",
        title: { de: "Betrieb & Deployment", en: "Operations & deployment" },
        body: {
          de: `# Betrieb & Deployment

Covey ist schlank zu betreiben: ein Binary, eine Datenbank, optional Sandbox-Infrastruktur.

## Bausteine

- **Control Plane** — das \`covey\`-Binary (API/BFF + Orchestrierung + eingebettete Web-UI).
- **Sandbox-Daemon** — ein kleines zweites Binary (\`coveyd\`), das in der Sandbox läuft.
- **Postgres** — der Anker für State, Queue, Memory und verschlüsselte Secrets.

## Migrationen

Versionierte SQL-Migrationen sind ins Binary eingebettet und laufen beim \`serve\`-Start automatisch (Auto-Migrate mit \`pg_advisory_lock\`). Bestehende Migrationen werden nie editiert.

## Konfiguration

Der Betrieb wird über Umgebungsvariablen gesteuert (Datenbank-URL, Listen-Adresse, öffentliche URL, Master-Key). Der Master-Key ist kritisch — ohne ihn lassen sich verschlüsselte Secrets nicht entschlüsseln.

## Egress-Kontrolle

Der Netzwerkausgang der Sandboxen ist kontrollierbar: wohin ein Agent sprechen darf, entscheidet die Plattform. Ein zentraler Enforcement-Punkt gegen Datenabfluss.

> Detaillierte Betriebsleitfäden (Deployment, GitLab, E-Mail, Zammad) liegen im Repository unter \`docs/\`.`,
          en: `# Operations & deployment

Covey is lean to operate: one binary, one database, optional sandbox infrastructure.

## Building blocks

- **Control plane** — the \`covey\` binary (API/BFF + orchestration + embedded web UI).
- **Sandbox daemon** — a small second binary (\`coveyd\`) that runs in the sandbox.
- **Postgres** — the anchor for state, queue, memory and encrypted secrets.

## Migrations

Versioned SQL migrations are embedded in the binary and run automatically at \`serve\` start (auto-migrate with \`pg_advisory_lock\`). Existing migrations are never edited.

## Configuration

Operation is controlled via environment variables (database URL, listen address, public URL, master key). The master key is critical — without it, encrypted secrets cannot be decrypted.

## Egress control

The sandboxes' network egress is controllable: where an agent may talk is decided by the platform. A central enforcement point against data exfiltration.

> Detailed operations guides (deployment, GitLab, email, Zammad) live in the repository under \`docs/\`.`,
        },
      },
    ],
  },
  {
    id: "companion",
    title: { de: "Companion (Ausblick)", en: "Companion (outlook)" },
    pages: [
      {
        slug: "companion",
        title: { de: "Die Companion-App", en: "The Companion app" },
        body: {
          de: `# Die Companion-App

> **In Entwicklung.** Das Konzept steht, der Bau folgt. Fundament ist Coveys Gedächtnis-Infrastruktur.

Die Companion ist eine eigene, optionale App: ein Ort, an dem ein Mensch seinen Brain-Load ablädt — Ideen per Sprache, Notizen, Dokumente — und ihn als Kontext für seine Agenten verfügbar macht.

## Zweck: Kontinuität

Der Zweck ist nicht der persönliche „zweite Kopf", sondern Team-Kontinuität: Vertretung, Urlaub, Onboarding. Freigegebenes Rollen-Wissen lässt Kolleg:innen und Agenten übernehmen, wenn jemand ausfällt.

## Ablauf: abladen → kuratieren → füttern

1. **Abladen** — unterwegs erfassen (voice-first).
2. **Kuratieren** — ein Kurator-Agent verdichtet das Rohe zu verlinkten Wiki-Seiten, erkennt Lücken und fragt nach.
3. **Füttern** — freigegebenes Wissen fließt, zentral durchgesetzt, als Kontext in die Aufgaben der Agenten.

## Datenschutz

Der Brain-Dump gehört dem Menschen. Privates bleibt privat vor anderen Menschen; eine Freigabe an die Abteilung ist eine bewusste Entscheidung, gesteuert vom Team-Lead. Es ist kein Überwachungswerkzeug.

## Scopes

Sichtbarkeit ist gestaffelt: \`privat\` → \`abteilung\` → \`org\`. Der Team-Lead steuert das Geteilte, nicht das Private.`,
          en: `# The Companion app

> **In development.** The concept stands, the build follows. Its foundation is Covey's memory infrastructure.

The Companion is a separate, optional app: a place where a person offloads their brain-load — ideas by voice, notes, documents — and makes it available as context for their agents.

## Purpose: continuity

The purpose is not the personal "second brain" but team continuity: cover, vacation, onboarding. Shared role knowledge lets colleagues and agents take over when someone is out.

## Flow: offload → curate → feed

1. **Offload** — capture on the go (voice-first).
2. **Curate** — a curator agent distills the raw into linked wiki pages, spots gaps and asks back.
3. **Feed** — shared knowledge flows, enforced centrally, as context into the agents' tasks.

## Privacy

The brain-dump belongs to the person. Private stays private from other people; sharing to the department is a deliberate choice, governed by the team lead. It is not a surveillance tool.

## Scopes

Visibility is tiered: \`private\` → \`department\` → \`org\`. The team lead governs the shared layer, not the private one.`,
        },
      },
    ],
  },
];

export const DOC_PAGES: DocPage[] = DOC_SECTIONS.flatMap((s) => s.pages);
export const FIRST_DOC = DOC_PAGES[0];
export const docLang = (l: string): Lang => (l.startsWith("en") ? "en" : "de");
