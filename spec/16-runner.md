# 16 — Runner (verteilte Data Plane)

Heute läuft die Data Plane auf **dem Host der Control Plane**: Der `docker`-Provider ruft die lokale Docker-CLI, das persistente Home liegt als Verzeichnis daneben, der Egress-Proxy ist ein Container auf derselben Maschine. Das trägt für eine Instanz und eine Handvoll Agenten, aber es macht die Größe der Belegschaft zu einer Frage der Größe *einer* Maschine.

Der **Runner** löst das: ein eigenständiger Prozess auf einem beliebigen Host, der sich bei der Control Plane **registriert** und von dort Sandboxen zugewiesen bekommt — dasselbe Muster wie GitLab-Runner. Die Control Plane bleibt der Single Point of Truth; die Runner sind, wie die Sandboxen selbst, dumm und ersetzbar.

Warum man das will: mehr Agenten als eine Maschine trägt; Datenresidenz pro Abteilung oder Mandant (siehe [`09-enterprise-modell.md`](09-enterprise-modell.md)); Hardware-Nähe (ARM-Builds, GPU, ein Runner im Netz des Zielsystems); und die saubere Trennung von Control Plane und Rechenlast, die heute nur konzeptionell existiert.

## Was bereits trägt

Die wichtigste Eigenschaft ist schon da: **Der Daemon wählt heraus.** `coveyd` liest `COVEY_WS_URL` und `COVEY_DAEMON_TOKEN` aus seiner Umgebung und baut die WebSocket-Verbindung zur Control Plane auf; die Control Plane wartet nur darauf. Sie ruft **nie** in eine Sandbox hinein.

```
Sandbox (irgendwo)  ──── WebSocket, ausgehend ────►  Control Plane
                          COVEY_DAEMON_TOKEN
```

Damit ist die Richtungsumkehr, die entfernte Ausführung überhaupt erst praktikabel macht, bereits im Daemon-Protokoll verankert ([`01-architektur.md`](01-architektur.md)) — eine Sandbox braucht keine eingehende Erreichbarkeit, nur einen Weg nach draußen. Ebenso trägt der Port `SandboxProvider`: Er hat genau eine Aufrufstelle im Orchestrator. Ein Provider, der statt der lokalen Docker-CLI einen registrierten Runner anspricht, ändert am Orchestrator nichts.

Was noch am lokalen Host klebt, ist entsprechend überschaubar:

| Heute lokal | Warum das bricht |
|---|---|
| `docker run` über die lokale CLI | Die Control Plane muss auf dem Docker-Host sitzen |
| Home als Host-Pfad `<data>/homes/<agent-id>` | Der Dateibrowser liest direkt vom Dateisystem |
| Egress-Proxy als Container daneben | Erreichbar über `host.docker.internal` |
| Ein einziger Provider ohne Auswahl | Kein Routing, kein Scheduling, keine Kapazität |

## Das Modell

```
                     ┌──────────────────────────────┐
                     │        Control Plane         │
                     │  Scheduler · Registry · DB   │
                     └───────┬──────────────┬───────┘
        Runner-Protokoll     │              │      Daemon-Protokoll
        (langlebig, 1×/Host) │              │      (pro Wake, 1×/Sandbox)
                     ┌───────▼──────┐       │
                     │ covey-runner │       │
                     │  Host A      │       │
                     │  ┌─────────┐ │       │
                     │  │ Sandbox ├─┼───────┘
                     │  └─────────┘ │
                     │  homes/      │  ← Arbeitsbestand, verwerfbar
                     └──────────────┘
```

**Zwei Verbindungen, zwei Protokolle.** Der Runner-Link ist langlebig und besteht pro Host; der Daemon-Link entsteht pro Wake und pro Sandbox. Das ist bewusst nicht dasselbe Protokoll: Der Runner muss auch dann ansprechbar sein, wenn **keine** Sandbox läuft — für Kapazitätsmeldungen, für den Dateizugriff auf ein schlafendes Home, für das Aufräumen verwaister Container. Das Daemon-Protokoll an einen Host statt an eine Sandbox zu binden, würde beide Lebensdauern vermischen.

Der Datenpfad des Daemons bleibt davon **unberührt**: Die Sandbox spricht weiterhin direkt mit der Control Plane, nicht über den Runner. Der Runner startet und stoppt Compute — er ist kein Proxy für die Agentenarbeit und sieht deren Events nicht.

## Registrierung

Wie bei GitLab getrennt in ein **Registration-Token** (org-weit, in der UI erzeugbar und widerrufbar) und ein daraus abgeleitetes **Runner-Token** (langlebig, pro Runner, nur als Hash gespeichert):

```
covey-runner register --url https://covey.example --token <registration-token> \
                      --tag php --tag arm64 --description "Build-Host Frankfurt"
```

Danach hält `covey-runner run` eine dauerhafte WebSocket-Verbindung auf `/api/runner/ws`, authentifiziert mit dem Runner-Token. Der Runner meldet beim Verbinden seine **Capabilities**:

- verfügbare Sandbox-Images (siehe unten),
- Architektur (`amd64`/`arm64`), CPU/RAM, freier Plattenplatz,
- die Tags aus der Registrierung,
- seine eigene Version, damit die Control Plane Versions-Drift sichtbar machen kann.

Ein Runner ohne Verbindung ist **offline**, nicht gelöscht — die Unterscheidung ist für den Betrieb wichtig (Wartungsfenster vs. Abbau), für die Agenten aber folgenlos: Sie weichen auf einen anderen Runner aus (siehe „Das Home"). Ein gelöschter Runner nimmt nur seinen lokalen Arbeitsbestand mit, keinen Zustand der Plattform.

## Runner-Protokoll

### Control Plane → Runner

| Nachricht | Zweck |
|---|---|
| `start_sandbox` | Sandbox starten: Agent-ID, Image, Home-Kennung, Env (`COVEY_WS_URL`, `COVEY_DAEMON_TOKEN`, Egress-Token) |
| `stop_sandbox` | Compute herunterfahren; das Home bleibt |
| `home_op` | Dateizugriff auf ein Home: auflisten, lesen, schreiben, löschen (siehe Dateizugriff) |
| `set_allowlist` | Egress-Allowlist für den lokalen Proxy des Runners aktualisieren |
| `prune` | Verwaiste Container und Homes gelöschter Agenten aufräumen |

### Runner → Control Plane

| Nachricht | Zweck |
|---|---|
| `registered` | Capabilities, Tags, Version — die erste Nachricht nach dem Verbinden |
| `sandbox_started` / `sandbox_failed` | Ergebnis eines `start_sandbox` (der `ready`-Beweis kommt weiterhin vom Daemon selbst) |
| `sandbox_exited` | Die Sandbox ist von sich aus beendet (Absturz, OOM) — die Control Plane erfährt es, ohne auf den Daemon-Timeout zu warten |
| `home_result` | Antwort auf `home_op` |
| `capacity` | Laufende Sandboxen, freier Platz — Grundlage für Scheduling und Warnungen |
| `heartbeat` | Lebenszeichen |

`sandbox_exited` ist der Grund, warum der Runner den Container-Zustand überhaupt beobachten muss: Heute merkt die Control Plane einen Absturz erst am `ReadyTimeout` oder am abbrechenden Daemon-Link. Mit einem Runner, der den lokalen Docker-Daemon ohnehin fragt, wird daraus eine gemeldete Tatsache statt einer Vermutung.

## Das Home

Hier endet die GitLab-Analogie scheinbar: Ein CI-Runner darf frei gewählt werden, weil er **zustandslos** ist — ein Job klont neu, baut, wirft alles weg. Coveys Sandbox hat dagegen ein persistentes Home, und das *ist* Teil des Versprechens („geht eine Sandbox verloren, wird sie aus Config + Home neu aufgebaut", [`01-architektur.md`](01-architektur.md)).

Der Schein trügt. Ein vermessenes Entwickler-Home (7,1 GB) besteht fast vollständig aus Dingen, die **anderswo bereits eine Quelle haben**:

| Anteil | Größe | Quelle der Wahrheit |
|---|---|---|
| `repos/` | 3,0 GB | das Git-Remote des Projekts |
| `flutter`, `.pub-cache`, `.gradle`, `.npm`, `jdk`, `.dartServer` | 4,0 GB | ableitbar — reiner Cache |
| `wiki/`, `.claude/skills/` | wenige MB | die Control Plane (bereits zentral) |
| `.claude/projects/` (Session-Transkripte) | 30 MB | *nirgends* |
| `uploads/`, `work/`, Artefakte | ~15 MB | teils das Zielsystem, teils *nirgends* |

**Rund 7,05 der 7,1 GB sind ableitbar oder zentral geführt.** Das Home ist damit kein kostbarer Zustand, sondern zu über 99 % ein **Cache** — und das ändert die Antwort auf die Runner-Frage grundlegend.

### Zentral geführt, ins Home materialisiert

Für den wertvollen Rest ist das Muster in Covey **bereits gebaut**: Das Wiki liegt in Postgres und wird zu Aufgabenbeginn als `~/wiki/*.md` materialisiert, am Aufgabenende zurückgesynct ([`05-gedaechtnis.md`](05-gedaechtnis.md), „hybride Speicherung"); die Skills desselbe nach `~/.claude/skills/`. Die Control Plane ist dort längst die Quelle der Wahrheit, das Home nur die Arbeitskopie.

**Entschieden ist, dieses Muster auf den Rest auszudehnen** (D12): Was weder ableitbar ist noch anderswo eine Quelle hat, wird zentral geführt und beim Wake ins Home geholt.

- **Session-Transkripte.** Sie sind heute das Einzige, was eine *blockierte* Aufgabe wirklich an einen Host fesselt: Die Session-ID liegt in der DB (`RuntimeSessionID`), das für `--resume` nötige Transkript aber nur im Home ([`12-claude-code-adapter.md`](12-claude-code-adapter.md)). Es wird beim Einschlafen mitgesichert und beim Wake zurückgeschrieben — sonst verliert ein geparkter Agent seinen Faden, sobald er woanders aufwacht.
- **Artefakte und Uploads.** Dasselbe Sync-Muster wie beim Wiki, kein neues Konzept.

Nicht zentral geführt wird, was eine Quelle hat: **Checkouts** klont der Runner neu (das ist der Zweck eines Git-Remotes), **Caches und SDKs** baut er nach dem Pin im Projekt-Repo wieder auf („Version → Home, Toolchain → Image", D11). Beides zentral zu spiegeln hieße, ein Remote und einen Paket-Mirror nachzubauen.

> **Warum nicht das ganze Home als Git-Repo?** Naheliegend, aber es passt nur auf den kuratierten Textteil — und der ist das Wiki, das bereits zentral liegt. `repos/` sind selbst Git-Checkouts (verschachtelte Repos, plus Duplizierung eines existierenden Remotes), Caches und SDKs haben in einer Versionsgeschichte nichts verloren, und die Transkripte sind append-only JSONL, an dem Git aufbläht, ohne dass je jemand einen Diff liest. Das Wiki *mit* Git-Historie zu unterlegen bleibt eine attraktive, eigenständige Idee — sie beantwortet aber die Runner-Frage nicht und ist hier bewusst nicht mitentschieden.

### Affinität als Präferenz, nicht als Bindung

Ist das Home vollständig wiederherstellbar, wird die Bindung an einen Runner von einer *Voraussetzung* zu einer *Optimierung*. Der Unterschied ist der Ausfall:

- **Präferenz:** Der Scheduler bevorzugt den Runner, auf dem der Agent zuletzt lief — dort liegen Checkouts und Caches warm. Ist er weg, läuft der Agent woanders an, mit einem kalten Home.
- Der Preis eines kalten Starts ist real und soll nicht kleingeredet werden: 3 GB neu klonen plus 4 GB SDKs und Pakete. Ein Flutter-Agent braucht auf einem frischen Runner Minuten, bevor er die erste Zeile liest.
- Aber er ist **Zeit, nicht Datenverlust** — und das ist der ganze Unterschied. Ein Runner-Ausfall macht einen Agenten langsam, nicht arbeitsunfähig.

Das Home eines Agenten auf einem Runner ist damit ein **verwerfbarer Arbeitsbestand**: Der Runner darf ihn bei Platzmangel aufräumen (`prune`), und ein neu aufgesetzter Runner braucht keine Datenübernahme.

Die Präferenz ist dabei **klebrig, aber ohne Rückkehr-Automatik**: Fällt ein Runner aus, wandern seine Agenten auf einen anderen und *bleiben dort*. Kommt der alte zurück, wird nicht zurückmigriert — sonst folgt auf die Ausfallwelle eine zweite Welle kalter Starts. Der Ausgleich passiert von selbst beim nächsten ohnehin kalten Start.

## Warmup: warm ist der Runner, nicht das Paar

Ein fester Runner allein macht das Warmup nicht warm genug. Der Grund steht im heutigen Provider: Eine Sandbox hat **genau einen Mount** — ihr eigenes Home. Damit ist jeder Cache privat, obwohl kein Byte daran agentenspezifisch ist:

| | Größe im vermessenen Home | agentenspezifisch? |
|---|---|---|
| `.pub-cache` | 1,0 GB | nein — dieselben Pakete |
| `.gradle` | 951 MB | nein |
| `flutter` (SDK) | 1,0 GB | nein — die Version steht im Projekt |
| `.npm` | 402 MB | nein |
| `jdk` | 346 MB | nein |

Zwei Entwickler-Agenten auf demselben Runner halten heute zweimal dieselben 4 GB; fünf halten 20 GB. Und ein **neuer** Agent lädt auf einem Runner, der diese Pakete längst hundertfach liegen hat, alles erneut — weil er sie nicht sehen kann.

Deshalb hält der Runner einen **geteilten Bestand**, den er in jede Sandbox einblendet:

- **Toolchains und SDKs** — fvm-Flutter-SDKs, JDK-Toolchains. Unveränderlich und per Version adressiert.
- **Paket-Caches** — `.pub-cache`, `.gradle/caches`, `.npm/_cacache`, Composer. Inhaltsadressiert mit Integritäts-Hashes.
- **Git-Mirror** — der Runner hält Bare-Mirrors der Projekte; ein Checkout wird zum `--reference`-Klon statt zu 3 GB über die Leitung.

### Geteilt heißt nur-lesbar

Ein geteilter Cache ist ein **Kanal zwischen Agenten** und damit eine Isolationsfrage ([`06-observability-control.md`](06-observability-control.md)). Entschieden ist deshalb: **Die Sandbox liest den geteilten Bestand, sie schreibt ihn nie.**

- Technisch als Overlay: der geteilte Bestand als nur-lesbare untere Schicht, die Schreibschicht liegt im Home des Agenten. Alles, was ein Werkzeug neu holt, landet privat — Lesezugriffe fallen auf den geteilten Bestand durch. Wo ein Werkzeug das nativ kann, wird es genutzt statt nachgebaut (Gradle etwa kennt einen nur-lesbaren Dependency-Cache genau für diesen CI-Fall).
- Gefüllt wird der geteilte Bestand ausschließlich vom **Runner**, nachträglich: Nach einem erfolgreichen Lauf befördert er neu geholte Artefakte aus der privaten Schicht nach unten — beschränkt auf inhaltsadressierte Pfade mit prüfbarem Hash. Ein Agent kann damit einem anderen nichts unterschieben; er kann höchstens etwas beitragen, das seinem eigenen Hash entspricht.
- Der Preis ist ehrlich zu nennen: Ein **noch unbekanntes** Paket lädt der erste Agent selbst, und erst der zweite profitiert. Warmup ist ein Effekt über die Zeit, keine Garantie beim ersten Lauf.

Beim `--reference`-Klon kommt eine Eigenheit dazu: Ein so erzeugter Checkout hängt am Objektspeicher des Mirrors. Räumt der Runner ihn weg, ist der Checkout beschädigt. Entweder der Mirror wird nie geräumt, solange Checkouts darauf verweisen, oder es wird mit `--dissociate` geklont — das kopiert die nötigen Objekte einmal und kostet Platz statt Zerbrechlichkeit.

### Was das für den Ausfall bedeutet

Der geteilte Bestand entschärft genau die Schwäche, die ein fester Runner sonst hat. Fällt ein Runner aus, wandern **alle** seine Agenten gleichzeitig auf den Ausweich-Runner — ohne geteilten Bestand zieht dort jeder für sich Gigabytes, gleichzeitig. Mit ihm ist der Ausweich-Runner in aller Regel **nicht kalt**, weil andere Agenten dieselben Pakete längst dorthin gezogen haben. Aus „3 GB Klon plus 4 GB SDKs" wird im Normalfall ein Bruchteil davon.

Damit ist der kalte Start kein hingenommener Regelfall mehr, sondern die Ausnahme: Er trifft den *ersten* Agenten auf einem frischen Runner, nicht jeden Agenten bei jedem Wechsel.

## Scheduling

Bewusst einfach, weil kein Runner „der richtige" sein muss — nur der günstigste:

1. Kandidaten: alle **verbundenen** Runner, deren Tags die `runner_tags` des Agenten erfüllen **und** die sein Image vorhalten.
2. Davon bevorzugt der, auf dem der Agent zuletzt lief (`last_runner_id`) — dort ist sein Arbeitsbestand warm.
3. Sonst der mit den wenigsten laufenden Sandboxen. Kein Bin-Packing, keine Ressourcenmodellierung.
4. Keiner passend → die Aufgabe bleibt liegen, mit erklärendem Zustand statt einer Fehlermeldung über einen fehlgeschlagenen Container-Start.

`last_runner_id` ist ausdrücklich ein **Hinweis, keine Zusage**: Der Scheduler darf ihn jederzeit übergehen, und nichts im System darf annehmen, dass ein Home noch da ist. Ein Agent, dessen bevorzugter Runner fehlt, wacht auf einem anderen auf — langsamer, aber ohne Zutun eines Menschen.

## Sandbox-Images pro Agent

Der Runner macht eine Lücke sichtbar, die auch ohne ihn schmerzt: Das Sandbox-Image ist heute **instanzweit** (`COVEY_SANDBOX_IMAGE`). Jeder Agent bekommt dasselbe — der Mail-Agent trägt die JVM des Entwickler-Agenten mit.

Deshalb gehört das Image **an den Agenten** (D11 in [`07-offene-entscheidungen.md`](07-offene-entscheidungen.md)), als Profil:

| Profil | Inhalt | für wen |
|---|---|---|
| `base` | coveyd, Node, git, chromium, ripgrep | Support, Mail, QA, Recherche |
| `dev` | + PHP, JDK, `fvm`, `uv` | Entwickler-Agenten |
| org-eigen | beliebig | Sonderfälle, engere Sandboxen |

Der Schnitt geht ausdrücklich **nicht** entlang einzelner Sprachen. Ein Profil ist eine *Vereinigung*: Ein Entwickler-Agent arbeitet legitim an einem PHP- und einem Flutter-Projekt, und ein Image pro Sprache brächte genau die Frage zurück, die „Version → Home, Toolchain → Image" bereits beantwortet hat — welches Image starte ich beim Wake, wenn noch nicht feststeht, welches Ticket kommt?

Für Runner ist das Image zugleich eine **Capability**: Ein Runner meldet, welche Images er vorhält, und bekommt nur passende Agenten. Der Preis ist bekannt und tragbar — der Warm-Pool fragmentiert pro Image.

## Vertrauensgrenze

Ein Runner erhält mit `start_sandbox` das `COVEY_DAEMON_TOKEN` und das Egress-Token eines Agenten, um sie in den Container zu injizieren. Er **kann** damit jeden Agenten imitieren, den er hostet. Das ist dasselbe Vertrauensniveau wie bei einem CI-Runner, der Job-Tokens sieht — aber es muss ausgesprochen sein:

> **Runner sind vertrauenswürdige Infrastruktur der Organisation, keine fremden Kisten.** Ein Runner ist kein Weg, nicht vertrauenswürdige Rechenkapazität einzubinden.

Daraus folgen harte Regeln:

- **Kein Datenbankzugriff.** Ein Runner spricht ausschließlich das Runner-Protokoll. Das betrifft heute konkret den Egress-Proxy: Er bekommt im harten Isolationsmodus `COVEY_DATABASE_URL` und liest seine Allowlist selbst aus Postgres. Auf einem entfernten Runner hieße das, die Postgres-Zugangsdaten auf jeden Host zu verteilen — das kippt die Vertrauensgrenze und ist **Voraussetzung** für alles Weitere: Die Allowlist muss über die authentifizierte Control-Plane-API kommen (siehe unten).
- **Kein langlebiges Zielsystem-Secret.** Der Runner sieht Daemon- und Egress-Token, nie die gebrokerten Zugangsdaten — die gehen weiterhin direkt an den Daemon ([`04-identitaet-secrets.md`](04-identitaet-secrets.md)).
- **TLS ist Pflicht.** Die Control Plane muss von fremden Hosts erreichbar sein; `COVEY_PUBLIC_URL` über Klartext-HTTP wäre die Preisgabe jedes Daemon-Tokens.
- **Widerruf und Audit pro Runner.** Ein Runner-Token ist einzeln widerrufbar; Registrierung, Verbindung, jede Sandbox-Zuweisung und jeder `home_op` sind auditierbare Ereignisse ([`06-observability-control.md`](06-observability-control.md)).

## Egress bei verteilten Runnern

Der harte Isolationsmodus (`network`) verlangt einen Proxy **im Netzsegment der Sandbox** — also pro Runner, vom Runner gestartet und gepflegt. Zwei Änderungen gegenüber heute:

1. **Allowlist über die API statt über die DB.** Der Proxy fragt die Control Plane (mit dem Runner-Token authentifiziert) und cacht; Änderungen schiebt die Control Plane per `set_allowlist` nach. Das ist die oben genannte Voraussetzung und **auch ohne Runner die richtige Bauform** — der Proxy ist ein Enforcement-Punkt, kein Datenbank-Client.
2. **Die Control Plane ist ein echter Fremdhost.** `host.docker.internal` wird zur öffentlichen Adresse; sie muss auf der internen Allowlist des Proxys stehen, damit der Daemon-Link durch den harten Modus kommt. Der Mechanismus dafür existiert (`COVEY_EGRESS_ALLOW`), nur der Wert stimmt nicht mehr automatisch.

## Dateizugriff

`FileAccess` liefert heute einen **Host-Pfad**, den der Dateibrowser direkt liest. Mit entfernten Runnern wird daraus `home_op` über den Runner-Link.

Die Begründung, warum der Dateizugriff bewusst **nicht** durch das Daemon-Protokoll geht, bleibt dabei erhalten — sie wird sogar sauberer: Das Home muss auch dann lesbar sein, wenn die Sandbox schläft, und schlafend ist der Normalzustand. Der Runner-Link besteht durchgehend, der Daemon-Link nur während eines Laufs. Der Runner ist damit genau die richtige Naht für diese Anforderung; der Daemon wäre die falsche gewesen.

## Nicht im ersten Wurf

Bewusst ausgeklammert, damit der erste Wurf klein bleibt:

- **Autoscaling** von Runnern (Cloud-API, Spot-Instanzen).
- **Vorausschauendes Warmhalten** — den geteilten Bestand vorsorglich auf Runner spiegeln, auf denen ein Agent noch nie lief. Der Bestand wächst aus tatsächlicher Nutzung; ihn zu *antizipieren* ist eine spätere Optimierung.
- **Rückmigration** nach einem Runner-Ausfall (siehe „Das Home": die Präferenz bleibt klebrig).
- **Mehrere gleichzeitige Sandboxen pro Agent** — „seriell vor parallel" gilt weiter.
- **Runner als Mandantengrenze.** Ein Runner pro Mandant ist naheliegend, aber die Isolationszusagen dafür gehören in [`09-enterprise-modell.md`](09-enterprise-modell.md) und sind eine eigene Entscheidung.
- **Nicht-Docker-Runner** (Firecracker, Kubernetes-Pods). Das Runner-Protokoll ist so geschnitten, dass sie später dahinter passen — gebaut wird zuerst der Docker-Runner.

## Bau-Reihenfolge

Jede Stufe ist für sich nützlich und einzeln abnehmbar:

| Stufe | Inhalt | Wert für sich allein |
|---|---|---|
| 0 | Egress-Proxy von der DB lösen (Allowlist über die API) | Richtige Bauform des Enforcement-Punkts, unabhängig von Runnern |
| 1 | Image pro Agent (`sandbox_image`), Profile `base`/`dev` | Der Mail-Agent trägt keine JVM mehr |
| 2 | Home vollständig wiederherstellbar: Session-Transkripte, Artefakte und Uploads zentral sichern (Muster wie `wikilocal.go`) | Ein verlorenes Home kostet Zeit statt Arbeit — auch auf **einem** Host schon wertvoll |
| 3 | `covey-runner` als drittes Binary: Registrierung, `start_sandbox`/`stop_sandbox`, `RunnerPool` als `SandboxProvider` | Sandboxen laufen auf einem zweiten Host |
| 4 | Geteilter Bestand pro Runner: Toolchains, Paket-Caches, Git-Mirror — nur-lesbar eingeblendet, vom Runner befüllt | Zweiter Agent auf demselben Host startet warm; **auch mit einem einzigen Runner** sofort wirksam |
| 5 | `home_op` — Dateibrowser über den Runner-Link | Der Dateibrowser funktioniert auch entfernt |
| 6 | Tags, Kapazität, Runner-Ansicht in der UI | Betreibbarkeit ab mehr als zwei Runnern |

Die Stufen 0 bis 2 sind vom Runner unabhängig und sollten zuerst laufen — jede ist für sich eine Verbesserung des Ist-Zustands. Stufe 0, weil sie sonst zur Sicherheitslücke wird, sobald ein Runner entfernt läuft. Stufe 1, weil die Image-Capability sonst in Stufe 3 nachgereicht werden müsste. **Stufe 2 ist die eigentliche Voraussetzung:** Solange ein blockierter Agent seinen Faden verliert, wenn er woanders aufwacht, wäre jeder Runner-Wechsel ein stiller Datenverlust — und die Affinität müsste doch wieder eine Bindung sein.
