package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// anthropicBaseURL ist die Prüf-Adresse der Live-Validierung — Variable statt
// Konstante, damit Tests einen httptest-Server unterschieben können.
var anthropicBaseURL = "https://api.anthropic.com"

// CredCheck ist das Ergebnis der Speicher-Validierung eines Credentials.
// Checked=false heißt: kein bekannter Credential-Key oder Prüfung nicht
// möglich — das Secret ist trotzdem gespeichert (Netz-Ausfall darf das
// Hinterlegen nicht blockieren).
type CredCheck struct {
	Checked bool   `json:"checked"`
	Valid   bool   `json:"valid"`
	Hint    string `json:"hint,omitempty"`
}

// checkCredential validiert bekannte Credential-Keys sofort beim Speichern,
// damit ein totes Token hier auffällt — und nicht erst, wenn eine Aufgabe tief
// in der Sandbox mit 401 stirbt. Falsch einsortierte Werte (API-Key im
// OAuth-Slot und umgekehrt) werden ohne Netz-Zugriff erkannt.
func checkCredential(ctx context.Context, key, value string) CredCheck {
	value = strings.TrimSpace(value)
	switch key {
	case "anthropic_api_key":
		if strings.HasPrefix(value, "sk-ant-oat") {
			return CredCheck{Checked: true, Valid: false,
				Hint: "Das ist ein Abo-OAuth-Token (sk-ant-oat…) — es gehört unter den Key claude_code_oauth_token."}
		}
		return probeAnthropic(ctx, value, false)
	case "claude_code_oauth_token":
		if strings.HasPrefix(value, "sk-ant-api") {
			return CredCheck{Checked: true, Valid: false,
				Hint: "Das ist ein API-Key (sk-ant-api…) — er gehört unter den Key anthropic_api_key."}
		}
		return probeAnthropic(ctx, value, true)
	}
	return CredCheck{}
}

// probeAnthropic macht einen leichten, kostenlosen API-Aufruf (Modell-Liste)
// mit exakt der Auth-Mechanik, die auch Claude Code in der Sandbox nutzt.
func probeAnthropic(ctx context.Context, credential string, oauth bool) CredCheck {
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, anthropicBaseURL+"/v1/models", nil)
	if err != nil {
		return CredCheck{}
	}
	req.Header.Set("anthropic-version", "2023-06-01")
	if oauth {
		// OAuth-Tokens authentifizieren nur als Bearer mit dem OAuth-Beta-Header.
		req.Header.Set("Authorization", "Bearer "+credential)
		req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	} else {
		req.Header.Set("x-api-key", credential)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return CredCheck{Hint: "Live-Prüfung nicht möglich (kein Netz zur Anthropic-API) — Wert ist gespeichert, aber ungeprüft."}
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusOK:
		return CredCheck{Checked: true, Valid: true}
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		hint := "Die Anthropic-API lehnt das Credential ab (" + resp.Status + ")."
		if oauth {
			hint += " Abo-Token sind widerruflich und laufen ab — im Terminal `claude setup-token` ausführen und das neue Token hier speichern."
		} else {
			hint += " API-Key in der Anthropic-Console prüfen bzw. neu erzeugen."
		}
		return CredCheck{Checked: true, Valid: false, Hint: hint}
	default:
		return CredCheck{Hint: "Live-Prüfung lieferte " + resp.Status + " — Wert ist gespeichert, aber ungeprüft."}
	}
}
