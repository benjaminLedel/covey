package builtin

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	key, err := GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(nil, key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSealOpenRoundtrip(t *testing.T) {
	s := newTestStore(t)
	orgID := uuid.New()
	nonce, ct, err := s.seal(aad(orgID, nil, "zammad_token"), "super-geheim")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(ct), "super-geheim") {
		t.Fatal("Klartext im Chiffrat")
	}
	got, err := s.open("zammad_token", aad(orgID, nil, "zammad_token"), nonce, ct)
	if err != nil {
		t.Fatal(err)
	}
	if got != "super-geheim" {
		t.Fatalf("got %q", got)
	}
}

func TestOpenRejectsSwappedKey(t *testing.T) {
	// AAD bindet das Chiffrat an org+key: unter anderem Key darf es nicht aufgehen.
	s := newTestStore(t)
	orgID := uuid.New()
	nonce, ct, _ := s.seal(aad(orgID, nil, "zammad_token"), "super-geheim")
	if _, err := s.open("anderes_secret", aad(orgID, nil, "anderes_secret"), nonce, ct); err == nil {
		t.Fatal("Row-Swapping muss an der AAD scheitern")
	}
	if _, err := s.open("zammad_token", aad(uuid.New(), nil, "zammad_token"), nonce, ct); err == nil {
		t.Fatal("fremde Org muss an der AAD scheitern")
	}
}

func TestAgentAADIsolatesScopes(t *testing.T) {
	// Agent-eigene Secrets hängen am Agenten: weder unter der Org noch unter
	// einem anderen Agenten darf das Chiffrat aufgehen.
	s := newTestStore(t)
	orgID, agentID := uuid.New(), uuid.New()
	nonce, ct, _ := s.seal(aad(orgID, &agentID, "zammad_token"), "agent-geheim")
	if got, err := s.open("zammad_token", aad(orgID, &agentID, "zammad_token"), nonce, ct); err != nil || got != "agent-geheim" {
		t.Fatalf("roundtrip fehlgeschlagen: %v", err)
	}
	if _, err := s.open("zammad_token", aad(orgID, nil, "zammad_token"), nonce, ct); err == nil {
		t.Fatal("org-AAD darf agent-Chiffrat nicht öffnen")
	}
	other := uuid.New()
	if _, err := s.open("zammad_token", aad(orgID, &other, "zammad_token"), nonce, ct); err == nil {
		t.Fatal("fremder Agent muss an der AAD scheitern")
	}
}

func TestNewRejectsBadKey(t *testing.T) {
	if _, err := New(nil, "zu-kurz"); err == nil {
		t.Fatal("ungültiger Master-Key muss abgelehnt werden")
	}
	if _, err := New(nil, strings.Repeat("zz", 32)); err == nil {
		t.Fatal("nicht-hex Master-Key muss abgelehnt werden")
	}
}
