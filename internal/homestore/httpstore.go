package homestore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

// HTTPStore is the BlobStore of a remote runner: it reaches the blocks through
// the control plane's runner API instead of holding the store itself.
//
// A runner never gets the store's credentials — that would be the same
// omission as the database URL in the egress proxy. With the `builtin` backend
// the control plane hands out the bytes itself; with `s3` it will hand out
// pre-signed URLs so the payload goes past it (spec/16, "How the blocks reach
// the runner"). Either way the runner is a client with a token, not a
// participant in the storage layer.
//
// The organisation is not a parameter of the requests: it follows from the
// runner token, and the control plane scopes every answer to it. A runner that
// asked after a foreign organisation's block would be asking about something
// that, to it, does not exist.
type HTTPStore struct {
	base   string
	token  string
	client *http.Client
}

func NewHTTPStore(controlURL, token string) *HTTPStore {
	return &HTTPStore{
		base:  strings.TrimRight(controlURL, "/"),
		token: token,
		// Generous: a block is up to 4 MB and the line to the control plane may
		// be a slow one. Short enough that a hung transfer does not hold a wake
		// forever.
		client: &http.Client{Timeout: 5 * time.Minute},
	}
}

func (h *HTTPStore) url(hash string) string {
	return h.base + "/api/runner/v1/blocks/" + url.PathEscape(hash)
}

func (h *HTTPStore) do(req *http.Request) (*http.Response, error) {
	req.Header.Set("Authorization", "Bearer "+h.token)
	return h.client.Do(req)
}

func (h *HTTPStore) Has(ctx context.Context, _ uuid.UUID, hash string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, h.url(hash), nil)
	if err != nil {
		return false, err
	}
	resp, err := h.do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("block %s: %s", short(hash), resp.Status)
	}
}

// HasMany satisfies BulkAsker: one question for many blocks.
//
// This is the difference between a home that syncs and one that does not. Every
// block of a home is asked about before it travels, and a grown home has six
// figures of them — one HTTPS round trip each meant a 16.9 GB home never
// finished inside the thirty minutes the control plane allows a sync. The
// answer names only what the store already has; anything left out is missing,
// which is the same information and a far smaller answer on a home the store
// does not know yet.
//
// An older control plane does not have the route. Then this reports "no" for
// nothing and lets the caller fall back to asking one by one — a slow sync is
// better than a wrong one.
func (h *HTTPStore) HasMany(ctx context.Context, orgID uuid.UUID, hashes []string) (map[string]bool, error) {
	if len(hashes) == 0 {
		return map[string]bool{}, nil
	}
	body, err := json.Marshal(map[string]any{"hashes": hashes})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.base+"/api/runner/v1/blocks-have", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
		// A control plane from before the bundled question. Ask singly — but
		// not one after another: the reason the bundle exists is the round
		// trip, and it does not go away because the other side is older.
		return AskEach(ctx, h, orgID, hashes, httpAskWorkers)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("blocks-have: %s", resp.Status)
	}
	var out struct {
		Have []string `json:"have"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&out); err != nil {
		return nil, err
	}
	have := make(map[string]bool, len(out.Have))
	for _, hash := range out.Have {
		have[hash] = true
	}
	return have, nil
}

// httpAskWorkers bounds the fallback against an older control plane. Eight,
// not sixteen: this is the platform's own front door, not a bucket built for
// fan-out.
const httpAskWorkers = 8

func (h *HTTPStore) Put(ctx context.Context, _ uuid.UUID, hash string, r io.Reader) error {
	body, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, h.url(hash), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := h.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("block %s: %s", short(hash), resp.Status)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

func (h *HTTPStore) Get(ctx context.Context, _ uuid.UUID, hash string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.url(hash), nil)
	if err != nil {
		return nil, err
	}
	resp, err := h.do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return nil, fmt.Errorf("%w: %s", ErrNotFound, hash)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("block %s: %s", short(hash), resp.Status)
	}
	return resp.Body, nil
}

// Delete and List are the garbage collection's, and it runs on the control
// plane. A runner has no business removing blocks or enumerating what an
// organisation holds — that is the one operation where a mistake is not
// recoverable.
func (h *HTTPStore) Delete(context.Context, uuid.UUID, string) error {
	return fmt.Errorf("a runner does not delete blocks")
}

func (h *HTTPStore) List(context.Context, uuid.UUID) ([]string, error) {
	return nil, fmt.Errorf("a runner does not enumerate blocks")
}

func short(hash string) string {
	if len(hash) > 8 {
		return hash[:8]
	}
	return hash
}
