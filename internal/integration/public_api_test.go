package integration

import (
	"context"
	"net/http"
	"testing"

	"covey/internal/settings"
	"covey/internal/waitlist"
)

// The public endpoints are the only ones reachable without a session, so what
// they say about an address is what an attacker learns by asking. All three
// answer identically for a known address and an unknown one — the alternative
// is an endpoint that tells anybody who is registered here.
func TestPublicEndpointsDoNotDisclose(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	smtp := s.workingMailer(t)
	_ = smtp
	if err := s.settings.Set(ctx, settings.SignupMode, settings.ModeWaitlist, nil); err != nil {
		t.Fatal(err)
	}
	code, err := waitlist.New(s.pool).Create(ctx, waitlist.Options{Label: "test", MaxUses: 1})
	if err != nil {
		t.Fatal(err)
	}
	res := s.postJSON(t, "/api/v1/public/signup", map[string]any{
		"code": code, "email": "bekannt@example.test",
		"display_name": "Bekannt", "password": "long-enough-password",
	})
	res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("registration answers %d", res.StatusCode)
	}

	// A reset for an address that exists and one that does not: the same
	// answer, because a different one would be a directory of accounts.
	for _, email := range []string{"bekannt@example.test", "gibtesnicht@example.test"} {
		res := s.postJSON(t, "/api/v1/public/password-reset", map[string]any{"email": email})
		res.Body.Close()
		if res.StatusCode != http.StatusAccepted {
			t.Errorf("password-reset for %s answers %d, expected 202", email, res.StatusCode)
		}
	}

	// And the same for a second confirmation mail. An account somebody
	// registered with a typo'd address is otherwise dead: registering again
	// answers "already taken", and the way out would run through an
	// administrator with database access.
	for _, email := range []string{"bekannt@example.test", "gibtesnicht@example.test"} {
		res := s.postJSON(t, "/api/v1/public/verify/resend", map[string]any{"email": email, "lang": "de"})
		res.Body.Close()
		if res.StatusCode != http.StatusAccepted {
			t.Errorf("verify/resend for %s answers %d, expected 202", email, res.StatusCode)
		}
	}

	// A body that is not readable is a bad request in all three.
	for _, path := range []string{
		"/api/v1/public/signup", "/api/v1/public/password-reset",
		"/api/v1/public/verify/resend", "/api/v1/public/verify",
		"/api/v1/public/password-reset/confirm",
	} {
		res := s.postJSON(t, path, "kein objekt")
		res.Body.Close()
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("%s with an unreadable body answers %d", path, res.StatusCode)
		}
	}

	// A token nobody issued is refused the same way an expired one is — the
	// three cases are distinguishable only to whoever holds a valid token.
	res = s.postJSON(t, "/api/v1/public/verify", map[string]any{"token": "erfunden"})
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("an invented confirmation token answers %d", res.StatusCode)
	}
	res = s.postJSON(t, "/api/v1/public/password-reset/confirm",
		map[string]any{"token": "erfunden", "password": "ein-langes-passwort"})
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("an invented reset token answers %d", res.StatusCode)
	}
}

// What a closed instance says about itself: the mode, and nothing else. The
// login page reads it to decide whether to offer a registration at all.
func TestSignupStateOverTheAPI(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	res := s.postJSON(t, "/api/v1/public/password-reset", map[string]any{"email": "x@example.test"})
	res.Body.Close()

	// Without a proven mailer the instance stays closed — an instance that
	// opens without one cannot send the confirmation it promises.
	if err := s.settings.Set(ctx, settings.SignupMode, settings.ModeOpen, nil); err == nil {
		t.Error("registration was opened without a mailer that has been tested")
	}

	s.workingMailer(t)
	if err := s.settings.Set(ctx, settings.SignupMode, settings.ModeOpen, nil); err != nil {
		t.Fatalf("with a proven mailer: %v", err)
	}

	// A registration without a code now works, and one with a password that is
	// too short does not.
	res = s.postJSON(t, "/api/v1/public/signup", map[string]any{
		"email": "offen@example.test", "display_name": "Offen", "password": "kurz",
	})
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("a four-character password answers %d", res.StatusCode)
	}
	res = s.postJSON(t, "/api/v1/public/signup", map[string]any{
		"email": "keine-adresse", "display_name": "X", "password": "ein-langes-passwort",
	})
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("an address that is not one answers %d", res.StatusCode)
	}
}
