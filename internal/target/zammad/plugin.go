package zammad

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"covey/internal/target"
)

// System bindet Zammad als Zielsystem-Plugin an die target-Registry:
// Webhook-Eingang (HMAC, Idempotenz, Korrelation), die fünf Agent-Aktionen
// und die Aktions-Doku für den System-Prompt.
type System struct{}

func init() {
	target.Register(target.Descriptor{
		Name:        "zammad",
		Label:       "Zammad",
		Description: "Open-Source-Helpdesk (spec/13): Tickets lesen, antworten, Status setzen, eskalieren. Webhook-Wake über Trigger, Auth per API-Token (Secrets zammad_token + zammad_url).",
		Kind:        "builtin",
		System:      System{},
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
		Title:          fmt.Sprintf("Zammad-Ticket #%s: %s", p.Ticket.Number, p.Ticket.Title),
		TaskBody: fmt.Sprintf("Neues Ticket im Zammad (id=%d, nummer=%s).\nTitel: %s\n\nNachricht des Kunden:\n%s\n\nBearbeite das Ticket über den Action-Proxy (system zammad, ticket_id=%d).",
			p.Ticket.ID, p.Ticket.Number, p.Ticket.Title, p.Article.Body, p.Ticket.ID),
		ResumeInput: fmt.Sprintf("Kundenantwort auf Ticket #%d:\n%s", p.Ticket.ID, p.Article.Body),
		Wake:        p.IsCustomerMessage(),
	}, nil
}

// ActionSubject: externe Antworten (internal=false) sind ein eigenes,
// schärfer regelbares Guard-Rail-Subjekt.
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
			return nil, fmt.Errorf("state fehlt")
		}
		return nil, zc.SetState(ctx, in.TicketID, in.State)
	case "escalate":
		note := in.Note
		if note == "" {
			note = "Eskalation durch Covey-Agent."
		}
		return nil, zc.Escalate(ctx, in.TicketID, note)
	default:
		return nil, fmt.Errorf("unbekannte aktion %q", strings.TrimSpace(action))
	}
}

func (System) PromptDoc() string {
	return `Verfügbare Zammad-Aktionen: get_ticket {"ticket_id":N}, list_articles {"ticket_id":N},
   reply {"ticket_id":N,"body":"...","internal":true|false}, set_state {"ticket_id":N,"state":"..."},
   escalate {"ticket_id":N,"note":"..."}.`
}
