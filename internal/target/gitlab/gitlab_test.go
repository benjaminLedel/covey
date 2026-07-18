package gitlab

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"covey/internal/target"
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

func TestClientDiscovery(t *testing.T) {
	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		switch r.URL.Path {
		case "/api/v4/projects":
			json.NewEncoder(w).Encode([]Project{{ID: 15, PathWithNamespace: "gruppe/support"}})
		default:
			json.NewEncoder(w).Encode([]Issue{{IID: 23, ProjectID: 15, Title: "Login kaputt", State: "opened"}})
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "test-token")
	ctx := context.Background()

	ps, err := c.ListProjects(ctx)
	if err != nil || len(ps) != 1 || ps[0].ID != 15 {
		t.Fatalf("ListProjects: %v %+v", err, ps)
	}
	if !strings.Contains(gotQuery, "membership=true") {
		t.Fatalf("ListProjects muss auf Mitgliedschaft filtern: %s", gotQuery)
	}

	issues, err := c.ListIssues(ctx, 15, "", "", "", false)
	if err != nil || len(issues) != 1 || issues[0].IID != 23 {
		t.Fatalf("ListIssues (Projekt): %v %+v", err, issues)
	}
	if gotPath != "/api/v4/projects/15/issues" || !strings.Contains(gotQuery, "state=opened") {
		t.Fatalf("ListIssues muss projektbezogen und mit Default state=opened laufen: %s?%s", gotPath, gotQuery)
	}

	if _, err := c.ListIssues(ctx, 0, "all", "bug,support", "login", false); err != nil {
		t.Fatalf("ListIssues (global): %v", err)
	}
	if gotPath != "/api/v4/issues" || !strings.Contains(gotQuery, "scope=all") {
		t.Fatalf("ohne project_id muss das globale /issues mit scope=all laufen: %s?%s", gotPath, gotQuery)
	}
	if strings.Contains(gotQuery, "state=") {
		t.Fatalf("state=all darf keinen state-Parameter senden: %s", gotQuery)
	}
	if !strings.Contains(gotQuery, "labels=bug%2Csupport") || !strings.Contains(gotQuery, "search=login") {
		t.Fatalf("labels/search müssen durchgereicht werden: %s", gotQuery)
	}

	if _, err := c.ListIssues(ctx, 0, "", "", "", true); err != nil {
		t.Fatalf("ListIssues (assigned, global): %v", err)
	}
	if !strings.Contains(gotQuery, "scope=assigned_to_me") || strings.Contains(gotQuery, "scope=all") {
		t.Fatalf("assigned=true muss scope=assigned_to_me statt scope=all senden: %s", gotQuery)
	}
	if _, err := c.ListIssues(ctx, 15, "", "", "", true); err != nil {
		t.Fatalf("ListIssues (assigned, Projekt): %v", err)
	}
	if gotPath != "/api/v4/projects/15/issues" || !strings.Contains(gotQuery, "scope=assigned_to_me") {
		t.Fatalf("assigned=true muss auch projektbezogen scope=assigned_to_me senden: %s?%s", gotPath, gotQuery)
	}
}

func TestListActionsRespectIntakeScope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v4/projects":
			json.NewEncoder(w).Encode([]Project{
				{ID: 15, PathWithNamespace: "gruppe/support"},
				{ID: 99, PathWithNamespace: "gruppe/geheim"},
			})
		default:
			issueIn := Issue{IID: 23, ProjectID: 15}
			issueIn.References.Full = "gruppe/support#23"
			issueOut := Issue{IID: 7, ProjectID: 99}
			issueOut.References.Full = "gruppe/geheim#7"
			json.NewEncoder(w).Encode([]Issue{issueIn, issueOut})
		}
	}))
	defer srv.Close()

	t.Setenv("COVEY_GITLAB_INTAKE_PROJECTS", "gruppe/support")
	sys := System{}
	cred := target.Credential{BaseURL: srv.URL, Token: "test-token"}
	ctx := context.Background()

	res, err := sys.Execute(ctx, "list_projects", []byte(`{}`), cred)
	if err != nil {
		t.Fatalf("list_projects: %v", err)
	}
	if ps := res.([]Project); len(ps) != 1 || ps[0].ID != 15 {
		t.Fatalf("list_projects muss die Allowlist anwenden: %+v", ps)
	}

	res, err = sys.Execute(ctx, "list_issues", []byte(`{}`), cred)
	if err != nil {
		t.Fatalf("list_issues: %v", err)
	}
	if issues := res.([]Issue); len(issues) != 1 || issues[0].IID != 23 {
		t.Fatalf("list_issues muss die Allowlist anwenden: %+v", issues)
	}
}

// tarGz baut ein GitLab-artiges Repository-Archiv aus name→inhalt-Paaren.
func tarGz(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	// pax_global_header wie im echten GitLab-Archiv — muss ignoriert werden.
	if err := tw.WriteHeader(&tar.Header{Name: "pax_global_header", Typeflag: tar.TypeXGlobalHeader}); err != nil {
		t.Fatal(err)
	}
	for name, content := range entries {
		hdr := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg}
		if strings.HasSuffix(name, "/") {
			hdr = &tar.Header{Name: name, Mode: 0o755, Typeflag: tar.TypeDir}
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if hdr.Typeflag == tar.TypeReg {
			if _, err := tw.Write([]byte(content)); err != nil {
				t.Fatal(err)
			}
		}
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestCheckout(t *testing.T) {
	archive := tarGz(t, map[string]string{
		"support-main-abc123/":            "",
		"support-main-abc123/README.md":   "# Support",
		"support-main-abc123/pkg/auth.go": "package auth // hier wohnt der Bug",
	})
	var gotPath, gotAuth, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth, gotQuery = r.URL.Path, r.Header.Get("PRIVATE-TOKEN"), r.URL.RawQuery
		w.Write(archive)
	}))
	defer srv.Close()

	sys := System{}
	cred := target.Credential{BaseURL: srv.URL, Token: "test-token"}
	workdir := t.TempDir()
	ctx := target.WithWorkdir(context.Background(), workdir)

	res, err := sys.Execute(ctx, "checkout", []byte(`{"project_id":15,"ref":"main"}`), cred)
	if err != nil {
		t.Fatalf("checkout: %v", err)
	}
	if gotPath != "/api/v4/projects/15/repository/archive.tar.gz" || gotAuth != "test-token" {
		t.Fatalf("falscher API-Aufruf: %s (auth %q)", gotPath, gotAuth)
	}
	if !strings.Contains(gotQuery, "sha=main") {
		t.Fatalf("ref muss als sha-Parameter laufen: %s", gotQuery)
	}
	co := res.(CheckoutResult)
	if co.Files != 2 {
		t.Fatalf("erwartet 2 Dateien, war %d", co.Files)
	}
	data, err := os.ReadFile(filepath.Join(co.Path, "pkg", "auth.go"))
	if err != nil || !strings.Contains(string(data), "Bug") {
		t.Fatalf("entpackte Datei fehlt/falsch: %v %q", err, data)
	}
	if !strings.HasPrefix(co.Path, filepath.Join(workdir, "repos")) {
		t.Fatalf("checkout muss unter <workdir>/repos landen: %s", co.Path)
	}

	// Zweiter Checkout desselben Stands ersetzt den alten (kein Fehler).
	if _, err := sys.Execute(ctx, "checkout", []byte(`{"project_id":15}`), cred); err != nil {
		t.Fatalf("wiederholter checkout: %v", err)
	}

	// Ohne Sandbox-Workdir (z. B. Control-Plane-Kontext) klare Ablehnung.
	if _, err := sys.Execute(context.Background(), "checkout", []byte(`{"project_id":15}`), cred); err == nil {
		t.Fatal("checkout ohne workdir muss fehlschlagen")
	}
	// Ohne project_id klare Ablehnung.
	if _, err := sys.Execute(ctx, "checkout", []byte(`{}`), cred); err == nil {
		t.Fatal("checkout ohne project_id muss fehlschlagen")
	}
}

func TestCheckoutSubPathAndLimit(t *testing.T) {
	archive := tarGz(t, map[string]string{
		"support-main-abc123/":                   "",
		"support-main-abc123/web/upload/form.js": "const maxSize = 5", // Teil-Checkout-Inhalt
	})
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Write(archive)
	}))
	defer srv.Close()

	sys := System{}
	cred := target.Credential{BaseURL: srv.URL, Token: "test-token"}
	ctx := target.WithWorkdir(context.Background(), t.TempDir())

	if _, err := sys.Execute(ctx, "checkout", []byte(`{"project_id":15,"path":"web/upload"}`), cred); err != nil {
		t.Fatalf("teil-checkout: %v", err)
	}
	if !strings.Contains(gotQuery, "path=web%2Fupload") {
		t.Fatalf("path muss als Archiv-Parameter laufen: %s", gotQuery)
	}

	// Limit per Env drücken: 2-MB-Datei gegen 1-MB-Limit → klarer Fehler
	// mit Hinweis auf die Auswege (path / list_tree / read_file).
	big := tarGz(t, map[string]string{
		"support-main-abc123/":         "",
		"support-main-abc123/blob.bin": strings.Repeat("x", 2<<20),
	})
	archive = big
	t.Setenv("COVEY_GITLAB_CHECKOUT_MAX_MB", "1")
	_, err := sys.Execute(ctx, "checkout", []byte(`{"project_id":15}`), cred)
	if err == nil || !strings.Contains(err.Error(), "list_tree") {
		t.Fatalf("größen-limit muss mit Auswegen fehlschlagen: %v", err)
	}
}

func TestTreeAndReadFile(t *testing.T) {
	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.EscapedPath(), r.URL.RawQuery
		if strings.Contains(r.URL.Path, "/repository/tree") {
			json.NewEncoder(w).Encode([]TreeEntry{{Name: "upload", Type: "tree", Path: "web/upload"}})
			return
		}
		w.Write([]byte("const maxSize = 5 * 1024 * 1024"))
	}))
	defer srv.Close()

	sys := System{}
	cred := target.Credential{BaseURL: srv.URL, Token: "test-token"}
	ctx := context.Background()

	res, err := sys.Execute(ctx, "list_tree", []byte(`{"project_id":15,"path":"web","recursive":true}`), cred)
	if err != nil {
		t.Fatalf("list_tree: %v", err)
	}
	if entries := res.([]TreeEntry); len(entries) != 1 || entries[0].Path != "web/upload" {
		t.Fatalf("tree falsch: %+v", entries)
	}
	if !strings.Contains(gotQuery, "recursive=true") || !strings.Contains(gotQuery, "path=web") {
		t.Fatalf("tree-parameter fehlen: %s", gotQuery)
	}

	res, err = sys.Execute(ctx, "read_file", []byte(`{"project_id":15,"file_path":"web/upload/form.js","ref":"main"}`), cred)
	if err != nil {
		t.Fatalf("read_file: %v", err)
	}
	out := res.(map[string]any)
	if !strings.Contains(out["content"].(string), "maxSize") || out["truncated"].(bool) {
		t.Fatalf("read_file-inhalt falsch: %+v", out)
	}
	// GitLab verlangt den komplett URL-kodierten Dateipfad (inkl. "/").
	if !strings.Contains(gotPath, "/repository/files/web%2Fupload%2Fform.js/raw") {
		t.Fatalf("file_path muss URL-kodiert sein: %s", gotPath)
	}
	if !strings.Contains(gotQuery, "ref=main") {
		t.Fatalf("ref muss durchgereicht werden: %s", gotQuery)
	}

	if _, err := sys.Execute(ctx, "read_file", []byte(`{"project_id":15}`), cred); err == nil {
		t.Fatal("read_file ohne file_path muss fehlschlagen")
	}
}

func TestExtractTarGzRejectsTraversal(t *testing.T) {
	archive := tarGz(t, map[string]string{
		"repo-main/":          "",
		"../../etc/evil.conf": "böse",
	})
	if _, _, err := extractTarGz(bytes.NewReader(archive), t.TempDir()); err == nil {
		t.Fatal("pfad-traversal im archiv muss abgelehnt werden")
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
