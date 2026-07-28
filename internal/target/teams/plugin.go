package teams

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"covey/internal/target"
)

// System bindet Microsoft Teams als Zielsystem-Plugin an die target-Registry:
// Webhook-Eingang (Bot-Framework-JWT, Idempotenz, Korrelation), die
// Agent-Aktionen und die Aktions-Doku für den System-Prompt (spec/15).
type System struct{}

func init() {
	target.Register(target.Descriptor{
		Name:        "teams",
		Label:       "Microsoft Teams",
		Description: "Chat-Kanal über den Azure Bot Service (spec/15): Nachrichten empfangen (Messaging-Endpoint, JWT-verifiziert) und senden (Bot Connector). Auth per OAuth2 (Secrets teams_token = appId:appPassword + optional teams_url).",
		Kind:        "builtin",
		Category:    target.CategoryComms,
		System:      System{},
		SetupDoc: `1. In Azure eine Bot-Registration (Azure Bot / Bot Channels Registration)
   anlegen und den Kanal "Microsoft Teams" aktivieren. Microsoft-App-ID
   notieren und ein Client Secret (App-Passwort) erzeugen.

2. Messaging-Endpoint der Bot-Registration setzen auf:
   {public_url}/api/webhooks/teams/<agent-slug>

3. Unter Secrets hinterlegen und dem Agenten zuweisen:
   teams_token = <app-id>:<app-passwort>
   teams_url   = (optional) Token-Endpoint, Default
                 https://login.microsoftonline.com/botframework.com/oauth2/v2.0/token

4. In der ACCESS.md des Agenten freischalten:
   - system: teams scope: read,write

5. Prozess-Env für die Webhook-Verifikation:
   COVEY_TEAMS_WEBHOOK_SECRET=<app-id>   (die Bot-App-ID = erwartete JWT-Audience;
                                          leer = Verifikation aus, nur für Tests)

6. Optionale Prozess-Env:
   COVEY_TEAMS_INTAKE_TENANTS="<tenant-id>"   (leer = alle Tenants)

7. Ein Teams-App-Manifest mit der App-ID als bot-id in Teams hochladen/seitwärts
   laden, damit Nutzer den Agenten anschreiben können.

Details: docs/betrieb-teams.md im Repository.`,
	})
}

func (System) Name() string { return "teams" }

// VerifyWebhook validiert das Bot-Framework-JWT. Das "secret" ist hier die
// erwartete Bot-App-ID (JWT-Audience), gesetzt via COVEY_TEAMS_WEBHOOK_SECRET;
// leer = Verifikation deaktiviert (Dev/faketeams).
func (System) VerifyWebhook(secret string, body []byte, header http.Header) bool {
	return VerifyToken(secret, header.Get("Authorization"))
}

func (System) ParseWebhook(body []byte) (target.WebhookEvent, error) {
	a, err := ParseWebhook(body)
	if err != nil {
		return target.WebhookEvent{}, err
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
		text = "(kein Text)"
	}

	replyHint := fmt.Sprintf(
		"reply {\"service_url\":%q,\"conversation_id\":%q,\"reply_to_activity_id\":%q,\"text\":\"…\"}",
		a.ServiceURL, a.Conversation.ID, a.ID)

	attachSection := attachmentSection(a.Files())

	return target.WebhookEvent{
		DedupKey:       a.DedupKey(),
		CorrelationKey: CorrelationKey(a.Conversation.ID),
		Title:          fmt.Sprintf("Teams-Nachricht von %s", from),
		TaskBody: fmt.Sprintf(
			"Neue Microsoft-Teams-Nachricht von %s (%s).\n\nNachricht:\n%s%s\n\nAntworte über den Action-Proxy (system teams):\n%s",
			from, convKind, text, attachSection, replyHint),
		ResumeInput: fmt.Sprintf("Antwort von %s in Teams:\n%s%s", from, text, attachSection),
		Wake:        a.ShouldWake(),
	}, nil
}

// attachmentSection formatiert die Datei-Anhänge als Text-Block für den
// Aufgaben-Body inkl. des fertigen download_attachment-Aufrufs je Anhang. Die
// Download-URLs sind kurzlebig — der Agent sollte sie zeitnah laden.
func attachmentSection(files []Attachment) string {
	if len(files) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n\nAnhänge (%d) — lade sie mit download_attachment in die Sandbox und lies sie mit dem Read-Tool (URLs sind kurzlebig, zeitnah laden):", len(files))
	for i, at := range files {
		fmt.Fprintf(&b, "\n  %d. %s (%s)\n     download_attachment {\"url\":%q,\"name\":%q}",
			i+1, at.Filename(), at.ContentType, at.DownloadURL(), at.Filename())
	}
	return b.String()
}

// ActionSubject: jede Aktion ist ein eigenes, separat regelbares
// Guard-Rail-Subjekt (teams:send, teams:reply, teams:create_conversation).
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
	default:
		return nil, fmt.Errorf("unbekannte aktion %q", strings.TrimSpace(action))
	}
}

func requireFields(action string, vals ...string) error {
	for _, v := range vals {
		if strings.TrimSpace(v) == "" {
			return fmt.Errorf("teams %s: pflichtfeld fehlt", action)
		}
	}
	return nil
}

func (System) PromptDoc() string {
	return `Verfügbare Microsoft-Teams-Aktionen:
   send {"service_url":"...","conversation_id":"...","text":"..."} — Nachricht in eine bestehende Konversation.
   reply {"service_url":"...","conversation_id":"...","reply_to_activity_id":"...","text":"..."} — Antwort auf eine Nachricht (ohne reply_to_activity_id → send).
   create_conversation {"service_url":"...","tenant_id":"...","user_id":"...","text":"..."} — proaktiver 1:1-Chat mit einem Nutzer.
   download_attachment {"url":"...","name":"..."} — lädt einen Datei-Anhang der Nachricht in die Sandbox (unter attachments/); danach mit dem Read-Tool ansehen. url/name stehen im Task-Body.
   service_url und conversation_id stammen aus der auslösenden Nachricht (steht im Task-Body).
   Korrelations-Key für Status blocked: teams:conversation:<conversation_id>.`
}
