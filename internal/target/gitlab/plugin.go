package gitlab

import (
	"context"
	"encoding/json"
	"fmt"
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
		Description: "GitLab-Issues als Arbeitsvorrat: Issues finden (list_projects/list_issues), Quellcode auschecken, Projekt aufsetzen und Bugs am Code verifizieren (checkout + Sandbox-Shell), Fixes entwickeln — auf Feature-Branch committen (commit), Merge Request an den Vorgesetzten eröffnen (create_merge_request) und den Review-Loop leben: bei jedem Heartbeat-Lauf offene MRs auf neues Review-Feedback prüfen (list_merge_requests/list_mr_notes/comment_mr), rote CI selbst diagnostizieren (list_pipelines/list_pipeline_jobs/get_job_log) und auf den Merge reagieren. Intake per HEARTBEAT.md (Polling), Auth per API-Token (Secrets gitlab_token + gitlab_url).",
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

4. Intake per Heartbeat (GitLab hat keinen Webhook — der Agent nimmt Arbeit
   ausschließlich per Polling auf) — in der HEARTBEAT.md des Agenten:
   - alle: 15m nur-wenn: gitlab titel: GitLab-Issues sichten aufgabe: Finde
     offene Issues (list_issues state=opened), bearbeite neue und prüfe per
     list_notes, ob auf deine Rückfragen geantwortet wurde. Bei Bugs: Code
     per checkout holen und die Behauptung am Quelltext verifizieren. Prüfe
     außerdem deine offenen Merge Requests (list_merge_requests state=opened):
     bearbeite neues Review-Feedback (list_mr_notes) und schließe die
     zugehörige Aufgabe ab, sobald ein MR gemergt ist.
   (nur-wenn: gitlab ist optional — die Control Plane weckt den Agenten
    dann nur, wenn mindestens ein offenes Issue im Intake-Scope existiert.
    Prüfe offene MRs am besten in einem eigenen, MR-losen Heartbeat ohne
    nur-wenn:, damit auch Review-Feedback ohne offenes Issue geweckt wird.)
   Optionaler Projekt-Filter (gilt für list_issues/list_projects):
   COVEY_GITLAB_INTAKE_PROJECTS="gruppe/support"   (leer = alle)

Details: docs/betrieb-gitlab.md im Repository.`,
	})
}

func (System) Name() string { return "gitlab" }

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

// HasWork (target.WorkChecker): billiger Vorab-Check der Control Plane für
// nur-wenn:-Heartbeats — gibt es mindestens ein offenes Issue im Intake-Scope?
// Nutzt denselben Pfad wie list_issues (globales GET /issues, danach
// COVEY_GITLAB_INTAKE_PROJECTS-Filter): was der Agent nicht sähe, weckt ihn
// auch nicht. Anders als das Gelesen-Flag bei E-Mail bleibt ein offenes Issue
// so lange „Arbeit", bis es geschlossen ist — der Heartbeat feuert also in
// jedem Intervall, solange irgendein Issue offen ist; gespart wird nur die
// Leerlauf-Phase ohne offene Issues.
func (System) HasWork(ctx context.Context, cred target.Credential) (bool, error) {
	gc := NewClient(cred.BaseURL, cred.Token)
	issues, err := gc.ListIssues(ctx, 0, "opened", "", "", false)
	if err != nil {
		return false, err
	}
	for _, i := range issues {
		if projectInScope(i.ProjectID, issueProjectPath(i)) {
			return true, nil
		}
	}
	return false, nil
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
		ProjectID  int    `json:"project_id"`
		IssueIID   int    `json:"issue_iid"`
		MRIID      int    `json:"mr_iid"`
		PipelineID int    `json:"pipeline_id"`
		JobID      int    `json:"job_id"`
		Body       string `json:"body"`
		Internal   *bool  `json:"internal"`
		State      string `json:"state"`
		Note       string `json:"note"`
		Labels     string `json:"labels"`
		Search     string `json:"search"`
		Ref        string `json:"ref"`
		Assigned   bool   `json:"assigned"`
		Path       string `json:"path"`
		FilePath   string `json:"file_path"`
		Recursive  bool   `json:"recursive"`
		Sha        string `json:"sha"`
		Since      string `json:"since"`
		Target     string `json:"target_branch"`
		Username   string `json:"username"`
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
	case "get_merge_request":
		if in.ProjectID == 0 || in.MRIID == 0 {
			return nil, fmt.Errorf("project_id oder mr_iid fehlt")
		}
		return gc.GetMergeRequest(ctx, in.ProjectID, in.MRIID)
	case "list_mr_notes":
		if in.ProjectID == 0 || in.MRIID == 0 {
			return nil, fmt.Errorf("project_id oder mr_iid fehlt")
		}
		return gc.ListMRNotes(ctx, in.ProjectID, in.MRIID)
	case "comment_mr":
		if in.ProjectID == 0 || in.MRIID == 0 || strings.TrimSpace(in.Body) == "" {
			return nil, fmt.Errorf("project_id, mr_iid oder body fehlt")
		}
		return gc.CommentMR(ctx, in.ProjectID, in.MRIID, in.Body)
	case "list_pipelines":
		if in.ProjectID == 0 {
			return nil, fmt.Errorf("project_id fehlt")
		}
		return gc.ListPipelines(ctx, in.ProjectID, in.Ref)
	case "list_pipeline_jobs":
		if in.ProjectID == 0 || in.PipelineID == 0 {
			return nil, fmt.Errorf("project_id oder pipeline_id fehlt")
		}
		return gc.ListPipelineJobs(ctx, in.ProjectID, in.PipelineID)
	case "retry_pipeline":
		if in.ProjectID == 0 || in.PipelineID == 0 {
			return nil, fmt.Errorf("project_id oder pipeline_id fehlt")
		}
		return gc.RetryPipeline(ctx, in.ProjectID, in.PipelineID)
	case "get_job_log":
		if in.ProjectID == 0 || in.JobID == 0 {
			return nil, fmt.Errorf("project_id oder job_id fehlt")
		}
		logText, truncated, err := gc.GetJobLog(ctx, in.ProjectID, in.JobID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"job_id": in.JobID, "log": logText, "truncated": truncated}, nil
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
   list_merge_requests {"project_id":N,"state":"opened"|"merged"|"closed"|"all","search":"...","target_branch":"..."},
   get_merge_request {"project_id":N,"mr_iid":N} liefert einen einzelnen MR mit Review-Zustand (detailed_merge_status,
   has_conflicts) und CI-Ergebnis (head_pipeline), list_mr_notes {"project_id":N,"mr_iid":N} den Diskussionsstand eines MR
   (Review-Kommentare), comment_mr {"project_id":N,"mr_iid":N,"body":"..."} antwortet im Review-Dialog,
   list_pipelines {"project_id":N,"ref":"branch (optional)"} listet CI-Läufe — prüfe damit nach jedem Push, ob die
   Pipeline deines Branches grün ist. Ist sie ROT, diagnostiziere selbst statt zu raten oder zu fragen:
   list_pipeline_jobs {"project_id":N,"pipeline_id":N} zeigt die Jobs mit Status, get_job_log {"project_id":N,"job_id":N}
   liefert das Log-Ende des fehlgeschlagenen Jobs — Ursache beheben, erneut committen, Pipeline erneut prüfen.
   Scheitert ein Job an Infrastruktur (Runner fehlt, Registry down, fehlender Repo-Zugriff), gehört das als
   Befund in den MR-Kommentar. Ist so eine externe Ursache später behoben (z. B. Zugriff nachträglich erteilt),
   starte den Lauf mit retry_pipeline {"project_id":N,"pipeline_id":N} neu und prüfe danach das Ergebnis —
   melde grün gewordene Pipelines kurz per comment_mr.
   WICHTIG — kein Busy-Waiting auf CI: Läuft eine Pipeline noch, prüfe ihren Status höchstens zweimal.
   Ist sie dann immer noch nicht fertig, beende deinen Lauf regulär mit done (Zwischenstand als add_note) —
   dein nächster Heartbeat-Lauf prüft das Ergebnis. Minutenlanges Status-Polling verschwendet dein Turn-Budget.
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
   2. Projekt AUFSETZEN wie ein neuer Kollege: README/CONTRIBUTING lesen, Abhängigkeiten installieren
      (npm install / pip install / go mod download …), einmal Build und Tests laufen lassen, BEVOR du etwas
      änderst — so kennst du den grünen Ausgangszustand und siehst, ob ein Fehlschlag von dir kommt.
   3. Fix lokal im Checkout editieren — minimal-invasiv, Stil der Umgebung übernehmen.
   4. VERIFIZIEREN, bevor du pushst: die Tests des Projekts im Checkout ausführen (bzw. Build/Kompilier-Check,
      wenn es keine Tests gibt) und für den Fix möglichst einen Test ergänzen. Schlagen Tests fehl, pushe NICHT.
   5. commit auf einen sprechenden Feature-Branch (z. B. fix/issue-<iid>-kurzbeschreibung).
   6. create_merge_request an deinen Vorgesetzten; verweise in der description auf das Issue (#<iid>),
      beschreibe Ursache, Fix und wie du ihn verifiziert hast (welche Tests liefen). Hat das Projekt CI,
      prüfe mit get_merge_request bzw. list_pipelines, ob die Pipeline deines Branches grün wird.
   7. Im Issue kommentieren: Link zum MR, kurze Zusammenfassung. Das Issue NICHT selbst schließen —
      das passiert beim Merge bzw. durch deinen Vorgesetzten.
   8. Aufgabe mit done beenden — NICHT blocken. GitLab hat keinen Webhook; auf Review wird per Polling
      gewartet, nicht mit Status blocked. Dein nächster Heartbeat-Lauf prüft deine offenen MRs auf
      Review-Feedback und den Merge-Zustand. (Ein blocked würde hier nie geweckt und blockierte deinen
      Heartbeat dauerhaft.)
   Review-Feedback einarbeiten — bei JEDEM Heartbeat-Lauf, nicht nur bei neuen Issues: hole mit
   list_merge_requests {"state":"opened"} deine offenen MRs und prüfe jeden mit list_mr_notes auf neue
   Review-Kommentare seit deiner letzten Antwort. Verlangt Feedback Änderungen, hole dir mit checkout
   (ref=source_branch) den Branch, arbeite JEDEN Punkt ein, führe die Tests erneut aus und pushe mit commit
   auf denselben Branch (ohne start_branch — der Branch existiert). Antworte mit comment_mr, was du geändert
   hast. Bist du anderer Meinung, begründe das im comment_mr am Code statt blind zu ändern. Prüfe mit
   list_merge_requests {"state":"merged"} bzw. get_merge_request, ob ein MR inzwischen gemergt wurde —
   dann kommentiere im zugehörigen Issue das Ergebnis; wurde er ohne Merge geschlossen (state="closed"),
   prüfe per list_mr_notes warum und eskaliere, wenn unklar. Prüfe vor jeder MR-Antwort mit list_mr_notes,
   ob du auf den aktuellen Stand schon reagiert hast — so bearbeitest du bei wiederkehrenden Läufen nichts doppelt.
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
   WICHTIG — NIE mit Status blocked enden: GitLab nimmt Arbeit rein per Polling auf, es gibt keinen Webhook,
   der eine geblockte Aufgabe wieder weckt. Warte weder auf eine Issue-Antwort noch auf ein MR-Review mit
   blocked — beende jeden Lauf mit done und lass offene Issues/MRs von deinem nächsten Heartbeat-Lauf
   erneut aufgreifen.`
}
