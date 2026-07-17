package gitlab

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVerifyToken(t *testing.T) {
	if !VerifyToken("webhook-geheim", "webhook-geheim") {
		t.Fatal("korrektes Token muss akzeptiert werden")
	}
	if VerifyToken("webhook-geheim", "falsch") {
		t.Fatal("falsches Token muss abgelehnt werden")
	}
	if VerifyToken("webhook-geheim", "") {
		t.Fatal("fehlender Header muss abgelehnt werden")
	}
	if !VerifyToken("", "") {
		t.Fatal("leeres Secret deaktiviert die Prüfung (Dev-Modus)")
	}
}

func TestParseWebhookIssue(t *testing.T) {
	body := []byte(`{"object_kind":"issue","user":{"username":"kunde"},
		"project":{"id":15,"path_with_namespace":"gruppe/support"},
		"object_attributes":{"iid":23,"title":"Login kaputt","state":"opened","action":"open","description":"Es geht nicht"}}`)
	p, err := ParseWebhook(body)
	if err != nil {
		t.Fatal(err)
	}
	if p.Project.ID != 15 || p.IssueIID() != 23 || p.IssueTitle() != "Login kaputt" {
		t.Fatalf("payload falsch geparst: %+v", p)
	}
	if !p.IsWakeEvent() {
		t.Fatal("neu eröffnetes Issue muss wecken")
	}
	if CorrelationKey(p.Project.ID, p.IssueIID()) != "gitlab:issue:15:23" {
		t.Fatalf("korrelations-key: %s", CorrelationKey(p.Project.ID, p.IssueIID()))
	}
}

func TestParseWebhookNote(t *testing.T) {
	body := []byte(`{"object_kind":"note","user":{"username":"kunde"},
		"project":{"id":15,"path_with_namespace":"gruppe/support"},
		"object_attributes":{"id":99,"note":"Geht immer noch nicht","noteable_type":"Issue"},
		"issue":{"iid":23,"title":"Login kaputt"}}`)
	p, err := ParseWebhook(body)
	if err != nil {
		t.Fatal(err)
	}
	if p.IssueIID() != 23 || p.ObjectAttributes.Note != "Geht immer noch nicht" {
		t.Fatalf("note-payload falsch geparst: %+v", p)
	}
	if !p.IsWakeEvent() {
		t.Fatal("fremder Issue-Kommentar muss wecken")
	}
	if p.DedupKey() != "gitlab:15:note:99" {
		t.Fatalf("dedup-key: %s", p.DedupKey())
	}
}

func TestParseWebhookRejectsMissingIssue(t *testing.T) {
	if _, err := ParseWebhook([]byte(`{"object_kind":"issue","project":{"id":15}}`)); err == nil {
		t.Fatal("payload ohne issue-iid muss abgelehnt werden")
	}
	if _, err := ParseWebhook([]byte(`{"object_kind":"issue","object_attributes":{"iid":1}}`)); err == nil {
		t.Fatal("payload ohne project.id muss abgelehnt werden")
	}
}

func TestNoWakeCases(t *testing.T) {
	p := WebhookPayload{ObjectKind: "issue"}
	p.ObjectAttributes.Action = "update"
	if p.IsWakeEvent() {
		t.Fatal("Issue-Update (Labels etc.) darf nicht wecken")
	}

	p = WebhookPayload{ObjectKind: "note"}
	p.ObjectAttributes.NoteableType = "MergeRequest"
	if p.IsWakeEvent() {
		t.Fatal("Kommentare auf Merge Requests dürfen nicht wecken")
	}

	t.Setenv("COVEY_GITLAB_AGENT_USERNAMES", "covey-bot")
	p = WebhookPayload{ObjectKind: "note"}
	p.ObjectAttributes.NoteableType = "Issue"
	p.User.Username = "Covey-Bot"
	if p.IsWakeEvent() {
		t.Fatal("Agent-Kommentar darf keinen Wake auslösen (Echo-Schleife)")
	}
}

func TestIntakeScope(t *testing.T) {
	p := WebhookPayload{}
	p.Project.ID = 15
	p.Project.PathWithNamespace = "Gruppe/Support"
	if !p.InIntakeScope() {
		t.Fatal("ohne Allowlist sind alle Projekte im Scope")
	}
	t.Setenv("COVEY_GITLAB_INTAKE_PROJECTS", "gruppe/support")
	if !p.InIntakeScope() {
		t.Fatal("Projektpfad-Vergleich muss case-insensitiv sein")
	}
	t.Setenv("COVEY_GITLAB_INTAKE_PROJECTS", "15")
	if !p.InIntakeScope() {
		t.Fatal("numerische Projekt-id muss matchen")
	}
	t.Setenv("COVEY_GITLAB_INTAKE_PROJECTS", "anderes/projekt")
	if p.InIntakeScope() {
		t.Fatal("Projekt außerhalb der Allowlist darf nicht im Scope sein")
	}
}

func TestClientActions(t *testing.T) {
	var gotAuth, gotPath, gotMethod string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("PRIVATE-TOKEN")
		gotPath = r.URL.Path
		gotMethod = r.Method
		gotBody = nil
		if r.Body != nil {
			json.NewDecoder(r.Body).Decode(&gotBody)
		}
		switch {
		case r.URL.Path == "/api/v4/projects/15/issues/23" && r.Method == http.MethodGet:
			json.NewEncoder(w).Encode(Issue{IID: 23, ProjectID: 15, Title: "Login kaputt", State: "opened"})
		case r.URL.Path == "/api/v4/projects/15/issues/23/notes" && r.Method == http.MethodGet:
			json.NewEncoder(w).Encode([]Note{{ID: 1, Body: "Hilfe"}})
		case r.URL.Path == "/api/v4/projects/15/issues/23/notes" && r.Method == http.MethodPost:
			json.NewEncoder(w).Encode(Note{ID: 2})
		default:
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "test-token")
	ctx := context.Background()

	issue, err := c.GetIssue(ctx, 15, 23)
	if err != nil || issue.Title != "Login kaputt" {
		t.Fatalf("GetIssue: %v %+v", err, issue)
	}
	if gotAuth != "test-token" {
		t.Fatalf("PRIVATE-TOKEN-Header falsch: %q", gotAuth)
	}

	notes, err := c.ListNotes(ctx, 15, 23)
	if err != nil || len(notes) != 1 {
		t.Fatalf("ListNotes: %v %+v", err, notes)
	}

	if _, err := c.Comment(ctx, 15, 23, "Bitte Screenshot schicken", false); err != nil {
		t.Fatalf("Comment: %v", err)
	}
	if gotBody["internal"] != false || gotBody["body"] != "Bitte Screenshot schicken" {
		t.Fatalf("Comment-Body falsch: %+v", gotBody)
	}

	if err := c.SetState(ctx, 15, 23, "close"); err != nil {
		t.Fatalf("SetState: %v", err)
	}
	if gotMethod != http.MethodPut || gotPath != "/api/v4/projects/15/issues/23" {
		t.Fatalf("SetState muss PUT /projects/15/issues/23 sein: %s %s", gotMethod, gotPath)
	}
	if gotBody["state_event"] != "close" {
		t.Fatalf("SetState-Body falsch: %+v", gotBody)
	}
	if err := c.SetState(ctx, 15, 23, "opened"); err == nil {
		t.Fatal("ungültiger state_event muss abgelehnt werden")
	}

	if err := c.Escalate(ctx, 15, 23, "Bitte übernehmen"); err != nil {
		t.Fatalf("Escalate: %v", err)
	}
	if gotMethod != http.MethodPut || gotBody["assignee_ids"] == nil {
		t.Fatalf("Escalate muss die Zuweisung entfernen: %s %+v", gotMethod, gotBody)
	}
}

func TestClientErrorSurface(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"401 Unauthorized"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "falsch")
	if _, err := c.GetIssue(context.Background(), 15, 23); err == nil {
		t.Fatal("HTTP-Fehler muss als error auftauchen")
	}
}

func TestActionSubject(t *testing.T) {
	sys := System{}
	if got := sys.ActionSubject("comment", []byte(`{"internal":false}`)); got != "gitlab:comment_external" {
		t.Fatalf("externer Kommentar: %s", got)
	}
	if got := sys.ActionSubject("comment", []byte(`{"internal":true}`)); got != "gitlab:comment_internal" {
		t.Fatalf("interner Kommentar: %s", got)
	}
	if got := sys.ActionSubject("comment", []byte(`{}`)); got != "gitlab:comment_internal" {
		t.Fatalf("Default muss intern (sicher) sein: %s", got)
	}
	if got := sys.ActionSubject("set_state", nil); got != "gitlab:set_state" {
		t.Fatalf("set_state: %s", got)
	}
}
