package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"
)

// APIEmbedder ist das echte, semantische Embedding: ein HTTP-Aufruf gegen einen
// Embedding-Provider. Voyage und OpenAI teilen sich Request- und Response-Form
// (Bearer-Auth, {model, input} → {data:[{embedding}]}) und unterscheiden sich
// nur im Namen des Dimensions-Feldes — deshalb trägt eine Implementierung beide.
//
// Die Dimension wird beim Provider auf Dim (256) bestellt: beide Modellfamilien
// beherrschen verkürzte Ausgaben (Matryoshka), damit bleibt das Schema
// vector(256) aus Migration 0002/0031 unverändert.
//
// Es gibt bewusst KEINEN stillen Rückfall auf HashEmbedder: Vektoren zweier
// Modelle sind untereinander nicht vergleichbar, ein gemischter Index wäre
// schlechter als ein durchgehend schlechter. Fehler kommen als Fehler zurück.
type APIEmbedder struct {
	provider string // "voyage" | "openai"
	model    string
	key      string
	url      string
	hc       *http.Client
}

// Provider-Defaults: Modelle, die eine 256-dim Ausgabe können.
var embeddingDefaults = map[string]struct{ url, model string }{
	"voyage": {"https://api.voyageai.com/v1/embeddings", "voyage-3.5-lite"},
	"openai": {"https://api.openai.com/v1/embeddings", "text-embedding-3-small"},
}

// SupportedProviders sind die Werte, die COVEY_EMBEDDING_PROVIDER annehmen darf.
func SupportedProviders() []string { return []string{"builtin", "voyage", "openai"} }

// NewAPIEmbedder baut den Embedder für einen Provider. Leere Werte für model und
// url fallen auf die Provider-Defaults zurück.
func NewAPIEmbedder(provider, model, key, url string) (*APIEmbedder, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	def, ok := embeddingDefaults[provider]
	if !ok {
		return nil, fmt.Errorf("unbekannter embedding-provider %q (erlaubt: %s)",
			provider, strings.Join(SupportedProviders(), ", "))
	}
	if strings.TrimSpace(key) == "" {
		return nil, fmt.Errorf("embedding-provider %q braucht COVEY_EMBEDDING_API_KEY", provider)
	}
	if strings.TrimSpace(model) == "" {
		model = def.model
	}
	if strings.TrimSpace(url) == "" {
		url = def.url
	}
	return &APIEmbedder{
		provider: provider,
		model:    model,
		key:      strings.TrimSpace(key),
		url:      url,
		hc:       &http.Client{Timeout: 20 * time.Second},
	}, nil
}

func (e *APIEmbedder) Name() string {
	return fmt.Sprintf("%s:%s:%d", e.provider, e.model, Dim)
}

// maxEmbedChars deckelt den Eingabetext. Eine Wiki-Seite ist selten länger;
// gekappt wird rune-sicher, damit kein Umlaut zerschnitten wird.
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
		return out, nil // Nullvektor, wie beim Hash-Embedder
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

	// Ein Wiederholungsversuch: Embedding-Endpunkte antworten gelegentlich mit
	// 429/5xx, und ein verlorenes Embedding hieße eine Seite ohne Retrieval.
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
	req.Header.Set("Authorization", "Bearer "+e.key)

	resp, err := e.hc.Do(req)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()

	var parsed embedResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil && resp.StatusCode == http.StatusOK {
		return out, fmt.Errorf("embedding-antwort unlesbar: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		msg := resp.Status
		if parsed.Error != nil && parsed.Error.Message != "" {
			msg = parsed.Error.Message
		}
		return out, fmt.Errorf("embedding-provider %s: %s", e.provider, msg)
	}
	if len(parsed.Data) == 0 || len(parsed.Data[0].Embedding) == 0 {
		return out, fmt.Errorf("embedding-provider %s lieferte keinen Vektor", e.provider)
	}
	got := parsed.Data[0].Embedding
	if len(got) != Dim {
		// Nicht auffüllen oder abschneiden: ein Vektor falscher Länge passt weder
		// ins Schema noch zu den übrigen Seiten.
		return out, fmt.Errorf("embedding-provider %s lieferte %d Dimensionen, erwartet %d — Modell %q kann die Ausgabelänge nicht",
			e.provider, len(got), Dim, e.model)
	}
	copy(out[:], got)
	return normalize(out), nil
}

// normalize bringt den Vektor auf Länge 1 — die Ähnlichkeitsschwellen und das
// Skalarprodukt in den Tests setzen normalisierte Vektoren voraus.
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
