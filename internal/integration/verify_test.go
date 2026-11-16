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

// A reset is how an account is recovered, and recovery has to lock out
// whatever the old password let in: a browser signed in before the reset, an
// API key and a pairing code minted from such a session. None of them may
// outlive the new password.
func TestPasswordResetEndsEveryOldCredential(t *testing.T) {
	s := newStack(t)
	smtp := startFakeSMTP(t)
	s.configureMailer(t, smtp)

	before := login(t, s, "admin@test.local", "admin-passwort")
	key, _ := createKey(t, before, "pipeline", 0)
	pairing := before.expect(http.MethodPost, "/api/v1/auth/pairings", nil, http.StatusCreated)
	code, _ := pairing["code"].(string)

	res := s.postJSON(t, "/api/v1/public/password-reset", map[string]any{
		"email": "admin@test.local", "lang": "en"})
	res.Body.Close()
	_, _, msg := smtp.snapshot()
	token := tokenOf(t, linkIn(t, msg, "/reset"))
	res = s.postJSON(t, "/api/v1/public/password-reset/confirm", map[string]any{
		"token": token, "password": "the-new-long-password"})
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("setting the password answers %d, expected 200", res.StatusCode)
	}

	if r := before.do(http.MethodGet, "/api/v1/auth/me", nil); r.StatusCode != http.StatusUnauthorized {
		r.Body.Close()
		t.Errorf("a session from before the reset answers %d, expected 401", r.StatusCode)
	} else {
		r.Body.Close()
	}
	if r := bearer(t, s, key, http.MethodGet, "/api/v1/auth/me", nil); r.StatusCode != http.StatusUnauthorized {
		r.Body.Close()
		t.Errorf("an API key from before the reset answers %d, expected 401", r.StatusCode)
	} else {
		r.Body.Close()
	}
	if status, out := redeem(t, s, code, "phone"); status == http.StatusCreated {
		t.Errorf("a pairing code from before the reset still mints a key: %v", out)
	}

	// The new password signs in as usual.
	login(t, s, "admin@test.local", "the-new-long-password").
		expect(http.MethodGet, "/api/v1/auth/me", nil, http.StatusOK)
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

// TestResetLinkIgnoresTheHostHeader is #693: without a configured site
// address the reset link took its host from the request, so whoever asked for
// somebody else's reset chose where the genuine mail sent the token.
func TestResetLinkIgnoresTheHostHeader(t *testing.T) {
	s := newStack(t)
	smtp := startFakeSMTP(t)
	s.configureMailer(t, smtp)
	reset := func() *http.Response {
		body := strings.NewReader(`{"email":"admin@test.local","lang":"en"}`)
		req, _ := http.NewRequest(http.MethodPost, s.http.URL+"/api/v1/public/password-reset", body)
		req.Header.Set("Content-Type", "application/json")
		req.Host = "evil.example"
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res
	}

	// No site address: the answer is unchanged, and no mail goes out.
	s.srv.SiteURL = ""
	if res := reset(); res.StatusCode != http.StatusAccepted {
		t.Fatalf("reset answers %d, expected 202", res.StatusCode)
	}
	if _, _, msg := smtp.snapshot(); msg != "" {
		t.Fatalf("a reset mail went out without a configured site address:\n%s", msg)
	}

	// With one, the link is built on it, whatever the Host header says.
	s.srv.SiteURL = "https://covey.example"
	reset()
	_, _, msg := smtp.snapshot()
	if link := linkIn(t, msg, "/reset"); !strings.HasPrefix(link, "https://covey.example/reset?") {
		t.Fatalf("reset link %s, expected it on the configured site address", link)
	}
}
