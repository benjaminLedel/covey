package zammad

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// WebhookPayload ist der relevante Ausschnitt des Zammad-Webhook-JSON.
type WebhookPayload struct {
	Ticket struct {
		ID         int    `json:"id"`
		Number     string `json:"number"`
		Title      string `json:"title"`
		State      string `json:"state"`
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

// VerifySignature prüft die HMAC-SHA1-Signatur aus dem Header
// X-Hub-Signature ("sha1=<hex>"). Leeres Secret = Prüfung deaktiviert (Dev).
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
		return p, fmt.Errorf("webhook payload: ticket.id fehlt")
	}
	return p, nil
}

// CorrelationKey ist der stabile, natürliche Korrelations-Key für Zammad:
// die Ticket-id, die in jedem Webhook mitkommt (spec/13, Entscheidung D1).
func CorrelationKey(ticketID int) string {
	return fmt.Sprintf("zammad:ticket:%d", ticketID)
}

// DedupKey macht die Webhook-Verarbeitung idempotent — Zammad wiederholt
// Zustellungen bis zu 4×; dasselbe Ticket+Artikel-Paar darf nur einen Wake auslösen.
func (p WebhookPayload) DedupKey() string {
	return fmt.Sprintf("zammad:%d:%d:%s", p.Ticket.ID, p.Article.ID, p.Ticket.State)
}

// IsCustomerMessage: nur Kunden-Artikel wecken geblockte Agenten — die eigene
// Antwort des Agenten (Sender=Agent) darf keinen Wake-Zyklus erzeugen.
func (p WebhookPayload) IsCustomerMessage() bool {
	return strings.EqualFold(p.Article.Sender, "Customer") && !p.Article.Internal
}
