// Package claudeapi ist der schmale Zugang der Control Plane zur Messages-API.
//
// Die Control Plane ruft Claude an zwei Stellen selbst auf: der Config-Copilot
// hilft beim Schreiben der Agenten-Config, und der Traum räumt nachts das
// Gedächtnis auf. Beide brauchen dieselbe Auth-Mechanik (API-Key oder
// Abo-OAuth-Token der Organisation) und dürfen nicht voneinander abhängen —
// deshalb liegt sie hier und nicht in einem der beiden Aufrufer.
//
// Leitplanke wie gehabt: das Credential verlässt die Control Plane nie. Es geht
// weder in den Browser noch in eine Sandbox.
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

// BaseURL ist Variable statt Konstante, damit Tests einen httptest-Server
// unterschieben können.
var BaseURL = "https://api.anthropic.com"

// Message ist ein Turn. Content ist ein einfacher String — die Messages-API
// nimmt das als einzelnen Text-Block.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Call bündelt, was je Aufrufer verschieden ist.
type Call struct {
	Model     string
	MaxTokens int
	// Effort steuert die Denktiefe (low | medium | high | xhigh | max). Leer =
	// Vorgabe des Modells.
	Effort string
	// NoThinking schaltet das Denken ab. Ab Opus 5 denkt das Modell per Vorgabe,
	// und MaxTokens deckelt Denken *und* Antwort gemeinsam — bei einer eng
	// umrissenen Aufgabe frisst das die ganze Zeit: gemessen zwei Minuten für
	// einen einzigen umzubenennenden Titel. Effort allein half nicht (über das
	// Abo-OAuth-Credential wirkt es nicht sichtbar), das Abschalten schon.
	//
	// Nur für Aufrufe ohne Werkzeuge und mit maschinenlesbarer Antwort: ohne
	// Denken kann das Modell Werkzeugaufrufe in den Fließtext schreiben, und
	// gelegentlich rutschen <thinking>-Marken in die Antwort.
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

// Messages ruft die Messages-API mit exakt der Auth-Mechanik auf, die auch die
// Runtime nutzt: API-Key über x-api-key, Abo-OAuth-Token über Bearer plus
// oauth-Beta-Header. Bei OAuth-Tokens verlangt Anthropic den
// Claude-Code-Identitäts-Block als erstes System-Segment.
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

// ResolveOrg findet das Claude-Credential einer Organisation. API-Key hat
// Vorrang vor dem Abo-OAuth-Token. Beide Aufrufer — Config-Copilot und Traum —
// müssen hier zum selben Ergebnis kommen, deshalb liegt die Reihenfolge an
// einer Stelle.
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
