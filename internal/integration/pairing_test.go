package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// redeem is the app's side of a pairing: no cookie, no key — the code is the
// badge.
func redeem(t *testing.T, s *stack, code, device string) (int, map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"code": code, "device": device})
	resp, err := http.Post(s.http.URL+"/api/v1/auth/pair", "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// TestPairingHandsTheAppAKey walks the QR pairing (#330): the session mints a
// code, the app exchanges it once for a key of its own seat, the page sees
// with which device, and nothing about the code works a second time.
func TestPairingHandsTheAppAKey(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")

	created := admin.expect(http.MethodPost, "/api/v1/auth/pairings", nil, http.StatusCreated)
	code, _ := created["code"].(string)
	id, _ := created["id"].(string)
	if !strings.HasPrefix(code, "coveypair_") || id == "" {
		t.Fatalf("a pairing carries a code and an id: %v", created)
	}
	if st := admin.expect(http.MethodGet, "/api/v1/auth/pairings/"+id, nil, http.StatusOK); st["redeemed_at"] != nil {
		t.Fatalf("a fresh code is not redeemed: %v", st)
	}

	status, out := redeem(t, s, code, "Ada's iPhone\x07")
	if status != http.StatusCreated {
		t.Fatalf("redeem: %d %v", status, out)
	}
	token, _ := out["token"].(string)
	if out["name"] != "Mobile app · Ada's iPhone" {
		t.Fatalf("the key is named after the device, without control characters: %v", out["name"])
	}

	// The key works, and it is the admin's seat.
	resp := bearer(t, s, token, http.MethodGet, "/api/v1/auth/me", nil)
	var me map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&me)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || me["Email"] != "admin@test.local" {
		t.Fatalf("the paired key must work as the admin's seat: %d %v", resp.StatusCode, me)
	}

	// The page that showed the code learns with which device it was used.
	st := admin.expect(http.MethodGet, "/api/v1/auth/pairings/"+id, nil, http.StatusOK)
	if st["redeemed_at"] == nil || st["device"] != "Ada's iPhone" {
		t.Fatalf("the pairing says it was used, and by whom: %v", st)
	}
	// And the key is in the list, where it can be revoked.
	found := false
	for _, k := range admin.expectList(http.MethodGet, "/api/v1/auth/api-keys", nil, http.StatusOK) {
		found = found || k["name"] == "Mobile app · Ada's iPhone"
	}
	if !found {
		t.Fatal("the paired key must be listed beside the other keys")
	}

	// Single use.
	if status, _ := redeem(t, s, code, "second phone"); status != http.StatusUnauthorized {
		t.Fatalf("a used code must not work again: %d", status)
	}
	// Unknown, and not even shaped like a code: the same answer.
	if status, _ := redeem(t, s, "coveypair_nope", "x"); status != http.StatusUnauthorized {
		t.Fatalf("an unknown code: %d", status)
	}
	if status, _ := redeem(t, s, "covey_looks_like_a_key", "x"); status != http.StatusUnauthorized {
		t.Fatalf("a key is not a pairing code: %d", status)
	}

	// A key must not mint codes: a leaked key would otherwise pair devices of
	// its own (sessionOnly, like minting a key).
	if resp := bearer(t, s, token, http.MethodPost, "/api/v1/auth/pairings", nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("an API key must not create a pairing: %d", resp.StatusCode)
	}
}

// TestPairingExpiresAndIsRedeemedOnce: an expired code is refused, and two
// phones scanning the same code at the same moment get one key between them.
func TestPairingExpiresAndIsRedeemedOnce(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")

	old := admin.expect(http.MethodPost, "/api/v1/auth/pairings", nil, http.StatusCreated)
	if _, err := s.pool.Exec(context.Background(),
		`UPDATE device_pairings SET expires_at = now() - interval '1 second' WHERE id=$1`, old["id"]); err != nil {
		t.Fatal(err)
	}
	if status, _ := redeem(t, s, old["code"].(string), "late"); status != http.StatusUnauthorized {
		t.Fatalf("an expired code must be refused: %d", status)
	}

	code := admin.expect(http.MethodPost, "/api/v1/auth/pairings", nil, http.StatusCreated)["code"].(string)
	var wg sync.WaitGroup
	var mu sync.Mutex
	won := 0
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if status, _ := redeem(t, s, code, "racer"); status == http.StatusCreated {
				mu.Lock()
				won++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if won != 1 {
		t.Fatalf("one code, one key — %d redemptions succeeded", won)
	}
}
