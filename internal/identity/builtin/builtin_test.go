package builtin

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/internal/identity"
)

func newTestProvider(t *testing.T) *Provider {
	t.Helper()
	p, err := New(nil) // the pool is only needed for AuthenticateHuman
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAgentTokenRoundtrip(t *testing.T) {
	p := newTestProvider(t)
	agentID := uuid.New()
	tok, err := p.IssueAgentToken(context.Background(), agentID,
		identity.Scope{Audience: "daemon"}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.VerifyAgentToken(context.Background(), tok.Value, "daemon")
	if err != nil {
		t.Fatal(err)
	}
	if got != agentID {
		t.Fatalf("agentID: got %s, want %s", got, agentID)
	}
}

func TestAgentTokenWrongAudience(t *testing.T) {
	p := newTestProvider(t)
	tok, _ := p.IssueAgentToken(context.Background(), uuid.New(),
		identity.Scope{Audience: "daemon"}, time.Minute)
	if _, err := p.VerifyAgentToken(context.Background(), tok.Value, "zammad"); err == nil {
		t.Fatal("a token with the wrong audience must be rejected")
	}
}

func TestAgentTokenExpired(t *testing.T) {
	p := newTestProvider(t)
	tok, _ := p.IssueAgentToken(context.Background(), uuid.New(),
		identity.Scope{Audience: "daemon"}, -time.Minute)
	if _, err := p.VerifyAgentToken(context.Background(), tok.Value, "daemon"); err == nil {
		t.Fatal("an expired token must be rejected")
	}
}

func TestAgentTokenTampered(t *testing.T) {
	p := newTestProvider(t)
	tok, _ := p.IssueAgentToken(context.Background(), uuid.New(),
		identity.Scope{Audience: "daemon"}, time.Minute)
	tampered := tok.Value[:len(tok.Value)-4] + "AAAA"
	if _, err := p.VerifyAgentToken(context.Background(), tampered, "daemon"); err == nil {
		t.Fatal("a tampered token must be rejected")
	}
}

func TestPasswordHashRoundtrip(t *testing.T) {
	hash, err := HashPassword("geheim123")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Fatalf("expected PHC format, got %q", hash)
	}
	if !VerifyPassword("geheim123", hash) {
		t.Fatal("the correct password must verify")
	}
	if VerifyPassword("wrong", hash) {
		t.Fatal("a wrong password must not verify")
	}
	if VerifyPassword("geheim123", "broken") {
		t.Fatal("a broken hash must not verify")
	}
}
