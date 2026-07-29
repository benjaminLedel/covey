package dev

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"covey/internal/target"
)

// System bindet das dev-Plugin an die target-Registry: der lokale Werkzeug-
// kasten der Sandbox, ohne externes Zielsystem und ohne Credentials.
type System struct{}

func init() {
	target.Register(target.Descriptor{
		Name:          "dev",
		Label:         "Dev-Sandbox",
		Description:   "Der eigene Computer des Agenten: Shell-Befehle in der Sandbox ausführen (exec), langlaufende Prozesse verwalten (start/stop/logs/list) — Dev-Server, Datenbanken, headless Chrome — und die eigentliche Programmierarbeit an einen Sub-Agenten im Projekt-Checkout übergeben (agent). Der Sub-Agent arbeitet mit dem Claude-Code-Harness des Projekts selbst (CLAUDE.md, .claude/agents, Skills, Commands) und erreicht dabei keine Zielsysteme. Läuft vollständig im Daemon der Sandbox; braucht keine Secrets.",
		Kind:          "builtin",
		Category:      target.CategoryDev,
		System:        System{},
		NoCredentials: true,
		SetupDoc: `1. Plugin hier aktivieren — Secrets sind nicht nötig, alle Aktionen
   laufen lokal in der Sandbox des Agenten (nichts auf der Control Plane).

2. In der ACCESS.md des Agenten freischalten:
   - system: dev scope: exec,processes,agent

3. Soll der Agent Pakete installieren oder einen Browser laden, die
   nötigen Hosts über die Egress-Templates freigeben (npm, PyPI, Go, …).

Hinweis: Mit start gestartete Prozesse leben bis zum Ende der Wach-Phase
des Agenten — beim Einschlafen der Sandbox werden sie beendet.

Zur Aktion agent (Sub-Agent im Projekt-Checkout): Sie startet einen zweiten
Runtime-Lauf IM ausgecheckten Projekt, damit dort dessen eigener
Claude-Code-Harness greift (CLAUDE.md, .claude/agents, Skills, Commands).
Der Sub-Agent läuft hermetisch — kein Action-Proxy, also kein Zugriff auf
GitLab, E-Mail oder andere Zielsysteme; commit und Kommunikation bleiben beim
beauftragenden Agenten. Seine Kosten zählen auf dasselbe Budget, seine Events
stehen (als Sub-Lauf markiert) in derselben Aufzeichnung. Wer das nicht will,
verbietet die Aktion zentral per Guard-Rail auf dem Subjekt dev:agent.`,
	})
	target.OnShutdown(super.shutdown)
}

func (System) Name() string { return "dev" }

// VerifyWebhook/ParseWebhook: das dev-Plugin hat keinen Webhook-Eingang —
// es gibt kein externes System, das Ereignisse schicken könnte.
func (System) VerifyWebhook(string, []byte, http.Header) bool { return false }

func (System) ParseWebhook([]byte) (target.WebhookEvent, error) {
	return target.WebhookEvent{}, fmt.Errorf("dev hat keinen webhook-eingang")
}

func (System) ActionSubject(action string, _ json.RawMessage) string {
	return "dev:" + action
}

func (System) Execute(ctx context.Context, action string, params json.RawMessage, _ target.Credential) (any, error) {
	var in struct {
		Cmd         string `json:"cmd"`
		Name        string `json:"name"`
		Cwd         string `json:"cwd"`
		TimeoutSecs int    `json:"timeout_secs"`
		TailLines   int    `json:"tail_lines"`
		Task        string `json:"task"`
		Model       string `json:"model"`
		MaxTurns    int    `json:"max_turns"`
	}
	if err := json.Unmarshal(params, &in); err != nil {
		return nil, fmt.Errorf("params: %w", err)
	}
	dir := resolveDir(target.Workdir(ctx), in.Cwd)

	switch action {
	case "exec":
		return runExec(ctx, in.Cmd, dir, in.TimeoutSecs)
	case "agent":
		return runSubAgent(ctx, in.Cwd, in.Task, in.Model, in.MaxTurns)
	case "start":
		return super.start(in.Name, in.Cmd, dir)
	case "stop":
		return super.stop(in.Name)
	case "logs":
		return super.logs(in.Name, in.TailLines)
	case "list":
		return super.list(), nil
	default:
		return nil, fmt.Errorf("unbekannte aktion %q", strings.TrimSpace(action))
	}
}

// runSubAgent übergibt einen Arbeitsauftrag an einen geschachtelten
// Runtime-Lauf, der IM angegebenen Projekt-Checkout startet. Erst dadurch
// greift der Claude-Code-Harness des Projekts (CLAUDE.md, .claude/agents,
// Skills, Commands) — der äußere Lauf sieht ihn nie, weil er im Agenten-Home
// läuft. Den Lauf selbst fährt der Daemon; das Plugin kennt ihn nur als
// Runner im Context (wie Workdir und die Artefakt-Senke).
func runSubAgent(ctx context.Context, cwd, task, model string, maxTurns int) (any, error) {
	if strings.TrimSpace(cwd) == "" {
		return nil, fmt.Errorf("cwd fehlt: der Sub-Agent startet im Projekt-Checkout (Pfad aus dem checkout-Ergebnis)")
	}
	if strings.TrimSpace(task) == "" {
		return nil, fmt.Errorf("task fehlt: beschreibe den Arbeitsauftrag so, dass ein Kollege ohne deinen Kontext arbeiten kann")
	}
	run := target.SubAgent(ctx)
	if run == nil {
		return nil, fmt.Errorf("kein Sub-Agent verfügbar (Aktion läuft außerhalb einer Sandbox)")
	}
	return run(ctx, target.SubAgentRequest{Dir: cwd, Task: task, Model: model, MaxTurns: maxTurns})
}

// resolveDir löst cwd relativ zum Sandbox-Home auf; absolute Pfade bleiben —
// die Sandbox ist der eigene Rechner des Agenten, es gibt hier keine
// Pfad-Grenze zu verteidigen (die Isolation liefert die Sandbox selbst).
func resolveDir(workdir, cwd string) string {
	cwd = strings.TrimSpace(cwd)
	switch {
	case cwd == "":
		return workdir
	case filepath.IsAbs(cwd):
		return cwd
	default:
		return filepath.Join(workdir, cwd)
	}
}

const (
	defaultExecTimeout = 120 * time.Second
	maxExecTimeout     = 900 * time.Second
)

// runExec führt einen Shell-Befehl synchron aus. Ein Exit-Code ungleich 0
// ist ein Ergebnis, kein Fehler — der Agent soll Testfehlschläge lesen,
// nicht einen Action-Error erraten. Der Timeout tötet die ganze
// Prozessgruppe, damit kein `sh -c`-Kind hängen bleibt.
func runExec(ctx context.Context, command, dir string, timeoutSecs int) (any, error) {
	if strings.TrimSpace(command) == "" {
		return nil, fmt.Errorf("cmd fehlt")
	}
	timeout := defaultExecTimeout
	if timeoutSecs > 0 {
		timeout = time.Duration(timeoutSecs) * time.Second
	}
	if timeout > maxExecTimeout {
		timeout = maxExecTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = dir
	buf := &logBuffer{}
	cmd.Stdout, cmd.Stderr = buf, buf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}

	start := time.Now()
	err := cmd.Run()
	output, truncated := buf.Tail(0)
	out := map[string]any{
		"exit_code":   0,
		"output":      output,
		"truncated":   truncated,
		"duration_ms": time.Since(start).Milliseconds(),
	}
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		out["exit_code"] = -1
		out["error"] = fmt.Sprintf("timeout nach %s — für Langläufer start statt exec nutzen", timeout)
	case err == nil:
	default:
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			out["exit_code"] = ee.ExitCode()
		} else {
			return nil, fmt.Errorf("exec: %w", err)
		}
	}
	return out, nil
}

func (System) PromptDoc() string {
	return `Verfügbare dev-Aktionen — deine Sandbox ist dein eigener Rechner, alles hier läuft lokal darin:
   agent {"cwd":"<Pfad aus dem checkout-Ergebnis>","task":"<Arbeitsauftrag>","max_turns":60} —
   übergibt die eigentliche PROGRAMMIERARBEIT an einen Sub-Agenten, der IM Projekt-Checkout startet.
   Das ist der wichtigste Unterschied zu allem anderen hier: Nur dort gelten die Regeln des Projekts —
   seine CLAUDE.md, seine .claude/agents (z. B. getrennte Frontend-/Backend-Spezialisten), Skills und
   Commands. Du selbst arbeitest im Home und siehst davon nichts; deshalb liest du den Code nicht
   selbst zusammen, sondern beauftragst den Sub-Agenten.
   Der Auftrag ist eine ÜBERGABE an einen Kollegen ohne deinen Kontext — schreibe hinein: was kaputt ist
   bzw. gebaut werden soll, wie man es reproduziert, die Abnahmekriterien, bekannte Fundstellen
   (Datei:Zeile) und Auflagen (minimal-invasiv, Tests ausführen, Test ergänzen). Kein "siehe oben".
   Der Sub-Agent kann lesen, ändern, bauen und testen — er erreicht WEDER GitLab NOCH E-Mail und kann
   nicht committen. Das Einchecken und die gesamte Kommunikation bleiben bei dir.
   Ergebnis: {"result":"<Bericht: Ursache, Änderung, Verifikation>","changed_files":[…],"deleted":[…],
   "cost_usd":…,"turns_exhausted":true|false}. Die Dateilisten gehen unverändert in die commit-Aktion;
   sie nennen den GESAMTEN Stand gegenüber dem Checkout — also auch die Arbeit eines früheren
   Auftrags an derselben Aufgabe, nicht nur die des letzten.
   Bei "turns_exhausted":true war der Auftrag zu groß: Schließe mit dem Teilergebnis ab und lege den
   Rest per covey/create_task an, statt es erneut im selben Lauf zu versuchen.
   exec {"cmd":"npm test","cwd":"unterordner (optional, relativ zu deinem Home)","timeout_secs":120} —
   führt einen Shell-Befehl aus und liefert exit_code + output (letzte Ausgabe, gekappt). Für Builds,
   Tests, Installationen; ein Exit-Code ungleich 0 kommt als Ergebnis zurück, lies den Output.
   start {"name":"app","cmd":"npm run dev","cwd":"..."} — startet einen LANGLAUFENDEN Prozess im
   Hintergrund (Dev-Server, Datenbank, headless Browser); er läuft weiter, während du andere Aktionen
   ausführst. logs {"name":"app","tail_lines":100} zeigt seine letzte Ausgabe, list {} alle Prozesse
   mit Status, stop {"name":"app"} beendet die ganze Prozessgruppe (SIGTERM, nach 5s SIGKILL).
   Typischer Ablauf, um eine Web-App wirklich laufen zu sehen statt nur ihren Code zu lesen:
   1. Abhängigkeiten installieren: exec {"cmd":"npm install"} (bzw. pip/go — die Paket-Registries
      müssen im Egress freigegeben sein).
   2. App hochfahren: start {"name":"app","cmd":"npm run dev"} und mit logs prüfen, ob sie lauscht.
   3. Dagegen testen: exec {"cmd":"curl -s http://localhost:PORT/..."} — oder mit einem headless
      Browser, z. B. start {"name":"chrome","cmd":"chromium --headless=new --remote-debugging-port=9222 --no-sandbox"}
      und dann per DevTools-Protokoll bzw. einem selbst installierten Playwright/Puppeteer steuern.
   4. Die App auch ANSEHEN, nicht nur ancurlen — Screenshot mit headless Chrome/Chromium:
      exec {"cmd":"chromium --headless=new --disable-gpu --screenshot=shot.png --window-size=1280,900 http://localhost:PORT/"}
      (der Binary-Name variiert: chromium, google-chrome oder der volle Pfad — prüfe mit exec, was da ist).
      Das PNG liegt danach in deinem Home: öffne es mit deinem Read-Tool — du kannst Bilder sehen und so
      Layout-Fehler, leere Seiten oder Fehlermeldungen im UI selbst erkennen.
   5. Befunde aus logs ziehen, am Ende stop für alles, was du gestartet hast.
   Prozesse leben nur bis zum Ende deiner Wach-Phase — die Plattform räumt sie beim Einschlafen ab;
   starte sie in einer neuen Wach-Phase neu, statt dich auf alte zu verlassen.`
}
