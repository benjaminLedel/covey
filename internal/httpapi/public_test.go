package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Whether an installation accepts registrations is the one question the
// public website asks without a session — and the only right answer
// when in doubt is "no". Without a settings store (tests, older
// wiring) that must not turn into an open form.
func TestSignupStateOhneStoreIstGeschlossen(t *testing.T) {
	s := &Server{} // Settings == nil
	rec := httptest.NewRecorder()
	s.handleSignupState(rec, httptest.NewRequest(http.MethodGet, "/api/v1/public/signup-state", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("Status %d, erwartet 200", rec.Code)
	}
	var got signupState
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("Antwort nicht lesbar: %v", err)
	}
	if got.Mode != "off" {
		t.Errorf("mode=%q, erwartet \"off\" — fail-closed", got.Mode)
	}
	// The name stands on the registration page and later in the mails; empty
	// would be a hole in the sentence there.
	if got.SiteName == "" {
		t.Error("site_name ist leer")
	}
	// Whoever closes registration wants it closed now and
	// not once a proxy's TTL has run out.
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control=%q, erwartet no-store", cc)
	}
}

// A display name reaches a mail that can be sent to any address, and in that
// mail an address becomes a link and {{…}} a substitution (#552). What passes
// is one line of plain text; everything else greets the recipient instead.
func TestGreetingKeepsOnlyAPlainName(t *testing.T) {
	const to = "ada@example.org"
	cases := map[string]string{
		"Ada Lovelace":                  "Ada Lovelace",
		"  Ada \n Lovelace ":            "Ada Lovelace",
		"Dr. Ada Lovelace":              "Dr. Ada Lovelace",
		"":                              to,
		"Ada https://example.org/login": to,
		"Ada\n\nhttp://example.org":     to,
		"www.example.org":               to,
		"see example.org/login":         to,
		"Ada {{link}}":                  to,
		strings.Repeat("a", 65):         to,
	}
	for in, want := range cases {
		if got := greeting(in, to); got != want {
			t.Errorf("greeting(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTransactionalMailCarriesNoAddressFromTheName(t *testing.T) {
	m := transactional("en", "verify", "covey", "Ada\n\nhttps://example.org/x\n\n",
		"https://covey.example/verify?token=t", "ada@example.org")
	for _, part := range []string{m.Body, m.HTML} {
		if strings.Contains(part, "example.org/x") {
			t.Fatalf("the name's address reached the mail:\n%s", part)
		}
	}
	if !strings.Contains(m.Body, "Hello ada@example.org,") {
		t.Fatalf("expected the recipient's address as greeting:\n%s", m.Body)
	}
}
