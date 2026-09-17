package integration

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/internal/agents"
	"covey/internal/memory"
	"covey/internal/orchestrator"
	"covey/internal/skills"
)

// Findings A-E from FR-003: five places where the organisation boundary
// did not hold. Each test tries exactly what used to work.
//
// The setup is always the same: a second organisation with an agent of its
// own, and then the reach over from the first one.

// nachbar creates a second organisation together with an agent and returns both.
func nachbar(t *testing.T, s *stack) (uuid.UUID, agents.Agent) {
	t.Helper()
	ctx := context.Background()
	orgID := uuid.New()
	if _, err := s.pool.Exec(ctx, `INSERT INTO organizations (id, name) VALUES ($1,'Nachbar-AG')`, orgID); err != nil {
		t.Fatal(err)
	}
	a, err := s.registry.Create(ctx, orgID, "support", "Nachbars Support", "mock", nil)
	if err != nil {
		t.Fatal(err)
	}
	return orgID, a
}

// A: the event bus delivered every event to every open connection —
// agent and task IDs, status, action names, guard rail decisions,
// live, and without any ID having to be guessed.
func TestEreignisseBleibenInDerOrganisation(t *testing.T) {
	s := newStack(t)
	fremdeOrg, fremderAgent := nachbar(t, s)

	eigene, cancelEigene := s.orch.Events().Subscribe(s.orgID)
	defer cancelEigene()
	fremde, cancelFremde := s.orch.Events().Subscribe(fremdeOrg)
	defer cancelFremde()
	ohneOrg, cancelOhne := s.orch.Events().Subscribe(uuid.Nil)
	defer cancelOhne()

	s.orch.Events().Publish(orchestrator.Event{
		Type: "agent_status", AgentID: fremderAgent.ID.String(), OrgID: fremdeOrg,
		Data: map[string]string{"status": "working"},
	})

	select {
	case ev := <-fremde:
		if ev.AgentID != fremderAgent.ID.String() {
			t.Errorf("falsches Ereignis: %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("die eigene Organisation bekommt ihr Ereignis nicht")
	}
	select {
	case ev := <-eigene:
		t.Errorf("fremdes Ereignis durchgereicht: %+v", ev)
	default:
	}
	// An account without a membership hears nothing, rather than everything.
	select {
	case ev := <-ohneOrg:
		t.Errorf("Abo ohne Organisation bekommt Ereignisse: %+v", ev)
	default:
	}

	// Fail-closed: a publication without an organisation reaches nobody,
	// not everyone — otherwise one forgotten line would be a leak again.
	s.orch.Events().Publish(orchestrator.Event{Type: "task", AgentID: fremderAgent.ID.String()})
	select {
	case ev := <-eigene:
		t.Errorf("Ereignis ohne Organisation wurde verteilt: %+v", ev)
	case ev := <-fremde:
		t.Errorf("Ereignis ohne Organisation wurde verteilt: %+v", ev)
	default:
	}
}

// B: the webhook resolved the agent by slug across organisations —
// the OLDEST won. Two tenants with a "support" would have received the
// other one's mail.
func TestWebhookLiefertNichtInDieFalscheOrganisation(t *testing.T) {
	s := newStack(t)
	eigener := s.newSupportAgent("support")
	_, fremder := nachbar(t, s)

	if eigener.ID == fremder.ID {
		t.Fatal("Aufbau kaputt")
	}

	// Ambiguous: the server prefers not delivering at all over delivering wrong.
	resp := s.postJSON(t, "/api/webhooks/zammad/support", map[string]any{"ticket": map[string]any{"id": 1}})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("mehrdeutiger Slug ergibt %d, erwartet 404", resp.StatusCode)
	}

	// By ID everyone stays reachable — it is unique across the instance.
	_, err := s.registry.FindBySlug(context.Background(), "support")
	if err != agents.ErrAmbiguousSlug {
		t.Errorf("FindBySlug liefert %v, erwartet ErrAmbiguousSlug", err)
	}
	if _, err := s.registry.Get(context.Background(), fremder.ID); err != nil {
		t.Errorf("über die ID muss der Agent erreichbar bleiben: %v", err)
	}
}

// C: dream-actions/{id}/undo did not know the organisation — a write access
// across the boundary.
func TestTraumRuecknahmeBleibtInDerOrganisation(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	_, fremderAgent := nachbar(t, s)

	// The page this is about — otherwise the undo fails because there is
	// nothing to rename, and the test would check the wrong thing.
	if _, err := s.mem.Write(ctx, fremderAgent.ID, memory.PageInput{
		Slug: "fremde-seite", Title: "Neuer Titel",
		Body: "Was der Nachbar aufgeschrieben hat.", Source: "agent",
	}); err != nil {
		t.Fatal(err)
	}

	// A dream with an undoable action at the neighbour.
	traumID := uuid.New()
	if _, err := s.pool.Exec(ctx, `INSERT INTO dreams (id, agent_id, status, started_at)
		VALUES ($1,$2,'done',now())`, traumID, fremderAgent.ID); err != nil {
		t.Fatal(err)
	}
	aktionID := uuid.New()
	if _, err := s.pool.Exec(ctx, `INSERT INTO dream_actions (id, dream_id, kind, page_slug, before)
		VALUES ($1,$2,'retitle','fremde-seite','Alter Titel')`, aktionID, traumID); err != nil {
		t.Fatal(err)
	}

	store := s.dreams
	if err := store.Undo(ctx, s.orgID, aktionID); err == nil {
		t.Error("die fremde Traum-Aktion liess sich zurücknehmen")
	}
	// The own organisation still reaches its own — the check does not block
	// everything.
	if err := store.Undo(ctx, fremderAgent.OrgID, aktionID); err != nil {
		t.Errorf("die eigene Aktion muss zurücknehmbar bleiben: %v", err)
	}
}

// D: skills.Assign checked the skill but not the agent — that allowed putting
// text into the prompt of a foreign agent.
func TestSkillZuweisungPruefstDenAgenten(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	_, fremderAgent := nachbar(t, s)

	store := skills.NewStore(s.pool)
	eigene, err := store.Create(ctx, s.orgID, skills.Spec{
		Name: "gemeinsam", Description: "Eine Bibliotheks-Fähigkeit",
		Files: []skills.File{{Path: "SKILL.md", Content: "# Gemeinsam\n\nEine Anleitung.\n"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := store.Assign(ctx, s.orgID, eigene.ID, fremderAgent.ID); err == nil {
		t.Error("eigene Fähigkeit liess sich einem fremden Agenten anhängen")
	}
	// The own agent stays assignable.
	eigenerAgent := s.newSupportAgent("eigener")
	if err := store.Assign(ctx, s.orgID, eigene.ID, eigenerAgent.ID); err != nil {
		t.Errorf("die eigene Zuweisung muss weiter gehen: %v", err)
	}
}

// E: the egress template was attached unchecked — you saw the allowlist of
// a foreign organisation and got it released for yourself.
func TestEgressVorlageBleibtInDerOrganisation(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	fremdeOrg, _ := nachbar(t, s)

	fremdeVorlage, err := s.egress.CreateTemplate(ctx, fremdeOrg, "Nachbars Liste", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.egress.AddTemplateHost(ctx, fremdeOrg, fremdeVorlage.ID, "geheim.nachbar.de", ""); err != nil {
		t.Fatal(err)
	}

	eigener := s.newSupportAgent("egress-agent")
	if err := s.egress.SetAgentTemplate(ctx, eigener.ID, fremdeVorlage.ID, true); err == nil {
		t.Error("fremde Vorlage liess sich anhängen")
	}

	liste, err := s.egress.EffectiveAllowlist(ctx, eigener.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, muster := range liste {
		if muster == "geheim.nachbar.de" {
			t.Error("der fremde Host steht in der eigenen Freigabeliste")
		}
	}

	// The own template keeps working.
	eigeneVorlage, err := s.egress.CreateTemplate(ctx, s.orgID, "Eigene Liste", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.egress.SetAgentTemplate(ctx, eigener.ID, eigeneVorlage.ID, true); err != nil {
		t.Errorf("die eigene Vorlage muss anhängbar bleiben: %v", err)
	}
}
