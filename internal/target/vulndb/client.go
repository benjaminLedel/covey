package vulndb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"covey/internal/reqlog"
)

// Gemeinsamer HTTP-Unterbau der vier Quellen. Alle Aufrufe laufen im
// Sandbox-Daemon und damit durch den Egress-Proxy — ein gesperrter Host
// scheitert deshalb nicht als Zeitüberschreitung, sondern als Proxy-Fehler.
// Das ist der häufigste Betriebsfehler dieses Plugins, und der Agent kann ihn
// nur melden, wenn die Meldung ihn benennt: darum die Umschreibung in
// netError.

// maxBody begrenzt, was eine Quelle zurückgeben darf. npm liefert für große
// Pakete Metadaten im zweistelligen Megabyte-Bereich; ohne Deckel zieht ein
// einziger Aufruf den Sandbox-Speicher leer.
const maxBody = 24 << 20

// httpTimeout ist großzügig bemessen: ein OSV-Batch über mehrere hundert
// Pakete rechnet serverseitig eine Weile.
const httpTimeout = 60 * time.Second

func newHTTPClient() *http.Client {
	return reqlog.Client("vulndb", httpTimeout)
}

// getJSON holt eine JSON-Antwort. header darf nil sein.
func getJSON(ctx context.Context, c *http.Client, rawURL string, header map[string]string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	return do(ctx, c, req, out)
}

// postJSON schickt einen JSON-Körper und liest eine JSON-Antwort.
func postJSON(ctx context.Context, c *http.Client, rawURL string, body, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return do(ctx, c, req, out)
}

func do(ctx context.Context, c *http.Client, req *http.Request, out any) error {
	resp, err := c.Do(req)
	if err != nil {
		return netError(req.URL, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return fmt.Errorf("%s: reading the answer: %w", req.URL.Host, err)
	}
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return errNotFound
	case resp.StatusCode == http.StatusTooManyRequests:
		return fmt.Errorf("%s: rate limit reached (HTTP 429) — wait or store an API key as vulndb_token", req.URL.Host)
	case resp.StatusCode == http.StatusForbidden && len(raw) == 0:
		// Ein leerer 403 kommt in dieser Umgebung fast immer vom
		// Egress-Proxy, nicht von der Datenbank.
		return fmt.Errorf("%s: refused without a body (HTTP 403) — the host is probably missing from the egress allowlist", req.URL.Host)
	case resp.StatusCode >= 400:
		return fmt.Errorf("%s: HTTP %d: %s", req.URL.Host, resp.StatusCode, snippet(raw))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%s: answer is not valid JSON: %w", req.URL.Host, err)
	}
	return nil
}

// errNotFound trennt „gibt es nicht" von „ging schief" — ein Advisory, das eine
// Quelle nicht kennt, ist ein normaler Zustand und kein Fehler.
var errNotFound = fmt.Errorf("not known to this source")

// netError benennt den wahrscheinlichen Grund. Ohne diesen Hinweis meldet der
// Agent „die Datenbank antwortet nicht", und niemand kommt auf die Egress-Liste.
func netError(u *url.URL, err error) error {
	return fmt.Errorf("%s not reachable (%v) — if this persists, the host is probably missing from the egress allowlist", u.Host, err)
}

func snippet(raw []byte) string {
	const max = 200
	if len(raw) > max {
		return string(raw[:max]) + "…"
	}
	return string(raw)
}
