// Package workrecord assembles the work record of a colleague: facts that the
// control plane itself wrote down, per agent and time range
// (spec/21-operations-and-improvement.md).
//
// Why not just hand out the recordings when someone wants to know how an
// agent works — the obvious way is wrong on three counts at once:
// recordings carry ticket and mail content of other departments, a
// reader would thereby be an exfiltration path through the whole org chart; text
// from a target system that reaches the agent who proposes configurations is the
// injection path from spec/04 aimed at the most valuable target of the platform;
// and a month of recordings fits no reasonable price into a context
// window.
//
// So: facts. What was counted, not what was said. With TWO honest exceptions,
// and both are one line instead of a history:
//
//   - The TASK TITLES. These often come from the wake source and can therefore
//     carry a ticket subject.
//   - The QUESTION of a stuck task (StuckTask.Question). The assessed agent
//     writes it himself in his covey/block directive, and in it he regularly
//     quotes what he is waiting for — so also text from a target system.
//
// Both stay in, because the record is unreadable without them: "waiting for an
// event" is an observation, "waiting to see whether the customer comes back" is
// a finding. And both are named HERE instead of discovered later — a record
// that promises "only facts" while carrying two free-text fields is believed
// exactly where one would have to check it.
//
// The rest of the protection lies elsewhere: that this text does not steer the
// reader stands as an instruction in ReviewDoc (internal/agents/compile.go) —
// and behind it the promise that carries the whole thing, namely that a proposal
// does not run, but is signed by a human.
//
// The package stands deliberately next to httpapi and not inside it: the same
// record is later read by covey Doctor over covey/work_record, and that sits in
// the orchestrator.
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

// Record is the work record. Eight sections, each from a named source. Only two
// fields in it are free text: task titles and StuckTask.Question — see the
// package head, that is where they stand with their origin.
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

	// Notes names what was cut short. A record that silently stops at 200 tasks
	// reads like a complete one.
	Notes []string `json:"notes,omitempty"`
}

// Count is a line "label → count".
type Count struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

// Throughput: what came in and what became of it.
type Throughput struct {
	ByState  []Count    `json:"by_state"`
	ByOrigin []Count    `json:"by_origin"`
	Tasks    []TaskLine `json:"tasks"`
}

// TaskLine is one task as one line — with the title, which is the one honest
// exception of this package.
type TaskLine struct {
	ID         uuid.UUID  `json:"id"`
	Title      string     `json:"title"`
	State      string     `json:"state"`
	Origin     string     `json:"origin"`
	CreatedAt  time.Time  `json:"created_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	CostUSD    float64    `json:"cost_usd"`
}

// ActionCount: which actions were executed, succeeded and failed.
type ActionCount struct {
	Action string `json:"action"`
	OK     int    `json:"ok"`
	Failed int    `json:"failed"`
}

// Indicator is a counting rule of the agent from his KPIS.md, evaluated.
//
// Deliberately without history and without trend, unlike the price list of the
// UI (internal/httpapi/indicators.go): here one reads, one does not compare —
// and a sparkline in a record is a series of numbers that nobody can
// recompute.
type Indicator struct {
	Key    string `json:"key"`
	Title  string `json:"title"`
	Goal   int    `json:"goal,omitempty"`
	Period string `json:"period,omitempty"`
	Count  int64  `json:"count"`
	// UnitUSD is missing while too little was counted: a unit price from two
	// data points is noise, and the platform therefore does not release it at
	// all (observability.UnitCost).
	UnitUSD *float64 `json:"unit_usd,omitempty"`
}

type Cost struct {
	TotalUSD float64 `json:"total_usd"`
	// Tasks is the number of tasks with costs — the denominator behind the
	// average, so that it is recomputable instead of believed.
	Tasks      int     `json:"tasks"`
	PerTaskUSD float64 `json:"per_task_usd"`
}

// Friction: where the agent was stopped, and where he himself was rejected.
type Friction struct {
	// Approvals are the approvals that his actions triggered, by
	// outcome.
	Approvals []Count `json:"approvals"`
	// Denied are the attempts to do something forbidden, by action.
	Denied []Count `json:"denied"`
	// Proposals are the open items that THIS agent wrote, by outcome. The
	// rejection rate on one's own proposals is the number that says whether
	// covey Doctor is any good — and it stands in his own record as it does
	// for everyone else (spec/21).
	Proposals []Count `json:"proposals"`
}

// StuckTask is the failure mode nobody sees because nothing fails: a task waits
// for an event that never comes.
type StuckTask struct {
	ID             uuid.UUID `json:"id"`
	Title          string    `json:"title"`
	CorrelationKey string    `json:"correlation_key"`
	// Question is the agent's own text from his covey/block directive — one of
	// the two free-text fields of the record (package head).
	Question     string    `json:"question,omitempty"`
	BlockedSince time.Time `json:"blocked_since"`
}

// maxTaskLines bounds the list of lines. What lies above it stands in
// the counts — and in Notes, so that the
// cut does not look like completeness.
const maxTaskLines = 200

// Builder holds what the record needs. Skills may be nil: the lint rules that
// read skills then fall away.
type Builder struct {
	Pool     *pgxpool.Pool
	Registry *agents.Registry
	Obs      *observability.Store
	Skills   agents.SkillLookup
}

// Build assembles the record for one agent and one time range.
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

// throughput reads the tasks of the time range: counts over everything, lines
// for the newest ones. The costs come out along the way — they hang on the same
// tasks, and two passes over the same set would be two truths.
func (b *Builder) throughput(ctx context.Context, agentID uuid.UUID, since time.Time) (Throughput, Cost, error) {
	var tp Throughput
	var cost Cost

	byState, err := b.counts(ctx, `SELECT state, count(*) FROM backlog_tasks
		WHERE agent_id=$1 AND created_at >= $2 GROUP BY 1 ORDER BY 2 DESC`, agentID, since)
	if err != nil {
		return tp, cost, err
	}
	tp.ByState = byState

	// The origin is cut at the colon: `agent:qa` and
	// `continuation:<uuid>` are classes, not single values — otherwise the
	// grouping would have as many rows as there are continuations.
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

	// The costs over the WHOLE period, not over the rows shown.
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

// aborts answers "why did runs end" with the four reasons there are: turn
// limit, error, budget, kill switch. All from the recording that the control
// plane itself wrote.
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

// work counts the executed actions, split into succeeded and failed. The split
// is the point: twenty attempts and zero successes look like operation in a
// total.
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

// indicators evaluates the counting rules of the agent from his own KPIS.md.
//
// A parse error does not cost the record: a config that was saved before the
// parser should not produce an empty page. It shows up in the Notes
// instead.
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
	// The agent's own proposals — written BY this agent, not about him.
	if f.Proposals, err = b.counts(ctx, `SELECT status, count(*) FROM improvement_items
		WHERE author_agent_id=$1 AND created_at >= $2 GROUP BY 1 ORDER BY 2 DESC`, agentID, since); err != nil {
		return f, err
	}
	return f, nil
}

// stuck are the blocked tasks — deliberately WITHOUT a time window. A task that
// has waited three months for an event that never comes is exactly the finding
// that a time window would hide.
func (b *Builder) stuck(ctx context.Context, agentID uuid.UUID) ([]StuckTask, error) {
	// The question the agent got stuck with does not stand on the task but in
	// the note of the transition — that is where Block() writes it. It belongs
	// here: "waiting for an event" is an observation, "waiting to see whether
	// the customer comes back" is a finding.
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

// findings are the standing findings of the config lint — what the mechanical
// rules already say about this config anyway. They cost nothing and so far
// stand only on the agent's page; in the record they answer the first of the
// three causes before someone looks for them by hand.
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

// counts is the always same shape "key, count".
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
