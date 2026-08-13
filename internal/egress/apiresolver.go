package egress

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

// AllowlistResponse is what the control plane answers for one agent.
//
// The token hash travels along deliberately: without it the proxy would have to
// ask back on every single request instead of checking the per-sandbox token
// locally. It is a hash of a token the runner already receives in full when it
// starts the sandbox, so it discloses nothing the holder does not have anyway.
type AllowlistResponse struct {
	Patterns  []string `json:"patterns"`
	TokenHash string   `json:"token_hash"`
}

// DecisionsRequest is a batch of the decision log.
type DecisionsRequest struct {
	Decisions []Decision `json:"decisions"`
}

// APIResolver fetches the allowlist from the control plane instead of from
// Postgres — the construction the standalone proxy needs. It runs in the proxy
// container, which on a remote runner is a foreign host: handing it the
// database URL would mean distributing the Postgres credentials to every
// machine that runs sandboxes (spec/16, "Trust boundary"). It authenticates
// with the runner token of the runner it belongs to.
type APIResolver struct {
	*resolver
	base   string // control plane base URL, without a trailing slash
	token  string // runner token
	client *http.Client
}

// NewAPIResolver starts the resolver together with its log writer, bound to ctx.
func NewAPIResolver(ctx context.Context, baseURL, token string, defaults []string, ttl time.Duration, log *slog.Logger) *APIResolver {
	a := &APIResolver{
		base:  strings.TrimRight(baseURL, "/"),
		token: token,
		// A timeout that is short on purpose: the proxy holds a sandbox's
		// connection open while it asks. Whoever waits 30 seconds here has
		// turned a control-plane hiccup into a hanging agent.
		client: &http.Client{Timeout: 10 * time.Second},
	}
	a.resolver = newResolver(ctx, a.load, a.write, defaults, ttl, log)
	return a
}

func (a *APIResolver) load(ctx context.Context, id uuid.UUID) ([]string, string, error) {
	u := a.base + "/api/runner/v1/egress/allowlist?agent=" + url.QueryEscape(id.String())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Authorization", "Bearer "+a.token)

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		// The agent does not exist, or not in this runner's organisation. That
		// is a fail-closed answer and not a fault, so it may be cached: without
		// that, a sandbox left over from a deleted agent would keep asking.
		return nil, "", nil
	default:
		return nil, "", fmt.Errorf("allowlist: %s: %s", resp.Status, firstBytes(resp.Body))
	}

	var out AllowlistResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, "", err
	}
	return out.Patterns, out.TokenHash, nil
}

func (a *APIResolver) write(ctx context.Context, batch []Decision) error {
	body, err := json.Marshal(DecisionsRequest{Decisions: batch})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		a.base+"/api/runner/v1/egress/decisions", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+a.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("decisions: %s: %s", resp.Status, firstBytes(resp.Body))
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// firstBytes keeps an error body to the part that says something — it ends up
// in a log line, and a page of HTML from a reverse proxy in between buries it.
func firstBytes(r io.Reader) string {
	buf, _ := io.ReadAll(io.LimitReader(r, 200))
	return strings.TrimSpace(string(buf))
}
