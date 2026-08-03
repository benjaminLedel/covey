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
	return dot // Vektoren sind normalisiert
}

// mustEmbed ruft den Embedder auf und lässt den Test scheitern, wenn er einen
// Fehler liefert — der Hash-Embedder tut das nie, das Interface erlaubt es aber.
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
		t.Fatal("Embedding muss deterministisch sein")
	}
}

func TestHashEmbedderNormalized(t *testing.T) {
	v := mustEmbed(t, HashEmbedder{}, "irgendein text mit mehreren wörtern")
	var norm float64
	for _, f := range v {
		norm += float64(f) * float64(f)
	}
	if math.Abs(norm-1) > 1e-4 {
		t.Fatalf("Norm erwartet 1, got %f", norm)
	}
}

func TestHashEmbedderSimilarityOrdering(t *testing.T) {
	e := HashEmbedder{}
	query := mustEmbed(t, e, "Kunde Meier Login Problem Passwort")
	related := mustEmbed(t, e, "Meier konnte sich nicht einloggen, Passwort zurückgesetzt")
	unrelated := mustEmbed(t, e, "Rechnung Q3 Controlling Export fehlgeschlagen")
	if cosine(query, related) <= cosine(query, unrelated) {
		t.Fatalf("verwandter Text muss ähnlicher sein: related=%f unrelated=%f",
			cosine(query, related), cosine(query, unrelated))
	}
}

func TestHashEmbedderEmpty(t *testing.T) {
	v := mustEmbed(t, HashEmbedder{}, "")
	for _, f := range v {
		if f != 0 {
			t.Fatal("leerer Text muss Nullvektor liefern")
		}
	}
}

func TestVectorLiteral(t *testing.T) {
	var v [Dim]float32
	v[0], v[1] = 0.5, -1
	lit := vectorLiteral(v)
	if !strings.HasPrefix(lit, "[0.5,-1,0,") || !strings.HasSuffix(lit, ",0]") {
		t.Fatalf("unerwartetes Literal: %.40s…%s", lit, lit[len(lit)-4:])
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
		t.Fatalf("leere Treffer → leerer Block, got %q", got)
	}
	// Der Wiki-Block rendert je Treffer eine Seite: Titel + [[slug]] als
	// Überschrift, darunter den Body (Zeilenumbrüche bleiben — es ist Markdown),
	// optional die verwandten Seiten.
	got := FormatForPrompt([]Entry{{
		Title:   "Kunde Meier",
		Slug:    "kunde-meier",
		Content: "Zeile1\nZeile2",
		Links:   []string{"firefox"},
	}})
	for _, want := range []string{"### Kunde Meier [[kunde-meier]]", "Zeile1\nZeile2", "Verwandt: [[firefox]]"} {
		if !strings.Contains(got, want) {
			t.Fatalf("Wiki-Block enthält %q nicht: %q", want, got)
		}
	}
}
