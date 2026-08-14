package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"time"

	"covey/internal/buildinfo"
)

// The two calls register and run make before the protocol takes over. Both go
// through the same runner API a runner uses for everything else — it has no
// second way to reach the platform.

var client = &http.Client{Timeout: 30 * time.Second}

type registerRequest struct {
	Token       string   `json:"token"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Version     string   `json:"version,omitempty"`
	Arch        string   `json:"arch,omitempty"`
}

type registerResponse struct {
	identity
	Token string `json:"token"`
}

// register turns the organisation's registration token into this host's own.
func register(ctx context.Context, controlURL, token, description string, tags []string) (identity, string, error) {
	body, err := json.Marshal(registerRequest{
		Token: token, Description: description, Tags: tags,
		Version: buildinfo.String(), Arch: runtime.GOARCH,
	})
	if err != nil {
		return identity{}, "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(controlURL, "/")+"/api/runner/v1/register", bytes.NewReader(body))
	if err != nil {
		return identity{}, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return identity{}, "", fmt.Errorf("the control plane is not reachable at %s: %w", controlURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return identity{}, "", fmt.Errorf("registration refused (%s): %s", resp.Status, message(resp.Body))
	}
	var out registerResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return identity{}, "", err
	}
	return out.identity, out.Token, nil
}

// whoami asks who this token belongs to. It runs before the connection because
// a wrong token should say so here, and not as a WebSocket that closes without
// a reason.
func whoami(ctx context.Context, controlURL, token string) (identity, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimRight(controlURL, "/")+"/api/runner/v1/whoami", nil)
	if err != nil {
		return identity{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return identity{}, fmt.Errorf("the control plane is not reachable at %s: %w", controlURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return identity{}, fmt.Errorf("this runner token is not valid at %s — "+
			"registered against another instance, or revoked there", controlURL)
	}
	if resp.StatusCode != http.StatusOK {
		return identity{}, fmt.Errorf("%s: %s", resp.Status, message(resp.Body))
	}
	var out identity
	return out, json.NewDecoder(resp.Body).Decode(&out)
}

// message keeps an error body to the part that says something.
func message(r io.Reader) string {
	raw, _ := io.ReadAll(io.LimitReader(r, 500))
	var out struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(raw, &out); err == nil && out.Error != "" {
		return out.Error
	}
	return strings.TrimSpace(string(raw))
}
