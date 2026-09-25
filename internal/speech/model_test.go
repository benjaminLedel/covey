package speech

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func fakeModel(t *testing.T, data []byte) (Model, *httptest.Server) {
	t.Helper()
	sum := sha256.Sum256(data)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(data)
	}))
	t.Cleanup(srv.Close)
	return Model{Name: "test", URL: srv.URL, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(data))}, srv
}

func wait(t *testing.T, s *Store) (bool, error) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ready, fetching, err := s.Status()
		if !fetching {
			return ready, err
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("fetch did not finish")
	return false, nil
}

func TestFetchesVerifiesAndKeepsTheModel(t *testing.T) {
	m, _ := fakeModel(t, []byte("whisper weights"))
	s := &Store{Model: m, Dir: t.TempDir()}
	s.Ensure()
	if ready, err := wait(t, s); !ready {
		t.Fatalf("not ready: %v", err)
	}
	got, err := os.ReadFile(s.Path())
	if err != nil || string(got) != "whisper weights" {
		t.Fatalf("file = %q, %v", got, err)
	}
}

func TestAWrongDigestIsNeverKept(t *testing.T) {
	m, _ := fakeModel(t, []byte("whisper weights"))
	m.SHA256 = "00" + m.SHA256[2:]
	s := &Store{Model: m, Dir: t.TempDir()}
	s.Ensure()
	if ready, err := wait(t, s); ready || err == nil {
		t.Fatalf("ready=%v err=%v, want a digest error", ready, err)
	}
	if _, err := os.Stat(s.Path()); !os.IsNotExist(err) {
		t.Fatalf("model file exists after a failed verification: %v", err)
	}
}

func TestAPlacedFileIsVerifiedNotTrusted(t *testing.T) {
	m, _ := fakeModel(t, []byte("whisper weights"))
	m.URL = "http://127.0.0.1:1/unreachable"
	dir := t.TempDir()
	s := &Store{Model: m, Dir: dir}
	if err := os.WriteFile(s.Path(), []byte("something else"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.Ensure()
	if ready, _ := wait(t, s); ready {
		t.Fatal("a file with the wrong digest was accepted")
	}

	// The right file, placed by an operator, needs no network.
	s = &Store{Model: m, Dir: dir}
	if err := os.WriteFile(s.Path(), []byte("whisper weights"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.Ensure()
	if ready, err := wait(t, s); !ready {
		t.Fatalf("placed file not accepted: %v", err)
	}
}

func TestOffAndUnknownModels(t *testing.T) {
	if s, err := New("off", t.TempDir(), nil); s != nil || err != nil {
		t.Fatalf("off: %v %v", s, err)
	}
	if _, err := New("huge", t.TempDir(), nil); err == nil {
		t.Fatal("unknown model accepted")
	}
	if s, err := New("base", "/data", nil); err != nil || s.Path() != "/data/models/ggml-base.bin" {
		t.Fatalf("base: %v %v", s, err)
	}
}
