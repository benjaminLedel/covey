package daemon

import (
	"context"
	"encoding/json"
	"testing"
)

// newTestProxy builds an actionProxy backed by a Client whose secret cache is
// pre-populated — substituteSecrets then resolves entirely from RAM, no
// WebSocket round-trip needed (mirrors how Client.secret() itself short-
// circuits once a secret has been granted once, see credential()/secret()).
func newTestProxy(secrets map[string]InjectSecret) *actionProxy {
	return &actionProxy{
		taskID: "task-1",
		client: &Client{secrets: secrets, pending: map[string]chan Message{}},
	}
}

func TestSubstituteSecretsReplacesPlaceholder(t *testing.T) {
	p := newTestProxy(map[string]InjectSecret{
		"misavo_staging_pass": {Granted: true, Value: "s3cret!"},
	})
	in := json.RawMessage(`{"selector":"#password","text":"{{secret:misavo_staging_pass}}"}`)

	out, err := p.substituteSecrets(context.Background(), in)
	if err != nil {
		t.Fatalf("substituteSecrets: %v", err)
	}
	var got struct{ Text string }
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("result is not valid JSON: %v (%s)", err, out)
	}
	if got.Text != "s3cret!" {
		t.Fatalf("text = %q, want %q", got.Text, "s3cret!")
	}
}

func TestSubstituteSecretsNoPlaceholderIsNoop(t *testing.T) {
	p := newTestProxy(nil)
	in := json.RawMessage(`{"selector":"#password","text":"plain text"}`)

	out, err := p.substituteSecrets(context.Background(), in)
	if err != nil {
		t.Fatalf("substituteSecrets: %v", err)
	}
	// Same bytes back — no secret was requested (nil cache would panic/deny a
	// lookup if one had been attempted), confirming the short-circuit.
	if string(out) != string(in) {
		t.Fatalf("params changed without a placeholder: got %s", out)
	}
}

func TestSubstituteSecretsMultipleDistinctKeys(t *testing.T) {
	p := newTestProxy(map[string]InjectSecret{
		"misavo_staging_user": {Granted: true, Value: "anika.otto@buergerhaus-krebeck.de"},
		"misavo_staging_pass": {Granted: true, Value: "start123"},
	})
	in := json.RawMessage(`{"steps":["{{secret:misavo_staging_user}}","{{secret:misavo_staging_pass}}"]}`)

	out, err := p.substituteSecrets(context.Background(), in)
	if err != nil {
		t.Fatalf("substituteSecrets: %v", err)
	}
	var got struct{ Steps []string }
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("result is not valid JSON: %v (%s)", err, out)
	}
	if len(got.Steps) != 2 || got.Steps[0] != "anika.otto@buergerhaus-krebeck.de" || got.Steps[1] != "start123" {
		t.Fatalf("steps = %v, want both secrets resolved", got.Steps)
	}
}

func TestSubstituteSecretsUngrantedIsError(t *testing.T) {
	p := newTestProxy(map[string]InjectSecret{
		// Not Granted: the cache never stores a denial (see Client.secret), so
		// this only models "not yet cached" — the real deny path goes through
		// the WebSocket. What matters here is that substituteSecrets propagates
		// an error instead of silently typing an empty/placeholder string.
	})
	in := json.RawMessage(`{"text":"{{secret:unknown_key}}"}`)

	if _, err := p.substituteSecrets(context.Background(), in); err == nil {
		t.Fatal("expected an error for an unresolvable secret, got nil")
	}
}

func TestJSONStringEscapeHandlesQuotesAndBackslashes(t *testing.T) {
	esc := jsonStringEscape(`p@ss"w\ord`)
	full := json.RawMessage(`{"text":"` + esc + `"}`)
	var got struct{ Text string }
	if err := json.Unmarshal(full, &got); err != nil {
		t.Fatalf("escaped value does not round-trip as JSON: %v (%s)", err, full)
	}
	if got.Text != `p@ss"w\ord` {
		t.Fatalf("got %q, want %q", got.Text, `p@ss"w\ord`)
	}
}
