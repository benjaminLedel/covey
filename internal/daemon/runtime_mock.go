package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

// Mock is a scriptable runtime for tests and offline demos. It is driven by
// directives in the task body and uses the same action proxy as a real runtime
// — the vertical slice (broker, guard-rails, recording, blocked loop) is thus
// exercised completely, only without an LLM.
//
// Directives (one per line):
//
//	[mock:action <system>/<action> <json-params>]  → action through the action proxy
//	[mock:block key=<correlation-key> question=<text>]
//	[mock:fail <error-text>]
//	[mock:result <text>]
//	[mock:memory <text>]
//	[mock:maxturns <handover-state>]         → run ends at the turn limit
//	[mock:maxturns-always <handover-state>]  → likewise, on every resumption too
//	[mock:prompt]                            → returns the system prompt as the result
//
// Without directives: done with a generic result. On resume: done with the
// resume input as the result. If the action proxy answers pending_approval, the
// mock runtime goes blocked with its correlation_key — as instructed.
type Mock struct{}

func (Mock) Name() string { return "mock" }

func init() {
	RegisterRuntime(RuntimeDescriptor{
		Name:        "mock",
		Label:       "Mock",
		Description: "Scriptable test runtime without a real LLM — no cost, no credentials. For demos and offline tests.",
		// No credentials: the mock needs none, and NeedsCredential() derives
		// from that rather than being declared a second time.
		Capabilities: RuntimeCapabilities{Resume: true, SkillsDir: ".claude/skills"},
		New:          func() Runtime { return Mock{} },
		Setup: []SetupStep{
			{Text: "Set this agent's runtime to `mock`."},
			{Text: "Switch to the agent (`Agents` → agent) and put a task into its `Backlog`."},
			{Text: "The agent runs through with a scripted answer — no credential, no cost, no `/login` error."},
		},
	})
}

var mockDirective = regexp.MustCompile(`\[mock:(action|block|fail|result|memory|sleep|maxturns-always|maxturns|prompt)\s*([^\]]*)\]`)

func (Mock) Run(ctx context.Context, spec RunSpec, onEvent func(kind string, payload json.RawMessage)) (RunResult, error) {
	res := RunResult{
		Status:    "done",
		SessionID: "mock-session-" + spec.TaskID,
		CostUSD:   0.0123,
		// Deliberately with the cache share dominating, as in a real run — that
		// way the integration tests fail if the cached input side is dropped
		// somewhere along the way again.
		InputTokens:         1000,
		OutputTokens:        200,
		CacheReadTokens:     40000,
		CacheCreationTokens: 3000,
		Model:               "mock-model",
	}
	if spec.ResumeSessionID != "" {
		res.SessionID = spec.ResumeSessionID
	}
	emit := func(text string) {
		payload, _ := json.Marshal(map[string]string{"type": "assistant", "text": text})
		onEvent("runtime", payload)
	}
	emit("Task accepted: " + spec.Title)
	if strings.TrimSpace(spec.MemoryContext) != "" {
		emit("Memory context is available.")
	}
	if spec.ResumeSessionID != "" {
		emit("Resuming session " + spec.ResumeSessionID + ": " + spec.ResumeInput)
	}

	actionPort := os.Getenv("COVEY_ACTION_PORT")
	for _, env := range spec.Env {
		if v, ok := strings.CutPrefix(env, "COVEY_ACTION_PORT="); ok {
			actionPort = v
		}
	}

	for _, m := range mockDirective.FindAllStringSubmatch(spec.Body, -1) {
		kind, arg := m[1], strings.TrimSpace(m[2])
		switch kind {
		case "action":
			path, params, _ := strings.Cut(arg, " ")
			if params == "" {
				params = "{}"
			}
			status, body, err := callActionProxy(ctx, actionPort, path, params)
			if err != nil {
				res.Status = "failed"
				res.Error = fmt.Sprintf("action %s: %v", path, err)
				return res, nil
			}
			emit(fmt.Sprintf("Action %s → %s", path, status))
			if status == "pending_approval" {
				// As in the platform protocol: block until approval.
				var pending struct {
					CorrelationKey string `json:"correlation_key"`
				}
				json.Unmarshal(body, &pending)
				res.Status = "blocked"
				res.CorrelationKey = pending.CorrelationKey
				res.Question = "Waiting for human approval for " + path
				return res, nil
			}
			if status == "denied" {
				emit("Action forbidden by a guard-rail, choosing another way.")
			}
			if status == "error" {
				// Execution errors (unknown system, credential denied …) are a
				// hard failure — just like with a real runtime.
				var e struct {
					Error string `json:"error"`
				}
				json.Unmarshal(body, &e)
				res.Status = "failed"
				res.Error = fmt.Sprintf("action %s: %s", path, e.Error)
				return res, nil
			}
		case "block":
			if spec.ResumeSessionID != "" {
				continue // do not block again on resume
			}
			res.Status = "blocked"
			res.CorrelationKey = mockKV(arg, "key")
			res.Question = mockKV(arg, "question")
			return res, nil
		case "fail":
			res.Status = "failed"
			res.Error = arg
			return res, nil
		case "maxturns", "maxturns-always":
			// Run at the turn limit: work happened, a result did not — the real
			// runtime fetches the handover state from the aborted session here,
			// the mock takes it from the directive. "maxturns" passes through on
			// resume (the continuation reaches a result), "maxturns-always" does
			// not (it exercises breaking off the chain).
			if kind == "maxturns" && spec.ResumeSessionID != "" {
				continue
			}
			res.Status = "incomplete"
			res.Result = arg
			res.Error = "turn limit reached (mock) — run cut off before it produced a result"
			return res, nil
		case "sleep":
			d, err := time.ParseDuration(arg)
			if err != nil {
				d = time.Second
			}
			select {
			case <-ctx.Done():
				return res, ctx.Err()
			case <-time.After(d):
			}
		case "prompt":
			// Makes the assembled system prompt inspectable (platform protocol,
			// target-system docs, team directory) — otherwise it is invisible
			// from the outside.
			res.Result = spec.SystemPrompt
		case "result":
			res.Result = arg
		case "memory":
			res.Memory = arg
		}
	}
	if res.Result == "" {
		if spec.ResumeSessionID != "" {
			res.Result = "Resumed and completed: " + spec.ResumeInput
		} else {
			res.Result = "Done: " + spec.Title
		}
	}
	return res, nil
}

// mockKV reads key=... question=... out of a directive (question swallows the rest).
func mockKV(s, key string) string {
	idx := strings.Index(s, key+"=")
	if idx < 0 {
		return ""
	}
	v := s[idx+len(key)+1:]
	if key != "question" {
		if sp := strings.IndexByte(v, ' '); sp >= 0 {
			v = v[:sp]
		}
	}
	return strings.TrimSpace(v)
}

func callActionProxy(ctx context.Context, port, path, params string) (string, []byte, error) {
	if port == "" {
		return "", nil, fmt.Errorf("no COVEY_ACTION_PORT set")
	}
	url := "http://127.0.0.1:" + port + "/actions/" + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader([]byte(params)))
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	var out struct {
		Status string `json:"status"`
	}
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		return "", nil, fmt.Errorf("proxy answer: %w", err)
	}
	return out.Status, buf.Bytes(), nil
}
