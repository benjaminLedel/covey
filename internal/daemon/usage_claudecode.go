package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"time"
)

// usageTimeout: the question is cheap but not free, and it must never hold up
// anything. Whoever does not answer in this time is treated as not answering.
const usageTimeout = 45 * time.Second

// Usage asks Claude Code what the credential in effect has consumed.
//
// Measured against the real binary: `claude -p "/usage"` answers headless,
// WITHOUT a model turn (num_turns 0, total_cost_usd 0, duration_api_ms 0) — the
// figures come from a server-side usage endpoint, so they are the provider's
// own and cover the account rather than this machine (spec/07, D13).
//
// The catch is the shape: even under --output-format json the numbers sit as
// PROSE in the `result` field. That makes this a scraping dependency, handled
// as one — a text that no longer parses yields an unreported Usage and the
// platform falls back to its own estimate. It never fails the caller.
func (c *ClaudeCode) Usage(ctx context.Context, env []string) (Usage, error) {
	ctx, cancel := context.WithTimeout(ctx, usageTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, c.Binary, "-p", "/usage", "--output-format", "json")
	cmd.Env = childEnv(env...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return Usage{WindowPercent: -1, WeekPercent: -1}, err
	}

	// The envelope is JSON, its `result` is the human-readable answer.
	var envelope struct {
		Result string `json:"result"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil || envelope.Result == "" {
		// Not the envelope we expected — treat the whole output as the text
		// rather than giving up: an engine that stops wrapping it is a format
		// change, and a format change must not cost us the figure outright.
		return ParseUsage(stdout.String()), nil
	}
	return ParseUsage(envelope.Result), nil
}
