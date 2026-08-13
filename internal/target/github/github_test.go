package github

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/benjaminLedel/covey-plugin-sdk/target"
)

// newTestClient points a client at a test server. The explicit /api/v3 is the
// GitHub Enterprise Server shape — that way the paths in the handlers read
// exactly as normalizeBaseURL builds them.
func newTestClient(srv *httptest.Server) *Client {
	return NewClient(srv.URL+"/api/v3", "test-token")
}

func TestNormalizeBaseURL(t *testing.T) {
	cases := map[string]string{
		"":                               defaultBaseURL,
		"   ":                            defaultBaseURL,
		"https://api.github.com":         "https://api.github.com",
		"https://api.github.com/":        "https://api.github.com",
		"https://ghe.example.com":        "https://ghe.example.com/api/v3",
		"https://ghe.example.com/":       "https://ghe.example.com/api/v3",
		"https://ghe.example.com/api/v3": "https://ghe.example.com/api/v3",
	}
	for in, want := range cases {
		if got := normalizeBaseURL(in); got != want {
			t.Errorf("normalizeBaseURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSplitRepoRejectsTraversal(t *testing.T) {
	if _, _, err := splitRepo("acme/support"); err != nil {
		t.Fatalf("acme/support must be valid: %v", err)
	}
	for _, bad := range []string{"", "acme", "acme/sup/port", "/support", "acme/", "../etc/passwd", "acme/..", "acme/a b"} {
		if _, _, err := splitRepo(bad); err == nil {
			t.Errorf("repo %q must be refused", bad)
		}
	}
}

func TestRepoInScope(t *testing.T) {
	t.Setenv("COVEY_GITHUB_INTAKE_REPOS", "")
	if !repoInScope("anything/at-all") {
		t.Fatal("an empty allowlist must let everything through")
	}
	t.Setenv("COVEY_GITHUB_INTAKE_REPOS", " Acme/Support , other/* ")
	if !repoInScope("acme/support") {
		t.Error("the exact name must match case-insensitively")
	}
	if !repoInScope("other/anything") {
		t.Error("owner/* must cover the whole owner")
	}
	if repoInScope("foreign/repo") {
		t.Error("a repository outside the list must stay out")
	}
}

// TestListIssuesFiltersPullRequests: GitHub delivers pull requests through the
// issue endpoints too. An agent that got them back here would work on the same
// item twice — once as an issue, once as a PR.
func TestListIssuesFiltersPullRequests(t *testing.T) {
	t.Setenv("COVEY_GITHUB_INTAKE_REPOS", "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/repos/acme/support/issues" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("state"); got != "open" {
			t.Errorf("state = %q, want open (the GitLab spelling \"opened\" must be translated)", got)
		}
		json.NewEncoder(w).Encode([]map[string]any{
			{"number": 1, "title": "Login broken", "body": "cannot log in"},
			{"number": 2, "title": "a pull request", "pull_request": map[string]any{"html_url": "x"}},
			{"number": 3, "title": "Export fails", "body": "csv"},
		})
	}))
	defer srv.Close()

	issues, err := newTestClient(srv).ListIssues(context.Background(), "acme/support", "opened", "", "", "", false)
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(issues) != 2 {
		t.Fatalf("want 2 issues without the PR, got %d: %+v", len(issues), issues)
	}
	if issues[0].Repo != "acme/support" {
		t.Errorf("the repo must be filled in for the repository endpoint, got %q", issues[0].Repo)
	}
}

func TestListIssuesAppliesSearchAndMilestone(t *testing.T) {
	t.Setenv("COVEY_GITHUB_INTAKE_REPOS", "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{
			{"number": 1, "title": "Login broken", "milestone": map[string]any{"title": "Release 2"}},
			{"number": 2, "title": "Export fails", "milestone": map[string]any{"title": "Release 2"}},
			{"number": 3, "title": "Login slow", "milestone": map[string]any{"title": "Release 3"}},
		})
	}))
	defer srv.Close()
	c := newTestClient(srv)
	ctx := context.Background()

	got, err := c.ListIssues(ctx, "acme/support", "open", "", "login", "", false)
	if err != nil || len(got) != 2 {
		t.Fatalf("search must match title substrings: %v %+v", err, got)
	}
	got, err = c.ListIssues(ctx, "acme/support", "open", "", "", "Release 2", false)
	if err != nil || len(got) != 2 {
		t.Fatalf("milestone must filter by title: %v %+v", err, got)
	}
	got, err = c.ListIssues(ctx, "acme/support", "open", "", "login", "Release 3", false)
	if err != nil || len(got) != 1 || got[0].Number != 3 {
		t.Fatalf("both filters must apply together: %v %+v", err, got)
	}
}

// TestUnknownStateIsAnError: a typo must not quietly widen the query to "all".
// The agent would get closed issues back, comment on them, and nothing in the
// answer would say that it asked for something else.
func TestUnknownStateIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("a faulty state must not reach the API")
	}))
	defer srv.Close()
	c := newTestClient(srv)
	ctx := context.Background()

	if _, err := c.ListIssues(ctx, "acme/support", "opne", "", "", "", false); err == nil {
		t.Error("list_issues must refuse an unknown state")
	}
	if _, err := c.ListPulls(ctx, "acme/support", "offen", "", ""); err == nil {
		t.Error("list_pull_requests must refuse an unknown state")
	}
}

// TestListCommentsFetchesTheNewest: the client does not page. On a long thread
// the natural order would hand back the opening exchange and cut off the end —
// but "who wrote last?" is exactly what every decision here hangs on.
func TestListCommentsFetchesTheNewest(t *testing.T) {
	var gotDirection string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotDirection = r.URL.Query().Get("direction")
		// The server answers newest first, as asked.
		json.NewEncoder(w).Encode([]map[string]any{
			{"id": 3, "body": "third", "created_at": "2026-01-03T10:00:00Z"},
			{"id": 2, "body": "second", "created_at": "2026-01-02T10:00:00Z"},
			{"id": 1, "body": "first", "created_at": "2026-01-01T10:00:00Z"},
		})
	}))
	defer srv.Close()

	cs, err := newTestClient(srv).ListComments(context.Background(), "acme/support", 5)
	if err != nil {
		t.Fatalf("ListComments: %v", err)
	}
	if gotDirection != "desc" {
		t.Errorf("the newest page must be requested: direction=%q", gotDirection)
	}
	if len(cs) != 3 || cs[0].Body != "first" || cs[2].Body != "third" {
		t.Fatalf("the result must be handed back chronologically: %+v", cs)
	}
}

// TestListPullRequestsMerged: "merged" is not a state to GitHub but a property
// of a closed PR. The filter must run on merged_at from the LIST answer — one
// request, not one per hit.
func TestListPullRequestsMerged(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if got := r.URL.Query().Get("state"); got != "closed" {
			t.Errorf("state = %q, want closed", got)
		}
		json.NewEncoder(w).Encode([]map[string]any{
			{"number": 8, "title": "merged one", "merged_at": "2026-01-01T10:00:00Z"},
			{"number": 9, "title": "abandoned one", "merged_at": nil},
		})
	}))
	defer srv.Close()

	res, err := actions["list_pull_requests"](context.Background(), newTestClient(srv), actionParams{
		Repo: "acme/support", State: "merged",
	})
	if err != nil {
		t.Fatalf("list_pull_requests: %v", err)
	}
	prs, _ := res.([]PullRequest)
	if len(prs) != 1 || prs[0].Number != 8 || !prs[0].Merged {
		t.Fatalf("only the merged PR may come back: %+v", prs)
	}
	if requests != 1 {
		t.Errorf("the filter must not cost one request per hit: %d requests", requests)
	}
}

// TestSetLabelsIsPartial: a state change must not take the subject-matter
// labels with it, so the action adds and removes instead of overwriting.
func TestSetLabelsIsPartial(t *testing.T) {
	var deleted []string
	var posted []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/v3/repos/acme/support/issues/7/labels/"):
			deleted = append(deleted, strings.TrimPrefix(r.URL.Path, "/api/v3/repos/acme/support/issues/7/labels/"))
			w.Write([]byte(`[]`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v3/repos/acme/support/issues/7/labels":
			var body struct {
				Labels []string `json:"labels"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			posted = body.Labels
			json.NewEncoder(w).Encode([]Label{{Name: "bug"}, {Name: "in-progress"}})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	labels, err := newTestClient(srv).SetLabels(context.Background(), "acme/support", 7,
		[]string{"in-progress"}, []string{"triage"})
	if err != nil {
		t.Fatalf("SetLabels: %v", err)
	}
	if len(deleted) != 1 || deleted[0] != "triage" {
		t.Errorf("remove_labels must delete individually: %v", deleted)
	}
	if len(posted) != 1 || posted[0] != "in-progress" {
		t.Errorf("add_labels must be added: %v", posted)
	}
	if strings.Join(labels, ",") != "bug,in-progress" {
		t.Errorf("the label state reached must come back: %v", labels)
	}
}

func TestSetLabelsRefusesCommaInEntry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("a faulty entry must not reach the API")
	}))
	defer srv.Close()
	if _, err := newTestClient(srv).SetLabels(context.Background(), "acme/support", 7,
		[]string{"bug,ui"}, nil); err == nil {
		t.Fatal("a label with a comma must be refused")
	}
}

// TestEscalateHandsBack: the escalation comments AND removes the bot's own
// assignment — otherwise the issue stays with an agent that has just said it
// cannot get any further.
func TestEscalateHandsBack(t *testing.T) {
	var commented string
	var unassigned []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v3/repos/acme/support/issues/7/comments" && r.Method == http.MethodPost:
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			commented = body["body"]
			w.Write([]byte(`{"id":1}`))
		case r.URL.Path == "/api/v3/user":
			json.NewEncoder(w).Encode(User{Login: "covey-bot"})
		case r.URL.Path == "/api/v3/repos/acme/support/issues/7/assignees" && r.Method == http.MethodDelete:
			var body struct {
				Assignees []string `json:"assignees"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			unassigned = body.Assignees
			w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	if err := newTestClient(srv).Escalate(context.Background(), "acme/support", 7, "Please take over"); err != nil {
		t.Fatalf("Escalate: %v", err)
	}
	if commented != "Please take over" {
		t.Errorf("the note must be posted: %q", commented)
	}
	if len(unassigned) != 1 || unassigned[0] != "covey-bot" {
		t.Errorf("the bot must remove ITS OWN assignment: %v", unassigned)
	}
}

// TestListPullCommentsMerges: GitHub keeps three kinds of contribution apart —
// comments, reviews and comments on lines of the diff. A review that only
// carries a verdict is feedback the agent must not miss, so all three land in
// one chronological list.
func TestListPullCommentsMerges(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/repos/acme/support/issues/9/comments":
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": 1, "body": "thanks", "created_at": "2026-01-01T10:00:00Z"},
			})
		case "/api/v3/repos/acme/support/pulls/9/reviews":
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": 2, "state": "CHANGES_REQUESTED", "submitted_at": "2026-01-01T12:00:00Z"},
				{"id": 99, "state": "PENDING", "body": ""},
			})
		case "/api/v3/repos/acme/support/pulls/9/comments":
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": 3, "body": "nil check missing", "path": "main.go", "created_at": "2026-01-01T11:00:00Z"},
			})
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	defer srv.Close()

	cs, err := newTestClient(srv).ListPullComments(context.Background(), "acme/support", 9)
	if err != nil {
		t.Fatalf("ListPullComments: %v", err)
	}
	if len(cs) != 3 {
		t.Fatalf("want 3 entries without the PENDING shell, got %d: %+v", len(cs), cs)
	}
	wantKinds := []string{"comment", "review_comment", "review"}
	for i, want := range wantKinds {
		if cs[i].Kind != want {
			t.Errorf("entry %d: kind = %q, want %q (chronological order)", i, cs[i].Kind, want)
		}
	}
	if cs[2].CreatedAt != "2026-01-01T12:00:00Z" {
		t.Errorf("a review's submitted_at must become created_at: %+v", cs[2])
	}
}

// TestCommitPushesOneCommit walks the whole Git Data route: blobs, one tree,
// one commit, one ref move. The point is that a change across several files
// arrives as ONE commit and not as one per file.
func TestCommitPushesOneCommit(t *testing.T) {
	workdir := t.TempDir()
	checkout := filepath.Join(workdir, "repos", "acme-support-main")
	if err := os.MkdirAll(filepath.Join(checkout, "internal"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(checkout, "main.go"), []byte("package main"), 0o644)
	os.WriteFile(filepath.Join(checkout, "internal", "a.go"), []byte("package internal"), 0o644)

	var blobs int
	var treeEntries []map[string]any
	var commitBody, refBody map[string]any
	var refMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v3/repos/acme/support":
			json.NewEncoder(w).Encode(Repo{FullName: "acme/support", DefaultBranch: "main"})
		case r.URL.Path == "/api/v3/repos/acme/support/git/ref/heads/fix/login":
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"message":"Not Found"}`))
		case r.URL.Path == "/api/v3/repos/acme/support/git/ref/heads/main":
			w.Write([]byte(`{"object":{"sha":"basecommit"}}`))
		case r.URL.Path == "/api/v3/repos/acme/support/git/commits/basecommit":
			w.Write([]byte(`{"tree":{"sha":"basetree"}}`))
		case r.URL.Path == "/api/v3/repos/acme/support/git/blobs":
			blobs++
			fmt.Fprintf(w, `{"sha":"blob%d"}`, blobs)
		case r.URL.Path == "/api/v3/repos/acme/support/git/trees":
			var body struct {
				BaseTree string           `json:"base_tree"`
				Tree     []map[string]any `json:"tree"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			if body.BaseTree != "basetree" {
				t.Errorf("base_tree = %q, want basetree", body.BaseTree)
			}
			treeEntries = body.Tree
			w.Write([]byte(`{"sha":"newtree"}`))
		case r.URL.Path == "/api/v3/repos/acme/support/git/commits":
			json.NewDecoder(r.Body).Decode(&commitBody)
			w.Write([]byte(`{"sha":"newcommit"}`))
		case r.URL.Path == "/api/v3/repos/acme/support/git/refs":
			refMethod = r.Method
			json.NewDecoder(r.Body).Decode(&refBody)
			w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	res, err := CommitFromCheckout(context.Background(), newTestClient(srv), "acme/support",
		"fix/login", "", "fix the login", checkout,
		[]string{"main.go", "internal/a.go"}, []string{"old.go"}, workdir)
	if err != nil {
		t.Fatalf("CommitFromCheckout: %v", err)
	}
	if blobs != 2 {
		t.Errorf("want one blob per changed file, got %d", blobs)
	}
	if len(treeEntries) != 3 {
		t.Fatalf("want 3 tree entries (2 changed + 1 deleted), got %d: %+v", len(treeEntries), treeEntries)
	}
	// The deletion has to be an EXPLICIT null — an omitted sha would mean
	// "unchanged" and the file would survive the commit.
	del := treeEntries[2]
	if del["path"] != "old.go" {
		t.Fatalf("the deletion must come last: %+v", del)
	}
	if sha, ok := del["sha"]; !ok || sha != nil {
		t.Errorf("a deletion must carry sha:null, got %#v (present=%t)", sha, ok)
	}
	if commitBody["tree"] != "newtree" {
		t.Errorf("the commit must hang off the new tree: %+v", commitBody)
	}
	if parents, _ := commitBody["parents"].([]any); len(parents) != 1 || parents[0] != "basecommit" {
		t.Errorf("the commit must hang off the start branch: %+v", commitBody["parents"])
	}
	if refMethod != http.MethodPost || refBody["ref"] != "refs/heads/fix/login" {
		t.Errorf("a new branch must be created via POST /git/refs: %s %+v", refMethod, refBody)
	}
	if !res.BranchCreated || res.Branch != "fix/login" {
		t.Errorf("the result must report the branch created: %+v", res)
	}
}

// TestCommitRefusesDefaultBranch: the route into the main branch leads
// exclusively through a pull request — fail-closed, not by prompt discipline.
func TestCommitRefusesDefaultBranch(t *testing.T) {
	workdir := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/git/") {
			t.Errorf("nothing may be written: %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(Repo{FullName: "acme/support", DefaultBranch: "main"})
	}))
	defer srv.Close()

	_, err := CommitFromCheckout(context.Background(), newTestClient(srv), "acme/support",
		"main", "", "straight in", workdir, []string{"main.go"}, nil, workdir)
	if err == nil || !strings.Contains(err.Error(), "default branch") {
		t.Fatalf("a commit onto the default branch must be refused, got %v", err)
	}
}

func TestCommitRefusesPathsOutsideTheSandbox(t *testing.T) {
	workdir := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(Repo{FullName: "acme/support", DefaultBranch: "main"})
	}))
	defer srv.Close()

	if _, err := CommitFromCheckout(context.Background(), newTestClient(srv), "acme/support",
		"fix/x", "", "msg", "/etc", []string{"passwd"}, nil, workdir); err == nil {
		t.Fatal("a checkout_path outside the sandbox must be refused")
	}
	if _, err := repoRelPath("../../etc/passwd"); err == nil {
		t.Fatal("a file path with traversal must be refused")
	}
	if _, err := repoRelPath("/etc/passwd"); err == nil {
		t.Fatal("an absolute file path must be refused")
	}
}

// tarGz builds a repository archive in GitHub's shape: everything under one
// top-level directory <owner>-<repo>-<sha>.
func tarGz(t *testing.T, top string, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		hdr := &tar.Header{Name: top + "/" + name, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

// TestCheckoutStripsTopLevelAndKeepsCaches: the archive's top level carries the
// SHA and changes with every commit. Stripping it is what makes the destination
// directory stable — and only a stable directory lets node_modules & co. carry
// over between runs.
func TestCheckoutStripsTopLevelAndKeepsCaches(t *testing.T) {
	workdir := t.TempDir()
	archive := tarGz(t, "acme-support-abc123", map[string]string{
		"README.md":     "# support",
		"cmd/main.go":   "package main",
		"internal/a.go": "package internal",
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/repos/acme/support/tarball/main" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Write(archive)
	}))
	defer srv.Close()

	ctx := context.Background()
	res, err := Checkout(ctx, newTestClient(srv), "acme/support", "main", workdir)
	if err != nil {
		t.Fatalf("Checkout: %v", err)
	}
	if res.Files != 3 {
		t.Errorf("files = %d, want 3", res.Files)
	}
	if _, err := os.Stat(filepath.Join(res.Path, "README.md")); err != nil {
		t.Errorf("the top-level directory must be stripped: %v", err)
	}
	if _, err := os.Stat(filepath.Join(res.Path, "acme-support-abc123")); err == nil {
		t.Error("the SHA-bearing shell directory must not survive")
	}

	// A dependency cache and a stale source file: the second checkout must keep
	// the one and replace the other.
	os.MkdirAll(filepath.Join(res.Path, "node_modules", "left-pad"), 0o755)
	os.WriteFile(filepath.Join(res.Path, "node_modules", "left-pad", "index.js"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(res.Path, "stale.txt"), []byte("gone"), 0o644)

	res2, err := Checkout(ctx, newTestClient(srv), "acme/support", "main", workdir)
	if err != nil {
		t.Fatalf("second Checkout: %v", err)
	}
	if res2.Path != res.Path {
		t.Errorf("the destination directory must be stable across runs: %q vs %q", res.Path, res2.Path)
	}
	if _, err := os.Stat(filepath.Join(res.Path, "node_modules", "left-pad", "index.js")); err != nil {
		t.Errorf("the dependency cache must survive: %v", err)
	}
	if _, err := os.Stat(filepath.Join(res.Path, "stale.txt")); err == nil {
		t.Error("old source files must be replaced")
	}
}

func TestCheckoutRefusesTraversalInArchive(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := "pwned"
	tw.WriteHeader(&tar.Header{Name: "../../escape.txt", Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg})
	tw.Write([]byte(body))
	tw.Close()
	gz.Close()

	if _, err := extractTarGzInto(bytes.NewReader(buf.Bytes()), t.TempDir()); err == nil {
		t.Fatal("an archive entry pointing outside must be refused (zip slip)")
	}
}

func TestCheckoutRespectsSizeLimit(t *testing.T) {
	t.Setenv("COVEY_GITHUB_CHECKOUT_MAX_MB", "1")
	archive := tarGz(t, "acme-support-abc", map[string]string{
		"big.bin": strings.Repeat("x", 2<<20),
	})
	if _, err := extractTarGzInto(bytes.NewReader(archive), t.TempDir()); err == nil ||
		!strings.Contains(err.Error(), "larger than 1 MB") {
		t.Fatalf("the size limit must bite, got %v", err)
	}
}

// TestHasWorkSignedIssues: the gate triggers on the EDGE. An issue the bot
// answered last rests; a new answer on top of it makes it work again, and the
// signature has to change with it — otherwise the control plane suppresses the
// second wake as a repeat of the first.
func TestHasWorkSignedIssues(t *testing.T) {
	t.Setenv("COVEY_GITHUB_INTAKE_REPOS", "")
	comments := []map[string]any{
		{"id": 10, "body": "help", "created_at": "2026-01-01T10:00:00Z", "user": map[string]any{"login": "kunde"}},
		{"id": 11, "body": "on it", "created_at": "2026-01-01T11:00:00Z", "user": map[string]any{"login": "covey-bot"}},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/user":
			json.NewEncoder(w).Encode(User{Login: "covey-bot"})
		case "/api/v3/issues":
			json.NewEncoder(w).Encode([]map[string]any{{
				"number": 5, "title": "Login", "comments": len(comments),
				"repository": map[string]any{"full_name": "acme/support"},
			}})
		case "/api/v3/repos/acme/support/issues/5/comments":
			json.NewEncoder(w).Encode(comments)
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	defer srv.Close()

	cred := target.Credential{BaseURL: srv.URL + "/api/v3", Token: "t"}
	ctx := context.Background()

	has, _, err := System{}.HasWorkSigned(ctx, cred, "issues")
	if err != nil {
		t.Fatalf("HasWorkSigned: %v", err)
	}
	if has {
		t.Fatal("an issue the bot answered last must not count as work")
	}

	comments = append(comments, map[string]any{
		"id": 12, "body": "still broken", "created_at": "2026-01-01T12:00:00Z",
		"user": map[string]any{"login": "kunde"},
	})
	has, sig, err := System{}.HasWorkSigned(ctx, cred, "issues")
	if err != nil || !has {
		t.Fatalf("a fresh answer must count as work: %v %t", err, has)
	}
	if !strings.Contains(sig, "issue:acme/support#5@12") {
		t.Errorf("the signature must carry the newest comment id: %q", sig)
	}

	comments = append(comments, map[string]any{
		"id": 13, "body": "and another thing", "created_at": "2026-01-01T13:00:00Z",
		"user": map[string]any{"login": "kunde"},
	})
	_, sig2, _ := System{}.HasWorkSigned(ctx, cred, "issues")
	if sig2 == sig {
		t.Error("a further comment must change the signature, otherwise the second wake is suppressed")
	}
}

// TestHasWorkSignedPullPush: GitHub does not record a push in the conversation.
// Without the head SHA in the signature the reviewer would never learn of the
// commit that followed its feedback.
func TestHasWorkSignedPullPush(t *testing.T) {
	t.Setenv("COVEY_GITHUB_INTAKE_REPOS", "")
	headSHA := "aaaaaaaaaaaaaaaa"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/user":
			json.NewEncoder(w).Encode(User{Login: "covey-bot"})
		case "/api/v3/search/issues":
			json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{
				{"number": 9, "html_url": "https://github.com/acme/support/pull/9"},
			}})
		case "/api/v3/repos/acme/support/pulls/9":
			json.NewEncoder(w).Encode(map[string]any{
				"number": 9, "head": map[string]any{"ref": "fix/login", "sha": headSHA},
			})
		case "/api/v3/repos/acme/support/issues/9/comments":
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": 20, "created_at": "2026-01-01T10:00:00Z", "user": map[string]any{"login": "reviewer"}},
			})
		case "/api/v3/repos/acme/support/pulls/9/reviews",
			"/api/v3/repos/acme/support/pulls/9/comments":
			w.Write([]byte(`[]`))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	defer srv.Close()

	cred := target.Credential{BaseURL: srv.URL + "/api/v3", Token: "t"}
	has, sig, err := System{}.HasWorkSigned(context.Background(), cred, "pr")
	if err != nil || !has {
		t.Fatalf("a PR with foreign feedback must count as work: %v %t", err, has)
	}
	headSHA = "bbbbbbbbbbbbbbbb"
	_, sig2, _ := System{}.HasWorkSigned(context.Background(), cred, "pr")
	if sig == sig2 {
		t.Errorf("a new push must change the signature: %q", sig)
	}
}

func TestVerifyWebhookSignature(t *testing.T) {
	body := []byte(`{"repository":{"full_name":"acme/support"}}`)
	mac := hmac.New(sha256.New, []byte("s3cret"))
	mac.Write(body)
	good := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	h := http.Header{}
	h.Set("X-Hub-Signature-256", good)
	if !(System{}).VerifyWebhook("s3cret", body, h) {
		t.Error("a correct signature must be accepted")
	}
	h.Set("X-Hub-Signature-256", "sha256=deadbeef")
	if (System{}).VerifyWebhook("s3cret", body, h) {
		t.Error("a wrong signature must be refused")
	}
	h.Set("X-Hub-Signature-256", "sha1="+hex.EncodeToString(mac.Sum(nil)))
	if (System{}).VerifyWebhook("s3cret", body, h) {
		t.Error("a signature without the sha256 prefix must be refused")
	}
	if !(System{}).VerifyWebhook("", body, http.Header{}) {
		t.Error("an empty secret switches the check off (dev)")
	}
}

func TestWebhookEvents(t *testing.T) {
	t.Setenv("COVEY_GITHUB_INTAKE_REPOS", "")
	t.Setenv("COVEY_GITHUB_BOT_LOGINS", "covey-bot")
	parse := func(t *testing.T, raw string) target.WebhookEvent {
		t.Helper()
		p, err := ParseWebhook([]byte(raw))
		if err != nil {
			t.Fatalf("ParseWebhook: %v", err)
		}
		return p.Event()
	}

	t.Run("a newly opened issue becomes a task", func(t *testing.T) {
		ev := parse(t, `{"action":"opened","repository":{"full_name":"acme/support"},
			"sender":{"login":"kunde","type":"User"},
			"issue":{"number":5,"title":"Login broken","body":"cannot log in","user":{"login":"kunde"}}}`)
		if !ev.Wake || ev.CorrelateOnly {
			t.Fatalf("intake must wake and be allowed to create a task: %+v", ev)
		}
		if ev.CorrelationKey != "github:issue:acme/support#5" {
			t.Errorf("correlation key = %q", ev.CorrelationKey)
		}
		if !strings.Contains(ev.TaskBody, "issue_number=5") {
			t.Errorf("the task body must name the action-proxy parameters: %q", ev.TaskBody)
		}
	})

	t.Run("a label change is registered but wakes nobody", func(t *testing.T) {
		ev := parse(t, `{"action":"labeled","repository":{"full_name":"acme/support"},
			"sender":{"login":"kunde"},"issue":{"number":5,"title":"x"}}`)
		if ev.Wake {
			t.Errorf("only opened/reopened is intake: %+v", ev)
		}
		if ev.DedupKey == "" {
			t.Error("the delivery must still be deduplicated")
		}
	})

	t.Run("a comment only wakes whoever is waiting", func(t *testing.T) {
		ev := parse(t, `{"action":"created","repository":{"full_name":"acme/support"},
			"sender":{"login":"kunde"},"issue":{"number":5,"title":"x"},
			"comment":{"id":77,"body":"still broken","user":{"login":"kunde"}}}`)
		if !ev.Wake || !ev.CorrelateOnly {
			t.Fatalf("a comment must correlate, not create a task: %+v", ev)
		}
		if !strings.Contains(ev.ResumeInput, "still broken") {
			t.Errorf("the resume input must carry the comment: %q", ev.ResumeInput)
		}
	})

	t.Run("a comment on a PR correlates against the PR thread", func(t *testing.T) {
		ev := parse(t, `{"action":"created","repository":{"full_name":"acme/support"},
			"sender":{"login":"kunde"},
			"issue":{"number":9,"title":"x","pull_request":{"html_url":"u"}},
			"comment":{"id":78,"body":"nope","user":{"login":"kunde"}}}`)
		if ev.CorrelationKey != "github:pull:acme/support#9" {
			t.Errorf("a PR conversation must correlate against the pull key: %q", ev.CorrelationKey)
		}
	})

	t.Run("the agent's own voice wakes nothing", func(t *testing.T) {
		ev := parse(t, `{"action":"created","repository":{"full_name":"acme/support"},
			"sender":{"login":"covey-bot"},"issue":{"number":5,"title":"x"},
			"comment":{"id":79,"body":"working on it","user":{"login":"covey-bot"}}}`)
		if ev.Wake {
			t.Fatal("the bot's own comment must not wake it — that is the loop")
		}
		ev = parse(t, `{"action":"opened","repository":{"full_name":"acme/support"},
			"sender":{"login":"some-app","type":"Bot"},"issue":{"number":6,"title":"x"}}`)
		if ev.Wake {
			t.Fatal("a GitHub App identifies itself through sender.type")
		}
	})

	t.Run("a review wakes the author", func(t *testing.T) {
		ev := parse(t, `{"action":"submitted","repository":{"full_name":"acme/support"},
			"sender":{"login":"qa"},"pull_request":{"number":9,"title":"fix"},
			"review":{"id":3,"state":"CHANGES_REQUESTED","body":"nil check missing","user":{"login":"qa"}}}`)
		if !ev.Wake || !ev.CorrelateOnly {
			t.Fatalf("a review must correlate: %+v", ev)
		}
		if !strings.Contains(ev.ResumeInput, "nil check missing") {
			t.Errorf("the resume input must carry the review: %q", ev.ResumeInput)
		}
	})

	t.Run("the merge ends the wait", func(t *testing.T) {
		ev := parse(t, `{"action":"closed","repository":{"full_name":"acme/support"},
			"sender":{"login":"chef"},"pull_request":{"number":9,"title":"fix","merged":true}}`)
		if !ev.Wake || !ev.CorrelateOnly {
			t.Fatalf("a merge must correlate: %+v", ev)
		}
		if !strings.Contains(ev.ResumeInput, "merged") {
			t.Errorf("the result must be named: %q", ev.ResumeInput)
		}
	})

	t.Run("a repository outside the intake scope wakes nothing", func(t *testing.T) {
		t.Setenv("COVEY_GITHUB_INTAKE_REPOS", "acme/*")
		ev := parse(t, `{"action":"opened","repository":{"full_name":"foreign/repo"},
			"sender":{"login":"kunde"},"issue":{"number":1,"title":"x"}}`)
		if ev.Wake {
			t.Fatal("the intake filter must hold")
		}
	})

	t.Run("a payload without a repository is refused", func(t *testing.T) {
		if _, err := ParseWebhook([]byte(`{"action":"opened"}`)); err == nil {
			t.Fatal("a payload without repository.full_name must be refused")
		}
	})
}

// TestAttachmentURLAllowlist: the URL comes out of an issue body — that is text
// a stranger wrote. Without the host check the action would be a request
// forgery primitive carried out by the daemon with a valid token.
func TestAttachmentURLAllowlist(t *testing.T) {
	ok := []string{
		"https://github.com/user-attachments/assets/2f0c-abc",
		"https://objects.githubusercontent.com/x/y.png",
		"https://private-user-images.githubusercontent.com/1/2.png",
	}
	for _, u := range ok {
		if _, err := checkAttachmentURL(u); err != nil {
			t.Errorf("%q must be allowed: %v", u, err)
		}
	}
	bad := []string{
		"http://github.com/user-attachments/assets/1", // not https
		"https://github.com/acme/support/settings",    // github, but not an attachment
		"https://evil.example.com/x.png",
		"https://169.254.169.254/latest/meta-data/",
		"https://githubusercontent.com.evil.example.com/x.png",
	}
	for _, u := range bad {
		if _, err := checkAttachmentURL(u); err == nil {
			t.Errorf("%q must be refused", u)
		}
	}
}

// TestExecuteRejectsUnknownAndMissingRepo: the two mistakes an agent actually
// makes. Both have to fail loudly instead of returning an empty result.
func TestExecuteRejectsUnknownAndMissingRepo(t *testing.T) {
	ctx := context.Background()
	cred := target.Credential{Token: "t"}
	if _, err := (System{}).Execute(ctx, "does_not_exist", json.RawMessage(`{}`), cred); err == nil {
		t.Error("an unknown action must be an error")
	}
	if _, err := (System{}).Execute(ctx, "get_issue", json.RawMessage(`{"issue_number":1}`), cred); err == nil ||
		!strings.Contains(err.Error(), "repo missing") {
		t.Errorf("a missing repo must be named: %v", err)
	}
	if _, err := (System{}).Execute(ctx, "get_issue", json.RawMessage(`{"repo":"../evil","issue_number":1}`), cred); err == nil {
		t.Error("an invalid repo must be refused before the request")
	}
}

// TestCommentSuppressesDuplicate is the loop protection: an agent that posts its
// own last comment again is talking to itself, and each repetition wakes the
// next run.
func TestCommentSuppressesDuplicate(t *testing.T) {
	posted := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v3/user":
			json.NewEncoder(w).Encode(User{Login: "covey-bot"})
		case r.URL.Path == "/api/v3/repos/acme/support/issues/5/comments" && r.Method == http.MethodGet:
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": 1, "body": "Checked, nothing found.", "created_at": "2026-01-01T10:00:00Z",
					"user": map[string]any{"login": "covey-bot"}},
			})
		case r.URL.Path == "/api/v3/repos/acme/support/issues/5/comments" && r.Method == http.MethodPost:
			posted++
			w.Write([]byte(`{"id":2}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	run := func(body string) any {
		res, err := actions["comment"](context.Background(), newTestClient(srv), actionParams{
			Repo: "acme/support", IssueNumber: 5, Body: body,
		})
		if err != nil {
			t.Fatalf("comment: %v", err)
		}
		return res
	}

	res := run("Checked, nothing found.")
	m, _ := res.(map[string]any)
	if m["skipped"] != "duplicate" {
		t.Errorf("an identical repetition must be suppressed: %+v", res)
	}
	if posted != 0 {
		t.Error("nothing may be posted for a duplicate")
	}
	run("Found it: internal/auth.go:42 returns nil.")
	if posted != 1 {
		t.Error("a new comment must be posted")
	}
}

// TestDescriptorRegistered pins the plugin's registration down: name, category
// and the two optional interfaces the control plane asks for.
func TestDescriptorRegistered(t *testing.T) {
	d, ok := target.Describe("github")
	if !ok {
		t.Fatal("the github plugin must be in the registry")
	}
	if d.Category != target.CategoryCode || d.Label != "GitHub" {
		t.Errorf("descriptor wrong: %+v", d)
	}
	if !d.BaseURLOptional {
		t.Error("github.com is the default endpoint — github_url must be optional")
	}
	if d.NoCredentials {
		t.Error("the plugin needs a token")
	}
	if _, ok := d.System.(target.Webhooker); !ok {
		t.Error("GitHub has a webhook — the plugin must implement Webhooker")
	}
	if _, ok := d.System.(target.SignedWorkChecker); !ok {
		t.Error("the heartbeat gate needs SignedWorkChecker")
	}
	if _, ok := d.System.(target.KindWorkChecker); !ok {
		t.Error("nur-wenn: github:<kind> needs KindWorkChecker")
	}
	if (System{}).ActionSubject("comment", nil) != "github:comment" {
		t.Error("the guard-rail subject must be prefixed with the system")
	}
	if doc := (System{}).PromptDoc(); !strings.Contains(doc, "owner/name") {
		t.Error("the prompt doc must explain the repo identifier")
	}
}
