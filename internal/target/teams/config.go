package teams

import (
	"os"
	"strconv"
	"strings"
)

// Betriebs-Konfiguration des Teams-Plugins aus ENV (12-Factor, wie die
// Webhook-Secrets in internal/config und das Zammad-Plugin). Alles hat sichere
// Defaults, sodass ein nicht gesetztes Feld das bisherige Verhalten beibehält.

// defaultTokenEndpoint ist der OAuth2-Token-Endpoint für den Bot-Connector-
// Zugriff (client_credentials). Default ist der Multi-Tenant-Bot-Framework-
// Endpoint; über das gebrokerte teams_url pro Agent überschreibbar (z. B. ein
// tenant-spezifischer Endpoint für Single-Tenant-Bots).
func defaultTokenEndpoint() string {
	return "https://login.microsoftonline.com/botframework.com/oauth2/v2.0/token"
}

// connectorScope ist der OAuth2-Scope, den der Bot Connector erwartet.
const connectorScope = "https://api.botframework.com/.default"

// intakeTenants liefert die Allowlist der Microsoft-365-Tenant-IDs, aus denen
// Nachrichten überhaupt eine Aufgabe auslösen dürfen. Format:
//
//	COVEY_TEAMS_INTAKE_TENANTS="11111111-2222-3333-4444-555555555555, …"
//
// Leer/ungesetzt → keine Einschränkung (alle Tenants). Vergleich
// case-insensitiv, führende/abschließende Leerzeichen werden ignoriert.
func intakeTenants() map[string]bool {
	return parseSet(os.Getenv("COVEY_TEAMS_INTAKE_TENANTS"))
}

// maxAttachmentBytes ist die Obergrenze für einen einzelnen, in die Sandbox
// materialisierten Anhang. Default 25 MB, via COVEY_TEAMS_ATTACHMENT_MAX_MB
// überschreibbar (1 bis 1024 MB). Fail-closed: ein unbrauchbarer Wert lässt den
// Default stehen — auch ein absurd großer, der beim Umrechnen in Bytes
// überliefe und die Größenprüfung damit gerade aushebelte.
func maxAttachmentBytes() int64 {
	if v := strings.TrimSpace(os.Getenv("COVEY_TEAMS_ATTACHMENT_MAX_MB")); v != "" {
		if mb, err := strconv.ParseInt(v, 10, 64); err == nil && mb > 0 && mb <= 1024 {
			return mb << 20
		}
	}
	return 25 << 20
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
