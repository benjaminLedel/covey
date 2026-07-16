package memory

import (
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

func TestHashEmbedderDeterministic(t *testing.T) {
	e := HashEmbedder{}
	a := e.Embed("Kunde Meier hat ein Login-Problem")
	b := e.Embed("Kunde Meier hat ein Login-Problem")
	if a != b {
		t.Fatal("Embedding muss deterministisch sein")
	}
}

func TestHashEmbedderNormalized(t *testing.T) {
	v := HashEmbedder{}.Embed("irgendein text mit mehreren wörtern")
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
	query := e.Embed("Kunde Meier Login Problem Passwort")
	related := e.Embed("Meier konnte sich nicht einloggen, Passwort zurückgesetzt")
	unrelated := e.Embed("Rechnung Q3 Controlling Export fehlgeschlagen")
	if cosine(query, related) <= cosine(query, unrelated) {
		t.Fatalf("verwandter Text muss ähnlicher sein: related=%f unrelated=%f",
			cosine(query, related), cosine(query, unrelated))
	}
}

func TestHashEmbedderEmpty(t *testing.T) {
	v := HashEmbedder{}.Embed("")
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
	got := FormatForPrompt([]Entry{{Content: "Zeile1\nZeile2"}})
	if !strings.Contains(got, "- Zeile1 Zeile2") {
		t.Fatalf("Zeilenumbrüche müssen geglättet werden: %q", got)
	}
}
