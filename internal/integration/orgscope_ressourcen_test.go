package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"covey/internal/guardrails"
	identbuiltin "covey/internal/identity/builtin"
	"covey/internal/memory"
	"covey/internal/org"
	"covey/internal/skills"
	"covey/internal/templates"
)

// After it turned out that 25 of the 67 agent endpoints did not check the
// organization boundary, the obvious question is: does that also hold for
// everything ELSE addressed through an ID? Tasks, wiki pages, guard-rails,
// skills, templates, departments, humans, approvals, artifacts.
//
// This test creates the same objects in a FOREIGN organization and calls every
// route belonging to them with those IDs. Expectation: nothing gets through.
func TestFremdeRessourcenSindNirgendsErreichbar(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")

	// --- A fully equipped foreign organization ---
	fremdeOrg := uuid.New()
	if _, err := s.pool.Exec(ctx, "INSERT INTO organizations (id, name) VALUES ($1,'Fremd-AG')", fremdeOrg); err != nil {
		t.Fatal(err)
	}
	fremdAgent, err := s.registry.Create(ctx, fremdeOrg, "fremd-agent", "Fremd", "mock", nil)
	if err != nil {
		t.Fatal(err)
	}
	fremdTask, err := s.backlog.Create(ctx, fremdeOrg, fremdAgent.ID, "Fremde Aufgabe", "vertraulich", "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	fremdStage, err := s.backlog.CreateStage(ctx, fremdAgent.ID, "Fremde Spalte", "#fff")
	if err != nil {
		t.Fatal(err)
	}
	fremdSeite, err := s.mem.Write(ctx, fremdAgent.ID, memory.PageInput{
		Title: "Fremdes Wissen", Body: "Interna der anderen Organisation.",
	})
	if err != nil {
		t.Fatal(err)
	}
	fremdRegel, err := s.rails.Create(ctx, guardrails.Rule{
		OrgID: fremdeOrg, ScopeLevel: "global", RuleType: guardrails.RuleDenySystem, Pattern: "zammad", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	fremdSkill, err := s.skills.Create(ctx, fremdeOrg, skills.Spec{
		Name: "fremder-skill", Description: "geheim",
		Files: []skills.File{{Path: "SKILL.md", Content: "Betriebsgeheimnis"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	vorlagen := templates.NewStore(s.pool)
	fremdVorlage, err := vorlagen.Create(ctx, fremdeOrg, "Fremde Vorlage", "geheim",
		json.RawMessage(`{"kind":"covey.agent-config"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	orgStore := org.NewStore(s.pool)
	fremdAbteilung, err := orgStore.CreateDepartment(ctx, fremdeOrg, "Fremde Abteilung", "", "#abc")
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := identbuiltin.HashPassword("egal-1234")
	fremdMensch, err := orgStore.CreateHuman(ctx, fremdeOrg, "fremd@fremd.local", "Fremder Mensch",
		"agent_owner", hash, org.Profile{})
	if err != nil {
		t.Fatal(err)
	}
	fremdFreigabe, err := s.obs.CreateApproval(ctx, fremdeOrg, fremdAgent.ID, nil, "zammad:reply_external", nil)
	if err != nil {
		t.Fatal(err)
	}
	fremdBlob, err := s.obs.PutBlob(ctx, fremdeOrg, fremdAgent.ID, nil, "image/png", []byte("\x89PNG-geheim"))
	if err != nil {
		t.Fatal(err)
	}
	fremdEgress, err := s.egress.CreateTemplate(ctx, fremdeOrg, "fremde-vorlage", "geheim")
	if err != nil {
		t.Fatal(err)
	}

	// --- Every route with the foreign ID ---
	type fall struct{ method, path string }
	faelle := []fall{
		{http.MethodGet, "/api/v1/tasks/" + fremdTask.ID.String() + "/notes"},
		{http.MethodGet, "/api/v1/tasks/" + fremdTask.ID.String() + "/transitions"},
		{http.MethodPost, "/api/v1/tasks/" + fremdTask.ID.String() + "/archive"},
		{http.MethodPost, "/api/v1/tasks/" + fremdTask.ID.String() + "/cancel"},
		{http.MethodPost, "/api/v1/tasks/" + fremdTask.ID.String() + "/retry"},
		{http.MethodPost, "/api/v1/tasks/" + fremdTask.ID.String() + "/stage"},
		{http.MethodPatch, "/api/v1/stages/" + fremdStage.ID.String()},
		{http.MethodDelete, "/api/v1/stages/" + fremdStage.ID.String()},
		{http.MethodPatch, "/api/v1/memories/" + fremdSeite.ID.String()},
		{http.MethodDelete, "/api/v1/memories/" + fremdSeite.ID.String()},
		{http.MethodPatch, "/api/v1/guardrails/" + fremdRegel.ID.String()},
		{http.MethodDelete, "/api/v1/guardrails/" + fremdRegel.ID.String()},
		{http.MethodGet, "/api/v1/skills/" + fremdSkill.ID.String()},
		{http.MethodPut, "/api/v1/skills/" + fremdSkill.ID.String()},
		{http.MethodDelete, "/api/v1/skills/" + fremdSkill.ID.String()},
		{http.MethodGet, "/api/v1/templates/" + fremdVorlage.ID.String()},
		{http.MethodDelete, "/api/v1/templates/" + fremdVorlage.ID.String()},
		{http.MethodPost, "/api/v1/templates/" + fremdVorlage.ID.String() + "/instantiate"},
		{http.MethodPatch, "/api/v1/departments/" + fremdAbteilung.ID.String() + "/name"},
		{http.MethodPatch, "/api/v1/departments/" + fremdAbteilung.ID.String() + "/color"},
		{http.MethodDelete, "/api/v1/departments/" + fremdAbteilung.ID.String()},
		{http.MethodPost, "/api/v1/departments/" + fremdAbteilung.ID.String() + "/leads"},
		{http.MethodGet, "/api/v1/org/humans/" + fremdMensch.ID.String()},
		{http.MethodPatch, "/api/v1/org/humans/" + fremdMensch.ID.String() + "/department"},
		{http.MethodPatch, "/api/v1/org/humans/" + fremdMensch.ID.String() + "/manager"},
		{http.MethodPatch, "/api/v1/users/" + fremdMensch.ID.String()},
		{http.MethodDelete, "/api/v1/users/" + fremdMensch.ID.String()},
		{http.MethodPost, "/api/v1/approvals/" + fremdFreigabe.ID.String() + "/decide"},
		{http.MethodGet, "/api/v1/recordings/blobs/" + fremdBlob.String()},
		{http.MethodDelete, "/api/v1/egress/templates/" + fremdEgress.ID.String()},
		{http.MethodPost, "/api/v1/egress/templates/" + fremdEgress.ID.String() + "/hosts"},
	}

	var offen []string
	for _, f := range faelle {
		resp := admin.do(f.method, f.path, map[string]any{"name": "übernommen", "color": "#000", "decision": "approve"})
		resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			// Show the resource type instead of the UUID in the message.
			teile := strings.Split(strings.TrimPrefix(f.path, "/api/v1/"), "/")
			offen = append(offen, f.method+" /"+teile[0]+"/…")
		}
	}
	if len(offen) > 0 {
		t.Errorf("%d routes let a foreign organization through:\n  %s",
			len(offen), strings.Join(offen, "\n  "))
	}
}
