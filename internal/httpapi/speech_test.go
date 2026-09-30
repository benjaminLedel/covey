package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"covey/internal/speech"
)

func TestSpeechModelIsServedOnlyOnceVerified(t *testing.T) {
	weights := []byte("model weights")
	sum := sha256.Sum256(weights)
	store := &speech.Store{
		Model: speech.Model{Name: "test", Engine: "parakeet", Files: []speech.File{{Name: "model.bin", URL: "http://127.0.0.1:1/unreachable", SHA256: hex.EncodeToString(sum[:]), Size: int64(len(weights))}}},
		Dir:   t.TempDir(),
	}
	s := &Server{Speech: speech.SetOf(store)}

	info := func() map[string]any {
		w := httptest.NewRecorder()
		s.handleSpeechModel(w, httptest.NewRequest(http.MethodGet, "/api/v1/speech/model", nil))
		out := map[string]any{}
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return out
	}
	file := func(h http.Header) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/speech/model/file", nil)
		for k, v := range h {
			r.Header[k] = v
		}
		s.handleSpeechModelFile(w, r)
		return w
	}
	settle := func() {
		for i := 0; i < 200; i++ {
			if _, fetching, _ := store.Status(); !fetching {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	// Unreachable source, nothing placed: not ready, the reason said.
	_ = info()
	settle()
	if got := info(); got["ready"] != false || got["sha256"] != hex.EncodeToString(sum[:]) {
		t.Fatalf("info before = %v", got)
	}
	if w := file(nil); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("file before = %d", w.Code)
	}

	// An operator places the file: the next ask verifies and serves it,
	// resumably.
	settle()
	if err := os.WriteFile(store.Path("model.bin"), weights, 0o644); err != nil {
		t.Fatal(err)
	}
	_ = info()
	settle()
	if got := info(); got["ready"] != true {
		t.Fatalf("info after = %v", got)
	}
	if w := file(nil); w.Code != http.StatusOK || w.Body.String() != string(weights) {
		t.Fatalf("file = %d %q", w.Code, w.Body.String())
	}
	if w := file(http.Header{"Range": {"bytes=6-"}}); w.Code != http.StatusPartialContent || w.Body.String() != "weights" {
		t.Fatalf("range = %d %q", w.Code, w.Body.String())
	}
}

func TestSpeechOffSaysSo(t *testing.T) {
	s := &Server{}
	w := httptest.NewRecorder()
	s.handleSpeechModel(w, httptest.NewRequest(http.MethodGet, "/api/v1/speech/model", nil))
	if w.Body.String() != "{\"enabled\":false}\n" {
		t.Fatalf("off = %q", w.Body.String())
	}
}

func TestSpeechModelsAreListedAndPickedByName(t *testing.T) {
	set, err := speech.NewSet("parakeet", []string{"sensevoice"}, nil, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	// The fetches the requests start run on after the test (#409). This
	// cleanup comes after TempDir's and so runs before it: the directory is
	// removed only once no fetch is still writing into it.
	t.Cleanup(func() {
		deadline := time.Now().Add(10 * time.Second)
		for _, n := range set.Names {
			st, _ := set.Get(n)
			for {
				if _, fetching, _ := st.Status(); !fetching || time.Now().After(deadline) {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
		}
	})
	// Asking about a model starts its fetch: nowhere, in a test.
	for _, n := range set.Names {
		st, _ := set.Get(n)
		for i := range st.Model.Files {
			st.Model.Files[i].URL = "http://127.0.0.1:1/unreachable"
		}
	}
	s := &Server{Speech: set}
	get := func(q string) (int, map[string]any) {
		w := httptest.NewRecorder()
		s.handleSpeechModel(w, httptest.NewRequest(http.MethodGet, "/api/v1/speech/model"+q, nil))
		out := map[string]any{}
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	_, def := get("")
	if def["name"] != "parakeet" || def["default"] != "parakeet" {
		t.Fatalf("default = %v", def)
	}
	models, _ := def["models"].([]any)
	var names []string
	for _, m := range models {
		names = append(names, m.(map[string]any)["name"].(string))
	}
	if strings.Join(names, ",") != "silero,titanet,sensevoice,parakeet" {
		t.Fatalf("models = %v, want smallest first", names)
	}
	if _, sv := get("?name=sensevoice"); sv["name"] != "sensevoice" || sv["engine"] != "sensevoice" || sv["size"] != float64(speech.Models["sensevoice"].Size()) {
		t.Fatalf("sensevoice = %v", sv)
	}
	if code, _ := get("?name=base"); code != http.StatusNotFound {
		t.Fatalf("a model not offered: %d", code)
	}
}

// A voice the operator offers (#497) is listed under voices, with what the
// app needs to load it, and not under models, which an older app takes for
// recognisers.
func TestSpeechVoicesAreListedApart(t *testing.T) {
	set, err := speech.NewSet("parakeet", nil, []string{"piper-en-norman"}, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	// Nothing is fetched in a test: the stores are asked, not ensured.
	for _, n := range append(append([]string{}, set.Names...), set.Voices...) {
		st, _ := set.Get(n)
		for i := range st.Model.Files {
			st.Model.Files[i].URL = "http://127.0.0.1:1/unreachable"
		}
	}
	t.Cleanup(func() {
		deadline := time.Now().Add(10 * time.Second)
		for _, n := range append(append([]string{}, set.Names...), set.Voices...) {
			st, _ := set.Get(n)
			for {
				if _, fetching, _ := st.Status(); !fetching || time.Now().After(deadline) {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
		}
	})
	s := &Server{Speech: set}
	w := httptest.NewRecorder()
	s.handleSpeechModel(w, httptest.NewRequest(http.MethodGet, "/api/v1/speech/model", nil))
	var out struct {
		Models []map[string]any `json:"models"`
		Voices []map[string]any `json:"voices"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	for _, m := range out.Models {
		if m["engine"] == speech.EngineTTS {
			t.Fatalf("a voice among the models: %v", m)
		}
	}
	if len(out.Voices) != 1 {
		t.Fatalf("voices = %v", out.Voices)
	}
	v := out.Voices[0]
	info, _ := v["voice"].(map[string]any)
	if v["name"] != "piper-en-norman" || v["engine"] != "tts" || v["unpack"] != "tar.bz2" ||
		info["family"] != "vits" || info["language"] != "en-US" || info["placeholder"] != true || info["licence"] == "" {
		t.Fatalf("voice = %v", v)
	}
}
