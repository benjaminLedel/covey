package teams

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"covey/internal/target"
)

// System binds Microsoft Teams into the target registry as a target-system
// plugin: webhook intake (Bot Framework JWT, idempotency, correlation), the
// agent actions and the action documentation for the system prompt (spec/15).
type System struct{}

func init() {
	target.Register(target.Descriptor{
		Name:        "teams",
		Label:       "Microsoft Teams",
		Description: "Chat channel via the Azure Bot Service (spec/15): receive messages (messaging endpoint, JWT-verified) and send them (Bot Connector). Auth via OAuth2 (secrets teams_token = appId:appPassword + optional teams_url).",
		Kind:        "builtin",
		Category:    target.CategoryComms,
		Scopes:      []string{"read", "write"},
		System:      System{},
		// teams_url is the token endpoint — without it the multi-tenant default
		// from config.go applies. Only single-tenant bots set it, hence not a
		// mandatory secret.
		BaseURLOptional: true,
		SetupDoc: `1. Create a bot registration in Azure (Azure Bot / Bot Channels
   Registration) and enable the "Microsoft Teams" channel. Note the Microsoft
   app ID and generate a client secret (app password).

2. Set the messaging endpoint of the bot registration to:
   {public_url}/api/webhooks/teams/<agent-slug>

3. Store under Secrets and assign to the agent:
   teams_token = <app-id>:<app-password>
   teams_url   = (optional) token endpoint, default
                 https://login.microsoftonline.com/botframework.com/oauth2/v2.0/token

4. Grant it in the agent's ACCESS.md:
   - system: teams scope: read,write

5. Process env for the webhook verification:
   COVEY_TEAMS_WEBHOOK_SECRET=<app-id>   (the bot app ID = expected JWT audience;
                                          empty = verification off, tests only)

6. Optional process env:
   COVEY_TEAMS_INTAKE_TENANTS="<tenant-id>"   (empty = all tenants)

7. Upload/sideload a Teams app manifest carrying the app ID as bot-id into
   Teams so that users can message the agent.

Details: docs/ops-teams.md in the repository.`,
	})
}

func (System) Name() string { return "teams" }

// VerifyWebhook validates the Bot Framework JWT. The "secret" is the expected
// bot app ID here (the JWT audience), set via COVEY_TEAMS_WEBHOOK_SECRET;
// empty = verification disabled (dev/faketeams).
func (System) VerifyWebhook(secret string, body []byte, header http.Header) bool {
	return VerifyToken(secret, header.Get("Authorization"))
}

func (System) ParseWebhook(body []byte) (target.WebhookEvent, error) {
	a, err := ParseWebhook(body)
	if err != nil {
		return target.WebhookEvent{}, err
	}
	if a.IsFileConsent() {
		return consentEvent(a), nil
	}
	from := a.From.Name
	if from == "" {
		from = a.From.ID
	}
	convKind := a.Conversation.ConversationType
	if convKind == "" {
		convKind = "chat"
	}
	text := a.CleanText()
	if text == "" {
		text = "(no text)"
	}

	replyHint := fmt.Sprintf(
		"reply {\"service_url\":%q,\"conversation_id\":%q,\"reply_to_activity_id\":%q,\"text\":\"…\"}",
		a.ServiceURL, a.Conversation.ID, a.ID)

	attachSection := attachmentSection(a.Files())

	return target.WebhookEvent{
		DedupKey:       a.DedupKey(),
		CorrelationKey: CorrelationKey(a.Conversation.ID),
		Title:          fmt.Sprintf("Teams message from %s", from),
		TaskBody: fmt.Sprintf(
			"New Microsoft Teams message from %s (%s).\n\nMessage:\n%s%s\n\nReply through the action proxy (system teams):\n%s",
			from, convKind, text, attachSection, replyHint),
		ResumeInput: fmt.Sprintf("Reply from %s in Teams:\n%s%s", from, text, attachSection),
		Wake:        a.ShouldWake(),
	}, nil
}

// consentEvent turns the answer to a consent card into a wake event. At this
// point the agent is parked waiting for exactly this decision (correlated via
// the conversation); the ResumeInput is therefore a direct work order, not a
// message to be answered.
//
// Always CorrelateOnly: a consent is the continuation of work this agent
// started. If nobody is parked on it (task already finished, unblocked
// otherwise, late delivery), it is not new work — otherwise a task would
// appear that tells an unsuspecting agent to upload a file it knows nothing
// about.
func consentEvent(a Activity) target.WebhookEvent {
	name := a.Value.UploadInfo.Name
	if name == "" {
		name = "the file"
	}
	if !a.ConsentAccepted() {
		reason := "declined it"
		if strings.EqualFold(a.Value.Action, "accept") {
			// Consent without an upload URL — nothing the agent could work with.
			reason = "accepted it, but Teams delivered no upload URL"
		}
		return target.WebhookEvent{
			DedupKey:       a.DedupKey(),
			CorrelationKey: CorrelationKey(a.Conversation.ID),
			Title:          fmt.Sprintf("Teams: %s was not accepted", name),
			TaskBody: fmt.Sprintf(
				"The recipient was offered %s and %s. Do not upload anything. If the content matters, offer it as text — otherwise just take note.",
				name, reason),
			ResumeInput: fmt.Sprintf(
				"The recipient was offered %s and %s. Do not upload anything and finish the job; offer the content as text if need be.",
				name, reason),
			Wake:          a.ShouldWake(),
			CorrelateOnly: true,
		}
	}

	// context.key carries the path send_file asked for — that makes the call
	// complete and the agent does not have to guess which file was meant. If it
	// is missing (foreign card, old flow), the placeholder stays.
	path := strings.TrimSpace(a.Value.Context.Key)
	if path == "" {
		path = "<your file>"
	}
	up := a.Value.UploadInfo
	call := fmt.Sprintf(
		"upload_file {\"upload_url\":%q,\"path\":%q,\"service_url\":%q,\"conversation_id\":%q,\"content_url\":%q,\"unique_id\":%q,\"file_type\":%q,\"name\":%q}",
		up.UploadURL, path, a.ServiceURL, a.Conversation.ID, up.ContentURL, up.UniqueID, up.FileType, up.Name)

	return target.WebhookEvent{
		DedupKey:       a.DedupKey(),
		CorrelationKey: CorrelationKey(a.Conversation.ID),
		Title:          fmt.Sprintf("Teams: consent for %s", name),
		TaskBody: "The recipient consented to receiving the file. Upload it now — " +
			"the upload URL is short-lived, so do it immediately:\n" + call,
		ResumeInput:   "Consent granted. Upload the file right now (the upload URL expires):\n" + call,
		Wake:          a.ShouldWake(),
		CorrelateOnly: true,
	}
}

// attachmentSection formats the file attachments as a text block for the task
// body, including the ready-made download_attachment call per attachment. The
// download URLs are short-lived — the agent should fetch them promptly.
func attachmentSection(files []Attachment) string {
	if len(files) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n\nAttachments (%d) — load them into the sandbox with download_attachment and read them with the read tool (the URLs are short-lived, fetch them promptly):", len(files))
	for i, at := range files {
		fmt.Fprintf(&b, "\n  %d. %s (%s)\n     download_attachment {\"url\":%q,\"name\":%q}",
			i+1, at.Filename(), at.ContentType, at.DownloadURL(), at.Filename())
	}
	return b.String()
}

// ActionSubject: every action is its own, separately governable guard-rail
// subject (teams:send, teams:reply, teams:create_conversation).
func (System) ActionSubject(action string, params json.RawMessage) string {
	return "teams:" + action
}

func (System) Execute(ctx context.Context, action string, params json.RawMessage, cred target.Credential) (any, error) {
	c, err := NewClient(cred)
	if err != nil {
		return nil, err
	}
	var in struct {
		ServiceURL        string `json:"service_url"`
		ConversationID    string `json:"conversation_id"`
		ReplyToActivityID string `json:"reply_to_activity_id"`
		TenantID          string `json:"tenant_id"`
		UserID            string `json:"user_id"`
		Text              string `json:"text"`
		URL               string `json:"url"`
		Name              string `json:"name"`
		Path              string `json:"path"`
		Description       string `json:"description"`
		UploadURL         string `json:"upload_url"`
		ContentURL        string `json:"content_url"`
		UniqueID          string `json:"unique_id"`
		FileType          string `json:"file_type"`
	}
	if err := json.Unmarshal(params, &in); err != nil {
		return nil, fmt.Errorf("params: %w", err)
	}

	switch action {
	case "send":
		if err := requireFields("send", in.ServiceURL, in.ConversationID, in.Text); err != nil {
			return nil, err
		}
		return c.SendMessage(ctx, in.ServiceURL, in.ConversationID, in.Text)
	case "reply":
		if err := requireFields("reply", in.ServiceURL, in.ConversationID, in.Text); err != nil {
			return nil, err
		}
		return c.Reply(ctx, in.ServiceURL, in.ConversationID, in.ReplyToActivityID, in.Text)
	case "create_conversation":
		if err := requireFields("create_conversation", in.ServiceURL, in.UserID, in.Text); err != nil {
			return nil, err
		}
		return c.CreateConversation(ctx, in.ServiceURL, in.TenantID, in.UserID, in.Text)
	case "download_attachment":
		if err := requireFields("download_attachment", in.URL); err != nil {
			return nil, err
		}
		return DownloadAttachmentToSandbox(ctx, c, in.URL, in.Name, target.Workdir(ctx))
	case "send_file":
		if err := requireFields("send_file", in.ServiceURL, in.ConversationID, in.Path); err != nil {
			return nil, err
		}
		return RequestFileConsent(ctx, c, in.ServiceURL, in.ConversationID, in.Path, in.Description, target.Workdir(ctx))
	case "upload_file":
		if err := requireFields("upload_file", in.UploadURL, in.Path); err != nil {
			return nil, err
		}
		return UploadConsentedFile(ctx, c, UploadInput{
			UploadURL:      in.UploadURL,
			Path:           in.Path,
			ServiceURL:     in.ServiceURL,
			ConversationID: in.ConversationID,
			ContentURL:     in.ContentURL,
			UniqueID:       in.UniqueID,
			FileType:       in.FileType,
			Name:           in.Name,
		}, target.Workdir(ctx))
	default:
		return nil, fmt.Errorf("unknown action %q", strings.TrimSpace(action))
	}
}

func requireFields(action string, vals ...string) error {
	for _, v := range vals {
		if strings.TrimSpace(v) == "" {
			return fmt.Errorf("teams %s: required field missing", action)
		}
	}
	return nil
}

func (System) PromptDoc() string {
	return `Available Microsoft Teams actions:
   send {"service_url":"...","conversation_id":"...","text":"..."} — a message into an existing conversation.
   reply {"service_url":"...","conversation_id":"...","reply_to_activity_id":"...","text":"..."} — a reply to a message (without reply_to_activity_id → send).
   create_conversation {"service_url":"...","tenant_id":"...","user_id":"...","text":"..."} — a proactive 1:1 chat with a user.
   download_attachment {"url":"...","name":"..."} — loads a file attachment of the message into the sandbox (under attachments/); then look at it with the read tool. url/name are in the task body.
   send_file {"service_url":"...","conversation_id":"...","path":"report.pdf","description":"..."} — asks the recipient whether they want to accept the file (a consent card). path points into your working directory. Afterwards you end with blocked; the recipient's click wakes you with the upload URL.
   upload_file {"upload_url":"...","path":"report.pdf", ...} — pushes the bytes up after consent has been given. The complete call stands ready in the task body; the upload URL is short-lived, so run it immediately.
   Sending files only works in two steps — without the recipient's consent there is no upload URL; that is a Teams requirement and cannot be circumvented.
   service_url and conversation_id come from the triggering message (they are in the task body).
   Correlation key for status blocked: teams:conversation:<conversation_id>.`
}
