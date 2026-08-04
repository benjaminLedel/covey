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
		t.Fatal("plaintext inside the ciphertext")
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
	// The AAD binds the ciphertext to org+key: under another key it must not open.
	s := newTestStore(t)
	orgID := uuid.New()
	nonce, ct, _ := s.seal(aad(orgID, nil, "zammad_token"), "super-geheim")
	if _, err := s.open("anderes_secret", aad(orgID, nil, "anderes_secret"), nonce, ct); err == nil {
		t.Fatal("row swapping must fail on the AAD")
	}
	if _, err := s.open("zammad_token", aad(uuid.New(), nil, "zammad_token"), nonce, ct); err == nil {
		t.Fatal("a foreign org must fail on the AAD")
	}
}

func TestAgentAADIsolatesScopes(t *testing.T) {
	// An agent's own secrets hang off the agent: the ciphertext must open
	// neither under the org nor under another agent.
	s := newTestStore(t)
	orgID, agentID := uuid.New(), uuid.New()
	nonce, ct, _ := s.seal(aad(orgID, &agentID, "zammad_token"), "agent-geheim")
	if got, err := s.open("zammad_token", aad(orgID, &agentID, "zammad_token"), nonce, ct); err != nil || got != "agent-geheim" {
		t.Fatalf("roundtrip failed: %v", err)
	}
	if _, err := s.open("zammad_token", aad(orgID, nil, "zammad_token"), nonce, ct); err == nil {
		t.Fatal("the org AAD must not open an agent ciphertext")
	}
	other := uuid.New()
	if _, err := s.open("zammad_token", aad(orgID, &other, "zammad_token"), nonce, ct); err == nil {
		t.Fatal("a foreign agent must fail on the AAD")
	}
}

func TestNewRejectsBadKey(t *testing.T) {
	if _, err := New(nil, "too-short"); err == nil {
		t.Fatal("an invalid master key must be rejected")
	}
	if _, err := New(nil, strings.Repeat("zz", 32)); err == nil {
		t.Fatal("a non-hex master key must be rejected")
	}
}
