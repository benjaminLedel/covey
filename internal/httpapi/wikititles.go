package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"covey/internal/memory"
)

// Titel-Pass der Wiki-Wartung (spec/05).
//
// Der Wartungs-Pass verschmilzt Dubletten rein rechnerisch über den Vektorindex
// — Titel kann er nicht anfassen, dafür braucht es Sprachverständnis. Dieser
// Pass ergänzt ihn: er benennt die Seiten um, die der episodic-Befund als
// Tagebuch-Titel markiert.
//
// Er schreibt direkt, wie der Dubletten-Teil auch. Das ist die schwächere
// Änderung von beiden: der Slug bleibt (Verweise brechen nicht), der Inhalt
// bleibt, und jede Umbenennung steht mit ihrem alten Titel im Protokoll und
// lässt sich einzeln zurücknehmen — während der Dubletten-Teil im selben Lauf
// ganze Seiten löscht.
//
// Nicht im 10-Minuten-Ticker: dort läuft der Pass für alle Agenten, und jeder
// Durchlauf kostet einen LLM-Aufruf. Das ist eine Kostenentscheidung, keine
// Sicherheitsleitplanke.

// retitleModel: der Pass läuft auf dem jeweils aktuellsten Opus. Die Aufgabe
// ist kurz, aber sie verlangt Urteilsvermögen darüber, was die Entität hinter
// einem Vorgangstitel eigentlich ist.
const retitleModel = "claude-opus-5"

// retitleMaxTokens: pro Seite gut 40 Token Antwort, plus Reserve — und der
// Deckel gilt ab Opus 5 für Denken *und* Antwort gemeinsam, also grosszuegig.
// Nicht gestreamt, also unter dem SDK-Timeout-Bereich bleiben.
const retitleMaxTokens = 16000

// retitleEffort: die Aufgabe ist eng umrissen — aus Titel und Inhaltsanfang
// eine kuerzere Benennung ziehen. Ohne diesen Hebel denkt Opus 5 per Vorgabe
// so lange, dass ein Lauf ueber 24 Seiten zwei Minuten braucht.
const retitleEffort = "low"

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

// wikiRun ist ein laufender oder abgeschlossener Wartungslauf. Er lebt in der
// Control Plane, nicht im Browser-Tab: der Titel-Pass dauert eine halbe Minute,
// und ein Reload in dieser Zeit darf weder den Fortschritt verlieren noch einen
// zweiten Lauf (und damit einen zweiten LLM-Aufruf) erlauben.
type wikiRun struct {
	Status    string            `json:"status"` // running | done | error
	Phase     string            `json:"phase"`  // merge | titles
	Merged    int               `json:"merged"`
	Checked   int               `json:"checked"` // Seiten mit Tagebuch-Titel
	Skipped   int               `json:"skipped"` // über retitleMaxPages hinaus
	Proposals []retitleProposal `json:"proposals"`
	Error     string            `json:"error,omitempty"`
	StartedAt time.Time         `json:"started_at"`
}

// wikiRunStore hält je Agent den letzten Lauf. Im Speicher, nicht in der DB:
// ein Lauf ist Sekunden alt und beim Neustart der Control Plane ohnehin
// bedeutungslos — Vorschläge, die niemand mehr bestätigen kann, sind Müll.
type wikiRunStore struct {
	mu   sync.Mutex
	runs map[uuid.UUID]*wikiRun
}

func newWikiRunStore() *wikiRunStore { return &wikiRunStore{runs: map[uuid.UUID]*wikiRun{}} }

// begin startet einen Lauf, sofern keiner läuft. started=false heißt: es läuft
// schon einer — der Aufrufer bekommt ihn zurück, statt einen zweiten zu treten.
func (st *wikiRunStore) begin(id uuid.UUID, now time.Time) (wikiRun, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if cur, ok := st.runs[id]; ok && cur.Status == "running" {
		return *cur, false
	}
	run := &wikiRun{Status: "running", Phase: "merge", Proposals: []retitleProposal{}, StartedAt: now}
	st.runs[id] = run
	return *run, true
}

func (st *wikiRunStore) update(id uuid.UUID, fn func(*wikiRun)) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if run, ok := st.runs[id]; ok {
		fn(run)
	}
}

func (st *wikiRunStore) get(id uuid.UUID) (wikiRun, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	run, ok := st.runs[id]
	if !ok {
		return wikiRun{}, false
	}
	return *run, true
}

// clear räumt einen abgeschlossenen Lauf weg (alle Vorschläge erledigt). Einen
// laufenden lässt es stehen — sonst wäre das Wegklicken ein Abbruch.
func (st *wikiRunStore) clear(id uuid.UUID) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if run, ok := st.runs[id]; ok && run.Status != "running" {
		delete(st.runs, id)
	}
}

// drop nimmt eine Zeile aus dem Protokoll — zurückgenommen oder abgehakt,
// beides heißt: erledigt, nach einem Reload nicht wieder zeigen.
func (st *wikiRunStore) drop(id uuid.UUID, slug string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	run, ok := st.runs[id]
	if !ok {
		return
	}
	kept := run.Proposals[:0]
	for _, pr := range run.Proposals {
		if pr.Slug != slug {
			kept = append(kept, pr)
		}
	}
	run.Proposals = kept
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

// handleWikiMaintainStart stößt den Wartungslauf an: erst Dubletten
// verschmelzen (schreibt), dann Titel vorschlagen (schreibt nicht). Kehrt
// sofort zurück; der Lauf läuft im Hintergrund weiter und wird per GET
// abgefragt. Läuft schon einer, gibt es den zurück — kein zweiter LLM-Aufruf.
func (s *Server) handleWikiMaintainStart(w http.ResponseWriter, r *http.Request) {
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

	run, started := s.wikiRuns.begin(id, time.Now())
	if !started {
		writeJSON(w, http.StatusOK, run)
		return
	}
	// Bewusst nicht an r.Context() gehängt: der Request ist gleich beendet,
	// der Lauf soll es nicht sein.
	go s.runWikiMaintenance(id, cred, oauth)
	writeJSON(w, http.StatusAccepted, run)
}

// handleWikiMaintainStatus liefert den Stand des Laufs — die Grundlage für die
// Fortschrittsanzeige und dafür, dass ein Reload den Knopf gesperrt lässt.
func (s *Server) handleWikiMaintainStatus(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	run, ok := s.wikiRuns.get(id)
	if !ok {
		writeJSON(w, http.StatusOK, wikiRun{Status: "idle", Proposals: []retitleProposal{}})
		return
	}
	writeJSON(w, http.StatusOK, run)
}

// handleWikiMaintainDismiss hakt das Protokoll ab: ohne slug den ganzen Lauf,
// mit slug eine einzelne Zeile. Was hier verschwindet, kommt nach einem Reload
// nicht wieder.
func (s *Server) handleWikiMaintainDismiss(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	if slug := r.URL.Query().Get("slug"); slug != "" {
		s.wikiRuns.drop(id, slug)
	} else {
		s.wikiRuns.clear(id)
	}
	run, ok := s.wikiRuns.get(id)
	if !ok {
		writeJSON(w, http.StatusOK, wikiRun{Status: "idle", Proposals: []retitleProposal{}})
		return
	}
	writeJSON(w, http.StatusOK, run)
}

// runWikiMaintenance ist der Lauf selbst. Jeder Ausgang — auch der Fehler —
// hinterlässt einen Endzustand, sonst hinge der Knopf für immer auf "läuft".
func (s *Server) runWikiMaintenance(id uuid.UUID, cred string, oauth bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	fail := func(err error) {
		s.Log.Error("wiki-wartung", "agent", id, "err", err)
		s.wikiRuns.update(id, func(run *wikiRun) {
			run.Status = "error"
			run.Error = err.Error()
		})
	}

	merged, err := s.Memory.Consolidate(ctx, id)
	if err != nil {
		fail(err)
		return
	}
	s.wikiRuns.update(id, func(run *wikiRun) {
		run.Merged = merged
		run.Phase = "titles"
	})

	pages, err := s.Memory.List(ctx, id, 5000)
	if err != nil {
		fail(err)
		return
	}
	var todo []memory.Entry
	for _, pg := range pages {
		if memory.NeedsRetitle(pg.Title) {
			todo = append(todo, pg)
		}
	}
	skipped := 0
	if len(todo) > retitleMaxPages {
		skipped = len(todo) - retitleMaxPages
		todo = todo[:retitleMaxPages]
	}
	// Die Zahl der zu prüfenden Seiten steht fest, bevor das Modell antwortet —
	// nur damit lässt sich überhaupt etwas anzeigen als "es tut sich was".
	s.wikiRuns.update(id, func(run *wikiRun) {
		run.Checked = len(todo)
		run.Skipped = skipped
	})
	if len(todo) == 0 {
		s.wikiRuns.update(id, func(run *wikiRun) { run.Status = "done" })
		return
	}

	raw, err := callAnthropicMessages(ctx, cred, oauth,
		anthCall{Model: retitleModel, MaxTokens: retitleMaxTokens, Effort: retitleEffort},
		retitleSystem, []assistMessage{{Role: "user", Content: retitlePrompt(todo)}})
	if err != nil {
		fail(err)
		return
	}
	byslug := map[string]memory.Entry{}
	for _, pg := range todo {
		byslug[pg.Slug] = pg
	}
	// Übernehmen, nicht vorlegen: derselbe Lauf verschmilzt einen Absatz weiter
	// oben ganze Seiten und löscht die Dublette — dagegen ist eine Umbenennung
	// harmlos. Der Slug bleibt, der Inhalt bleibt, und die Liste unten führt
	// jede Änderung mit altem Titel, sodass sie einzeln zurückgenommen werden
	// kann. Titel laufen weiterhin nur auf Knopfdruck, nie im Ticker — der
	// Grund dafür ist die LLM-Rechnung, nicht die Vorsicht.
	applied := []retitleProposal{}
	for _, pr := range parseRetitleReply(raw, byslug) {
		page := byslug[pr.Slug]
		if err := s.Memory.UpdatePage(ctx, page.ID, pr.Title, page.Content); err != nil {
			s.Log.Warn("wiki-titel", "slug", pr.Slug, "err", err)
			continue
		}
		applied = append(applied, pr)
	}
	s.wikiRuns.update(id, func(run *wikiRun) {
		run.Proposals = applied
		run.Status = "done"
	})
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
