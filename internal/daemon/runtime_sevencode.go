// SevenCode — the fourth engine (spec/25-sevencode-adapter.md).
//
// SevenCode is a coding-agent CLI for the educa AI API. It matters to covey for
// one reason: it is a second harness in front of the same gateway educa-ai
// already drives (spec/23), so an organisation on educa gets a choice of agent
// loop rather than a choice of endpoint.
//
// TWO PROGRAMS ANSWER TO THIS NAME, and the adapter has to say which one it
// drives. The npm package `sevencode` is stuck at 0.0.2 and speaks opencode's
// surface — `run --format json`, `--session`, `OPENCODE_*`, a config under
// ~/.config/opencode. The harness covey runs is built from the educa group's own
// repository, counts 1.0.x, and shares almost nothing with it: the prompt is the
// value of `-p`, permissions are a mode chosen on the command line, and the
// endpoint is a variable rather than a provider entry. The previous draft of
// this file drove the first while meaning the second.
//
// Neither program refuses an argument it does not know — the parse loop has no
// fall-through for unknown flags — so getting this wrong does not fail. It runs
// something else, or nothing, and exits 0. That is the failure spec/19 warns of
// ("a run that completes is not a run that works") arriving from the side where
// nothing looks wrong, and it is why every flag below names where in the source
// it was read.
//
// Read off 1.0.x, against the source of the CLI that ships:
//
//   - The run is `sevencode --json -p <text>`. `--json` writes the session's own
//     event stream, one JSON object per line, unchanged
//     (src/headless.ts:115). There is no `run` subcommand: a bare argument that
//     is not a flag is the prompt (src/index.ts:156), and `-p` takes it as a
//     value (src/index.ts:90).
//   - Permissions are decided ONCE, by the mode flag (src/index.ts:133-142), and
//     in a run with no terminal nobody is asked afterwards. Which is why a mode
//     has to be named: with the asking mode the CLI rejects a write call itself,
//     and an agent that cannot edit looks like an agent that cannot work rather
//     than one that was not allowed to.
//   - `usage` is per turn and not cumulative (src/core/types.ts:149), so the
//     figures are summed. There is no cost field anywhere in the stream: the CLI
//     counts tokens and leaves the price to whoever sold them, exactly as educa
//     does (spec/23) — so Prices() stays empty and a seat on this engine is
//     booked in tokens.
//   - `done` names what ended the run: `model` means it was finished, `turns`
//     and `truncated` mean it was cut off with work left (src/core/types.ts:409).
//     Nothing else in the stream says that, and the limit itself cannot be set —
//     there is no flag for it. What covey can do is not call a cut-off run a
//     completed one.
//   - NO SESSION IS NAMED IN THE STREAM. `--resume` with an id the CLI has never
//     seen is accepted and then writes nothing, exiting 0 (src/index.ts:411) — a
//     resume that found nothing would otherwise be recorded as a run that
//     finished. So the session comes from where the CLI keeps it
//     (`<home>/.sevencode/projects/<slug>/<id>.jsonl`, src/config/paths.ts:15),
//     and a resumed run whose session is not there is refused before the child
//     starts.
//
// What this engine does NOT have, left absent rather than guessed:
//
//   - NO SYSTEM-PROMPT FLAG. The compiled config goes in front of the task in the
//     one message. A file does the job — `<home>/.sevencode/AGENTS.md` is read as
//     instructions for every project (src/config/paths.ts:72) — but that is the
//     operator's file too, and who owns it is a question for spec/25 rather than
//     a directory covey should write into.
//   - NO TURN LIMIT, NO TOOL SCOPE. `RunSpec.MaxTurns` and `AllowedTools` are
//     inert here. The CLI has no flag for either; what it has is the mode, which
//     is one step coarser.
//   - NO EFFORT LEVER AND NO MODEL LIST. `--model` is the only one of the three,
//     and which models exist belongs to the instance behind it (spec/23).
package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

// sevencodeMode is the autonomy the run is started with. `--auto` is the CLI's
// own default and the only mode that does something in a run with no terminal:
// every call is checked, nothing waits for a person who is not there.
//
// Naming it rather than relying on the default is deliberate. The alternatives
// fail in opposite directions — the asking modes produce a run that cannot write
// anything, and the mode that skips checking reaches past what the broker and the
// egress point allowed. A run's autonomy should not depend on which of them the
// CLI happens to prefer in a later version.
//
// This is not a hole in the guard rails. What an agent may reach outside its
// sandbox is decided by the broker, the egress point and the policy engine
// (spec/06) — all of them outside the runtime, which is why covey does not leave
// that question to a CLI's own prompt.
const sevencodeMode = "--auto"

// The three variables a run needs that are not the credential.
const (
	// Where the CLI keeps its login, its sessions and its instructions. Left
	// unset that is `$HOME/.sevencode` (src/config/paths.ts:42) — so the pin
	// states the dependence rather than leaving it to a default: the agent home
	// is the one directory that survives a sandbox, and a session history that
	// did not land there would be gone at the next wake, taking the resume with
	// it.
	sevencodeHomeFormat = "SEVENCODE_HOME=%s/.sevencode"
	// The CLI speaks its interface language, taken from the machine's locale.
	// Without this the transcript carries CLI prose in whatever language the
	// sandbox image happened to carry — and the image is UTF-8 POSIX by
	// convention, which is how a German CLI text ends up in an English record.
	sevencodeLangEnv = "SEVENCODE_LANG=en"
	// The CLI replaces its own file on a timer, from the instance it was built
	// for (src/core/update.ts:375). It has to be told not to: the engine layer is
	// mounted read-only and pinned by digest, and a harness that updates itself
	// is a harness that silently stops being the one the run was recorded
	// against. There is no way to stop it from outside — the flag is covey's only
	// means, and the read-only mount would turn the attempt into a failure the
	// run never sees.
	sevencodeNoSelfUpdate = "SEVENCODE_NO_AUTO_UPDATE=1"
)

// sevencodeSessions lists the session files of this agent's home. The CLI keeps
// them as `<home>/.sevencode/projects/<project>/<file>.jsonl`
// (src/config/paths.ts:15), the project directory named after the work directory
// by a rule of its own. The glob stops one level short of that rule on purpose:
// the question covey has to answer is "does this agent own this session", and
// answering it without repeating a derivation it does not own means the answer
// does not change when the CLI renames its directories and does not break when an
// agent's work directory moves between two wakes. The scope stays one agent's
// home either way — the session of another agent is never found here.
func sevencodeSessions(homeDir string) []string {
	if homeDir == "" {
		return nil
	}
	files, err := filepath.Glob(filepath.Join(homeDir, ".sevencode", "projects", "*", "*.jsonl"))
	if err != nil {
		return nil
	}
	return files
}

// sevencodeHasSession reports whether the CLI knows this session. The question
// has to be asked of the store rather than of the run, because the run answers
// "unknown id" with success and silence.
func sevencodeHasSession(spec RunSpec, id string) bool {
	if id == "" {
		return false
	}
	for _, f := range sevencodeSessions(spec.HomeDir) {
		if sevencodeSessionID(filepath.Base(f)) == id {
			return true
		}
	}
	return false
}

// sevencodeLatestSession is the session the CLI wrote most recently, and the only
// answer there is to "which session did this run use" — the stream does not say.
// One run is one session, so the freshest file is the run's own: a session the
// run continued was appended to, and a session it created is new.
func sevencodeLatestSession(spec RunSpec) string {
	var id string
	var when int64
	for _, f := range sevencodeSessions(spec.HomeDir) {
		info, err := os.Stat(f)
		if err != nil {
			continue
		}
		if n := info.ModTime().UnixNano(); n > when {
			when, id = n, sevencodeSessionID(filepath.Base(f))
		}
	}
	return id
}

// sevencodeSessionID reads the id out of the file name. The CLI puts it there
// (src/core/session-store.ts:286), and it is the only form the id ever takes.
func sevencodeSessionID(name string) string {
	name = strings.TrimSuffix(name, ".jsonl")
	if i := strings.LastIndex(name, "-"); i >= 0 {
		return name[i+1:]
	}
	return name
}

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
		Description: "SevenCode headless (`sevencode --json -p …) — a coding-agent CLI on an educa AI endpoint. " +
			"Flags, events and session handling read off the 1.0 line; see spec/25.",
		Credentials: []RuntimeCredential{
			// The token, read from the run's environment. The endpoint is the
			// other half of the pair and is not covey's to name: where the
			// model sits is the operator's decision, and a base URL invented
			// here would send a brokered token somewhere nobody authorised
			// (spec/04). It arrives with the engine's own configuration
			// (spec/26) — see the setup step that names it.
			{Kind: CredAPIKey, Label: "API token",
				Secret: "sevencode_api_token", EnvVar: "SEVENCODE_API_KEY"},
		},
		CLI: RuntimeCLI{Name: sevencodeDefaultBinary, Env: sevencodeBinEnv},
		Capabilities: RuntimeCapabilities{
			// The CLI continues a session it has stored, so an agent on this
			// engine can wait for an answer (spec/03). The id cannot come from
			// the stream — see the package comment.
			Resume: true,
			// `<home>/.sevencode/skills/<name>/SKILL.md` is the layout the CLI
			// reads (src/config/paths.ts:12), and it is the same one the other
			// engines are given: relative to the home, which is what the
			// materialiser writes into.
			SkillsDir: ".sevencode/skills",
			// No effort lever: `--model` is the only lever this CLI has.
			EffortLevels: nil,
			// No model list. The CLI sits in front of one gateway whose model
			// list is that instance's to publish (spec/23), so an unset model
			// passes through to the CLI's own default rather than being pinned
			// here.
			Models: nil,
		},
		New: func() Runtime { return NewSevenCode() },
		Setup: []SetupStep{
			{
				Text: "Obtain an API token for the educa AI instance. Billed per contract.",
			},
			{
				Text: "Say where the instance is. The CLI reads both halves of the access from the environment, and covey names only the token:",
				Items: []string{
					"SEVENCODE_API_BASE — the instance to work against. The operator's decision, carried by the engine's configuration (spec/26) or the host.",
					"SEVENCODE_API_KEY — the token, brokered per run.",
				},
			},
			{
				Text: "Get the CLI — one of the two:",
				Items: []string{
					"An engine catalogue: the CLI arrives on a read-only layer of its own, no image rebuild (spec/26).",
					"Or point COVEY_SEVENCODE_BIN at a standing install.",
				},
			},
			{
				Text: "Check which of the two you got — `sevencode --version`:",
				Items: []string{
					"a version of the 1.0 line — this is the one",
					"`0.0.x` — that is the npm build, a different program with the same name; it speaks `run --format json` and would run something else entirely (spec/25)",
				},
			},
		},
	})
}

// Prices is empty on purpose. The CLI reports no cost for anything it does —
// there is no money field in its event stream at all — and an instance's price is
// its own to publish. What a run of this engine brings back is tokens, and those
// are booked instead (spec/13, spec/23).
func (SevenCode) Prices() PriceList { return PriceList{} }

func (s *SevenCode) Name() string { return "sevencode" }

// taskPrompt puts the compiled config and the memory in front of the task, in the
// one message the CLI takes. See the package comment: there is no flag for a
// system prompt, and the file that would do it is the operator's.
func (s *SevenCode) taskPrompt(spec RunSpec) string {
	task := spec.Title + "\n\n" + spec.Body
	if spec.ResumeSessionID != "" {
		// A resumed run speaks into a session that already carries the config,
		// so repeating it would say it twice.
		return spec.ResumeInput
	}
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

// buildArgs is the flag set, every entry read from the parse loop of the CLI that
// ships (src/index.ts:89-156).
//
// The order is the part that carries weight. `-p` takes the prompt as its value
// and the loop has no branch for an argument it does not recognise, so the flags
// go first and the prompt arrives last, in the one position where it cannot be
// read as an option and no option can be read as part of it.
func (s *SevenCode) buildArgs(spec RunSpec) []string {
	args := []string{"--json", sevencodeMode}
	if spec.ResumeSessionID != "" {
		args = append(args, "--resume", spec.ResumeSessionID)
	}
	if spec.Model != "" {
		args = append(args, "--model", spec.Model)
	}
	return append(args, "-p", s.taskPrompt(spec))
}

// sevencodeEvent is one line of `--json`. Only the fields this adapter acts on
// are named; the whole line reaches the recording either way.
type sevencodeEvent struct {
	Type string `json:"type"`
	// Text is the body of a text_delta — and of a thinking_delta, whose text is
	// in the recording but is not the answer.
	Text string `json:"text"`
	// Usage are the figures of ONE turn. They are summed, not taken.
	Usage struct {
		InputTokens     int64 `json:"inputTokens"`
		OutputTokens    int64 `json:"outputTokens"`
		ReasoningTokens int64 `json:"reasoningTokens"`
	} `json:"usage"`
	// Message is what an error line says.
	Message string `json:"message"`
	// StoppedBy and Turns are what `done` amounts to.
	StoppedBy string `json:"stoppedBy"`
	Turns     int    `json:"turns"`
}

// sevencodeOutcome is what one run's event stream amounted to.
type sevencodeOutcome struct {
	text      strings.Builder
	errors    []string
	in, out   int64
	turns     int
	stoppedBy string
	sawDone   bool
	waitErr   error
	stderr    string
}

// Run drives `sevencode --json -p <text>`.
func (s *SevenCode) Run(ctx context.Context, spec RunSpec, onEvent func(kind string, payload json.RawMessage)) (RunResult, error) {
	// A session the CLI has never seen is not a run without history — it is a
	// run that does not happen, and the CLI reports it as a success with an empty
	// answer. Asking the store is the only way to know that before the fact.
	if spec.ResumeSessionID != "" && !sevencodeHasSession(spec, spec.ResumeSessionID) {
		return RunResult{Status: "failed", Error: fmt.Sprintf(
			"sevencode has no session %s in this agent home — the run would end without an answer",
			spec.ResumeSessionID)}, nil
	}

	out, err := s.stream(ctx, spec, onEvent)
	if err != nil {
		return RunResult{Status: "failed"}, err
	}
	if ctx.Err() != nil {
		return RunResult{Status: "failed"}, ctx.Err()
	}

	res := RunResult{
		Status:       "failed",
		Model:        spec.Model,
		SessionID:    spec.ResumeSessionID,
		InputTokens:  out.in,
		OutputTokens: out.out,
	}
	if res.SessionID == "" {
		res.SessionID = sevencodeLatestSession(spec)
	}

	// An error line is the CLI's own way of saying the run did not happen — no
	// credential, no model, an endpoint that refused. It beats the exit code,
	// because it carries a sentence and the exit code carries a number.
	if len(out.errors) > 0 {
		res.Error = strings.Join(out.errors, "; ")
		return res, nil
	}
	if out.waitErr != nil && !out.sawDone {
		msg := strings.TrimSpace(out.stderr)
		if msg == "" {
			res.Error = fmt.Sprintf("sevencode could not be run (%v) — is the CLI in the sandbox image?", out.waitErr)
			return res, nil
		}
		res.Error = fmt.Sprintf("sevencode exit: %v — %s", out.waitErr, msg)
		return res, nil
	}
	// A run the turn limit or a truncation cut off said so, and saying it is the
	// difference between an agent that could not finish and one that would not
	// (src/core/types.ts:409). The limit itself cannot be set on this engine, so
	// what covey has left is not to write a cut-off run into the record as a
	// completed one.
	if out.sawDone && out.stoppedBy != "" && out.stoppedBy != "model" {
		res.Error = fmt.Sprintf("sevencode stopped after %d turns (%s) — the task was not finished",
			out.turns, out.stoppedBy)
		return res, nil
	}
	applyStatus(&res, out.text.String())
	return res, nil
}

// stream starts the CLI and reads its events. Every line reaches the recording as
// it came, exactly as with the other engines: what the adapter does not understand
// is still what a person reads afterwards.
func (s *SevenCode) stream(ctx context.Context, spec RunSpec,
	onEvent func(kind string, payload json.RawMessage)) (sevencodeOutcome, error) {
	var out sevencodeOutcome

	cmd := exec.CommandContext(ctx, s.Binary, s.buildArgs(spec)...)
	// Working directory and home stay separate, as with every other engine: the
	// CLI reads its configuration relative to the directory it started in, while
	// HOME has to keep pointing at the persistent agent home.
	cmd.Dir = spec.WorkDir
	if cmd.Dir == "" {
		cmd.Dir = spec.HomeDir
	}
	// Without the daemon's own COVEY_* variables (see childEnv) — the run gets
	// only what the caller hands it. The brokered credential arrives through
	// spec.Env, and since os/exec keeps the LAST assignment of a duplicated
	// variable, anything appended below would override it: so the three below go
	// in, the token does not.
	//
	// The environment is not inherited beyond that. The CLI reads a `.env` in the
	// directory it works in even when told to ignore its configuration
	// (src/config.ts:30), and the work directory is where an agent's own files
	// sit — so the endpoint has to be handed rather than left to whatever the
	// workspace happens to contain.
	cmd.Env = childEnv(spec.Env...)
	if spec.HomeDir != "" {
		cmd.Env = append(cmd.Env, "HOME="+spec.HomeDir,
			fmt.Sprintf(sevencodeHomeFormat, spec.HomeDir))
	}
	cmd.Env = append(cmd.Env, sevencodeLangEnv, sevencodeNoSelfUpdate)

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
		switch ev.Type {
		case "text_delta":
			// Deltas, appended. A run that says something in two turns has two
			// of them, and the status line may sit in the last one.
			if ev.Text != "" {
				out.text.WriteString(ev.Text)
			}
		case "usage":
			// Per turn, so the run is their sum. What the CLI counts apart as
			// reasoning tokens has no place in a RunResult and stays in the
			// recording, where the line that carried it stands.
			out.in += ev.Usage.InputTokens
			out.out += ev.Usage.OutputTokens
		// `tool_start` and its result need no case of their own: they are in the
		// recording like every other line, and a tool call that was refused is
		// not a run that failed — the model usually goes on and tries something
		// else.
		case "error":
			if msg := strings.TrimSpace(ev.Message); msg != "" {
				out.errors = append(out.errors, msg)
			}
		case "done":
			out.sawDone = true
			out.turns = ev.Turns
			out.stoppedBy = ev.StoppedBy
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
