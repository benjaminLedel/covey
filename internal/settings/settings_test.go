package settings

import (
	"context"
	"errors"
	"testing"
)

// Without a connection the store returns the defaults. That is no trick of the
// test, but the state of every fresh database: nothing is seeded, what stands
// in the code applies.
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

// The check sits in the store and not in the handler — otherwise CLI and API
// could disagree about what a valid value is.
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
		{SiteURL, "", true},
		{SiteURL, "https://covey.example.com", true},
		{SiteURL, "https://covey.example.com/covey/", true},
		{SiteURL, "covey.example.com", false},
		{SiteURL, "ftp://covey.example.com", false},
		{SiteURL, "https://covey.example.com/?x=1", false},
		{NotifyWindow, "5m", true},
		{NotifyWindow, "0s", true},
		{NotifyWindow, "24h", true},
		{NotifyWindow, "25h", false},
		{NotifyWindow, "-1m", false},
		{NotifyWindow, "soon", false},
		{NotifyClassKey("task"), "on", true},
		{NotifyClassKey("task"), "off", true},
		{NotifyClassKey("task"), "yes", false},
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
