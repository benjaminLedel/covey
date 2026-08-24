/* Inhalt des Docs-Bereichs der öffentlichen Website — zweisprachig (de/en).
   Rendering über die Markdown-Komponente (components/Markdown.tsx). Neue Seiten
   hier ergänzen; die Navigation baut sich aus DOC_SECTIONS auf. Titel und Body
   liegen je Sprache vor; Docs.tsx wählt anhand der Sprache in der URL.

   Drei Felder tragen dabei nicht die Seite, sondern ihre Auffindbarkeit:

   - `slug` liegt je Sprache vor. Vorher trug die englische Fassung den
     deutschen Slug (/en/docs/gedaechtnis), und eine URL ist das Erste, was
     jemand von einer Seite sieht — der Leser wie der Index.
   - `description` ist von Hand geschrieben. Automatisch aus der ersten Zeile
     geschnitten (descriptionFromMarkdown) ist sie ein Zufallstreffer; sie
     steht aber als einziger Fließtext der Seite im Suchergebnis.
   - `faq` sind echte Fragen mit echten Antworten, gerendert UND als
     strukturierte Daten ausgezeichnet. Beides aus einer Quelle, weil eine
     Auszeichnung, die etwas anderes behauptet als die sichtbare Seite, gegen
     die Regeln jeder Suchmaschine verstößt — und zu Recht. */

export type Lang = "de" | "en";
export type Localized = Record<Lang, string>;
export type Faq = { q: string; a: string };
export type DocPage = {
  slug: Localized;
  title: Localized;
  description: Localized;
  body: Localized;
  faq?: Record<Lang, Faq[]>;
};
export type DocSection = { id: string; title: Localized; pages: DocPage[] };

export const DOC_SECTIONS: DocSection[] = [
  {
    id: "einfuehrung",
    title: { de: "Einführung", en: "Introduction" },
    pages: [
      {
        slug: { de: "was-ist-covey", en: "what-is-covey" },
        title: { de: "Was ist Covey?", en: "What is Covey?" },
        description: {
          de: "Covey führt KI-Agenten wie Mitarbeiter: eigene Identität, isolierte Sandbox, gebrokerte Zugänge, Backlog und Org-Chart. Ein Go-Binary, eine Postgres-Datenbank, AGPL-3.0.",
          en: "Covey runs AI agents like employees: own identity, isolated sandbox, brokered access, backlog and org chart. One Go binary, one Postgres database, AGPL-3.0.",
        },
        body: {
          de: `# Was ist Covey?

Covey führt KI-Agenten wie Mitarbeiter: Jeder Agent hat eine Identität, einen isolierten Arbeitsplatz, gebrokerte Zugänge zu Zielsystemen, ein Backlog und einen Platz im Org-Chart. Die Leitmetapher ist die IT- und HR-Abteilung für KI-Agenten — und sie ist wörtlich gemeint, bis in den Aufbau der Software hinein.

Technisch ist Covey **ein Go-Binary neben einer Postgres-Datenbank**. Die Admin-Oberfläche ist einkompiliert, die Migrationen auch. Kein nginx davor, kein separates Frontend-Hosting, kein Message-Broker daneben. Die Lizenz ist AGPL-3.0; man betreibt es selbst, auf der eigenen Maschine, mit den eigenen Schlüsseln.

## Wofür Covey gebaut ist

Für den Fall, dass ein Unternehmen mehr als einen Agenten betreibt und die Frage aufkommt, wer eigentlich zugesehen hat. Also: Wer hat dem Agenten diesen Zugang gegeben, was hat er damit letzte Woche getan, was hat es gekostet, und wer hält ihn an, wenn er Unsinn macht.

Das ist der Unterschied zu einem Agenten-Framework. Ein Framework hilft beim Bauen des Agenten. Covey verwaltet den fertigen Agenten im Betrieb — und ist deshalb gegenüber dem Framework austauschbar: die Runtime hängt an einem dünnen Adapter, der erste ist [Claude Code headless](/docs/architektur).

## Die Organisation ist die Einheit, nicht der Nutzer

Covey gehört keiner Person. Die Einheit ist die Organisation, und daran hängt alles Weitere: Agenten sind organisationseigene Ressourcen, ihre Secrets liegen im Tresor der Organisation, ihre Kosten laufen auf deren Konto, und mehrere Menschen mit verschiedenen Rollen schauen auf dieselbe Belegschaft — IT, Team-Lead, Security, Audit, Controlling.

Das ist die tragende Unterscheidung zu Single-User-Apps, die einen persönlichen „KI-Mitarbeiter" versprechen. Deren Modell endet an der Stelle, an der die zweite Person mitreden muss.

## Entsprechungen im Unternehmen

Fast jede Komponente hat ein Gegenstück, das jeder aus dem Arbeitsalltag kennt. Wer die linke Spalte kennt, kann die rechte lesen:

- Identität / Active Directory → Agent-Identität, optional mit eigener E-Mail-Adresse
- Arbeitsplatz / PC → isolierte Sandbox mit persistentem \`/home\`
- Onboarding / Rollenbeschreibung → \`SOUL.md\`, versioniert wie Code
- Passwort-Tresor / PAM → Secrets-Broker, kurzlebige Tokens zur Laufzeit
- Aufgabenliste / Ticket → Backlog als eigenständiges Objekt
- Betriebshandbuch / Compliance → zentrale Guard-Rails, außerhalb des Prompts erzwungen
- SIEM / EDR → Session-Recording, Kosten je Lauf, Not-Aus für die ganze Organisation

## Was Covey nicht ist

Kein Modell und kein Modell-Anbieter — Covey bringt das eigene Konto mit (Anthropic-API-Schlüssel oder Abo-Token). Kein Chat-Fenster; die Arbeit kommt aus einem Backlog, einem Webhook oder einem Heartbeat, nicht aus einer Unterhaltung. Und keine Cloud: Es gibt eine öffentliche Instanz zum Anschauen, aber der Normalfall ist die eigene.

## Weiter

- [Kernkonzepte](/docs/kernkonzepte) — die Begriffe, die überall wiederkehren
- [Architektur-Überblick](/docs/architektur) — Control Plane, Data Plane, Daemon-Protokoll
- [Schnellstart (Docker)](/docs/schnellstart) — die Installation in fünf Zeilen`,
          en: `# What is Covey?

Covey runs AI agents like employees: every agent has an identity, an isolated workplace, brokered access to target systems, a backlog and a place in the org chart. The guiding metaphor is the IT and HR department for AI agents — and it is meant literally, right down to how the software is built.

Technically, Covey is **one Go binary next to a Postgres database**. The admin interface is compiled in, so are the migrations. No nginx in front, no separate frontend hosting, no message broker on the side. The licence is AGPL-3.0; you run it yourself, on your own machine, with your own keys.

## What Covey is for

For the moment a company runs more than one agent and somebody asks who has actually been watching. That is: who gave this agent that access, what did it do with it last week, what did it cost, and who stops it when it goes wrong.

That is the difference from an agent framework. A framework helps you build the agent. Covey manages the finished agent in operation — which is why the framework stays replaceable: the runtime hangs off a thin adapter, and the first one is [Claude Code headless](/en/docs/architecture).

## The organisation is the unit, not the user

Covey belongs to no single person. The unit is the organisation, and everything else follows from that: agents are organisation-owned resources, their secrets live in the organisation's vault, their cost lands on its account, and several people in different roles look at the same workforce — IT, team lead, security, audit, controlling.

This is the load-bearing distinction from single-user apps that promise a personal "AI employee". Their model ends where the second person needs a say.

## Counterparts in the company

Nearly every component has a counterpart everyone knows from working life. If you know the left-hand column, you can read the right-hand one:

- Identity / Active Directory → agent identity, optionally with its own email address
- Workstation / PC → isolated sandbox with a persistent \`/home\`
- Onboarding / role description → \`SOUL.md\`, versioned like code
- Password vault / PAM → secrets broker, short-lived tokens at runtime
- Task list / ticket → backlog as an object in its own right
- Operations manual / compliance → central guard-rails, enforced outside the prompt
- SIEM / EDR → session recording, cost per run, an emergency stop for the whole organisation

## What Covey is not

Not a model and not a model vendor — Covey brings your own account (an Anthropic API key or a subscription token). Not a chat window; work arrives from a backlog, a webhook or a heartbeat, not from a conversation. And not a cloud: there is a public instance to look at, but the normal case is your own.

## Next

- [Core concepts](/en/docs/core-concepts) — the terms that recur everywhere
- [Architecture overview](/en/docs/architecture) — control plane, data plane, daemon protocol
- [Quick start (Docker)](/en/docs/quickstart) — the install in five lines`,
        },
        faq: {
          de: [
            {
              q: "Braucht Covey eine Cloud oder ein Konto bei Ihnen?",
              a: "Nein. Covey wird selbst gehostet: ein Binary, eine Postgres-Datenbank, Docker für die Sandboxen. Ein Konto brauchen Sie nur beim Modell-Anbieter — Anthropic-API-Schlüssel oder Abo-Token, beides hinterlegen Sie in Ihrer eigenen Instanz.",
            },
            {
              q: "Was unterscheidet Covey von einem Agenten-Framework wie LangChain oder CrewAI?",
              a: "Ein Framework baut den Agenten, Covey betreibt ihn. Identität, Zugänge, Backlog, Guard-Rails, Recording und Kosten sind Betriebsfragen und liegen außerhalb der Runtime — deshalb bleibt die Runtime austauschbar. Der erste Adapter ist Claude Code headless.",
            },
            {
              q: "Welche Lizenz hat Covey?",
              a: "AGPL-3.0. Selbst betreiben, ändern und weitergeben ist erlaubt. Die eine Pflicht, die praktisch zählt: Wer eine geänderte Fassung über ein Netz anderen anbietet, muss diesen Nutzern den geänderten Quelltext zu denselben Bedingungen zugänglich machen. Covey im eigenen Haus zu betreiben löst nichts davon aus.",
            },
            {
              q: "Kann ein Agent auch ohne Zielsystem arbeiten?",
              a: "Ja. Nach der Installation steht ein Demo-Agent bereit, der ohne angebundenes System auskommt — er arbeitet am Aufgabentext und schreibt sein Ergebnis zurück. Das reicht, um einen vollständigen Lauf mit Recording und Kosten zu sehen.",
            },
          ],
          en: [
            {
              q: "Does Covey need a cloud, or an account with you?",
              a: "No. Covey is self-hosted: one binary, one Postgres database, Docker for the sandboxes. The only account you need is with the model provider — an Anthropic API key or a subscription token, both deposited in your own instance.",
            },
            {
              q: "How is Covey different from an agent framework like LangChain or CrewAI?",
              a: "A framework builds the agent, Covey operates it. Identity, access, backlog, guard-rails, recording and cost are operational questions and live outside the runtime — which is what keeps the runtime replaceable. The first adapter is Claude Code headless.",
            },
            {
              q: "What licence is Covey under?",
              a: "AGPL-3.0. You may run it, modify it and pass it on. The one obligation that matters in practice: if you offer a modified version to others over a network, those users must be able to get your modified source under the same terms. Running Covey inside your own organisation triggers none of that.",
            },
            {
              q: "Can an agent work without a target system?",
              a: "Yes. After installation a demo agent is waiting that needs no connected system — it works on the task text and writes its result back. That is enough to see a full run including recording and cost.",
            },
          ],
        },
      },
      {
        slug: { de: "kernkonzepte", en: "core-concepts" },
        title: { de: "Kernkonzepte", en: "Core concepts" },
        description: {
          de: "Das Glossar der Plattform: Agent, Control Plane, Data Plane, Runtime, Backlog, Guard-Rail, Secrets-Broker, Arbeitsplatz und Wiki-Gedächtnis — kurz erklärt.",
          en: "The platform's glossary: agent, control plane, data plane, runtime, backlog, guard-rail, secrets broker, workplace and wiki memory — briefly explained.",
        },
        body: {
          de: `# Kernkonzepte

Zwölf Begriffe, die in der Oberfläche, in der Spezifikation und in diesen Seiten immer wieder auftauchen. Wer sie einmal liest, versteht den Rest ohne Rückfragen.

## Agent

Eine konfigurierte, dauerhaft bestehende Entität mit Identität, Sandbox, Zugängen und Backlog — das Gegenstück zum Mitarbeiter. Ein Agent ist kein Prozess, der läuft: Der Normalzustand ist \`sleeping\`. Geweckt wird er von einer Aufgabe, einem Webhook oder einem Heartbeat, arbeitet seriell und schläft wieder ein. Eine eigene E-Mail-Adresse ist optional.

## Control Plane

Der zentrale, zustandsführende Dienst — ein Go-Binary: Scheduler und Dispatcher, Agent-Registry, Org-Chart, Backlog-Store, Identitäts- und Secrets-Broker, Guard-Rail-Engine, Observability. Sie kennt den Zustand jedes Agenten. **Die Control Plane ist das Produkt**; Sandboxen sind Verbrauchsmaterial, Runtimes austauschbar.

## Data Plane

Die Gesamtheit der Sandboxen, in denen tatsächlich gearbeitet wird. Sie sind bewusst dumm und ersetzbar: Geht eine verloren, wird sie aus Config und Home neu gebaut. Persistent ist nur das Home des Agenten.

## Runtime und Daemon

Die **Runtime** ist das Agenten-Framework, das die Modell-Schleife fährt — erster Adapter ist Claude Code headless über \`claude -p\`. Der **Daemon** (\`coveyd\`) ist der schlanke Prozess in der Sandbox, der das Plattform-Protokoll spricht und die Runtime startet. Der Adapter dazwischen ist dünn, damit ein Wechsel der Runtime kein Umbau der Plattform wird.

## Arbeitsplatz (Runtime-Vertrag)

Auf wessen Vertrag ein Agent arbeitet. Ein Arbeitsplatz trägt die Engine und ihre Kapazität — ein Abo-Sitz, ein API-Schlüssel, mehrere davon. Die Reihenfolge der Credentials ist die Merit Order: Bezahltes Kontingent wird vor Bezahltem-nach-Verbrauch gefahren. Bei einer einzigen Anmeldung merkt man davon nichts; die Plattform legt den Arbeitsplatz selbst an.

## Backlog

Die persistente Aufgabenliste eines Agenten, als eigenständiges Objekt und nicht als Chat-Verlauf. Zustände: \`open\`, \`in_progress\`, \`blocked\`, \`done\`, \`failed\`, \`cancelled\`. \`blocked\` ist der interessante: Der Agent wartet auf eine Antwort — von einem Menschen oder aus einem Zielsystem — und wird geweckt, wenn sie eintrifft.

## Guard-Rail

Eine zentral definierte Grenze für Agentenverhalten: Freigabepflicht für eine Aktion, verbotenes System, gesperrtes Tool, Egress-Regel. Erzwungen wird sie **außerhalb der Runtime** und fail-closed — ein Agent kann sie nicht wegreden, weil sie nicht in seinem Prompt steht.

## Secrets-Broker

Der Dienst, der Zugänge zur Laufzeit ausstellt, kurzlebig und gescopt. Langlebige Secrets kommen nicht in die Sandbox. Gespeichert wird mit AES-GCM in Postgres-Spalten, verschlüsselt mit dem \`COVEY_MASTER_KEY\` der Instanz.

## Zielsystem

Ein angebundenes Fremdsystem — Zammad, Salesforce, Jira, Confluence, GitHub, GitLab, Microsoft Teams, SharePoint, Nextcloud, E-Mail, ein Headless-Browser, ein MCP-Server. Jedes ist ein Plugin — kompiliert, als Manifest, als WebAssembly-Modul oder als MCP-Server; der Kern kennt keine Sonderfälle. Aktionen laufen über einen Proxy, damit Guard-Rails und Aufzeichnung greifen.

## Wiki-Gedächtnis

Was der Agent behält: verlinkte Markdown-Seiten mit einem \`pgvector\`-Index, nicht eine Halde flacher Schnipsel. Lesbar und von Hand korrigierbar — man kann einem Agenten etwas beibringen und ihn gezielt etwas vergessen lassen.

## Config-as-Code

Das Verhalten eines Agenten steht in Dateien: \`SOUL.md\` (Rolle und Ton), \`PLAYBOOKS.md\` (Arbeitsabläufe), \`CAPABILITIES.md\` (Zuständigkeit), \`ACCESS.md\` (Zugänge), \`HEARTBEAT.md\` (wiederkehrende Aufgaben), \`ORG.md\` (Vorgesetzte). Versioniert, mit Verlauf, änderbar über die Oberfläche oder die API.

## Recording und Not-Aus

Jeder Lauf wird aufgezeichnet: Werkzeugaufrufe, Zielsystem-Aktionen, Freigaben, Screenshots des Browsers, Tokens und Kosten. Der Not-Aus hält einen Agenten an — oder die gesamte Belegschaft der Organisation auf einen Griff.

## Weiter

- [Architektur-Überblick](/docs/architektur) — wie diese Teile zusammenhängen
- [Das Agenten-Modell](/docs/agenten-modell) — was einen Agenten ausmacht
- [Backlog & Lifecycle](/docs/backlog-lifecycle) — die Zustände im Detail`,
          en: `# Core concepts

Twelve terms that keep coming back in the interface, in the specification and on these pages. Read them once and the rest needs no explaining.

## Agent

A configured, permanently existing entity with identity, sandbox, access and backlog — the counterpart to an employee. An agent is not a process that runs: its normal state is \`sleeping\`. A task, a webhook or a heartbeat wakes it, it works serially, and it goes back to sleep. A dedicated email address is optional.

## Control plane

The central, stateful service — one Go binary: scheduler and dispatcher, agent registry, org chart, backlog store, identity and secrets broker, guard-rail engine, observability. It knows the state of every agent. **The control plane is the product**; sandboxes are commodity, runtimes are swappable.

## Data plane

All the sandboxes where the work actually happens. They are deliberately dumb and replaceable: lose one and it is rebuilt from config and home. Only the agent's home is persistent.

## Runtime and daemon

The **runtime** is the agent framework that drives the model loop — the first adapter is Claude Code headless via \`claude -p\`. The **daemon** (\`coveyd\`) is the lean process inside the sandbox that speaks the platform protocol and starts the runtime. The adapter between them is thin, so swapping the runtime never becomes a rebuild of the platform.

## Workplace (runtime contract)

Whose contract an agent works on. A workplace carries the engine and its capacity — a subscription seat, an API key, several of them. The order of the credentials is the merit order: capacity already paid for runs before capacity billed per token. With a single login you notice none of this; the platform creates the workplace itself.

## Backlog

An agent's persistent task list, as an object in its own right rather than a chat history. States: \`open\`, \`in_progress\`, \`blocked\`, \`done\`, \`failed\`, \`cancelled\`. \`blocked\` is the interesting one: the agent is waiting for an answer — from a human or from a target system — and is woken when it arrives.

## Guard-rail

A centrally defined boundary on agent behaviour: approval required for an action, a forbidden system, a blocked tool, an egress rule. It is enforced **outside the runtime** and fail-closed — an agent cannot talk its way past it, because it is not in its prompt.

## Secrets broker

The service that issues access at runtime, short-lived and scoped. Long-lived secrets do not enter the sandbox. Storage is AES-GCM in Postgres columns, encrypted with the instance's \`COVEY_MASTER_KEY\`.

## Target system

A connected foreign system — Zammad, Salesforce, Jira, Confluence, GitHub, GitLab, Microsoft Teams, SharePoint, Nextcloud, email, a headless browser, an MCP server. Each is a plugin — compiled, a manifest, a WebAssembly module or an MCP server; the core knows no special cases. Actions go through a proxy so guard-rails and recording apply.

## Wiki memory

What the agent keeps: linked Markdown pages with a \`pgvector\` index, not a heap of flat snippets. Readable and correctable by hand — you can teach an agent something, and make it forget something specific.

## Config as code

An agent's behaviour lives in files: \`SOUL.md\` (role and tone), \`PLAYBOOKS.md\` (procedures), \`CAPABILITIES.md\` (remit), \`ACCESS.md\` (access), \`HEARTBEAT.md\` (recurring tasks), \`ORG.md\` (supervisor). Versioned, with a history, editable through the interface or the API.

## Recording and kill switch

Every run is recorded: tool calls, target-system actions, approvals, browser screenshots, tokens and cost. The kill switch stops one agent — or the organisation's entire workforce in a single move.

## Next

- [Architecture overview](/en/docs/architecture) — how these parts fit together
- [The agent model](/en/docs/agent-model) — what makes up an agent
- [Backlog & lifecycle](/en/docs/backlog-and-lifecycle) — the states in detail`,
        },
        faq: {
          de: [
            {
              q: "Was ist der Unterschied zwischen Runtime und Agent?",
              a: "Der Agent ist die dauerhafte Identität mit Konfiguration, Backlog und Gedächtnis. Die Runtime ist die austauschbare Maschinerie, die während eines Laufs das Modell fährt. Derselbe Agent kann auf eine andere Runtime umgestellt werden, ohne seine Geschichte zu verlieren.",
            },
            {
              q: "Warum arbeitet ein Agent seriell und nicht parallel?",
              a: "Weil parallele Läufe desselben Agenten sich um dasselbe Home, dieselbe Aufgabe und denselben Zustand streiten würden. Mehr Durchsatz erreicht man über mehr Agenten, nicht über mehr Gleichzeitigkeit in einem.",
            },
            {
              q: "Was passiert, wenn eine Sandbox abstürzt?",
              a: "Nichts, was sich nicht wiederherstellen ließe. Der Zustand liegt in der Control Plane, das Home auf der Platte. Die Sandbox wird beim nächsten Wecken neu gebaut; eine unterbrochene Aufgabe geht zurück in den Backlog.",
            },
          ],
          en: [
            {
              q: "What is the difference between a runtime and an agent?",
              a: "The agent is the permanent identity with its configuration, backlog and memory. The runtime is the replaceable machinery that drives the model during a run. The same agent can be moved to a different runtime without losing its history.",
            },
            {
              q: "Why does an agent work serially rather than in parallel?",
              a: "Because parallel runs of the same agent would fight over the same home, the same task and the same state. Throughput comes from more agents, not from more concurrency inside one.",
            },
            {
              q: "What happens when a sandbox crashes?",
              a: "Nothing that cannot be restored. The state lives in the control plane, the home on disk. The sandbox is rebuilt at the next wake; an interrupted task goes back into the backlog.",
            },
          ],
        },
      },
      {
        slug: { de: "architektur", en: "architecture" },
        title: { de: "Architektur-Überblick", en: "Architecture overview" },
        description: {
          de: "Control Plane als einzelnes Go-Binary, Data Plane aus ephemeren Docker-Sandboxen, dazwischen ein WebSocket-Protokoll. Postgres trägt Zustand, Queue und Vektorsuche.",
          en: "The control plane as one Go binary, a data plane of ephemeral Docker sandboxes, a WebSocket protocol between them. Postgres carries state, queue and vector search.",
        },
        body: {
          de: `# Architektur-Überblick

Covey zerfällt in zwei Hälften, die sich sehr unterschiedlich verhalten: eine **Control Plane**, die den Zustand führt und immer läuft, und eine **Data Plane** aus Sandboxen, die entstehen und wieder verschwinden. Dazwischen liegt ein Protokoll, und genau an dieser Naht wird die Plattform gegenüber der Runtime austauschbar.

## Control Plane — ein Prozess, ein Binary

Ein einzelnes Go-Binary vereint Scheduler und Dispatcher, Agent-Registry und Org-Chart, Backlog-Store, Identitäts- und Secrets-Broker, Guard-Rail-Engine, Observability und die HTTP-API. Die React-Oberfläche ist über \`//go:embed\` einkompiliert, die SQL-Migrationen ebenso; beim Start migriert die Instanz sich selbst, abgesichert über ein \`pg_advisory_lock\`.

Der Grund für den einen Prozess ist nicht Sparsamkeit, sondern Betreibbarkeit: Wer Covey installiert, soll eine Datei kopieren und sie starten. Alles, was sonst als zweiter Dienst danebenstünde — Queue, Pub/Sub, Vektorindex —, liegt in Postgres.

## Data Plane — dumm und ersetzbar

Pro Wecken startet die Control Plane einen Container aus dem Sandbox-Image und darin \`coveyd\`. Der Container erbt nichts von der Umgebung des Hosts: Was er sieht, hat die Control Plane hineingelegt. Persistent ist allein das Home des Agenten, das als Volume eingehängt wird.

Daraus folgt die Betriebsregel: Geht eine Sandbox verloren, wird sie aus Config und Home neu gebaut. Ein Agent, der beim nächsten Lauf einen frischen Container bekommt, verliert nichts, was ihm gehört.

## Daemon-Protokoll — die stabile Naht

Zwischen Control Plane und Sandbox läuft ein bidirektionales Protokoll über WebSocket. Es transportiert den Auftrag hinein, Werkzeugaufrufe und Ergebnisse heraus, Freigabe-Anfragen in beide Richtungen und am Ende Tokens und Kosten.

Die Runtime dahinter ist austauschbar, weil sie das Protokoll nicht kennt — dazwischen sitzt ein dünner Adapter. Der erste ist Claude Code headless (\`claude -p\`); ein weiterer ändert an der Plattform nichts, solange er dieselben Nachrichten spricht.

## Warum die Sandbox ein Geschwister-Container ist

Die Control Plane startet Sandboxen über den Docker-Socket des Hosts — sie laufen also **neben** ihr, nicht in ihr. Das hat eine praktische Folge, über die viele beim Anpassen des Compose-Setups stolpern: Der \`-v\`-Pfad eines Agenten-Homes wird vom Docker-Daemon des **Hosts** aufgelöst. Deshalb muss das Datenverzeichnis auf Host und Container denselben Pfad haben; ein benanntes Volume ginge ins Leere.

## Postgres als Anker

Ein Datenspeicher trägt fast alles:

- **Zustand** — Agenten, Organisationen, Rollen, Konfigurationsversionen
- **Queue** — \`SELECT … FOR UPDATE SKIP LOCKED\` statt eines Brokers
- **Pub/Sub** — \`LISTEN/NOTIFY\` weckt den Dispatcher, ohne dass jemand pollt
- **Gedächtnis** — \`pgvector\` für die Suche über das Wiki der Agenten
- **Secrets** — AES-GCM-verschlüsselte Spalten, gebunden an die Organisation

## Batteries included, but swappable

Jede Fähigkeit hat eine eingebaute, datenbankgestützte Voreinstellung und ein schmales Interface für einen externen Anbieter: \`IdentityProvider\` builtin (JWT/Argon2id) ↔ OIDC, \`SecretStore\` builtin (AES-GCM) ↔ Vault. Der Normalbetrieb läuft mit \`builtin\` überall — Keycloak, Vault und Redis sind Optionen, nie Voraussetzungen.

## Verteilte Data Plane

Reicht eine Maschine nicht mehr, registrieren sich **Runner** bei der Control Plane und übernehmen Sandboxen; die Homes liegen dann zentral. Der Aufbau bleibt derselbe, nur der Ort der Container ändert sich.

## Weiter

- [Betrieb & Deployment](/docs/betrieb) — Ports, Umgebungsvariablen, Egress, Updates
- [Identität & Secrets](/docs/identitaet-secrets) — was in die Sandbox darf und was nicht
- [Zielsysteme & Plugins](/docs/zielsysteme) — wie ein Agent an fremde Systeme kommt`,
          en: `# Architecture overview

Covey falls into two halves that behave very differently: a **control plane** that holds the state and is always running, and a **data plane** of sandboxes that come and go. Between them sits a protocol, and that seam is exactly what keeps the platform replaceable with respect to the runtime.

## Control plane — one process, one binary

A single Go binary unites scheduler and dispatcher, agent registry and org chart, backlog store, identity and secrets broker, guard-rail engine, observability and the HTTP API. The React interface is compiled in via \`//go:embed\`, and so are the SQL migrations; at startup the instance migrates itself, guarded by a \`pg_advisory_lock\`.

The reason for the single process is not frugality but operability: whoever installs Covey should copy one file and start it. Everything that would otherwise stand next to it as a second service — queue, pub/sub, vector index — lives in Postgres.

## Data plane — dumb and replaceable

For each wake the control plane starts a container from the sandbox image, running \`coveyd\` inside. The container inherits nothing from the host's environment: what it sees is what the control plane put there. The only persistent part is the agent's home, mounted as a volume.

From that follows the operating rule: lose a sandbox and it is rebuilt from config and home. An agent that gets a fresh container on its next run loses nothing that belongs to it.

## Daemon protocol — the stable seam

A bidirectional protocol over WebSocket runs between control plane and sandbox. It carries the assignment in, tool calls and results out, approval requests in both directions, and at the end tokens and cost.

The runtime behind it is swappable because it does not know the protocol — a thin adapter sits in between. The first one is Claude Code headless (\`claude -p\`); another changes nothing about the platform as long as it speaks the same messages.

## Why the sandbox is a sibling container

The control plane starts sandboxes through the host's Docker socket — so they run **next to** it, not inside it. That has one practical consequence people trip over when adapting the compose setup: an agent home's \`-v\` path is resolved by the **host's** Docker daemon. The data directory therefore has to carry the same path on host and container; a named volume would point at nothing.

## Postgres as the anchor

One data store carries almost everything:

- **State** — agents, organisations, roles, configuration versions
- **Queue** — \`SELECT … FOR UPDATE SKIP LOCKED\` instead of a broker
- **Pub/sub** — \`LISTEN/NOTIFY\` wakes the dispatcher without anybody polling
- **Memory** — \`pgvector\` for search across the agents' wiki
- **Secrets** — AES-GCM-encrypted columns, bound to the organisation

## Batteries included, but swappable

Every capability has a built-in, database-backed default and a narrow interface for an external provider: \`IdentityProvider\` builtin (JWT/Argon2id) ↔ OIDC, \`SecretStore\` builtin (AES-GCM) ↔ Vault. Normal operation runs with \`builtin\` everywhere — Keycloak, Vault and Redis are options, never prerequisites.

## Distributed data plane

When one machine is no longer enough, **runners** register with the control plane and take over sandboxes; the homes then live centrally. The structure stays the same, only the location of the containers changes.

## Next

- [Operations & deployment](/en/docs/operations) — ports, environment variables, egress, updates
- [Identity & secrets](/en/docs/identity-and-secrets) — what may enter the sandbox and what may not
- [Target systems & plugins](/en/docs/target-systems) — how an agent reaches foreign systems`,
        },
        faq: {
          de: [
            {
              q: "Braucht Covey Redis, RabbitMQ oder Kafka?",
              a: "Nein. Die Warteschlange ist \`SELECT … FOR UPDATE SKIP LOCKED\`, die Benachrichtigung \`LISTEN/NOTIFY\`, die Vektorsuche \`pgvector\` — alles in derselben Postgres-Instanz. Ein Broker wäre ein zweiter Dienst mit eigenem Ausfallverhalten für einen Nutzen, den die Datenbank schon hat.",
            },
            {
              q: "Kann ich eine andere Runtime als Claude Code verwenden?",
              a: "Vom Aufbau her ja: Runtimes hängen als Plugins an einer Registry und sprechen über einen dünnen Adapter das Daemon-Protokoll. Ausgeliefert ist derzeit Claude Code headless, dazu eine Mock-Runtime für Tests und Demos ohne Modellkosten.",
            },
            {
              q: "Warum läuft die Sandbox in Docker und nicht als Unterprozess?",
              a: "Weil Prozess-Isolation keine ist, sobald ein Agent Werkzeuge ausführt. Der Container gibt Namespaces, ein eigenes Dateisystem und einen kontrollierbaren Netzausgang. Einen local-Provider gab es früher; er ist entfernt, damit niemand versehentlich ohne Isolation produktiv geht.",
            },
            {
              q: "Wie skaliert Covey über eine Maschine hinaus?",
              a: "Über Runner: eigenständige Prozesse, die sich bei der Control Plane registrieren und Sandboxen ausführen. Die Control Plane bleibt eine, der Zustand bleibt in Postgres, die Homes liegen zentral.",
            },
          ],
          en: [
            {
              q: "Does Covey need Redis, RabbitMQ or Kafka?",
              a: "No. The queue is \`SELECT … FOR UPDATE SKIP LOCKED\`, the notification is \`LISTEN/NOTIFY\`, the vector search is \`pgvector\` — all in the same Postgres instance. A broker would be a second service with its own failure modes for something the database already does.",
            },
            {
              q: "Can I use a runtime other than Claude Code?",
              a: "Structurally, yes: runtimes hang off a registry as plugins and speak the daemon protocol through a thin adapter. What ships today is Claude Code headless, plus a mock runtime for tests and demos without model cost.",
            },
            {
              q: "Why does the sandbox run in Docker rather than as a subprocess?",
              a: "Because process isolation is not isolation once an agent executes tools. The container gives you namespaces, its own filesystem and a controllable way out to the network. There used to be a local provider; it was removed so nobody goes to production without isolation by accident.",
            },
            {
              q: "How does Covey scale beyond one machine?",
              a: "Through runners: standalone processes that register with the control plane and execute sandboxes. There is still one control plane, the state stays in Postgres, and the homes live centrally.",
            },
          ],
        },
      },
    ],
  },
  {
    id: "erste-schritte",
    title: { de: "Erste Schritte", en: "Getting started" },
    pages: [
      {
        slug: { de: "schnellstart", en: "quickstart" },
        title: { de: "Schnellstart (Docker)", en: "Quick start (Docker)" },
        description: {
          de: "Covey selbst hosten: Repository klonen, Master-Key erzeugen, Sandbox-Image bauen, docker compose up — danach läuft die Plattform auf Port 8494.",
          en: "Self-host Covey: clone the repository, generate a master key, build the sandbox image, docker compose up — then the platform runs on port 8494.",
        },
        body: {
          de: `# Schnellstart (Docker)

Covey läuft als einzelnes Binary neben einer Postgres-Datenbank. Ohne Go, ohne Node, ohne lokale Postgres — Docker genügt.

## Voraussetzungen

- **Docker** mit Compose-Plugin (\`docker compose version\` ≥ 2.x)
- **OpenSSL** für den Master-Key (auf macOS/Linux vorinstalliert)

## Installieren

\`\`\`
git clone https://github.com/benjaminLedel/covey.git && cd covey
cp .env.example .env
echo "COVEY_MASTER_KEY=$(openssl rand -hex 32)" >> .env

docker build -f Dockerfile.sandbox -t covey-sandbox:latest .
docker compose up -d --build
\`\`\`

Dann **http://localhost:8494** öffnen, Login \`admin@covey.local\` / \`covey-admin\` (änderbar in der \`.env\` über \`COVEY_ADMIN_EMAIL\` / \`COVEY_ADMIN_PASSWORD\`).

Der \`COVEY_MASTER_KEY\` ver- und entschlüsselt alle hinterlegten Secrets. Geht er verloren, ist jedes hinterlegte Credential unlesbar — die \`.env\` gehört gesichert und nicht ins Git.

## Warum der Image-Build eine eigene Zeile ist

Alles außer \`docker build\` dauert Sekunden. Diese Zeile baut den Container, **in dem ein Agent arbeitet** (Claude Code, chromium, eine Node- und Java-Toolchain) und braucht ein paar Minuten.

Zum Umsehen kann man sie weglassen: Die Plattform startet, die Oberfläche funktioniert, Agenten und Configs lassen sich anlegen. Erst der erste Lauf scheitert dann — und sagt auch, woran. Covey prüft das beim Start und meldet es in den **ersten Schritten** auf der Agenten-Übersicht.

## Was \`docker compose\` startet

- \`db\` — PostgreSQL mit \`pgvector\`.
- \`bootstrap\` — legt einmalig Organisation, Admin, Demo-Agent und dessen Arbeitsplatz an (idempotent) und beendet sich.
- \`covey\` — die Control Plane: API + Orchestrator + eingebettete Admin-UI auf Port 8494. Migrationen laufen beim Start automatisch.

Zwei Dinge tragen dabei die Sandbox-Isolation und gehen beim Anpassen leicht verloren: der **Docker-Socket** im covey-Container (er startet jede Sandbox als Geschwister-Container) und ein **Datenverzeichnis unter identischem Pfad** auf Host und im Container (die Agenten-Homes werden vom Host-Daemon gemountet).

## Der erste Agent

Nach dem Login führt die **Einrichtung** durch drei Fragen, jede überspringbar: die Engine und ihr Zugang — bei Claude Code das Secret \`anthropic_api_key\` (API-Schlüssel) oder \`claude_code_oauth_token\` (Abo-Konto, Token einmalig mit \`claude setup-token\` erzeugen), geprüft bevor er gespeichert wird —, dann drei Sätze darüber, was Ihr Unternehmen macht, und zuletzt die **Personalabteilung**: ein Agent, dessen Aufgabe es ist, die anderen zu entwerfen.

Danach ist *Neuer Agent → Ausschreibung* der kürzeste Weg zur ersten Kollegin: in ein paar Sätzen beschreiben, was sie tun soll. Was dabei herauskommt, ist ein **Entwurf** — er arbeitet erst, wenn Sie ihn einstellen. Der Demo-Agent, der nach dem Login bereitsteht, ist bereits eingestellt und läuft, sobald der Zugang steht.

Die Checkliste **Erste Schritte** auf der Agenten-Übersicht führt den Weg zu Ende; sie liest den tatsächlichen Zustand der Organisation und verschwindet, wenn alles erledigt ist.

## Lieber das blanke Binary?

\`\`\`
curl -sSL https://raw.githubusercontent.com/benjaminLedel/covey/main/installer/install.sh | sh
\`\`\`

Holt das passende Binary aus dem neuesten Release und prüft die SHA-256-Summe. Die Control Plane braucht dann noch PostgreSQL (mit pgvector) und Docker für die Sandboxen; das Skript nennt die verbleibenden Schritte.

## Weiter

- [Den ersten Agenten anlegen](/docs/ersten-agenten) — vom Credential zum ersten Lauf
- [Betrieb & Deployment](/docs/betrieb) — HTTPS, Sicherungen, Updates`,
          en: `# Quick start (Docker)

Covey runs as a single binary next to a Postgres database. No Go, no Node, no local Postgres — Docker is all you need.

## Prerequisites

- **Docker** with the Compose plugin (\`docker compose version\` ≥ 2.x)
- **OpenSSL** for the master key (pre-installed on macOS/Linux)

## Install

\`\`\`
git clone https://github.com/benjaminLedel/covey.git && cd covey
cp .env.example .env
echo "COVEY_MASTER_KEY=$(openssl rand -hex 32)" >> .env

docker build -f Dockerfile.sandbox -t covey-sandbox:latest .
docker compose up -d --build
\`\`\`

Then open **http://localhost:8494** and sign in with \`admin@covey.local\` / \`covey-admin\` (changeable in \`.env\` through \`COVEY_ADMIN_EMAIL\` / \`COVEY_ADMIN_PASSWORD\`).

The \`COVEY_MASTER_KEY\` en- and decrypts every deposited secret. Lose it and every stored credential is unreadable — keep the \`.env\` safe, and out of Git.

## Why the image build is a line of its own

Everything except \`docker build\` takes seconds. That line builds the container **an agent works inside** (Claude Code, chromium, a Node and Java toolchain) and takes a few minutes.

You can skip it to look around first: the platform starts, the interface works, agents and configs can be created. Only the first run fails then — and it says why. Covey checks for this at startup and reports it in the **first steps** on the agent overview.

## What \`docker compose\` starts

- \`db\` — PostgreSQL with \`pgvector\`.
- \`bootstrap\` — creates the organisation, the admin, a demo agent and its workplace once (idempotent), then exits.
- \`covey\` — the control plane: API + orchestrator + embedded admin UI on port 8494. Migrations run automatically at start.

Two things carry the sandbox isolation and are easy to lose when adapting the file: the **Docker socket** inside the covey container (it starts every sandbox as a sibling container) and a **data directory under an identical path** on host and container (agent homes are mounted by the host's daemon).

## Your first agent

After signing in, **Setup** walks you through three questions, each one skippable: the engine and its credential — for Claude Code the secret \`anthropic_api_key\` (an API key) or \`claude_code_oauth_token\` (a subscription account; generate the token once with \`claude setup-token\`), checked before it is stored — then three sentences on what your company does, and finally the **People department**: an agent whose job is drafting the others.

After that, *New agent → brief* is the shortest way to your first colleague: describe in a few sentences what they should do. What comes out is a **draft** — it only starts working once you hire it. The demo agent waiting after sign-in is already hired and runs as soon as the credential is there.

The **first steps** checklist on the agent overview walks the rest of the way; it reads the organisation's actual state and disappears once everything is done.

## Rather have the plain binary?

\`\`\`
curl -sSL https://raw.githubusercontent.com/benjaminLedel/covey/main/installer/install.sh | sh
\`\`\`

It fetches the right binary from the latest release and verifies its SHA-256 checksum. The control plane then still needs PostgreSQL (with pgvector) and Docker for the sandboxes; the script prints the remaining steps.

## Next

- [Create your first agent](/en/docs/first-agent) — from credential to first run
- [Operations & deployment](/en/docs/operations) — HTTPS, backups, updates`,
        },
        faq: {
          de: [
            {
              q: "Welche Ports braucht Covey?",
              a: "Einen: **8494** für API und Oberfläche. Postgres läuft im Compose-Setup auf dem internen Netz und muss nicht nach außen. Die Sandboxen brauchen keinen eingehenden Port — sie rufen die Control Plane von sich aus.",
            },
            {
              q: "Warum dauert der Sandbox-Image-Build so lange?",
              a: "Weil in dem Image der Arbeitsplatz eines Agenten steckt: Claude Code, git, ripgrep, chromium für den Browser, dazu eine Node- und Java-Toolchain und Build-Werkzeuge für native npm-Pakete. Das sind einige Gigabyte, und sie werden einmal gebaut, nicht bei jedem Lauf.",
            },
            {
              q: "Kann ich Covey ohne den Image-Build ausprobieren?",
              a: "Ja — die Plattform startet, die Oberfläche funktioniert, Agenten und Konfigurationen lassen sich anlegen. Nur der erste Lauf scheitert dann, und zwar mit einer Meldung, die genau das sagt: beim Start im Log und in der Checkliste auf der Agenten-Übersicht.",
            },
            {
              q: "Was passiert, wenn ich den COVEY_MASTER_KEY verliere?",
              a: "Dann ist jedes hinterlegte Secret unlesbar — der Schlüssel ver- und entschlüsselt sie mit AES-GCM. Es gibt keine Hintertür. Sichern Sie die \`.env\`, und behandeln Sie den Schlüssel wie ein Datenbank-Passwort.",
            },
            {
              q: "Läuft Covey auf einem Raspberry Pi oder auf ARM?",
              a: "Die Binaries gibt es für linux/amd64, linux/arm64 sowie macOS in beiden Architekturen. Entscheidend ist weniger die Architektur als der Arbeitsspeicher: In jeder Sandbox laufen eine Node-Runtime und je nach Aufgabe ein chromium.",
            },
          ],
          en: [
            {
              q: "Which ports does Covey need?",
              a: "One: **8494** for the API and the interface. In the compose setup Postgres lives on the internal network and needs no exposure. The sandboxes need no inbound port — they dial the control plane themselves.",
            },
            {
              q: "Why does the sandbox image build take so long?",
              a: "Because that image is an agent's workplace: Claude Code, git, ripgrep, chromium for the browser plugin, plus a Node and Java toolchain and the build tools for native npm packages. That is several gigabytes, and it is built once rather than per run.",
            },
            {
              q: "Can I try Covey without building the image?",
              a: "Yes — the platform starts, the interface works, agents and configurations can be created. Only the first run fails, and it fails with a message that says exactly that: in the log at startup and in the checklist on the agent overview.",
            },
            {
              q: "What happens if I lose the COVEY_MASTER_KEY?",
              a: "Every deposited secret becomes unreadable — the key en- and decrypts them with AES-GCM. There is no back door. Keep the \`.env\` safe and treat the key like a database password.",
            },
            {
              q: "Does Covey run on a Raspberry Pi or on ARM?",
              a: "Binaries exist for linux/amd64, linux/arm64 and macOS in both architectures. The deciding factor is memory rather than architecture: every sandbox runs a Node runtime and, depending on the task, a chromium.",
            },
          ],
        },
      },
      {
        slug: { de: "ersten-agenten", en: "first-agent" },
        title: { de: "Den ersten Agenten anlegen", en: "Create your first agent" },
        description: {
          de: "In fünf Schritten vom leeren Covey zum arbeitenden KI-Agenten: einrichten, ausschreiben, Entwurf durchsehen und einstellen, Aufgabe stellen, Lauf im Recording ansehen.",
          en: "Five steps from an empty Covey to a working AI agent: set it up, write a brief, look the draft through and hire it, give it a task, watch the run in the recording.",
        },
        body: {
          de: `# Den ersten Agenten anlegen

Einen Agenten anzulegen ist ein Onboarding: Identität, Rolle, Zugänge, Vorgesetzter. Der Unterschied zum Menschen ist, dass die Rollenbeschreibung diesmal gelesen wird — sie ist der Prompt.

Fünf Schritte, und die Checkliste **Erste Schritte** auf der Agenten-Übersicht hakt sie mit, während Sie sie tun. Sie liest den echten Zustand Ihrer Organisation, nicht Ihren Fortschritt in einer Tour — was einmal steht, steht.

## 1. Einrichtung

Die Seite *Einrichtung* stellt drei Fragen, jede überspringbar:

- **Motor und Zugang.** Welche Engine Ihre Agenten denken lässt, und das Credential dazu — bei Claude Code ein API-Schlüssel (Abrechnung nach Verbrauch) oder ein Abo-Token (einmalig im Terminal mit \`claude setup-token\` erzeugt), bei Codex ein API-Schlüssel oder der Inhalt von \`~/.codex/auth.json\`. Der Wert wird gegen den Anbieter geprüft, **bevor** er gespeichert wird — besser hier als eine Stunde später im Lauf eines Agenten. Der Arbeitsplatz entsteht dabei von selbst; ein zweiter Token wird automatisch zu weiterer Kapazität.
- **Was Ihr Unternehmen macht.** Drei bis fünf Sätze. Sie bleiben an der Organisation und gehen von da an in jede Ausschreibung, in die Konfiguration neu entworfener Agenten und in den Config-Assistenten ein.
- **Ihre Personalabteilung.** Ein Agent, dessen Aufgabe es ist, die anderen zu entwerfen.

Die Seite läuft für sich, ohne Navigation daneben, und verschwindet aus dem Menü, sobald die drei Karten stehen. Alles davon geht auch später von Hand (Secrets, Runtimes, Vorlagenbibliothek). Was die Einrichtung kauft, ist die Reihenfolge: ohne Zugang kann nichts laufen, was die Oberfläche anbietet.

## 2. Die Ausschreibung

*Neuer Agent* bietet vier Wege. Der kürzeste ist die **Ausschreibung**: ein Freitextfeld — was soll die neue Kollegin tun? — plus Abteilung und Vorgesetzter. Daraus wird ein Auftrag an die Personalabteilung, und die Oberfläche zeigt danach das laufende Einstellungsgespräch. Ist die Beschreibung zu dünn, fragt der Agent zurück, statt zu raten.

Daneben bleiben die anderen drei Wege: eine **Vorlage** aus der Bibliothek, das **manuelle** Formular für den, der genau weiß, was er will, und der **Bundle-Import** aus \`examples/\` (Coding-Agent, QA-Agent, Web-Rechercheur, Log-Triage).

## 3. Durchsehen und einstellen

Was dabei herauskommt, ist ein **Entwurf** — bei jedem der vier Wege. Er steht auf der Agenten-Übersicht im Feld *Bewerbungen*: angelegt, ansehbar, änderbar, und er arbeitet nicht. Kein Dispatch, kein Heartbeat, kein scharfer Webhook, keine Sandbox, keine Kosten.

Sehen Sie sich seine Konfiguration an — das ist der eigentliche Sinn des Zustands:

- \`SOUL.md\` — die Rollenbeschreibung: Wer bist du, wofür bist du zuständig, in welchem Ton antwortest du, wo hörst du auf. Sie wird bei jedem Lauf in den Prompt kompiliert, ist versioniert und lässt sich mit Verlauf zurückrollen.
- \`PLAYBOOKS.md\` — Arbeitsabläufe, Schritt für Schritt
- \`CAPABILITIES.md\` — wofür der Agent zuständig ist und wofür nicht
- \`ACCESS.md\` — die Zugänge, in der Form \`- system: zammad scope: read,write,comment\`
- \`HEARTBEAT.md\` — wiederkehrende Aufgaben, etwa \`- alle: 30m titel: Posteingang aufgabe: Neue Tickets sichten.\`
- \`ORG.md\` — der Vorgesetzte, an den eskaliert wird

Ein guter erster Satz in der \`SOUL.md\` ist konkreter, als man denkt: „Du beantwortest Fragen zum Abrechnungssystem" trägt weiter als „Du bist ein hilfreicher Assistent".

Die Entwürfe stehen auf der Agenten-Übersicht in einem eigenen Feld **Bewerbungen**, abgesetzt von der Belegschaft darunter. **Einstellen** zeigt vorher eine Zusammenfassung — Rolle, angefragte Zielsysteme mit Scopes, Vorgesetzter, Runtime, Budgetdeckel — und gibt danach die wartenden Aufgaben frei. Passt der Entwurf nicht, verwirft ihn **Ablehnen**; er hat nie gearbeitet, es gibt nichts aufzuräumen.

## 4. Erste Aufgabe

Im Backlog des Agenten eine Aufgabe anlegen. „Wecken" startet die Abarbeitung sofort, statt auf den nächsten Auslöser zu warten. Danach ist der Agent wieder \`sleeping\` — das ist der Normalzustand, und er kostet nichts.

## 5. Zusehen

Das Recording zeigt den Lauf von innen: jeden Werkzeugaufruf, jede Zielsystem-Aktion, jede Freigabe, bei Browser-Aufgaben die Screenshots, am Ende Tokens und Kosten. Wer wissen will, warum ein Agent etwas getan hat, liest hier nach und nicht im Modell.

## Danach: Zuständigkeit statt Aufgaben

Ein Agent, der nur auf angelegte Aufgaben wartet, ist ein teures Kommandozeilenwerkzeug. Interessant wird er mit einer **Weckquelle**: ein Webhook aus dem Ticketsystem, ein Heartbeat im Minutentakt, eine eingehende E-Mail. Dann arbeitet er, wenn Arbeit da ist, und schläft, wenn keine da ist.

## Weiter

- [Das Agenten-Modell](/docs/agenten-modell) — was einen Agenten technisch ausmacht
- [Zielsysteme & Plugins](/docs/zielsysteme) — Zammad, GitLab, E-Mail, Teams anbinden
- [Guard-Rails & Kontrolle](/docs/guard-rails) — Freigaben, Not-Aus, Aufzeichnung`,
          en: `# Create your first agent

Creating an agent is an onboarding: identity, role, access, supervisor. The difference from a human is that this time the role description actually gets read — it is the prompt.

Five steps, and the **first steps** checklist on the agent overview ticks them off while you do them. It reads your organisation's real state rather than your progress through a tour — once something stands, it stands.

## 1. Setup

The *Setup* page asks three questions, each one skippable:

- **Engine and credential.** Which engine your agents think on, and the credential for it — for Claude Code an API key (billed per use) or a subscription token (generate it once in the terminal with \`claude setup-token\`), for Codex an API key or the contents of \`~/.codex/auth.json\`. The value is checked against the provider **before** it is stored — better here than an hour later inside an agent's run. The workplace is created around it; a second token automatically becomes further capacity.
- **What your company does.** Three to five sentences. They stay on the organisation and from then on go into every hiring brief, into the configuration of newly drafted agents and into the config assistant.
- **Your People department.** An agent whose job is drafting the others.

The page runs on its own, with no navigation next to it, and disappears from the menu once the three cards are done. All of it can also be done by hand later (secrets, runtimes, the template library). What the setup buys is the order: without a credential nothing the interface offers can actually run.

## 2. The brief

*New agent* offers four ways in. The shortest is the **brief**: one free-text field — what should the new colleague do? — plus department and supervisor. That becomes an assignment for the People department, and the interface then shows the hiring conversation as it happens. If the description is too thin, the agent asks back instead of guessing.

The other three ways remain: a **template** from the library, the **manual** form for whoever knows exactly what they want, and the **bundle import** from \`examples/\` (coding agent, QA agent, web researcher, log triage).

## 3. Look it through and hire it

What comes out is a **draft** — on all four ways in. It sits on the agent overview under *Applications*: created, inspectable, changeable, and not working. No dispatch, no heartbeat, no live webhook, no sandbox, no cost.

Look at its configuration — that is what the state is for:

- \`SOUL.md\` — the role description: who you are, what you are responsible for, in what tone you answer, where you stop. It is compiled into the prompt on every run, is versioned and can be rolled back with its history.
- \`PLAYBOOKS.md\` — procedures, step by step
- \`CAPABILITIES.md\` — what the agent is responsible for and what not
- \`ACCESS.md\` — the access, in the form \`- system: zammad scope: read,write,comment\`
- \`HEARTBEAT.md\` — recurring tasks, e.g. \`- alle: 30m titel: Inbox aufgabe: Triage new tickets.\`
- \`ORG.md\` — the supervisor to escalate to

A good first sentence in \`SOUL.md\` is more concrete than you would think: "You answer questions about the billing system" carries further than "You are a helpful assistant".

The drafts sit on the agent overview in their own **Applications** panel, set apart from the workforce below. **Hire** shows a summary first — role, requested target systems with their scopes, supervisor, runtime, budget cap — and then releases the waiting tasks. If the draft does not fit, **Reject** discards it; it never worked, so there is nothing to clean up.

## 4. First task

Create a task in the agent's backlog. "Wake" starts processing right away instead of waiting for the next trigger. Afterwards the agent is \`sleeping\` again — that is the normal state, and it costs nothing.

## 5. Watch

The recording shows the run from the inside: every tool call, every target-system action, every approval, screenshots for browser work, and tokens and cost at the end. Whoever wants to know why an agent did something reads it here, not in the model.

## After that: a remit, not a list of chores

An agent that only waits for tasks somebody types is an expensive command-line tool. It gets interesting with a **wake source**: a webhook from the ticket system, a heartbeat every few minutes, an incoming email. Then it works when there is work, and sleeps when there is none.

## Next

- [The agent model](/en/docs/agent-model) — what technically makes up an agent
- [Target systems & plugins](/en/docs/target-systems) — connecting Zammad, GitLab, email, Teams
- [Guard-rails & control](/en/docs/guard-rails) — approvals, kill switch, recording`,
        },
        faq: {
          de: [
            {
              q: "Wie viel kostet ein Agentenlauf?",
              a: "So viel wie die Tokens, die das Modell dabei verbraucht — Covey selbst rechnet nichts ab. Jeder Lauf wird mit Tokens und Kosten aufgezeichnet, aufgeschlüsselt nach Agent und Modell. Pro Agent lässt sich ein Budget setzen; ist es erreicht, arbeitet er nicht weiter.",
            },
            {
              q: "Was gehört in die SOUL.md und was in die PLAYBOOKS.md?",
              a: "In die \`SOUL.md\` gehört, wer der Agent ist: Rolle, Zuständigkeit, Ton, Grenzen. In die \`PLAYBOOKS.md\` gehört, wie er vorgeht: die Schrittfolge für einen wiederkehrenden Fall. Faustregel — die SOUL ändert sich selten, ein Playbook ändert sich, sobald sich der Ablauf ändert.",
            },
            {
              q: "Kann ich einen Agenten aus einem anderen Covey übernehmen?",
              a: "Ja. Die Konfiguration lässt sich als Bündel exportieren und in einer anderen Instanz importieren — mitsamt Playbooks, Zugriffswünschen und Heartbeat. Die Secrets bleiben, wo sie waren; sie gehören der Organisation, nicht dem Bündel.",
            },
            {
              q: "Warum tut mein Agent nichts, obwohl eine Aufgabe im Backlog liegt?",
              a: "Die drei häufigsten Gründe: Es liegt kein Credential vor (dann sagt es die Checkliste), das Sandbox-Image fehlt (dann sagt es der Start-Log), oder der Agent wurde per Not-Aus angehalten. Im Recording steht in allen drei Fällen, woran es lag.",
            },
          ],
          en: [
            {
              q: "What does an agent run cost?",
              a: "As much as the tokens the model consumes — Covey itself bills nothing. Every run is recorded with tokens and cost, broken down by agent and model. You can set a budget per agent; once it is reached, the agent stops working.",
            },
            {
              q: "What belongs in SOUL.md and what in PLAYBOOKS.md?",
              a: "\`SOUL.md\` holds who the agent is: role, remit, tone, limits. \`PLAYBOOKS.md\` holds how it proceeds: the sequence of steps for a recurring case. Rule of thumb — the soul rarely changes, a playbook changes as soon as the procedure does.",
            },
            {
              q: "Can I move an agent from another Covey instance?",
              a: "Yes. A configuration can be exported as a bundle and imported into another instance — playbooks, requested access and heartbeat included. Secrets stay where they were; they belong to the organisation, not to the bundle.",
            },
            {
              q: "Why is my agent doing nothing although there is a task in its backlog?",
              a: "The three usual reasons: no credential is deposited (the checklist says so), the sandbox image is missing (the startup log says so), or the agent was stopped with the kill switch. In all three cases the recording names the reason.",
            },
          ],
        },
      },
    ],
  },
  {
    id: "konzepte",
    title: { de: "Konzepte", en: "Concepts" },
    pages: [
      {
        slug: { de: "agenten-modell", en: "agent-model" },
        title: { de: "Das Agenten-Modell", en: "The agent model" },
        description: {
          de: "Was einen Covey-Agenten ausmacht: eigene Identität, isolierte Sandbox mit persistentem Home, versioniertes Verhalten in SOUL.md und Rechenzeit nur bei Bedarf.",
          en: "What makes up a Covey agent: its own identity, an isolated sandbox with a persistent home, versioned behaviour in SOUL.md, and compute only on demand.",
        },
        body: {
          de: `# Das Agenten-Modell

Ein Agent ist kein Prompt und kein Chat-Verlauf. Er ist eine Entität, die bestehen bleibt, wenn niemand hinschaut: mit Identität, Arbeitsplatz, Zugängen, Gedächtnis und einem Verhalten, das als Code versioniert ist.

## Identität

Jeder Agent hat eine eigene Identität in der Plattform — nicht die des Menschen, der ihn angelegt hat. Daran hängen seine Zugänge, sein Platz im Org-Chart und jede Spur, die er hinterlässt. Optional bekommt er eine eigene E-Mail-Adresse, und dann sieht ihn ein Zielsystem als das, was er ist: als Absender mit Namen.

Das ist der Unterschied zu einem Skript, das unter dem Konto eines Administrators läuft. Wenn hinterher jemand fragt, wer diesen Kommentar geschrieben hat, gibt es eine Antwort.

## Der erste Tag

Ein Agent trägt den Zeitpunkt, an dem er eingestellt wurde. Fehlt er, ist er ein **Entwurf**: angelegt, konfigurierbar, im Org-Chart sichtbar — und er arbeitet nicht. Kein Dispatch, kein Heartbeat, kein scharfer Webhook, keine Sandbox, keine Kosten. Aufgaben dürfen trotzdem in seinem Backlog liegen und starten am ersten Tag.

Der Kill-Switch hätte technisch gereicht, würde aber zwei verschiedene Tatsachen in dasselbe Feld legen: „der wurde gestoppt" und „der hat noch nicht angefangen". Und das Einstellen ist keine Fahne, sondern ein Zeitpunkt — er steht später im Mitarbeiterprofil neben dem eines Menschen.

So entsteht jeder Agent, den niemand von Hand geschrieben hat: aus einer Vorlage, aus einem Bundle-Import und vor allem dort, wo ein *Agent* den Agenten entwirft. Einstellen bleibt ein menschlicher Akt; es gibt dafür keine Aktion, die ein Agent aufrufen könnte.

## Sandbox mit persistentem Home

Gearbeitet wird in einem Container, der zum Wecken entsteht und danach verschwindet. Was bleibt, ist \`/home/agent\`: geklonte Repositories, heruntergeladene Anhänge, Notizen, die Wiki-Seiten des Agenten. Beim nächsten Lauf findet er seinen Arbeitsplatz so vor, wie er ihn verlassen hat — die Maschine darum herum ist neu.

Der Arbeitsplatz ist im Browser einsehbar: durchsehen, eine Vorlage hineinlegen, eine Datei ändern, eine Auswahl als ZIP herausziehen. Auch während der Agent schläft, denn das Home liegt auf der Platte und nicht im Container.

## Verhalten als Code

\`SOUL.md\`, \`PLAYBOOKS.md\`, \`CAPABILITIES.md\`, \`ACCESS.md\`, \`HEARTBEAT.md\`, \`ORG.md\` — sechs Dateien, versioniert, mit Verlauf und Rücksprung. Eine Verhaltensänderung ist damit ein Vorgang, den man lesen, prüfen und zurücknehmen kann, und kein Deployment.

## Seriell vor parallel

Ein Agent bearbeitet eine Aufgabe zur Zeit. Das ist eine Entscheidung, keine Beschränkung: Zwei gleichzeitige Läufe desselben Agenten würden sich um dasselbe Home und denselben Zustand streiten, und der Fehler wäre nicht reproduzierbar. Mehr Durchsatz kommt über mehr Agenten.

## Immer erreichbar, Rechenzeit nur bei Bedarf

„Always-on" ist eine Eigenschaft der Bedienung, nicht der Rechnung. Ein billiger Dispatch-Loop hält den Agenten erreichbar; der teure Teil — Container, Modell — läuft nur, wenn Arbeit anliegt. Ein Agent im Zustand \`sleeping\` kostet nichts außer einer Zeile in der Datenbank.

Für Aufgaben, bei denen der Start ins Gewicht fällt, lässt sich eine **warme Sandbox** einschalten: Der Container bleibt zwischen zwei Weckungen stehen. Das kostet Speicher und spart Sekunden — eine bewusste Abwägung, keine Voreinstellung.

## Grenzen, die der Agent nicht verschieben kann

\`max_turns\` begrenzt, wie viele Schritte ein Lauf machen darf, ein Budget begrenzt, was er kosten darf, die Guard-Rails begrenzen, was er tun darf. Alle drei liegen außerhalb des Modells — sie stehen nicht im Prompt und lassen sich nicht wegargumentieren.

## Weiter

- [Backlog & Lifecycle](/docs/backlog-lifecycle) — die Zustände und wer sie ändert
- [Das Wiki-Gedächtnis](/docs/gedaechtnis) — was der Agent behält
- [Guard-Rails & Kontrolle](/docs/guard-rails) — wo die Plattform eingreift`,
          en: `# The agent model

An agent is not a prompt and not a chat history. It is an entity that persists when nobody is looking: with an identity, a workplace, access, memory and behaviour versioned as code.

## Identity

Every agent has its own identity in the platform — not that of the human who created it. Its access hangs off it, so does its place in the org chart and every trace it leaves. Optionally it gets its own email address, and then a target system sees it for what it is: a sender with a name.

That is the difference from a script running under an administrator's account. When somebody later asks who wrote this comment, there is an answer.

## The first day

An agent carries the moment it was hired. Without one it is a **draft**: created, configurable, visible in the org chart — and not working. No dispatch, no heartbeat, no live webhook, no sandbox, no cost. Tasks may still sit in its backlog and start on the first day.

The kill switch would have been technically sufficient, but it would put two different facts in the same field: "this one was stopped" and "this one has not started yet". And hiring is not a flag but a moment in time — it later sits in the employee profile next to a human's.

That is how every agent comes about that nobody wrote by hand: from a template, from a bundle import, and above all where an *agent* drafts the agent. Hiring stays a human act; there is no action for it that an agent could call.

## Sandbox with a persistent home

Work happens in a container that is created for the wake and disappears afterwards. What stays is \`/home/agent\`: cloned repositories, downloaded attachments, notes, the agent's wiki pages. On the next run it finds its workplace as it left it — the machine around it is new.

The workplace is browsable in the UI: look through it, drop in a template, edit a file, pull a selection out as a ZIP. Also while the agent sleeps, because the home lives on disk and not in the container.

## Behaviour as code

\`SOUL.md\`, \`PLAYBOOKS.md\`, \`CAPABILITIES.md\`, \`ACCESS.md\`, \`HEARTBEAT.md\`, \`ORG.md\` — six files, versioned, with history and rollback. A change in behaviour is therefore something you can read, review and take back, not a deployment.

## Serial before parallel

An agent handles one task at a time. That is a decision, not a limitation: two concurrent runs of the same agent would fight over the same home and the same state, and the resulting bug would not reproduce. Throughput comes from more agents.

## Always reachable, compute only on demand

"Always-on" is a property of the experience, not of the bill. A cheap dispatch loop keeps the agent reachable; the expensive part — container, model — runs only when there is work. An agent in state \`sleeping\` costs nothing but a row in the database.

For work where startup time matters, a **warm sandbox** can be switched on: the container stays up between two wakes. It costs memory and saves seconds — a deliberate trade-off, not a default.

## Limits the agent cannot move

\`max_turns\` bounds how many steps a run may take, a budget bounds what it may cost, guard-rails bound what it may do. All three sit outside the model — they are not in the prompt and cannot be argued away.

## Next

- [Backlog & lifecycle](/en/docs/backlog-and-lifecycle) — the states and who changes them
- [The wiki memory](/en/docs/memory) — what the agent keeps
- [Guard-rails & control](/en/docs/guard-rails) — where the platform steps in`,
        },
        faq: {
          de: [
            {
              q: "Behält ein Agent seine Dateien zwischen zwei Läufen?",
              a: "Ja. Der Container ist ephemer, das Home nicht — es wird als Volume eingehängt und überlebt jeden Neustart. Ein geklontes Repository, eine heruntergeladene Datei oder eine Notiz ist beim nächsten Wecken noch da.",
            },
            {
              q: "Kann ein Agent einen anderen beauftragen?",
              a: "Über den Org-Chart und den Backlog: Ein Agent kann einem anderen eine Aufgabe anlegen und auf deren Ergebnis warten, statt selbst zu tun, wofür ein anderer zuständig ist. Die Eskalation nach oben geht an einen Menschen.",
            },
            {
              q: "Was ist eine warme Sandbox?",
              a: "Ein Container, der zwischen zwei Weckungen stehen bleibt, damit der nächste Lauf nicht kalt startet. Sinnvoll bei Agenten, die im Minutentakt geweckt werden; unnötig bei einem, der dreimal am Tag arbeitet.",
            },
          ],
          en: [
            {
              q: "Does an agent keep its files between runs?",
              a: "Yes. The container is ephemeral, the home is not — it is mounted as a volume and survives every restart. A cloned repository, a downloaded file or a note is still there at the next wake.",
            },
            {
              q: "Can one agent task another?",
              a: "Through the org chart and the backlog: an agent can create a task for another and wait for its result instead of doing what somebody else is responsible for. Escalation upwards goes to a human.",
            },
            {
              q: "What is a warm sandbox?",
              a: "A container that stays up between two wakes so the next run does not start cold. Useful for agents woken every few minutes; unnecessary for one that works three times a day.",
            },
          ],
        },
      },
      {
        slug: { de: "gedaechtnis", en: "memory" },
        title: { de: "Das Wiki-Gedächtnis", en: "The wiki memory" },
        description: {
          de: "Wie ein KI-Agent Wissen behält: verlinkte Markdown-Seiten mit pgvector-Index statt flacher Schnipsel — lesbar, korrigierbar und über Läufe hinweg verdichtet.",
          en: "How a Covey agent retains knowledge: linked Markdown pages with a pgvector index instead of flat snippets — readable, correctable, condensed across runs.",
        },
        body: {
          de: `# Das Wiki-Gedächtnis

Ein Agent, der nach jeder Aufgabe vergisst, was er gelernt hat, wiederholt jeden Fehler und jede Recherche. Covey gibt ihm dafür kein Protokoll und keine Schnipselhalde, sondern ein **Wiki**: verlinkte Markdown-Seiten, eine je Sache, durchsuchbar über einen \`pgvector\`-Index.

## Warum ein Wiki und kein Schnipsel-Speicher

Der übliche Aufbau legt jede Erkenntnis als Textstück ab und sucht später nach Ähnlichkeit. Das findet Formulierungen, keine Zusammenhänge — und genau die machen den Unterschied zwischen einem Agenten, der Fakten wiedergibt, und einem, der eine Sache kennt: Kunde → Ticket → Lösung, Kunde → zuständiger Kollege, Projekt → Repository → offene Baustelle.

Im Wiki hat jede Sache ihre Seite, und die Seiten verweisen mit \`[[Wikilinks]]\` aufeinander. Diese Verweise sind der Graph. Nebenbei ist das Ergebnis für Menschen lesbar, was der zweite Grund ist: Ein Gedächtnis, in das niemand hineinschauen kann, ist im Zweifel nicht korrigierbar.

## Drei Operationen

- **Ablegen** — am Ende einer Aufgabe ordnet der Agent seine Erkenntnisse den richtigen Seiten zu, statt eine neue anzulegen.
- **Nachschlagen** — beim Aufnehmen einer Aufgabe liefert die Vektorsuche die passenden Seiten, dazu den kompakten Index des ganzen Wikis: Er weiß dadurch auch, was er *nicht* weiß.
- **Verdichten** — ein getakteter Lauf führt Duplikate zusammen und repariert tote Verweise, damit das Wissen dichter wird und nicht nur mehr.

## Hybride Speicherung

Die Seiten liegen zweifach: als \`.md\`-Dateien im Home der Sandbox, wo der Agent damit arbeitet wie mit Dateien, und in der Control Plane als maßgebliche Fassung mit Index. Geht die Sandbox verloren, geht kein Wissen verloren.

## Einbettung: eingebaut oder extern

Ohne weitere Konfiguration läuft eine eingebaute Einbettung, die Wortüberlappung misst — das reicht zum Ausprobieren, findet aber die eigene Seite nicht wieder, sobald der Agent anders formuliert. Für echte semantische Suche setzt man \`COVEY_EMBEDDING_PROVIDER\` auf einen Dienst (Voyage, OpenAI) oder betreibt ihn selbst über Ollama; der Bestand wird beim nächsten Start automatisch neu eingebettet.

## Von Hand eingreifen

Das Gedächtnis ist in der Oberfläche sichtbar und änderbar: eine Seite lesen, etwas beitragen, das ein Agent nie erfahren konnte, oder gezielt löschen, was falsch ist. Das ist der praktische Teil des Versprechens — ein Agent, dem man etwas beibringen kann, ohne seinen Prompt anzufassen.

## Auch für Menschen

Derselbe Apparat trägt das Gedächtnis von Menschen: Die [Companion-App](/docs/companion) nutzt dieselben Seiten und denselben Index, nur mit einem Menschen als Eigentümer.

## Weiter

- [Das Agenten-Modell](/docs/agenten-modell) — wo das Gedächtnis im Lauf auftaucht
- [Backlog & Lifecycle](/docs/backlog-lifecycle) — wann abgelegt und nachgeschlagen wird`,
          en: `# The wiki memory

An agent that forgets what it learned after every task repeats every mistake and every piece of research. For that, Covey gives it neither a transcript nor a heap of snippets but a **wiki**: linked Markdown pages, one per thing, searchable through a \`pgvector\` index.

## Why a wiki and not a snippet store

The usual design files every insight as a chunk of text and later searches by similarity. That finds phrasings, not connections — and connections are exactly what separates an agent that recites facts from one that knows a subject: customer → ticket → solution, customer → responsible colleague, project → repository → open issue.

In the wiki every thing has its page, and pages point at each other with \`[[wikilinks]]\`. Those links are the graph. As a side effect the result is readable by humans, which is the second reason: a memory nobody can look into cannot be corrected when it is wrong.

## Three operations

- **File** — at the end of a task the agent files its insights onto the right pages instead of creating a new one.
- **Look up** — when picking up a task, vector search returns the matching pages plus the compact index of the whole wiki: it therefore also knows what it does *not* know.
- **Condense** — a scheduled pass merges duplicates and repairs dead links, so knowledge gets denser rather than merely larger.

## Hybrid storage

Pages live twice: as \`.md\` files in the sandbox home, where the agent works with them as files, and in the control plane as the authoritative copy with the index. Lose the sandbox and no knowledge is lost.

## Embedding: built-in or external

With no further configuration a built-in embedding runs that measures word overlap — enough to try things out, but it will not find its own page again once the agent phrases things differently. For real semantic search, point \`COVEY_EMBEDDING_PROVIDER\` at a service (Voyage, OpenAI) or run one yourself via Ollama; existing pages are re-embedded automatically at the next start.

## Reaching in by hand

The memory is visible and editable in the interface: read a page, contribute something an agent could never have learned, or delete precisely what is wrong. That is the practical part of the promise — an agent you can teach without touching its prompt.

## For humans too

The same apparatus carries human memory: the [Companion app](/en/docs/companion) uses the same pages and the same index, only with a person as the owner.

## Next

- [The agent model](/en/docs/agent-model) — where memory shows up in a run
- [Backlog & lifecycle](/en/docs/backlog-and-lifecycle) — when things are filed and looked up`,
        },
        faq: {
          de: [
            {
              q: "Wo liegen die Gedächtnisdaten meiner Agenten?",
              a: "In Ihrer Postgres-Datenbank und im Home der jeweiligen Sandbox. Nichts davon verlässt Ihre Installation — außer Sie richten bewusst einen fremden Einbettungsdienst ein. Wer das vermeiden will, betreibt die Einbettung selbst über Ollama.",
            },
            {
              q: "Kann ich einen Agenten gezielt etwas vergessen lassen?",
              a: "Ja. Die Wiki-Seiten sind in der Oberfläche einsehbar und löschbar. Weil das Gedächtnis aus lesbaren Seiten besteht und nicht aus Vektoren allein, kann man auch einzelne Sätze korrigieren, statt alles wegzuwerfen.",
            },
            {
              q: "Wächst das Gedächtnis unbegrenzt?",
              a: "Es wird verdichtet: Ein getakteter Wartungslauf führt Duplikate zusammen und repariert tote Verweise. Zusätzlich lässt sich ein plattformweiter Aufräum-Heartbeat einschalten, bei dem jeder Agent sein eigenes Wiki pflegt.",
            },
          ],
          en: [
            {
              q: "Where does my agents' memory live?",
              a: "In your Postgres database and in the home of the respective sandbox. None of it leaves your installation — unless you deliberately configure an external embedding service. If you want to avoid that, run the embedding yourself via Ollama.",
            },
            {
              q: "Can I make an agent forget something specific?",
              a: "Yes. The wiki pages are visible and deletable in the interface. Because the memory consists of readable pages and not of vectors alone, you can also correct individual sentences instead of throwing everything away.",
            },
            {
              q: "Does the memory grow without limit?",
              a: "It gets condensed: a scheduled maintenance pass merges duplicates and repairs dead links. On top of that you can switch on a platform-wide cleanup heartbeat in which every agent tends its own wiki.",
            },
          ],
        },
      },
      {
        slug: { de: "guard-rails", en: "guard-rails" },
        title: { de: "Guard-Rails & Kontrolle", en: "Guard-rails & control" },
        description: {
          de: "Freigaben, Aufzeichnung und Not-Aus: Wie Covey Grenzen für KI-Agenten außerhalb der Runtime erzwingt — fail-closed, nicht über den Prompt, und für jeden Lauf nachvollziehbar.",
          en: "Approvals, recording and a kill switch: how Covey enforces boundaries for AI agents outside the runtime — fail-closed, not through the prompt, and traceable for every run.",
        },
        body: {
          de: `# Guard-Rails & Kontrolle

Die Frage, die über den Einsatz von Agenten in einem Unternehmen entscheidet, ist nicht „kann er das?", sondern „was passiert, wenn er sich irrt?". Covey beantwortet sie mit drei Dingen: erzwungenen Grenzen, Freigaben an den richtigen Stellen und einer Aufzeichnung, die hinterher trägt.

## Warum nicht im Prompt

„Du darfst keine Kundendaten löschen" im Prompt ist eine Bitte. Sie hilft im Normalfall und versagt genau dann, wenn es darauf ankommt: bei einer ungewöhnlichen Formulierung, bei einem Text aus einem Ticket, der wie eine Anweisung klingt, bei einem Modellwechsel.

Guard-Rails stehen deshalb **außerhalb der Runtime**. Der Agent kann sie nicht lesen, nicht überreden und nicht umgehen — er merkt nur, dass eine Aktion abgelehnt wurde. Im Zweifel wird abgelehnt, nicht durchgelassen.

## Die drei Stellen, an denen es greift

- **Secrets-Broker** — welcher Zugang, mit welchem Scope, für welchen Lauf. Ein Token, das nie in die Sandbox gelangt, kann auch nicht mitgenommen werden.
- **Egress** — wohin die Sandbox überhaupt sprechen darf. Mit \`COVEY_EGRESS_ENFORCE\` und harter Netz-Isolation liegt zwischen Agent und Internet ein Proxy, der die Allowlist durchsetzt.
- **Aktionsebene** — welche Aktion erlaubt ist, welche eine Freigabe braucht, welche verboten ist. Regeln greifen global, je Abteilung oder je Agent.

## Freigaben

Kritische Aktionen halten an und warten auf einen Menschen. Die Voreinstellung nach der Installation zeigt das Muster: eine Antwort nach außen zum Kunden braucht eine Freigabe, HR-Systeme sind gesperrt, alles, was \`delete\` heißt, ist hart verboten.

Der Agent bleibt derweil in \`blocked\` — er wartet, ohne Rechenzeit zu verbrauchen, und macht weiter, sobald die Freigabe da ist.

## Aufzeichnung

Jeder Lauf wird mitgeschrieben: Werkzeugaufrufe mit Argumenten, Zielsystem-Aktionen, Freigaben mit Entscheider, bei Browser-Aufgaben Screenshots, dazu Tokens und Kosten je Modell. Das ist der Teil, den Revision und Datenschutz sehen wollen — und der Teil, mit dem man einen Fehlgriff hinterher versteht.

## Not-Aus

Ein Schalter hält einen Agenten an. Ein zweiter hält die gesamte Belegschaft der Organisation an. Beide wirken sofort, laufende Sandboxen werden beendet. Es ist die Antwort auf die Frage, die in jeder Sicherheitsabnahme kommt: „Und wenn wir das alles stoppen müssen?"

## Kosten als Grenze

Ein Budget je Agent ist auch eine Guard-Rail — die gegen den Fehler, der niemandem wehtut außer der Rechnung. Ist es aufgebraucht, arbeitet der Agent nicht weiter.

## Weiter

- [Identität & Secrets](/docs/identitaet-secrets) — wie Zugänge gebrokert werden
- [Betrieb & Deployment](/docs/betrieb) — Egress-Isolation einschalten
- [Zielsysteme & Plugins](/docs/zielsysteme) — welche Aktionen es überhaupt gibt`,
          en: `# Guard-rails & control

The question that decides whether agents get used in a company is not "can it do this?" but "what happens when it gets it wrong?". Covey answers with three things: enforced boundaries, approvals in the right places, and a recording that holds up afterwards.

## Why not in the prompt

"You must not delete customer data" in the prompt is a request. It helps in the normal case and fails exactly when it matters: on an unusual phrasing, on text from a ticket that reads like an instruction, on a change of model.

Guard-rails therefore sit **outside the runtime**. The agent cannot read them, talk them round or bypass them — all it notices is that an action was refused. In case of doubt it is refused, not let through.

## The three places it applies

- **Secrets broker** — which access, at which scope, for which run. A token that never enters the sandbox cannot be carried out of it.
- **Egress** — where the sandbox may talk at all. With \`COVEY_EGRESS_ENFORCE\` and hard network isolation, a proxy sits between agent and internet and enforces the allowlist.
- **Action layer** — which action is allowed, which needs an approval, which is forbidden. Rules apply globally, per department or per agent.

## Approvals

Critical actions stop and wait for a human. The defaults after installation show the pattern: an outbound reply to a customer needs an approval, HR systems are off limits, anything called \`delete\` is hard-denied.

Meanwhile the agent sits in \`blocked\` — it waits without consuming compute and continues as soon as the approval arrives.

## Recording

Every run is written down: tool calls with arguments, target-system actions, approvals with the person who decided, screenshots for browser work, plus tokens and cost per model. This is the part audit and data protection want to see — and the part that lets you understand a misstep afterwards.

## Kill switch

One switch stops an agent. A second stops the organisation's entire workforce. Both take effect immediately and running sandboxes are torn down. It is the answer to the question that comes up in every security review: "And if we have to stop all of this?"

## Cost as a boundary

A budget per agent is a guard-rail too — the one against the mistake that hurts nobody except the invoice. Once it is used up, the agent stops working.

## Next

- [Identity & secrets](/en/docs/identity-and-secrets) — how access is brokered
- [Operations & deployment](/en/docs/operations) — switching on egress isolation
- [Target systems & plugins](/en/docs/target-systems) — which actions exist in the first place`,
        },
        faq: {
          de: [
            {
              q: "Kann ein Agent seine eigenen Guard-Rails ändern?",
              a: "Nein. Sie liegen in der Control Plane, nicht in seiner Konfiguration, und werden außerhalb der Runtime durchgesetzt. Ändern kann sie ein Mensch mit der passenden Rolle — und diese Änderung steht im Audit-Trail.",
            },
            {
              q: "Was ist der Unterschied zwischen Egress-Allowlist und Freigabe?",
              a: "Die Allowlist entscheidet, mit welchen Hosts die Sandbox überhaupt sprechen darf — technisch, im Netz. Eine Freigabe entscheidet über eine einzelne fachliche Aktion, etwa eine Antwort an einen Kunden. Das eine ist eine Mauer, das andere ein Vier-Augen-Prinzip.",
            },
            {
              q: "Hilft das gegen Prompt-Injection aus einem Ticket?",
              a: "Es ist genau der Grund für den Aufbau. Ein Text aus einem Zielsystem kann das Modell in die Irre führen — er kann aber keine Regel ändern, die außerhalb des Modells liegt. Die Aktion scheitert dann an der Guard-Rail und landet als abgelehnter Versuch im Recording.",
            },
            {
              q: "Wie lange werden Aufzeichnungen aufbewahrt?",
              a: "So lange, wie Sie es einstellen — es ist Ihre Datenbank. Die Aufbewahrungsdauer ist konfigurierbar, und was für Ihre Revision nötig ist, entscheiden Sie, nicht die Plattform.",
            },
          ],
          en: [
            {
              q: "Can an agent change its own guard-rails?",
              a: "No. They live in the control plane, not in its configuration, and are enforced outside the runtime. A human with the right role can change them — and that change appears in the audit trail.",
            },
            {
              q: "What is the difference between an egress allowlist and an approval?",
              a: "The allowlist decides which hosts the sandbox may talk to at all — technically, on the network. An approval decides a single business action, such as a reply to a customer. One is a wall, the other is four eyes.",
            },
            {
              q: "Does this help against prompt injection from a ticket?",
              a: "It is precisely the reason for the design. Text from a target system can mislead the model — but it cannot change a rule that lives outside the model. The action then fails at the guard-rail and lands in the recording as a refused attempt.",
            },
            {
              q: "How long are recordings kept?",
              a: "As long as you configure — it is your database. Retention is configurable, and what your audit needs is your decision, not the platform's.",
            },
          ],
        },
      },
      {
        slug: { de: "identitaet-secrets", en: "identity-and-secrets" },
        title: { de: "Identität & Secrets", en: "Identity & secrets" },
        description: {
          de: "Agenten-Identität, RBAC und der Secrets-Broker: Zugänge werden zur Laufzeit ausgestellt, kurzlebig und gescopt. Langlebige Secrets kommen nie in die Sandbox.",
          en: "Agent identity, RBAC and the secrets broker: access is issued at runtime, short-lived and scoped. Long-lived secrets never enter the sandbox; storage is AES-GCM encrypted.",
        },
        body: {
          de: `# Identität & Secrets

Die unangenehmste Frage an jede Agenten-Installation lautet: Wo liegt eigentlich das Passwort, mit dem der Agent ins Ticketsystem kommt? Die übliche Antwort — in einer Umgebungsvariable im Container — ist der Grund, warum die Sicherheitsabteilung Nein sagt.

## Der Grundsatz

**Niemals langlebige Secrets in die Sandbox.** Zugänge werden zur Laufzeit gebrokert, kurzlebig und auf den Zweck geschnitten. Was der Agent bekommt, gilt für diesen Lauf und für dieses System — nicht darüber hinaus.

## Agenten-Identität

Ein Agent ist ein eigenständiges Subjekt, kein Prozess unter einem Sammelkonto. Er hat eine Identität, an der Zugänge, Org-Chart-Position und jede Spur hängen. In Zielsystemen tritt er unter eigenem Namen auf — nachvollziehbar bis in die Kommentarspalte eines Tickets.

## Menschen und Rollen

Auf derselben Organisation arbeiten Menschen mit verschiedenen Rollen: Org-Admin, Agenten-Eigentümer, Betrachter, Revision. Wer welche Agenten sieht, wer Secrets hinterlegen darf, wer Freigaben erteilen kann — das entscheidet die Rolle, und jede administrative Handlung steht im Audit-Trail.

Angemeldet wird eingebaut über JWT und Argon2id. Wer ein Unternehmens-Login will, hängt einen OIDC-Anbieter an dieselbe Schnittstelle — Keycloak, Entra, was im Haus steht.

## Wie Secrets gespeichert werden

In Postgres-Spalten, verschlüsselt mit AES-GCM. Der Schlüssel ist der \`COVEY_MASTER_KEY\` der Instanz; er steht in der Umgebung, nicht in der Datenbank. Die Verschlüsselung bindet jeden Wert zusätzlich an seine Organisation, damit ein Ciphertext nicht in einer anderen Organisation gelesen werden kann.

Praktische Folge: Der Master-Key ist so wichtig wie das Datenbank-Passwort, und ein Verlust ist endgültig. Sichern.

## Kein Klartext in der Oberfläche

Ein hinterlegtes Secret lässt sich ersetzen, aber nicht wieder anzeigen. Das ist Absicht — ein Wert, den man auslesen kann, wandert früher oder später in eine Chat-Nachricht.

## Prüfung beim Hinterlegen

Bekannte Credentials werden beim Speichern sofort gegen ihr System geprüft. Ein abgelaufener Abo-Token fällt damit an der Stelle auf, an der man ihn eingibt — und nicht eine Stunde später als rätselhafter Fehlschlag im Lauf eines Agenten.

## Kapazität statt Passwort-Zettel

Mehrere Zugänge zur selben Engine — drei Abo-Sitze und ein API-Schlüssel — sind keine Sicherheitsfrage, sondern eine kaufmäntische. Covey bildet sie als [Arbeitsplatz mit Merit Order](/docs/kernkonzepte) ab: bezahltes Kontingent zuerst, Verbrauchsabrechnung als Spitzenlast.

## Weiter

- [Guard-Rails & Kontrolle](/docs/guard-rails) — was mit dem Zugang getan werden darf
- [Zielsysteme & Plugins](/docs/zielsysteme) — welche Systeme Zugänge brauchen
- [Betrieb & Deployment](/docs/betrieb) — Master-Key, HTTPS, Sicherungen`,
          en: `# Identity & secrets

The most awkward question you can ask any agent installation is: where exactly is the password the agent uses to get into the ticket system? The usual answer — in an environment variable inside the container — is why the security team says no.

## The principle

**Never put long-lived secrets in the sandbox.** Access is brokered at runtime, short-lived and cut to purpose. What the agent gets applies to this run and this system — not beyond it.

## Agent identity

An agent is a subject of its own, not a process under a shared account. It has an identity that carries its access, its position in the org chart and every trace it leaves. In target systems it appears under its own name — traceable right down to the comment column of a ticket.

## People and roles

People with different roles work on the same organisation: platform admin, agent owner, viewer, audit. Who sees which agents, who may deposit secrets, who may grant approvals — the role decides, and every administrative action lands in the audit trail.

Sign-in is built in via JWT and Argon2id. If you want a company login, hang an OIDC provider off the same interface — Keycloak, Entra, whatever the house runs.

## How secrets are stored

In Postgres columns, encrypted with AES-GCM. The key is the instance's \`COVEY_MASTER_KEY\`; it lives in the environment, not in the database. The encryption additionally binds every value to its organisation, so a ciphertext cannot be read inside a different one.

Practical consequence: the master key matters as much as the database password, and losing it is final. Back it up.

## No plaintext in the interface

A stored secret can be replaced but not shown again. That is deliberate — a value you can read out ends up in a chat message sooner or later.

## Checked when deposited

Known credentials are verified against their system the moment they are saved. An expired subscription token therefore shows up where you type it — not an hour later as a puzzling failure inside an agent's run.

## Capacity instead of a list of passwords

Several credentials for the same engine — three subscription seats and an API key — are not a security question but a commercial one. Covey models them as a [workplace with a merit order](/en/docs/core-concepts): capacity already paid for first, metered billing as peak load.

## Next

- [Guard-rails & control](/en/docs/guard-rails) — what may be done with that access
- [Target systems & plugins](/en/docs/target-systems) — which systems need access
- [Operations & deployment](/en/docs/operations) — master key, HTTPS, backups`,
        },
        faq: {
          de: [
            {
              q: "Kann ich HashiCorp Vault statt des eingebauten Speichers verwenden?",
              a: "Der Speicher liegt hinter einem schmalen Interface, das genau dafür gezogen wurde: builtin (AES-GCM in Postgres) oder ein externer Anbieter. Der eingebaute Weg ist die Voreinstellung, damit eine Installation ohne Vault vollständig ist.",
            },
            {
              q: "Sieht der Agent meinen API-Schlüssel?",
              a: "Den Modell-Schlüssel bekommt die Runtime für den Lauf gestellt — ohne ihn kann sie das Modell nicht rufen. Zugänge zu Zielsystemen dagegen laufen über den Aktions-Proxy: Der Agent nennt die Aktion, den Token setzt die Control Plane ein.",
            },
            {
              q: "Was passiert bei einem abgelaufenen Token?",
              a: "Er wird beim Hinterlegen geprüft und im Betrieb erkannt: Ein abgelehnter Wert wird geparkt, der Agent weicht auf eine andere Kapazität aus, wenn es eine gibt, und im Recording steht der Grund.",
            },
          ],
          en: [
            {
              q: "Can I use HashiCorp Vault instead of the built-in store?",
              a: "The store sits behind a narrow interface drawn for exactly that: builtin (AES-GCM in Postgres) or an external provider. The built-in route is the default so that an installation without Vault is complete.",
            },
            {
              q: "Does the agent see my API key?",
              a: "The runtime is given the model key for the run — without it, it cannot call the model. Access to target systems, by contrast, goes through the action proxy: the agent names the action, the control plane inserts the token.",
            },
            {
              q: "What happens with an expired token?",
              a: "It is checked when deposited and detected in operation: a rejected value is parked, the agent moves to other capacity if there is any, and the recording states the reason.",
            },
          ],
        },
      },
      {
        slug: { de: "backlog-lifecycle", en: "backlog-and-lifecycle" },
        title: { de: "Backlog & Lifecycle", en: "Backlog & lifecycle" },
        description: {
          de: "Wie Arbeit zu einem Agenten kommt: Backlog als eigenständiges Objekt, Weckquellen über Webhook und Heartbeat, die Zustände open, in_progress, blocked, done.",
          en: "How work reaches an agent: the backlog as an object of its own, wake sources via webhook and heartbeat, and the states open, in_progress, blocked, done.",
        },
        body: {
          de: `# Backlog & Lifecycle

Arbeit kommt bei Covey nicht aus einem Chat-Fenster, sondern aus einem Backlog. Das ist keine Geschmacksfrage: Eine Aufgabe, die ein eigenes Objekt ist, hat einen Zustand, einen Verantwortlichen, eine Historie und ein Ende — ein Chat-Verlauf hat nichts davon.

## Die Zustände

- \`open\` — angelegt, wartet auf Bearbeitung
- \`in_progress\` — der Agent arbeitet gerade daran
- \`blocked\` — wartet auf eine Antwort von außen
- \`done\`, \`failed\`, \`cancelled\` — abgeschlossen, gescheitert, zurückgezogen

Auf dem Board sind die Spalten frei konfigurierbar; darunter bleiben es diese Zustände.

## Der Lauf

Geweckt wird ein Agent von einer neuen Aufgabe, einem Webhook, einem Heartbeat oder von Hand. Dann läuft immer dieselbe Kette: Sandbox hoch → Aufgabe aufnehmen (dabei im Gedächtnis nachschlagen) → arbeiten → Ergebnis festhalten und ins Gedächtnis ablegen → Sandbox runter, Agent schläft.

Die Kette ist bewusst kurz. Was länger dauert als ein Lauf, wird nicht durch Warten gelöst, sondern durch \`blocked\`.

## Warum blocked der interessante Zustand ist

Ein Agent, der auf eine Kundenantwort wartet, darf keine Rechenzeit verbrauchen — und er darf die Antwort auch nicht verpassen. Also wird die Aufgabe geparkt und mit einem Korrelationsschlüssel versehen, etwa \`zammad:ticket:4711\`.

Trifft später ein Ereignis mit demselben Schlüssel ein, weckt es genau diese Aufgabe wieder, mit ihrem Kontext. Zwischen Frage und Antwort können Minuten liegen oder drei Tage; die Kosten dafür sind null.

## Weckquellen

- **Webhook** — das Zielsystem ruft Covey, wenn dort etwas passiert. Der schnellste Weg, und der einzige ohne Leerlauf.
- **Heartbeat** — ein Takt aus der \`HEARTBEAT.md\`, etwa \`- alle: 30m titel: Posteingang aufgabe: Neue Tickets sichten.\` Mit \`nur-wenn:\` fragt die Control Plane vorher billig nach, ob es überhaupt etwas zu tun gibt, und lässt den Agenten sonst schlafen.
- **Von Hand** — „Wecken" in der Oberfläche, oder ein API-Aufruf.

## Turn-Limit und Budget

Ein Lauf hat eine Obergrenze an Schritten (\`max_turns\`). Wird sie erreicht, bricht der Lauf kontrolliert ab, statt sich im Kreis zu drehen — meist ein Zeichen, dass die Aufgabe zu groß geschnitten ist. Häufige Abbrüche dieser Art meldet auch die Konfigurationsprüfung.

## Weiter

- [Zielsysteme & Plugins](/docs/zielsysteme) — woher die Weckereignisse kommen
- [Das Agenten-Modell](/docs/agenten-modell) — was zwischen zwei Läufen bleibt
- [Guard-Rails & Kontrolle](/docs/guard-rails) — wo ein Lauf anhält`,
          en: `# Backlog & lifecycle

In Covey, work does not arrive through a chat window but through a backlog. That is not a matter of taste: a task that is an object of its own has a state, an owner, a history and an end — a chat history has none of those.

## The states

- \`open\` — created, waiting to be worked on
- \`in_progress\` — the agent is working on it right now
- \`blocked\` — waiting for an answer from outside
- \`done\`, \`failed\`, \`cancelled\` — finished, failed, withdrawn

On the board the columns are freely configurable; underneath, these states remain.

## The run

An agent is woken by a new task, a webhook, a heartbeat or by hand. Then the same chain always follows: sandbox up → pick up the task (looking things up in memory) → work → record the result and file it into memory → sandbox down, agent asleep.

The chain is deliberately short. Anything that takes longer than a run is not solved by waiting but by \`blocked\`.

## Why blocked is the interesting state

An agent waiting for a customer's reply must not burn compute — and must not miss the reply either. So the task is parked and given a correlation key, for example \`zammad:ticket:4711\`.

When an event with the same key arrives later, it wakes exactly that task again, with its context. Minutes or three days may pass between question and answer; the cost of waiting is zero.

## Wake sources

- **Webhook** — the target system calls Covey when something happens there. The fastest route, and the only one without idle polling.
- **Heartbeat** — a schedule from \`HEARTBEAT.md\`, e.g. \`- alle: 30m titel: Inbox aufgabe: Triage new tickets.\` With \`nur-wenn:\` the control plane cheaply checks first whether there is anything to do at all, and otherwise lets the agent sleep.
- **By hand** — "Wake" in the interface, or an API call.

## Turn limit and budget

A run has an upper bound on steps (\`max_turns\`). When it is reached the run aborts in a controlled way instead of going in circles — usually a sign the task was cut too large. Frequent aborts of this kind are also reported by the configuration lint.

## Next

- [Target systems & plugins](/en/docs/target-systems) — where wake events come from
- [The agent model](/en/docs/agent-model) — what remains between two runs
- [Guard-rails & control](/en/docs/guard-rails) — where a run stops`,
        },
        faq: {
          de: [
            {
              q: "Wie lange kann eine Aufgabe blockiert warten?",
              a: "Unbegrenzt, und ohne laufende Kosten. Der Agent schläft; erst das passende Ereignis — eine Kundenantwort, ein Kommentar, eine Freigabe — weckt genau diese Aufgabe wieder auf.",
            },
            {
              q: "Was passiert, wenn ein Lauf mitten in einer Aufgabe abbricht?",
              a: "Die Aufgabe geht zurück in den Backlog und wird beim nächsten Wecken erneut aufgenommen. Beim Start der Control Plane werden verwaiste Aufgaben eingesammelt, damit nichts liegen bleibt, weil ein Prozess neu gestartet wurde.",
            },
            {
              q: "Kann ich Aufgaben von außen anlegen?",
              a: "Ja, über die API oder über ein Zielsystem: Ein Ticket, eine E-Mail oder ein Issue kann eine Aufgabe erzeugen. Genau das ist der Normalfall — von Hand angelegte Aufgaben sind eher die Ausnahme.",
            },
          ],
          en: [
            {
              q: "How long can a task stay blocked?",
              a: "Indefinitely, and with no running cost. The agent sleeps; only the matching event — a customer reply, a comment, an approval — wakes that particular task again.",
            },
            {
              q: "What happens if a run breaks off mid-task?",
              a: "The task goes back into the backlog and is picked up again at the next wake. When the control plane starts, orphaned tasks are collected so nothing is left behind just because a process restarted.",
            },
            {
              q: "Can I create tasks from outside?",
              a: "Yes, through the API or through a target system: a ticket, an email or an issue can create a task. That is in fact the normal case — hand-written tasks are the exception.",
            },
          ],
        },
      },
    ],
  },
  {
    id: "integrationen",
    title: { de: "Integrationen & Betrieb", en: "Integrations & operations" },
    pages: [
      {
        slug: { de: "zielsysteme", en: "target-systems" },
        title: { de: "Zielsysteme & Plugins", en: "Target systems & plugins" },
        description: {
          de: "Zammad, Salesforce, Jira, Confluence, GitHub, GitLab, Teams, SharePoint, Nextcloud, E-Mail, Browser und MCP: Wie Covey Agenten über Plugins an Fremdsysteme anbindet.",
          en: "Zammad, Salesforce, Jira, Confluence, GitHub, GitLab, Teams, SharePoint, Nextcloud, email, browser and MCP: how Covey connects agents to foreign systems through plugins.",
        },
        body: {
          de: `# Zielsysteme & Plugins

Ein Agent wird nützlich, wenn er in den Systemen arbeitet, in denen ohnehin gearbeitet wird. In Covey ist jedes davon ein **Plugin**: Es beschreibt seine Aktionen, seine Scopes und seine Weckereignisse, und der Kern kennt keine Sonderfälle.

## Was mitgeliefert wird

- **Zammad** — Tickets sichten, intern notieren, nach außen antworten; Webhook als Weckquelle
- **Salesforce Service Cloud** — Fälle mit ihrer ganzen Konversation, Antwort als Notiz, Portal-Kommentar oder Mail
- **GitHub** und **GitLab** — Issues, Pull- und Merge-Requests, Pipelines, Checkout in der Sandbox
- **Jira** — das Ticket neben dem Repository: per JQL suchen, übernehmen, durch den Workflow bewegen; Cloud und Data Center
- **Confluence** — die Dokumentation, an der beides hängt: Seiten als Markdown lesen und schreiben. Weckt niemanden — der Agent kommt her, während er an etwas anderem arbeitet
- **E-Mail (IMAP/SMTP)** — ein Postfach als Weckquelle, Antworten im Thread
- **Microsoft Teams** — Chat als Kanal zwischen Mensch und Agent
- **SharePoint** über Microsoft Graph und **Nextcloud** über WebDAV — Dateien
- **Browser** — headless Chrome für Oberflächen ohne API, mit Screenshots im Recording
- **MCP** — beliebige Model-Context-Protocol-Server als Werkzeugquelle
- **VulnDB** — bekannte Schwachstellen in Abhängigkeiten (npm, Composer, Dart/Flutter)

## Der Aktions-Proxy

Der Agent ruft ein Zielsystem nicht direkt. Er nennt eine Aktion, die Control Plane führt sie aus und gibt das Ergebnis zurück. Drei Dinge fallen dabei ab, die man sonst nachbauen müsste: das Token bleibt draußen, die Guard-Rails greifen an einer Stelle, und die Aktion steht im Recording.

## Weckereignisse

Der beste Weg ist der Webhook: Passiert im Ticketsystem etwas, ruft es Covey, und der zuständige Agent wacht auf. Wo es keinen Webhook gibt, hilft ein Heartbeat mit \`nur-wenn:\` — die Control Plane prüft billig, ob es Arbeit gibt, und weckt sonst niemanden.

Ereignisse tragen einen Korrelationsschlüssel, damit eine wartende Aufgabe wieder aufgenommen wird und nicht eine neue entsteht.

## Zugänge im Agenten

Welche Systeme ein Agent nutzen darf, steht in seiner \`ACCESS.md\`:

\`\`\`
- system: zammad scope: read,write,comment
- system: gitlab scope: read,write
\`\`\`

Die Werte dahinter — URL, Token — liegen bei der Organisation, nicht beim Agenten, und werden zur Laufzeit gebrokert.

## Ein eigenes System anbinden

Vier Wege, je nachdem, wie weit die Anbindung tragen soll. Am schnellsten ist ein **MCP-Server** — der Agent bekommt dessen Werkzeuge, ohne dass an Covey etwas geändert wird. Ein **Manifest** beschreibt eine REST-API als JSON-Datei und wird zur Laufzeit installiert, ohne Neubau. Ein **WebAssembly-Modul** bringt eigene Logik mit und kommt ebenfalls aus dem Katalog. Und ein **kompiliertes Plugin** in Go ist der Weg, wenn die Integration übersetzen muss, was das Fremdsystem speichert — Jira und Confluence tun genau das mit ihren Dokumentformaten. Alle vier bedienen dieselbe Schnittstelle; die mitgelieferten liegen im Plugin-Pack und sind die Vorlage.

## Weiter

- [Guard-Rails & Kontrolle](/docs/guard-rails) — Freigaben für kritische Aktionen
- [Identität & Secrets](/docs/identitaet-secrets) — wie die Zugänge gestellt werden
- [Backlog & Lifecycle](/docs/backlog-lifecycle) — was ein Weckereignis auslöst`,
          en: `# Target systems & plugins

An agent becomes useful when it works in the systems where the work already happens. In Covey each of them is a **plugin**: it declares its actions, its scopes and its wake events, and the core knows no special cases.

## What ships with it

- **Zammad** — triage tickets, add internal notes, reply externally; webhook as a wake source
- **Salesforce Service Cloud** — cases with their whole conversation; reply as a note, a portal comment or a mail
- **GitHub** and **GitLab** — issues, pull and merge requests, pipelines, checkout inside the sandbox
- **Jira** — the ticket beside the repository: search by JQL, take it on, move it through its workflow; Cloud and Data Center
- **Confluence** — the documentation both of them hang off: pages read and written as Markdown. Wakes nobody — an agent comes here while it works on something else
- **Email (IMAP/SMTP)** — a mailbox as a wake source, replies in-thread
- **Microsoft Teams** — chat as the channel between human and agent
- **SharePoint** via Microsoft Graph and **Nextcloud** via WebDAV — files
- **Browser** — headless Chrome for interfaces without an API, with screenshots in the recording
- **MCP** — any Model Context Protocol server as a source of tools
- **VulnDB** — known vulnerabilities in dependencies (npm, Composer, Dart/Flutter)

## The action proxy

The agent does not call a target system directly. It names an action, the control plane executes it and returns the result. Three things fall out of that which you would otherwise have to build: the token stays outside, the guard-rails apply in one place, and the action lands in the recording.

## Wake events

The best route is the webhook: when something happens in the ticket system, it calls Covey and the responsible agent wakes up. Where there is no webhook, a heartbeat with \`nur-wenn:\` helps — the control plane checks cheaply whether there is work and otherwise wakes nobody.

Events carry a correlation key so that a waiting task is resumed instead of a new one being created.

## Access in the agent

Which systems an agent may use is stated in its \`ACCESS.md\`:

\`\`\`
- system: zammad scope: read,write,comment
- system: gitlab scope: read,write
\`\`\`

The values behind them — URL, token — belong to the organisation, not to the agent, and are brokered at runtime.

## Connecting your own system

Four routes, depending on how far the connection has to carry. The quickest is an **MCP server** — the agent gets its tools without anything in Covey changing. A **manifest** describes a REST API as a JSON file and installs at runtime, with no rebuild. A **WebAssembly module** brings logic of its own and comes from the catalogue too. And a **compiled plugin** in Go is the route when the integration has to translate what the foreign system stores — Jira and Confluence do exactly that with their document formats. All four serve the same interface; the ones that ship sit in the plugin pack and are the template.

## Next

- [Guard-rails & control](/en/docs/guard-rails) — approvals for critical actions
- [Identity & secrets](/en/docs/identity-and-secrets) — how access is provided
- [Backlog & lifecycle](/en/docs/backlog-and-lifecycle) — what a wake event triggers`,
        },
        faq: {
          de: [
            {
              q: "Kann ich ein System anbinden, für das es kein Plugin gibt?",
              a: "Ja — am schnellsten über einen MCP-Server, dessen Werkzeuge der Agent dann nutzen kann. Wer Weckereignisse, Scopes und Aktionen im Recording braucht, schreibt ein Plugin: als Manifest (JSON, ohne Neubau installierbar), als WebAssembly-Modul oder kompiliert in Go. Die mitgelieferten liegen im Plugin-Pack und sind die Vorlage.",
            },
            {
              q: "Braucht jedes Zielsystem einen Webhook?",
              a: "Nein. Ohne Webhook arbeitet der Agent über einen Heartbeat, idealerweise mit \`nur-wenn:\` — dann prüft die Control Plane vorab billig, ob überhaupt Arbeit anliegt, statt den Agenten ins Leere zu wecken.",
            },
            {
              q: "Wie kommt der Agent an ein Git-Repository?",
              a: "Über das GitHub- oder GitLab-Plugin: Der Checkout passiert in der Sandbox, mit einem gebrokerten, kurzlebigen Zugang. Das Arbeitsverzeichnis bleibt im Home des Agenten und steht beim nächsten Lauf wieder bereit.",
            },
          ],
          en: [
            {
              q: "Can I connect a system that has no plugin?",
              a: "Yes — fastest through an MCP server, whose tools the agent can then use. If you need wake events, scopes and actions in the recording, write a plugin: as a manifest (JSON, installable with no rebuild), as a WebAssembly module, or compiled in Go. The ones that ship sit in the plugin pack and are the template.",
            },
            {
              q: "Does every target system need a webhook?",
              a: "No. Without one the agent works from a heartbeat, ideally with \`nur-wenn:\` — the control plane then checks cheaply in advance whether there is any work, instead of waking the agent for nothing.",
            },
            {
              q: "How does the agent get to a Git repository?",
              a: "Through the GitHub or GitLab plugin: the checkout happens inside the sandbox with brokered, short-lived access. The working directory stays in the agent's home and is there again on the next run.",
            },
          ],
        },
      },
      {
        slug: { de: "betrieb", en: "operations" },
        title: { de: "Betrieb & Deployment", en: "Operations & deployment" },
        description: {
          de: "Covey im Betrieb: ein Binary plus Postgres, Port 8494, Migrationen beim Start, HTTPS über Reverse-Proxy, Egress-Isolation, Sicherungen und Updates.",
          en: "Running Covey: one binary plus Postgres, port 8494, migrations at startup, HTTPS through a reverse proxy, egress isolation, backups and updates without ceremony.",
        },
        body: {
          de: `# Betrieb & Deployment

Covey ist bewusst langweilig zu betreiben: ein Prozess, eine Datenbank, ein Port. Alles, was darüber hinausgeht, ist optional.

## Was läuft

- **covey** — die Control Plane, hört auf **8494** (API, Oberfläche, Daemon-WebSocket)
- **PostgreSQL** mit \`pgvector\` — Zustand, Queue, Gedächtnis, Secrets
- **Docker** — für die Sandboxen, gestartet über den Socket des Hosts
- optional **covey-runner** — wenn die Data Plane auf mehrere Maschinen soll

Migrationen laufen beim Start automatisch, abgesichert über ein Advisory Lock; zwei gleichzeitig startende Instanzen migrieren nicht gegeneinander.

## Die Adressen auseinanderhalten

Zwei Variablen sehen ähnlich aus und meinen Gegenteiliges:

- \`COVEY_PUBLIC_URL\` zeigt **nach innen** — unter dieser Adresse erreichen die **Sandboxen** die Control Plane. Steht hier die Domain der Website, wählen die Container über das offene Netz zurück und scheitern an der Egress-Allowlist.
- \`COVEY_SITE_URL\` zeigt **nach außen** — Website, Sitemap, kopierbare Webhook-URLs. Leer lassen ist der Normalfall; der Server leitet sie aus dem Request ab.

Beim Start warnt Covey, wenn diese beiden Rollen vertauscht aussehen.

## HTTPS

Ein Reverse-Proxy davor, TLS dort terminieren, \`COVEY_PUBLIC_URL\` beziehungsweise \`COVEY_SITE_URL\` passend setzen. Das sichere Cookie schaltet sich dann von selbst ein. Für die Datenbank \`sslmode=require\` oder höher.

## Egress-Isolation

Zwei Stufen. **Kooperativ**: Der Datenverkehr der Sandbox läuft über einen Proxy, der die Allowlist durchsetzt. **Hart** (\`COVEY_EGRESS_ISOLATION=network\`): Die Sandbox hängt in einem internen Netz ohne Internet, und der Proxy-Container ist der einzige Weg hinaus — nicht mehr umgehbar. Für die harte Stufe wird ein zweites Image gebaut.

## Sicherungen

Zwei Dinge: die Postgres-Datenbank und der \`COVEY_MASTER_KEY\`. Ohne den Schlüssel ist ein Datenbank-Backup zwar vollständig, aber jedes Secret darin unlesbar. Die Homes der Agenten (\`COVEY_DATA_DIR\`) sind nützlich, aber wiederherstellbar — sie enthalten Arbeitsstände, keinen unersetzlichen Zustand.

## Updates

Neues Binary oder neues Image, Prozess neu starten; die Migrationen laufen mit. Nach einem Update lohnt ein Blick mit

\`\`\`
covey config lint
\`\`\`

Das ändert nichts, sondern meldet Konfigurationen, die mit der neuen Fassung nicht mehr gut zusammengehen: zu kurze Heartbeat-Takte, blockierende Aufgaben an Systemen ohne Webhook, Boards mit Spalten, die Aufgaben statt Zuständen benennen, häufige Turn-Abbrüche. Der Exit-Code ist 1, wenn es Befunde gibt — ein Upgrade-Skript kann darauf reagieren.

## Beobachten

\`covey version\` beantwortet, welcher Stand läuft; dieselbe Angabe steht in der Startzeile und unten in der Oberfläche. Kosten, Tokens und Läufe stehen in der Oberfläche je Agent und je Modell. Das Request-Log zeigt die HTTP-Ränder — was hereinkam, was hinausging.

## Weiter

- [Schnellstart (Docker)](/docs/schnellstart) — die Installation
- [Architektur-Überblick](/docs/architektur) — warum die Sandbox ein Geschwister-Container ist
- [Guard-Rails & Kontrolle](/docs/guard-rails) — Not-Aus und Aufzeichnung`,
          en: `# Operations & deployment

Covey is deliberately boring to run: one process, one database, one port. Everything beyond that is optional.

## What runs

- **covey** — the control plane, listening on **8494** (API, interface, daemon WebSocket)
- **PostgreSQL** with \`pgvector\` — state, queue, memory, secrets
- **Docker** — for the sandboxes, started through the host's socket
- optionally **covey-runner** — when the data plane should span several machines

Migrations run automatically at startup, guarded by an advisory lock; two instances starting at once do not migrate against each other.

## Keeping the addresses apart

Two variables look alike and mean opposite things:

- \`COVEY_PUBLIC_URL\` points **inwards** — the address at which the **sandboxes** reach the control plane. Put the website's domain here and the containers dial back over the open network and fail at the egress allowlist.
- \`COVEY_SITE_URL\` points **outwards** — website, sitemap, copyable webhook URLs. Leaving it empty is the normal case; the server derives it from the request.

At startup Covey warns when these two roles look swapped.

## HTTPS

A reverse proxy in front, TLS terminated there, \`COVEY_PUBLIC_URL\` and \`COVEY_SITE_URL\` set accordingly. The secure cookie then switches itself on. For the database, \`sslmode=require\` or higher.

## Egress isolation

Two levels. **Cooperative**: the sandbox's traffic goes through a proxy that enforces the allowlist. **Hard** (\`COVEY_EGRESS_ISOLATION=network\`): the sandbox sits on an internal network without internet, and the proxy container is the only way out — no longer bypassable. The hard level needs a second image built.

## Backups

Two things: the Postgres database and the \`COVEY_MASTER_KEY\`. Without the key a database backup is complete but every secret in it is unreadable. The agents' homes (\`COVEY_DATA_DIR\`) are useful but reproducible — they hold work in progress, not irreplaceable state.

## Updates

New binary or new image, restart the process; the migrations come along. After an update it is worth a look with

\`\`\`
covey config lint
\`\`\`

It changes nothing and reports configurations that no longer sit well with the new version: heartbeat intervals that are too short, blocking tasks on systems without a webhook, boards with columns naming tasks instead of states, frequent turn-limit aborts. The exit code is 1 when there are findings — an upgrade script can react to it.

## Observing

\`covey version\` answers which build is running; the same information appears in the startup line and at the bottom of the interface. Cost, tokens and runs are in the interface per agent and per model. The request log shows the HTTP edges — what came in, what went out.

## Next

- [Quick start (Docker)](/en/docs/quickstart) — the installation
- [Architecture overview](/en/docs/architecture) — why the sandbox is a sibling container
- [Guard-rails & control](/en/docs/guard-rails) — kill switch and recording`,
        },
        faq: {
          de: [
            {
              q: "Wie viel Arbeitsspeicher braucht ein Covey-Server?",
              a: "Die Control Plane selbst ist genügsam — ein Go-Prozess neben Postgres. Der Bedarf entsteht in den Sandboxen: Jede läuft mit einer Node-Runtime, bei Browser-Aufgaben zusätzlich mit chromium. Planen Sie nach gleichzeitig wachen Agenten, nicht nach der Anzahl angelegter.",
            },
            {
              q: "Kann ich Covey hinter einen Reverse-Proxy stellen?",
              a: "Ja, das ist der vorgesehene Weg für HTTPS. Wichtig ist nur, \`COVEY_PUBLIC_URL\` nicht auf die öffentliche Domain zu setzen, wenn die Sandboxen sie nicht erreichen — diese Variable zeigt nach innen.",
            },
            {
              q: "Wie aktualisiere ich ohne Datenverlust?",
              a: "Binary oder Image tauschen und neu starten; die Migrationen laufen beim Start und sind gegen parallele Starts abgesichert. Vorher die Datenbank sichern, den Master-Key ohnehin aufbewahren. Danach \`covey config lint\` laufen lassen.",
            },
            {
              q: "Läuft Covey auch ohne Internetzugang?",
              a: "Die Plattform ja. Die Agenten brauchen den Modell-Endpunkt — bei harter Egress-Isolation steht der Proxy davor und lässt genau die erlaubten Hosts durch. Ein selbst betriebener Einbettungsdienst hält zusätzlich die Wiki-Suche im Haus.",
            },
          ],
          en: [
            {
              q: "How much memory does a Covey server need?",
              a: "The control plane itself is frugal — a Go process next to Postgres. The demand comes from the sandboxes: each runs a Node runtime, plus chromium for browser work. Size by the number of agents awake at once, not by the number created.",
            },
            {
              q: "Can I put Covey behind a reverse proxy?",
              a: "Yes, that is the intended route for HTTPS. The one thing to get right is not setting \`COVEY_PUBLIC_URL\` to the public domain when the sandboxes cannot reach it — that variable points inwards.",
            },
            {
              q: "How do I update without losing data?",
              a: "Swap the binary or image and restart; migrations run at startup and are guarded against concurrent starts. Back up the database first, and keep the master key anyway. Afterwards run \`covey config lint\`.",
            },
            {
              q: "Does Covey run without internet access?",
              a: "The platform does. The agents need the model endpoint — with hard egress isolation the proxy sits in front and lets exactly the allowed hosts through. A self-hosted embedding service additionally keeps wiki search in the building.",
            },
          ],
        },
      },
    ],
  },
  {
    id: "companion",
    title: { de: "Companion (Ausblick)", en: "Companion (outlook)" },
    pages: [
      {
        slug: { de: "companion", en: "companion" },
        title: { de: "Die Companion-App", en: "The Companion app" },
        description: {
          de: "Der Ausblick: eine App, in der Menschen Wissen abladen, ein Kurator-Agent es zu Wiki-Seiten verdichtet und Freigegebenes als Kontext zu den Agenten fließt.",
          en: "The outlook: an app where people offload their knowledge, a curator agent distils it into linked wiki pages, and shared knowledge flows as context into the agents' work.",
        },
        body: {
          de: `# Die Companion-App

> **In Entwicklung.** Das Konzept steht, der Bau folgt. Grundlage ist die Gedächtnis-Infrastruktur von Covey.

Der Companion ist eine eigenständige, optionale App: der Ort, an dem ein Mensch ablädt, was er im Kopf trägt — Ideen unterwegs gesprochen, Notizen, Dokumente — und daraus Kontext für seine Agenten macht.

## Zweck: Kontinuität, nicht Selbstoptimierung

Das Ziel ist nicht das persönliche „zweite Gehirn", sondern die Kontinuität im Team: Vertretung, Urlaub, Onboarding. Rollenwissen, das geteilt ist, lässt Kollegen und Agenten übernehmen, wenn jemand nicht da ist.

## Der Ablauf

1. **Abladen** — unterwegs erfassen, mit der Stimme zuerst.
2. **Kuratieren** — ein Kurator-Agent verdichtet das Rohe zu verlinkten Wiki-Seiten, erkennt Lücken und fragt nach.
3. **Füttern** — freigegebenes Wissen fließt, zentral durchgesetzt, als Kontext in die Aufgaben der Agenten.

## Datenschutz

Der Brain-Dump gehört dem Menschen. Privates bleibt privat vor anderen Menschen; eine Freigabe an die Abteilung ist eine bewusste Entscheidung, gesteuert vom Team-Lead. Kein Überwachungswerkzeug — der Companion dient der Produktivität der Person, nicht ihrer Kontrolle.

## Sichtbarkeit

Gestaffelt: \`privat\` → \`abteilung\` → \`org\`. Der Team-Lead steuert das Geteilte, nicht das Private.

## Warum das technisch aufgeht

Der Apparat existiert bereits: [Das Wiki-Gedächtnis](/docs/gedaechtnis) der Agenten ist genau dieses Modell — verlinkte Seiten, Vektorindex, Verdichtung. Der Companion setzt einen Menschen als Eigentümer an die Stelle des Agenten.`,
          en: `# The Companion app

> **In development.** The concept stands, the build follows. Its foundation is Covey's memory infrastructure.

The Companion is a separate, optional app: the place where a person offloads what they carry in their head — ideas spoken on the move, notes, documents — and turns it into context for their agents.

## Purpose: continuity, not self-optimisation

The goal is not the personal "second brain" but continuity in the team: cover, holidays, onboarding. Role knowledge that is shared lets colleagues and agents take over when somebody is away.

## The flow

1. **Offload** — capture on the go, voice first.
2. **Curate** — a curator agent distils the raw material into linked wiki pages, spots gaps and asks back.
3. **Feed** — shared knowledge flows, enforced centrally, as context into the agents' tasks.

## Privacy

The brain dump belongs to the person. Private stays private from other people; sharing with the department is a deliberate decision, governed by the team lead. Not a surveillance tool — the Companion serves the person's productivity, not their supervision.

## Visibility

Tiered: \`private\` → \`department\` → \`org\`. The team lead governs the shared layer, not the private one.

## Why this works technically

The apparatus already exists: the agents' [wiki memory](/en/docs/memory) is exactly this model — linked pages, vector index, consolidation. The Companion puts a person in the owner's place instead of an agent.`,
        },
      },
    ],
  },
];

export const DOC_PAGES: DocPage[] = DOC_SECTIONS.flatMap((s) => s.pages);
export const FIRST_DOC = DOC_PAGES[0];
export const docLang = (l: string): Lang => (l.startsWith("en") ? "en" : "de");
