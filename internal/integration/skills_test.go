package integration

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
		// covey keeps the fields of the Agent Skills format; a key outside it
		// would be gone without a trace after saving — with a runtime-specific
		// key like model: or hooks: that changes what the skill does.
		{"unknown frontmatter key", map[string]any{
			"files": skillFiles([2]string{"SKILL.md",
				"---\nname: mit-model\ndescription: d\nmodel: opus\n---\n\n# X\n"})}},
		{"name against the specification", map[string]any{"name": "doppel--strich", "description": "d",
			"files": skillFiles([2]string{"SKILL.md", "x"})}},
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

func zipBytes(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write(content)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func postZip(t *testing.T, c *apiClient, path string, data []byte) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, c.base+path, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/zip")
	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// TestSkillsAgentSkillsFormat walks a skill in the Agent Skills format through
// covey: a repository archive with two skills, the optional frontmatter fields,
// a binary asset and a script — previewed, imported in one go, refused on a
// second import without replace, fetched by URL, and materialized into the
// agent's home with its frontmatter, its bytes and its file modes intact.
func TestSkillsAgentSkillsFormat(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("format-laeufer")

	png := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0x00, 0xff}
	archive := zipBytes(t, map[string][]byte{
		"repo-main/README.md": []byte("readme"),
		"repo-main/skills/pdf/SKILL.md": []byte("---\nname: pdf\ndescription: >\n  Work with PDFs.\n  Use when: a PDF arrives.\n" +
			"license: Apache-2.0\ncompatibility: Requires python3\nallowed-tools: Bash(python3:*) Read\n" +
			"metadata:\n  author: example-org\n  version: 1.0\n---\n# PDF\n\nRun scripts/extract.sh.\n"),
		"repo-main/skills/pdf/scripts/extract.sh": []byte("#!/bin/sh\necho extracted\n"),
		"repo-main/skills/pdf/assets/logo.png":    png,
		"repo-main/skills/docx/SKILL.md":          []byte("---\nname: docx\ndescription: Word files.\n---\nBody\n"),
	})

	// Dry run: both skills listed, nothing stored.
	code, res := postZip(t, admin, "/api/v1/skills/import?dry_run=1", archive)
	if code != http.StatusOK || len(res["skills"].([]any)) != 2 || res["imported"].(float64) != 0 {
		t.Fatalf("dry run: HTTP %d %+v", code, res)
	}
	if list := getSkillList(t, admin, "/api/v1/skills"); len(list) != 0 {
		t.Fatalf("a dry run must not store anything: %+v", list)
	}

	// Import only pdf.
	code, res = postZip(t, admin, "/api/v1/skills/import?names=pdf", archive)
	if code != http.StatusOK || res["imported"].(float64) != 1 {
		t.Fatalf("import: HTTP %d %+v", code, res)
	}
	item := res["skills"].([]any)[0].(map[string]any)
	if item["status"] != "created" || item["binary_files"].(float64) != 1 {
		t.Fatalf("import item unexpected: %+v", item)
	}
	pdfID := item["id"].(string)

	var full struct {
		License       string            `json:"license"`
		Compatibility string            `json:"compatibility"`
		AllowedTools  string            `json:"allowed_tools"`
		Metadata      map[string]string `json:"metadata"`
		Description   string            `json:"description"`
		Files         []struct {
			Path, Content, Encoding string
		} `json:"files"`
	}
	resp := admin.do(http.MethodGet, "/api/v1/skills/"+pdfID, nil)
	json.NewDecoder(resp.Body).Decode(&full)
	resp.Body.Close()
	if full.License != "Apache-2.0" || full.Compatibility != "Requires python3" ||
		full.AllowedTools != "Bash(python3:*) Read" || full.Metadata["version"] != "1.0" ||
		full.Description != "Work with PDFs. Use when: a PDF arrives." {
		t.Fatalf("frontmatter fields lost: %+v", full)
	}
	foundPNG := false
	for _, f := range full.Files {
		if f.Path == "assets/logo.png" {
			raw, _ := base64.StdEncoding.DecodeString(f.Content)
			foundPNG = f.Encoding == "base64" && bytes.Equal(raw, png)
		}
	}
	if !foundPNG {
		t.Fatalf("the binary asset did not survive the round trip: %+v", full.Files)
	}

	// The whole archive again without replace: pdf exists, so NOTHING is
	// stored — docx included.
	code, res = postZip(t, admin, "/api/v1/skills/import", archive)
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("an existing name without replace must refuse the import: HTTP %d %+v", code, res)
	}
	if list := getSkillList(t, admin, "/api/v1/skills"); len(list) != 1 {
		t.Fatalf("a refused import must store nothing: %+v", list)
	}

	// By URL, with replace: the forge serves the archive, both land.
	forge := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		w.Write(archive)
	}))
	defer forge.Close()
	s.srv.SkillFetcher.Client = forge.Client()
	res = admin.expect(http.MethodPost, "/api/v1/skills/import",
		map[string]any{"url": forge.URL + "/skills.zip", "replace": true}, http.StatusOK)
	if res["imported"].(float64) != 2 {
		t.Fatalf("URL import: %+v", res)
	}

	// Materialized into the home: the frontmatter carries the optional fields,
	// the asset its bytes, the script its executable bit.
	admin.expect(http.MethodPut, "/api/v1/skills/"+pdfID+"/agents/"+agent.ID.String(), nil, http.StatusOK)
	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Run", "[mock:result ok]", "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "task done", 15*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateDone
	})
	dir := filepath.Join(s.homeBase, agent.ID.String(), ".claude", "skills", "pdf")
	entry, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"license: Apache-2.0", `allowed-tools: "Bash(python3:*) Read"`,
		"metadata:\n  author: example-org\n  version: \"1.0\"\n---\n"} {
		if !strings.Contains(string(entry), want) {
			t.Errorf("materialized SKILL.md lacks %q:\n%s", want, entry)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "assets", "logo.png")); !bytes.Equal(got, png) {
		t.Errorf("asset not byte for byte: %v", got)
	}
	if info, err := os.Stat(filepath.Join(dir, "scripts", "extract.sh")); err != nil || info.Mode().Perm()&0o100 == 0 {
		t.Errorf("script not executable: %v %v", info, err)
	}
}
