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

// System binds the dev plugin to the target registry: the sandbox's local
// toolbox, without an external target system and without credentials.
type System struct{}

func init() {
	target.Register(target.Descriptor{
		Name:          "dev",
		Label:         "Dev sandbox",
		Description:   "The agent's own computer: run shell commands in the sandbox (exec), manage long-running processes (start/stop/logs/list) — dev servers, databases, headless Chrome — and hand the actual programming work to a sub-agent inside the project checkout (agent). The sub-agent works with the project's own Claude Code harness (CLAUDE.md, .claude/agents, skills, commands) and reaches no target systems while doing so. Runs entirely in the sandbox daemon; needs no secrets.",
		Kind:          "builtin",
		Category:      target.CategoryDev,
		System:        System{},
		NoCredentials: true,
		SetupDoc: `1. Activate the plugin here — no secrets are needed, all actions run
   locally in the agent's sandbox (nothing on the control plane).

2. Enable it in the agent's ACCESS.md:
   - system: dev scope: exec,processes,agent

3. If the agent is to install packages or load a browser, release the
   necessary hosts via the egress templates (npm, PyPI, Go, …).

4. Add "docker" to the scope list (system: dev scope: exec,processes,docker)
   to give the agent a real, isolated Docker daemon — a Docker-in-Docker
   sidecar started alongside its sandbox (sandbox_docker.go), never the
   host's own socket. Needed for agents that have to run a project's own
   "docker compose" stack rather than a single process (e.g. QA verifying a
   branch against its own full local environment instead of a shared staging
   deployment that only ever runs main). The sidecar's own image pulls go
   through the same egress allowlist as the rest of the sandbox — release the
   registries it needs (Container registries template, plus whatever the
   compose stack pulls from) the same way as any other egress.

Note: processes started with start live until the end of the agent's waking
phase — they are terminated when the sandbox goes to sleep.

On the agent action (sub-agent in the project checkout): it starts a second
runtime run INSIDE the checked-out project so that the project's own Claude
Code harness applies there (CLAUDE.md, .claude/agents, skills, commands). The
sub-agent runs hermetically — no action proxy, hence no access to GitLab,
email or other target systems; commit and communication stay with the
commissioning agent. Its cost counts against the same budget, its events are
in the same recording (marked as a sub-run). Anyone who does not want this
forbids the action centrally via a guard-rail on the subject dev:agent.`,
	})
	target.OnShutdown(super.shutdown)
}

func (System) Name() string { return "dev" }

// VerifyWebhook/ParseWebhook: the dev plugin has no webhook inbound — there is
// no external system that could send events.
func (System) VerifyWebhook(string, []byte, http.Header) bool { return false }

func (System) ParseWebhook([]byte) (target.WebhookEvent, error) {
	return target.WebhookEvent{}, fmt.Errorf("dev has no webhook inbound")
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
		return super.start(in.Name, in.Cmd, dir, target.Workdir(ctx))
	case "stop":
		return super.stop(in.Name)
	case "logs":
		return super.logs(in.Name, in.TailLines, target.Workdir(ctx))
	case "list":
		return super.list(target.Workdir(ctx)), nil
	default:
		return nil, fmt.Errorf("unknown action %q", strings.TrimSpace(action))
	}
}

// runSubAgent hands an assignment to a nested runtime run that starts INSIDE
// the given project checkout. Only that makes the project's Claude Code
// harness apply (CLAUDE.md, .claude/agents, skills, commands) — the outer run
// never sees it because it runs in the agent home. The daemon drives the run
// itself; the plugin knows it only as a runner in the context (like Workdir
// and the artifact sink).
func runSubAgent(ctx context.Context, cwd, task, model string, maxTurns int) (any, error) {
	if strings.TrimSpace(cwd) == "" {
		return nil, fmt.Errorf("cwd missing: the sub-agent starts in the project checkout (path from the checkout result)")
	}
	if strings.TrimSpace(task) == "" {
		return nil, fmt.Errorf("task missing: describe the assignment so that a colleague without your context can work from it")
	}
	run := target.SubAgent(ctx)
	if run == nil {
		return nil, fmt.Errorf("no sub-agent available (action runs outside a sandbox)")
	}
	return run(ctx, target.SubAgentRequest{Dir: cwd, Task: task, Model: model, MaxTurns: maxTurns})
}

// resolveDir resolves cwd relative to the sandbox home; absolute paths stay as
// they are — the sandbox is the agent's own machine, there is no path boundary
// to defend here (the sandbox itself provides the isolation).
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

// runExec runs a shell command synchronously. An exit code other than 0 is a
// result, not an error — the agent is meant to read test failures, not to
// guess at an action error. The timeout kills the whole process group so that
// no `sh -c` child is left hanging.
func runExec(ctx context.Context, command, dir string, timeoutSecs int) (any, error) {
	if strings.TrimSpace(command) == "" {
		return nil, fmt.Errorf("cmd missing")
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
		out["error"] = fmt.Sprintf("timeout after %s — use start instead of exec for long runners", timeout)
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
	return docHead + docAgent + docRest
}

// PromptDocForScopes hides the sub-agent action from an agent whose ACCESS.md
// does not grant "dev" scope "agent".
//
// Not a cosmetic saving. Advertising it unconditionally produced a silent
// failure with a very expensive shape: the agent read that it could hand the
// programming work to a sub-agent, researched and wrote a careful multi-
// paragraph assignment, the scope check refused the call, and the model — which
// had no reason to expect a refusal — reported the refusal as a success ("the
// sub-agent is working in the background, I will check back") and finished the
// task as done. No commit, no merge request, no comment on the issue: the whole
// run evaporated, twice on the same ticket, and because nothing was written to
// the issue the intake gate kept re-firing it as unprocessed.
//
// Fail-open on an empty scope list, as ScopedDocSystem requires: a missing
// entry must never take a capability away.
func (System) PromptDocForScopes(scopes []string) string {
	if len(scopes) == 0 {
		return docHead + docAgent + docRest
	}
	for _, s := range scopes {
		if strings.TrimSpace(s) == "agent" {
			return docHead + docAgent + docRest
		}
	}
	return docHead + docRest
}

const docHead = `Available dev actions — your sandbox is your own machine, everything here runs locally in it:
`

const docAgent = `   agent {"cwd":"<the path from the checkout result>","task":"<assignment>","max_turns":60} —
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
`

const docRest = `   exec {"cmd":"npm test","cwd":"subfolder (optional, relative to your home)","timeout_secs":120} —
   runs a shell command and returns exit_code + output (the last output, truncated). For builds,
   tests, installations; an exit code other than 0 comes back as the result, read the output.
   start {"name":"app","cmd":"npm run dev","cwd":"..."} — starts a LONG-RUNNING process in the
   background (a dev server, a database, a headless browser); it keeps running while you perform other
   actions. logs {"name":"app","tail_lines":100} shows its last output, list {} all processes
   with their status, stop {"name":"app"} ends the whole process group (SIGTERM, SIGKILL after 5s).
   A JOB OUTLIVES YOUR RUN. exec breaks off after at most 15 minutes — anything longer (a full test
   suite, a long build) belongs in start, and there is a second reason for that: output and exit code
   of a started job are recorded in your home. Should your run end before the job does — at the turn
   limit, say — the NEXT run reads the outcome with logs {"name":"…"}, even after a sandbox restart.
   Such an answer carries "from_earlier_run":true and, in "exit", either the exit code or the note that
   the job did not outlive its sandbox. So take the FIRST step of a run over an unfinished job to be
   list {} — never start a suite a second time whose result is already lying there. Choose a name that
   still says something in the next run ("suite-mr1685", not "test1"): the record belongs to the name,
   and a new start under the same name overwrites its predecessor's log.
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
   The PROCESSES only live until the end of your waking phase — the platform clears them away when you
   fall asleep; start a dev server anew in a new waking phase instead of relying on an old one. What
   survives is the RECORD of a job: its log and its exit code stay readable with logs/list.

   REAL DOCKER (only if your ACCESS.md grants "dev" scope "docker" — check DOCKER_HOST with
   exec {"cmd":"echo $DOCKER_HOST"} if unsure; empty means you do not have it): "docker" and
   "docker compose" work as normal shell commands via exec/start — not against the host, against
   your OWN Docker-in-Docker sidecar, so containers you start there are yours alone and gone when
   your sandbox sleeps. This is for running a project's real docker-compose stack — e.g. verifying a
   specific branch/MR end-to-end against its OWN backend+database+auth instead of a shared staging
   deployment that only ever reflects main and can therefore never show a branch's actual behavior.
   Typical shape: checkout the branch (gitlab:checkout or equivalent), clone whatever sibling repos
   the stack's own README asks for (also via checkout, into the right relative path), read that
   README for the real compose/start commands instead of guessing, start {"name":"stack","cmd":"docker compose up -d"},
   wait for it to become healthy (the project's own wait/health script, or repeated
   exec {"cmd":"docker compose ps"}), run your actual verification against localhost, then
   exec {"cmd":"docker compose down -v"} to tear it down. Image pulls go through the same egress
   allowlist as everything else you do — a registry host missing there fails the pull, not silently
   ignores it.`
