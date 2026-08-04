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
	return `Available dev actions — your sandbox is your own machine, everything here runs locally in it:
   agent {"cwd":"<the path from the checkout result>","task":"<assignment>","max_turns":60} —
   hands the actual PROGRAMMING WORK to a sub-agent that starts INSIDE the project checkout.
   That is the most important difference from everything else here: only there do the project's rules
   apply — its CLAUDE.md, its .claude/agents (e.g. separate frontend/backend specialists), skills and
   commands. You yourself work in the home and see none of it; that is why you do not piece the code
   together yourself but commission the sub-agent.
   The assignment is a HANDOVER to a colleague without your context — write into it: what is broken
   or is to be built, how to reproduce it, the acceptance criteria, known locations
   (file:line) and the conditions (minimally invasive, run the tests, add a test). No "see above".
   The sub-agent can read, change, build and test — it reaches NEITHER GitLab NOR email and cannot
   commit. Checking in and all communication stay with you.
   One assignment per checkout: while a sub-agent is working there, a second call with the same
   cwd is refused — wait for the report, otherwise two runs overwrite each other's files.
   The result: {"result":"<report: cause, change, verification>","changed_files":[…],"deleted":[…],
   "cost_usd":…,"turns_exhausted":true|false,"error":"<only if the run failed>"}. The file lists
   go into the commit action unchanged; they name the ENTIRE state against the checkout — including
   the work of an earlier assignment on the same task, not only that of the last one.
   CHECK "error": it stands IN the result, the call itself still counts as successful (status ok).
   If it is set, the sub-run failed — then do not commit but read the error and either
   commission it again or escalate in the issue with what you know.
   With "turns_exhausted":true the assignment was too big: close off with the partial result and file
   the rest with covey/create_task instead of trying again in the same run.
   exec {"cmd":"npm test","cwd":"subfolder (optional, relative to your home)","timeout_secs":120} —
   runs a shell command and returns exit_code + output (the last output, truncated). For builds,
   tests, installations; an exit code other than 0 comes back as the result, read the output.
   start {"name":"app","cmd":"npm run dev","cwd":"..."} — starts a LONG-RUNNING process in the
   background (a dev server, a database, a headless browser); it keeps running while you perform other
   actions. logs {"name":"app","tail_lines":100} shows its last output, list {} all processes
   with their status, stop {"name":"app"} ends the whole process group (SIGTERM, SIGKILL after 5s).
   The typical procedure for really seeing a web app run instead of only reading its code:
   1. Install the dependencies: exec {"cmd":"npm install"} (or pip/go — the package registries
      have to be released in the egress).
   2. Bring the app up: start {"name":"app","cmd":"npm run dev"} and check with logs whether it is listening.
   3. Test against it: exec {"cmd":"curl -s http://localhost:PORT/..."} — or with a headless
      browser, e.g. start {"name":"chrome","cmd":"chromium --headless=new --remote-debugging-port=9222 --no-sandbox"}
      and then drive it through the DevTools protocol or a Playwright/Puppeteer you installed yourself.
   4. Also LOOK at the app, do not only curl it — a screenshot with headless Chrome/Chromium:
      exec {"cmd":"chromium --headless=new --disable-gpu --screenshot=shot.png --window-size=1280,900 http://localhost:PORT/"}
      (the binary name varies: chromium, google-chrome or the full path — check with exec what is there).
      The PNG then lies in your home: open it with your read tool — you can see images and thereby
      recognise layout errors, empty pages or error messages in the UI yourself.
   5. Pull findings from logs, at the end stop everything you started.
   Processes only live until the end of your waking phase — the platform clears them away when you fall
   asleep; start them anew in a new waking phase instead of relying on old ones.`
}
