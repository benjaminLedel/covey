package installer

import "strings"

import "testing"

// Der Kern der Sache: Eine Instanz darf dem Skript nur dann eine feste
// Version vorgeben, wenn es dazu auch ein Release geben kann. Ein
// Entwicklungs-Build heißt "dev" oder trägt einen describe-Zusatz — beides
// gibt es auf GitHub nie, und das Skript liefe in einen 404, statt einfach die
// neueste Veröffentlichung zu nehmen.
func TestVersionFuerRelease(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"v0.1.0", "v0.1.0"},
		{"v1.2.3", "v1.2.3"},
		{"v2.0.0-rc1", "v2.0.0-rc1"}, // Vorabversionen werden veröffentlicht
		{"dev", ""},
		{"", ""},
		{"v0.1.0-3-gabc1234", ""},       // git describe nach dem Tag
		{"v0.1.0-dirty", ""},            // Arbeitsbaum mit Änderungen
		{"v0.1.0-3-gabc1234-dirty", ""}, // beides
		{"0.1.0", ""},                   // ohne v kein Tag dieses Projekts
	}
	for _, tc := range cases {
		if got := VersionFuerRelease(tc.in); got != tc.want {
			t.Errorf("VersionFuerRelease(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestRenderSetztVorspann(t *testing.T) {
	out := Render("v0.1.0", "runner")
	for _, muss := range []string{
		"COVEY_INSTALL_VERSION=\"v0.1.0\"",
		"COVEY_INSTALL_DEFAULT=\"runner\"",
		"export COVEY_INSTALL_VERSION",
	} {
		if !strings.Contains(out, muss) {
			t.Errorf("Vorspann enthält %q nicht", muss)
		}
	}
	// Der Rumpf muss unverändert dabei sein — sonst liefert die Instanz etwas
	// anderes aus als das Repository.
	if !strings.Contains(out, Script) {
		t.Error("das eingebettete Skript fehlt im Ergebnis")
	}
	// Genau ein Shebang, und zwar in der ersten Zeile: Der Vorspann bringt
	// einen mit, der Rumpf auch. Stünde der zweite mittendrin, wäre er nur ein
	// Kommentar — aber die erste Zeile muss stimmen.
	if !strings.HasPrefix(out, "#!/bin/sh\n") {
		t.Error("das Ergebnis beginnt nicht mit einem Shebang")
	}
}

// Ohne Version darf kein VERSION-Vorspann entstehen: Das Skript soll dann
// selbst die neueste Veröffentlichung ermitteln.
func TestRenderOhneVersion(t *testing.T) {
	out := Render("", "")
	if strings.Contains(out, "COVEY_INSTALL_VERSION=") {
		t.Error("ohne Version darf keine Version vorgegeben werden")
	}
	if !strings.Contains(out, "COVEY_INSTALL_DEFAULT=\"server\"") {
		t.Error("ohne Angabe muss die Voreinstellung \"server\" sein")
	}
}
