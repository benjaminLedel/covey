package github

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"covey/internal/target"
)

// The webhook intake. GitHub is the first code target system in Covey that has
// one — GitLab takes up work purely by polling. What the webhook is used for
// here is deliberately narrow, and the reason is the division of labour with
// the heartbeat:
//
//   - A NEWLY OPENED issue in scope becomes a task. That is intake, and the
//     webhook is the faster route to it than the poll.
//   - Everything else (a comment, a review, the close of a pull request) only
//     WAKES a task that is already waiting for it — CorrelateOnly. If nobody is
//     waiting, it is not new work: the heartbeat's edge check
//     (issueWorkPending) finds the thread anyway at its next run, and a task
//     created here on top of that would mean the same job done twice.
//
// That way both routes may be configured at once without getting in each
// other's way, and each on its own is enough.

// VerifySignature checks the HMAC-SHA256 signature from the X-Hub-Signature-256
// header ("sha256=<hex>"). An empty secret = check disabled (dev only — an open
// endpoint lets anyone create tasks in a foreign org).
func VerifySignature(secret string, body []byte, header string) bool {
	if secret == "" {
		return true
	}
	sig, ok := strings.CutPrefix(strings.TrimSpace(header), "sha256=")
	if !ok {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(want), []byte(sig))
}

// WebhookPayload is the relevant excerpt of the GitHub webhook JSON. GitHub
// sends one shape per event type; the fields that do not belong to the event at
// hand stay nil.
type WebhookPayload struct {
	Action     string `json:"action"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	Sender User `json:"sender"`
	Issue  *struct {
		Number      int    `json:"number"`
		Title       string `json:"title"`
		Body        string `json:"body"`
		HTMLURL     string `json:"html_url"`
		User        User   `json:"user"`
		PullRequest *struct {
			HTMLURL string `json:"html_url"`
		} `json:"pull_request,omitempty"`
	} `json:"issue"`
	PullRequest *struct {
		Number  int    `json:"number"`
		Title   string `json:"title"`
		HTMLURL string `json:"html_url"`
		Merged  bool   `json:"merged"`
	} `json:"pull_request"`
	Comment *struct {
		ID   int64  `json:"id"`
		Body string `json:"body"`
		User User   `json:"user"`
		Path string `json:"path"`
	} `json:"comment"`
	Review *struct {
		ID    int64  `json:"id"`
		Body  string `json:"body"`
		State string `json:"state"`
		User  User   `json:"user"`
	} `json:"review"`
}

// ParseWebhook decodes the payload and rejects anything without a repository —
// every event Covey acts on carries one.
func ParseWebhook(body []byte) (WebhookPayload, error) {
	var p WebhookPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return p, fmt.Errorf("webhook payload: %w", err)
	}
	if p.Repository.FullName == "" {
		return p, fmt.Errorf("webhook payload: repository.full_name missing")
	}
	return p, nil
}

// Event kinds, derived from the payload's SHAPE rather than read from the
// X-GitHub-Event header. The reason is the plugin interface: target.Webhooker
// hands ParseWebhook the body alone — VerifyWebhook is the only place with the
// headers. The shapes are unambiguous, so the derivation costs nothing:
//
//	review present               → a review was submitted
//	comment + issue              → a comment on an issue or PR conversation
//	comment + pull_request       → a comment on a line of the diff
//	issue alone                  → the issue itself changed
//	pull_request alone           → the PR itself changed
const (
	kindReview        = "review"
	kindComment       = "comment"
	kindReviewComment = "review_comment"
	kindIssue         = "issue"
	kindPull          = "pull"
	kindOther         = "other"
)

// Kind derives the event kind from the payload shape (see the constants).
func (p WebhookPayload) Kind() string {
	switch {
	case p.Review != nil:
		return kindReview
	case p.Comment != nil && p.Issue != nil:
		return kindComment
	case p.Comment != nil && p.PullRequest != nil:
		return kindReviewComment
	case p.Issue != nil:
		return kindIssue
	case p.PullRequest != nil:
		return kindPull
	default:
		return kindOther
	}
}

// IssueCorrelationKey is the stable correlation key for an issue thread. A
// blocked task carries it and is woken by the matching comment.
func IssueCorrelationKey(repo string, number int) string {
	return fmt.Sprintf("github:issue:%s#%d", strings.ToLower(repo), number)
}

// PullCorrelationKey is the counterpart for a pull request thread.
func PullCorrelationKey(repo string, number int) string {
	return fmt.Sprintf("github:pull:%s#%d", strings.ToLower(repo), number)
}

// isOwnVoice: does the contribution come from the agent itself? Then it must
// not wake anything — otherwise the agent's own comment starts the next run,
// which comments, which starts the next run. GitHub Apps identify themselves
// through sender.type; a bot on a personal access token is an ordinary user to
// GitHub and has to be named through COVEY_GITHUB_BOT_LOGINS.
func isOwnVoice(u User) bool {
	if strings.EqualFold(u.Type, "Bot") {
		return true
	}
	return botLogins()[strings.ToLower(strings.TrimSpace(u.Login))]
}

// Event turns a payload into the wake event for the orchestrator. Anything not
// recognised answers with Wake=false: the delivery is registered (and thereby
// acknowledged) but changes nothing — GitHub sends whatever the hook was
// subscribed to, and an unknown shape is not a reason to invent work.
func (p WebhookPayload) Event() target.WebhookEvent {
	repo := p.Repository.FullName
	inScope := repoInScope(repo)
	own := isOwnVoice(p.Sender)

	switch p.Kind() {
	case kindIssue:
		if p.Issue.PullRequest != nil {
			break // a PR reached us through the issue shape
		}
		return target.WebhookEvent{
			DedupKey:       fmt.Sprintf("github:issues:%s#%d:%s", repo, p.Issue.Number, p.Action),
			CorrelationKey: IssueCorrelationKey(repo, p.Issue.Number),
			Title:          fmt.Sprintf("GitHub issue %s#%d: %s", repo, p.Issue.Number, p.Issue.Title),
			TaskBody: fmt.Sprintf("New issue in %s (number=%d, reported by @%s).\nTitle: %s\n%s\n\nDescription:\n%s\n\nWork on it through the action proxy (system github, repo=%q, issue_number=%d).",
				repo, p.Issue.Number, p.Issue.User.Login, p.Issue.Title, p.Issue.HTMLURL, p.Issue.Body, repo, p.Issue.Number),
			ResumeInput: fmt.Sprintf("Issue %s#%d was %s.", repo, p.Issue.Number, p.Action),
			// Only a newly opened (or reopened) issue is intake. label/assign/
			// edit events change an item that already exists — the agent sees
			// that at its next run.
			Wake: inScope && !own && (p.Action == "opened" || p.Action == "reopened"),
		}

	case kindComment:
		key, kind := IssueCorrelationKey(repo, p.Issue.Number), "issue"
		if p.Issue.PullRequest != nil {
			key, kind = PullCorrelationKey(repo, p.Issue.Number), "pull request"
		}
		return target.WebhookEvent{
			DedupKey:       fmt.Sprintf("github:comment:%s:%d:%s", repo, p.Comment.ID, p.Action),
			CorrelationKey: key,
			Title:          fmt.Sprintf("GitHub %s %s#%d: new comment", kind, repo, p.Issue.Number),
			ResumeInput: fmt.Sprintf("New comment from @%s on %s %s#%d:\n%s",
				p.Comment.User.Login, kind, repo, p.Issue.Number, p.Comment.Body),
			Wake:          inScope && !own && p.Action == "created",
			CorrelateOnly: true,
		}

	case kindReviewComment:
		return target.WebhookEvent{
			DedupKey:       fmt.Sprintf("github:review_comment:%s:%d:%s", repo, p.Comment.ID, p.Action),
			CorrelationKey: PullCorrelationKey(repo, p.PullRequest.Number),
			Title:          fmt.Sprintf("GitHub pull request %s#%d: comment on the diff", repo, p.PullRequest.Number),
			ResumeInput: fmt.Sprintf("Review comment from @%s on pull request %s#%d (%s):\n%s",
				p.Comment.User.Login, repo, p.PullRequest.Number, p.Comment.Path, p.Comment.Body),
			Wake:          inScope && !own && p.Action == "created",
			CorrelateOnly: true,
		}

	case kindReview:
		if p.PullRequest == nil {
			break
		}
		return target.WebhookEvent{
			DedupKey:       fmt.Sprintf("github:review:%s:%d:%s", repo, p.Review.ID, p.Action),
			CorrelationKey: PullCorrelationKey(repo, p.PullRequest.Number),
			Title:          fmt.Sprintf("GitHub pull request %s#%d: review (%s)", repo, p.PullRequest.Number, p.Review.State),
			ResumeInput: fmt.Sprintf("Review from @%s on pull request %s#%d (%s):\n%s",
				p.Review.User.Login, repo, p.PullRequest.Number, p.Review.State, p.Review.Body),
			Wake:          inScope && !own && p.Action == "submitted",
			CorrelateOnly: true,
		}

	case kindPull:
		result := "closed without a merge"
		if p.PullRequest.Merged {
			result = "merged"
		}
		return target.WebhookEvent{
			DedupKey:       fmt.Sprintf("github:pull:%s#%d:%s:%t", repo, p.PullRequest.Number, p.Action, p.PullRequest.Merged),
			CorrelationKey: PullCorrelationKey(repo, p.PullRequest.Number),
			Title:          fmt.Sprintf("GitHub pull request %s#%d: %s", repo, p.PullRequest.Number, result),
			ResumeInput:    fmt.Sprintf("Pull request %s#%d (%s) was %s.", repo, p.PullRequest.Number, p.PullRequest.Title, result),
			// The close of a PR is only news for whoever is waiting for it —
			// otherwise it is not work. The sender is deliberately not checked:
			// a merge carried out by the agent itself still ends the wait.
			Wake:          inScope && p.Action == "closed",
			CorrelateOnly: true,
		}
	}

	return target.WebhookEvent{
		DedupKey: fmt.Sprintf("github:%s:%s:%s", p.Kind(), repo, p.Action),
		Wake:     false,
	}
}
