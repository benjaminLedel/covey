package integration

import (
	"context"
	"errors"
	"testing"

	"covey/internal/secrets"
)

// TestSecretScoping prüft die Auflösungsregeln des SecretStores gegen echtes
// Postgres: ohne Zuweisung erreicht ein Org-Secret niemanden → explizite
// Zuweisung öffnet den Zugriff → agent-eigenes Secret hat Vorrang.
func TestSecretScoping(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	alice := s.newSupportAgent("alice")
	bob := s.newSupportAgent("bob")

	// Org-weites Secret ohne Zuweisung: erreicht keinen Agenten.
	if err := s.secrets.Put(ctx, s.orgID, "zammad_token", "org-token"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.secrets.Resolve(ctx, s.orgID, alice.ID, "zammad_token"); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("alice ohne Zuweisung darf nichts auflösen: %v", err)
	}
	if _, err := s.secrets.Resolve(ctx, s.orgID, bob.ID, "zammad_token"); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("bob ohne Zuweisung darf nichts auflösen: %v", err)
	}

	// Explizite Zuweisung an alice: nur sie bekommt das Secret.
	if err := s.secrets.Assign(ctx, s.orgID, "zammad_token", alice.ID); err != nil {
		t.Fatal(err)
	}
	if v, err := s.secrets.Resolve(ctx, s.orgID, alice.ID, "zammad_token"); err != nil || v != "org-token" {
		t.Fatalf("alice mit Zuweisung: %q, %v", v, err)
	}
	if _, err := s.secrets.Resolve(ctx, s.orgID, bob.ID, "zammad_token"); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("bob darf ein alice-zugewiesenes Secret nicht auflösen: %v", err)
	}

	// Agent-eigenes Secret bei bob: hat Vorrang und gilt nur für ihn.
	if err := s.secrets.PutAgent(ctx, s.orgID, bob.ID, "zammad_token", "bob-token"); err != nil {
		t.Fatal(err)
	}
	if v, err := s.secrets.Resolve(ctx, s.orgID, bob.ID, "zammad_token"); err != nil || v != "bob-token" {
		t.Fatalf("bobs agent-eigenes Secret: %q, %v", v, err)
	}
	if v, err := s.secrets.Resolve(ctx, s.orgID, alice.ID, "zammad_token"); err != nil || v != "org-token" {
		t.Fatalf("alice sieht weiterhin das Org-Secret: %q, %v", v, err)
	}

	// Previews: das Org-Secret listet seine Zuweisung, bobs Secret bleibt agent-eigen.
	// Ohne Sensibel-Markierung ist ein Secret eine Variable: voller Klartext.
	previews, err := s.secrets.Previews(ctx, s.orgID)
	if err != nil || len(previews) != 1 {
		t.Fatalf("previews: %+v, %v", previews, err)
	}
	if len(previews[0].AgentIDs) != 1 || previews[0].AgentIDs[0] != alice.ID.String() {
		t.Fatalf("zuweisung fehlt in preview: %+v", previews[0])
	}
	if previews[0].Sensitive || previews[0].Value != "org-token" {
		t.Fatalf("nicht-sensibles Org-Secret muss einsehbar sein: %+v", previews[0])
	}
	ownPreviews, err := s.secrets.AgentPreviews(ctx, s.orgID, bob.ID)
	if err != nil || len(ownPreviews) != 1 || ownPreviews[0].Key != "zammad_token" {
		t.Fatalf("agent-previews: %+v, %v", ownPreviews, err)
	}
	if ownPreviews[0].Sensitive || ownPreviews[0].Value != "bob-token" {
		t.Fatalf("nicht-sensibles Agent-Secret muss einsehbar sein: %+v", ownPreviews[0])
	}

	// Als sensibel markiert: write-only, nur noch Präfix — auf beiden Ebenen.
	if err := s.secrets.MarkSensitive(ctx, s.orgID, "zammad_token"); err != nil {
		t.Fatal(err)
	}
	if err := s.secrets.MarkAgentSensitive(ctx, s.orgID, bob.ID, "zammad_token"); err != nil {
		t.Fatal(err)
	}
	previews, err = s.secrets.Previews(ctx, s.orgID)
	if err != nil || len(previews) != 1 || !previews[0].Sensitive || previews[0].Value != "" {
		t.Fatalf("sensibles Org-Secret darf keinen Klartext liefern: %+v, %v", previews, err)
	}
	ownPreviews, err = s.secrets.AgentPreviews(ctx, s.orgID, bob.ID)
	if err != nil || len(ownPreviews) != 1 || !ownPreviews[0].Sensitive || ownPreviews[0].Value != "" {
		t.Fatalf("sensibles Agent-Secret darf keinen Klartext liefern: %+v, %v", ownPreviews, err)
	}
	// Der Broker löst sensible Secrets weiterhin auf — der Schutz gilt der UI/API.
	if v, err := s.secrets.Resolve(ctx, s.orgID, alice.ID, "zammad_token"); err != nil || v != "org-token" {
		t.Fatalf("resolve nach markSensitive: %q, %v", v, err)
	}
	// Überschreiben behält das Flag — einmal sensibel bleibt sensibel.
	if err := s.secrets.Put(ctx, s.orgID, "zammad_token", "org-token-neu-lang"); err != nil {
		t.Fatal(err)
	}
	previews, err = s.secrets.Previews(ctx, s.orgID)
	if err != nil || len(previews) != 1 || !previews[0].Sensitive {
		t.Fatalf("überschreiben darf sensitive nicht zurücksetzen: %+v, %v", previews, err)
	}
	if previews[0].Value != "" || previews[0].Prefix != "org-" {
		t.Fatalf("sensibles Secret: nur Präfix erwartet: %+v", previews[0])
	}
	if err := s.secrets.MarkSensitive(ctx, s.orgID, "gibt_es_nicht"); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("markSensitive auf unbekanntes Secret: %v", err)
	}

	// Zuweisung entfernen: alice verliert den Zugriff wieder; bob behält nur
	// sein agent-eigenes Secret, bis es gelöscht ist.
	if err := s.secrets.Unassign(ctx, s.orgID, "zammad_token", alice.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.secrets.Resolve(ctx, s.orgID, alice.ID, "zammad_token"); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("alice nach unassign: %v", err)
	}
	if err := s.secrets.DeleteAgent(ctx, s.orgID, bob.ID, "zammad_token"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.secrets.Resolve(ctx, s.orgID, bob.ID, "zammad_token"); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("bob nach delete des agent-eigenen Secrets: %v", err)
	}

	// Schutzkanten: Zuweisung an unbekannte Secrets/Agenten scheitert sauber.
	if err := s.secrets.Assign(ctx, s.orgID, "gibt_es_nicht", alice.ID); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("assign auf unbekanntes Secret: %v", err)
	}
	if err := s.secrets.PutAgent(ctx, s.orgID, s.orgID /* keine agent-id */, "x", "y"); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("put für fremden/unbekannten Agenten: %v", err)
	}
}
