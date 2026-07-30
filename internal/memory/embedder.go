package memory

import (
	"context"
	"hash/fnv"
	"math"
	"strings"
	"unicode"
)

// HashEmbedder ist das Built-in-Embedding: Wort- und Bigramm-Feature-Hashing
// in einen normalisierten 256-dim Vektor. Deterministisch, offline, dependency-
// frei — aber ein Lexikon-Maß, kein semantisches: es misst Wortüberlappung.
// Verschieden formulierte Sätze mit gleicher Bedeutung landen nahe 0, weshalb
// Suche, Ingest-Zuordnung und Konsolidierung damit kaum greifen. Für ein Wiki,
// das verdichten statt anwachsen soll, gehört ein echtes Embedding davor
// (APIEmbedder) — dieses hier ist der Offline-Rückfall ohne API-Schlüssel.
type HashEmbedder struct{}

// Name ist der Fingerabdruck des Embedders. Er steht in wiki_pages.embed_model
// und entscheidet, welche Seiten neu eingebettet werden müssen: Vektoren
// verschiedener Modelle sind untereinander nicht vergleichbar.
func (HashEmbedder) Name() string { return "builtin-hash:256" }

func tokenize(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
}

func (h HashEmbedder) Embed(_ context.Context, text string) ([Dim]float32, error) {
	return h.embed(text), nil
}

func (HashEmbedder) embed(text string) [Dim]float32 {
	var v [Dim]float32
	tokens := tokenize(text)
	add := func(feature string, weight float32) {
		h := fnv.New32a()
		h.Write([]byte(feature))
		sum := h.Sum32()
		idx := sum % Dim
		sign := float32(1)
		if sum&(1<<31) != 0 {
			sign = -1
		}
		v[idx] += sign * weight
	}
	for i, tok := range tokens {
		add(tok, 1)
		if i > 0 {
			add(tokens[i-1]+"_"+tok, 0.5)
		}
	}
	var norm float64
	for _, f := range v {
		norm += float64(f) * float64(f)
	}
	if norm > 0 {
		inv := float32(1 / math.Sqrt(norm))
		for i := range v {
			v[i] *= inv
		}
	}
	return v
}
