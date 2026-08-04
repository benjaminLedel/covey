package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"
)

// APIEmbedder is the real, semantic embedding: an HTTP call against an
// embedding provider. Voyage and OpenAI share the request and response shape
// (bearer auth, {model, input} → {data:[{embedding}]}) and differ only in the
// name of the dimensions field — which is why one implementation carries both.
//
// The dimension is ordered from the provider as Dim (256): both model families
// support shortened outputs (Matryoshka), so the vector(256) schema from
// migration 0002/0031 stays unchanged.
//
// There is deliberately NO silent fallback to HashEmbedder: vectors of two
// models are not comparable with each other, and a mixed index would be worse
// than a consistently bad one. Errors come back as errors.
type APIEmbedder struct {
	provider string // "ollama" | "voyage" | "openai"
	model    string
	key      string
	url      string
	log      *slog.Logger
	hc       *http.Client
	// truncWarn: the Matryoshka truncation is reported once per process, not on
	// every page.
	truncWarn sync.Once
}

// Provider defaults. "ollama" is the self-hosted route: a small model on your
// own host instead of a third-party service — the wiki contents do not leave the
// house. The default model is EmbeddingGemma (308M, multilingual,
// Matryoshka-trained and therefore officially truncatable to 256 dimensions),
// the default host is the compose service name.
var embeddingDefaults = map[string]struct {
	url, model string
	needsKey   bool
}{
	"voyage": {"https://api.voyageai.com/v1/embeddings", "voyage-3.5-lite", true},
	"openai": {"https://api.openai.com/v1/embeddings", "text-embedding-3-small", true},
	"ollama": {"http://embeddings:11434/v1/embeddings", "embeddinggemma", false},
}

// SupportedProviders are the values COVEY_EMBEDDING_PROVIDER may take.
func SupportedProviders() []string { return []string{"builtin", "ollama", "voyage", "openai"} }

// NewAPIEmbedder builds the embedder for a provider. Empty values for model and
// url fall back to the provider defaults; log may be nil.
func NewAPIEmbedder(provider, model, key, url string, log *slog.Logger) (*APIEmbedder, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	def, ok := embeddingDefaults[provider]
	if !ok {
		return nil, fmt.Errorf("unknown embedding provider %q (allowed: %s)",
			provider, strings.Join(SupportedProviders(), ", "))
	}
	// Only third-party services need a key — a local server does not want one,
	// and demanding a placeholder would be nonsense.
	if def.needsKey && strings.TrimSpace(key) == "" {
		return nil, fmt.Errorf("embedding provider %q needs COVEY_EMBEDDING_API_KEY", provider)
	}
	if strings.TrimSpace(model) == "" {
		model = def.model
	}
	if strings.TrimSpace(url) == "" {
		url = def.url
	}
	if log == nil {
		log = slog.Default()
	}
	return &APIEmbedder{
		provider: provider,
		model:    model,
		key:      strings.TrimSpace(key),
		url:      url,
		log:      log,
		hc:       &http.Client{Timeout: 60 * time.Second},
	}, nil
}

func (e *APIEmbedder) Name() string {
	return fmt.Sprintf("%s:%s:%d", e.provider, e.model, Dim)
}

// maxEmbedChars caps the input text. A wiki page is rarely longer; the cut is
// rune-safe so no multi-byte character gets sliced in half.
const maxEmbedChars = 8000

type embedResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (e *APIEmbedder) Embed(ctx context.Context, text string) ([Dim]float32, error) {
	var out [Dim]float32
	text = strings.TrimSpace(text)
	if text == "" {
		return out, nil // zero vector, same as the hash embedder
	}
	text = truncRunes(text, maxEmbedChars)

	body := map[string]any{"model": e.model, "input": []string{text}}
	if e.provider == "voyage" {
		body["output_dimension"] = Dim
		body["input_type"] = "document"
	} else {
		body["dimensions"] = Dim
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return out, err
	}

	// One retry: embedding endpoints occasionally answer with 429/5xx, and a
	// lost embedding would mean a page without retrieval.
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return out, ctx.Err()
			case <-time.After(time.Second):
			}
		}
		vec, err := e.call(ctx, payload)
		if err == nil {
			return vec, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return out, err
		}
	}
	return out, lastErr
}

func (e *APIEmbedder) call(ctx context.Context, payload []byte) ([Dim]float32, error) {
	var out [Dim]float32
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.url, bytes.NewReader(payload))
	if err != nil {
		return out, err
	}
	req.Header.Set("Content-Type", "application/json")
	if e.key != "" {
		req.Header.Set("Authorization", "Bearer "+e.key)
	}

	resp, err := e.hc.Do(req)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()

	var parsed embedResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil && resp.StatusCode == http.StatusOK {
		return out, fmt.Errorf("embedding response unreadable: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		msg := resp.Status
		if parsed.Error != nil && parsed.Error.Message != "" {
			msg = parsed.Error.Message
		}
		return out, fmt.Errorf("embedding provider %s: %s", e.provider, msg)
	}
	if len(parsed.Data) == 0 || len(parsed.Data[0].Embedding) == 0 {
		return out, fmt.Errorf("embedding provider %s returned no vector", e.provider)
	}
	got := parsed.Data[0].Embedding
	switch {
	case len(got) == Dim:
	case len(got) > Dim:
		// Matryoshka: models like EmbeddingGemma are trained so that you can cut
		// the vector off at the front and re-normalize it — that is exactly the
		// intended route to shorter outputs. It becomes necessary when the
		// server ignores the requested dimension (llama.cpp, for instance).
		// Report it once so it does not happen silently.
		full := len(got)
		e.truncWarn.Do(func() {
			e.log.Info("wiki embedding: vector is truncated to the schema width (Matryoshka)",
				"model", e.model, "returned", full, "used", Dim)
		})
		got = got[:Dim]
	default:
		// Padding would be invention: too short is not repairable.
		return out, fmt.Errorf("embedding provider %s returned only %d dimensions, %d are needed — model %q is too small",
			e.provider, len(got), Dim, e.model)
	}
	copy(out[:], got)
	return normalize(out), nil
}

// normalize brings the vector to length 1 — the similarity thresholds and the
// dot product in the tests assume normalized vectors.
func normalize(v [Dim]float32) [Dim]float32 {
	var norm float64
	for _, f := range v {
		norm += float64(f) * float64(f)
	}
	if norm == 0 {
		return v
	}
	inv := float32(1 / math.Sqrt(norm))
	for i := range v {
		v[i] *= inv
	}
	return v
}
