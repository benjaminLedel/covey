package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"covey/internal/accounts"
	"covey/internal/settings"
	"covey/internal/waitlist"
)

// postJSON addresses the instance WITHOUT a session — the only way that counts
// here: whoever registers has no account and no cookie yet.
func (s *stack) postJSON(t *testing.T, path string, body any) *http.Response {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, s.http.URL+path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// Self-registration via a waitlist code (FR-002, P4).
//
// The actual subject is not the form but the booking: account and redemption
// come into being in ONE transaction. A code spent for an account that never
// came into being is a lost use; an account from an already spent code is a
// gate that did not hold.

func TestRegistrierungMitCode(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	settingsStore := s.settings
	codes := waitlist.New(s.pool)

	// Closed is the shipping state: the endpoint then does not exist for the
	// outside world.
	res := s.postJSON(t, "/api/v1/public/signup", map[string]any{
		"code": "COVEY-4K7MQ-P2D9X", "email": "erika@example.de",
		"display_name": "Erika", "password": "hinreichend-lang",
	})
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("geschlossene Instanz antwortet %d, erwartet 404", res.StatusCode)
	}
	res.Body.Close()

	s.workingMailer(t)
	if err := settingsStore.Set(ctx, settings.SignupMode, settings.ModeWaitlist, nil); err != nil {
		t.Fatal(err)
	}

	// Without a valid code nobody gets through.
	res = s.postJSON(t, "/api/v1/public/signup", map[string]any{
		"code": "COVEY-4K7MQ-P2D9X", "email": "erika@example.de",
		"display_name": "Erika", "password": "hinreichend-lang",
	})
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("unbekannter Code ergibt %d, erwartet 400", res.StatusCode)
	}
	res.Body.Close()

	code, err := codes.Create(ctx, waitlist.Options{Label: "Test", MaxUses: 1})
	if err != nil {
		t.Fatal(err)
	}

	res = s.postJSON(t, "/api/v1/public/signup", map[string]any{
		"code": code, "email": "Erika@Example.de",
		"display_name": "Erika Musterfrau", "password": "hinreichend-lang",
	})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("Registrierung ergibt %d, erwartet 201", res.StatusCode)
	}
	res.Body.Close()

	acc, err := accounts.New(s.pool).ByEmail(ctx, "erika@example.de")
	if err != nil {
		t.Fatalf("Konto nicht angelegt: %v", err)
	}
	if acc.DisplayName != "Erika Musterfrau" {
		t.Errorf("Name = %q", acc.DisplayName)
	}
	// The address is NOT confirmed: that is what the mail that just went out is
	// for (#168). What the link does with it stands in verify_test.go.
	if acc.Verified() {
		t.Error("Adresse gilt als bestätigt, obwohl sie niemand bestätigt hat")
	}
	// The organisation is NOT chosen here: the account stands for itself, until
	// its owner joins or founds one.
	var humans int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM humans WHERE email=$1", "erika@example.de").Scan(&humans); err != nil {
		t.Fatal(err)
	}
	if humans != 0 {
		t.Errorf("Registrierung hat %d Mitgliedschaften angelegt — erwartet 0", humans)
	}

	// The same code a second time: spent.
	res = s.postJSON(t, "/api/v1/public/signup", map[string]any{
		"code": code, "email": "otto@example.de",
		"display_name": "Otto", "password": "hinreichend-lang",
	})
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("verbrauchter Code ergibt %d, erwartet 400", res.StatusCode)
	}
	res.Body.Close()

	// The same address a second time: that is what whoever registers learns —
	// otherwise he waits for a mail that never comes.
	code2, err := codes.Create(ctx, waitlist.Options{MaxUses: 1})
	if err != nil {
		t.Fatal(err)
	}
	res = s.postJSON(t, "/api/v1/public/signup", map[string]any{
		"code": code2, "email": "erika@example.de",
		"display_name": "Erika", "password": "hinreichend-lang",
	})
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("vergebene Adresse ergibt %d, erwartet 409", res.StatusCode)
	}
	res.Body.Close()

	// And the code from the failed attempt is NOT spent: the transaction was
	// rolled back.
	liste, err := codes.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range liste {
		if c.Label == "" && c.UsedCount != 0 {
			t.Errorf("Code aus dem gescheiterten Versuch steht auf %d Nutzungen — die Buchung hätte zurückgerollt werden müssen", c.UsedCount)
		}
	}
}

// The case the transaction is there for: two requests in the same second, one
// code with one use. Exactly one may get through.
func TestEinmalCodeHaeltGleichzeitigkeitStand(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	s.workingMailer(t)
	if err := s.settings.Set(ctx, settings.SignupMode, settings.ModeWaitlist, nil); err != nil {
		t.Fatal(err)
	}
	codes := waitlist.New(s.pool)
	code, err := codes.Create(ctx, waitlist.Options{Label: "gleichzeitig", MaxUses: 1})
	if err != nil {
		t.Fatal(err)
	}

	const versuche = 6
	var wg sync.WaitGroup
	ergebnis := make([]int, versuche)
	start := make(chan struct{})
	for i := range versuche {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			res := s.postJSON(t, "/api/v1/public/signup", map[string]any{
				"code": code, "email": adresse(i), "display_name": "Test", "password": "hinreichend-lang",
			})
			ergebnis[i] = res.StatusCode
			res.Body.Close()
		}(i)
	}
	close(start)
	wg.Wait()

	erfolge := 0
	for _, code := range ergebnis {
		if code == http.StatusCreated {
			erfolge++
		}
	}
	if erfolge != 1 {
		t.Fatalf("%d von %d Anfragen kamen durch — ein Einmal-Code darf genau einmal gelten (%v)", erfolge, versuche, ergebnis)
	}

	// Only the accounts from this registration are counted — the stack brings
	// the admin account along, and a test that counts it too measures the stack.
	var konten int
	if err := s.pool.QueryRow(ctx,
		"SELECT count(*) FROM accounts WHERE email LIKE '%@example.de'").Scan(&konten); err != nil {
		t.Fatal(err)
	}
	if konten != 1 {
		t.Errorf("%d Konten entstanden, erwartet 1", konten)
	}
}

func adresse(i int) string {
	return string(rune('a'+i)) + "@example.de"
}

// An expired code is no longer valid, and a revoked one immediately so — the
// server says both differently, because there are different ways out.
func TestAbgelaufenUndZurueckgezogen(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	s.workingMailer(t)
	if err := s.settings.Set(ctx, settings.SignupMode, settings.ModeWaitlist, nil); err != nil {
		t.Fatal(err)
	}
	codes := waitlist.New(s.pool)

	gestern := time.Now().Add(-24 * time.Hour)
	abgelaufen, err := codes.Create(ctx, waitlist.Options{Label: "alt", ExpiresAt: &gestern})
	if err != nil {
		t.Fatal(err)
	}
	res := s.postJSON(t, "/api/v1/public/signup", map[string]any{
		"code": abgelaufen, "email": "a@example.de", "display_name": "A", "password": "hinreichend-lang",
	})
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("abgelaufener Code ergibt %d, erwartet 400", res.StatusCode)
	}
	res.Body.Close()

	zurueck, err := codes.Create(ctx, waitlist.Options{Label: "zurueckgezogen"})
	if err != nil {
		t.Fatal(err)
	}
	kanonisch, _ := waitlist.Normalize(zurueck)
	if err := codes.Revoke(ctx, waitlist.Hash(kanonisch)[:12]); err != nil {
		t.Fatal(err)
	}
	res = s.postJSON(t, "/api/v1/public/signup", map[string]any{
		"code": zurueck, "email": "b@example.de", "display_name": "B", "password": "hinreichend-lang",
	})
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("zurückgezogener Code ergibt %d, erwartet 400", res.StatusCode)
	}
	res.Body.Close()

	var konten int
	if err := s.pool.QueryRow(ctx,
		"SELECT count(*) FROM accounts WHERE email LIKE '%@example.de'").Scan(&konten); err != nil {
		t.Fatal(err)
	}
	if konten != 0 {
		t.Errorf("%d Konten aus ungültigen Codes entstanden", konten)
	}
}
