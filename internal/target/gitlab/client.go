// Package gitlab bindet GitLab als Zielsystem-Plugin an (analog spec/13 für
// Zammad): REST-Client (API v4) für die Agent-Aktionen und Webhook-Verarbeitung
// (Token-verifiziert, idempotent). Einheit der Arbeit ist das Issue.
package gitlab

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"covey/internal/reqlog"
)

// Client spricht die GitLab-REST-API (v4) mit einem (gebrokerten) API-Token.
// Das Token kommt pro Aufruf aus dem SecretStore — es wird nie persistiert.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

func NewClient(baseURL, token string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Token:   token,
		HTTP:    reqlog.Client("gitlab", 15*time.Second),
	}
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+"/api/v4"+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("PRIVATE-TOKEN", c.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("gitlab %s %s: HTTP %d: %.300s", method, path, resp.StatusCode, data)
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

type Issue struct {
	ID          int      `json:"id"`
	IID         int      `json:"iid"`
	ProjectID   int      `json:"project_id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	State       string   `json:"state"`
	Labels      []string `json:"labels"`
	WebURL      string   `json:"web_url"`
	// Assignees macht die Zuweisung für den Agenten sichtbar — Playbooks wie
	// „bearbeite nur dir zugewiesene Issues" brauchen diese Information.
	Assignees []struct {
		Username string `json:"username"`
	} `json:"assignees"`
	// Milestone ordnet das Issue einem Vorhaben zu (Release, Ausschreibung,
	// Sprint). Ein Agent, der ein ganzes Vorhaben führt, braucht den Titel, um
	// zu erkennen, was zu seinem Auftrag gehört; GitLab liefert null, wenn das
	// Issue keinem Meilenstein zugeordnet ist.
	Milestone *struct {
		Title   string `json:"title"`
		DueDate string `json:"due_date"`
		State   string `json:"state"`
	} `json:"milestone"`
	// Author ist der Melder: Wer den Bedarf aufgeschrieben hat, ist der
	// natürliche Empfänger des Merge Requests, der ihn erledigt.
	Author struct {
		Username string `json:"username"`
	} `json:"author"`
	// References.Full ist die volle Referenz "gruppe/projekt#iid" — daraus
	// lässt sich der Projektpfad für den Intake-Filter ableiten.
	References struct {
		Full string `json:"full"`
	} `json:"references"`
}

type Project struct {
	ID                int    `json:"id"`
	PathWithNamespace string `json:"path_with_namespace"`
	Description       string `json:"description"`
	WebURL            string `json:"web_url"`
}

type Note struct {
	ID       int    `json:"id"`
	Body     string `json:"body"`
	Internal bool   `json:"internal"`
	System   bool   `json:"system"`
	Author   struct {
		Username string `json:"username"`
	} `json:"author"`
	CreatedAt string `json:"created_at"`
}

// ListProjects — GET /projects?membership=true: alle Projekte, in denen der
// Bot-Nutzer Mitglied ist. Der Einstieg für Agenten, die ihre project_ids
// noch nicht kennen.
func (c *Client) ListProjects(ctx context.Context) ([]Project, error) {
	var out []Project
	err := c.do(ctx, http.MethodGet,
		"/projects?membership=true&simple=true&archived=false&order_by=last_activity_at&per_page=100", nil, &out)
	return out, err
}

// ListIssues findet Issues — mit projectID über GET /projects/{id}/issues,
// ohne (projectID=0) über das globale GET /issues mit scope=all: alle Issues,
// die das Token sehen darf. state ist "opened" (Default), "closed" oder "all";
// labels (kommasepariert), search und milestone (Meilenstein-TITEL, wie er in
// GitLab steht) schränken optional ein. assigned=true liefert nur Issues, die
// dem Bot-Nutzer des Tokens zugewiesen sind (scope=assigned_to_me) — für
// Agenten, die laut Playbook nur ihre eigene Zuweisung bearbeiten.
func (c *Client) ListIssues(ctx context.Context, projectID int, state, labels, search, milestone string, assigned bool) ([]Issue, error) {
	q := url.Values{}
	if state == "" {
		state = "opened"
	}
	if state != "all" {
		q.Set("state", state)
	}
	if labels != "" {
		q.Set("labels", labels)
	}
	if search != "" {
		q.Set("search", search)
	}
	if milestone != "" {
		q.Set("milestone", milestone)
	}
	if assigned {
		q.Set("scope", "assigned_to_me")
	}
	q.Set("order_by", "updated_at")
	q.Set("per_page", "100")
	path := "/issues?" + q.Encode()
	if projectID != 0 {
		path = fmt.Sprintf("/projects/%d/issues?%s", projectID, q.Encode())
	} else if !assigned {
		path = "/issues?scope=all&" + q.Encode()
	}
	var out []Issue
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

// GetIssue — GET /projects/{id}/issues/{iid}
func (c *Client) GetIssue(ctx context.Context, projectID, issueIID int) (Issue, error) {
	var i Issue
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("/projects/%d/issues/%d", projectID, issueIID), nil, &i)
	return i, err
}

// CreateIssue — POST /projects/{id}/issues: legt ein neues Issue (Ticket) an.
// Für den Intake von Bug-Reports, die NICHT aus GitLab selbst kommen (z. B. per
// E-Mail gemeldet) — der Agent überführt die Meldung in ein nachverfolgbares
// Ticket. title ist Pflicht; description (Markdown), labels (kommagetrennt) und
// assignee (User-ID, 0 = keine Zuweisung) sind optional.
func (c *Client) CreateIssue(ctx context.Context, projectID int, title, description, labels string, assigneeID int) (Issue, error) {
	body := map[string]any{"title": title}
	if description != "" {
		body["description"] = description
	}
	if labels != "" {
		body["labels"] = labels
	}
	if assigneeID != 0 {
		body["assignee_ids"] = []int{assigneeID}
	}
	var out Issue
	err := c.do(ctx, http.MethodPost, fmt.Sprintf("/projects/%d/issues", projectID), body, &out)
	return out, err
}

// ListNotes — GET /projects/{id}/issues/{iid}/notes (chronologisch)
func (c *Client) ListNotes(ctx context.Context, projectID, issueIID int) ([]Note, error) {
	var out []Note
	err := c.do(ctx, http.MethodGet,
		fmt.Sprintf("/projects/%d/issues/%d/notes?sort=asc&order_by=created_at", projectID, issueIID), nil, &out)
	return out, err
}

// Comment — POST /projects/{id}/issues/{iid}/notes. internal=true ist eine
// interne Notiz (nur für Projektmitglieder ab Reporter sichtbar), internal=false
// ein öffentlicher Kommentar — auch für externe Reporter sichtbar.
func (c *Client) Comment(ctx context.Context, projectID, issueIID int, body string, internal bool) (Note, error) {
	var out Note
	err := c.do(ctx, http.MethodPost, fmt.Sprintf("/projects/%d/issues/%d/notes", projectID, issueIID),
		map[string]any{"body": body, "internal": internal}, &out)
	return out, err
}

// DownloadArchive streamt das Repository-Archiv (tar.gz) —
// GET /projects/{id}/repository/archive.tar.gz, optional auf einen Ref
// (Branch, Tag, SHA) und ein Unterverzeichnis (subPath) eingeschränkt —
// Letzteres macht große Repos per Teil-Checkout handhabbar. Bewusst nicht
// über do(): der Body ist binär und kann groß sein; der Aufrufer schließt
// den Reader.
func (c *Client) DownloadArchive(ctx context.Context, projectID int, ref, subPath string) (io.ReadCloser, error) {
	path := fmt.Sprintf("/projects/%d/repository/archive.tar.gz", projectID)
	q := url.Values{}
	if ref != "" {
		q.Set("sha", ref)
	}
	if subPath != "" {
		q.Set("path", subPath)
	}
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/api/v4"+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("PRIVATE-TOKEN", c.Token)
	// Eigener HTTP-Client ohne das knappe API-Timeout — der Download eines
	// großen Repos darf länger dauern; die Grenze setzt der Aufruf-Context.
	resp, err := reqlog.Client("gitlab", 0).Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		resp.Body.Close()
		return nil, fmt.Errorf("gitlab GET %s: HTTP %d: %.300s", path, resp.StatusCode, data)
	}
	return resp.Body, nil
}

// uploadRefRE zerlegt eine GitLab-Upload-Referenz in <secret>/<dateiname>.
// GitLab bettet in Markdown angehängte Bilder als /uploads/<32-hex>/<name>
// ein — projekt-relativ. Der Match funktioniert auf dem nackten Pfad
// (/uploads/…), auf der vollen Web-URL (…/gruppe/projekt/uploads/…) und auf
// der schon zerlegten Form <secret>/<name>.
var uploadRefRE = regexp.MustCompile(`([0-9a-fA-F]{32})/([^/?#\s]+)$`)

// maxUploadBytes begrenzt einen einzelnen heruntergeladenen Upload — ein
// Screenshot ist klein, ein versehentlich verlinktes Riesen-Asset soll die
// Sandbox nicht fluten.
const maxUploadBytes = 25 << 20 // 25 MB

// DownloadUpload lädt einen an ein Issue/MR angehängten Upload (Screenshot)
// gebrokert herunter — GET /projects/{id}/uploads/{secret}/{filename}. Wie
// DownloadArchive läuft er am JSON-do() vorbei (der Body ist binär), das Token
// bleibt im Daemon. ref ist die Referenz aus dem Markdown: der nackte Pfad
// "/uploads/<secret>/<datei>", die volle Web-URL oder schon "<secret>/<datei>".
// Rückgabe: Dateiname, Content-Type und der Reader (vom Aufrufer zu schließen).
func (c *Client) DownloadUpload(ctx context.Context, projectID int, ref string) (filename, contentType string, body io.ReadCloser, err error) {
	m := uploadRefRE.FindStringSubmatch(strings.TrimSpace(ref))
	if m == nil {
		return "", "", nil, fmt.Errorf("keine gültige Upload-Referenz in %q — erwartet /uploads/<secret>/<datei> aus der Issue-Beschreibung", ref)
	}
	secret, name := m[1], m[2]
	if decoded, derr := url.PathUnescape(name); derr == nil {
		name = decoded
	}
	path := fmt.Sprintf("/projects/%d/uploads/%s/%s", projectID, secret, url.PathEscape(name))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/api/v4"+path, nil)
	if err != nil {
		return "", "", nil, err
	}
	req.Header.Set("PRIVATE-TOKEN", c.Token)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", "", nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		resp.Body.Close()
		hint := ""
		if resp.StatusCode == http.StatusNotFound {
			hint = " (der Upload-Endpoint braucht GitLab ≥ 16.6 bzw. Projekt-Zugriff)"
		}
		return "", "", nil, fmt.Errorf("gitlab GET %s: HTTP %d%s: %.200s", path, resp.StatusCode, hint, data)
	}
	return name, resp.Header.Get("Content-Type"), resp.Body, nil
}

// UploadResult ist die Antwort von POST /projects/{id}/uploads: die Markdown-
// Referenz, die man in einen Kommentar einbetten kann, plus die relative URL.
type UploadResult struct {
	Alt      string `json:"alt"`
	URL      string `json:"url"`
	FullPath string `json:"full_path"`
	Markdown string `json:"markdown"`
}

// UploadFile lädt eine Datei (Screenshot) an ein Projekt — POST /projects/{id}/
// uploads, multipart. Wie DownloadUpload läuft er am JSON-do() vorbei (der Body
// ist multipart), das Token bleibt im Daemon. Das zurückgegebene "markdown"
// (![alt](/uploads/<secret>/<datei>)) bettet man in einen comment_mr-Body ein.
func (c *Client) UploadFile(ctx context.Context, projectID int, filename string, data []byte) (UploadResult, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filepath.Base(filename))
	if err != nil {
		return UploadResult{}, err
	}
	if _, err := fw.Write(data); err != nil {
		return UploadResult{}, err
	}
	if err := mw.Close(); err != nil {
		return UploadResult{}, err
	}
	path := fmt.Sprintf("/projects/%d/uploads", projectID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/api/v4"+path, &buf)
	if err != nil {
		return UploadResult{}, err
	}
	req.Header.Set("PRIVATE-TOKEN", c.Token)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return UploadResult{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return UploadResult{}, fmt.Errorf("gitlab POST %s: HTTP %d: %.200s", path, resp.StatusCode, body)
	}
	var out UploadResult
	if err := json.Unmarshal(body, &out); err != nil {
		return UploadResult{}, fmt.Errorf("upload-antwort: %w", err)
	}
	return out, nil
}

// TreeEntry ist ein Eintrag des Repository-Baums (Datei oder Verzeichnis).
type TreeEntry struct {
	Name string `json:"name"`
	Type string `json:"type"` // "blob" (Datei) | "tree" (Verzeichnis)
	Path string `json:"path"`
}

// ListTree — GET /projects/{id}/repository/tree: den Repository-Baum
// durchblättern, ohne etwas herunterzuladen. Für Repos, die zu groß für
// einen Checkout sind: erst navigieren, dann gezielt Dateien lesen.
func (c *Client) ListTree(ctx context.Context, projectID int, path, ref string, recursive bool) ([]TreeEntry, error) {
	q := url.Values{}
	if path != "" {
		q.Set("path", path)
	}
	if ref != "" {
		q.Set("ref", ref)
	}
	if recursive {
		q.Set("recursive", "true")
	}
	q.Set("per_page", "100")
	var out []TreeEntry
	err := c.do(ctx, http.MethodGet,
		fmt.Sprintf("/projects/%d/repository/tree?%s", projectID, q.Encode()), nil, &out)
	return out, err
}

// maxReadFileBytes begrenzt eine einzelne per read_file gelesene Datei.
const maxReadFileBytes = 512 << 10 // 512 KB

// ReadFile — GET /projects/{id}/repository/files/{path}/raw: eine einzelne
// Datei lesen, ohne Checkout. Der Dateipfad wird komplett URL-kodiert
// (inkl. "/"), wie die GitLab-API es verlangt.
func (c *Client) ReadFile(ctx context.Context, projectID int, filePath, ref string) (content string, truncated bool, err error) {
	path := fmt.Sprintf("/projects/%d/repository/files/%s/raw", projectID, url.QueryEscape(filePath))
	if ref != "" {
		path += "?ref=" + url.QueryEscape(ref)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/api/v4"+path, nil)
	if err != nil {
		return "", false, err
	}
	req.Header.Set("PRIVATE-TOKEN", c.Token)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxReadFileBytes+1))
	if err != nil {
		return "", false, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", false, fmt.Errorf("gitlab GET %s: HTTP %d: %.300s", path, resp.StatusCode, data)
	}
	if len(data) > maxReadFileBytes {
		return string(data[:maxReadFileBytes]), true, nil
	}
	return string(data), false, nil
}

// Commit ist ein Eintrag der Commit-Historie — genug Kontext, um zu erkennen,
// ob ein gemeldeter Bug inzwischen behoben wurde (Titel, Autor, Datum).
type Commit struct {
	ID         string `json:"id"`
	ShortID    string `json:"short_id"`
	Title      string `json:"title"`
	AuthorName string `json:"author_name"`
	CreatedAt  string `json:"created_at"`
	WebURL     string `json:"web_url"`
}

// ListCommits — GET /projects/{id}/repository/commits: die Historie eines
// Refs, optional auf einen Pfad (Datei/Verzeichnis) und ein Startdatum
// (since, ISO 8601) eingeschränkt. Damit prüft ein Agent, ob es seit dem
// Erstellen eines Issues Commits gibt, die den gemeldeten Fehler bereits
// beheben.
func (c *Client) ListCommits(ctx context.Context, projectID int, ref, path, since string) ([]Commit, error) {
	q := url.Values{}
	if ref != "" {
		q.Set("ref_name", ref)
	}
	if path != "" {
		q.Set("path", path)
	}
	if since != "" {
		q.Set("since", since)
	}
	q.Set("per_page", "50")
	var out []Commit
	err := c.do(ctx, http.MethodGet,
		fmt.Sprintf("/projects/%d/repository/commits?%s", projectID, q.Encode()), nil, &out)
	return out, err
}

// maxDiffBytesPerFile begrenzt den Diff einer einzelnen Datei in GetCommitDiff.
const maxDiffBytesPerFile = 16 << 10 // 16 KB

// CommitDiff ist der Diff einer Datei innerhalb eines Commits.
type CommitDiff struct {
	OldPath     string `json:"old_path"`
	NewPath     string `json:"new_path"`
	NewFile     bool   `json:"new_file"`
	DeletedFile bool   `json:"deleted_file"`
	Diff        string `json:"diff"`
	Truncated   bool   `json:"truncated,omitempty"`
}

// GetCommitDiff — GET /projects/{id}/repository/commits/{sha}/diff: was ein
// Commit tatsächlich ändert. Einzelne Datei-Diffs werden auf
// maxDiffBytesPerFile gekürzt, damit Riesen-Commits den Kontext des Agenten
// nicht sprengen.
func (c *Client) GetCommitDiff(ctx context.Context, projectID int, sha string) ([]CommitDiff, error) {
	var out []CommitDiff
	err := c.do(ctx, http.MethodGet,
		fmt.Sprintf("/projects/%d/repository/commits/%s/diff?per_page=100", projectID, url.PathEscape(sha)), nil, &out)
	for i := range out {
		if len(out[i].Diff) > maxDiffBytesPerFile {
			out[i].Diff = out[i].Diff[:maxDiffBytesPerFile]
			out[i].Truncated = true
		}
	}
	return out, err
}

// MergeRequest ist ein Eintrag der MR-Liste — genug, um offene oder gemergte
// Fixes zu einem Thema zu finden.
type MergeRequest struct {
	IID          int    `json:"iid"`
	ProjectID    int    `json:"project_id"`
	Title        string `json:"title"`
	State        string `json:"state"`
	SourceBranch string `json:"source_branch"`
	TargetBranch string `json:"target_branch"`
	MergedAt     string `json:"merged_at"`
	UpdatedAt    string `json:"updated_at"`
	WebURL       string `json:"web_url"`
	Author       struct {
		Username string `json:"username"`
	} `json:"author"`
	// References.Full ist die volle Referenz "gruppe/projekt!iid" — daraus
	// lässt sich der Projektpfad für den Intake-Filter ableiten (analog Issue).
	References struct {
		Full string `json:"full"`
	} `json:"references"`
}

// ListMergeRequests — GET /projects/{id}/merge_requests. state ist "opened",
// "merged", "closed" oder "all" (Default: all); search filtert auf Titel und
// Beschreibung, targetBranch auf den Ziel-Branch.
func (c *Client) ListMergeRequests(ctx context.Context, projectID int, state, search, targetBranch string) ([]MergeRequest, error) {
	q := url.Values{}
	if state != "" && state != "all" {
		q.Set("state", state)
	}
	if search != "" {
		q.Set("search", search)
	}
	if targetBranch != "" {
		q.Set("target_branch", targetBranch)
	}
	q.Set("order_by", "updated_at")
	q.Set("per_page", "50")
	var out []MergeRequest
	err := c.do(ctx, http.MethodGet,
		fmt.Sprintf("/projects/%d/merge_requests?%s", projectID, q.Encode()), nil, &out)
	return out, err
}

// ListMyOpenMergeRequests — GET /merge_requests?scope=created_by_me&state=opened:
// die offenen Merge Requests, die der Bot-Nutzer des Tokens selbst eröffnet hat.
// Der billige Vorab-Check für den Review-Loop (HasWork) — ohne project_id, also
// projektübergreifend, wie ListIssues(0, …). Der Filter läuft über die Token-
// Identität, es braucht keinen Username.
func (c *Client) ListMyOpenMergeRequests(ctx context.Context) ([]MergeRequest, error) {
	var out []MergeRequest
	err := c.do(ctx, http.MethodGet,
		"/merge_requests?scope=created_by_me&state=opened&order_by=updated_at&per_page=50", nil, &out)
	return out, err
}

// ListReviewMergeRequests — GET /merge_requests?reviewer_username=<user>&state=opened:
// die offenen Merge Requests, in denen der Bot-Nutzer als Reviewer eingetragen
// ist — die Review-Warteschlange eines QA-/Test-Agenten, projektübergreifend
// (wie ListMyOpenMergeRequests, nur aus Reviewer- statt Autoren-Sicht). Trägt
// den Review-Loop von der anderen Seite: der Entwickler-Agent setzt den
// QA-Agenten als Reviewer, dieser findet den MR hierüber.
func (c *Client) ListReviewMergeRequests(ctx context.Context, reviewerUsername string) ([]MergeRequest, error) {
	var out []MergeRequest
	err := c.do(ctx, http.MethodGet,
		"/merge_requests?reviewer_username="+url.QueryEscape(reviewerUsername)+"&state=opened&order_by=updated_at&per_page=50", nil, &out)
	return out, err
}

// CurrentUser — GET /user: das Profil des Token-Inhabers (der Bot-Nutzer).
// Gebraucht, um in einem MR-Thread den eigenen letzten Kommentar von fremdem
// Review-Feedback zu unterscheiden.
func (c *Client) CurrentUser(ctx context.Context) (User, error) {
	var u User
	err := c.do(ctx, http.MethodGet, "/user", nil, &u)
	return u, err
}

// MergeRequestDetail ist der volle Blick auf einen einzelnen MR — inklusive
// Review-Zustand (Merge-Status, Konflikte) und CI-Ergebnis (head_pipeline),
// damit ein Agent seinen eigenen MR wie ein Entwickler betreuen kann.
type MergeRequestDetail struct {
	IID          int    `json:"iid"`
	Title        string `json:"title"`
	Description  string `json:"description"`
	State        string `json:"state"`
	SourceBranch string `json:"source_branch"`
	TargetBranch string `json:"target_branch"`
	MergedAt     string `json:"merged_at"`
	WebURL       string `json:"web_url"`
	HasConflicts bool   `json:"has_conflicts"`
	// DetailedMergeStatus, z. B. "mergeable", "ci_still_running",
	// "conflict" — GitLabs eigene Zusammenfassung, warum ein MR (nicht) mergebar ist.
	DetailedMergeStatus string `json:"detailed_merge_status"`
	Author              struct {
		Username string `json:"username"`
	} `json:"author"`
	HeadPipeline *Pipeline `json:"head_pipeline"`
}

// Pipeline ist der CI-Lauf eines Refs/MRs — Status "success", "failed",
// "running" etc.
type Pipeline struct {
	ID        int    `json:"id"`
	Status    string `json:"status"`
	Ref       string `json:"ref"`
	SHA       string `json:"sha"`
	WebURL    string `json:"web_url"`
	UpdatedAt string `json:"updated_at"`
}

// GetMergeRequest — GET /projects/{id}/merge_requests/{iid}
func (c *Client) GetMergeRequest(ctx context.Context, projectID, mrIID int) (MergeRequestDetail, error) {
	var out MergeRequestDetail
	err := c.do(ctx, http.MethodGet,
		fmt.Sprintf("/projects/%d/merge_requests/%d", projectID, mrIID), nil, &out)
	return out, err
}

// ListMRNotes — GET /projects/{id}/merge_requests/{iid}/notes (chronologisch):
// der Diskussionsstand eines MR inklusive Review-Kommentaren auf dem Diff.
func (c *Client) ListMRNotes(ctx context.Context, projectID, mrIID int) ([]Note, error) {
	var out []Note
	err := c.do(ctx, http.MethodGet,
		fmt.Sprintf("/projects/%d/merge_requests/%d/notes?sort=asc&order_by=created_at", projectID, mrIID), nil, &out)
	return out, err
}

// CommentMR — POST /projects/{id}/merge_requests/{iid}/notes: die Antwort des
// Agenten im Review-Dialog seines MR.
func (c *Client) CommentMR(ctx context.Context, projectID, mrIID int, body string) (Note, error) {
	var out Note
	err := c.do(ctx, http.MethodPost,
		fmt.Sprintf("/projects/%d/merge_requests/%d/notes", projectID, mrIID),
		map[string]any{"body": body}, &out)
	return out, err
}

// SetMRReviewer — PUT /projects/{id}/merge_requests/{iid} mit reviewer_ids:
// trägt den/die Reviewer eines bestehenden MR ein. So übergibt der
// Entwickler-Agent seinen MR gezielt an den QA-Agenten (bzw. gibt ihn zurück),
// ohne dass die Zuweisung an den Vorgesetzten verlorengeht.
func (c *Client) SetMRReviewer(ctx context.Context, projectID, mrIID int, reviewerIDs []int) (MergeRequestDetail, error) {
	var out MergeRequestDetail
	err := c.do(ctx, http.MethodPut, fmt.Sprintf("/projects/%d/merge_requests/%d", projectID, mrIID),
		map[string]any{"reviewer_ids": reviewerIDs}, &out)
	return out, err
}

// ApproveMR — POST /projects/{id}/merge_requests/{iid}/approve: die formelle
// Freigabe eines Reviewers. Der QA-Agent nutzt sie als grünes Signal an den
// Vorgesetzten: „Feature getestet, alles grün" — das Mergen selbst bleibt beim
// Menschen. Ist Approval im Projekt nicht aktiviert, meldet GitLab einen Fehler;
// dann genügt der bestätigende comment_mr.
func (c *Client) ApproveMR(ctx context.Context, projectID, mrIID int) error {
	return c.do(ctx, http.MethodPost,
		fmt.Sprintf("/projects/%d/merge_requests/%d/approve", projectID, mrIID), nil, nil)
}

// ListPipelines — GET /projects/{id}/pipelines, optional auf einen Ref
// eingeschränkt: Lief die CI auf meinem Branch durch, bevor ich den MR zum
// Review gebe bzw. nach dem Nacharbeiten?
func (c *Client) ListPipelines(ctx context.Context, projectID int, ref string) ([]Pipeline, error) {
	q := url.Values{}
	if ref != "" {
		q.Set("ref", ref)
	}
	q.Set("per_page", "20")
	var out []Pipeline
	err := c.do(ctx, http.MethodGet,
		fmt.Sprintf("/projects/%d/pipelines?%s", projectID, q.Encode()), nil, &out)
	return out, err
}

// Job ist ein CI-Job eines Pipeline-Laufs — genug, um den fehlgeschlagenen
// Job zu finden und sein Log zu ziehen.
type Job struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	Stage        string `json:"stage"`
	Status       string `json:"status"`
	AllowFailure bool   `json:"allow_failure"`
	WebURL       string `json:"web_url"`
}

// ListPipelineJobs — GET /projects/{id}/pipelines/{pipeline_id}/jobs: die Jobs
// eines CI-Laufs mit Status. Der Einstieg in die Diagnose einer roten Pipeline.
func (c *Client) ListPipelineJobs(ctx context.Context, projectID, pipelineID int) ([]Job, error) {
	var out []Job
	err := c.do(ctx, http.MethodGet,
		fmt.Sprintf("/projects/%d/pipelines/%d/jobs?per_page=100", projectID, pipelineID), nil, &out)
	return out, err
}

// maxJobLogBytes begrenzt das zurückgegebene Job-Log; behalten wird das ENDE —
// dort stehen Fehlermeldungen und Test-Zusammenfassungen.
const maxJobLogBytes = 48 << 10

// GetJobLog — GET /projects/{id}/jobs/{job_id}/trace: das Log eines CI-Jobs.
// Traces können riesig sein; gelesen wird gedeckelt, geliefert das Log-Ende.
func (c *Client) GetJobLog(ctx context.Context, projectID, jobID int) (string, bool, error) {
	path := fmt.Sprintf("/projects/%d/jobs/%d/trace", projectID, jobID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/api/v4"+path, nil)
	if err != nil {
		return "", false, err
	}
	req.Header.Set("PRIVATE-TOKEN", c.Token)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", false, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", false, fmt.Errorf("gitlab GET %s: HTTP %d: %.300s", path, resp.StatusCode, data)
	}
	if len(data) > maxJobLogBytes {
		return string(data[len(data)-maxJobLogBytes:]), true, nil
	}
	return string(data), false, nil
}

// RetryPipeline — POST /projects/{id}/pipelines/{pipeline_id}/retry: startet
// die fehlgeschlagenen Jobs eines CI-Laufs neu — für den Fall, dass die
// Ursache außerhalb des Codes lag und inzwischen behoben ist (z. B.
// nachträglich erteilter Repo-Zugriff, Runner wieder da).
func (c *Client) RetryPipeline(ctx context.Context, projectID, pipelineID int) (Pipeline, error) {
	var out Pipeline
	err := c.do(ctx, http.MethodPost,
		fmt.Sprintf("/projects/%d/pipelines/%d/retry", projectID, pipelineID), nil, &out)
	return out, err
}

// Branch ist ein Eintrag der Branch-Liste. Default markiert den
// Default-Branch des Projekts.
type Branch struct {
	Name    string `json:"name"`
	Default bool   `json:"default"`
	Commit  struct {
		ShortID   string `json:"short_id"`
		CreatedAt string `json:"created_at"`
	} `json:"commit"`
}

// ListBranches — GET /projects/{id}/repository/branches, optional mit
// Namenssuche. Damit findet ein Agent den richtigen Ref, statt Branch-Namen
// zu raten.
func (c *Client) ListBranches(ctx context.Context, projectID int, search string) ([]Branch, error) {
	q := url.Values{}
	if search != "" {
		q.Set("search", search)
	}
	q.Set("per_page", "100")
	var out []Branch
	err := c.do(ctx, http.MethodGet,
		fmt.Sprintf("/projects/%d/repository/branches?%s", projectID, q.Encode()), nil, &out)
	return out, err
}

// ProjectDetail liefert die Projekt-Metadaten, die der Entwickler-Workflow
// braucht — vor allem den Default-Branch als Basis für Feature-Branches und
// als Ziel von Merge Requests.
type ProjectDetail struct {
	ID                int    `json:"id"`
	PathWithNamespace string `json:"path_with_namespace"`
	DefaultBranch     string `json:"default_branch"`
	WebURL            string `json:"web_url"`
}

// GetProject — GET /projects/{id}
func (c *Client) GetProject(ctx context.Context, projectID int) (ProjectDetail, error) {
	var p ProjectDetail
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("/projects/%d", projectID), nil, &p)
	return p, err
}

// FileExists — HEAD /projects/{id}/repository/files/{path}?ref=…: entscheidet,
// ob ein Commit die Datei anlegt (create) oder ändert (update) — die
// Commits-API verlangt die richtige Aktion und lehnt die falsche ab.
func (c *Client) FileExists(ctx context.Context, projectID int, filePath, ref string) (bool, error) {
	path := fmt.Sprintf("/projects/%d/repository/files/%s?ref=%s",
		projectID, url.QueryEscape(filePath), url.QueryEscape(ref))
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, c.BaseURL+"/api/v4"+path, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("PRIVATE-TOKEN", c.Token)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return false, err
	}
	resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return false, nil
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return true, nil
	default:
		return false, fmt.Errorf("gitlab HEAD %s: HTTP %d", path, resp.StatusCode)
	}
}

// CommitAction ist ein Eintrag im actions-Array der Commits-API: eine Datei
// anlegen, ändern oder löschen. Inhalte laufen base64-kodiert — so überleben
// auch Binärdateien und Sonderzeichen den JSON-Transport.
type CommitAction struct {
	Action   string `json:"action"` // "create" | "update" | "delete"
	FilePath string `json:"file_path"`
	Content  string `json:"content,omitempty"`
	Encoding string `json:"encoding,omitempty"`
}

// CommitFiles — POST /projects/{id}/repository/commits: ein Commit mit allen
// Datei-Änderungen in einem API-Aufruf. startBranch != "" legt den Branch als
// Kopie davon an (der Push-Weg des Agenten: das Token bleibt im Daemon, ein
// git-Remote mit Credentials existiert nie).
func (c *Client) CommitFiles(ctx context.Context, projectID int, branch, startBranch, message string, actions []CommitAction) (Commit, error) {
	body := map[string]any{
		"branch":         branch,
		"commit_message": message,
		"actions":        actions,
	}
	if startBranch != "" {
		body["start_branch"] = startBranch
	}
	var out Commit
	err := c.do(ctx, http.MethodPost, fmt.Sprintf("/projects/%d/repository/commits", projectID), body, &out)
	return out, err
}

// CreateMergeRequest — POST /projects/{id}/merge_requests: eröffnet den MR
// für einen gepushten Feature-Branch. assigneeID/reviewerID (idR der
// Vorgesetzte des Agenten) sind optional (0 = nicht setzen); der
// Source-Branch wird nach dem Merge automatisch entfernt.
func (c *Client) CreateMergeRequest(ctx context.Context, projectID int, sourceBranch, targetBranch, title, description string, assigneeID, reviewerID int) (MergeRequest, error) {
	body := map[string]any{
		"source_branch":        sourceBranch,
		"target_branch":        targetBranch,
		"title":                title,
		"description":          description,
		"remove_source_branch": true,
	}
	if assigneeID != 0 {
		body["assignee_ids"] = []int{assigneeID}
	}
	if reviewerID != 0 {
		body["reviewer_ids"] = []int{reviewerID}
	}
	var out MergeRequest
	err := c.do(ctx, http.MethodPost, fmt.Sprintf("/projects/%d/merge_requests", projectID), body, &out)
	return out, err
}

// SetState — PUT /projects/{id}/issues/{iid} mit state_event ("close"|"reopen").
func (c *Client) SetState(ctx context.Context, projectID, issueIID int, stateEvent string) error {
	if stateEvent != "close" && stateEvent != "reopen" {
		return fmt.Errorf("ungültiger state %q (erlaubt: close, reopen)", stateEvent)
	}
	return c.do(ctx, http.MethodPut, fmt.Sprintf("/projects/%d/issues/%d", projectID, issueIID),
		map[string]any{"state_event": stateEvent}, nil)
}

// User ist das Minimalprofil eines GitLab-Nutzers für die Zuweisung.
type User struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name"`
	State    string `json:"state"`
}

// LookupUser — GET /users?username=…: löst einen GitLab-Username in die
// numerische User-ID auf (die Issue-API kennt nur assignee_ids). Der
// Username-Filter der API matcht exakt, liefert aber eine Liste.
func (c *Client) LookupUser(ctx context.Context, username string) (User, error) {
	username = strings.TrimPrefix(strings.TrimSpace(username), "@")
	if username == "" {
		return User{}, fmt.Errorf("username fehlt")
	}
	var out []User
	if err := c.do(ctx, http.MethodGet, "/users?username="+url.QueryEscape(username), nil, &out); err != nil {
		return User{}, err
	}
	if len(out) == 0 {
		return User{}, fmt.Errorf("gitlab-nutzer %q nicht gefunden", username)
	}
	return out[0], nil
}

// AssignIssue — PUT /projects/{id}/issues/{iid} mit assignee_ids: weist das
// Issue einer Person zu (z. B. zum Testen einer Bugfix-Antwort).
func (c *Client) AssignIssue(ctx context.Context, projectID, issueIID int, userIDs []int) error {
	return c.do(ctx, http.MethodPut, fmt.Sprintf("/projects/%d/issues/%d", projectID, issueIID),
		map[string]any{"assignee_ids": userIDs}, nil)
}

// SetLabels — PUT /projects/{id}/issues/{iid} mit add_labels/remove_labels:
// setzt und entfernt Labels an einem BESTEHENDEN Issue, ohne die übrigen
// anzutasten. Genau diese Teil-Operation braucht ein Agent, der den
// Arbeitszustand eines Vorgangs im Board führt (etwa „bereit" → „in Arbeit"):
// Würde er die volle labels-Liste schreiben, löschte jeder Zustandswechsel die
// fachlichen Labels gleich mit.
//
// GitLab antwortet mit dem aktualisierten Issue — das geben wir zurück, damit
// der Agent den erreichten Zustand sieht, statt ihn erneut abzufragen.
func (c *Client) SetLabels(ctx context.Context, projectID, issueIID int, add, remove []string) (Issue, error) {
	body := map[string]any{}
	if joined := joinLabels(add); joined != "" {
		body["add_labels"] = joined
	}
	if joined := joinLabels(remove); joined != "" {
		body["remove_labels"] = joined
	}
	if len(body) == 0 {
		return Issue{}, fmt.Errorf("weder add_labels noch remove_labels angegeben")
	}
	var out Issue
	err := c.do(ctx, http.MethodPut,
		fmt.Sprintf("/projects/%d/issues/%d", projectID, issueIID), body, &out)
	return out, err
}

// joinLabels macht aus der Label-Liste des Agenten den kommaseparierten String,
// den die GitLab-API erwartet. Leere Einträge fallen raus — ein versehentliches
// "" im Array soll nicht als Label mit leerem Namen ankommen.
func joinLabels(labels []string) string {
	out := make([]string, 0, len(labels))
	for _, l := range labels {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return strings.Join(out, ",")
}

// Escalate setzt eine interne Notiz und entfernt die Zuweisung
// (assignee_ids leer), damit ein Mensch das Issue übernimmt.
func (c *Client) Escalate(ctx context.Context, projectID, issueIID int, note string) error {
	if _, err := c.Comment(ctx, projectID, issueIID, note, true); err != nil {
		return err
	}
	return c.do(ctx, http.MethodPut, fmt.Sprintf("/projects/%d/issues/%d", projectID, issueIID),
		map[string]any{"assignee_ids": []int{}}, nil)
}
