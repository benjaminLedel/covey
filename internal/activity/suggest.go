package activity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"covey/internal/llm"
)

// Suggestion is recurring work the log shows an agent could take over (#370).
type Suggestion struct {
	Title          string   `json:"title"`
	Description    string   `json:"description"`
	Pattern        string   `json:"pattern"`
	MinutesPerWeek int      `json:"minutes_per_week"`
	Systems        []string `json:"systems"`
	Agent          string   `json:"agent"`
	Brief          string   `json:"brief"`
	Evidence       []string `json:"evidence"`
}

// Suggestions is the result of one evaluation.
type Suggestions struct {
	Days        int          `json:"days"`
	Sessions    int          `json:"sessions"`
	Suggestions []Suggestion `json:"suggestions"`
	CreatedAt   time.Time    `json:"created_at"`
}

// ErrNoSuggestions: the model's answer could not be read.
var ErrNoSuggestions = errors.New("the model returned no readable suggestions")

const suggestSystem = `You read a person's activity log of the last days — per day, the apps, windows and web pages they worked in, how long, and short excerpts of what they were writing — and find recurring work that an AI agent could take over. The agents live in covey, a platform where agents work like colleagues: they have access to systems (ticketing, mail, repositories, wikis, CRMs …) through connectors, pick up tasks, and hand results back to people.

Suggest only work that the log shows recurring (on several days, or several times a day), that is largely mechanical or follows rules — triage, standard replies, copying between systems, routine checks, recurring reports — and that runs in systems an agent can reach. Not one-off work, not conversations, not decisions that need the person's judgement, not creative work. Better no suggestion than a weak one. At most five, the most worthwhile first.

Answer with JSON only, an array of objects:
[{"title": "short name of the work",
  "description": "what the person does, in one or two sentences",
  "pattern": "how often and when, e.g. 'on 8 of 10 working days, mornings'",
  "minutes_per_week": estimated minutes per week, an integer,
  "systems": ["the systems involved"],
  "agent": "what the agent would do, in one sentence",
  "brief": "a complete job posting for the agent, as the person would write it: the task, the systems it needs, how it should work, when it hands back to a person — 5 to 10 sentences",
  "evidence": ["two or three concrete observations from the log, with day and time"]}]

Write every text in the language given. If there is nothing worth suggesting, answer [].`

// Condense turns sessions into the compact per-day text the model reads:
// per day and app, the time spent, the pages or windows seen most, and a
// couple of excerpts. Two weeks of five-second samples fit in one request
// this way.
func Condense(sessions []Session, loc *time.Location) string {
	type key struct{ day, app, place string }
	type agg struct {
		minutes  float64
		first    time.Time
		windows  map[string]int
		excerpts []string
	}
	groups := map[key]*agg{}
	for _, s := range sessions {
		place := s.App
		if s.URL != "" {
			if u, err := url.Parse(s.URL); err == nil && u.Host != "" {
				place = u.Host
			}
		}
		k := key{s.StartedAt.In(loc).Format("2006-01-02 Mon"), s.App, place}
		g := groups[k]
		if g == nil {
			g = &agg{first: s.StartedAt, windows: map[string]int{}}
			groups[k] = g
		}
		g.minutes += s.EndedAt.Sub(s.StartedAt).Minutes()
		if s.StartedAt.Before(g.first) {
			g.first = s.StartedAt
		}
		if s.Window != "" {
			g.windows[s.Window]++
		}
		if ex := strings.TrimSpace(s.Excerpt); ex != "" && len(g.excerpts) < 2 {
			r := []rune(ex)
			if len(r) > 120 {
				ex = string(r[len(r)-120:])
			}
			g.excerpts = append(g.excerpts, ex)
		}
	}
	keys := make([]key, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].day != keys[j].day {
			return keys[i].day < keys[j].day
		}
		return groups[keys[i]].first.Before(groups[keys[j]].first)
	})
	var b strings.Builder
	day := ""
	for _, k := range keys {
		g := groups[k]
		if g.minutes < 1 {
			continue
		}
		if k.day != day {
			day = k.day
			fmt.Fprintf(&b, "\n%s\n", day)
		}
		fmt.Fprintf(&b, "- from %s, %.0f min, %s", g.first.In(loc).Format("15:04"), g.minutes, k.app)
		if k.place != k.app {
			fmt.Fprintf(&b, " · %s", k.place)
		}
		type wc struct {
			w string
			n int
		}
		var ws []wc
		for w, n := range g.windows {
			ws = append(ws, wc{w, n})
		}
		sort.Slice(ws, func(i, j int) bool { return ws[i].n > ws[j].n })
		for i, w := range ws {
			if i == 3 {
				break
			}
			fmt.Fprintf(&b, " · %q", w.w)
		}
		for _, ex := range g.excerpts {
			fmt.Fprintf(&b, " · content %q", ex)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// Suggest asks the model for agent suggestions from the condensed log.
func Suggest(ctx context.Context, p llm.Provider, sessions []Session, lang string, loc *time.Location) ([]Suggestion, error) {
	if len(sessions) == 0 {
		return nil, ErrNothingToReview
	}
	out, err := p.Complete(ctx, llm.Request{
		Tier:      llm.TierBest,
		Effort:    "medium",
		MaxTokens: 6000,
		System:    suggestSystem,
		Messages:  []llm.Message{{Role: "user", Content: "Language: " + lang + "\n\nActivity log:\n" + Condense(sessions, loc)}},
	})
	if err != nil {
		return nil, err
	}
	return parseSuggestions(out)
}

// parseSuggestions reads the model's JSON, with or without a code fence
// around it.
func parseSuggestions(out string) ([]Suggestion, error) {
	out = strings.TrimSpace(out)
	if i := strings.Index(out, "["); i >= 0 {
		if j := strings.LastIndex(out, "]"); j > i {
			out = out[i : j+1]
		}
	}
	var s []Suggestion
	if err := json.Unmarshal([]byte(out), &s); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNoSuggestions, err)
	}
	kept := s[:0]
	for _, x := range s {
		if strings.TrimSpace(x.Title) != "" && strings.TrimSpace(x.Brief) != "" {
			kept = append(kept, x)
		}
	}
	return kept, nil
}

// SaveSuggestions keeps the seat's latest suggestions.
func (s *Store) SaveSuggestions(ctx context.Context, orgID, humanID uuid.UUID, x Suggestions) (Suggestions, error) {
	body, err := json.Marshal(x.Suggestions)
	if err != nil {
		return Suggestions{}, err
	}
	err = s.pool.QueryRow(ctx, `INSERT INTO human_agent_suggestions (human_id, org_id, days, sessions, suggestions)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (human_id) DO UPDATE SET days = EXCLUDED.days, sessions = EXCLUDED.sessions,
			suggestions = EXCLUDED.suggestions, created_at = now()
		RETURNING created_at`, humanID, orgID, x.Days, x.Sessions, body).Scan(&x.CreatedAt)
	return x, err
}

// LoadSuggestions returns the seat's latest suggestions, or false.
func (s *Store) LoadSuggestions(ctx context.Context, humanID uuid.UUID) (Suggestions, bool, error) {
	var x Suggestions
	var body []byte
	err := s.pool.QueryRow(ctx, `SELECT days, sessions, suggestions, created_at FROM human_agent_suggestions WHERE human_id=$1`,
		humanID).Scan(&x.Days, &x.Sessions, &body, &x.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Suggestions{}, false, nil
	}
	if err != nil {
		return Suggestions{}, false, err
	}
	return x, true, json.Unmarshal(body, &x.Suggestions)
}
