package integration

// The installation's own mailer (#167).
//
// What is being tested is not SMTP — the stdlib does that. It is the two
// promises the mail configuration makes to whoever operates an instance:
//
//   - The test mail takes the SAME path a real mail takes. A test that
//     travelled a different route could pass while every real mail failed, and
//     would have certified nothing.
//   - Registration does not open while no mail has demonstrably gone out. A
//     filled-in SMTP host is not evidence.

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"covey/internal/accounts"
	"covey/internal/settings"
)

// The suite already has an SMTP double (email_test.go, fakeSMTPServer): it is
// the one the email target plugin is tested against. That is the right server
// for this test too — the control plane's sender has to get along with the
// same plain, extensionless relay an agent's does.

// --- Helpers for the other tests ---

// proveMailer is what the registration gate wants to see: a test mail that
// went through. The tests around self-registration are not about the mailer,
// so they take the shortest honest route to that state — what the record
// actually certifies is tested below.
func (s *stack) proveMailer(t *testing.T) {
	t.Helper()
	if err := s.settings.RecordMailTest(context.Background(), time.Now(), ""); err != nil {
		t.Fatal(err)
	}
}

// workingMailer is what a test needs that is not about the mailer but cannot
// do without one: a mail server that answers, and the record of a test mail
// that went through. Since #168 a registration without a mailer creates
// nothing at all — which is the point, and which every sign-up test has to
// stand in front of.
func (s *stack) workingMailer(t *testing.T) *fakeSMTPServer {
	t.Helper()
	smtp := startFakeSMTP(t)
	s.configureMailer(t, smtp)
	s.proveMailer(t)
	return smtp
}

// configureMailer points the instance at a mail server and returns it.
func (s *stack) configureMailer(t *testing.T, smtp *fakeSMTPServer) {
	t.Helper()
	ctx := context.Background()
	host, port, err := net.SplitHostPort(smtp.addr())
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{
		settings.MailHost:     host,
		settings.MailPort:     port,
		settings.MailFrom:     "covey@example.test",
		settings.MailFromName: "covey test instance",
		settings.MailSecurity: settings.SecurityNone,
	} {
		if err := s.settings.Set(ctx, key, value, nil); err != nil {
			t.Fatalf("%s=%s: %v", key, value, err)
		}
	}
}

// --- The tests ---

func TestTestMailAndTheRegistrationGate(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	if err := accounts.New(s.pool).SetPlatformRole(ctx, "admin@test.local", accounts.RoleSystemAdmin); err != nil {
		t.Fatal(err)
	}
	admin := login(t, s, "admin@test.local", "admin-passwort")

	// Closed is the delivery state, and it stays closed while nothing can be
	// sent: an instance that opens registration without a mailer produces
	// accounts nobody can ever confirm.
	if err := s.settings.Set(ctx, settings.SignupMode, settings.ModeWaitlist, nil); err == nil {
		t.Fatal("registration opened without a mailer")
	}

	// Without a host the test mail does not even try — and says so as a bad
	// request, not as a gateway failure. There is no gateway yet.
	resp := admin.do(http.MethodPost, "/api/v1/platform/mail/test", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("test mail without a host answers %d, expected 400", resp.StatusCode)
	}

	// A host that nothing listens on: the attempt is made, fails, and the
	// failure is recorded — which is the state the gate has to see.
	if err := s.settings.Set(ctx, settings.MailHost, "127.0.0.1", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.settings.Set(ctx, settings.MailPort, "1", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.settings.Set(ctx, settings.MailFrom, "covey@example.test", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.settings.Set(ctx, settings.MailSecurity, settings.SecurityNone, nil); err != nil {
		t.Fatal(err)
	}
	resp = admin.do(http.MethodPost, "/api/v1/platform/mail/test", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("failed test mail answers %d, expected 502", resp.StatusCode)
	}
	all, err := s.settings.All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if all[settings.MailLastTestError] == "" {
		t.Error("the failed attempt left no error behind")
	}
	if err := s.settings.Set(ctx, settings.SignupMode, settings.ModeWaitlist, nil); err == nil {
		t.Fatal("registration opened although the last test mail failed")
	}

	// Now a server that answers.
	smtp := startFakeSMTP(t)
	s.configureMailer(t, smtp)
	admin.expect(http.MethodPost, "/api/v1/platform/mail/test", nil, http.StatusOK)
	_, rcpts, msg := smtp.snapshot()
	if len(rcpts) != 1 || rcpts[0] != "admin@test.local" {
		t.Fatalf("recipients %v, expected exactly the signed-in admin", rcpts)
	}
	for _, want := range []string{
		"To: admin@test.local",                             // the signed-in admin, nobody else
		`From: "covey test instance" <covey@example.test>`, // the stored sender
		"Auto-Submitted: auto-generated",                   // no out-of-office loop
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the message lacks %q:\n%s", want, msg)
		}
	}

	// And only now does the gate open.
	if err := s.settings.Set(ctx, settings.SignupMode, settings.ModeWaitlist, nil); err != nil {
		t.Fatalf("registration stays closed although the test mail arrived: %v", err)
	}
}

// The SMTP password is sealed and never comes back out. It is the one setting
// whose value leaving the server would be a finding rather than a bug.
func TestSMTPPasswordStaysInside(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	if err := accounts.New(s.pool).SetPlatformRole(ctx, "admin@test.local", accounts.RoleSystemAdmin); err != nil {
		t.Fatal(err)
	}
	admin := login(t, s, "admin@test.local", "admin-passwort")

	const secret = "hunter2-but-longer"
	resp := admin.do(http.MethodPut, "/api/v1/platform/settings/"+settings.MailPassword,
		map[string]string{"value": secret})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("storing the password answers %d", resp.StatusCode)
	}

	// Not in the table's plain column …
	var value *string
	if err := s.pool.QueryRow(ctx,
		"SELECT value FROM system_settings WHERE key=$1", settings.MailPassword).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != nil {
		t.Errorf("the password stands in `value`: %q", *value)
	}

	// … not in the API's answer …
	list := admin.expectList(http.MethodGet, "/api/v1/platform/settings", nil, http.StatusOK)
	raw, _ := json.Marshal(list)
	if strings.Contains(string(raw), secret) {
		t.Error("the settings list returns the password")
	}
	found := false
	for _, e := range list {
		if e["key"] == settings.MailPassword {
			found = true
			if e["secret"] != true || e["set"] != true {
				t.Errorf("%s: secret=%v set=%v, expected both true", e["key"], e["secret"], e["set"])
			}
		}
	}
	if !found {
		t.Error("the password is missing from the list entirely — then nobody can replace it")
	}

	// … and yet readable for the sender, which is the entire point.
	got, err := s.settings.GetSecret(ctx, settings.MailPassword)
	if err != nil {
		t.Fatal(err)
	}
	if got != secret {
		t.Errorf("read back %q, expected %q", got, secret)
	}

	// An empty value clears it — that is how "remove password" works.
	resp = admin.do(http.MethodPut, "/api/v1/platform/settings/"+settings.MailPassword,
		map[string]string{"value": ""})
	resp.Body.Close()
	set, err := s.settings.SecretSet(ctx, settings.MailPassword)
	if err != nil {
		t.Fatal(err)
	}
	if set {
		t.Error("the password survived being cleared")
	}
}

// What the installation writes about itself, an administrator does not write.
// Otherwise the registration gate could be opened by hand, and it would be
// decoration.
func TestTestResultIsNotSettable(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	if err := accounts.New(s.pool).SetPlatformRole(ctx, "admin@test.local", accounts.RoleSystemAdmin); err != nil {
		t.Fatal(err)
	}
	admin := login(t, s, "admin@test.local", "admin-passwort")

	resp := admin.do(http.MethodPut, "/api/v1/platform/settings/"+settings.MailLastTestAt,
		map[string]string{"value": time.Now().Format(time.RFC3339)})
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("setting the test result answers %d, expected 400", resp.StatusCode)
	}
	if err := s.settings.Set(ctx, settings.SignupMode, settings.ModeWaitlist, nil); err == nil {
		t.Fatal("registration opened through a hand-written test result")
	}
}
