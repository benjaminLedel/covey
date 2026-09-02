package integration

// Confirming an address and getting back into an account (#168).
//
// What is being tested is the chain, not the endpoints one by one: a
// registration produces a mail, the link in that mail produces a confirmed
// account and a session, and the same link produces nothing a second time.
// Every one of those steps is where the previous ones become worthless if it
// breaks.

import (
	"context"
	"io"
	"mime/quotedprintable"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"covey/internal/accounts"
	"covey/internal/settings"
	"covey/internal/waitlist"
)

// linkIn pulls the first covey link out of a delivered message.
//
// Decoding quoted-printable is not optional here: the transfer encoding turns
// the "=" of "?token=" into "=3D" and folds lines longer than 76 characters
// with a trailing "=". A test that searched the raw text would find a link
// that no mail client ever shows, and would pass while the real one is broken.
func linkIn(t *testing.T, raw, path string) string {
	t.Helper()
	_, body, found := strings.Cut(raw, "\r\n\r\n")
	if !found {
		t.Fatalf("the message has no body:\n%s", raw)
	}
	decoded, err := io.ReadAll(quotedprintable.NewReader(strings.NewReader(body)))
	if err != nil {
		t.Fatalf("body not decodable: %v", err)
	}
	re := regexp.MustCompile(`https?://[^\s]*` + regexp.QuoteMeta(path) + `\?token=[A-Za-z0-9_-]+`)
	link := re.FindString(string(decoded))
	if link == "" {
		t.Fatalf("no %s link in the mail:\n%s", path, decoded)
	}
	return link
}

// tokenOf takes the token out of a link, the way the page does that the link
// opens.
func tokenOf(t *testing.T, link string) string {
	t.Helper()
	_, token, found := strings.Cut(link, "token=")
	if !found {
		t.Fatalf("link without a token: %s", link)
	}
	return token
}

func TestRegistrationConfirmsTheAddress(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	smtp := startFakeSMTP(t)
	s.configureMailer(t, smtp)
	s.proveMailer(t)
	if err := s.settings.Set(ctx, settings.SignupMode, settings.ModeWaitlist, nil); err != nil {
		t.Fatal(err)
	}
	code, err := waitlist.New(s.pool).Create(ctx, waitlist.Options{Label: "test", MaxUses: 1})
	if err != nil {
		t.Fatal(err)
	}

	res := s.postJSON(t, "/api/v1/public/signup", map[string]any{
		"code": code, "email": "erika@example.test",
		"display_name": "Erika", "password": "long-enough-password", "lang": "de",
	})
	res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("registration answers %d, expected 201", res.StatusCode)
	}

	// The account exists and is NOT confirmed — that is what the mail is for.
	acc, err := accounts.New(s.pool).ByEmail(ctx, "erika@example.test")
	if err != nil {
		t.Fatal(err)
	}
	if acc.Verified() {
		t.Error("the address counts as confirmed although nobody confirmed it")
	}

	_, rcpts, msg := smtp.snapshot()
	if len(rcpts) != 1 || rcpts[0] != "erika@example.test" {
		t.Fatalf("recipients %v", rcpts)
	}
	// The language travelled with the registration: the mail is the German one.
	if !strings.Contains(msg, "=?utf-8?") && !strings.Contains(msg, "Adresse") {
		t.Errorf("the subject does not look like the German one:\n%s", msg)
	}
	link := linkIn(t, msg, "/verify")
	token := tokenOf(t, link)

	// The link confirms and signs in — the session comes with the answer.
	client := &apiClient{t: t, base: s.http.URL, http: &http.Client{Jar: newJar()}}
	resp := client.do(http.MethodPost, "/api/v1/public/verify", map[string]string{"token": token})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("confirmation answers %d, expected 200", resp.StatusCode)
	}
	acc, err = accounts.New(s.pool).ByEmail(ctx, "erika@example.test")
	if err != nil {
		t.Fatal(err)
	}
	if !acc.Verified() {
		t.Error("the address is still unconfirmed after the link was opened")
	}
	me := client.expect(http.MethodGet, "/api/v1/auth/me", nil, http.StatusOK)
	if me["Email"] != "erika@example.test" && me["email"] != "erika@example.test" {
		t.Errorf("the session does not belong to the confirmed account: %v", me)
	}

	// A second time it is worth nothing. Somebody who forwards the mail hands
	// on a link that has been used up.
	resp = client.do(http.MethodPost, "/api/v1/public/verify", map[string]string{"token": token})
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("the used link answers %d, expected 400", resp.StatusCode)
	}
}

// A mail server that refuses takes the whole registration with it. Otherwise
// what stays behind is an account nobody can confirm and a waitlist use burned
// for it.
func TestRegistrationWithoutAMailIsNoRegistration(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	s.proveMailer(t)
	if err := s.settings.Set(ctx, settings.SignupMode, settings.ModeWaitlist, nil); err != nil {
		t.Fatal(err)
	}
	// A host nothing listens on: configured, therefore attempted, and it fails.
	for key, value := range map[string]string{
		settings.MailHost: "127.0.0.1", settings.MailPort: "1",
		settings.MailFrom: "covey@example.test", settings.MailSecurity: settings.SecurityNone,
	} {
		if err := s.settings.Set(ctx, key, value, nil); err != nil {
			t.Fatal(err)
		}
	}
	code, err := waitlist.New(s.pool).Create(ctx, waitlist.Options{Label: "test", MaxUses: 1})
	if err != nil {
		t.Fatal(err)
	}

	res := s.postJSON(t, "/api/v1/public/signup", map[string]any{
		"code": code, "email": "erika@example.test",
		"display_name": "Erika", "password": "long-enough-password",
	})
	res.Body.Close()
	// 503 and not 500: nothing about the request was wrong, and the state is
	// not permanent — the sentence has to send somebody back, not away.
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("registration answers %d although no mail went out, expected 503", res.StatusCode)
	}

	if _, err := accounts.New(s.pool).ByEmail(ctx, "erika@example.test"); err == nil {
		t.Error("an account was created for which no confirmation could be sent")
	}
	var used int
	if err := s.pool.QueryRow(ctx,
		"SELECT used_count FROM waitlist_codes").Scan(&used); err != nil {
		t.Fatal(err)
	}
	if used != 0 {
		t.Errorf("the code was used %d times although the registration failed", used)
	}
}

func TestPasswordReset(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	smtp := startFakeSMTP(t)
	s.configureMailer(t, smtp)

	// The reset works on a CLOSED instance: whoever has an account here is not
	// a stranger asking to be let in.
	if mode := s.settings.Mode(ctx); mode != settings.ModeOff {
		t.Fatalf("the stack starts with signup.mode=%s", mode)
	}

	// An unknown address gets the same answer as a known one, and sends
	// nothing.
	res := s.postJSON(t, "/api/v1/public/password-reset", map[string]any{"email": "nobody@example.test"})
	res.Body.Close()
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("unknown address answers %d, expected 202", res.StatusCode)
	}
	if _, _, msg := smtp.snapshot(); msg != "" {
		t.Errorf("a mail went out for an unknown address:\n%s", msg)
	}

	res = s.postJSON(t, "/api/v1/public/password-reset", map[string]any{
		"email": "admin@test.local", "lang": "en"})
	res.Body.Close()
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("known address answers %d, expected 202", res.StatusCode)
	}
	_, rcpts, msg := smtp.snapshot()
	if len(rcpts) != 1 || rcpts[0] != "admin@test.local" {
		t.Fatalf("recipients %v", rcpts)
	}
	token := tokenOf(t, linkIn(t, msg, "/reset"))

	// A password that is too short is refused before the token is spent — the
	// link has to survive a typo.
	res = s.postJSON(t, "/api/v1/public/password-reset/confirm", map[string]any{
		"token": token, "password": "short"})
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("a short password answers %d, expected 400", res.StatusCode)
	}

	res = s.postJSON(t, "/api/v1/public/password-reset/confirm", map[string]any{
		"token": token, "password": "the-new-long-password"})
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("setting the password answers %d, expected 200", res.StatusCode)
	}

	// The new password works, the old one does not, and the link is used up.
	login(t, s, "admin@test.local", "the-new-long-password")
	old := s.postJSON(t, "/api/v1/auth/login",
		map[string]string{"email": "admin@test.local", "password": "admin-passwort"})
	old.Body.Close()
	if old.StatusCode == http.StatusOK {
		t.Error("the old password still works")
	}
	res = s.postJSON(t, "/api/v1/public/password-reset/confirm", map[string]any{
		"token": token, "password": "yet-another-password"})
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("the used link answers %d, expected 400", res.StatusCode)
	}
}

// An expired link is worth as much as a wrong one, and says so the same way.
func TestExpiredTokenIsRefused(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	store := accounts.New(s.pool)
	acc, err := store.ByEmail(ctx, "admin@test.local")
	if err != nil {
		t.Fatal(err)
	}
	token, err := store.IssueToken(ctx, acc.ID, accounts.PurposeReset, -time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ResetPassword(ctx, token, "irrelevant"); err == nil {
		t.Error("an expired link set a password")
	}
}
