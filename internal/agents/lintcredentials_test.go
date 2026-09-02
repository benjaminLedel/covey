package agents

import (
	"strings"
	"testing"
	"time"
)

func TestLintCredentialRules(t *testing.T) {
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *time.Time { t := now.Add(d); return &t }
	base := Subject{Slug: "dev", Now: now, Files: map[string]string{
		"ACCESS.md": "- system: jira scope: read\n- system: gitlab scope: read",
	}}

	rules := func(s Subject) []string {
		var out []string
		for _, f := range lintCredentials(s) {
			out = append(out, f.Rule)
		}
		return out
	}

	s := base
	s.Credentials = []CredentialState{
		{System: "jira", Key: "jira_token", RejectedAt: at(-time.Hour), RejectedReason: "HTTP 401"},
		{System: "gitlab", Key: "gitlab_token", ExpiresAt: at(10 * 24 * time.Hour)},
		{System: "github", Key: "github_token", RejectedAt: at(-time.Hour)}, // not in ACCESS.md
	}
	got := rules(s)
	if strings.Join(got, ",") != "credential-rejected,credential-expiring" {
		t.Fatalf("rules = %v", got)
	}
	f := lintCredentials(s)[0]
	if f.Severity != SeverityWarn || !strings.Contains(f.Message, "HTTP 401") || !strings.Contains(f.Hint, "Secrets → jira_token") {
		t.Fatalf("rejected finding = %+v", f)
	}
	if f := lintCredentials(s)[1]; !strings.Contains(f.Message, "in 10 days") || !strings.Contains(f.Message, "2026-09-12") {
		t.Fatalf("expiring finding = %+v", f)
	}

	// Far away, or unknown: nothing to say.
	s.Credentials = []CredentialState{
		{System: "jira", Key: "jira_token", ExpiresAt: at(60 * 24 * time.Hour)},
		{System: "gitlab", Key: "gitlab_token"},
	}
	if got := rules(s); len(got) != 0 {
		t.Fatalf("a token with time left is not a finding: %v", got)
	}

	// Past the date: a finding of its own, and a rotatable one names the
	// rotation that did not happen — own secrets point at the agent's page.
	s.Credentials = []CredentialState{
		{System: "jira", Key: "jira_token", Own: true, Rotatable: true, ExpiresAt: at(-24 * time.Hour)},
	}
	fs := lintCredentials(s)
	if len(fs) != 1 || fs[0].Rule != "credential-expired" || !strings.Contains(fs[0].Hint, "rotation failed") ||
		!strings.Contains(fs[0].Hint, "the agent's own secrets → jira_token") {
		t.Fatalf("expired finding = %+v", fs)
	}

	// A rejection outranks an expiry — one finding per key.
	s.Credentials = []CredentialState{
		{System: "jira", Key: "jira_token", RejectedAt: at(-time.Hour), ExpiresAt: at(-time.Hour)},
	}
	if got := rules(s); strings.Join(got, ",") != "credential-rejected" {
		t.Fatalf("rules = %v", got)
	}

	// The whole lint carries them, warn first.
	s.Credentials = []CredentialState{{System: "jira", Key: "jira_token", RejectedAt: at(-time.Hour)}}
	all := Lint(s)
	if len(all) == 0 || all[0].Rule != "credential-rejected" {
		t.Fatalf("Lint = %+v", all)
	}
}
