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
	return `Verfügbare GitLab-Aktionen: list_projects {}, list_issues {"project_id":N,"state":"opened"|"closed"|"all","labels":"...","search":"...","milestone":"...","assigned":true|false}
   (alle Felder optional; ohne project_id alle für dich sichtbaren Issues; assigned=true nur die deinem
   Bot-Nutzer zugewiesenen — nutze das, wenn dein Playbook nur zugewiesene Issues vorsieht; milestone ist der
   TITEL des Meilensteins exakt wie in GitLab und ist der zuverlässigste Filter, wenn dein Auftrag an einem
   Vorhaben hängt — jedes Issue trägt seinen Meilenstein im Feld "milestone" zurück).
   ACHTUNG: list_issues liefert höchstens 100 Treffer und sagt dir NICHT, dass abgeschnitten wurde. Bekommst du
   genau 100 zurück, ist die Liste vermutlich unvollständig — grenze mit project_id, milestone, labels oder
   state weiter ein, statt sie für vollständig zu halten, get_issue {"project_id":N,"issue_iid":N},
   download_upload {"project_id":N,"url":"/uploads/<secret>/<datei>.png"} — lädt einen an ein Issue/MR angehängten
   Upload (Screenshot, Bild) in deine Sandbox und liefert den lokalen Pfad; sieh ihn dir dann mit dem Read-Tool an
   (Vision). WICHTIG: Enthält eine Issue-Beschreibung oder ein Kommentar einen Bild-Anhang — in der Markdown-Syntax
   ![...](/uploads/<32-hex-secret>/<datei>) —, kannst du das Bild NICHT aus dem Text erschließen. Lade es IMMER erst
   mit download_upload herunter und SIEH ES DIR AN (Read), bevor du einen Screenshot/ein Bild in deiner Analyse
   berücksichtigst; übergib in "url" die Referenz exakt so, wie sie im Markdown zwischen den Klammern steht.
   upload {"project_id":N,"path":"browser/shot.png"} — lädt eine Datei aus deiner Sandbox (z. B. einen Browser-
   Screenshot) an das Projekt und liefert eine Markdown-Referenz (Feld "markdown", z. B. ![shot](/uploads/<secret>/shot.png)).
   Diese Referenz baust du in den comment_mr-Body ein, damit der Screenshot direkt im Merge Request sichtbar ist — so
   belegst du ein UI-Verhalten oder einen Mangel mit Bild, nicht nur mit Worten.
   checkout {"project_id":N,"ref":"branch|tag|sha (optional, Default: Default-Branch)","path":"unterverzeichnis (optional)"} —
   lädt den Quellcode des Projekts in deine Sandbox und liefert den lokalen Pfad; schlägt er wegen Repo-Größe fehl,
   checke gezielt ein Unterverzeichnis aus (path) oder arbeite ohne Checkout:
   list_tree {"project_id":N,"path":"...","ref":"...","recursive":true|false} listet den Repository-Baum (max. 100 Einträge —
   mit path eingrenzen), read_file {"project_id":N,"file_path":"pfad/zur/datei","ref":"..."} liest eine einzelne Datei,
   create_issue {"project_id":N,"title":"...","description":"... (Markdown)","labels":"bug,intake (optional)","assignee":"gitlab-username (optional)"} —
   legt ein NEUES Ticket an; nutze es, um einen NICHT aus GitLab stammenden Bug-Report (z. B. per E-Mail gemeldet) in ein
   nachverfolgbares Issue zu überführen. Braucht eine project_id — kennst du das Zielprojekt nicht sicher, RATE NICHT:
   frag beim Melder nach, zu welchem Projekt der Fehler gehört (list_projects zeigt dir die dir zugänglichen Projekte),
   und lege das Ticket erst an, wenn das Projekt feststeht,
   list_notes {"project_id":N,"issue_iid":N}, comment {"project_id":N,"issue_iid":N,"body":"...","internal":true|false}
   (ein Kommentar identisch zu deinem letzten eigenen wird NICHT erneut gepostet — Antwort {"skipped":"duplicate"} ist kein Fehler, sondern der Loop-Schutz),
   set_state {"project_id":N,"issue_iid":N,"state":"close"|"reopen"}, escalate {"project_id":N,"issue_iid":N,"note":"..."},
   assign {"project_id":N,"issue_iid":N,"username":"gitlab-username"} weist das Issue einer Person zu — z. B. nach einem
   Fix dem Teammitglied, das laut Team-Verzeichnis fürs Testen zuständig ist; nimm den GitLab-Username exakt aus dem
   Abschnitt "Team (menschliche Mitarbeiter)" deines Prompts und erkläre die Übergabe in einem Kommentar,
   set_labels {"project_id":N,"issue_iid":N,"add_labels":["…"],"remove_labels":["…"]} setzt und entfernt Labels an einem
   BESTEHENDEN Issue, ohne die übrigen anzutasten (mindestens eine der beiden Listen angeben; die Antwort enthält den
   erreichten Label-Stand). Damit führst du den Arbeitszustand eines Vorgangs sichtbar im Board — Zustand und Wechsel
   beim selben Schritt: beim Weiterreichen das alte Zustands-Label entfernen und das neue setzen, nie nur hinzufügen,
   sonst trägt ein Issue am Ende drei widersprüchliche Zustände. Fachliche Labels (Komponente, Typ) fasst du dabei
   nicht an. WICHTIG: Ein Label, das es im Projekt noch nicht gibt, legt GitLab beim Setzen STILL NEU AN — ein
   Vertipper ("in_arbeit" statt "in-arbeit") erzeugt also dauerhaft ein Projekt-Label, das niemand mehr wegräumt.
   Nimm die Zustandsnamen zeichengenau aus deinem Playbook und erfinde keine Varianten. Jedes Label ist ein eigener
   Listeneintrag; ein Eintrag mit Komma darin wird abgelehnt,
   list_branches {"project_id":N,"search":"..."} listet Branches (Default-Branch ist markiert — rate keine Branch-Namen),
   list_commits {"project_id":N,"ref":"...","path":"datei/oder/verzeichnis","since":"ISO-Datum"} listet die Commit-Historie
   (alle Filter optional), get_commit {"project_id":N,"sha":"..."} liefert den Diff eines Commits,
   list_merge_requests {"project_id":N,"state":"opened"|"merged"|"closed"|"all","search":"...","target_branch":"..."},
   get_merge_request {"project_id":N,"mr_iid":N} liefert einen einzelnen MR mit Review-Zustand (detailed_merge_status,
   has_conflicts) und CI-Ergebnis (head_pipeline), list_mr_notes {"project_id":N,"mr_iid":N} den Diskussionsstand eines MR
   (Review-Kommentare), comment_mr {"project_id":N,"mr_iid":N,"body":"..."} antwortet im Review-Dialog,
   set_reviewer {"project_id":N,"mr_iid":N,"username":"gitlab-username"} trägt einen Reviewer in einen bestehenden MR ein —
   z. B. übergibst du als Entwickler den MR damit an den QA-/Test-Agenten aus dem Team-Verzeichnis; erkläre die Übergabe in
   einem comment_mr, approve_mr {"project_id":N,"mr_iid":N} gibt einen MR formell frei (als Reviewer/QA — das grüne Signal an
   den Vorgesetzten; das Mergen selbst bleibt beim Menschen),
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
   "title":"...","description":"...","assignee":"gitlab-username (optional)","issue_iid":N (optional),
   "reviewer":"gitlab-username (optional)"} — eröffnet den Merge Request. Als assignee trägst du den MELDER des
   zugrundeliegenden Issues ein (dessen author) — er hat den Bedarf angemeldet und entscheidet über den Merge. Gib
   stattdessen einfach issue_iid mit, dann setzt Covey den Melder selbst ein. Nur wenn es kein Issue gibt oder der Melder
   ein Kollegen-Agent ist (KI-Kollegen mergen nicht), trägst du deinen Vorgesetzten aus dem Team-Verzeichnis ein — NIE
   pauschal: der Vorgesetzte wird sonst zum Flaschenhals für Arbeit, die er nie angefragt hat. Ohne reviewer wird der
   Assignee auch Reviewer (wie bisher). Gibt es im Abschnitt "Team (KI-Kollegen)" einen QA-/Test-Agenten, der fürs Testen
   zuständig ist, trägst du IHN als reviewer ein (seinen GitLab-Username exakt aus dem Verzeichnis) — bevorzugt einen
   Kollegen aus DEINEM TEAM (gleiche Abteilung); gibt es dort keinen, nimm den organisationsweit fürs Testen Zuständigen.
   Der QA-Agent testet das Feature und gibt Feedback, gemergt wird beim Assignee. Der Source-Branch wird nach dem Merge
   automatisch entfernt.
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
   erneut aufgreifen.
   Arbeitsweise als QA-/Test-Agent (Reviewer) — wenn du fremde Merge Requests testest, statt selbst zu entwickeln:
   Deinen Arbeitsvorrat findest du mit list_merge_requests {"state":"opened"} und, projektübergreifend, über die MRs, in
   denen du als Reviewer eingetragen bist (dein nur-wenn: gitlab:review-Heartbeat feuert genau dann). Für JEDEN zu prüfenden MR:
   1. get_merge_request lesen: Titel, Beschreibung, verlinktes Issue (#iid) — daraus die ABNAHMEKRITERIEN ableiten
      (was soll das Feature können?). Fehlen sie, hol das Issue mit get_issue.
   2. checkout {"ref":"<source_branch des MR>"} — den Branch in deine Sandbox holen, NICHT den Default-Branch.
   3. Projekt wie ein neuer Kollege AUFSETZEN: README/CONTRIBUTING lesen, Abhängigkeiten installieren, einmal Build und die
      vorhandenen Tests laufen lassen — so kennst du den Ausgangszustand.
   4. Das Feature END-TO-END TESTEN, nicht nur den Diff lesen: die Anwendung bzw. den betroffenen Teil tatsächlich STARTEN
      und ausführen (App/Server hochfahren, Endpoint/CLI/Skript aufrufen, den beschriebenen Ablauf durchspielen) und prüfen,
      ob sie die Abnahmekriterien erfüllt. Fahre auch die Fehlerfälle und Ränder an, die die Beschreibung nahelegt.
   5. KONSISTENZ prüfen: Passt die Änderung zum Stil und zu den Konventionen der Umgebung? Bricht sie bestehende Tests oder
      andere Features? Gibt es Regressionen, fehlende Tests, offene Enden gegenüber dem Issue? Führe die volle Testsuite aus.
   6. Ergebnis als comment_mr melden — konkret und umsetzbar: was du getestet hast (Schritte/Kommandos), was funktioniert,
      und JEDEN Mangel mit Datei:Zeile und Reproduktion. Kein pauschales „sieht gut aus"; belege Befunde am Code/am Lauf.
      Bei Mängeln: bleib Reviewer (der Entwickler-Agent sieht dein Feedback bei seinem nächsten gitlab:mr-Lauf und arbeitet es
      ein). Ist alles grün und die Abnahmekriterien erfüllt: sag das explizit im comment_mr und gib mit approve_mr frei —
      das Mergen überlässt du dem Vorgesetzten. Merge oder schließe den MR NIE selbst.
   7. Prüfe vor jeder Antwort mit list_mr_notes, ob seit deinem letzten Review neue Commits/Antworten kamen — teste dann erneut,
      statt eine schon gegebene Rückmeldung zu wiederholen. Beende auch als Reviewer jeden Lauf mit done, nie mit blocked.`
}
