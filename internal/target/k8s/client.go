package k8s

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// client is the thin REST client against a Kubernetes API server. No
// client-go: that library pulls in a third of Kubernetes for what is a plain
// JSON API behind a bearer token, and the actions here need a dozen paths.
type client struct {
	base  string
	token string
	hc    *http.Client
}

const requestTimeout = 30 * time.Second

// newClient builds the client for one action. caPEM is the cluster's CA
// certificate; empty means "the API server presents a publicly trusted
// certificate" and the system roots apply.
//
// There is deliberately no way to skip verification. A k8s client that talks to
// a production cluster without checking who answers is a man-in-the-middle
// waiting to happen, and the token it would hand over is the one that reads
// every namespace. An operator with a self-signed cluster CA has to supply it.
func newClient(base, token, caPEM string) (*client, error) {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		return nil, fmt.Errorf("k8s_url is missing — the API server endpoint (e.g. https://cluster.example:6443)")
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" {
		return nil, fmt.Errorf("k8s_url must be an https URL, got %q", base)
	}
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("k8s_token is missing — a ServiceAccount token")
	}

	tr := &http.Transport{}
	if pem := strings.TrimSpace(caPEM); pem != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(pem)) {
			return nil, fmt.Errorf("ca_pem is not a readable PEM certificate")
		}
		tr.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	}
	return &client{base: base, token: token, hc: &http.Client{Transport: tr}}, nil
}

// do performs one API call. A non-2xx answer comes back as an error carrying
// the API server's own message — its 403s name the missing RBAC verb exactly,
// which is the most useful thing an agent can be told when a read is refused.
func (c *client) do(ctx context.Context, method, path string, query url.Values, body []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	full := c.base + path
	if len(query) > 0 {
		full += "?" + query.Encode()
	}
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, full, rdr)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/strategic-merge-patch+json")
	}

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("api server unreachable (%s): %w", c.base, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("kubernetes API: %s: %s", resp.Status, apiMessage(raw))
	}
	return raw, nil
}

// apiMessage pulls the human-readable part out of a Kubernetes Status object so
// an RBAC refusal reads as the sentence the API server wrote rather than as a
// wall of JSON.
func apiMessage(raw []byte) string {
	var st struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &st); err == nil && st.Message != "" {
		return st.Message
	}
	s := strings.TrimSpace(string(raw))
	if len(s) > 400 {
		s = s[:400] + " …"
	}
	return s
}

func (c *client) getJSON(ctx context.Context, path string, query url.Values, out any) error {
	raw, err := c.do(ctx, http.MethodGet, path, query, nil)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// getText fetches a non-JSON body (container logs).
func (c *client) getText(ctx context.Context, path string, query url.Values) (string, error) {
	raw, err := c.do(ctx, http.MethodGet, path, query, nil)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
