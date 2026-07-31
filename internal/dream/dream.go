// Package dream ist die Nachtruhe des Agenten.
//
// Ein Agent sammelt tagsüber Wissen und legt es ab, wie es gerade anfällt:
// Seiten mit Vorgangstiteln, Dubletten, Notizen ohne Verweis. Im Traum geht er
// das Gesammelte noch einmal durch und ordnet es — er verschmilzt, was dasselbe
// sagt, und benennt um, was einen Vorgang statt einer Sache benennt. Später
// kommt dazu, was ein Gedächtnis sonst noch braucht: verlinken, einordnen,
// Verwahrlostes wegräumen.
//
// Zwei Eigenschaften machen den Traum zu mehr als einem Wartungsjob:
//
//   - Er schreibt. Ungefragt, nachts, ohne Zuschauer. Deshalb hält jede
//     Handlung ihren Zustand davor fest — was der Traum getan hat, lässt sich
//     am Morgen einzeln zurücknehmen.
//   - Er ist les- und nachvollziehbar. Nicht „Wartung ausgeführt", sondern
//     welche Seite, von welchem Titel auf welchen, und warum.
package dream

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"covey/internal/claudeapi"
	"covey/internal/memory"
)

// Model: der Traum läuft auf dem jeweils aktuellsten Opus. Die Aufgaben sind
// kurz, verlangen aber Urteilsvermögen darüber, was die Sache hinter einer
// Notiz eigentlich ist.
const Model = "claude-opus-5"

// MaxTokens großzügig: ab Opus 5 deckelt das Limit Denken *und* Antwort.
const MaxTokens = 16000

// Effort niedrig. Denken bleibt an: gemessen liefert das Modell mit
// abgeschaltetem Denken zwar in zwei statt sechzig Sekunden, aber gar kein
// verwertbares Ergebnis mehr — null Umbenennungen bei einem klar
// umzubenennenden Titel. Ein Traum läuft nachts; eine Minute ist dort kein
// Argument, ein leeres Ergebnis schon.
const Effort = "low"

// MaxPages deckelt einen Durchlauf. Was darüber liegt, bleibt für die nächste
// Nacht liegen und wird im Traum ausgewiesen — ein Traum darf nicht
// vollständiger aussehen, als er war.
const MaxPages = 40

// bodyChars: so viel Inhalt sieht das Modell je Seite. Der erste Absatz sagt
// fast immer, worum es geht; der Rest kostet nur.
const bodyChars = 400

// staleAfter: ein Traum, der so lange auf "running" steht, hat den Neustart der
// Control Plane nicht überlebt. Er wird beim nächsten Start abgeräumt, statt
// den Agenten für immer als träumend zu führen.
const staleAfter = 30 * time.Minute

// Action ist eine einzelne Handlung im Traum.
type Action struct {
	ID       uuid.UUID  `json:"id"`
	Kind     string     `json:"kind"` // retitle | merge
	PageSlug string     `json:"page_slug,omitempty"`
	Before   string     `json:"before,omitempty"`
	After    string     `json:"after,omitempty"`
	Reason   string     `json:"reason,omitempty"`
	UndoneAt *time.Time `json:"undone_at,omitempty"`
}

// Dream ist ein Traum samt seiner Handlungen.
type Dream struct {
	ID         uuid.UUID  `json:"id"`
	AgentID    uuid.UUID  `json:"agent_id"`
	Trigger    string     `json:"trigger"` // manual | nightly
	Status     string     `json:"status"`  // running | done | error
	Error      string     `json:"error,omitempty"`
	Phase      string     `json:"phase,omitempty"`
	LookedAt   int        `json:"looked_at"`
	Skipped    int        `json:"skipped"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	Actions    []Action   `json:"actions"`
}

// Store hält die Traum-Historie.
type Store struct {
	pool *pgxpool.Pool
	mem  *memory.Store
	log  *slog.Logger
}

func NewStore(pool *pgxpool.Pool, mem *memory.Store, log *slog.Logger) *Store {
	if log == nil {
		log = slog.Default()
	}
	return &Store{pool: pool, mem: mem, log: log}
}

// ErrAsleep: für diesen Agenten läuft bereits ein Traum. Kein Fehlerfall im
// engeren Sinn — der Aufrufer soll den laufenden anzeigen, nicht einen zweiten
// starten (jeder Traum kostet einen LLM-Aufruf).
var ErrAsleep = fmt.Errorf("agent träumt bereits")

// Begin legt einen Traum an, sofern keiner läuft. Räumt zuvor Träume ab, die
// einen Neustart nicht überlebt haben.
func (s *Store) Begin(ctx context.Context, agentID uuid.UUID, trigger string) (Dream, error) {
	if _, err := s.pool.Exec(ctx,
		`UPDATE dreams SET status='error', error='abgebrochen (Control Plane neu gestartet)',
		 finished_at=now() WHERE agent_id=$1 AND status='running' AND started_at < $2`,
		agentID, time.Now().Add(-staleAfter)); err != nil {
		return Dream{}, err
	}
	var running bool
	if err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM dreams WHERE agent_id=$1 AND status='running')`,
		agentID).Scan(&running); err != nil {
		return Dream{}, err
	}
	if running {
		return Dream{}, ErrAsleep
	}
	d := Dream{ID: uuid.New(), AgentID: agentID, Trigger: trigger, Status: "running",
		Phase: "merge", StartedAt: time.Now(), Actions: []Action{}}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO dreams (id, agent_id, trigger, status, phase, started_at)
		 VALUES ($1,$2,$3,'running','merge',$4)`, d.ID, agentID, trigger, d.StartedAt)
	return d, err
}

func (s *Store) setPhase(ctx context.Context, id uuid.UUID, phase string, lookedAt, skipped int) {
	_, _ = s.pool.Exec(ctx,
		`UPDATE dreams SET phase=$2, looked_at=$3, skipped=$4 WHERE id=$1`, id, phase, lookedAt, skipped)
}

func (s *Store) finish(ctx context.Context, id uuid.UUID, status, errMsg string) {
	_, _ = s.pool.Exec(ctx,
		`UPDATE dreams SET status=$2, error=$3, phase='', finished_at=now() WHERE id=$1`, id, status, errMsg)
}

func (s *Store) addAction(ctx context.Context, dreamID uuid.UUID, a Action) {
	_, _ = s.pool.Exec(ctx,
		`INSERT INTO dream_actions (id, dream_id, kind, page_slug, before, after, reason)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		uuid.New(), dreamID, a.Kind, a.PageSlug, a.Before, a.After, a.Reason)
}

// List liefert die Traum-Historie eines Agenten, neueste zuerst, samt
// Handlungen. Zwei Abfragen statt eines Joins: die Historie ist kurz, und ein
// Join über eine 1:n-Beziehung müsste hier von Hand entfaltet werden.
func (s *Store) List(ctx context.Context, agentID uuid.UUID, limit int) ([]Dream, error) {
	if limit <= 0 {
		limit = 30
	}
	rows, err := s.pool.Query(ctx,
		`SELECT id, agent_id, trigger, status, error, phase, looked_at, skipped, started_at, finished_at
		 FROM dreams WHERE agent_id=$1 ORDER BY started_at DESC LIMIT $2`, agentID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Dream{}
	byID := map[uuid.UUID]int{}
	for rows.Next() {
		var d Dream
		if err := rows.Scan(&d.ID, &d.AgentID, &d.Trigger, &d.Status, &d.Error, &d.Phase,
			&d.LookedAt, &d.Skipped, &d.StartedAt, &d.FinishedAt); err != nil {
			return nil, err
		}
		d.Actions = []Action{}
		byID[d.ID] = len(out)
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return out, nil
	}
	ids := make([]uuid.UUID, 0, len(out))
	for _, d := range out {
		ids = append(ids, d.ID)
	}
	arows, err := s.pool.Query(ctx,
		`SELECT id, dream_id, kind, page_slug, before, after, reason, undone_at
		 FROM dream_actions WHERE dream_id = ANY($1) ORDER BY created_at`, ids)
	if err != nil {
		return nil, err
	}
	defer arows.Close()
	for arows.Next() {
		var a Action
		var dreamID uuid.UUID
		if err := arows.Scan(&a.ID, &dreamID, &a.Kind, &a.PageSlug, &a.Before, &a.After,
			&a.Reason, &a.UndoneAt); err != nil {
			return nil, err
		}
		if i, ok := byID[dreamID]; ok {
			out[i].Actions = append(out[i].Actions, a)
		}
	}
	return out, arows.Err()
}

// Undo nimmt eine Handlung zurück. Nur Umbenennungen sind umkehrbar —
// verschmolzene Seiten wieder zu trennen hieße, den Inhalt zu erraten.
func (s *Store) Undo(ctx context.Context, actionID uuid.UUID) error {
	var kind, slug, before string
	var agentID uuid.UUID
	var undone *time.Time
	err := s.pool.QueryRow(ctx,
		`SELECT a.kind, a.page_slug, a.before, a.undone_at, d.agent_id
		 FROM dream_actions a JOIN dreams d ON d.id = a.dream_id WHERE a.id=$1`,
		actionID).Scan(&kind, &slug, &before, &undone, &agentID)
	if err != nil {
		return err
	}
	if undone != nil {
		return nil // schon zurückgenommen — kein Fehler, nur nichts zu tun
	}
	if kind != "retitle" {
		return fmt.Errorf("%s lässt sich nicht zurücknehmen", kind)
	}
	page, err := s.mem.Read(ctx, agentID, slug)
	if err != nil {
		return err
	}
	if err := s.mem.UpdatePage(ctx, page.ID, before, page.Content); err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `UPDATE dream_actions SET undone_at=now() WHERE id=$1`, actionID)
	return err
}

// Current liefert den laufenden Traum eines Agenten, falls es einen gibt.
func (s *Store) Current(ctx context.Context, agentID uuid.UUID) (Dream, bool, error) {
	list, err := s.List(ctx, agentID, 1)
	if err != nil || len(list) == 0 {
		return Dream{}, false, err
	}
	if list[0].Status != "running" {
		return Dream{}, false, nil
	}
	return list[0], true, nil
}

// SleepersSince nennt die Agenten, die seit dem gegebenen Zeitpunkt nicht mehr
// geträumt haben und Wiki-Seiten besitzen — die Arbeitsliste des Nachtlaufs.
func (s *Store) SleepersSince(ctx context.Context, since time.Time) ([]uuid.UUID, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT DISTINCT p.agent_id FROM wiki_pages p
		 WHERE NOT EXISTS (
		   SELECT 1 FROM dreams d WHERE d.agent_id = p.agent_id AND d.started_at >= $1
		 )`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err == nil {
			out = append(out, id)
		}
	}
	return out, rows.Err()
}

// --- Der Traum selbst ---

// Credential ist das aufgelöste Claude-Credential der Organisation.
type Credential struct {
	Value string
	OAuth bool
}

// Run träumt: verschmelzen, dann umbenennen. Jeder Ausgang — auch der Fehler —
// hinterlässt einen Endzustand, sonst gilt der Agent für immer als träumend.
func (s *Store) Run(ctx context.Context, d Dream, cred Credential) {
	fail := func(err error) { s.finish(ctx, d.ID, "error", err.Error()) }

	// 1. Verschmelzen — rein rechnerisch über den Vektorindex, ohne Modell.
	merged, err := s.mem.Consolidate(ctx, d.AgentID)
	if err != nil {
		fail(err)
		return
	}
	for i := 0; i < merged; i++ {
		s.addAction(ctx, d.ID, Action{Kind: "merge"})
	}

	// 2. Umbenennen — dafür braucht es Sprachverständnis.
	s.setPhase(ctx, d.ID, "titles", 0, 0)
	pages, err := s.mem.List(ctx, d.AgentID, 5000)
	if err != nil {
		fail(err)
		return
	}
	var todo []memory.Entry
	for _, p := range pages {
		if memory.NeedsRetitle(p.Title) {
			todo = append(todo, p)
		}
	}
	skipped := 0
	if len(todo) > MaxPages {
		skipped = len(todo) - MaxPages
		todo = todo[:MaxPages]
	}
	s.setPhase(ctx, d.ID, "titles", len(todo), skipped)
	if len(todo) == 0 {
		s.finish(ctx, d.ID, "done", "")
		return
	}

	raw, err := claudeapi.Messages(ctx, cred.Value, cred.OAuth,
		claudeapi.Call{Model: Model, MaxTokens: MaxTokens, Effort: Effort},
		retitleSystem, []claudeapi.Message{{Role: "user", Content: retitlePrompt(todo)}})
	if err != nil {
		fail(err)
		return
	}
	bySlug := map[string]memory.Entry{}
	for _, p := range todo {
		bySlug[p.Slug] = p
	}
	items := parseRetitle(raw, bySlug)
	// Wer nachts unbeaufsichtigt arbeitet, muss sagen können, warum er nichts
	// getan hat. Ohne den Anfang der Rohantwort ist ein leerer Traum von einem
	// missverstandenen nicht zu unterscheiden.
	if len(items) == 0 {
		s.log.Info("traum: keine umbenennung", "agent", d.AgentID, "geprueft", len(todo),
			"antwort_zeichen", len(raw), "antwort_anfang", head(raw, 300))
	}
	for _, pr := range items {
		page := bySlug[pr.Slug]
		if err := s.mem.UpdatePage(ctx, page.ID, pr.Title, page.Content); err != nil {
			continue
		}
		s.addAction(ctx, d.ID, Action{Kind: "retitle", PageSlug: pr.Slug,
			Before: page.Title, After: pr.Title, Reason: pr.Reason})
	}
	s.finish(ctx, d.ID, "done", "")
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

type retitleItem struct {
	Slug   string `json:"slug"`
	Title  string `json:"title"`
	Reason string `json:"reason"`
}

func retitlePrompt(pages []memory.Entry) string {
	var b strings.Builder
	b.WriteString("Seiten:\n\n")
	for _, p := range pages {
		b.WriteString("--- slug: " + p.Slug + "\n")
		b.WriteString("titel: " + p.Title + "\n")
		body := strings.Join(strings.Fields(p.Content), " ")
		if r := []rune(body); len(r) > bodyChars {
			body = string(r[:bodyChars]) + " …"
		}
		b.WriteString("inhalt: " + body + "\n\n")
	}
	return b.String()
}

// parseRetitle zieht die Vorschläge aus der Antwort und verwirft, was nicht
// taugt: erfundene Slugs, leere oder unveränderte Titel, und alles, was selbst
// wieder als Tagebuch-Titel durchginge — ein Vorschlag, der den Befund nicht
// behebt, ist keiner.
func parseRetitle(raw string, known map[string]memory.Entry) []retitleItem {
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
		Proposals []retitleItem `json:"proposals"`
	}
	if err := json.Unmarshal([]byte(txt), &parsed); err != nil {
		return nil
	}
	out := []retitleItem{}
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
		out = append(out, retitleItem{Slug: pr.Slug, Title: title, Reason: strings.TrimSpace(pr.Reason)})
	}
	return out
}

// --- Nachtlauf ---

// CredentialFor löst das Claude-Credential für einen Agenten auf. Als Funktion
// übergeben, damit dieses Paket weder die Registry noch den Secrets-Broker
// kennen muss — es träumt, es sucht keine Schlüssel.
type CredentialFor func(ctx context.Context, agentID uuid.UUID) (Credential, bool)

// nightlyCheck: wie oft geprüft wird, ob die Traumzeit erreicht ist. Fein genug,
// dass ein Neustart kurz vor der Uhrzeit die Nacht nicht verschluckt, grob
// genug, dass es nicht auffällt.
const nightlyCheck = 5 * time.Minute

// RunNightly lässt jeden Agenten einmal pro Nacht träumen, sobald die
// Ortszeit `at` (Format "03:00") überschritten ist. Bewusst seriell: zwanzig
// Agenten gleichzeitig träumen zu lassen hieße zwanzig gleichzeitige
// LLM-Aufrufe, und niemand wartet nachts auf das Ergebnis.
//
// Wer schon geträumt hat, erkennt der Lauf an der Historie selbst — kein
// separater Merker, der mit ihr auseinanderlaufen könnte.
func (s *Store) RunNightly(ctx context.Context, at string, credFor CredentialFor, log *slog.Logger) {
	hh, mm, err := parseClock(at)
	if err != nil {
		log.Warn("traumzeit unlesbar — Nachtlauf aus", "at", at, "err", err)
		return
	}
	log.Info("nachtlauf aktiv", "at", at)
	ticker := time.NewTicker(nightlyCheck)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		now := time.Now()
		since := time.Date(now.Year(), now.Month(), now.Day(), hh, mm, 0, 0, now.Location())
		if now.Before(since) {
			continue // die Nacht ist noch nicht so weit
		}
		ids, err := s.SleepersSince(ctx, since)
		if err != nil {
			log.Warn("nachtlauf: schlafende agenten nicht ermittelbar", "err", err)
			continue
		}
		for _, id := range ids {
			cred, ok := credFor(ctx, id)
			if !ok {
				continue // ohne Credential kein Traum; kein Grund zu lärmen
			}
			d, err := s.Begin(ctx, id, "nightly")
			if err != nil {
				if err != ErrAsleep {
					log.Warn("nachtlauf: traum nicht startbar", "agent", id, "err", err)
				}
				continue
			}
			log.Info("agent träumt", "agent", id)
			s.Run(ctx, d, cred)
		}
	}
}

// parseClock liest "HH:MM".
func parseClock(s string) (int, int, error) {
	t, err := time.Parse("15:04", strings.TrimSpace(s))
	if err != nil {
		return 0, 0, err
	}
	return t.Hour(), t.Minute(), nil
}

// head kürzt für die Log-Ausgabe rune-sicher.
func head(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
