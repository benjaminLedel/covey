package httpapi

import "testing"

// What the HTTPS promise covers is a decision, not a constant.
//
// The header used to read "max-age=31536000; includeSubDomains" on every HTTPS
// instance. That is a promise about a domain that is not ours: an organisation
// running covey at covey.example.com got a guarantee covering every sibling
// name under it, cached in every visitor's browser for a year — and an internal
// tool still on HTTP under one of those names simply disappears (#132).
//
// So the default reaches this host and no further, and going further is
// something an operator says out loud.
func TestHSTSPromisesOnlyWhatWeKnow(t *testing.T) {
	fuer := map[string]string{
		"":           "max-age=31536000",
		"basic":      "max-age=31536000",
		"subdomains": "max-age=31536000; includeSubDomains",
		"off":        "",
		"unsinniges": "max-age=31536000", // an unreadable setting stays safe
	}
	for einstellung, erwartet := range fuer {
		if got := hstsHeader(einstellung); got != erwartet {
			t.Errorf("HSTS %q = %q, expected %q", einstellung, got, erwartet)
		}
	}
	// "off" exists for the installation whose proxy sets the header. nginx
	// appends rather than replaces, and a browser takes the FIRST one
	// (RFC 6797 §8.1) — ours, which is how the binary used to undo what the
	// proxy had deliberately left out.
	if hstsHeader("off") != "" {
		t.Error(`"off" has to mean silence, or the proxy cannot own the header`)
	}
}
