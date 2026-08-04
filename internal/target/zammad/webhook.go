package zammad

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// WebhookPayload is the relevant excerpt of the Zammad webhook JSON.
type WebhookPayload struct {
	Ticket struct {
		ID         int    `json:"id"`
		Number     string `json:"number"`
		Title      string `json:"title"`
		State      string `json:"state"`
		Group      string `json:"group"`
		ArticleIDs []int  `json:"article_ids"`
	} `json:"ticket"`
	Article struct {
		ID       int    `json:"id"`
		From     string `json:"from"`
		Subject  string `json:"subject"`
		Body     string `json:"body"`
		Sender   string `json:"sender"` // "Customer" | "Agent" | "System"
		Internal bool   `json:"internal"`
	} `json:"article"`
}

// VerifySignature checks the HMAC-SHA1 signature from the X-Hub-Signature
// header ("sha1=<hex>"). An empty secret = check disabled (dev).
func VerifySignature(secret string, body []byte, header string) bool {
	if secret == "" {
		return true
	}
	sig, ok := strings.CutPrefix(header, "sha1=")
	if !ok {
		return false
	}
	mac := hmac.New(sha1.New, []byte(secret))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(want), []byte(sig))
}

func ParseWebhook(body []byte) (WebhookPayload, error) {
	var p WebhookPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return p, fmt.Errorf("webhook payload: %w", err)
	}
	if p.Ticket.ID == 0 {
		return p, fmt.Errorf("webhook payload: ticket.id missing")
	}
	return p, nil
}

// CorrelationKey is the stable, natural correlation key for Zammad: the ticket
// id that comes with every webhook (spec/13, decision D1).
func CorrelationKey(ticketID int) string {
	return fmt.Sprintf("zammad:ticket:%d", ticketID)
}

// DedupKey makes the webhook processing idempotent — Zammad retries deliveries
// up to 4 times; the same ticket+article pair may only trigger one wake.
func (p WebhookPayload) DedupKey() string {
	return fmt.Sprintf("zammad:%d:%d:%s", p.Ticket.ID, p.Article.ID, p.Ticket.State)
}

// IsCustomerMessage: only customer articles wake blocked agents — the agent's
// own reply (Sender=Agent) must not create a wake cycle.
func (p WebhookPayload) IsCustomerMessage() bool {
	return strings.EqualFold(p.Article.Sender, "Customer") && !p.Article.Internal
}

// InIntakeScope checks the configurable intake filter: if a group allowlist
// (COVEY_ZAMMAD_INTAKE_GROUPS) is set, only a ticket from one of those groups
// (queues) is taken up. Without an allowlist: all groups.
func (p WebhookPayload) InIntakeScope() bool {
	groups := intakeGroups()
	if len(groups) == 0 {
		return true
	}
	return groups[strings.ToLower(strings.TrimSpace(p.Ticket.Group))]
}

// ShouldWake is the complete intake decision: a customer message from an
// admitted group. Only then does a task arise or a blocked task get woken
// (orchestrator.HandleWebhook gates on this flag).
func (p WebhookPayload) ShouldWake() bool {
	return p.IsCustomerMessage() && p.InIntakeScope()
}
