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

// embedServer ist ein Provider-Double: es merkt sich den letzten Request-Body
// und antwortet mit einem Vektor der gewünschten Länge.
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
				"error": map[string]string{"message": "kein Kontingent mehr"},
			})
			return
		}
		vec := make([]float32, dims)
		for i := range vec {
			vec[i] = float32(i%7) + 1 // unnormalisiert, damit normalize() prüfbar wird
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
	e, err := NewAPIEmbedder("voyage", "", "geheim", srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	v, err := e.Embed(context.Background(), "Kunde Meier hat ein Login-Problem")
	if err != nil {
		t.Fatal(err)
	}
	// Der Vektor kommt normalisiert zurück — die Schwellen rechnen mit Länge 1.
	var norm float64
	for _, f := range v {
		norm += float64(f) * float64(f)
	}
	if math.Abs(norm-1) > 1e-4 {
		t.Fatalf("Vektor muss normalisiert sein, Norm=%f", norm)
	}
	// Voyage bekommt output_dimension, den Schlüssel als Bearer, das Default-Modell.
	if got := (*last)["output_dimension"]; got != float64(Dim) {
		t.Fatalf("output_dimension=%v, erwartet %d", got, Dim)
	}
	if got := (*last)["model"]; got != "voyage-3.5-lite" {
		t.Fatalf("Default-Modell nicht gesetzt: %v", got)
	}
	if got := (*last)["_auth"]; got != "Bearer geheim" {
		t.Fatalf("Authorization-Header falsch: %v", got)
	}
	if !strings.HasPrefix(e.Name(), "voyage:voyage-3.5-lite:") {
		t.Fatalf("Fingerabdruck unerwartet: %s", e.Name())
	}
}

func TestAPIEmbedderOpenAIUsesDimensions(t *testing.T) {
	srv, last := embedServer(t, Dim, http.StatusOK, nil)
	e, err := NewAPIEmbedder("openai", "text-embedding-3-large", "k", srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Embed(context.Background(), "text"); err != nil {
		t.Fatal(err)
	}
	if got := (*last)["dimensions"]; got != float64(Dim) {
		t.Fatalf("openai erwartet 'dimensions', got %v", got)
	}
	if _, ok := (*last)["output_dimension"]; ok {
		t.Fatal("openai darf kein output_dimension senden")
	}
}

// Ein Vektor falscher Länge darf nicht aufgefüllt oder abgeschnitten werden —
// er passt weder ins Schema noch zu den übrigen Seiten.
func TestAPIEmbedderRejectsWrongDimension(t *testing.T) {
	srv, _ := embedServer(t, 128, http.StatusOK, nil)
	e, _ := NewAPIEmbedder("voyage", "", "k", srv.URL)
	_, err := e.Embed(context.Background(), "text")
	if err == nil || !strings.Contains(err.Error(), "128") {
		t.Fatalf("erwartet Dimensionsfehler, got %v", err)
	}
}

// Fehler des Providers werden weitergereicht (kein stiller Hash-Rückfall) und
// einmal wiederholt.
func TestAPIEmbedderSurfacesProviderError(t *testing.T) {
	var calls int32
	srv, _ := embedServer(t, Dim, http.StatusTooManyRequests, &calls)
	e, _ := NewAPIEmbedder("voyage", "", "k", srv.URL)
	_, err := e.Embed(context.Background(), "text")
	if err == nil || !strings.Contains(err.Error(), "Kontingent") {
		t.Fatalf("Provider-Fehler muss durchschlagen, got %v", err)
	}
	if calls != 2 {
		t.Fatalf("erwartet 1 Wiederholung (2 Aufrufe), got %d", calls)
	}
}

func TestAPIEmbedderEmptyTextNoCall(t *testing.T) {
	var calls int32
	srv, _ := embedServer(t, Dim, http.StatusOK, &calls)
	e, _ := NewAPIEmbedder("voyage", "", "k", srv.URL)
	v, err := e.Embed(context.Background(), "   ")
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("leerer Text darf keinen API-Aufruf auslösen, got %d", calls)
	}
	for _, f := range v {
		if f != 0 {
			t.Fatal("leerer Text muss Nullvektor liefern")
		}
	}
}

func TestNewAPIEmbedderValidation(t *testing.T) {
	if _, err := NewAPIEmbedder("gibtsnicht", "", "k", ""); err == nil {
		t.Fatal("unbekannter Provider muss abgelehnt werden")
	}
	if _, err := NewAPIEmbedder("voyage", "", "  ", ""); err == nil {
		t.Fatal("fehlender API-Schlüssel muss abgelehnt werden")
	}
	e, err := NewAPIEmbedder("openai", "", "k", "")
	if err != nil {
		t.Fatal(err)
	}
	if e.url != "https://api.openai.com/v1/embeddings" {
		t.Fatalf("Default-URL nicht gesetzt: %s", e.url)
	}
}
