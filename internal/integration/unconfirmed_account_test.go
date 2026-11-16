package integration

import (
	"context"
	"net/http"
	"testing"

	"covey/internal/accounts"
	"covey/internal/secrets/sealbox"
	"covey/internal/settings"
	"covey/internal/sso"
)

// registerUnconfirmed signs an address up through the public form and leaves
// the confirmation mail unopened — the account exists, its address is not
// proven. The returned client holds the session its password produces.
func (s *stack) registerUnconfirmed(t *testing.T, email, name, password string) *apiClient {
	t.Helper()
	res := s.postJSON(t, "/api/v1/public/signup", map[string]any{
		"email": email, "display_name": name, "password": password,
	})
	res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("signup %s: %d", email, res.StatusCode)
	}
	acc, err := accounts.New(s.pool).ByEmail(context.Background(), email)
	if err != nil || acc.Verified() {
		t.Fatalf("%s after signup: verified=%v err=%v", email, acc.Verified(), err)
	}
	return login(t, s, email, password)
}

// refusedLogin asserts that a password does not sign in.
func (s *stack) refusedLogin(t *testing.T, email, password string) {
	t.Helper()
	res := s.postJSON(t, "/api/v1/auth/login", map[string]string{"email": email, "password": password})
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("login %s with a password that should be gone: %d, expected 401", email, res.StatusCode)
	}
}

// A seat an administrator creates goes to the person the administrator means,
// not to whoever registered the address first without confirming it. The
// unconfirmed account is taken over: the administrator's password, the address
// confirmed, the registrant's session and links gone. One that holds a seat
// elsewhere cannot be taken over by one organisation and is refused instead.
func TestAdminSeatTakesOverUnconfirmedRegistration(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	s.workingMailer(t)
	if err := s.settings.Set(ctx, settings.SignupMode, settings.ModeOpen, nil); err != nil {
		t.Fatal(err)
	}
	accs := accounts.New(s.pool)

	// An organisation admin adds the address.
	registrant := s.registerUnconfirmed(t, "ada@example.org", "Not Ada", "registrant-pw-1")
	admin := login(t, s, "admin@test.local", "admin-passwort")
	admin.expect(http.MethodPost, "/api/v1/users", map[string]any{
		"email": "ada@example.org", "display_name": "Ada", "role": "org_admin", "password": "admin-chosen-pw",
	}, http.StatusCreated)

	s.refusedLogin(t, "ada@example.org", "registrant-pw-1")
	registrant.expect(http.MethodGet, "/api/v1/auth/me", nil, http.StatusUnauthorized)
	ada := login(t, s, "ada@example.org", "admin-chosen-pw")
	me := ada.expect(http.MethodGet, "/api/v1/auth/me", nil, http.StatusOK)
	if me["OrgID"] != s.orgID.String() || me["Role"] != "org_admin" {
		t.Fatalf("ada after the admin created her seat: %v", me)
	}
	acc, err := accs.ByEmail(ctx, "ada@example.org")
	if err != nil || !acc.Verified() || acc.DisplayName != "Ada" {
		t.Fatalf("taken-over account: %+v %v", acc, err)
	}
	var tokens int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM account_tokens WHERE account_id=$1`, acc.ID).Scan(&tokens); err != nil || tokens != 0 {
		t.Fatalf("the registrant's links survived the take-over: %d %v", tokens, err)
	}

	// The same for the first admin of a new organisation.
	s.alsSystemadmin(t)
	s.registerUnconfirmed(t, "linus@example.net", "Not Linus", "registrant-pw-2")
	other := admin.expect(http.MethodPost, "/api/v1/platform/orgs", map[string]string{
		"name": "Other", "admin_email": "linus@example.net", "admin_name": "Linus", "admin_password": "platform-chosen-pw",
	}, http.StatusCreated)
	s.refusedLogin(t, "linus@example.net", "registrant-pw-2")
	me = login(t, s, "linus@example.net", "platform-chosen-pw").expect(http.MethodGet, "/api/v1/auth/me", nil, http.StatusOK)
	if me["OrgID"] != other["id"] || me["Role"] != "org_admin" {
		t.Fatalf("linus after the organisation was created: %v", me)
	}

	// An unconfirmed account the instance administration already seated
	// elsewhere is not one organisation's to take over: refused, untouched.
	s.registerUnconfirmed(t, "grace@example.net", "Grace", "grace-pw-1")
	grace, err := accs.ByEmail(ctx, "grace@example.net")
	if err != nil {
		t.Fatal(err)
	}
	admin.expect(http.MethodPost, "/api/v1/platform/orgs/"+other["id"].(string)+"/members",
		map[string]string{"account_id": grace.ID.String(), "role": "auditor"}, http.StatusCreated)
	admin.expect(http.MethodPost, "/api/v1/users", map[string]any{
		"email": "grace@example.net", "display_name": "Grace", "role": "org_admin", "password": "admin-chosen-pw",
	}, http.StatusConflict)
	login(t, s, "grace@example.net", "grace-pw-1")

	// A confirmed account keeps its password and simply gains the seat.
	login(t, s, "linus@example.net", "platform-chosen-pw")
	admin.expect(http.MethodPost, "/api/v1/users", map[string]any{
		"email": "linus@example.net", "display_name": "Linus", "role": "auditor", "password": "yet-another-pw",
	}, http.StatusCreated)
	login(t, s, "linus@example.net", "platform-chosen-pw")
	s.refusedLogin(t, "linus@example.net", "yet-another-pw")
}

// The same for single sign-on: the provider vouches for an address, and an
// account somebody registered for it here without confirming it becomes the
// provider's person's — without the registrant's password, session or links —
// also while single sign-on is optional and passwords are otherwise kept.
func TestSingleSignOnTakesOverUnconfirmedRegistration(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	s.workingMailer(t)
	if err := s.settings.Set(ctx, settings.SignupMode, settings.ModeOpen, nil); err != nil {
		t.Fatal(err)
	}
	idp := newFakeIdP(t)
	key, _ := sealbox.GenerateMasterKey()
	box, _ := sealbox.New(key)
	var token string
	s.srv.SSO = &sso.Service{Pool: s.pool, Box: box, LookupTXT: func(context.Context, string) ([]string, error) {
		return []string{token}, nil
	}}
	admin := login(t, s, "admin@test.local", "admin-passwort")
	admin.expect(http.MethodPut, "/api/v1/org/sso", map[string]string{
		"issuer": idp.srv.URL, "client_id": idp.clientID, "secret": "s3cret"}, http.StatusOK)
	d := admin.expect(http.MethodPost, "/api/v1/org/sso/domains", map[string]string{"domain": "test.local"}, http.StatusCreated)
	token = d["token"].(string)
	admin.expect(http.MethodPost, "/api/v1/org/sso/domains/test.local/verify", nil, http.StatusOK)

	registrant := s.registerUnconfirmed(t, "bob@test.local", "Not Bob", "registrant-pw-1")

	bob, end := idp.signIn(t, s, "bob@test.local", map[string]any{
		"sub": "bob-sub", "email": "bob@test.local", "email_verified": true, "name": "Bob"})
	if e := ssoError(end); e != "" {
		t.Fatalf("bob's sign-in failed: %s", e)
	}
	me := bob.expect(http.MethodGet, "/api/v1/auth/me", nil, http.StatusOK)
	if me["OrgID"] != s.orgID.String() || me["Role"] != "auditor" {
		t.Fatalf("bob after single sign-on: %v", me)
	}

	s.refusedLogin(t, "bob@test.local", "registrant-pw-1")
	registrant.expect(http.MethodGet, "/api/v1/auth/me", nil, http.StatusUnauthorized)
	acc, err := accounts.New(s.pool).ByEmail(ctx, "bob@test.local")
	if err != nil || !acc.Verified() || acc.DisplayName != "Bob" {
		t.Fatalf("taken-over account: %+v %v", acc, err)
	}
	var hash string
	if err := s.pool.QueryRow(ctx, `SELECT password_hash FROM accounts WHERE id=$1`, acc.ID).Scan(&hash); err != nil || hash != sso.NoPassword {
		t.Fatalf("the registrant's password survived: %q %v", hash, err)
	}

	// The provider's person keeps coming in.
	if _, end := idp.signIn(t, s, "bob@test.local", map[string]any{"sub": "bob-sub", "email": "bob@test.local"}); ssoError(end) != "" {
		t.Fatalf("bob's second sign-in failed: %s", ssoError(end))
	}
}
