package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"covey/internal/agents"
	"covey/internal/claudeapi"
)

// KI-Assistent zum Anpassen von Agenten („Config-Copilot", FR-001).
//
// Idee: Sobald in der Organisation ein org-weites Claude-Credential hinterlegt
// ist (dasselbe, das die Agenten-Runtime speist), kann die Control Plane es
// serverseitig wiederverwenden, um einen Menschen beim Formulieren der
// Agenten-Config (SOUL.md & Co.) zu unterstützen — ein LLM, das beim
// Konfigurieren der anderen LLMs hilft.
//
// Leitplanken: Das Credential verlässt die Control Plane nie (kein Browser,
// keine Sandbox). Der Assistent committet nichts selbst — er liefert
// Vorschläge, die der Mensch als Diff reviewt und bewusst speichert
// (Config-as-Code bleibt gewahrt).

// assistModel: festes Modell des Config-Copilots — das jeweils aktuellste Opus.
const assistModel = "claude-opus-4-8"

// assistMaxTokens begrenzt die Antwort; nicht-gestreamt, also unter dem
// SDK-Timeout-Bereich (~16k) bleiben.
const assistMaxTokens = 8000

// anthropicBaseURL wird aus credcheck.go geteilt (Variable, damit Tests einen
// httptest-Server unterschieben können).

// assistEditableFiles sind die Config-Dateien, die der Assistent vorschlagen
// darf — alle vom Editor verwalteten Dateien. ACCESS.md und EGRESS.md sind
// strukturierte Text-Sichten auf die Tools-/Egress-Stores; ihr Speichern läuft
// über einen Write-Through (kann die Security-Rolle erfordern). TOOLS.md ist
// Legacy und in ACCESS.md aufgegangen — daher bewusst nicht dabei.
var assistEditableFiles = map[string]bool{
	"SOUL.md":         true,
	"CAPABILITIES.md": true,
	"PLAYBOOKS.md":    true,
	"ORG.md":          true,
	"HEARTBEAT.md":    true,
	"ACCESS.md":       true,
	"EGRESS.md":       true,
}

// assistFileOrder ist die Reihenfolge, in der Dateien dem Modell als Kontext
// gezeigt werden.
var assistFileOrder = []string{
	"SOUL.md", "CAPABILITIES.md", "PLAYBOOKS.md", "ORG.md",
	"HEARTBEAT.md", "ACCESS.md", "EGRESS.md",
}

// assistMessage ist ein Turn im Dialog Mensch↔Assistent. Content ist ein
// einfacher String — die Messages-API akzeptiert das als einzelnen Text-Block.
type assistMessage = claudeapi.Message

// assistProposal ist ein vorgeschlagener neuer Inhalt für eine Config-Datei.
type assistProposal struct {
	File    string `json:"file"`
	Content string `json:"content"`
}

// assistResponse ist die Antwort an die UI: Prosa-Nachricht plus optionale
// Datei-Vorschläge, die der Mensch als Diff übernehmen kann.
type assistResponse struct {
	Reply     string           `json:"reply"`
	Proposals []assistProposal `json:"proposals"`
}

// resolveOrgClaude findet das org-weite Claude-Credential (Reihenfolge und
// Schlüsselnamen liegen in claudeapi, damit Copilot und Traum dasselbe sehen).
func (s *Server) resolveOrgClaude(ctx context.Context, orgID uuid.UUID) (cred string, oauth, ok bool) {
	return claudeapi.ResolveOrg(ctx, s.Secrets, orgID)
}

// handleAssistStatus meldet der UI, ob der Config-Copilot verfügbar ist — also
// ob ein org-weites Claude-Credential auflösbar ist. Ohne eines wird die
// Funktion in der UI gar nicht angeboten (keine tote UI, keine Kosten).
func (s *Server) handleAssistStatus(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	_, _, ok := s.resolveOrgClaude(r.Context(), p.OrgID)
	writeJSON(w, http.StatusOK, map[string]bool{"available": ok})
}

// handleConfigAssist ist der Dialog-Endpunkt: nimmt den Gesprächsverlauf plus
// den aktuellen (ggf. ungespeicherten) Config-Draft, stellt den Plattform-
// Kontext zusammen, befragt Claude und liefert Nachricht + Datei-Vorschläge.
func (s *Server) handleConfigAssist(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	p := principalFrom(r)
	cred, oauth, ok := s.resolveOrgClaude(r.Context(), p.OrgID)
	if !ok {
		writeErr(w, http.StatusPreconditionFailed,
			"kein org-weites Claude-Credential hinterlegt — der KI-Assistent ist nicht verfügbar")
		return
	}
	var in struct {
		Messages []assistMessage   `json:"messages"`
		Files    map[string]string `json:"files"`
	}
	if err := readJSON(r, &in); err != nil || len(in.Messages) == 0 {
		writeErr(w, http.StatusBadRequest, "messages fehlt")
		return
	}
	a, err := s.Registry.Get(r.Context(), id)
	if err != nil {
		mapErr(w, err)
		return
	}
	system := s.buildAssistSystem(r.Context(), p.OrgID, a, in.Files)

	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	raw, err := claudeapi.Messages(ctx, cred, oauth,
		claudeapi.Call{Model: assistModel, MaxTokens: assistMaxTokens}, system, in.Messages)
	if err != nil {
		s.Log.Error("config-assist", "agent", id, "err", err)
		writeErr(w, http.StatusBadGateway, "KI-Assistent nicht erreichbar: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, parseAssistReply(raw))
}

// buildAssistSystem baut den System-Prompt: Rolle, Plattform-Protokoll,
// angebundene Zielsysteme, geltende Guard-Rails, aktueller Config-Stand und
// der JSON-Antwort-Vertrag. So schlägt der Assistent nur Verhalten vor, das die
// Plattform auch zulässt.
func (s *Server) buildAssistSystem(ctx context.Context, orgID uuid.UUID, a agents.Agent, files map[string]string) string {
	var b strings.Builder
	b.WriteString(`Du bist der Config-Assistent der Covey-Plattform: Du hilfst einem Menschen (dem Agent-Owner) dabei, die Konfiguration eines KI-Agenten zu schreiben und zu überarbeiten. Antworte auf Deutsch, nüchtern und präzise.

Ein Agent auf Covey wird über Markdown-Dateien konfiguriert (Config-as-Code). Du darfst alle diese Dateien vorschlagen:
- SOUL.md — Charakter, Rolle, Ton, Grundhaltung des Agenten (das Herzstück).
- CAPABILITIES.md — konkrete Fähigkeiten und was der Agent tun darf/soll.
- PLAYBOOKS.md — wiederkehrende Abläufe Schritt für Schritt.
- ORG.md — Einbettung in die Organisation, an wen eskaliert wird.
- HEARTBEAT.md — periodische „Was liegt an?"-Impulse (optional).
- ACCESS.md — welche Zielsysteme der Agent nutzen darf (Broker-Scopes, Tool-Allowlist). Strukturierte Text-Sicht auf den Tools-Store; behalte exakt das Format des aktuellen Stands bei (Kommentarzeilen mit '#', dann Einträge). Änderungen wirken auf die Broker-Zugänge und können beim Speichern die Security-Rolle erfordern.
- EGRESS.md — welche Hosts der Agent ausgehend erreichen darf (Templates + eigene Hosts). Strukturierte Text-Sicht auf den Egress-Store; ebenfalls exakt das bestehende Format beibehalten; Speichern kann die Security-Rolle erfordern.

Deine Aufgabe: Aus der Beschreibung des Menschen konkrete, gut formulierte Config-Inhalte entwerfen oder bestehende gezielt überarbeiten. Schlage nur Verhalten vor, das die Plattform auch zulässt (keine Zielsysteme/Aktionen erfinden, die unten nicht aufgeführt sind; geltende Guard-Rails respektieren). Bei ACCESS.md/EGRESS.md nur Systeme/Templates verwenden, die real existieren (siehe Kontext unten).

`)
	b.WriteString("## Plattform-Protokoll (gilt für jeden Agenten)\n")
	b.WriteString(agents.ProtocolInstructions)
	b.WriteString("\n\n")

	b.WriteString("## Dieser Agent\n")
	b.WriteString("Anzeigename: " + a.DisplayName + "\n")
	if a.JobTitle != "" {
		b.WriteString("Funktion: " + a.JobTitle + "\n")
	}
	b.WriteString("Runtime: " + a.Runtime + "\n\n")

	if docs, err := s.Targets.EnabledDocsForAgent(ctx, orgID, a.ID); err == nil && len(docs) > 0 {
		b.WriteString("## Connected target systems (available actions)\n")
		for _, d := range docs {
			b.WriteString(d)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	} else {
		b.WriteString("## Connected target systems\nNone — the agent currently has no target systems connected.\n\n")
	}

	if rules, err := s.Rails.List(ctx, orgID); err == nil {
		var active []string
		for _, ru := range rules {
			if !ru.Enabled {
				continue
			}
			if ru.ScopeLevel == "agent" && (ru.AgentID == nil || *ru.AgentID != a.ID) {
				continue // agent-gescopte Regel eines anderen Agenten
			}
			active = append(active, "- "+ru.RuleType+": "+ru.Pattern)
		}
		if len(active) > 0 {
			b.WriteString("## Geltende Guard-Rails (plattform-erzwungen)\n")
			b.WriteString(strings.Join(active, "\n"))
			b.WriteString("\n\n")
		}
	}

	b.WriteString("## Aktueller Config-Stand\n")
	wroteAny := false
	for _, name := range assistFileOrder {
		content := strings.TrimSpace(files[name])
		if content == "" {
			continue
		}
		wroteAny = true
		b.WriteString("### " + name + "\n```\n" + content + "\n```\n")
	}
	if !wroteAny {
		b.WriteString("Alle Config-Dateien sind noch leer — der Agent wird gerade neu aufgesetzt.\n")
	}
	b.WriteString("\n")

	b.WriteString(`## Antwortformat (WICHTIG)
Antworte AUSSCHLIESSLICH mit einem einzigen JSON-Objekt, ohne Markdown-Codefence, ohne Text davor oder danach:
{"reply": "<deine Nachricht an den Menschen>", "proposals": [{"file": "SOUL.md", "content": "<vollständiger neuer Dateiinhalt>"}]}

Regeln:
- "reply" ist deine kurze Prosa-Antwort (was du vorschlägst und warum).
- "proposals" enthält je einen Eintrag pro Datei, die du ändern willst, mit dem VOLLSTÄNDIGEN neuen Inhalt (nicht nur ein Diff). Leeres Array, wenn du nur erklärst/nachfragst und (noch) nichts änderst.
- Erlaubte Dateinamen: SOUL.md, CAPABILITIES.md, PLAYBOOKS.md, ORG.md, HEARTBEAT.md, ACCESS.md, EGRESS.md.
- Bei ACCESS.md/EGRESS.md das exakte Format des aktuellen Stands beibehalten (Kommentarzeilen, Einträge) und nur real existierende Systeme/Templates verwenden.
- Schlage nichts vor, was die Plattform nicht kann.`)
	return b.String()
}

// parseAssistReply zieht das JSON-Objekt aus der Modell-Antwort. Fällt das
// Parsen fehl (das Modell hat doch Prosa geliefert), wird der ganze Text als
// Nachricht ohne Vorschläge zurückgegeben — nie ein harter Fehler.
func parseAssistReply(raw string) assistResponse {
	txt := strings.TrimSpace(raw)
	// Etwaigen ```json … ```-Fence entfernen.
	if strings.HasPrefix(txt, "```") {
		if i := strings.IndexByte(txt, '\n'); i >= 0 {
			txt = txt[i+1:]
		}
		txt = strings.TrimSuffix(strings.TrimSpace(txt), "```")
		txt = strings.TrimSpace(txt)
	}
	// Auf das erste '{' … letzte '}' eingrenzen (falls Text drumherum steht).
	if i := strings.IndexByte(txt, '{'); i > 0 {
		txt = txt[i:]
	}
	if j := strings.LastIndexByte(txt, '}'); j >= 0 && j < len(txt)-1 {
		txt = txt[:j+1]
	}

	var out assistResponse
	if err := json.Unmarshal([]byte(txt), &out); err != nil || strings.TrimSpace(out.Reply) == "" {
		return assistResponse{Reply: strings.TrimSpace(raw), Proposals: nil}
	}
	// Vorschläge auf die editierbaren Dateien filtern.
	filtered := out.Proposals[:0]
	for _, pr := range out.Proposals {
		if assistEditableFiles[pr.File] {
			filtered = append(filtered, pr)
		}
	}
	out.Proposals = filtered
	return out
}
