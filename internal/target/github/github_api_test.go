package github

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/benjaminLedel/covey-plugin-sdk/target"
)

// routes maps "METHOD /path" onto its answer. Anything NOT routed fails the
// test loudly — a plugin quietly calling an endpoint nobody expected is exactly
// the sort of thing these tests exist to catch, and a permissive default
// handler would hide it.
type routes map[string]http.HandlerFunc

// serve starts a test server over a route table and returns a client pointed at
// it plus the call log (in order, as "METHOD /path").
func serve(t *testing.T, rs routes) (*Client, *[]string) {
	t.Helper()
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + strings.TrimPrefix(r.URL.Path, "/api/v3")
		calls = append(calls, key)
		h, ok := rs[key]
		if !ok {
			t.Errorf("unrouted request %s (query %q)", key, r.URL.RawQuery)
			w.WriteHeader(http.StatusNotImplemented)
			return
		}
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	return NewClient(srv.URL+"/api/v3", "test-token"), &calls
}

// jsonOK answers a fixed JSON document.
func jsonOK(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	}
}

// status answers an HTTP error with a GitHub-shaped message body.
func status(code int, message string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(code)
		fmt.Fprintf(w, `{"message":%q}`, message)
	}
}

// run carries out an action by name — the same route the daemon takes.
func run(t *testing.T, c *Client, name string, in actionParams) any {
	t.Helper()
	fn, ok := actions[name]
	if !ok {
		t.Fatalf("action %q is not registered", name)
	}
	res, err := fn(context.Background(), c, in)
	if err != nil {
		t.Fatalf("action %s: %v", name, err)
	}
	return res
}

// runErr carries out an action and demands that it fail.
func runErr(t *testing.T, c *Client, name string, in actionParams) error {
	t.Helper()
	res, err := actions[name](context.Background(), c, in)
	if err == nil {
		t.Fatalf("action %s should have failed, got %+v", name, res)
	}
	return err
}

// -----------------------------------------------------------------------------
// Reading the code: tree, file, commits, branches
// -----------------------------------------------------------------------------

// TestListTreeTakesTwoRoutes: GitHub splits the job. Non-recursively the
// contents API answers for one directory; recursively the git trees API
// answers for the whole repo and the path prefix filters afterwards.
func TestListTreeTakesTwoRoutes(t *testing.T) {
	c, calls := serve(t, routes{
		"GET /repos/acme/support/contents/internal": jsonOK(`[
			{"path":"internal/auth.go","type":"file","size":120,"sha":"a1"},
			{"path":"internal/db","type":"dir","sha":"b2"}]`),
		"GET /repos/acme/support": jsonOK(`{"full_name":"acme/support","default_branch":"main"}`),
		"GET /repos/acme/support/git/trees/main": jsonOK(`{"truncated":false,"tree":[
			{"path":"README.md","type":"blob","size":10},
			{"path":"internal","type":"tree"},
			{"path":"internal/auth.go","type":"blob","size":120},
			{"path":"web/app.tsx","type":"blob","size":40}]}`),
	})
	ctx := context.Background()

	flat, err := c.ListTree(ctx, "acme/support", "internal", "", false)
	if err != nil {
		t.Fatalf("ListTree flat: %v", err)
	}
	if len(flat) != 2 || flat[0].Type != "blob" || flat[1].Type != "tree" {
		t.Fatalf("the contents API's file/dir must become blob/tree: %+v", flat)
	}

	// Without a ref the recursive route has to look the default branch up
	// first — it cannot address a tree without one.
	deep, err := c.ListTree(ctx, "acme/support", "internal", "", true)
	if err != nil {
		t.Fatalf("ListTree recursive: %v", err)
	}
	if len(deep) != 2 {
		t.Fatalf("the path prefix must filter (internal + internal/auth.go): %+v", deep)
	}
	for _, e := range deep {
		if !strings.HasPrefix(e.Path, "internal") {
			t.Errorf("entry outside the prefix: %q", e.Path)
		}
	}
	if !strings.Contains(strings.Join(*calls, " "), "GET /repos/acme/support/git/trees/main") {
		t.Errorf("the recursive route must use the git trees API: %v", *calls)
	}
}

func TestReadFileDecodesAndGuards(t *testing.T) {
	big := strings.Repeat("x", maxReadFileBytes+500)
	c, _ := serve(t, routes{
		"GET /repos/acme/support/contents/go.mod": jsonOK(fmt.Sprintf(
			`{"type":"file","encoding":"base64","size":11,"content":%q}`,
			base64.StdEncoding.EncodeToString([]byte("module covey")))),
		"GET /repos/acme/support/contents/internal": jsonOK(`{"type":"dir"}`),
		// GitHub hands large files back WITHOUT content — they need the blob
		// API, which is deliberately not taken here.
		"GET /repos/acme/support/contents/huge.bin": jsonOK(`{"type":"file","encoding":"none","size":9000000,"content":""}`),
		"GET /repos/acme/support/contents/big.txt": jsonOK(fmt.Sprintf(
			`{"type":"file","encoding":"base64","size":%d,"content":%q}`,
			len(big), base64.StdEncoding.EncodeToString([]byte(big)))),
	})
	ctx := context.Background()

	content, truncated, err := c.ReadFile(ctx, "acme/support", "go.mod", "")
	if err != nil || content != "module covey" || truncated {
		t.Fatalf("ReadFile: %v %q %t", err, content, truncated)
	}
	if _, _, err := c.ReadFile(ctx, "acme/support", "internal", ""); err == nil ||
		!strings.Contains(err.Error(), "directory") {
		t.Errorf("a directory must be named as such: %v", err)
	}
	if _, _, err := c.ReadFile(ctx, "acme/support", "huge.bin", ""); err == nil ||
		!strings.Contains(err.Error(), "checkout") {
		t.Errorf("a file too large must point at the checkout: %v", err)
	}
	if _, _, err := c.ReadFile(ctx, "acme/support", "", ""); err == nil {
		t.Error("a missing file_path must be refused before the request")
	}
	_, truncated, err = c.ReadFile(ctx, "acme/support", "big.txt", "")
	if err != nil || !truncated {
		t.Errorf("a long file must be cut AND say so: %v %t", err, truncated)
	}
}

// TestGetCommitDiffTruncates: a generated file's patch would otherwise crowd
// everything else out of the agent's context — and it has to SEE that it was
// cut, or it reads half a diff as the whole truth.
func TestGetCommitDiffTruncates(t *testing.T) {
	long := strings.Repeat("+line\n", maxDiffBytesPerFile)
	c, _ := serve(t, routes{
		"GET /repos/acme/support/commits/abc123": jsonOK(fmt.Sprintf(`{"files":[
			{"filename":"small.go","status":"modified","additions":1,"deletions":0,"patch":"@@ -1 +1 @@"},
			{"filename":"generated.ts","status":"modified","patch":%q}]}`, long)),
	})
	files, err := c.GetCommitDiff(context.Background(), "acme/support", "abc123")
	if err != nil {
		t.Fatalf("GetCommitDiff: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("want 2 files, got %d", len(files))
	}
	if files[0].Truncated {
		t.Error("a short patch must stay whole")
	}
	if !files[1].Truncated || len(files[1].Patch) != maxDiffBytesPerFile {
		t.Errorf("a long patch must be cut to the limit and flagged: %d %t",
			len(files[1].Patch), files[1].Truncated)
	}
	if _, err := c.GetCommitDiff(context.Background(), "acme/support", " "); err == nil {
		t.Error("a missing sha must be refused before the request")
	}
}

// TestListBranchesMarksDefault: the agent must not guess branch names, and it
// has to see which one is the default — commit refuses that one.
func TestListBranchesMarksDefault(t *testing.T) {
	c, _ := serve(t, routes{
		"GET /repos/acme/support/branches": jsonOK(`[
			{"name":"main","protected":true,"commit":{"sha":"m1"}},
			{"name":"fix/login","commit":{"sha":"f1"}},
			{"name":"feat/export","commit":{"sha":"e1"}}]`),
		"GET /repos/acme/support": jsonOK(`{"full_name":"acme/support","default_branch":"main"}`),
	})
	ctx := context.Background()

	all, err := c.ListBranches(ctx, "acme/support", "")
	if err != nil || len(all) != 3 {
		t.Fatalf("ListBranches: %v %+v", err, all)
	}
	var def string
	for _, b := range all {
		if b.Default {
			def = b.Name
		}
	}
	if def != "main" {
		t.Errorf("the default branch must be marked, got %q", def)
	}

	found, err := c.ListBranches(ctx, "acme/support", "fix")
	if err != nil || len(found) != 1 || found[0].Name != "fix/login" {
		t.Fatalf("search must filter the answer: %v %+v", err, found)
	}
}

// TestListCommitsPassesFilters: the filters are how an agent checks whether a
// reported fault has been fixed since — they must reach GitHub, not be dropped.
func TestListCommitsPassesFilters(t *testing.T) {
	var q url.Values
	c, _ := serve(t, routes{
		"GET /repos/acme/support/commits": func(w http.ResponseWriter, r *http.Request) {
			q = r.URL.Query()
			w.Write([]byte(`[{"sha":"a1","commit":{"message":"fix login","author":{"name":"m","date":"2026-01-01T00:00:00Z"}}}]`))
		},
	})
	commits, err := c.ListCommits(context.Background(), "acme/support", "main", "internal/auth.go", "2026-01-01T00:00:00Z")
	if err != nil || len(commits) != 1 {
		t.Fatalf("ListCommits: %v %+v", err, commits)
	}
	for key, want := range map[string]string{
		"sha": "main", "path": "internal/auth.go", "since": "2026-01-01T00:00:00Z",
	} {
		if got := q.Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

// -----------------------------------------------------------------------------
// Issue actions
// -----------------------------------------------------------------------------

func TestIssueActions(t *testing.T) {
	t.Setenv("COVEY_GITHUB_INTAKE_REPOS", "")
	var createBody, assignBody, stateBody map[string]any
	capture := func(into *map[string]any, answer string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			json.NewDecoder(r.Body).Decode(into)
			w.Write([]byte(answer))
		}
	}
	c, _ := serve(t, routes{
		"GET /repos/acme/support/issues/5":            jsonOK(`{"number":5,"title":"Login","state":"open","user":{"login":"kunde"}}`),
		"POST /repos/acme/support/issues":             capture(&createBody, `{"number":12,"title":"From email"}`),
		"GET /users/qa-egon":                          jsonOK(`{"login":"qa-egon","type":"User"}`),
		"POST /repos/acme/support/issues/5/assignees": capture(&assignBody, `{"number":5}`),
		"PATCH /repos/acme/support/issues/5":          capture(&stateBody, `{"number":5,"state":"closed"}`),
	})

	issue := run(t, c, "get_issue", actionParams{Repo: "acme/support", IssueNumber: 5}).(Issue)
	if issue.Repo != "acme/support" || issue.Number != 5 {
		t.Errorf("get_issue must fill the repo in: %+v", issue)
	}

	run(t, c, "create_issue", actionParams{
		Repo: "acme/support", Title: "From email", Description: "reported by mail",
		Labels: "bug, intake ,", Assignee: "qa-egon",
	})
	labels, _ := createBody["labels"].([]any)
	if len(labels) != 2 || labels[0] != "bug" || labels[1] != "intake" {
		t.Errorf("labels must be split and trimmed, empty entries dropped: %+v", createBody["labels"])
	}
	if as, _ := createBody["assignees"].([]any); len(as) != 1 || as[0] != "qa-egon" {
		t.Errorf("the assignee must be resolved: %+v", createBody["assignees"])
	}

	run(t, c, "assign", actionParams{Repo: "acme/support", IssueNumber: 5, Username: "qa-egon"})
	if as, _ := assignBody["assignees"].([]any); len(as) != 1 || as[0] != "qa-egon" {
		t.Errorf("assign must add: %+v", assignBody)
	}

	run(t, c, "set_state", actionParams{Repo: "acme/support", IssueNumber: 5, State: "close"})
	if stateBody["state"] != "closed" {
		t.Errorf("close must become GitHub's \"closed\": %+v", stateBody)
	}
	run(t, c, "set_state", actionParams{Repo: "acme/support", IssueNumber: 5, State: "reopen"})
	if stateBody["state"] != "open" {
		t.Errorf("reopen must become \"open\": %+v", stateBody)
	}
	if err := runErr(t, c, "set_state", actionParams{Repo: "acme/support", IssueNumber: 5, State: "pending"}); err == nil ||
		!strings.Contains(err.Error(), "unknown") {
		t.Errorf("an invalid state must be named: %v", err)
	}
}

// TestActionsDemandTheirIdentifiers: an action that quietly works on item 0
// would touch the wrong thing. Every one of these has to say what is missing.
func TestActionsDemandTheirIdentifiers(t *testing.T) {
	c, _ := serve(t, routes{})
	cases := []struct{ action, want string }{
		{"get_issue", "issue_number"},
		{"list_comments", "issue_number"},
		{"comment", "issue_number"},
		{"set_state", "issue_number"},
		{"assign", "issue_number"},
		{"set_labels", "issue_number"},
		{"escalate", "issue_number"},
		{"get_pull_request", "pr_number"},
		{"list_pr_comments", "pr_number"},
		{"comment_pr", "pr_number"},
		{"request_review", "pr_number"},
		{"approve_pr", "pr_number"},
		{"request_changes", "pr_number"},
		{"list_run_jobs", "run_id"},
		{"rerun_failed_jobs", "run_id"},
		{"get_job_log", "job_id"},
		{"create_issue", "title"},
		{"read_file", "file_path"},
	}
	for _, tc := range cases {
		err := runErr(t, c, tc.action, actionParams{Repo: "acme/support"})
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %q must name %q", tc.action, err, tc.want)
		}
	}
	// download_attachment is the one action without a repo — but it does need
	// its URL.
	if err := runErr(t, c, "download_attachment", actionParams{}); !strings.Contains(err.Error(), "url") {
		t.Errorf("download_attachment must demand the url: %v", err)
	}
}

func TestListReposRespectsIntakeScope(t *testing.T) {
	t.Setenv("COVEY_GITHUB_INTAKE_REPOS", "acme/*")
	c, _ := serve(t, routes{
		"GET /user/repos": jsonOK(`[
			{"full_name":"acme/support","default_branch":"main"},
			{"full_name":"acme/web","default_branch":"main"},
			{"full_name":"foreign/thing","default_branch":"main"}]`),
	})
	repos := run(t, c, "list_repos", actionParams{}).([]Repo)
	if len(repos) != 2 {
		t.Fatalf("the allowlist must filter: %+v", repos)
	}
	for _, r := range repos {
		if r.Full != r.FullName || r.Full == "" {
			t.Errorf("the repo identifier must be filled in: %+v", r)
		}
	}
}

// -----------------------------------------------------------------------------
// Pull requests
// -----------------------------------------------------------------------------

// prRoutes is the endpoint set create_pull_request needs. The answers are
// overridden per case.
func prRoutes(t *testing.T, created, assigned, reviewers *map[string]any) routes {
	t.Helper()
	capture := func(into *map[string]any, answer string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			json.NewDecoder(r.Body).Decode(into)
			w.Write([]byte(answer))
		}
	}
	return routes{
		"GET /repos/acme/support":                              jsonOK(`{"full_name":"acme/support","default_branch":"main"}`),
		"GET /repos/acme/support/issues/42":                    jsonOK(`{"number":42,"title":"Login","user":{"login":"kunde"}}`),
		"GET /users/kunde":                                     jsonOK(`{"login":"kunde","type":"User"}`),
		"GET /users/qa-egon":                                   jsonOK(`{"login":"qa-egon","type":"User"}`),
		"GET /users/covey-bot":                                 jsonOK(`{"login":"covey-bot","type":"User"}`),
		"POST /repos/acme/support/pulls":                       capture(created, `{"number":9,"title":"Fix login","user":{"login":"covey-bot"},"head":{"ref":"fix/login"},"base":{"ref":"main"}}`),
		"POST /repos/acme/support/issues/9/assignees":          capture(assigned, `{"number":9}`),
		"POST /repos/acme/support/pulls/9/requested_reviewers": capture(reviewers, `{"number":9}`),
	}
}

// TestCreatePullRequestFindsTheReporter: the PR falls to whoever wrote the need
// down, not to the manager by default — otherwise the manager becomes the
// bottleneck for work they never asked for.
func TestCreatePullRequestFindsTheReporter(t *testing.T) {
	var created, assigned, reviewers map[string]any
	c, _ := serve(t, prRoutes(t, &created, &assigned, &reviewers))

	out := run(t, c, "create_pull_request", actionParams{
		Repo: "acme/support", Head: "fix/login", Title: "Fix login",
		Description: "Fixes #42", IssueNumber: 42, Reviewer: "qa-egon",
	}).(map[string]any)

	if created["base"] != "main" {
		t.Errorf("base must default to the default branch: %+v", created)
	}
	if created["head"] != "fix/login" || created["draft"] != false {
		t.Errorf("head/draft wrong: %+v", created)
	}
	if out["assignee"] != "kunde" {
		t.Errorf("the assignee must be the issue's reporter, got %v", out["assignee"])
	}
	if as, _ := assigned["assignees"].([]any); len(as) != 1 || as[0] != "kunde" {
		t.Errorf("the assignee must be entered on the PR: %+v", assigned)
	}
	if out["reviewer"] != "qa-egon" {
		t.Errorf("the named QA agent must become the reviewer, got %v", out["reviewer"])
	}
	if rs, _ := reviewers["reviewers"].([]any); len(rs) != 1 || rs[0] != "qa-egon" {
		t.Errorf("the reviewer must be requested: %+v", reviewers)
	}
}

// TestCreatePullRequestSkipsSelfReview: GitHub refuses to have an author review
// their own PR. Without this the whole call would fail on the last step and the
// agent would be left with a PR it thinks did not happen.
func TestCreatePullRequestSkipsSelfReview(t *testing.T) {
	var created, assigned, reviewers map[string]any
	rs := prRoutes(t, &created, &assigned, &reviewers)
	// The author IS the assignee here — the reviewer endpoint must not be hit.
	rs["POST /repos/acme/support/pulls"] = func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&created)
		w.Write([]byte(`{"number":9,"user":{"login":"covey-bot"}}`))
	}
	delete(rs, "POST /repos/acme/support/pulls/9/requested_reviewers")
	c, calls := serve(t, rs)

	out := run(t, c, "create_pull_request", actionParams{
		Repo: "acme/support", Head: "fix/login", Title: "Fix login", Assignee: "covey-bot",
	}).(map[string]any)

	if _, ok := out["reviewer"]; ok {
		t.Errorf("no reviewer may be entered when it would be the author: %+v", out)
	}
	for _, call := range *calls {
		if strings.Contains(call, "requested_reviewers") {
			t.Errorf("the reviewer endpoint must not be called: %v", *calls)
		}
	}
}

// TestCreatePullRequestReportsPartialFailure: assignee and reviewer hang off
// separate endpoints. Once the PR exists a failure on those must be REPORTED,
// not swallowed and not turned into an error that hides the PR.
func TestCreatePullRequestReportsPartialFailure(t *testing.T) {
	var created, assigned, reviewers map[string]any
	rs := prRoutes(t, &created, &assigned, &reviewers)
	rs["POST /repos/acme/support/issues/9/assignees"] = status(http.StatusForbidden, "no permission")
	rs["POST /repos/acme/support/pulls/9/requested_reviewers"] = status(http.StatusUnprocessableEntity, "not a collaborator")
	c, _ := serve(t, rs)

	out := run(t, c, "create_pull_request", actionParams{
		Repo: "acme/support", Head: "fix/login", Title: "Fix login",
		Assignee: "kunde", Reviewer: "qa-egon",
	}).(map[string]any)

	if out["pull_request"] == nil {
		t.Fatal("the PR exists — it must come back")
	}
	if out["assignee_error"] == nil || out["reviewer_error"] == nil {
		t.Errorf("both failures must be visible: %+v", out)
	}
}

func TestCreatePullRequestRefusesImpossibleInput(t *testing.T) {
	var created, assigned, reviewers map[string]any
	c, _ := serve(t, prRoutes(t, &created, &assigned, &reviewers))

	if err := runErr(t, c, "create_pull_request", actionParams{
		Repo: "acme/support", Head: "main", Base: "main", Title: "x", Assignee: "kunde",
	}); !strings.Contains(err.Error(), "identical") {
		t.Errorf("head == base must be refused: %v", err)
	}
	if err := runErr(t, c, "create_pull_request", actionParams{
		Repo: "acme/support", Head: "fix/login", Title: "x",
	}); !strings.Contains(err.Error(), "assignee missing") {
		t.Errorf("a PR without a named recipient must be refused: %v", err)
	}
	if err := runErr(t, c, "create_pull_request", actionParams{
		Repo: "acme/support", Title: "x", Assignee: "kunde",
	}); !strings.Contains(err.Error(), "head") {
		t.Errorf("a PR without a source branch must be refused: %v", err)
	}
}

// TestCreatePullRequestAcceptsBranchAsHead: the commit action answers with
// "branch". Taking that name as head too spares the agent a rename it would
// otherwise get wrong half the time.
func TestCreatePullRequestAcceptsBranchAsHead(t *testing.T) {
	var created, assigned, reviewers map[string]any
	c, _ := serve(t, prRoutes(t, &created, &assigned, &reviewers))
	run(t, c, "create_pull_request", actionParams{
		Repo: "acme/support", Branch: "fix/login", Title: "Fix login", Assignee: "kunde",
	})
	if created["head"] != "fix/login" {
		t.Errorf("branch must serve as head: %+v", created)
	}
}

// TestGetPullRequestCarriesTheChecks: mergeable says nothing about whether the
// tests are green, and that is what the agent is deciding on.
func TestGetPullRequestCarriesTheChecks(t *testing.T) {
	c, _ := serve(t, routes{
		"GET /repos/acme/support/pulls/9": jsonOK(`{"number":9,"state":"open","mergeable":true,
			"mergeable_state":"clean","head":{"ref":"fix/login","sha":"deadbeef"}}`),
		"GET /repos/acme/support/commits/deadbeef/check-runs": jsonOK(`{"check_runs":[
			{"name":"test","status":"completed","conclusion":"failure"}]}`),
	})
	out := run(t, c, "get_pull_request", actionParams{Repo: "acme/support", PRNumber: 9}).(map[string]any)
	pr := out["pull_request"].(PullRequest)
	if pr.Repo != "acme/support" || pr.Mergeable == nil || !*pr.Mergeable {
		t.Errorf("the PR must come back complete: %+v", pr)
	}
	checks := out["checks"].([]CheckRun)
	if len(checks) != 1 || checks[0].Conclusion != "failure" {
		t.Errorf("a red check must be visible next to mergeable=true: %+v", checks)
	}
}

// TestGetPullRequestSurvivesCheckFailure: the Actions permission is optional
// (a read-only token may lack it). Losing the checks must not lose the PR.
func TestGetPullRequestSurvivesCheckFailure(t *testing.T) {
	c, _ := serve(t, routes{
		"GET /repos/acme/support/pulls/9":                     jsonOK(`{"number":9,"head":{"sha":"deadbeef"}}`),
		"GET /repos/acme/support/commits/deadbeef/check-runs": status(http.StatusForbidden, "no access"),
	})
	out := run(t, c, "get_pull_request", actionParams{Repo: "acme/support", PRNumber: 9}).(map[string]any)
	if out["pull_request"] == nil || out["checks_error"] == nil {
		t.Errorf("the PR must survive, the failure must be named: %+v", out)
	}
}

// TestReviewVerdicts: approve is the green signal, request_changes the one that
// actually holds a merge up. A reason is mandatory for the latter — "changes
// requested" without saying what is not review, it is an obstacle.
func TestReviewVerdicts(t *testing.T) {
	var approve, changes, requested map[string]any
	capture := func(into *map[string]any) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if body["event"] == "APPROVE" {
				approve = body
			} else {
				changes = body
			}
			w.Write([]byte(`{"id":1,"state":"APPROVED"}`))
		}
	}
	c, _ := serve(t, routes{
		"POST /repos/acme/support/pulls/9/reviews": capture(&approve),
		"GET /users/qa-egon":                       jsonOK(`{"login":"qa-egon"}`),
		"POST /repos/acme/support/pulls/9/requested_reviewers": func(w http.ResponseWriter, r *http.Request) {
			json.NewDecoder(r.Body).Decode(&requested)
			w.Write([]byte(`{"number":9}`))
		},
	})

	run(t, c, "approve_pr", actionParams{Repo: "acme/support", PRNumber: 9, Body: "tested end to end, green"})
	if approve["event"] != "APPROVE" || approve["body"] != "tested end to end, green" {
		t.Errorf("approve_pr must submit an APPROVE review: %+v", approve)
	}

	run(t, c, "request_changes", actionParams{Repo: "acme/support", PRNumber: 9, Body: "auth.go:42 returns nil"})
	if changes["event"] != "REQUEST_CHANGES" {
		t.Errorf("request_changes must submit REQUEST_CHANGES: %+v", changes)
	}
	if err := runErr(t, c, "request_changes", actionParams{Repo: "acme/support", PRNumber: 9}); !strings.Contains(err.Error(), "body missing") {
		t.Errorf("request_changes without a reason must be refused: %v", err)
	}

	run(t, c, "request_review", actionParams{Repo: "acme/support", PRNumber: 9, Username: "qa-egon"})
	if rs, _ := requested["reviewers"].([]any); len(rs) != 1 || rs[0] != "qa-egon" {
		t.Errorf("request_review must enter the reviewer: %+v", requested)
	}
}

// -----------------------------------------------------------------------------
// CI (GitHub Actions)
// -----------------------------------------------------------------------------

func TestWorkflowActions(t *testing.T) {
	var branchQuery string
	rerun := false
	c, _ := serve(t, routes{
		"GET /repos/acme/support/actions/runs": func(w http.ResponseWriter, r *http.Request) {
			branchQuery = r.URL.Query().Get("branch")
			w.Write([]byte(`{"workflow_runs":[{"id":77,"name":"CI","status":"completed","conclusion":"failure","head_branch":"fix/login"}]}`))
		},
		"GET /repos/acme/support/actions/runs/77/jobs": jsonOK(`{"jobs":[
			{"id":881,"name":"test","status":"completed","conclusion":"failure"}]}`),
		"POST /repos/acme/support/actions/runs/77/rerun-failed-jobs": func(w http.ResponseWriter, _ *http.Request) {
			rerun = true
			w.WriteHeader(http.StatusCreated)
		},
	})

	runs := run(t, c, "list_workflow_runs", actionParams{Repo: "acme/support", Branch: "fix/login"}).([]WorkflowRun)
	if len(runs) != 1 || runs[0].Conclusion != "failure" {
		t.Fatalf("list_workflow_runs: %+v", runs)
	}
	if branchQuery != "fix/login" {
		t.Errorf("the branch filter must reach GitHub, got %q", branchQuery)
	}
	// ref is accepted as a spelling of branch — the agent uses both words.
	run(t, c, "list_workflow_runs", actionParams{Repo: "acme/support", Ref: "main"})
	if branchQuery != "main" {
		t.Errorf("ref must serve as the branch, got %q", branchQuery)
	}

	jobs := run(t, c, "list_run_jobs", actionParams{Repo: "acme/support", RunID: 77}).([]Job)
	if len(jobs) != 1 || jobs[0].ID != 881 {
		t.Fatalf("list_run_jobs: %+v", jobs)
	}

	run(t, c, "rerun_failed_jobs", actionParams{Repo: "acme/support", RunID: 77})
	if !rerun {
		t.Error("rerun_failed_jobs must reach the endpoint")
	}
}

// TestGetJobLogTakesTheEnd: the failure stands at the bottom of a CI log, the
// setup noise at the top. Cutting the wrong end would hand the agent the part
// that never explains anything.
func TestGetJobLogTakesTheEnd(t *testing.T) {
	head := strings.Repeat("setup noise\n", maxJobLogBytes/6)
	tail := "FAIL internal/auth: nil pointer at auth.go:42\n"
	c, _ := serve(t, routes{
		"GET /repos/acme/support/actions/jobs/881/logs": func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(head + tail))
		},
		"GET /repos/acme/support/actions/jobs/882/logs": func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte("short log\n"))
		},
	})

	out := run(t, c, "get_job_log", actionParams{Repo: "acme/support", JobID: 881}).(map[string]any)
	logText := out["log"].(string)
	if out["truncated"] != true {
		t.Errorf("a long log must be flagged as cut: %+v", out["truncated"])
	}
	if !strings.HasSuffix(logText, tail) {
		t.Error("the END of the log must survive — that is where the failure is")
	}
	if len(logText) != maxJobLogBytes {
		t.Errorf("the log must be cut to the limit, got %d", len(logText))
	}

	out = run(t, c, "get_job_log", actionParams{Repo: "acme/support", JobID: 882}).(map[string]any)
	if out["truncated"] != false || out["log"] != "short log\n" {
		t.Errorf("a short log must stay whole: %+v", out)
	}
}

// -----------------------------------------------------------------------------
// Identity, errors, escalation
// -----------------------------------------------------------------------------

// TestLookupUserFailsLoudly: GitHub silently swallows an unknown assignee — the
// request succeeds and the assignment does not happen. That is the kind of
// failure that only shows up days later, so the login is checked first.
func TestLookupUserFailsLoudly(t *testing.T) {
	c, _ := serve(t, routes{
		"GET /users/qa-egon": jsonOK(`{"login":"qa-egon","type":"User"}`),
		"GET /users/ghost":   status(http.StatusNotFound, "Not Found"),
	})
	ctx := context.Background()

	// The @ prefix is how a human writes a login — it must not reach the URL.
	u, err := c.LookupUser(ctx, " @qa-egon ")
	if err != nil || u.Login != "qa-egon" {
		t.Fatalf("LookupUser: %v %+v", err, u)
	}
	if _, err := c.LookupUser(ctx, "ghost"); err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Errorf("an unknown login must be named: %v", err)
	}
	if _, err := c.LookupUser(ctx, ""); err == nil {
		t.Error("an empty login must be refused before the request")
	}
}

// TestRateLimitIsNamed: a bare "HTTP 403" reads like a permission problem and
// sends the agent looking for the wrong cause — it would go asking for token
// scopes it already has instead of simply waiting.
func TestRateLimitIsNamed(t *testing.T) {
	c, _ := serve(t, routes{
		"GET /repos/acme/support/issues/5": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("X-RateLimit-Reset", "1800000000")
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`{"message":"API rate limit exceeded"}`))
		},
		"GET /repos/acme/support/issues/6": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("X-RateLimit-Remaining", "4999")
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`{"message":"Resource not accessible by personal access token"}`))
		},
	})
	ctx := context.Background()

	_, err := c.GetIssue(ctx, "acme/support", 5)
	if err == nil || !strings.Contains(err.Error(), "rate limit exhausted") {
		t.Errorf("an exhausted budget must be named as such: %v", err)
	}
	if !strings.Contains(fmt.Sprint(err), "2027-01-15") {
		t.Errorf("the reset time must be readable: %v", err)
	}

	_, err = c.GetIssue(ctx, "acme/support", 6)
	if err == nil || strings.Contains(err.Error(), "rate limit") {
		t.Errorf("a genuine permission problem must NOT be dressed up as a rate limit: %v", err)
	}
}

// TestEscalateSurvivesUnknownIdentity: the comment IS the escalation. If the
// who-am-I lookup fails there is nothing to hand back, but the note stands —
// reporting an error there would hide a message that was posted.
func TestEscalateSurvivesUnknownIdentity(t *testing.T) {
	posted := false
	c, _ := serve(t, routes{
		"POST /repos/acme/support/issues/7/comments": func(w http.ResponseWriter, _ *http.Request) {
			posted = true
			w.Write([]byte(`{"id":1}`))
		},
		"GET /user": status(http.StatusUnauthorized, "Bad credentials"),
	})
	if err := c.Escalate(context.Background(), "acme/support", 7, "Please take over"); err != nil {
		t.Fatalf("the escalation must stand: %v", err)
	}
	if !posted {
		t.Error("the note must have been posted")
	}
}

func TestSetLabelsToleratesAMissingLabel(t *testing.T) {
	c, _ := serve(t, routes{
		// Removing a label that is not set is not a failure — the goal state is
		// what counts, not the route to it.
		"DELETE /repos/acme/support/issues/7/labels/triage": status(http.StatusNotFound, "Label does not exist"),
		"GET /repos/acme/support/issues/7/labels":           jsonOK(`[{"name":"bug"}]`),
	})
	labels, err := c.SetLabels(context.Background(), "acme/support", 7, nil, []string{"triage"})
	if err != nil {
		t.Fatalf("a label that is not set must not fail: %v", err)
	}
	if len(labels) != 1 || labels[0] != "bug" {
		t.Errorf("the state reached must come back even without add_labels: %v", labels)
	}
	if _, err := c.SetLabels(context.Background(), "acme/support", 7, nil, nil); err == nil {
		t.Error("two empty lists must be refused — there is nothing to do")
	}
}

// -----------------------------------------------------------------------------
// The QA view and the plugin's outer surface
// -----------------------------------------------------------------------------

// TestHasWorkReviewScope: as reviewer the PR waits until the bot has reviewed
// the CURRENT state. GitHub clears the review request once a review lands, so
// the standing request is GitHub's own statement that it is still owed.
func TestHasWorkReviewScope(t *testing.T) {
	t.Setenv("COVEY_GITHUB_INTAKE_REPOS", "")
	requested := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch strings.TrimPrefix(r.URL.Path, "/api/v3") {
		case "/user":
			json.NewEncoder(w).Encode(User{Login: "qa-egon"})
		case "/search/issues":
			if !strings.Contains(r.URL.Query().Get("q"), "review-requested:@me") {
				t.Errorf("the review scope must search for requested reviews: %q", r.URL.RawQuery)
			}
			w.Write([]byte(`{"items":[{"number":9,"html_url":"https://github.com/acme/support/pull/9"}]}`))
		case "/repos/acme/support/pulls/9":
			reviewers := `[]`
			if requested {
				reviewers = `[{"login":"qa-egon"}]`
			}
			fmt.Fprintf(w, `{"number":9,"head":{"sha":"cafebabe0000"},"requested_reviewers":%s}`, reviewers)
		case "/repos/acme/support/issues/9/comments":
			// The QA agent itself wrote last …
			w.Write([]byte(`[{"id":30,"created_at":"2026-01-01T10:00:00Z","user":{"login":"qa-egon"}}]`))
		case "/repos/acme/support/pulls/9/reviews", "/repos/acme/support/pulls/9/comments":
			w.Write([]byte(`[]`))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	defer srv.Close()

	cred := target.Credential{BaseURL: srv.URL + "/api/v3", Token: "t"}
	ctx := context.Background()

	// … but the review request still stands, so the review is still owed.
	has, _, err := System{}.HasWorkSigned(ctx, cred, "review")
	if err != nil || !has {
		t.Fatalf("a standing review request must count as work: %v %t", err, has)
	}

	// Once GitHub has cleared the request and the bot wrote last, it rests.
	requested = false
	has, err = System{}.HasWorkKind(ctx, cred, "reviews")
	if err != nil || has {
		t.Fatalf("a review already given must not wake again: %v %t", err, has)
	}
}

// TestHasWorkUnknownScopeFailsOpen: a typo in nur-wenn: must not silence the
// agent. Reporting less than HasWork would park it for good, and nothing in the
// UI would say why.
func TestHasWorkUnknownScopeFailsOpen(t *testing.T) {
	t.Setenv("COVEY_GITHUB_INTAKE_REPOS", "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch strings.TrimPrefix(r.URL.Path, "/api/v3") {
		case "/user":
			json.NewEncoder(w).Encode(User{Login: "covey-bot"})
		case "/issues":
			w.Write([]byte(`[{"number":5,"comments":0,"repository":{"full_name":"acme/support"}}]`))
		case "/search/issues":
			w.Write([]byte(`{"items":[]}`))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	defer srv.Close()

	cred := target.Credential{BaseURL: srv.URL + "/api/v3", Token: "t"}
	has, err := System{}.HasWork(context.Background(), cred)
	if err != nil || !has {
		t.Fatalf("HasWork must see the untouched issue: %v %t", err, has)
	}
	has, err = System{}.HasWorkKind(context.Background(), cred, "typo-scope")
	if err != nil || !has {
		t.Fatalf("an unknown scope must fail open: %v %t", err, has)
	}
}

// TestHasWorkManyIssuesSkipsTheCommentCheck: the check runs in every heartbeat
// interval. With a hundred open issues it must not turn into a hundred
// requests — the agent then decides for itself which of them is new.
func TestHasWorkManyIssuesSkipsTheCommentCheck(t *testing.T) {
	t.Setenv("COVEY_GITHUB_INTAKE_REPOS", "")
	var list []map[string]any
	for i := 1; i <= issueMaxCommentChecks+1; i++ {
		list = append(list, map[string]any{
			"number": i, "comments": 3,
			"repository": map[string]any{"full_name": "acme/support"},
		})
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/api/v3")
		if strings.HasSuffix(p, "/comments") {
			t.Errorf("no comment check may run above the cap: %q", p)
		}
		if p == "/issues" {
			json.NewEncoder(w).Encode(list)
			return
		}
		t.Errorf("unexpected path %q", p)
	}))
	defer srv.Close()

	cred := target.Credential{BaseURL: srv.URL + "/api/v3", Token: "t"}
	has, sig, err := System{}.HasWorkSigned(context.Background(), cred, "issues")
	if err != nil || !has {
		t.Fatalf("many open issues must wake: %v %t", err, has)
	}
	if !strings.Contains(sig, fmt.Sprintf("issues:many@%d", len(list))) {
		t.Errorf("the signature must carry the count so it changes with it: %q", sig)
	}
}

// TestSystemSurface covers the small methods the control plane calls directly.
func TestSystemSurface(t *testing.T) {
	t.Setenv("COVEY_GITHUB_INTAKE_REPOS", "")
	t.Setenv("COVEY_GITHUB_BOT_LOGINS", "")
	if (System{}).Name() != "github" {
		t.Errorf("Name = %q", (System{}).Name())
	}
	ev, err := (System{}).ParseWebhook([]byte(`{"action":"opened",
		"repository":{"full_name":"acme/support"},"sender":{"login":"kunde"},
		"issue":{"number":5,"title":"Login"}}`))
	if err != nil || !ev.Wake {
		t.Fatalf("ParseWebhook: %v %+v", err, ev)
	}
	if _, err := (System{}).ParseWebhook([]byte(`not json`)); err == nil {
		t.Error("a broken payload must be an error")
	}
	if _, err := (System{}).Execute(context.Background(), "get_issue",
		json.RawMessage(`{"repo":`), target.Credential{}); err == nil ||
		!strings.Contains(err.Error(), "params") {
		t.Errorf("broken params must be named as such: %v", err)
	}
}

func TestIsRequestedReviewer(t *testing.T) {
	pr := PullRequest{RequestedReviewers: []User{{Login: "QA-Egon"}, {Login: "other"}}}
	if !isRequestedReviewer(pr, "qa-egon") {
		t.Error("the login comparison must ignore case — GitHub does too")
	}
	if isRequestedReviewer(pr, "somebody") {
		t.Error("a stranger must not count as reviewer")
	}
	if isRequestedReviewer(PullRequest{}, "qa-egon") {
		t.Error("a PR without reviewers asks nobody")
	}
}

func TestSplitLabels(t *testing.T) {
	got := splitLabels(" bug , ui ,, ")
	if len(got) != 2 || got[0] != "bug" || got[1] != "ui" {
		t.Errorf("splitLabels = %#v", got)
	}
	if len(splitLabels("")) != 0 {
		t.Error("an empty string must produce no labels")
	}
}

func TestRepoFromHTMLURL(t *testing.T) {
	cases := map[string]string{
		"https://github.com/acme/support/pull/9":   "acme/support",
		"https://github.com/acme/support/issues/3": "acme/support",
		"https://github.com/acme":                  "",
		"":                                         "",
		"://broken":                                "",
	}
	for in, want := range cases {
		if got := repoFromHTMLURL(in); got != want {
			t.Errorf("repoFromHTMLURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// -----------------------------------------------------------------------------
// commit: the branch that already exists
// -----------------------------------------------------------------------------

// TestCommitOntoExistingBranch is the second push of the review loop: the
// branch is there, so the commit hangs off ITS head and the ref is moved, not
// created.
func TestCommitOntoExistingBranch(t *testing.T) {
	workdir := t.TempDir()
	checkout := filepath.Join(workdir, "repos", "acme-support-fix-login")
	os.MkdirAll(checkout, 0o755)
	os.WriteFile(filepath.Join(checkout, "main.go"), []byte("package main // fixed"), 0o644)

	var patched map[string]any
	var patchPath string
	c, calls := serve(t, routes{
		"GET /repos/acme/support":                         jsonOK(`{"full_name":"acme/support","default_branch":"main"}`),
		"GET /repos/acme/support/git/ref/heads/fix/login": jsonOK(`{"object":{"sha":"branchhead"}}`),
		"GET /repos/acme/support/git/commits/branchhead":  jsonOK(`{"tree":{"sha":"branchtree"}}`),
		"POST /repos/acme/support/git/blobs":              jsonOK(`{"sha":"blob1"}`),
		"POST /repos/acme/support/git/trees":              jsonOK(`{"sha":"newtree"}`),
		"POST /repos/acme/support/git/commits":            jsonOK(`{"sha":"newcommit"}`),
		"PATCH /repos/acme/support/git/refs/heads/fix/login": func(w http.ResponseWriter, r *http.Request) {
			patchPath = r.URL.Path
			json.NewDecoder(r.Body).Decode(&patched)
			w.Write([]byte(`{}`))
		},
	})

	res, err := CommitFromCheckout(context.Background(), c, "acme/support",
		"fix/login", "", "work the review in", checkout, []string{"main.go"}, nil, workdir)
	if err != nil {
		t.Fatalf("CommitFromCheckout: %v", err)
	}
	if res.BranchCreated {
		t.Error("an existing branch must not be reported as created")
	}
	if patched["force"] != false {
		t.Errorf("no force push: %+v", patched)
	}
	if patched["sha"] != "newcommit" || !strings.HasSuffix(patchPath, "/git/refs/heads/fix/login") {
		t.Errorf("the branch must be moved onto the new commit: %s %+v", patchPath, patched)
	}
	for _, call := range *calls {
		if call == "POST /repos/acme/support/git/refs" {
			t.Error("an existing branch must not be created afresh")
		}
	}
}

// TestCommitOnAMovedBranchExplainsItself: a 422 on the ref move means somebody
// else has pushed. The agent has to be told to fetch and redo — a bare "HTTP
// 422" would send it retrying the same push.
func TestCommitOnAMovedBranchExplainsItself(t *testing.T) {
	workdir := t.TempDir()
	checkout := filepath.Join(workdir, "repos", "acme-support-fix-login")
	os.MkdirAll(checkout, 0o755)
	os.WriteFile(filepath.Join(checkout, "main.go"), []byte("x"), 0o644)

	c, _ := serve(t, routes{
		"GET /repos/acme/support":                            jsonOK(`{"full_name":"acme/support","default_branch":"main"}`),
		"GET /repos/acme/support/git/ref/heads/fix/login":    jsonOK(`{"object":{"sha":"stale"}}`),
		"GET /repos/acme/support/git/commits/stale":          jsonOK(`{"tree":{"sha":"t"}}`),
		"POST /repos/acme/support/git/blobs":                 jsonOK(`{"sha":"b"}`),
		"POST /repos/acme/support/git/trees":                 jsonOK(`{"sha":"nt"}`),
		"POST /repos/acme/support/git/commits":               jsonOK(`{"sha":"nc"}`),
		"PATCH /repos/acme/support/git/refs/heads/fix/login": status(http.StatusUnprocessableEntity, "Update is not a fast forward"),
	})

	_, err := CommitFromCheckout(context.Background(), c, "acme/support",
		"fix/login", "", "msg", checkout, []string{"main.go"}, nil, workdir)
	if err == nil || !strings.Contains(err.Error(), "moved on") {
		t.Fatalf("a moved branch must be explained, got %v", err)
	}
	if !strings.Contains(err.Error(), "checkout") {
		t.Errorf("the way out must be named: %v", err)
	}
}

// TestCommitWithoutAStartBranch: if the named start branch does not exist, the
// commit must fail with that in plain words rather than dropping the change
// somewhere else.
func TestCommitWithoutAStartBranch(t *testing.T) {
	workdir := t.TempDir()
	checkout := filepath.Join(workdir, "repos", "x")
	os.MkdirAll(checkout, 0o755)
	os.WriteFile(filepath.Join(checkout, "main.go"), []byte("x"), 0o644)

	c, _ := serve(t, routes{
		"GET /repos/acme/support":                         jsonOK(`{"full_name":"acme/support","default_branch":"main"}`),
		"GET /repos/acme/support/git/ref/heads/fix/login": status(http.StatusNotFound, "Not Found"),
		"GET /repos/acme/support/git/ref/heads/develop":   status(http.StatusNotFound, "Not Found"),
	})
	_, err := CommitFromCheckout(context.Background(), c, "acme/support",
		"fix/login", "develop", "msg", checkout, []string{"main.go"}, nil, workdir)
	if err == nil || !strings.Contains(err.Error(), "start branch") {
		t.Fatalf("a missing start branch must be named: %v", err)
	}
}

func TestCommitRefusesEmptyWork(t *testing.T) {
	workdir := t.TempDir()
	c, _ := serve(t, routes{
		"GET /repos/acme/support":                        jsonOK(`{"full_name":"acme/support","default_branch":"main"}`),
		"GET /repos/acme/support/git/ref/heads/fix/x":    status(http.StatusNotFound, "Not Found"),
		"GET /repos/acme/support/git/ref/heads/main":     jsonOK(`{"object":{"sha":"basecommit"}}`),
		"GET /repos/acme/support/git/commits/basecommit": jsonOK(`{"tree":{"sha":"basetree"}}`),
	})
	ctx := context.Background()
	for _, tc := range []struct{ branch, message, want string }{
		{"", "msg", "branch or message missing"},
		{"fix/x", "", "branch or message missing"},
	} {
		if _, err := CommitFromCheckout(ctx, c, "acme/support", tc.branch, "", tc.message,
			workdir, []string{"a.go"}, nil, workdir); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("branch=%q message=%q: %v", tc.branch, tc.message, err)
		}
	}
	if _, err := CommitFromCheckout(ctx, c, "acme/support", "fix/x", "", "msg",
		workdir, nil, nil, workdir); err == nil || !strings.Contains(err.Error(), "nothing to commit") {
		t.Errorf("a commit without files must be refused: %v", err)
	}
	// A file the checkout does not hold must not become an empty blob.
	if _, err := CommitFromCheckout(ctx, c, "acme/support", "fix/x", "", "msg",
		workdir, []string{"absent.go"}, nil, workdir); err == nil ||
		!strings.Contains(err.Error(), "read file") {
		t.Errorf("a missing file must be named: %v", err)
	}
}

// TestSandboxBoundActionsNeedASandbox: checkout, commit and download_attachment
// write into the sandbox. Outside one (in Control Plane context) they must
// refuse instead of writing into the daemon's own file system.
func TestSandboxBoundActionsNeedASandbox(t *testing.T) {
	c, _ := serve(t, routes{})
	ctx := context.Background()
	for _, name := range []string{"checkout", "commit", "download_attachment"} {
		_, err := actions[name](ctx, c, actionParams{
			Repo: "acme/support", Branch: "fix/x", Message: "m", Files: []string{"a.go"},
			URL: "https://github.com/user-attachments/assets/abc",
		})
		if err == nil || !strings.Contains(err.Error(), "sandbox") {
			t.Errorf("%s without a workdir: %v", name, err)
		}
	}
}

// -----------------------------------------------------------------------------
// Attachments
// -----------------------------------------------------------------------------

// TestAttachmentName: GitHub's asset links carry a bare id in the path, so the
// original name survives only in the Content-Disposition header.
func TestAttachmentName(t *testing.T) {
	u := mustParse(t, "https://github.com/user-attachments/assets/2f0c-abc")
	withCD := &http.Response{Header: http.Header{}}
	withCD.Header.Set("Content-Disposition", `inline; filename="screenshot login.png"`)
	if got := attachmentName(u, withCD); got != "screenshot login.png" {
		t.Errorf("the header name must win, got %q", got)
	}

	bare := &http.Response{Header: http.Header{}}
	if got := attachmentName(u, bare); got != "2f0c-abc" {
		t.Errorf("without the header the path base must serve, got %q", got)
	}

	root := mustParse(t, "https://raw.githubusercontent.com/")
	if got := attachmentName(root, bare); got != "attachment" {
		t.Errorf("without either there must be a fallback, got %q", got)
	}

	broken := &http.Response{Header: http.Header{}}
	broken.Header.Set("Content-Disposition", "this is not a media type ;;;")
	if got := attachmentName(u, broken); got != "2f0c-abc" {
		t.Errorf("an unparsable header must fall through, got %q", got)
	}
}

func mustParse(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return u
}

// -----------------------------------------------------------------------------
// The vocabulary of two systems
// -----------------------------------------------------------------------------

// TestStateVocabulary: agent playbooks are written across systems, so GitLab's
// words have to work here too. What must NOT happen is a silent fallback — see
// TestUnknownStateIsAnError.
func TestStateVocabulary(t *testing.T) {
	issue := map[string]string{
		"": "open", "open": "open", "opened": "open", " OPEN ": "open",
		"closed": "closed", "close": "closed", "all": "all",
	}
	for in, want := range issue {
		got, err := normalizeIssueState(in)
		if err != nil || got != want {
			t.Errorf("normalizeIssueState(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	pull := map[string]string{
		"": "open", "open": "open", "opened": "open",
		"closed": "closed", "close": "closed",
		// "merged" is not a state to GitHub — it is fetched as closed and
		// filtered on merged_at afterwards.
		"merged": "closed", "all": "all",
	}
	for in, want := range pull {
		got, err := normalizePullState(in)
		if err != nil || got != want {
			t.Errorf("normalizePullState(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

// TestWebhookKindCoversEveryShape: the event type stands in a header the plugin
// interface does not pass on, so it is derived from the payload's shape. Every
// shape GitHub sends has to land somewhere, and an unknown one must land in
// "other" rather than being read as something it is not.
func TestWebhookKindCoversEveryShape(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		want    string
	}{
		{"issue alone", `{"issue":{"number":1}}`, kindIssue},
		{"comment on an issue", `{"issue":{"number":1},"comment":{"id":2}}`, kindComment},
		{"comment on a diff line", `{"pull_request":{"number":1},"comment":{"id":2}}`, kindReviewComment},
		{"a submitted review", `{"pull_request":{"number":1},"review":{"id":3}}`, kindReview},
		{"the PR itself", `{"pull_request":{"number":1}}`, kindPull},
		{"something else entirely", `{"ref":"refs/heads/main"}`, kindOther},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var p WebhookPayload
			if err := json.Unmarshal([]byte(tc.payload), &p); err != nil {
				t.Fatal(err)
			}
			if got := p.Kind(); got != tc.want {
				t.Errorf("Kind() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestWebhookReviewCommentCorrelates: a comment on a line of the diff is the
// most concrete review feedback there is — it must reach the author.
func TestWebhookReviewCommentCorrelates(t *testing.T) {
	t.Setenv("COVEY_GITHUB_INTAKE_REPOS", "")
	t.Setenv("COVEY_GITHUB_BOT_LOGINS", "")
	p, err := ParseWebhook([]byte(`{"action":"created",
		"repository":{"full_name":"acme/support"},"sender":{"login":"qa-egon"},
		"pull_request":{"number":9,"title":"Fix login"},
		"comment":{"id":55,"body":"nil check missing","path":"internal/auth.go","user":{"login":"qa-egon"}}}`))
	if err != nil {
		t.Fatalf("ParseWebhook: %v", err)
	}
	ev := p.Event()
	if !ev.Wake || !ev.CorrelateOnly {
		t.Fatalf("a review comment must correlate: %+v", ev)
	}
	if ev.CorrelationKey != "github:pull:acme/support#9" {
		t.Errorf("correlation key = %q", ev.CorrelationKey)
	}
	if !strings.Contains(ev.ResumeInput, "internal/auth.go") ||
		!strings.Contains(ev.ResumeInput, "nil check missing") {
		t.Errorf("the resume input must carry the place AND the objection: %q", ev.ResumeInput)
	}
}

// TestWebhookIgnoresPullShapedIssues: GitHub sends a PR through the issue shape
// as well. Taken as an issue it would create a second task for work that is
// already a pull request.
func TestWebhookIgnoresPullShapedIssues(t *testing.T) {
	t.Setenv("COVEY_GITHUB_INTAKE_REPOS", "")
	p, err := ParseWebhook([]byte(`{"action":"opened",
		"repository":{"full_name":"acme/support"},"sender":{"login":"kunde"},
		"issue":{"number":9,"title":"Fix login","pull_request":{"html_url":"u"}}}`))
	if err != nil {
		t.Fatalf("ParseWebhook: %v", err)
	}
	if ev := p.Event(); ev.Wake {
		t.Errorf("a PR in issue clothing must not become an issue task: %+v", ev)
	}
}

// TestLastCommentIsMineFailsOpen: without a known identity the plugin cannot
// say whose voice was last. Answering "mine" would park the thread for good and
// nothing in the UI would say why.
func TestLastCommentIsMineFailsOpen(t *testing.T) {
	comments := []Comment{
		{ID: 1, CreatedAt: "2026-01-01T10:00:00Z", User: User{Login: "covey-bot"}},
	}
	if lastCommentIsMine(comments, "") {
		t.Error("an unknown own login must fail open")
	}
	if lastCommentIsMine(nil, "covey-bot") {
		t.Error("an empty thread is not answered — the first triage is outstanding")
	}
	if !lastCommentIsMine(comments, "COVEY-BOT") {
		t.Error("the login comparison must ignore case")
	}
	// Out of order, and a tie on the timestamp decided by the id: the newest
	// entry wins, not the last one in the slice.
	unordered := []Comment{
		{ID: 9, CreatedAt: "2026-01-03T10:00:00Z", User: User{Login: "kunde"}},
		{ID: 3, CreatedAt: "2026-01-01T10:00:00Z", User: User{Login: "covey-bot"}},
	}
	if lastCommentIsMine(unordered, "covey-bot") {
		t.Error("the NEWEST comment decides, not the position in the slice")
	}
	tie := []Comment{
		{ID: 1, CreatedAt: "2026-01-01T10:00:00Z", User: User{Login: "covey-bot"}},
		{ID: 2, CreatedAt: "2026-01-01T10:00:00Z", User: User{Login: "kunde"}},
	}
	if lastCommentIsMine(tie, "covey-bot") {
		t.Error("on an equal timestamp the higher id must decide")
	}
}

func TestSortCommentsIsStable(t *testing.T) {
	cs := []Comment{
		{ID: 5, CreatedAt: "2026-01-02T10:00:00Z"},
		{ID: 2, CreatedAt: "2026-01-01T10:00:00Z"},
		{ID: 1, CreatedAt: "2026-01-01T10:00:00Z"},
		{ID: 9, CreatedAt: "2026-01-03T10:00:00Z"},
	}
	sortComments(cs)
	var ids []int64
	for _, c := range cs {
		ids = append(ids, c.ID)
	}
	if fmt.Sprint(ids) != "[1 2 5 9]" {
		t.Errorf("chronological, ties by id: %v", ids)
	}
}

// TestPullReviewPendingCapsAndFilters: the check runs in every heartbeat
// interval. It must skip repositories outside the intake scope and must not
// turn a large working set into hundreds of requests.
func TestPullReviewPendingCapsAndFilters(t *testing.T) {
	t.Setenv("COVEY_GITHUB_INTAKE_REPOS", "acme/*")
	var items []map[string]any
	for i := 1; i <= pullMaxChecks+1; i++ {
		items = append(items, map[string]any{
			"number": i, "html_url": fmt.Sprintf("https://github.com/acme/support/pull/%d", i),
		})
	}
	// Two entries that must be skipped before any detail request: a foreign
	// repository and one whose URL does not yield a repo at all.
	items = append(items,
		map[string]any{"number": 500, "html_url": "https://github.com/foreign/repo/pull/1"},
		map[string]any{"number": 501, "html_url": "not-a-url"})

	details := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/api/v3")
		switch {
		case p == "/user":
			json.NewEncoder(w).Encode(User{Login: "covey-bot"})
		case p == "/search/issues":
			json.NewEncoder(w).Encode(map[string]any{"items": items})
		case strings.HasPrefix(p, "/repos/foreign/"):
			t.Errorf("a repository outside the scope must not be fetched: %q", p)
		case strings.HasSuffix(p, "/comments"), strings.HasSuffix(p, "/reviews"):
			w.Write([]byte(`[]`))
		case strings.HasPrefix(p, "/repos/acme/support/pulls/"):
			details++
			w.Write([]byte(`{"number":1,"head":{"sha":"aaaaaaaaaaaa"}}`))
		default:
			t.Errorf("unexpected path %q", p)
		}
	}))
	defer srv.Close()

	cred := target.Credential{BaseURL: srv.URL + "/api/v3", Token: "t"}
	has, sig, err := System{}.HasWorkSigned(context.Background(), cred, "pr")
	if err != nil || !has {
		t.Fatalf("a large working set must still wake: %v %t", err, has)
	}
	if !strings.Contains(sig, "pulls:many@") {
		t.Errorf("above the cap the signature must carry the count: %q", sig)
	}
	if details > pullMaxChecks+1 {
		t.Errorf("the cap must bite: %d detail requests", details)
	}
}

// -----------------------------------------------------------------------------
// The action layer
//
// The tests above mostly drive the client. These go through the action table —
// the layer the daemon actually calls — because that is where the intake filter,
// the parameter mapping and the answer shape live.
// -----------------------------------------------------------------------------

func TestReadingActionsGoThroughTheTable(t *testing.T) {
	t.Setenv("COVEY_GITHUB_INTAKE_REPOS", "acme/*")
	c, _ := serve(t, routes{
		"GET /issues": jsonOK(`[
			{"number":1,"title":"Login","repository":{"full_name":"acme/support"}},
			{"number":2,"title":"Foreign","repository":{"full_name":"foreign/repo"}}]`),
		"GET /repos/acme/support/issues/5/comments": jsonOK(`[{"id":1,"body":"help"}]`),
		"GET /repos/acme/support/contents/internal": jsonOK(`[{"path":"internal/a.go","type":"file","size":3}]`),
		"GET /repos/acme/support/contents/go.mod": jsonOK(
			`{"type":"file","encoding":"base64","size":3,"content":"eA=="}`),
		"GET /repos/acme/support/branches":          jsonOK(`[{"name":"main","commit":{"sha":"m"}}]`),
		"GET /repos/acme/support":                   jsonOK(`{"full_name":"acme/support","default_branch":"main"}`),
		"GET /repos/acme/support/commits":           jsonOK(`[{"sha":"a1","commit":{"message":"m"}}]`),
		"GET /repos/acme/support/commits/a1":        jsonOK(`{"files":[{"filename":"a.go","status":"modified"}]}`),
		"GET /repos/acme/support/pulls":             jsonOK(`[{"number":9,"title":"Fix"}]`),
		"GET /repos/acme/support/issues/9/comments": jsonOK(`[{"id":2,"body":"lgtm"}]`),
		"GET /repos/acme/support/pulls/9/reviews":   jsonOK(`[]`),
		"GET /repos/acme/support/pulls/9/comments":  jsonOK(`[]`),
	})

	// list_issues without a repo goes through the global endpoint — and the
	// intake filter has to apply on THIS path too, not only in the client.
	issues := run(t, c, "list_issues", actionParams{State: "open"}).([]Issue)
	if len(issues) != 1 || issues[0].Repo != "acme/support" {
		t.Fatalf("the allowlist must filter the action's result: %+v", issues)
	}

	if cs := run(t, c, "list_comments", actionParams{Repo: "acme/support", IssueNumber: 5}).([]Comment); len(cs) != 1 {
		t.Errorf("list_comments: %+v", cs)
	}
	if tr := run(t, c, "list_tree", actionParams{Repo: "acme/support", Path: "internal"}).([]TreeEntry); len(tr) != 1 {
		t.Errorf("list_tree: %+v", tr)
	}
	file := run(t, c, "read_file", actionParams{Repo: "acme/support", FilePath: "go.mod"}).(map[string]any)
	if file["content"] != "x" || file["truncated"] != false {
		t.Errorf("read_file must answer with content and the truncation flag: %+v", file)
	}
	if bs := run(t, c, "list_branches", actionParams{Repo: "acme/support"}).([]Branch); len(bs) != 1 || !bs[0].Default {
		t.Errorf("list_branches: %+v", bs)
	}
	if cm := run(t, c, "list_commits", actionParams{Repo: "acme/support"}).([]Commit); len(cm) != 1 {
		t.Errorf("list_commits: %+v", cm)
	}
	if d := run(t, c, "get_commit", actionParams{Repo: "acme/support", SHA: "a1"}).([]CommitDiff); len(d) != 1 {
		t.Errorf("get_commit: %+v", d)
	}
	if prs := run(t, c, "list_pull_requests", actionParams{Repo: "acme/support"}).([]PullRequest); len(prs) != 1 {
		t.Errorf("list_pull_requests: %+v", prs)
	}
	if cs := run(t, c, "list_pr_comments", actionParams{Repo: "acme/support", PRNumber: 9}).([]Comment); len(cs) != 1 {
		t.Errorf("list_pr_comments: %+v", cs)
	}
}

func TestWritingActionsGoThroughTheTable(t *testing.T) {
	var commented map[string]any
	c, _ := serve(t, routes{
		"GET /user": jsonOK(`{"login":"covey-bot"}`),
		"GET /repos/acme/support/issues/9/comments": jsonOK(`[{"id":1,"body":"earlier","user":{"login":"kunde"},"created_at":"2026-01-01T10:00:00Z"}]`),
		"GET /repos/acme/support/pulls/9/reviews":   jsonOK(`[]`),
		"GET /repos/acme/support/pulls/9/comments":  jsonOK(`[]`),
		"POST /repos/acme/support/issues/9/comments": func(w http.ResponseWriter, r *http.Request) {
			json.NewDecoder(r.Body).Decode(&commented)
			w.Write([]byte(`{"id":2}`))
		},
		"POST /repos/acme/support/issues/7/labels":      jsonOK(`[{"name":"in-progress"}]`),
		"POST /repos/acme/support/issues/7/comments":    jsonOK(`{"id":3}`),
		"DELETE /repos/acme/support/issues/7/assignees": jsonOK(`{}`),
	})

	// comment_pr runs over the PR's whole conversation for its duplicate check
	// and then posts through the issue comments endpoint.
	run(t, c, "comment_pr", actionParams{Repo: "acme/support", PRNumber: 9, Body: "worked the feedback in"})
	if commented["body"] != "worked the feedback in" {
		t.Errorf("comment_pr must post: %+v", commented)
	}

	labels := run(t, c, "set_labels", actionParams{
		Repo: "acme/support", IssueNumber: 7, AddLabels: []string{"in-progress"},
	}).(map[string]any)
	if fmt.Sprint(labels["labels"]) != "[in-progress]" {
		t.Errorf("set_labels must answer with the state reached: %+v", labels)
	}

	// escalate without a note must still say something — a bare handover with
	// no explanation is what the default text exists for.
	if out := run(t, c, "escalate", actionParams{Repo: "acme/support", IssueNumber: 7}).(map[string]any); out["escalated"] != true {
		t.Errorf("escalate: %+v", out)
	}
}

// TestListIssuesAssignedScope: an agent whose playbook works only on its own
// issues needs the assignee filter to reach GitHub — both on the global
// endpoint (filter=assigned) and on a single repository (assignee=<login>),
// which needs the who-am-I lookup first.
func TestListIssuesAssignedScope(t *testing.T) {
	t.Setenv("COVEY_GITHUB_INTAKE_REPOS", "")
	var lastQuery url.Values
	c, _ := serve(t, routes{
		"GET /user": jsonOK(`{"login":"covey-bot"}`),
		"GET /issues": func(w http.ResponseWriter, r *http.Request) {
			lastQuery = r.URL.Query()
			w.Write([]byte(`[]`))
		},
		"GET /repos/acme/support/issues": func(w http.ResponseWriter, r *http.Request) {
			lastQuery = r.URL.Query()
			w.Write([]byte(`[]`))
		},
	})
	ctx := context.Background()

	c.ListIssues(ctx, "", "open", "", "", "", true)
	if lastQuery.Get("filter") != "assigned" {
		t.Errorf("the global endpoint must narrow to assigned: %v", lastQuery)
	}
	c.ListIssues(ctx, "", "open", "", "", "", false)
	if lastQuery.Get("filter") != "all" {
		t.Errorf("without the flag everything visible: %v", lastQuery)
	}
	c.ListIssues(ctx, "acme/support", "open", "bug,ui", "", "", true)
	if lastQuery.Get("assignee") != "covey-bot" {
		t.Errorf("the repository endpoint must name the bot: %v", lastQuery)
	}
	if lastQuery.Get("labels") != "bug,ui" {
		t.Errorf("the label filter must reach GitHub: %v", lastQuery)
	}
}

// TestHasWorkAssignedSubScope covers nur-wenn: github:issues:assigned — the
// gate an agent needs whose playbook only touches its own issues. Without it
// every open issue of somebody else's would start the agent.
func TestHasWorkAssignedSubScope(t *testing.T) {
	t.Setenv("COVEY_GITHUB_INTAKE_REPOS", "")
	var filter string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch strings.TrimPrefix(r.URL.Path, "/api/v3") {
		case "/user":
			json.NewEncoder(w).Encode(User{Login: "covey-bot"})
		case "/issues":
			filter = r.URL.Query().Get("filter")
			w.Write([]byte(`[]`))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	defer srv.Close()

	cred := target.Credential{BaseURL: srv.URL + "/api/v3", Token: "t"}
	for _, kind := range []string{"issues:assigned", "issue:assigned", "assigned"} {
		filter = ""
		has, sig, err := System{}.HasWorkSigned(context.Background(), cred, kind)
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		if filter != "assigned" {
			t.Errorf("%s must gate on the assigned issues, got filter=%q", kind, filter)
		}
		// Nothing assigned → no work, and an empty signature so the gate does
		// not carry a stale state forward.
		if has || sig != "" {
			t.Errorf("%s: nothing assigned must mean no work: %t %q", kind, has, sig)
		}
	}
}

// TestPullReviewPendingRestsWhenTheBotWroteLast: the author's own view. Once
// the bot has answered the feedback, the PR is somebody else's turn — waking on
// it again every interval is the loop this gate exists to prevent.
func TestPullReviewPendingRestsWhenTheBotWroteLast(t *testing.T) {
	t.Setenv("COVEY_GITHUB_INTAKE_REPOS", "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch p := strings.TrimPrefix(r.URL.Path, "/api/v3"); {
		case p == "/user":
			json.NewEncoder(w).Encode(User{Login: "covey-bot"})
		case p == "/search/issues":
			if !strings.Contains(r.URL.Query().Get("q"), "author:@me") {
				t.Errorf("the pr scope must search for one's OWN PRs: %q", r.URL.RawQuery)
			}
			w.Write([]byte(`{"items":[{"number":9,"html_url":"https://github.com/acme/support/pull/9"}]}`))
		case p == "/repos/acme/support/pulls/9":
			w.Write([]byte(`{"number":9,"head":{"sha":"aaaaaaaaaaaa"}}`))
		case p == "/repos/acme/support/issues/9/comments":
			w.Write([]byte(`[
				{"id":1,"created_at":"2026-01-01T10:00:00Z","user":{"login":"qa-egon"}},
				{"id":2,"created_at":"2026-01-02T10:00:00Z","user":{"login":"covey-bot"}}]`))
		case strings.HasSuffix(p, "/reviews"), strings.HasSuffix(p, "/pulls/9/comments"):
			w.Write([]byte(`[]`))
		default:
			t.Errorf("unexpected path %q", p)
		}
	}))
	defer srv.Close()

	cred := target.Credential{BaseURL: srv.URL + "/api/v3", Token: "t"}
	has, sig, err := System{}.HasWorkSigned(context.Background(), cred, "pr")
	if err != nil {
		t.Fatalf("HasWorkSigned: %v", err)
	}
	if has || sig != "" {
		t.Errorf("a PR the bot answered last must rest: %t %q", has, sig)
	}
}

// TestHasWorkWithNothingOpen: an empty working set must produce no work and no
// signature — a signature over nothing would be a state the gate carries
// forward for no reason.
func TestHasWorkWithNothingOpen(t *testing.T) {
	t.Setenv("COVEY_GITHUB_INTAKE_REPOS", "acme/*")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch strings.TrimPrefix(r.URL.Path, "/api/v3") {
		case "/issues":
			// Everything visible lies outside the intake scope.
			w.Write([]byte(`[{"number":1,"repository":{"full_name":"foreign/repo"}}]`))
		case "/search/issues":
			w.Write([]byte(`{"items":[]}`))
		case "/user":
			json.NewEncoder(w).Encode(User{Login: "covey-bot"})
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	defer srv.Close()

	cred := target.Credential{BaseURL: srv.URL + "/api/v3", Token: "t"}
	has, sig, err := System{}.HasWorkSigned(context.Background(), cred, "")
	if err != nil {
		t.Fatalf("HasWorkSigned: %v", err)
	}
	if has || sig != "" {
		t.Errorf("nothing in scope must mean no work: %t %q", has, sig)
	}
}

// -----------------------------------------------------------------------------
// Caps, refs and empty answers
// -----------------------------------------------------------------------------

// TestListTreeCapsBothRoutes: a whole repository tree does not belong in an
// agent's context. Both routes stop at the cap.
func TestListTreeCapsBothRoutes(t *testing.T) {
	var flat, deep []map[string]any
	for i := range perPage + 20 {
		flat = append(flat, map[string]any{"path": fmt.Sprintf("f%d.go", i), "type": "file"})
		deep = append(deep, map[string]any{"path": fmt.Sprintf("src/f%d.go", i), "type": "blob"})
	}
	c, _ := serve(t, routes{
		"GET /repos/acme/support/contents/": func(w http.ResponseWriter, _ *http.Request) {
			json.NewEncoder(w).Encode(flat)
		},
		"GET /repos/acme/support/git/trees/main": func(w http.ResponseWriter, _ *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{"truncated": true, "tree": deep})
		},
	})
	ctx := context.Background()

	got, err := c.ListTree(ctx, "acme/support", "", "", false)
	if err != nil || len(got) != perPage {
		t.Fatalf("the contents route must cap at %d: %v %d", perPage, err, len(got))
	}
	got, err = c.ListTree(ctx, "acme/support", "src", "main", true)
	if err != nil || len(got) != perPage {
		t.Fatalf("the trees route must cap at %d: %v %d", perPage, err, len(got))
	}
}

// TestRefReachesTheContentsAPI: reading a file or a directory at a BRANCH is
// how the QA agent looks at a PR without checking it out. A dropped ref would
// silently answer from the default branch.
func TestRefReachesTheContentsAPI(t *testing.T) {
	var treeRef, fileRef string
	c, _ := serve(t, routes{
		"GET /repos/acme/support/contents/internal": func(w http.ResponseWriter, r *http.Request) {
			treeRef = r.URL.Query().Get("ref")
			w.Write([]byte(`[]`))
		},
		"GET /repos/acme/support/contents/go.mod": func(w http.ResponseWriter, r *http.Request) {
			fileRef = r.URL.Query().Get("ref")
			w.Write([]byte(`{"type":"file","encoding":"base64","content":"eA=="}`))
		},
	})
	ctx := context.Background()
	c.ListTree(ctx, "acme/support", "internal", "fix/login", false)
	c.ReadFile(ctx, "acme/support", "go.mod", "fix/login")
	if treeRef != "fix/login" || fileRef != "fix/login" {
		t.Errorf("the ref must travel: tree=%q file=%q", treeRef, fileRef)
	}
}

func TestReadFileRejectsUndecodableContent(t *testing.T) {
	c, _ := serve(t, routes{
		"GET /repos/acme/support/contents/broken": jsonOK(
			`{"type":"file","encoding":"base64","size":4,"content":"!!!not base64!!!"}`),
	})
	if _, _, err := c.ReadFile(context.Background(), "acme/support", "broken", ""); err == nil ||
		!strings.Contains(err.Error(), "decode") {
		t.Fatalf("undecodable content must be an error: %v", err)
	}
}

// TestListPullsFiltersBaseAndSearch: base narrows GitHub-side, search narrows
// the answer — an agent looking for "login" must not get the export PR back.
func TestListPullsFiltersBaseAndSearch(t *testing.T) {
	var base string
	c, _ := serve(t, routes{
		"GET /repos/acme/support/pulls": func(w http.ResponseWriter, r *http.Request) {
			base = r.URL.Query().Get("base")
			w.Write([]byte(`[{"number":9,"title":"Fix login"},{"number":10,"title":"CSV export"}]`))
		},
	})
	prs, err := c.ListPulls(context.Background(), "acme/support", "open", "login", "main")
	if err != nil || len(prs) != 1 || prs[0].Number != 9 {
		t.Fatalf("search must filter the answer: %v %+v", err, prs)
	}
	if base != "main" {
		t.Errorf("base must reach GitHub, got %q", base)
	}
}

// TestEmptyCIAnswersAreLists: an agent that gets `null` back where it expects a
// list has to special-case it. A repository without Actions is the normal case,
// not an exception.
func TestEmptyCIAnswersAreLists(t *testing.T) {
	c, _ := serve(t, routes{
		"GET /repos/acme/support/actions/runs":           jsonOK(`{"total_count":0}`),
		"GET /repos/acme/support/actions/runs/77/jobs":   jsonOK(`{"total_count":0}`),
		"GET /repos/acme/support/commits/abc/check-runs": jsonOK(`{"total_count":0}`),
	})
	ctx := context.Background()
	if runs, err := c.ListWorkflowRuns(ctx, "acme/support", ""); err != nil || runs == nil || len(runs) != 0 {
		t.Errorf("ListWorkflowRuns must answer with an empty list: %v %#v", err, runs)
	}
	if jobs, err := c.ListRunJobs(ctx, "acme/support", 77); err != nil || jobs == nil || len(jobs) != 0 {
		t.Errorf("ListRunJobs must answer with an empty list: %v %#v", err, jobs)
	}
	if checks, err := c.ListCheckRuns(ctx, "acme/support", "abc"); err != nil || checks == nil || len(checks) != 0 {
		t.Errorf("ListCheckRuns must answer with an empty list: %v %#v", err, checks)
	}
}

// TestCommitSizeLimits: the blobs travel through the JSON API base64-encoded.
// Huge commits do not belong on this route, and the agent has to be told which
// of the two limits it hit.
func TestCommitSizeLimits(t *testing.T) {
	workdir := t.TempDir()
	checkout := filepath.Join(workdir, "repos", "acme-support-main")
	os.MkdirAll(checkout, 0o755)

	c, _ := serve(t, routes{
		"GET /repos/acme/support":                       jsonOK(`{"full_name":"acme/support","default_branch":"main"}`),
		"GET /repos/acme/support/git/ref/heads/fix/big": status(http.StatusNotFound, "Not Found"),
		"GET /repos/acme/support/git/ref/heads/main":    jsonOK(`{"object":{"sha":"base"}}`),
		"GET /repos/acme/support/git/commits/base":      jsonOK(`{"tree":{"sha":"t"}}`),
		"POST /repos/acme/support/git/blobs":            jsonOK(`{"sha":"b"}`),
	})
	ctx := context.Background()

	// One file past the per-file limit.
	os.WriteFile(filepath.Join(checkout, "huge.bin"), make([]byte, maxCommitFileBytes+1), 0o644)
	_, err := CommitFromCheckout(ctx, c, "acme/support", "fix/big", "", "msg", checkout,
		[]string{"huge.bin"}, nil, workdir)
	if err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("the per-file limit must bite: %v", err)
	}

	// Several files that together pass the commit limit.
	var files []string
	chunk := make([]byte, maxCommitFileBytes)
	for i := range (maxCommitTotalBytes / maxCommitFileBytes) + 1 {
		name := fmt.Sprintf("part%d.bin", i)
		os.WriteFile(filepath.Join(checkout, name), chunk, 0o644)
		files = append(files, name)
	}
	_, err = CommitFromCheckout(ctx, c, "acme/support", "fix/big", "", "msg", checkout, files, nil, workdir)
	if err == nil || !strings.Contains(err.Error(), "split the change") {
		t.Fatalf("the commit limit must bite and say what to do: %v", err)
	}
}

func TestRepoInScopeWithoutASlash(t *testing.T) {
	t.Setenv("COVEY_GITHUB_INTAKE_REPOS", "acme/*")
	// A name that is not "owner/name" cannot match an owner rule — it must fall
	// out rather than being read as an owner of its own.
	if repoInScope("support") {
		t.Error("a name without an owner must not pass the allowlist")
	}
	if repoInScope("") {
		t.Error("an empty name must not pass the allowlist")
	}
}

// TestWebhookReviewWithoutAPullRequest: GitHub always sends the PR with a
// review, but a malformed or truncated delivery must not panic or invent a
// correlation key pointing at pull request 0.
func TestWebhookReviewWithoutAPullRequest(t *testing.T) {
	t.Setenv("COVEY_GITHUB_INTAKE_REPOS", "")
	p, err := ParseWebhook([]byte(`{"action":"submitted",
		"repository":{"full_name":"acme/support"},"sender":{"login":"qa"},
		"review":{"id":3,"state":"APPROVED"}}`))
	if err != nil {
		t.Fatalf("ParseWebhook: %v", err)
	}
	ev := p.Event()
	if ev.Wake || ev.CorrelationKey != "" {
		t.Errorf("a review without its PR must wake nothing: %+v", ev)
	}
	if ev.DedupKey == "" {
		t.Error("the delivery must still be deduplicated")
	}
}

// -----------------------------------------------------------------------------
// The last few branches: things that must NOT be swallowed
// -----------------------------------------------------------------------------

// TestSetLabelsPropagatesRealFailures: a label that is not set is fine to
// remove (404). Anything else — no permission, the repository gone — is a real
// failure, and swallowing it would leave the board showing a state the agent
// never reached.
func TestSetLabelsPropagatesRealFailures(t *testing.T) {
	c, _ := serve(t, routes{
		"DELETE /repos/acme/support/issues/7/labels/triage": status(http.StatusForbidden, "no permission"),
	})
	if _, err := c.SetLabels(context.Background(), "acme/support", 7, nil, []string{"triage"}); err == nil ||
		!strings.Contains(err.Error(), "403") {
		t.Fatalf("a genuine failure must propagate: %v", err)
	}
}

// TestDuplicateCheckFailsOpen: the loop protection compares against the bot's
// own last comment. If the who-am-I lookup breaks, the comment must be posted
// as usual — no legitimate answer may be blocked by a broken check.
func TestDuplicateCheckFailsOpen(t *testing.T) {
	posted := 0
	c, _ := serve(t, routes{
		"GET /user": status(http.StatusUnauthorized, "Bad credentials"),
		"GET /repos/acme/support/issues/5/comments": jsonOK(
			`[{"id":1,"body":"same text","user":{"login":"covey-bot"},"created_at":"2026-01-01T10:00:00Z"}]`),
		"POST /repos/acme/support/issues/5/comments": func(w http.ResponseWriter, _ *http.Request) {
			posted++
			w.Write([]byte(`{"id":2}`))
		},
	})
	run(t, c, "comment", actionParams{Repo: "acme/support", IssueNumber: 5, Body: "same text"})
	if posted != 1 {
		t.Error("without a usable identity the comment must go out — fail open")
	}
}

// TestCommentPRSuppressesDuplicate: the review dialogue needs the same brake as
// the issue thread, and it has to see the WHOLE conversation to apply it — a
// repeated answer would otherwise wake the reviewer for nothing.
func TestCommentPRSuppressesDuplicate(t *testing.T) {
	posted := 0
	c, _ := serve(t, routes{
		"GET /user": jsonOK(`{"login":"covey-bot"}`),
		"GET /repos/acme/support/issues/9/comments": jsonOK(`[]`),
		// The bot's last word was a REVIEW, not an ordinary comment — the
		// duplicate check only sees it because the three kinds are merged.
		"GET /repos/acme/support/pulls/9/reviews": jsonOK(
			`[{"id":7,"state":"COMMENTED","body":"worked it in","submitted_at":"2026-01-02T10:00:00Z","user":{"login":"covey-bot"}}]`),
		"GET /repos/acme/support/pulls/9/comments": jsonOK(`[]`),
		"POST /repos/acme/support/issues/9/comments": func(w http.ResponseWriter, _ *http.Request) {
			posted++
			w.Write([]byte(`{"id":8}`))
		},
	})
	res := run(t, c, "comment_pr", actionParams{Repo: "acme/support", PRNumber: 9, Body: "worked it in"})
	if m, _ := res.(map[string]any); m["skipped"] != "duplicate" {
		t.Errorf("the repetition must be suppressed: %+v", res)
	}
	if posted != 0 {
		t.Error("nothing may be posted for a duplicate")
	}
}

// TestListIssuesActionValidatesTheRepo: with a repo given, the action has to
// check it before the request — the same guard every other action gets.
func TestListIssuesActionValidatesTheRepo(t *testing.T) {
	t.Setenv("COVEY_GITHUB_INTAKE_REPOS", "")
	c, _ := serve(t, routes{
		"GET /repos/acme/support/issues": jsonOK(`[{"number":1,"title":"Login"}]`),
	})
	if issues := run(t, c, "list_issues", actionParams{Repo: "acme/support"}).([]Issue); len(issues) != 1 {
		t.Errorf("list_issues with a repo: %+v", issues)
	}
	if err := runErr(t, c, "list_issues", actionParams{Repo: "not-a-repo"}); !strings.Contains(err.Error(), "owner/name") {
		t.Errorf("an invalid repo must be refused before the request: %v", err)
	}
}

// TestPullReviewPendingSkipsWhatItCannotPlace: a hit whose repository lies
// outside the intake scope, or whose URL yields no repository at all, must be
// skipped BEFORE the detail request — not fetched and then discarded.
func TestPullReviewPendingSkipsWhatItCannotPlace(t *testing.T) {
	t.Setenv("COVEY_GITHUB_INTAKE_REPOS", "acme/*")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch p := strings.TrimPrefix(r.URL.Path, "/api/v3"); {
		case p == "/user":
			json.NewEncoder(w).Encode(User{Login: "covey-bot"})
		case p == "/search/issues":
			w.Write([]byte(`{"items":[
				{"number":1,"html_url":"https://github.com/foreign/repo/pull/1"},
				{"number":2,"html_url":"nonsense"}]}`))
		default:
			t.Errorf("nothing may be fetched in detail: %q", p)
		}
	}))
	defer srv.Close()

	cred := target.Credential{BaseURL: srv.URL + "/api/v3", Token: "t"}
	has, _, err := System{}.HasWorkSigned(context.Background(), cred, "pr")
	if err != nil {
		t.Fatalf("HasWorkSigned: %v", err)
	}
	if has {
		t.Error("nothing placeable must mean no work")
	}
}

// TestCreatePullRequestReportsAnUnknownReviewer: the PR is already open at this
// point. An unknown reviewer login must be REPORTED, not turned into an error
// that hides a pull request which exists.
func TestCreatePullRequestReportsAnUnknownReviewer(t *testing.T) {
	var created, assigned, reviewers map[string]any
	rs := prRoutes(t, &created, &assigned, &reviewers)
	rs["GET /users/ghost"] = status(http.StatusNotFound, "Not Found")
	delete(rs, "POST /repos/acme/support/pulls/9/requested_reviewers")
	c, _ := serve(t, rs)

	out := run(t, c, "create_pull_request", actionParams{
		Repo: "acme/support", Head: "fix/login", Title: "Fix login",
		Assignee: "kunde", Reviewer: "ghost",
	}).(map[string]any)

	if out["pull_request"] == nil {
		t.Fatal("the PR exists — it must come back")
	}
	if out["reviewer_error"] == nil || !strings.Contains(fmt.Sprint(out["reviewer_error"]), "ghost") {
		t.Errorf("the unknown reviewer must be named: %+v", out)
	}
	if out["assignee"] != "kunde" {
		t.Errorf("the assignee must have been entered all the same: %+v", out)
	}
}
