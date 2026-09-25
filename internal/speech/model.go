// Package speech holds the Whisper model the covey app transcribes with
// (#348).
//
// The app recognises speech on the device with whisper.cpp, and the model
// file comes from the covey instance rather than from a third party at the
// app's first start: the instance fetches it once from a pinned URL,
// verifies it against a pinned SHA-256, keeps it under the data directory
// and serves it to signed-in apps. An installation that must not reach the
// internet puts the file there itself; it is verified the same way. A file
// whose digest does not match is never served.
package speech

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// Model is one Whisper model as the instance offers it.
type Model struct {
	Name   string `json:"name"`
	URL    string `json:"-"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Models are the ggml Whisper models of whisper.cpp, pinned by digest. The
// multilingual ones only: covey is used in more languages than English.
var Models = map[string]Model{
	"tiny":   {Name: "tiny", URL: hf("tiny"), SHA256: "be07e048e1e599ad46341c8d2a135645097a538221678b7acdd1b1919c6e1b21", Size: 77691713},
	"base":   {Name: "base", URL: hf("base"), SHA256: "60ed5bc3dd14eea856493d334349b405782ddcaf0028d4b5df4088345fba2efe", Size: 147951465},
	"small":  {Name: "small", URL: hf("small"), SHA256: "1be3a9b2063867b937e64e2ec7483364a79917e157fa98c5d94b5c1fffea987b", Size: 487601967},
	"medium": {Name: "medium", URL: hf("medium"), SHA256: "6c14d5adee5f86394037b4e4e8b59f1673b6cee10e3cf0b11bbdbee79c156208", Size: 1533763059},
}

func hf(name string) string {
	return "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-" + name + ".bin"
}

// Store keeps one model under dir.
type Store struct {
	Model  Model
	Dir    string
	Client *http.Client
	Log    *slog.Logger

	// received counts the bytes of a running fetch, for the progress the
	// app shows while the instance is still downloading.
	received atomic.Int64

	mu       sync.Mutex
	fetching bool
	verified bool
	lastErr  error
}

// New returns the store for the named model, or nil when speech is switched
// off ("off" or an empty name).
func New(name, dataDir string, log *slog.Logger) (*Store, error) {
	if name == "" || name == "off" {
		return nil, nil
	}
	m, ok := Models[name]
	if !ok {
		return nil, fmt.Errorf("unknown speech model %q (tiny, base, small, medium or off)", name)
	}
	return &Store{Model: m, Dir: filepath.Join(dataDir, "models"), Log: log}, nil
}

// Path is where the model file lives.
func (s *Store) Path() string { return filepath.Join(s.Dir, "ggml-"+s.Model.Name+".bin") }

// Status says whether the model can be served, and why not.
func (s *Store) Status() (ready bool, fetching bool, lastErr error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.verified, s.fetching, s.lastErr
}

// Received is how much of the model a running fetch has downloaded.
func (s *Store) Received() int64 { return s.received.Load() }

// Ensure makes the model available: a file already on disk is verified once;
// a missing one is fetched in the background. It returns at once — the
// caller asks Status (or tries again) rather than waiting on a 150 MB
// download inside a request.
func (s *Store) Ensure() {
	s.mu.Lock()
	if s.verified || s.fetching {
		s.mu.Unlock()
		return
	}
	s.fetching = true
	s.mu.Unlock()
	go func() {
		err := s.prepare(context.Background())
		s.mu.Lock()
		s.fetching = false
		s.verified = err == nil
		s.lastErr = err
		s.mu.Unlock()
		if err != nil && s.Log != nil {
			s.Log.Warn("speech model not available", "model", s.Model.Name, "err", err)
		}
	}()
}

func (s *Store) prepare(ctx context.Context) error {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(s.Path()); err == nil {
		// Put there by a previous fetch or by an operator: verified before it
		// is trusted, and removed if it is not what the pin says.
		if err := verifyFile(s.Path(), s.Model.SHA256); err != nil {
			_ = os.Remove(s.Path())
			return err
		}
		return nil
	}
	return s.fetch(ctx)
}

func (s *Store) fetch(ctx context.Context) error {
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Minute}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.Model.URL, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("fetching %s: %w", s.Model.Name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetching %s: HTTP %d", s.Model.Name, resp.StatusCode)
	}
	tmp, err := os.CreateTemp(s.Dir, "ggml-*.part")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	h := sha256.New()
	s.received.Store(0)
	n, err := io.Copy(io.MultiWriter(tmp, h, progress{&s.received}), io.LimitReader(resp.Body, s.Model.Size+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if n != s.Model.Size {
		return fmt.Errorf("fetching %s: %d bytes, expected %d", s.Model.Name, n, s.Model.Size)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != s.Model.SHA256 {
		return fmt.Errorf("fetching %s: sha256 %s, expected %s", s.Model.Name, got, s.Model.SHA256)
	}
	return os.Rename(tmp.Name(), s.Path())
}

func verifyFile(path, want string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return fmt.Errorf("%s: sha256 %s, expected %s", filepath.Base(path), got, want)
	}
	return nil
}

// progress counts what passes through it.
type progress struct{ n *atomic.Int64 }

func (p progress) Write(b []byte) (int, error) {
	p.n.Add(int64(len(b)))
	return len(b), nil
}
