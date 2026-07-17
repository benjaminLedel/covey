package gitlab

import (
	"crypto/hmac"
	"encoding/json"
	"fmt"
	"strings"
)

// WebhookPayload ist der relevante Ausschnitt des GitLab-Webhook-JSON.
// GitLab schickt je nach Ereignis unterschiedliche Formen; hier interessieren
// Issue Hooks (object_kind=issue) und Note Hooks auf Issues (object_kind=note,
// noteable_type=Issue). Beide tragen project.id + issue-iid — den natürlichen
// Korrelations-Key.
type WebhookPayload struct {
	ObjectKind string `json:"object_kind"` // "issue" | "note"
	User       struct {
		Username string `json:"username"`
	} `json:"user"`
	Project struct {
		ID                int    `json:"id"`
		PathWithNamespace string `json:"path_with_namespace"`
	} `json:"project"`
	ObjectAttributes struct {
		// Issue Hook
		IID         int    `json:"iid"`
		Title       string `json:"title"`
		Description string `json:"description"`
		State       string `json:"state"`
		Action      string `json:"action"` // "open" | "reopen" | "update" | "close"
		UpdatedAt   string `json:"updated_at"`
		// Note Hook
		ID           int    `json:"id"`
		Note         string `json:"note"`
		NoteableType string `json:"noteable_type"` // "Issue" | "MergeRequest" | …
	} `json:"object_attributes"`
	// Note Hooks tragen das zugehörige Issue separat.
	Issue struct {
		IID   int    `json:"iid"`
		Title string `json:"title"`
	} `json:"issue"`
}

// VerifyToken prüft das Webhook-Secret aus dem Header X-Gitlab-Token —
// GitLab signiert nicht (kein HMAC), sondern schickt das konfigurierte Secret
// im Klartext mit; Vergleich in konstanter Zeit. Leeres Secret = Prüfung
// deaktiviert (Dev).
func VerifyToken(secret, header string) bool {
	if secret == "" {
		return true
	}
	return hmac.Equal([]byte(secret), []byte(header))
}

func ParseWebhook(body []byte) (WebhookPayload, error) {
	var p WebhookPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return p, fmt.Errorf("webhook payload: %w", err)
	}
	if p.Project.ID == 0 {
		return p, fmt.Errorf("webhook payload: project.id fehlt")
	}
	if p.IssueIID() == 0 {
		return p, fmt.Errorf("webhook payload: issue-iid fehlt (object_kind=%s)", p.ObjectKind)
	}
	return p, nil
}

// IssueIID liefert die Issue-IID unabhängig von der Hook-Form: Issue Hooks
// tragen sie in object_attributes, Note Hooks im issue-Objekt.
func (p WebhookPayload) IssueIID() int {
	if p.ObjectKind == "note" {
		return p.Issue.IID
	}
	return p.ObjectAttributes.IID
}

// IssueTitle analog zu IssueIID.
func (p WebhookPayload) IssueTitle() string {
	if p.ObjectKind == "note" {
		return p.Issue.Title
	}
	return p.ObjectAttributes.Title
}

// CorrelationKey ist der stabile, natürliche Korrelations-Key für GitLab:
// Projekt-id + Issue-iid, in jeder Hook-Form vorhanden (analog spec/13, D1).
func CorrelationKey(projectID, issueIID int) string {
	return fmt.Sprintf("gitlab:issue:%d:%d", projectID, issueIID)
}

// DedupKey macht die Webhook-Verarbeitung idempotent — GitLab wiederholt
// Zustellungen bei Fehlern. Notes haben eine eindeutige id; Issue-Ereignisse
// werden über Aktion + Änderungszeitpunkt unterschieden.
func (p WebhookPayload) DedupKey() string {
	if p.ObjectKind == "note" {
		return fmt.Sprintf("gitlab:%d:note:%d", p.Project.ID, p.ObjectAttributes.ID)
	}
	return fmt.Sprintf("gitlab:%d:issue:%d:%s:%s",
		p.Project.ID, p.ObjectAttributes.IID, p.ObjectAttributes.Action, p.ObjectAttributes.UpdatedAt)
}

// IsWakeEvent: nur ein neu eröffnetes (oder wiedereröffnetes) Issue bzw. ein
// Issue-Kommentar eines fremden Nutzers weckt — der eigene Kommentar des
// Agenten (COVEY_GITLAB_AGENT_USERNAMES) darf keinen Wake-Zyklus erzeugen,
// und Update-/Close-Ereignisse (Label-Änderungen etc.) lösen keine Arbeit aus.
func (p WebhookPayload) IsWakeEvent() bool {
	switch p.ObjectKind {
	case "issue":
		return p.ObjectAttributes.Action == "open" || p.ObjectAttributes.Action == "reopen"
	case "note":
		if !strings.EqualFold(p.ObjectAttributes.NoteableType, "Issue") {
			return false
		}
		return !agentUsernames()[strings.ToLower(strings.TrimSpace(p.User.Username))]
	default:
		return false
	}
}

// InIntakeScope prüft den konfigurierbaren Intake-Filter: Ist eine
// Projekt-Allowlist (COVEY_GITLAB_INTAKE_PROJECTS) gesetzt, wird nur ein Issue
// aus einem dieser Projekte (Pfad oder numerische id) aufgenommen. Ohne
// Allowlist: alle Projekte.
func (p WebhookPayload) InIntakeScope() bool {
	return projectInScope(p.Project.ID, p.Project.PathWithNamespace)
}

// ShouldWake ist die vollständige Aufnahme-Entscheidung: ein Wake-Ereignis
// aus einem zugelassenen Projekt. Nur dann entsteht eine Aufgabe bzw. wird
// eine geblockte Aufgabe geweckt (orchestrator.HandleWebhook gated darauf).
func (p WebhookPayload) ShouldWake() bool {
	return p.IsWakeEvent() && p.InIntakeScope()
}
