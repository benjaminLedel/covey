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
// Webhook-Eingang (Token-Prüfung, Idempotenz, Korrelation), die
// Agent-Aktionen und die Aktions-Doku für den System-Prompt.
type System struct{}

func init() {
	target.Register(target.Descriptor{
		Name:        "gitlab",
		Label:       "GitLab",
		Description: "GitLab-Issues als Arbeitsvorrat: Issues finden (list_projects/list_issues), Quellcode auschecken und Bugs am Code verifizieren (checkout), Fixes entwickeln — auf Feature-Branch committen (commit) und Merge Request an den Vorgesetzten eröffnen (create_merge_request) —, kommentieren, schließen, eskalieren. Intake per HEARTBEAT.md (Polling) oder optionalem Webhook, Auth per API-Token (Secrets gitlab_token + gitlab_url).",
		Kind:        "builtin",
		System:      System{},
		SetupDoc: `1. In GitLab einen eigenen Bot-Nutzer anlegen (z. B. covey-bot), den
   Zielprojekten hinzufügen und als dieser Nutzer ein Access Token mit
   Scope "api" erzeugen. Rolle: Reporter reicht fürs Lesen/Kommentieren;
   soll der Agent Fixes pushen und Merge Requests eröffnen (commit /
   create_merge_request), braucht er Developer.

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
		Sha       string `json:"sha"`
		Since     string `json:"since"`
		Target    string `json:"target_branch"`
		Username  string `json:"username"`
		// Entwickler-Workflow: commit + create_merge_request.
		Branch       string   `json:"branch"`
		StartBranch  string   `json:"start_branch"`
		Message      string   `json:"message"`
		CheckoutPath string   `json:"checkout_path"`
		Files        []string `json:"files"`
		Deleted      []string `json:"deleted"`
		SourceBranch string   `json:"source_branch"`
		Title        string   `json:"title"`
		Description  string   `json:"description"`
		Assignee     string   `json:"assignee"`
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
	case "list_commits":
		if in.ProjectID == 0 {
			return nil, fmt.Errorf("project_id fehlt")
		}
		return gc.ListCommits(ctx, in.ProjectID, in.Ref, in.Path, in.Since)
	case "get_commit":
		if in.ProjectID == 0 || in.Sha == "" {
			return nil, fmt.Errorf("project_id oder sha fehlt")
		}
		return gc.GetCommitDiff(ctx, in.ProjectID, in.Sha)
	case "list_merge_requests":
		if in.ProjectID == 0 {
			return nil, fmt.Errorf("project_id fehlt")
		}
		return gc.ListMergeRequests(ctx, in.ProjectID, in.State, in.Search, in.Target)
	case "list_branches":
		if in.ProjectID == 0 {
			return nil, fmt.Errorf("project_id fehlt")
		}
		return gc.ListBranches(ctx, in.ProjectID, in.Search)
	case "commit":
		if in.ProjectID == 0 {
			return nil, fmt.Errorf("project_id fehlt")
		}
		return CommitFromCheckout(ctx, gc, in.ProjectID, in.Branch, in.StartBranch,
			in.Message, in.CheckoutPath, in.Files, in.Deleted, target.Workdir(ctx))
	case "create_merge_request":
		if in.ProjectID == 0 || strings.TrimSpace(in.SourceBranch) == "" || strings.TrimSpace(in.Title) == "" {
			return nil, fmt.Errorf("project_id, source_branch oder title fehlt")
		}
		targetBranch := strings.TrimSpace(in.Target)
		if targetBranch == "" {
			proj, err := gc.GetProject(ctx, in.ProjectID)
			if err != nil {
				return nil, err
			}
			targetBranch = proj.DefaultBranch
		}
		if in.SourceBranch == targetBranch {
			return nil, fmt.Errorf("source_branch und target_branch sind identisch (%q)", targetBranch)
		}
		// Der Reviewer (idR der Vorgesetzte) muss auflösbar sein — ein MR
		// ohne benannten Menschen als Empfänger ist hier nicht vorgesehen.
		if strings.TrimSpace(in.Assignee) == "" {
			return nil, fmt.Errorf("assignee fehlt — trage den GitLab-Username deines Vorgesetzten aus dem Team-Verzeichnis ein")
		}
		u, err := gc.LookupUser(ctx, in.Assignee)
		if err != nil {
			return nil, err
		}
		return gc.CreateMergeRequest(ctx, in.ProjectID, in.SourceBranch, targetBranch,
			in.Title, in.Description, u.ID, u.ID)
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
	case "assign":
		if in.ProjectID == 0 || in.IssueIID == 0 {
			return nil, fmt.Errorf("project_id oder issue_iid fehlt")
		}
		u, err := gc.LookupUser(ctx, in.Username)
		if err != nil {
			return nil, err
		}
		if err := gc.AssignIssue(ctx, in.ProjectID, in.IssueIID, []int{u.ID}); err != nil {
			return nil, err
		}
		return map[string]any{"assigned_to": u.Username, "user_id": u.ID}, nil
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
   set_state {"project_id":N,"issue_iid":N,"state":"close"|"reopen"}, escalate {"project_id":N,"issue_iid":N,"note":"..."},
   assign {"project_id":N,"issue_iid":N,"username":"gitlab-username"} weist das Issue einer Person zu — z. B. nach einem
   Fix dem Teammitglied, das laut Team-Verzeichnis fürs Testen zuständig ist; nimm den GitLab-Username exakt aus dem
   Abschnitt "Team (menschliche Mitarbeiter)" deines Prompts und erkläre die Übergabe in einem Kommentar,
   list_branches {"project_id":N,"search":"..."} listet Branches (Default-Branch ist markiert — rate keine Branch-Namen),
   list_commits {"project_id":N,"ref":"...","path":"datei/oder/verzeichnis","since":"ISO-Datum"} listet die Commit-Historie
   (alle Filter optional), get_commit {"project_id":N,"sha":"..."} liefert den Diff eines Commits,
   list_merge_requests {"project_id":N,"state":"opened"|"merged"|"closed"|"all","search":"...","target_branch":"..."}.
   Schreibende Entwickler-Aktionen:
   commit {"project_id":N,"branch":"fix/…","start_branch":"main (optional, Default: Default-Branch)","message":"...",
   "checkout_path":"<Pfad aus dem checkout-Ergebnis>","files":["repo/relativer/pfad.go",...],"deleted":["alt.go",...]} —
   pusht deine lokal editierten Dateien als EINEN Commit auf den Branch; existiert der Branch nicht, wird er vom
   start_branch abgezweigt. Direkte Commits auf den Default-Branch sind verboten — der Weg dorthin führt über:
   create_merge_request {"project_id":N,"source_branch":"fix/…","target_branch":"main (optional, Default: Default-Branch)",
   "title":"...","description":"...","assignee":"gitlab-username"} — eröffnet den Merge Request; als assignee trägst du
   deinen Vorgesetzten aus dem Team-Verzeichnis ein (er wird Assignee UND Reviewer), der Source-Branch wird nach dem
   Merge automatisch entfernt.
   Arbeitsweise als Entwickler — wenn du einen Bug nicht nur bestätigst, sondern behebst:
   1. checkout des Projekts, den Fehler am Code nachvollziehen (Datei:Zeile).
   2. Fix lokal im Checkout editieren — minimal-invasiv, Stil der Umgebung übernehmen.
   3. VERIFIZIEREN, bevor du pushst: die Tests des Projekts im Checkout ausführen (bzw. Build/Kompilier-Check,
      wenn es keine Tests gibt) und für den Fix möglichst einen Test ergänzen. Schlagen Tests fehl, pushe NICHT.
   4. commit auf einen sprechenden Feature-Branch (z. B. fix/issue-<iid>-kurzbeschreibung).
   5. create_merge_request an deinen Vorgesetzten; verweise in der description auf das Issue (#<iid>),
      beschreibe Ursache, Fix und wie du ihn verifiziert hast (welche Tests liefen).
   6. Im Issue kommentieren: Link zum MR, kurze Zusammenfassung. Das Issue NICHT selbst schließen —
      das passiert beim Merge bzw. durch deinen Vorgesetzten.
   Deinen Arbeitsvorrat findest du selbst: list_issues {"state":"opened"} liefert die offenen Issues.
   Arbeitsweise bei Bug-Reports und technischen Fragen: Antworte NIE nur aus Plausibilität oder Vorwissen.
   Prüfe IMMER ZUERST, ob der gemeldete Fehler inzwischen schon behoben ist: list_commits auf dem relevanten
   Branch mit since=Erstellungsdatum des Issues (und ohne path-Filter — der Fix kann in einer ganz anderen
   Schicht liegen als vermutet, z. B. Frontend statt Backend), dazu list_merge_requests mit passenden
   Suchbegriffen. Klingt ein Commit-Titel nach dem gemeldeten Problem, prüfe seinen Diff mit get_commit.
   Ist der Fehler bereits behoben, antworte genau das — nenne Commit (SHA, Titel, Datum) — und bestätige
   den Bug NICHT erneut; schlage vor, das Issue zu schließen, sobald der Fix deployt ist.
   Erst danach: hole dir mit checkout den Quellcode, suche die betroffene Stelle (Grep/Read) und prüfe die
   Behauptung am Code. Verfolge dabei den gemeldeten Weg vollständig — vom UI-Element über den tatsächlich
   aufgerufenen Endpoint bis zur Verarbeitung; bestätige keinen Verdacht in einer Schicht, ohne die anderen
   (Frontend, Routing, Backend) zumindest geprüft zu haben. Bestätige den Bug nur, wenn du ihn im Quelltext
   nachvollziehen kannst — nenne dann Datei, Zeile und die fehlerhafte Logik. Findest du ihn nicht, beschreibe,
   was du geprüft hast, und stelle eine gezielte Rückfrage (z. B. nach Version oder Reproduktionsschritten).
   Zitiere in jedem Kommentar die konkreten Fundstellen (Datei:Zeile) — eine Antwort ohne Code-Beleg ist nur
   bei rein organisatorischen Issues zulässig.
   Prüfe vor dem Kommentieren mit list_notes, ob du (dein Bot-Nutzer) schon geantwortet hast und ob seitdem
   eine neue Antwort kam — so bearbeitest du bei wiederkehrenden Läufen nichts doppelt.
   Korrelations-Key für Status blocked: gitlab:issue:<project_id>:<issue_iid>.`
}
