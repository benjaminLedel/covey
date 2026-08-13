// Codex — the second engine, and the first from another provider
// (spec/19-codex-adapter.md).
//
// STATUS: the declaration is complete and correct, the RUN is not verified.
// What is implemented here follows OpenAI's documentation; what that
// documentation does not settle — above all whether `codex exec` can resume a
// session — is NOT guessed at. An adapter written against an invented flag is
// worse than no adapter, because it fails in the fleet instead of at the build.
//
// Concretely: Capabilities.Resume is false. That is not a claim that Codex
// cannot resume; it is the honest state of our knowledge, and it has a real
// consequence — the assignment refuses to put an agent that blocks on this
// engine (spec/18). Whoever verifies resume against the binary flips the flag
// and the restriction disappears.
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

type Codex struct {
	// Binary is the CLI path; default "codex", overridable via COVEY_CODEX_BIN
	// (that is how the adapter is tested against a fake binary).
	Binary string
}

func NewCodex() *Codex {
	bin := os.Getenv("COVEY_CODEX_BIN")
	if bin == "" {
		bin = "codex"
	}
	return &Codex{Binary: bin}
}

func (Codex) Name() string { return "codex" }

func init() {
	RegisterRuntime(RuntimeDescriptor{
		Name:        "codex",
		Label:       "Codex (OpenAI)",
		Description: "OpenAI Codex headless (codex exec). Planned — the run is not yet verified against the binary; see spec/19.",
		Credentials: []RuntimeCredential{
			// The API key is an environment variable and explicitly supported
			// only in `codex exec` — exactly the injection point Covey uses.
			{Kind: CredAPIKey, Label: "API key",
				Secret: "openai_api_key", EnvVar: "OPENAI_API_KEY"},
			// The ChatGPT plan login is a FILE. This is the case that broke the
			// assumption "a brokered credential is an environment variable",
			// and the reason an engine declares its delivery form at all.
			// Written for the run, removed after it — a credential left in the
			// home would be a long-lived secret in the sandbox (spec/04).
			{Kind: CredSubscription, Label: "ChatGPT plan",
				Secret: "codex_auth_json", Path: ".codex/auth.json"},
		},
		Capabilities: RuntimeCapabilities{
			// Deliberately false until verified against the binary — see the
			// package comment. An engine that cannot resume carries agents that
			// finish in one run, not one that waits for an answer.
			Resume: false,
			// Codex's own convention for materialised skills is not
			// established. Empty means: write none, rather than writing them
			// where nothing reads them — configured, visible, without effect is
			// the worst of the available failures.
			SkillsDir: "",
			// Codex has a reasoning-effort control of its own, but neither its
			// flag nor its level names are verified against the binary here,
			// and Run() does not pass one. Declaring none is the same rule as
			// above: not offered beats offered without effect. Filling this in
			// is one of the open points of spec/19.
			EffortLevels: nil,
		},
		New: func() Runtime { return NewCodex() },
		Setup: []SetupStep{
			{
				Text: "Obtain a credential — one of the two variants:",
				Items: []string{
					"API (pay-per-token): API key from the OpenAI platform. Billed separately.",
					"ChatGPT plan (Plus/Pro/Team): the login of `codex login`, as the contents of `~/.codex/auth.json`.",
				},
			},
			{
				Text: "Store it under `Secrets` — key depending on the variant:",
				Items: []string{
					"API key → key `openai_api_key`",
					"ChatGPT plan → key `codex_auth_json` (the whole file contents)",
				},
			},
			{Text: "Create a `Runtime` with the engine `codex` and add the credential to it."},
		},
	})
}

// Prices is the price list Covey uses to turn Codex's token counts into a
// comparable figure — Codex reports usage and no money (spec/19).
//
// Deliberately EMPTY until the figures are checked against the provider's
// current price list. An unknown model then yields no cost rather than a wrong
// one, which is the behaviour the price list is built around: a run without a
// price is honest, a run priced at zero makes an agent look free.
func (Codex) Prices() PriceList { return PriceList{} }

// Run drives `codex exec`.
//
// UNVERIFIED against the binary. The event stream is read as documented — JSON
// Lines with thread/turn/item events, and a `turn.completed` carrying token
// counts — and everything the documentation does not cover is deliberately
// absent rather than invented: no resume flag, no turn limit, no tool scope.
// Those are listed as open points in spec/19 and have to be established, not
// inferred.
func (c *Codex) Run(ctx context.Context, spec RunSpec, onEvent func(kind string, payload json.RawMessage)) (RunResult, error) {
	res := RunResult{Status: "failed", Model: spec.Model}

	if spec.ResumeSessionID != "" {
		// The control plane should not get here — the assignment refuses a
		// blocking agent on an engine without Resume. If it does anyway, say so
		// plainly rather than starting a fresh run that silently loses the
		// conversation it was supposed to continue.
		res.Error = "codex: continuing a session is not verified for this engine — " +
			"the task cannot be resumed here (see spec/19)"
		return res, nil
	}

	// Covey agent homes are valid workspaces even when the current task does
	// not involve a checked-out repository. The sandbox container is the trust
	// boundary; without this flag Codex exits before processing the prompt.
	args := []string{"exec", "--json", "--skip-git-repo-check"}
	if spec.Model != "" {
		args = append(args, "--model", spec.Model)
	}
	args = append(args, spec.Body)

	cmd := exec.CommandContext(ctx, c.Binary, args...)
	cmd.Env = childEnv(spec.Env...)
	if spec.WorkDir != "" {
		cmd.Dir = spec.WorkDir
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		res.Error = err.Error()
		return res, nil
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		res.Error = fmt.Sprintf("codex could not be started (%v) — is the binary in the sandbox?", err)
		return res, nil
	}

	var lastText string
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		raw := json.RawMessage(line)
		onEvent("runtime", raw)

		var ev struct {
			Type  string `json:"type"`
			Text  string `json:"text"`
			Usage struct {
				InputTokens         int64 `json:"input_tokens"`
				CachedInputTokens   int64 `json:"cached_input_tokens"`
				OutputTokens        int64 `json:"output_tokens"`
				ReasoningOutputToks int64 `json:"reasoning_output_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal(raw, &ev) != nil {
			continue
		}
		if ev.Text != "" {
			lastText = ev.Text
		}
		if strings.HasSuffix(ev.Type, "turn.completed") || ev.Type == "turn.completed" {
			// The token kinds do not map one to one onto ours, and translating
			// them is the adapter's job rather than the schema's. Reasoning
			// tokens are billed as output and belong there; Codex has no
			// counterpart to a cache WRITE, so that stays zero rather than
			// being filled with something plausible.
			res.InputTokens += ev.Usage.InputTokens
			res.OutputTokens += ev.Usage.OutputTokens + ev.Usage.ReasoningOutputToks
			res.CacheReadTokens += ev.Usage.CachedInputTokens
		}
	}
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return res, ctx.Err()
	}
	if waitErr != nil {
		res.Error = fmt.Sprintf("codex exit: %v", waitErr)
		return res, nil
	}

	// Codex reports no dollar figure; the platform prices the tokens itself
	// where its price list knows the model (pricing.go). An unknown model
	// leaves the cost at zero AND unattributed, which the evaluation shows as
	// "no price" rather than as free.
	if usd, ok := PriceRun(c, spec.Model, res.InputTokens, res.OutputTokens,
		res.CacheReadTokens, res.CacheCreationTokens); ok {
		res.CostUSD = usd
	}

	res.Status = "done"
	res.Result = lastText
	applyStatus(&res, lastText)
	return res, nil
}
