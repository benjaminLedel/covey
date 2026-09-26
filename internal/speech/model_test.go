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
	return Model{Name: "test", Engine: "whisper", Files: []File{{Name: "model.bin", URL: srv.URL, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(data))}}}, srv
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
	got, err := os.ReadFile(s.Path("model.bin"))
	if err != nil || string(got) != "whisper weights" {
		t.Fatalf("file = %q, %v", got, err)
	}
}

func TestAWrongDigestIsNeverKept(t *testing.T) {
	m, _ := fakeModel(t, []byte("whisper weights"))
	m.Files[0].SHA256 = "00" + m.Files[0].SHA256[2:]
	s := &Store{Model: m, Dir: t.TempDir()}
	s.Ensure()
	if ready, err := wait(t, s); ready || err == nil {
		t.Fatalf("ready=%v err=%v, want a digest error", ready, err)
	}
	if _, err := os.Stat(s.Path("model.bin")); !os.IsNotExist(err) {
		t.Fatalf("model file exists after a failed verification: %v", err)
	}
}

func TestAPlacedFileIsVerifiedNotTrusted(t *testing.T) {
	m, _ := fakeModel(t, []byte("whisper weights"))
	m.Files[0].URL = "http://127.0.0.1:1/unreachable"
	dir := t.TempDir()
	s := &Store{Model: m, Dir: dir}
	if err := os.WriteFile(s.Path("model.bin"), []byte("something else"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.Ensure()
	if ready, _ := wait(t, s); ready {
		t.Fatal("a file with the wrong digest was accepted")
	}

	// The right file, placed by an operator, needs no network.
	s = &Store{Model: m, Dir: dir}
	if err := os.WriteFile(s.Path("model.bin"), []byte("whisper weights"), 0o644); err != nil {
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
	if s, err := New("base", "/data", nil); err != nil || s.Path("ggml-base.bin") != "/data/models/base/ggml-base.bin" {
		t.Fatalf("base: %v %v", s, err)
	}
}

func TestAModelOfSeveralFilesIsFetchedFileByFile(t *testing.T) {
	files := map[string][]byte{"/encoder.onnx": []byte("encoder weights"), "/tokens.txt": []byte("a 0\nb 1\n")}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(files[r.URL.Path])
	}))
	t.Cleanup(srv.Close)
	m := Model{Name: "multi", Engine: "parakeet"}
	for _, name := range []string{"encoder.onnx", "tokens.txt"} {
		data := files["/"+name]
		sum := sha256.Sum256(data)
		m.Files = append(m.Files, File{Name: name, URL: srv.URL + "/" + name, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(data))})
	}
	s := &Store{Model: m, Dir: t.TempDir()}
	s.Ensure()
	if ready, err := wait(t, s); !ready {
		t.Fatalf("not ready: %v", err)
	}
	for name, data := range files {
		got, err := os.ReadFile(s.Path(name[1:]))
		if err != nil || string(got) != string(data) {
			t.Fatalf("%s = %q, %v", name, got, err)
		}
	}
	if s.Received() != m.Size() {
		t.Fatalf("received %d, want %d", s.Received(), m.Size())
	}
	if d := m.Digest(); d == m.Files[0].SHA256 || len(d) != 64 {
		t.Fatalf("digest of several files = %q", d)
	}
}

func TestAWhisperModelFromBeforeItsOwnDirectoryIsKept(t *testing.T) {
	m, _ := fakeModel(t, []byte("whisper weights"))
	m.Files[0].URL = "http://127.0.0.1:1/unreachable"
	data := t.TempDir()
	if err := os.WriteFile(data+"/model.bin", []byte("whisper weights"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &Store{Model: m, Dir: data + "/test"}
	s.Ensure()
	if ready, err := wait(t, s); !ready {
		t.Fatalf("legacy file not taken over: %v", err)
	}
}
