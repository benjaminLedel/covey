package gitlab

import (
	"crypto/hmac"
	"encoding/json"
	"fmt"
	"strings"
)

// WebhookPayload ist der relevante Ausschnitt des GitLab-Webhook-JSON.
// GitLab schickt je nach Ereignis unterschiedliche Formen; hier interessieren
// Issue Hooks (object_kind=issue), Merge-Request-Hooks (object_kind=
// merge_request) und Note Hooks auf beiden (object_kind=note, noteable_type=
// Issue|MergeRequest). Alle tragen project.id + die jeweilige iid — den
// natürlichen Korrelations-Key.
type WebhookPayload struct {
	ObjectKind string `json:"object_kind"` // "issue" | "note" | "merge_request"
	User       struct {
		Username string `json:"username"`
	} `json:"user"`
	Project struct {
		ID                int    `json:"id"`
		PathWithNamespace string `json:"path_with_namespace"`
	} `json:"project"`
	ObjectAttributes struct {
		// Issue- und Merge-Request-Hook
		IID         int    `json:"iid"`
		Title       string `json:"title"`
		Description string `json:"description"`
		State       string `json:"state"`
		Action      string `json:"action"` // "open" | "reopen" | "update" | "close" | "merge"
		UpdatedAt   string `json:"updated_at"`
		// Merge-Request-Hook
		SourceBranch string `json:"source_branch"`
		TargetBranch string `json:"target_branch"`
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
	// Note Hooks auf Merge Requests tragen den MR separat.
	MergeRequest struct {
		IID          int    `json:"iid"`
		Title        string `json:"title"`
		SourceBranch string `json:"source_branch"`
		State        string `json:"state"`
	} `json:"merge_request"`
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
	if p.IsMergeRequestEvent() {
		if p.MRIID() == 0 {
			return p, fmt.Errorf("webhook payload: mr-iid fehlt (object_kind=%s)", p.ObjectKind)
		}
		return p, nil
	}
	if p.IssueIID() == 0 {
		return p, fmt.Errorf("webhook payload: issue-iid fehlt (object_kind=%s)", p.ObjectKind)
	}
	return p, nil
}

// IsMergeRequestEvent: das Ereignis betrifft einen Merge Request — entweder
// der MR-Hook selbst oder ein Kommentar darauf.
func (p WebhookPayload) IsMergeRequestEvent() bool {
	if p.ObjectKind == "merge_request" {
		return true
	}
	return p.ObjectKind == "note" && strings.EqualFold(p.ObjectAttributes.NoteableType, "MergeRequest")
}

// MRIID liefert die MR-IID unabhängig von der Hook-Form: MR-Hooks tragen sie
// in object_attributes, Note Hooks im merge_request-Objekt.
func (p WebhookPayload) MRIID() int {
	if p.ObjectKind == "note" {
		return p.MergeRequest.IID
	}
	return p.ObjectAttributes.IID
}

// MRTitle analog zu MRIID.
func (p WebhookPayload) MRTitle() string {
	if p.ObjectKind == "note" {
		return p.MergeRequest.Title
	}
	return p.ObjectAttributes.Title
}

// MRSourceBranch analog zu MRIID — der Branch, auf dem der Agent bei
// Review-Nacharbeit weiterarbeitet.
func (p WebhookPayload) MRSourceBranch() string {
	if p.ObjectKind == "note" {
		return p.MergeRequest.SourceBranch
	}
	return p.ObjectAttributes.SourceBranch
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

// MRCorrelationKey ist der Korrelations-Key für Merge Requests — darauf
// blockt ein Agent, der nach create_merge_request auf Review wartet.
func MRCorrelationKey(projectID, mrIID int) string {
	return fmt.Sprintf("gitlab:mr:%d:%d", projectID, mrIID)
}

// DedupKey macht die Webhook-Verarbeitung idempotent — GitLab wiederholt
// Zustellungen bei Fehlern. Notes haben eine eindeutige id; Issue- und
// MR-Ereignisse werden über Aktion + Änderungszeitpunkt unterschieden.
func (p WebhookPayload) DedupKey() string {
	switch p.ObjectKind {
	case "note":
		return fmt.Sprintf("gitlab:%d:note:%d", p.Project.ID, p.ObjectAttributes.ID)
	case "merge_request":
		return fmt.Sprintf("gitlab:%d:mr:%d:%s:%s",
			p.Project.ID, p.ObjectAttributes.IID, p.ObjectAttributes.Action, p.ObjectAttributes.UpdatedAt)
	default:
		return fmt.Sprintf("gitlab:%d:issue:%d:%s:%s",
			p.Project.ID, p.ObjectAttributes.IID, p.ObjectAttributes.Action, p.ObjectAttributes.UpdatedAt)
	}
}

// IsWakeEvent: ein neu eröffnetes (oder wiedereröffnetes) Issue, ein
// Kommentar eines fremden Nutzers (auf Issue ODER Merge Request — Letzteres
// ist das Review-Feedback des Entwickler-Workflows) sowie Merge/Close eines
// MR wecken. Der eigene Kommentar des Agenten (COVEY_GITLAB_AGENT_USERNAMES)
// darf keinen Wake-Zyklus erzeugen, und Update-Ereignisse (Label-Änderungen
// etc.) lösen keine Arbeit aus.
func (p WebhookPayload) IsWakeEvent() bool {
	switch p.ObjectKind {
	case "issue":
		return p.ObjectAttributes.Action == "open" || p.ObjectAttributes.Action == "reopen"
	case "merge_request":
		// Nur der Abschluss des Reviews (Merge bzw. Ablehnung) ist relevant —
		// er weckt die darauf geblockte Aufgabe des MR-Autors.
		return p.ObjectAttributes.Action == "merge" || p.ObjectAttributes.Action == "close"
	case "note":
		if !strings.EqualFold(p.ObjectAttributes.NoteableType, "Issue") &&
			!strings.EqualFold(p.ObjectAttributes.NoteableType, "MergeRequest") {
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
