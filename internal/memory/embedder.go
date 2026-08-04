package memory

import (
	"context"
	"hash/fnv"
	"math"
	"strings"
	"unicode"
)

// HashEmbedder is the built-in embedding: feature hashing over words and
// bigrams into a normalized 256-dim vector. Deterministic, offline, dependency-
// free — but a lexical measure, not a semantic one: it measures word overlap.
// Differently phrased sentences with the same meaning end up near 0, which is
// why search, ingest assignment and consolidation barely engage with it. For a
// wiki that is meant to condense rather than grow, a real embedding belongs in
// front of it (APIEmbedder) — this one is the offline fallback without an API
// key.
type HashEmbedder struct{}

// Name is the embedder's fingerprint. It is stored in wiki_pages.embed_model
// and decides which pages have to be re-embedded: vectors of different models
// are not comparable with each other.
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
