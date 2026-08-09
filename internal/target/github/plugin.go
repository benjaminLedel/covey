package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"covey/internal/target"
)

// System binds GitHub in as a target-system plugin to the target registry: the
// webhook entry (HMAC-verified, idempotent), the polling checks for the
// heartbeat, the agent actions and the action documentation for the system
// prompt.
type System struct{}

func init() {
	target.Register(target.Descriptor{
		Name:        "github",
		Label:       "GitHub",
		Description: "GitHub issues and pull requests as the working set: find issues (list_repos/list_issues, by milestone too), file externally reported bugs as a ticket (create_issue), maintain the working state on the board (set_labels/assign), check out source code and verify bugs against the code (checkout + sandbox shell), look at screenshots attached to issues (download_attachment + vision), develop fixes — commit onto a feature branch (commit), open a pull request (create_pull_request, optionally with a QA agent as reviewer) and live the review loop: check open PRs for new review feedback (list_pull_requests/list_pr_comments/comment_pr), diagnose red Actions runs yourself (list_workflow_runs/list_run_jobs/get_job_log) and react to the merge. Usable as a QA/test agent too: test others' pull requests in which you are entered as reviewer end to end and give feedback (approve_pr/request_changes, nur-wenn: github:review). Intake either through HEARTBEAT.md (polling) or through the webhook; auth by API token (the secret github_token; github_url only for GitHub Enterprise Server).",
		Kind:        "builtin",
		Category:    target.CategoryCode,
		Scopes:      []string{"read", "write", "comment", "merge"},
		System:      System{},
		// github.com is the default endpoint — a github_url is only needed for
		// GitHub Enterprise Server.
		BaseURLOptional: true,
		SetupDoc: `1. Create an account of its own for the agent (covey-bot, say), add it to the
   target repositories and generate a token as that user:
   - Fine-grained token (recommended): grant it access to the repositories in
     question and the permissions Contents (read & write), Issues (read &
     write), Pull requests (read & write), Actions (read) and Metadata (read).
   - Classic token: scope "repo" (plus "read:org" if you want to see
     organisation repositories).
   Read-only work needs no write permissions; commit / create_pull_request do.

2. Store under Secrets and assign to the agent:
   github_token = the token from step 1
   github_url   = only for GitHub Enterprise Server, e.g. https://ghe.example.com
                  (github.com needs no entry)

3. Enable in the agent's ACCESS.md:
   - system: github scope: read,write,comment

4. Intake — GitHub offers BOTH routes; one of them is enough:

   a) By heartbeat (polling, works without a public URL) — separate entries in
      the agent's HEARTBEAT.md, each gated on its own:
      - alle: 15m nur-wenn: github:issues titel: Review GitHub issues aufgabe:
        Find open issues (list_issues state=open), work on the new ones and check
        with list_comments whether your queries have been answered. For bugs: fetch
        the code with checkout and verify the claim against the source.
      - alle: 15m nur-wenn: github:pr titel: Look after pull requests aufgabe:
        Check your open pull requests (list_pull_requests state=open) for new
        review feedback (list_pr_comments), work it in and react to the merge.
      (The sub-scope after the colon saves the expensive agent run deliberately:
       nur-wenn: github:issues fires on ANY open issue in the intake scope,
       nur-wenn: github:pr only when one of your own open PRs has unanswered
       review feedback, nur-wenn: github:review only when a PR is waiting for
       YOUR review. IMPORTANT — if your playbook works only on issues ASSIGNED
       TO YOU (list_issues assigned=true), use nur-wenn: github:issues:assigned;
       otherwise every open issue of someone else's would start your agent.)

   b) By webhook (real time) — in the repository under
      Settings › Webhooks › Add webhook:
      - Payload URL: {public_url}/api/webhooks/github/<agent-slug>
      - Content type: application/json
      - Secret: a random value; store the SAME value as the agent's webhook
        secret in Covey.
      - Events: "Issues", "Issue comments", "Pull requests",
        "Pull request reviews", "Pull request review comments".
      A newly opened issue becomes a task; comments, reviews and the close of a
      pull request only wake a task that is waiting for exactly that thread.

   Optional repository filter (applies to list_issues/list_repos, the webhook
   intake and the nur-wenn: checks):
   COVEY_GITHUB_INTAKE_REPOS="acme/support, acme/*"   (empty = all)

   If the bot uses a personal access token rather than a GitHub App, also name
   its login so that its own comments do not wake it again:
   COVEY_GITHUB_BOT_LOGINS="covey-bot"

Details: docs/ops-github.md in the repository.`,
	})
}

func (System) Name() string { return "github" }

// VerifyWebhook (target.Webhooker) checks GitHub's HMAC-SHA256 signature.
func (System) VerifyWebhook(secret string, body []byte, header http.Header) bool {
	return VerifySignature(secret, body, header.Get("X-Hub-Signature-256"))
}

// ParseWebhook (target.Webhooker) turns the payload into the wake event.
func (System) ParseWebhook(body []byte) (target.WebhookEvent, error) {
	p, err := ParseWebhook(body)
	if err != nil {
		return target.WebhookEvent{}, err
	}
	return p.Event(), nil
}

// HasWork (target.WorkChecker): the control plane's cheap pre-check for
// nur-wenn: heartbeats — it saves the (expensive) agent wake when there is
// nothing to do at the moment. Work is present when ONE of the following holds:
//
//   - There is an open issue in the intake scope on which the bot has not yet
//     commented last.
//   - The bot has an open pull request it opened itself with unanswered review
//     feedback. That carries the review loop.
//
// What counts everywhere is the EDGE (has something happened since the bot's
// last move?), not the level (is anything open anywhere?) — otherwise the same
// unfinished item wakes the agent afresh in every interval.
func (System) HasWork(ctx context.Context, cred target.Credential) (bool, error) {
	has, _, err := System{}.HasWorkSigned(ctx, cred, "")
	return has, err
}

// HasWorkKind (target.KindWorkChecker) gates a single kind of work so that
// several heartbeats fire separately:
//
//   - "issues"/"issue"             → is ANY open issue in the intake scope
//     waiting for a reaction?
//   - "issues:assigned"/"assigned" → is an open issue waiting that is ASSIGNED
//     to the bot itself? Exactly that is what an agent needs whose playbook
//     works only on its own issues.
//   - "pr"/"prs"/"mr"              → is one of the PRs the bot opened ITSELF
//     waiting for an answer (the author's view, the developer review loop)?
//   - "review"/"reviews"           → is one of the PRs in which the bot is
//     entered as REVIEWER waiting for its review (the QA/test view)?
//   - otherwise                    → both of HasWork, fail-open on an unknown
//     scope.
func (System) HasWorkKind(ctx context.Context, cred target.Credential, kind string) (bool, error) {
	has, _, err := System{}.HasWorkSigned(ctx, cred, kind)
	return has, err
}

// HasWorkSigned (target.SignedWorkChecker) is the actual check: besides the
// yes/no it returns the signature of the waiting items so that the control
// plane does not wake twice on the same state. An agent may thereby end a run
// silently — the QA colleague's feedback was an approval, there is nothing to
// do — without being woken again in the next interval. If a new contribution or
// a push comes along, the signature changes and the agent wakes. Whether a
// piece of feedback means work (reported defects) or only information (an
// approval) is thus decided by the agent and not by the gate.
func (System) HasWorkSigned(ctx context.Context, cred target.Credential, kind string) (bool, string, error) {
	gc := NewClient(cred.BaseURL, cred.Token)
	var (
		waiting []string
		err     error
	)
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "issues", "issue":
		waiting, err = issueWorkPending(ctx, gc, false)
	case "issues:assigned", "issue:assigned", "assigned":
		waiting, err = issueWorkPending(ctx, gc, true)
	// "mr" is accepted as an alias so that a playbook carried over from GitLab
	// does not silently gate on nothing.
	case "pr", "prs", "pull", "pulls", "mr", "mrs":
		waiting, err = pullReviewPending(ctx, gc, false)
	case "review", "reviews":
		waiting, err = pullReviewPending(ctx, gc, true)
	default:
		// Without a sub-scope both count — issues AND one's own review loop.
		waiting, err = issueWorkPending(ctx, gc, false)
		if err == nil {
			var prs []string
			if prs, err = pullReviewPending(ctx, gc, false); err == nil {
				waiting = append(waiting, prs...)
			}
		}
	}
	if err != nil {
		return false, "", err
	}
	return len(waiting) > 0, workSig(waiting), nil
}

// issueMaxCommentChecks caps the comment check of issueWorkPending: the check
// runs in every heartbeat interval and must not run away with the number of
// open issues. Whoever has more open issues than that gets woken — the call
// "which of them is new" is then made by the agent itself.
const issueMaxCommentChecks = 30

// issueWorkPending: is at least one open issue in the intake scope waiting for
// the agent? The global GET /issues, followed by the COVEY_GITHUB_INTAKE_REPOS
// filter — what the agent would not see through list_issues does not wake it
// either. assignedOnly=true counts only the issues assigned to the bot.
//
// What is decisive is the edge, not the level: an open issue is work as long as
// the last comment does NOT come from the bot (or there is none at all yet —
// then the first triage is outstanding). If the bot wrote last, the issue rests
// until someone answers.
//
// The contract that follows: an agent that has worked on an issue must comment
// there. A silent run counts as "not yet worked on" and wakes again.
func issueWorkPending(ctx context.Context, gc *Client, assignedOnly bool) ([]string, error) {
	issues, err := gc.ListIssues(ctx, "", "open", "", "", "", assignedOnly)
	if err != nil {
		return nil, err
	}
	var inScope []Issue
	for _, i := range issues {
		if repoInScope(i.Repo) {
			inScope = append(inScope, i)
		}
	}
	if len(inScope) == 0 {
		return nil, nil
	}
	if len(inScope) > issueMaxCommentChecks {
		// Too many open issues for the comment check: wake without looking at
		// them one by one. The signature then carries only the count — it
		// changes as soon as an issue comes along or drops out.
		return []string{fmt.Sprintf("issues:many@%d", len(inScope))}, nil
	}
	me, err := gc.CurrentUser(ctx)
	if err != nil {
		return nil, err
	}
	var waiting []string
	for _, i := range inScope {
		// An issue nobody has commented on yet is untouched by definition — the
		// comment count spares us the request.
		if i.Comments == 0 {
			waiting = append(waiting, fmt.Sprintf("issue:%s#%d@0", i.Repo, i.Number))
			continue
		}
		comments, err := gc.ListComments(ctx, i.Repo, i.Number)
		if err != nil {
			return nil, err
		}
		if lastCommentIsMine(comments, me.Login) {
			continue // already answered — rests until someone replies to it
		}
		waiting = append(waiting, threadSig("issue", i.Repo, i.Number, comments, ""))
	}
	return waiting, nil
}

// pullMaxChecks caps the same way as issueMaxCommentChecks, for pull requests.
const pullMaxChecks = 30

// pullReviewPending: which pull requests are waiting for the agent?
//
//	asReviewer=false → the PRs the bot opened ITSELF whose last contribution
//	                   comes from someone else (the developer's review loop).
//	asReviewer=true  → the PRs in which the bot is entered as REVIEWER and has
//	                   not yet reviewed the current state (the QA view).
//
// The head SHA goes into the signature: unlike GitLab, GitHub does not record a
// push as a comment in the thread. Without the SHA a developer's answer to a
// review would settle the PR for good — the reviewer would never learn of the
// commit that followed it.
func pullReviewPending(ctx context.Context, gc *Client, asReviewer bool) ([]string, error) {
	var (
		items []Issue
		err   error
	)
	if asReviewer {
		items, err = gc.ListReviewPulls(ctx)
	} else {
		items, err = gc.ListMyOpenPulls(ctx)
	}
	if err != nil {
		return nil, err
	}
	me, err := gc.CurrentUser(ctx)
	if err != nil {
		return nil, err
	}
	var waiting []string
	checked := 0
	for _, it := range items {
		if it.Repo == "" || !repoInScope(it.Repo) {
			continue
		}
		if checked++; checked > pullMaxChecks {
			return []string{fmt.Sprintf("pulls:many@%d", checked)}, nil
		}
		pr, err := gc.GetPull(ctx, it.Repo, it.Number)
		if err != nil {
			return nil, err
		}
		comments, err := gc.ListPullComments(ctx, it.Repo, it.Number)
		if err != nil {
			return nil, err
		}
		if asReviewer {
			// As reviewer: the PR waits until the bot has reviewed the CURRENT
			// state. That the requested review still stands is GitHub's own
			// statement — it clears the request once a review is submitted, and
			// a new push asks for it afresh.
			if !isRequestedReviewer(pr, me.Login) && lastCommentIsMine(comments, me.Login) {
				continue
			}
		} else if lastCommentIsMine(comments, me.Login) {
			continue // the bot answered last — it is somebody else's turn
		}
		waiting = append(waiting, threadSig("pull", it.Repo, it.Number, comments, pr.Head.SHA))
	}
	return waiting, nil
}

func isRequestedReviewer(pr PullRequest, login string) bool {
	for _, r := range pr.RequestedReviewers {
		if strings.EqualFold(r.Login, login) {
			return true
		}
	}
	return false
}

// lastCommentIsMine: does the chronologically last contribution come from the
// bot? Empty thread → false (the first triage is outstanding). An unknown own
// login → false as well, fail-open: better one wake too many than a thread that
// nobody ever picks up.
func lastCommentIsMine(comments []Comment, me string) bool {
	if me == "" || len(comments) == 0 {
		return false
	}
	last := comments[0]
	for _, c := range comments[1:] {
		if c.CreatedAt > last.CreatedAt || (c.CreatedAt == last.CreatedAt && c.ID > last.ID) {
			last = c
		}
	}
	return strings.EqualFold(last.User.Login, me)
}

// threadSig describes a waiting item in such a way that the description changes
// exactly when something new has happened there: the repository, the number,
// the highest comment id of the thread and — for pull requests — the head SHA,
// so that a push counts as news too.
func threadSig(kind, repo string, number int, comments []Comment, headSHA string) string {
	var last int64
	for _, c := range comments {
		if c.ID > last {
			last = c.ID
		}
	}
	sig := fmt.Sprintf("%s:%s#%d@%d", kind, repo, number, last)
	if headSHA != "" {
		sig += "+" + headSHA[:min(len(headSHA), 12)]
	}
	return sig
}

// workSig condenses the waiting items into a stable signature. Sorted, because
// GitHub returns them by updated_at — otherwise the signature would change
// through the order alone.
func workSig(waiting []string) string {
	if len(waiting) == 0 {
		return ""
	}
	sorted := append([]string(nil), waiting...)
	sort.Strings(sorted)
	return strings.Join(sorted, ",")
}

// ActionSubject maps action+params onto the guard-rail subject. Unlike Zammad
// and GitLab there is no internal/external split: GitHub knows no internal
// comments, every contribution is visible to whoever can see the repository.
// The writing actions therefore each carry their own subject, so a rule can
// govern "may comment" and "may push" separately.
func (System) ActionSubject(action string, params json.RawMessage) string {
	return "github:" + action
}

// isDuplicateComment is the server-side brake against comment loops: if the new
// comment body is identical to the bot's most recent OWN comment, it is not
// posted again. Fail-open: if the who-am-I check goes wrong, the comment is
// posted as usual (no legitimate comment should be blocked). Only the
// repetition of one's own last comment is suppressed.
func isDuplicateComment(ctx context.Context, gc *Client, comments []Comment, body string) bool {
	me, err := gc.CurrentUser(ctx)
	if err != nil || me.Login == "" {
		return false
	}
	var lastOwn, lastAt string
	for _, c := range comments {
		if !strings.EqualFold(c.User.Login, me.Login) {
			continue
		}
		if c.CreatedAt >= lastAt { // ISO 8601 sorts lexicographically
			lastAt, lastOwn = c.CreatedAt, c.Body
		}
	}
	return lastOwn != "" && strings.TrimSpace(lastOwn) == strings.TrimSpace(body)
}

// actionParams is the union of all parameters any GitHub action needs. One
// shared struct instead of one per action: the agent sends a flat JSON object,
// and whatever is missing from it simply stays empty — that is the interface to
// the model, not the shape we would wish for.
type actionParams struct {
	Repo        string `json:"repo"`
	IssueNumber int    `json:"issue_number"`
	PRNumber    int    `json:"pr_number"`
	RunID       int64  `json:"run_id"`
	JobID       int64  `json:"job_id"`
	Body        string `json:"body"`
	State       string `json:"state"`
	Note        string `json:"note"`
	Labels      string `json:"labels"`
	Search      string `json:"search"`
	Milestone   string `json:"milestone"`
	Ref         string `json:"ref"`
	Assigned    bool   `json:"assigned"`
	// set_labels works additively/subtractively instead of overwriting the
	// whole list — otherwise every state change takes the subject-matter labels
	// along with it.
	AddLabels    []string `json:"add_labels"`
	RemoveLabels []string `json:"remove_labels"`
	Path         string   `json:"path"`
	FilePath     string   `json:"file_path"`
	URL          string   `json:"url"`
	Recursive    bool     `json:"recursive"`
	SHA          string   `json:"sha"`
	Since        string   `json:"since"`
	Base         string   `json:"base"`
	Username     string   `json:"username"`
	// The developer workflow: commit + create_pull_request.
	Branch       string   `json:"branch"`
	StartBranch  string   `json:"start_branch"`
	Message      string   `json:"message"`
	CheckoutPath string   `json:"checkout_path"`
	Files        []string `json:"files"`
	Deleted      []string `json:"deleted"`
	Head         string   `json:"head"`
	Title        string   `json:"title"`
	Description  string   `json:"description"`
	Draft        bool     `json:"draft"`
	Assignee     string   `json:"assignee"`
	Reviewer     string   `json:"reviewer"`
}

// needRepo is the check every repository-bound action starts with. The message
// says what is expected — an agent that guesses a repo name burns a turn on a
// 404.
func (in actionParams) needRepo() error {
	if strings.TrimSpace(in.Repo) == "" {
		return fmt.Errorf("repo missing — expected \"owner/name\" (list_repos shows the repositories available to you)")
	}
	_, _, err := splitRepo(in.Repo)
	return err
}

// action carries out ONE GitHub action. Each is readable on its own and the
// dispatch is a table — taking on one more action means adding an entry, not
// touching a switch.
type action func(ctx context.Context, gc *Client, in actionParams) (any, error)

// repoAction wraps an action that needs a repository with the check for it.
func repoAction(fn action) action {
	return func(ctx context.Context, gc *Client, in actionParams) (any, error) {
		if err := in.needRepo(); err != nil {
			return nil, err
		}
		return fn(ctx, gc, in)
	}
}

// actions is the dispatch: a name from the daemon protocol onto its execution.
var actions = map[string]action{
	"list_repos": func(ctx context.Context, gc *Client, in actionParams) (any, error) {
		repos, err := gc.ListRepos(ctx)
		if err != nil {
			return nil, err
		}
		out := []Repo{}
		for _, r := range repos {
			if repoInScope(r.FullName) {
				out = append(out, r)
			}
		}
		return out, nil
	},
	"list_issues": func(ctx context.Context, gc *Client, in actionParams) (any, error) {
		// repo is optional here: without one the global endpoint returns the
		// issues of every repository the token can see.
		if strings.TrimSpace(in.Repo) != "" {
			if err := in.needRepo(); err != nil {
				return nil, err
			}
		}
		issues, err := gc.ListIssues(ctx, in.Repo, in.State, in.Labels, in.Search, in.Milestone, in.Assigned)
		if err != nil {
			return nil, err
		}
		out := []Issue{}
		for _, i := range issues {
			if repoInScope(i.Repo) {
				out = append(out, i)
			}
		}
		return out, nil
	},
	"get_issue": repoAction(func(ctx context.Context, gc *Client, in actionParams) (any, error) {
		if in.IssueNumber == 0 {
			return nil, fmt.Errorf("issue_number missing")
		}
		return gc.GetIssue(ctx, in.Repo, in.IssueNumber)
	}),
	"create_issue": repoAction(func(ctx context.Context, gc *Client, in actionParams) (any, error) {
		if strings.TrimSpace(in.Title) == "" {
			return nil, fmt.Errorf("title missing")
		}
		var assignees []string
		if a := strings.TrimSpace(in.Assignee); a != "" {
			u, err := gc.LookupUser(ctx, a)
			if err != nil {
				return nil, err
			}
			assignees = []string{u.Login}
		}
		return gc.CreateIssue(ctx, in.Repo, in.Title, in.Description, splitLabels(in.Labels), assignees)
	}),
	"list_comments": repoAction(func(ctx context.Context, gc *Client, in actionParams) (any, error) {
		if in.IssueNumber == 0 {
			return nil, fmt.Errorf("issue_number missing")
		}
		return gc.ListComments(ctx, in.Repo, in.IssueNumber)
	}),
	"comment": repoAction(func(ctx context.Context, gc *Client, in actionParams) (any, error) {
		if in.IssueNumber == 0 || strings.TrimSpace(in.Body) == "" {
			return nil, fmt.Errorf("issue_number or body missing")
		}
		if cs, err := gc.ListComments(ctx, in.Repo, in.IssueNumber); err == nil && isDuplicateComment(ctx, gc, cs, in.Body) {
			return map[string]any{"skipped": "duplicate",
				"reason": "identical to your own last comment — not posted again"}, nil
		}
		return gc.Comment(ctx, in.Repo, in.IssueNumber, in.Body)
	}),
	"set_state": repoAction(func(ctx context.Context, gc *Client, in actionParams) (any, error) {
		if in.IssueNumber == 0 {
			return nil, fmt.Errorf("issue_number missing")
		}
		return gc.SetState(ctx, in.Repo, in.IssueNumber, in.State)
	}),
	"assign": repoAction(func(ctx context.Context, gc *Client, in actionParams) (any, error) {
		if in.IssueNumber == 0 {
			return nil, fmt.Errorf("issue_number missing")
		}
		u, err := gc.LookupUser(ctx, in.Username)
		if err != nil {
			return nil, err
		}
		if _, err := gc.AddAssignees(ctx, in.Repo, in.IssueNumber, []string{u.Login}); err != nil {
			return nil, err
		}
		return map[string]any{"assigned_to": u.Login, "issue_number": in.IssueNumber}, nil
	}),
	"set_labels": repoAction(func(ctx context.Context, gc *Client, in actionParams) (any, error) {
		if in.IssueNumber == 0 {
			return nil, fmt.Errorf("issue_number missing")
		}
		labels, err := gc.SetLabels(ctx, in.Repo, in.IssueNumber, in.AddLabels, in.RemoveLabels)
		if err != nil {
			return nil, err
		}
		return map[string]any{"issue_number": in.IssueNumber, "labels": labels}, nil
	}),
	"escalate": repoAction(func(ctx context.Context, gc *Client, in actionParams) (any, error) {
		if in.IssueNumber == 0 {
			return nil, fmt.Errorf("issue_number missing")
		}
		note := in.Note
		if note == "" {
			note = "Escalated by a Covey agent."
		}
		return map[string]any{"escalated": true}, gc.Escalate(ctx, in.Repo, in.IssueNumber, note)
	}),
	"download_attachment": func(ctx context.Context, gc *Client, in actionParams) (any, error) {
		if strings.TrimSpace(in.URL) == "" {
			return nil, fmt.Errorf("url missing")
		}
		return DownloadAttachmentToSandbox(ctx, gc, in.URL, target.Workdir(ctx))
	},
	"checkout": repoAction(func(ctx context.Context, gc *Client, in actionParams) (any, error) {
		return Checkout(ctx, gc, in.Repo, in.Ref, target.Workdir(ctx))
	}),
	"list_tree": repoAction(func(ctx context.Context, gc *Client, in actionParams) (any, error) {
		return gc.ListTree(ctx, in.Repo, in.Path, in.Ref, in.Recursive)
	}),
	"read_file": repoAction(func(ctx context.Context, gc *Client, in actionParams) (any, error) {
		if in.FilePath == "" {
			return nil, fmt.Errorf("file_path missing")
		}
		content, truncated, err := gc.ReadFile(ctx, in.Repo, in.FilePath, in.Ref)
		if err != nil {
			return nil, err
		}
		return map[string]any{"file_path": in.FilePath, "ref": in.Ref,
			"content": content, "truncated": truncated}, nil
	}),
	"list_branches": repoAction(func(ctx context.Context, gc *Client, in actionParams) (any, error) {
		return gc.ListBranches(ctx, in.Repo, in.Search)
	}),
	"list_commits": repoAction(func(ctx context.Context, gc *Client, in actionParams) (any, error) {
		return gc.ListCommits(ctx, in.Repo, in.Ref, in.Path, in.Since)
	}),
	"get_commit": repoAction(func(ctx context.Context, gc *Client, in actionParams) (any, error) {
		return gc.GetCommitDiff(ctx, in.Repo, in.SHA)
	}),
	"commit": repoAction(func(ctx context.Context, gc *Client, in actionParams) (any, error) {
		return CommitFromCheckout(ctx, gc, in.Repo, in.Branch, in.StartBranch,
			in.Message, in.CheckoutPath, in.Files, in.Deleted, target.Workdir(ctx))
	}),
	"list_pull_requests": repoAction(func(ctx context.Context, gc *Client, in actionParams) (any, error) {
		prs, err := gc.ListPulls(ctx, in.Repo, in.State, in.Search, in.Base)
		if err != nil {
			return nil, err
		}
		// "merged" is not a state to GitHub but a property of a closed PR —
		// without this filter the agent would get the abandoned ones back too.
		if strings.EqualFold(strings.TrimSpace(in.State), "merged") {
			out := []PullRequest{}
			for _, pr := range prs {
				if pr.MergedAt != "" {
					pr.Merged = true
					out = append(out, pr)
				}
			}
			return out, nil
		}
		return prs, nil
	}),
	"get_pull_request": repoAction(func(ctx context.Context, gc *Client, in actionParams) (any, error) {
		if in.PRNumber == 0 {
			return nil, fmt.Errorf("pr_number missing")
		}
		pr, err := gc.GetPull(ctx, in.Repo, in.PRNumber)
		if err != nil {
			return nil, err
		}
		// The CI state belongs with it: mergeable says nothing about whether
		// the tests are green, and that is what the agent is deciding on.
		checks, err := gc.ListCheckRuns(ctx, in.Repo, pr.Head.SHA)
		if err != nil {
			return map[string]any{"pull_request": pr, "checks_error": err.Error()}, nil
		}
		return map[string]any{"pull_request": pr, "checks": checks}, nil
	}),
	"list_pr_comments": repoAction(func(ctx context.Context, gc *Client, in actionParams) (any, error) {
		if in.PRNumber == 0 {
			return nil, fmt.Errorf("pr_number missing")
		}
		return gc.ListPullComments(ctx, in.Repo, in.PRNumber)
	}),
	"comment_pr": repoAction(func(ctx context.Context, gc *Client, in actionParams) (any, error) {
		if in.PRNumber == 0 || strings.TrimSpace(in.Body) == "" {
			return nil, fmt.Errorf("pr_number or body missing")
		}
		if cs, err := gc.ListPullComments(ctx, in.Repo, in.PRNumber); err == nil && isDuplicateComment(ctx, gc, cs, in.Body) {
			return map[string]any{"skipped": "duplicate",
				"reason": "identical to your own last comment — not posted again"}, nil
		}
		return gc.Comment(ctx, in.Repo, in.PRNumber, in.Body)
	}),
	"create_pull_request": repoAction(func(ctx context.Context, gc *Client, in actionParams) (any, error) {
		head := strings.TrimSpace(in.Head)
		if head == "" {
			head = strings.TrimSpace(in.Branch) // the branch just committed to
		}
		if head == "" || strings.TrimSpace(in.Title) == "" {
			return nil, fmt.Errorf("head (the source branch) or title missing")
		}
		base := strings.TrimSpace(in.Base)
		if base == "" {
			r, err := gc.GetRepo(ctx, in.Repo)
			if err != nil {
				return nil, err
			}
			base = r.DefaultBranch
		}
		if head == base {
			return nil, fmt.Errorf("head and base are identical (%q)", base)
		}
		// The assignee must be resolvable — a PR without a named human as its
		// recipient is not provided for here. If it is missing but the
		// underlying issue is named, the PR falls to that issue's REPORTER:
		// whoever wrote the need down decides on the merge. Entering the
		// manager across the board makes them the bottleneck for work they
		// never asked for.
		assignee := strings.TrimSpace(in.Assignee)
		if assignee == "" && in.IssueNumber != 0 {
			iss, err := gc.GetIssue(ctx, in.Repo, in.IssueNumber)
			if err != nil {
				return nil, err
			}
			assignee = iss.User.Login
		}
		if assignee == "" {
			return nil, fmt.Errorf("assignee missing — enter the GitHub login of the issue reporter (failing that, your manager) or pass issue_number along")
		}
		u, err := gc.LookupUser(ctx, assignee)
		if err != nil {
			return nil, err
		}
		pr, err := gc.CreatePull(ctx, in.Repo, head, base, in.Title, in.Description, in.Draft)
		if err != nil {
			return nil, err
		}
		// Assignee and reviewer hang off separate endpoints on GitHub — the PR
		// exists at this point, so a failure here is reported but does not undo
		// it.
		out := map[string]any{"pull_request": pr}
		if _, err := gc.AddAssignees(ctx, in.Repo, pr.Number, []string{u.Login}); err != nil {
			out["assignee_error"] = err.Error()
		} else {
			out["assignee"] = u.Login
		}
		// reviewer is optional: if a QA/test agent is responsible, you enter it
		// as the reviewer (the assignee stays the recipient). GitHub refuses to
		// have the author review their own PR, so an assignee identical to the
		// author is not entered as reviewer either.
		reviewer := strings.TrimSpace(in.Reviewer)
		if reviewer == "" {
			reviewer = u.Login
		}
		if !strings.EqualFold(reviewer, pr.User.Login) {
			ru, err := gc.LookupUser(ctx, reviewer)
			if err != nil {
				out["reviewer_error"] = err.Error()
			} else if _, err := gc.RequestReviewers(ctx, in.Repo, pr.Number, []string{ru.Login}); err != nil {
				out["reviewer_error"] = err.Error()
			} else {
				out["reviewer"] = ru.Login
			}
		}
		return out, nil
	}),
	"request_review": repoAction(func(ctx context.Context, gc *Client, in actionParams) (any, error) {
		if in.PRNumber == 0 {
			return nil, fmt.Errorf("pr_number missing")
		}
		u, err := gc.LookupUser(ctx, in.Username)
		if err != nil {
			return nil, err
		}
		if _, err := gc.RequestReviewers(ctx, in.Repo, in.PRNumber, []string{u.Login}); err != nil {
			return nil, err
		}
		return map[string]any{"reviewer": u.Login, "pr_number": in.PRNumber}, nil
	}),
	"approve_pr": repoAction(func(ctx context.Context, gc *Client, in actionParams) (any, error) {
		if in.PRNumber == 0 {
			return nil, fmt.Errorf("pr_number missing")
		}
		if _, err := gc.ApprovePull(ctx, in.Repo, in.PRNumber, in.Body); err != nil {
			return nil, err
		}
		return map[string]any{"approved": true, "pr_number": in.PRNumber}, nil
	}),
	"request_changes": repoAction(func(ctx context.Context, gc *Client, in actionParams) (any, error) {
		if in.PRNumber == 0 {
			return nil, fmt.Errorf("pr_number missing")
		}
		if _, err := gc.RequestChanges(ctx, in.Repo, in.PRNumber, in.Body); err != nil {
			return nil, err
		}
		return map[string]any{"changes_requested": true, "pr_number": in.PRNumber}, nil
	}),
	"list_workflow_runs": repoAction(func(ctx context.Context, gc *Client, in actionParams) (any, error) {
		branch := strings.TrimSpace(in.Branch)
		if branch == "" {
			branch = strings.TrimSpace(in.Ref)
		}
		return gc.ListWorkflowRuns(ctx, in.Repo, branch)
	}),
	"list_run_jobs": repoAction(func(ctx context.Context, gc *Client, in actionParams) (any, error) {
		if in.RunID == 0 {
			return nil, fmt.Errorf("run_id missing")
		}
		return gc.ListRunJobs(ctx, in.Repo, in.RunID)
	}),
	"get_job_log": repoAction(func(ctx context.Context, gc *Client, in actionParams) (any, error) {
		if in.JobID == 0 {
			return nil, fmt.Errorf("job_id missing")
		}
		logText, truncated, err := gc.GetJobLog(ctx, in.Repo, in.JobID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"job_id": in.JobID, "log": logText, "truncated": truncated}, nil
	}),
	"rerun_failed_jobs": repoAction(func(ctx context.Context, gc *Client, in actionParams) (any, error) {
		if in.RunID == 0 {
			return nil, fmt.Errorf("run_id missing")
		}
		return map[string]any{"rerun": true, "run_id": in.RunID}, gc.RerunFailedJobs(ctx, in.Repo, in.RunID)
	}),
}

// splitLabels turns the comma-separated form the prompt documents into the list
// GitHub's API expects.
func splitLabels(raw string) []string {
	out := []string{}
	for _, p := range strings.Split(raw, ",") {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func (System) Execute(ctx context.Context, actionName string, params json.RawMessage, cred target.Credential) (any, error) {
	fn, ok := actions[actionName]
	if !ok {
		return nil, fmt.Errorf("unknown action %q", strings.TrimSpace(actionName))
	}
	var in actionParams
	if err := json.Unmarshal(params, &in); err != nil {
		return nil, fmt.Errorf("params: %w", err)
	}
	return fn(ctx, NewClient(cred.BaseURL, cred.Token), in)
}

func (System) PromptDoc() string {
	return `Available GitHub actions. A repository is ALWAYS named as "repo":"owner/name" (e.g. "acme/support") — that is
   GitHub's identifier; there are no numeric project ids. list_repos {} shows the repositories available to you.
   list_issues {"repo":"owner/name","state":"open"|"closed"|"all","labels":"bug,ui","search":"...","milestone":"...","assigned":true|false}
   (all fields optional; without repo all the issues visible to you; assigned=true only the ones assigned to your bot
   user — use that when your playbook only provides for assigned issues; milestone is the TITLE of the milestone
   exactly as in GitHub. Pull requests are NOT part of the result — GitHub lists them among the issues, this action
   sorts them out; use list_pull_requests for them).
   CAUTION: list_issues returns at most 100 hits and does NOT tell you that it truncated. If you get exactly 100 back,
   the list is probably incomplete — narrow it further with repo, milestone, labels or state instead of taking it for
   complete. "search" and "milestone" filter the fetched page, so they narrow the RESULT, not the request —
   combine them with repo when you need certainty.
   get_issue {"repo":"…","issue_number":N},
   download_attachment {"url":"https://github.com/user-attachments/assets/<id>"} — loads an image attached to an
   issue/PR into your sandbox and returns the local path; then look at it with the read tool (vision). IMPORTANT: if an
   issue description or a comment contains an image — in the Markdown syntax ![...](https://github.com/user-attachments/…)
   or as a bare <img src="…"> — you can NOT derive the image from the text. ALWAYS download it first and LOOK AT IT
   (Read) before you take a screenshot into account in your analysis. Only GitHub's own attachment addresses are
   downloaded; a link to somewhere else is refused. NOTE: GitHub has no API for UPLOADING attachments — you cannot
   put a screenshot of your own into a comment. Describe what you saw instead, and commit an image into the branch if
   it has to be preserved.
   create_issue {"repo":"…","title":"…","description":"… (Markdown)","labels":"bug,intake (optional)","assignee":"github-login (optional)"} —
   files a NEW ticket; use it to turn a bug report that does NOT come from GitHub (reported by email, say) into a
   traceable issue. If you do not know the target repository for certain, DO NOT GUESS: ask the reporter which
   repository the fault belongs to (list_repos shows you what is available), and only file the ticket once it is settled.
   list_comments {"repo":"…","issue_number":N}, comment {"repo":"…","issue_number":N,"body":"…"}
   (a comment identical to your own last one is NOT posted again — the answer {"skipped":"duplicate"} is not an error
   but the loop protection. NOTE: GitHub knows NO internal comments — everything you write is visible to whoever can
   see the repository. Write nothing you would not say to the reporter),
   set_state {"repo":"…","issue_number":N,"state":"close"|"reopen"}, escalate {"repo":"…","issue_number":N,"note":"…"}
   (comments and gives the issue back by removing your own assignment),
   assign {"repo":"…","issue_number":N,"username":"github-login"} assigns the issue to a person — after a fix,
   for instance, to the team member responsible for testing according to the team directory; take the GitHub login
   exactly from the section "Team (human employees)" of your prompt and explain the handover in a comment,
   set_labels {"repo":"…","issue_number":N,"add_labels":["…"],"remove_labels":["…"]} sets and removes labels on an
   EXISTING issue without touching the others (give at least one of the two lists; the answer contains the label state
   reached). That is how you maintain an item's working state visibly on the board — state and change in the same step:
   when passing it on, remove the old state label and set the new one, never only add, or an issue ends up carrying
   three contradictory states. The subject-matter labels (component, type) you do not touch. Take the state names
   character for character from your playbook and invent no variants — a label that does not exist in the repository
   makes the call FAIL, which is loud but avoidable. Every label is its own list entry; an entry with a comma is refused.
   Reading the code: checkout {"repo":"…","ref":"branch|tag|sha (optional, default: the default branch)"} loads the
   source into your sandbox and returns the local path. GitHub's archive cannot be narrowed to a subdirectory — if a
   repo is too large, work without a checkout: list_tree {"repo":"…","path":"…","ref":"…","recursive":true|false}
   lists the repository tree (max. 100 entries — narrow it with path), read_file {"repo":"…","file_path":"path/to/file","ref":"…"}
   reads a single file, list_branches {"repo":"…","search":"…"} lists branches (the default branch is marked — do not
   guess branch names), list_commits {"repo":"…","ref":"…","path":"file/or/directory","since":"ISO date"} lists the
   commit history (all filters optional), get_commit {"repo":"…","sha":"…"} returns a commit's diff.
   Pull requests: list_pull_requests {"repo":"…","state":"open"|"closed"|"merged"|"all","search":"…","base":"main"},
   get_pull_request {"repo":"…","pr_number":N} returns a single PR with its merge state (mergeable, mergeable_state)
   AND the CI checks on its head commit — mergeable says nothing about whether the tests are green, so read both,
   list_pr_comments {"repo":"…","pr_number":N} returns the WHOLE conversation of a PR in one chronological list:
   ordinary comments, submitted reviews (field "state": APPROVED / CHANGES_REQUESTED / COMMENTED) and comments on
   single lines of the diff (field "path"). The field "kind" says which of the three an entry is — a review that only
   demands changes in its verdict, without a comment, is feedback you must not miss,
   comment_pr {"repo":"…","pr_number":N,"body":"…"} answers in the review dialogue,
   request_review {"repo":"…","pr_number":N,"username":"github-login"} enters a reviewer on an existing PR —
   as a developer you hand the PR over to the QA/test agent from the team directory with it; explain the handover in a
   comment_pr, approve_pr {"repo":"…","pr_number":N,"body":"… (optional)"} formally approves a PR (as reviewer/QA — the
   green signal to the assignee; the merging itself stays with the human), request_changes {"repo":"…","pr_number":N,"body":"…"}
   is its counterpart: it holds the PR up until the defects you name are fixed. Use it when you found real defects —
   a comment alone does not block a merge. NEVER merge or close a PR yourself.
   CI (GitHub Actions): list_workflow_runs {"repo":"…","branch":"… (optional)"} lists the runs — use it after every push
   to check whether your branch's run is green. If it is RED, diagnose it yourself instead of guessing or asking:
   list_run_jobs {"repo":"…","run_id":N} shows the jobs with their status, get_job_log {"repo":"…","job_id":N} returns
   the end of the failed job's log — fix the cause, commit again, check the run again. If a job fails on infrastructure
   (a runner missing, a registry down, missing access), that belongs in the PR comment as a finding. If such an external
   cause is fixed later, start the failed jobs again with rerun_failed_jobs {"repo":"…","run_id":N} and check the
   result afterwards — report runs that have gone green briefly by comment_pr.
   IMPORTANT — no busy-waiting on CI: if a run is still going, check its status at most twice. If it is still not
   finished then, end your run regularly with done (the interim state as add_note) — your next heartbeat run checks the
   result. Minutes of status polling waste your turn budget.
   Writing developer actions:
   commit {"repo":"…","branch":"fix/…","start_branch":"main (optional, default: the default branch)","message":"…",
   "checkout_path":"<the path from the checkout result>","files":["repo/relative/path.go",…],"deleted":["old.go",…]} —
   pushes your locally edited files as ONE commit onto the branch; if the branch does not exist, it is branched off the
   start_branch. Direct commits onto the default branch are forbidden — the route there goes through:
   create_pull_request {"repo":"…","head":"fix/…","base":"main (optional, default: the default branch)","title":"…",
   "description":"…","assignee":"github-login (optional)","issue_number":N (optional),"reviewer":"github-login (optional)",
   "draft":false} — opens the pull request. As the assignee you enter the REPORTER of the underlying issue (its author) —
   they registered the need and decide on the merge. Simply pass issue_number instead and Covey enters the reporter
   itself. Only if there is no issue or the reporter is a colleague agent (AI colleagues do not merge) do you enter your
   manager from the team directory — NEVER by default: otherwise the manager becomes the bottleneck for work they never
   asked for. If the section "Team (AI colleagues)" contains a QA/test agent responsible for testing, you enter THEM as
   the reviewer (their GitHub login exactly from the directory) — preferably a colleague from YOUR TEAM (the same
   department); if there is none there, take whoever is responsible for testing organisation-wide. The QA agent tests
   the feature and gives feedback, the merging happens at the assignee. Reference the issue in the description with
   "Fixes #<number>" so that GitHub closes it on the merge.
   How to work as a developer — when you do not only confirm a bug but fix it:
   1. checkout the repository, reproduce the fault against the code (file:line).
   2. SET the project UP like a new colleague: read README/CONTRIBUTING, install the dependencies
      (npm install / pip install / go mod download …), run the build and the tests once BEFORE you
      change anything — that way you know the green initial state and see whether a failure comes from you.
   3. Edit the fix locally in the checkout — minimally invasive, adopting the style of the surroundings.
   4. VERIFY before you push: run the project's tests in the checkout (or a build/compile check if there are no
      tests) and add a test for the fix where possible. If tests fail, do NOT push.
   5. commit onto a meaningful feature branch (e.g. fix/issue-<number>-short-description).
   6. create_pull_request; refer to the issue (#<number>) in the description, describe the cause, the fix and how you
      verified it (which tests ran). Then check with list_workflow_runs whether your branch's run goes green.
   7. Comment in the issue: a link to the PR, a short summary. Do NOT close the issue yourself — that happens on the
      merge or through the assignee.
   8. End the task with done — do NOT block, unless your intake runs through the GitHub webhook AND you are
      deliberately waiting for exactly one answer. With heartbeat intake a blocked task would never be woken.
   Working review feedback in — at EVERY heartbeat run, not only for new issues: fetch your open pull requests with
   list_pull_requests {"state":"open"} and check each one with list_pr_comments for new review comments since your last
   answer. If feedback demands changes, fetch the branch with checkout (ref=the PR's head branch), work EVERY point in,
   run the tests again and push with commit onto the same branch (without start_branch — the branch exists). Answer with
   comment_pr what you changed. If you disagree, argue it from the code in the comment_pr instead of changing blindly.
   Check with get_pull_request whether a PR has been merged in the meantime — then comment the result in the associated
   issue; if it was closed without a merge, check why with list_pr_comments and escalate if that is unclear.
   Before every PR answer, check with list_pr_comments whether you have already reacted to the current state — that way
   recurring runs do not work on anything twice.
   You find your working set yourself: list_issues {"state":"open"} returns the open issues.
   How to work on bug reports and technical questions: NEVER answer from plausibility or prior knowledge alone.
   ALWAYS check FIRST whether the reported fault has been fixed in the meantime: list_commits on the relevant branch
   with since=the issue's creation date (and without a path filter — the fix can sit in a completely different layer
   than suspected, the frontend instead of the backend, say), plus list_pull_requests with fitting search terms. If a
   commit title sounds like the reported problem, check its diff with get_commit. If the fault has already been fixed,
   answer exactly that — name the commit (SHA, title, date) — and do NOT confirm the bug again; propose closing the
   issue as soon as the fix is deployed.
   Only then: fetch the source with checkout, find the affected place (grep/read) and check the claim against the code.
   Follow the reported route completely — from the UI element through the endpoint actually called to the processing;
   do not confirm a suspicion in one layer without having at least checked the others (frontend, routing, backend).
   Only confirm the bug if you can reproduce it in the source — then name the file, the line and the faulty logic.
   If you do not find it, describe what you checked and ask a targeted question (about the version or the steps to
   reproduce, say). Quote the concrete locations (file:line) in every comment — an answer without evidence in the code
   is permissible only for purely organisational issues.
   Before commenting, check with list_comments whether you (your bot user) have already answered and whether a new
   answer has arrived since — that way recurring runs do not work on anything twice.
   How to work as a QA/test agent (reviewer) — when you test others' pull requests instead of developing yourself:
   You find your working set through the PRs in which you are entered as the reviewer (your nur-wenn: github:review
   heartbeat fires for exactly that). For EVERY PR to check:
   1. Read get_pull_request: title, description, the linked issue (#number) — derive the ACCEPTANCE CRITERIA from them
      (what should the feature be able to do?). If they are missing, fetch the issue with get_issue.
   2. checkout {"ref":"<the PR's head branch>"} — fetch the branch into your sandbox, NOT the default branch.
   3. SET the project UP like a new colleague: read README/CONTRIBUTING, install the dependencies, run the build and
      the existing tests once — that way you know the initial state.
   4. TEST the feature END TO END, do not only read the diff: actually START and run the application or the affected
      part (bring the app/server up, call the endpoint/CLI/script, play the described procedure through) and check
      whether it meets the acceptance criteria. Drive the error cases and edges the description suggests too.
   5. Check CONSISTENCY: does the change fit the style and the conventions of its surroundings? Does it break existing
      tests or other features? Are there regressions, missing tests, loose ends against the issue? Run the full suite.
   6. Report the result as a comment_pr — concretely and actionably: what you tested (steps/commands), what works, and
      EVERY defect with file:line and a reproduction. No blanket "looks good"; support findings from the code/the run.
      On defects: request_changes with the reasons — that holds the PR up until they are fixed, and the developer agent
      sees your feedback at its next github:pr run. If everything is green and the acceptance criteria are met: say so
      explicitly in the comment_pr and approve with approve_pr — the merging you leave to the assignee.
      NEVER merge or close the PR yourself.
   7. Before every answer, check with list_pr_comments whether new commits/answers have arrived since your last review —
      then test again instead of repeating feedback you have already given.`
}
