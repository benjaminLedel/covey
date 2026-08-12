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

// postJSON spricht die Instanz OHNE Sitzung an — der einzige Weg, der hier
// zählt: wer sich registriert, hat noch kein Konto und kein Cookie.
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

// Selbstregistrierung über einen Wartelisten-Code (FR-002, P4).
//
// Der eigentliche Gegenstand ist nicht das Formular, sondern die Buchung: Konto
// und Einlösung entstehen in EINER Transaktion. Ein Code, der für ein nie
// entstandenes Konto verbraucht wurde, ist eine verlorene Nutzung; ein Konto
// aus einem schon verbrauchten Code ist ein Tor, das nicht gehalten hat.

func TestRegistrierungMitCode(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	settingsStore := settings.New(s.pool)
	codes := waitlist.New(s.pool)

	// Geschlossen ist der Auslieferungszustand: der Endpunkt gibt es dann für
	// die Außenwelt nicht.
	res := s.postJSON(t, "/api/v1/public/signup", map[string]any{
		"code": "COVEY-4K7MQ-P2D9X", "email": "erika@example.de",
		"display_name": "Erika", "password": "hinreichend-lang",
	})
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("geschlossene Instanz antwortet %d, erwartet 404", res.StatusCode)
	}
	res.Body.Close()

	if err := settingsStore.Set(ctx, settings.SignupMode, settings.ModeWaitlist, nil); err != nil {
		t.Fatal(err)
	}

	// Ohne gültigen Code kommt niemand durch.
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
	// Ohne Mailversand gilt die Adresse sofort als bestätigt — eine
	// Bestätigung, die niemand verschicken kann, wäre ein totes Konto.
	if !acc.Verified() {
		t.Error("Konto ohne Mailer muss sofort bestätigt sein")
	}
	// Die Organisation wird hier NICHT gewählt: das Konto steht für sich, bis
	// sein Inhaber beitritt oder gründet.
	var humans int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM humans WHERE email=$1", "erika@example.de").Scan(&humans); err != nil {
		t.Fatal(err)
	}
	if humans != 0 {
		t.Errorf("Registrierung hat %d Mitgliedschaften angelegt — erwartet 0", humans)
	}

	// Derselbe Code ein zweites Mal: verbraucht.
	res = s.postJSON(t, "/api/v1/public/signup", map[string]any{
		"code": code, "email": "otto@example.de",
		"display_name": "Otto", "password": "hinreichend-lang",
	})
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("verbrauchter Code ergibt %d, erwartet 400", res.StatusCode)
	}
	res.Body.Close()

	// Dieselbe Adresse ein zweites Mal: das erfährt, wer registriert — sonst
	// wartet er auf eine Mail, die nie kommt.
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

	// Und der Code aus dem gescheiterten Versuch ist NICHT verbraucht: die
	// Transaktion ist zurückgerollt.
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

// Der Fall, für den die Transaktion da ist: zwei Anfragen in derselben
// Sekunde, ein Code mit einer Nutzung. Genau eine darf durchkommen.
func TestEinmalCodeHaeltGleichzeitigkeitStand(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	if err := settings.New(s.pool).Set(ctx, settings.SignupMode, settings.ModeWaitlist, nil); err != nil {
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
				"code": code, "email": mail(i), "display_name": "Test", "password": "hinreichend-lang",
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

	// Gezählt werden nur die Konten aus dieser Registrierung — der Stack bringt
	// das Admin-Konto mit, und ein Test, der das mitzählt, misst den Stack.
	var konten int
	if err := s.pool.QueryRow(ctx,
		"SELECT count(*) FROM accounts WHERE email LIKE '%@example.de'").Scan(&konten); err != nil {
		t.Fatal(err)
	}
	if konten != 1 {
		t.Errorf("%d Konten entstanden, erwartet 1", konten)
	}
}

func mail(i int) string {
	return string(rune('a'+i)) + "@example.de"
}

// Ein abgelaufener Code gilt nicht mehr, und ein zurückgezogener sofort nicht
// mehr — beides sagt der Server verschieden, weil es verschiedene Auswege gibt.
func TestAbgelaufenUndZurueckgezogen(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	if err := settings.New(s.pool).Set(ctx, settings.SignupMode, settings.ModeWaitlist, nil); err != nil {
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
