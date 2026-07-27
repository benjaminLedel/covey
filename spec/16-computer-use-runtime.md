# 16 — Computer-Use-Runtime (voller Desktop, Maus/Tastatur/Screenshot)

Design-Spike für eine **zweite Runtime** neben dem Claude-Code-Adapter ([`12-claude-code-adapter.md`](12-claude-code-adapter.md)): ein Agent, der einen **vollwertigen Desktop** bedient — navigiert, klickt, tippt, Screenshots macht — statt nur eine Shell und Dateiwerkzeuge zu fahren. Anders als ein Browser-Zielsystem ist das **kein Target-Plugin**, sondern ein **Runtime-Adapter** an derselben Naht wie Claude Code: es ändert, *wie* die LLM-Schleife läuft, nicht *welches Zielsystem* der Agent bedient.

> **Status: Spike, nicht gebaut.** Dieses Dokument klärt Architektur, offene Fragen und Risiken *vor* dem Bau — im Sinne der Risiko-Meilenstein-Regel aus [`11-mvp-plan.md`](11-mvp-plan.md) (Design-Spike vor M-Bau). Die konkreten Tool-Versionen/Header sind gegen die Anthropic-Doku (Stand Juli 2026) notiert und **vor Baubeginn gegenzuchecken** — sie entwickeln sich.

## Warum Runtime, nicht Zielsystem

Die Runtime-Abstraktion aus [`01-architektur.md`](01-architektur.md) trennt zwei Dinge:

- **Zielsysteme** (`internal/target/…`) sind *was* der Agent bearbeitet (Zammad, Nextcloud, GitLab) — angebunden über den Action-Proxy, gebrokerte Credentials, Guard-Rail-Subjekte pro Aktion.
- **Runtimes** (`internal/daemon/`, Registry `RegisterRuntime`) sind *wie* die Agenten-Schleife fährt — Claude Code headless ist die erste, austauschbar hinter dem `Runtime`-Interface (`Name()` + `Run(ctx, spec, onEvent)`).

Computer Use ist Letzteres: Claude Code kann **kein** natives Computer Use. Das läuft nur über die **Messages-API** mit dem Beta-Tool `computer_20251124` (plus `bash_20250124` und `text_editor_20250728` — die client-seitig ausgeführten Anthropic-Tools). Also eine eigene Agenten-Schleife, kein `claude -p`-Subprozess.

Damit bleibt die ganze Control Plane unverändert: Scheduler, Backlog, Kosten-/Status-Logik, Recording, Guard-Rails am Egress sitzen eine Ebene über der Runtime und wissen nicht, ob dahinter Claude Code oder Computer Use läuft. Der Daemon wählt die Runtime weiter per `cfg.Runtime`.

## Mechanik: die Agenten-Schleife

Der neue Adapter (`internal/daemon/runtime_computeruse.go`) implementiert `Runtime.Run` als **Tool-Use-Loop gegen die Messages-API** — Anthropics `computer`-Tool ist *client-side*: das Modell liefert `tool_use`-Aktionen, der Daemon führt sie **in der Sandbox** aus und schickt einen Screenshot als `tool_result` zurück.

```
assign_task ──▶ Messages.create(model, tools=[computer,bash,text_editor], system=SOUL.md)
                     │
      ┌──────────────▼─────────────── loop (stop_reason == tool_use) ───────────┐
      │  Modell: tool_use {action: screenshot | left_click | type | key | …}     │
      │  Daemon: Aktion via xdotool/scrot AUSFÜHREN (in der Sandbox)             │
      │  Daemon: Screenshot als tool_result-Bild ZURÜCK  ──▶ Messages.create(…)  │
      └──────────────────────────────────────────────────────────────────────────┘
                     │  stop_reason == end_turn  → COVEY_STATUS parsen
                     ▼
              RunResult{status, result, cost, tokens}
```

- **Tools:** `computer_20251124` (Screenshot + Maus/Tastatur/Scroll), zusätzlich `bash_20250124` und `text_editor_20250728` für Datei-/Shell-Arbeit ohne den Umweg über die GUI. Beta-Header `computer-use-2025-11-24`.
- **Modell:** ein computer-use-fähiges Modell (z. B. `claude-sonnet-5` mit `computer_20251124`, alternativ `claude-opus-4-8`). Auf allen Plattformen noch **Beta**.
- **Effort/Auflösung:** `effort: "xhigh"` für die anspruchsvollen Fälle; Screenshots in **1080p** als guter Kompromiss aus Genauigkeit und Bild-Token-Kosten (720p/1366×768 für kostensensible Läufe).
- **Kosten:** aus `usage`-Tokens je Iteration akkumuliert → `RunResult.CostUSD`/`InputTokens`/`OutputTokens` (kein separater `total_cost_usd` wie bei `claude -p`).
- **Recording:** jeder Screenshot + jede Aktion geht über `onEvent` an die Control Plane → das Session-Recording ([`06-observability-control.md`](06-observability-control.md)) wird praktisch ein **Bildschirm-Mitschnitt**. Das ist der stärkste Auftritt des Observability-Features.
- **COVEY_STATUS:** dieselbe Abschluss-Konvention wie bei Claude Code (`applyStatus` in `runtime.go`) — der System-Prompt verlangt die Statuszeile, der Adapter parst sie aus dem letzten Text-Block.

## Der Desktop in der Sandbox

Die schwere Infra ist ein **Sandbox-Image mit virtuellem Desktop** (im Grunde Anthropics `computer-use-demo`-Image adaptiert):

- **Xvfb** — virtueller X11-Framebuffer (kein physischer Bildschirm).
- **Leichter Window-Manager** (openbox/mutter) + **xdotool** (Maus/Tastatur) + **scrot** (Screenshots).
- **Echter Browser** (Chromium/Firefox) und die Apps, die der Agent bedienen soll.
- Optional **x11vnc + noVNC** — damit ein Mensch dem Agenten **live zusieht oder übernimmt** (Approval/Takeover). Passt zur Trust-by-Design-Leitlinie: der Kill-Switch bekommt ein Fenster.

Der Desktop läuft als persistenter Prozess über eine Wach-Phase (Cookies/Tabs/Zustand bleiben) und wird beim Einschlafen beendet — dafür gibt es bereits den `OnShutdown`-Hook (wie beim `dev`-Supervisor). „Dumm und ersetzbar" gilt weiter: geht die Sandbox verloren, wird der Desktop aus dem Image neu gebaut, der Zustand liegt im persistenten `/home`.

## Auth: API-Key statt Abo-Token

Computer Use ist **API-only** — es braucht einen `ANTHROPIC_API_KEY`, **nicht** den Claude-Code-Abo-OAuth-Token (`claude_code_oauth_token`). Der Descriptor wird also `NeedsCredential: true` gesetzt; der Key ist ein **gebrokertes Secret** ([`04-identitaet-secrets.md`](04-identitaet-secrets.md)) und wird pro Lauf injiziert, liegt nicht dauerhaft in der Sandbox — dieselbe Leitplanke wie beim Claude-Code-Key.

## `blocked` ↔ Wiederaufnahme

Claude Code löst die `blocked`-Kante ([`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md)) über `--resume <session_id>` mit Bordmitteln. Die Messages-API hat **keine** solche serverseitige Session — der Kontext ist die `messages`-Liste, die der Client hält. Für Computer Use heißt das: bei `blocked` müsste der Adapter den **Konversations-Verlauf** (inkl. der jüngsten Screenshots) selbst zur geparkten Aufgabe persistieren und beim korrelierten Wake wieder einspeisen. Das ist teurer als bei Claude Code (große Bild-Kontexte) — Optionen: nur die letzten N Screenshots behalten, serverseitige Compaction/Context-Editing der Messages-API nutzen, oder `blocked` in dieser Runtime bewusst einschränken. **Offene Entscheidung.**

## Zwei ehrliche Spannungen

1. **Guard-Rails passen schlechter.** Coveys Sicherheitsmodell erzwingt *pro Aktion* zentral (`nextcloud:delete` approval-pflichtig, Egress-Allowlist). „Klick auf Pixel (x,y)" ist opak — es lässt sich nicht sauber pro Aktion freigeben. Was **bleibt** und weiter greift: die zentrale **Egress-Sperre** gatet den Netzausgang der Sandbox (der Browser kommt nur auf erlaubte Hosts). Was **grobkörniger** wird: „genehmige genau diese Aktion" → praktisch nur ganze-Session-Approval (über den noVNC-Live-View mit Takeover) oder Host-basierte Steuerung über Egress. Diese Runtime gehört per Default **hinter Approval** und hinter eine enge Egress-Allowlist.
2. **Maximale Angriffsfläche.** Ein voller Desktop mit Browser ist das mächtigste Werkzeug überhaupt. Fail-closed am Egress bleibt die harte Grenze; die weiche Innen-Grenze (`--allowedTools` bei Claude Code) hat hier kein direktes Gegenstück — der Tool-Satz ist fix (`computer`/`bash`/`text_editor`).

## Was gebaut werden muss (Phasen)

1. **`runtime_computeruse.go`** — Agenten-Loop gegen die Messages-API (`computer`/`bash`/`text_editor`), Aktions-Ausführung via xdotool/scrot, Screenshots als `tool_result`, Kosten/Status/Events. `RegisterRuntime({Name:"computer-use", NeedsCredential:true, New:…})`.
2. **Sandbox-Image** — Xvfb + WM + xdotool + scrot + Browser; als eigenes Runtime-Image (die Runtime-Wahl bestimmt das Basis-Image).
3. **noVNC-Live-View** — im Web-UI ein Fenster auf den laufenden Desktop (Zusehen + Takeover) → speist Recording und Kill-Switch.
4. **Guard-Rail-Integration** — Session-Approval-Gate für diese Runtime, plus die bestehende Egress-Allowlist als harte Grenze.

## Offene Entscheidungen (für [`07-offene-entscheidungen.md`](07-offene-entscheidungen.md))

- **D-CU1:** `blocked`-Wiederaufnahme ohne serverseitige Session — Verlauf persistieren vs. `blocked` in dieser Runtime einschränken vs. Compaction.
- **D-CU2:** Guard-Rail-Granularität — reicht Session-Approval + Egress, oder braucht es einen Aktions-Filter vor der Ausführung (z. B. Klick-Ziel gegen eine Allowlist)?
- **D-CU3:** Kosten-Deckel — Bild-Kontexte sind teuer; harter `max_turns`/Budget-Stopp und Screenshot-Auflösung als Stellschrauben.
- **D-CU4:** Modell-Wahl je Agent (Registry-Feld `model`) — computer-use-fähige Modelle nur für diese Runtime zulassen.
- **D-CU5:** Verhältnis zu einem reinen `browser`-Zielsystem (headless Chrome via CDP, ohne Desktop) — leichteres, guard-rail-freundlicheres Mittelding; ggf. beides anbieten.
