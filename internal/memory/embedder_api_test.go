package memory

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// embedServer is a provider double: it remembers the last request body and
// answers with a vector of the requested length.
func embedServer(t *testing.T, dims int, status int, calls *int32) (*httptest.Server, *map[string]any) {
	t.Helper()
	last := map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls != nil {
			atomic.AddInt32(calls, 1)
		}
		_ = json.NewDecoder(r.Body).Decode(&last)
		last["_auth"] = r.Header.Get("Authorization")
		if status != http.StatusOK {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]string{"message": "quota exhausted"},
			})
			return
		}
		vec := make([]float32, dims)
		for i := range vec {
			vec[i] = float32(i%7) + 1 // unnormalized, so normalize() becomes testable
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"embedding": vec}},
		})
	}))
	t.Cleanup(srv.Close)
	return srv, &last
}

func TestAPIEmbedderVoyage(t *testing.T) {
	srv, last := embedServer(t, Dim, http.StatusOK, nil)
	e, err := NewAPIEmbedder("voyage", "", "geheim", srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	v, err := e.Embed(context.Background(), "Kunde Meier hat ein Login-Problem")
	if err != nil {
		t.Fatal(err)
	}
	// The vector comes back normalized — the thresholds assume length 1.
	var norm float64
	for _, f := range v {
		norm += float64(f) * float64(f)
	}
	if math.Abs(norm-1) > 1e-4 {
		t.Fatalf("vector must be normalized, norm=%f", norm)
	}
	// Voyage gets output_dimension, the key as bearer, the default model.
	if got := (*last)["output_dimension"]; got != float64(Dim) {
		t.Fatalf("output_dimension=%v, expected %d", got, Dim)
	}
	if got := (*last)["model"]; got != "voyage-3.5-lite" {
		t.Fatalf("default model not set: %v", got)
	}
	if got := (*last)["_auth"]; got != "Bearer geheim" {
		t.Fatalf("Authorization header wrong: %v", got)
	}
	if !strings.HasPrefix(e.Name(), "voyage:voyage-3.5-lite:") {
		t.Fatalf("unexpected fingerprint: %s", e.Name())
	}
}

func TestAPIEmbedderOpenAIUsesDimensions(t *testing.T) {
	srv, last := embedServer(t, Dim, http.StatusOK, nil)
	e, err := NewAPIEmbedder("openai", "text-embedding-3-large", "k", srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Embed(context.Background(), "text"); err != nil {
		t.Fatal(err)
	}
	if got := (*last)["dimensions"]; got != float64(Dim) {
		t.Fatalf("openai expects 'dimensions', got %v", got)
	}
	if _, ok := (*last)["output_dimension"]; ok {
		t.Fatal("openai must not send output_dimension")
	}
}

// A vector that is too short is not repairable — padding would be invention.
func TestAPIEmbedderRejectsTooFewDimensions(t *testing.T) {
	srv, _ := embedServer(t, 128, http.StatusOK, nil)
	e, _ := NewAPIEmbedder("voyage", "", "k", srv.URL, nil)
	_, err := e.Embed(context.Background(), "text")
	if err == nil || !strings.Contains(err.Error(), "128") {
		t.Fatalf("expected a dimension error, got %v", err)
	}
}

// If the server delivers more dimensions than the schema holds (because it
// ignores the requested dimension), the vector is truncated to Dim and
// re-normalized — the route intended for Matryoshka models, instead of aborting.
func TestAPIEmbedderTruncatesMatryoshka(t *testing.T) {
	srv, _ := embedServer(t, 768, http.StatusOK, nil)
	e, _ := NewAPIEmbedder("ollama", "", "", srv.URL, nil)
	v, err := e.Embed(context.Background(), "text")
	if err != nil {
		t.Fatalf("768 dimensions must be truncated, got %v", err)
	}
	var norm float64
	for _, f := range v {
		norm += float64(f) * float64(f)
	}
	if math.Abs(norm-1) > 1e-4 {
		t.Fatalf("truncated vector must be re-normalized, norm=%f", norm)
	}
	// Truncated means: the first Dim values, not arbitrary ones. The stub builds
	// i%7+1, so the first value is 1 — after normalization > 0.
	if v[0] <= 0 {
		t.Fatalf("truncation must keep the start of the vector, v[0]=%f", v[0])
	}
}

// The self-hosted route works without a key: no placeholder needed and no
// Authorization header on the wire.
func TestAPIEmbedderOllamaWithoutKey(t *testing.T) {
	srv, last := embedServer(t, Dim, http.StatusOK, nil)
	e, err := NewAPIEmbedder("ollama", "", "", srv.URL, nil)
	if err != nil {
		t.Fatalf("ollama must not demand a key: %v", err)
	}
	if _, err := e.Embed(context.Background(), "text"); err != nil {
		t.Fatal(err)
	}
	if got := (*last)["_auth"]; got != "" {
		t.Fatalf("without a key no Authorization header may be set, got %v", got)
	}
	if got := (*last)["model"]; got != "embeddinggemma" {
		t.Fatalf("default model not set: %v", got)
	}
	if got := (*last)["dimensions"]; got != float64(Dim) {
		t.Fatalf("ollama speaks the OpenAI format, expected dimensions=%d, got %v", Dim, got)
	}
}

// Without an explicit URL, ollama points at the compose service name.
func TestAPIEmbedderOllamaDefaultURL(t *testing.T) {
	e, err := NewAPIEmbedder("ollama", "", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if e.url != "http://embeddings:11434/v1/embeddings" {
		t.Fatalf("unexpected default URL: %s", e.url)
	}
}

// Provider errors are passed through (no silent hash fallback) and retried once.
func TestAPIEmbedderSurfacesProviderError(t *testing.T) {
	var calls int32
	srv, _ := embedServer(t, Dim, http.StatusTooManyRequests, &calls)
	e, _ := NewAPIEmbedder("voyage", "", "k", srv.URL, nil)
	_, err := e.Embed(context.Background(), "text")
	if err == nil || !strings.Contains(err.Error(), "quota") {
		t.Fatalf("provider error must surface, got %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected 1 retry (2 calls), got %d", calls)
	}
}

func TestAPIEmbedderEmptyTextNoCall(t *testing.T) {
	var calls int32
	srv, _ := embedServer(t, Dim, http.StatusOK, &calls)
	e, _ := NewAPIEmbedder("voyage", "", "k", srv.URL, nil)
	v, err := e.Embed(context.Background(), "   ")
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("empty text must not trigger an API call, got %d", calls)
	}
	for _, f := range v {
		if f != 0 {
			t.Fatal("empty text must yield the zero vector")
		}
	}
}

func TestNewAPIEmbedderValidation(t *testing.T) {
	if _, err := NewAPIEmbedder("gibtsnicht", "", "k", "", nil); err == nil {
		t.Fatal("unknown provider must be rejected")
	}
	if _, err := NewAPIEmbedder("voyage", "", "  ", "", nil); err == nil {
		t.Fatal("missing API key must be rejected")
	}
	e, err := NewAPIEmbedder("openai", "", "k", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if e.url != "https://api.openai.com/v1/embeddings" {
		t.Fatalf("default URL not set: %s", e.url)
	}
}
