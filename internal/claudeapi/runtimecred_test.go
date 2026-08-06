package claudeapi

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"covey/internal/secrets"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		key  string
		want RuntimeKind
	}{
		{"anthropic_api_key", KindAPIKey},
		{"claude_code_oauth_token", KindOAuth},
		{"anthropic_api_key_team_a", KindAPIKey},
		{"claude_code_oauth_token_team_a", KindOAuth},
		{"anthropic_api_key_cost_centre_b", KindAPIKey},
		// Ohne Trennstrich ist es ein anderer Name, kein Suffix — sonst wuerde
		// jedes zufaellig aehnliche Secret zum Laufzeit-Credential.
		{"anthropic_api_keyring", KindNone},
		{"claude_code_oauth_tokens", KindNone},
		{"zammad_token", KindNone},
		{"", KindNone},
		// Der Praefix muss am Anfang stehen.
		{"old_anthropic_api_key", KindNone},
	}
	for _, c := range cases {
		if got := Classify(c.key); got != c.want {
			t.Errorf("Classify(%q) = %v, want %v", c.key, got, c.want)
		}
	}
}

func TestRuntimeKindMapping(t *testing.T) {
	if got := KindAPIKey.EnvVar(); got != "ANTHROPIC_API_KEY" {
		t.Errorf("KindAPIKey.EnvVar() = %q", got)
	}
	if got := KindOAuth.EnvVar(); got != "CLAUDE_CODE_OAUTH_TOKEN" {
		t.Errorf("KindOAuth.EnvVar() = %q", got)
	}
	if KindNone.EnvVar() != "" || KindNone.Stem() != "" || KindNone.String() != "" {
		t.Error("KindNone must map to nothing")
	}
	if KindAPIKey.OAuth() || !KindOAuth.OAuth() {
		t.Error("only the subscription token authenticates as bearer")
	}
	if KindAPIKey.Stem() != KeyAPIKey || KindOAuth.Stem() != KeyOAuth {
		t.Error("Stem must give back the classic name")
	}
	if KindAPIKey.String() != "api_key" || KindOAuth.String() != "oauth" {
		t.Error("unexpected JSON identifier")
	}
}

func TestRank(t *testing.T) {
	// Bewusst durcheinander: eigene vor organisationsweiten, API-Key vor
	// Abo-Token, dann Name aufsteigend.
	in := []Candidate{
		{Key: "claude_code_oauth_token_z", Kind: KindOAuth},
		{Key: "anthropic_api_key_b", Kind: KindAPIKey},
		{Key: "anthropic_api_key_a", Kind: KindAPIKey},
		{Key: "claude_code_oauth_token_own", Kind: KindOAuth, Owned: true},
		{Key: "anthropic_api_key_own", Kind: KindAPIKey, Owned: true},
	}
	Rank(in)
	want := []string{
		"anthropic_api_key_own",       // eigen + API-Key
		"claude_code_oauth_token_own", // eigen + Abo
		"anthropic_api_key_a",         // Org + API-Key, Name aufsteigend
		"anthropic_api_key_b",
		"claude_code_oauth_token_z",
	}
	for i, w := range want {
		if in[i].Key != w {
			t.Fatalf("Rank position %d = %q, want %q (full: %+v)", i, in[i].Key, w, in)
		}
	}
}

// fakeStore ist der schmale Teil von secrets.Store, den ResolveAgent und
// ResolveOrgNamed anfassen. Alles andere darf panisch werden — wird es
// aufgerufen, ist die Auswahl weiter gewandert als gedacht.
type fakeStore struct {
	secrets.Store
	org   map[string]string               // organisationsweit
	agent map[uuid.UUID]map[string]string // agenteneigen
	// assigned steuert, welche Org-Secrets den Agenten erreichen.
	assigned map[string]bool
}

func (f *fakeStore) Get(_ context.Context, _ uuid.UUID, key string) (string, error) {
	if v, ok := f.org[key]; ok {
		return v, nil
	}
	return "", secrets.ErrNotFound
}

func (f *fakeStore) Keys(_ context.Context, _ uuid.UUID) ([]string, error) {
	var out []string
	for k := range f.org {
		out = append(out, k)
	}
	return out, nil
}

func (f *fakeStore) Resolve(_ context.Context, _, agentID uuid.UUID, key string) (string, error) {
	if v, ok := f.agent[agentID][key]; ok {
		return v, nil
	}
	if v, ok := f.org[key]; ok && f.assigned[key] {
		return v, nil
	}
	return "", secrets.ErrNotFound
}

func (f *fakeStore) ResolvableKeys(_ context.Context, _, agentID uuid.UUID) ([]secrets.ResolvableKey, error) {
	var out []secrets.ResolvableKey
	seen := map[string]bool{}
	for k := range f.agent[agentID] {
		out = append(out, secrets.ResolvableKey{Key: k, Owned: true})
		seen[k] = true
	}
	for k := range f.org {
		if f.assigned[k] && !seen[k] {
			out = append(out, secrets.ResolvableKey{Key: k, Owned: false})
		}
	}
	return out, nil
}

func TestResolveAgentPinned(t *testing.T) {
	ctx := context.Background()
	orgID, a := uuid.New(), uuid.New()
	st := &fakeStore{
		org:      map[string]string{"claude_code_oauth_token_a": "tok-a", "claude_code_oauth_token_b": " tok-b\n"},
		assigned: map[string]bool{"claude_code_oauth_token_a": true, "claude_code_oauth_token_b": true},
	}

	res, err := ResolveAgent(ctx, st, orgID, a, "claude_code_oauth_token_b")
	if err != nil {
		t.Fatalf("pinned: %v", err)
	}
	if res.Key != "claude_code_oauth_token_b" || res.Kind != KindOAuth {
		t.Errorf("wrong credential: %+v", res)
	}
	if res.Value != "tok-b" {
		t.Errorf("whitespace from copy-and-paste not trimmed: %q", res.Value)
	}
}

func TestResolveAgentPinnedMissingDoesNotFallBack(t *testing.T) {
	ctx := context.Background()
	orgID, a := uuid.New(), uuid.New()
	// Der klassische Name liegt bereit — genau darauf darf NICHT
	// zurueckgefallen werden, sonst belastet der Lauf ein fremdes Konto.
	st := &fakeStore{
		org:      map[string]string{"anthropic_api_key": "sk-fallback"},
		assigned: map[string]bool{"anthropic_api_key": true},
	}
	if _, err := ResolveAgent(ctx, st, orgID, a, "claude_code_oauth_token_gone"); err != ErrPinnedMissing {
		t.Fatalf("expected ErrPinnedMissing, got %v", err)
	}
	// Ein festgelegter Name, der gar kein Laufzeit-Credential ist, ebenso.
	if _, err := ResolveAgent(ctx, st, orgID, a, "zammad_token"); err != ErrPinnedMissing {
		t.Fatalf("non-credential pin: expected ErrPinnedMissing, got %v", err)
	}
}

func TestResolveAgentFallbackOrder(t *testing.T) {
	ctx := context.Background()
	orgID, a := uuid.New(), uuid.New()

	// 1. Die klassischen Namen zuerst — hier haengt die Rueckwaertskompatibilitaet.
	st := &fakeStore{
		org: map[string]string{
			"anthropic_api_key":         "classic-api",
			"claude_code_oauth_token":   "classic-oauth",
			"anthropic_api_key_aaaaaaa": "suffixed",
		},
		assigned: map[string]bool{
			"anthropic_api_key": true, "claude_code_oauth_token": true, "anthropic_api_key_aaaaaaa": true,
		},
	}
	res, err := ResolveAgent(ctx, st, orgID, a, "")
	if err != nil || res.Key != "anthropic_api_key" {
		t.Fatalf("classic API key must win: %+v (%v)", res, err)
	}

	// 2. Ohne die klassischen: eigenes vor Org, API-Key vor Abo.
	st = &fakeStore{
		org:      map[string]string{"claude_code_oauth_token_org": "org-oauth"},
		agent:    map[uuid.UUID]map[string]string{a: {"anthropic_api_key_own": "own-api"}},
		assigned: map[string]bool{"claude_code_oauth_token_org": true},
	}
	res, err = ResolveAgent(ctx, st, orgID, a, "")
	if err != nil || res.Key != "anthropic_api_key_own" || res.Value != "own-api" {
		t.Fatalf("agent-owned API key must win: %+v (%v)", res, err)
	}

	// 3. Ein nicht zugewiesenes Org-Secret erreicht niemanden.
	st = &fakeStore{
		org:      map[string]string{"claude_code_oauth_token_x": "unreachable"},
		assigned: map[string]bool{},
	}
	if _, err := ResolveAgent(ctx, st, orgID, a, ""); err == nil {
		t.Fatal("unassigned org secret must not reach the agent")
	}
}

func TestResolveOrgNamed(t *testing.T) {
	ctx := context.Background()
	orgID := uuid.New()

	// Klassischer API-Key schlaegt klassisches Abo-Token.
	st := &fakeStore{org: map[string]string{"anthropic_api_key": "api", "claude_code_oauth_token": "oauth"}}
	key, cred, oauth, ok := ResolveOrgNamed(ctx, st, orgID)
	if !ok || key != "anthropic_api_key" || cred != "api" || oauth {
		t.Fatalf("classic order: key=%q cred=%q oauth=%v ok=%v", key, cred, oauth, ok)
	}

	// Nur suffigierte: API-Key vor Abo-Token, dann Name aufsteigend.
	st = &fakeStore{org: map[string]string{
		"claude_code_oauth_token_a": "o-a",
		"anthropic_api_key_z":       "a-z",
		"anthropic_api_key_m":       "a-m",
	}}
	key, cred, oauth, ok = ResolveOrgNamed(ctx, st, orgID)
	if !ok || key != "anthropic_api_key_m" || cred != "a-m" || oauth {
		t.Fatalf("suffixed order: key=%q cred=%q oauth=%v ok=%v", key, cred, oauth, ok)
	}

	// Gar keines: nicht verfuegbar, statt irgendetwas zurueckzugeben.
	st = &fakeStore{org: map[string]string{"zammad_token": "nope"}}
	if _, _, _, ok := ResolveOrgNamed(ctx, st, orgID); ok {
		t.Fatal("a target-system secret must not count as a runtime credential")
	}
}
