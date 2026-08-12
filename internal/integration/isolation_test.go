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

// Die Befunde A–E aus FR-003: fünf Stellen, an denen die Organisationsgrenze
// nicht hielt. Jeder Test versucht genau das, was vorher ging.
//
// Der Aufbau ist immer derselbe: eine zweite Organisation mit einem eigenen
// Agenten, und dann der Griff aus der ersten hinüber.

// nachbar legt eine zweite Organisation samt Agent an und gibt beides zurück.
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

// A: Der Ereignis-Bus verteilte jedes Ereignis an jede offene Verbindung —
// Agenten- und Aufgaben-IDs, Status, Aktionsnamen, Leitplanken-Entscheidungen,
// live und ohne dass eine ID zu raten war.
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
	// Ein Konto ohne Mitgliedschaft hört nichts, statt alles.
	select {
	case ev := <-ohneOrg:
		t.Errorf("Abo ohne Organisation bekommt Ereignisse: %+v", ev)
	default:
	}

	// Fail-closed: eine Veröffentlichung ohne Organisation erreicht niemanden,
	// statt alle — sonst wäre eine vergessene Zeile wieder ein Leck.
	s.orch.Events().Publish(orchestrator.Event{Type: "task", AgentID: fremderAgent.ID.String()})
	select {
	case ev := <-eigene:
		t.Errorf("Ereignis ohne Organisation wurde verteilt: %+v", ev)
	case ev := <-fremde:
		t.Errorf("Ereignis ohne Organisation wurde verteilt: %+v", ev)
	default:
	}
}

// B: Der Webhook löste den Agenten per Slug über Organisationen hinweg auf —
// der ÄLTESTE gewann. Zwei Mandanten mit einem "support" hätten die Post des
// jeweils anderen bekommen.
func TestWebhookLiefertNichtInDieFalscheOrganisation(t *testing.T) {
	s := newStack(t)
	eigener := s.newSupportAgent("support")
	_, fremder := nachbar(t, s)

	if eigener.ID == fremder.ID {
		t.Fatal("Aufbau kaputt")
	}

	// Mehrdeutig: der Server stellt lieber nicht zu, als falsch zuzustellen.
	resp := s.postJSON(t, "/api/webhooks/zammad/support", map[string]any{"ticket": map[string]any{"id": 1}})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("mehrdeutiger Slug ergibt %d, erwartet 404", resp.StatusCode)
	}

	// Über die ID bleibt jeder erreichbar — sie ist instanzweit eindeutig.
	_, err := s.registry.FindBySlug(context.Background(), "support")
	if err != agents.ErrAmbiguousSlug {
		t.Errorf("FindBySlug liefert %v, erwartet ErrAmbiguousSlug", err)
	}
	if _, err := s.registry.Get(context.Background(), fremder.ID); err != nil {
		t.Errorf("über die ID muss der Agent erreichbar bleiben: %v", err)
	}
}

// C: dream-actions/{id}/undo kannte die Organisation nicht — ein Schreibzugriff
// über die Grenze.
func TestTraumRuecknahmeBleibtInDerOrganisation(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	_, fremderAgent := nachbar(t, s)

	// Die Seite, um die es geht — sonst scheitert die Rücknahme daran, dass es
	// nichts umzubenennen gibt, und der Test prüfte die falsche Sache.
	if _, err := s.mem.Write(ctx, fremderAgent.ID, memory.PageInput{
		Slug: "fremde-seite", Title: "Neuer Titel",
		Body: "Was der Nachbar aufgeschrieben hat.", Source: "agent",
	}); err != nil {
		t.Fatal(err)
	}

	// Ein Traum mit einer rücknehmbaren Aktion beim Nachbarn.
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
	// Die eigene Organisation kommt an ihre eigene heran — die Prüfung sperrt
	// nicht einfach alles.
	if err := store.Undo(ctx, fremderAgent.OrgID, aktionID); err != nil {
		t.Errorf("die eigene Aktion muss zurücknehmbar bleiben: %v", err)
	}
}

// D: skills.Assign prüfte die Fähigkeit, aber nicht den Agenten — damit liess
// sich einem fremden Agenten Text in den Prompt legen.
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
	// Der eigene Agent bleibt zuweisbar.
	eigenerAgent := s.newSupportAgent("eigener")
	if err := store.Assign(ctx, s.orgID, eigene.ID, eigenerAgent.ID); err != nil {
		t.Errorf("die eigene Zuweisung muss weiter gehen: %v", err)
	}
}

// E: Die Egress-Vorlage wurde ungeprüft angehängt — man sah die Freigabeliste
// einer fremden Organisation und bekam sie selbst frei.
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

	// Die eigene Vorlage geht weiter.
	eigeneVorlage, err := s.egress.CreateTemplate(ctx, s.orgID, "Eigene Liste", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.egress.SetAgentTemplate(ctx, eigener.ID, eigeneVorlage.ID, true); err != nil {
		t.Errorf("die eigene Vorlage muss anhängbar bleiben: %v", err)
	}
}
