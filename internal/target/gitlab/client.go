// Package gitlab binds GitLab in as a target-system plugin (analogous to
// spec/13 for Zammad): a REST client (API v4) for the agent actions and webhook
// processing (token-verified, idempotent). The unit of work is the issue.
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
	"strconv"
	"strings"
	"time"

	"covey/internal/reqlog"
)

// Client speaks the GitLab REST API (v4) with a (brokered) API token. The token
// comes from the SecretStore per call — it is never persisted.
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
	_, err := c.doHeaders(ctx, method, path, body, out)
	return err
}

// doHeaders is do() with the response headers handed back. GitLab describes
// paginated collections exclusively in the headers (X-Total, X-Total-Pages,
// X-Next-Page) — whoever discards them cannot tell a full first page from a
// complete list. Everything that does not paginate keeps using do().
func (c *Client) doHeaders(ctx context.Context, method, path string, body any, out any) (http.Header, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+"/api/v4"+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("PRIVATE-TOKEN", c.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.Header, httpErr(method, path, resp.StatusCode, data)
	}
	if out != nil {
		return resp.Header, json.Unmarshal(data, out)
	}
	return resp.Header, nil
}

// httpErr turns a GitLab error status into a sentence the agent can act on.
//
// The reason it exists is one particular quirk: GitLab answers the approve
// endpoint with **401 Unauthorized** when approving is not permitted — because
// the merge request is already merged or closed, because the caller is its
// author, or because an approval rule forbids it. Raw, that reads like a
// credential problem: an agent gets "HTTP 401" and concludes its token is
// broken, reports a broken GitLab connection and stops — while in truth
// somebody simply closed the merge request in the meantime. Reported from
// production about a QA agent, and it is a plausible mistake, because 401 means
// exactly the opposite everywhere else in this API.
//
// Only the interpretation is added; the original text stays, because it is the
// evidence.
func httpErr(method, path string, status int, data []byte) error {
	hint := ""
	switch {
	case status == http.StatusUnauthorized && isMergeRequestDecision(path):
		hint = " — with this endpoint GitLab means 'not permitted', NOT a bad token:" +
			" the merge request is already merged or closed, you are its author, or an approval rule stands in the way." +
			" Check its state with get_merge_request; if it is no longer open, the work is done — close your task with done" +
			" instead of trying again"
	case status == http.StatusUnauthorized:
		hint = " — the brokered token is rejected (expired, revoked or scoped too narrowly)"
	case status == http.StatusForbidden:
		hint = " — the token is valid, but its scope does not cover this action"
	}
	return fmt.Errorf("gitlab %s %s: HTTP %d%s: %.300s", method, path, status, hint, data)
}

// isMergeRequestDecision spots the endpoints on which GitLab uses 401 as "not
// permitted" instead of "not authenticated".
func isMergeRequestDecision(path string) bool {
	if !strings.Contains(path, "/merge_requests/") {
		return false
	}
	for _, suffix := range []string{"/approve", "/unapprove", "/merge"} {
		if strings.HasSuffix(path, suffix) {
			return true
		}
	}
	return false
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
	// Assignees makes the assignment visible to the agent — playbooks such as
	// "only work on issues assigned to you" need this information.
	Assignees []struct {
		Username string `json:"username"`
	} `json:"assignees"`
	// Milestone attaches the issue to an undertaking (a release, a tender, a
	// sprint). An agent running a whole undertaking needs the title to recognise
	// what belongs to its assignment; GitLab returns null when the issue is
	// attached to no milestone.
	Milestone *struct {
		Title   string `json:"title"`
		DueDate string `json:"due_date"`
		State   string `json:"state"`
	} `json:"milestone"`
	// Author is the reporter: whoever wrote the need down is the natural
	// recipient of the merge request that settles it.
	Author struct {
		Username string `json:"username"`
	} `json:"author"`
	// References.Full is the full reference "group/project#iid" — the project
	// path for the intake filter can be derived from it.
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
	// BodyTruncated/BodyChars are not filled by GitLab but by the action layer
	// when it shortens an over-long comment for the agent (see cutBody). omitempty,
	// so they only appear where they were actually set.
	BodyTruncated bool `json:"body_truncated,omitempty"`
	BodyChars     int  `json:"body_chars,omitempty"`
}

// ListProjects — GET /projects?membership=true: all projects in which the bot
// user is a member. The entry point for agents that do not yet know their
// project_ids.
func (c *Client) ListProjects(ctx context.Context) ([]Project, error) {
	var out []Project
	err := c.do(ctx, http.MethodGet,
		"/projects?membership=true&simple=true&archived=false&order_by=last_activity_at&per_page=100", nil, &out)
	return out, err
}

// ListIssues finds issues — with projectID through GET /projects/{id}/issues,
// without one (projectID=0) through the global GET /issues with scope=all: all
// issues the token may see. state is "opened" (the default), "closed" or "all";
// labels (comma-separated), search and milestone (the milestone TITLE as it
// stands in GitLab) narrow it optionally. assigned=true returns only issues
// assigned to the token's bot user (scope=assigned_to_me) — for agents that,
// according to their playbook, only work on their own assignment.
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

// CreateIssue — POST /projects/{id}/issues: files a new issue (ticket). For the
// intake of bug reports that do NOT come from GitLab itself (reported by email,
// say) — the agent turns the report into a traceable ticket. title is
// mandatory; description (Markdown), labels (comma-separated) and assignee (a
// user id, 0 = no assignment) are optional.
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

const (
	// notesWindowDefault is what an agent gets without asking for a size: enough
	// for a normal ticket history, small enough for one that has been running for
	// a year.
	notesWindowDefault = 20
	// notesWindowMax is GitLab's limit for per_page — anything larger is silently
	// capped to 100 by the API, so we cap it ourselves and stay honest.
	notesWindowMax = 100
	// notesWindowInternal is the window of the internal readers (heartbeat check,
	// duplicate protection). They are allowed to be generous: their answer never
	// reaches an agent's context and therefore costs no tokens — only one request.
	notesWindowInternal = 100
)

// notesLimit clamps a requested window size to what GitLab actually serves.
// Not a detail: whoever asks for 500 gets 100 from GitLab and would otherwise
// have the 500 in their answer text — and describe a window that does not exist.
func notesLimit(limit int) int {
	switch {
	case limit <= 0:
		return notesWindowDefault
	case limit > notesWindowMax:
		return notesWindowMax
	}
	return limit
}

// NotesPage is ONE window of a comment thread plus what GitLab said about the
// whole of it. It exists because a ticket's history grows without bound: an
// issue that takes a daily report for a year carries hundreds of comments, and
// whoever loads them all pushes them into the agent's context on every call.
//
// The window sits at the NEW end — that is where the current state of a thread
// is. Within the window Notes runs chronologically ascending, exactly as before,
// so that everything reading the thread from behind (lastHumanNoteIsMine,
// threadSig) stays valid.
type NotesPage struct {
	Notes   []Note
	Page    int
	Total   int  // from X-Total; -1 when GitLab did not state it
	HasMore bool // is there anything older behind this window?
}

// notesWindow fetches one window of a notes collection: sort=desc makes GitLab
// deliver the NEWEST first, per_page determines the size, page counts backwards
// into the history (page=1 the newest window, page=2 the one before it).
// Afterwards the slice is turned round so the caller gets it chronologically.
//
// The size of the whole comes from the pagination headers. GitLab omits X-Total
// for very large collections, and some proxies swallow the headers — hence the
// fallback: a completely full page means there is probably more behind it.
func (c *Client) notesWindow(ctx context.Context, base string, limit, page int) (NotesPage, error) {
	limit = notesLimit(limit)
	if page <= 0 {
		page = 1
	}
	q := url.Values{}
	q.Set("sort", "desc")
	q.Set("order_by", "created_at")
	q.Set("per_page", fmt.Sprint(limit))
	q.Set("page", fmt.Sprint(page))
	var out []Note
	hdr, err := c.doHeaders(ctx, http.MethodGet, base+"/notes?"+q.Encode(), nil, &out)
	if err != nil {
		return NotesPage{}, err
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	// From vague to precise: whoever comes later overrules. The starting guess
	// only has the page size; the counts state it exactly; X-Next-Page is the
	// direct answer. NOT judged by presence but by the numbers — a proxy that
	// swallows X-Next-Page and lets X-Total-Pages through would otherwise turn
	// page 1 of 7 into a complete thread, which is exactly the silent truncation
	// this window is meant to abolish.
	p := NotesPage{Notes: out, Page: page, Total: -1, HasMore: len(out) == limit}
	if hdr != nil {
		if n, err := strconv.Atoi(hdr.Get("X-Total")); err == nil {
			p.Total, p.HasMore = n, page*limit < n
		}
		if n, err := strconv.Atoi(hdr.Get("X-Total-Pages")); err == nil {
			p.HasMore = page < n
		}
		if strings.TrimSpace(hdr.Get("X-Next-Page")) != "" {
			p.HasMore = true
		}
	}
	return p, nil
}

// ListNotes — GET /projects/{id}/issues/{iid}/notes: the newest limit comments
// of an issue (page counts backwards into the history), chronological within
// the window.
func (c *Client) ListNotes(ctx context.Context, projectID, issueIID, limit, page int) (NotesPage, error) {
	return c.notesWindow(ctx, fmt.Sprintf("/projects/%d/issues/%d", projectID, issueIID), limit, page)
}

// GetIssueNote — GET /projects/{id}/issues/{iid}/notes/{note_id}: a single
// comment in full. The way back for one that the action layer shortened.
func (c *Client) GetIssueNote(ctx context.Context, projectID, issueIID, noteID int) (Note, error) {
	var out Note
	err := c.do(ctx, http.MethodGet,
		fmt.Sprintf("/projects/%d/issues/%d/notes/%d", projectID, issueIID, noteID), nil, &out)
	return out, err
}

// Comment — POST /projects/{id}/issues/{iid}/notes. internal=true is an
// internal note (visible only to project members from reporter upwards),
// internal=false a public comment — visible to external reporters too.
func (c *Client) Comment(ctx context.Context, projectID, issueIID int, body string, internal bool) (Note, error) {
	var out Note
	err := c.do(ctx, http.MethodPost, fmt.Sprintf("/projects/%d/issues/%d/notes", projectID, issueIID),
		map[string]any{"body": body, "internal": internal}, &out)
	return out, err
}

// DownloadArchive streams the repository archive (tar.gz) —
// GET /projects/{id}/repository/archive.tar.gz, optionally narrowed to a ref
// (branch, tag, SHA) and a subdirectory (subPath) — the latter makes large
// repos manageable through a partial checkout. Deliberately not routed through
// do(): the body is binary and can be large; the caller closes the reader.
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
	// A separate HTTP client without the tight API timeout — downloading a large
	// repo may take longer; the limit is set by the call's context.
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

// uploadRefRE splits a GitLab upload reference into <secret>/<filename>.
// GitLab embeds images attached in Markdown as /uploads/<32-hex>/<name> —
// project-relative. The match works on the bare path (/uploads/…), on the full
// web URL (…/group/project/uploads/…) and on the already split form
// <secret>/<name>.
var uploadRefRE = regexp.MustCompile(`([0-9a-fA-F]{32})/([^/?#\s]+)$`)

// maxUploadBytes caps a single downloaded upload — a screenshot is small, an
// accidentally linked huge asset should not flood the sandbox.
const maxUploadBytes = 25 << 20 // 25 MB

// DownloadUpload downloads an upload attached to an issue/MR (a screenshot) in
// brokered fashion — GET /projects/{id}/uploads/{secret}/{filename}. Like
// DownloadArchive it goes past the JSON do() (the body is binary), the token
// stays in the daemon. ref is the reference from the Markdown: the bare path
// "/uploads/<secret>/<file>", the full web URL or already "<secret>/<file>".
// Returns: the file name, the content type and the reader (to be closed by the
// caller).
func (c *Client) DownloadUpload(ctx context.Context, projectID int, ref string) (filename, contentType string, body io.ReadCloser, err error) {
	m := uploadRefRE.FindStringSubmatch(strings.TrimSpace(ref))
	if m == nil {
		return "", "", nil, fmt.Errorf("no valid upload reference in %q — /uploads/<secret>/<file> from the issue description is expected", ref)
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
			hint = " (the upload endpoint needs GitLab >= 16.6 respectively project access)"
		}
		return "", "", nil, fmt.Errorf("gitlab GET %s: HTTP %d%s: %.200s", path, resp.StatusCode, hint, data)
	}
	return name, resp.Header.Get("Content-Type"), resp.Body, nil
}

// UploadResult is the answer of POST /projects/{id}/uploads: the Markdown
// reference one can embed in a comment plus the relative URL.
type UploadResult struct {
	Alt      string `json:"alt"`
	URL      string `json:"url"`
	FullPath string `json:"full_path"`
	Markdown string `json:"markdown"`
}

// UploadFile uploads a file (a screenshot) to a project — POST /projects/{id}/
// uploads, multipart. Like DownloadUpload it goes past the JSON do() (the body
// is multipart), the token stays in the daemon. The returned "markdown"
// (![alt](/uploads/<secret>/<file>)) is embedded in a comment_mr body.
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
		return UploadResult{}, fmt.Errorf("upload answer: %w", err)
	}
	return out, nil
}

// TreeEntry is an entry of the repository tree (a file or a directory).
type TreeEntry struct {
	Name string `json:"name"`
	Type string `json:"type"` // "blob" (file) | "tree" (directory)
	Path string `json:"path"`
}

// ListTree — GET /projects/{id}/repository/tree: leafing through the repository
// tree without downloading anything. For repos too large for a checkout:
// navigate first, then read files selectively.
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

// maxReadFileBytes caps a single file read via read_file.
const maxReadFileBytes = 512 << 10 // 512 KB

// ReadFile — GET /projects/{id}/repository/files/{path}/raw: reading a single
// file without a checkout. The file path is URL-encoded completely (including
// "/"), as the GitLab API demands.
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

// Commit is an entry of the commit history — enough context to recognise
// whether a reported bug has been fixed in the meantime (title, author, date).
type Commit struct {
	ID         string `json:"id"`
	ShortID    string `json:"short_id"`
	Title      string `json:"title"`
	AuthorName string `json:"author_name"`
	CreatedAt  string `json:"created_at"`
	WebURL     string `json:"web_url"`
}

// ListCommits — GET /projects/{id}/repository/commits: the history of a ref,
// optionally narrowed to a path (a file/directory) and a start date (since,
// ISO 8601). With it an agent checks whether there are commits since an issue
// was created that already fix the reported fault.
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

// maxDiffBytesPerFile caps a single file's diff in GetCommitDiff.
const maxDiffBytesPerFile = 16 << 10 // 16 KB

// CommitDiff is the diff of a file within a commit.
type CommitDiff struct {
	OldPath     string `json:"old_path"`
	NewPath     string `json:"new_path"`
	NewFile     bool   `json:"new_file"`
	DeletedFile bool   `json:"deleted_file"`
	Diff        string `json:"diff"`
	Truncated   bool   `json:"truncated,omitempty"`
}

// GetCommitDiff — GET /projects/{id}/repository/commits/{sha}/diff: what a
// commit actually changes. Individual file diffs are truncated to
// maxDiffBytesPerFile so that huge commits do not blow up the agent's context.
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

// MergeRequest is an entry of the MR list — enough to find open or merged fixes
// on a topic.
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
	// References.Full is the full reference "group/project!iid" — the project
	// path for the intake filter can be derived from it (as with Issue).
	References struct {
		Full string `json:"full"`
	} `json:"references"`
}

// ListMergeRequests — GET /projects/{id}/merge_requests. state is "opened",
// "merged", "closed" or "all" (default: all); search filters on title and
// description, targetBranch on the target branch.
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
// the open merge requests the token's bot user opened itself. The cheap
// pre-check for the review loop (HasWork) — without a project_id, that is
// across projects, like ListIssues(0, …). The filter runs through the token
// identity, no username is needed.
func (c *Client) ListMyOpenMergeRequests(ctx context.Context) ([]MergeRequest, error) {
	var out []MergeRequest
	err := c.do(ctx, http.MethodGet,
		"/merge_requests?scope=created_by_me&state=opened&order_by=updated_at&per_page=50", nil, &out)
	return out, err
}

// ListReviewMergeRequests — GET /merge_requests?scope=all&reviewer_username=<user>&state=opened:
// the open merge requests in which the bot user is entered as reviewer — the
// review queue of a QA/test agent, across projects (like
// ListMyOpenMergeRequests, only from the reviewer's rather than the author's
// point of view). It carries the review loop from the other side: the developer
// agent sets the QA agent as reviewer, who finds the MR through this.
//
// scope=all is what makes it work, and its absence is silent: GitLab defaults
// this endpoint to scope=created_by_me, so without it the query asks for merge
// requests the bot opened ITSELF and is also reviewer on. A QA agent opens
// none — the answer is an empty list, HTTP 200, in fifty milliseconds. The
// `nur-wenn: gitlab:review` heartbeat then reports "no work" every quarter of
// an hour and the agent sleeps through its review queue, without a single error
// anywhere.
func (c *Client) ListReviewMergeRequests(ctx context.Context, reviewerUsername string) ([]MergeRequest, error) {
	var out []MergeRequest
	err := c.do(ctx, http.MethodGet,
		"/merge_requests?scope=all&reviewer_username="+url.QueryEscape(reviewerUsername)+"&state=opened&order_by=updated_at&per_page=50", nil, &out)
	return out, err
}

// CurrentUser — GET /user: the profile of the token holder (the bot user).
// Needed to tell one's own last comment in an MR thread apart from someone
// else's review feedback.
func (c *Client) CurrentUser(ctx context.Context) (User, error) {
	var u User
	err := c.do(ctx, http.MethodGet, "/user", nil, &u)
	return u, err
}

// MergeRequestDetail is the full view of a single MR — including the review
// state (merge status, conflicts) and the CI result (head_pipeline), so that an
// agent can look after its own MR like a developer.
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
	// DetailedMergeStatus, e.g. "mergeable", "ci_still_running", "conflict" —
	// GitLab's own summary of why an MR is (not) mergeable.
	DetailedMergeStatus string `json:"detailed_merge_status"`
	// BlockingDiscussionsResolved: are all threads that block the merge
	// resolved? False means an open review discussion — the QA agent must not
	// merge over that.
	BlockingDiscussionsResolved bool `json:"blocking_discussions_resolved"`
	// SHA is the head of the diff at read time. Passed back on the merge, it is
	// the guarantee that exactly the reviewed state gets merged: if a commit has
	// arrived in the meantime, GitLab refuses with 409.
	SHA    string `json:"sha"`
	Author struct {
		Username string `json:"username"`
	} `json:"author"`
	Reviewers []struct {
		Username string `json:"username"`
	} `json:"reviewers"`
	HeadPipeline *Pipeline `json:"head_pipeline"`
}

// MRApprovals is the approval state of an MR — GET
// /merge_requests/{iid}/approvals. approved_by carries the users who have
// approved; that is how an agent checks whether ITS OWN approval is on record
// before it merges.
type MRApprovals struct {
	ApprovalsRequired int  `json:"approvals_required"`
	ApprovalsLeft     int  `json:"approvals_left"`
	UserHasApproved   bool `json:"user_has_approved"`
	ApprovedBy        []struct {
		User struct {
			Username string `json:"username"`
		} `json:"user"`
	} `json:"approved_by"`
}

// Pipeline is the CI run of a ref/MR — status "success", "failed", "running"
// etc.
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

// ListMRNotes — GET /projects/{id}/merge_requests/{iid}/notes: an MR's
// discussion state including review comments on the diff, windowed like
// ListNotes.
func (c *Client) ListMRNotes(ctx context.Context, projectID, mrIID, limit, page int) (NotesPage, error) {
	return c.notesWindow(ctx, fmt.Sprintf("/projects/%d/merge_requests/%d", projectID, mrIID), limit, page)
}

// GetMRNote — GET /projects/{id}/merge_requests/{iid}/notes/{note_id}.
func (c *Client) GetMRNote(ctx context.Context, projectID, mrIID, noteID int) (Note, error) {
	var out Note
	err := c.do(ctx, http.MethodGet,
		fmt.Sprintf("/projects/%d/merge_requests/%d/notes/%d", projectID, mrIID, noteID), nil, &out)
	return out, err
}

// CommentMR — POST /projects/{id}/merge_requests/{iid}/notes: the agent's
// answer in the review dialogue of its MR.
func (c *Client) CommentMR(ctx context.Context, projectID, mrIID int, body string) (Note, error) {
	var out Note
	err := c.do(ctx, http.MethodPost,
		fmt.Sprintf("/projects/%d/merge_requests/%d/notes", projectID, mrIID),
		map[string]any{"body": body}, &out)
	return out, err
}

// SetMRReviewer — PUT /projects/{id}/merge_requests/{iid} with reviewer_ids:
// enters the reviewer(s) of an existing MR. That is how the developer agent
// hands its MR over to the QA agent deliberately (or hands it back), without
// the assignment to the manager getting lost.
func (c *Client) SetMRReviewer(ctx context.Context, projectID, mrIID int, reviewerIDs []int) (MergeRequestDetail, error) {
	var out MergeRequestDetail
	err := c.do(ctx, http.MethodPut, fmt.Sprintf("/projects/%d/merge_requests/%d", projectID, mrIID),
		map[string]any{"reviewer_ids": reviewerIDs}, &out)
	return out, err
}

// ApproveMR — POST /projects/{id}/merge_requests/{iid}/approve: a reviewer's
// formal approval. The QA agent uses it as the green signal to the manager:
// "feature tested, all green" — the merging itself stays with the human. If
// approval is not enabled in the project, GitLab reports an error; the
// confirming comment_mr then suffices.
func (c *Client) ApproveMR(ctx context.Context, projectID, mrIID int) error {
	return c.do(ctx, http.MethodPost,
		fmt.Sprintf("/projects/%d/merge_requests/%d/approve", projectID, mrIID), nil, nil)
}

// GetMRApprovals — GET /projects/{id}/merge_requests/{iid}/approvals: who has
// approved. The merge gate reads it to establish that the agent's own approval
// is on record — merging something one has not oneself accepted is exactly what
// the gate is meant to prevent.
func (c *Client) GetMRApprovals(ctx context.Context, projectID, mrIID int) (MRApprovals, error) {
	var out MRApprovals
	err := c.do(ctx, http.MethodGet,
		fmt.Sprintf("/projects/%d/merge_requests/%d/approvals", projectID, mrIID), nil, &out)
	return out, err
}

// MergeMR — PUT /projects/{id}/merge_requests/{iid}/merge. sha pins the state
// to be merged: GitLab merges only if the head is still that commit, so a
// commit pushed after the review can never be merged unseen. Passing an empty
// sha is deliberately not supported — the gate in the plugin always has the
// state it checked.
func (c *Client) MergeMR(ctx context.Context, projectID, mrIID int, sha string, removeSourceBranch bool) (MergeRequestDetail, error) {
	var out MergeRequestDetail
	body := map[string]any{
		"sha":                         sha,
		"should_remove_source_branch": removeSourceBranch,
	}
	err := c.do(ctx, http.MethodPut,
		fmt.Sprintf("/projects/%d/merge_requests/%d/merge", projectID, mrIID), body, &out)
	return out, err
}

// ListPipelines — GET /projects/{id}/pipelines, optionally narrowed to a ref:
// did the CI on my branch pass, before I hand the MR over for review
// respectively after reworking it?
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

// Job is a CI job of a pipeline run — enough to find the failed job and pull
// its log.
type Job struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	Stage        string `json:"stage"`
	Status       string `json:"status"`
	AllowFailure bool   `json:"allow_failure"`
	WebURL       string `json:"web_url"`
}

// ListPipelineJobs — GET /projects/{id}/pipelines/{pipeline_id}/jobs: the jobs
// of a CI run with their status. The entry point into diagnosing a red
// pipeline.
func (c *Client) ListPipelineJobs(ctx context.Context, projectID, pipelineID int) ([]Job, error) {
	var out []Job
	err := c.do(ctx, http.MethodGet,
		fmt.Sprintf("/projects/%d/pipelines/%d/jobs?per_page=100", projectID, pipelineID), nil, &out)
	return out, err
}

// maxJobLogBytes caps the returned job log; what is kept is the END — that is
// where error messages and test summaries stand.
const maxJobLogBytes = 48 << 10

// GetJobLog — GET /projects/{id}/jobs/{job_id}/trace: the log of a CI job.
// Traces can be huge; reading is capped and the end of the log is returned.
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

// RetryPipeline — POST /projects/{id}/pipelines/{pipeline_id}/retry: starts the
// failed jobs of a CI run again — for the case that the cause lay outside the
// code and has been fixed in the meantime (repo access granted afterwards, the
// runner back again).
func (c *Client) RetryPipeline(ctx context.Context, projectID, pipelineID int) (Pipeline, error) {
	var out Pipeline
	err := c.do(ctx, http.MethodPost,
		fmt.Sprintf("/projects/%d/pipelines/%d/retry", projectID, pipelineID), nil, &out)
	return out, err
}

// Branch is an entry of the branch list. Default marks the project's default
// branch.
type Branch struct {
	Name    string `json:"name"`
	Default bool   `json:"default"`
	Commit  struct {
		ShortID   string `json:"short_id"`
		CreatedAt string `json:"created_at"`
	} `json:"commit"`
}

// ListBranches — GET /projects/{id}/repository/branches, optionally with a name
// search. With it an agent finds the right ref instead of guessing branch
// names.
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

// ProjectDetail returns the project metadata the developer workflow needs —
// above all the default branch as the basis for feature branches and as the
// target of merge requests.
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

// FileExists — HEAD /projects/{id}/repository/files/{path}?ref=…: decides
// whether a commit creates the file (create) or changes it (update) — the
// commits API demands the right action and refuses the wrong one.
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

// CommitAction is an entry in the actions array of the commits API: creating,
// changing or deleting a file. Contents travel base64-encoded — that way binary
// files and special characters survive the JSON transport too.
type CommitAction struct {
	Action   string `json:"action"` // "create" | "update" | "delete"
	FilePath string `json:"file_path"`
	Content  string `json:"content,omitempty"`
	Encoding string `json:"encoding,omitempty"`
}

// CommitFiles — POST /projects/{id}/repository/commits: one commit with all
// file changes in a single API call. startBranch != "" creates the branch as a
// copy of it (the agent's push route: the token stays in the daemon, a git
// remote with credentials never exists).
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

// CreateMergeRequest — POST /projects/{id}/merge_requests: opens the MR for a
// pushed feature branch. assigneeID/reviewerID (as a rule the agent's manager)
// are optional (0 = do not set); the source branch is removed automatically
// after the merge.
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

// SetState — PUT /projects/{id}/issues/{iid} with state_event ("close"|"reopen").
func (c *Client) SetState(ctx context.Context, projectID, issueIID int, stateEvent string) error {
	if stateEvent != "close" && stateEvent != "reopen" {
		return fmt.Errorf("invalid state %q (allowed: close, reopen)", stateEvent)
	}
	return c.do(ctx, http.MethodPut, fmt.Sprintf("/projects/%d/issues/%d", projectID, issueIID),
		map[string]any{"state_event": stateEvent}, nil)
}

// SetMRState — same idea as SetState, for a merge request: PUT
// /projects/{id}/merge_requests/{iid} with state_event ("close"|"reopen").
// GitLab has no separate close endpoint for MRs, just this field on the same
// resource merge/approve already write to.
func (c *Client) SetMRState(ctx context.Context, projectID, mrIID int, stateEvent string) error {
	if stateEvent != "close" && stateEvent != "reopen" {
		return fmt.Errorf("invalid state %q (allowed: close, reopen)", stateEvent)
	}
	return c.do(ctx, http.MethodPut, fmt.Sprintf("/projects/%d/merge_requests/%d", projectID, mrIID),
		map[string]any{"state_event": stateEvent}, nil)
}

// User is the minimal profile of a GitLab user for the assignment.
type User struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name"`
	State    string `json:"state"`
}

// LookupUser — GET /users?username=…: resolves a GitLab username into the
// numeric user id (the issue API knows only assignee_ids). The API's username
// filter matches exactly but returns a list.
func (c *Client) LookupUser(ctx context.Context, username string) (User, error) {
	username = strings.TrimPrefix(strings.TrimSpace(username), "@")
	if username == "" {
		return User{}, fmt.Errorf("username missing")
	}
	var out []User
	if err := c.do(ctx, http.MethodGet, "/users?username="+url.QueryEscape(username), nil, &out); err != nil {
		return User{}, err
	}
	if len(out) == 0 {
		return User{}, fmt.Errorf("gitlab user %q not found", username)
	}
	return out[0], nil
}

// AssignIssue — PUT /projects/{id}/issues/{iid} with assignee_ids: assigns the
// issue to a person (for testing a bugfix answer, say).
func (c *Client) AssignIssue(ctx context.Context, projectID, issueIID int, userIDs []int) error {
	return c.do(ctx, http.MethodPut, fmt.Sprintf("/projects/%d/issues/%d", projectID, issueIID),
		map[string]any{"assignee_ids": userIDs}, nil)
}

// SetLabels — PUT /projects/{id}/issues/{iid} with add_labels/remove_labels:
// sets and removes labels on an EXISTING issue without touching the others.
// Exactly this partial operation is what an agent needs that maintains an
// item's working state on the board ("ready" → "in progress", say): were it to
// write the full labels list, every state change would delete the
// subject-matter labels along with it.
//
// GitLab answers with the updated issue — we return that so the agent sees the
// state reached instead of querying it again.
func (c *Client) SetLabels(ctx context.Context, projectID, issueIID int, add, remove []string) (Issue, error) {
	body := map[string]any{}
	joinedAdd, err := joinLabels(add)
	if err != nil {
		return Issue{}, fmt.Errorf("add_labels: %w", err)
	}
	joinedRemove, err := joinLabels(remove)
	if err != nil {
		return Issue{}, fmt.Errorf("remove_labels: %w", err)
	}
	if joinedAdd != "" {
		body["add_labels"] = joinedAdd
	}
	if joinedRemove != "" {
		body["remove_labels"] = joinedRemove
	}
	if len(body) == 0 {
		return Issue{}, fmt.Errorf("neither add_labels nor remove_labels given")
	}
	var out Issue
	err = c.do(ctx, http.MethodPut,
		fmt.Sprintf("/projects/%d/issues/%d", projectID, issueIID), body, &out)
	return out, err
}

// joinLabels turns the agent's label list into the comma-separated string the
// GitLab API expects. Empty entries drop out — an accidental "" in the array
// should not arrive as a label with an empty name.
//
// An entry WITH a comma, by contrast, is an error, not a silent split: because
// GitLab creates missing labels automatically when setting them, a single
// wrongly built entry ("a,b") would permanently become two project labels —
// visible only once the board has already overgrown. Better to hand the error
// back to the agent.
func joinLabels(labels []string) (string, error) {
	out := make([]string, 0, len(labels))
	for _, l := range labels {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if strings.Contains(l, ",") {
			return "", fmt.Errorf("label %q contains a comma — give every label as its own list entry", l)
		}
		out = append(out, l)
	}
	return strings.Join(out, ","), nil
}

// Escalate posts an internal note and removes the assignment (assignee_ids
// empty) so that a human takes the issue over.
func (c *Client) Escalate(ctx context.Context, projectID, issueIID int, note string) error {
	if _, err := c.Comment(ctx, projectID, issueIID, note, true); err != nil {
		return err
	}
	return c.do(ctx, http.MethodPut, fmt.Sprintf("/projects/%d/issues/%d", projectID, issueIID),
		map[string]any{"assignee_ids": []int{}}, nil)
}
