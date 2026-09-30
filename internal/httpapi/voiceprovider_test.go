package httpapi

import "testing"

// The voice provider's address (#497): http(s) only, no credentials or
// query in it, and the /v1 the requests add themselves taken off.
func TestNormalizeVoiceProviderURL(t *testing.T) {
	for in, want := range map[string]string{
		"":                               "",
		" https://speech.example.org/ ":  "https://speech.example.org",
		"http://192.168.0.10:8880/v1":    "http://192.168.0.10:8880",
		"https://example.org/tts/v1/":    "https://example.org/tts",
		"https://example.org/speech-api": "https://example.org/speech-api",
	} {
		got, err := normalizeVoiceProviderURL(in)
		if err != nil || got != want {
			t.Errorf("%q = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"ftp://example.org", "example.org", "https://user:pw@example.org", "https://example.org/?key=x", "https://"} {
		if _, err := normalizeVoiceProviderURL(in); err == nil {
			t.Errorf("%q accepted", in)
		}
	}
}

// The speed a provider honours: 0 is left out, the rest held to 0.7–1.3.
func TestClampSpeed(t *testing.T) {
	for in, want := range map[float64]float64{0.2: 0.7, 0.7: 0.7, 1: 1, 1.25: 1.25, 2: 1.3} {
		if got, ok := clampSpeed(in); !ok || got != want {
			t.Errorf("%v = %v %v, want %v", in, got, ok, want)
		}
	}
	if _, ok := clampSpeed(0); ok {
		t.Error("0 must be left out")
	}
}
