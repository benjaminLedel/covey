package waitlist

import (
	"strings"
	"testing"
)

// A code travels through channels that corrupt characters: read out over the
// phone, typed off a mail, thrown in lowercased. Normalisation is therefore no
// comfort, but the reason for the choice of alphabet.
func TestNormalizeVerzeiht(t *testing.T) {
	code, err := NewCode()
	if err != nil {
		t.Fatal(err)
	}
	kanonisch, ok := Normalize(code)
	if !ok {
		t.Fatalf("frisch erzeugter Code gilt nicht: %q", code)
	}

	varianten := []string{
		strings.ToLower(code),                // lowercased
		strings.ReplaceAll(code, "-", ""),    // without hyphens
		strings.ReplaceAll(code, "-", " "),   // with spaces
		" " + code + " ",                     // with padding
		strings.TrimPrefix(code, Prefix+"-"), // without prefix
	}
	for _, v := range varianten {
		got, ok := Normalize(v)
		if !ok || got != kanonisch {
			t.Errorf("Normalize(%q) = %q,%v — erwartet %q", v, got, ok, kanonisch)
		}
	}
}

// Crockford maps the classic confusions: O is a zero, I and L
// are ones. Whoever reads it wrong still gets through.
func TestVerwechslungenWerdenAbgebildet(t *testing.T) {
	kanonisch, ok := Normalize("COVEY-0123456789")
	if !ok {
		t.Skip("Länge passt nicht — Format geändert")
	}
	fuerVerlesen, ok := Normalize("COVEY-O123456789")
	if !ok || fuerVerlesen != kanonisch {
		t.Errorf("O muss zur Null werden: %q vs %q", fuerVerlesen, kanonisch)
	}
	mitI, _ := Normalize("COVEY-0I23456789")
	mitL, _ := Normalize("COVEY-0L23456789")
	if mitI != mitL || mitI != kanonisch {
		t.Errorf("I und L müssen Einsen sein: %q / %q vs %q", mitI, mitL, kanonisch)
	}
}

// What cannot be a code is rejected before the database is asked.
func TestNormalizeLehntAb(t *testing.T) {
	for _, s := range []string{"", "COVEY", "COVEY-KURZ", "COVEY-4K7MQ-P2D9XZZZ", "COVEY-4K7MQ-P2D9U", "COVEY-4K7MQ-P2D9!"} {
		if _, ok := Normalize(s); ok {
			t.Errorf("%q sollte nicht als Code durchgehen", s)
		}
	}
}

// Two codes in a row must not be the same — and the prefix makes
// them recognisable without being part of the secret.
func TestNewCodeFormat(t *testing.T) {
	gesehen := map[string]bool{}
	for i := 0; i < 50; i++ {
		c, err := NewCode()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(c, Prefix+"-") {
			t.Fatalf("kein Präfix: %q", c)
		}
		if gesehen[c] {
			t.Fatalf("Code doppelt gezogen: %q", c)
		}
		gesehen[c] = true
		k, ok := Normalize(c)
		if !ok || len(k) != symbols {
			t.Fatalf("%q normalisiert zu %q", c, k)
		}
		if Format(k) != c {
			t.Fatalf("Format(%q) = %q, erwartet %q", k, Format(k), c)
		}
	}
}

func TestHashIstStabilUndNichtDerCode(t *testing.T) {
	k, _ := Normalize("COVEY-4K7MQ-P2D9X")
	h := Hash(k)
	if h == k || len(h) != 64 {
		t.Fatalf("Hash sieht falsch aus: %q", h)
	}
	if Hash(k) != h {
		t.Fatal("Hash schwankt")
	}
}

func TestMatchesPattern(t *testing.T) {
	faelle := []struct {
		email, pattern string
		want           bool
	}{
		{"erika@firma.de", "@firma.de", true},
		// Deliberately narrow: a subdomain is not the same domain, and a
		// gate should be narrow. Whoever needs sub.firma.de names it.
		{"erika@sub.firma.de", "@firma.de", false},
		{"erika@fremd.de", "@firma.de", false},
		// The "@" anchors the pattern — otherwise boesefirma.de would come through too.
		{"erika@boesefirma.de", "@firma.de", false},
		{"erika@firma.de", "erika@firma.de", true},
		{"ERIKA@Firma.de", " @firma.de ", true},
		{"otto@firma.de", "erika@firma.de", false},
	}
	for _, f := range faelle {
		if got := matchesPattern(f.email, f.pattern); got != f.want {
			t.Errorf("matchesPattern(%q,%q) = %v", f.email, f.pattern, got)
		}
	}
}
