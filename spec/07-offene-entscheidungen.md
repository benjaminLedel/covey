# 07 — Offene Entscheidungen & MVP-Scope

Stand: Ergebnis des Brainstorms. Diese Punkte sind bewusst offen und sollten früh festgenagelt werden, weil sie stark kaskadieren.

## Offene Entscheidungen

### D1 — Event-Korrelation *(höchste Priorität, nächster Schritt)*

Wie wird ein eingehendes Ereignis (Mail-Antwort, Ticket-Update) zuverlässig auf eine geparkte (`blocked`) Aufgabe gemappt?

Optionen: Reply-to mit Task-ID · zentraler Event-Router · Hybrid (Details in [`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md)).

Diese Entscheidung bestimmt, wie „echt" die E-Mail-als-Bus-Idee trägt, und beeinflusst Scheduler, Wake-Logik und den ganzen `blocked`-Mechanismus. **Zuerst klären.**

### D2 — Sandbox: Build vs. Buy

Firecracker/gVisor selbst betreiben vs. Sandbox-as-a-Service (E2B, Beam, Northflank). Persistentes Volume + ephemere Compute ist gesetzt; die Frage ist die Betriebsform. Empfehlung aus dem Brainstorm: **die Sandbox-Infra nicht from scratch bauen** — der differenzierte Teil ist die Control Plane (Details in [`01-architektur.md`](01-architektur.md)). Marktbefunde und konkrete Bausteine in [`08-marktumfeld.md`](08-marktumfeld.md) (u. a. Daytona-Wechsel auf closed-source Juni 2026).

### D3 — Agent-Identität: echter User vs. Service-Account

Pro System entscheiden (echte Identität + natives Audit vs. schlank/lizenzfrei). Beeinflusst Kosten (Lizenzen) und wie real der Org-Chart wird (Details in [`02-agenten-modell.md`](02-agenten-modell.md)).

### D4 — Backlog-Store: bestehendes Ticketsystem vs. eigener Store

Bestehendes Ticketsystem zweckentfremden (gemeinsame Aufgaben-Realität mit Menschen, starkes Org-Chart-Gefühl) vs. schlanker eigener Store (weniger Kopplung). Details in [`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md).

### D5 — Gedächtnis-Scoping

Pro Agent, pro Team oder geteilt (mit Zugriffsregeln auf Wiki-Seitenebene, `scope`-Frontmatter)? Wahrscheinlich pro-Agent-Kern + geteilter Org-Layer. Details in [`05-gedaechtnis.md`](05-gedaechtnis.md).

### D6 — Erste Runtime(s)

Mit welcher Runtime startet der Adapter-Satz? Naheliegend: Claude Code (bekannt, CLI-basiert, einfach zu bootstrappen) als erster Adapter, danach OpenHands/Harness.

### D7 — Codename ✅ *entschieden*

**Covey.** Ein „covey" ist ein kleiner, koordinierter Schwarm — abstrakt an der Oberfläche, mit stiller Bedeutung darunter. „a covey of agents" als Leitbild.

### D8 — E-Mail-Identität pro Agent

E-Mail ist **optional** (siehe [`02-agenten-modell.md`](02-agenten-modell.md)): Welche Agenten brauchen wirklich eine? Faustregel: nur, wenn die Rolle Mensch↔Agent-Kommunikation oder E-Mail-basierte Wake-Trigger erfordert. Reine Event-/Ticket-getriebene Automatisierungs-Agenten kommen ohne aus. Konkret pro Agenten-Typ zu entscheiden.

### D9 — Mandanten-Modell

Single-org self-hosted (ein Unternehmen, eine Instanz) vs. mehr-mandanten-fähig (mehrere isolierte Organisationen auf einer Instanz). Primär genügt single-org; Mehr-Mandanten-Fähigkeit ist eine spätere Ausbaustufe, muss aber — falls überhaupt angestrebt — bei Daten- und Policy-Isolation von Anfang an im Datenmodell mitgedacht werden. Details in [`09-enterprise-modell.md`](09-enterprise-modell.md).

### D10 — Backend-Sprache: Go vs. Kotlin

Beide tragfähig für den nebenläufigkeits-schweren Orchestration-Core. **Tendenz Go** (single-binary-Deployment, Ökosystem-Nähe zu kagent/Sandbox-SDKs, KI schreibt idiomatisches Go) vs. **Kotlin** (reicheres Typsystem für die Policy-Engine, strukturierte Nebenläufigkeit). Frontend bleibt in beiden Fällen TS/Tailwind. Trade-off-Tabelle in [`10-architektur-stack.md`](10-architektur-stack.md).

### Runtime-Erweiterungen: Computer Use & Recording-Tiefe (nach MVP)

Diese Gruppe hängt am Design-Spike [`16-computer-use-runtime.md`](16-computer-use-runtime.md) (echtes Computer Use als zweite Runtime neben Claude Code) und am Recording-Profil aus [`06-observability-control.md`](06-observability-control.md). Sie sind bewusst **post-MVP** — hier festgehalten, damit die Naht (Runtime-Registry, `RunSpec`, Recording) sie nicht verbaut.

#### D-CU1 — `blocked`-Wiederaufnahme ohne serverseitige Session

Claude Code löst `blocked → working` über `--resume <session_id>` mit Bordmitteln ([`12`](12-claude-code-adapter.md)). Die Messages-API-Runtime hat keine serverseitige Session — der Kontext ist die `messages`-Liste, inkl. teurer Bild-Kontexte. Optionen: Verlauf (letzte N Screenshots) selbst persistieren · serverseitige Compaction/Context-Editing · `blocked` in dieser Runtime einschränken. Details in [`16`](16-computer-use-runtime.md).

#### D-CU2 — Guard-Rail-Granularität bei Pixel-Aktionen

Coveys Guard-Rails greifen *pro Aktion* (`system:aktion`, [`06`](06-observability-control.md)). „Klick auf (x,y)" ist opak — pro-Aktion-Freigabe geht nicht sauber. Frage: reicht **Session-Approval** (über den noVNC-Live-View mit Takeover) plus die bestehende **Egress-Allowlist** als harte Grenze, oder braucht es einen Aktions-Filter vor der Ausführung (z. B. Klick-Ziel/aktive App gegen eine Allowlist)? Fail-closed bleibt Vorgabe.

#### D-CU3 — Kosten-Deckel für Bild-Kontexte

Screenshots sind teure Tokens. Stellschrauben: harter `max_turns`/Budget-Stopp, Screenshot-Auflösung (1080p-Empfehlung), Task-Budget der API. Welche Kombination wird Default für diese Runtime?

#### D-CU4 — Modell-Wahl je Agent

Computer Use ist API-only und nur mit computer-use-fähigen Modellen möglich. Das Registry-Feld `model` ([`12`](12-claude-code-adapter.md)) müsste je Runtime auf zulässige Modelle eingeschränkt werden — und der API-Key ist ein anderes Secret als der Claude-Code-Abo-Token ([`04`](04-identitaet-secrets.md)).

#### D-CU5 — Browser-MCP vs. native Computer-Use-Runtime *(Empfehlung: MCP zuerst)*

Claude Codes *eingebautes* Computer Use läuft nur interaktiv, nicht im Headless-`-p`-Modus, den Covey fährt. Aber Claude Code unterstützt **MCP im Headless-Modus** (`--mcp-config`) — ein Browser-/GUI-MCP-Server gibt dem *bestehenden* Claude-Code-Runtime navigate/click/screenshot. Das steckt in Coveys vorhandenen MCP-Zielsystem-Mechanismus (`kind=mcp`), bekommt **Guard-Rail-Subjekte pro Tool** (`browser:navigate` …), nutzt den Abo-Token und die bestehende `blocked`-Mechanik — ohne neue Runtime. **Empfehlung: erst dieser Weg** (leichter, guard-rail-freundlicher); die native Computer-Use-Runtime nur, wenn echte Pixel-Ebene über beliebige Desktop-Apps gebraucht wird.

#### D-CU6 — Screenshot-Blob-Store & Retention-Default

Screenshots liegen out-of-band (referenziert, nicht inline im Event-Payload). Optionen: `recording_blobs`-Tabelle (Postgres `bytea`, passt zu „Postgres als Anker") · Disk unter `COVEY_DATA_DIR` · externer Object-Store (Skalierungspfad). Plus: Default-Retention für Bild-Blobs (gröber als Text-Events) und der Prune-Job. Details in [`06`](06-observability-control.md).

## Vorgeschlagener MVP-Scope

Ziel: der kürzeste Weg zu **einem echten Agenten, der sich wie ein Angestellter verhält**, nicht die volle Flotte.

**In Scope (MVP):**

1. Control Plane mit Scheduler + Dispatch-Loop für **einen** Agenten-Typ (z. B. Support-Agent).
2. **Ein** Runtime-Adapter (Vorschlag: Claude Code).
3. Persistente Sandbox mit persistentem Home (via Buy-Lösung, D2).
4. Config-as-Code: `SOUL.md` + minimaler Satz MD-Dateien, kompiliert zu Prompt/Config.
5. Secrets-Broker mit Keycloak + RFC 8693 für **ein** Zielsystem (z. B. Ticketsystem).
6. Backlog als First-Class-Objekt mit dem vollen Zustandsautomaten inkl. `blocked`.
7. Event-Korrelation für den einen `blocked`-Fall (Entscheidung D1 umgesetzt).
8. **Ein minimaler zentraler Guard-Rail-Satz** — plattform-erzwungen: Egress-Deny nach außen ohne Freigabe, Deny für nicht-freigegebene Systeme/Tools, Approval-Pflicht für destruktive Aktionen. Fail-closed.
9. Session-Recording + Kill-Switch + einfaches Kosten-Tracking.

**Explizit später (nicht MVP):**

- Weitere Runtimes/Adapter.
- Supervisor-Agent (KI-gestützte Anomalie-Erkennung).
- Geteiltes Org-weites Gedächtnis.
- Inter-Agent-Kommunikation über A2A/MCP (E-Mail-Bus reicht zunächst).
- Voll ausgebautes Admin-Dashboard (Minimalsicht genügt).

**Leitfrage für den MVP:** Kann ein Support-Agent ein Ticket triagieren, selbst beantworten oder eskalieren, bei einer Rückfrage sauber `blocked` gehen, durch die eingehende Antwort korrekt wieder aufwachen und die Lösung ins Gedächtnis schreiben — vollständig aufgezeichnet, durch zentrale Guard-Rails eingehegt und mit Kill-Switch? Wenn ja, steht der Kern.
