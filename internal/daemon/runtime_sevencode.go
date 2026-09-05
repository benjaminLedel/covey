// SevenCode — the fourth engine (spec/25-sevencode-adapter.md).
//
// SevenCode is a coding-agent CLI: one bundled Node programme, a German-language
// interface, aimed at the educa AI API. It matters to covey for one reason: it
// is a second harness in front of the same gateway educa-ai already drives
// (spec/23), so an organisation on educa gets a choice of agent loop rather than
// a choice of endpoint.
//
// Everything this file states about the CLI was read off the CLI itself —
// `sevencode --version` and `sevencode --help` of version 1.0.7. No repository
// was read and no download is named in the setup steps below, because neither
// was verifiable from here; an engine declaration built on a third-party
// description of the binary is how adapters end up with invented flags.
//
// STATUS: the declaration follows the CLI's own help output (`sevencode
// --help`, version 1.0.7) — that part is read, not guessed. What is NOT
// verified is a run through covey: no event schema, no measured token counts, no
// observed session id. The rule is the one spec/19 set for Codex — an adapter
// written against an invented flag is worse than no adapter, because it fails in
// the fleet instead of at the build — so everything the help does not name is
// absent here rather than inferred, and the open points stand in spec/25.
//
// Three consequences follow, and each is visible from outside this file:
//
//   - NO RESUME. `--resume [id]` exists, but nothing in the documented surface
//     prints the id of the session a run just had, and a resume flag without an
//     id is a flag that cannot be used. Capabilities.Resume is therefore false —
//     not a claim that SevenCode cannot resume, but the honest state of our
//     knowledge, and it has a real consequence: the assignment refuses to put an
//     agent that blocks on this engine (spec/03, spec/18).
//   - NO USAGE FIGURES. `--json` promises events, and their field names are
//     unknown here. Parsing them would mean guessing key names, and a guessed
//     token count is not a measurement — it feeds the capacity limits
//     (spec/18). So the run reads plain stdout, and the cost message stays away
//     because `RunResult.Measured()` is honestly false.
//   - NO SYSTEM-PROMPT FLAG. The compiled agent config has to go into the one
//     argument the CLI takes, which is documented below.
package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// sevencodeDefaultBinary is the CLI name; COVEY_SEVENCODE_BIN overrides it (the
// idiom the other engines use, and what the test against a fake binary relies
// on).
const sevencodeDefaultBinary = "sevencode"

// sevencodeErrTail is how much of the CLI's stderr a failed run carries into the
// task's error text. The first bytes, not the last: a CLI says what went wrong
// before it says anything else.
const sevencodeErrTail = 8 << 10

type SevenCode struct {
	// Binary is the CLI path.
	Binary string
	// BaseURL is the educa AI endpoint the CLI is pointed at, empty = the CLI's
	// own default. It is deliberately not guessed here: unlike educa-ai, which
	// documents its hosted instance, this CLI's default endpoint is its own
	// build-time constant, and inventing it would send credentials somewhere
	// nobody checked.
	BaseURL string
}

func NewSevenCode() *SevenCode {
	bin := strings.TrimSpace(os.Getenv("COVEY_SEVENCODE_BIN"))
	if bin == "" {
		bin = sevencodeDefaultBinary
	}
	return &SevenCode{
		Binary:  bin,
		BaseURL: strings.TrimRight(strings.TrimSpace(os.Getenv("COVEY_SEVENCODE_BASE_URL")), "/"),
	}
}

func init() {
	RegisterRuntime(RuntimeDescriptor{
		Name:  "sevencode",
		Label: "SevenCode",
		Description: "SevenCode headless (`sevencode -p`) — a coding-agent CLI on an educa AI endpoint. " +
			"Flags as the CLI itself documents them, the run not yet verified; see spec/25.",
		Credentials: []RuntimeCredential{
			// The variable pair the CLI names for CI and containers: a token here
			// and the endpoint from the environment. API token first, as with the
			// other engines — whoever holds both should spend money on purpose.
			{Kind: CredAPIKey, Label: "API token",
				Secret: "sevencode_api_token", EnvVar: "SEVENCODE_API_KEY"},
			// `sevencode login` writes a login file into the home instead. This is
			// the delivery form spec/19 introduced for Codex: the value is a FILE,
			// written before the run and removed after it, because a login left in
			// the agent home would be a long-lived secret (spec/04).
			{Kind: CredSubscription, Label: "Account login",
				Secret: "sevencode_credentials_json", Path: ".sevencode/credentials.json"},
		},
		Capabilities: RuntimeCapabilities{
			// False until the session id can be read back — see the package
			// comment. An engine that cannot resume carries agents that finish in
			// one run, not one that waits for an answer.
			Resume: false,
			// The CLI reads project and user configuration (`--no-config` turns
			// that off), but where it looks for skills is not documented. Empty
			// means: write none, rather than writing them where nothing reads
			// them — configured, visible, without effect is the worst of the
			// available failures.
			SkillsDir: "",
			// No reasoning-effort control in the documented surface. Declaring
			// none is the same rule as above: not offered beats offered without
			// effect.
			EffortLevels: nil,
			// No model list. The CLI sits in front of one gateway whose model list
			// is that instance's to publish (spec/23), so an unset model passes
			// through to the CLI's own default instead of being pinned here.
			Models: nil,
		},
		New: func() Runtime { return NewSevenCode() },
		Setup: []SetupStep{
			{
				Text: "Obtain a credential — one of the two variants:",
				Items: []string{
					"API token for the educa AI instance (`SEVENCODE_API_KEY`). Billed per contract.",
					"Account login: run `sevencode login` on a machine, take `~/.sevencode/credentials.json` and store its contents.",
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
				Text: "Put the CLI into the sandbox image — it is not in the base image:",
				Items: []string{
					"install it from the channel your organisation gets it from; covey names no download URL it has not verified",
					"Node 22 or newer is required; the `node:26-slim` sandbox base satisfies it",
					"record `sevencode --version` with the image — this declaration is read from 1.0.7, a new version is a reason to read `--help` again (spec/25)",
					"Override the path with `COVEY_SEVENCODE_BIN` if the CLI is not on `PATH`",
				},
			},
			{
				Text: "Point the installation at the instance and allow it in the egress allowlist:",
				Items: []string{
					"`COVEY_SEVENCODE_BASE_URL` on the daemon (optional — without it the CLI's own default endpoint applies)",
					"the host of that endpoint, or the sandbox reaches no model",
				},
			},
			{Text: "Create a `Runtime` with the engine `sevencode`, add the credential and set this agent's runtime to it."},
			{Text: "Note the limitation before assigning an agent that waits for an answer: this engine cannot resume a session (spec/25)."},
		},
	})
}

// Prices is empty ON PURPOSE, following spec/23: what a token costs on an educa
// instance follows from a contract, not from a published list, and a guessed
// figure would look like a measurement. It exists because it is the seam — and
// today there is nothing for it to price, since this adapter reads no usage.
func (SevenCode) Prices() PriceList { return PriceList{} }

func (s *SevenCode) Name() string { return "sevencode" }

// taskPrompt builds the ONE argument the CLI takes. It has no flag for a system
// prompt — `--help` names none — so the compiled agent config (SOUL.md plus the
// protocol instructions, spec/12) goes in front of the task text, in the same
// message. That is a weaker position than a system turn: what the protocol
// demands of the run — above all the closing `COVEY_STATUS` line — is asked of
// the model as part of the request. Open point in spec/25.
func (s *SevenCode) taskPrompt(spec RunSpec) string {
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

// buildArgs is the flag set, and it is deliberately small: every entry is one
// the CLI documents. `--auto` rather than `--yolo` — a covey run has nobody to
// answer a permission prompt, so the run must not ask, while `--auto` keeps the
// CLI's own check on every call. `--no-config` is NOT passed: a project's
// AGENTS.md/CLAUDE.md belongs to the work, as it does on the other engines.
func (s *SevenCode) buildArgs(spec RunSpec) []string {
	args := []string{"-p", s.taskPrompt(spec), "--auto"}
	if spec.Model != "" {
		args = append(args, "--model", spec.Model)
	}
	return args
}

// Run drives `sevencode -p`.
//
// UNVERIFIED against a binary through covey. The process handling is the same
// one the Claude Code adapter uses — hermetic environment, work dir separate
// from home, exit code as the failure signal — but the output is read as plain
// text, not as events, because the event schema is undocumented (package
// comment). The recording of such a run therefore shows no runtime events, and
// the task's result is whatever the CLI printed.
func (s *SevenCode) Run(ctx context.Context, spec RunSpec, onEvent func(kind string, payload json.RawMessage)) (RunResult, error) {
	res := RunResult{Status: "failed", Model: spec.Model}

	if spec.ResumeSessionID != "" {
		// The control plane should not get here — the assignment refuses a
		// blocking agent on an engine without Resume. If it does anyway, say so
		// plainly rather than starting a fresh run that silently loses the
		// conversation it was supposed to continue.
		res.Error = "sevencode: continuing a session is not available on this engine — " +
			"the run cannot resume here (see spec/25)"
		return res, nil
	}

	cmd := exec.CommandContext(ctx, s.Binary, s.buildArgs(spec)...)
	// Working directory and home stay separate, as with the other engines: the
	// CLI reads its configuration relative to the cwd, while HOME has to keep
	// pointing at the persistent agent home.
	cmd.Dir = spec.WorkDir
	if cmd.Dir == "" {
		cmd.Dir = spec.HomeDir
	}
	// Without the daemon's COVEY_* variables (see childEnv) — the run gets only
	// what the caller hands it. The brokered credential arrives through
	// spec.Env, and since os/exec keeps the LAST assignment of a duplicated
	// variable, anything appended below would override it: so the endpoint goes
	// in, the token does not.
	cmd.Env = childEnv(spec.Env...)
	if spec.HomeDir != "" {
		cmd.Env = append(cmd.Env, "HOME="+spec.HomeDir)
	}
	if s.BaseURL != "" {
		cmd.Env = append(cmd.Env, "SEVENCODE_API_BASE="+s.BaseURL)
	}

	var out bytes.Buffer
	errBuf := &limitWriter{n: sevencodeErrTail}
	cmd.Stdout = &out
	cmd.Stderr = errBuf

	runErr := cmd.Run()
	if ctx.Err() != nil {
		return res, ctx.Err()
	}
	if runErr != nil {
		msg := strings.TrimSpace(errBuf.String())
		if msg == "" {
			res.Error = fmt.Sprintf("sevencode could not be run (%v) — is the CLI in the sandbox image?", runErr)
			return res, nil
		}
		res.Error = fmt.Sprintf("sevencode exit: %v — %s", runErr, msg)
		return res, nil
	}

	res.Status = "done"
	res.Result = strings.TrimSpace(out.String())
	// No usage, no session id, no dollar figure: RunResult.Measured() is false,
	// so the daemon sends no cost message at all. That is the honest record of a
	// run whose consumption the engine did not report — not a run that was free.
	applyStatus(&res, out.String())
	return res, nil
}

// limitWriter keeps the first n bytes written to it and drops the rest, so a
// chatty CLI cannot fill memory while a failed run still carries its reason.
type limitWriter struct {
	buf bytes.Buffer
	n   int
}

func (w *limitWriter) Write(p []byte) (int, error) {
	if rest := w.n - w.buf.Len(); rest > 0 {
		if len(p) > rest {
			w.buf.Write(p[:rest])
		} else {
			w.buf.Write(p)
		}
	}
	return len(p), nil
}

func (w *limitWriter) String() string { return w.buf.String() }
