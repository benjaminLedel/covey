package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"covey/internal/memory"
)

// Titel-Pass der Wiki-Wartung (spec/05).
//
// Der Wartungs-Pass verschmilzt Dubletten rein rechnerisch über den Vektorindex
// — Titel kann er nicht anfassen, dafür braucht es Sprachverständnis. Dieser
// Pass ergänzt ihn: er legt für die Seiten, die der episodic-Befund als
// Tagebuch-Titel markiert, kürzere Entitäts-Titel *vor*. Geschrieben wird
// nichts; das Übernehmen läuft über den bestehenden Weg (PATCH /memories/{id}),
// den ein Mensch auslöst.
//
// Warum Vorschlag statt Automatik: der Titel ist die Adresse, unter der ein
// Agent seine eigene Erinnerung wiederfindet. Ein Modell, das ihn im
// 10-Minuten-Takt unbeaufsichtigt umschreibt, wäre ein Schreibzugriff auf
// fremdes Gedächtnis ohne Vier-Augen-Prinzip — dieselbe Leitplanke, unter der
// schon der Config-Copilot nichts selbst committet.

// retitleModel: der Pass läuft auf dem jeweils aktuellsten Opus. Die Aufgabe
// ist kurz, aber sie verlangt Urteilsvermögen darüber, was die Entität hinter
// einem Vorgangstitel eigentlich ist.
const retitleModel = "claude-opus-5"

// retitleMaxTokens: pro Seite gut 40 Token Antwort, plus Reserve. Nicht
// gestreamt, also unter dem SDK-Timeout-Bereich bleiben.
const retitleMaxTokens = 8000

// retitleMaxPages deckelt einen Durchlauf. Mehr Seiten sprengen weniger das
// Kontextfenster als die menschliche Bereitschaft, eine Vorschlagsliste
// durchzusehen — wer 200 Titel bestätigen soll, bestätigt blind.
const retitleMaxPages = 40

// retitleBodyChars: so viel Inhalt bekommt das Modell je Seite zu sehen. Der
// erste Absatz sagt fast immer, worum die Seite geht; der Rest kostet nur.
const retitleBodyChars = 400

type retitleProposal struct {
	Slug   string `json:"slug"`
	Old    string `json:"old"`
	Title  string `json:"title"`
	Reason string `json:"reason,omitempty"`
}

type retitleResponse struct {
	Proposals []retitleProposal `json:"proposals"`
	Checked   int               `json:"checked"` // wie viele Seiten geprüft wurden
	Skipped   int               `json:"skipped"` // wie viele über retitleMaxPages hinaus lagen
}

const retitleSystem = `Du räumst die Titel eines Agenten-Wikis auf.

Das Wiki ist das Langzeitgedächtnis eines KI-Agenten: eine Seite pro Entität
(Kunde, Projekt, System, Person, Problem, Thema). Die vorgelegten Seiten haben
Titel, die einen Vorgang statt einer Sache benennen — sie sind zu lang, tragen
ein Datum, oder lesen sich wie ein Tagebucheintrag.

Schlage für jede Seite einen Titel vor, der die Entität benennt:
- höchstens 60 Zeichen, ideal 20 bis 45
- kein Datum, kein Status ("fertig", "wartet", "Stand 30.07.")
- kein ganzer Satz, keine Wertung — der Titel ist eine Adresse, keine Nachricht
- konkrete Bezeichner behalten (Projekt- und Repo-Namen, Ticket-Nummern wie
  !100 oder #222, Werkzeugnamen) — daran findet der Agent die Seite wieder
- Sprache des bisherigen Titels beibehalten

Lass eine Seite aus, wenn der bisherige Titel bereits die Entität benennt und
du ihn nicht ehrlich verbesserst. Ein unveränderter Vorschlag ist kein Ergebnis.

Antworte ausschließlich mit JSON, ohne Fließtext davor oder danach:
{"proposals":[{"slug":"…","title":"…","reason":"kurze Begründung"}]}`

// handleWikiRetitle legt Titel-Vorschläge vor. Rein lesend — der Endpunkt
// schreibt nichts ins Wiki.
func (s *Server) handleWikiRetitle(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	if _, err := s.Registry.Get(r.Context(), id); err != nil {
		mapErr(w, err)
		return
	}
	p := principalFrom(r)
	cred, oauth, ok := s.resolveOrgClaude(r.Context(), p.OrgID)
	if !ok {
		writeErr(w, http.StatusPreconditionFailed,
			"kein org-weites Claude-Credential hinterlegt — der Titel-Pass ist nicht verfügbar")
		return
	}

	pages, err := s.Memory.List(r.Context(), id, 5000)
	if err != nil {
		mapErr(w, err)
		return
	}
	var todo []memory.Entry
	for _, pg := range pages {
		if memory.NeedsRetitle(pg.Title) {
			todo = append(todo, pg)
		}
	}
	out := retitleResponse{Proposals: []retitleProposal{}, Checked: len(todo)}
	if len(todo) > retitleMaxPages {
		out.Skipped = len(todo) - retitleMaxPages
		todo = todo[:retitleMaxPages]
	}
	if len(todo) == 0 {
		writeJSON(w, http.StatusOK, out)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()
	raw, err := callAnthropicMessages(ctx, cred, oauth, retitleModel, retitleMaxTokens,
		retitleSystem, []assistMessage{{Role: "user", Content: retitlePrompt(todo)}})
	if err != nil {
		s.Log.Error("wiki-titel-pass", "agent", id, "err", err)
		writeErr(w, http.StatusBadGateway, "Titel-Pass nicht erreichbar: "+err.Error())
		return
	}

	byslug := map[string]memory.Entry{}
	for _, pg := range todo {
		byslug[pg.Slug] = pg
	}
	out.Proposals = parseRetitleReply(raw, byslug)
	writeJSON(w, http.StatusOK, out)
}

// retitlePrompt legt die Seiten als kompakte Liste vor: Slug als Schlüssel,
// bisheriger Titel, Anfang des Inhalts.
func retitlePrompt(pages []memory.Entry) string {
	var b strings.Builder
	b.WriteString("Seiten:\n\n")
	for _, p := range pages {
		b.WriteString("--- slug: " + p.Slug + "\n")
		b.WriteString("titel: " + p.Title + "\n")
		body := strings.Join(strings.Fields(p.Content), " ")
		if r := []rune(body); len(r) > retitleBodyChars {
			body = string(r[:retitleBodyChars]) + " …"
		}
		b.WriteString("inhalt: " + body + "\n\n")
	}
	return b.String()
}

// parseRetitleReply zieht die Vorschläge aus der Modell-Antwort und verwirft,
// was nicht taugt: unbekannte Slugs (das Modell hat sich einen ausgedacht),
// leere oder unveränderte Titel, und alles, was selbst wieder als Tagebuch-Titel
// durchginge — ein Vorschlag, der den Befund nicht behebt, ist keiner.
func parseRetitleReply(raw string, known map[string]memory.Entry) []retitleProposal {
	txt := strings.TrimSpace(raw)
	if strings.HasPrefix(txt, "```") {
		if i := strings.IndexByte(txt, '\n'); i >= 0 {
			txt = txt[i+1:]
		}
		txt = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(txt), "```"))
	}
	if i := strings.IndexByte(txt, '{'); i > 0 {
		txt = txt[i:]
	}
	if j := strings.LastIndexByte(txt, '}'); j >= 0 && j < len(txt)-1 {
		txt = txt[:j+1]
	}

	var parsed struct {
		Proposals []retitleProposal `json:"proposals"`
	}
	if err := json.Unmarshal([]byte(txt), &parsed); err != nil {
		return []retitleProposal{}
	}
	out := []retitleProposal{}
	seen := map[string]bool{}
	for _, pr := range parsed.Proposals {
		page, ok := known[pr.Slug]
		if !ok || seen[pr.Slug] {
			continue
		}
		title := strings.TrimSpace(pr.Title)
		if title == "" || title == strings.TrimSpace(page.Title) || memory.NeedsRetitle(title) {
			continue
		}
		seen[pr.Slug] = true
		out = append(out, retitleProposal{
			Slug:   pr.Slug,
			Old:    page.Title,
			Title:  title,
			Reason: strings.TrimSpace(pr.Reason),
		})
	}
	return out
}
