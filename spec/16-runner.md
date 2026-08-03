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
| `sync_home` | Home als Snapshot in den Store schreiben — regulär nach dem Job, außerdem erzwingbar (Wartung, Runner-Abbau) |
| `home_op` | Dateizugriff auf ein Home: auflisten, lesen, schreiben, löschen (siehe Dateizugriff) |
| `set_allowlist` | Egress-Allowlist für den lokalen Proxy des Runners aktualisieren |
| `prune` | Verwaiste Container und Homes gelöschter Agenten aufräumen |

### Runner → Control Plane

| Nachricht | Zweck |
|---|---|
| `registered` | Capabilities, Tags, Version — die erste Nachricht nach dem Verbinden |
| `sandbox_started` / `sandbox_failed` | Ergebnis eines `start_sandbox` (der `ready`-Beweis kommt weiterhin vom Daemon selbst) |
| `sandbox_exited` | Die Sandbox ist von sich aus beendet (Absturz, OOM) — die Control Plane erfährt es, ohne auf den Daemon-Timeout zu warten |
| `home_synced` | Snapshot geschrieben: Kennung, übertragene Blöcke, Gesamtgröße — erst danach darf lokal aufgeräumt werden |
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
| **alles Übrige** | **48 MB** | *nirgends* |

Die Tabelle zeigt zweierlei. Erstens: **Fast nichts darin ist einzigartig.** Die 4 GB Toolchain-Caches sind auf jedem Entwickler-Home byteweise dieselben, und selbst Checkouts überschneiden sich zwischen Agenten am selben Projekt. Zweitens: Der wirklich einzigartige Teil ist mit 48 MB winzig — und er liegt **quer im Home verstreut**. Neben den 30 MB Session-Transkripten finden sich dort `useSevenAssistant.ts` (95 KB extrahierter Code), `panel.json`, `subagent-223.json`, `fix223/`, `verify-729/` — direkt im Wurzelverzeichnis, nicht in einem dafür vorgesehenen Ordner. Ein Agent legt seine Zwischenstände, Auswertungen und selbstgeschriebenen Helfer dort ab, wo es ihm im Lauf sinnvoll erscheint. Sein Home ist sein Arbeitsplatz, kein Formular.

Daraus folgt, was **nicht** funktioniert: eine Liste. Weder eine Positivliste („sichere `work/` und `uploads/`") noch eine Negativliste („verwirf, was das Image-Profil als Cache kennt") überlebt den Kontakt mit einem Agenten, der sich morgen ein Verzeichnis `analyse/` anlegt. Jede Liste ist eine Regel, die falsch sein kann, und ihr Fehler kostet Arbeit, die schon bezahlt wurde.

### Der zentrale Home-Store

**Entschieden: Nach jedem Job wird das Home als Ganzes in einen zentralen Store gesynct und beim Wake von dort materialisiert.** Kein Whitelisting, keine Negativliste, keine Prüfung, ob ein Checkout sauber ist — das Home geht vollständig hinein und vollständig heraus. Die Frage „was ist wertvoll?" wird damit gar nicht erst gestellt.

Praktikabel wird das durch die Bauform des Stores — **inhaltsadressiert und blockweise dedupliziert**:

- Jede Datei zerfällt in Blöcke, der Block-Hash ist sein Schlüssel. Gleicher Inhalt heißt **ein** Block, org-weit über alle Agenten und alle Snapshots hinweg.
- Nach dem Job wandern nur die **neuen** Blöcke nach oben. Ein typischer Lauf verändert Megabytes an einem 7-GB-Home.
- Beim Wake kommen nur die **fehlenden** Blöcke herunter; was der Runner lokal schon hat, bleibt liegen.
- Ein Snapshot pro Job. Historie und Rollback („das Home von vorgestern") fallen als Nebenprodukt ab.

Der Effekt auf die gemessenen Zahlen ist der eigentliche Punkt: Die 4 GB Toolchain-Caches liegen zentral **einmal**, nicht einmal pro Agent — dedupliziert genau deshalb, weil sie überall identisch sind. Und die 48 MB einzigartiger Arbeit sind mitgesichert, **ohne dass irgendwo notiert werden musste, dass es sie gibt**.

Damit erledigen sich drei Sorgen auf einmal: Die Session-Transkripte reisen automatisch mit (ein geparkter Agent behält seinen `--resume`-Faden, wo immer er aufwacht, [`12-claude-code-adapter.md`](12-claude-code-adapter.md)); unveröffentlichte Änderungen in einem Checkout sind kein Sonderfall mehr; und selbstgeschriebene Werkzeuge brauchen keine Regel, die sie schützt.

### Cache für die Masse, einzige Kopie für den Rest

Der Store heißt „Cache", und für 99 % seines Inhalts stimmt das: Ginge er verloren, würden Toolchains neu geladen und Repos neu geklont — ärgerlich, nicht tragisch. Für die 48 MB stimmt es **nicht**. Sie existieren nirgends sonst.

Daraus folgt eine Betriebsregel, die man nicht überlesen darf: **Der Home-Store ist sicherungspflichtig wie die Datenbank.** Ihn als reinen Cache zu behandeln — beliebig löschbar, kein Backup — wäre genau der Datenverlust, den die ganze Konstruktion verhindern soll. Er ist ein Cache in seiner *Funktion*, nicht in seiner *Schutzwürdigkeit*.

### Ausschlüsse sind Optimierung, nicht Politik

Einzelne Pfade vom Sync auszunehmen bleibt sinnvoll — reine Analyse-Caches wie `.dartServer` (317 MB) etwa gewinnen durch Dedup wenig und ändern sich ständig. Der Unterschied zum verworfenen Listen-Ansatz ist die **Rolle** der Liste:

- Vorher war ihre Vollständigkeit **Voraussetzung für Korrektheit**. Ein vergessener Pfad hieß Datenverlust.
- Jetzt ist sie eine **Kostenfrage**. Die Voreinstellung ist leer: Ohne Konfiguration wird alles gesynct.

Ausschlüsse gelten deshalb nur für nachweislich ableitbare Pfade, und im Zweifel wird gesynct. Ein falsch gesetzter Ausschluss kostet dann immer noch etwas — nur muss ihn jemand ausdrücklich gesetzt haben, statt ihn zu vergessen.

### Was das kostet

Ehrlich zu nennen, weil es nicht gratis ist:

- **Der erste Sync** eines gewachsenen Entwickler-Homes ist ein voller Durchgang — 7 GB. Danach Deltas.
- **Der Sleep-Pfad wird länger.** Der Sync läuft beim echten Einschlafen, nicht beim Parken einer warmen Sandbox — die warme Session ([`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md)) bleibt davon unberührt.
- **Ein Speicher für Blöcke.** Postgres ist für GB-große Binärdaten der falsche Ort (siehe unten).
- **Aufräumen.** Snapshots häufen sich; ohne Retention wächst der Store unbegrenzt. Bedienbar in der Oberfläche, nicht nur per Umgebungsvariable — siehe „Oberfläche".

### Wo die Blöcke liegen

Ein weiterer Port nach dem Muster von `IdentityProvider` und `SecretStore` ([`10-architektur-stack.md`](10-architektur-stack.md), „batteries included, but swappable"): **`BlobStore`**, Interface vor Implementierung, zwei Umsetzungen.

| Backend | Wann | Was es kostet |
|---|---|---|
| `builtin` (Voreinstellung) | ein Verzeichnis neben `data/homes`, Blöcke als `blocks/<xx>/<hash>` | nichts — kein Zusatzdienst, kein neues Betriebsteil |
| `s3` | wenn Durabilität, Replikation oder Trennung von der Control-Plane-Platte gefordert ist | ein weiterer Dienst im Betrieb |

Die Voreinstellung ist bewusst das Verzeichnis. Für eine Installation auf einer Maschine — der Normalfall von Covey — ist ein Objektspeicher unnötige Betriebsfläche, und das Versprechen „ein Binary + Postgres" soll nicht klammheimlich zu „ein Binary + Postgres + MinIO" werden.

**Wenn Objektspeicher, dann S3-kompatibel — nicht „AWS S3".** Das Protokoll ist der gemeinsame Nenner von Hetzner Object Storage, Garage, MinIO, Ceph RadosGW und SeaweedFS; Covey schreibt keinen Server vor, sondern spricht das Protokoll. Zur Auswahl auf der Betreiberseite: Was der Hoster ohnehin anbietet (auf der in [`10-architektur-stack.md`](10-architektur-stack.md) genannten Hetzner/Proxmox-Infra also Hetzner Object Storage), sonst **Garage** — leichtgewichtig und für genau solche kleinen, selbst betriebenen Cluster gebaut. MinIO, wenn es im Haus ohnehin läuft.

**Die Client-Frage bleibt bis Stufe 7 offen** — bewusst, denn sie ist kleiner, als sie aussieht. Ein Block-Store braucht fünf Operationen: `PUT`, `GET`, `HEAD`, `DELETE` und das Signieren kurzlebiger URLs. Weil Blöcke klein und unveränderlich sind, entfällt der aufwendigste Teil eines S3-Clients komplett: **Multipart-Upload wird nie gebraucht.**

Gemessen, statt geschätzt:

| Kandidat | Kosten | Anmerkung |
|---|---|---|
| `minio-go/v7` | **18 indirekte Module, 41 kompilierte Fremdpakete** | Covey hat heute 22 Module *insgesamt*. Bringt `msgp`, `xxh3`, `crc64nvme`, `md5-simd`, `compress` für Replikation, ILM, Notifications, S3 Select — nichts davon wird je benutzt |
| `aws-sdk-go-v2` | noch schwerer | Offiziell und gründlich, aber gegen fremde Endpunkte umständlicher |
| eigener Minimal-Client | keine Abhängigkeit | SigV4 über `crypto/hmac` + `crypto/sha256`; die Presign-Variante ist die einfachere (Query-String-Auth) |

Der Eigenbau ist hier vertretbarer als üblich, weil sein **Fehlermodus laut und geschlossen** ist: Eine falsche Signatur ergibt ein `403`, sofort sichtbar — nicht eine still geschwächte Sicherheitseigenschaft. Das ist der Unterschied zwischen „Krypto-Primitive benutzen" und „Krypto-Protokoll erfinden": SigV4 ist ein Signatur-Rezept über HMAC-SHA256, kein Handshake.

Dagegen spricht das, was eine gereifte Bibliothek mitbringt: die Eigenheiten der einzelnen Anbieter (Path-Style vs. Virtual-Host, Regions-Ermittlung, Fehler-Parsing). Wer nur einen Anbieter bedient, merkt davon nichts; wer fünf bedient, merkt es an jedem.

Entschieden wird das, wenn Stufe 7 ansteht. Bis dahin zählt allein, dass `BlobStore` ein Port ist und `builtin` **gar keine** Abhängigkeit braucht.

### Wie die Blöcke zum Runner kommen

Ein Runner bekommt **niemals die Zugangsdaten des Stores** — das wäre dasselbe Versäumnis wie die Datenbank-URL im Egress-Proxy (siehe „Vertrauensgrenze"). Stattdessen stellt die Control Plane pro Transfer **kurzlebige, gescopte URLs** aus, genau nach dem Muster des Secrets-Brokers ([`04-identitaet-secrets.md`](04-identitaet-secrets.md)):

- Beim `s3`-Backend als **pre-signed URLs** — der Runner lädt direkt beim Objektspeicher, die Control Plane sieht die Bytes nie.
- Beim `builtin`-Backend liefert die Control Plane gleichwertige kurzlebige URLs auf sich selbst.

Beides verhindert zugleich, dass 7 GB durch die Runner-WebSocket müssen: Der Steuerkanal bleibt schmal, die Nutzlast geht daran vorbei.

> **Warum nicht Git als Transport?** Naheliegend, aber Git passt nur auf den kuratierten Textteil. `repos/` sind selbst Git-Checkouts (verschachtelte Repos plus Duplizierung eines existierenden Remotes), Caches und SDKs haben in einer Versionsgeschichte nichts verloren, und die Transkripte sind append-only JSONL, an dem Git aufbläht, ohne dass je ein Diff gelesen wird. Was Git hier attraktiv macht — Dedup und Historie — liefert der inhaltsadressierte Store ohnehin, und zwar ohne diese Nachteile. Das Wiki *mit* Git-Historie zu unterlegen bleibt eine eigenständig attraktive Idee, ist hier aber nicht mitentschieden.

### Affinität als Präferenz, nicht als Bindung

Ist das Home aus dem Store wiederherstellbar, wird die Bindung an einen Runner von einer *Voraussetzung* zu einer *Optimierung*. Der Unterschied zeigt sich am Ausfall:

- **Präferenz:** Der Scheduler bevorzugt den Runner, auf dem der Agent zuletzt lief — dort liegen dessen Blöcke schon lokal, das Materialisieren ist fast umsonst.
- **Auf einem anderen Runner** holt er nur die Blöcke, die dort fehlen. Das ist deutlich weniger als ein kalter Start im ursprünglichen Sinn: Nichts wird neu geklont oder aus dem Internet gezogen, es kommt aus dem eigenen Store — und alles, was andere Agenten dort schon abgelegt haben, ist bereits da.
- Der schlimmste Fall bleibt ein **frischer** Runner ohne einen einzigen Block. Dann ist es ein voller Transfer, und ein Flutter-Agent braucht Minuten, bevor er die erste Zeile liest. Aber das ist **Zeit, nicht Datenverlust** — und es trifft den ersten Agenten auf einer neuen Maschine, nicht jeden Wechsel.

Das Home auf einem Runner ist damit ein **echter Arbeitsbestand ohne Sonderregeln**: Der Runner darf ihn bei Platzmangel vollständig wegräumen (`prune`), sobald der Sync durch ist — es gibt nichts darin, das nicht im Store läge. Die einzige harte Regel lautet: **Kein `prune` vor erfolgreichem Sync.** Ein neu aufgesetzter Runner braucht keine Datenübernahme.

Die Präferenz ist dabei **klebrig, aber ohne Rückkehr-Automatik**: Fällt ein Runner aus, wandern seine Agenten auf einen anderen und *bleiben dort*. Kommt der alte zurück, wird nicht zurückmigriert — sonst folgt auf die Ausfallwelle eine zweite Welle kalter Starts. Der Ausgleich passiert von selbst beim nächsten ohnehin kalten Start.

## Warmup: der lokale Blockspeicher

Warmup braucht **keinen eigenen Mechanismus** — es fällt aus dem Store ab, sobald der Runner die geholten Blöcke lokal behält statt sie wegzuwerfen. Ein Block gehört keinem Agenten: Er ist durch seinen Hash bestimmt, und wer denselben Inhalt braucht, bekommt denselben Block.

Das trifft genau die Verschwendung, die heute unvermeidbar ist. Eine Sandbox hat aktuell **einen einzigen Mount**, ihr eigenes Home — jeder Cache ist privat, obwohl kein Byte daran agentenspezifisch ist:

| | Größe im vermessenen Home | agentenspezifisch? |
|---|---|---|
| `.pub-cache` | 1,0 GB | nein — dieselben Pakete |
| `.gradle` | 951 MB | nein |
| `flutter` (SDK) | 1,0 GB | nein — die Version steht im Projekt |
| `.npm` | 402 MB | nein |
| `jdk` | 346 MB | nein |

Zwei Entwickler-Agenten auf demselben Host halten heute zweimal dieselben 4 GB; fünf halten 20 GB. Mit blockweiser Deduplizierung liegen sie **einmal** im lokalen Speicher des Runners — und der zweite Agent materialisiert sein Home daraus, ohne ein Byte über die Leitung zu holen. Ein *neuer* Agent auf einem eingelaufenen Runner startet damit fast warm, obwohl er dort noch nie lief.

### Warum das die Isolationsfrage auflöst

Ein geteilter Cache ist sonst ein **Kanal zwischen Agenten** und damit eine Isolationsfrage ([`06-observability-control.md`](06-observability-control.md)) — wer in einen gemeinsamen Paket-Cache schreiben darf, kann anderen etwas unterschieben. Bei Inhaltsadressierung entfällt das Problem **konstruktionsbedingt**:

- Ein Block wird über den Hash **seines Inhalts** angefordert. Liefert der lokale Speicher ihn, ist der Inhalt per Definition derselbe, den der Store liefern würde.
- Welche Blöcke das Home eines Agenten bilden, steht in **seinem eigenen** Snapshot. Kein anderer Agent kann diese Zuordnung beeinflussen.

Es gibt damit keine Beförderungsregel, keine Hash-Prüfliste und keinen nur-lesbaren Unterbau, den jemand befüllen müsste. Das Teilen ist sicher, weil es gar nicht die Möglichkeit gibt, unter einem fremden Hash etwas anderes abzulegen.

### Was das für den Ausfall bedeutet

Fällt ein Runner aus, wandern **alle** seine Agenten gleichzeitig auf einen anderen. Ohne geteilte Blöcke zöge dort jeder für sich Gigabytes, zeitgleich. Mit ihnen holt der Ausweich-Runner **jeden Block genau einmal**, egal wie viele Agenten ihn brauchen — und alles, was andere Agenten dort schon abgelegt haben, ist bereits da.

Der kalte Start ist damit keine Frage des Agentenwechsels mehr, sondern nur noch eine des **frischen Hosts**: Er trifft den ersten Agenten auf einer neuen Maschine, danach niemanden mehr.

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

- **Kein Datenbankzugriff und keine Store-Zugangsdaten.** Ein Runner spricht ausschließlich das Runner-Protokoll; Blöcke holt und schreibt er über kurzlebige, gescopte URLs (siehe „Wie die Blöcke zum Runner kommen"). Das betrifft heute konkret den Egress-Proxy: Er bekommt im harten Isolationsmodus `COVEY_DATABASE_URL` und liest seine Allowlist selbst aus Postgres. Auf einem entfernten Runner hieße das, die Postgres-Zugangsdaten auf jeden Host zu verteilen — das kippt die Vertrauensgrenze und ist **Voraussetzung** für alles Weitere: Die Allowlist muss über die authentifizierte Control-Plane-API kommen (siehe unten).
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

## Oberfläche

Ein Store, der still im Hintergrund wächst und dessen Inhalt niemand sehen kann, ist ein Betriebsrisiko — man bemerkt ihn erst, wenn die Platte voll ist. Beides gehört deshalb in die UI und nicht nur in Umgebungsvariablen.

### Das Home eines Agenten

Auf der Agenten-Seite, neben dem bestehenden Dateibrowser:

| Angabe | Warum sie dort steht |
|---|---|
| Größe des Homes, davon **belegt nach Dedup** | Der Unterschied ist die eigentliche Aussage: 7,1 GB Home, aber vielleicht 200 MB, die nur dieser Agent hält |
| Letzter Sync: Zeitpunkt, Dauer, übertragene Blöcke | Ob der Sync überhaupt läuft, und ob er teuer ist |
| Anzahl Snapshots und Zeitraum | Was die Retention gerade übrig lässt |
| Aktueller bzw. zuletzt genutzter Runner | Wo der Arbeitsbestand warm liegt |
| Die größten Verzeichnisse | Beantwortet „warum ist das Home so groß?" ohne Shell-Zugang — und legt Kandidaten für einen Ausschluss offen |

Dazu die Snapshot-Liste mit zwei Aktionen: **Wiederherstellen** (ein früherer Stand wird zum aktuellen — die Rollback-Fähigkeit, die aus der Bauform ohnehin abfällt) und **Jetzt sichern** (Sync erzwingen, etwa vor einer Wartung).

Wiederherstellen ist eine ändernde Aktion an fremder Arbeit und braucht deshalb dieselbe Behandlung wie andere Eingriffe: nur mit der passenden Rolle, mit Bestätigung, und als Audit-Ereignis ([`06-observability-control.md`](06-observability-control.md)). Sie ist außerdem nur zulässig, während der Agent **nicht** läuft — sonst schreibt die laufende Sandbox in ein Home, das sich unter ihr ändert.

### Retention

Org-weite Einstellung, mit Knopf statt nur mit Variable:

- **Snapshots je Agent behalten:** die letzten *N* (Voreinstellung 10).
- **Höchstalter:** Snapshots älter als *X* Tage entfernen (Voreinstellung 30).
- **Immer behalten:** der jüngste Snapshot jedes Agenten — auch wenn beide Regeln ihn träfen. Eine Retention, die einem Agenten sein letztes Home nimmt, ist ein Löschbefehl mit Umweg.
- **Jetzt aufräumen** als ausdrücklicher Knopf, mit Vorschau: was fiele weg, wie viel Platz käme frei.

Beim Aufräumen wird ein Block erst entfernt, wenn **kein** verbleibender Snapshot ihn mehr referenziert — das ist der Preis der Deduplizierung und der Grund, warum „diesen Snapshot löschen" nicht linear Platz freigibt. Die Vorschau nennt deshalb den tatsächlich frei werdenden Platz, nicht die Summe der Snapshot-Größen; alles andere wäre eine Zahl, die nie stimmt.

Der Füllstand des Stores gehört zusätzlich auf das Dashboard: Gesamtgröße, Wachstum, und eine Warnung, bevor die Platte knapp wird — nicht danach.

## Nicht im ersten Wurf

Bewusst ausgeklammert, damit der erste Wurf klein bleibt:

- **Autoscaling** von Runnern (Cloud-API, Spot-Instanzen).
- **Vorausschauendes Warmhalten** — Blöcke vorsorglich auf Runner spiegeln, auf denen ein Agent noch nie lief. Der lokale Blockcache wächst aus tatsächlicher Nutzung; ihn zu *antizipieren* ist eine spätere Optimierung.
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
| 2 | Home-Store: inhaltsadressierter Blockspeicher, Sync nach dem Job, Materialisieren beim Wake, lokaler Blockcache | Ein verlorenes Home kostet Zeit statt Arbeit, und der zweite Agent auf demselben Host startet warm — **beides schon mit einem einzigen Host** |
| 3 | `covey-runner` als drittes Binary: Registrierung, `start_sandbox`/`stop_sandbox`, `RunnerPool` als `SandboxProvider` | Sandboxen laufen auf einem zweiten Host |
| 4 | `home_op` — Dateibrowser über den Runner-Link | Der Dateibrowser funktioniert auch entfernt |
| 5 | Tags, Kapazität, Runner-Ansicht in der UI | Betreibbarkeit ab mehr als zwei Runnern |
| 6 | Oberfläche: Home-Info, Snapshot-Liste, Retention-Einstellung und -Knopf, Füllstand aufs Dashboard | Der Store ist sichtbar und bedienbar, statt still zu wachsen |
| 7 | `BlobStore`-Backend `s3` (Port existiert ab Stufe 2, `builtin` genügt bis hierher) | Durabilität und Replikation, wenn die Control-Plane-Platte nicht reicht |

Die Stufen 0 bis 2 sind vom Runner unabhängig und sollten zuerst laufen — jede ist für sich eine Verbesserung des Ist-Zustands. Stufe 0, weil sie sonst zur Sicherheitslücke wird, sobald ein Runner entfernt läuft. Stufe 1, weil die Image-Capability sonst in Stufe 3 nachgereicht werden müsste.

**Stufe 2 trägt das ganze Gebäude.** Sie macht das Home ersetzbar, und erst damit ist ein Runner-Wechsel kein Datenverlust; ohne sie müsste die Affinität doch wieder eine Bindung sein. Sie liefert das Warmup gleich mit — der Blockcache *ist* der geteilte Bestand. Und sie lohnt sich, bevor ein einziger zweiter Host existiert: Heute halten zwei Entwickler-Agenten auf derselben Maschine zweimal dieselben 4 GB, und ein gelöschtes Home ist unwiederbringlich.
