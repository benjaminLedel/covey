// Package workrecord baut die Arbeitsakte eines Kollegen zusammen: Fakten, die
// die Control Plane selbst aufgeschrieben hat, je Agent und Zeitraum
// (spec/21-operations-and-improvement.md).
//
// Warum nicht einfach die Recordings hergeben, wenn jemand wissen will, wie ein
// Agent arbeitet — der naheliegende Weg ist in drei Punkten gleichzeitig
// falsch: Recordings tragen Ticket- und Mail-Inhalte anderer Abteilungen, ein
// Leser wäre damit ein Exfiltrations-Pfad durch das ganze Org-Chart; Text aus
// einem Zielsystem, der den Agenten erreicht, der Konfigurationen vorschlägt,
// ist der Injection-Pfad aus spec/04 auf das wertvollste Ziel der Plattform
// gerichtet; und ein Monat Recordings passt zu keinem vernünftigen Preis in ein
// Kontextfenster.
//
// Also: Fakten. Was gezählt wurde, nicht was gesagt wurde. Mit ZWEI ehrlichen
// Ausnahmen, und beide sind eine Zeile statt eines Verlaufs:
//
//   - Die AUFGABENTITEL. Die kommen häufig aus der Weck-Quelle und können damit
//     einen Ticket-Betreff tragen.
//   - Die FRAGE einer hängenden Aufgabe (StuckTask.Question). Die schreibt der
//     beurteilte Agent selbst in seiner covey/block-Direktive, und er zitiert
//     darin regelmäßig, worauf er wartet — also auch Text aus einem Zielsystem.
//
// Beide bleiben drin, weil die Akte ohne sie nicht lesbar ist: „wartet auf ein
// Ereignis" ist eine Beobachtung, „wartet darauf, ob der Kunde zurückkommt" ist
// ein Befund. Und beide werden HIER benannt statt später entdeckt — eine Akte,
// die „nur Fakten" verspricht und zwei Freitext-Felder führt, wird genau an der
// Stelle geglaubt, an der man sie prüfen müsste.
//
// Der Rest der Absicherung liegt nicht hier: dass dieser Text den Leser nicht
// steuert, steht als Anweisung in ReviewDoc (internal/agents/compile.go) — und
// dahinter die Zusage, die das Ganze trägt, nämlich dass ein Vorschlag nicht
// läuft, sondern von einem Menschen unterschrieben wird.
//
// Das Paket steht bewusst neben httpapi und nicht darin: dieselbe Akte liest
// später der Betriebsingenieur über covey/work_record, und der sitzt im
// Orchestrator.
package workrecord

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"covey/internal/agents"
	"covey/internal/observability"
)

// Record ist die Akte. Acht Abschnitte, jeder aus einer benannten Quelle. Nur
// zwei Felder darin sind Freitext: Aufgabentitel und StuckTask.Question — siehe
// den Paketkopf, dort stehen sie mit ihrer Herkunft.
type Record struct {
	AgentID     uuid.UUID `json:"agent_id"`
	Slug        string    `json:"slug"`
	DisplayName string    `json:"display_name"`
	JobTitle    string    `json:"job_title,omitempty"`
	From        time.Time `json:"from"`
	To          time.Time `json:"to"`

	Throughput Throughput       `json:"throughput"`
	Aborts     []Count          `json:"aborts"`
	Work       []ActionCount    `json:"work"`
	Indicators []Indicator      `json:"indicators"`
	Cost       Cost             `json:"cost"`
	Friction   Friction         `json:"friction"`
	Findings   []agents.Finding `json:"findings"`
	Stuck      []StuckTask      `json:"stuck"`

	// Notes benennt, was gekürzt wurde. Eine Akte, die still bei 200 Aufgaben
	// aufhört, liest sich wie eine vollständige.
	Notes []string `json:"notes,omitempty"`
}

// Count ist eine Zeile „Bezeichnung → Anzahl".
type Count struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

// Throughput: was hereinkam und was daraus wurde.
type Throughput struct {
	ByState  []Count    `json:"by_state"`
	ByOrigin []Count    `json:"by_origin"`
	Tasks    []TaskLine `json:"tasks"`
}

// TaskLine ist eine Aufgabe als eine Zeile — mit dem Titel, der die eine
// ehrliche Ausnahme dieses Pakets ist.
type TaskLine struct {
	ID         uuid.UUID  `json:"id"`
	Title      string     `json:"title"`
	State      string     `json:"state"`
	Origin     string     `json:"origin"`
	CreatedAt  time.Time  `json:"created_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	CostUSD    float64    `json:"cost_usd"`
}

// ActionCount: welche Aktionen ausgeführt wurden, gelungen und gescheitert.
type ActionCount struct {
	Action string `json:"action"`
	OK     int    `json:"ok"`
	Failed int    `json:"failed"`
}

// Indicator ist eine Zählregel des Agenten aus seiner KPIS.md, ausgewertet.
//
// Bewusst ohne Verlauf und ohne Trend, anders als die Preisliste der
// Oberfläche (internal/httpapi/indicators.go): hier wird gelesen, nicht
// verglichen — und eine Sparkline in einer Akte ist eine Zahlenreihe, die
// niemand nachrechnen kann.
type Indicator struct {
	Key    string `json:"key"`
	Title  string `json:"title"`
	Goal   int    `json:"goal,omitempty"`
	Period string `json:"period,omitempty"`
	Count  int64  `json:"count"`
	// UnitUSD fehlt, solange zu wenig gezählt wurde: ein Stückpreis aus zwei
	// Datenpunkten ist Rauschen, und die Plattform gibt ihn deshalb gar nicht
	// erst heraus (observability.UnitCost).
	UnitUSD *float64 `json:"unit_usd,omitempty"`
}

type Cost struct {
	TotalUSD float64 `json:"total_usd"`
	// Tasks ist die Zahl der Aufgaben mit Kosten — der Nenner hinter dem
	// Durchschnitt, damit er nachrechenbar ist statt geglaubt.
	Tasks      int     `json:"tasks"`
	PerTaskUSD float64 `json:"per_task_usd"`
}

// Friction: wo der Agent angehalten wurde, und wo er selbst abgelehnt wurde.
type Friction struct {
	// Approvals sind die Freigaben, die seine Aktionen ausgelöst haben, nach
	// Ausgang.
	Approvals []Count `json:"approvals"`
	// Denied sind die Versuche, etwas Verbotenes zu tun, nach Aktion.
	Denied []Count `json:"denied"`
	// Proposals sind die offenen Punkte, die DIESER Agent geschrieben hat,
	// nach Ausgang. Die Ablehnungsquote auf die eigenen Vorschläge ist die
	// Zahl, die sagt, ob ein Betriebsingenieur etwas taugt — und sie steht in
	// seiner eigenen Akte wie bei jedem anderen auch (spec/21).
	Proposals []Count `json:"proposals"`
}

// StuckTask ist die Fehlerform, die niemand sieht, weil nichts fehlschlägt:
// eine Aufgabe wartet auf ein Ereignis, das nie kommt.
type StuckTask struct {
	ID             uuid.UUID `json:"id"`
	Title          string    `json:"title"`
	CorrelationKey string    `json:"correlation_key"`
	// Question ist der Text des Agenten selbst aus seiner covey/block-Direktive
	// — eines der beiden Freitext-Felder der Akte (Paketkopf).
	Question     string    `json:"question,omitempty"`
	BlockedSince time.Time `json:"blocked_since"`
}

// maxTaskLines begrenzt die Zeilen-Liste. Was darüber liegt, steht in den
// Zählungen — und in Notes, damit die Kürzung nicht wie Vollständigkeit
// aussieht.
const maxTaskLines = 200

// Builder hält, was die Akte braucht. Skills darf nil sein: die Lint-Regeln,
// die Skills lesen, fallen dann weg.
type Builder struct {
	Pool     *pgxpool.Pool
	Registry *agents.Registry
	Obs      *observability.Store
	Skills   agents.SkillLookup
}

// Build stellt die Akte für einen Agenten und einen Zeitraum zusammen.
func (b *Builder) Build(ctx context.Context, agentID uuid.UUID, since time.Time) (Record, error) {
	agent, err := b.Registry.Get(ctx, agentID)
	if err != nil {
		return Record{}, err
	}
	rec := Record{
		AgentID: agent.ID, Slug: agent.Slug, DisplayName: agent.DisplayName,
		JobTitle: agent.JobTitle, From: since, To: time.Now(),
	}

	if rec.Throughput, rec.Cost, err = b.throughput(ctx, agentID, since); err != nil {
		return rec, err
	}
	if len(rec.Throughput.Tasks) == maxTaskLines {
		rec.Notes = append(rec.Notes,
			"task list truncated to the newest 200 — the counts above cover the whole period")
	}
	if rec.Aborts, err = b.aborts(ctx, agentID, since); err != nil {
		return rec, err
	}
	if rec.Work, err = b.work(ctx, agentID, since); err != nil {
		return rec, err
	}
	if rec.Indicators, err = b.indicators(ctx, agent, since, rec.Cost.TotalUSD); err != nil {
		return rec, err
	}
	if rec.Friction, err = b.friction(ctx, agentID, since); err != nil {
		return rec, err
	}
	if rec.Stuck, err = b.stuck(ctx, agentID); err != nil {
		return rec, err
	}
	rec.Findings = b.findings(ctx, agent)
	return rec, nil
}

// throughput liest die Aufgaben des Zeitraums: Zählungen über alles, Zeilen für
// die neuesten. Die Kosten fallen dabei mit ab — sie hängen an denselben
// Aufgaben, und zwei Durchgänge über dieselbe Menge wären zwei Wahrheiten.
func (b *Builder) throughput(ctx context.Context, agentID uuid.UUID, since time.Time) (Throughput, Cost, error) {
	var tp Throughput
	var cost Cost

	byState, err := b.counts(ctx, `SELECT state, count(*) FROM backlog_tasks
		WHERE agent_id=$1 AND created_at >= $2 GROUP BY 1 ORDER BY 2 DESC`, agentID, since)
	if err != nil {
		return tp, cost, err
	}
	tp.ByState = byState

	// Die Herkunft wird am Doppelpunkt abgeschnitten: `agent:qa` und
	// `continuation:<uuid>` sind Klassen, keine Einzelwerte — sonst hätte die
	// Gruppierung so viele Zeilen wie Fortsetzungen.
	byOrigin, err := b.counts(ctx, `SELECT split_part(origin, ':', 1), count(*) FROM backlog_tasks
		WHERE agent_id=$1 AND created_at >= $2 GROUP BY 1 ORDER BY 2 DESC`, agentID, since)
	if err != nil {
		return tp, cost, err
	}
	tp.ByOrigin = byOrigin

	rows, err := b.Pool.Query(ctx, `SELECT t.id, t.title, t.state, t.origin, t.created_at,
			CASE WHEN t.state IN ('done','failed','cancelled') THEN t.updated_at END,
			COALESCE((SELECT sum(c.usd) FROM cost_entries c WHERE c.task_id = t.id), 0)
		FROM backlog_tasks t
		WHERE t.agent_id=$1 AND t.created_at >= $2
		ORDER BY t.created_at DESC LIMIT $3`, agentID, since, maxTaskLines)
	if err != nil {
		return tp, cost, err
	}
	defer rows.Close()
	tp.Tasks = []TaskLine{}
	for rows.Next() {
		var l TaskLine
		if err := rows.Scan(&l.ID, &l.Title, &l.State, &l.Origin, &l.CreatedAt, &l.FinishedAt, &l.CostUSD); err != nil {
			return tp, cost, err
		}
		tp.Tasks = append(tp.Tasks, l)
	}
	if err := rows.Err(); err != nil {
		return tp, cost, err
	}

	// Die Kosten über den GANZEN Zeitraum, nicht über die gezeigten Zeilen.
	if err := b.Pool.QueryRow(ctx, `SELECT COALESCE(sum(usd),0), count(DISTINCT task_id)
		FROM cost_entries WHERE agent_id=$1 AND created_at >= $2`, agentID, since).
		Scan(&cost.TotalUSD, &cost.Tasks); err != nil {
		return tp, cost, err
	}
	if cost.Tasks > 0 {
		cost.PerTaskUSD = cost.TotalUSD / float64(cost.Tasks)
	}
	return tp, cost, nil
}

// aborts beantwortet „warum endeten Läufe" mit den vier Gründen, die es gibt:
// Turn-Limit, Fehler, Budget, Notaus. Alles aus dem Recording, das die Control
// Plane selbst geschrieben hat.
func (b *Builder) aborts(ctx context.Context, agentID uuid.UUID, since time.Time) ([]Count, error) {
	return b.counts(ctx, `SELECT
			CASE
				WHEN kind='guardrail' THEN 'budget'
				WHEN payload->>'reason'='max_turns' THEN 'max_turns'
				WHEN payload->>'status'='task_failed' THEN 'error'
				ELSE 'killed'
			END,
			count(*)
		FROM recording_events
		WHERE agent_id=$1 AND created_at >= $2
		  AND ( (kind='lifecycle' AND (payload->>'reason'='max_turns'
		         OR payload->>'status' IN ('task_failed','killed')))
		     OR (kind='guardrail' AND payload->>'rule'='budget_limit') )
		GROUP BY 1 ORDER BY 2 DESC`, agentID, since)
}

// work zählt die ausgeführten Aktionen, getrennt nach gelungen und gescheitert.
// Die Trennung ist der Punkt: zwanzig Versuche und null Erfolge sehen in einer
// Summe aus wie Betrieb.
func (b *Builder) work(ctx context.Context, agentID uuid.UUID, since time.Time) ([]ActionCount, error) {
	rows, err := b.Pool.Query(ctx, `SELECT payload->>'action',
			count(*) FILTER (WHERE payload->>'ok' = 'true'),
			count(*) FILTER (WHERE payload->>'ok' <> 'true')
		FROM recording_events
		WHERE agent_id=$1 AND kind=$2 AND created_at >= $3 AND payload ? 'action'
		GROUP BY 1 ORDER BY count(*) DESC`, agentID, observability.KindAction, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ActionCount{}
	for rows.Next() {
		var a ActionCount
		if err := rows.Scan(&a.Action, &a.OK, &a.Failed); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// indicators wertet die Zählregeln des Agenten aus seiner eigenen KPIS.md aus.
//
// Ein Parse-Fehler kostet die Akte nicht: eine Config, die vor dem Parser
// gespeichert wurde, soll keine leere Seite erzeugen. Sie taucht dafür in den
// Notes auf.
func (b *Builder) indicators(ctx context.Context, agent agents.Agent, since time.Time, totalUSD float64) ([]Indicator, error) {
	cfg, err := b.Registry.CurrentConfig(ctx, agent.ID)
	if err != nil {
		return []Indicator{}, nil
	}
	kpis, err := agents.ParseKPIs(cfg.Files["KPIS.md"])
	if err != nil {
		return []Indicator{}, nil
	}
	out := []Indicator{}
	for _, k := range kpis {
		count, _, _, err := b.Obs.CountIndicator(ctx, observability.Indicator{
			Key: k.Key, Title: k.Title, Action: k.Action,
			Origin: k.Origin, Per: k.Per, Goal: k.Goal, Period: k.Period,
		}, []uuid.UUID{agent.ID}, since)
		if err != nil {
			return out, err
		}
		out = append(out, Indicator{
			Key: k.Key, Title: k.Title, Goal: k.Goal, Period: k.Period,
			Count: count, UnitUSD: observability.UnitCost(totalUSD, count),
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Count > out[j].Count })
	return out, nil
}

func (b *Builder) friction(ctx context.Context, agentID uuid.UUID, since time.Time) (Friction, error) {
	var f Friction
	var err error
	if f.Approvals, err = b.counts(ctx, `SELECT status, count(*) FROM approvals
		WHERE agent_id=$1 AND requested_at >= $2 GROUP BY 1 ORDER BY 2 DESC`, agentID, since); err != nil {
		return f, err
	}
	if f.Denied, err = b.counts(ctx, `SELECT COALESCE(payload->>'action', payload->>'rule'), count(*)
		FROM recording_events
		WHERE agent_id=$1 AND kind=$2 AND created_at >= $3 AND payload->>'decision'='denied'
		GROUP BY 1 ORDER BY 2 DESC`, agentID, observability.KindGuardrail, since); err != nil {
		return f, err
	}
	// Die eigenen Vorschläge — geschrieben VON diesem Agenten, nicht über ihn.
	if f.Proposals, err = b.counts(ctx, `SELECT status, count(*) FROM improvement_items
		WHERE author_agent_id=$1 AND created_at >= $2 GROUP BY 1 ORDER BY 2 DESC`, agentID, since); err != nil {
		return f, err
	}
	return f, nil
}

// stuck sind die blockierten Aufgaben — bewusst OHNE Zeitfenster. Eine Aufgabe,
// die seit drei Monaten auf ein Ereignis wartet, das nie kommt, ist genau der
// Befund, den ein Zeitraum verstecken würde.
func (b *Builder) stuck(ctx context.Context, agentID uuid.UUID) ([]StuckTask, error) {
	// Die Frage, mit der der Agent stehen geblieben ist, steht nicht an der
	// Aufgabe, sondern in der Notiz des Uebergangs — dort schreibt Block() sie
	// hin. Sie gehoert dazu: „wartet auf ein Ereignis" ist eine Beobachtung,
	// „wartet darauf, ob der Kunde zurueckkommt" ist ein Befund.
	rows, err := b.Pool.Query(ctx, `SELECT t.id, t.title, COALESCE(t.correlation_key,''),
			COALESCE((SELECT tr.note FROM task_transitions tr
			          WHERE tr.task_id = t.id AND tr.to_state='blocked'
			          ORDER BY tr.id DESC LIMIT 1), ''),
			t.updated_at
		FROM backlog_tasks t WHERE t.agent_id=$1 AND t.state='blocked'
		ORDER BY t.updated_at LIMIT 50`, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []StuckTask{}
	for rows.Next() {
		var st StuckTask
		if err := rows.Scan(&st.ID, &st.Title, &st.CorrelationKey, &st.Question, &st.BlockedSince); err != nil {
			return nil, err
		}
		st.Question = strings.TrimPrefix(st.Question, "blocked: ")
		out = append(out, st)
	}
	return out, rows.Err()
}

// findings sind die stehenden Befunde des Config-Lints — was die mechanischen
// Regeln über diese Config ohnehin schon sagen. Sie kosten nichts und stehen
// bisher nur auf der Agentenseite; in der Akte beantworten sie die erste der
// drei Ursachen, bevor jemand sie von Hand sucht.
func (b *Builder) findings(ctx context.Context, agent agents.Agent) []agents.Finding {
	subjects, err := agents.LintSubjects(ctx, b.Pool, agent.OrgID, b.Skills)
	if err != nil {
		return []agents.Finding{}
	}
	out := []agents.Finding{}
	for _, sub := range subjects {
		if sub.AgentID == agent.ID {
			out = append(out, agents.Lint(sub.Subject)...)
		}
	}
	return out
}

// counts ist die immer gleiche Form „Schlüssel, Anzahl".
func (b *Builder) counts(ctx context.Context, sql string, args ...any) ([]Count, error) {
	rows, err := b.Pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Count{}
	for rows.Next() {
		var c Count
		if err := rows.Scan(&c.Key, &c.Count); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
