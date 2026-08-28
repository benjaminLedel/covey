package sandbox

import "testing"

func TestValidateImagePattern(t *testing.T) {
	for _, ok := range []string{"*", "postgres:16", "postgres:*", "ghcr.io/acme/*", "registry.local:5000/db@*"} {
		if _, err := ValidateImagePattern(ok); err != nil {
			t.Errorf("%q was refused: %v", ok, err)
		}
	}
	// The one that matters: an unbound star reads like a repository and is a
	// registry. `postgres*` covers `postgres-evil.example.com/backdoor`.
	for _, bad := range []string{"", "   ", "postgres*", "*postgres", "post*gres", "ghcr.io/*/db", "two words"} {
		if _, err := ValidateImagePattern(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

func TestImageAllowed(t *testing.T) {
	list := []string{"postgres:*", "redis:7", "ghcr.io/acme/*"}
	for _, yes := range []string{"postgres:16", "postgres:17-alpine", "redis:7", "ghcr.io/acme/db:1"} {
		if !ImageAllowed(list, yes) {
			t.Errorf("%q should be allowed by %v", yes, list)
		}
	}
	for _, no := range []string{"redis:8", "mysql:8", "ghcr.io/other/db:1", "postgres-evil.example.com/backdoor", ""} {
		if ImageAllowed(list, no) {
			t.Errorf("%q should NOT be allowed by %v", no, list)
		}
	}
	// A reference without a tag is `:latest` to docker, and has to be to the
	// allowlist too — otherwise the shorter spelling is the one that slips
	// through.
	if !ImageAllowed([]string{"postgres:*"}, "postgres") {
		t.Error("`postgres` was not covered by `postgres:*`")
	}
	if ImageAllowed([]string{"postgres:16"}, "postgres") {
		t.Error("`postgres` was read as `postgres:16`")
	}

	// Empty allows nothing — the fail-closed default a fresh installation has.
	if ImageAllowed(nil, "postgres:16") {
		t.Error("an empty allowlist allowed something")
	}
	// And everything is a decision somebody can make.
	if !ImageAllowed([]string{"*"}, "anything:latest") {
		t.Error("`*` did not allow everything")
	}
}

func TestSuggestPattern(t *testing.T) {
	cases := map[string]string{
		"postgres:16":              "postgres:*",
		"postgres":                 "postgres:*",
		"ghcr.io/acme/db:1.2":      "ghcr.io/acme/db:*",
		"ghcr.io/acme/db":          "ghcr.io/acme/db:*",
		"registry.local:5000/db:3": "registry.local:5000/db:*",
		"registry.local:5000/db":   "registry.local:5000/db:*",
		"postgres@sha256:abc":      "postgres@*",
	}
	for in, want := range cases {
		if got := SuggestPattern(in); got != want {
			t.Errorf("SuggestPattern(%q) = %q, want %q", in, got, want)
		}
	}
	// Whatever it suggests has to be a pattern the validation accepts, and it
	// has to actually cover the image it was suggested for. Otherwise the
	// remedy in the error message is one that does not work.
	for in := range cases {
		p, err := ValidateImagePattern(SuggestPattern(in))
		if err != nil {
			t.Errorf("the suggestion for %q is not a valid pattern: %v", in, err)
			continue
		}
		if !ImageAllowed([]string{p}, in) {
			t.Errorf("the suggestion %q does not cover %q", p, in)
		}
	}
}
