package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/examples"
	"covey/internal/backlog"
)

// skillView covers all three response shapes: the library list (without files),
// a single skill (with files) and the resolved agent view (with origin on
// top).
type skillView struct {
	ID          string   `json:"id"`
	AgentID     string   `json:"agent_id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	AssignedTo  []string `json:"assigned_to"`
	Origin      string   `json:"origin"`
	Files       []struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	} `json:"files"`
}

func (v skillView) file(path string) string {
	for _, f := range v.Files {
		if f.Path == path {
			return f.Content
		}
	}
	return ""
}

func getSkillList(t *testing.T, c *apiClient, path string) []skillView {
	t.Helper()
	resp := c.do(http.MethodGet, path, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: HTTP %d", path, resp.StatusCode)
	}
	var out []skillView
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return out
}

func getSkill(t *testing.T, c *apiClient, path string) skillView {
	t.Helper()
	resp := c.do(http.MethodGet, path, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: HTTP %d", path, resp.StatusCode)
	}
	var out skillView
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return out
}

func skillFiles(files ...[2]string) []map[string]string {
	out := make([]map[string]string, 0, len(files))
	for _, f := range files {
		out = append(out, map[string]string{"path": f[0], "content": f[1]})
	}
	return out
}

// TestSkillsAPI checks the HTTP surface of the agent skills against real
// Postgres: creation with frontmatter, the library's opt-in rule (without a link
// a skill reaches nobody), the precedence of agent-owned skills, the input
// validation and the role permissions.
func TestSkillsAPI(t *testing.T) {
	s := newStack(t)

	s.mitglied(t, "skill-auditor@test.local", "Auditor", "auditor", "auditor-passwort")
	admin := login(t, s, "admin@test.local", "admin-passwort")
	auditor := login(t, s, "skill-auditor@test.local", "auditor-passwort")

	alice := s.newSupportAgent("alice")
	bob := s.newSupportAgent("bob")

	// The older route /api/v1/skills/covey-agent.zip (the Claude Code skill for
	// download) lives under the same prefix as /api/v1/skills/{id} and has to take
	// precedence over it as a literal segment — otherwise it would suddenly be an
	// "invalid id".
	zip := admin.do(http.MethodGet, "/api/v1/skills/covey-agent.zip", nil)
	zip.Body.Close()
	if zip.StatusCode != http.StatusOK || zip.Header.Get("Content-Type") != "application/zip" {
		t.Fatalf("the skill download has to stay reachable: HTTP %d (%s)",
			zip.StatusCode, zip.Header.Get("Content-Type"))
	}

	// Creation: name and description may come from the SKILL.md frontmatter — that
	// way a finished file can simply be pasted in.
	created := admin.expect(http.MethodPost, "/api/v1/skills", map[string]any{
		"files": skillFiles(
			[2]string{"SKILL.md", "---\nname: deploy\ndescription: \"Use this when: a release is due\"\n---\n\n# Deploy\n\nStep 1.\n"},
			[2]string{"checkliste.md", "- [ ] tests green\n"},
		),
	}, http.StatusCreated)
	deployID, _ := created["id"].(string)
	if created["name"] != "deploy" || created["description"] != "Use this when: a release is due" {
		t.Fatalf("the frontmatter has to fill name and description: %+v", created)
	}

	// What gets stored is the body WITHOUT frontmatter — the description lives in
	// its column, otherwise the two could contradict each other.
	full := getSkill(t, admin, "/api/v1/skills/"+deployID)
	if body := full.file("SKILL.md"); !strings.HasPrefix(body, "# Deploy") {
		t.Fatalf("the frontmatter has to be cut off, got %q", body)
	}
	if full.file("checkliste.md") == "" {
		t.Fatal("the additional file is missing — that is exactly what the feature lives on")
	}

	// The same name on the same level does not silently replace anything.
	admin.expect(http.MethodPost, "/api/v1/skills", map[string]any{
		"name": "deploy", "description": "Second attempt",
		"files": skillFiles([2]string{"SKILL.md", "# X\n"}),
	}, http.StatusConflict)

	// Input validation is 400, not 500: it tells the caller what is missing.
	for _, bad := range []struct {
		was  string
		body map[string]any
	}{
		{"capital letters in the name", map[string]any{"name": "Deploy", "description": "d",
			"files": skillFiles([2]string{"SKILL.md", "x"})}},
		{"path escapes from the skill", map[string]any{"name": "traversal", "description": "d",
			"files": skillFiles([2]string{"SKILL.md", "x"}, [2]string{"../../.claude/settings.json", "{}"})}},
		{"SKILL.md missing", map[string]any{"name": "leer", "description": "d",
			"files": skillFiles([2]string{"referenz.md", "x"})}},
		{"description missing", map[string]any{"name": "ohne-desc",
			"files": skillFiles([2]string{"SKILL.md", "x"})}},
		// Covey stores only name/description; everything else in the frontmatter
		// would be gone without a trace after saving. With allowed-tools that
		// would mean: the skill runs with MORE rights than its author wrote.
		{"unknown frontmatter key", map[string]any{
			"files": skillFiles([2]string{"SKILL.md",
				"---\nname: mit-tools\ndescription: d\nallowed-tools: Bash, Read\n---\n\n# X\n"})}},
	} {
		admin.expect(http.MethodPost, "/api/v1/skills", bad.body, http.StatusBadRequest)
		if list := getSkillList(t, admin, "/api/v1/skills"); len(list) != 1 {
			t.Fatalf("%s must not create anything, library: %+v", bad.was, list)
		}
	}

	// Opt-in: a library skill without a link reaches nobody.
	if list := getSkillList(t, admin, "/api/v1/skills"); len(list) != 1 || len(list[0].AssignedTo) != 0 {
		t.Fatalf("the library expects one unlinked entry: %+v", list)
	}
	if own := getSkillList(t, admin, "/api/v1/agents/"+alice.ID.String()+"/skills"); len(own) != 0 {
		t.Fatalf("without a link alice must get nothing: %+v", own)
	}

	// Link it: only alice gets the skill, bob does not.
	admin.expect(http.MethodPut, "/api/v1/skills/"+deployID+"/agents/"+alice.ID.String(), nil, http.StatusOK)
	aliceSkills := getSkillList(t, admin, "/api/v1/agents/"+alice.ID.String()+"/skills")
	if len(aliceSkills) != 1 || aliceSkills[0].Origin != "library" || aliceSkills[0].file("checkliste.md") == "" {
		t.Fatalf("alice expects the linked library skill including its files: %+v", aliceSkills)
	}
	if bobSkills := getSkillList(t, admin, "/api/v1/agents/"+bob.ID.String()+"/skills"); len(bobSkills) != 0 {
		t.Fatalf("without a link bob must get nothing: %+v", bobSkills)
	}
	if list := getSkillList(t, admin, "/api/v1/skills"); len(list[0].AssignedTo) != 1 ||
		list[0].AssignedTo[0] != alice.ID.String() {
		t.Fatalf("the library has to show the link: %+v", list)
	}

	// An agent-owned skill of the same name: allowed and taking precedence.
	bobOwn := admin.expect(http.MethodPost, "/api/v1/agents/"+bob.ID.String()+"/skills", map[string]any{
		"name": "deploy", "description": "Bob's own deploy route",
		"files": skillFiles([2]string{"SKILL.md", "# Bob's deploy\n"}),
	}, http.StatusCreated)
	admin.expect(http.MethodPut, "/api/v1/skills/"+deployID+"/agents/"+bob.ID.String(), nil, http.StatusOK)
	bobSkills := getSkillList(t, admin, "/api/v1/agents/"+bob.ID.String()+"/skills")
	if len(bobSkills) != 1 || bobSkills[0].Origin != "agent" ||
		!strings.Contains(bobSkills[0].file("SKILL.md"), "Bob's deploy") {
		t.Fatalf("on a name clash the agent's own skill has to win: %+v", bobSkills)
	}

	// Agent-owned skills cannot be linked — they already belong to someone.
	admin.expect(http.MethodPut, "/api/v1/skills/"+bobOwn["id"].(string)+"/agents/"+alice.ID.String(),
		nil, http.StatusBadRequest)

	// Unknown agent or skill: 404, not 500.
	admin.expect(http.MethodPut, "/api/v1/skills/"+deployID+"/agents/"+uuid.NewString(), nil, http.StatusNotFound)
	admin.expect(http.MethodGet, "/api/v1/skills/"+uuid.NewString(), nil, http.StatusNotFound)

	// A revocation that clears nothing is not a success — otherwise the interface
	// acknowledges an effect that never existed (here: alice is not linked to
	// bob's own skill at all).
	admin.expect(http.MethodDelete, "/api/v1/skills/"+bobOwn["id"].(string)+"/agents/"+alice.ID.String(),
		nil, http.StatusNotFound)

	// Replacing swaps out the whole file set (an omitted file is gone).
	admin.expect(http.MethodPut, "/api/v1/skills/"+deployID, map[string]any{
		"description": "Use this at release time",
		"files":       skillFiles([2]string{"SKILL.md", "# Deploy\n\nNew.\n"}),
	}, http.StatusOK)
	full = getSkill(t, admin, "/api/v1/skills/"+deployID)
	if len(full.Files) != 1 || full.Description != "Use this at release time" {
		t.Fatalf("PUT has to replace description and file set: %+v", full)
	}

	// Renaming is rejected: the name is directory and /slash-command.
	admin.expect(http.MethodPut, "/api/v1/skills/"+deployID, map[string]any{
		"name": "deploy-neu", "description": "x",
		"files": skillFiles([2]string{"SKILL.md", "# X\n"}),
	}, http.StatusConflict)

	// Roles: the auditor reads but changes nothing (spec/09).
	auditor.expect(http.MethodGet, "/api/v1/skills", nil, http.StatusOK)
	auditor.expect(http.MethodGet, "/api/v1/agents/"+alice.ID.String()+"/skills", nil, http.StatusOK)
	auditor.expect(http.MethodPost, "/api/v1/skills", map[string]any{"name": "x", "description": "d",
		"files": skillFiles([2]string{"SKILL.md", "x"})}, http.StatusForbidden)
	auditor.expect(http.MethodPut, "/api/v1/skills/"+deployID+"/agents/"+alice.ID.String(), nil, http.StatusForbidden)
	auditor.expect(http.MethodDelete, "/api/v1/skills/"+deployID, nil, http.StatusForbidden)

	// Revoking and deleting take effect on the agent view.
	admin.expect(http.MethodDelete, "/api/v1/skills/"+deployID+"/agents/"+alice.ID.String(), nil, http.StatusOK)
	if own := getSkillList(t, admin, "/api/v1/agents/"+alice.ID.String()+"/skills"); len(own) != 0 {
		t.Fatalf("after the revocation alice must get nothing any more: %+v", own)
	}
	admin.expect(http.MethodDelete, "/api/v1/skills/"+deployID, nil, http.StatusOK)
	if list := getSkillList(t, admin, "/api/v1/skills"); len(list) != 0 {
		t.Fatalf("the library has to be empty: %+v", list)
	}
	// Bob's own skill stays untouched by that.
	if bobSkills = getSkillList(t, admin, "/api/v1/agents/"+bob.ID.String()+"/skills"); len(bobSkills) != 1 {
		t.Fatalf("an agent-owned skill must not be deleted along with it: %+v", bobSkills)
	}
}

// TestDeliveryLeadSkills pins down the conversion of the shipped delivery lead:
// its procedures live in the bundle as skills, no longer in PLAYBOOKS.md — and
// the shipped bundle has to take the route through the import, whose skill
// validation is strict.
//
// That every skill is also mentioned somewhere is checked by
// examples.TestBuiltinSkills for all templates without a database.
func TestDeliveryLeadSkills(t *testing.T) {
	s := newStack(t)
	c := login(t, s, "admin@test.local", "admin-passwort")

	var bundle json.RawMessage
	for _, b := range examples.Builtins() {
		if b.Key == "builtin:delivery-lead" {
			bundle = b.Bundle
		}
	}
	if bundle == nil {
		t.Fatal("the shipped delivery-lead template was not found")
	}
	imported := c.expect(http.MethodPost, "/api/v1/agents/import?slug=lead-probe", bundle, http.StatusCreated)
	nid := imported["agent"].(map[string]any)["id"].(string)

	got := getSkillList(t, c, "/api/v1/agents/"+nid+"/skills")
	want := map[string]bool{"ruecklaeufer": false, "arbeit-vergeben": false,
		"ticket-aufbereiten": false, "tagesbericht": false}
	for _, sk := range got {
		if _, ok := want[sk.Name]; !ok {
			t.Errorf("unexpected skill %q", sk.Name)
			continue
		}
		want[sk.Name] = true
		if sk.Origin != "agent" {
			t.Errorf("%s: origin=%q (the lead procedures belong to it alone)", sk.Name, sk.Origin)
		}
		if len(sk.file("SKILL.md")) < 200 {
			t.Errorf("%s: SKILL.md too thin (%d characters)", sk.Name, len(sk.file("SKILL.md")))
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("skill %q is missing from the imported agent", name)
		}
	}
}

// TestSkillsMaterialisierung is the vertical slice: whatever lies in the library
// through the API and is linked to an agent has to be in its home under
// .claude/skills/<name>/ during the run — where Claude Code looks for personal
// skills. And a revocation has to take effect just as well: the home survives
// the run, so an unlinked skill would otherwise stay effective forever.
func TestSkillsMaterialisierung(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("skill-laeufer")

	created := admin.expect(http.MethodPost, "/api/v1/skills", map[string]any{
		"name": "recherche", "description": "Use this when: a source has to be checked",
		"files": skillFiles(
			[2]string{"SKILL.md", "# Research\n\nSee quellen.md.\n"},
			[2]string{"quellen.md", "- commercial register\n"},
		),
	}, http.StatusCreated)
	skillID := created["id"].(string)
	admin.expect(http.MethodPut, "/api/v1/skills/"+skillID+"/agents/"+agent.ID.String(), nil, http.StatusOK)

	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "First run", "[mock:result ok]", "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "first task done", 15*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateDone
	})

	dir := filepath.Join(s.homeBase, agent.ID.String(), ".claude", "skills", "recherche")
	raw, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		t.Fatalf("SKILL.md has to be in the home: %v", err)
	}
	// The frontmatter is only created on write — with a quoted description,
	// otherwise Claude Code would read the colon as a YAML map.
	if !strings.HasPrefix(string(raw), "---\nname: recherche\n") ||
		!strings.Contains(string(raw), `description: "Use this when: a source has to be checked"`) ||
		!strings.Contains(string(raw), "# Research") {
		t.Fatalf("the materialized SKILL.md is unexpected:\n%s", raw)
	}
	if _, err := os.Stat(filepath.Join(dir, "quellen.md")); err != nil {
		t.Fatalf("the additional file has to come along: %v", err)
	}

	// Revocation: clear the link, the next run cleans up the directory.
	admin.expect(http.MethodDelete, "/api/v1/skills/"+skillID+"/agents/"+agent.ID.String(), nil, http.StatusOK)
	task2, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Second run", "[mock:result ok]", "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "second task done", 15*time.Second, func() bool {
		return s.taskState(task2.ID) == backlog.StateDone
	})
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("a revoked skill has to disappear from the home: %v", err)
	}
}
