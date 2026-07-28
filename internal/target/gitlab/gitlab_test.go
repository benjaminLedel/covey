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

// TestProjectInScope prüft den Intake-Filter (COVEY_GITLAB_INTAKE_PROJECTS),
// der ohne Webhook nur noch die Discovery-Aktionen (list_issues/list_projects)
// und den nur-wenn:-Vorabcheck (HasWork) begrenzt.
func TestProjectInScope(t *testing.T) {
	if !projectInScope(15, "Gruppe/Support") {
		t.Fatal("ohne Allowlist sind alle Projekte im Scope")
	}
	t.Setenv("COVEY_GITLAB_INTAKE_PROJECTS", "gruppe/support")
	if !projectInScope(15, "Gruppe/Support") {
		t.Fatal("Projektpfad-Vergleich muss case-insensitiv sein")
	}
	t.Setenv("COVEY_GITLAB_INTAKE_PROJECTS", "15")
	if !projectInScope(15, "gruppe/support") {
		t.Fatal("numerische Projekt-id muss matchen")
	}
	t.Setenv("COVEY_GITLAB_INTAKE_PROJECTS", "anderes/projekt")
	if projectInScope(15, "gruppe/support") {
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

func TestHasWork(t *testing.T) {
	var issues []Issue
	var myMRs []MergeRequest
	var mrNotes []Note
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v4/issues":
			if r.URL.Query().Get("state") != "opened" {
				t.Errorf("issues muss state=opened filtern: %s", r.URL.RawQuery)
			}
			json.NewEncoder(w).Encode(issues)
		case r.URL.Path == "/api/v4/merge_requests":
			if r.URL.Query().Get("scope") != "created_by_me" || r.URL.Query().Get("state") != "opened" {
				t.Errorf("mr-vorabcheck muss scope=created_by_me&state=opened sein: %s", r.URL.RawQuery)
			}
			json.NewEncoder(w).Encode(myMRs)
		case r.URL.Path == "/api/v4/user":
			json.NewEncoder(w).Encode(User{ID: 1, Username: "covey-bot"})
		case strings.HasSuffix(r.URL.Path, "/notes"):
			json.NewEncoder(w).Encode(mrNotes)
		default:
			t.Errorf("unerwarteter request: %s?%s", r.URL.Path, r.URL.RawQuery)
		}
	}))
	defer srv.Close()

	sys := System{}
	cred := target.Credential{BaseURL: srv.URL, Token: "test-token"}
	ctx := context.Background()

	// Weder offene Issues noch offene MRs → keine Arbeit.
	if has, err := sys.HasWork(ctx, cred); err != nil || has {
		t.Fatalf("nichts offen: has=%v err=%v", has, err)
	}

	// Offenes Issue im Scope weckt (und kurzschließt vor der MR-Prüfung).
	issueIn := Issue{IID: 23, ProjectID: 15}
	issueIn.References.Full = "gruppe/support#23"
	issueOut := Issue{IID: 7, ProjectID: 99}
	issueOut.References.Full = "gruppe/geheim#7"
	issues = []Issue{issueOut, issueIn}
	if has, err := sys.HasWork(ctx, cred); err != nil || !has {
		t.Fatalf("offene issues: has=%v err=%v", has, err)
	}

	// Intake-Allowlist greift auch im Vorab-Check.
	t.Setenv("COVEY_GITLAB_INTAKE_PROJECTS", "gruppe/support")
	issues = []Issue{issueOut}
	if has, err := sys.HasWork(ctx, cred); err != nil || has {
		t.Fatalf("issue außerhalb der allowlist: has=%v err=%v", has, err)
	}

	// --- MR-Review-Zweig (keine offenen Issues im Scope) ---
	issues = nil
	mrIn := MergeRequest{IID: 9, ProjectID: 15}
	mrIn.References.Full = "gruppe/support!9"

	// Offener MR ohne Kommentare: Review steht noch aus → keine Arbeit.
	myMRs = []MergeRequest{mrIn}
	mrNotes = nil
	if has, err := sys.HasWork(ctx, cred); err != nil || has {
		t.Fatalf("frischer MR ohne feedback: has=%v err=%v", has, err)
	}

	// Letzter Kommentar vom Bot selbst → schon beantwortet, keine Arbeit.
	mrNotes = []Note{
		{ID: 1, Body: "Bitte Test ergänzen", Author: struct {
			Username string `json:"username"`
		}{Username: "leaddev"}},
		{ID: 2, Body: "Erledigt", Author: struct {
			Username string `json:"username"`
		}{Username: "covey-bot"}},
	}
	if has, err := sys.HasWork(ctx, cred); err != nil || has {
		t.Fatalf("MR bereits beantwortet: has=%v err=%v", has, err)
	}

	// Neues fremdes Feedback nach der Bot-Antwort → Arbeit. Ein abschließender
	// System-Kommentar (z. B. "changed the description") darf das nicht verdecken.
	mrNotes = append(mrNotes,
		Note{ID: 3, Body: "Noch ein Punkt", Author: struct {
			Username string `json:"username"`
		}{Username: "leaddev"}},
		Note{ID: 4, System: true, Body: "changed the description", Author: struct {
			Username string `json:"username"`
		}{Username: "leaddev"}},
	)
	if has, err := sys.HasWork(ctx, cred); err != nil || !has {
		t.Fatalf("unbeantwortetes review-feedback: has=%v err=%v", has, err)
	}

	// MR außerhalb der Allowlist → kein Notes-Abruf, keine Arbeit.
	mrOut := MergeRequest{IID: 4, ProjectID: 99}
	mrOut.References.Full = "gruppe/geheim!4"
	myMRs = []MergeRequest{mrOut}
	if has, err := sys.HasWork(ctx, cred); err != nil || has {
		t.Fatalf("MR außerhalb der allowlist: has=%v err=%v", has, err)
	}
}

// TestHasWorkKind prüft, dass der nur-wenn:-Unterscope die Arbeits-Arten
// getrennt gatet: gitlab:issues sieht nur Issues, gitlab:mr nur MR-Reviews,
// ein leerer/unbekannter Scope beides (wie HasWork).
func TestHasWorkKind(t *testing.T) {
	var issues []Issue
	var myMRs []MergeRequest
	var mrNotes []Note
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v4/issues":
			json.NewEncoder(w).Encode(issues)
		case r.URL.Path == "/api/v4/merge_requests":
			json.NewEncoder(w).Encode(myMRs)
		case r.URL.Path == "/api/v4/user":
			json.NewEncoder(w).Encode(User{ID: 1, Username: "covey-bot"})
		case strings.HasSuffix(r.URL.Path, "/notes"):
			json.NewEncoder(w).Encode(mrNotes)
		default:
			t.Errorf("unerwarteter request: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	sys := System{}
	cred := target.Credential{BaseURL: srv.URL, Token: "t"}
	ctx := context.Background()

	issueIn := Issue{IID: 1, ProjectID: 15}
	issueIn.References.Full = "gruppe/support#1"
	mrIn := MergeRequest{IID: 9, ProjectID: 15}
	mrIn.References.Full = "gruppe/support!9"
	foreign := []Note{{ID: 1, Body: "Bitte ändern", Author: struct {
		Username string `json:"username"`
	}{Username: "leaddev"}}}

	check := func(kind string, want bool) {
		t.Helper()
		has, err := sys.HasWorkKind(ctx, cred, kind)
		if err != nil {
			t.Fatalf("HasWorkKind(%q): %v", kind, err)
		}
		if has != want {
			t.Fatalf("HasWorkKind(%q) = %v, erwartet %v", kind, has, want)
		}
	}

	// Nur offenes Issue, keine MR-Arbeit.
	issues, myMRs, mrNotes = []Issue{issueIn}, nil, nil
	check("issues", true)
	check("mr", false)
	check("", true) // Fallback prüft beides

	// Nur unbeantwortetes MR-Feedback, kein offenes Issue.
	issues, myMRs, mrNotes = nil, []MergeRequest{mrIn}, foreign
	check("issues", false)
	check("mr", true)
	check("unbekannt", true) // unbekannter Scope → wie HasWork (beides)

	// Gar nichts offen.
	issues, myMRs, mrNotes = nil, nil, nil
	check("issues", false)
	check("mr", false)
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

// TestDownloadUpload deckt den Kern des Screenshot-Lesens ab: die im
// Issue-Markdown eingebettete Upload-Referenz ![...](/uploads/<secret>/<datei>)
// wird gebrokert (Token bleibt im Daemon) in die Sandbox geladen, sodass der
// Agent das Bild danach mit Read (Vision) ansehen kann.
func TestDownloadUpload(t *testing.T) {
	const secret = "0123456789abcdef0123456789abcdef"
	png := []byte("\x89PNG\r\n\x1a\nFAKE-SCREENSHOT")
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("PRIVATE-TOKEN")
		w.Header().Set("Content-Type", "image/png")
		w.Write(png)
	}))
	defer srv.Close()

	sys := System{}
	cred := target.Credential{BaseURL: srv.URL, Token: "test-token"}
	workdir := t.TempDir()
	ctx := target.WithWorkdir(context.Background(), workdir)

	// So, wie die Referenz im Markdown einer Issue-Beschreibung steht.
	params := []byte(`{"project_id":15,"url":"/uploads/` + secret + `/login-fehler.png"}`)
	res, err := sys.Execute(ctx, "download_upload", params, cred)
	if err != nil {
		t.Fatalf("download_upload: %v", err)
	}
	if want := "/api/v4/projects/15/uploads/" + secret + "/login-fehler.png"; gotPath != want {
		t.Fatalf("falscher API-Pfad: %s (erwartet %s)", gotPath, want)
	}
	if gotAuth != "test-token" {
		t.Fatalf("Token muss als PRIVATE-TOKEN laufen, war %q", gotAuth)
	}
	up := res.(DownloadUploadResult)
	if up.Filename != "login-fehler.png" || up.ContentType != "image/png" {
		t.Fatalf("unerwartetes Ergebnis: %+v", up)
	}
	if !strings.HasPrefix(up.Path, filepath.Join(workdir, "uploads")) {
		t.Fatalf("Upload muss unter <workdir>/uploads landen: %s", up.Path)
	}
	data, err := os.ReadFile(up.Path)
	if err != nil || !bytes.Equal(data, png) {
		t.Fatalf("heruntergeladenes Bild fehlt/falsch: %v", err)
	}

	// Auch die volle Web-URL als Referenz muss auf denselben Upload-Endpoint zeigen.
	full := srv.URL + "/gruppe/projekt/uploads/" + secret + "/login-fehler.png"
	if _, err := sys.Execute(ctx, "download_upload",
		[]byte(`{"project_id":15,"url":"`+full+`"}`), cred); err != nil {
		t.Fatalf("download_upload mit voller URL: %v", err)
	}
	if want := "/api/v4/projects/15/uploads/" + secret + "/login-fehler.png"; gotPath != want {
		t.Fatalf("volle URL muss auf den Upload-Endpoint gemappt werden: %s", gotPath)
	}

	// Ohne project_id oder url klare Ablehnung.
	if _, err := sys.Execute(ctx, "download_upload", []byte(`{"project_id":15}`), cred); err == nil {
		t.Fatal("download_upload ohne url muss fehlschlagen")
	}
	// Referenz ohne gültiges Upload-Muster wird abgelehnt.
	if _, err := sys.Execute(ctx, "download_upload",
		[]byte(`{"project_id":15,"url":"/uploads/zu-kurz/x.png"}`), cred); err == nil {
		t.Fatal("ungültige Upload-Referenz muss fehlschlagen")
	}
	// Ohne Sandbox-Workdir klare Ablehnung.
	if _, err := sys.Execute(context.Background(), "download_upload", params, cred); err == nil {
		t.Fatal("download_upload ohne workdir muss fehlschlagen")
	}
}

// TestCreateIssueAction deckt den Intake extern gemeldeter Bugs ab: der Agent
// überführt eine Meldung (z. B. per E-Mail) in ein GitLab-Ticket. Ohne title
// bzw. project_id muss die Aktion klar ablehnen — das trägt das „erst nachfragen,
// wenn das Projekt unklar ist"-Playbook (kein Ticket ins Blaue).
func TestCreateIssueAction(t *testing.T) {
	var gotPath, gotMethod string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		gotBody = nil
		if r.Body != nil {
			json.NewDecoder(r.Body).Decode(&gotBody)
		}
		switch {
		case r.URL.Path == "/api/v4/users" && r.Method == http.MethodGet:
			json.NewEncoder(w).Encode([]struct {
				ID       int    `json:"id"`
				Username string `json:"username"`
			}{{ID: 77, Username: "qa-bot"}})
		case r.URL.Path == "/api/v4/projects/15/issues" && r.Method == http.MethodPost:
			json.NewEncoder(w).Encode(Issue{IID: 42, ProjectID: 15, Title: "Login kaputt", State: "opened", WebURL: "https://gl/…/issues/42"})
		default:
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()

	sys := System{}
	cred := target.Credential{BaseURL: srv.URL, Token: "test-token"}
	ctx := context.Background()

	res, err := sys.Execute(ctx, "create_issue",
		[]byte(`{"project_id":15,"title":"Login kaputt","description":"Gemeldet per Mail von kunde@x.de","labels":"bug,intake","assignee":"qa-bot"}`), cred)
	if err != nil {
		t.Fatalf("create_issue: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/v4/projects/15/issues" {
		t.Fatalf("falscher API-Aufruf: %s %s", gotMethod, gotPath)
	}
	if gotBody["title"] != "Login kaputt" || gotBody["labels"] != "bug,intake" {
		t.Fatalf("Body falsch übertragen: %+v", gotBody)
	}
	if _, ok := gotBody["assignee_ids"]; !ok {
		t.Fatalf("assignee muss zu assignee_ids aufgelöst werden: %+v", gotBody)
	}
	iss := res.(Issue)
	if iss.IID != 42 {
		t.Fatalf("unerwartetes Issue: %+v", iss)
	}

	// Ohne title (Projekt bekannt, aber keine Meldung) — Ablehnung.
	if _, err := sys.Execute(ctx, "create_issue", []byte(`{"project_id":15}`), cred); err == nil {
		t.Fatal("create_issue ohne title muss fehlschlagen")
	}
	// Ohne project_id (Projekt unklar) — Ablehnung; der Agent muss stattdessen nachfragen.
	if _, err := sys.Execute(ctx, "create_issue", []byte(`{"title":"Irgendein Bug"}`), cred); err == nil {
		t.Fatal("create_issue ohne project_id muss fehlschlagen")
	}
}

// TestCheckoutPreservesCaches sichert den Speed-Fix ab: ein erneuter Checkout
// desselben Refs ersetzt den Quellcode, lässt aber Dependency-Caches
// (node_modules) stehen — sonst müsste jeder QA-Lauf neu installieren.
func TestCheckoutPreservesCaches(t *testing.T) {
	archive1 := tarGz(t, map[string]string{
		"support-main-aaa/":         "",
		"support-main-aaa/main.go":  "package main // v1",
		"support-main-aaa/stale.go": "wird beim nächsten Checkout entfernt",
	})
	archive2 := tarGz(t, map[string]string{
		"support-main-bbb/":        "", // andere SHA → früher anderes Verzeichnis
		"support-main-bbb/main.go": "package main // v2",
	})
	current := archive1
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write(current)
	}))
	defer srv.Close()

	sys := System{}
	cred := target.Credential{BaseURL: srv.URL, Token: "t"}
	ctx := target.WithWorkdir(context.Background(), t.TempDir())

	res, err := sys.Execute(ctx, "checkout", []byte(`{"project_id":15,"ref":"main"}`), cred)
	if err != nil {
		t.Fatalf("checkout 1: %v", err)
	}
	path := res.(CheckoutResult).Path
	// Der Agent installiert Dependencies + hinterlässt Build-Cache.
	if err := os.MkdirAll(filepath.Join(path, "node_modules", "dep"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "node_modules", "dep", "index.js"), []byte("cached"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Zweiter Checkout (neue SHA) desselben Refs.
	current = archive2
	res2, err := sys.Execute(ctx, "checkout", []byte(`{"project_id":15,"ref":"main"}`), cred)
	if err != nil {
		t.Fatalf("checkout 2: %v", err)
	}
	path2 := res2.(CheckoutResult).Path
	if path2 != path {
		t.Fatalf("stabiles Verzeichnis erwartet: %q != %q", path2, path)
	}
	// node_modules muss überlebt haben ...
	if b, err := os.ReadFile(filepath.Join(path2, "node_modules", "dep", "index.js")); err != nil || string(b) != "cached" {
		t.Fatalf("node_modules-Cache nicht erhalten: %v %q", err, b)
	}
	// ... Quellcode aktualisiert ...
	if b, err := os.ReadFile(filepath.Join(path2, "main.go")); err != nil || !strings.Contains(string(b), "v2") {
		t.Fatalf("Quellcode nicht aktualisiert: %v %q", err, b)
	}
	// ... veraltete Quelldatei entfernt.
	if _, err := os.Stat(filepath.Join(path2, "stale.go")); !os.IsNotExist(err) {
		t.Fatalf("veraltete Datei stale.go hätte entfernt sein müssen: %v", err)
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

func TestHistoryActions(t *testing.T) {
	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.EscapedPath(), r.URL.RawQuery
		switch {
		case strings.Contains(r.URL.Path, "/repository/commits/") && strings.HasSuffix(r.URL.Path, "/diff"):
			json.NewEncoder(w).Encode([]CommitDiff{{NewPath: "web/upload/form.js",
				Diff: "@@ -1 +1 @@\n-alt\n+" + strings.Repeat("x", maxDiffBytesPerFile)}})
		case strings.Contains(r.URL.Path, "/repository/commits"):
			json.NewEncoder(w).Encode([]Commit{{ID: "abc123", ShortID: "abc123",
				Title: "Upload-Button wiederherstellen", AuthorName: "alke"}})
		case strings.Contains(r.URL.Path, "/repository/branches"):
			json.NewEncoder(w).Encode([]Branch{{Name: "educa-x-bugfix", Default: true}})
		case strings.Contains(r.URL.Path, "/merge_requests"):
			json.NewEncoder(w).Encode([]MergeRequest{{IID: 7, Title: "Fix Upload", State: "merged"}})
		}
	}))
	defer srv.Close()

	sys := System{}
	cred := target.Credential{BaseURL: srv.URL, Token: "test-token"}
	ctx := context.Background()

	res, err := sys.Execute(ctx, "list_commits",
		[]byte(`{"project_id":15,"ref":"main","path":"web","since":"2026-07-15T00:00:00Z"}`), cred)
	if err != nil {
		t.Fatalf("list_commits: %v", err)
	}
	if cs := res.([]Commit); len(cs) != 1 || cs[0].Title != "Upload-Button wiederherstellen" {
		t.Fatalf("commits falsch: %+v", cs)
	}
	if !strings.Contains(gotQuery, "ref_name=main") || !strings.Contains(gotQuery, "path=web") ||
		!strings.Contains(gotQuery, "since=2026-07-15") {
		t.Fatalf("commit-filter fehlen: %s", gotQuery)
	}

	res, err = sys.Execute(ctx, "get_commit", []byte(`{"project_id":15,"sha":"abc123"}`), cred)
	if err != nil {
		t.Fatalf("get_commit: %v", err)
	}
	diffs := res.([]CommitDiff)
	if len(diffs) != 1 || !diffs[0].Truncated || len(diffs[0].Diff) != maxDiffBytesPerFile {
		t.Fatalf("diff muss auf maxDiffBytesPerFile gekürzt und markiert sein: len=%d truncated=%v",
			len(diffs[0].Diff), diffs[0].Truncated)
	}
	if !strings.Contains(gotPath, "/repository/commits/abc123/diff") {
		t.Fatalf("diff-pfad falsch: %s", gotPath)
	}

	res, err = sys.Execute(ctx, "list_merge_requests", []byte(`{"project_id":15,"state":"merged","search":"upload"}`), cred)
	if err != nil {
		t.Fatalf("list_merge_requests: %v", err)
	}
	if mrs := res.([]MergeRequest); len(mrs) != 1 || mrs[0].State != "merged" {
		t.Fatalf("mrs falsch: %+v", mrs)
	}
	if !strings.Contains(gotQuery, "state=merged") || !strings.Contains(gotQuery, "search=upload") {
		t.Fatalf("mr-filter fehlen: %s", gotQuery)
	}

	res, err = sys.Execute(ctx, "list_branches", []byte(`{"project_id":15,"search":"bugfix"}`), cred)
	if err != nil {
		t.Fatalf("list_branches: %v", err)
	}
	if bs := res.([]Branch); len(bs) != 1 || !bs[0].Default {
		t.Fatalf("branches falsch: %+v", bs)
	}
	if !strings.Contains(gotQuery, "search=bugfix") {
		t.Fatalf("branch-suche fehlt: %s", gotQuery)
	}

	for _, call := range [][2]string{
		{"list_commits", `{}`}, {"get_commit", `{"project_id":15}`},
		{"list_merge_requests", `{}`}, {"list_branches", `{}`},
	} {
		if _, err := sys.Execute(ctx, call[0], []byte(call[1]), cred); err == nil {
			t.Fatalf("%s ohne Pflichtfelder muss fehlschlagen", call[0])
		}
	}
}

func TestCommitAction(t *testing.T) {
	var commitBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v4/projects/15" && r.Method == http.MethodGet:
			json.NewEncoder(w).Encode(ProjectDetail{ID: 15, DefaultBranch: "main"})
		case strings.Contains(r.URL.Path, "/repository/branches"):
			json.NewEncoder(w).Encode([]Branch{}) // Feature-Branch existiert noch nicht
		case strings.Contains(r.URL.Path, "/repository/files/") && r.Method == http.MethodHead:
			// pkg/auth.go existiert im Repo, pkg/auth_test.go ist neu.
			if strings.Contains(r.URL.EscapedPath(), "auth_test") {
				w.WriteHeader(http.StatusNotFound)
			}
		case strings.HasSuffix(r.URL.Path, "/repository/commits") && r.Method == http.MethodPost:
			json.NewDecoder(r.Body).Decode(&commitBody)
			json.NewEncoder(w).Encode(Commit{ID: "def456", ShortID: "def456", Title: "Fix"})
		default:
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()

	sys := System{}
	cred := target.Credential{BaseURL: srv.URL, Token: "test-token"}
	workdir := t.TempDir()
	ctx := target.WithWorkdir(context.Background(), workdir)

	// Lokal editierter Checkout-Stand in der Sandbox.
	co := filepath.Join(workdir, "repos", "support-main-abc123")
	if err := os.MkdirAll(filepath.Join(co, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(co, "pkg", "auth.go"), []byte("package auth // gefixt"), 0o644)
	os.WriteFile(filepath.Join(co, "pkg", "auth_test.go"), []byte("package auth // neuer Test"), 0o644)

	params := `{"project_id":15,"branch":"fix/issue-23-login","message":"Login-Bug beheben",
		"checkout_path":"` + co + `","files":["pkg/auth.go","pkg/auth_test.go"],"deleted":["pkg/alt.go"]}`
	res, err := sys.Execute(ctx, "commit", []byte(params), cred)
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	cr := res.(CommitResult)
	if !cr.BranchCreated || cr.Branch != "fix/issue-23-login" || cr.Commit.ID != "def456" {
		t.Fatalf("commit-Ergebnis falsch: %+v", cr)
	}
	if commitBody["start_branch"] != "main" || commitBody["branch"] != "fix/issue-23-login" {
		t.Fatalf("neuer Branch muss vom Default-Branch abzweigen: %+v", commitBody)
	}
	actions := commitBody["actions"].([]any)
	if len(actions) != 3 {
		t.Fatalf("erwartet 3 actions, war %d", len(actions))
	}
	byPath := map[string]map[string]any{}
	for _, a := range actions {
		m := a.(map[string]any)
		byPath[m["file_path"].(string)] = m
	}
	if byPath["pkg/auth.go"]["action"] != "update" || byPath["pkg/auth.go"]["encoding"] != "base64" {
		t.Fatalf("existierende Datei muss als base64-update laufen: %+v", byPath["pkg/auth.go"])
	}
	if byPath["pkg/auth_test.go"]["action"] != "create" {
		t.Fatalf("neue Datei muss als create laufen: %+v", byPath["pkg/auth_test.go"])
	}
	if byPath["pkg/alt.go"]["action"] != "delete" {
		t.Fatalf("deleted muss als delete laufen: %+v", byPath["pkg/alt.go"])
	}

	// Fail-closed-Fälle.
	for name, p := range map[string]string{
		"Default-Branch":  `{"project_id":15,"branch":"main","message":"x","checkout_path":"` + co + `","files":["pkg/auth.go"]}`,
		"ohne Dateien":    `{"project_id":15,"branch":"fix/x","message":"x","checkout_path":"` + co + `"}`,
		"ohne message":    `{"project_id":15,"branch":"fix/x","checkout_path":"` + co + `","files":["pkg/auth.go"]}`,
		"Pfad-Traversal":  `{"project_id":15,"branch":"fix/x","message":"x","checkout_path":"` + co + `","files":["../../etc/passwd"]}`,
		"fremder Pfad":    `{"project_id":15,"branch":"fix/x","message":"x","checkout_path":"/etc","files":["passwd"]}`,
		"ohne project_id": `{"branch":"fix/x","message":"x","checkout_path":"` + co + `","files":["pkg/auth.go"]}`,
	} {
		if _, err := sys.Execute(ctx, "commit", []byte(p), cred); err == nil {
			t.Fatalf("commit muss fehlschlagen: %s", name)
		}
	}
	// Ohne Sandbox-Workdir (Control-Plane-Kontext) klare Ablehnung.
	if _, err := sys.Execute(context.Background(), "commit", []byte(params), cred); err == nil {
		t.Fatal("commit ohne workdir muss fehlschlagen")
	}
}

func TestCommitOnExistingBranch(t *testing.T) {
	var commitBody map[string]any
	var existsRef string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v4/projects/15":
			json.NewEncoder(w).Encode(ProjectDetail{ID: 15, DefaultBranch: "main"})
		case strings.Contains(r.URL.Path, "/repository/branches"):
			json.NewEncoder(w).Encode([]Branch{{Name: "fix/issue-23-login"}})
		case r.Method == http.MethodHead:
			existsRef = r.URL.Query().Get("ref")
		case strings.HasSuffix(r.URL.Path, "/repository/commits"):
			json.NewDecoder(r.Body).Decode(&commitBody)
			json.NewEncoder(w).Encode(Commit{ID: "def457"})
		}
	}))
	defer srv.Close()

	workdir := t.TempDir()
	co := filepath.Join(workdir, "repos", "support")
	os.MkdirAll(co, 0o755)
	os.WriteFile(filepath.Join(co, "fix.go"), []byte("package fix"), 0o644)
	ctx := target.WithWorkdir(context.Background(), workdir)

	res, err := System{}.Execute(ctx, "commit", []byte(`{"project_id":15,"branch":"fix/issue-23-login",
		"message":"Nachbesserung","checkout_path":"`+co+`","files":["fix.go"]}`),
		target.Credential{BaseURL: srv.URL, Token: "t"})
	if err != nil {
		t.Fatalf("commit auf bestehendem Branch: %v", err)
	}
	if cr := res.(CommitResult); cr.BranchCreated {
		t.Fatalf("bestehender Branch darf nicht als neu gemeldet werden: %+v", cr)
	}
	if _, hasStart := commitBody["start_branch"]; hasStart {
		t.Fatalf("bestehender Branch darf kein start_branch senden: %+v", commitBody)
	}
	if existsRef != "fix/issue-23-login" {
		t.Fatalf("Existenz-Prüfung muss gegen den bestehenden Branch laufen: %q", existsRef)
	}
}

func TestCreateMergeRequestAction(t *testing.T) {
	var mrBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v4/projects/15":
			json.NewEncoder(w).Encode(ProjectDetail{ID: 15, DefaultBranch: "main"})
		case r.URL.Path == "/api/v4/users":
			if r.URL.Query().Get("username") == "leaddev" {
				json.NewEncoder(w).Encode([]User{{ID: 7, Username: "leaddev"}})
			} else {
				json.NewEncoder(w).Encode([]User{})
			}
		case r.URL.Path == "/api/v4/projects/15/merge_requests" && r.Method == http.MethodPost:
			json.NewDecoder(r.Body).Decode(&mrBody)
			json.NewEncoder(w).Encode(MergeRequest{IID: 9, Title: "Fix Login", State: "opened",
				SourceBranch: "fix/issue-23-login", TargetBranch: "main", WebURL: "https://git.example.com/mr/9"})
		}
	}))
	defer srv.Close()

	sys := System{}
	cred := target.Credential{BaseURL: srv.URL, Token: "test-token"}
	ctx := context.Background()

	res, err := sys.Execute(ctx, "create_merge_request", []byte(`{"project_id":15,
		"source_branch":"fix/issue-23-login","title":"Fix Login","description":"Closes #23","assignee":"@leaddev"}`), cred)
	if err != nil {
		t.Fatalf("create_merge_request: %v", err)
	}
	if mr := res.(MergeRequest); mr.IID != 9 || mr.TargetBranch != "main" {
		t.Fatalf("mr falsch: %+v", mr)
	}
	// Ohne target_branch muss der Default-Branch des Projekts gezogen werden,
	// der Vorgesetzte wird Assignee UND Reviewer, der Branch nach Merge entfernt.
	if mrBody["target_branch"] != "main" || mrBody["remove_source_branch"] != true {
		t.Fatalf("mr-body falsch: %+v", mrBody)
	}
	for _, key := range []string{"assignee_ids", "reviewer_ids"} {
		ids, _ := mrBody[key].([]any)
		if len(ids) != 1 || ids[0] != float64(7) {
			t.Fatalf("%s muss der Vorgesetzte sein: %+v", key, mrBody)
		}
	}

	for name, p := range map[string]string{
		"ohne assignee":      `{"project_id":15,"source_branch":"fix/x","title":"Fix"}`,
		"unbekannter Nutzer": `{"project_id":15,"source_branch":"fix/x","title":"Fix","assignee":"gibtsnicht"}`,
		"ohne source_branch": `{"project_id":15,"title":"Fix","assignee":"leaddev"}`,
		"ohne title":         `{"project_id":15,"source_branch":"fix/x","assignee":"leaddev"}`,
		"source gleich ziel": `{"project_id":15,"source_branch":"main","title":"Fix","assignee":"leaddev"}`,
	} {
		if _, err := sys.Execute(ctx, "create_merge_request", []byte(p), cred); err == nil {
			t.Fatalf("create_merge_request muss fehlschlagen: %s", name)
		}
	}
}

func TestMRReviewActions(t *testing.T) {
	var commentBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v4/projects/15/merge_requests/9" && r.Method == http.MethodGet:
			json.NewEncoder(w).Encode(MergeRequestDetail{IID: 9, Title: "Fix Login", State: "opened",
				SourceBranch: "fix/issue-23-login", DetailedMergeStatus: "mergeable",
				HeadPipeline: &Pipeline{ID: 4, Status: "success", Ref: "fix/issue-23-login"}})
		case r.URL.Path == "/api/v4/projects/15/merge_requests/9/notes" && r.Method == http.MethodGet:
			json.NewEncoder(w).Encode([]Note{{ID: 120, Body: "Bitte noch einen Test ergänzen"}})
		case r.URL.Path == "/api/v4/projects/15/merge_requests/9/notes" && r.Method == http.MethodPost:
			json.NewDecoder(r.Body).Decode(&commentBody)
			json.NewEncoder(w).Encode(Note{ID: 121, Body: "Erledigt"})
		case r.URL.Path == "/api/v4/projects/15/pipelines":
			if r.URL.Query().Get("ref") != "fix/issue-23-login" {
				t.Errorf("ref-filter fehlt: %s", r.URL.RawQuery)
			}
			json.NewEncoder(w).Encode([]Pipeline{{ID: 4, Status: "success", Ref: "fix/issue-23-login"}})
		default:
			t.Errorf("unerwarteter request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	sys := System{}
	cred := target.Credential{BaseURL: srv.URL, Token: "test-token"}
	ctx := context.Background()

	res, err := sys.Execute(ctx, "get_merge_request", []byte(`{"project_id":15,"mr_iid":9}`), cred)
	if err != nil {
		t.Fatalf("get_merge_request: %v", err)
	}
	if mr := res.(MergeRequestDetail); mr.DetailedMergeStatus != "mergeable" ||
		mr.HeadPipeline == nil || mr.HeadPipeline.Status != "success" {
		t.Fatalf("mr-detail falsch: %+v", mr)
	}

	res, err = sys.Execute(ctx, "list_mr_notes", []byte(`{"project_id":15,"mr_iid":9}`), cred)
	if err != nil {
		t.Fatalf("list_mr_notes: %v", err)
	}
	if notes := res.([]Note); len(notes) != 1 || notes[0].ID != 120 {
		t.Fatalf("mr-notes falsch: %+v", notes)
	}

	if _, err = sys.Execute(ctx, "comment_mr", []byte(`{"project_id":15,"mr_iid":9,"body":"Erledigt"}`), cred); err != nil {
		t.Fatalf("comment_mr: %v", err)
	}
	if commentBody["body"] != "Erledigt" {
		t.Fatalf("comment-body falsch: %+v", commentBody)
	}

	res, err = sys.Execute(ctx, "list_pipelines", []byte(`{"project_id":15,"ref":"fix/issue-23-login"}`), cred)
	if err != nil {
		t.Fatalf("list_pipelines: %v", err)
	}
	if ps := res.([]Pipeline); len(ps) != 1 || ps[0].Status != "success" {
		t.Fatalf("pipelines falsch: %+v", ps)
	}

	// Pflichtparameter fehlen → Fehler statt stiller Leerlauf.
	for name, call := range map[string][2]string{
		"get_merge_request ohne mr_iid":       {"get_merge_request", `{"project_id":15}`},
		"list_mr_notes ohne mr_iid":           {"list_mr_notes", `{"project_id":15}`},
		"comment_mr ohne body":                {"comment_mr", `{"project_id":15,"mr_iid":9}`},
		"list_pipelines ohne project":         {"list_pipelines", `{}`},
		"list_pipeline_jobs ohne pipeline_id": {"list_pipeline_jobs", `{"project_id":15}`},
		"get_job_log ohne job_id":             {"get_job_log", `{"project_id":15}`},
	} {
		if _, err := sys.Execute(ctx, call[0], []byte(call[1]), cred); err == nil {
			t.Fatalf("%s muss fehlschlagen", name)
		}
	}
}

func TestPipelineDiagnosisActions(t *testing.T) {
	bigLog := strings.Repeat("x", maxJobLogBytes) + "FEHLER: assertion failed\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v4/projects/15/pipelines/4/jobs":
			json.NewEncoder(w).Encode([]Job{
				{ID: 41, Name: "build", Stage: "build", Status: "success"},
				{ID: 42, Name: "test", Stage: "test", Status: "failed"},
			})
		case "/api/v4/projects/15/jobs/42/trace":
			w.Write([]byte(bigLog))
		case "/api/v4/projects/15/pipelines/4/retry":
			if r.Method != http.MethodPost {
				t.Errorf("retry muss POST sein, got %s", r.Method)
			}
			json.NewEncoder(w).Encode(Pipeline{ID: 5, Status: "pending", Ref: "fix/x"})
		default:
			t.Errorf("unerwarteter request: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	sys := System{}
	cred := target.Credential{BaseURL: srv.URL, Token: "test-token"}
	ctx := context.Background()

	res, err := sys.Execute(ctx, "list_pipeline_jobs", []byte(`{"project_id":15,"pipeline_id":4}`), cred)
	if err != nil {
		t.Fatalf("list_pipeline_jobs: %v", err)
	}
	jobs := res.([]Job)
	if len(jobs) != 2 || jobs[1].Status != "failed" {
		t.Fatalf("jobs falsch: %+v", jobs)
	}

	res, err = sys.Execute(ctx, "get_job_log", []byte(`{"project_id":15,"job_id":42}`), cred)
	if err != nil {
		t.Fatalf("get_job_log: %v", err)
	}
	out := res.(map[string]any)
	logText := out["log"].(string)
	// Das ENDE des Logs muss erhalten bleiben (dort steht der Fehler), der
	// Anfang darf der Kappung zum Opfer fallen.
	if !strings.Contains(logText, "FEHLER: assertion failed") {
		t.Fatal("log-ende muss erhalten bleiben")
	}
	if out["truncated"] != true || len(logText) > maxJobLogBytes {
		t.Fatalf("log muss auf %d bytes gekappt sein: len=%d truncated=%v",
			maxJobLogBytes, len(logText), out["truncated"])
	}

	res, err = sys.Execute(ctx, "retry_pipeline", []byte(`{"project_id":15,"pipeline_id":4}`), cred)
	if err != nil {
		t.Fatalf("retry_pipeline: %v", err)
	}
	if p := res.(Pipeline); p.ID != 5 || p.Status != "pending" {
		t.Fatalf("retry-ergebnis falsch: %+v", p)
	}
	if _, err := sys.Execute(ctx, "retry_pipeline", []byte(`{"project_id":15}`), cred); err == nil {
		t.Fatal("retry_pipeline ohne pipeline_id muss fehlschlagen")
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

func TestAssignAction(t *testing.T) {
	var gotPath, gotMethod, gotQuery string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod, gotQuery = r.URL.Path, r.Method, r.URL.RawQuery
		gotBody = nil
		if r.Body != nil {
			json.NewDecoder(r.Body).Decode(&gotBody)
		}
		switch r.URL.Path {
		case "/api/v4/users":
			if r.URL.Query().Get("username") == "maxm" {
				json.NewEncoder(w).Encode([]User{{ID: 42, Username: "maxm", Name: "Max Mustermann"}})
			} else {
				json.NewEncoder(w).Encode([]User{})
			}
		default:
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()

	sys := System{}
	cred := target.Credential{BaseURL: srv.URL, Token: "test-token"}
	ctx := context.Background()

	out, err := sys.Execute(ctx, "assign", []byte(`{"project_id":15,"issue_iid":23,"username":"@maxm"}`), cred)
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	if m := out.(map[string]any); m["assigned_to"] != "maxm" || m["user_id"] != 42 {
		t.Fatalf("assign-Ergebnis falsch: %+v", out)
	}
	if gotMethod != http.MethodPut || gotPath != "/api/v4/projects/15/issues/23" {
		t.Fatalf("assign muss PUT /projects/15/issues/23 sein: %s %s (query %s)", gotMethod, gotPath, gotQuery)
	}
	ids, _ := gotBody["assignee_ids"].([]any)
	if len(ids) != 1 || ids[0] != float64(42) {
		t.Fatalf("assignee_ids falsch: %+v", gotBody)
	}

	if _, err := sys.Execute(ctx, "assign", []byte(`{"project_id":15,"issue_iid":23,"username":"gibtsnicht"}`), cred); err == nil {
		t.Fatal("unbekannter Username muss einen Fehler liefern (nie raten)")
	}
	if _, err := sys.Execute(ctx, "assign", []byte(`{"username":"maxm"}`), cred); err == nil {
		t.Fatal("assign ohne project_id/issue_iid muss abgelehnt werden")
	}
}

// TestReviewerHandoff deckt die Übergabe eines MR an einen QA-/Test-Agenten ab:
// create_merge_request mit separatem reviewer, set_reviewer auf einen
// bestehenden MR und approve_mr als Freigabe.
func TestReviewerHandoff(t *testing.T) {
	var mrBody, reviewerBody map[string]any
	var approvePath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v4/projects/15":
			json.NewEncoder(w).Encode(ProjectDetail{ID: 15, DefaultBranch: "main"})
		case r.URL.Path == "/api/v4/users":
			switch r.URL.Query().Get("username") {
			case "leaddev":
				json.NewEncoder(w).Encode([]User{{ID: 7, Username: "leaddev"}})
			case "qa-bot":
				json.NewEncoder(w).Encode([]User{{ID: 8, Username: "qa-bot"}})
			default:
				json.NewEncoder(w).Encode([]User{})
			}
		case r.URL.Path == "/api/v4/projects/15/merge_requests" && r.Method == http.MethodPost:
			json.NewDecoder(r.Body).Decode(&mrBody)
			json.NewEncoder(w).Encode(MergeRequest{IID: 9, State: "opened"})
		case r.URL.Path == "/api/v4/projects/15/merge_requests/9" && r.Method == http.MethodPut:
			json.NewDecoder(r.Body).Decode(&reviewerBody)
			json.NewEncoder(w).Encode(MergeRequestDetail{IID: 9})
		case r.URL.Path == "/api/v4/projects/15/merge_requests/9/approve" && r.Method == http.MethodPost:
			approvePath = r.URL.Path
			json.NewEncoder(w).Encode(map[string]any{"id": 9})
		default:
			t.Errorf("unerwarteter request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	sys := System{}
	cred := target.Credential{BaseURL: srv.URL, Token: "test-token"}
	ctx := context.Background()

	// create_merge_request mit reviewer ≠ assignee: Vorgesetzter bleibt Assignee,
	// QA-Agent wird Reviewer.
	if _, err := sys.Execute(ctx, "create_merge_request", []byte(`{"project_id":15,
		"source_branch":"fix/issue-23-login","title":"Fix Login","assignee":"leaddev","reviewer":"qa-bot"}`), cred); err != nil {
		t.Fatalf("create_merge_request mit reviewer: %v", err)
	}
	if ids, _ := mrBody["assignee_ids"].([]any); len(ids) != 1 || ids[0] != float64(7) {
		t.Fatalf("assignee muss der Vorgesetzte (7) sein: %+v", mrBody)
	}
	if ids, _ := mrBody["reviewer_ids"].([]any); len(ids) != 1 || ids[0] != float64(8) {
		t.Fatalf("reviewer muss der QA-Agent (8) sein: %+v", mrBody)
	}

	// set_reviewer auf einen bestehenden MR.
	out, err := sys.Execute(ctx, "set_reviewer", []byte(`{"project_id":15,"mr_iid":9,"username":"@qa-bot"}`), cred)
	if err != nil {
		t.Fatalf("set_reviewer: %v", err)
	}
	if m := out.(map[string]any); m["reviewer"] != "qa-bot" || m["user_id"] != 8 {
		t.Fatalf("set_reviewer-Ergebnis falsch: %+v", out)
	}
	if ids, _ := reviewerBody["reviewer_ids"].([]any); len(ids) != 1 || ids[0] != float64(8) {
		t.Fatalf("reviewer_ids falsch: %+v", reviewerBody)
	}

	// approve_mr als Freigabe.
	if _, err := sys.Execute(ctx, "approve_mr", []byte(`{"project_id":15,"mr_iid":9}`), cred); err != nil {
		t.Fatalf("approve_mr: %v", err)
	}
	if approvePath != "/api/v4/projects/15/merge_requests/9/approve" {
		t.Fatalf("approve muss POST .../approve sein, war %q", approvePath)
	}

	// Pflichtparameter fehlen → Fehler.
	for name, call := range map[string][2]string{
		"set_reviewer ohne mr_iid":   {"set_reviewer", `{"project_id":15,"username":"qa-bot"}`},
		"set_reviewer unbek. Nutzer": {"set_reviewer", `{"project_id":15,"mr_iid":9,"username":"gibtsnicht"}`},
		"approve_mr ohne mr_iid":     {"approve_mr", `{"project_id":15}`},
	} {
		if _, err := sys.Execute(ctx, call[0], []byte(call[1]), cred); err == nil {
			t.Fatalf("%s muss fehlschlagen", name)
		}
	}
}

// TestHasWorkKindIssuesAssigned prüft, dass nur-wenn: gitlab:issues:assigned nur
// die dem Bot ZUGEWIESENEN offenen Issues zählt (scope=assigned_to_me) — sonst
// weckt jedes fremde offene Issue im Scope einen Agenten, der laut Playbook nur
// zugewiesene Issues bearbeitet.
func TestHasWorkKindIssuesAssigned(t *testing.T) {
	var issues []Issue
	var sawScope string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/issues" {
			t.Errorf("unerwarteter request: %s", r.URL.Path)
			return
		}
		sawScope = r.URL.Query().Get("scope")
		json.NewEncoder(w).Encode(issues)
	}))
	defer srv.Close()

	sys := System{}
	cred := target.Credential{BaseURL: srv.URL, Token: "t"}
	ctx := context.Background()

	mine := Issue{IID: 23, ProjectID: 15}
	mine.References.Full = "gruppe/support#23"

	// Kein zugewiesenes Issue → keine Arbeit; der Check muss assigned_to_me sein.
	issues = nil
	if has, err := sys.HasWorkKind(ctx, cred, "issues:assigned"); err != nil || has {
		t.Fatalf("ohne Zuweisung: has=%v err=%v", has, err)
	}
	if sawScope != "assigned_to_me" {
		t.Fatalf("assigned-Subscope muss scope=assigned_to_me abfragen, war %q", sawScope)
	}

	// Ein zugewiesenes offenes Issue → Arbeit.
	issues = []Issue{mine}
	if has, err := sys.HasWorkKind(ctx, cred, "assigned"); err != nil || !has {
		t.Fatalf("mit Zuweisung: has=%v err=%v", has, err)
	}
}

// TestHasWorkKindReview prüft den Reviewer-seitigen Vorabcheck (nur-wenn:
// gitlab:review): ein an mich zum Review übergebener MR ist Arbeit, solange nicht
// ICH als Letzter kommentiert habe — inklusive des frischen MR ganz ohne Notiz.
func TestHasWorkKindReview(t *testing.T) {
	var reviewMRs []MergeRequest
	var mrNotes []Note
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v4/user":
			json.NewEncoder(w).Encode(User{ID: 8, Username: "qa-bot"})
		case r.URL.Path == "/api/v4/merge_requests":
			if r.URL.Query().Get("reviewer_username") != "qa-bot" {
				t.Errorf("reviewer_username-Filter fehlt: %s", r.URL.RawQuery)
			}
			json.NewEncoder(w).Encode(reviewMRs)
		case strings.HasSuffix(r.URL.Path, "/notes"):
			json.NewEncoder(w).Encode(mrNotes)
		default:
			t.Errorf("unerwarteter request: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	sys := System{}
	cred := target.Credential{BaseURL: srv.URL, Token: "t"}
	ctx := context.Background()

	mrIn := MergeRequest{IID: 9, ProjectID: 15}
	mrIn.References.Full = "gruppe/support!9"
	author := []Note{{ID: 1, Body: "Habe nachgebessert", Author: struct {
		Username string `json:"username"`
	}{Username: "leaddev"}}}
	mine := []Note{{ID: 2, Body: "Getestet, ein Mangel: …", Author: struct {
		Username string `json:"username"`
	}{Username: "qa-bot"}}}

	check := func(want bool) {
		t.Helper()
		has, err := sys.HasWorkKind(ctx, cred, "review")
		if err != nil {
			t.Fatalf("HasWorkKind(review): %v", err)
		}
		if has != want {
			t.Fatalf("HasWorkKind(review) = %v, erwartet %v", has, want)
		}
	}

	// Kein MR mir zum Review zugewiesen → keine Arbeit.
	reviewMRs, mrNotes = nil, nil
	check(false)

	// Frisch zugewiesener MR ohne Kommentar → Erst-Review steht aus (Arbeit).
	reviewMRs, mrNotes = []MergeRequest{mrIn}, nil
	check(true)

	// Autor hat zuletzt geantwortet (nachgebessert) → erneut prüfen (Arbeit).
	reviewMRs, mrNotes = []MergeRequest{mrIn}, author
	check(true)

	// Ich (qa-bot) habe zuletzt kommentiert → Runde beantwortet, keine Arbeit.
	reviewMRs, mrNotes = []MergeRequest{mrIn}, mine
	check(false)
}
