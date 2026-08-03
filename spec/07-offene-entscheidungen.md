# 07 — Offene Entscheidungen & MVP-Scope

Stand: Ergebnis des Brainstorms. Diese Punkte sind bewusst offen und sollten früh festgenagelt werden, weil sie stark kaskadieren. Entschiedene Punkte bleiben mit `✅ *entschieden*` stehen — die Begründung gehört zur Entscheidung und geht sonst verloren.

## Offene Entscheidungen

### D1 — Event-Korrelation *(höchste Priorität, nächster Schritt)*

Wie wird ein eingehendes Ereignis (Mail-Antwort, Ticket-Update) zuverlässig auf eine geparkte (`blocked`) Aufgabe gemappt?

Optionen: Reply-to mit Task-ID · zentraler Event-Router · Hybrid (Details in [`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md)).

Diese Entscheidung bestimmt, wie „echt" die E-Mail-als-Bus-Idee trägt, und beeinflusst Scheduler, Wake-Logik und den ganzen `blocked`-Mechanismus. **Zuerst klären.**

### D2 — Sandbox: Build vs. Buy

Firecracker/gVisor selbst betreiben vs. Sandbox-as-a-Service (E2B, Beam, Northflank). Persistentes Volume + ephemere Compute ist gesetzt; die Frage ist die Betriebsform. Empfehlung aus dem Brainstorm: **die Sandbox-Infra nicht from scratch bauen** — der differenzierte Teil ist die Control Plane (Details in [`01-architektur.md`](01-architektur.md)). Marktbefunde und konkrete Bausteine in [`08-marktumfeld.md`](08-marktumfeld.md) (u. a. Daytona-Wechsel auf closed-source Juni 2026). Wie die projektspezifische Toolchain in die Sandbox kommt, ist davon unabhängig entschieden — siehe D11.

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

### D11 — Projektpassende Sandbox-Umgebung ✅ *entschieden*

Wie kommt die projektspezifische Toolchain in eine Sandbox, die am **Agenten** hängt? Ein Entwickler-Agent arbeitet an mehreren Projekten mit verschiedenen Technologien und Versionen; sein Container startet aber beim Wake, bevor feststeht, welches Ticket aus welchem Projekt drankommt. „Ein Image pro Projekt" setzt deshalb einen Agenten pro Projekt voraus.

Entschieden: **Version → Home, Toolchain → Image.** Das Image trägt Systempakete (PHP, JDK) und die Versionsmanager (`fvm`, `uv`); die SDK-**Versionen** zieht sich der Agent nach dem Pin im Projekt-Repo selbst ins persistente Home.

Das Image hängt dabei am **Agenten**, nicht an der Sprache: Ein Profil ist eine *Vereinigung* von Toolchains (`base` für Support-, Mail- und QA-Agenten, `dev` für Entwickler), damit derselbe Agent an einem PHP- **und** einem Flutter-Projekt arbeiten kann. Ein Image pro *Sprache* brächte genau die oben verworfene Frage zurück — welches starte ich beim Wake? Dass die Auswahl heute instanzweit über `COVEY_SANDBOX_IMAGE` läuft und noch kein Feld am Agenten ist, ist eine offene Bau-Aufgabe, keine Eigenschaft der Entscheidung: siehe [`16-runner.md`](16-runner.md), wo das Image zugleich zur Runner-Capability wird.

Verworfen: **Pakete zur Laufzeit über die UI nachinstallieren.** Drei Gründe, alle strukturell — der Agent läuft als Nicht-Root (Claude Code verweigert `--dangerously-skip-permissions` als root); ein Paketmanager auf der Egress-Allowlist ist ein generischer Code-Ausführungskanal und kein Zielsystem-Host; und eine Sandbox, deren Werkzeuge aus einer Klickliste plus dem Zustand eines Mirrors entstehen, ist nicht mehr aus Config + Home rekonstruierbar — der Kern der „dumm und ersetzbar"-Zusage aus [`01-architektur.md`](01-architektur.md).

Betriebsseite samt Egress-Templates in [`../docs/betrieb-deployment.md`](../docs/betrieb-deployment.md).

### D12 — Verteilte Data Plane: Runner ✅ *entschieden*

Wie kommt die Data Plane von **einem** Host auf viele? Heute startet die Control Plane Sandboxen über die lokale Docker-CLI: Die Größe der Belegschaft ist damit die Größe einer Maschine, und Datenresidenz pro Abteilung oder Hardware-Nähe (ARM, GPU, ein Host im Netz des Zielsystems) sind nicht darstellbar.

Entschieden: **registrierte Runner nach dem Vorbild der GitLab-Runner** — ein eigenständiger Prozess auf einem beliebigen Host meldet sich mit einem Registration-Token an, hält eine ausgehende Verbindung und bekommt von dort Sandboxen zugewiesen. Der Port `SandboxProvider` bleibt die Naht; der Orchestrator merkt nichts davon. Ausführlich in [`16-runner.md`](16-runner.md).

Die eigentliche Frage dahinter ist nicht die Registrierung, sondern das **persistente Home** — und dort trügt der erste Eindruck. Ein vermessenes Entwickler-Home von 7,1 GB besteht zu über 99 % aus Dingen, die anderswo bereits eine Quelle haben: 3,0 GB Checkouts (das Git-Remote), 4,0 GB Caches und SDKs (ableitbar nach dem Pin im Projekt-Repo), dazu Wiki und Skills, die die Control Plane ohnehin führt. Nicht wiederherstellbar sind nur rund 45 MB: die Claude-Code-Session-Transkripte und die Arbeitsartefakte.

Entschieden ist deshalb **nicht** Bindung, sondern **Klassifizierung**: Was eine Quelle hat, wird nicht gespiegelt (Checkouts klont der Runner, Caches baut er neu); was keine hat, wird zentral geführt und beim Wake ins Home materialisiert — genau das Muster, das für das Wiki bereits gebaut ist ([`05-gedaechtnis.md`](05-gedaechtnis.md), „hybride Speicherung"; `internal/daemon/wikilocal.go`). Damit ist das Home eines Agenten auf einem Runner ein **verwerfbarer Arbeitsbestand**.

Daraus folgt die Rolle des Runners: **Affinität ist eine Scheduling-Präferenz, keine Bindung.** Der Scheduler bevorzugt den Runner, auf dem der Agent zuletzt lief, weil dort Checkouts und Caches warm liegen; fehlt er, wacht der Agent woanders auf und *bleibt dort* (keine Rückmigration, sonst folgt auf die Ausfallwelle eine zweite). Ein kalter Start kostet Minuten — aber Zeit, nicht Arbeit. Ein Runner-Ausfall macht einen Agenten langsam, nicht arbeitsunfähig, und Failover fällt ohne eigenen Mechanismus ab.

Damit „langsam" selten bleibt, gehört das **Warmup an den Runner statt an das Paar Agent↔Runner**: Er hält einen geteilten Bestand aus Toolchains, Paket-Caches und Git-Mirrors und blendet ihn in jede Sandbox ein. Heute ist das unmöglich — eine Sandbox hat genau einen Mount, ihr eigenes Home —, obwohl an diesen 4 GB kein Byte agentenspezifisch ist: Zwei Entwickler-Agenten auf einem Host halten zweimal dasselbe, und ein neuer Agent lädt alles erneut, obwohl der Runner es längst hat. Entschieden ist dabei **nur-lesbar**: Die Sandbox liest den geteilten Bestand, schreibt aber in eine private Overlay-Schicht; befüllt wird er ausschließlich vom Runner, nachträglich und beschränkt auf inhaltsadressierte Pfade mit prüfbarem Hash. Ein geteilter Cache ist ein Kanal zwischen Agenten — so kann ein Agent einem anderen nichts unterschieben, sondern höchstens etwas beitragen, das seinem eigenen Hash entspricht. Nebeneffekt: Der Ausweich-Runner beim Ausfall ist meist gar nicht kalt, weil andere Agenten dieselben Pakete schon dorthin gezogen haben.

Verworfen: **Bindung mit ausdrücklichem Umzug** (der Agent ist an einen Runner gepinnt, ein Wechsel ist Handarbeit) — sie macht einen Host-Ausfall zum Stillstand eines Mitarbeiters und wäre nur nötig, wenn das Home tatsächlich unersetzlich wäre; die Messung sagt das Gegenteil. Ebenfalls verworfen: **geteilter Speicher** (NFS/CSI) — er verlegt `node_modules`, `vendor` und Gradle-Caches auf ein Netzdateisystem und macht eine Betriebsentscheidung des Hosts zur Voraussetzung der Plattform. Und **das ganze Home als Git-Repo**: Git passt auf den kuratierten Textteil, aber `repos/` sind selbst Checkouts (verschachtelte Repos, Duplizierung eines existierenden Remotes), Caches gehören in keine Historie, und Transkripte sind append-only JSONL, an dem Git aufbläht, ohne dass je ein Diff gelesen wird. Alle drei bleiben *unter* dem Runner möglich, ohne dass die Control Plane davon wissen muss.

**Voraussetzung, die diese Entscheidung erst trägt:** Die Session-Transkripte müssen zentral gesichert werden. Sie sind heute das Einzige, was eine blockierte Aufgabe wirklich an einen Host fesselt — die Session-ID liegt in der DB, das für `--resume` nötige Transkript nur im Home. Ohne diesen Sync wäre jeder Runner-Wechsel ein stiller Verlust des Gesprächsfadens.

Ein Runner ist **vertrauenswürdige Infrastruktur der Organisation** — er sieht die Daemon- und Egress-Tokens der Agenten, die er hostet, und kann sie damit imitieren. Er ist kein Weg, fremde Rechenkapazität einzubinden. Daraus folgt insbesondere: kein Datenbankzugriff vom Runner aus, TLS zwingend, Widerruf und Audit pro Runner.

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
