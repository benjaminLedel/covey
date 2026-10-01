// Package claudeapi is the control plane's narrow access to the Messages API.
//
// It is one implementation behind internal/llm and no longer called directly:
// the features that need a model (config copilot, dream, the setup's
// personalisation) ask the port, and the port picks the provider. What stays
// here is what is specific to this one — the auth mechanics, which are subtle
// enough to have exactly one home (API key via x-api-key, the organisation's
// subscription OAuth token via Bearer plus the Claude Code identity block).
//
// Guard-rail as always: the credential never leaves the control plane. It goes
// neither into the browser nor into a sandbox.
package claudeapi

import (
	"bufio"
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
	Stream       bool            `json:"stream,omitempty"`
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

// Messages calls the Messages API and returns the answer's text; send has
// the auth mechanics.
func Messages(ctx context.Context, credential string, oauth bool, call Call, system string, messages []Message) (string, error) {
	resp, err := send(ctx, credential, oauth, call, system, messages, false)
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

// streamEvent is the part of a streamed event this package reads: the text
// a delta adds, and an error the stream ends with.
type streamEvent struct {
	Type  string `json:"type"`
	Delta struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"delta"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// MessagesStream is Messages with the answer streamed (#529): onText gets
// each piece of text as it comes, in order, and the whole text is returned
// at the end, as Messages returns it. A stream that breaks off or reports
// an error is an error, whatever onText has been given by then.
func MessagesStream(ctx context.Context, credential string, oauth bool, call Call, system string, messages []Message, onText func(string)) (string, error) {
	resp, err := send(ctx, credential, oauth, call, system, messages, true)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		var parsed messagesResp
		_ = json.Unmarshal(raw, &parsed)
		if parsed.Error != nil && parsed.Error.Message != "" {
			return "", errors.New(parsed.Error.Message)
		}
		return "", errors.New("HTTP " + resp.Status)
	}
	var out strings.Builder
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	stopped := false
	for sc.Scan() {
		line := sc.Text()
		data, ok := strings.CutPrefix(line, "data:")
		if !ok {
			continue
		}
		var ev streamEvent
		if json.Unmarshal([]byte(strings.TrimSpace(data)), &ev) != nil {
			continue
		}
		switch ev.Type {
		case "content_block_delta":
			if ev.Delta.Type == "text_delta" && ev.Delta.Text != "" {
				out.WriteString(ev.Delta.Text)
				onText(ev.Delta.Text)
			}
		case "error":
			if ev.Error != nil && ev.Error.Message != "" {
				return "", errors.New(ev.Error.Message)
			}
			return "", errors.New("the stream reported an error")
		case "message_stop":
			stopped = true
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	if !stopped {
		return "", errors.New("the stream ended before the message did")
	}
	return out.String(), nil
}

// send makes the request, with exactly the auth mechanics the runtime uses
// too: API key via x-api-key, subscription OAuth token via Bearer plus the
// oauth beta header. For OAuth tokens Anthropic requires the Claude Code
// identity block as the first system segment.
func send(ctx context.Context, credential string, oauth bool, call Call, system string, messages []Message, stream bool) (*http.Response, error) {
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
		Stream:    stream,
	}
	if call.Effort != "" {
		req.OutputConfig = &outputConfig{Effort: call.Effort}
	}
	if call.NoThinking {
		req.Thinking = &thinkingConfig{Type: "disabled"}
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, BaseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	hreq.Header.Set("content-type", "application/json")
	hreq.Header.Set("anthropic-version", "2023-06-01")
	if oauth {
		hreq.Header.Set("Authorization", "Bearer "+credential)
		hreq.Header.Set("anthropic-beta", "oauth-2025-04-20")
	} else {
		hreq.Header.Set("x-api-key", credential)
	}
	return http.DefaultClient.Do(hreq)
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

// IsOAuth says how a credential authenticates: as a subscription OAuth token
// (Bearer) or as an API key (x-api-key). The value's own prefix decides where
// it has one — sk-ant-oat… is a token, sk-ant-api… a key, whatever it was
// filed as (the save check in internal/httpapi/credcheck.go reads the same
// prefixes). Only a value without a known prefix falls back on what it was
// declared as.
func IsOAuth(value string, declaredSubscription bool) bool {
	value = strings.TrimSpace(value)
	switch {
	case strings.HasPrefix(value, "sk-ant-oat"):
		return true
	case strings.HasPrefix(value, "sk-ant-api"):
		return false
	}
	return declaredSubscription
}
