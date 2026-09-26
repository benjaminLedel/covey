// Package speech holds the speech models the covey app transcribes with
// (#348, #351, #353).
//
// The app recognises speech on the device, and the model files come from
// the covey instance rather than from a third party at the app's first
// start: the instance fetches them once from pinned URLs, verifies each
// against a pinned SHA-256, keeps them under the data directory and serves
// them to signed-in apps. An installation that must not reach the internet
// puts the files there itself; they are verified the same way. A file whose
// digest does not match is never served.
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

// File is one file of a model, pinned by digest.
type File struct {
	Name   string `json:"name"`
	URL    string `json:"-"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Model is one speech model as the instance offers it: which engine runs it
// on the phone, and its files.
type Model struct {
	Name   string
	Engine string // "whisper" (whisper.cpp) or "parakeet" (sherpa-onnx)
	// Credit is the attribution the model's licence asks for, shown with it.
	Credit string
	Files  []File
}

// Size is the model's files together.
func (m Model) Size() int64 {
	var n int64
	for _, f := range m.Files {
		n += f.Size
	}
	return n
}

// Digest identifies the model's exact files: the file's own digest for a
// single file, otherwise the SHA-256 over the files' names and digests. The
// app keeps a model under it, so a changed file is a new download.
func (m Model) Digest() string {
	if len(m.Files) == 1 {
		return m.Files[0].SHA256
	}
	h := sha256.New()
	for _, f := range m.Files {
		fmt.Fprintf(h, "%s %s\n", f.Name, f.SHA256)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// File returns the named file of the model.
func (m Model) File(name string) (File, bool) {
	for _, f := range m.Files {
		if f.Name == name {
			return f, true
		}
	}
	return File{}, false
}

// Models are the models the instance can offer, pinned by digest.
//
// Whisper: the multilingual ggml models of whisper.cpp — covey is used in
// more languages than English. Parakeet: NVIDIA's Parakeet TDT 0.6B v3 in
// sherpa-onnx's int8 export, 25 European languages (#353).
var Models = map[string]Model{
	"tiny":   whisper("tiny", "be07e048e1e599ad46341c8d2a135645097a538221678b7acdd1b1919c6e1b21", 77691713),
	"base":   whisper("base", "60ed5bc3dd14eea856493d334349b405782ddcaf0028d4b5df4088345fba2efe", 147951465),
	"small":  whisper("small", "1be3a9b2063867b937e64e2ec7483364a79917e157fa98c5d94b5c1fffea987b", 487601967),
	"medium": whisper("medium", "6c14d5adee5f86394037b4e4e8b59f1673b6cee10e3cf0b11bbdbee79c156208", 1533763059),
	"parakeet": {
		Name:   "parakeet",
		Engine: "parakeet",
		Credit: "NVIDIA Parakeet TDT 0.6B v3 · CC-BY-4.0",
		Files: []File{
			parakeet("encoder.int8.onnx", "acfc2b4456377e15d04f0243af540b7fe7c992f8d898d751cf134c3a55fd2247", 652184281),
			parakeet("decoder.int8.onnx", "179e50c43d1a9de79c8a24149a2f9bac6eb5981823f2a2ed88d655b24248db4e", 11845275),
			parakeet("joiner.int8.onnx", "3164c13fc2821009440d20fcb5fdc78bff28b4db2f8d0f0b329101719c0948b3", 6355277),
			parakeet("tokens.txt", "d58544679ea4bc6ac563d1f545eb7d474bd6cfa467f0a6e2c1dc1c7d37e3c35d", 93939),
		},
	},
}

func whisper(name, sum string, size int64) Model {
	return Model{Name: name, Engine: "whisper", Files: []File{{
		Name:   "ggml-" + name + ".bin",
		URL:    "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-" + name + ".bin",
		SHA256: sum,
		Size:   size,
	}}}
}

func parakeet(name, sum string, size int64) File {
	return File{
		Name:   name,
		URL:    "https://huggingface.co/csukuangfj/sherpa-onnx-nemo-parakeet-tdt-0.6b-v3-int8/resolve/main/" + name,
		SHA256: sum,
		Size:   size,
	}
}

// Store keeps one model under Dir.
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
		return nil, fmt.Errorf("unknown speech model %q (tiny, base, small, medium, parakeet or off)", name)
	}
	return &Store{Model: m, Dir: filepath.Join(dataDir, "models", name), Log: log}, nil
}

// Path is where one of the model's files lives.
func (s *Store) Path(file string) string { return filepath.Join(s.Dir, file) }

// Status says whether the model can be served, and why not.
func (s *Store) Status() (ready bool, fetching bool, lastErr error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.verified, s.fetching, s.lastErr
}

// Received is how much of the model a running fetch has downloaded.
func (s *Store) Received() int64 { return s.received.Load() }

// Ensure makes the model available: files already on disk are verified
// once; missing ones are fetched in the background. It returns at once — the
// caller asks Status (or tries again) rather than waiting on hundreds of MB
// inside a request.
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
	s.received.Store(0)
	for _, f := range s.Model.Files {
		path := s.Path(f.Name)
		// Before #353 a Whisper model lay directly in models/: moved into its
		// own directory rather than fetched again.
		legacy := filepath.Join(filepath.Dir(s.Dir), f.Name)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			if _, err := os.Stat(legacy); err == nil {
				_ = os.Rename(legacy, path)
			}
		}
		if _, err := os.Stat(path); err == nil {
			// Put there by a previous fetch or by an operator: verified before
			// it is trusted, and removed if it is not what the pin says.
			if err := verifyFile(path, f.SHA256); err != nil {
				_ = os.Remove(path)
				return err
			}
			s.received.Add(f.Size)
			continue
		}
		if err := s.fetch(ctx, f); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) fetch(ctx context.Context, f File) error {
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Minute}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.URL, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("fetching %s/%s: %w", s.Model.Name, f.Name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetching %s/%s: HTTP %d", s.Model.Name, f.Name, resp.StatusCode)
	}
	tmp, err := os.CreateTemp(s.Dir, f.Name+".*.part")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h, progress{&s.received}), io.LimitReader(resp.Body, f.Size+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if n != f.Size {
		return fmt.Errorf("fetching %s/%s: %d bytes, expected %d", s.Model.Name, f.Name, n, f.Size)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != f.SHA256 {
		return fmt.Errorf("fetching %s/%s: sha256 %s, expected %s", s.Model.Name, f.Name, got, f.SHA256)
	}
	return os.Rename(tmp.Name(), s.Path(f.Name))
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
