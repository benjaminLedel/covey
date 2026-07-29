# 03 — Lifecycle & Scheduling

Dies ist das Herzstück der Plattform. Der Scheduler/Dispatcher ist das eigentliche Produkt: ein OS-Scheduler + cron + Inbox + Zustandsverwaltung für Agenten.

## Der „Always-on"-Trick

Ein Agent soll sich verhalten wie ein Mitarbeiter: immer erreichbar, mit einem Backlog, proaktiv. Aber **„always-on" ist eine UX-Eigenschaft, keine Runtime-Eigenschaft.** Ein Mensch ist dauerhaft da, verbrennt dabei aber fast keine Energie — er wird aktiv, wenn etwas reinkommt. Wörtlich always-on (Dauer-Inferenz) wäre teuer und würde Halluzinations-Rauschen produzieren.

Die Lösung: **immer erreichbar und stateful, aber Compute nur bei Bedarf.** Das Vehikel dafür ist das Backlog plus ein billiger Dispatch-Loop.

## Dispatch-Loop

Pro Agent läuft ein dauerhafter, **billiger** Dispatch-Loop — **kein LLM**, reine Orchestrierung. Er kennt drei Wake-Quellen:

| Wake-Quelle | Beispiel |
|---|---|
| **Event** | neues Ticket, Webhook, eingehende Mail (falls der Agent eine hat), Delegation von einem anderen Agenten |
| **Scheduler-Tick** | alle N Minuten „was liegt an?" |
| **Zeitplan (cron)** | „Montag 9 Uhr Wochenreport" — konfiguriert je Agent in `HEARTBEAT.md`, siehe unten |

Erst wenn eine dieser Quellen feuert, wird die teure Agent-Runtime in der Sandbox geweckt (`wake` → `assign_task`, siehe [`01-architektur.md`](01-architektur.md)).

### Heartbeat: wiederkehrende Aufgaben (`HEARTBEAT.md`)

Die Zeitplan-Quelle ist Config-as-Code: jede Zeile in `HEARTBEAT.md` ([`02-agenten-modell.md`](02-agenten-modell.md)) definiert eine wiederkehrende Aufgabe.

```markdown
- alle: 30m      titel: Posteingang sichten   aufgabe: Prüfe neue Tickets und triagiere sie.
- täglich: 09:00 titel: Tagesbericht          aufgabe: Fasse den gestrigen Tag zusammen.
- alle: 5m nur-wenn: email titel: Postfach    aufgabe: Bearbeite die ungelesenen Mails.
```

Zwei Zeitplan-Formen, genau eine je Eintrag:

- **`alle:`** — Intervall (`30m`, `2h`, `1d`). Fällig, sobald seit dem letzten Lauf das Intervall verstrichen ist.
- **`täglich:`** — feste Tageszeit (`HH:MM`, Serverzeit). Fällig einmal pro Tag ab dieser Uhrzeit.

Optional je Eintrag:

- **`nur-wenn:`** — Name eines Zielsystems, das beim Feuern erst Arbeit melden muss. Die Control Plane fragt das Plugin über das optionale `target.WorkChecker`-Interface (`HasWork`) mit selbst aufgelösten Secrets — für `email` etwa: „gibt es ungelesene Mails im Arbeitsvorrat?" Meldet das System keine Arbeit, entfällt der Lauf ohne Agenten-Wake; `last_fired_at` wird trotzdem fortgeschrieben, der Zeitplan pollt regulär weiter. Die Prüfung ist fail-open: lässt sich die Bedingung nicht auswerten (Plugin ohne `WorkChecker`, fehlende Secrets, Verbindungsfehler), feuert der Heartbeat regulär — eine kaputte Bedingung darf keine Arbeit liegen lassen. So wird aus dem Polling-Intake webhookloser Systeme ein billiger Control-Plane-Check; die teure Runtime startet nur, wenn wirklich etwas vorliegt.

**Kollegen sind keine Menschen (Ping-Pong-Bremse):** Zielsysteme, deren Bedingung an einem Gesprächsfaden hängt (GitLab-Issue, Merge Request), messen die *Flanke*: Arbeit liegt vor, solange nach dem letzten eigenen Beitrag jemand anderes geschrieben hat. Zwischen zwei Agenten trägt diese Regel allein einen Selbstläufer — jeder kommentiert, um sein eigenes Gate zu schließen, und öffnet damit das des anderen. Die Control Plane reicht dem Work-Check deshalb die Identitäten der übrigen Agenten derselben Organisation mit (`target.WithPeers`, gespeist aus den Profil-Identitäten). Ein **Mensch** weckt weiterhin immer; der Beitrag eines **Kollegen-Agenten** nur dann, wenn seither auch am Gegenstand gearbeitet wurde (bei GitLab: neue Commits). Reiner Text zwischen zwei Agenten — Dank, Nachtrag, Statusmeldung — ist keine Arbeit. Ohne hinterlegte Identitäten verhält sich der Check wie zuvor (fail-open).

Mechanik: Beim Speichern der Config werden die Einträge in `agent_heartbeats` materialisiert (`titel:` ist der Schlüssel; `last_fired_at` überlebt Config-Versionen und startet bei *jetzt* — ein frisch angelegter Heartbeat feuert also erst nach Ablauf seines Zeitplans, nicht sofort). Der periodische Tick des Dispatch-Loops prüft fällige Einträge per SQL und legt sie als reguläre Backlog-Aufgabe mit `origin='heartbeat'` an — ab dort greift der normale Lifecycle (wake, triage, working). Kill-Switch und Flotten-Notaus unterdrücken das Feuern.

**Kein Aufstauen:** Ist die Aufgabe des letzten Laufs noch nicht terminal (open/in_progress/blocked), wird kein Duplikat angelegt; der Lauf gilt trotzdem als gefeuert, damit nach Abschluss der reguläre Zeitplan weiterläuft statt sofort nachzuschlagen. Verpasste Läufe (Control Plane down) werden nicht nachgeholt — es feuert höchstens der nächste fällige Lauf.

**Manueller Trigger:** `POST /api/v1/agents/{id}/heartbeats/{name}/fire` (Rollen: platform_admin, agent_owner — in der UI der Button „Jetzt ausführen" im Heartbeat-Tab) feuert einen Heartbeat sofort, unabhängig vom Zeitplan. Es gelten dieselben Regeln wie beim Tick: Kill-Switch/Notaus und eine noch offene Aufgabe des letzten Laufs lehnen ab (409), und `last_fired_at` wird fortgeschrieben — der reguläre Zeitplan rechnet ab dem manuellen Lauf weiter. Eine `nur-wenn:`-Bedingung wird beim manuellen Trigger **nicht** geprüft: wer den Button drückt, will den Lauf.

**Gestaffelte Kosten beim Tick:** Der Tick darf nicht jedes Mal Opus anwerfen. Ein kleines, billiges Modell entscheidet zuerst „gibt es überhaupt etwas zu tun?" — bei „nein" schläft der Agent weiter. Erst bei „ja" wird die volle Runtime geweckt. Der Tick ist das, was Proaktivität erzeugt: Ohne externen Trigger merkt der Support-Agent selbst „Ticket #42 wartet seit zwei Tagen auf Kundenantwort, ich hake nach."

### Generischer Webhook-Trigger (optional je Agent)

Neben den Zielsystem-Webhooks (Plugin, Signatur-Prüfung, Korrelation — siehe [`13-zammad-integration.md`](13-zammad-integration.md)) kann jeder Agent **optional** einen generischen Webhook als Wake-Quelle bekommen — für Fremdsysteme ohne eigenes Plugin (CI-Pipelines, Cron-Jobs, Zapier, Monitoring):

- **Aktivierung** über die API/UI (`POST /api/v1/agents/{id}/webhook`, Manager-Rollen) erzeugt ein geheimes Token; erneutes Aktivieren rotiert es (alte URL wird ungültig), `DELETE` deaktiviert. Default ist **aus** — das Token liegt als nullable Spalte am Agenten (`webhook_token`), nicht in der Sandbox.
- **Auslösen:** `POST /api/trigger/{token}` — das Token in der URL ist die gesamte Authentifizierung. Payload optional als JSON `{"title", "body", "priority", "dedup_key"}`; jeder andere Body wird als Roh-Text in den Aufgaben-Body übernommen (nichts geht verloren, auch fremde Payload-Formate nicht).
- **Wirkung:** legt eine reguläre Backlog-Aufgabe mit `origin='webhook:trigger'` an und stößt den Dispatch sofort an — ab dort greift der normale Lifecycle. Kein Plugin, keine Korrelation: Wer korrelierte Wakes braucht, nimmt ein Zielsystem-Plugin.
- **Idempotenz:** optional über `dedup_key` (je Agent gescopt, gleiche `webhook_events`-Tabelle wie der Event-Router) — Retries des Fremdsystems erzeugen keine Duplikate.
- **Fail-closed:** ein gestoppter Agent (Kill-Switch) lehnt den Trigger ab; ein unbekanntes Token liefert 404.

## Zustandsmaschine

```
   ┌──────────┐  Event/Tick/Zeitplan   ┌───────────┐
   │ sleeping │ ─────────────────────▶ │ triggered │
   └──────────┘                        └─────┬─────┘
        ▲                                    │
        │                                    ▼
        │                              ┌───────────┐
        │                              │  triage   │  Backlog + Gedächtnis prüfen
        │                              └─────┬─────┘
        │                                    │
        │                                    ▼
        │                              ┌───────────┐
        │  done                        │  working  │
        └──────────────┐               └──┬─────▲──┘
                       │                  │     │
                  ┌────┴─────┐   block    │     │  korreliertes Event
                  │   done   │◀───────┐   ▼     │
                  └──────────┘        ┌─────────┴─┐
                                      │  blocked  │  wartet auf externes Ereignis
                                      └───────────┘
```

| Zustand | Bedeutung | Compute |
|---|---|---|
| `sleeping` | erreichbar, wartet auf Wake | keiner (nur Dispatch-Loop) |
| `triggered` | eine Wake-Quelle hat gefeuert | minimal (Tick-Entscheidung) |
| `triage` | Backlog + Gedächtnis prüfen, priorisieren | Runtime an |
| `working` | Aufgabe wird in der Sandbox bearbeitet | Runtime an (voll) |
| `blocked` | Aufgabe geparkt, wartet auf externes Ereignis | keiner (suspendiert) |
| `done` | Aufgabe abgeschlossen, Ergebnis + Gedächtnis-Update | Runtime fährt runter |

Der vollständige Zyklus: `sleeping → triggered → triage → working → (blocked ⇄ working) → done → sleeping`.

## Der `blocked`-Zustand

Der Zustand, den fast alle vergessen — und der aus einem Agenten einen Angestellten macht. Ein echter Mitarbeiter parkt Aufgaben („warte auf Antwort vom Kunden", „warte auf Freigabe vom Chef") statt darauf zu pollen oder sich eine Antwort zu halluzinieren.

Der Agent muss sagen können: **„Ich bin auf X geblockt, weck mich, wenn die Antwort kommt"** — und dann *wirklich suspendieren*. Der Daemon meldet `blocked` mit einem **Korrelations-Key** an die Control Plane; die Sandbox wird heruntergefahren. Die `blocked → working`-Kante wird geschlossen, wenn ein eingehendes Event auf diesen Key gemappt wird.

Sauberes `blocked`-Handling ist der Unterschied zwischen „Agent" und „Angestellter".

## Der abgebrochene Lauf: Turn-Limit statt Ergebnis

`blocked` ist das geplante Anhalten. Daneben steht das **ungeplante**: Ein Lauf stößt an sein Turn-Limit (`max_turns`), bevor er zu einem Ergebnis kommt. Er hat gearbeitet — geklont, gelesen, halb gefixt —, aber nichts davon steht in einem Ergebnis, und der Kontext stirbt mit der Sandbox.

Naiv behandelt ist das ein stiller Fehlschlag, und aus ihm entsteht die teuerste Schleife des Systems: Der Heartbeat feuert erneut, ein frischer Lauf fängt dieselbe Arbeit von vorn an, läuft wieder ins Limit — beliebig oft.

Der Runtime-Adapter meldet diesen Fall deshalb als eigenen Status **`incomplete`** (nicht `failed`) und legt zwei Dinge bei:

1. **Den Übergabe-Stand.** Ein einziger zusätzlicher Turn auf der bereits abgebrochenen Session (`--resume`, ohne Werkzeuge) fragt den Agenten nach *Erledigt / Offen / Nächster Schritt*. Auf gecachtem Kontext kostet das fast nichts, verglichen mit dem Lauf, der sonst verloren wäre.
2. **Die Runtime-Session** zum Wiederaufsetzen.

Die Control Plane macht daraus:

- eine **Notiz an der Aufgabe** mit dem Übergabe-Stand (im Board sichtbar, siehe [Notizen](#notizen-zwischenstände-an-der-aufgabe)),
- den Abschluss der Aufgabe als `failed` — mit sprechendem Fehlertext statt leerem Feld — und dem Übergabe-Stand als Ergebnis,
- eine **Folgeaufgabe** (`parent_task_id`, Herkunft `continuation:<task-id>`), die dieselbe Session wieder aufnimmt und dort weiterarbeitet, wo der Lauf abbrach.

Die Folgeaufgabe trägt bewusst **denselben Titel** wie ihre Ursprungsaufgabe: Die Heartbeat-Dedup erkennt daran, dass diese Arbeit noch läuft, und feuert nicht daneben.

**Loop-Schutz.** Eine Fortsetzung, die wieder ins Limit läuft, erzeugt die nächste — aber nicht endlos. Nach drei Fortsetzungen in Folge (`maxContinuations`) eskaliert die Aufgabe an den Vorgesetzten, statt weiterzulaufen. Wer nach vier vollen Läufen kein Ergebnis hat, braucht keinen fünften, sondern einen Menschen: Entweder ist der Auftrag zu groß geschnitten oder `max_turns` zu klein. Ohne diese Grenze ersetzte die Fortsetzung nur eine Endlosschleife durch eine andere.

Der bessere Weg für den Agenten bleibt, gar nicht erst ins Limit zu laufen: Wird eine Aufgabe zu groß, zerlegt er sie selbst (`covey/create_task`, siehe [Teilaufgaben und Delegation](#teilaufgaben-und-delegation-coveycreate_task)) und schließt den laufenden Auftrag mit einem Teilergebnis ab.

## Backlog als First-Class-Objekt

Das Backlog ist **kein flüchtiger Queue**, sondern ein persistentes, inspizierbares Objekt in der Control Plane. Jede Aufgabe trägt:

- **State** (`open`, `in_progress`, `blocked`, `done`, `failed`, `cancelled`),
- **Priorität**,
- **Herkunft** (wer/was hat sie zugewiesen — `manual:<email>`, `heartbeat`, `webhook:<system>`, `webhook:trigger`, `agent:<slug>` für vom Agenten selbst angelegte, `continuation:<task-id>` für die Fortsetzung eines abgebrochenen Laufs),
- **Historie** (Zustandsübergänge, Zeitstempel),
- ggf. **Korrelations-Key** (wenn `blocked`),
- ggf. **Ursprungsaufgabe** (`parent_task_id` — Teilaufgabe, Delegation oder Fortsetzung; trägt zugleich den Loop-Schutz, siehe unten),
- ggf. **Stage** (frei definierbare Kanban-Spalte, siehe unten),
- ggf. **Notizen** (proaktive Zwischenstände des Agenten, siehe unten).

**Terminale States sind keine Sackgassen, und das Backlog wächst nicht ins Unendliche:**

- **Retry:** `failed → open` und `cancelled → open` sind zulässige Übergänge — eine gescheiterte oder verworfene Aufgabe lässt sich manuell **erneut einplanen** (Ergebnis/Fehler werden geleert, die Historie bleibt in den Transitions, der Agent wird geweckt). `done` bleibt final.
- **Archivieren statt löschen:** Terminale Aufgaben (`done`/`failed`/`cancelled`) lassen sich einzeln oder gesammelt („Aufräumen") **archivieren** (`archived_at`). Archiviert heißt: aus dem aktiven Backlog ausgeblendet, aber vollständig erhalten — Historie und Recording-Verweise bleiben gültig, das UI zeigt das Archiv auf Wunsch ein. Aktive Aufgaben (`open`/`in_progress`/`blocked`) sind bewusst nicht archivierbar.

### Stages: Kanban-Overlay über dem State

Der **State** ist die Maschinen-Wahrheit — an ihm hängen Scheduler (`ClaimNext` greift `open`), `blocked`-Suspendierung und Abschluss. Er ist fest und darf nicht frei werden, sonst verliert der Orchestrator seinen Halt.

Darüber liegt eine zweite, **rein anzeigende** Dimension: die **Stage**. Stages sind frei benennbare Kanban-Spalten, **pro Agent** definiert (z. B. `Triage → Recherche → Warten → Antwort → Erledigt`). Sie tragen keine Semantik für den Scheduler — sie machen sichtbar, *wo im eigenen Workflow* eine Aufgabe steht.

- **Der Agent bewegt sich selbst.** Über den Action-Proxy (`covey/set_stage`, siehe [`01-architektur.md`](01-architektur.md)) schiebt der Agent seine laufende Aufgabe in eine Stage; existiert sie nicht, wird sie automatisch als neue Spalte angelegt — der Agent „erfindet" seine Spalten also im Arbeiten.
- **Spalten sind Zustände, keine Überschriften.** Das automatische Anlegen ist bequem und verführt zum Wildwuchs: Ein Agent, der in jedem Lauf einen neuen Namen für dieselbe Tätigkeit prägt (`Issue-Triage`, `GitLab-Sichtung`) oder den Vorgang statt des Zustands benennt (`#83 CSV-Import`), baut sich in Tagen ein Board mit einem Dutzend toter Spalten. Der kompilierte System-Prompt hält deshalb drei Regeln fest: den *Zustand* benennen, bestehende Spalten wiederverwenden, mit einer Handvoll auskommen. Was darüber hinaus dokumentiert werden will, ist eine Notiz, keine Spalte.
- **Auto-Cleanup der Agenten-Spalten.** Spalten, die der Agent selbst angelegt hat (`created_by='agent'`), werden automatisch wieder abgeräumt, sobald keine aktive (unarchivierte) Aufgabe mehr darin liegt — geprüft nach jedem Stage-Move, nach jedem terminalen Zustandsübergang und nach dem Archivieren. So bleibt das Board frei von verwaisten Arbeitszuständen. Menschlich angelegte Spalten (UI, Default-Stages) bleiben stehen, auch wenn sie leer sind.
- **Terminale Aufgaben verlassen die Agenten-Spalte.** Erreicht eine Aufgabe einen terminalen State, führt Auto-Follow sie auch aus einer *selbst erfundenen* Spalte nach „Erledigt" — eine fertige Aufgabe gehört nicht in „Recherche". Erst dadurch greift das Cleanup: Bliebe in jeder erfundenen Spalte eine terminale Aufgabe liegen, wäre keine davon je „leer", und das Board sammelte über Wochen ein Dutzend toter Arbeitszustände. Von **Menschen** angelegte Spalten sind auch hier ausgenommen — bewusste Platzierung wird nie überschrieben.
- **Das Board räumt sich selbst auf.** Auto-Cleanup greift nur bei *leeren* Spalten — eine erledigte Karte, die liegen bleibt, hält ihre Spalte am Leben. Deshalb archiviert die Control Plane getaktet (stündlich) jede terminale Aufgabe, die seit `COVEY_BOARD_RETENTION` (Default 24 h) nicht mehr angefasst wurde; die dadurch leeren Agenten-Spalten fallen mit weg. Archivieren ist kein Löschen: Zustand, Historie und Recording bleiben, die Aufgabe wandert nur aus dem aktiven Board ins Archiv. Bewusst zeitversetzt — frisch Erledigtes soll sichtbar bleiben, sonst verschwindet die Arbeit des letzten Laufs vor den Augen dessen, der sie prüfen will. Eine negative Dauer schaltet das Aufräumen ab; der „Aufräumen"-Knopf im Board bleibt für „und zwar jetzt". Aufräumen ist **Hygiene, keine Entscheidung** — es gehört in die Plattform, nicht in den Prompt eines Agenten, der es unter Last vergisst.
- **Menschen ebenso.** Verwalter ziehen Aufgaben im Board per Drag & Drop und pflegen die Spalten (anlegen, umbenennen, umsortieren, färben, löschen).
- **Persistenz:** Tabelle `agent_stages` (pro Agent, mit `position`/`color`), Aufgabe referenziert `stage_id` (nullable → „Ohne Stage"). Löschen einer Stage setzt betroffene Aufgaben auf `NULL` zurück, nie Datenverlust.
- **Overlay, nicht Ersatz:** Eine Aufgabe hat gleichzeitig einen `state` (z. B. `blocked`) und eine `stage` (z. B. `Warten`). Die Kanban-Spalten des UI kommen aus den Stages; der State steht als Badge auf der Karte.
- **Auto-Follow der Standard-Spalten:** Jeder Agent startet mit `Backlog → In Arbeit → Erledigt`. Solange eine Aufgabe in einer dieser Standard-Spalten (oder in keiner) liegt, führt der Store die Spalte beim Zustandsübergang automatisch nach (`open`→Backlog, `in_progress`/`blocked`→In Arbeit, terminal→Erledigt). Sobald Agent oder Mensch die Aufgabe bewusst in eine **eigene** Spalte legt, gilt manuelle Platzierung — Auto-Follow fasst sie nicht mehr an. Fehlt eine Standard-Spalte (umbenannt/gelöscht), entfällt das Nachführen ersatzlos.

### Notizen: Zwischenstände an der Aufgabe

Neben Stage und State trägt eine Aufgabe **Notizen** (`task_notes`): proaktive Zwischenstände, die der Agent mitten im Lauf über den Action-Proxy anhängt (`covey/add_note`, siehe [`01-architektur.md`](01-architektur.md)) — Befunde, Versuchtes, Arbeitsstand. Die Abgrenzung ist bewusst einfach: **Hilft es nur dieser Aufgabe, ist es eine Notiz an der Aufgabe; hilft es auch künftigen Aufgaben, gehört es ins Gedächtnis** (`covey/remember`, siehe [`05-gedaechtnis.md`](05-gedaechtnis.md)). Notizen hängen an der Aufgabe (Cascade beim Löschen, sichtbar in der Karte im Board) und fließen nicht in die Memory-Abfrage künftiger Aufgaben ein.

### Teilaufgaben und Delegation (`covey/create_task`)

Aufgaben entstehen nicht nur von außen (Mensch, Heartbeat, Webhook, Trigger). Ein Agent kann selbst welche anlegen — über die Meta-Aktion `covey/create_task` am Action-Proxy (siehe [`01-architektur.md`](01-architektur.md)):

- **Teilaufgabe** (ohne `agent`) — der Agent zerlegt Arbeit, die für einen Lauf zu groß ist. Das ist die gesunde Alternative zum Festfahren: Teilergebnis abschließen, Rest als Aufgabe hinterlegen, statt ins Turn-Limit zu laufen.
- **Delegation** (`"agent": "<slug>"`) — die Aufgabe landet beim Kollegen aus derselben Organisation und weckt ihn. Damit wird der Org-Chart aus [`02-agenten-modell.md`](02-agenten-modell.md) operativ: Eskalation und Delegation brauchen kein Sonderprotokoll und keinen Umweg über ein externes Ticketsystem.

Jede so erzeugte Aufgabe hängt über `parent_task_id` an der Aufgabe, aus der sie hervorging, und trägt die Herkunft `agent:<slug>` — im Audit steht also, wer sie erzeugt hat und woraus.

**Ein Agent, der Aufgaben anlegen kann, kann sich selbst beschäftigen, bis das Budget leer ist.** Deshalb fail-closed in vier Richtungen:

| Grenze | Regel |
|---|---|
| Policy | Guard-Rail-Subjekt `covey:create_task`, bei Delegation `covey:create_task:foreign` — getrennt regelbar, `denied`/`pending` wie bei jeder Zielsystem-Aktion |
| Tiefe | Eine Kette selbst erzeugter Aufgaben endet nach `maxAgentTaskDepth` — keine unendliche Zerlegung |
| Breite | Ein einzelner Lauf spaltet höchstens `maxAgentTasksPerRun` Aufgaben ab |
| Dubletten | Existiert beim Ziel-Agenten bereits eine offene Aufgabe gleichen Titels, entsteht keine zweite |

Der Dublettenschutz ist die wichtigste der vier: Ohne ihn baut ein wiederkehrender Lauf, der jedes Mal dieselbe Aufgabe anlegt, eine Warteschlange, die nie leer wird — dieselbe Klasse Fehler wie ein Heartbeat, der auf den Pegel statt auf die Flanke triggert.

Die Delegation bleibt in der Organisation: Der Ziel-Agent wird über seinen Slug **innerhalb der Org des Absenders** aufgelöst, ein pausierter Agent nimmt nichts an. Inter-Agenten-Aufgaben unterliegen damit denselben Recording- und Policy-Regeln wie alles andere (vgl. das Risiko „Agent-zu-Agent-Missbrauch" in [`04-identitaet-secrets.md`](04-identitaet-secrets.md)).

### Ironie/Chance: Backlog = Ticketsystem für Agenten

Das Backlog *ist* im Grunde ein Ticketsystem — für die Agenten selbst. Zwei Optionen:

1. **Bestehendes Ticketsystem zweckentfremden.** Menschen und Agenten teilen dieselbe Aufgaben-Realität; ein Kollege sieht, was der Agent auf dem Tisch hat, und kann umpriorisieren. Stärkt das Org-Chart-Gefühl massiv.
2. **Schlanker eigener Store.** Weniger Kopplung, volle Kontrolle über das Schema.

Option 1 ist überraschend mächtig für die Mitarbeiter-Metapher. Entscheidung offen — siehe [`07-offene-entscheidungen.md`](07-offene-entscheidungen.md).

## Seriell vs. parallel

**Innerhalb eines Agenten strikt seriell:** eine Aufgabe zur Zeit, der Rest wartet im Backlog. Ein LLM mit einem Kontext kann nicht ehrlich jonglieren. Seriell deckt sich mit „ein PC, ein Worker", ist debugbar und konsistent. Nebenläufigkeit *innerhalb* eines Agenten erkauft man sich mit massiver Komplexität bei Memory und Konsistenz.

**Parallelität = mehr Agenten spawnen**, nicht mehr Threads pro Agent. Das ist eine Kostenfrage, keine Feature-Frage.

## Event-Korrelation (offene Kernfrage)

Wenn Agenten geblockt warten und über Events geweckt werden, braucht die Plattform **zuverlässige Korrelation**: Das eingehende Ereignis muss auf die geparkte Aufgabe gemappt werden. Das gilt **kanalunabhängig** — die Antwort kann als Mail, als Ticket-Update, als Webhook oder als Nachricht von einem anderen Agenten kommen. E-Mail ist nur *einer* dieser Kanäle. Zwei Ansätze:

| Ansatz | Mechanik | Bewertung |
|---|---|---|
| **Korrelations-Key** | Der Agent hinterlegt beim `blocked`-Gehen einen Key; der ausgehende Anstoß trägt ihn mit (Task-ID im Subject-Tag / Message-ID bei Mail, Ticket-ID bei Tickets, Callback-Token bei Webhooks); das eingehende Event trägt ihn zurück | einfach, dezentral; anfällig, wenn die Gegenstelle den Key verliert |
| **Zentraler Event-Router** | Control Plane empfängt alle eingehenden Events und mappt sie über Regeln/Heuristiken (Absender, Betreff, Ticket-ID) auf Agenten + Aufgaben | robuster, zentral auditierbar; mehr Logik in der Control Plane |
| **Hybrid** | Korrelations-Key als primärer Match, Router als Fallback-Heuristik | pragmatisch |

Diese Entscheidung bestimmt, wie zuverlässig geparkte Aufgaben über alle Kanäle wieder aufwachen. **Das ist der nächste festzunagelnde Punkt** (siehe [`07-offene-entscheidungen.md`](07-offene-entscheidungen.md)).

## Kostenkonsequenz

Always-on × viele Agenten wird nur bezahlbar, weil **idle wirklich idle** ist. Per-Agent-Budget und hibernierende Sandboxen sind keine spätere Optimierung, sondern Grundvoraussetzung — sonst skaliert die Rechnung beim zehnten Agenten weg. Details zum Budget-Tracking in [`06-observability-control.md`](06-observability-control.md).
