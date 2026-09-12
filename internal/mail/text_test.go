package mail

import (
	"strings"
	"testing"
)

// The catalogues are the interface's own (web/src/locales/*.json), embedded
// through package web. A key that exists there has to come out here; the point
// of the detour is that the mail and the screen never say two different things.
func TestTextReadsTheInterfaceCatalogue(t *testing.T) {
	de := Text("de", "mails.verify.subject", nil)
	if de == "" || de == "mails.verify.subject" {
		t.Fatalf("German catalogue does not answer for a known key: %q", de)
	}
	en := Text("en", "mails.verify.subject", nil)
	if en == "" || en == "mails.verify.subject" {
		t.Fatalf("English catalogue does not answer for a known key: %q", en)
	}
}

func TestTextFallsBackToEnglishForAnUnknownLanguage(t *testing.T) {
	want := Text(BaseLang, "mails.verify.subject", nil)
	if got := Text("kl", "mails.verify.subject", nil); got != want {
		t.Fatalf("unknown language gave %q, expected the English %q", got, want)
	}
}

// A visible key is the intended answer for a key nothing carries: it says
// "this is a bug" louder than an empty line does.
func TestTextReturnsTheKeyWhenNobodyCarriesIt(t *testing.T) {
	const key = "mails.this.key.does.not.exist"
	if got := Text("de", key, nil); got != key {
		t.Fatalf("unknown key gave %q, expected the key itself", got)
	}
}

func TestTextSubstitutesPlaceholders(t *testing.T) {
	// i18next syntax, so the same string serves the interface and a mail.
	raw := Text("en", "mails.verify.body", nil)
	if !strings.Contains(raw, "{{") {
		t.Skip("this catalogue entry carries no placeholder any more")
	}
	name := strings.TrimSuffix(strings.SplitN(raw, "{{", 2)[1], "}}")
	name = strings.SplitN(name, "}}", 2)[0]
	got := Text("en", "mails.verify.body", map[string]string{name: "XYZZY"})
	if strings.Contains(got, "{{"+name+"}}") {
		t.Fatalf("placeholder %q was not substituted: %q", name, got)
	}
	if !strings.Contains(got, "XYZZY") {
		t.Fatalf("value did not reach the text: %q", got)
	}
}

func TestLangNormalises(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"de", "de"},
		{" DE ", "de"},
		{"de-DE", "de"},
		{"de_AT", "de"},
		{"DE-ch", "de"},
		{"", BaseLang},
		{"kl", BaseLang},
		{"-de", BaseLang}, // no language before the separator
	} {
		if got := Lang(tc.in); got != tc.want {
			t.Errorf("Lang(%q) = %q, expected %q", tc.in, got, tc.want)
		}
	}
}

// Ten catalogues ship with the binary (web/src/locales/parity.test.ts keeps
// their keys in step). If one of them stopped being embedded, mails in that
// language would quietly turn English — and nothing else would say so.
func TestEveryCatalogueAnswers(t *testing.T) {
	for _, lang := range []string{"de", "en", "es", "fr", "it", "nl", "pl", "pt", "ja", "zh"} {
		if got := Lang(lang); got != lang {
			t.Errorf("catalogue %q is not embedded (Lang gave %q)", lang, got)
		}
	}
}

func TestLookupWalksADottedKey(t *testing.T) {
	cat := map[string]any{"a": map[string]any{"b": "tief"}, "n": 1.0}
	if got, ok := lookup(cat, "a.b"); !ok || got != "tief" {
		t.Errorf("lookup(a.b) = %q, %v", got, ok)
	}
	for _, key := range []string{"a", "a.c", "a.b.c", "x.y", "n"} {
		if _, ok := lookup(cat, key); ok {
			t.Errorf("lookup(%q) unexpectedly found something", key)
		}
	}
}
