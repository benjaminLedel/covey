// SevenCode — the fourth engine (spec/25-sevencode-adapter.md).
//
// SevenCode is a coding-agent CLI aimed at the educa AI API. It matters to
// covey for one reason: it is a second harness in front of the same gateway
// educa-ai already drives (spec/23), so an organisation on educa gets a choice
// of agent loop rather than a choice of endpoint.
//
// EVERYTHING HERE WAS READ OFF THE BINARY, and the version matters: the npm
// package `sevencode` published **0.0.2** (13.09.2026), and its command surface
// is **opencode's** — the help prints `opencode …`, the wrapper honours
// `OPENCODE_BIN_PATH`, the config lives under `~/.config/opencode`, and every
// environment variable it reads is `OPENCODE_*`. The npm version and the source
// project's `package.json` (1.0.16) are two different countings; `--version` is
// the one an installation can check.
//
// This adapter's first draft rested on a `--help` of a "1.0.7" whose flags the
// shipped CLI does not have: `-p` for the prompt (in `run` that is
// `--password`), `--auto`, `--json`, `--resume`. That is exactly the failure
// spec/19 warned about — an adapter against an invented flag fails in the fleet
// rather than at the build — and it is why what follows names how it was
// measured, not where it was read.
//
// Measured against a local OpenAI-compatible double, so a real run without
// model cost:
//
//   - The run is `sevencode run <message> --format json`. One JSON line per
//     event, common envelope `{type, timestamp, sessionID, part}`; the emitter
//     in the binary knows exactly five types: `step_start`, `text`,
//     `reasoning` (only with `--thinking`), `tool_use`, `step_finish`, plus
//     `error` for a session error.
//   - EVERY event carries the session id, and `run --session <id>` continues
//     that session — checked against the double, which received the earlier
//     turns with the second run. So this engine RESUMES, and the restriction
//     that an agent on it must not block (spec/03) falls away.
//   - `step_finish` carries `cost` and `tokens{total,input,output,reasoning,
//     cache{read,write}}`. So a run is measured rather than unpriced.
//   - `permission.asked` is answered by `run` itself with "auto-rejecting".
//     Without a permission setting the agent may therefore do nothing at all —
//     no bash, no edit. `OPENCODE_PERMISSION` is merged into the config by the
//     CLI, and that is what replaces the `--auto` this adapter used to invent.
//
// What is still NOT settled, and is left absent rather than guessed:
//
//   - NO SYSTEM-PROMPT FLAG. The compiled agent config goes in front of the
//     task in the one message, as before. There IS a measured route to a real
//     system turn — an agent definition under `.opencode/agent/<name>.md`
//     replaces the CLI's own system prompt — but it hangs off the config
//     directory, and which of the two owns that directory is the question
//     spec/25 has to answer before covey starts writing files there.
//   - NO EFFORT LEVELS. `--variant` is the lever, and the help says its values
//     are the provider's ("high, max, minimal"). A list this engine cannot
//     name is not declared here, because a level that is wrong for the
//     instance's provider is a run-time error rather than a choice.
//   - NO ENDPOINT VARIABLE. The CLI reads none; where the model sits is part
//     of the provider entry in its configuration, which belongs to the
//     operator. covey therefore delivers the token and says which variable the
//     configuration should reference — it does not invent a base URL.
package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// The CLI name and the variable that overrides it — the idiom the other engines
// use, and what the test against a fake binary relies on.
const (
	sevencodeDefaultBinary = "sevencode"
	sevencodeBinEnv        = "COVEY_SEVENCODE_BIN"
)

// sevencodeErrTail is how much of the CLI's stderr a failed run carries into the
// task's error text. The first bytes, not the last: a CLI says what went wrong
// before it says anything else.
const sevencodeErrTail = 8 << 10

// sevencodePermission is what the run may do inside its sandbox, handed to the
// CLI as `OPENCODE_PERMISSION` and merged into whatever the configuration says.
//
// It has to be set, because the alternative is not "ask somebody" but "no":
// `run` answers a permission request itself, with a rejection. An agent whose
// bash tool is refused looks like an agent that cannot work rather than one
// that was not allowed to.
//
// Allowing it here is not a hole in the guard rails. What an agent may reach
// outside its sandbox is decided by the broker, the egress point and the policy
// engine (spec/06) — all of them outside the runtime, which is the whole reason
// covey does not leave that question to a CLI's own prompt.
const sevencodePermission = `{"bash":"allow","edit":"allow","webfetch":"allow"}`

type SevenCode struct {
	// Binary is the CLI path.
	Binary string
}

func NewSevenCode() *SevenCode {
	bin := strings.TrimSpace(os.Getenv(sevencodeBinEnv))
	if bin == "" {
		bin = sevencodeDefaultBinary
	}
	return &SevenCode{Binary: bin}
}

func init() {
	RegisterRuntime(RuntimeDescriptor{
		Name:  "sevencode",
		Label: "SevenCode",
		Description: "SevenCode headless (`sevencode run --format json`) — a coding-agent CLI on an educa AI endpoint. " +
			"Flags, events and session handling read off version 0.0.2; see spec/25.",
		Credentials: []RuntimeCredential{
			// The token the provider entry in the CLI's configuration
			// references as `{env:SEVENCODE_API_KEY}`. The name is covey's, not
			// the CLI's — it reads no credential variable of its own — and the
			// setup step below says where it is referenced.
			{Kind: CredAPIKey, Label: "API token",
				Secret: "sevencode_api_token", EnvVar: "SEVENCODE_API_KEY"},
			// `sevencode auth login` writes its credentials into the data
			// directory. This is the delivery form spec/19 introduced for
			// Codex: the value is a FILE, written before the run and removed
			// after it, because a login left in the agent home would be a
			// long-lived secret (spec/04). The path is the CLI's own
			// (`<data>/auth.json`, and without XDG_DATA_HOME the data
			// directory is `$HOME/.local/share/opencode`).
			{Kind: CredSubscription, Label: "Account login",
				Secret: "sevencode_credentials_json", Path: ".local/share/opencode/auth.json"},
		},
		CLI: RuntimeCLI{Name: sevencodeDefaultBinary, Env: sevencodeBinEnv},
		Capabilities: RuntimeCapabilities{
			// Measured: every event names the session, and `run --session <id>`
			// continues it with its history. So this engine can carry an agent
			// that waits for an answer (spec/03).
			Resume: true,
			// Measured: a skill under `<dir>/.opencode/skill/<name>/SKILL.md` is
			// found — the description of the CLI's `skill` tool named it back.
			SkillsDir: ".opencode/skill",
			// `--variant` exists, its values belong to the provider. See the
			// package comment: a list this engine cannot name is not declared.
			EffortLevels: nil,
			// No model list. The CLI sits in front of one gateway whose model
			// list is that instance's to publish (spec/23), so an unset model
			// passes through to the CLI's own default instead of being pinned
			// here.
			Models: nil,
		},
		New: func() Runtime { return NewSevenCode() },
		Setup: []SetupStep{
			{
				Text: "Obtain a credential — one of the two variants:",
				Items: []string{
					"API token for the educa AI instance. Billed per contract.",
					"Account login: run `sevencode auth login` on a machine and take the resulting `auth.json` from the CLI's data directory.",
				},
			},
			{
				Text: "Store it under `Secrets` — key depending on the variant:",
				Items: []string{
					"API token → key `sevencode_api_token`",
					"Account login → key `sevencode_credentials_json` (the whole file contents)",
				},
			},
			{
				Text: "Put the CLI into the sandbox image, or into the engine catalogue (spec/26) — it is not in the base image:",
				Items: []string{
					"`npm install -g sevencode`; the package brings its platform binary as an optional dependency, so `--ignore-scripts` is enough",
					"Node 22 or newer is required; the `node:26-slim` sandbox base satisfies it",
					"record `sevencode --version` with the image — this declaration is read from 0.0.2, and a new version is a reason to read `--help` again (spec/25)",
					"Override the path with `COVEY_SEVENCODE_BIN` if the CLI is not on `PATH`",
				},
			},
			{
				Text: "Configure the provider in the CLI's own configuration — where the model sits is your decision, and covey invents no endpoint:",
				Items: []string{
					"an entry under `provider` in `~/.config/opencode/opencode.json` of the agent home, with your endpoint under `options.baseURL`",
					"`\"apiKey\": \"{env:SEVENCODE_API_KEY}\"` — that is the variable covey brokers the token into, per run",
					"allow the host of that endpoint in the egress allowlist, or the sandbox reaches no model",
				},
			},
			{Text: "Create a `Runtime` with the engine `sevencode`, add the credential and set this agent's runtime to it."},
		},
	})
}

// Prices is empty ON PURPOSE, following spec/23: what a token costs on an educa
// instance follows from a contract, not from a published list, and a guessed
// figure would look like a measurement. The run does report a `cost` of its own
// — whatever the CLI's provider entry says a token costs — and that figure is
// taken as it comes rather than being priced a second time here.
func (SevenCode) Prices() PriceList { return PriceList{} }

func (s *SevenCode) Name() string { return "sevencode" }

// taskPrompt builds the message the run is given. The CLI has no flag for a
// system prompt — `--help` names none — so the compiled agent config (SOUL.md
// plus the protocol instructions, spec/12) goes in front of the task text, in
// the same message. That is a weaker position than a system turn: what the
// protocol demands of the run — above all the closing `COVEY_STATUS` line — is
// asked of the model as part of the request. The measured way out is in the
// package comment, and it is an open point in spec/25, not a gap nobody saw.
func (s *SevenCode) taskPrompt(spec RunSpec) string {
	if spec.ResumeSessionID != "" {
		// A resumed run speaks into a session that already carries the config
		// and the task. Repeating them would be a second brief on top of the
		// first, and the CLI keeps the history itself (measured).
		return spec.ResumeInput
	}
	task := spec.Title + "\n\n" + spec.Body
	head := spec.SystemPrompt
	if spec.MemoryContext != "" {
		if head != "" {
			head += "\n\n"
		}
		head += spec.MemoryContext
	}
	if head == "" {
		return task
	}
	return head + "\n\n---\n\n" + task
}

// buildArgs is the flag set, and every entry is one the CLI documents in its own
// `run --help`. The message is positional and goes LAST, so nothing after it can
// be read as an option.
func (s *SevenCode) buildArgs(spec RunSpec) []string {
	args := []string{"run", "--format", "json"}
	if spec.ResumeSessionID != "" {
		args = append(args, "--session", spec.ResumeSessionID)
	}
	if spec.Model != "" {
		args = append(args, "--model", spec.Model)
	}
	// The reasoning lever, under the provider's own name. covey only passes on
	// what somebody entered — see the package comment on why no list is
	// declared here.
	if spec.Effort != "" {
		args = append(args, "--variant", spec.Effort)
	}
	return append(args, s.taskPrompt(spec))
}

// sevencodeEvent is one line of `--format json`. Only the fields this adapter
// acts on are named; the whole line goes into the recording either way.
type sevencodeEvent struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionID"`
	Part      struct {
		Type   string  `json:"type"`
		Text   string  `json:"text"`
		Tool   string  `json:"tool"`
		Reason string  `json:"reason"`
		Cost   float64 `json:"cost"`
		Tokens struct {
			Input  int64 `json:"input"`
			Output int64 `json:"output"`
			Cache  struct {
				Read  int64 `json:"read"`
				Write int64 `json:"write"`
			} `json:"cache"`
		} `json:"tokens"`
		State struct {
			Status string `json:"status"`
			Error  string `json:"error"`
		} `json:"state"`
	} `json:"part"`
	Error struct {
		Name string `json:"name"`
		Data struct {
			Message string `json:"message"`
		} `json:"data"`
	} `json:"error"`
}

// sevencodeOutcome is what one run's event stream amounted to.
type sevencodeOutcome struct {
	sessionID string
	text      strings.Builder
	errors    []string
	cost      float64
	in, out   int64
	cacheR    int64
	cacheW    int64
	sawFinish bool
	waitErr   error
	stderr    string
}

// Run drives `sevencode run --format json`.
func (s *SevenCode) Run(ctx context.Context, spec RunSpec, onEvent func(kind string, payload json.RawMessage)) (RunResult, error) {
	out, err := s.stream(ctx, spec, onEvent)
	if err != nil {
		return RunResult{Status: "failed"}, err
	}
	if ctx.Err() != nil {
		return RunResult{Status: "failed"}, ctx.Err()
	}

	res := RunResult{
		Status:              "failed",
		Model:               spec.Model,
		SessionID:           out.sessionID,
		CostUSD:             out.cost,
		InputTokens:         out.in,
		OutputTokens:        out.out,
		CacheReadTokens:     out.cacheR,
		CacheCreationTokens: out.cacheW,
	}

	// A session error is the CLI's own way of saying the run did not happen —
	// no credential, no model, an endpoint that refused. It beats the exit code,
	// because it carries a sentence and the exit code carries a number.
	if len(out.errors) > 0 {
		res.Error = strings.Join(out.errors, "; ")
		return res, nil
	}
	if out.waitErr != nil && !out.sawFinish {
		msg := strings.TrimSpace(out.stderr)
		if msg == "" {
			res.Error = fmt.Sprintf("sevencode could not be run (%v) — is the CLI in the sandbox image?", out.waitErr)
			return res, nil
		}
		res.Error = fmt.Sprintf("sevencode exit: %v — %s", out.waitErr, msg)
		return res, nil
	}
	applyStatus(&res, out.text.String())
	return res, nil
}

// stream starts the CLI and reads its events. Every line reaches the recording
// as it came, exactly as with Claude Code: what the adapter does not understand
// is still what a person reads afterwards.
func (s *SevenCode) stream(ctx context.Context, spec RunSpec,
	onEvent func(kind string, payload json.RawMessage)) (sevencodeOutcome, error) {
	var out sevencodeOutcome

	cmd := exec.CommandContext(ctx, s.Binary, s.buildArgs(spec)...)
	// Working directory and home stay separate, as with the other engines: the
	// CLI reads its configuration and its skills relative to the cwd, while HOME
	// has to keep pointing at the persistent agent home.
	cmd.Dir = spec.WorkDir
	if cmd.Dir == "" {
		cmd.Dir = spec.HomeDir
	}
	// Without the daemon's COVEY_* variables (see childEnv) — the run gets only
	// what the caller hands it. The brokered credential arrives through
	// spec.Env, and since os/exec keeps the LAST assignment of a duplicated
	// variable, anything appended below would override it: so the permission
	// setting goes in, the token does not.
	cmd.Env = childEnv(spec.Env...)
	if spec.HomeDir != "" {
		cmd.Env = append(cmd.Env, "HOME="+spec.HomeDir)
	}
	cmd.Env = append(cmd.Env, "OPENCODE_PERMISSION="+sevencodePermission)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return out, err
	}
	errBuf := &limitWriter{n: sevencodeErrTail}
	cmd.Stderr = errBuf
	if err := cmd.Start(); err != nil {
		return out, fmt.Errorf("starting sevencode: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 8<<20)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		raw := json.RawMessage(append([]byte(nil), line...))
		onEvent("runtime", raw)

		var ev sevencodeEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			// A line that is not JSON is not an error: the recording has it,
			// and a CLI is allowed to say something on its way past.
			continue
		}
		if ev.SessionID != "" {
			out.sessionID = ev.SessionID
		}
		switch ev.Type {
		case "text":
			// The parts arrive finished (the emitter waits for `time.end`), so
			// they are appended rather than replaced: a run that says something
			// in two turns has two of them, and the status line may sit in the
			// last.
			if ev.Part.Text != "" {
				if out.text.Len() > 0 {
					out.text.WriteString("\n")
				}
				out.text.WriteString(ev.Part.Text)
			}
		// `tool_use` needs no case of its own: it is in the recording like
		// every other line, and a tool call that failed is not a run that
		// failed — the model usually goes on and tries something else.
		case "step_finish":
			out.sawFinish = true
			out.cost += ev.Part.Cost
			out.in += ev.Part.Tokens.Input
			out.out += ev.Part.Tokens.Output
			out.cacheR += ev.Part.Tokens.Cache.Read
			out.cacheW += ev.Part.Tokens.Cache.Write
		case "error":
			msg := strings.TrimSpace(ev.Error.Data.Message)
			if msg == "" {
				msg = strings.TrimSpace(ev.Error.Name)
			}
			if msg != "" {
				out.errors = append(out.errors, msg)
			}
		}
	}
	out.waitErr = cmd.Wait()
	if scanErr := scanner.Err(); scanErr != nil && out.waitErr == nil {
		out.waitErr = scanErr
	}
	out.stderr = errBuf.String()
	return out, nil
}

// limitWriter keeps the first n bytes written to it and drops the rest, so a
// chatty CLI cannot fill memory while a failed run still carries its reason.
type limitWriter struct {
	buf strings.Builder
	n   int
}

func (w *limitWriter) Write(p []byte) (int, error) {
	if rest := w.n - w.buf.Len(); rest > 0 {
		if len(p) > rest {
			w.buf.WriteString(string(p[:rest]))
		} else {
			w.buf.Write(p)
		}
	}
	return len(p), nil
}

func (w *limitWriter) String() string { return w.buf.String() }
