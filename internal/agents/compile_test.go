package agents

import (
	"strings"
	"testing"
)

func TestCompilePromptOrderAndProtocol(t *testing.T) {
	files := map[string]string{
		"SOUL.md":         "# Support-Agent\n\n## Rolle\nFirst-Level-Support.",
		"PLAYBOOKS.md":    "## Ticket-Triage\n1. Lesen.",
		"CAPABILITIES.md": "## Zuständig\nTickets.",
		"ACCESS.md":       "- system: zammad scope: read,write",
		"TONE.md":         "Sie-Form.",
	}
	p := CompilePrompt(files)

	soul := strings.Index(p, "Support-Agent")
	caps := strings.Index(p, "Zuständig")
	play := strings.Index(p, "Ticket-Triage")
	tone := strings.Index(p, "Sie-Form")
	if !(soul < caps && caps < play && play < tone) {
		t.Fatalf("wrong order: soul=%d caps=%d play=%d tone=%d", soul, caps, play, tone)
	}
	if strings.Contains(p, "system: zammad") {
		t.Fatal("ACCESS.md must not be compiled into the prompt")
	}
	if !strings.Contains(p, "COVEY_STATUS") {
		t.Fatal("protocol instructions missing")
	}
}

func TestCompilePromptEmptyFiles(t *testing.T) {
	p := CompilePrompt(map[string]string{"SOUL.md": "  \n"})
	if !strings.HasPrefix(p, "## covey platform protocol") {
		t.Fatalf("empty files should be skipped, got: %.60q", p)
	}
}

func TestParseAccess(t *testing.T) {
	content := `# Zugänge

- system: zammad     scope: read,write,comment
- system: confluence scope: read
-   system: teams   scopes: send-message
irrelevante Zeile
`
	accs := ParseAccess(content)
	if len(accs) != 3 {
		t.Fatalf("expected 3 accesses, got %d: %+v", len(accs), accs)
	}
	if accs[0].System != "zammad" || len(accs[0].Scopes) != 3 || accs[0].Scopes[1] != "write" {
		t.Fatalf("zammad access parsed wrongly: %+v", accs[0])
	}
	if accs[2].System != "teams" || accs[2].Scopes[0] != "send-message" {
		t.Fatalf("teams access parsed wrongly: %+v", accs[2])
	}
}

func TestParseAccessEmpty(t *testing.T) {
	if accs := ParseAccess(""); len(accs) != 0 {
		t.Fatalf("an empty ACCESS.md should parse to nothing, got %+v", accs)
	}
}

func TestTeamSection(t *testing.T) {
	s := TeamSection([]TeamMember{
		{Name: "Max Mustermann", JobTitle: "QA Engineer", Email: "max@example.com",
			Identities:       []TeamIdentity{{Label: "GitLab", Value: "maxm"}, {Label: "Zammad", Value: "max@firma.de"}},
			Responsibilities: "testet Bugfixes im Projekt educa"},
		{Name: "Erika Beispiel"}, // name only — no empty brackets/dashes
		{Name: "  "},             // without a name: skip
	})
	if !strings.Contains(s, "## Team (human employees)") {
		t.Fatalf("heading missing: %q", s)
	}
	if !strings.Contains(s, "- Max Mustermann — QA Engineer (Email: max@example.com, GitLab: maxm, Zammad: max@firma.de) — responsible for: testet Bugfixes im Projekt educa") {
		t.Fatalf("full-profile line wrong: %q", s)
	}
	if !strings.Contains(s, "- Erika Beispiel\n") && !strings.HasSuffix(s, "- Erika Beispiel") {
		t.Fatalf("minimal-profile line wrong: %q", s)
	}
	if strings.Contains(s, "()") || strings.Contains(s, "Erika Beispiel —") {
		t.Fatalf("empty fields must not leave artefacts behind: %q", s)
	}

	if TeamSection(nil) != "" || TeamSection([]TeamMember{{Name: " "}}) != "" {
		t.Fatal("without members the section has to be empty")
	}
}

func TestTeamAgentsSection(t *testing.T) {
	s := TeamAgentsSection([]AgentColleague{
		{Name: "covey-qa", JobTitle: "QA-Agent", Department: "Engineering", SameTeam: true,
			Identities:       []TeamIdentity{{Label: "GitLab", Value: "covey-qa"}},
			Responsibilities: "testet Merge Requests"},
		{Name: "covey-support", JobTitle: "Support-Agent", Department: "Support", SameTeam: false,
			Identities: []TeamIdentity{{Label: "GitLab", Value: "covey-support"}}},
		{Name: "  "}, // without a name: skip
	})
	if !strings.Contains(s, "## Team (AI colleagues)") {
		t.Fatalf("heading missing: %q", s)
	}
	if !strings.Contains(s, "- covey-qa — QA-Agent — YOUR TEAM (Engineering) (GitLab: covey-qa) — responsible for: testet Merge Requests") {
		t.Fatalf("same-team colleague line wrong: %q", s)
	}
	if !strings.Contains(s, "- covey-support — Support-Agent — team: Support (GitLab: covey-support)") {
		t.Fatalf("other-team line wrong: %q", s)
	}
	if strings.Contains(s, "covey-support — YOUR TEAM") {
		t.Fatalf("only colleagues from one's own team may be marked YOUR TEAM: %q", s)
	}
	if TeamAgentsSection(nil) != "" || TeamAgentsSection([]AgentColleague{{Name: " "}}) != "" {
		t.Fatal("without colleagues the section has to be empty")
	}
}

func TestTeamAgentsSectionSupervisor(t *testing.T) {
	s := TeamAgentsSection([]AgentColleague{
		{Name: "covey-lead", JobTitle: "Lead-Agent", Supervisor: true,
			Identities: []TeamIdentity{{Label: "GitLab", Value: "covey-lead"}}},
	})
	if !strings.Contains(s, "- covey-lead — Lead-Agent — YOUR MANAGER (GitLab: covey-lead)") {
		t.Fatalf("manager marking missing or misplaced: %q", s)
	}
}

func TestTeamSectionSupervisor(t *testing.T) {
	s := TeamSection([]TeamMember{
		{Name: "Lena Lead", JobTitle: "Engineering Lead", Supervisor: true,
			Identities: []TeamIdentity{{Label: "GitLab", Value: "leaddev"}}},
		{Name: "Max Mustermann"},
	})
	if !strings.Contains(s, "- Lena Lead — Engineering Lead — YOUR MANAGER (GitLab: leaddev)") {
		t.Fatalf("manager marking missing or misplaced: %q", s)
	}
	if strings.Contains(s, "Max Mustermann — YOUR MANAGER") {
		t.Fatalf("only the manager may be marked: %q", s)
	}
	if !strings.Contains(s, "merge\nrequests") && !strings.Contains(s, "merge requests") {
		t.Fatalf("the note about merge requests to the manager is missing: %q", s)
	}
}
