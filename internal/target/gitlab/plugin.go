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
		Description: "GitLab-Issues als Arbeitsvorrat: Issues finden (list_projects/list_issues), Quellcode auschecken und Bugs am Code verifizieren (checkout), kommentieren, schließen, eskalieren. Intake per HEARTBEAT.md (Polling) oder optionalem Webhook, Auth per API-Token (Secrets gitlab_token + gitlab_url).",
		Kind:        "builtin",
		System:      System{},
		SetupDoc: `1. In GitLab einen eigenen Bot-Nutzer anlegen (z. B. covey-bot), den
   Zielprojekten als Reporter/Developer hinzufügen und als dieser Nutzer ein
   Access Token mit Scope "api" erzeugen.

2. Unter Secrets hinterlegen und dem Agenten zuweisen:
   gitlab_url   = https://gitlab.example.com   (ohne /api/v4)
   gitlab_token = das Token aus Schritt 1

3. In der ACCESS.md des Agenten freischalten:
   - system: gitlab scope: read,write,comment

4. Intake per Heartbeat (empfohlen, kein Webhook nötig) — in der
   HEARTBEAT.md des Agenten:
   - alle: 15m titel: GitLab-Issues sichten aufgabe: Finde offene Issues
     (list_issues state=opened), bearbeite neue und prüfe per list_notes,
     ob auf deine Rückfragen geantwortet wurde. Bei Bugs: Code per checkout
     holen und die Behauptung am Quelltext verifizieren.
   Optionaler Projekt-Filter (gilt für Webhook UND list_issues/list_projects):
   COVEY_GITLAB_INTAKE_PROJECTS="gruppe/support"   (leer = alle)

5. Optional statt/zusätzlich zu 4 — Webhook für sofortige Wakes und das
   automatische Wecken geblockter Aufgaben. Im Zielprojekt (Settings →
   Webhooks):
   URL:          {public_url}/api/webhooks/gitlab/<agent-slug>
   Secret token: Wert von COVEY_GITLAB_WEBHOOK_SECRET (Prozess-Env)
   Trigger:      "Issues events" und "Comments" ankreuzen
   Dazu Echo-Schutz setzen: COVEY_GITLAB_AGENT_USERNAMES="covey-bot"

Details: docs/betrieb-gitlab.md im Repository.`,
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
		ev.TaskBody = fmt.Sprintf("Kommentar zu GitLab-Issue %s#%d (project_id=%d).\nTitel: %s\n\nKommentar von @%s:\n%s\n\nBearbeite das Issue über den Action-Proxy (system gitlab, project_id=%d, issue_iid=%d).\nGeht es um einen Bug oder eine technische Frage: hole dir zuerst mit checkout den Quellcode und prüfe die Aussage am Code, bevor du antwortest.",
			p.Project.PathWithNamespace, p.IssueIID(), p.Project.ID, p.IssueTitle(),
			p.User.Username, p.ObjectAttributes.Note, p.Project.ID, p.IssueIID())
		ev.ResumeInput = fmt.Sprintf("Kommentar von @%s zu Issue %s#%d:\n%s",
			p.User.Username, p.Project.PathWithNamespace, p.IssueIID(), p.ObjectAttributes.Note)
	} else {
		ev.TaskBody = fmt.Sprintf("Neues Issue im GitLab (project_id=%d, iid=%d).\nProjekt: %s\nTitel: %s\n\nBeschreibung:\n%s\n\nBearbeite das Issue über den Action-Proxy (system gitlab, project_id=%d, issue_iid=%d).\nGeht es um einen Bug oder eine technische Frage: hole dir zuerst mit checkout den Quellcode, bestätige oder widerlege die Behauptung am Code (Datei:Zeile) und antworte erst dann.",
			p.Project.ID, p.IssueIID(), p.Project.PathWithNamespace, p.IssueTitle(),
			p.ObjectAttributes.Description, p.Project.ID, p.IssueIID())
		ev.ResumeInput = fmt.Sprintf("Issue %s#%d wurde %s: %s",
			p.Project.PathWithNamespace, p.IssueIID(), p.ObjectAttributes.Action, p.IssueTitle())
	}
	return ev, nil
}

// issueProjectPath leitet den Projektpfad aus der vollen Referenz
// ("gruppe/support#23") ab — die Issue-API liefert path_with_namespace nicht
// direkt. Leerer Rückgabewert, wenn keine Referenz vorhanden ist; der
// Intake-Filter matcht dann nur noch über die numerische Projekt-id.
func issueProjectPath(i Issue) string {
	if idx := strings.LastIndex(i.References.Full, "#"); idx > 0 {
		return i.References.Full[:idx]
	}
	return ""
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
		Labels    string `json:"labels"`
		Search    string `json:"search"`
		Ref       string `json:"ref"`
		Assigned  bool   `json:"assigned"`
		Path      string `json:"path"`
		FilePath  string `json:"file_path"`
		Recursive bool   `json:"recursive"`
	}
	if err := json.Unmarshal(params, &in); err != nil {
		return nil, fmt.Errorf("params: %w", err)
	}

	switch action {
	case "list_projects":
		ps, err := gc.ListProjects(ctx)
		if err != nil {
			return nil, err
		}
		out := []Project{}
		for _, p := range ps {
			if projectInScope(p.ID, p.PathWithNamespace) {
				out = append(out, p)
			}
		}
		return out, nil
	case "list_issues":
		issues, err := gc.ListIssues(ctx, in.ProjectID, in.State, in.Labels, in.Search, in.Assigned)
		if err != nil {
			return nil, err
		}
		out := []Issue{}
		for _, i := range issues {
			if projectInScope(i.ProjectID, issueProjectPath(i)) {
				out = append(out, i)
			}
		}
		return out, nil
	case "get_issue":
		return gc.GetIssue(ctx, in.ProjectID, in.IssueIID)
	case "checkout":
		if in.ProjectID == 0 {
			return nil, fmt.Errorf("project_id fehlt")
		}
		return Checkout(ctx, gc, in.ProjectID, in.Ref, in.Path, target.Workdir(ctx))
	case "list_tree":
		if in.ProjectID == 0 {
			return nil, fmt.Errorf("project_id fehlt")
		}
		return gc.ListTree(ctx, in.ProjectID, in.Path, in.Ref, in.Recursive)
	case "read_file":
		if in.ProjectID == 0 || in.FilePath == "" {
			return nil, fmt.Errorf("project_id oder file_path fehlt")
		}
		content, truncated, err := gc.ReadFile(ctx, in.ProjectID, in.FilePath, in.Ref)
		if err != nil {
			return nil, err
		}
		return map[string]any{"file_path": in.FilePath, "ref": in.Ref,
			"content": content, "truncated": truncated}, nil
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
	return `Verfügbare GitLab-Aktionen: list_projects {}, list_issues {"project_id":N,"state":"opened"|"closed"|"all","labels":"...","search":"...","assigned":true|false}
   (alle Felder optional; ohne project_id alle für dich sichtbaren Issues; assigned=true nur die deinem
   Bot-Nutzer zugewiesenen — nutze das, wenn dein Playbook nur zugewiesene Issues vorsieht), get_issue {"project_id":N,"issue_iid":N},
   checkout {"project_id":N,"ref":"branch|tag|sha (optional, Default: Default-Branch)","path":"unterverzeichnis (optional)"} —
   lädt den Quellcode des Projekts in deine Sandbox und liefert den lokalen Pfad; schlägt er wegen Repo-Größe fehl,
   checke gezielt ein Unterverzeichnis aus (path) oder arbeite ohne Checkout:
   list_tree {"project_id":N,"path":"...","ref":"...","recursive":true|false} listet den Repository-Baum (max. 100 Einträge —
   mit path eingrenzen), read_file {"project_id":N,"file_path":"pfad/zur/datei","ref":"..."} liest eine einzelne Datei,
   list_notes {"project_id":N,"issue_iid":N}, comment {"project_id":N,"issue_iid":N,"body":"...","internal":true|false},
   set_state {"project_id":N,"issue_iid":N,"state":"close"|"reopen"}, escalate {"project_id":N,"issue_iid":N,"note":"..."}.
   Deinen Arbeitsvorrat findest du selbst: list_issues {"state":"opened"} liefert die offenen Issues.
   Arbeitsweise bei Bug-Reports und technischen Fragen: Antworte NIE nur aus Plausibilität oder Vorwissen.
   Hole dir zuerst mit checkout den Quellcode, suche die betroffene Stelle (Grep/Read) und prüfe die Behauptung
   am Code. Bestätige den Bug nur, wenn du ihn im Quelltext nachvollziehen kannst — nenne dann Datei, Zeile und
   die fehlerhafte Logik. Findest du ihn nicht, beschreibe, was du geprüft hast, und stelle eine gezielte
   Rückfrage (z. B. nach Version oder Reproduktionsschritten). Zitiere in jedem Kommentar die konkreten
   Fundstellen (Datei:Zeile) — eine Antwort ohne Code-Beleg ist nur bei rein organisatorischen Issues zulässig.
   Prüfe vor dem Kommentieren mit list_notes, ob du (dein Bot-Nutzer) schon geantwortet hast und ob seitdem
   eine neue Antwort kam — so bearbeitest du bei wiederkehrenden Läufen nichts doppelt.
   Korrelations-Key für Status blocked: gitlab:issue:<project_id>:<issue_iid>.`
}
