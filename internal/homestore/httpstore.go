package homestore

import (
	"bytes"
	"context"
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
