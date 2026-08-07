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
	nonce, ct, err := s.seal(aad(orgID, nil, "zammad_token", 0), "super-geheim")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(ct), "super-geheim") {
		t.Fatal("plaintext inside the ciphertext")
	}
	got, err := s.open("zammad_token", aad(orgID, nil, "zammad_token", 0), nonce, ct)
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
	nonce, ct, _ := s.seal(aad(orgID, nil, "zammad_token", 0), "super-geheim")
	if _, err := s.open("anderes_secret", aad(orgID, nil, "anderes_secret", 0), nonce, ct); err == nil {
		t.Fatal("row swapping must fail on the AAD")
	}
	if _, err := s.open("zammad_token", aad(uuid.New(), nil, "zammad_token", 0), nonce, ct); err == nil {
		t.Fatal("a foreign org must fail on the AAD")
	}
}

func TestAgentAADIsolatesScopes(t *testing.T) {
	// An agent's own secrets hang off the agent: the ciphertext must open
	// neither under the org nor under another agent.
	s := newTestStore(t)
	orgID, agentID := uuid.New(), uuid.New()
	nonce, ct, _ := s.seal(aad(orgID, &agentID, "zammad_token", 0), "agent-geheim")
	if got, err := s.open("zammad_token", aad(orgID, &agentID, "zammad_token", 0), nonce, ct); err != nil || got != "agent-geheim" {
		t.Fatalf("roundtrip failed: %v", err)
	}
	if _, err := s.open("zammad_token", aad(orgID, nil, "zammad_token", 0), nonce, ct); err == nil {
		t.Fatal("the org AAD must not open an agent ciphertext")
	}
	other := uuid.New()
	if _, err := s.open("zammad_token", aad(orgID, &other, "zammad_token", 0), nonce, ct); err == nil {
		t.Fatal("a foreign agent must fail on the AAD")
	}
}

func TestSlotAADIsolatesPoolValues(t *testing.T) {
	// Within one pool the values must not be swappable either — otherwise a row
	// swap inside the same key would go unnoticed, which is exactly what the
	// AAD is there to prevent.
	s := newTestStore(t)
	orgID := uuid.New()
	nonce, ct, _ := s.seal(aad(orgID, nil, "claude_code_oauth_token", 1), "wert-eins")
	if got, err := s.open("claude_code_oauth_token", aad(orgID, nil, "claude_code_oauth_token", 1), nonce, ct); err != nil || got != "wert-eins" {
		t.Fatalf("roundtrip failed: %q, %v", got, err)
	}
	if _, err := s.open("claude_code_oauth_token", aad(orgID, nil, "claude_code_oauth_token", 2), nonce, ct); err == nil {
		t.Fatal("another slot must fail on the AAD")
	}
	if _, err := s.open("claude_code_oauth_token", aad(orgID, nil, "claude_code_oauth_token", 0), nonce, ct); err == nil {
		t.Fatal("slot 0 must fail on the AAD")
	}
}

func TestSlotZeroAADStaysCompatible(t *testing.T) {
	// Slot 0 carries no suffix on purpose: everything written before the pools
	// is slot 0, and its ciphertext has to keep opening after the migration.
	orgID, agentID := uuid.New(), uuid.New()
	if got, want := string(aad(orgID, nil, "zammad_token", 0)), orgID.String()+"/zammad_token"; got != want {
		t.Fatalf("org AAD of slot 0 changed: %q", got)
	}
	want := orgID.String() + "/" + agentID.String() + "/zammad_token"
	if got := string(aad(orgID, &agentID, "zammad_token", 0)); got != want {
		t.Fatalf("agent AAD of slot 0 changed: %q", got)
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
