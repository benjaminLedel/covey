# Covey — Spezifikation

> **Codename: Covey.** Ein „covey" ist ein kleiner, koordinierter Schwarm — eine abgestimmte Gruppe, die zusammen unterwegs ist. Genau das ist die Plattform: viele Agenten, zentral orchestriert.

Eine zentrale Plattform, die KI-Agenten wie Mitarbeiter behandelt — mit Identität, Arbeitsplatz, Zugängen, Backlog und Vorgesetztem — und dem IT-Admin die Werkzeuge gibt, sie zu führen und zu überwachen.

**Coveys Einheit ist die Organisation, nicht der einzelne Nutzer.** Das ist die tragende Abgrenzung zu den Single-User-„AI-Employee"-Apps: Covey ist die Plattform, die ein *Unternehmen* betreibt, um seine gesamte Agenten-Belegschaft zu verwalten und zu governen — mit vielen menschlichen Stakeholdern (IT, Team-Leads, Security/Compliance, Audit, Controlling), zentraler Governance und unternehmensweitem Org-Chart. Agenten sind organisationseigene Ressourcen, keine persönlichen Assistenten. Details in [`09-enterprise-modell.md`](09-enterprise-modell.md).

Die Leitmetapher, aus der die gesamte Architektur folgt: Die Plattform ist die **IT- und HR-Abteilung für KI-Agenten**. Fast jede Komponente hat ein Gegenstück im echten Unternehmen, und genau daraus ergibt sich der Bauplan — überall lässt sich bewährte Prior Art übernehmen statt neu zu erfinden.

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

## Dokumente

| Datei | Inhalt |
|---|---|
| [`01-architektur.md`](01-architektur.md) | System-Übersicht, Control Plane vs. Data Plane, Runtime-Abstraktion, Daemon-Protokoll |
| [`02-agenten-modell.md`](02-agenten-modell.md) | Der Agent als Entität: Identität, Sandbox, Zugänge, Config-as-Code, Org-Chart |
| [`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md) | Zustandsmaschine, Dispatch-Loop, Wake-Quellen, Backlog, Blocking, Event-Korrelation |
| [`04-identitaet-secrets.md`](04-identitaet-secrets.md) | Keycloak, RFC 8693 Token Exchange, Secrets-Broker, Threat-Model |
| [`05-gedaechtnis.md`](05-gedaechtnis.md) | Memory-Schichten, Knowledge-Graph (Graphiti), persistentes Home |
| [`06-observability-control.md`](06-observability-control.md) | Zentrale Guard-Rails, Session-Recording, Approval-Gates, Kill-Switch, Kostenkontrolle, Supervisor-Agent |
| [`07-offene-entscheidungen.md`](07-offene-entscheidungen.md) | Offene Fragen, Build-vs-Buy, MVP-Scope |
| [`08-marktumfeld.md`](08-marktumfeld.md) | Marktrecherche: Konkurrenz-Plattformen, Open-Source-Bausteine, Build-vs-Adopt-Matrix |
| [`09-enterprise-modell.md`](09-enterprise-modell.md) | Organisation als Einheit: menschliche Rollen & RBAC, SSO, Mandanten, Cost-Center, Compliance |
| [`10-architektur-stack.md`](10-architektur-stack.md) | Frontend, Backend-Sprache (Go/Kotlin), „Batteries included, but swappable", Pluggable-Interfaces, Postgres-Anker |
| [`11-mvp-plan.md`](11-mvp-plan.md) | Bau-Reihenfolge: Meilensteine M0–M7, kritischer Pfad, Abnahme-Checkliste |
| [`12-claude-code-adapter.md`](12-claude-code-adapter.md) | Erster Runtime-Adapter: Steuerung von Claude Code headless via `claude -p`, Flag-Mapping, `blocked`↔`--resume` |
| [`13-zammad-integration.md`](13-zammad-integration.md) | MVP-Zielsystem Zammad: Wake via Trigger/Webhook, REST-Aktionen, Broker-Token, `blocked`↔`pending`, Korrelation via Ticket-ID |

## Designprinzipien

1. **Organisation als Einheit, nicht der Nutzer.** Covey ist eine Enterprise-Plattform: org-eigene Agenten, mehrere menschliche Rollen mit RBAC, zentrale Governance, unternehmensweiter Org-Chart. Kein Single-User-Produktivitäts-Tool.
2. **Die Control Plane ist das Produkt.** Sandboxen sind Commodity, Runtimes austauschbar. Der Wert liegt in Scheduling, Identität, Governance und Observability — der Schicht, die sonst niemand baut.
3. **Runtime-agnostisch.** Die Plattform managt nicht das Framework, sondern die Sandbox. In der Sandbox läuft ein schlanker Daemon mit einheitlichem Protokoll; OpenHands, Harness, Claude Code & Co. sind dahinter austauschbar.
4. **Immer erreichbar, Compute nur bei Bedarf.** „Always-on" ist eine UX-Eigenschaft, keine Runtime-Eigenschaft. Idle muss wirklich idle sein, sonst skaliert die Rechnung weg.
5. **Config-as-Code.** Agentenverhalten ist versioniert in Git. Änderungen laufen über PR/Review, nicht über Deploy. Audit fällt gratis ab.
6. **Niemals langlebige Secrets in die Sandbox.** Zugriff wird zur Laufzeit gebrokert, kurzlebig und gescopt.
7. **Guard-Rails zentral und plattform-erzwungen.** Harte Grenzen werden nicht dem Agenten überlassen (ein Prompt lässt sich umgehen oder injizieren), sondern zentral definiert und außerhalb der Runtime durchgesetzt — am Broker, am Egress, im Tool-Layer. Fail-closed.
8. **Trust by design.** Ohne lückenlose Nachvollziehbarkeit, Freigaben und einen Kill-Switch gibt es keine Adoption. Observability ist kein Add-on, sondern Grundvoraussetzung.
9. **Seriell vor parallel.** Ein Agent bearbeitet eine Aufgabe zur Zeit. Parallelität ist eine Frage von mehr Agenten, nicht von Nebenläufigkeit innerhalb eines Agenten.
10. **Batteries included, but swappable.** Jede Plattform-Fähigkeit (IdM, Secrets, Queue, Observability) hat eine simple, DB-gestützte Built-in-Default und ein schmales Interface für einen externen Provider. Der MVP läuft mit `builtin` überall — Binary + Postgres + Sandbox-Infra; Keycloak/Vault/Redis/Langfuse sind optional zuschaltbar, nicht Voraussetzung.

## Glossar

- **Agent** — Eine konfigurierte, persistente Entität mit Identität, Sandbox, Zugängen und Backlog. Das Gegenstück zum Mitarbeiter. Eine eigene E-Mail-Adresse ist optional, nicht zwingend.
- **Guard-Rail** — Eine zentral definierte, plattformseitig erzwungene Grenze für Agentenverhalten (z. B. Egress-Regel, verbotenes System/Tool, Approval-Pflicht). Greift außerhalb der Runtime und ist vom Agenten nicht umgehbar.
- **Runtime** — Das Agent-Framework, das die eigentliche LLM-Schleife fährt (OpenHands, Harness, Claude Code …). Austauschbar.
- **Daemon** — Der schlanke Prozess in der Sandbox, der das einheitliche Plattform-Protokoll spricht und die Runtime bootstrappt.
- **Control Plane** — Der zentrale Dienst: Scheduler, Identitäts-Broker, Backlog-Store, Observability. Kennt den Zustand aller Agenten.
- **Data Plane** — Die Gesamtheit der Sandboxen, in denen Agenten tatsächlich arbeiten.
- **Dispatch-Loop** — Der billige, dauerhaft laufende Orchestrierungs-Loop pro Agent (kein LLM), der Wake-Events verarbeitet.
- **Tick** — Ein periodischer „Was liegt an?"-Impuls, der einen Agenten proaktiv macht.
- **Backlog** — Die persistente, priorisierte Aufgabenliste eines Agenten.
- **Secrets-Broker** — Der Dienst, der Agenten kurzlebige, gescopte Access-Tokens für Zielsysteme ausstellt.
- **Supervisor-Agent** — Ein optionaler Agent, der die Aktivität anderer Agenten reviewt und Anomalien flaggt.
- **Enforcement-Punkt** — Eine Stelle, an der die Plattform ohnehin im Datenfluss sitzt (Broker, Egress, Tool-Layer) und an der Guard-Rails technisch durchgesetzt werden.
- **Organisation / Tenant** — Die Einheit, für die eine Covey-Instanz betrieben wird. Alle Agenten, Rollen, Guard-Rails, Budgets und Audits sind org-scoped.
- **Menschliche Rolle** — Ein Mensch mit definierten Rechten auf der Plattform (z. B. Platform-Admin, Agent-Owner, Security/Compliance, Auditor, Controlling). Über RBAC gesteuert, per SSO authentifiziert.
- **Agent-Owner** — Der Mensch (meist Team-Lead einer Abteilung), der einen bestimmten Agenten verantwortet: dessen Config, Backlog-Priorität, Freigaben.
