package memory

import (
	"context"
	"math"
	"strings"
	"testing"
)

func cosine(a, b [Dim]float32) float64 {
	var dot float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
	}
	return dot // vectors are normalized
}

// mustEmbed calls the embedder and fails the test if it returns an error — the
// hash embedder never does, but the interface allows it.
func mustEmbed(t *testing.T, e Embedder, text string) [Dim]float32 {
	t.Helper()
	v, err := e.Embed(context.Background(), text)
	if err != nil {
		t.Fatalf("Embed(%q): %v", text, err)
	}
	return v
}

func TestHashEmbedderDeterministic(t *testing.T) {
	e := HashEmbedder{}
	a := mustEmbed(t, e, "Kunde Meier hat ein Login-Problem")
	b := mustEmbed(t, e, "Kunde Meier hat ein Login-Problem")
	if a != b {
		t.Fatal("embedding must be deterministic")
	}
}

func TestHashEmbedderNormalized(t *testing.T) {
	v := mustEmbed(t, HashEmbedder{}, "irgendein text mit mehreren wörtern")
	var norm float64
	for _, f := range v {
		norm += float64(f) * float64(f)
	}
	if math.Abs(norm-1) > 1e-4 {
		t.Fatalf("norm expected 1, got %f", norm)
	}
}

func TestHashEmbedderSimilarityOrdering(t *testing.T) {
	e := HashEmbedder{}
	query := mustEmbed(t, e, "Kunde Meier Login Problem Passwort")
	related := mustEmbed(t, e, "Meier konnte sich nicht einloggen, Passwort zurückgesetzt")
	unrelated := mustEmbed(t, e, "Rechnung Q3 Controlling Export fehlgeschlagen")
	if cosine(query, related) <= cosine(query, unrelated) {
		t.Fatalf("related text must be more similar: related=%f unrelated=%f",
			cosine(query, related), cosine(query, unrelated))
	}
}

func TestHashEmbedderEmpty(t *testing.T) {
	v := mustEmbed(t, HashEmbedder{}, "")
	for _, f := range v {
		if f != 0 {
			t.Fatal("empty text must yield the zero vector")
		}
	}
}

func TestVectorLiteral(t *testing.T) {
	var v [Dim]float32
	v[0], v[1] = 0.5, -1
	lit := vectorLiteral(v)
	if !strings.HasPrefix(lit, "[0.5,-1,0,") || !strings.HasSuffix(lit, ",0]") {
		t.Fatalf("unexpected literal: %.40s…%s", lit, lit[len(lit)-4:])
	}
}

func TestIsNoise(t *testing.T) {
	noise := []string{
		"Keine neuen Erkenntnisse",
		"keine neuen Erkenntnisse.",
		"  KEINE NEUEN ERKENNTNISSE  ",
		"Nichts Neues gelernt",
		"n/a", "N/A", "-", "–", "", "none", "nothing new",
	}
	for _, s := range noise {
		if !IsNoise(s) {
			t.Errorf("IsNoise(%q) = false, want true", s)
		}
	}
	substance := []string{
		"Kunde Meier nutzt Firefox, Login-Probleme lagen am Passwort-Reset",
		"Keine neuen Erkenntnisse, aber Kunde X reagiert nur auf Anrufe",
		"Ticket 42: Lösung war Cache leeren",
	}
	for _, s := range substance {
		if IsNoise(s) {
			t.Errorf("IsNoise(%q) = true, want false", s)
		}
	}
}

func TestFormatForPrompt(t *testing.T) {
	if got := FormatForPrompt(nil); got != "" {
		t.Fatalf("no hits → empty block, got %q", got)
	}
	// The wiki block renders one page per hit: title + [[slug]] as the heading,
	// the body below it (line breaks are kept — it is Markdown), optionally the
	// related pages.
	got := FormatForPrompt([]Entry{{
		Title:   "Kunde Meier",
		Slug:    "kunde-meier",
		Content: "Zeile1\nZeile2",
		Links:   []string{"firefox"},
	}})
	for _, want := range []string{"### Kunde Meier [[kunde-meier]]", "Zeile1\nZeile2", "Related: [[firefox]]"} {
		if !strings.Contains(got, want) {
			t.Fatalf("wiki block does not contain %q: %q", want, got)
		}
	}
}
