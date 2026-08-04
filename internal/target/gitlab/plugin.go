package gitlab

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
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
		Description: "GitLab-Issues als Arbeitsvorrat: Issues finden (list_projects/list_issues, auch nach Meilenstein), extern gemeldete Bugs als Ticket anlegen (create_issue), den Arbeitszustand im Board führen (set_labels/assign), Quellcode auschecken, Projekt aufsetzen und Bugs am Code verifizieren (checkout + Sandbox-Shell), an Issues angehängte Screenshots/Bilder lesen (download_upload + Vision), eigene Screenshots an einen MR/ein Issue anhängen (upload + comment_mr), Fixes entwickeln — auf Feature-Branch committen (commit), Merge Request an den Vorgesetzten eröffnen (create_merge_request, optional mit QA-Agent als reviewer) und den Review-Loop leben: bei jedem Heartbeat-Lauf offene MRs auf neues Review-Feedback prüfen (list_merge_requests/list_mr_notes/comment_mr), rote CI selbst diagnostizieren (list_pipelines/list_pipeline_jobs/get_job_log) und auf den Merge reagieren. Auch als QA-/Test-Agent nutzbar: fremde MRs, in denen man als Reviewer eingetragen ist, end-to-end testen und Feedback geben (set_reviewer/approve_mr, nur-wenn: gitlab:review). Intake per HEARTBEAT.md (Polling), Auth per API-Token (Secrets gitlab_token + gitlab_url).",
		Kind:        "builtin",
		Category:    target.CategoryCode,
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
   ausschließlich per Polling auf) — in der HEARTBEAT.md des Agenten zwei
   getrennte, je eigen gegatete Einträge:
   - alle: 15m nur-wenn: gitlab:issues titel: GitLab-Issues sichten aufgabe:
     Finde offene Issues (list_issues state=opened), bearbeite neue und prüfe
     per list_notes, ob auf deine Rückfragen geantwortet wurde. Bei Bugs: Code
     per checkout holen und die Behauptung am Quelltext verifizieren.
   - alle: 15m nur-wenn: gitlab:mr titel: Merge Requests betreuen aufgabe:
     Prüfe deine offenen Merge Requests (list_merge_requests state=opened) auf
     neues Review-Feedback (list_mr_notes), arbeite es ein und reagiere auf
     Merge/Close.
   (Der Unterscope nach dem Doppelpunkt spart den teuren Agenten-Lauf gezielt:
    nur-wenn: gitlab:issues feuert bei IRGENDEINEM offenen Issue im Intake-Scope
    (für Agenten, die alle offenen Issues triagieren),
    nur-wenn: gitlab:mr nur, wenn einer deiner offenen MRs unbeantwortetes
    Review-Feedback hat. So laufen beide Tasks getrennt, ohne dass der eine
    für die Arbeit des anderen mit-feuert. nur-wenn: gitlab ohne Unterscope
    prüft beides gemeinsam — nur nötig, wenn du beide Jobs in EINEM Task willst.)
    WICHTIG — bearbeitet dein Playbook nur DIR ZUGEWIESENE Issues (list_issues
    assigned=true), nutze nur-wenn: gitlab:issues:assigned. Dann weckt dich nur
    ein dir zugewiesenes offenes Issue — sonst würde jedes fremde offene Issue
    im Scope deinen Agenten in jedem Intervall unnötig starten.
   Optionaler Projekt-Filter (gilt für list_issues/list_projects):
   COVEY_GITLAB_INTAKE_PROJECTS="gruppe/support"   (leer = alle)

Details: docs/ops-gitlab.md im Repository.`,
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

// mrProjectPath leitet den Projektpfad aus der vollen MR-Referenz
// ("gruppe/projekt!9") ab — analog issueProjectPath, aber getrennt am "!".
func mrProjectPath(m MergeRequest) string {
	if idx := strings.LastIndex(m.References.Full, "!"); idx > 0 {
		return m.References.Full[:idx]
	}
	return ""
}

// HasWork (target.WorkChecker): billiger Vorab-Check der Control Plane für
// nur-wenn:-Heartbeats. Ohne Webhook nimmt GitLab rein per Polling auf; dieser
// Check spart den (teuren) Agenten-Wake, wenn es gerade nichts zu tun gibt.
// Arbeit liegt vor, wenn EINES gilt:
//
//   - Es gibt ein offenes Issue im Intake-Scope (globales GET /issues, danach
//     COVEY_GITLAB_INTAKE_PROJECTS-Filter) — was der Agent nicht sähe, weckt
//     ihn auch nicht —, auf das der Bot noch nicht als Letzter geantwortet hat.
//   - Der Bot hat einen offenen, selbst eröffneten Merge Request mit
//     unbeantwortetem Review-Feedback (der letzte Nicht-System-Kommentar stammt
//     von jemand anderem als dem Bot). Das trägt den Review-Loop ohne Webhook.
//
// Der Merge-Abschluss braucht keinen eigenen Zweig: ist das zugehörige Issue
// noch offen, weckt es über den Issue-Zweig; ist es beim Merge automatisch
// geschlossen worden, gibt es nichts mehr zu tun. Maßgeblich ist überall die
// **Flanke** (hat sich seit dem letzten Zug des Bots etwas getan?), nicht der
// Pegel (steht irgendwo etwas offen?) — sonst weckt derselbe unerledigte Vorgang
// den Agenten in jedem Intervall erneut.
func (System) HasWork(ctx context.Context, cred target.Credential) (bool, error) {
	has, _, err := System{}.HasWorkSigned(ctx, cred, "")
	return has, err
}

// HasWorkKind (target.KindWorkChecker) gatet eine einzelne Arbeits-Art, damit
// mehrere Heartbeats (nur-wenn: gitlab:issues, :mr, :review) getrennt feuern:
//
//   - "issues"/"issue"  → wartet IRGENDEIN offenes Issue im Intake-Scope auf
//     eine Reaktion (für Agenten, die alle offenen Issues triagieren)?
//   - "issues:assigned"/"assigned" → wartet ein offenes Issue, das dem Bot-
//     Nutzer selbst ZUGEWIESEN ist (scope=assigned_to_me)? Genau das braucht ein
//     Agent, dessen Playbook nur seine eigenen Issues bearbeitet (list_issues
//     assigned=true) — sonst weckt ihn jedes fremde offene Issue im Scope.
//     „Wartet" heißt in beiden Fällen: der Bot hat dort noch nicht als Letzter
//     kommentiert (siehe issueWorkPending).
//   - "mr"/"mrs"        → wartet einer der SELBST eröffneten MRs des Bots auf
//     Antwort (Autoren-Sicht, der Entwickler-Review-Loop)?
//   - "review"/"reviews" → wartet einer der MRs, in denen der Bot als REVIEWER
//     eingetragen ist, auf sein Review (QA-/Test-Sicht)?
//   - sonst             → beides von HasWork, fail-open bei unbekanntem Scope.
func (System) HasWorkKind(ctx context.Context, cred target.Credential, kind string) (bool, error) {
	has, _, err := System{}.HasWorkSigned(ctx, cred, kind)
	return has, err
}

// HasWorkSigned (target.SignedWorkChecker) ist die eigentliche Prüfung: sie
// liefert neben dem Ja/Nein die Signatur der wartenden Vorgänge, damit die
// Control Plane nicht zweimal auf denselben Stand weckt. Ein Agent darf einen
// Lauf dadurch schweigend beenden — die Rückmeldung des QA-Kollegen war eine
// Freigabe, es gibt nichts zu tun — ohne im nächsten Intervall erneut geweckt
// zu werden. Kommt dagegen ein neuer Beitrag oder ein Push dazu, ändert sich
// die Signatur und der Agent wacht auf. Ob eine Rückmeldung Arbeit bedeutet
// (gemeldete Mängel) oder nur Information (Freigabe), entscheidet damit der
// Agent und nicht das Gate.
func (System) HasWorkSigned(ctx context.Context, cred target.Credential, kind string) (bool, string, error) {
	gc := NewClient(cred.BaseURL, cred.Token)
	var (
		waiting []string
		err     error
	)
	switch kind {
	case "issues", "issue":
		waiting, err = issueWorkPending(ctx, gc, false)
	case "issues:assigned", "issue:assigned", "assigned":
		waiting, err = issueWorkPending(ctx, gc, true)
	case "mr", "mrs":
		waiting, err = mrReviewPending(ctx, gc)
	case "review", "reviews":
		waiting, err = mrReviewAssignedPending(ctx, gc)
	default:
		// Ohne Unterscope zählt beides — Issues UND der eigene Review-Loop.
		waiting, err = issueWorkPending(ctx, gc, false)
		if err == nil {
			var mrs []string
			if mrs, err = mrReviewPending(ctx, gc); err == nil {
				waiting = append(waiting, mrs...)
			}
		}
	}
	if err != nil {
		return false, "", err
	}
	return len(waiting) > 0, workSig(waiting), nil
}

// issueMaxNotesChecks begrenzt die Kommentar-Prüfung von issueWorkPending: der
// Check läuft in jedem Heartbeat-Intervall und darf nicht mit der Zahl offener
// Issues davonlaufen. Wer mehr offene Issues hat als das, wird geweckt — die
// Zuordnung „was davon ist neu" trifft dann der Agent selbst.
const issueMaxNotesChecks = 30

// issueWorkPending: wartet mindestens ein offenes Issue im Intake-Scope auf den
// Agenten? Globales GET /issues, danach COVEY_GITLAB_INTAKE_PROJECTS-Filter —
// was der Agent per list_issues nicht sähe, weckt ihn auch nicht.
// assignedOnly=true zählt nur die dem Bot-Nutzer zugewiesenen Issues
// (scope=assigned_to_me) — passend zu einem Playbook, das ausschließlich
// zugewiesene Issues bearbeitet; sonst würde jedes fremde offene Issue im Scope
// den Agenten wecken.
//
// Entscheidend ist die Flanke, nicht der Pegel: Ein offenes Issue ist Arbeit,
// solange der letzte Nicht-System-Kommentar NICHT vom Bot stammt (oder es noch
// gar keinen gibt — dann steht die Erst-Triage aus). Hat der Bot zuletzt
// geschrieben, ruht das Issue, bis jemand antwortet. Ohne diese Kante bliebe ein
// dauerhaft zugewiesenes Issue „ewig Arbeit" und der Heartbeat weckte den
// Agenten in jedem Intervall neu auf dieselbe, längst erledigte Sache — dieselbe
// Logik trägt bereits mrReviewPending/mrReviewAssignedPending.
//
// Der Vertrag daraus: **Ein Agent, der an einem Issue gearbeitet hat, muss dort
// kommentieren.** Ein stiller Lauf gilt als „noch nicht bearbeitet" und weckt
// erneut. Das Playbook „Issue-Triage" hält das so.
func issueWorkPending(ctx context.Context, gc *Client, assignedOnly bool) ([]string, error) {
	issues, err := gc.ListIssues(ctx, 0, "opened", "", "", "", assignedOnly)
	if err != nil {
		return nil, err
	}
	inScope := issues[:0]
	for _, i := range issues {
		if projectInScope(i.ProjectID, issueProjectPath(i)) {
			inScope = append(inScope, i)
		}
	}
	if len(inScope) == 0 {
		return nil, nil
	}
	if len(inScope) > issueMaxNotesChecks {
		// Zu viele offene Issues für die Kommentar-Prüfung: wecken, ohne sie
		// einzeln anzusehen. Die Signatur trägt dann nur die Anzahl — sie
		// ändert sich, sobald ein Issue dazukommt oder wegfällt.
		return []string{fmt.Sprintf("issues:many@%d", len(inScope))}, nil
	}
	me, err := gc.CurrentUser(ctx)
	if err != nil {
		return nil, err
	}
	var waiting []string
	for _, i := range inScope {
		notes, err := gc.ListNotes(ctx, i.ProjectID, i.IID)
		if err != nil {
			return nil, err
		}
		if lastHumanNoteIsMine(notes, me.Username) {
			continue // schon beantwortet — ruht, bis jemand darauf antwortet
		}
		waiting = append(waiting, threadSig("issue", i.ProjectID, i.IID, notes))
	}
	return waiting, nil
}

// threadSig beschreibt einen wartenden Vorgang so, dass sich die Beschreibung
// genau dann ändert, wenn dort etwas Neues passiert ist: Projekt, Nummer und
// die höchste Note-ID des Threads. GitLab vergibt Note-IDs monoton und
// vermerkt auch Pushes als System-Note — neue Commits ändern die Signatur
// also mit, ohne dass es einen zusätzlichen Request kostet.
func threadSig(kind string, projectID, iid int, notes []Note) string {
	last := 0
	for _, n := range notes {
		if n.ID > last {
			last = n.ID
		}
	}
	return fmt.Sprintf("%s%d!%d@%d", kind, projectID, iid, last)
}

// workSig fasst die wartenden Vorgänge zu einer stabilen Signatur zusammen.
// Sortiert, weil GitLab nach updated_at liefert — sonst wechselte die Signatur
// allein durch die Reihenfolge.
func workSig(waiting []string) string {
	if len(waiting) == 0 {
		return ""
	}
	sorted := append([]string(nil), waiting...)
	sort.Strings(sorted)
	return strings.Join(sorted, ",")
}

// lastHumanNoteIsMine sagt, ob der letzte Nicht-System-Kommentar eines Threads
// vom Bot selbst stammt. Ohne jeden menschlichen Kommentar ist die Antwort
// false: ein unkommentierter Thread wartet auf den ersten Zug.
func lastHumanNoteIsMine(notes []Note, me string) bool {
	for i := len(notes) - 1; i >= 0; i-- {
		if notes[i].System {
			continue
		}
		return notes[i].Author.Username == me
	}
	return false
}

// mrReviewPending prüft, ob einer der offenen, selbst eröffneten Merge Requests
// des Bots auf eine Antwort wartet: Der letzte menschliche (Nicht-System-)
// Kommentar im Thread stammt nicht vom Bot. Frische MRs ganz ohne Kommentare
// (der Bot hat gerade eröffnet, das Review steht noch aus) zählen NICHT als
// Arbeit — sonst würde jeder offene MR den Agenten in jedem Intervall wecken.
func mrReviewPending(ctx context.Context, gc *Client) ([]string, error) {
	mrs, err := gc.ListMyOpenMergeRequests(ctx)
	if err != nil {
		return nil, err
	}
	inScope := mrs[:0]
	for _, m := range mrs {
		if projectInScope(m.ProjectID, mrProjectPath(m)) {
			inScope = append(inScope, m)
		}
	}
	if len(inScope) == 0 {
		return nil, nil
	}
	me, err := gc.CurrentUser(ctx)
	if err != nil {
		return nil, err
	}
	var waiting []string
	for _, m := range inScope {
		notes, err := gc.ListMRNotes(ctx, m.ProjectID, m.IID)
		if err != nil {
			return nil, err
		}
		// Notes kommen chronologisch (sort=asc); der letzte Nicht-System-
		// Kommentar entscheidet. Ist er von jemand anderem als dem Bot, wartet
		// Review-Feedback auf Bearbeitung.
		for i := len(notes) - 1; i >= 0; i-- {
			if notes[i].System {
				continue
			}
			if notes[i].Author.Username != me.Username {
				waiting = append(waiting, threadSig("mr", m.ProjectID, m.IID, notes))
			}
			break // letzter menschlicher Kommentar ist vom Bot → schon beantwortet
		}
	}
	return waiting, nil
}

// mrReviewAssignedPending ist das Spiegelbild von mrReviewPending aus der
// Reviewer-Sicht: Wartet einer der offenen Merge Requests, in denen der Bot als
// REVIEWER eingetragen ist, auf sein Review? Das trägt den Review-Loop für einen
// QA-/Test-Agenten ohne Webhook, gegatet über nur-wenn: gitlab:review.
//
// Arbeit liegt vor, wenn seit dem letzten eigenen Kommentar ein Mensch
// geschrieben hat oder der Autor NEUE COMMITS gepusht hat — oder wenn der Bot
// hier noch gar nichts gesagt hat. Anders als beim Autoren-Loop zählt ein
// frischer, an mich zum Review übergebener MR (noch ohne Kommentar) SEHR WOHL
// als Arbeit: genau er wartet auf mein Erst-Review. Hat der Bot zuletzt
// kommentiert, ruht der MR, bis der Autor mit Code reagiert — eine bloße
// Textantwort des Autoren-Agenten („danke für das Review") ist kein Anlass für
// eine neue Review-Runde, sonst schaukeln sich beide Agenten gegenseitig hoch.
func mrReviewAssignedPending(ctx context.Context, gc *Client) ([]string, error) {
	me, err := gc.CurrentUser(ctx)
	if err != nil {
		return nil, err
	}
	mrs, err := gc.ListReviewMergeRequests(ctx, me.Username)
	if err != nil {
		return nil, err
	}
	var waiting []string
	for _, m := range mrs {
		if !projectInScope(m.ProjectID, mrProjectPath(m)) {
			continue
		}
		notes, err := gc.ListMRNotes(ctx, m.ProjectID, m.IID)
		if err != nil {
			return nil, err
		}
		if !lastHumanNoteIsMine(notes, me.Username) {
			waiting = append(waiting, threadSig("mr", m.ProjectID, m.IID, notes))
		}
	}
	return waiting, nil
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

// isDuplicateComment ist die Server-Bremse gegen Kommentar-Loops: ist der neue
// Kommentar-Body identisch zum jüngsten EIGENEN (nicht-System-)Kommentar des
// Bots, wird er nicht erneut gepostet. Fail-open: geht der Wer-bin-ich-Check
// schief, wird normal kommentiert (kein legitimer Kommentar soll blockiert
// werden). Nur die Wiederholung des eigenen letzten Kommentars wird unterdrückt.
func isDuplicateComment(ctx context.Context, gc *Client, notes []Note, body string) bool {
	me, err := gc.CurrentUser(ctx)
	if err != nil || me.Username == "" {
		return false
	}
	var lastOwn, lastAt string
	for _, n := range notes {
		if n.System || n.Author.Username != me.Username {
			continue
		}
		if n.CreatedAt >= lastAt { // ISO8601 ist lexikografisch sortierbar
			lastAt, lastOwn = n.CreatedAt, n.Body
		}
	}
	return lastOwn != "" && strings.TrimSpace(lastOwn) == strings.TrimSpace(body)
}

// aktionsParams ist die Vereinigung aller Parameter, die irgendeine
// GitLab-Aktion braucht. Ein gemeinsames Struct statt eines je Aktion: Der
// Agent schickt ein flaches JSON-Objekt, und was darin fehlt, bleibt schlicht
// leer — das ist die Schnittstelle zum Modell, nicht unsere Wunschform.
type aktionsParams struct {
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
	Milestone  string `json:"milestone"`
	Ref        string `json:"ref"`
	Assigned   bool   `json:"assigned"`
	// set_labels arbeitet additiv/subtraktiv statt die ganze Liste zu
	// überschreiben — sonst nimmt jeder Zustandswechsel die fachlichen
	// Labels mit.
	AddLabels    []string `json:"add_labels"`
	RemoveLabels []string `json:"remove_labels"`
	Path         string   `json:"path"`
	FilePath     string   `json:"file_path"`
	URL          string   `json:"url"`
	Recursive    bool     `json:"recursive"`
	Sha          string   `json:"sha"`
	Since        string   `json:"since"`
	Target       string   `json:"target_branch"`
	Username     string   `json:"username"`
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
	Reviewer     string   `json:"reviewer"`
}

// aktion fuehrt EINE GitLab-Aktion aus. Frueher lag jede davon als Fall in
// einem 300-Zeilen-switch; eine Aktion dazuzunehmen hiess, diese Funktion
// anzufassen. Jetzt ist jede fuer sich lesbar und die Verteilung eine Tabelle.
type aktion func(ctx context.Context, gc *Client, in aktionsParams) (any, error)

// aktionen ist die Verteilung: Name aus dem Daemon-Protokoll auf Ausfuehrung.
// Wer eine Aktion sucht, liest hier einen Namen und springt an eine Stelle,
// statt sich durch die Nachbarn zu scrollen.
var aktionen = map[string]aktion{
	"list_projects": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
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
	},
	"list_issues": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
		issues, err := gc.ListIssues(ctx, in.ProjectID, in.State, in.Labels, in.Search, in.Milestone, in.Assigned)
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
	},
	"get_issue": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
		return gc.GetIssue(ctx, in.ProjectID, in.IssueIID)
	},
	"download_upload": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
		if in.ProjectID == 0 || strings.TrimSpace(in.URL) == "" {
			return nil, fmt.Errorf("project_id oder url fehlt")
		}
		return DownloadUploadToSandbox(ctx, gc, in.ProjectID, in.URL, target.Workdir(ctx))
	},
	"upload": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
		if in.ProjectID == 0 || strings.TrimSpace(in.Path) == "" {
			return nil, fmt.Errorf("project_id oder path fehlt")
		}
		return UploadFromSandbox(ctx, gc, in.ProjectID, in.Path, target.Workdir(ctx))
	},
	"checkout": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
		if in.ProjectID == 0 {
			return nil, fmt.Errorf("project_id fehlt")
		}
		return Checkout(ctx, gc, in.ProjectID, in.Ref, in.Path, target.Workdir(ctx))
	},
	"list_tree": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
		if in.ProjectID == 0 {
			return nil, fmt.Errorf("project_id fehlt")
		}
		return gc.ListTree(ctx, in.ProjectID, in.Path, in.Ref, in.Recursive)
	},
	"read_file": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
		if in.ProjectID == 0 || in.FilePath == "" {
			return nil, fmt.Errorf("project_id oder file_path fehlt")
		}
		content, truncated, err := gc.ReadFile(ctx, in.ProjectID, in.FilePath, in.Ref)
		if err != nil {
			return nil, err
		}
		return map[string]any{"file_path": in.FilePath, "ref": in.Ref,
			"content": content, "truncated": truncated}, nil
	},
	"list_commits": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
		if in.ProjectID == 0 {
			return nil, fmt.Errorf("project_id fehlt")
		}
		return gc.ListCommits(ctx, in.ProjectID, in.Ref, in.Path, in.Since)
	},
	"get_commit": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
		if in.ProjectID == 0 || in.Sha == "" {
			return nil, fmt.Errorf("project_id oder sha fehlt")
		}
		return gc.GetCommitDiff(ctx, in.ProjectID, in.Sha)
	},
	"list_merge_requests": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
		if in.ProjectID == 0 {
			return nil, fmt.Errorf("project_id fehlt")
		}
		return gc.ListMergeRequests(ctx, in.ProjectID, in.State, in.Search, in.Target)
	},
	"get_merge_request": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
		if in.ProjectID == 0 || in.MRIID == 0 {
			return nil, fmt.Errorf("project_id oder mr_iid fehlt")
		}
		return gc.GetMergeRequest(ctx, in.ProjectID, in.MRIID)
	},
	"list_mr_notes": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
		if in.ProjectID == 0 || in.MRIID == 0 {
			return nil, fmt.Errorf("project_id oder mr_iid fehlt")
		}
		return gc.ListMRNotes(ctx, in.ProjectID, in.MRIID)
	},
	"comment_mr": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
		if in.ProjectID == 0 || in.MRIID == 0 || strings.TrimSpace(in.Body) == "" {
			return nil, fmt.Errorf("project_id, mr_iid oder body fehlt")
		}
		if notes, err := gc.ListMRNotes(ctx, in.ProjectID, in.MRIID); err == nil && isDuplicateComment(ctx, gc, notes, in.Body) {
			return map[string]any{"skipped": "duplicate",
				"reason": "identisch zum letzten eigenen Kommentar — nicht erneut gepostet"}, nil
		}
		return gc.CommentMR(ctx, in.ProjectID, in.MRIID, in.Body)
	},
	"set_reviewer": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
		if in.ProjectID == 0 || in.MRIID == 0 {
			return nil, fmt.Errorf("project_id oder mr_iid fehlt")
		}
		u, err := gc.LookupUser(ctx, in.Username)
		if err != nil {
			return nil, err
		}
		if _, err := gc.SetMRReviewer(ctx, in.ProjectID, in.MRIID, []int{u.ID}); err != nil {
			return nil, err
		}
		return map[string]any{"reviewer": u.Username, "user_id": u.ID}, nil
	},
	"approve_mr": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
		if in.ProjectID == 0 || in.MRIID == 0 {
			return nil, fmt.Errorf("project_id oder mr_iid fehlt")
		}
		if err := gc.ApproveMR(ctx, in.ProjectID, in.MRIID); err != nil {
			return nil, err
		}
		return map[string]any{"approved": true, "mr_iid": in.MRIID}, nil
	},
	"list_pipelines": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
		if in.ProjectID == 0 {
			return nil, fmt.Errorf("project_id fehlt")
		}
		return gc.ListPipelines(ctx, in.ProjectID, in.Ref)
	},
	"list_pipeline_jobs": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
		if in.ProjectID == 0 || in.PipelineID == 0 {
			return nil, fmt.Errorf("project_id oder pipeline_id fehlt")
		}
		return gc.ListPipelineJobs(ctx, in.ProjectID, in.PipelineID)
	},
	"retry_pipeline": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
		if in.ProjectID == 0 || in.PipelineID == 0 {
			return nil, fmt.Errorf("project_id oder pipeline_id fehlt")
		}
		return gc.RetryPipeline(ctx, in.ProjectID, in.PipelineID)
	},
	"get_job_log": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
		if in.ProjectID == 0 || in.JobID == 0 {
			return nil, fmt.Errorf("project_id oder job_id fehlt")
		}
		logText, truncated, err := gc.GetJobLog(ctx, in.ProjectID, in.JobID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"job_id": in.JobID, "log": logText, "truncated": truncated}, nil
	},
	"list_branches": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
		if in.ProjectID == 0 {
			return nil, fmt.Errorf("project_id fehlt")
		}
		return gc.ListBranches(ctx, in.ProjectID, in.Search)
	},
	"commit": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
		if in.ProjectID == 0 {
			return nil, fmt.Errorf("project_id fehlt")
		}
		return CommitFromCheckout(ctx, gc, in.ProjectID, in.Branch, in.StartBranch,
			in.Message, in.CheckoutPath, in.Files, in.Deleted, target.Workdir(ctx))
	},
	"create_merge_request": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
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
		// Der Assignee muss auflösbar sein — ein MR ohne benannten Menschen als
		// Empfänger ist hier nicht vorgesehen. Fehlt er, aber ist das
		// zugrundeliegende Issue benannt, fällt der MR an dessen MELDER: Wer den
		// Bedarf aufgeschrieben hat, entscheidet über den Merge. Pauschal den
		// Vorgesetzten einzutragen macht ihn zum Flaschenhals für Arbeit, die er
		// nie angefragt hat.
		assignee := strings.TrimSpace(in.Assignee)
		if assignee == "" && in.IssueIID != 0 {
			iss, err := gc.GetIssue(ctx, in.ProjectID, in.IssueIID)
			if err != nil {
				return nil, err
			}
			assignee = iss.Author.Username
		}
		if assignee == "" {
			return nil, fmt.Errorf("assignee fehlt — trage den GitLab-Username des Issue-Melders ein (ersatzweise deinen Vorgesetzten) oder gib issue_iid mit")
		}
		u, err := gc.LookupUser(ctx, assignee)
		if err != nil {
			return nil, err
		}
		// reviewer optional: ist ein QA-/Test-Agent zuständig, trägst du ihn als
		// Reviewer ein (Assignee bleibt der Vorgesetzte). Ohne reviewer prüft der
		// Assignee selbst — Reviewer = Assignee wie bisher.
		reviewerID := u.ID
		if r := strings.TrimSpace(in.Reviewer); r != "" && r != assignee {
			ru, err := gc.LookupUser(ctx, r)
			if err != nil {
				return nil, err
			}
			reviewerID = ru.ID
		}
		return gc.CreateMergeRequest(ctx, in.ProjectID, in.SourceBranch, targetBranch,
			in.Title, in.Description, u.ID, reviewerID)
	},
	"create_issue": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
		if in.ProjectID == 0 || strings.TrimSpace(in.Title) == "" {
			return nil, fmt.Errorf("project_id oder title fehlt")
		}
		assigneeID := 0
		if a := strings.TrimSpace(in.Assignee); a != "" {
			u, err := gc.LookupUser(ctx, a)
			if err != nil {
				return nil, err
			}
			assigneeID = u.ID
		}
		return gc.CreateIssue(ctx, in.ProjectID, in.Title, in.Description, in.Labels, assigneeID)
	},
	"list_notes": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
		return gc.ListNotes(ctx, in.ProjectID, in.IssueIID)
	},
	"comment": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
		internal := in.Internal == nil || *in.Internal
		if notes, err := gc.ListNotes(ctx, in.ProjectID, in.IssueIID); err == nil && isDuplicateComment(ctx, gc, notes, in.Body) {
			return map[string]any{"skipped": "duplicate",
				"reason": "identisch zum letzten eigenen Kommentar — nicht erneut gepostet"}, nil
		}
		return gc.Comment(ctx, in.ProjectID, in.IssueIID, in.Body, internal)
	},
	"set_state": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
		if in.State == "" {
			return nil, fmt.Errorf("state fehlt")
		}
		return nil, gc.SetState(ctx, in.ProjectID, in.IssueIID, in.State)
	},
	"assign": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
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
	},
	"set_labels": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
		if in.ProjectID == 0 || in.IssueIID == 0 {
			return nil, fmt.Errorf("project_id oder issue_iid fehlt")
		}
		iss, err := gc.SetLabels(ctx, in.ProjectID, in.IssueIID, in.AddLabels, in.RemoveLabels)
		if err != nil {
			return nil, err
		}
		return map[string]any{"issue_iid": iss.IID, "labels": iss.Labels}, nil
	},
	"escalate": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
		note := in.Note
		if note == "" {
			note = "Eskalation durch Covey-Agent."
		}
		return nil, gc.Escalate(ctx, in.ProjectID, in.IssueIID, note)
	},
}

func (System) Execute(ctx context.Context, action string, params json.RawMessage, cred target.Credential) (any, error) {
	fn, ok := aktionen[action]
	if !ok {
		return nil, fmt.Errorf("unbekannte aktion %q", strings.TrimSpace(action))
	}
	var in aktionsParams
	if err := json.Unmarshal(params, &in); err != nil {
		return nil, fmt.Errorf("params: %w", err)
	}
	return fn(ctx, NewClient(cred.BaseURL, cred.Token), in)
}

func (System) PromptDoc() string {
	return `Available GitLab actions: list_projects {}, list_issues {"project_id":N,"state":"opened"|"closed"|"all","labels":"...","search":"...","milestone":"...","assigned":true|false}
   (all fields optional; without project_id all the issues visible to you; assigned=true only the ones assigned
   to your bot user — use that when your playbook only provides for assigned issues; milestone is the
   TITLE of the milestone exactly as in GitLab and is the most reliable filter when your assignment hangs off an
   engagement — every issue carries its milestone back in the field "milestone").
   CAUTION: list_issues returns at most 100 hits and does NOT tell you that it truncated. If you get exactly
   100 back, the list is probably incomplete — narrow it further with project_id, milestone, labels or
   state instead of taking it for complete, get_issue {"project_id":N,"issue_iid":N},
   download_upload {"project_id":N,"url":"/uploads/<secret>/<file>.png"} — loads an upload attached to an issue/MR
   (a screenshot, an image) into your sandbox and returns the local path; then look at it with the read tool
   (vision). IMPORTANT: if an issue description or a comment contains an image attachment — in the Markdown syntax
   ![...](/uploads/<32-hex-secret>/<file>) — you can NOT derive the image from the text. ALWAYS download it first
   with download_upload and LOOK AT IT (Read) before you take a screenshot/an image into account in your analysis;
   pass in "url" the reference exactly as it stands between the brackets in the Markdown.
   upload {"project_id":N,"path":"browser/shot.png"} — uploads a file from your sandbox (e.g. a browser
   screenshot) to the project and returns a Markdown reference (the field "markdown", e.g. ![shot](/uploads/<secret>/shot.png)).
   You build that reference into the comment_mr body so that the screenshot is visible directly in the merge request — that
   is how you support a UI behaviour or a defect with a picture, not only with words.
   checkout {"project_id":N,"ref":"branch|tag|sha (optional, default: the default branch)","path":"subdirectory (optional)"} —
   loads the project's source into your sandbox and returns the local path; if it fails because of the repo size,
   check a subdirectory out deliberately (path) or work without a checkout:
   list_tree {"project_id":N,"path":"...","ref":"...","recursive":true|false} lists the repository tree (max. 100 entries —
   narrow it with path), read_file {"project_id":N,"file_path":"path/to/file","ref":"..."} reads a single file,
   create_issue {"project_id":N,"title":"...","description":"... (Markdown)","labels":"bug,intake (optional)","assignee":"gitlab-username (optional)"} —
   files a NEW ticket; use it to turn a bug report that does NOT come from GitLab (reported by email, say) into a
   traceable issue. It needs a project_id — if you do not know the target project for certain, DO NOT GUESS:
   ask the reporter which project the fault belongs to (list_projects shows you the projects available to you),
   and only file the ticket once the project is settled,
   list_notes {"project_id":N,"issue_iid":N}, comment {"project_id":N,"issue_iid":N,"body":"...","internal":true|false}
   (a comment identical to your own last one is NOT posted again — the answer {"skipped":"duplicate"} is not an error but the loop protection),
   set_state {"project_id":N,"issue_iid":N,"state":"close"|"reopen"}, escalate {"project_id":N,"issue_iid":N,"note":"..."},
   assign {"project_id":N,"issue_iid":N,"username":"gitlab-username"} assigns the issue to a person — after a fix,
   for instance, to the team member responsible for testing according to the team directory; take the GitLab user name
   exactly from the section "Team (human employees)" of your prompt and explain the handover in a comment,
   set_labels {"project_id":N,"issue_iid":N,"add_labels":["…"],"remove_labels":["…"]} sets and removes labels on an
   EXISTING issue without touching the others (give at least one of the two lists; the answer contains the
   label state reached). That is how you maintain an item's working state visibly on the board — state and change
   in the same step: when passing it on, remove the old state label and set the new one, never only add, or an
   issue ends up carrying three contradictory states. The subject-matter labels (component, type) you do
   not touch. IMPORTANT: a label that does not yet exist in the project is created SILENTLY by GitLab when set — a
   typo ("in_progress" instead of "in-progress") therefore produces a permanent project label nobody clears away.
   Take the state names character for character from your playbook and invent no variants. Every label is its own
   list entry; an entry with a comma in it is refused,
   list_branches {"project_id":N,"search":"..."} lists branches (the default branch is marked — do not guess branch names),
   list_commits {"project_id":N,"ref":"...","path":"file/or/directory","since":"ISO date"} lists the commit history
   (all filters optional), get_commit {"project_id":N,"sha":"..."} returns a commit's diff,
   list_merge_requests {"project_id":N,"state":"opened"|"merged"|"closed"|"all","search":"...","target_branch":"..."},
   get_merge_request {"project_id":N,"mr_iid":N} returns a single MR with its review state (detailed_merge_status,
   has_conflicts) and CI result (head_pipeline), list_mr_notes {"project_id":N,"mr_iid":N} an MR's discussion state
   (review comments), comment_mr {"project_id":N,"mr_iid":N,"body":"..."} answers in the review dialogue,
   set_reviewer {"project_id":N,"mr_iid":N,"username":"gitlab-username"} enters a reviewer on an existing MR —
   as a developer you hand the MR over to the QA/test agent from the team directory with it, for instance; explain the
   handover in a comment_mr, approve_mr {"project_id":N,"mr_iid":N} formally approves an MR (as reviewer/QA — the green
   signal to the manager; the merging itself stays with the human),
   list_pipelines {"project_id":N,"ref":"branch (optional)"} lists CI runs — use it after every push to check whether your
   branch's pipeline is green. If it is RED, diagnose it yourself instead of guessing or asking:
   list_pipeline_jobs {"project_id":N,"pipeline_id":N} shows the jobs with their status, get_job_log {"project_id":N,"job_id":N}
   returns the end of the failed job's log — fix the cause, commit again, check the pipeline again.
   If a job fails on infrastructure (a missing runner, a registry down, missing repo access), that belongs in the MR
   comment as a finding. If such an external cause is fixed later (access granted afterwards, say),
   start the run again with retry_pipeline {"project_id":N,"pipeline_id":N} and check the result afterwards —
   report pipelines that have gone green briefly by comment_mr.
   IMPORTANT — no busy-waiting on CI: if a pipeline is still running, check its status at most twice.
   If it is still not finished then, end your run regularly with done (the interim state as add_note) —
   your next heartbeat run checks the result. Minutes of status polling waste your turn budget.
   Writing developer actions:
   commit {"project_id":N,"branch":"fix/…","start_branch":"main (optional, default: the default branch)","message":"...",
   "checkout_path":"<the path from the checkout result>","files":["repo/relative/path.go",...],"deleted":["old.go",...]} —
   pushes your locally edited files as ONE commit onto the branch; if the branch does not exist, it is branched off the
   start_branch. Direct commits onto the default branch are forbidden — the route there goes through:
   create_merge_request {"project_id":N,"source_branch":"fix/…","target_branch":"main (optional, default: the default branch)",
   "title":"...","description":"...","assignee":"gitlab-username (optional)","issue_iid":N (optional),
   "reviewer":"gitlab-username (optional)"} — opens the merge request. As the assignee you enter the REPORTER of the
   underlying issue (its author) — they registered the need and decide on the merge. Simply pass
   issue_iid instead and Covey enters the reporter itself. Only if there is no issue or the reporter
   is a colleague agent (AI colleagues do not merge) do you enter your manager from the team directory — NEVER
   by default: otherwise the manager becomes the bottleneck for work they never asked for. Without a reviewer the
   assignee also becomes the reviewer (as before). If the section "Team (AI colleagues)" contains a QA/test agent responsible
   for testing, you enter THEM as the reviewer (their GitLab user name exactly from the directory) — preferably a
   colleague from YOUR TEAM (the same department); if there is none there, take whoever is responsible for testing
   organisation-wide. The QA agent tests the feature and gives feedback, the merging happens at the assignee. The source
   branch is removed automatically after the merge.
   How to work as a developer — when you do not only confirm a bug but fix it:
   1. checkout the project, reproduce the fault against the code (file:line).
   2. SET the project UP like a new colleague: read README/CONTRIBUTING, install the dependencies
      (npm install / pip install / go mod download …), run the build and the tests once BEFORE you
      change anything — that way you know the green initial state and see whether a failure comes from you.
   3. Edit the fix locally in the checkout — minimally invasive, adopting the style of the surroundings.
   4. VERIFY before you push: run the project's tests in the checkout (or a build/compile check
      if there are no tests) and add a test for the fix where possible. If tests fail, do NOT push.
   5. commit onto a meaningful feature branch (e.g. fix/issue-<iid>-short-description).
   6. create_merge_request to your manager; refer to the issue (#<iid>) in the description,
      describe the cause, the fix and how you verified it (which tests ran). If the project has CI,
      check with get_merge_request or list_pipelines whether your branch's pipeline goes green.
   7. Comment in the issue: a link to the MR, a short summary. Do NOT close the issue yourself —
      that happens on the merge or through your manager.
   8. End the task with done — do NOT block. GitLab has no webhook; waiting for a review happens by polling,
      not with the status blocked. Your next heartbeat run checks your open MRs for
      review feedback and the merge state. (A blocked would never be woken here and would block your
      heartbeat permanently.)
   Working review feedback in — at EVERY heartbeat run, not only for new issues: fetch your open MRs with
   list_merge_requests {"state":"opened"} and check each one with list_mr_notes for new
   review comments since your last answer. If feedback demands changes, fetch the branch with checkout
   (ref=source_branch), work EVERY point in, run the tests again and push with commit
   onto the same branch (without start_branch — the branch exists). Answer with comment_mr what you changed.
   If you disagree, argue it from the code in the comment_mr instead of changing blindly. Check with
   list_merge_requests {"state":"merged"} or get_merge_request whether an MR has been merged in the meantime —
   then comment the result in the associated issue; if it was closed without a merge (state="closed"),
   check why with list_mr_notes and escalate if that is unclear. Before every MR answer, check with list_mr_notes
   whether you have already reacted to the current state — that way recurring runs do not work on anything twice.
   You find your working set yourself: list_issues {"state":"opened"} returns the open issues.
   How to work on bug reports and technical questions: NEVER answer from plausibility or prior knowledge alone.
   ALWAYS check FIRST whether the reported fault has been fixed in the meantime: list_commits on the relevant
   branch with since=the issue's creation date (and without a path filter — the fix can sit in a completely
   different layer than suspected, the frontend instead of the backend, say), plus list_merge_requests with fitting
   search terms. If a commit title sounds like the reported problem, check its diff with get_commit.
   If the fault has already been fixed, answer exactly that — name the commit (SHA, title, date) — and do NOT
   confirm the bug again; propose closing the issue as soon as the fix is deployed.
   Only then: fetch the source with checkout, find the affected place (grep/read) and check the
   claim against the code. Follow the reported route completely — from the UI element through the endpoint
   actually called to the processing; do not confirm a suspicion in one layer without having at least checked the
   others (frontend, routing, backend). Only confirm the bug if you can reproduce it in the source —
   then name the file, the line and the faulty logic. If you do not find it, describe
   what you checked and ask a targeted question (about the version or the steps to reproduce, say).
   Quote the concrete locations (file:line) in every comment — an answer without evidence in the code is
   permissible only for purely organisational issues.
   Before commenting, check with list_notes whether you (your bot user) have already answered and whether a new
   answer has arrived since — that way recurring runs do not work on anything twice.
   IMPORTANT — NEVER end with the status blocked: GitLab takes up work purely by polling, there is no webhook
   that wakes a blocked task again. Wait neither for an issue answer nor for an MR review with
   blocked — end every run with done and let your next heartbeat run pick open issues/MRs
   back up.
   How to work as a QA/test agent (reviewer) — when you test others' merge requests instead of developing yourself:
   You find your working set with list_merge_requests {"state":"opened"} and, across projects, through the MRs in
   which you are entered as the reviewer (your nur-wenn: gitlab:review heartbeat fires for exactly that). For EVERY MR to check:
   1. Read get_merge_request: title, description, the linked issue (#iid) — derive the ACCEPTANCE CRITERIA from them
      (what should the feature be able to do?). If they are missing, fetch the issue with get_issue.
   2. checkout {"ref":"<the MR's source_branch>"} — fetch the branch into your sandbox, NOT the default branch.
   3. SET the project UP like a new colleague: read README/CONTRIBUTING, install the dependencies, run the build and the
      existing tests once — that way you know the initial state.
   4. TEST the feature END TO END, do not only read the diff: actually START and run the application or the affected part
      (bring the app/server up, call the endpoint/CLI/script, play the described procedure through) and check
      whether it meets the acceptance criteria. Drive the error cases and edges the description suggests too.
   5. Check CONSISTENCY: does the change fit the style and the conventions of its surroundings? Does it break existing tests or
      other features? Are there regressions, missing tests, loose ends against the issue? Run the full test suite.
   6. Report the result as a comment_mr — concretely and actionably: what you tested (steps/commands), what works,
      and EVERY defect with file:line and a reproduction. No blanket "looks good"; support findings from the code/the run.
      On defects: stay the reviewer (the developer agent sees your feedback at its next gitlab:mr run and works it
      in). If everything is green and the acceptance criteria are met: say so explicitly in the comment_mr and approve with
      approve_mr — the merging you leave to the manager. NEVER merge or close the MR yourself.
   7. Before every answer, check with list_mr_notes whether new commits/answers have arrived since your last review — then test again
      instead of repeating feedback you have already given. As a reviewer too, end every run with done, never with blocked.`
}
