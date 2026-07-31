package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"covey/internal/agents"
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
type assistMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

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

// resolveOrgClaude findet das org-weite Claude-Credential. Rückgabe:
// (Credential, oauth?, gefunden?). API-Key hat Vorrang vor Abo-OAuth-Token.
func (s *Server) resolveOrgClaude(ctx context.Context, orgID uuid.UUID) (cred string, oauth, ok bool) {
	if v, err := s.Secrets.Get(ctx, orgID, "anthropic_api_key"); err == nil {
		if v = strings.TrimSpace(v); v != "" {
			return v, false, true
		}
	}
	if v, err := s.Secrets.Get(ctx, orgID, "claude_code_oauth_token"); err == nil {
		if v = strings.TrimSpace(v); v != "" {
			return v, true, true
		}
	}
	return "", false, false
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
	raw, err := callAnthropicMessages(ctx, cred, oauth,
		anthCall{Model: assistModel, MaxTokens: assistMaxTokens}, system, in.Messages)
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
		b.WriteString("## Angebundene Zielsysteme (verfügbare Aktionen)\n")
		for _, d := range docs {
			b.WriteString(d)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	} else {
		b.WriteString("## Angebundene Zielsysteme\nKeine — der Agent hat aktuell keine Zielsysteme angebunden.\n\n")
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

// --- Anthropic Messages-API (roher HTTP-Call, wie credcheck.go) ---

type anthTextBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type anthMessagesReq struct {
	Model        string            `json:"model"`
	MaxTokens    int               `json:"max_tokens"`
	System       []anthTextBlock   `json:"system,omitempty"`
	Messages     []assistMessage   `json:"messages"`
	OutputConfig *anthOutputConfig `json:"output_config,omitempty"`
}

// anthOutputConfig steuert die Denktiefe. Ab Opus 5 denkt das Modell per
// Voreinstellung, und max_tokens deckelt Denken *und* Antwort gemeinsam — für
// eine eng umrissene Umformulierungsaufgabe ist das teuer erkauft: gemessen
// über zwei Minuten Wartezeit für 24 Titel. Ein niedriges effort ist hier der
// richtige Hebel; Denken ganz abzuschalten wäre der falsche, weil das Modell
// dann `<thinking>`-Tags in den Antworttext schreiben kann und das JSON bricht.
type anthOutputConfig struct {
	Effort string `json:"effort,omitempty"` // low | medium | high | xhigh | max
}

// anthCall bündelt, was je Aufrufer verschieden ist. Die Auth-Mechanik ist
// geteilt, die Modellwahl nicht.
type anthCall struct {
	Model     string
	MaxTokens int
	Effort    string // leer = Vorgabe des Modells
}

type anthMessagesResp struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
	Error      *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// callAnthropicMessages ruft die Messages-API mit exakt der Auth-Mechanik auf,
// die auch die Runtime nutzt (API-Key via x-api-key, Abo-OAuth-Token via
// Bearer + oauth-Beta-Header). Bei OAuth-Tokens verlangt Anthropic den
// Claude-Code-Identitäts-Block als erstes System-Segment.
func callAnthropicMessages(ctx context.Context, credential string, oauth bool, call anthCall, system string, messages []assistMessage) (string, error) {
	sys := []anthTextBlock{}
	if oauth {
		sys = append(sys, anthTextBlock{Type: "text",
			Text: "You are Claude Code, Anthropic's official CLI for Claude."})
	}
	sys = append(sys, anthTextBlock{Type: "text", Text: system})

	req0 := anthMessagesReq{
		Model:     call.Model,
		MaxTokens: call.MaxTokens,
		System:    sys,
		Messages:  messages,
	}
	if call.Effort != "" {
		req0.OutputConfig = &anthOutputConfig{Effort: call.Effort}
	}
	body, err := json.Marshal(req0)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		anthropicBaseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	if oauth {
		req.Header.Set("Authorization", "Bearer "+credential)
		req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	} else {
		req.Header.Set("x-api-key", credential)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))

	var parsed anthMessagesResp
	_ = json.Unmarshal(raw, &parsed)
	if resp.StatusCode != http.StatusOK {
		if parsed.Error != nil && parsed.Error.Message != "" {
			return "", errors.New(parsed.Error.Message)
		}
		return "", errors.New("HTTP " + resp.Status)
	}
	var out strings.Builder
	for _, c := range parsed.Content {
		if c.Type == "text" {
			out.WriteString(c.Text)
		}
	}
	return out.String(), nil
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
