# 12 — Claude-Code-Adapter (`-p` / Headless)

Konkretisiert den Runtime-Adapter aus [`01-architektur.md`](01-architektur.md) für die **erste Runtime des MVP** (M1 in [`11-mvp-plan.md`](11-mvp-plan.md)). Claude Code wird **headless** über den `-p`/`--print`-Modus gesteuert: ein Prompt rein, der volle Agenten-Loop läuft, ein Ergebnis raus, Exit-Code. Genau die programmatische Steuerung, die der Daemon braucht — kein interaktives Terminal.

## Grundmechanik

Der Daemon in der Sandbox ruft Claude Code als **Subprozess** auf:

```bash
claude -p "<Aufgabe>" \
  --output-format stream-json \
  --append-system-prompt "<kompilierte SOUL.md>" \
  --allowedTools "Read,Edit,Bash" \
  --max-turns 20 \
  --max-budget-usd 0.50
```

`-p` schaltet vom interaktiven REPL in einen einzelnen Batch-Lauf; alle CLI-Optionen funktionieren mit `-p`.

## Flag → Covey-Konzept

Die Steuerung mappt fast 1:1 auf das Daemon-Protokoll:

| Claude-Code-Flag | Covey-Konzept |
|---|---|
| `-p "<task>"` | `assign_task` — Aufgabe aus dem Backlog ([`03`](03-lifecycle-scheduling.md)) |
| `--append-system-prompt` / `--system-prompt` | `inject_config` — kompilierte `SOUL.md` ([`02`](02-agenten-modell.md)) |
| `--model <id\|alias>` | `inject_config.model` — Modell je Agent (Registry-Feld, PATCH `/agents/{id}/model`); leer = Default des Binaries/Accounts |
| `--max-turns <n>` | `inject_config.max_turns` — Turn-Limit je Agent (Registry-Feld, PATCH `/agents/{id}/max-turns`); 0 = Orchestrator-Default (30) |
| `--output-format stream-json` | `event`-Strom → Recording ([`06`](06-observability-control.md)) |
| `--output-format json` → `total_cost_usd` | `cost` → Kostenkontrolle ([`06`](06-observability-control.md)) |
| `--resume <session_id>` | `blocked → working` — Wiederaufnahme ([`03`](03-lifecycle-scheduling.md)) |
| `--allowedTools` / `--permission-mode` | Tool-Guard-Rails ([`06`](06-observability-control.md)) |
| `--max-turns`, `--max-budget-usd` | Budget-/Runaway-Guard-Rails ([`06`](06-observability-control.md)) |
| `--mcp-config` | Tool-/Zielsystem-Anbindung ([`04`](04-identitaet-secrets.md)) |
| Exit-Code ≠ 0 | Fehlerpfad → `task_done`(error) |

## `blocked` ↔ Session-Resume (der M4-Kern)

Headless-Läufe sind per Default zustandslos, lassen sich aber **threaden**: `--output-format json` liefert eine `session_id`; ein späterer `claude -p --resume <session_id> "<neue Eingabe>"` lädt den Kontext des bestehenden Laufs.

Das mappt exakt auf Coveys `blocked`-Mechanik:

1. Agent stellt eine Rückfrage → Daemon meldet `blocked` mit **Korrelations-Key**; zusätzlich wird die **`session_id`** des Claude-Code-Laufs zur geparkten Aufgabe gespeichert.
2. Sandbox fährt runter (kein Compute).
3. Korreliertes Event trifft ein (Ticket-Update) → Daemon startet die Sandbox und ruft `claude -p --resume <session_id> "<Antwort>"`.
4. Claude Code stellt den Gesprächskontext **selbst** wieder her — Covey muss ihn nicht rekonstruieren.

Damit ist die `blocked → working`-Kante aus [`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md) mit Bordmitteln der Runtime realisiert.

> **Kurzzeit vs. Langzeit.** Die Claude-Code-Session ist der *kurzzeitige Arbeitskontext* (begrenztes Kontextfenster, ggf. Ablauf). Das *dauerhafte* Gedächtnis über Aufgaben hinweg bleibt Coveys Memory-Schicht ([`05-gedaechtnis.md`](05-gedaechtnis.md)) — beim `done` einspeisen, beim `triage` abfragen. Auf die Session-Persistenz allein sollte man sich für Langzeitwissen nicht verlassen.

## Streaming → Recording

`--output-format stream-json` emittiert NDJSON (ein JSON-Event pro Zeile, Typ `assistant` / `tool_use` / `result`). Der Daemon liest den Strom und leitet jede Zeile als `event`-Nachricht an die Control Plane weiter → lückenloses **Session-Recording** ([`06-observability-control.md`](06-observability-control.md)) praktisch geschenkt, inklusive jedes Tool-Calls.

## Kosten

Das abschließende `result`-Event (bzw. `--output-format json`) enthält `total_cost_usd` samt Aufschlüsselung pro Modell → direkt in Coveys **Cost-Tracking** pro Agent ([`06-observability-control.md`](06-observability-control.md)), ohne separate Usage-Abfrage.

## Auth

Headless braucht nicht-interaktive Credentials: `ANTHROPIC_API_KEY` als ENV, ein langlebiger OAuth-Token via `claude setup-token`, oder Provider-Credentials (Bedrock/Vertex/Foundry). In Covey ist dieser Key selbst ein **gebrokertes Secret** ([`04-identitaet-secrets.md`](04-identitaet-secrets.md)): der Daemon bekommt ihn zur Laufzeit injiziert, er liegt nicht dauerhaft in der Sandbox.

## Permissions & Guard-Rails

Für den vollen nicht-interaktiven Betrieb braucht es `--dangerously-skip-permissions` (überspringt die interaktiven Freigabe-Prompts). Das ist in Covey **vertretbar, weil die Sandbox isoliert ist und die harten Grenzen ohnehin extern erzwungen werden** — am Broker, am Egress, im Tool-Layer ([`06-observability-control.md`](06-observability-control.md)). Die interaktive Freigabe von Claude Code wäre innerhalb der Sandbox redundant; Coveys Guard-Rails sitzen eine Ebene darüber.

Als **Defense-in-Depth** bleibt `--allowedTools` (und `--permission-mode`) trotzdem gesetzt, um den Tool-Umfang schon im Subprozess zu beschneiden — die weiche Innen-Grenze zusätzlich zur harten Außen-Grenze. Der Standard-Umfang (`daemon.DefaultAllowedTools`) deckt die produktive Grundausstattung ab: Dateien lesen/schreiben/bearbeiten/suchen (`Read`, `Write`, `Edit`, `Glob`, `Grep`, `NotebookEdit`), Shell (`Bash`, `BashOutput`, `KillShell`), Web (`WebFetch`, `WebSearch`) sowie `Task`/`TodoWrite`. Web-Zugriffe laufen dabei weiterhin durch den Egress-Proxy — die Allowlist bleibt die harte Grenze.

## Sub-Run im Projekt-Checkout

Ein Lauf startet im **Agenten-Home** (`/home/agent`) — dort liegen `~/.claude`, die Wiki-Arbeitskopie und die Dependency-Caches. Der Quellcode eines Projekts landet dagegen unter `~/repos/<projekt>-<ref>/` ([`13-zammad-integration.md`](13-zammad-integration.md) beschreibt das Muster für Zielsysteme; die `checkout`-Aktion des GitLab-Plugins entpackt das Archiv dorthin). Claude Code sucht Projekt-Memory (`CLAUDE.md`), `.claude/agents`, Skills und Commands aber **relativ zum Arbeitsverzeichnis** — vom Home aus sieht ein Agent davon nichts.

Das kostet doppelt: Der Agent leitet die Projektstruktur bei jedem Heartbeat-Lauf neu her (frischer Prozess, gedeckeltes Turn-Budget), und die Konventionen des Projekts wirken nicht auf das Ergebnis.

Deshalb trennt `RunSpec` **Arbeitsverzeichnis und Home**: `WorkDir` setzt das cwd des Subprozesses, `HomeDir` bleibt `HOME`. Darauf setzt der **Sub-Run** auf — ein geschachtelter Lauf desselben Adapters, der im Checkout startet und dort den Harness des Projekts vollständig vorfindet. Der Agent stößt ihn über die Aktion `dev:agent` an; ausgeführt wird er vom Daemon (`SubAgentRunner`, per Context an das Plugin gereicht wie `Workdir` und die Artefakt-Senke).

Die Rollenteilung ist der Kern:

| | Äußerer Lauf | Sub-Run |
|---|---|---|
| Arbeitsverzeichnis | Agenten-Home | Projekt-Checkout |
| Prompt | kompilierte Agenten-Config (`SOUL.md` …) | Harness des Projekts + knapper Auftragsrahmen |
| Zielsysteme | über den Action-Proxy | **keine** — kein `COVEY_ACTION_PORT` |
| Aufgabe | Triage, Kommunikation, `commit`, Merge Request, Gedächtnis | verstehen, ändern, bauen, testen |

Der Sub-Run erreicht **keine Zielsysteme**: Ohne Action-Proxy kommt er weder an Ticketsystem noch an Mail und kann nicht einchecken. Das hält die Grenze scharf (die Kommunikation bleibt beim Agenten, der das Protokoll kennt) und nimmt zugleich Instruktionen aus fremdem Repo-Inhalt den Weg zu den gebrokerten Zugängen. Dazu gehört, dass **kein Subprozess die `COVEY_*`-Umgebung des Daemons erbt**: In ihr stehen `COVEY_WS_URL` und `COVEY_DAEMON_TOKEN` — damit ließe sich eine eigene WebSocket zur Control Plane öffnen und `request_credential` schicken, also der Broker direkt ansprechen. Der Adapter filtert diese Variablen deshalb aus der Umgebung jedes Laufs (`daemon.childEnv`); was ein Lauf legitim braucht, gibt der Aufrufer ausdrücklich mit. Auch die `git`-Aufrufe, mit denen der Daemon *nach* dem Sub-Run die Dateiliste zieht, laufen so — `git` führt Kommandos aus, die in der Repository-Konfiguration stehen (`core.fsmonitor`, Filter), und die ist nach dem Sub-Run nicht vertrauenswürdiger als der übrige Checkout.

**Was der Sub-Run trotzdem hat** — und was daraus folgt: Die Auto-Discovery, die ihn erst sinnvoll macht, lädt *ausführbare* Konfiguration aus dem Repository — Hooks, MCP-Server-Definitionen, Skills, Subagenten. Zusammen mit `--dangerously-skip-permissions` heißt das: **Repo-Inhalt ist Code, der in der Sandbox läuft.** Der äußere Lauf im Home tat das nie; mit dem Sub-Run ist es eine bewusste Erweiterung der Angriffsfläche. Die Zielsysteme sind sauber draußen (siehe oben), es bleiben aber drei Dinge in Reichweite:

- der **gebrokerte LLM-Key** (er muss ins ENV, sonst läuft die Runtime nicht),
- das **Netz innerhalb der Egress-Allowlist**,
- das **Agenten-Home**. `HOME` bleibt bewusst geteilt — ohne `~/.claude` und die Dependency-Caches wäre der Sub-Run weder lauffähig noch inkrementell. Damit liest Repo-gelieferter Code aber auch die Wiki-Arbeitskopie (das Gedächtnis des Agenten), andere Checkouts unter `~/repos` und alles sonst im Home. Zusammen mit dem erlaubten Egress ist das ein Exfiltrationspfad.

Für Repositories der eigenen Organisation ist das die richtige Abwägung — sie sind ohnehin die Quelle des Codes, den der Agent baut und ausführt. Bei fremden Repositories (Fork, externer Auftrag) ist es das nicht: Dort gehört der Sub-Run über die Guard-Rail auf `dev:agent` freigabepflichtig gemacht oder verboten, und die Egress-Allowlist eng gezogen. Ein Feingranular-Schalter („Harness laden, aber ohne Hooks/MCP") ist bislang nicht vorgesehen — die Runtime kennt ihn nicht.

Beobachtbarkeit und Kostenkontrolle bleiben erhalten, weil der Sub-Run dieselben Protokoll-Nachrichten benutzt: Seine stream-json-Zeilen fließen (als Sub-Lauf markiert) als `event` in dieselbe Aufzeichnung, seine `cost`-Meldung geht durch `AddCost` und die Budget-Prüfung. **Der Deckel ist damit gröber als beim äußeren Lauf**: `total_cost_usd` liefert Claude Code erst mit dem Result-Event, also erst nach bis zu 200 Turns des Sub-Runs. Zwischen zwei Budget-Prüfungen kann jetzt mehr auflaufen als vorher — umgehen kann ein Sub-Run das Budget nicht, aber überziehen. Feinkörniger würde es nur mit Zwischen-Meldungen aus der laufenden Session (offener Punkt). Das Guard-Rail-Subjekt `dev:agent` macht den Sub-Run zentral verbietbar oder freigabepflichtig.

Die Markierung ist ein zusätzlicher Schlüssel `covey_sub_agent` **im** Zeilen-Objekt, keine Hülle darum: Aufzeichnung und Timeline lesen das stream-json-Format direkt, eine Hülle würde `type` verdecken und der Sub-Run stünde als JSON-Klumpen in der Aufzeichnung statt als Turn mit seinen Tool-Aufrufen — ausgerechnet dort, wo die eigentliche Arbeit passiert. Sie trägt das Arbeitsverzeichnis (`dir`) und, **nur auf der ersten Zeile eines Laufs**, den Arbeitsauftrag (`task`, gekürzt). Nur auf der ersten, weil er sonst mit seiner Länge × Zeilenzahl in die Aufzeichnung ginge; als Überschrift genügt er einmal. Die Timeline fasst zusammenhängend markierte Zeilen daraufhin zu einem eigenen, zugeklappten Block zusammen ([`06-observability-control.md`](06-observability-control.md)).

Pro Checkout läuft **höchstens ein Sub-Run**: Der Action-Proxy bedient jede Anfrage in einer eigenen Goroutine und die Runtime ruft Werkzeuge durchaus parallel auf — zwei Läufe im selben Verzeichnis schrieben sich gegenseitig die Dateien um und meldeten beide denselben kumulativen Stand. Ein zweiter Auftrag mit demselben `cwd` wird deshalb abgelehnt, solange der erste arbeitet; verschiedene Verzeichnisse laufen parallel.

Damit der Agent hinterher weiß, **was** geändert wurde, legt der Checkout ein git-Repository mit dem Upstream-Stand als Baseline-Commit an (das Archiv bringt keine `.git` mit) und markiert ihn mit dem Tag `covey-baseline`. Der Sub-Run meldet die Differenz **zu diesem Commit** als `changed_files`/`deleted` zurück — genau die Listen, die die `commit`-Aktion erwartet. Gemessen wird gegen einen Commit und nicht gegen ein `git status`-Abbild von vorher, weil der Sub-Agent im Checkout lokal committen darf: Viele Projekte verlangen das in ihrer `CLAUDE.md`, und nach einem Commit zeigt `git status` nichts mehr — die Arbeit läge fertig auf Platte, der Bericht wäre leer. Erfasst wird deshalb beides zusammen: was seit der Baseline committet wurde und was daneben im Arbeitsverzeichnis offen liegt. Die Listen sind damit **kumulativ**: Sie beschreiben den gesamten Stand gegen Upstream, auch die Arbeit eines früheren Sub-Runs derselben Aufgabe — genau das, was in den Merge Request gehört. Gelesen werden sie NUL-getrennt und ohne git-Quoting (`-z`, `core.quotepath=false`); sonst käme `prüfung.go` als `pr\303\274fung.go` im Bericht an und ginge so weiter in die `commit`-Aktion. Nebeneffekt der Baseline: Projekt-Skripte, die `git` aufrufen, funktionieren.

## CLI (`-p`) vs. Agent SDK

Anthropic bietet neben der CLI auch ein **Agent SDK** (Python/TypeScript) zum Einbetten als Bibliothek; die offizielle Empfehlung ist SDK für Produktions-Automatisierung, CLI für Skripte. Da Coveys Daemon in **Go** läuft und es kein offizielles Go-SDK gibt, ist der pragmatische Weg der **Subprozess-Aufruf von `claude -p`**: Prozess starten, stdin/stdout, Exit-Code — wie jedes andere CLI. Entstünde der Daemon-Teil je in Python/TS, wäre das SDK die reichere Alternative (native Message-Objekte, Tool-Approval-Callbacks).

## Hinweise / offene Punkte

- **`--bare`** überspringt Auto-Discovery (Hooks/Skills/MCP/CLAUDE.md) und macht Läufe deterministisch — empfohlen für Skripte, wird künftig Default für `-p`. Trade-off: MCP-Server müssen dann explizit per `--mcp-config` übergeben werden. Für den **äußeren** Lauf sinnvoll, weil Reproduzierbarkeit über lokale Zufalls-Config zählt. Für den **Sub-Run** wäre es das Gegenteil des Zwecks: Dort ist die Auto-Discovery des Projekt-Harness genau das, was gewollt ist (siehe oben) — falls `--bare` Default wird, muss der Sub-Run es explizit abwählen.
- **Exit-Codes:** nicht-null bei Fehler (z. B. `--max-turns` erreicht), aber keine stabile globale Code-Tabelle — auf ≠ 0 prüfen, keine spezifischen Codes annehmen.
- **Hintergrund-Tasks**, die Claude Code startet (z. B. Dev-Server), werden nach dem Ergebnis mit kurzer Gnadenfrist beendet; ein Deckel verhindert Blockieren. Für einen Support-Agenten selten relevant, für spätere Coding-Agenten zu beachten.
- Geprüft gegen die Claude-Code-Headless-Doku (`code.claude.com/docs/en/headless`, Stand Juli 2026); die Flags entwickeln sich — vor Baubeginn kurz gegenchecken.
