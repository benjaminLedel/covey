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
                     │  homes/      │  ← persistent, hostlokal
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

Ein Runner ohne Verbindung ist **offline**, nicht gelöscht: Seine Agenten bleiben ihm zugeordnet, ihre Aufgaben bleiben im Backlog liegen. Löschen ist eine bewusste Aktion mit Konsequenzen (siehe Affinität).

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

## Home-Affinität

**Entschieden: Ein Agent ist an einen Runner gebunden; sein Home bleibt lokal auf dessen Platte.** (D12 in [`07-offene-entscheidungen.md`](07-offene-entscheidungen.md).)

Hier endet die GitLab-Analogie, und das ist der wichtigste Punkt dieses Dokuments. Ein CI-Runner ist **zustandslos**: Ein Job klont neu, baut, wirft alles weg — deshalb darf ihn der Scheduler frei wählen. Coveys Sandbox ist das Gegenteil: Das persistente Home *ist* das Versprechen („geht eine Sandbox verloren, wird sie aus Config + Home neu aufgebaut", [`01-architektur.md`](01-architektur.md)). Es trägt das Gedächtnis, die Checkouts, die Caches, die installierten SDKs ([`05-gedaechtnis.md`](05-gedaechtnis.md)).

Persistenter Zustand und freie Schedulebarkeit vertragen sich nicht von selbst. Die Affinität macht die Kopplung **sichtbar**, statt sie hinter geteiltem Speicher zu verstecken — und sie passt zur Leitmetapher: Ein Mitarbeiter hat einen Schreibtisch, und der steht in einem Gebäude.

Konkret:

- Der Agent trägt `runner_tags` (welche Art Host er braucht) und `runner_id` (an welchen er gebunden ist, anfangs leer).
- **Beim ersten Wake** wählt der Scheduler einen passenden, verbundenen Runner und **pinnt** den Agenten darauf. Ab da geht jeder Wake dorthin.
- Ist der Runner offline, ist der Agent **nicht weckbar**. Die Aufgabe bleibt im Backlog, der Zustand ist in der UI sichtbar und benannt — nicht ein stiller Timeout, sondern „Runner *Build-Host Frankfurt* ist offline".
- **Umzug ist eine ausdrückliche Handlung**, kein Automatismus: entweder das Home kopieren (Control Plane koordiniert `home_op` lesend auf dem alten, schreibend auf dem neuen Runner) oder es verwerfen und aus Config neu aufbauen. Beides ist vertretbar — aber es ist eine Entscheidung mit Datenverlust-Risiko und gehört deshalb vor die Augen eines Menschen.

Kein Rebalancing, kein automatisches Failover. Wer echte Ausfallsicherheit für Homes will, legt sie **unter** den Runner: geteilter Speicher oder repliziertes Volume sind eine Betriebsentscheidung des Hosts, keine Aufgabe der Control Plane. Die verworfenen Alternativen samt Begründung stehen in D12.

## Scheduling

Bewusst einfach, weil Affinität die meiste Arbeit erledigt:

1. Ist der Agent gepinnt → dieser Runner. Offline → Wake schlägt an, nicht aus.
2. Sonst: alle verbundenen Runner, deren Tags die `runner_tags` des Agenten erfüllen **und** die sein Image haben.
3. Aus denen der mit den wenigsten laufenden Sandboxen. Kein Bin-Packing, keine Ressourcenmodellierung.
4. Keiner passend → die Aufgabe bleibt liegen, mit erklärendem Zustand statt einer Fehlermeldung über einen fehlgeschlagenen Container-Start.

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
- **Automatische Home-Migration** und Failover zwischen Runnern.
- **Mehrere gleichzeitige Sandboxen pro Agent** — „seriell vor parallel" gilt weiter.
- **Runner als Mandantengrenze.** Ein Runner pro Mandant ist naheliegend, aber die Isolationszusagen dafür gehören in [`09-enterprise-modell.md`](09-enterprise-modell.md) und sind eine eigene Entscheidung.
- **Nicht-Docker-Runner** (Firecracker, Kubernetes-Pods). Das Runner-Protokoll ist so geschnitten, dass sie später dahinter passen — gebaut wird zuerst der Docker-Runner.

## Bau-Reihenfolge

Jede Stufe ist für sich nützlich und einzeln abnehmbar:

| Stufe | Inhalt | Wert für sich allein |
|---|---|---|
| 0 | Egress-Proxy von der DB lösen (Allowlist über die API) | Richtige Bauform des Enforcement-Punkts, unabhängig von Runnern |
| 1 | Image pro Agent (`sandbox_image`), Profile `base`/`dev` | Der Mail-Agent trägt keine JVM mehr |
| 2 | `covey-runner` als drittes Binary: Registrierung, `start_sandbox`/`stop_sandbox`, `RunnerPool` als `SandboxProvider` | Sandboxen laufen auf einem zweiten Host |
| 3 | `home_op` — Dateibrowser über den Runner-Link | Der Dateibrowser funktioniert auch entfernt |
| 4 | Tags, Kapazität, Runner-Ansicht in der UI | Betreibbarkeit ab mehr als zwei Runnern |

Stufe 0 und 1 sind vom Rest unabhängig und sollten zuerst laufen: Stufe 0, weil sie sonst zur Sicherheitslücke wird, sobald ein Runner entfernt läuft; Stufe 1, weil die Image-Capability sonst in Stufe 2 nachgereicht werden müsste.
