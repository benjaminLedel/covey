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

## CLI (`-p`) vs. Agent SDK

Anthropic bietet neben der CLI auch ein **Agent SDK** (Python/TypeScript) zum Einbetten als Bibliothek; die offizielle Empfehlung ist SDK für Produktions-Automatisierung, CLI für Skripte. Da Coveys Daemon in **Go** läuft und es kein offizielles Go-SDK gibt, ist der pragmatische Weg der **Subprozess-Aufruf von `claude -p`**: Prozess starten, stdin/stdout, Exit-Code — wie jedes andere CLI. Entstünde der Daemon-Teil je in Python/TS, wäre das SDK die reichere Alternative (native Message-Objekte, Tool-Approval-Callbacks).

## Hinweise / offene Punkte

- **`--bare`** überspringt Auto-Discovery (Hooks/Skills/MCP/CLAUDE.md) und macht Läufe deterministisch — empfohlen für Skripte, wird künftig Default für `-p`. Trade-off: MCP-Server müssen dann explizit per `--mcp-config` übergeben werden. Für Covey sinnvoll, weil Reproduzierbarkeit über lokale Zufalls-Config zählt.
- **Exit-Codes:** nicht-null bei Fehler (z. B. `--max-turns` erreicht), aber keine stabile globale Code-Tabelle — auf ≠ 0 prüfen, keine spezifischen Codes annehmen.
- **Hintergrund-Tasks**, die Claude Code startet (z. B. Dev-Server), werden nach dem Ergebnis mit kurzer Gnadenfrist beendet; ein Deckel verhindert Blockieren. Für einen Support-Agenten selten relevant, für spätere Coding-Agenten zu beachten.
- Geprüft gegen die Claude-Code-Headless-Doku (`code.claude.com/docs/en/headless`, Stand Juli 2026); die Flags entwickeln sich — vor Baubeginn kurz gegenchecken.
