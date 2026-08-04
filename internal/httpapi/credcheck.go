package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// anthropicBaseURL is the address the live validation probes — a variable
// rather than a constant so tests can slip in an httptest server.
var anthropicBaseURL = "https://api.anthropic.com"

// CredCheck is the result of validating a credential on save. Checked=false
// means: not a known credential key, or the check was not possible — the
// secret is stored regardless (a network outage must not block saving it).
type CredCheck struct {
	Checked bool   `json:"checked"`
	Valid   bool   `json:"valid"`
	Hint    string `json:"hint,omitempty"`
}

// checkCredential validates known credential keys right at save time, so a
// dead token shows up here — and not only when a task dies deep inside the
// sandbox with a 401. Misfiled values (API key in the OAuth slot and vice
// versa) are caught without touching the network.
func checkCredential(ctx context.Context, key, value string) CredCheck {
	value = strings.TrimSpace(value)
	switch key {
	case "anthropic_api_key":
		if strings.HasPrefix(value, "sk-ant-oat") {
			return CredCheck{Checked: true, Valid: false,
				Hint: "This is a subscription OAuth token (sk-ant-oat…) — it belongs under the key claude_code_oauth_token."}
		}
		return probeAnthropic(ctx, value, false)
	case "claude_code_oauth_token":
		if strings.HasPrefix(value, "sk-ant-api") {
			return CredCheck{Checked: true, Valid: false,
				Hint: "This is an API key (sk-ant-api…) — it belongs under the key anthropic_api_key."}
		}
		return probeAnthropic(ctx, value, true)
	}
	return CredCheck{}
}

// probeAnthropic makes one light, free API call (the model list) using exactly
// the auth mechanics Claude Code uses inside the sandbox.
func probeAnthropic(ctx context.Context, credential string, oauth bool) CredCheck {
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, anthropicBaseURL+"/v1/models", nil)
	if err != nil {
		return CredCheck{}
	}
	req.Header.Set("anthropic-version", "2023-06-01")
	if oauth {
		// OAuth tokens only authenticate as Bearer with the OAuth beta header.
		req.Header.Set("Authorization", "Bearer "+credential)
		req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	} else {
		req.Header.Set("x-api-key", credential)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return CredCheck{Hint: "Live check not possible (no network route to the Anthropic API) — the value is stored, but unverified."}
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusOK:
		return CredCheck{Checked: true, Valid: true}
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		hint := "The Anthropic API rejects the credential (" + resp.Status + ")."
		if oauth {
			hint += " Subscription tokens are revocable and expire — run `claude setup-token` in the terminal and store the new token here."
		} else {
			hint += " Check the API key in the Anthropic Console, or generate a new one."
		}
		return CredCheck{Checked: true, Valid: false, Hint: hint}
	default:
		return CredCheck{Hint: "Live check returned " + resp.Status + " — the value is stored, but unverified."}
	}
}
