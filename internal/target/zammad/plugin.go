package zammad

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"covey/internal/target"
)

// System binds Zammad as a target-system plugin to the target registry:
// webhook inbound (HMAC, idempotency, correlation), the five agent actions and
// the action doc for the system prompt.
type System struct{}

func init() {
	target.Register(target.Descriptor{
		Name:        "zammad",
		Label:       "Zammad",
		Description: "Open-source helpdesk (spec/13): read tickets, reply, set state, escalate. Webhook wake via triggers, auth by API token (secrets zammad_token + zammad_url).",
		Kind:        "builtin",
		Category:    target.CategoryTicketing,
		System:      System{},
		SetupDoc: `1. Create an agent user in Zammad with a least-privilege role (ticket.agent
   for the target groups) and generate an API token as that user
   (enable Token Access under Admin → System → API).

2. Store under Secrets and assign to the agent:
   zammad_url   = https://helpdesk.example.com   (without /api/v1)
   zammad_token = the token from step 1

3. Enable it in the agent's ACCESS.md:
   - system: zammad scope: read,write,comment

4. Create a webhook + trigger in Zammad (Admin → Manage):
   Webhook endpoint: {public_url}/api/webhooks/zammad/<agent-slug>
   HMAC token:       value of COVEY_ZAMMAD_WEBHOOK_SECRET (process env)
   Trigger:          on ticket created/updated + sender customer → webhook

5. Optional process env:
   COVEY_ZAMMAD_INTAKE_GROUPS="Support L1"   (empty = all groups)
   COVEY_ZAMMAD_REPLY_TYPE=email             (web for chat instances)

Details: docs/ops-zammad.md in the repository.`,
	})
}

func (System) Name() string { return "zammad" }

func (System) VerifyWebhook(secret string, body []byte, header http.Header) bool {
	return VerifySignature(secret, body, header.Get("X-Hub-Signature"))
}

func (System) ParseWebhook(body []byte) (target.WebhookEvent, error) {
	p, err := ParseWebhook(body)
	if err != nil {
		return target.WebhookEvent{}, err
	}
	return target.WebhookEvent{
		DedupKey:       p.DedupKey(),
		CorrelationKey: CorrelationKey(p.Ticket.ID),
		Title:          fmt.Sprintf("Zammad ticket #%s: %s", p.Ticket.Number, p.Ticket.Title),
		TaskBody: fmt.Sprintf("New ticket in Zammad (id=%d, number=%s).\nTitle: %s\n\nMessage from the customer:\n%s\n\nWork on the ticket through the action proxy (system zammad, ticket_id=%d).",
			p.Ticket.ID, p.Ticket.Number, p.Ticket.Title, p.Article.Body, p.Ticket.ID),
		ResumeInput: fmt.Sprintf("Customer reply on ticket #%d:\n%s", p.Ticket.ID, p.Article.Body),
		Wake:        p.ShouldWake(),
	}, nil
}

// ActionSubject: external replies (internal=false) are a separate guard-rail
// subject that can be governed more strictly.
func (System) ActionSubject(action string, params json.RawMessage) string {
	if action == "reply" {
		var p struct {
			Internal *bool `json:"internal"`
		}
		json.Unmarshal(params, &p)
		if p.Internal != nil && !*p.Internal {
			return "zammad:reply_external"
		}
		return "zammad:reply_internal"
	}
	return "zammad:" + action
}

func (System) Execute(ctx context.Context, action string, params json.RawMessage, cred target.Credential) (any, error) {
	zc := NewClient(cred.BaseURL, cred.Token)

	var in struct {
		TicketID int    `json:"ticket_id"`
		Body     string `json:"body"`
		Internal *bool  `json:"internal"`
		State    string `json:"state"`
		Note     string `json:"note"`
	}
	if err := json.Unmarshal(params, &in); err != nil {
		return nil, fmt.Errorf("params: %w", err)
	}

	switch action {
	case "get_ticket":
		return zc.GetTicket(ctx, in.TicketID)
	case "list_articles":
		return zc.ListArticles(ctx, in.TicketID)
	case "reply":
		internal := in.Internal == nil || *in.Internal
		return zc.Reply(ctx, in.TicketID, in.Body, internal)
	case "set_state":
		if in.State == "" {
			return nil, fmt.Errorf("state missing")
		}
		return nil, zc.SetState(ctx, in.TicketID, in.State)
	case "escalate":
		note := in.Note
		if note == "" {
			note = "Escalated by a Covey agent."
		}
		return nil, zc.Escalate(ctx, in.TicketID, note)
	default:
		return nil, fmt.Errorf("unknown action %q", strings.TrimSpace(action))
	}
}

func (System) PromptDoc() string {
	return `Available Zammad actions: get_ticket {"ticket_id":N}, list_articles {"ticket_id":N},
   reply {"ticket_id":N,"body":"...","internal":true|false}, set_state {"ticket_id":N,"state":"..."},
   escalate {"ticket_id":N,"note":"..."}.
   Correlation key for status blocked: zammad:ticket:<ticket_id>.`
}
