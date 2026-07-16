package zammad

import (
	"os"
	"strings"
)

// Betriebs-Konfiguration des Zammad-Plugins aus ENV (12-Factor, wie die
// Webhook-Secrets in internal/config). Alles hat sichere Defaults, sodass ein
// nicht gesetztes Feld das bisherige Verhalten beibehält.
//
// Warum ENV und nicht DB: Der Built-in-SecretStore und die Webhook-Secrets
// laufen bereits über ENV, und die Control Plane ist im MVP single-node. Eine
// per-Org-Konfiguration in der DB (mehrere Support-Queues auf mehreren Agenten)
// ist der nächste Schritt — siehe docs/betrieb-zammad.md, Abschnitt „Ausblick".

// intakeGroups liefert die Allowlist der Zammad-Gruppen (Queues), aus denen
// Tickets überhaupt eine Aufgabe auslösen dürfen. Format:
//
//	COVEY_ZAMMAD_INTAKE_GROUPS="Support L1, Beschwerden"
//
// Leer/ungesetzt → keine Einschränkung (alle Gruppen). Vergleich case-insensitiv,
// führende/abschließende Leerzeichen werden ignoriert.
func intakeGroups() map[string]bool {
	return parseSet(os.Getenv("COVEY_ZAMMAD_INTAKE_GROUPS"))
}

// externalReplyType bestimmt den Zammad-Artikel-Typ für kundensichtbare
// Antworten (internal=false). Default "email" — die Antwort geht per Mail an
// den Kunden. Für Web-/Chat-basierte Instanzen via
//
//	COVEY_ZAMMAD_REPLY_TYPE=web
//
// überschreibbar. Interne Notizen (internal=true) sind immer type "note".
func externalReplyType() string {
	if t := strings.TrimSpace(os.Getenv("COVEY_ZAMMAD_REPLY_TYPE")); t != "" {
		return t
	}
	return "email"
}

// parseSet zerlegt eine kommaseparierte ENV-Liste in ein Set kleingeschriebener,
// getrimmter Werte. Leere Einträge werden verworfen.
func parseSet(raw string) map[string]bool {
	out := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		v := strings.ToLower(strings.TrimSpace(part))
		if v != "" {
			out[v] = true
		}
	}
	return out
}
