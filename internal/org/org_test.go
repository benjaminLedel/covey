package org

import "testing"

func TestNormalizeIdentities(t *testing.T) {
	got := NormalizeIdentities(map[string]string{
		" GitLab ": " @maxm ",
		"zammad":   "max@firma.de",
		"github":   "  ",
		"":         "wert-ohne-system",
	})
	if len(got) != 2 || got["gitlab"] != "maxm" || got["zammad"] != "max@firma.de" {
		t.Fatalf("Normalisierung falsch: %+v", got)
	}
	if got := NormalizeIdentities(nil); got == nil || len(got) != 0 {
		t.Fatalf("nil-Eingabe muss leere Map liefern (JSONB ist NOT NULL): %+v", got)
	}
}

func TestFieldKey(t *testing.T) {
	cases := map[string]string{
		"Standort":       "standort",
		"Slack-Handle":   "slack-handle",
		" Büro / Etage ": "buero-etage",
		"---":            "",
	}
	for in, want := range cases {
		if got := FieldKey(in); got != want {
			t.Fatalf("FieldKey(%q) = %q, want %q", in, got, want)
		}
	}
}
