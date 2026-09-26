package push

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// RelayPath is where an instance with the app's key accepts notifications
// from instances without one.
const RelayPath = "/api/v1/push/relay"

// Relay hands a notification to an instance that holds the app's APNs key.
type Relay struct {
	URL    string // the relay instance, e.g. https://app.covey.work
	client *http.Client
}

func NewRelay(url string) *Relay {
	return &Relay{URL: strings.TrimRight(url, "/"), client: &http.Client{Timeout: 15 * time.Second}}
}

func (r *Relay) Send(ctx context.Context, m Message) error {
	body, _ := json.Marshal(m)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.URL+RelayPath, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	switch resp.StatusCode {
	case http.StatusAccepted, http.StatusOK, http.StatusNoContent:
		return nil
	case http.StatusGone:
		return ErrGone
	}
	return fmt.Errorf("push relay: HTTP %d", resp.StatusCode)
}
