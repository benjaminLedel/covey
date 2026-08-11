package gitlab

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"covey/internal/target"
)

// System binds GitLab in as a target-system plugin to the target registry: the
// webhook entry (token check, idempotency, correlation), the agent actions and
// the action documentation for the system prompt.
type System struct{}

func init() {
	target.Register(target.Descriptor{
		Name:        "gitlab",
		Label:       "GitLab",
		Description: "GitLab issues as the working set: find issues (list_projects/list_issues, by milestone too), file externally reported bugs as a ticket (create_issue), maintain the working state on the board (set_labels/assign), check out source code, set the project up and verify bugs against the code (checkout + sandbox shell), read screenshots/images attached to issues (download_upload + vision), attach your own screenshots to an MR/an issue (upload + comment_mr), develop fixes — commit onto a feature branch (commit), open a merge request to your manager (create_merge_request, optionally with a QA agent as reviewer) and live the review loop: on every heartbeat run check open MRs for new review feedback (list_merge_requests/list_mr_notes/comment_mr), diagnose red CI yourself (list_pipelines/list_pipeline_jobs/get_job_log) and react to the merge. Usable as a QA/test agent too: test others' MRs in which you are entered as reviewer end to end, give feedback and, where assigned, close the acceptance with the merge (set_reviewer/approve_mr/merge_mr, nur-wenn: gitlab:review). Intake through HEARTBEAT.md (polling), auth by API token (the secrets gitlab_token + gitlab_url).",
		Kind:        "builtin",
		Category:    target.CategoryCode,
		Scopes:      []string{"read", "write", "comment", "merge"},
		System:      System{},
		SetupDoc: `1. Create a bot user of its own in GitLab (covey-bot, say), add it to the
   target projects and, as that user, generate an access token with the
   scope "api". Role: reporter suffices for reading/commenting; if the agent
   is to push fixes and open merge requests (commit /
   create_merge_request), it needs developer. A QA agent that is to close its
   acceptance with the merge (merge_mr) needs developer as well — maintainer on
   protected target branches.

2. Store under Secrets and assign to the agent:
   gitlab_url   = https://gitlab.example.com   (without /api/v4)
   gitlab_token = the token from step 1

3. Enable in the agent's ACCESS.md:
   - system: gitlab scope: read,write,comment
   For a QA agent that is to merge, add the scope merge. Whoever wants to
   forbid the merge for a particular agent despite the role either lists the
   tools explicitly in ACCESS.md (tools: without merge_mr) or rules the subject
   gitlab:merge_mr by a guard rail — with "ask" every merge goes through the
   Approvals page.

4. Intake by heartbeat (GitLab has no webhook — the agent takes up work
   exclusively by polling) — two separate entries in the agent's
   HEARTBEAT.md, each gated on its own:
   - alle: 15m nur-wenn: gitlab:issues titel: Review GitLab issues aufgabe:
     Find open issues (list_issues state=opened), work on the new ones and check
     with list_notes whether your queries have been answered. For bugs: fetch the
     code with checkout and verify the claim against the source.
   - alle: 15m nur-wenn: gitlab:mr titel: Look after merge requests aufgabe:
     Check your open merge requests (list_merge_requests state=opened) for
     new review feedback (list_mr_notes), work it in and react to
     merge/close.
   (The sub-scope after the colon saves the expensive agent run deliberately:
    nur-wenn: gitlab:issues fires on ANY open issue in the intake scope
    (for agents that triage all open issues),
    nur-wenn: gitlab:mr only when one of your open MRs has unanswered
    review feedback. That way both tasks run separately without one of them
    firing for the other's work. nur-wenn: gitlab without a sub-scope
    checks both together — only needed if you want both jobs in ONE task.)
    IMPORTANT — if your playbook works only on issues ASSIGNED TO YOU (list_issues
    assigned=true), use nur-wenn: gitlab:issues:assigned. Then only an open issue
    assigned to you wakes you — otherwise every open issue of someone else's
    in the scope would start your agent needlessly in every interval.
   Optional project filter (applies to list_issues/list_projects):
   COVEY_GITLAB_INTAKE_PROJECTS="group/support"   (empty = all)

Details: docs/ops-gitlab.md in the repository.`,
	})
}

func (System) Name() string { return "gitlab" }

// issueProjectPath derives the project path from the full reference
// ("group/support#23") — the issue API does not return path_with_namespace
// directly. An empty return value when no reference is present; the intake
// filter then only matches through the numeric project id.
func issueProjectPath(i Issue) string {
	if idx := strings.LastIndex(i.References.Full, "#"); idx > 0 {
		return i.References.Full[:idx]
	}
	return ""
}

// mrProjectPath derives the project path from the full MR reference
// ("group/project!9") — as issueProjectPath, but split at the "!".
func mrProjectPath(m MergeRequest) string {
	if idx := strings.LastIndex(m.References.Full, "!"); idx > 0 {
		return m.References.Full[:idx]
	}
	return ""
}

// HasWork (target.WorkChecker): the control plane's cheap pre-check for
// nur-wenn: heartbeats. Without a webhook GitLab takes up work purely by
// polling; this check saves the (expensive) agent wake-up when there is nothing
// to do at the moment. Work is present when ONE of the following holds:
//
//   - There is an open issue in the intake scope (the global GET /issues,
//     followed by the COVEY_GITLAB_INTAKE_PROJECTS filter) — what the agent
//     would not see does not wake it either — on which the bot has not yet
//     answered last.
//   - The bot has an open merge request it opened itself with unanswered
//     review feedback (the last non-system comment comes from someone other
//     than the bot). That carries the review loop without a webhook.
//
// The completion of a merge needs no branch of its own: if the associated issue
// is still open, it wakes through the issue branch; if it was closed
// automatically on the merge, there is nothing left to do. What counts
// everywhere is the **edge** (has something happened since the bot's last
// move?), not the level (is anything open anywhere?) — otherwise the same
// unfinished item wakes the agent afresh in every interval.
func (System) HasWork(ctx context.Context, cred target.Credential) (bool, error) {
	has, _, err := System{}.HasWorkSigned(ctx, cred, "")
	return has, err
}

// HasWorkKind (target.KindWorkChecker) gates a single kind of work so that
// several heartbeats (nur-wenn: gitlab:issues, :mr, :review) fire separately:
//
//   - "issues"/"issue"  → is ANY open issue in the intake scope waiting for a
//     reaction (for agents that triage all open issues)?
//   - "issues:assigned"/"assigned" → is an open issue waiting that is ASSIGNED
//     to the bot user itself (scope=assigned_to_me)? Exactly that is what an
//     agent needs whose playbook only works on its own issues (list_issues
//     assigned=true) — otherwise every open issue of someone else's in the
//     scope wakes it. "Waiting" means in both cases: the bot has not yet
//     commented there last (see issueWorkPending).
//   - "mr"/"mrs"        → is one of the MRs the bot opened ITSELF waiting for
//     an answer (the author's view, the developer review loop)?
//   - "review"/"reviews" → is one of the MRs in which the bot is entered as
//     REVIEWER waiting for its review (the QA/test view)?
//   - otherwise         → both of HasWork, fail-open on an unknown scope.
func (System) HasWorkKind(ctx context.Context, cred target.Credential, kind string) (bool, error) {
	has, _, err := System{}.HasWorkSigned(ctx, cred, kind)
	return has, err
}

// HasWorkSigned (target.SignedWorkChecker) is the actual check: besides the
// yes/no it returns the signature of the waiting items so that the control
// plane does not wake twice on the same state. An agent may thereby end a run
// silently — the QA colleague's feedback was an approval, there is nothing to
// do — without being woken again in the next interval. If, by contrast, a new
// contribution or a push comes along, the signature changes and the agent wakes
// up. Whether a piece of feedback means work (reported defects) or only
// information (an approval) is thus decided by the agent and not by the gate.
func (System) HasWorkSigned(ctx context.Context, cred target.Credential, kind string) (bool, string, error) {
	gc := NewClient(cred.BaseURL, cred.Token)
	var (
		waiting []string
		err     error
	)
	switch kind {
	case "issues", "issue":
		waiting, err = issueWorkPending(ctx, gc, false)
	case "issues:assigned", "issue:assigned", "assigned":
		waiting, err = issueWorkPending(ctx, gc, true)
	case "mr", "mrs":
		waiting, err = mrReviewPending(ctx, gc)
	case "review", "reviews":
		waiting, err = mrReviewAssignedPending(ctx, gc)
	default:
		// Without a sub-scope both count — issues AND one's own review loop.
		waiting, err = issueWorkPending(ctx, gc, false)
		if err == nil {
			var mrs []string
			if mrs, err = mrReviewPending(ctx, gc); err == nil {
				waiting = append(waiting, mrs...)
			}
		}
	}
	if err != nil {
		return false, "", err
	}
	return len(waiting) > 0, workSig(waiting), nil
}

// issueMaxNotesChecks caps the comment check of issueWorkPending: the check
// runs in every heartbeat interval and must not run away with the number of
// open issues. Whoever has more open issues than that gets woken — the call
// "which of them is new" is then made by the agent itself.
const issueMaxNotesChecks = 30

// issueWorkPending: is at least one open issue in the intake scope waiting for
// the agent? The global GET /issues, followed by the
// COVEY_GITLAB_INTAKE_PROJECTS filter — what the agent would not see through
// list_issues does not wake it either. assignedOnly=true counts only the issues
// assigned to the bot user (scope=assigned_to_me) — fitting for a playbook that
// works exclusively on assigned issues; otherwise every open issue of someone
// else's in the scope would wake the agent.
//
// What is decisive is the edge, not the level: an open issue is work as long as
// the last non-system comment does NOT come from the bot (or there is none at
// all yet — then the first triage is outstanding). If the bot wrote last, the
// issue rests until someone answers. Without that edge a permanently assigned
// issue would remain "work for ever" and the heartbeat would wake the agent
// afresh in every interval on the same, long-settled matter — the same logic
// already carries mrReviewPending/mrReviewAssignedPending.
//
// The contract that follows: **an agent that has worked on an issue must
// comment there.** A silent run counts as "not yet worked on" and wakes again.
// The playbook "issue triage" holds it that way.
func issueWorkPending(ctx context.Context, gc *Client, assignedOnly bool) ([]string, error) {
	issues, err := gc.ListIssues(ctx, 0, "opened", "", "", "", assignedOnly)
	if err != nil {
		return nil, err
	}
	inScope := issues[:0]
	for _, i := range issues {
		if projectInScope(i.ProjectID, issueProjectPath(i)) {
			inScope = append(inScope, i)
		}
	}
	if len(inScope) == 0 {
		return nil, nil
	}
	if len(inScope) > issueMaxNotesChecks {
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
		// The internal window: the check needs the END of the thread — the last
		// comment and the highest note id. It does not go into any context, so it
		// may be generous.
		p, err := gc.ListNotes(ctx, i.ProjectID, i.IID, notesWindowInternal, 1)
		if err != nil {
			return nil, err
		}
		if lastHumanNoteIsMine(p.Notes, me.Username) {
			continue // already answered — rests until someone replies to it
		}
		waiting = append(waiting, threadSig("issue", i.ProjectID, i.IID, p.Notes))
	}
	return waiting, nil
}

// threadSig describes a waiting item in such a way that the description changes
// exactly when something new has happened there: the project, the number and
// the highest note id of the thread. GitLab hands out note ids monotonically
// and records pushes as a system note too — new commits therefore change the
// signature along with them without costing an additional request.
func threadSig(kind string, projectID, iid int, notes []Note) string {
	last := 0
	for _, n := range notes {
		if n.ID > last {
			last = n.ID
		}
	}
	return fmt.Sprintf("%s%d!%d@%d", kind, projectID, iid, last)
}

// workSig condenses the waiting items into a stable signature. Sorted, because
// GitLab returns them by updated_at — otherwise the signature would change
// through the order alone.
func workSig(waiting []string) string {
	if len(waiting) == 0 {
		return ""
	}
	sorted := append([]string(nil), waiting...)
	sort.Strings(sorted)
	return strings.Join(sorted, ",")
}

// lastHumanNoteIsMine says whether a thread's last non-system comment comes
// from the bot itself. Without any human comment the answer is false: an
// uncommented thread is waiting for the first move.
func lastHumanNoteIsMine(notes []Note, me string) bool {
	for i := len(notes) - 1; i >= 0; i-- {
		if notes[i].System {
			continue
		}
		return notes[i].Author.Username == me
	}
	return false
}

// mrReviewPending checks whether one of the bot's open, self-opened merge
// requests is waiting for an answer: the last human (non-system) comment in the
// thread does not come from the bot. Fresh MRs with no comments at all (the bot
// has just opened it, the review is still outstanding) do NOT count as work —
// otherwise every open MR would wake the agent in every interval.
func mrReviewPending(ctx context.Context, gc *Client) ([]string, error) {
	mrs, err := gc.ListMyOpenMergeRequests(ctx)
	if err != nil {
		return nil, err
	}
	inScope := mrs[:0]
	for _, m := range mrs {
		if projectInScope(m.ProjectID, mrProjectPath(m)) {
			inScope = append(inScope, m)
		}
	}
	if len(inScope) == 0 {
		return nil, nil
	}
	me, err := gc.CurrentUser(ctx)
	if err != nil {
		return nil, err
	}
	var waiting []string
	for _, m := range inScope {
		p, err := gc.ListMRNotes(ctx, m.ProjectID, m.IID, notesWindowInternal, 1)
		if err != nil {
			return nil, err
		}
		notes := p.Notes
		// Within the window the notes arrive chronologically; the last non-system
		// comment decides. If it is from someone other than the bot, review
		// feedback is waiting to be worked on.
		for i := len(notes) - 1; i >= 0; i-- {
			if notes[i].System {
				continue
			}
			if notes[i].Author.Username != me.Username {
				waiting = append(waiting, threadSig("mr", m.ProjectID, m.IID, notes))
			}
			break // the last human comment is the bot's → already answered
		}
	}
	return waiting, nil
}

// mrReviewAssignedPending is the mirror image of mrReviewPending from the
// reviewer's point of view: is one of the open merge requests in which the bot
// is entered as REVIEWER waiting for its review? That carries the review loop
// for a QA/test agent without a webhook, gated through nur-wenn: gitlab:review.
//
// Work is present when a human has written since one's own last comment or the
// author has pushed NEW COMMITS — or when the bot has said nothing here at all
// yet. Unlike in the author loop, a fresh MR handed to me for review (still
// without a comment) VERY MUCH counts as work: it is precisely that one waiting
// for my first review. If the bot commented last, the MR rests until the author
// reacts with code — a mere text answer from the author agent ("thanks for the
// review") is no occasion for a new review round, otherwise the two agents work
// each other up.
func mrReviewAssignedPending(ctx context.Context, gc *Client) ([]string, error) {
	me, err := gc.CurrentUser(ctx)
	if err != nil {
		return nil, err
	}
	mrs, err := gc.ListReviewMergeRequests(ctx, me.Username)
	if err != nil {
		return nil, err
	}
	var waiting []string
	for _, m := range mrs {
		if !projectInScope(m.ProjectID, mrProjectPath(m)) {
			continue
		}
		p, err := gc.ListMRNotes(ctx, m.ProjectID, m.IID, notesWindowInternal, 1)
		if err != nil {
			return nil, err
		}
		if !lastHumanNoteIsMine(p.Notes, me.Username) {
			waiting = append(waiting, threadSig("mr", m.ProjectID, m.IID, p.Notes))
		}
	}
	return waiting, nil
}

// ActionSubject: public comments (internal=false) are a guard-rail subject of
// their own that can be ruled more sharply — analogous to zammad:reply_external.
func (System) ActionSubject(action string, params json.RawMessage) string {
	if action == "comment" {
		var p struct {
			Internal *bool `json:"internal"`
		}
		json.Unmarshal(params, &p)
		if p.Internal != nil && !*p.Internal {
			return "gitlab:comment_external"
		}
		return "gitlab:comment_internal"
	}
	return "gitlab:" + action
}

// mergeBlockedReason is the merge gate: it returns the reason why this MR must
// NOT be merged by the agent — empty means the way is clear. Fail-closed
// throughout: what cannot be established (approval state not readable, pipeline
// missing) counts as a reason against, never for. The four conditions are the
// ones the QA agent cannot judge from its own run:
//
//   - the MR is open and free of conflicts,
//   - every blocking discussion is resolved,
//   - the head pipeline has passed,
//   - the agent's OWN approval is on record.
//
// The last one is what keeps a developer agent from merging its own work: in
// GitLab nobody can approve their own MR, so an author never gets past this
// point — quite apart from the tool assignment (ACCESS.md tools:) and the
// guard-rail subject gitlab:merge_mr, with which the merge can additionally be
// denied or put behind an approval gate for the whole organization.
func mergeBlockedReason(ctx context.Context, gc *Client, projectID int, mr MergeRequestDetail) string {
	if mr.State != "opened" {
		return fmt.Sprintf("the merge request is not open (state %q)", mr.State)
	}
	if mr.SHA == "" {
		return "GitLab reports no head commit (sha) — without it the reviewed state cannot be pinned"
	}
	if mr.HasConflicts {
		return "the merge request has conflicts with the target branch"
	}
	if !mr.BlockingDiscussionsResolved {
		return "there are unresolved discussions on the merge request"
	}
	if s := mr.DetailedMergeStatus; s != "" && s != "mergeable" {
		return fmt.Sprintf("GitLab does not consider the merge request mergeable (detailed_merge_status %q)", s)
	}
	if mr.HeadPipeline == nil {
		return "no pipeline has run on the head commit"
	}
	if mr.HeadPipeline.Status != "success" {
		return fmt.Sprintf("the pipeline of the head commit is not green (status %q)", mr.HeadPipeline.Status)
	}
	me, err := gc.CurrentUser(ctx)
	if err != nil || me.Username == "" {
		return "one's own user could not be established (fail-closed)"
	}
	approvals, err := gc.GetMRApprovals(ctx, projectID, mr.IID)
	if err != nil {
		return "the approval state could not be read (fail-closed): " + err.Error()
	}
	approvedByMe := approvals.UserHasApproved
	for _, a := range approvals.ApprovedBy {
		if a.User.Username == me.Username {
			approvedByMe = true
		}
	}
	if !approvedByMe {
		return "your own approval is not on record — test first, then approve_mr, only then merge"
	}
	if approvals.ApprovalsLeft > 0 {
		return fmt.Sprintf("the project still requires %d further approval(s)", approvals.ApprovalsLeft)
	}
	return ""
}

// isDuplicateComment is the server-side brake against comment loops: if the new
// comment body is identical to the bot's most recent OWN (non-system) comment,
// it is not posted again. Fail-open: if the who-am-I check goes wrong, the
// comment is posted as usual (no legitimate comment should be blocked). Only
// the repetition of one's own last comment is suppressed.
func isDuplicateComment(ctx context.Context, gc *Client, notes []Note, body string) bool {
	me, err := gc.CurrentUser(ctx)
	if err != nil || me.Username == "" {
		return false
	}
	var lastOwn, lastAt string
	for _, n := range notes {
		if n.System || n.Author.Username != me.Username {
			continue
		}
		if n.CreatedAt >= lastAt { // ISO8601 sorts lexicographically
			lastAt, lastOwn = n.CreatedAt, n.Body
		}
	}
	return lastOwn != "" && strings.TrimSpace(lastOwn) == strings.TrimSpace(body)
}

// notesBodyMax caps a SINGLE comment in the agent-facing answer. On threads
// that take a daily report the length per entry is the cost driver, not the
// number: three reports of 12k characters each outweigh twenty short comments.
// Whoever needs the full text fetches it with get_note — that is one turn, and
// deliberate.
const notesBodyMax = 4000

// cutBody shortens an over-long comment and says so. Counted in CHARACTERS, not
// bytes: a German daily report carries umlauts, and in UTF-8 each of them takes
// two bytes — measured in bytes the cut would bite at around 2000 characters for
// one text and at 4000 for another, and the stated figure would not match what
// the prompt promises.
func cutBody(n Note) Note {
	zeichen := utf8.RuneCountInString(n.Body)
	if zeichen <= notesBodyMax {
		return n
	}
	n.BodyChars, n.BodyTruncated = zeichen, true
	n.Body = string([]rune(n.Body)[:notesBodyMax]) + "\n\n[… cut off — full text with get_note …]"
	return n
}

func kommentarZahl(n int) string {
	if n == 1 {
		return "1 comment"
	}
	return fmt.Sprintf("%d comments", n)
}

// notesResult is the answer of list_notes/list_mr_notes: the window plus the
// statement of how it relates to the whole. The reason for the shape is a bug
// report from production — the plain array said nothing about the thread being
// longer than what was in it, and an agent cannot tell a full page from a
// complete history.
func notesResult(p NotesPage, limit int, action, idFeld string, projectID, iid int) map[string]any {
	notes := make([]Note, 0, len(p.Notes))
	for _, n := range p.Notes {
		notes = append(notes, cutBody(n))
	}
	// limit belongs in the answer: page counts in windows of this size, so
	// whoever pages on without it lands somewhere else than they think.
	res := map[string]any{"notes": notes, "page": p.Page, "limit": limit, "has_more": p.HasMore}
	if p.Total >= 0 {
		res["total"] = p.Total
	}
	// weiter builds the follow-up call — WITH limit, because page and limit only
	// mean anything together: whoever fetched 100 and then follows a hint without
	// limit gets window 21–40 of a 20-page grid, i.e. comments they already have,
	// while everything in between silently falls through.
	weiter := func(page int) string {
		return fmt.Sprintf("%s {\"project_id\":%d,\"%s\":%d,\"limit\":%d,\"page\":%d}",
			action, projectID, idFeld, iid, limit, page)
	}
	// The window is described counted from the NEWEST comment, because that is
	// where it sits: page 1 is the current state of the thread, every further
	// page one step further into the past.
	von, bis := (p.Page-1)*limit+1, (p.Page-1)*limit+len(p.Notes)
	switch {
	case len(p.Notes) == 0 && p.Page > 1:
		// Paged past the end. Saying "0 comments (older, 161–160)" here would send
		// the agent off leafing further backwards.
		res["window"] = fmt.Sprintf("empty — page %d lies behind the end of the thread", p.Page)
		res["truncated"] = true
		res["hint"] = "the thread is shorter than this page. " + weiter(1) + " fetches the current state"
		return res
	case p.Page == 1 && !p.HasMore:
		res["window"] = fmt.Sprintf("complete thread, %s", kommentarZahl(len(p.Notes)))
	case p.Page == 1:
		res["window"] = fmt.Sprintf("newest %s", kommentarZahl(len(p.Notes)))
	default:
		res["window"] = fmt.Sprintf("%s (older, %d–%d counted from the newest)", kommentarZahl(len(p.Notes)), von, bis)
	}
	if p.Total >= 0 && (p.HasMore || p.Page > 1) {
		res["window"] = fmt.Sprintf("%s of %d", res["window"], p.Total)
	}
	if p.HasMore || p.Page > 1 {
		res["truncated"] = true
		switch {
		case p.HasMore:
			res["hint"] = "this is NOT the whole thread. " + weiter(p.Page+1) +
				" goes one window further back; a larger limit (max 100) enlarges the window"
		default:
			res["hint"] = "this is NOT the whole thread — nothing older follows; " + weiter(p.Page-1) +
				" holds the newer comments"
		}
	}
	return res
}

// aktionsParams is the union of all parameters any GitLab action needs. One
// shared struct instead of one per action: the agent sends a flat JSON object,
// and whatever is missing from it simply stays empty — that is the interface to
// the model, not the shape we would wish for.
type aktionsParams struct {
	ProjectID  int    `json:"project_id"`
	IssueIID   int    `json:"issue_iid"`
	MRIID      int    `json:"mr_iid"`
	PipelineID int    `json:"pipeline_id"`
	JobID      int    `json:"job_id"`
	Body       string `json:"body"`
	Internal   *bool  `json:"internal"`
	State      string `json:"state"`
	Note       string `json:"note"`
	Labels     string `json:"labels"`
	Search     string `json:"search"`
	Milestone  string `json:"milestone"`
	Ref        string `json:"ref"`
	Assigned   bool   `json:"assigned"`
	// set_labels works additively/subtractively instead of overwriting the
	// whole list — otherwise every state change takes the subject-matter labels
	// along with it.
	AddLabels    []string `json:"add_labels"`
	RemoveLabels []string `json:"remove_labels"`
	Path         string   `json:"path"`
	FilePath     string   `json:"file_path"`
	URL          string   `json:"url"`
	Recursive    bool     `json:"recursive"`
	Sha          string   `json:"sha"`
	Since        string   `json:"since"`
	Target       string   `json:"target_branch"`
	Username     string   `json:"username"`
	// The developer workflow: commit + create_merge_request.
	Branch       string   `json:"branch"`
	StartBranch  string   `json:"start_branch"`
	Message      string   `json:"message"`
	CheckoutPath string   `json:"checkout_path"`
	Files        []string `json:"files"`
	Deleted      []string `json:"deleted"`
	SourceBranch string   `json:"source_branch"`
	Title        string   `json:"title"`
	Description  string   `json:"description"`
	Assignee     string   `json:"assignee"`
	Reviewer     string   `json:"reviewer"`
	// The comment window: limit is the number of comments per call, page counts
	// backwards into the history (1 = the newest window), note_id fetches a
	// single comment in full.
	Limit  int `json:"limit"`
	Page   int `json:"page"`
	NoteID int `json:"note_id"`
}

// aktion carries out ONE GitLab action. Formerly each of them lay as a case in
// a 300-line switch; taking on one more action meant touching that function.
// Now each is readable on its own and the dispatch is a table.
type aktion func(ctx context.Context, gc *Client, in aktionsParams) (any, error)

// aktionen is the dispatch: a name from the daemon protocol onto its execution.
// Whoever looks for an action reads one name here and jumps to one place
// instead of scrolling through its neighbours.
var aktionen = map[string]aktion{
	"list_projects": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
		ps, err := gc.ListProjects(ctx)
		if err != nil {
			return nil, err
		}
		out := []Project{}
		for _, p := range ps {
			if projectInScope(p.ID, p.PathWithNamespace) {
				out = append(out, p)
			}
		}
		return out, nil
	},
	"list_issues": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
		issues, err := gc.ListIssues(ctx, in.ProjectID, in.State, in.Labels, in.Search, in.Milestone, in.Assigned)
		if err != nil {
			return nil, err
		}
		out := []Issue{}
		for _, i := range issues {
			if projectInScope(i.ProjectID, issueProjectPath(i)) {
				out = append(out, i)
			}
		}
		return out, nil
	},
	"get_issue": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
		return gc.GetIssue(ctx, in.ProjectID, in.IssueIID)
	},
	"download_upload": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
		if in.ProjectID == 0 || strings.TrimSpace(in.URL) == "" {
			return nil, fmt.Errorf("project_id or url missing")
		}
		return DownloadUploadToSandbox(ctx, gc, in.ProjectID, in.URL, target.Workdir(ctx))
	},
	"upload": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
		if in.ProjectID == 0 || strings.TrimSpace(in.Path) == "" {
			return nil, fmt.Errorf("project_id or path missing")
		}
		return UploadFromSandbox(ctx, gc, in.ProjectID, in.Path, target.Workdir(ctx))
	},
	"checkout": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
		if in.ProjectID == 0 {
			return nil, fmt.Errorf("project_id missing")
		}
		return Checkout(ctx, gc, in.ProjectID, in.Ref, in.Path, target.Workdir(ctx))
	},
	"list_tree": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
		if in.ProjectID == 0 {
			return nil, fmt.Errorf("project_id missing")
		}
		return gc.ListTree(ctx, in.ProjectID, in.Path, in.Ref, in.Recursive)
	},
	"read_file": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
		if in.ProjectID == 0 || in.FilePath == "" {
			return nil, fmt.Errorf("project_id or file_path missing")
		}
		content, truncated, err := gc.ReadFile(ctx, in.ProjectID, in.FilePath, in.Ref)
		if err != nil {
			return nil, err
		}
		return map[string]any{"file_path": in.FilePath, "ref": in.Ref,
			"content": content, "truncated": truncated}, nil
	},
	"list_commits": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
		if in.ProjectID == 0 {
			return nil, fmt.Errorf("project_id missing")
		}
		return gc.ListCommits(ctx, in.ProjectID, in.Ref, in.Path, in.Since)
	},
	"get_commit": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
		if in.ProjectID == 0 || in.Sha == "" {
			return nil, fmt.Errorf("project_id or sha missing")
		}
		return gc.GetCommitDiff(ctx, in.ProjectID, in.Sha)
	},
	"list_merge_requests": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
		if in.ProjectID == 0 {
			return nil, fmt.Errorf("project_id missing")
		}
		return gc.ListMergeRequests(ctx, in.ProjectID, in.State, in.Search, in.Target)
	},
	"get_merge_request": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
		if in.ProjectID == 0 || in.MRIID == 0 {
			return nil, fmt.Errorf("project_id or mr_iid missing")
		}
		return gc.GetMergeRequest(ctx, in.ProjectID, in.MRIID)
	},
	"list_mr_notes": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
		if in.ProjectID == 0 || in.MRIID == 0 {
			return nil, fmt.Errorf("project_id or mr_iid missing")
		}
		limit := notesLimit(in.Limit)
		p, err := gc.ListMRNotes(ctx, in.ProjectID, in.MRIID, limit, in.Page)
		if err != nil {
			return nil, err
		}
		return notesResult(p, limit, "list_mr_notes", "mr_iid", in.ProjectID, in.MRIID), nil
	},
	"comment_mr": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
		if in.ProjectID == 0 || in.MRIID == 0 || strings.TrimSpace(in.Body) == "" {
			return nil, fmt.Errorf("project_id, mr_iid or body missing")
		}
		if p, err := gc.ListMRNotes(ctx, in.ProjectID, in.MRIID, notesWindowInternal, 1); err == nil && isDuplicateComment(ctx, gc, p.Notes, in.Body) {
			return map[string]any{"skipped": "duplicate",
				"reason": "identical to your own last comment — not posted again"}, nil
		}
		return gc.CommentMR(ctx, in.ProjectID, in.MRIID, in.Body)
	},
	"set_reviewer": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
		if in.ProjectID == 0 || in.MRIID == 0 {
			return nil, fmt.Errorf("project_id or mr_iid missing")
		}
		u, err := gc.LookupUser(ctx, in.Username)
		if err != nil {
			return nil, err
		}
		if _, err := gc.SetMRReviewer(ctx, in.ProjectID, in.MRIID, []int{u.ID}); err != nil {
			return nil, err
		}
		return map[string]any{"reviewer": u.Username, "user_id": u.ID}, nil
	},
	"approve_mr": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
		if in.ProjectID == 0 || in.MRIID == 0 {
			return nil, fmt.Errorf("project_id or mr_iid missing")
		}
		if err := gc.ApproveMR(ctx, in.ProjectID, in.MRIID); err != nil {
			return nil, err
		}
		return map[string]any{"approved": true, "mr_iid": in.MRIID}, nil
	},
	"merge_mr": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
		if in.ProjectID == 0 || in.MRIID == 0 {
			return nil, fmt.Errorf("project_id or mr_iid missing")
		}
		mr, err := gc.GetMergeRequest(ctx, in.ProjectID, in.MRIID)
		if err != nil {
			return nil, err
		}
		if reason := mergeBlockedReason(ctx, gc, in.ProjectID, mr); reason != "" {
			return nil, fmt.Errorf("merge refused: %s — report the state per comment_mr and leave the merge to the human", reason)
		}
		merged, err := gc.MergeMR(ctx, in.ProjectID, in.MRIID, mr.SHA, true)
		if err != nil {
			return nil, err
		}
		return map[string]any{"merged": true, "mr_iid": in.MRIID, "sha": mr.SHA,
			"target_branch": merged.TargetBranch, "web_url": merged.WebURL}, nil
	},
	"list_pipelines": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
		if in.ProjectID == 0 {
			return nil, fmt.Errorf("project_id missing")
		}
		return gc.ListPipelines(ctx, in.ProjectID, in.Ref)
	},
	"list_pipeline_jobs": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
		if in.ProjectID == 0 || in.PipelineID == 0 {
			return nil, fmt.Errorf("project_id or pipeline_id missing")
		}
		return gc.ListPipelineJobs(ctx, in.ProjectID, in.PipelineID)
	},
	"retry_pipeline": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
		if in.ProjectID == 0 || in.PipelineID == 0 {
			return nil, fmt.Errorf("project_id or pipeline_id missing")
		}
		return gc.RetryPipeline(ctx, in.ProjectID, in.PipelineID)
	},
	"get_job_log": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
		if in.ProjectID == 0 || in.JobID == 0 {
			return nil, fmt.Errorf("project_id or job_id missing")
		}
		logText, truncated, err := gc.GetJobLog(ctx, in.ProjectID, in.JobID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"job_id": in.JobID, "log": logText, "truncated": truncated}, nil
	},
	"list_branches": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
		if in.ProjectID == 0 {
			return nil, fmt.Errorf("project_id missing")
		}
		return gc.ListBranches(ctx, in.ProjectID, in.Search)
	},
	"commit": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
		if in.ProjectID == 0 {
			return nil, fmt.Errorf("project_id missing")
		}
		return CommitFromCheckout(ctx, gc, in.ProjectID, in.Branch, in.StartBranch,
			in.Message, in.CheckoutPath, in.Files, in.Deleted, target.Workdir(ctx))
	},
	"create_merge_request": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
		if in.ProjectID == 0 || strings.TrimSpace(in.SourceBranch) == "" || strings.TrimSpace(in.Title) == "" {
			return nil, fmt.Errorf("project_id, source_branch or title missing")
		}
		targetBranch := strings.TrimSpace(in.Target)
		if targetBranch == "" {
			proj, err := gc.GetProject(ctx, in.ProjectID)
			if err != nil {
				return nil, err
			}
			targetBranch = proj.DefaultBranch
		}
		if in.SourceBranch == targetBranch {
			return nil, fmt.Errorf("source_branch and target_branch are identical (%q)", targetBranch)
		}
		// The assignee must be resolvable — an MR without a named human as its
		// recipient is not provided for here. If it is missing but the underlying
		// issue is named, the MR falls to that issue's REPORTER: whoever wrote the
		// need down decides on the merge. Entering the manager across the board
		// makes them the bottleneck for work they never asked for.
		assignee := strings.TrimSpace(in.Assignee)
		if assignee == "" && in.IssueIID != 0 {
			iss, err := gc.GetIssue(ctx, in.ProjectID, in.IssueIID)
			if err != nil {
				return nil, err
			}
			assignee = iss.Author.Username
		}
		if assignee == "" {
			return nil, fmt.Errorf("assignee missing — enter the GitLab username of the issue reporter (failing that, your manager) or pass issue_iid along")
		}
		u, err := gc.LookupUser(ctx, assignee)
		if err != nil {
			return nil, err
		}
		// reviewer is optional: if a QA/test agent is responsible, you enter it as
		// the reviewer (the assignee stays the manager). Without a reviewer the
		// assignee checks it itself — reviewer = assignee as before.
		reviewerID := u.ID
		if r := strings.TrimSpace(in.Reviewer); r != "" && r != assignee {
			ru, err := gc.LookupUser(ctx, r)
			if err != nil {
				return nil, err
			}
			reviewerID = ru.ID
		}
		return gc.CreateMergeRequest(ctx, in.ProjectID, in.SourceBranch, targetBranch,
			in.Title, in.Description, u.ID, reviewerID)
	},
	"create_issue": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
		if in.ProjectID == 0 || strings.TrimSpace(in.Title) == "" {
			return nil, fmt.Errorf("project_id or title missing")
		}
		assigneeID := 0
		if a := strings.TrimSpace(in.Assignee); a != "" {
			u, err := gc.LookupUser(ctx, a)
			if err != nil {
				return nil, err
			}
			assigneeID = u.ID
		}
		return gc.CreateIssue(ctx, in.ProjectID, in.Title, in.Description, in.Labels, assigneeID)
	},
	"list_notes": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
		if in.ProjectID == 0 || in.IssueIID == 0 {
			return nil, fmt.Errorf("project_id or issue_iid missing")
		}
		limit := notesLimit(in.Limit)
		p, err := gc.ListNotes(ctx, in.ProjectID, in.IssueIID, limit, in.Page)
		if err != nil {
			return nil, err
		}
		return notesResult(p, limit, "list_notes", "issue_iid", in.ProjectID, in.IssueIID), nil
	},
	"get_note": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
		if in.ProjectID == 0 || in.NoteID == 0 {
			return nil, fmt.Errorf("project_id or note_id missing")
		}
		switch {
		case in.IssueIID != 0:
			return gc.GetIssueNote(ctx, in.ProjectID, in.IssueIID, in.NoteID)
		case in.MRIID != 0:
			return gc.GetMRNote(ctx, in.ProjectID, in.MRIID, in.NoteID)
		}
		return nil, fmt.Errorf("issue_iid or mr_iid missing")
	},
	"comment": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
		internal := in.Internal == nil || *in.Internal
		if p, err := gc.ListNotes(ctx, in.ProjectID, in.IssueIID, notesWindowInternal, 1); err == nil && isDuplicateComment(ctx, gc, p.Notes, in.Body) {
			return map[string]any{"skipped": "duplicate",
				"reason": "identical to your own last comment — not posted again"}, nil
		}
		return gc.Comment(ctx, in.ProjectID, in.IssueIID, in.Body, internal)
	},
	"set_state": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
		if in.State == "" {
			return nil, fmt.Errorf("state missing")
		}
		return nil, gc.SetState(ctx, in.ProjectID, in.IssueIID, in.State)
	},
	"assign": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
		if in.ProjectID == 0 || in.IssueIID == 0 {
			return nil, fmt.Errorf("project_id or issue_iid missing")
		}
		u, err := gc.LookupUser(ctx, in.Username)
		if err != nil {
			return nil, err
		}
		if err := gc.AssignIssue(ctx, in.ProjectID, in.IssueIID, []int{u.ID}); err != nil {
			return nil, err
		}
		return map[string]any{"assigned_to": u.Username, "user_id": u.ID}, nil
	},
	"set_labels": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
		// Works on either an issue or a merge request — GitLab exposes labels
		// as two separate resources (there is no shared path), so the target
		// is whichever ID the caller actually supplied. issue_iid wins if an
		// agent somehow sends both, which should not happen in practice.
		switch {
		case in.ProjectID == 0:
			return nil, fmt.Errorf("project_id missing")
		case in.IssueIID != 0:
			iss, err := gc.SetLabels(ctx, in.ProjectID, in.IssueIID, in.AddLabels, in.RemoveLabels)
			if err != nil {
				return nil, err
			}
			return map[string]any{"issue_iid": iss.IID, "labels": iss.Labels}, nil
		case in.MRIID != 0:
			mr, err := gc.SetMRLabels(ctx, in.ProjectID, in.MRIID, in.AddLabels, in.RemoveLabels)
			if err != nil {
				return nil, err
			}
			return map[string]any{"mr_iid": mr.IID, "labels": mr.Labels}, nil
		default:
			return nil, fmt.Errorf("issue_iid or mr_iid missing")
		}
	},
	"escalate": func(ctx context.Context, gc *Client, in aktionsParams) (any, error) {
		note := in.Note
		if note == "" {
			note = "Escalated by a Covey agent."
		}
		return nil, gc.Escalate(ctx, in.ProjectID, in.IssueIID, note)
	},
}

func (System) Execute(ctx context.Context, action string, params json.RawMessage, cred target.Credential) (any, error) {
	fn, ok := aktionen[action]
	if !ok {
		return nil, fmt.Errorf("unknown action %q", strings.TrimSpace(action))
	}
	var in aktionsParams
	if err := json.Unmarshal(params, &in); err != nil {
		return nil, fmt.Errorf("params: %w", err)
	}
	return fn(ctx, NewClient(cred.BaseURL, cred.Token), in)
}

// The prompt doc in four blocks. Split by SCOPE, not rewritten: an agent reads
// the same wording it always did, only the parts its ACCESS.md does not cover
// fall away. That matters because the doc sits in the context of every turn —
// the reviewer block alone is around 900 tokens, and a developer agent without
// the merge scope carried it along on every one of its turns without ever being
// able to act on it.
//
// The boundaries follow the granted permissions: writing developer actions and
// the developer playbook need write, the QA/reviewer playbook needs merge. The
// action catalogue and the rules for bug reports apply to everyone.
func (System) PromptDoc() string {
	return promptDocActions + promptDocDeveloper + promptDocIssues + promptDocReviewer
}

// PromptDocForScopes (target.ScopedDocSystem) narrows the doc to the scopes
// granted in ACCESS.md. Fail-open: without scopes the full doc stands — a
// missing entry must not silently take capabilities away from an agent.
func (System) PromptDocForScopes(scopes []string) string {
	if len(scopes) == 0 {
		return System{}.PromptDoc()
	}
	granted := make(map[string]bool, len(scopes))
	for _, s := range scopes {
		granted[strings.ToLower(strings.TrimSpace(s))] = true
	}
	doc := promptDocActions
	if granted["write"] {
		doc += promptDocDeveloper
	}
	doc += promptDocIssues
	if granted["merge"] {
		doc += promptDocReviewer
	}
	return doc
}

const promptDocActions = `Available GitLab actions: list_projects {}, list_issues {"project_id":N,"state":"opened"|"closed"|"all","labels":"...","search":"...","milestone":"...","assigned":true|false}
   (all fields optional; without project_id all the issues visible to you; assigned=true only the ones assigned
   to your bot user — use that when your playbook only provides for assigned issues; milestone is the
   TITLE of the milestone exactly as in GitLab and is the most reliable filter when your assignment hangs off an
   engagement — every issue carries its milestone back in the field "milestone").
   CAUTION: list_issues returns at most 100 hits and does NOT tell you that it truncated. If you get exactly
   100 back, the list is probably incomplete — narrow it further with project_id, milestone, labels or
   state instead of taking it for complete, get_issue {"project_id":N,"issue_iid":N},
   download_upload {"project_id":N,"url":"/uploads/<secret>/<file>.png"} — loads an upload attached to an issue/MR
   (a screenshot, an image) into your sandbox and returns the local path; then look at it with the read tool
   (vision). IMPORTANT: if an issue description or a comment contains an image attachment — in the Markdown syntax
   ![...](/uploads/<32-hex-secret>/<file>) — you can NOT derive the image from the text. ALWAYS download it first
   with download_upload and LOOK AT IT (Read) before you take a screenshot/an image into account in your analysis;
   pass in "url" the reference exactly as it stands between the brackets in the Markdown.
   upload {"project_id":N,"path":"browser/shot.png"} — uploads a file from your sandbox (e.g. a browser
   screenshot) to the project and returns a Markdown reference (the field "markdown", e.g. ![shot](/uploads/<secret>/shot.png)).
   You build that reference into the comment_mr body so that the screenshot is visible directly in the merge request — that
   is how you support a UI behaviour or a defect with a picture, not only with words.
   checkout {"project_id":N,"ref":"branch|tag|sha (optional, default: the default branch)","path":"subdirectory (optional)"} —
   loads the project's source into your sandbox and returns the repository root in "path". ONE checkout without
   "path" is the normal case and gets you the whole project. Only if that fails on the repo size do you fetch
   subdirectories with "path": they land at their upstream place UNDER the same root and grow into one working
   tree, so fetch everything the project needs to build BEFORE you start working — every checkout redraws the
   baseline commit. Do not check a repository out subdirectory by subdirectory as a matter of course; each call
   costs you a turn, and half a project builds and tests as badly as none. If you only want to READ, do without
   a checkout entirely:
   list_tree {"project_id":N,"path":"...","ref":"...","recursive":true|false} lists the repository tree (max. 100 entries —
   narrow it with path), read_file {"project_id":N,"file_path":"path/to/file","ref":"..."} reads a single file,
   create_issue {"project_id":N,"title":"...","description":"... (Markdown)","labels":"bug,intake (optional)","assignee":"gitlab-username (optional)"} —
   files a NEW ticket; use it to turn a bug report that does NOT come from GitLab (reported by email, say) into a
   traceable issue. It needs a project_id — if you do not know the target project for certain, DO NOT GUESS:
   ask the reporter which project the fault belongs to (list_projects shows you the projects available to you),
   and only file the ticket once the project is settled,
   list_notes {"project_id":N,"issue_iid":N,"limit":N (optional, default 20, max 100),"page":N (optional)} —
   returns the NEWEST comments of the ticket, not all of them. The answer says in "window"/"total"/"has_more" how it
   relates to the whole; if "truncated" is set, page=2 fetches the next-older window (page=3 the one before that).
   For long-running tickets that is normal — take the current state from page 1 and only page back when you really
   need the earlier history. Comments longer than 4000 characters arrive cut off ("body_truncated":true);
   get_note {"project_id":N,"issue_iid":N|"mr_iid":N,"note_id":N} fetches such a comment in full,
   comment {"project_id":N,"issue_iid":N,"body":"...","internal":true|false}
   (a comment identical to your own last one is NOT posted again — the answer {"skipped":"duplicate"} is not an error but the loop protection),
   set_state {"project_id":N,"issue_iid":N,"state":"close"|"reopen"}, escalate {"project_id":N,"issue_iid":N,"note":"..."},
   assign {"project_id":N,"issue_iid":N,"username":"gitlab-username"} assigns the issue to a person — after a fix,
   for instance, to the team member responsible for testing according to the team directory; take the GitLab user name
   exactly from the section "Team (human employees)" of your prompt and explain the handover in a comment,
   set_labels {"project_id":N,"issue_iid":N,"add_labels":["…"],"remove_labels":["…"]} sets and removes labels on an
   EXISTING issue without touching the others (give at least one of the two lists; the answer contains the
   label state reached). Give "mr_iid" instead of "issue_iid" to do the exact same thing on a merge request
   — issues and merge requests are separate GitLab resources with separate label endpoints, but this one action
   covers both; give exactly one of the two IDs, never both. This is the action behind every label-driven handoff
   between agents on a merge request (needs-arch-review, ready-for-qa, qa-passed/qa-failed, security-veto) — there
   is no separate MR-labels action to look for. That is how you maintain an item's working state visibly on the
   board — state and change in the same step: when passing it on, remove the old state label and set the new one,
   never only add, or an issue/MR ends up carrying three contradictory states. The subject-matter labels
   (component, type) you do not touch. IMPORTANT: a label that does not yet exist in the project is created
   SILENTLY by GitLab when set — a typo ("in_progress" instead of "in-progress") therefore produces a permanent
   project label nobody clears away. Take the state names character for character from your playbook and invent
   no variants. Every label is its own list entry; an entry with a comma in it is refused,
   list_branches {"project_id":N,"search":"..."} lists branches (the default branch is marked — do not guess branch names),
   list_commits {"project_id":N,"ref":"...","path":"file/or/directory","since":"ISO date"} lists the commit history
   (all filters optional), get_commit {"project_id":N,"sha":"..."} returns a commit's diff,
   list_merge_requests {"project_id":N,"state":"opened"|"merged"|"closed"|"all","search":"...","target_branch":"..."},
   get_merge_request {"project_id":N,"mr_iid":N} returns a single MR with its review state (detailed_merge_status,
   has_conflicts) and CI result (head_pipeline), list_mr_notes {"project_id":N,"mr_iid":N,"limit":N,"page":N} an MR's
   discussion state (review comments) — windowed exactly like list_notes,
   comment_mr {"project_id":N,"mr_iid":N,"body":"..."} answers in the review dialogue,
   set_reviewer {"project_id":N,"mr_iid":N,"username":"gitlab-username"} enters a reviewer on an existing MR —
   as a developer you hand the MR over to the QA/test agent from the team directory with it, for instance; explain the
   handover in a comment_mr, approve_mr {"project_id":N,"mr_iid":N} formally approves an MR (as reviewer/QA — the green
   signal after a completed acceptance),
   merge_mr {"project_id":N,"mr_iid":N} merges the merge request and removes the source branch. Only for the
   REVIEWER after their own acceptance, never for the author of the MR — and only if your ACCESS.md carries the tool.
   The action checks fail-closed before merging: MR open and free of conflicts, every blocking discussion resolved,
   pipeline of the head commit green, your OWN approval on record. If one of them does not hold, it refuses with the
   reason instead of merging — write that reason per comment_mr and leave the merge to the human. Merged is exactly
   the commit you saw (sha); if a new commit has arrived in the meantime, GitLab refuses — then test again,

   list_pipelines {"project_id":N,"ref":"branch (optional)"} lists CI runs — use it after every push to check whether your
   branch's pipeline is green. If it is RED, diagnose it yourself instead of guessing or asking:
   list_pipeline_jobs {"project_id":N,"pipeline_id":N} shows the jobs with their status, get_job_log {"project_id":N,"job_id":N}
   returns the end of the failed job's log — fix the cause, commit again, check the pipeline again.
   If a job fails on infrastructure (a missing runner, a registry down, missing repo access), that belongs in the MR
   comment as a finding. If such an external cause is fixed later (access granted afterwards, say),
   start the run again with retry_pipeline {"project_id":N,"pipeline_id":N} and check the result afterwards —
   report pipelines that have gone green briefly by comment_mr.
   IMPORTANT — no busy-waiting on CI: if a pipeline is still running, check its status at most twice.
   If it is still not finished then, end your run regularly with done (the interim state as add_note) —
   your next heartbeat run checks the result. Minutes of status polling waste your turn budget.
`

// promptDocDeveloper needs the write scope: actions that change the repository,
// and the procedure that goes with them.
const promptDocDeveloper = `   Writing developer actions:
   commit {"project_id":N,"branch":"fix/…","start_branch":"main (optional, default: the default branch)","message":"...",
   "checkout_path":"<the path from the checkout result>","files":["repo/relative/path.go",...],"deleted":["old.go",...]} —
   pushes your locally edited files as ONE commit onto the branch; if the branch does not exist, it is branched off the
   start_branch. Direct commits onto the default branch are forbidden — the route there goes through:
   create_merge_request {"project_id":N,"source_branch":"fix/…","target_branch":"main (optional, default: the default branch)",
   "title":"...","description":"...","assignee":"gitlab-username (optional)","issue_iid":N (optional),
   "reviewer":"gitlab-username (optional)"} — opens the merge request. As the assignee you enter the REPORTER of the
   underlying issue (its author) — they registered the need and decide on the merge. Simply pass
   issue_iid instead and Covey enters the reporter itself. Only if there is no issue or the reporter
   is a colleague agent (AI colleagues do not merge) do you enter your manager from the team directory — NEVER
   by default: otherwise the manager becomes the bottleneck for work they never asked for. Without a reviewer the
   assignee also becomes the reviewer (as before). If the section "Team (AI colleagues)" contains a QA/test agent responsible
   for testing, you enter THEM as the reviewer (their GitLab user name exactly from the directory) — preferably a
   colleague from YOUR TEAM (the same department); if there is none there, take whoever is responsible for testing
   organisation-wide. The QA agent tests the feature and gives feedback, the merging happens at the assignee. The source
   branch is removed automatically after the merge.
   How to work as a developer — when you do not only confirm a bug but fix it:
   1. checkout the project, reproduce the fault against the code (file:line).
   2. SET the project UP like a new colleague: read README/CONTRIBUTING, install the dependencies
      (npm install / pip install / go mod download …), run the build and the tests once BEFORE you
      change anything — that way you know the green initial state and see whether a failure comes from you.
   3. Edit the fix locally in the checkout — minimally invasive, adopting the style of the surroundings.
   4. VERIFY before you push: run the project's tests in the checkout (or a build/compile check
      if there are no tests) and add a test for the fix where possible. If tests fail, do NOT push.
   5. commit onto a meaningful feature branch (e.g. fix/issue-<iid>-short-description).
   6. create_merge_request to your manager; refer to the issue (#<iid>) in the description,
      describe the cause, the fix and how you verified it (which tests ran). If the project has CI,
      check with get_merge_request or list_pipelines whether your branch's pipeline goes green.
   7. Comment in the issue: a link to the MR, a short summary. Do NOT close the issue yourself —
      that happens on the merge or through your manager.
   8. End the task with done — do NOT block. GitLab has no webhook; waiting for a review happens by polling,
      not with the status blocked. Your next heartbeat run checks your open MRs for
      review feedback and the merge state. (A blocked would never be woken here and would block your
      heartbeat permanently.)
   Working review feedback in — at EVERY heartbeat run, not only for new issues: fetch your open MRs with
   list_merge_requests {"state":"opened"} and check each one with list_mr_notes for new
   review comments since your last answer. If feedback demands changes, fetch the branch with checkout
   (ref=source_branch), work EVERY point in, run the tests again and push with commit
   onto the same branch (without start_branch — the branch exists). Answer with comment_mr what you changed.
   If you disagree, argue it from the code in the comment_mr instead of changing blindly. Check with
   list_merge_requests {"state":"merged"} or get_merge_request whether an MR has been merged in the meantime —
   then comment the result in the associated issue; if it was closed without a merge (state="closed"),
   check why with list_mr_notes and escalate if that is unclear. Before every MR answer, check with list_mr_notes
   whether you have already reacted to the current state — that way recurring runs do not work on anything twice.
`

// promptDocIssues applies to everyone: how to find your working set and how to
// deal with a bug report — reading and commenting, no scope of its own.
const promptDocIssues = `   You find your working set yourself: list_issues {"state":"opened"} returns the open issues.
   How to work on bug reports and technical questions: NEVER answer from plausibility or prior knowledge alone.
   ALWAYS check FIRST whether the reported fault has been fixed in the meantime: list_commits on the relevant
   branch with since=the issue's creation date (and without a path filter — the fix can sit in a completely
   different layer than suspected, the frontend instead of the backend, say), plus list_merge_requests with fitting
   search terms. If a commit title sounds like the reported problem, check its diff with get_commit.
   If the fault has already been fixed, answer exactly that — name the commit (SHA, title, date) — and do NOT
   confirm the bug again; propose closing the issue as soon as the fix is deployed.
   Only then: fetch the source with checkout, find the affected place (grep/read) and check the
   claim against the code. Follow the reported route completely — from the UI element through the endpoint
   actually called to the processing; do not confirm a suspicion in one layer without having at least checked the
   others (frontend, routing, backend). Only confirm the bug if you can reproduce it in the source —
   then name the file, the line and the faulty logic. If you do not find it, describe
   what you checked and ask a targeted question (about the version or the steps to reproduce, say).
   Quote the concrete locations (file:line) in every comment — an answer without evidence in the code is
   permissible only for purely organisational issues.
   Before commenting, check with list_notes whether you (your bot user) have already answered and whether a new
   answer has arrived since — that way recurring runs do not work on anything twice.
   IMPORTANT — NEVER end with the status blocked: GitLab takes up work purely by polling, there is no webhook
   that wakes a blocked task again. Wait neither for an issue answer nor for an MR review with
   blocked — end every run with done and let your next heartbeat run pick open issues/MRs
   back up.
`

// promptDocReviewer needs the merge scope: the acceptance procedure of a
// QA/test agent. A developer agent cannot act on it and would only carry it
// through every turn.
const promptDocReviewer = `   How to work as a QA/test agent (reviewer) — when you test others' merge requests instead of developing yourself:
   You find your working set with list_merge_requests {"state":"opened"} and, across projects, through the MRs in
   which you are entered as the reviewer (your nur-wenn: gitlab:review heartbeat fires for exactly that). For EVERY MR to check:
   1. Read get_merge_request: title, description, the linked issue (#iid) — derive the ACCEPTANCE CRITERIA from them
      (what should the feature be able to do?). If they are missing, fetch the issue with get_issue.
   2. checkout {"ref":"<the MR's source_branch>"} — fetch the branch into your sandbox, NOT the default branch.
   3. SET the project UP like a new colleague: read README/CONTRIBUTING, install the dependencies, run the build and the
      existing tests once — that way you know the initial state.
   4. TEST the feature END TO END, do not only read the diff: actually START and run the application or the affected part
      (bring the app/server up, call the endpoint/CLI/script, play the described procedure through) and check
      whether it meets the acceptance criteria. Drive the error cases and edges the description suggests too.
   5. Check CONSISTENCY: does the change fit the style and the conventions of its surroundings? Does it break existing tests or
      other features? Are there regressions, missing tests, loose ends against the issue? Run the full test suite.
   6. Report the result as a comment_mr — concretely and actionably: what you tested (steps/commands), what works,
      and EVERY defect with file:line and a reproduction. No blanket "looks good"; support findings from the code/the run.
      On defects: stay the reviewer (the developer agent sees your feedback at its next gitlab:mr run and works it
      in). If everything is green and the acceptance criteria are met: say so explicitly in the comment_mr and approve with
      approve_mr. Whether you then merge yourself depends on your ACCESS.md: is merge_mr assigned to you, the
      acceptance ends with the merge (merge_mr checks the four conditions itself and refuses with a reason —
      pass that reason on per comment_mr). Without merge_mr the approval is your final word and the merging
      stays with the human. Never close an MR yourself in either case.
   7. Before every answer, check with list_mr_notes whether new commits/answers have arrived since your last review — then test again
      instead of repeating feedback you have already given. As a reviewer too, end every run with done, never with blocked.`
