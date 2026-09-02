package settings

import (
	"context"
	"errors"
	"testing"
)

// Ohne Verbindung liefert der Store die Vorgaben. Das ist kein Testkniff,
// sondern der Zustand jeder frischen Datenbank: gesät wird nichts, es gilt,
// was im Code steht.
func TestVorgabenOhneDatenbank(t *testing.T) {
	var s *Store
	v, err := s.Get(context.Background(), SignupMode)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if v != ModeOff {
		t.Errorf("signup.mode = %q, erwartet %q — eine Installation, die nichts weiß, nimmt niemanden auf", v, ModeOff)
	}
}

func TestUnbekannterSchluessel(t *testing.T) {
	var s *Store
	if _, err := s.Get(context.Background(), "signup.mod"); !errors.Is(err, ErrUnknownKey) {
		t.Errorf("Tippfehler im Schlüssel muss auffallen, bekam: %v", err)
	}
}

// Die Prüfung sitzt im Store und nicht im Handler — sonst könnten CLI und API
// verschiedener Meinung darüber sein, was ein gültiger Wert ist.
func TestPruefung(t *testing.T) {
	faelle := []struct {
		key, value string
		ok         bool
	}{
		{SignupMode, "off", true},
		{SignupMode, "waitlist", true},
		{SignupMode, "open", true},
		{SignupMode, "offen", false},
		{SignupMode, "", false},
		{SignupOrgQuota, "3", true},
		{SignupOrgQuota, "-1", false},
		{SignupOrgQuota, "viele", false},
		{SiteName, "covey", true},
		{SiteName, "", false},
	}
	for _, f := range faelle {
		err := validate(f.key, f.value)
		if f.ok && err != nil {
			t.Errorf("%s=%q sollte gelten, bekam: %v", f.key, f.value, err)
		}
		if !f.ok && err == nil {
			t.Errorf("%s=%q sollte abgelehnt werden", f.key, f.value)
		}
	}
}

func TestKeysStabilSortiert(t *testing.T) {
	a, b := Keys(), Keys()
	if len(a) != len(Defaults) {
		t.Fatalf("Keys liefert %d von %d Schlüsseln", len(a), len(Defaults))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatal("Reihenfolge schwankt — die Ausgabe von `covey settings` wäre nicht wiedererkennbar")
		}
	}
}
