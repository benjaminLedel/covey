package settings

import (
	"context"
	"errors"
	"testing"
)

// The validation sits in the store, so the CLI and the API cannot disagree
// about what a valid value is. These are the branches the handlers never
// reach with a good value.
func TestValidateSiteURL(t *testing.T) {
	for _, good := range []string{"", "https://covey.example.com", "http://localhost:8494", "https://covey.example.com/pfad"} {
		if err := validate(SiteURL, good); err != nil {
			t.Errorf("%q was refused: %v", good, err)
		}
	}
	// A bare host produces links without a scheme; a query or fragment says
	// the address was copied out of a browser bar and means nothing here.
	for _, bad := range []string{"covey.example.com", "/pfad", "ftp://covey.example.com", "https://", "https://covey.example.com?a=1", "https://covey.example.com#oben"} {
		if err := validate(SiteURL, bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q was accepted (err=%v)", bad, err)
		}
	}
}

func TestValidateNotifyWindow(t *testing.T) {
	for _, good := range []string{"0s", "5m", "24h"} {
		if err := validate(NotifyWindow, good); err != nil {
			t.Errorf("%q was refused: %v", good, err)
		}
	}
	for _, bad := range []string{"-1s", "25h", "bald", "5"} {
		if err := validate(NotifyWindow, bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q was accepted (err=%v)", bad, err)
		}
	}
}

// Not a boolean: what makes the confirmation worth anything is WHEN it was
// given.
func TestValidateHomeStoreBackup(t *testing.T) {
	if err := validate(HomeStoreBackup, ""); err != nil {
		t.Errorf("withdrawing was refused: %v", err)
	}
	if err := validate(HomeStoreBackup, "2026-09-12T10:00:00Z"); err != nil {
		t.Errorf("an RFC3339 timestamp was refused: %v", err)
	}
	for _, bad := range []string{"true", "12.09.2026", "ja"} {
		if err := validate(HomeStoreBackup, bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q was accepted", bad)
		}
	}
}

func TestValidateSignupModeAndQuotaAndSiteName(t *testing.T) {
	for _, good := range []string{ModeOff, ModeWaitlist, ModeOpen} {
		if err := validate(SignupMode, good); err != nil {
			t.Errorf("%q was refused: %v", good, err)
		}
	}
	if err := validate(SignupMode, "offen"); !errors.Is(err, ErrInvalid) {
		t.Error("an unknown mode was accepted")
	}
	if err := validate(SignupOrgQuota, "0"); err != nil {
		t.Errorf("0 was refused: %v", err)
	}
	for _, bad := range []string{"-1", "viele", ""} {
		if err := validate(SignupOrgQuota, bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("quota %q was accepted", bad)
		}
	}
	if err := validate(SiteName, ""); !errors.Is(err, ErrInvalid) {
		t.Error("an empty site name was accepted")
	}
}

func TestValidateNotifyClassSwitch(t *testing.T) {
	key := NotifyClassKey("task")
	for _, good := range []string{On, Off} {
		if err := validate(key, good); err != nil {
			t.Errorf("%q was refused: %v", good, err)
		}
	}
	if err := validate(key, "vielleicht"); !errors.Is(err, ErrInvalid) {
		t.Error("an unknown class switch value was accepted")
	}
}

// A host with a scheme or a port glued on is the most frequent typo, and it
// fails much later — in a DNS lookup nobody is watching.
func TestValidateMail(t *testing.T) {
	for _, tc := range []struct {
		key, value string
		ok         bool
	}{
		{MailPort, "587", true}, {MailPort, "1", true}, {MailPort, "65535", true},
		{MailPort, "0", false}, {MailPort, "65536", false}, {MailPort, "587er", false},
		{MailFrom, "", true}, // clearing is allowed; sending then fails, loudly
		{MailFrom, "covey@example.test", true},
		{MailFrom, "covey (at) example.test", false},
		{MailSecurity, SecurityStartTLS, true}, {MailSecurity, SecurityTLS, true}, {MailSecurity, SecurityNone, true},
		{MailSecurity, "ssl", false},
		{MailHost, "smtp.example.test", true},
		{MailHost, "smtp://smtp.example.test", false},
		{MailHost, "smtp.example.test:587", false},
		{MailUser, "anything:goes/here", true},
		{MailFromName, "covey: die Plattform", true},
	} {
		err := validateMail(tc.key, tc.value)
		if tc.ok && err != nil {
			t.Errorf("%s=%q was refused: %v", tc.key, tc.value, err)
		}
		if !tc.ok && !errors.Is(err, ErrInvalid) {
			t.Errorf("%s=%q was accepted (err=%v)", tc.key, tc.value, err)
		}
	}
}

func TestValidateTelemetry(t *testing.T) {
	for _, tc := range []struct {
		key, value string
		ok         bool
	}{
		{TelemetryMode, On, true}, {TelemetryMode, Off, true}, {TelemetryMode, "an", false},
		{TelemetryURL, "", true}, // the second way to switch the channel off
		{TelemetryURL, DefaultTelemetryURL, true},
		{TelemetryURL, "covey.work/rueckkanal", false},
		{TelemetryURL, "ftp://covey.work", false},
	} {
		err := validateTelemetry(tc.key, tc.value)
		if tc.ok && err != nil {
			t.Errorf("%s=%q was refused: %v", tc.key, tc.value, err)
		}
		if !tc.ok && !errors.Is(err, ErrInvalid) {
			t.Errorf("%s=%q was accepted (err=%v)", tc.key, tc.value, err)
		}
	}
}

func TestMailConfiguredNeedsHostAndSender(t *testing.T) {
	if (Mail{Host: "smtp.example.test"}).Configured() {
		t.Error("a mailer without a sender counts as configured")
	}
	if (Mail{From: "covey@example.test"}).Configured() {
		t.Error("a mailer without a host counts as configured")
	}
	if !(Mail{Host: "smtp.example.test", From: "covey@example.test"}).Configured() {
		t.Error("host and sender are not enough")
	}
}

func TestMailAddrAndSender(t *testing.T) {
	m := Mail{Host: "smtp.example.test", Port: 587, From: "covey@example.test"}
	if got := m.Addr(); got != "smtp.example.test:587" {
		t.Errorf("Addr = %q", got)
	}
	if got := m.Sender(); got != "covey@example.test" {
		t.Errorf("Sender without a display name = %q", got)
	}
	m.FromName = "covey"
	if got := m.Sender(); got != `"covey" <covey@example.test>` {
		t.Errorf("Sender with a display name = %q", got)
	}
	// An IPv6 host is bracketed rather than glued together.
	if got := (Mail{Host: "::1", Port: 25}).Addr(); got != "[::1]:25" {
		t.Errorf("IPv6 Addr = %q", got)
	}
}

// A store without a pool answers the defaults — the state of every fresh
// database, and the one that decides how the value methods behave when the
// database is gone.
func TestValueMethodsWithoutADatabase(t *testing.T) {
	ctx := context.Background()
	var s *Store

	// Whether registration is open has to fail closed.
	if got := s.Mode(ctx); got != ModeOff {
		t.Errorf("Mode = %q, expected %q", got, ModeOff)
	}
	// "Send at once" must never be what an error means.
	if got := s.NotifyWindowValue(ctx); got != DefaultNotifyWindow {
		t.Errorf("NotifyWindowValue = %v, expected %v", got, DefaultNotifyWindow)
	}
	// The class switch fails OPEN: a mail that goes out although the store was
	// unreachable is the lesser fault next to one silently dropped.
	if !s.NotifyClassOn(ctx, "task") {
		t.Error("NotifyClassOn = false without a database")
	}
	if !s.NotifyClassOn(ctx, "eine-klasse-die-es-nicht-gibt") {
		t.Error("an unknown class was answered with false")
	}
}

func TestSiteURLValueFallsBackToTheEnvironmentAndTrims(t *testing.T) {
	ctx := context.Background()
	var s *Store
	if got := s.SiteURLValue(ctx, " https://covey.example/ "); got != "https://covey.example" {
		t.Errorf("SiteURLValue = %q", got)
	}
	if got := s.SiteURLValue(ctx, ""); got != "" {
		t.Errorf("without a setting and without an environment: %q", got)
	}
}

// "off" in the environment wins over the setting, so an operator who cannot
// reach the interface has a way that works before the first start.
func TestTelemetryOnEnvironmentWins(t *testing.T) {
	ctx := context.Background()
	var s *Store
	if _, on := s.TelemetryOn(ctx, "off"); on {
		t.Error("COVEY_TELEMETRY=off did not switch the channel off")
	}
	if _, on := s.TelemetryOn(ctx, " OFF "); on {
		t.Error("the environment value is read case- and space-sensitively")
	}
	url, on := s.TelemetryOn(ctx, "")
	if !on || url != DefaultTelemetryURL {
		t.Errorf("TelemetryOn = %q, %v — expected the default address", url, on)
	}
}

func TestKennungWithoutADatabaseStaysEmpty(t *testing.T) {
	var s *Store
	got, err := s.Kennung(context.Background())
	if err != nil {
		t.Fatalf("Kennung: %v", err)
	}
	if got != "" {
		t.Errorf("Kennung = %q — without a database there is nothing to remember", got)
	}
}

func TestNotifyWindowDefaultIsWithinItsOwnBound(t *testing.T) {
	if DefaultNotifyWindow > MaxNotifyWindow {
		t.Fatal("the default window is larger than the maximum the validation allows")
	}
	if err := validate(NotifyWindow, DefaultNotifyWindow.String()); err != nil {
		t.Fatalf("the default window does not pass its own validation: %v", err)
	}
}

func TestEveryDefaultPassesItsOwnValidation(t *testing.T) {
	// A default the store would refuse on a Set is a value nobody can put back
	// after changing it once.
	for _, key := range Keys() {
		if ReadOnly[key] || Secrets[key] {
			continue
		}
		if err := validate(key, Defaults[key]); err != nil {
			t.Errorf("default of %s (%q) does not pass validation: %v", key, Defaults[key], err)
		}
	}
}
