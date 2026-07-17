package gitlab

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"covey/internal/target"
)

// System bindet GitLab als Zielsystem-Plugin an die target-Registry:
// Webhook-Eingang (Token-Prüfung, Idempotenz, Korrelation), die fünf
// Agent-Aktionen und die Aktions-Doku für den System-Prompt.
type System struct{}

func init() {
	target.Register(target.Descriptor{
		Name:        "gitlab",
		Label:       "GitLab",
		Description: "GitLab-Issues als Arbeitsvorrat: Issues lesen, kommentieren, schließen, eskalieren. Webhook-Wake über Issue-/Note-Hooks, Auth per API-Token (Secrets gitlab_token + gitlab_url).",
		Kind:        "builtin",
		System:      System{},
	})
}

func (System) Name() string { return "gitlab" }

func (System) VerifyWebhook(secret string, body []byte, header http.Header) bool {
	return VerifyToken(secret, header.Get("X-Gitlab-Token"))
}

func (System) ParseWebhook(body []byte) (target.WebhookEvent, error) {
	p, err := ParseWebhook(body)
	if err != nil {
		return target.WebhookEvent{}, err
	}
	ev := target.WebhookEvent{
		DedupKey:       p.DedupKey(),
		CorrelationKey: CorrelationKey(p.Project.ID, p.IssueIID()),
		Title: fmt.Sprintf("GitLab-Issue %s#%d: %s",
			p.Project.PathWithNamespace, p.IssueIID(), p.IssueTitle()),
		Wake: p.ShouldWake(),
	}
	if p.ObjectKind == "note" {
		ev.TaskBody = fmt.Sprintf("Kommentar zu GitLab-Issue %s#%d (project_id=%d).\nTitel: %s\n\nKommentar von @%s:\n%s\n\nBearbeite das Issue über den Action-Proxy (system gitlab, project_id=%d, issue_iid=%d).",
			p.Project.PathWithNamespace, p.IssueIID(), p.Project.ID, p.IssueTitle(),
			p.User.Username, p.ObjectAttributes.Note, p.Project.ID, p.IssueIID())
		ev.ResumeInput = fmt.Sprintf("Kommentar von @%s zu Issue %s#%d:\n%s",
			p.User.Username, p.Project.PathWithNamespace, p.IssueIID(), p.ObjectAttributes.Note)
	} else {
		ev.TaskBody = fmt.Sprintf("Neues Issue im GitLab (project_id=%d, iid=%d).\nProjekt: %s\nTitel: %s\n\nBeschreibung:\n%s\n\nBearbeite das Issue über den Action-Proxy (system gitlab, project_id=%d, issue_iid=%d).",
			p.Project.ID, p.IssueIID(), p.Project.PathWithNamespace, p.IssueTitle(),
			p.ObjectAttributes.Description, p.Project.ID, p.IssueIID())
		ev.ResumeInput = fmt.Sprintf("Issue %s#%d wurde %s: %s",
			p.Project.PathWithNamespace, p.IssueIID(), p.ObjectAttributes.Action, p.IssueTitle())
	}
	return ev, nil
}

// ActionSubject: öffentliche Kommentare (internal=false) sind ein eigenes,
// schärfer regelbares Guard-Rail-Subjekt — analog zammad:reply_external.
func (System) ActionSubject(action string, params json.RawMessage) string {
	if action == "comment" {
		var p struct {
			Internal *bool `json:"internal"`
		}
		json.Unmarshal(params, &p)
		if p.Internal != nil && !*p.Internal {
			return "gitlab:comment_external"
		}
		return "gitlab:comment_internal"
	}
	return "gitlab:" + action
}

func (System) Execute(ctx context.Context, action string, params json.RawMessage, cred target.Credential) (any, error) {
	gc := NewClient(cred.BaseURL, cred.Token)

	var in struct {
		ProjectID int    `json:"project_id"`
		IssueIID  int    `json:"issue_iid"`
		Body      string `json:"body"`
		Internal  *bool  `json:"internal"`
		State     string `json:"state"`
		Note      string `json:"note"`
	}
	if err := json.Unmarshal(params, &in); err != nil {
		return nil, fmt.Errorf("params: %w", err)
	}

	switch action {
	case "get_issue":
		return gc.GetIssue(ctx, in.ProjectID, in.IssueIID)
	case "list_notes":
		return gc.ListNotes(ctx, in.ProjectID, in.IssueIID)
	case "comment":
		internal := in.Internal == nil || *in.Internal
		return gc.Comment(ctx, in.ProjectID, in.IssueIID, in.Body, internal)
	case "set_state":
		if in.State == "" {
			return nil, fmt.Errorf("state fehlt")
		}
		return nil, gc.SetState(ctx, in.ProjectID, in.IssueIID, in.State)
	case "escalate":
		note := in.Note
		if note == "" {
			note = "Eskalation durch Covey-Agent."
		}
		return nil, gc.Escalate(ctx, in.ProjectID, in.IssueIID, note)
	default:
		return nil, fmt.Errorf("unbekannte aktion %q", strings.TrimSpace(action))
	}
}

func (System) PromptDoc() string {
	return `Verfügbare GitLab-Aktionen: get_issue {"project_id":N,"issue_iid":N}, list_notes {"project_id":N,"issue_iid":N},
   comment {"project_id":N,"issue_iid":N,"body":"...","internal":true|false}, set_state {"project_id":N,"issue_iid":N,"state":"close"|"reopen"},
   escalate {"project_id":N,"issue_iid":N,"note":"..."}.`
}
