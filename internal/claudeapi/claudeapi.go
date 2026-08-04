// Package claudeapi is the control plane's narrow access to the Messages API.
//
// The control plane calls Claude itself in two places: the config copilot helps
// writing the agent config, and the dream tidies up the memory at night. Both
// need the same auth mechanics (API key or the organization's subscription
// OAuth token) and must not depend on each other — which is why it lives here
// and not in either of the two callers.
//
// Guard-rail as always: the credential never leaves the control plane. It goes
// neither into the browser nor into a sandbox.
package claudeapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"covey/internal/secrets"
)

// BaseURL is a variable rather than a constant so that tests can slip in an
// httptest server.
var BaseURL = "https://api.anthropic.com"

// Message is one turn. Content is a plain string — the Messages API takes that
// as a single text block.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Call bundles what differs per caller.
type Call struct {
	Model     string
	MaxTokens int
	// Effort controls the thinking depth (low | medium | high | xhigh | max).
	// Empty = the model's default.
	Effort string
	// NoThinking turns thinking off. From Opus 5 on the model thinks by default,
	// and MaxTokens caps thinking *and* answer together — on a narrowly outlined
	// task that eats all the time: measured two minutes for a single title to be
	// renamed. Effort alone did not help (over the subscription OAuth credential
	// it has no visible effect), turning it off did.
	//
	// Only for calls without tools and with a machine-readable answer: without
	// thinking the model can write tool calls into the prose, and occasionally
	// <thinking> markers slip into the answer.
	NoThinking bool
}

type textBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type outputConfig struct {
	Effort string `json:"effort,omitempty"`
}

type thinkingConfig struct {
	Type string `json:"type"` // adaptive | disabled
}

type messagesReq struct {
	Model        string          `json:"model"`
	MaxTokens    int             `json:"max_tokens"`
	System       []textBlock     `json:"system,omitempty"`
	Messages     []Message       `json:"messages"`
	OutputConfig *outputConfig   `json:"output_config,omitempty"`
	Thinking     *thinkingConfig `json:"thinking,omitempty"`
}

type messagesResp struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
	Error      *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// Messages calls the Messages API with exactly the auth mechanics the runtime
// uses too: API key via x-api-key, subscription OAuth token via Bearer plus the
// oauth beta header. For OAuth tokens Anthropic requires the Claude Code
// identity block as the first system segment.
func Messages(ctx context.Context, credential string, oauth bool, call Call, system string, messages []Message) (string, error) {
	sys := []textBlock{}
	if oauth {
		sys = append(sys, textBlock{Type: "text",
			Text: "You are Claude Code, Anthropic's official CLI for Claude."})
	}
	sys = append(sys, textBlock{Type: "text", Text: system})

	req := messagesReq{
		Model:     call.Model,
		MaxTokens: call.MaxTokens,
		System:    sys,
		Messages:  messages,
	}
	if call.Effort != "" {
		req.OutputConfig = &outputConfig{Effort: call.Effort}
	}
	if call.NoThinking {
		req.Thinking = &thinkingConfig{Type: "disabled"}
	}
	body, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, BaseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	hreq.Header.Set("content-type", "application/json")
	hreq.Header.Set("anthropic-version", "2023-06-01")
	if oauth {
		hreq.Header.Set("Authorization", "Bearer "+credential)
		hreq.Header.Set("anthropic-beta", "oauth-2025-04-20")
	} else {
		hreq.Header.Set("x-api-key", credential)
	}
	resp, err := http.DefaultClient.Do(hreq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))

	var parsed messagesResp
	_ = json.Unmarshal(raw, &parsed)
	if resp.StatusCode != http.StatusOK {
		if parsed.Error != nil && parsed.Error.Message != "" {
			return "", errors.New(parsed.Error.Message)
		}
		return "", errors.New("HTTP " + resp.Status)
	}
	var out strings.Builder
	for _, c := range parsed.Content {
		if c.Type == "text" {
			out.WriteString(c.Text)
		}
	}
	return out.String(), nil
}

// ResolveOrg finds an organization's Claude credential. The API key takes
// precedence over the subscription OAuth token. Both callers — config copilot
// and dream — must arrive at the same result here, which is why the order lives
// in one place.
func ResolveOrg(ctx context.Context, store secrets.Store, orgID uuid.UUID) (cred string, oauth, ok bool) {
	if v, err := store.Get(ctx, orgID, "anthropic_api_key"); err == nil {
		if v = strings.TrimSpace(v); v != "" {
			return v, false, true
		}
	}
	if v, err := store.Get(ctx, orgID, "claude_code_oauth_token"); err == nil {
		if v = strings.TrimSpace(v); v != "" {
			return v, true, true
		}
	}
	return "", false, false
}
