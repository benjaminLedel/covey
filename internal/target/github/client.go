// Package github binds GitHub in as a target-system plugin (the same pattern as
// the gitlab package): a REST client for the agent actions, an optional webhook
// intake (HMAC-SHA256) and the polling checks that carry the review loop. The
// unit of work is the issue.
//
// One difference to GitLab runs through everything: a repository is addressed
// by its NAME ("owner/repo"), not by a numeric id. That is GitHub's natural
// identifier — it stands in every URL the agent reads, so it does not have to
// look an id up first.
package github

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"covey/internal/reqlog"
)

// defaultBaseURL is github.com's API. The plugin therefore works without a
// github_url secret (Descriptor.BaseURLOptional); the override exists for
// GitHub Enterprise Server.
const defaultBaseURL = "https://api.github.com"

// Client speaks the GitHub REST API with a (brokered) token. The token comes
// from the SecretStore per call — it is never persisted.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// NewClient normalises the endpoint. Three spellings arrive here and all three
// have to work, because whoever enters github_url takes it from their browser:
//
//	""                          → https://api.github.com  (github.com)
//	https://ghe.example.com     → https://ghe.example.com/api/v3  (Enterprise)
//	https://ghe.example.com/api/v3 → taken as it stands
//
// The rule is deliberately mechanical: an address that does not already carry
// an API path and whose host is not an "api." host gets GitHub Enterprise
// Server's /api/v3 appended.
func NewClient(baseURL, token string) *Client {
	return &Client{
		BaseURL: normalizeBaseURL(baseURL),
		Token:   token,
		HTTP:    reqlog.Client("github", 15*time.Second),
	}
}

func normalizeBaseURL(raw string) string {
	b := strings.TrimRight(strings.TrimSpace(raw), "/")
	if b == "" {
		return defaultBaseURL
	}
	if strings.Contains(b, "/api/") || strings.HasSuffix(b, "/api") {
		return b
	}
	if u, err := url.Parse(b); err == nil && strings.HasPrefix(u.Hostname(), "api.") {
		return b
	}
	return b + "/api/v3"
}

// do carries out a JSON request. body == nil sends none, out == nil discards
// the answer.
func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	resp, err := c.raw(ctx, method, path, body, c.HTTP)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if out != nil && len(bytes.TrimSpace(data)) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

// raw carries out a request and hands the still-open response back — for binary
// bodies (the repository archive, a job log) that do not belong in memory. The
// caller closes the body. Errors are already unwrapped here, so that every
// caller reports the same message.
func (c *Client) raw(ctx context.Context, method, path string, body any, hc *http.Client) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	// Pinning the API version: GitHub changes the REST surface behind date
	// headers. Without the pin a server-side change would land in the plugin
	// unannounced.
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if hc == nil {
		hc = c.HTTP
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		resp.Body.Close()
		return nil, fmt.Errorf("github %s %s: HTTP %d%s: %.300s",
			method, path, resp.StatusCode, rateLimitHint(resp), data)
	}
	return resp, nil
}

// rateLimitHint turns GitHub's rate limit into a sentence the agent can act on.
// A bare "HTTP 403" reads like a permission problem and sends the agent looking
// for the wrong cause; the remaining quota decides which of the two it is.
func rateLimitHint(resp *http.Response) string {
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusTooManyRequests {
		return ""
	}
	if resp.Header.Get("X-RateLimit-Remaining") != "0" {
		return ""
	}
	reset := ""
	if ts, err := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64); err == nil {
		reset = " until " + time.Unix(ts, 0).UTC().Format(time.RFC3339)
	}
	return fmt.Sprintf(" (rate limit exhausted%s — this is not a permission problem; wait rather than retrying)", reset)
}

// Repo is a repository. Full ("owner/name") is the identifier every action
// takes.
type Repo struct {
	Full          string `json:"repo"`
	FullName      string `json:"full_name"`
	Description   string `json:"description"`
	HTMLURL       string `json:"html_url"`
	DefaultBranch string `json:"default_branch"`
	Private       bool   `json:"private"`
	Archived      bool   `json:"archived"`
	Permissions   struct {
		Push bool `json:"push"`
	} `json:"permissions"`
}

// User is an account. Login is what every action expects as "username" — the
// numeric id plays no part in GitHub's API.
type User struct {
	Login string `json:"login"`
	Type  string `json:"type"` // "User" | "Bot" | "Organization"
}

// Label carries the name only; colour and description do not belong in the
// agent's context.
type Label struct {
	Name string `json:"name"`
}

// Issue is an issue. GitHub also lists pull requests through the issue
// endpoints — PullRequest != nil marks exactly that case, and the plugin sorts
// those out (a PR is worked on through the pull actions).
type Issue struct {
	Repo      string  `json:"repo"`
	Number    int     `json:"number"`
	Title     string  `json:"title"`
	Body      string  `json:"body"`
	State     string  `json:"state"`
	Labels    []Label `json:"labels"`
	HTMLURL   string  `json:"html_url"`
	User      User    `json:"user"`
	Assignees []User  `json:"assignees"`
	// Milestone attaches the issue to an undertaking (a release, a sprint).
	// GitHub returns null when there is none.
	Milestone *struct {
		Title string `json:"title"`
		DueOn string `json:"due_on"`
		State string `json:"state"`
	} `json:"milestone"`
	Comments  int    `json:"comments"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	// PullRequest is set by GitHub when this "issue" is in fact a PR.
	PullRequest *struct {
		HTMLURL string `json:"html_url"`
	} `json:"pull_request,omitempty"`
	// Repository comes back from the global GET /issues only; for the
	// repository endpoint the plugin fills Repo in itself.
	Repository *struct {
		FullName string `json:"full_name"`
	} `json:"repository,omitempty"`
}

// IsPullRequest reports whether an entry from an issue list is in truth a pull
// request.
func (i Issue) IsPullRequest() bool { return i.PullRequest != nil }

// Comment is a contribution to an issue or pull request. GitHub keeps three
// kinds apart that mean the same thing to the agent — an issue comment, a
// review (with a verdict) and a review comment on a line of the diff. The
// plugin merges them into ONE chronological list; Kind says where the entry
// comes from.
type Comment struct {
	ID        int64  `json:"id"`
	Kind      string `json:"kind,omitempty"` // comment | review | review_comment
	Body      string `json:"body"`
	User      User   `json:"user"`
	CreatedAt string `json:"created_at"`
	HTMLURL   string `json:"html_url"`
	// State carries a review's verdict (APPROVED, CHANGES_REQUESTED,
	// COMMENTED); empty for ordinary comments.
	State string `json:"state,omitempty"`
	// Path/Line locate a review comment in the diff.
	Path string `json:"path,omitempty"`
	Line int    `json:"line,omitempty"`
	// SubmittedAt is a review's timestamp; normalised into CreatedAt.
	SubmittedAt string `json:"submitted_at,omitempty"`
}

// PullRequest is a pull request — GitHub's counterpart to the merge request.
type PullRequest struct {
	Repo   string `json:"repo"`
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	State  string `json:"state"` // open | closed
	Draft  bool   `json:"draft"`
	// Merged comes back from the single-PR endpoint only. MergedAt is in the
	// list answer too, so a "merged" filter over a list costs no extra request
	// per entry — which at 100 hits is the difference between one request and
	// a hundred.
	Merged   bool   `json:"merged"`
	MergedAt string `json:"merged_at,omitempty"`
	HTMLURL  string `json:"html_url"`
	User     User   `json:"user"`
	Head     struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
	Assignees          []User `json:"assignees"`
	RequestedReviewers []User `json:"requested_reviewers"`
	// Mergeable/MergeableState are GitHub's review state. Mergeable is null
	// while GitHub is still computing the merge — a fact the agent has to see,
	// hence the pointer.
	Mergeable      *bool  `json:"mergeable"`
	MergeableState string `json:"mergeable_state,omitempty"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

// splitRepo takes an agent's "owner/name" apart and checks it. Everything that
// goes into a URL path passes through here — a repository name the agent
// invented must not be able to leave the path (CWE-22) or address a foreign
// endpoint.
func splitRepo(full string) (owner, name string, err error) {
	full = strings.Trim(strings.TrimSpace(full), "/")
	owner, name, ok := strings.Cut(full, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return "", "", fmt.Errorf("repo %q invalid — expected \"owner/name\" (e.g. acme/support)", full)
	}
	for _, part := range []string{owner, name} {
		if part == "." || part == ".." || strings.ContainsAny(part, "?#%\\ ") {
			return "", "", fmt.Errorf("repo %q invalid — expected \"owner/name\"", full)
		}
	}
	return owner, name, nil
}

// repoPath builds "/repos/owner/name" + suffix from a checked repository name.
func repoPath(full, suffix string) (string, error) {
	owner, name, err := splitRepo(full)
	if err != nil {
		return "", err
	}
	return "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name) + suffix, nil
}

// perPage is the page size of every list request. Like the GitLab plugin the
// client deliberately does NOT page: one request, at most 100 hits. Whoever
// needs more narrows the query — a plugin that silently fetches thousands of
// issues fills the agent's context instead of its working set. The prompt says
// so plainly, because a truncated list that pretends to be complete is the
// worse failure.
const perPage = 100

// CurrentUser — GET /user: the bot's own identity. Needed everywhere a decision
// hangs off "did I write that myself?".
func (c *Client) CurrentUser(ctx context.Context) (User, error) {
	var u User
	err := c.do(ctx, http.MethodGet, "/user", nil, &u)
	return u, err
}

// ListRepos — GET /user/repos: the repositories the bot user can reach. The
// entry point for agents that do not yet know their repo names.
func (c *Client) ListRepos(ctx context.Context) ([]Repo, error) {
	var out []Repo
	err := c.do(ctx, http.MethodGet,
		fmt.Sprintf("/user/repos?affiliation=owner,collaborator,organization_member&sort=pushed&per_page=%d", perPage), nil, &out)
	for i := range out {
		out[i].Full = out[i].FullName
	}
	return out, err
}

// GetRepo — GET /repos/{owner}/{repo}: needed above all for the default branch,
// which commit and create_pull_request fall back to.
func (c *Client) GetRepo(ctx context.Context, repo string) (Repo, error) {
	path, err := repoPath(repo, "")
	if err != nil {
		return Repo{}, err
	}
	var r Repo
	err = c.do(ctx, http.MethodGet, path, nil, &r)
	r.Full = r.FullName
	return r, err
}

// ListIssues finds issues — with repo through GET /repos/{owner}/{repo}/issues,
// without one (repo == "") through the global GET /issues, which returns the
// issues of every repository the token can see.
//
// Two filters GitHub's list endpoint does not offer are applied here, on the
// answer: search (a substring of title/body) and milestone by TITLE — GitHub
// wants the milestone NUMBER, which differs per repository and which the agent
// therefore cannot know. Filtering afterwards keeps the parameter meaning the
// same as in the GitLab plugin.
//
// Pull requests are sorted out: GitHub delivers them through the issue
// endpoints too, but they are worked on through the pull actions.
func (c *Client) ListIssues(ctx context.Context, repo, state, labels, search, milestone string, assigned bool) ([]Issue, error) {
	wantState, err := normalizeIssueState(state)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("state", wantState)
	q.Set("per_page", strconv.Itoa(perPage))
	q.Set("sort", "updated")
	if labels != "" {
		q.Set("labels", labels)
	}
	var path string
	if strings.TrimSpace(repo) == "" {
		// The global endpoint: filter=assigned narrows it to the bot's own
		// issues, filter=all returns everything it can see.
		if assigned {
			q.Set("filter", "assigned")
		} else {
			q.Set("filter", "all")
		}
		path = "/issues?" + q.Encode()
	} else {
		if assigned {
			me, err := c.CurrentUser(ctx)
			if err != nil {
				return nil, err
			}
			q.Set("assignee", me.Login)
		}
		p, err := repoPath(repo, "/issues?"+q.Encode())
		if err != nil {
			return nil, err
		}
		path = p
	}
	var raw []Issue
	if err := c.do(ctx, http.MethodGet, path, nil, &raw); err != nil {
		return nil, err
	}
	out := []Issue{}
	for _, i := range raw {
		if i.IsPullRequest() {
			continue
		}
		i.Repo = issueRepo(i, repo)
		if !matchesSearch(search, i.Title, i.Body) || !matchesMilestone(milestone, i) {
			continue
		}
		out = append(out, i)
	}
	return out, nil
}

// normalizeIssueState accepts GitLab's vocabulary too ("opened"), because agent
// playbooks are written across systems. Anything else is an ERROR rather than a
// silent fallback: a typo that quietly widens the state to "all" hands the agent
// closed issues it then comments on, and nothing in the answer says so.
func normalizeIssueState(state string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "", "open", "opened":
		return "open", nil
	case "closed", "close":
		return "closed", nil
	case "all":
		return "all", nil
	default:
		return "", fmt.Errorf("state %q unknown — expected \"open\", \"closed\" or \"all\"", state)
	}
}

func issueRepo(i Issue, fallback string) string {
	if i.Repository != nil && i.Repository.FullName != "" {
		return i.Repository.FullName
	}
	return strings.TrimSpace(fallback)
}

func matchesSearch(search string, fields ...string) bool {
	search = strings.ToLower(strings.TrimSpace(search))
	if search == "" {
		return true
	}
	for _, f := range fields {
		if strings.Contains(strings.ToLower(f), search) {
			return true
		}
	}
	return false
}

func matchesMilestone(title string, i Issue) bool {
	title = strings.TrimSpace(title)
	if title == "" {
		return true
	}
	return i.Milestone != nil && strings.EqualFold(i.Milestone.Title, title)
}

// GetIssue — GET /repos/{owner}/{repo}/issues/{number}.
func (c *Client) GetIssue(ctx context.Context, repo string, number int) (Issue, error) {
	path, err := repoPath(repo, fmt.Sprintf("/issues/%d", number))
	if err != nil {
		return Issue{}, err
	}
	var i Issue
	if err := c.do(ctx, http.MethodGet, path, nil, &i); err != nil {
		return Issue{}, err
	}
	i.Repo = issueRepo(i, repo)
	return i, nil
}

// CreateIssue — POST /repos/{owner}/{repo}/issues.
func (c *Client) CreateIssue(ctx context.Context, repo, title, body string, labels, assignees []string) (Issue, error) {
	path, err := repoPath(repo, "/issues")
	if err != nil {
		return Issue{}, err
	}
	in := map[string]any{"title": title}
	if body != "" {
		in["body"] = body
	}
	if len(labels) > 0 {
		in["labels"] = labels
	}
	if len(assignees) > 0 {
		in["assignees"] = assignees
	}
	var out Issue
	if err := c.do(ctx, http.MethodPost, path, in, &out); err != nil {
		return Issue{}, err
	}
	out.Repo = issueRepo(out, repo)
	return out, nil
}

// ListComments — GET /repos/{owner}/{repo}/issues/{number}/comments. Works for
// pull requests too: to GitHub a PR is an issue with a branch attached, and its
// conversation runs through the same endpoint.
//
// Fetched NEWEST first and turned round afterwards. The client does not page,
// and on a thread longer than perPage the natural (oldest-first) order would
// hand back the opening exchange and cut off the end — the very part every
// decision here hangs on ("who wrote last?", "has my question been answered?").
// Losing the beginning of a long thread costs context; losing the end produces
// wrong answers.
func (c *Client) ListComments(ctx context.Context, repo string, number int) ([]Comment, error) {
	path, err := repoPath(repo, fmt.Sprintf("/issues/%d/comments?per_page=%d&sort=created&direction=desc", number, perPage))
	if err != nil {
		return nil, err
	}
	out := []Comment{}
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	slices.Reverse(out)
	for i := range out {
		out[i].Kind = "comment"
	}
	return out, nil
}

// Comment — POST …/issues/{number}/comments. GitHub knows no internal comments:
// every contribution is visible to whoever can see the repository. The plugin
// says so instead of pretending to an "internal" that does not exist.
func (c *Client) Comment(ctx context.Context, repo string, number int, body string) (Comment, error) {
	path, err := repoPath(repo, fmt.Sprintf("/issues/%d/comments", number))
	if err != nil {
		return Comment{}, err
	}
	var out Comment
	err = c.do(ctx, http.MethodPost, path, map[string]any{"body": body}, &out)
	out.Kind = "comment"
	return out, err
}

// SetState — PATCH …/issues/{number}. Accepts GitLab's verbs
// ("close"/"reopen") as well as GitHub's states ("closed"/"open").
func (c *Client) SetState(ctx context.Context, repo string, number int, state string) (Issue, error) {
	var want string
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "close", "closed":
		want = "closed"
	case "reopen", "open", "opened":
		want = "open"
	default:
		return Issue{}, fmt.Errorf("state %q unknown — expected \"close\" or \"reopen\"", state)
	}
	path, err := repoPath(repo, fmt.Sprintf("/issues/%d", number))
	if err != nil {
		return Issue{}, err
	}
	var out Issue
	err = c.do(ctx, http.MethodPatch, path, map[string]any{"state": want}, &out)
	out.Repo = issueRepo(out, repo)
	return out, err
}

// AddAssignees — POST …/issues/{number}/assignees. GitHub adds; it does not
// replace. That is what the action wants: a handover names an additional
// person, it does not wipe the existing assignment.
func (c *Client) AddAssignees(ctx context.Context, repo string, number int, logins []string) (Issue, error) {
	path, err := repoPath(repo, fmt.Sprintf("/issues/%d/assignees", number))
	if err != nil {
		return Issue{}, err
	}
	var out Issue
	err = c.do(ctx, http.MethodPost, path, map[string]any{"assignees": logins}, &out)
	out.Repo = issueRepo(out, repo)
	return out, err
}

// Escalate posts a comment and hands the item back: the bot removes its OWN
// assignment so that a human takes the issue over. GitHub knows no internal
// comments, so the note is visible to whoever can see the repository — the
// prompt says so, and the agent phrases it accordingly.
func (c *Client) Escalate(ctx context.Context, repo string, number int, note string) error {
	if _, err := c.Comment(ctx, repo, number, note); err != nil {
		return err
	}
	me, err := c.CurrentUser(ctx)
	if err != nil || me.Login == "" {
		// Without an identity there is nothing to hand back. The comment is the
		// escalation and it stands — the handover is the addition, so this is
		// not a failure to report.
		return nil
	}
	path, err := repoPath(repo, fmt.Sprintf("/issues/%d/assignees", number))
	if err != nil {
		return err
	}
	return c.do(ctx, http.MethodDelete, path, map[string]any{"assignees": []string{me.Login}}, nil)
}

// SetLabels works additively/subtractively instead of overwriting the whole
// list — otherwise every state change takes the subject-matter labels with it.
// GitHub has one endpoint per direction, so this is two calls at most.
func (c *Client) SetLabels(ctx context.Context, repo string, number int, add, remove []string) ([]string, error) {
	if len(add) == 0 && len(remove) == 0 {
		return nil, fmt.Errorf("add_labels and remove_labels are both empty — nothing to do")
	}
	for _, l := range append(append([]string{}, add...), remove...) {
		if strings.Contains(l, ",") {
			return nil, fmt.Errorf("label %q contains a comma — put every label in its own list entry", l)
		}
	}
	for _, name := range remove {
		path, err := repoPath(repo, fmt.Sprintf("/issues/%d/labels/%s", number, url.PathEscape(strings.TrimSpace(name))))
		if err != nil {
			return nil, err
		}
		// A label that is not set is not an error for the caller — the goal
		// state is what counts, not the route to it.
		if err := c.do(ctx, http.MethodDelete, path, nil, nil); err != nil && !strings.Contains(err.Error(), "HTTP 404") {
			return nil, err
		}
	}
	labels := []Label{}
	if len(add) > 0 {
		path, err := repoPath(repo, fmt.Sprintf("/issues/%d/labels", number))
		if err != nil {
			return nil, err
		}
		if err := c.do(ctx, http.MethodPost, path, map[string]any{"labels": add}, &labels); err != nil {
			return nil, err
		}
	} else {
		path, err := repoPath(repo, fmt.Sprintf("/issues/%d/labels?per_page=%d", number, perPage))
		if err != nil {
			return nil, err
		}
		if err := c.do(ctx, http.MethodGet, path, nil, &labels); err != nil {
			return nil, err
		}
	}
	out := []string{}
	for _, l := range labels {
		out = append(out, l.Name)
	}
	return out, nil
}

// Branch is a branch with the head commit it points at.
type Branch struct {
	Name      string `json:"name"`
	Protected bool   `json:"protected"`
	Default   bool   `json:"default,omitempty"`
	Commit    struct {
		SHA string `json:"sha"`
	} `json:"commit"`
}

// ListBranches — GET …/branches. search filters afterwards; GitHub's endpoint
// has no such parameter.
func (c *Client) ListBranches(ctx context.Context, repo, search string) ([]Branch, error) {
	path, err := repoPath(repo, fmt.Sprintf("/branches?per_page=%d", perPage))
	if err != nil {
		return nil, err
	}
	var raw []Branch
	if err := c.do(ctx, http.MethodGet, path, nil, &raw); err != nil {
		return nil, err
	}
	def := ""
	if r, err := c.GetRepo(ctx, repo); err == nil {
		def = r.DefaultBranch
	}
	out := []Branch{}
	for _, b := range raw {
		if !matchesSearch(search, b.Name) {
			continue
		}
		b.Default = b.Name == def
		out = append(out, b)
	}
	return out, nil
}

// Commit is an entry of the commit history.
type Commit struct {
	SHA    string `json:"sha"`
	Commit struct {
		Message string `json:"message"`
		Author  struct {
			Name string `json:"name"`
			Date string `json:"date"`
		} `json:"author"`
	} `json:"commit"`
	HTMLURL string `json:"html_url"`
}

// ListCommits — GET …/commits. All filters are optional: sha (branch/tag/SHA),
// path (only commits touching that file) and since (an ISO date).
func (c *Client) ListCommits(ctx context.Context, repo, ref, path, since string) ([]Commit, error) {
	q := url.Values{}
	q.Set("per_page", strconv.Itoa(perPage))
	if ref != "" {
		q.Set("sha", ref)
	}
	if path != "" {
		q.Set("path", path)
	}
	if since != "" {
		q.Set("since", since)
	}
	p, err := repoPath(repo, "/commits?"+q.Encode())
	if err != nil {
		return nil, err
	}
	out := []Commit{}
	err = c.do(ctx, http.MethodGet, p, nil, &out)
	return out, err
}

// maxDiffBytesPerFile caps a single file's patch — a generated file's diff
// would otherwise crowd everything else out of the agent's context.
const maxDiffBytesPerFile = 16 << 10

// CommitDiff is one file's change within a commit.
type CommitDiff struct {
	Filename  string `json:"filename"`
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Patch     string `json:"patch,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

// GetCommitDiff — GET …/commits/{sha}: the commit with its file changes. Long
// patches are cut off; the flag says so, so that the agent does not read a
// half-diff as the whole truth.
func (c *Client) GetCommitDiff(ctx context.Context, repo, sha string) ([]CommitDiff, error) {
	if strings.TrimSpace(sha) == "" {
		return nil, fmt.Errorf("sha missing")
	}
	path, err := repoPath(repo, "/commits/"+url.PathEscape(sha))
	if err != nil {
		return nil, err
	}
	var out struct {
		Files []CommitDiff `json:"files"`
	}
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	files := []CommitDiff{}
	for _, f := range out.Files {
		if len(f.Patch) > maxDiffBytesPerFile {
			f.Patch = f.Patch[:maxDiffBytesPerFile]
			f.Truncated = true
		}
		files = append(files, f)
	}
	return files, nil
}

// TreeEntry is one entry of the repository tree.
type TreeEntry struct {
	Path string `json:"path"`
	Type string `json:"type"` // blob | tree
	Size int    `json:"size,omitempty"`
	SHA  string `json:"sha,omitempty"`
}

// ListTree lists the repository tree. Two routes, because GitHub splits the
// job: non-recursively the contents API (one directory), recursively the git
// trees API filtered by the path prefix. Both are capped at perPage entries —
// a whole tree does not belong in an agent's context.
func (c *Client) ListTree(ctx context.Context, repo, path, ref string, recursive bool) ([]TreeEntry, error) {
	if !recursive {
		p, err := repoPath(repo, "/contents/"+escapePath(path))
		if err != nil {
			return nil, err
		}
		if ref != "" {
			p += "?ref=" + url.QueryEscape(ref)
		}
		var raw []struct {
			Path string `json:"path"`
			Type string `json:"type"` // file | dir
			Size int    `json:"size"`
			SHA  string `json:"sha"`
		}
		if err := c.do(ctx, http.MethodGet, p, nil, &raw); err != nil {
			return nil, err
		}
		out := []TreeEntry{}
		for i, e := range raw {
			if i >= perPage {
				break
			}
			kind := "blob"
			if e.Type == "dir" {
				kind = "tree"
			}
			out = append(out, TreeEntry{Path: e.Path, Type: kind, Size: e.Size, SHA: e.SHA})
		}
		return out, nil
	}

	if ref == "" {
		r, err := c.GetRepo(ctx, repo)
		if err != nil {
			return nil, err
		}
		ref = r.DefaultBranch
	}
	p, err := repoPath(repo, "/git/trees/"+url.PathEscape(ref)+"?recursive=1")
	if err != nil {
		return nil, err
	}
	var tree struct {
		Tree      []TreeEntry `json:"tree"`
		Truncated bool        `json:"truncated"`
	}
	if err := c.do(ctx, http.MethodGet, p, nil, &tree); err != nil {
		return nil, err
	}
	prefix := strings.Trim(strings.TrimSpace(path), "/")
	out := []TreeEntry{}
	for _, e := range tree.Tree {
		if prefix != "" && !strings.HasPrefix(e.Path, prefix+"/") && e.Path != prefix {
			continue
		}
		if len(out) >= perPage {
			break
		}
		out = append(out, e)
	}
	return out, nil
}

// escapePath escapes a repository-relative path segment by segment — the
// slashes have to survive, everything else must not leave the path.
func escapePath(p string) string {
	parts := strings.Split(strings.Trim(strings.TrimSpace(p), "/"), "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return strings.Join(parts, "/")
}

// maxReadFileBytes caps a single read file.
const maxReadFileBytes = 512 << 10

// ReadFile — GET …/contents/{path}: a single file's content. GitHub delivers it
// base64-encoded; large files come back without content and need the blob API,
// which is deliberately not taken here — that size belongs in a checkout.
func (c *Client) ReadFile(ctx context.Context, repo, filePath, ref string) (content string, truncated bool, err error) {
	if strings.TrimSpace(filePath) == "" {
		return "", false, fmt.Errorf("file_path missing")
	}
	p, err := repoPath(repo, "/contents/"+escapePath(filePath))
	if err != nil {
		return "", false, err
	}
	if ref != "" {
		p += "?ref=" + url.QueryEscape(ref)
	}
	var out struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
		Size     int    `json:"size"`
		Type     string `json:"type"`
	}
	if err := c.do(ctx, http.MethodGet, p, nil, &out); err != nil {
		return "", false, err
	}
	if out.Type == "dir" {
		return "", false, fmt.Errorf("%q is a directory — use list_tree", filePath)
	}
	if out.Encoding != "base64" || out.Content == "" {
		return "", false, fmt.Errorf("file %q is too large for read_file (%d bytes) — fetch it with checkout", filePath, out.Size)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(out.Content, "\n", ""))
	if err != nil {
		return "", false, fmt.Errorf("decode file content: %w", err)
	}
	if len(raw) > maxReadFileBytes {
		return string(raw[:maxReadFileBytes]), true, nil
	}
	return string(raw), false, nil
}

// DownloadTarball — GET …/tarball/{ref}: the repository archive. GitHub
// redirects to codeload.github.com; Go follows and drops the Authorization
// header on the host change, which is right — the redirect target carries its
// own signature. Deliberately not routed through do(): the body is binary and
// can be large; the caller closes the reader.
func (c *Client) DownloadTarball(ctx context.Context, repo, ref string) (io.ReadCloser, error) {
	suffix := "/tarball"
	if r := strings.TrimSpace(ref); r != "" {
		suffix += "/" + url.PathEscape(r)
	}
	path, err := repoPath(repo, suffix)
	if err != nil {
		return nil, err
	}
	// A separate HTTP client without the tight API timeout — downloading a
	// large repo may take longer; the limit is set by the call's context.
	resp, err := c.raw(ctx, http.MethodGet, path, nil, reqlog.Client("github", 0))
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// ListPulls — GET …/pulls. state accepts GitLab's vocabulary too; search and
// base filter afterwards.
func (c *Client) ListPulls(ctx context.Context, repo, state, search, base string) ([]PullRequest, error) {
	wantState, err := normalizePullState(state)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("state", wantState)
	q.Set("per_page", strconv.Itoa(perPage))
	q.Set("sort", "updated")
	q.Set("direction", "desc")
	if base != "" {
		q.Set("base", base)
	}
	p, err := repoPath(repo, "/pulls?"+q.Encode())
	if err != nil {
		return nil, err
	}
	var raw []PullRequest
	if err := c.do(ctx, http.MethodGet, p, nil, &raw); err != nil {
		return nil, err
	}
	out := []PullRequest{}
	for _, pr := range raw {
		if !matchesSearch(search, pr.Title, pr.Body) {
			continue
		}
		pr.Repo = repo
		out = append(out, pr)
	}
	return out, nil
}

// normalizePullState maps the vocabulary of both systems onto GitHub's.
// "merged" is not a state to GitHub but a property of a closed PR — it is
// fetched as "closed" and the caller filters on MergedAt. As with
// normalizeIssueState an unknown value is an error, not a silent widening.
func normalizePullState(state string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "", "open", "opened":
		return "open", nil
	case "closed", "close", "merged":
		return "closed", nil
	case "all":
		return "all", nil
	default:
		return "", fmt.Errorf("state %q unknown — expected \"open\", \"closed\", \"merged\" or \"all\"", state)
	}
}

// GetPull — GET …/pulls/{number}: a single PR with its merge state.
func (c *Client) GetPull(ctx context.Context, repo string, number int) (PullRequest, error) {
	path, err := repoPath(repo, fmt.Sprintf("/pulls/%d", number))
	if err != nil {
		return PullRequest{}, err
	}
	var pr PullRequest
	if err := c.do(ctx, http.MethodGet, path, nil, &pr); err != nil {
		return PullRequest{}, err
	}
	pr.Repo = repo
	return pr, nil
}

// ListMyOpenPulls finds the PRs the bot opened itself, across repositories —
// GitHub offers no such list endpoint, so it goes through the search API. The
// PRs come back thin (search returns issue objects); the caller fetches what it
// needs in detail.
func (c *Client) ListMyOpenPulls(ctx context.Context) ([]Issue, error) {
	return c.searchIssues(ctx, "is:pr is:open author:@me")
}

// ListReviewPulls finds the PRs in which the bot is entered as reviewer — the
// QA/test agent's working set.
func (c *Client) ListReviewPulls(ctx context.Context) ([]Issue, error) {
	return c.searchIssues(ctx, "is:pr is:open review-requested:@me")
}

// searchIssues runs GitHub's issue search. The hits carry the repository only
// in the html_url, so it is derived from there — the search API returns no
// repository object.
func (c *Client) searchIssues(ctx context.Context, query string) ([]Issue, error) {
	var out struct {
		Items []Issue `json:"items"`
	}
	path := "/search/issues?per_page=" + strconv.Itoa(perPage) +
		"&sort=updated&advanced_search=true&q=" + url.QueryEscape(query)
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	items := []Issue{}
	for _, i := range out.Items {
		i.Repo = repoFromHTMLURL(i.HTMLURL)
		items = append(items, i)
	}
	return items, nil
}

// repoFromHTMLURL pulls "owner/repo" out of an item's web URL
// (https://github.com/acme/support/pull/9). Empty if the shape does not fit —
// the caller then skips the entry rather than guessing.
func repoFromHTMLURL(htmlURL string) string {
	u, err := url.Parse(htmlURL)
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 {
		return ""
	}
	return parts[0] + "/" + parts[1]
}

// ListPullComments merges a PR's whole conversation into one chronological
// list: the ordinary comments, the submitted reviews (with their verdict) and
// the review comments on lines of the diff. GitHub keeps the three apart; to
// the agent they are one thread, and a review that only appears in one of the
// three lists is feedback it would otherwise miss.
func (c *Client) ListPullComments(ctx context.Context, repo string, number int) ([]Comment, error) {
	all, err := c.ListComments(ctx, repo, number)
	if err != nil {
		return nil, err
	}

	reviewsPath, err := repoPath(repo, fmt.Sprintf("/pulls/%d/reviews?per_page=%d", number, perPage))
	if err != nil {
		return nil, err
	}
	var reviews []Comment
	if err := c.do(ctx, http.MethodGet, reviewsPath, nil, &reviews); err != nil {
		return nil, err
	}
	for _, r := range reviews {
		// A review without a body and without a verdict says nothing (GitHub
		// records a PENDING shell) — it does not belong in the thread.
		if strings.TrimSpace(r.Body) == "" && (r.State == "" || r.State == "PENDING") {
			continue
		}
		r.Kind = "review"
		r.CreatedAt = r.SubmittedAt
		r.SubmittedAt = ""
		all = append(all, r)
	}

	linePath, err := repoPath(repo, fmt.Sprintf("/pulls/%d/comments?per_page=%d", number, perPage))
	if err != nil {
		return nil, err
	}
	var lineComments []Comment
	if err := c.do(ctx, http.MethodGet, linePath, nil, &lineComments); err != nil {
		return nil, err
	}
	for _, lc := range lineComments {
		lc.Kind = "review_comment"
		all = append(all, lc)
	}

	sortComments(all)
	return all, nil
}

// sortComments orders by timestamp; ISO 8601 sorts lexicographically. The id
// decides on a tie so that the order stays stable between two calls.
func sortComments(cs []Comment) {
	sort.SliceStable(cs, func(i, j int) bool {
		if cs[i].CreatedAt != cs[j].CreatedAt {
			return cs[i].CreatedAt < cs[j].CreatedAt
		}
		return cs[i].ID < cs[j].ID
	})
}

// CreatePull — POST …/pulls. head is the source branch, base the target.
func (c *Client) CreatePull(ctx context.Context, repo, head, base, title, body string, draft bool) (PullRequest, error) {
	path, err := repoPath(repo, "/pulls")
	if err != nil {
		return PullRequest{}, err
	}
	in := map[string]any{"title": title, "head": head, "base": base, "draft": draft}
	if body != "" {
		in["body"] = body
	}
	var pr PullRequest
	if err := c.do(ctx, http.MethodPost, path, in, &pr); err != nil {
		return PullRequest{}, err
	}
	pr.Repo = repo
	return pr, nil
}

// RequestReviewers — POST …/pulls/{number}/requested_reviewers.
func (c *Client) RequestReviewers(ctx context.Context, repo string, number int, logins []string) (PullRequest, error) {
	path, err := repoPath(repo, fmt.Sprintf("/pulls/%d/requested_reviewers", number))
	if err != nil {
		return PullRequest{}, err
	}
	var pr PullRequest
	err = c.do(ctx, http.MethodPost, path, map[string]any{"reviewers": logins}, &pr)
	pr.Repo = repo
	return pr, err
}

// ApprovePull — POST …/pulls/{number}/reviews with event=APPROVE: the formal
// green signal of a reviewer. The merging itself stays with a human.
func (c *Client) ApprovePull(ctx context.Context, repo string, number int, body string) (Comment, error) {
	path, err := repoPath(repo, fmt.Sprintf("/pulls/%d/reviews", number))
	if err != nil {
		return Comment{}, err
	}
	in := map[string]any{"event": "APPROVE"}
	if strings.TrimSpace(body) != "" {
		in["body"] = body
	}
	var out Comment
	err = c.do(ctx, http.MethodPost, path, in, &out)
	out.Kind = "review"
	return out, err
}

// RequestChanges — POST …/pulls/{number}/reviews with event=REQUEST_CHANGES:
// the reviewer's counterpart to the approval. GitHub blocks the merge with it
// where the branch protection demands a review, so a defect found does not only
// stand in a comment but actually holds the PR up.
func (c *Client) RequestChanges(ctx context.Context, repo string, number int, body string) (Comment, error) {
	if strings.TrimSpace(body) == "" {
		return Comment{}, fmt.Errorf("body missing — request_changes needs the reason (what is defective, where)")
	}
	path, err := repoPath(repo, fmt.Sprintf("/pulls/%d/reviews", number))
	if err != nil {
		return Comment{}, err
	}
	var out Comment
	err = c.do(ctx, http.MethodPost, path, map[string]any{"event": "REQUEST_CHANGES", "body": body}, &out)
	out.Kind = "review"
	return out, err
}

// WorkflowRun is a GitHub Actions run — the counterpart to the GitLab pipeline.
type WorkflowRun struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Status     string `json:"status"`     // queued | in_progress | completed
	Conclusion string `json:"conclusion"` // success | failure | cancelled | …
	HeadBranch string `json:"head_branch"`
	HeadSHA    string `json:"head_sha"`
	Event      string `json:"event"`
	HTMLURL    string `json:"html_url"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

// ListWorkflowRuns — GET …/actions/runs, optionally narrowed to one branch.
func (c *Client) ListWorkflowRuns(ctx context.Context, repo, branch string) ([]WorkflowRun, error) {
	q := url.Values{}
	q.Set("per_page", "20")
	if branch != "" {
		q.Set("branch", branch)
	}
	p, err := repoPath(repo, "/actions/runs?"+q.Encode())
	if err != nil {
		return nil, err
	}
	var out struct {
		Runs []WorkflowRun `json:"workflow_runs"`
	}
	err = c.do(ctx, http.MethodGet, p, nil, &out)
	if out.Runs == nil {
		out.Runs = []WorkflowRun{}
	}
	return out.Runs, err
}

// Job is one job of a workflow run.
type Job struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	HTMLURL    string `json:"html_url"`
	StartedAt  string `json:"started_at"`
}

// ListRunJobs — GET …/actions/runs/{id}/jobs.
func (c *Client) ListRunJobs(ctx context.Context, repo string, runID int64) ([]Job, error) {
	p, err := repoPath(repo, fmt.Sprintf("/actions/runs/%d/jobs?per_page=%d", runID, perPage))
	if err != nil {
		return nil, err
	}
	var out struct {
		Jobs []Job `json:"jobs"`
	}
	err = c.do(ctx, http.MethodGet, p, nil, &out)
	if out.Jobs == nil {
		out.Jobs = []Job{}
	}
	return out.Jobs, err
}

// maxJobLogBytes caps the log an agent gets to see. The END is what is wanted:
// a failure stands at the bottom, the setup noise at the top.
const maxJobLogBytes = 48 << 10

// GetJobLog — GET …/actions/jobs/{id}/logs: GitHub redirects to a signed blob
// URL, Go follows it. Returns the log's end plus a flag saying it was cut.
func (c *Client) GetJobLog(ctx context.Context, repo string, jobID int64) (string, bool, error) {
	p, err := repoPath(repo, fmt.Sprintf("/actions/jobs/%d/logs", jobID))
	if err != nil {
		return "", false, err
	}
	resp, err := c.raw(ctx, http.MethodGet, p, nil, reqlog.Client("github", 60*time.Second))
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()
	// Read at most one byte past the cap: only then can we distinguish a log
	// that just fits from one that was cut.
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxJobLogBytes*8))
	if err != nil {
		return "", false, err
	}
	if len(data) > maxJobLogBytes {
		return string(data[len(data)-maxJobLogBytes:]), true, nil
	}
	return string(data), false, nil
}

// RerunFailedJobs — POST …/actions/runs/{id}/rerun-failed-jobs: starts the
// failed jobs of a run afresh. For a red pipeline whose cause lay outside the
// change (a runner missing, a registry down) and has been fixed since.
func (c *Client) RerunFailedJobs(ctx context.Context, repo string, runID int64) error {
	p, err := repoPath(repo, fmt.Sprintf("/actions/runs/%d/rerun-failed-jobs", runID))
	if err != nil {
		return err
	}
	return c.do(ctx, http.MethodPost, p, map[string]any{}, nil)
}

// CheckRun is a check on a commit — GitHub Actions writes them, and so does
// every external CI hanging off the Checks API. get_pull reports them, because
// the mergeability alone does not say whether the tests are green.
type CheckRun struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	HTMLURL    string `json:"html_url"`
}

// ListCheckRuns — GET …/commits/{ref}/check-runs.
func (c *Client) ListCheckRuns(ctx context.Context, repo, ref string) ([]CheckRun, error) {
	p, err := repoPath(repo, "/commits/"+url.PathEscape(ref)+"/check-runs?per_page="+strconv.Itoa(perPage))
	if err != nil {
		return nil, err
	}
	var out struct {
		Runs []CheckRun `json:"check_runs"`
	}
	err = c.do(ctx, http.MethodGet, p, nil, &out)
	if out.Runs == nil {
		out.Runs = []CheckRun{}
	}
	return out.Runs, err
}

// LookupUser — GET /users/{login}: checks that a login exists before it is
// entered as an assignee or reviewer. GitHub silently swallows an unknown
// assignee (the request succeeds, the assignment does not happen) — that is the
// kind of failure that only shows up days later.
func (c *Client) LookupUser(ctx context.Context, login string) (User, error) {
	login = strings.TrimPrefix(strings.TrimSpace(login), "@")
	if login == "" {
		return User{}, fmt.Errorf("username missing")
	}
	var u User
	if err := c.do(ctx, http.MethodGet, "/users/"+url.PathEscape(login), nil, &u); err != nil {
		return User{}, fmt.Errorf("github user %q not found: %w", login, err)
	}
	return u, nil
}
