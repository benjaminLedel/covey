// educa AI — the third engine, and the first that is not a provider but a
// GATEWAY (spec/23-educa-adapter.md).
//
// educa AI Core (https://api.educaai.de) puts one bearer token in front of
// several LLM backends and serves them in two dialects: `/v1/chat/completions`
// in OpenAI's and `/v1/messages` in Anthropic's, the latter with tools, system
// prompt, SSE streaming and `/v1/messages/count_tokens`. That second dialect is
// what makes this engine cheap to build honestly: an API alone is not a harness
// — an agent that edits files, runs a shell, resumes a session and closes with
// COVEY_STATUS needs one — and Covey already has a verified harness that speaks
// exactly that dialect. So this engine is Claude Code with its base URL pointed
// at educa, not a second agent loop written from scratch.
//
// What follows from "gateway" rather than "provider", and is implemented here:
//
//   - THE MODEL IS NOT OPTIONAL. Which ids exist is the instance's business
//     (`GET /v1/models`), and the harness's own default names an Anthropic model
//     the gateway need not know. A run without a configured model is refused
//     with that sentence rather than sent off to fail at the first request.
//   - NO DOLLAR FIGURE IS TAKEN FROM THE HARNESS. Claude Code prices its runs
//     from ITS provider's list; through a gateway that number prices the wrong
//     contract, and for an unknown model it becomes zero — "a run priced at zero
//     is a lie in the direction nobody checks" (pricing.go). The amount is
//     therefore discarded and re-derived from this engine's own price list,
//     which is deliberately empty: what educa costs is a contract, not a
//     published table. Token counts stay, they are measured.
//   - NO UTILISATION SOURCE. `/usage` is a Claude Code command that asks
//     ANTHROPIC's account endpoint; behind a gateway it answers about the wrong
//     account or not at all. Hence Educa holds its ClaudeCode as a field instead
//     of embedding it — embedding would promote Usage() and silently make this
//     engine a UsageReporter (usage.go). educa reports per-token windows under
//     `/stats/{token_id}`, but that needs a second key (STATS_API_KEY) and the
//     token's id, neither of which the sandbox has. The platform therefore falls
//     back to its own estimate, as with Codex.
//
// STATUS: VERIFIED against the hosted instance (runtime_educa_live_test.go,
// which skips without a token). Measured, not reasoned about: the harness
// completes a run, executes tools, and `--resume` carries a session's context
// across a second run — the edge the whole `blocked` mechanism hangs off
// (spec/03). `--effort` passes through without breaking the run.
//
// One defect was measured and is NOT worked around here, because a workaround
// would hide it: the gateway loses the INPUT tokens in its streaming path. The
// same request answers `input_tokens: 61` non-streaming and `0` in
// `message_start`, and its `message_delta` carries no input field at all — so a
// run books its output side and reports nothing for the input. The harness
// always streams, so this is the normal case, not an edge one. It belongs to
// the endpoint and is fixed there; until it is, the platform sees a fraction of
// what a run read (spec/23).
package daemon

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
)

// educaDefaultBaseURL is the documented endpoint of the hosted instance. An
// on-premise educa is reached by setting COVEY_EDUCA_BASE_URL in the sandbox's
// environment — the same override idiom the other two engines use for their
// binary (COVEY_CLAUDE_BIN, COVEY_CODEX_BIN).
const educaDefaultBaseURL = "https://api.educaai.de"

// Educa drives the Claude Code binary against educa AI Core's
// Anthropic-compatible endpoint.
type Educa struct {
	// cc is held, not embedded: see the package comment — embedding would
	// promote Usage() and declare a utilisation source that does not exist.
	cc *ClaudeCode
	// BaseURL is what the harness talks to, without a trailing slash.
	BaseURL string
}

func NewEduca() *Educa {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("COVEY_EDUCA_BASE_URL")), "/")
	if base == "" {
		base = educaDefaultBaseURL
	}
	e := &Educa{cc: NewClaudeCode(), BaseURL: base}
	// The harness words its credential failures for Anthropic. Behind a gateway
	// that advice is not merely useless but misleading — `claude setup-token`
	// produces a token this endpoint has never heard of.
	e.cc.CredentialHint = e.credentialHint
	return e
}

// credentialHint translates the two failures somebody actually hits on a first
// setup into the terms of THIS engine. Everything else is left alone: a rewrite
// that fires on the wrong error hides the real one.
func (e *Educa) credentialHint(raw string) string {
	low := strings.ToLower(raw)
	switch {
	case strings.Contains(raw, "/login"),
		strings.Contains(low, "401"),
		strings.Contains(low, "unauthorized"),
		strings.Contains(low, "invalid bearer token"),
		strings.Contains(low, "authentication_error"):
		return "The educa AI instance did not accept the brokered token (" + raw + "). " +
			"Check under Secrets that `educa_api_token` (billed per token) or " +
			"`educa_seat_token` (contract seat) holds a current bearer token of " +
			e.BaseURL + ", and that the runtime has that credential."
	case strings.Contains(low, "model") &&
		(strings.Contains(low, "not found") || strings.Contains(low, "404") ||
			strings.Contains(low, "unknown")):
		return "The educa AI instance does not know this model (" + raw + "). " +
			"The ids it does serve come from the instance itself — " +
			"`curl -H 'Authorization: Bearer <token>' " + e.BaseURL + "/v1/models` — " +
			"and one of them belongs on the runtime."
	}
	return ""
}

func (e *Educa) Name() string { return "educa-ai" }

func init() {
	RegisterRuntime(RuntimeDescriptor{
		Name:  "educa-ai",
		Label: "educa AI",
		Description: "educa AI Core through its Anthropic-compatible endpoint, driven by the Claude Code harness. " +
			"Runs, uses tools and resumes sessions. Needs a bearer token under Secrets and a model id " +
			"from the instance's `GET /v1/models` — the engine has no default of its own.",
		Credentials: []RuntimeCredential{
			// Both forms are the SAME kind of value — one opaque bearer token —
			// and both reach the run through the same variable. What differs is
			// the contract behind it, and that is not cosmetic: the kind decides
			// the honest unit of a limit (money where money is spent, the window
			// quota where it is not, see RuntimeCredential.Metered) and it is
			// what the merit order in runtimes.Pick sorts on. A seat that is
			// paid for either way gets filled before anything metered is
			// touched. Two secret names keep the two contracts apart in a place
			// where a human can see which is which.
			//
			// API token first, as with the other engines: whoever holds both
			// should spend money on purpose, not by accident.
			{Kind: CredAPIKey, Label: "API token",
				Secret: "educa_api_token", EnvVar: "ANTHROPIC_AUTH_TOKEN"},
			{Kind: CredSubscription, Label: "Contract seat",
				Secret: "educa_seat_token", EnvVar: "ANTHROPIC_AUTH_TOKEN"},
		},
		Capabilities: RuntimeCapabilities{
			// The ids that were put to work, not the ones the instance lists.
			// Each of these solved a real multi-step task through the harness —
			// read the code, find the bug, fix it, re-run the check
			// (runtime_educa_task_test.go). Two of the instance's listed models
			// are deliberately absent: Qwen-AgentWorld-35B-A3B answers 500 on
			// every request, and EuroLLM-9B-Instruct has a 2048-token context
			// while Covey's protocol prompt alone is some 9 KB — it cannot hold
			// the prompt, let alone the task. Both would look like a working
			// choice in a dropdown built from /v1/models.
			// First is the default, and it is not the fastest of the three —
			// three seconds on one bug fix rank nothing. It is the one with
			// 262144 tokens of context against the others' 131072, which is
			// what an agent run actually spends (a 9 KB protocol prompt, the
			// SOUL, wiki context, tool output, and a session that keeps growing
			// across --resume), and the one that reports `stop_reason:
			// "tool_use"` where gpt-oss-120b reports "end_turn" on a response
			// carrying a tool_use block. The latter works today only because
			// the harness reads the block rather than the field; a default
			// should not rest on that.
			//
			// What this order does NOT claim is which model is cleverer. One
			// fixed median is no capability benchmark — if agents start failing
			// on hard reasoning, gpt-oss-120b is the first thing to try.
			Models: []string{
				"gemma-4-26B-A4B-it",
				"gpt-oss-120b",
				"gemma-4-E4B-it",
			},
			// Resume is a property of the HARNESS, not of the endpoint: Claude
			// Code keeps the session under the agent home and replays it on
			// --resume. Swapping the model provider does not touch that, so the
			// blocked→working edge (spec/03) carries here as it does on
			// claude-code.
			Resume: true,
			// Same reasoning: skills are files the harness reads, and it reads
			// them where it always does.
			SkillsDir: ".claude/skills",
			// The intersection of the two sides. `--effort` is the harness's
			// flag and travels into the request as a thinking budget, which
			// educa's schema passes through (MessagesRequest is
			// additionalProperties: true); educa's own documented ceiling is
			// `xhigh`, so Claude Code's `max` is left out rather than offered
			// against something no backend there advertises.
			EffortLevels: []string{"low", "medium", "high", "xhigh"},
		},
		New: func() Runtime { return NewEduca() },
		Setup: []SetupStep{
			{Text: "Obtain a bearer token from the educa AI instance (`Authorization: Bearer …`, opaque)."},
			{
				Text: "Store it under `Secrets` — the key says which contract it is:",
				Items: []string{
					"billed per token → key `educa_api_token`",
					"flat-rate contract seat → key `educa_seat_token`",
				},
			},
			{
				Text: "Create a `Runtime` with the engine `educa-ai` and add the credential. " +
					"The model can stay unset — each of these solved a real multi-step task " +
					"through the harness, and the first is what an agent gets by default:",
				Items: []string{
					"`gemma-4-26B-A4B-it` — the default: twice the context of the others, and correct tool-call semantics",
					"`gpt-oss-120b` — the fastest in the trial; try it when a task needs harder reasoning",
					"`gemma-4-E4B-it` — the small one; it gets there, it needs more turns",
				},
			},
			{
				Text:  "Allow the endpoint in the egress allowlist — without it the sandbox reaches no model:",
				Items: []string{"`api.educaai.de` (or the host of your own instance)"},
			},
			{Text: "Set this agent's runtime to the new one. The control plane brokers the token per waking phase, short-lived."},
		},
	})
}

// Prices is empty ON PURPOSE, and the emptiness is the point rather than a gap
// waiting to be filled: what a token costs on an educa instance follows from a
// contract, not from a published list, and a guessed figure would look like a
// measurement (pricing.go). Unknown model → no amount, and the evaluation shows
// the run as "no price" instead of as free.
//
// It exists all the same, because it is the seam: whoever knows their own rates
// enters them here and every past token count is priced by them at once.
func (e *Educa) Prices() PriceList { return PriceList{} }

// educaEnv is the environment that turns the Claude Code harness into an educa
// client. Appended AFTER whatever the caller passed, so it wins — os/exec keeps
// the last assignment of a duplicated variable.
func (e *Educa) educaEnv() []string {
	return []string{
		"ANTHROPIC_BASE_URL=" + e.BaseURL,
		// Cleared, not merely unused. A stray Anthropic credential in the
		// sandbox environment would otherwise be sent along to a THIRD-PARTY
		// endpoint — a leak of exactly the kind spec/04 exists to prevent, and
		// one nothing downstream would notice. The brokered educa token arrives
		// as ANTHROPIC_AUTH_TOKEN from the descriptor above.
		"ANTHROPIC_API_KEY=",
		"CLAUDE_CODE_OAUTH_TOKEN=",
		// The harness's side traffic (telemetry, error reporting, autoupdate)
		// goes to the provider, not to the gateway. Turning it off keeps the
		// egress allowlist honest: an educa agent then needs the educa host and
		// nothing else. An engine version that does not know the variable
		// ignores it — unlike an invented CLI flag, an unread environment
		// variable costs nothing.
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
	}
}

// Run drives the harness against educa. Everything about the run itself —
// flags, stream-json, the turn-limit handover, blocked/--resume — is
// ClaudeCode's and is not duplicated here; this method contributes the three
// things that differ at a gateway: the endpoint, the required model, and the
// refusal to inherit a price.
func (e *Educa) Run(ctx context.Context, spec RunSpec, onEvent func(kind string, payload json.RawMessage)) (RunResult, error) {
	d, known := Describe(e.Name())
	spec.Model = strings.TrimSpace(spec.Model)
	switch {
	case spec.Model == "":
		// Never leave it empty. The harness would fall back to ITS default, an
		// id of its own provider that the gateway need not route, and the run
		// would die on the first request with an error that reads like a Covey
		// bug. The engine's own default is an id it declared.
		spec.Model = d.DefaultModel()
	case known && !d.AcceptsModel(spec.Model):
		// The second door. The first is the validation where the model is
		// entered; this one catches what got past it — an imported bundle, a
		// row from before the engine declared its models.
		return RunResult{
			Status: "failed",
			Error: "engine educa-ai does not carry the model " + strconv.Quote(spec.Model) +
				": set one of " + strings.Join(d.Capabilities.Models, ", ") + " on the agent.",
		}, nil
	}

	spec.Env = append(append([]string{}, spec.Env...), e.educaEnv()...)

	res, err := e.cc.Run(ctx, spec, onEvent)
	if err != nil {
		return res, err
	}

	// The harness's dollar figure prices the wrong contract (see the package
	// comment). Drop it first, then let this engine's own list speak — empty
	// today, so the run keeps its measured tokens and carries no amount.
	//
	// A second reason to leave the list empty for now, and it is the stronger
	// one: the gateway drops the input tokens while streaming, so a price
	// entered today would be applied to the output side alone. That is not a
	// cheap run, it is a mismeasured one — and a figure computed from half the
	// tokens looks exactly like a figure computed from all of them.
	res.CostUSD = 0
	if usd, ok := PriceRun(e, res.Model, res.InputTokens, res.OutputTokens,
		res.CacheReadTokens, res.CacheCreationTokens); ok {
		res.CostUSD = usd
	}
	return res, nil
}
