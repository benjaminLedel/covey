package sandbox

import (
	"fmt"
	"strings"
)

// Which images may run beside a sandbox, and who decides.
//
// A service is an image reference, and an image reference is the decision
// which foreign code runs on the runner host. Inside the sandbox that decision
// is already the agent's — it runs shell commands there, under a user without
// root and behind the egress allowlist. A service container is not inside it:
// it is a second container beside it, from an image nobody looked at, with the
// host's memory and no accounting.
//
// So the line is not "may an agent name an image" but "which images may run
// here at all". The allowlist is that answer, per organisation, and it holds
// for every path — the declaration a manager types as much as the one an agent
// derives from a project's compose file. Whoever may EXTEND the list is the
// privileged party; naming an image is not privileged once the list stands.
//
// Fail-closed: an empty list allows nothing. That is the correct default for a
// fresh installation, and the upgrade seeds what agents already declare so no
// running instance loses a service to it.

// A pattern is either an exact reference (`postgres:16`) or one ending in a
// star that is BOUND to a separator:
//
//	postgres:*            every tag of the official postgres
//	ghcr.io/acme/*        everything from that namespace
//	registry.local/db@*   pinned by digest, any digest
//	*                     everything — an explicit decision, not a default
//
// The binding is the whole safety property. A free-floating `postgres*` would
// match `postgres-evil.example.com/backdoor` — same prefix, different registry,
// and nobody reading the list would see it. So the star may only follow `/`,
// `:` or `@`, or stand alone.
const (
	patternEverything = "*"
	starSeparators    = "/:@"
)

// ValidateImagePattern checks a pattern and returns it normalised.
func ValidateImagePattern(pattern string) (string, error) {
	p := strings.TrimSpace(pattern)
	if p == "" {
		return "", fmt.Errorf("an empty pattern allows nothing and says nothing — remove it instead")
	}
	if strings.ContainsAny(p, " \t\n") {
		return "", fmt.Errorf("%q is not an image reference", pattern)
	}
	if p == patternEverything {
		return p, nil
	}
	if i := strings.Index(p, "*"); i >= 0 {
		if i != len(p)-1 {
			return "", fmt.Errorf("%q: the star may only stand at the end — a pattern with something after it is read by nobody the way it is meant", pattern)
		}
		if i == 0 || !strings.ContainsRune(starSeparators, rune(p[i-1])) {
			return "", fmt.Errorf("%q: the star has to follow `/`, `:` or `@` — `postgres*` would also match `postgres-evil.example.com/backdoor`, and nobody reading the list would see it", pattern)
		}
	}
	return p, nil
}

// ImageAllowed reports whether the reference is covered by one of the patterns.
func ImageAllowed(patterns []string, image string) bool {
	image = normalizeRef(image)
	if image == "" {
		return false
	}
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		switch {
		case p == patternEverything:
			return true
		case strings.HasSuffix(p, "*"):
			if strings.HasPrefix(image, strings.TrimSuffix(p, "*")) {
				return true
			}
		case p == image:
			return true
		}
	}
	return false
}

// normalizeRef fills in what docker fills in: a reference without a tag and
// without a digest means `:latest`.
//
// It matters because the patterns are compared by prefix. `postgres:*` covers
// `postgres:16` and would NOT cover the bare `postgres` — although docker
// starts the same class of image for both. A list that allows an image under
// one spelling and refuses it under another is a list nobody can reason about,
// and the spelling that slips through is the shorter one.
func normalizeRef(image string) string {
	image = strings.TrimSpace(image)
	if image == "" {
		return ""
	}
	// After the last slash, because a registry may carry a port
	// (`registry.local:5000/db`) whose colon is not a tag separator.
	last := image
	if slash := strings.LastIndex(image, "/"); slash >= 0 {
		last = image[slash+1:]
	}
	if strings.ContainsAny(last, ":@") {
		return image
	}
	return image + ":latest"
}

// SuggestPattern is the pattern that WOULD allow this image — the remedy that
// travels with the refusal.
//
// A check whose message only says no is furniture: whoever reads it has to
// work out the syntax from the documentation before they can act, and the
// syntax is exactly the part they are most likely to get subtly wrong. So the
// refusal carries the line to add.
//
// The suggestion is the repository with every tag, not the exact reference:
// `postgres:16` today is `postgres:17` in three months, and a list of pinned
// tags is one somebody stops maintaining. Where a reference is pinned by
// digest the repository is the honest suggestion too — the digest is the part
// that changes.
func SuggestPattern(image string) string {
	image = strings.TrimSpace(image)
	if image == "" {
		return ""
	}
	if i := strings.IndexAny(image, "@"); i > 0 {
		return image[:i] + "@*"
	}
	// The tag separator is the last colon AFTER the last slash — a registry
	// with a port (`registry.local:5000/db`) carries a colon that is not one.
	if slash := strings.LastIndex(image, "/"); slash >= 0 {
		if i := strings.LastIndex(image[slash:], ":"); i > 0 {
			return image[:slash+i] + ":*"
		}
		return image + ":*"
	}
	if i := strings.LastIndex(image, ":"); i > 0 {
		return image[:i] + ":*"
	}
	return image + ":*"
}
