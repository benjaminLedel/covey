package gitlab

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"covey/internal/target"
)

// serveNotes answers a notes request the way GitLab does: sort=desc delivers
// the NEWEST first, per_page/page cut one window out of it, and the pagination
// headers state the size of the whole. Without per_page GitLab falls back to 20
// — precisely the default that used to leave agents with the twenty OLDEST
// comments of a ticket.
//
// The fake servers used to hand out the whole array and thereby hid exactly the
// behaviour that matters here.
func serveNotes(w http.ResponseWriter, r *http.Request, notes []Note) {
	q := r.URL.Query()
	all := append([]Note(nil), notes...) // as the test wrote them: chronological
	if q.Get("sort") == "desc" {
		for i, j := 0, len(all)-1; i < j; i, j = i+1, j-1 {
			all[i], all[j] = all[j], all[i]
		}
	}
	perPage, _ := strconv.Atoi(q.Get("per_page"))
	if perPage <= 0 {
		perPage = 20
	}
	page, _ := strconv.Atoi(q.Get("page"))
	if page <= 0 {
		page = 1
	}
	pages := (len(all) + perPage - 1) / perPage
	von := (page - 1) * perPage
	if von > len(all) {
		von = len(all)
	}
	bis := von + perPage
	if bis > len(all) {
		bis = len(all)
	}
	w.Header().Set("X-Total", strconv.Itoa(len(all)))
	w.Header().Set("X-Total-Pages", strconv.Itoa(pages))
	if page < pages {
		w.Header().Set("X-Next-Page", strconv.Itoa(page+1))
	}
	json.NewEncoder(w).Encode(all[von:bis])
}

// TestProjectInScope checks the intake filter (COVEY_GITLAB_INTAKE_PROJECTS),
// which without a webhook only bounds the discovery actions
// (list_issues/list_projects) and the nur-wenn: pre-check (HasWork).
func TestProjectInScope(t *testing.T) {
	if !projectInScope(15, "Gruppe/Support") {
		t.Fatal("without an allowlist all projects are in scope")
	}
	t.Setenv("COVEY_GITLAB_INTAKE_PROJECTS", "gruppe/support")
	if !projectInScope(15, "Gruppe/Support") {
		t.Fatal("the project path comparison must be case-insensitive")
	}
	t.Setenv("COVEY_GITLAB_INTAKE_PROJECTS", "15")
	if !projectInScope(15, "gruppe/support") {
		t.Fatal("the numeric project id must match")
	}
	t.Setenv("COVEY_GITLAB_INTAKE_PROJECTS", "anderes/projekt")
	if projectInScope(15, "gruppe/support") {
		t.Fatal("a project outside the allowlist must not be in scope")
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
			serveNotes(w, r, []Note{{ID: 1, Body: "Hilfe"}})
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
		t.Fatalf("PRIVATE-TOKEN header wrong: %q", gotAuth)
	}

	p, err := c.ListNotes(ctx, 15, 23, 0, 0)
	if err != nil || len(p.Notes) != 1 {
		t.Fatalf("ListNotes: %v %+v", err, p)
	}

	if _, err := c.Comment(ctx, 15, 23, "Bitte Screenshot schicken", false); err != nil {
		t.Fatalf("Comment: %v", err)
	}
	if gotBody["internal"] != false || gotBody["body"] != "Bitte Screenshot schicken" {
		t.Fatalf("comment body wrong: %+v", gotBody)
	}

	if err := c.SetState(ctx, 15, 23, "close"); err != nil {
		t.Fatalf("SetState: %v", err)
	}
	if gotMethod != http.MethodPut || gotPath != "/api/v4/projects/15/issues/23" {
		t.Fatalf("SetState must be PUT /projects/15/issues/23: %s %s", gotMethod, gotPath)
	}
	if gotBody["state_event"] != "close" {
		t.Fatalf("SetState body wrong: %+v", gotBody)
	}
	if err := c.SetState(ctx, 15, 23, "opened"); err == nil {
		t.Fatal("an invalid state_event must be refused")
	}

	if err := c.Escalate(ctx, 15, 23, "Bitte übernehmen"); err != nil {
		t.Fatalf("Escalate: %v", err)
	}
	if gotMethod != http.MethodPut || gotBody["assignee_ids"] == nil {
		t.Fatalf("Escalate must remove the assignment: %s %+v", gotMethod, gotBody)
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
		t.Fatalf("ListProjects must filter on membership: %s", gotQuery)
	}

	issues, err := c.ListIssues(ctx, 15, "", "", "", "", false)
	if err != nil || len(issues) != 1 || issues[0].IID != 23 {
		t.Fatalf("ListIssues (project): %v %+v", err, issues)
	}
	if gotPath != "/api/v4/projects/15/issues" || !strings.Contains(gotQuery, "state=opened") {
		t.Fatalf("ListIssues must run project-scoped and with the default state=opened: %s?%s", gotPath, gotQuery)
	}

	if _, err := c.ListIssues(ctx, 0, "all", "bug,support", "login", "", false); err != nil {
		t.Fatalf("ListIssues (global): %v", err)
	}
	if gotPath != "/api/v4/issues" || !strings.Contains(gotQuery, "scope=all") {
		t.Fatalf("without project_id the global /issues with scope=all must run: %s?%s", gotPath, gotQuery)
	}
	if strings.Contains(gotQuery, "state=") {
		t.Fatalf("state=all must send no state parameter: %s", gotQuery)
	}
	if !strings.Contains(gotQuery, "labels=bug%2Csupport") || !strings.Contains(gotQuery, "search=login") {
		t.Fatalf("labels/search must be passed through: %s", gotQuery)
	}

	if _, err := c.ListIssues(ctx, 0, "", "", "", "", true); err != nil {
		t.Fatalf("ListIssues (assigned, global): %v", err)
	}
	if !strings.Contains(gotQuery, "scope=assigned_to_me") || strings.Contains(gotQuery, "scope=all") {
		t.Fatalf("assigned=true must send scope=assigned_to_me instead of scope=all: %s", gotQuery)
	}
	if _, err := c.ListIssues(ctx, 15, "", "", "", "", true); err != nil {
		t.Fatalf("ListIssues (assigned, project): %v", err)
	}
	if gotPath != "/api/v4/projects/15/issues" || !strings.Contains(gotQuery, "scope=assigned_to_me") {
		t.Fatalf("assigned=true must send scope=assigned_to_me project-scoped too: %s?%s", gotPath, gotQuery)
	}

	// The milestone: the filter with which an agent grasps a whole undertaking.
	if _, err := c.ListIssues(ctx, 15, "", "", "", "ECA-2026-045 Bundesdruckerei LMS", false); err != nil {
		t.Fatalf("ListIssues (milestone): %v", err)
	}
	if !strings.Contains(gotQuery, "milestone=ECA-2026-045+Bundesdruckerei+LMS") {
		t.Fatalf("milestone must be passed through: %s", gotQuery)
	}
	if _, err := c.ListIssues(ctx, 15, "", "", "", "", false); err != nil {
		t.Fatalf("ListIssues (without milestone): %v", err)
	}
	if strings.Contains(gotQuery, "milestone=") {
		t.Fatalf("an empty milestone must send no parameter: %s", gotQuery)
	}
}

// The milestone must arrive at the issue — an agent running an undertaking
// decides by it what belongs to its assignment. GitLab returns null when none
// is set; that must not tip over into an empty title.
func TestIssueCarriesMilestone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"iid":739,"project_id":15,"milestone":{"title":"ECA-2026-045 Bundesdruckerei LMS","due_date":"2026-11-30","state":"active"}},
		                 {"iid":740,"project_id":15,"milestone":null}]`))
	}))
	defer srv.Close()

	issues, err := NewClient(srv.URL, "t").ListIssues(context.Background(), 15, "", "", "", "", false)
	if err != nil || len(issues) != 2 {
		t.Fatalf("ListIssues: %v %+v", err, issues)
	}
	if issues[0].Milestone == nil || issues[0].Milestone.Title != "ECA-2026-045 Bundesdruckerei LMS" {
		t.Fatalf("the milestone must arrive at the issue: %+v", issues[0].Milestone)
	}
	if issues[0].Milestone.DueDate != "2026-11-30" {
		t.Fatalf("the milestone's due date is missing: %+v", issues[0].Milestone)
	}
	if issues[1].Milestone != nil {
		t.Fatalf("without a milestone the field must stay nil: %+v", issues[1].Milestone)
	}
}

// set_labels maintains the working state on the board. What is decisive is that
// it works PARTIALLY (add_labels/remove_labels) instead of overwriting the
// label list — otherwise every state change takes the subject-matter labels
// along with it.
func TestSetLabelsIsPartial(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		gotBody = map[string]any{}
		json.NewDecoder(r.Body).Decode(&gotBody)
		json.NewEncoder(w).Encode(Issue{IID: 739, ProjectID: 15,
			Labels: []string{"ECA-2026-045", "MUSS-Kriterium", "in Arbeit"}})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "test-token")
	ctx := context.Background()

	iss, err := c.SetLabels(ctx, 15, 739, []string{"in Arbeit"}, []string{"bereit", ""})
	if err != nil {
		t.Fatalf("SetLabels: %v", err)
	}
	if gotMethod != http.MethodPut || gotPath != "/api/v4/projects/15/issues/739" {
		t.Fatalf("SetLabels must change the issue by PUT: %s %s", gotMethod, gotPath)
	}
	if gotBody["add_labels"] != "in Arbeit" || gotBody["remove_labels"] != "bereit" {
		t.Fatalf("add/remove must go separately and without empty entries: %+v", gotBody)
	}
	if gotBody["labels"] != nil {
		t.Fatalf("the full labels list must NOT be overwritten: %+v", gotBody)
	}
	if len(iss.Labels) != 3 {
		t.Fatalf("the label state reached must come back: %+v", iss.Labels)
	}

	// Removing only is allowed, giving nothing at all is not — otherwise an
	// incomplete call sends an ineffective PUT to GitLab.
	if _, err := c.SetLabels(ctx, 15, 739, nil, []string{"bereit"}); err != nil {
		t.Fatalf("remove_labels alone must be allowed: %v", err)
	}
	if gotBody["add_labels"] != nil {
		t.Fatalf("without add_labels the field must not be sent along: %+v", gotBody)
	}
	if _, err := c.SetLabels(ctx, 15, 739, nil, nil); err == nil {
		t.Fatal("without add_labels and remove_labels SetLabels must be refused")
	}
	if _, err := c.SetLabels(ctx, 15, 739, []string{"  "}, nil); err == nil {
		t.Fatal("whitespace alone is no label — it must be refused")
	}

	// An entry with a comma must NOT silently become two labels: GitLab creates
	// missing labels automatically when setting them, so a typo would
	// permanently produce two project labels.
	if _, err := c.SetLabels(ctx, 15, 739, []string{"lead::bereit,lead::in-arbeit"}, nil); err == nil {
		t.Fatal("a label with a comma must be refused instead of split")
	}
}

// The actions must work THROUGH Execute too — the client tests above call the
// methods directly and would not notice a wrong JSON struct tag in the plugin's
// parameter struct. That is exactly where the seam to the agent sits: what it
// sends is JSON.
func TestExecuteSetLabelsAndMilestone(t *testing.T) {
	var gotMethod, gotPath, gotQuery string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotQuery = r.Method, r.URL.Path, r.URL.RawQuery
		gotBody = map[string]any{}
		json.NewDecoder(r.Body).Decode(&gotBody)
		if r.Method == http.MethodGet {
			json.NewEncoder(w).Encode([]Issue{{IID: 739, ProjectID: 15, Title: "Mailvorlagen"}})
			return
		}
		json.NewEncoder(w).Encode(Issue{IID: 739, ProjectID: 15,
			Labels: []string{"MUSS-Kriterium", "lead::in-arbeit"}})
	}))
	defer srv.Close()

	sys := System{}
	cred := target.Credential{BaseURL: srv.URL, Token: "test-token"}
	ctx := context.Background()

	// milestone must carry through from the JSON into the query.
	if _, err := sys.Execute(ctx, "list_issues",
		[]byte(`{"project_id":15,"milestone":"ECA-2026-045 Bundesdruckerei LMS"}`), cred); err != nil {
		t.Fatalf("list_issues with milestone: %v", err)
	}
	if !strings.Contains(gotQuery, "milestone=ECA-2026-045+Bundesdruckerei+LMS") {
		t.Fatalf("milestone does not arrive in the query (struct tag?): %s", gotQuery)
	}

	// set_labels: lists out of the JSON, additive/subtractive, label state back.
	res, err := sys.Execute(ctx, "set_labels",
		[]byte(`{"project_id":15,"issue_iid":739,"add_labels":["lead::in-arbeit"],"remove_labels":["lead::bereit"]}`), cred)
	if err != nil {
		t.Fatalf("set_labels: %v", err)
	}
	if gotMethod != http.MethodPut || gotPath != "/api/v4/projects/15/issues/739" {
		t.Fatalf("wrong API call: %s %s", gotMethod, gotPath)
	}
	if gotBody["add_labels"] != "lead::in-arbeit" || gotBody["remove_labels"] != "lead::bereit" {
		t.Fatalf("add_labels/remove_labels do not arrive (struct tag?): %+v", gotBody)
	}
	out := res.(map[string]any)
	if out["issue_iid"] != 739 {
		t.Fatalf("the answer must name the issue: %+v", out)
	}
	if labels, ok := out["labels"].([]string); !ok || len(labels) != 2 {
		t.Fatalf("the answer must carry the label state reached: %+v", out)
	}

	// The mandatory fields and the comma case through Execute as well.
	for _, params := range []string{
		`{"issue_iid":739,"add_labels":["x"]}`,                   // project_id missing
		`{"project_id":15,"add_labels":["x"]}`,                   // issue_iid missing
		`{"project_id":15,"issue_iid":739}`,                      // neither add nor remove
		`{"project_id":15,"issue_iid":739,"add_labels":["a,b"]}`, // comma in the label
	} {
		if _, err := sys.Execute(ctx, "set_labels", []byte(params), cred); err == nil {
			t.Fatalf("set_labels %s must fail", params)
		}
	}
}

// TestExecuteSetLabelsOnMergeRequest guards the fix for a real production
// incident: set_labels required issue_iid and had no path to a merge
// request at all, so every label-driven handoff on an MR (needs-arch-review,
// ready-for-qa, qa-passed/qa-failed, security-veto) failed with "project_id
// or issue_iid missing" whenever an agent passed mr_iid instead — one agent
// even talked itself into a comment-based workaround rather than recognizing
// the tool was missing this path.
func TestExecuteSetLabelsOnMergeRequest(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		gotBody = map[string]any{}
		json.NewDecoder(r.Body).Decode(&gotBody)
		json.NewEncoder(w).Encode(MergeRequest{IID: 47, ProjectID: 15,
			Labels: []string{"needs-arch-review"}})
	}))
	defer srv.Close()

	sys := System{}
	cred := target.Credential{BaseURL: srv.URL, Token: "test-token"}
	ctx := context.Background()

	res, err := sys.Execute(ctx, "set_labels",
		[]byte(`{"project_id":15,"mr_iid":47,"add_labels":["needs-arch-review"]}`), cred)
	if err != nil {
		t.Fatalf("set_labels on a merge request: %v", err)
	}
	if gotMethod != http.MethodPut || gotPath != "/api/v4/projects/15/merge_requests/47" {
		t.Fatalf("wrong API call: %s %s (must be the MR endpoint, not the issue one)", gotMethod, gotPath)
	}
	if gotBody["add_labels"] != "needs-arch-review" {
		t.Fatalf("add_labels does not arrive: %+v", gotBody)
	}
	out := res.(map[string]any)
	if out["mr_iid"] != 47 {
		t.Fatalf("the answer must name the MR: %+v", out)
	}
	if labels, ok := out["labels"].([]string); !ok || len(labels) != 1 {
		t.Fatalf("the answer must carry the label state reached: %+v", out)
	}

	// Neither ID at all → refused, same as the issue path.
	if _, err := sys.Execute(ctx, "set_labels", []byte(`{"project_id":15,"add_labels":["x"]}`), cred); err == nil {
		t.Fatal("set_labels without issue_iid or mr_iid must fail")
	}
}

// TestExecuteSetStateOnMergeRequest: closing a merge request (e.g. one
// superseded by an already-merged sibling) has no dedicated GitLab endpoint —
// it is the same state_event field issues use, just on the MR resource.
func TestExecuteSetStateOnMergeRequest(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		gotBody = map[string]any{}
		json.NewDecoder(r.Body).Decode(&gotBody)
		json.NewEncoder(w).Encode(MergeRequest{IID: 45, ProjectID: 15})
	}))
	defer srv.Close()

	sys := System{}
	cred := target.Credential{BaseURL: srv.URL, Token: "test-token"}
	ctx := context.Background()

	res, err := sys.Execute(ctx, "set_state",
		[]byte(`{"project_id":15,"mr_iid":45,"state":"close"}`), cred)
	if err != nil {
		t.Fatalf("set_state (close) on a merge request: %v", err)
	}
	if gotMethod != http.MethodPut || gotPath != "/api/v4/projects/15/merge_requests/45" {
		t.Fatalf("wrong API call: %s %s (must be the MR endpoint, not the issue one)", gotMethod, gotPath)
	}
	if gotBody["state_event"] != "close" {
		t.Fatalf("state_event does not arrive: %+v", gotBody)
	}
	out := res.(map[string]any)
	if out["mr_iid"] != 45 || out["state"] != "close" {
		t.Fatalf("the answer must name the MR and the state reached: %+v", out)
	}

	// Neither ID at all → refused, same as the issue path.
	if _, err := sys.Execute(ctx, "set_state", []byte(`{"project_id":15,"state":"close"}`), cred); err == nil {
		t.Fatal("set_state without issue_iid or mr_iid must fail")
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
		t.Fatalf("list_projects must apply the allowlist: %+v", ps)
	}

	res, err = sys.Execute(ctx, "list_issues", []byte(`{}`), cred)
	if err != nil {
		t.Fatalf("list_issues: %v", err)
	}
	if issues := res.([]Issue); len(issues) != 1 || issues[0].IID != 23 {
		t.Fatalf("list_issues must apply the allowlist: %+v", issues)
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
				t.Errorf("issues must filter on state=opened: %s", r.URL.RawQuery)
			}
			json.NewEncoder(w).Encode(issues)
		case r.URL.Path == "/api/v4/merge_requests":
			if r.URL.Query().Get("scope") != "created_by_me" || r.URL.Query().Get("state") != "opened" {
				t.Errorf("the mr pre-check must be scope=created_by_me&state=opened: %s", r.URL.RawQuery)
			}
			json.NewEncoder(w).Encode(myMRs)
		case r.URL.Path == "/api/v4/user":
			json.NewEncoder(w).Encode(User{ID: 1, Username: "covey-bot"})
		case strings.HasSuffix(r.URL.Path, "/notes"):
			serveNotes(w, r, mrNotes)
		default:
			t.Errorf("unexpected request: %s?%s", r.URL.Path, r.URL.RawQuery)
		}
	}))
	defer srv.Close()

	sys := System{}
	cred := target.Credential{BaseURL: srv.URL, Token: "test-token"}
	ctx := context.Background()

	// Neither open issues nor open MRs → no work.
	if has, err := sys.HasWork(ctx, cred); err != nil || has {
		t.Fatalf("nothing open: has=%v err=%v", has, err)
	}

	// An open issue in scope wakes (and short-circuits before the MR check).
	issueIn := Issue{IID: 23, ProjectID: 15}
	issueIn.References.Full = "gruppe/support#23"
	issueOut := Issue{IID: 7, ProjectID: 99}
	issueOut.References.Full = "gruppe/geheim#7"
	issues = []Issue{issueOut, issueIn}
	if has, err := sys.HasWork(ctx, cred); err != nil || !has {
		t.Fatalf("open issues: has=%v err=%v", has, err)
	}

	// The intake allowlist takes effect in the pre-check too.
	t.Setenv("COVEY_GITLAB_INTAKE_PROJECTS", "gruppe/support")
	issues = []Issue{issueOut}
	if has, err := sys.HasWork(ctx, cred); err != nil || has {
		t.Fatalf("issue outside the allowlist: has=%v err=%v", has, err)
	}

	// --- The MR review branch (no open issues in scope) ---
	issues = nil
	mrIn := MergeRequest{IID: 9, ProjectID: 15}
	mrIn.References.Full = "gruppe/support!9"

	// An open MR without comments: the review is still outstanding → no work.
	myMRs = []MergeRequest{mrIn}
	mrNotes = nil
	if has, err := sys.HasWork(ctx, cred); err != nil || has {
		t.Fatalf("fresh MR without feedback: has=%v err=%v", has, err)
	}

	// A conversation has started → might be waiting on the bot. Comment
	// authorship can't disambiguate "the bot answered last" from "a
	// colleague agent commented under the same shared identity" (this
	// organization has no per-role bot accounts), so any non-system comment
	// counts as possible work — see mrReviewPending's doc comment.
	mrNotes = []Note{
		{ID: 1, Body: "Bitte Test ergänzen", Author: struct {
			Username string `json:"username"`
		}{Username: "leaddev"}},
		{ID: 2, Body: "Erledigt", Author: struct {
			Username string `json:"username"`
		}{Username: "covey-bot"}},
	}
	if has, err := sys.HasWork(ctx, cred); err != nil || !has {
		t.Fatalf("MR with any comment: has=%v err=%v", has, err)
	}

	// New foreign feedback after the bot's answer → work. A closing system
	// comment (e.g. "changed the description") must not mask that.
	mrNotes = append(mrNotes,
		Note{ID: 3, Body: "Noch ein Punkt", Author: struct {
			Username string `json:"username"`
		}{Username: "leaddev"}},
		Note{ID: 4, System: true, Body: "changed the description", Author: struct {
			Username string `json:"username"`
		}{Username: "leaddev"}},
	)
	if has, err := sys.HasWork(ctx, cred); err != nil || !has {
		t.Fatalf("unanswered review feedback: has=%v err=%v", has, err)
	}

	// An MR outside the allowlist → no notes fetch, no work.
	mrOut := MergeRequest{IID: 4, ProjectID: 99}
	mrOut.References.Full = "gruppe/geheim!4"
	myMRs = []MergeRequest{mrOut}
	if has, err := sys.HasWork(ctx, cred); err != nil || has {
		t.Fatalf("MR outside the allowlist: has=%v err=%v", has, err)
	}
}

// TestHasWorkKind checks that the nur-wenn: sub-scope gates the kinds of work
// separately: gitlab:issues sees only issues, gitlab:mr only MR reviews, an
// empty/unknown scope both (like HasWork).
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
			serveNotes(w, r, mrNotes)
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
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
			t.Fatalf("HasWorkKind(%q) = %v, expected %v", kind, has, want)
		}
	}

	// Only an open issue, no MR work.
	issues, myMRs, mrNotes = []Issue{issueIn}, nil, nil
	check("issues", true)
	check("mr", false)
	check("", true) // the fallback checks both

	// Only unanswered MR feedback, no open issue.
	issues, myMRs, mrNotes = nil, []MergeRequest{mrIn}, foreign
	check("issues", false)
	check("mr", true)
	check("unbekannt", true) // an unknown scope → like HasWork (both)

	// Nothing open at all.
	issues, myMRs, mrNotes = nil, nil, nil
	check("issues", false)
	check("mr", false)
}

// tarGz builds a GitLab-like repository archive out of name→content pairs.
func tarGz(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	// pax_global_header as in the real GitLab archive — must be ignored.
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
		t.Fatalf("wrong API call: %s (auth %q)", gotPath, gotAuth)
	}
	if !strings.Contains(gotQuery, "sha=main") {
		t.Fatalf("ref must run as the sha parameter: %s", gotQuery)
	}
	co := res.(CheckoutResult)
	if co.Files != 2 {
		t.Fatalf("expected 2 files, was %d", co.Files)
	}
	data, err := os.ReadFile(filepath.Join(co.Path, "pkg", "auth.go"))
	if err != nil || !strings.Contains(string(data), "Bug") {
		t.Fatalf("the unpacked file is missing/wrong: %v %q", err, data)
	}
	if !strings.HasPrefix(co.Path, filepath.Join(workdir, "repos")) {
		t.Fatalf("the checkout must land under <workdir>/repos: %s", co.Path)
	}

	// A second checkout of the same state replaces the old one (no error).
	if _, err := sys.Execute(ctx, "checkout", []byte(`{"project_id":15}`), cred); err != nil {
		t.Fatalf("repeated checkout: %v", err)
	}

	// Without a sandbox workdir (a control-plane context, say) a clear refusal.
	if _, err := sys.Execute(context.Background(), "checkout", []byte(`{"project_id":15}`), cred); err == nil {
		t.Fatal("checkout without a workdir must fail")
	}
	// Without project_id a clear refusal.
	if _, err := sys.Execute(ctx, "checkout", []byte(`{}`), cred); err == nil {
		t.Fatal("checkout without project_id must fail")
	}
}

// TestDownloadUpload covers the core of reading screenshots: the upload
// reference embedded in the issue Markdown, ![...](/uploads/<secret>/<file>),
// is loaded into the sandbox in brokered fashion (the token stays in the
// daemon) so that the agent can look at the image with Read (vision)
// afterwards.
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

	// Just as the reference stands in the Markdown of an issue description.
	params := []byte(`{"project_id":15,"url":"/uploads/` + secret + `/login-fehler.png"}`)
	res, err := sys.Execute(ctx, "download_upload", params, cred)
	if err != nil {
		t.Fatalf("download_upload: %v", err)
	}
	if want := "/api/v4/projects/15/uploads/" + secret + "/login-fehler.png"; gotPath != want {
		t.Fatalf("wrong API path: %s (expected %s)", gotPath, want)
	}
	if gotAuth != "test-token" {
		t.Fatalf("the token must run as PRIVATE-TOKEN, was %q", gotAuth)
	}
	up := res.(DownloadUploadResult)
	if up.Filename != "login-fehler.png" || up.ContentType != "image/png" {
		t.Fatalf("unexpected result: %+v", up)
	}
	if !strings.HasPrefix(up.Path, filepath.Join(workdir, "uploads")) {
		t.Fatalf("the upload must land under <workdir>/uploads: %s", up.Path)
	}
	data, err := os.ReadFile(up.Path)
	if err != nil || !bytes.Equal(data, png) {
		t.Fatalf("the downloaded image is missing/wrong: %v", err)
	}

	// The full web URL as a reference must point at the same upload endpoint.
	full := srv.URL + "/gruppe/projekt/uploads/" + secret + "/login-fehler.png"
	if _, err := sys.Execute(ctx, "download_upload",
		[]byte(`{"project_id":15,"url":"`+full+`"}`), cred); err != nil {
		t.Fatalf("download_upload with the full URL: %v", err)
	}
	if want := "/api/v4/projects/15/uploads/" + secret + "/login-fehler.png"; gotPath != want {
		t.Fatalf("the full URL must be mapped onto the upload endpoint: %s", gotPath)
	}

	// Without project_id or url a clear refusal.
	if _, err := sys.Execute(ctx, "download_upload", []byte(`{"project_id":15}`), cred); err == nil {
		t.Fatal("download_upload without a url must fail")
	}
	// A reference without a valid upload pattern is refused.
	if _, err := sys.Execute(ctx, "download_upload",
		[]byte(`{"project_id":15,"url":"/uploads/zu-kurz/x.png"}`), cred); err == nil {
		t.Fatal("an invalid upload reference must fail")
	}
	// Without a sandbox workdir a clear refusal.
	if _, err := sys.Execute(context.Background(), "download_upload", params, cred); err == nil {
		t.Fatal("download_upload without a workdir must fail")
	}
}

// TestCreateIssueAction covers the intake of externally reported bugs: the
// agent turns a report (by email, say) into a GitLab ticket. Without a title or
// a project_id the action must refuse clearly — that carries the "ask first
// when the project is unclear" playbook (no ticket filed into the blue).
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
		t.Fatalf("wrong API call: %s %s", gotMethod, gotPath)
	}
	if gotBody["title"] != "Login kaputt" || gotBody["labels"] != "bug,intake" {
		t.Fatalf("the body was transferred wrongly: %+v", gotBody)
	}
	if _, ok := gotBody["assignee_ids"]; !ok {
		t.Fatalf("assignee must be resolved into assignee_ids: %+v", gotBody)
	}
	iss := res.(Issue)
	if iss.IID != 42 {
		t.Fatalf("unexpected issue: %+v", iss)
	}

	// Without a title (the project is known but there is no report) — refusal.
	if _, err := sys.Execute(ctx, "create_issue", []byte(`{"project_id":15}`), cred); err == nil {
		t.Fatal("create_issue without a title must fail")
	}
	// Without project_id (the project is unclear) — refusal; the agent must ask
	// instead.
	if _, err := sys.Execute(ctx, "create_issue", []byte(`{"title":"Irgendein Bug"}`), cred); err == nil {
		t.Fatal("create_issue without project_id must fail")
	}
}

// TestCheckoutPreservesCaches secures the speed fix: a repeated checkout of the
// same ref replaces the source code but leaves dependency caches
// (node_modules) standing — otherwise every QA run would have to install
// afresh.
func TestCheckoutPreservesCaches(t *testing.T) {
	archive1 := tarGz(t, map[string]string{
		"support-main-aaa/":         "",
		"support-main-aaa/main.go":  "package main // v1",
		"support-main-aaa/stale.go": "wird beim nächsten Checkout entfernt",
	})
	archive2 := tarGz(t, map[string]string{
		"support-main-bbb/":        "", // a different SHA → formerly a different directory
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
	// The agent installs dependencies + leaves a build cache behind.
	if err := os.MkdirAll(filepath.Join(path, "node_modules", "dep"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "node_modules", "dep", "index.js"), []byte("cached"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A second checkout (a new SHA) of the same ref.
	current = archive2
	res2, err := sys.Execute(ctx, "checkout", []byte(`{"project_id":15,"ref":"main"}`), cred)
	if err != nil {
		t.Fatalf("checkout 2: %v", err)
	}
	path2 := res2.(CheckoutResult).Path
	if path2 != path {
		t.Fatalf("a stable directory was expected: %q != %q", path2, path)
	}
	// node_modules must have survived ...
	if b, err := os.ReadFile(filepath.Join(path2, "node_modules", "dep", "index.js")); err != nil || string(b) != "cached" {
		t.Fatalf("the node_modules cache was not preserved: %v %q", err, b)
	}
	// ... the source code was updated ...
	if b, err := os.ReadFile(filepath.Join(path2, "main.go")); err != nil || !strings.Contains(string(b), "v2") {
		t.Fatalf("the source code was not updated: %v %q", err, b)
	}
	// ... the stale source file was removed.
	if _, err := os.Stat(filepath.Join(path2, "stale.go")); !os.IsNotExist(err) {
		t.Fatalf("the stale file stale.go should have been removed: %v", err)
	}
}

func TestCheckoutSubPathAndLimit(t *testing.T) {
	archive := tarGz(t, map[string]string{
		"support-main-abc123/":                   "",
		"support-main-abc123/web/upload/form.js": "const maxSize = 5", // the partial-checkout content
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
		t.Fatalf("partial checkout: %v", err)
	}
	if !strings.Contains(gotQuery, "path=web%2Fupload") {
		t.Fatalf("path must run as an archive parameter: %s", gotQuery)
	}

	// Push the limit down through the env: a 2 MB file against a 1 MB limit → a
	// clear error pointing at the ways out (path / list_tree / read_file).
	big := tarGz(t, map[string]string{
		"support-main-abc123/":         "",
		"support-main-abc123/blob.bin": strings.Repeat("x", 2<<20),
	})
	archive = big
	t.Setenv("COVEY_GITLAB_CHECKOUT_MAX_MB", "1")
	_, err := sys.Execute(ctx, "checkout", []byte(`{"project_id":15}`), cred)
	if err == nil || !strings.Contains(err.Error(), "list_tree") {
		t.Fatalf("the size limit must fail with ways out: %v", err)
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
		t.Fatalf("the tree is wrong: %+v", entries)
	}
	if !strings.Contains(gotQuery, "recursive=true") || !strings.Contains(gotQuery, "path=web") {
		t.Fatalf("tree parameters are missing: %s", gotQuery)
	}

	res, err = sys.Execute(ctx, "read_file", []byte(`{"project_id":15,"file_path":"web/upload/form.js","ref":"main"}`), cred)
	if err != nil {
		t.Fatalf("read_file: %v", err)
	}
	out := res.(map[string]any)
	if !strings.Contains(out["content"].(string), "maxSize") || out["truncated"].(bool) {
		t.Fatalf("the read_file content is wrong: %+v", out)
	}
	// GitLab demands the completely URL-encoded file path (including "/").
	if !strings.Contains(gotPath, "/repository/files/web%2Fupload%2Fform.js/raw") {
		t.Fatalf("file_path must be URL-encoded: %s", gotPath)
	}
	if !strings.Contains(gotQuery, "ref=main") {
		t.Fatalf("ref must be passed through: %s", gotQuery)
	}

	if _, err := sys.Execute(ctx, "read_file", []byte(`{"project_id":15}`), cred); err == nil {
		t.Fatal("read_file without file_path must fail")
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
		t.Fatalf("the commits are wrong: %+v", cs)
	}
	if !strings.Contains(gotQuery, "ref_name=main") || !strings.Contains(gotQuery, "path=web") ||
		!strings.Contains(gotQuery, "since=2026-07-15") {
		t.Fatalf("commit filters are missing: %s", gotQuery)
	}

	res, err = sys.Execute(ctx, "get_commit", []byte(`{"project_id":15,"sha":"abc123"}`), cred)
	if err != nil {
		t.Fatalf("get_commit: %v", err)
	}
	diffs := res.([]CommitDiff)
	if len(diffs) != 1 || !diffs[0].Truncated || len(diffs[0].Diff) != maxDiffBytesPerFile {
		t.Fatalf("the diff must be truncated to maxDiffBytesPerFile and marked: len=%d truncated=%v",
			len(diffs[0].Diff), diffs[0].Truncated)
	}
	if !strings.Contains(gotPath, "/repository/commits/abc123/diff") {
		t.Fatalf("the diff path is wrong: %s", gotPath)
	}

	res, err = sys.Execute(ctx, "list_merge_requests", []byte(`{"project_id":15,"state":"merged","search":"upload"}`), cred)
	if err != nil {
		t.Fatalf("list_merge_requests: %v", err)
	}
	if mrs := res.([]MergeRequest); len(mrs) != 1 || mrs[0].State != "merged" {
		t.Fatalf("the mrs are wrong: %+v", mrs)
	}
	if !strings.Contains(gotQuery, "state=merged") || !strings.Contains(gotQuery, "search=upload") {
		t.Fatalf("mr filters are missing: %s", gotQuery)
	}

	res, err = sys.Execute(ctx, "list_branches", []byte(`{"project_id":15,"search":"bugfix"}`), cred)
	if err != nil {
		t.Fatalf("list_branches: %v", err)
	}
	if bs := res.([]Branch); len(bs) != 1 || !bs[0].Default {
		t.Fatalf("the branches are wrong: %+v", bs)
	}
	if !strings.Contains(gotQuery, "search=bugfix") {
		t.Fatalf("the branch search is missing: %s", gotQuery)
	}

	for _, call := range [][2]string{
		{"list_commits", `{}`}, {"get_commit", `{"project_id":15}`},
		{"list_merge_requests", `{}`}, {"list_branches", `{}`},
	} {
		if _, err := sys.Execute(ctx, call[0], []byte(call[1]), cred); err == nil {
			t.Fatalf("%s without its mandatory fields must fail", call[0])
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
			json.NewEncoder(w).Encode([]Branch{}) // the feature branch does not exist yet
		case strings.Contains(r.URL.Path, "/repository/files/") && r.Method == http.MethodHead:
			// pkg/auth.go exists in the repo, pkg/auth_test.go is new.
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

	// A locally edited checkout state in the sandbox.
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
		t.Fatalf("the commit result is wrong: %+v", cr)
	}
	if commitBody["start_branch"] != "main" || commitBody["branch"] != "fix/issue-23-login" {
		t.Fatalf("a new branch must be branched off the default branch: %+v", commitBody)
	}
	actions := commitBody["actions"].([]any)
	if len(actions) != 3 {
		t.Fatalf("expected 3 actions, was %d", len(actions))
	}
	byPath := map[string]map[string]any{}
	for _, a := range actions {
		m := a.(map[string]any)
		byPath[m["file_path"].(string)] = m
	}
	if byPath["pkg/auth.go"]["action"] != "update" || byPath["pkg/auth.go"]["encoding"] != "base64" {
		t.Fatalf("an existing file must run as a base64 update: %+v", byPath["pkg/auth.go"])
	}
	if byPath["pkg/auth_test.go"]["action"] != "create" {
		t.Fatalf("a new file must run as create: %+v", byPath["pkg/auth_test.go"])
	}
	if byPath["pkg/alt.go"]["action"] != "delete" {
		t.Fatalf("deleted must run as delete: %+v", byPath["pkg/alt.go"])
	}

	// The fail-closed cases.
	for name, p := range map[string]string{
		"default branch":     `{"project_id":15,"branch":"main","message":"x","checkout_path":"` + co + `","files":["pkg/auth.go"]}`,
		"without files":      `{"project_id":15,"branch":"fix/x","message":"x","checkout_path":"` + co + `"}`,
		"without message":    `{"project_id":15,"branch":"fix/x","checkout_path":"` + co + `","files":["pkg/auth.go"]}`,
		"path traversal":     `{"project_id":15,"branch":"fix/x","message":"x","checkout_path":"` + co + `","files":["../../etc/passwd"]}`,
		"foreign path":       `{"project_id":15,"branch":"fix/x","message":"x","checkout_path":"/etc","files":["passwd"]}`,
		"without project_id": `{"branch":"fix/x","message":"x","checkout_path":"` + co + `","files":["pkg/auth.go"]}`,
	} {
		if _, err := sys.Execute(ctx, "commit", []byte(p), cred); err == nil {
			t.Fatalf("commit must fail: %s", name)
		}
	}
	// Without a sandbox workdir (a control-plane context) a clear refusal.
	if _, err := sys.Execute(context.Background(), "commit", []byte(params), cred); err == nil {
		t.Fatal("commit without a workdir must fail")
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
		t.Fatalf("commit onto an existing branch: %v", err)
	}
	if cr := res.(CommitResult); cr.BranchCreated {
		t.Fatalf("an existing branch must not be reported as new: %+v", cr)
	}
	if _, hasStart := commitBody["start_branch"]; hasStart {
		t.Fatalf("an existing branch must send no start_branch: %+v", commitBody)
	}
	if existsRef != "fix/issue-23-login" {
		t.Fatalf("the existence check must run against the existing branch: %q", existsRef)
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
		t.Fatalf("the mr is wrong: %+v", mr)
	}
	// Without a target_branch the project's default branch must be pulled, the
	// manager becomes assignee AND reviewer, the branch is removed after the
	// merge.
	if mrBody["target_branch"] != "main" || mrBody["remove_source_branch"] != true {
		t.Fatalf("the mr body is wrong: %+v", mrBody)
	}
	for _, key := range []string{"assignee_ids", "reviewer_ids"} {
		ids, _ := mrBody[key].([]any)
		if len(ids) != 1 || ids[0] != float64(7) {
			t.Fatalf("%s must be the manager: %+v", key, mrBody)
		}
	}

	for name, p := range map[string]string{
		"without assignee":         `{"project_id":15,"source_branch":"fix/x","title":"Fix"}`,
		"unknown user":             `{"project_id":15,"source_branch":"fix/x","title":"Fix","assignee":"gibtsnicht"}`,
		"without source_branch":    `{"project_id":15,"title":"Fix","assignee":"leaddev"}`,
		"without title":            `{"project_id":15,"source_branch":"fix/x","assignee":"leaddev"}`,
		"source equals the target": `{"project_id":15,"source_branch":"main","title":"Fix","assignee":"leaddev"}`,
	} {
		if _, err := sys.Execute(ctx, "create_merge_request", []byte(p), cred); err == nil {
			t.Fatalf("create_merge_request must fail: %s", name)
		}
	}
}

// TestCommentDedup covers the server-side brake against comment loops: a
// comment identical to one's own last one is not posted again, a differing one
// is.
func TestCommentDedup(t *testing.T) {
	var posts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v4/user":
			json.NewEncoder(w).Encode(User{ID: 7, Username: "covey-dev"})
		case r.URL.Path == "/api/v4/projects/15/issues/23/notes" && r.Method == http.MethodGet:
			serveNotes(w, r, []Note{
				{ID: 1, Body: "Fremd-Kommentar", Author: struct {
					Username string `json:"username"`
				}{Username: "mensch"}, CreatedAt: "2026-07-01T10:00:00Z"},
				{ID: 2, Body: "MR eröffnet: !5", Author: struct {
					Username string `json:"username"`
				}{Username: "covey-dev"}, CreatedAt: "2026-07-02T10:00:00Z"},
			})
		case r.URL.Path == "/api/v4/projects/15/issues/23/notes" && r.Method == http.MethodPost:
			posts++
			json.NewEncoder(w).Encode(Note{ID: 3})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	sys := System{}
	cred := target.Credential{BaseURL: srv.URL, Token: "t"}
	ctx := context.Background()

	// Identical to one's own last comment → skipped, no POST.
	res, err := sys.Execute(ctx, "comment", []byte(`{"project_id":15,"issue_iid":23,"body":"MR eröffnet: !5"}`), cred)
	if err != nil {
		t.Fatalf("comment (dup): %v", err)
	}
	if m, _ := res.(map[string]any); m["skipped"] != "duplicate" {
		t.Fatalf("the duplicate should have been skipped: %+v", res)
	}
	if posts != 0 {
		t.Fatalf("no POST expected, was %d", posts)
	}

	// A differing comment → is posted.
	if _, err := sys.Execute(ctx, "comment", []byte(`{"project_id":15,"issue_iid":23,"body":"Neuer Stand"}`), cred); err != nil {
		t.Fatalf("comment (new): %v", err)
	}
	if posts != 1 {
		t.Fatalf("the new comment should have been posted, posts=%d", posts)
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
		case r.URL.Path == "/api/v4/user" && r.Method == http.MethodGet:
			json.NewEncoder(w).Encode(User{ID: 7, Username: "covey-dev"})
		case r.URL.Path == "/api/v4/projects/15/merge_requests/9/notes" && r.Method == http.MethodGet:
			serveNotes(w, r, []Note{{ID: 120, Body: "Bitte noch einen Test ergänzen"}})
		case r.URL.Path == "/api/v4/projects/15/merge_requests/9/notes" && r.Method == http.MethodPost:
			json.NewDecoder(r.Body).Decode(&commentBody)
			json.NewEncoder(w).Encode(Note{ID: 121, Body: "Erledigt"})
		case r.URL.Path == "/api/v4/projects/15/pipelines":
			if r.URL.Query().Get("ref") != "fix/issue-23-login" {
				t.Errorf("the ref filter is missing: %s", r.URL.RawQuery)
			}
			json.NewEncoder(w).Encode([]Pipeline{{ID: 4, Status: "success", Ref: "fix/issue-23-login"}})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
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
		t.Fatalf("the mr detail is wrong: %+v", mr)
	}

	res, err = sys.Execute(ctx, "list_mr_notes", []byte(`{"project_id":15,"mr_iid":9}`), cred)
	if err != nil {
		t.Fatalf("list_mr_notes: %v", err)
	}
	if out := res.(map[string]any); len(out["notes"].([]Note)) != 1 || out["notes"].([]Note)[0].ID != 120 {
		t.Fatalf("the mr notes are wrong: %+v", out)
	}

	if _, err = sys.Execute(ctx, "comment_mr", []byte(`{"project_id":15,"mr_iid":9,"body":"Erledigt"}`), cred); err != nil {
		t.Fatalf("comment_mr: %v", err)
	}
	if commentBody["body"] != "Erledigt" {
		t.Fatalf("the comment body is wrong: %+v", commentBody)
	}

	res, err = sys.Execute(ctx, "list_pipelines", []byte(`{"project_id":15,"ref":"fix/issue-23-login"}`), cred)
	if err != nil {
		t.Fatalf("list_pipelines: %v", err)
	}
	if ps := res.([]Pipeline); len(ps) != 1 || ps[0].Status != "success" {
		t.Fatalf("the pipelines are wrong: %+v", ps)
	}

	// Mandatory parameters are missing → an error instead of a silent no-op.
	for name, call := range map[string][2]string{
		"get_merge_request without mr_iid":       {"get_merge_request", `{"project_id":15}`},
		"list_mr_notes without mr_iid":           {"list_mr_notes", `{"project_id":15}`},
		"comment_mr without body":                {"comment_mr", `{"project_id":15,"mr_iid":9}`},
		"list_pipelines without a project":       {"list_pipelines", `{}`},
		"list_pipeline_jobs without pipeline_id": {"list_pipeline_jobs", `{"project_id":15}`},
		"get_job_log without job_id":             {"get_job_log", `{"project_id":15}`},
	} {
		if _, err := sys.Execute(ctx, call[0], []byte(call[1]), cred); err == nil {
			t.Fatalf("%s must fail", name)
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
				t.Errorf("retry must be POST, got %s", r.Method)
			}
			json.NewEncoder(w).Encode(Pipeline{ID: 5, Status: "pending", Ref: "fix/x"})
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
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
		t.Fatalf("the jobs are wrong: %+v", jobs)
	}

	res, err = sys.Execute(ctx, "get_job_log", []byte(`{"project_id":15,"job_id":42}`), cred)
	if err != nil {
		t.Fatalf("get_job_log: %v", err)
	}
	out := res.(map[string]any)
	logText := out["log"].(string)
	// The END of the log must be preserved (that is where the error stands),
	// the beginning may fall victim to the truncation.
	if !strings.Contains(logText, "FEHLER: assertion failed") {
		t.Fatal("the end of the log must be preserved")
	}
	if out["truncated"] != true || len(logText) > maxJobLogBytes {
		t.Fatalf("the log must be capped at %d bytes: len=%d truncated=%v",
			maxJobLogBytes, len(logText), out["truncated"])
	}

	res, err = sys.Execute(ctx, "retry_pipeline", []byte(`{"project_id":15,"pipeline_id":4}`), cred)
	if err != nil {
		t.Fatalf("retry_pipeline: %v", err)
	}
	if p := res.(Pipeline); p.ID != 5 || p.Status != "pending" {
		t.Fatalf("the retry result is wrong: %+v", p)
	}
	if _, err := sys.Execute(ctx, "retry_pipeline", []byte(`{"project_id":15}`), cred); err == nil {
		t.Fatal("retry_pipeline without pipeline_id must fail")
	}
}

func TestExtractTarGzRejectsTraversal(t *testing.T) {
	archive := tarGz(t, map[string]string{
		"repo-main/":          "",
		"../../etc/evil.conf": "böse",
	})
	if _, err := extractTarGzInto(bytes.NewReader(archive), t.TempDir()); err == nil {
		t.Fatal("path traversal in the archive must be refused")
	}
}

// securePath is the promise on the finished destination path: what the name
// test up front lets through must still not point out of the destination
// directory at the back.
func TestSecurePathStaysUnderRoot(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"pkg/app.go", "./a/../b.go", "tief/im/baum.txt"} {
		dest, err := securePath(root, name)
		if err != nil {
			t.Fatalf("%q is harmless and must go through: %v", name, err)
		}
		if !strings.HasPrefix(dest, root+string(filepath.Separator)) {
			t.Fatalf("%q landed outside: %q", name, dest)
		}
	}
	// Absolute names are caught by the caller beforehand; here only what leads
	// out of the destination directory counts.
	for _, name := range []string{"../evil.conf", "a/../../evil.conf", ".."} {
		if dest, err := securePath(root, name); err == nil {
			t.Fatalf("%q must be refused, produced %q", name, dest)
		}
	}
}

func TestClientErrorSurface(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"401 Unauthorized"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "falsch")
	if _, err := c.GetIssue(context.Background(), 15, 23); err == nil {
		t.Fatal("an HTTP error must surface as an error")
	}
}

func TestActionSubject(t *testing.T) {
	sys := System{}
	if got := sys.ActionSubject("comment", []byte(`{"internal":false}`)); got != "gitlab:comment_external" {
		t.Fatalf("external comment: %s", got)
	}
	if got := sys.ActionSubject("comment", []byte(`{"internal":true}`)); got != "gitlab:comment_internal" {
		t.Fatalf("internal comment: %s", got)
	}
	if got := sys.ActionSubject("comment", []byte(`{}`)); got != "gitlab:comment_internal" {
		t.Fatalf("the default must be internal (safe): %s", got)
	}
	if got := sys.ActionSubject("set_state", nil); got != "gitlab:set_state" {
		t.Fatalf("set_state: %s", got)
	}
	// docs/ops-gitlab.md §5.1 promises this subject — guard-rail rules that are
	// meant to gate the state change on the board hang off it.
	if got := sys.ActionSubject("set_labels", nil); got != "gitlab:set_labels" {
		t.Fatalf("set_labels: %s", got)
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
		t.Fatalf("the assign result is wrong: %+v", out)
	}
	if gotMethod != http.MethodPut || gotPath != "/api/v4/projects/15/issues/23" {
		t.Fatalf("assign must be PUT /projects/15/issues/23: %s %s (query %s)", gotMethod, gotPath, gotQuery)
	}
	ids, _ := gotBody["assignee_ids"].([]any)
	if len(ids) != 1 || ids[0] != float64(42) {
		t.Fatalf("assignee_ids are wrong: %+v", gotBody)
	}

	if _, err := sys.Execute(ctx, "assign", []byte(`{"project_id":15,"issue_iid":23,"username":"gibtsnicht"}`), cred); err == nil {
		t.Fatal("an unknown username must produce an error (never guess)")
	}
	if _, err := sys.Execute(ctx, "assign", []byte(`{"username":"maxm"}`), cred); err == nil {
		t.Fatal("assign without project_id/issue_iid must be refused")
	}
}

// TestReviewerHandoff covers handing an MR over to a QA/test agent:
// create_merge_request with a separate reviewer, set_reviewer on an existing MR
// and approve_mr as the approval.
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
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	sys := System{}
	cred := target.Credential{BaseURL: srv.URL, Token: "test-token"}
	ctx := context.Background()

	// create_merge_request with reviewer != assignee: the manager stays the
	// assignee, the QA agent becomes the reviewer.
	if _, err := sys.Execute(ctx, "create_merge_request", []byte(`{"project_id":15,
		"source_branch":"fix/issue-23-login","title":"Fix Login","assignee":"leaddev","reviewer":"qa-bot"}`), cred); err != nil {
		t.Fatalf("create_merge_request with a reviewer: %v", err)
	}
	if ids, _ := mrBody["assignee_ids"].([]any); len(ids) != 1 || ids[0] != float64(7) {
		t.Fatalf("the assignee must be the manager (7): %+v", mrBody)
	}
	if ids, _ := mrBody["reviewer_ids"].([]any); len(ids) != 1 || ids[0] != float64(8) {
		t.Fatalf("the reviewer must be the QA agent (8): %+v", mrBody)
	}

	// set_reviewer on an existing MR.
	out, err := sys.Execute(ctx, "set_reviewer", []byte(`{"project_id":15,"mr_iid":9,"username":"@qa-bot"}`), cred)
	if err != nil {
		t.Fatalf("set_reviewer: %v", err)
	}
	if m := out.(map[string]any); m["reviewer"] != "qa-bot" || m["user_id"] != 8 {
		t.Fatalf("the set_reviewer result is wrong: %+v", out)
	}
	if ids, _ := reviewerBody["reviewer_ids"].([]any); len(ids) != 1 || ids[0] != float64(8) {
		t.Fatalf("reviewer_ids are wrong: %+v", reviewerBody)
	}

	// approve_mr as the approval.
	if _, err := sys.Execute(ctx, "approve_mr", []byte(`{"project_id":15,"mr_iid":9}`), cred); err != nil {
		t.Fatalf("approve_mr: %v", err)
	}
	if approvePath != "/api/v4/projects/15/merge_requests/9/approve" {
		t.Fatalf("approve must be POST .../approve, was %q", approvePath)
	}

	// Mandatory parameters are missing → an error.
	for name, call := range map[string][2]string{
		"set_reviewer without mr_iid":    {"set_reviewer", `{"project_id":15,"username":"qa-bot"}`},
		"set_reviewer with unknown user": {"set_reviewer", `{"project_id":15,"mr_iid":9,"username":"gibtsnicht"}`},
		"approve_mr without mr_iid":      {"approve_mr", `{"project_id":15}`},
	} {
		if _, err := sys.Execute(ctx, call[0], []byte(call[1]), cred); err == nil {
			t.Fatalf("%s must fail", name)
		}
	}
}

// TestHasWorkKindIssuesAssigned checks that nur-wenn: gitlab:issues:assigned
// counts only the open issues ASSIGNED to the bot (scope=assigned_to_me) —
// otherwise every open issue of someone else's in the scope wakes an agent that
// according to its playbook only works on assigned issues.
func TestHasWorkKindIssuesAssigned(t *testing.T) {
	var issues []Issue
	var notes []Note
	var sawScope string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v4/issues":
			sawScope = r.URL.Query().Get("scope")
			json.NewEncoder(w).Encode(issues)
		case r.URL.Path == "/api/v4/user":
			json.NewEncoder(w).Encode(User{ID: 1, Username: "covey-bot"})
		case strings.HasSuffix(r.URL.Path, "/notes"):
			serveNotes(w, r, notes)
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	sys := System{}
	cred := target.Credential{BaseURL: srv.URL, Token: "t"}
	ctx := context.Background()

	mine := Issue{IID: 23, ProjectID: 15}
	mine.References.Full = "gruppe/support#23"
	by := func(user string) struct {
		Username string `json:"username"`
	} {
		return struct {
			Username string `json:"username"`
		}{Username: user}
	}

	// No assigned issue → no work; the check must be assigned_to_me.
	issues = nil
	if has, err := sys.HasWorkKind(ctx, cred, "issues:assigned"); err != nil || has {
		t.Fatalf("without an assignment: has=%v err=%v", has, err)
	}
	if sawScope != "assigned_to_me" {
		t.Fatalf("the assigned sub-scope must query scope=assigned_to_me, was %q", sawScope)
	}

	// A freshly assigned issue without a comment → the first triage is
	// outstanding, work.
	issues, notes = []Issue{mine}, nil
	if has, err := sys.HasWorkKind(ctx, cred, "assigned"); err != nil || !has {
		t.Fatalf("with an assignment: has=%v err=%v", has, err)
	}

	// Comment authorship used to decide "already answered" here by comparing
	// the last commenter against the bot's own username — broken in an
	// organization without per-role bot accounts, where a colleague agent's
	// comment is indistinguishable from the bot's own (see issueWorkPending's
	// doc comment). Every assigned, open issue now counts as work
	// unconditionally, regardless of who commented or how many times —
	// avoiding an endless re-wake on a truly settled issue is the job of the
	// signature-based dedup one layer up (heartbeatHasWork in the
	// orchestrator, exercised by TestWorkSignature for the MR case; the same
	// threadSig mechanism carries the issue case here), not of this boolean.
	notes = []Note{
		{ID: 1, Body: "Bitte fixen", Author: by("leaddev")},
		{ID: 2, Body: "Erledigt via MR !12", Author: by("covey-bot")},
	}
	if has, err := sys.HasWorkKind(ctx, cred, "assigned"); err != nil || !has {
		t.Fatalf("an assigned issue always counts as work: has=%v err=%v", has, err)
	}

	// Further notes, system or not, change nothing about that boolean either
	// way — only the signature moves, tested separately.
	notes = append(notes, Note{ID: 3, System: true, Body: "added label", Author: by("leaddev")})
	if has, err := sys.HasWorkKind(ctx, cred, "assigned"); err != nil || !has {
		t.Fatalf("still work after a system note: has=%v err=%v", has, err)
	}
	notes = append(notes, Note{ID: 4, Body: "Noch ein Fall", Author: by("leaddev")})
	if has, err := sys.HasWorkKind(ctx, cred, "assigned"); err != nil || !has {
		t.Fatalf("still work after a real note: has=%v err=%v", has, err)
	}
}

// TestHasWorkKindReview checks the reviewer-side pre-check (nur-wenn:
// gitlab:review): an MR handed to me for review is work as long as it was not I
// who commented last — including the fresh MR with no note at all.
func TestHasWorkKindReview(t *testing.T) {
	var reviewMRs []MergeRequest
	var mrNotes []Note
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v4/user":
			json.NewEncoder(w).Encode(User{ID: 8, Username: "qa-bot"})
		case r.URL.Path == "/api/v4/merge_requests":
			if r.URL.Query().Get("reviewer_username") != "qa-bot" {
				t.Errorf("the reviewer_username filter is missing: %s", r.URL.RawQuery)
			}
			// GitLab's default for this endpoint is scope=created_by_me. Asking
			// without a scope therefore only finds merge requests the bot
			// opened ITSELF — and a QA agent opens none, so the answer is an
			// empty list and the agent never wakes. The double answers here the
			// way GitLab does, otherwise the test passes over exactly the bug
			// it exists for.
			if r.URL.Query().Get("scope") != "all" {
				json.NewEncoder(w).Encode([]MergeRequest{})
				return
			}
			json.NewEncoder(w).Encode(reviewMRs)
		case strings.HasSuffix(r.URL.Path, "/notes"):
			serveNotes(w, r, mrNotes)
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
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
			t.Fatalf("HasWorkKind(review) = %v, expected %v", has, want)
		}
	}

	// No MR assigned to me for review → no work.
	reviewMRs, mrNotes = nil, nil
	check(false)

	// An MR assigned to me for review → work, regardless of its notes. This
	// kind collapses to level detection (see mrReviewAssignedPending's doc
	// comment): comment authorship can't tell "the author reworked it" from
	// "I already reviewed it" apart under a shared identity, so unlike the
	// old contract, `mine`/`author` no longer change the outcome — kept as
	// unused local vars here only to document what used to matter.
	_, _ = author, mine
	reviewMRs, mrNotes = []MergeRequest{mrIn}, nil
	check(true)
	reviewMRs, mrNotes = []MergeRequest{mrIn}, author
	check(true)
	reviewMRs, mrNotes = []MergeRequest{mrIn}, mine
	check(true)
}

// TestCheckoutGitBaseline secures the ground on which the sub-agent works in
// the checkout: the archive brings no .git along, which is why the checkout
// creates a baseline. Only through that do git-calling project scripts work,
// and only through that can one say afterwards WHAT the sub-agent changed (the
// file list for the commit action).
func TestCheckoutGitBaseline(t *testing.T) {
	archive := tarGz(t, map[string]string{
		"proj-main-abc/":           "",
		"proj-main-abc/README.md":  "# Projekt",
		"proj-main-abc/pkg/app.go": "package app",
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write(archive)
	}))
	defer srv.Close()

	workdir := t.TempDir()
	ctx := target.WithWorkdir(context.Background(), workdir)
	res, err := System{}.Execute(ctx, "checkout", []byte(`{"project_id":7,"ref":"main"}`),
		target.Credential{BaseURL: srv.URL, Token: "t"})
	if err != nil {
		t.Fatalf("checkout: %v", err)
	}
	dir := res.(CheckoutResult).Path

	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		t.Skipf("no git available: %v", err)
	}
	// A fresh checkout = a clean tree: everything unpacked sits in the baseline
	// commit.
	if out := gitOut(t, dir, "status", "--porcelain", "-uall"); out != "" {
		t.Fatalf("a fresh checkout must be clean, was:\n%s", out)
	}
	// The tag is the anchor against which the sub-run reports its work — even
	// then, when it has committed locally in between.
	if out := gitOut(t, dir, "tag", "--list", target.BaselineRef); out != target.BaselineRef {
		t.Fatalf("the checkout must tag the upstream state as %q, had: %q", target.BaselineRef, out)
	}
	// After a change git reports exactly that file — that is the list the
	// sub-agent hands back to the commit action.
	if err := os.WriteFile(filepath.Join(dir, "pkg", "app.go"), []byte("package app // fix"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pkg", "app_test.go"), []byte("package app"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := gitOut(t, dir, "status", "--porcelain", "-uall")
	if !strings.Contains(out, "pkg/app.go") || !strings.Contains(out, "pkg/app_test.go") {
		t.Fatalf("the changed and the new file must show up:\n%s", out)
	}

	// Dependency caches are not part of the work: they survive the checkout
	// (preserveDirs) and stay out of the status through .git/info/exclude.
	if err := os.MkdirAll(filepath.Join(dir, "node_modules", "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "node_modules", "lib", "index.js"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out := gitOut(t, dir, "status", "--porcelain", "-uall"); strings.Contains(out, "node_modules") {
		t.Fatalf("cache directories must not count as work:\n%s", out)
	}
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

// TestCreateMergeRequestAssigneeFromIssue covers that an MR without a named
// assignee goes to the REPORTER of the issue instead of to the manager across
// the board.
func TestCreateMergeRequestAssigneeFromIssue(t *testing.T) {
	var mrBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v4/projects/15":
			json.NewEncoder(w).Encode(ProjectDetail{ID: 15, DefaultBranch: "main"})
		case r.URL.Path == "/api/v4/projects/15/issues/23":
			iss := Issue{IID: 23, ProjectID: 15, Title: "Login kaputt"}
			iss.Author.Username = "mario"
			json.NewEncoder(w).Encode(iss)
		case r.URL.Path == "/api/v4/users":
			if u := r.URL.Query().Get("username"); u == "mario" {
				json.NewEncoder(w).Encode([]User{{ID: 42, Username: "mario"}})
			} else {
				json.NewEncoder(w).Encode([]User{})
			}
		case r.URL.Path == "/api/v4/projects/15/merge_requests" && r.Method == http.MethodPost:
			json.NewDecoder(r.Body).Decode(&mrBody)
			json.NewEncoder(w).Encode(MergeRequest{IID: 9})
		}
	}))
	defer srv.Close()

	sys := System{}
	cred := target.Credential{BaseURL: srv.URL, Token: "test-token"}
	if _, err := sys.Execute(context.Background(), "create_merge_request", []byte(`{"project_id":15,
		"source_branch":"fix/issue-23-login","title":"Fix Login","issue_iid":23}`), cred); err != nil {
		t.Fatalf("create_merge_request: %v", err)
	}
	ids, _ := mrBody["assignee_ids"].([]any)
	if len(ids) != 1 || ids[0] != float64(42) {
		t.Fatalf("the assignee must be the issue's reporter: %+v", mrBody)
	}
}

// TestWorkSignature covers the wake-up brake: besides the yes/no the pre-check
// returns a signature of the working set. It stays stable as long as nothing
// happens in the thread — that way an agent may end a run silently without
// being woken again on the same state at the next interval — and it changes as
// soon as a new contribution comes along.
func TestWorkSignature(t *testing.T) {
	var myMRs []MergeRequest
	var mrNotes []Note
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v4/issues":
			json.NewEncoder(w).Encode([]Issue{})
		case r.URL.Path == "/api/v4/merge_requests":
			json.NewEncoder(w).Encode(myMRs)
		case r.URL.Path == "/api/v4/user":
			json.NewEncoder(w).Encode(User{ID: 1, Username: "covey-bot"})
		case strings.HasSuffix(r.URL.Path, "/notes"):
			serveNotes(w, r, mrNotes)
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	sys := System{}
	cred := target.Credential{BaseURL: srv.URL, Token: "t"}
	ctx := context.Background()
	author := func(name string) struct {
		Username string `json:"username"`
	} {
		return struct {
			Username string `json:"username"`
		}{Username: name}
	}

	mr := MergeRequest{IID: 9, ProjectID: 15}
	mr.References.Full = "gruppe/support!9"
	myMRs = []MergeRequest{mr}

	// Without work the signature is empty — it then suppresses nothing. A
	// fresh MR with zero comments is the only case this can still tell
	// without relying on comment authorship (see mrReviewPending's doc
	// comment) — once anyone has said anything, it counts as possible work.
	mrNotes = nil
	has, sig, err := sys.HasWorkSigned(ctx, cred, "mr")
	if err != nil || has || sig != "" {
		t.Fatalf("without work: has=%v sig=%q err=%v", has, sig, err)
	}

	// Review feedback: work with a signature — and the same one on the second
	// check, because nothing has happened.
	mrNotes = []Note{
		{ID: 1, Body: "erledigt", Author: author("covey-bot")},
		{ID: 7, Body: "grün, Freigabe", Author: author("egon")},
	}
	has, sig, err = sys.HasWorkSigned(ctx, cred, "mr")
	if err != nil || !has || sig == "" {
		t.Fatalf("with feedback: has=%v sig=%q err=%v", has, sig, err)
	}
	if _, again, _ := sys.HasWorkSigned(ctx, cred, "mr"); again != sig {
		t.Fatalf("the signature must stay stable: %q vs %q", again, sig)
	}

	// A new contribution changes it → the agent is woken again.
	mrNotes = append(mrNotes, Note{ID: 8, Body: "und noch ein Mangel", Author: author("egon")})
	_, changed, _ := sys.HasWorkSigned(ctx, cred, "mr")
	if changed == sig {
		t.Fatalf("a new contribution must change the signature: %q", changed)
	}

	// A push counts too: GitLab records it as a system note.
	before := changed
	mrNotes = append(mrNotes, Note{ID: 9, Body: "added 2 commits", System: true, Author: author("gitlab")})
	if _, afterPush, _ := sys.HasWorkSigned(ctx, cred, "mr"); afterPush == before {
		t.Fatalf("a push must change the signature: %q", afterPush)
	}
}

// The three actions that no test ran through so far — get_issue, list_notes and
// escalate. Without them every rearrangement of the Execute dispatch would be a
// jump without a net: a mistyped action name would otherwise only show up in
// operation.
func TestExecuteRestlicheAktionen(t *testing.T) {
	type ruf struct {
		method, path string
		body         map[string]any
	}
	var rufe []ruf
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotBody = map[string]any{}
		json.NewDecoder(r.Body).Decode(&gotBody)
		rufe = append(rufe, ruf{r.Method, r.URL.Path, gotBody})
		switch {
		case strings.HasSuffix(r.URL.Path, "/notes") && r.Method == http.MethodGet:
			serveNotes(w, r, []Note{{ID: 1, Body: "first Notiz"}})
		case strings.HasSuffix(r.URL.Path, "/notes"):
			json.NewEncoder(w).Encode(Note{ID: 2, Body: "angelegt"})
		default:
			json.NewEncoder(w).Encode(Issue{IID: 42, ProjectID: 7, Title: "Drucker brennt"})
		}
	}))
	defer srv.Close()

	sys := System{}
	cred := target.Credential{BaseURL: srv.URL, Token: "test-token"}
	ctx := context.Background()

	// get_issue: one ticket in one piece.
	res, err := sys.Execute(ctx, "get_issue", []byte(`{"project_id":7,"issue_iid":42}`), cred)
	if err != nil {
		t.Fatalf("get_issue: %v", err)
	}
	if gotPath != "/api/v4/projects/7/issues/42" {
		t.Errorf("get_issue calls %s", gotPath)
	}
	if iss, ok := res.(Issue); !ok || iss.IID != 42 {
		t.Errorf("get_issue returns %#v", res)
	}

	// list_notes: the history on the ticket.
	res, err = sys.Execute(ctx, "list_notes", []byte(`{"project_id":7,"issue_iid":42}`), cred)
	if err != nil {
		t.Fatalf("list_notes: %v", err)
	}
	if gotPath != "/api/v4/projects/7/issues/42/notes" {
		t.Errorf("list_notes calls %s", gotPath)
	}
	out, ok := res.(map[string]any)
	if !ok || len(out["notes"].([]Note)) != 1 {
		t.Errorf("list_notes returns %#v", res)
	}
	// A short thread is complete — then no truncation marker may be attached to
	// it either, otherwise the agent goes leafing through pages that do not exist.
	if out["truncated"] != nil || out["window"] != "complete thread, 1 comment" {
		t.Errorf("a complete thread is described wrongly: %+v", out)
	}

	// escalate does TWO things: first an internal comment, then dropping the
	// assignment — the ticket lands back with the human. Both belong pinned
	// down, otherwise half of it falls away in a rearrangement without any test
	// speaking up.
	rufe = nil
	if _, err := sys.Execute(ctx, "escalate", []byte(`{"project_id":7,"issue_iid":42}`), cred); err != nil {
		t.Fatalf("escalate: %v", err)
	}
	if len(rufe) != 2 {
		t.Fatalf("escalate makes %d calls, expected 2 (comment + dropping the assignment): %+v", len(rufe), rufe)
	}
	if rufe[0].method != http.MethodPost || !strings.HasSuffix(rufe[0].path, "/notes") {
		t.Errorf("the first call is not a comment: %s %s", rufe[0].method, rufe[0].path)
	}
	if body, _ := rufe[0].body["body"].(string); body != "Escalated by a Covey agent." {
		t.Errorf("escalate without a note does not use the default: %q", body)
	}
	if intern, _ := rufe[0].body["internal"].(bool); !intern {
		t.Error("the escalation comment must be internal — it concerns the team, not the reporter")
	}
	if rufe[1].method != http.MethodPut {
		t.Errorf("the second call does not drop the assignment: %s %s", rufe[1].method, rufe[1].path)
	}
	if ids, ok := rufe[1].body["assignee_ids"].([]any); !ok || len(ids) != 0 {
		t.Errorf("escalate must empty the assignment, got %#v", rufe[1].body["assignee_ids"])
	}

	// … and with a text of its own that one stays.
	rufe = nil
	if _, err := sys.Execute(ctx, "escalate",
		[]byte(`{"project_id":7,"issue_iid":42,"note":"Kunde wartet seit drei Tagen"}`), cred); err != nil {
		t.Fatalf("escalate with a note: %v", err)
	}
	if body, _ := rufe[0].body["body"].(string); body != "Kunde wartet seit drei Tagen" {
		t.Errorf("escalate overwrites the text given: %q", body)
	}

	// An unknown action is an error, not a silent nothing.
	if _, err := sys.Execute(ctx, "gibtsnicht", []byte(`{}`), cred); err == nil {
		t.Error("an unknown action must produce an error")
	}
}

// Partial checkouts of one ref belong in ONE working tree. They used to each
// get a directory of their own, because the subPath went into the directory
// NAME — five subdirectories then lay side by side as five stumps and nothing
// in them could be built. Since the size error advises exactly this route, the
// documented way out led into a dead end.
func TestCheckoutPartialsGrowIntoOneTree(t *testing.T) {
	app := tarGz(t, map[string]string{
		"stupla-abc123/":           "",
		"stupla-abc123/Kernel.php": "<?php // app",
	})
	tests := tarGz(t, map[string]string{
		"stupla-abc123/":             "",
		"stupla-abc123/UnitTest.php": "<?php // tests",
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.RawQuery, "path=stupla%2Ftests") {
			w.Write(tests)
			return
		}
		w.Write(app)
	}))
	defer srv.Close()

	sys := System{}
	cred := target.Credential{BaseURL: srv.URL, Token: "t"}
	workdir := t.TempDir()
	ctx := target.WithWorkdir(context.Background(), workdir)

	first, err := sys.Execute(ctx, "checkout", []byte(`{"project_id":40,"ref":"main","path":"stupla/app"}`), cred)
	if err != nil {
		t.Fatalf("checkout app: %v", err)
	}
	second, err := sys.Execute(ctx, "checkout", []byte(`{"project_id":40,"ref":"main","path":"stupla/tests"}`), cred)
	if err != nil {
		t.Fatalf("checkout tests: %v", err)
	}
	a, b := first.(CheckoutResult), second.(CheckoutResult)
	if a.Path != b.Path {
		t.Fatalf("both partial checkouts must share one repository root: %s vs %s", a.Path, b.Path)
	}
	// The subtrees lie where they lie upstream — and the first survives the
	// second (only the fetched subtree is pruned).
	for _, rel := range []string{"stupla/app/Kernel.php", "stupla/tests/UnitTest.php"} {
		if _, err := os.Stat(filepath.Join(a.Path, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("%s is missing in the shared tree: %v", rel, err)
		}
	}
	// The git baseline belongs at the root, not into the subtree — otherwise
	// commit compares against the wrong root.
	if _, err := os.Stat(filepath.Join(a.Path, ".git")); err != nil {
		t.Fatalf("baseline missing at the repository root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(a.Path, "stupla", "app", ".git")); err == nil {
		t.Fatal("a partial checkout must not leave a nested git repository behind")
	}
	if b.LocalPath != filepath.Join(b.Path, "stupla", "tests") {
		t.Fatalf("local_path must name the subtree: %s", b.LocalPath)
	}
}

// A path leading out of the repository is refused before anything is written.
func TestCheckoutRefusesEscapingSubPath(t *testing.T) {
	sys := System{}
	ctx := target.WithWorkdir(context.Background(), t.TempDir())
	cred := target.Credential{BaseURL: "http://unused", Token: "t"}
	for _, p := range []string{"../../etc", "/etc"} {
		if _, err := sys.Execute(ctx, "checkout",
			[]byte(`{"project_id":1,"ref":"main","path":"`+p+`"}`), cred); err == nil {
			t.Fatalf("path %q must be refused", p)
		}
	}
}

// Two long refs agreeing in their first 48 characters must not share a
// directory — one checkout would silently overwrite the other.
func TestRepoDirNameKeepsLongRefsApart(t *testing.T) {
	long := "feature/a-very-long-branch-name-that-goes-past-48-chars"
	a := repoDirName(40, long+"-eins")
	b := repoDirName(40, long+"-zwei")
	if a == b {
		t.Fatalf("long refs collide into the same directory: %s", a)
	}
}

// GitLab answers the approve endpoint with 401 when approving is not permitted
// — the merge request is already merged or closed, the caller is its author, or
// an approval rule stands in the way. Raw, that reads like a credential
// problem: the agent concludes its token is broken and reports a broken GitLab
// connection, while in truth somebody simply closed the merge request. Reported
// from production.
func TestApproveOnClosedMRExplainsThe401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"message":"401 Unauthorized"}`))
	}))
	defer srv.Close()

	sys := System{}
	cred := target.Credential{BaseURL: srv.URL, Token: "gueltiges-token"}
	ctx := target.WithWorkdir(context.Background(), t.TempDir())

	_, err := sys.Execute(ctx, "approve_mr", []byte(`{"project_id":40,"mr_iid":1685}`), cred)
	if err == nil {
		t.Fatal("a 401 is an error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "not permitted") || !strings.Contains(msg, "get_merge_request") {
		t.Fatalf("the message has to say what it really means and what to do: %q", msg)
	}
	if !strings.Contains(msg, "401") {
		t.Fatalf("the original status stays as the evidence: %q", msg)
	}
}

// Everywhere else a 401 IS a token problem — the hint must not turn that on its
// head.
func TestPlain401StaysATokenProblem(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"message":"401 Unauthorized"}`))
	}))
	defer srv.Close()

	sys := System{}
	cred := target.Credential{BaseURL: srv.URL, Token: "abgelaufen"}
	ctx := target.WithWorkdir(context.Background(), t.TempDir())

	_, err := sys.Execute(ctx, "list_issues", []byte(`{"project_id":40}`), cred)
	if err == nil || !strings.Contains(err.Error(), "token is rejected") {
		t.Fatalf("a plain 401 stays a credential problem: %v", err)
	}
}

// mergeGateServer is a GitLab double for the merge gate: it delivers one MR,
// its approval state and takes the merge. mrState/approvals are set by the test
// beforehand; mergeBody records what was actually merged.
type mergeGateServer struct {
	mr        MergeRequestDetail
	approvals MRApprovals
	me        string
	mergeBody map[string]any
	merges    int
	queued    int
}

func (g *mergeGateServer) start(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v4/user":
			json.NewEncoder(w).Encode(User{ID: 8, Username: g.me})
		case r.URL.Path == "/api/v4/projects/40/merge_requests/1685" && r.Method == http.MethodGet:
			json.NewEncoder(w).Encode(g.mr)
		case r.URL.Path == "/api/v4/projects/40/merge_requests/1685/approvals":
			json.NewEncoder(w).Encode(g.approvals)
		case r.URL.Path == "/api/v4/projects/40/merge_requests/1685/merge" && r.Method == http.MethodPut:
			json.NewDecoder(r.Body).Decode(&g.mergeBody)
			result := g.mr
			if g.mergeBody["merge_when_pipeline_succeeds"] == true {
				// Real GitLab does not complete the merge here either — it stays
				// open until the pipeline it is pinned against turns green.
				g.queued++
				result.MergeWhenPipelineSucceeds = true
			} else {
				g.merges++
				result.State = "merged"
			}
			json.NewEncoder(w).Encode(result)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// greenMR is the state in which a merge is permitted: open, free of conflicts,
// discussions resolved, pipeline green — the approval comes from the approvals
// object.
func greenMR() MergeRequestDetail {
	return MergeRequestDetail{
		IID: 1685, State: "opened", SHA: "f47dacf6", TargetBranch: "educa-x-bugfix",
		DetailedMergeStatus:         "mergeable",
		BlockingDiscussionsResolved: true,
		HeadPipeline:                &Pipeline{ID: 47720, Status: "success"},
	}
}

// TestMergeMROnlyAfterOwnApproval: the reviewer merges what they themselves
// accepted — and merges exactly the commit they saw (sha).
func TestMergeMROnlyAfterOwnApproval(t *testing.T) {
	g := &mergeGateServer{mr: greenMR(), me: "egon.rastlos",
		approvals: MRApprovals{UserHasApproved: true}}
	srv := g.start(t)

	sys := System{}
	cred := target.Credential{BaseURL: srv.URL, Token: "test-token"}
	out, err := sys.Execute(context.Background(), "merge_mr", []byte(`{"project_id":40,"mr_iid":1685}`), cred)
	if err != nil {
		t.Fatalf("merge_mr on a green MR: %v", err)
	}
	if m := out.(map[string]any); m["merged"] != true || m["sha"] != "f47dacf6" {
		t.Fatalf("the merge result is wrong: %+v", out)
	}
	if g.mergeBody["sha"] != "f47dacf6" {
		t.Fatalf("the reviewed commit has to be pinned on the merge: %+v", g.mergeBody)
	}
	if g.mergeBody["should_remove_source_branch"] != true {
		t.Fatalf("the source branch is removed after the merge: %+v", g.mergeBody)
	}
}

// TestMergeMRRefusesUnacceptedStates is the actual point of the gate: everything
// the agent cannot judge from its own run blocks the merge — and the refusal
// says why, so the agent can pass the reason on per comment_mr.
func TestMergeMRRefusesUnacceptedStates(t *testing.T) {
	cases := map[string]struct {
		mutate func(*mergeGateServer)
		reason string
	}{
		"not approved by oneself": {
			func(g *mergeGateServer) { g.approvals = MRApprovals{} },
			"your own approval is not on record",
		},
		"approved by someone else": {
			func(g *mergeGateServer) {
				g.approvals = MRApprovals{}
				json.Unmarshal([]byte(`{"approved_by":[{"user":{"username":"tabea.schwarz"}}]}`), &g.approvals)
			},
			"your own approval is not on record",
		},
		"pipeline red": {
			func(g *mergeGateServer) { g.mr.HeadPipeline = &Pipeline{ID: 1, Status: "failed"} },
			"not green",
		},
		"pipeline still running, not approved yet": {
			func(g *mergeGateServer) {
				g.mr.HeadPipeline = &Pipeline{ID: 1, Status: "running"}
				g.approvals = MRApprovals{}
			},
			"your own approval is not on record",
		},
		"no pipeline at all": {
			func(g *mergeGateServer) { g.mr.HeadPipeline = nil },
			"no pipeline has run",
		},
		"discussion open": {
			func(g *mergeGateServer) { g.mr.BlockingDiscussionsResolved = false },
			"unresolved discussions",
		},
		"conflicts": {
			func(g *mergeGateServer) { g.mr.HasConflicts = true },
			"conflicts",
		},
		"already merged": {
			func(g *mergeGateServer) { g.mr.State = "merged" },
			"not open",
		},
		"gitlab says not mergeable": {
			func(g *mergeGateServer) { g.mr.DetailedMergeStatus = "ci_still_running" },
			"not consider the merge request mergeable",
		},
		"further approval required": {
			func(g *mergeGateServer) {
				g.approvals = MRApprovals{UserHasApproved: true, ApprovalsLeft: 1}
			},
			"further approval",
		},
		"no head commit": {
			func(g *mergeGateServer) { g.mr.SHA = "" },
			"no head commit",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			g := &mergeGateServer{mr: greenMR(), me: "egon.rastlos",
				approvals: MRApprovals{UserHasApproved: true}}
			tc.mutate(g)
			srv := g.start(t)

			sys := System{}
			cred := target.Credential{BaseURL: srv.URL, Token: "test-token"}
			_, err := sys.Execute(context.Background(), "merge_mr", []byte(`{"project_id":40,"mr_iid":1685}`), cred)
			if err == nil {
				t.Fatal("the merge has to be refused")
			}
			if !strings.Contains(err.Error(), tc.reason) {
				t.Fatalf("the refusal has to name the reason %q: %v", tc.reason, err)
			}
			if !strings.Contains(err.Error(), "comment_mr") {
				t.Fatalf("the refusal has to say what to do instead: %v", err)
			}
			if g.merges != 0 {
				t.Fatal("nothing may be merged in the refused case")
			}
			if g.queued != 0 {
				t.Fatal("nothing may be queued for auto-merge in the refused case either")
			}
		})
	}
}

// TestMergeMRQueuesWhenPipelineStillRunning is the actual point of the
// auto-merge path: an MR that is otherwise fully accepted (approved, no
// conflicts, discussions resolved) but whose pipeline just has not concluded
// yet must not be refused — GitLab's own merge_when_pipeline_succeeds queues
// it, so nobody has to come back and ask again once it turns green.
func TestMergeMRQueuesWhenPipelineStillRunning(t *testing.T) {
	g := &mergeGateServer{mr: greenMR(), me: "egon.rastlos",
		approvals: MRApprovals{UserHasApproved: true}}
	g.mr.HeadPipeline = &Pipeline{ID: 1, Status: "running"}
	srv := g.start(t)

	sys := System{}
	cred := target.Credential{BaseURL: srv.URL, Token: "test-token"}
	out, err := sys.Execute(context.Background(), "merge_mr", []byte(`{"project_id":40,"mr_iid":1685}`), cred)
	if err != nil {
		t.Fatalf("merge_mr with a still-running pipeline must queue, not refuse: %v", err)
	}
	m := out.(map[string]any)
	if m["queued_for_pipeline"] != true || m["pipeline_status"] != "running" {
		t.Fatalf("the result has to say it was queued and why: %+v", out)
	}
	if g.mergeBody["merge_when_pipeline_succeeds"] != true {
		t.Fatalf("the request has to ask GitLab for merge_when_pipeline_succeeds: %+v", g.mergeBody)
	}
	if g.mergeBody["sha"] != "f47dacf6" {
		t.Fatalf("the reviewed commit has to be pinned even when queuing: %+v", g.mergeBody)
	}
	if g.queued != 1 || g.merges != 0 {
		t.Fatalf("exactly one queue call and no immediate merge: queued=%d merges=%d", g.queued, g.merges)
	}

	// A pipeline that has already concluded negatively must still refuse —
	// waiting longer would not help, so queuing would just leave it in limbo.
	for _, status := range []string{"failed", "canceled", "skipped"} {
		t.Run("terminal "+status, func(t *testing.T) {
			g := &mergeGateServer{mr: greenMR(), me: "egon.rastlos",
				approvals: MRApprovals{UserHasApproved: true}}
			g.mr.HeadPipeline = &Pipeline{ID: 1, Status: status}
			srv := g.start(t)
			cred := target.Credential{BaseURL: srv.URL, Token: "test-token"}
			if _, err := sys.Execute(context.Background(), "merge_mr", []byte(`{"project_id":40,"mr_iid":1685}`), cred); err == nil {
				t.Fatalf("a %s pipeline must still refuse, not queue", status)
			}
			if g.queued != 0 {
				t.Fatalf("a %s pipeline must never be queued for auto-merge", status)
			}
		})
	}
}

// TestMergeMRNeedsIDs — as with every action, missing parameters are an error,
// not a merge on a guessed MR.
func TestMergeMRNeedsIDs(t *testing.T) {
	sys := System{}
	cred := target.Credential{BaseURL: "http://127.0.0.1:1", Token: "t"}
	for _, params := range []string{`{"project_id":40}`, `{"mr_iid":1685}`, `{}`} {
		if _, err := sys.Execute(context.Background(), "merge_mr", []byte(params), cred); err == nil {
			t.Fatalf("merge_mr must refuse %s", params)
		}
	}
}

// The scope split must not reword anything: with all scopes granted, exactly
// the old doc stands. Otherwise the filter would change agent behaviour instead
// of only shortening the prompt.
func TestPromptDocForScopesFullEqualsPromptDoc(t *testing.T) {
	full := (System{}).PromptDocForScopes([]string{"read", "write", "comment", "merge"})
	if full != (System{}).PromptDoc() {
		t.Fatal("with all scopes the doc must be identical to PromptDoc()")
	}
}

// Without the merge scope the reviewer playbook falls away — that is the part a
// developer agent could never act on. The action catalogue stays.
func TestPromptDocForScopesDropsReviewerWithoutMerge(t *testing.T) {
	doc := (System{}).PromptDocForScopes([]string{"read", "write", "comment"})
	if strings.Contains(doc, "How to work as a QA/test agent") {
		t.Fatal("without merge the reviewer playbook has no business being there")
	}
	if !strings.Contains(doc, "Available GitLab actions") {
		t.Fatal("the action catalogue applies to everyone")
	}
	if !strings.Contains(doc, "Writing developer actions") {
		t.Fatal("with write the developer actions stay")
	}
	if len(doc) >= len((System{}).PromptDoc()) {
		t.Fatal("the narrowed doc has to be shorter — that is the whole point")
	}
}

// Read-only: neither writing actions nor either playbook.
func TestPromptDocForScopesReadOnly(t *testing.T) {
	doc := (System{}).PromptDocForScopes([]string{"read"})
	if strings.Contains(doc, "Writing developer actions") ||
		strings.Contains(doc, "How to work as a QA/test agent") {
		t.Fatal("read alone carries neither writing actions nor the reviewer playbook")
	}
}

// Fail-open: no recorded scopes must not take anything away.
func TestPromptDocForScopesEmptyStaysFull(t *testing.T) {
	if (System{}).PromptDocForScopes(nil) != (System{}).PromptDoc() {
		t.Fatal("without scopes the full doc stands")
	}
}

// autorNote builds a comment by a specific user — the Author field is an
// anonymous struct, and writing it out inline three times makes every test that
// works with threads unreadable.
func autorNote(id int, user, body, wann string) Note {
	n := Note{ID: id, Body: body, CreatedAt: wann}
	n.Author.Username = user
	return n
}

// langerThread builds a ticket the way it looks in operation when an agent
// writes a daily report into it: chronological, oldest first.
func langerThread(n int) []Note {
	notes := make([]Note, 0, n)
	for i := 1; i <= n; i++ {
		notes = append(notes, autorNote(i, "delivery-lead",
			fmt.Sprintf("Tagesbericht %d", i),
			fmt.Sprintf("2026-01-%02dT08:00:00Z", (i%28)+1)))
	}
	return notes
}

// TestNotesFenster pins down the behaviour that was reported from operation:
// list_notes used to deliver the twenty OLDEST comments of a ticket and did not
// mention the truncation. Today the window sits at the new end, and the answer
// says how it relates to the whole.
func TestNotesFenster(t *testing.T) {
	notes := langerThread(130)
	var sawQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/notes"):
			sawQuery = r.URL.RawQuery
			serveNotes(w, r, notes)
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	sys := System{}
	cred := target.Credential{BaseURL: srv.URL, Token: "t"}
	ctx := context.Background()

	res, err := sys.Execute(ctx, "list_notes", []byte(`{"project_id":7,"issue_iid":42}`), cred)
	if err != nil {
		t.Fatalf("list_notes: %v", err)
	}
	out := res.(map[string]any)
	got := out["notes"].([]Note)
	if len(got) != notesWindowDefault {
		t.Fatalf("without a limit the window is %d comments, expected %d", len(got), notesWindowDefault)
	}
	// The NEWEST, and chronological within the window — 111…130, not 1…20.
	if got[0].ID != 111 || got[len(got)-1].ID != 130 {
		t.Errorf("the window is at the wrong end: %d…%d", got[0].ID, got[len(got)-1].ID)
	}
	if out["total"] != 130 || out["has_more"] != true || out["truncated"] != true {
		t.Errorf("the truncation is not stated: %+v", out)
	}
	if out["window"] != "newest 20 comments of 130" {
		t.Errorf("window = %q", out["window"])
	}
	if hint, _ := out["hint"].(string); !strings.Contains(hint, `"page":2`) {
		t.Errorf("the answer does not say how to page back: %q", hint)
	}
	if !strings.Contains(sawQuery, "per_page=20") || !strings.Contains(sawQuery, "sort=desc") {
		t.Errorf("the query does not fetch a window at the new end: %s", sawQuery)
	}

	// page=2 goes one window further into the past — without overlap.
	res, err = sys.Execute(ctx, "list_notes", []byte(`{"project_id":7,"issue_iid":42,"page":2}`), cred)
	if err != nil {
		t.Fatalf("list_notes page=2: %v", err)
	}
	out = res.(map[string]any)
	got = out["notes"].([]Note)
	if len(got) != 20 || got[0].ID != 91 || got[len(got)-1].ID != 110 {
		t.Errorf("page 2 is wrong: %d comments %d…%d", len(got), got[0].ID, got[len(got)-1].ID)
	}
	if out["window"] != "20 comments (older, 21–40 counted from the newest) of 130" {
		t.Errorf("window page 2 = %q", out["window"])
	}

	// limit enlarges the window, but only up to GitLab's maximum.
	res, err = sys.Execute(ctx, "list_notes", []byte(`{"project_id":7,"issue_iid":42,"limit":500}`), cred)
	if err != nil {
		t.Fatalf("list_notes limit=500: %v", err)
	}
	if got = res.(map[string]any)["notes"].([]Note); len(got) != notesWindowMax {
		t.Errorf("limit=500 delivers %d comments, expected the cap of %d", len(got), notesWindowMax)
	}

	// Mandatory parameters: list_notes used to be the only listing action that
	// did not check them.
	for name, params := range map[string]string{
		"without issue_iid":  `{"project_id":7}`,
		"without project_id": `{"issue_iid":42}`,
	} {
		if _, err := sys.Execute(ctx, "list_notes", []byte(params), cred); err == nil {
			t.Errorf("list_notes %s must fail", name)
		}
	}
}

// TestNotesFensterOhneHeader: not every instance (or proxy in front of it)
// delivers the pagination headers. Then the answer may not claim a total, but
// must still not describe a full page as a complete thread.
func TestNotesFensterOhneHeader(t *testing.T) {
	notes := langerThread(130)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Deliberately without X-Total/X-Total-Pages/X-Next-Page.
		perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
		umgedreht := append([]Note(nil), notes...)
		for i, j := 0, len(umgedreht)-1; i < j; i, j = i+1, j-1 {
			umgedreht[i], umgedreht[j] = umgedreht[j], umgedreht[i]
		}
		json.NewEncoder(w).Encode(umgedreht[:perPage])
	}))
	defer srv.Close()

	res, err := System{}.Execute(context.Background(), "list_notes",
		[]byte(`{"project_id":7,"issue_iid":42}`), target.Credential{BaseURL: srv.URL, Token: "t"})
	if err != nil {
		t.Fatalf("list_notes: %v", err)
	}
	out := res.(map[string]any)
	if _, da := out["total"]; da {
		t.Errorf("without X-Total no total may be claimed: %+v", out)
	}
	if out["has_more"] != true || out["truncated"] != true {
		t.Errorf("a full page must count as truncated: %+v", out)
	}
	if out["window"] != "newest 20 comments" {
		t.Errorf("window = %q", out["window"])
	}
}

// TestNotesBodyKappung: on threads carrying daily reports the LENGTH per entry
// is the cost driver, not the number. An over-long comment is cut, says so, and
// stays reachable in full through get_note.
func TestNotesBodyKappung(t *testing.T) {
	// Umlauts: two bytes each. Measured in bytes the cut would already bite at
	// 2000 characters — the limit counts CHARACTERS, as the prompt promises.
	grenzwertig := strings.Repeat("ä", notesBodyMax)
	lang := strings.Repeat("ä", notesBodyMax+500)
	notes := []Note{
		autorNote(1, "delivery-lead", "kurz", "2026-08-01T08:00:00Z"),
		autorNote(2, "delivery-lead", lang, "2026-08-02T08:00:00Z"),
		autorNote(3, "delivery-lead", grenzwertig, "2026-08-03T08:00:00Z"),
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/notes/2"):
			json.NewEncoder(w).Encode(notes[1])
		case strings.HasSuffix(r.URL.Path, "/notes"):
			serveNotes(w, r, notes)
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	sys := System{}
	cred := target.Credential{BaseURL: srv.URL, Token: "t"}
	ctx := context.Background()

	res, err := sys.Execute(ctx, "list_notes", []byte(`{"project_id":7,"issue_iid":42}`), cred)
	if err != nil {
		t.Fatalf("list_notes: %v", err)
	}
	got := res.(map[string]any)["notes"].([]Note)
	if got[0].BodyTruncated || got[0].Body != "kurz" {
		t.Errorf("a short comment must stay untouched: %+v", got[0])
	}
	if !got[1].BodyTruncated || got[1].BodyChars != notesBodyMax+500 {
		t.Errorf("the long comment is not marked: truncated=%v chars=%d (expected %d characters, NOT %d bytes)",
			got[1].BodyTruncated, got[1].BodyChars, notesBodyMax+500, len(lang))
	}
	if n := utf8.RuneCountInString(got[1].Body); n > notesBodyMax+64 {
		t.Errorf("the cut is ineffective: %d characters", n)
	}
	if !utf8.ValidString(got[1].Body) {
		t.Error("the cut tore a multi-byte character apart")
	}
	// Exactly at the limit nothing is cut — otherwise the marker would appear on
	// comments the promise still covers.
	if got[2].BodyTruncated || got[2].Body != grenzwertig {
		t.Errorf("a comment of exactly %d characters must stay untouched: truncated=%v", notesBodyMax, got[2].BodyTruncated)
	}

	// get_note fetches the full text back.
	res, err = sys.Execute(ctx, "get_note", []byte(`{"project_id":7,"issue_iid":42,"note_id":2}`), cred)
	if err != nil {
		t.Fatalf("get_note: %v", err)
	}
	if n := res.(Note); n.Body != lang {
		t.Errorf("get_note does not deliver the full text: %d instead of %d bytes", len(n.Body), len(lang))
	}
	if _, err := sys.Execute(ctx, "get_note", []byte(`{"project_id":7,"note_id":2}`), cred); err == nil {
		t.Error("get_note without issue_iid/mr_iid must fail")
	}
}

// TestWeckungLangerThread is the regression for the second, less visible half
// of the bug: threadSig formed the signature from the highest note id of the
// notes fetched. As long as those were always the same twenty OLDEST, the
// signature stopped changing — and a long thread stopped waking its agent.
func TestWeckungLangerThread(t *testing.T) {
	notes := langerThread(130)
	// The last word is a human's, so there is something waiting.
	notes = append(notes, autorNote(131, "mensch", "Und was ist mit Punkt 3?", "2026-02-01T09:00:00Z"))
	issue := Issue{IID: 42, ProjectID: 7}
	issue.References.Full = "gruppe/support#42"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v4/issues":
			json.NewEncoder(w).Encode([]Issue{issue})
		case r.URL.Path == "/api/v4/user":
			json.NewEncoder(w).Encode(User{ID: 1, Username: "delivery-lead"})
		case strings.HasSuffix(r.URL.Path, "/notes"):
			serveNotes(w, r, notes)
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	cred := target.Credential{BaseURL: srv.URL, Token: "t"}
	ctx := context.Background()

	has, sig, err := System{}.HasWorkSigned(ctx, cred, "issues")
	if err != nil || !has {
		t.Fatalf("the open question must wake: has=%v err=%v", has, err)
	}

	// A new comment arrives — the signature must change, otherwise the control
	// plane takes the wake-up as already served.
	notes = append(notes, autorNote(132, "mensch", "Ping?", "2026-02-02T09:00:00Z"))
	has2, sig2, err := System{}.HasWorkSigned(ctx, cred, "issues")
	if err != nil || !has2 {
		t.Fatalf("the follow-up must wake too: has=%v err=%v", has2, err)
	}
	if sig == sig2 {
		t.Errorf("the signature does not change on a new comment: %q", sig)
	}
}

// TestDuplikatSchutzLangerThread: the loop protection compares against one's own
// LAST comment. Fetched from the wrong end of a long thread it never found it —
// and the agent wrote the same text into the ticket in every run.
func TestDuplikatSchutzLangerThread(t *testing.T) {
	notes := langerThread(130)
	notes = append(notes, autorNote(131, "delivery-lead", "Tagesbericht 131", "2026-02-01T08:00:00Z"))
	var posts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v4/user":
			json.NewEncoder(w).Encode(User{ID: 1, Username: "delivery-lead"})
		case strings.HasSuffix(r.URL.Path, "/notes") && r.Method == http.MethodPost:
			posts++
			json.NewEncoder(w).Encode(Note{ID: 999})
		case strings.HasSuffix(r.URL.Path, "/notes"):
			serveNotes(w, r, notes)
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	sys := System{}
	cred := target.Credential{BaseURL: srv.URL, Token: "t"}
	ctx := context.Background()

	res, err := sys.Execute(ctx, "comment", []byte(`{"project_id":7,"issue_iid":42,"body":"Tagesbericht 131"}`), cred)
	if err != nil {
		t.Fatalf("comment: %v", err)
	}
	if out, ok := res.(map[string]any); !ok || out["skipped"] != "duplicate" {
		t.Errorf("the repetition of one's own last comment must be suppressed: %#v", res)
	}
	if posts != 0 {
		t.Errorf("despite the duplicate %d comments were posted", posts)
	}

	// Something new goes through, of course.
	if _, err := sys.Execute(ctx, "comment", []byte(`{"project_id":7,"issue_iid":42,"body":"Tagesbericht 132"}`), cred); err != nil {
		t.Fatalf("comment (new): %v", err)
	}
	if posts != 1 {
		t.Errorf("a new comment must be posted: posts=%d", posts)
	}
}

// TestNotesFensterWeiterblaettern pins down the three cases in which a window
// that describes itself wrongly does more damage than none at all: a follow-up
// call that silently changes the grid, an instance whose X-Next-Page is
// swallowed on the way, and a page behind the end of the thread.
func TestNotesFensterWeiterblaettern(t *testing.T) {
	notes := langerThread(130)
	kopfLos := false // true = answer WITHOUT X-Next-Page (a proxy filters it out)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if kopfLos {
			rec := httptest.NewRecorder()
			serveNotes(rec, r, notes)
			for k, v := range rec.Header() {
				if k != "X-Next-Page" {
					w.Header()[k] = v
				}
			}
			w.Write(rec.Body.Bytes())
			return
		}
		serveNotes(w, r, notes)
	}))
	defer srv.Close()

	sys := System{}
	cred := target.Credential{BaseURL: srv.URL, Token: "t"}
	ctx := context.Background()
	hole := func(params string) map[string]any {
		t.Helper()
		res, err := sys.Execute(ctx, "list_notes", []byte(params), cred)
		if err != nil {
			t.Fatalf("list_notes %s: %v", params, err)
		}
		return res.(map[string]any)
	}

	// 1. The hint has to carry the limit along. Whoever fetches 100 and then
	// follows a hint without limit lands on window 21–40 of a 20-grid: comments
	// they already have, while 100…31 fall through unnoticed.
	out := hole(`{"project_id":7,"issue_iid":42,"limit":100}`)
	if out["limit"] != 100 {
		t.Errorf("the answer does not say which window size applies: %+v", out["limit"])
	}
	hint, _ := out["hint"].(string)
	if !strings.Contains(hint, `"limit":100`) || !strings.Contains(hint, `"page":2`) {
		t.Errorf("the hint drops the limit and thereby changes the grid: %q", hint)
	}
	weiter := hole(`{"project_id":7,"issue_iid":42,"limit":100,"page":2}`)
	got := weiter["notes"].([]Note)
	if len(got) != 30 || got[len(got)-1].ID != 30 {
		t.Errorf("the second window does not join on seamlessly: %d comments up to id %d", len(got), got[len(got)-1].ID)
	}

	// 2. Last page, exactly full: 130 comments in windows of 65 — page 2 ends
	// precisely at the last comment, and nothing more may be promised.
	out = hole(`{"project_id":7,"issue_iid":42,"limit":65,"page":2}`)
	if out["has_more"] != false {
		t.Errorf("an exactly full last page must not promise more: %+v", out)
	}

	// 3. Without X-Next-Page the counts have to decide. Reading the header's mere
	// presence turned page 1 of 7 into a "complete thread" — the very silent
	// truncation this window exists to abolish.
	kopfLos = true
	out = hole(`{"project_id":7,"issue_iid":42}`)
	if out["has_more"] != true || out["truncated"] != true || out["window"] != "newest 20 comments of 130" {
		t.Errorf("without X-Next-Page the truncation falls under the table: %+v", out)
	}
	kopfLos = false

	// 4. Behind the end of the thread: the answer must lead back, not further
	// backwards.
	out = hole(`{"project_id":7,"issue_iid":42,"page":9}`)
	if len(out["notes"].([]Note)) != 0 {
		t.Fatalf("page 9 of a 7-page thread must be empty: %+v", out["notes"])
	}
	if hint, _ = out["hint"].(string); !strings.Contains(hint, `"page":1`) {
		t.Errorf("the empty page does not lead back to the current state: %q", hint)
	}
	if w, _ := out["window"].(string); !strings.Contains(w, "empty") {
		t.Errorf("window = %q", w)
	}
}

// TestIssueOverflowSignatureTracksEdits pins the bug this replaces. Above
// issueMaxNotesChecks the gate cannot afford a ListNotes per issue, and its
// fallback used to be len(inScope) alone. That made it count-sensitive and
// otherwise blind: with a stable number of assigned issues the signature never
// changed and "Zugewiesene Issues sichten" rested indefinitely — observed here
// as 15 hours of silence across 100 assigned issues.
//
// The three cases below are exactly the ones a count cannot tell apart.
func TestIssueOverflowSignatureTracksEdits(t *testing.T) {
	many := func(n int, stamp string) []Issue {
		out := make([]Issue, 0, n)
		for i := 1; i <= n; i++ {
			out = append(out, Issue{IID: i, ProjectID: 7, UpdatedAt: stamp})
		}
		return out
	}

	base := many(issueMaxNotesChecks+5, "2026-08-12T10:00:00Z")
	sigBase := workSig(issueStampSigs(base))
	if sigBase == "" {
		t.Fatal("an overflowing issue set must produce a signature")
	}

	// 1. Nothing happened: the signature has to stay put, or every tick wakes.
	if got := workSig(issueStampSigs(many(issueMaxNotesChecks+5, "2026-08-12T10:00:00Z"))); got != sigBase {
		t.Error("an unchanged issue set must keep its signature")
	}

	// 2. A comment on ONE issue — same count. This is the case the old fallback
	//    could not see at all.
	edited := many(issueMaxNotesChecks+5, "2026-08-12T10:00:00Z")
	edited[3].UpdatedAt = "2026-08-12T11:00:00Z"
	if workSig(issueStampSigs(edited)) == sigBase {
		t.Error("an edit to a single issue must change the signature (count is unchanged)")
	}

	// 3. One issue closes as another opens — count unchanged, membership not.
	swapped := many(issueMaxNotesChecks+5, "2026-08-12T10:00:00Z")
	swapped[0].IID = 9999
	if workSig(issueStampSigs(swapped)) == sigBase {
		t.Error("a swapped issue must change the signature (count is unchanged)")
	}

	// GitLab returns the list ordered by updated_at, so the same set arriving in
	// a different order must not read as new work.
	rev := many(issueMaxNotesChecks+5, "2026-08-12T10:00:00Z")
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	if workSig(issueStampSigs(rev)) != sigBase {
		t.Error("reordering the same set must not change the signature")
	}
}

// TestIssueOverflowSignatureWithoutTimestamp: a response without updated_at must
// degrade to identity rather than collapsing every issue onto one entry.
func TestIssueOverflowSignatureWithoutTimestamp(t *testing.T) {
	sigs := issueStampSigs([]Issue{{IID: 1, ProjectID: 7}, {IID: 2, ProjectID: 7}})
	if len(sigs) != 2 || sigs[0] == sigs[1] {
		t.Fatalf("issues without updated_at must stay distinguishable, got %v", sigs)
	}
}

// TestListIssuesPaginates pins the truncation this replaces: a single request
// with per_page=100 cut the working set at 100, and order_by=updated_at
// descending decided which issues fell off — the longest-untouched ones, i.e.
// exactly what a triage run exists to surface. Observed as 74 of 174 assigned
// issues invisible to every agent.
func TestListIssuesPaginates(t *testing.T) {
	var pagesServed []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		pagesServed = append(pagesServed, page)
		if got := r.URL.Query().Get("per_page"); got != "100" {
			t.Errorf("per_page = %q, want GitLab's maximum 100", got)
		}
		var out []Issue
		switch page {
		case "1":
			for i := 1; i <= 100; i++ {
				out = append(out, Issue{IID: i, ProjectID: 7})
			}
		case "2":
			for i := 101; i <= 174; i++ { // short page → last page
				out = append(out, Issue{IID: i, ProjectID: 7})
			}
		default:
			t.Errorf("asked for page %q after a short page — the walk must stop", page)
		}
		json.NewEncoder(w).Encode(out)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "t")
	issues, err := c.ListIssues(context.Background(), 0, "opened", "", "", "", true)
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(issues) != 174 {
		t.Fatalf("got %d issues, want all 174 across both pages", len(issues))
	}
	if len(pagesServed) != 2 {
		t.Errorf("pages requested = %v, want exactly page 1 and 2", pagesServed)
	}
	// The tail is the point: these are the stale tickets the old cap hid.
	if issues[len(issues)-1].IID != 174 {
		t.Errorf("last issue IID = %d, want 174 — the tail must survive", issues[len(issues)-1].IID)
	}
}

// TestListIssuesStopsOnExactMultiple: a full last page must not spin forever,
// and must not lose the page that follows it.
func TestListIssuesStopsOnExactMultiple(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var out []Issue
		if r.URL.Query().Get("page") == "1" {
			for i := 1; i <= 100; i++ {
				out = append(out, Issue{IID: i, ProjectID: 7})
			}
		} // page 2 → empty, ends the walk
		json.NewEncoder(w).Encode(out)
	}))
	defer srv.Close()

	issues, err := NewClient(srv.URL, "t").ListIssues(context.Background(), 0, "opened", "", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 100 || calls != 2 {
		t.Errorf("got %d issues in %d calls, want 100 in 2", len(issues), calls)
	}
}

// TestListIssuesFirstPageErrorIsAnError: a failure on page 1 must not read as
// "no issues" — that would let a triage run report all clear on an outage.
func TestListIssuesFirstPageErrorIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	if _, err := NewClient(srv.URL, "t").ListIssues(context.Background(), 0, "opened", "", "", "", true); err == nil {
		t.Fatal("a failing first page must return an error, not an empty list")
	}
}

// TestAbridgeIssueMarksTruncation: the cut has to be visible, or an agent reads
// a clipped description as an underspecified ticket.
func TestAbridgeIssueMarksTruncation(t *testing.T) {
	short := Issue{Description: "kurz und vollständig"}
	if abridgeIssue(short).Description != short.Description {
		t.Error("a short description must pass through untouched")
	}

	long := Issue{Description: strings.Repeat("ä", issueDescriptionSnippet+50)}
	got := abridgeIssue(long).Description
	if !strings.Contains(got, "get_issue") {
		t.Error("a truncated description must say where the full text is")
	}
	// Multi-byte safety: cutting []byte would have produced invalid UTF-8.
	if !utf8.ValidString(got) {
		t.Error("truncation broke UTF-8 — cut runes, not bytes")
	}
	if runes := []rune(got); len(runes) <= issueDescriptionSnippet {
		t.Errorf("expected snippet plus marker, got %d runes", len(runes))
	}
}
