// Package dream is the agent's night rest.
//
// During the day an agent gathers knowledge and files it away just as it comes
// in: pages titled after an incident, duplicates, notes without a link. In its
// dream it goes through what it collected once more and puts it in order — it
// merges what says the same thing, and renames what names an incident instead
// of a thing. Later on, whatever else a memory needs will follow: linking,
// classifying, clearing out what has gone to seed.
//
// Two properties make the dream more than a maintenance job:
//
//   - It writes. Unasked, at night, with nobody watching. That is why every
//     action records the state before it — what the dream did can be undone
//     individually in the morning.
//   - It is readable and traceable. Not "maintenance performed", but which
//     page, from which title to which, and why.
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

	"covey/internal/llm"
	"covey/internal/memory"
)

// Model: the dream runs on the latest Opus available. The tasks are short but
// require judgement about what the thing behind a note actually is.
const Model = "claude-opus-5"

// MaxTokens generously: from Opus 5 on, the limit caps thinking *and* answer.
const MaxTokens = 16000

// Effort low. Thinking stays on: measured, the model with thinking disabled
// answers in two instead of sixty seconds, but delivers no usable result at
// all — zero renames for a title that clearly needed renaming. A dream runs at
// night; a minute is no argument there, an empty result is.
const Effort = "low"

// MaxPages caps a single run. Whatever is above that is left for the next
// night and is reported in the dream — a dream must not look more complete
// than it was.
const MaxPages = 40

// bodyChars: this much content the model sees per page. The first paragraph
// almost always says what it is about; the rest only costs.
const bodyChars = 400

// staleAfter: a dream that has been "running" for this long did not survive a
// restart of the control plane. It is cleared out on the next start instead of
// listing the agent as dreaming forever.
const staleAfter = 30 * time.Minute

// Action is a single action within a dream.
type Action struct {
	ID       uuid.UUID  `json:"id"`
	Kind     string     `json:"kind"` // retitle | merge
	PageSlug string     `json:"page_slug,omitempty"`
	Before   string     `json:"before,omitempty"`
	After    string     `json:"after,omitempty"`
	Reason   string     `json:"reason,omitempty"`
	UndoneAt *time.Time `json:"undone_at,omitempty"`
}

// Dream is a dream together with its actions.
type Dream struct {
	ID       uuid.UUID `json:"id"`
	AgentID  uuid.UUID `json:"agent_id"`
	Trigger  string    `json:"trigger"` // manual | nightly
	Status   string    `json:"status"`  // running | done | error
	Error    string    `json:"error,omitempty"`
	Phase    string    `json:"phase,omitempty"`
	LookedAt int       `json:"looked_at"`
	// Story is the dream narrative — ornament, not a log. Empty when the dream
	// did nothing.
	Story      string     `json:"story,omitempty"`
	Skipped    int        `json:"skipped"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	Actions    []Action   `json:"actions"`
}

// Store holds the dream history.
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

// ErrAsleep: a dream is already running for this agent. Not an error in the
// narrow sense — the caller should show the running one instead of starting a
// second (every dream costs an LLM call).
var ErrAsleep = fmt.Errorf("agent is already dreaming")

// Begin creates a dream unless one is running. Clears out dreams that did not
// survive a restart first.
func (s *Store) Begin(ctx context.Context, agentID uuid.UUID, trigger string) (Dream, error) {
	if _, err := s.pool.Exec(ctx,
		`UPDATE dreams SET status='error', error='aborted (control plane restarted)',
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

// List returns an agent's dream history, newest first, including the actions.
// Two queries instead of one join: the history is short, and a join over a 1:n
// relation would have to be unfolded by hand here.
func (s *Store) List(ctx context.Context, agentID uuid.UUID, limit int) ([]Dream, error) {
	if limit <= 0 {
		limit = 30
	}
	rows, err := s.pool.Query(ctx,
		`SELECT id, agent_id, trigger, status, error, phase, looked_at, skipped, story, started_at, finished_at
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
			&d.LookedAt, &d.Skipped, &d.Story, &d.StartedAt, &d.FinishedAt); err != nil {
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

// Undo reverts a single action. Only renames are reversible — separating
// merged pages again would mean guessing the content.
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
		return nil // already undone — not an error, just nothing to do
	}
	if kind != "retitle" {
		return fmt.Errorf("%s cannot be undone", kind)
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

// Current returns an agent's running dream, if there is one.
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

// SleepersSince names the agents that have not dreamt since the given point in
// time and own wiki pages — the work list of the nightly run.
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

// --- The dream itself ---

// Provider is the control plane's path to a model — resolved by the caller
// (internal/llm), so that the dream does not care which provider answers.

// Run dreams: merge, then rename. Every outcome — the failure too — leaves a
// terminal state behind, otherwise the agent counts as dreaming forever.
func (s *Store) Run(ctx context.Context, d Dream, provider llm.Provider) {
	fail := func(err error) { s.finish(ctx, d.ID, "error", err.Error()) }

	// 1. Merge — purely computational via the vector index, without a model.
	merged, err := s.mem.Consolidate(ctx, d.AgentID)
	if err != nil {
		fail(err)
		return
	}
	for i := 0; i < merged; i++ {
		s.addAction(ctx, d.ID, Action{Kind: "merge"})
	}

	// 2. Rename — this one needs language understanding.
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

	raw, err := provider.Complete(ctx, llm.Request{
		Tier: llm.TierBest, MaxTokens: MaxTokens, Effort: Effort, System: retitleSystem,
		Messages: []llm.Message{{Role: "user", Content: retitlePrompt(todo)}},
	})
	if err != nil {
		fail(err)
		return
	}
	bySlug := map[string]memory.Entry{}
	for _, p := range todo {
		bySlug[p.Slug] = p
	}
	items := parseRetitle(raw, bySlug)
	// Whoever works unsupervised at night must be able to say why it did
	// nothing. Without the beginning of the raw answer, an empty dream cannot
	// be told apart from a misunderstood one.
	if len(items) == 0 {
		s.log.Info("dream: no rename", "agent", d.AgentID, "checked", len(todo),
			"answer_chars", len(raw), "answer_head", head(raw, 300))
	}
	for _, pr := range items {
		page := bySlug[pr.Slug]
		if err := s.mem.UpdatePage(ctx, page.ID, pr.Title, page.Content); err != nil {
			continue
		}
		s.addAction(ctx, d.ID, Action{Kind: "retitle", PageSlug: pr.Slug,
			Before: page.Title, After: pr.Title, Reason: pr.Reason})
	}
	// 3. Tell what it dreamt of — only if there is anything to tell.
	if len(items) > 0 || merged > 0 {
		if story := s.tellStory(ctx, provider, merged, items, bySlug); story != "" {
			_, _ = s.pool.Exec(ctx, `UPDATE dreams SET story=$2 WHERE id=$1`, d.ID, story)
		}
	}
	s.finish(ctx, d.ID, "done", "")
}

// tellStory has the model tell in two or three sentences what the agent dreamt
// of. Pure ornament: if the call fails, the narrative stays empty and the dream
// still counts as successful — memory upkeep must not fail over a story.
func (s *Store) tellStory(ctx context.Context, provider llm.Provider, merged int, items []retitleItem, bySlug map[string]memory.Entry) string {
	var b strings.Builder
	b.WriteString("What happened this night:\n\n")
	if merged > 0 {
		b.WriteString(fmt.Sprintf("- %d page pair(s) merged because they said the same thing\n", merged))
	}
	for _, it := range items {
		b.WriteString("- renamed: \"" + bySlug[it.Slug].Title + "\" → \"" + it.Title + "\"\n")
	}
	raw, err := provider.Complete(ctx, llm.Request{
		Tier: llm.TierBest, MaxTokens: storyMaxTokens, Effort: Effort, System: storySystem,
		Messages: []llm.Message{{Role: "user", Content: b.String()}},
	})
	if err != nil {
		s.log.Info("dream: narrative not possible", "err", err)
		return ""
	}
	return clampStory(strings.TrimSpace(raw))
}

// storyMaxChars is the emergency brake in case a model reads the sentence
// budget generously. Deliberately far above what five sentences need: the
// outlier gets truncated, not the normal case. A narrative that ends
// mid-sentence reads like a crash — exactly what the first version ran into.
const storyMaxChars = 1400

// clampStory truncates at the last end of sentence before the limit. A period
// only counts when a space follows: otherwise the cut runs straight through
// identifiers containing a period (user names like "first.last", file names,
// version numbers) — happened on the very first run, and it read like a crash.
func clampStory(story string) string {
	r := []rune(story)
	if len(r) <= storyMaxChars {
		return story
	}
	cut := r[:storyMaxChars]
	end := -1
	for i := 0; i < len(cut)-1; i++ {
		if strings.ContainsRune(".!?", cut[i]) && (cut[i+1] == ' ' || cut[i+1] == '\n') {
			end = i
		}
	}
	if end > 200 {
		return strings.TrimSpace(string(cut[:end+1]))
	}
	return strings.TrimSpace(string(cut)) + " …"
}

// storyMaxTokens: a short paragraph. From Opus 5 on, the cap covers thinking
// and answer together, hence not too tight.
const storyMaxTokens = 3000

const storySystem = `An AI agent tidied up its memory in its sleep. You tell what it dreamt of while doing so.

Three to five sentences, in the language of the titles listed. Tell the tidying up as a dream image: merged pages are figures that become one; a renamed title is something that finds its true name again. Pick up the concrete content — the projects, systems and things the titles are about.

Rules:
- Do not invent events that are not in the list. Images yes, claims no.
- No log tone ("I renamed …"), no bullet list, no heading.
- No preamble, no quotation marks around the whole, no emojis.
- Write in the third person about the dreamer or in the first person from its point of view — pick one and stick with it.

Answer with the narrative only.`

const retitleSystem = `You are tidying up the titles of an agent wiki.

The wiki is the long-term memory of an AI agent: one page per entity
(customer, project, system, person, problem, topic). The pages presented have
titles that name an incident instead of a thing — they are too long, carry a
date, or read like a diary entry.

Propose a title for each page that names the entity:
- at most 60 characters, ideally 20 to 45
- no date, no status ("done", "waiting", "as of 30 Jul")
- no full sentence, no judgement — the title is an address, not a message
- keep concrete identifiers (project and repo names, ticket numbers such as
  !100 or #222, tool names) — that is how the agent finds the page again
- keep the language of the previous title

Skip a page when the previous title already names the entity and you do not
honestly improve it. An unchanged proposal is not a result.

Answer with JSON only, without prose before or after:
{"proposals":[{"slug":"…","title":"…","reason":"short justification"}]}`

type retitleItem struct {
	Slug   string `json:"slug"`
	Title  string `json:"title"`
	Reason string `json:"reason"`
}

func retitlePrompt(pages []memory.Entry) string {
	var b strings.Builder
	b.WriteString("Pages:\n\n")
	for _, p := range pages {
		b.WriteString("--- slug: " + p.Slug + "\n")
		b.WriteString("title: " + p.Title + "\n")
		body := strings.Join(strings.Fields(p.Content), " ")
		if r := []rune(body); len(r) > bodyChars {
			body = string(r[:bodyChars]) + " …"
		}
		b.WriteString("content: " + body + "\n\n")
	}
	return b.String()
}

// parseRetitle pulls the proposals out of the answer and discards what is no
// good: invented slugs, empty or unchanged titles, and anything that would
// itself pass as a diary title again — a proposal that does not fix the finding
// is none.
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

// --- Nightly run ---

// ProviderFor resolves the control-plane model access for an agent. Passed in
// as a function so that this package needs to know neither the registry nor the
// secrets broker — it dreams, it does not look for keys.
type ProviderFor func(ctx context.Context, agentID uuid.UUID) (llm.Provider, bool)

// nightlyCheck: how often it is checked whether dream time has been reached.
// Fine-grained enough that a restart shortly before the hour does not swallow
// the night, coarse enough that it does not stand out.
const nightlyCheck = 5 * time.Minute

// RunNightly lets every agent dream once per night as soon as the local time
// `at` (format "03:00") has passed. Deliberately serial: letting twenty agents
// dream at once would mean twenty concurrent LLM calls, and nobody waits for
// the result at night.
//
// Who has already dreamt, the run reads off the history itself — no separate
// marker that could drift apart from it.
func (s *Store) RunNightly(ctx context.Context, at string, providerFor ProviderFor, log *slog.Logger) {
	hh, mm, err := parseClock(at)
	if err != nil {
		log.Warn("dream time unreadable — nightly run off", "at", at, "err", err)
		return
	}
	log.Info("nightly run active", "at", at)
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
			continue // the night has not come that far yet
		}
		ids, err := s.SleepersSince(ctx, since)
		if err != nil {
			log.Warn("nightly run: sleeping agents cannot be determined", "err", err)
			continue
		}
		for _, id := range ids {
			provider, ok := providerFor(ctx, id)
			if !ok {
				continue // without a credential no dream; no reason to make noise
			}
			d, err := s.Begin(ctx, id, "nightly")
			if err != nil {
				if err != ErrAsleep {
					log.Warn("nightly run: dream cannot be started", "agent", id, "err", err)
				}
				continue
			}
			log.Info("agent is dreaming", "agent", id)
			s.Run(ctx, d, provider)
		}
	}
}

// parseClock reads "HH:MM".
func parseClock(s string) (int, int, error) {
	t, err := time.Parse("15:04", strings.TrimSpace(s))
	if err != nil {
		return 0, 0, err
	}
	return t.Hour(), t.Minute(), nil
}

// head truncates rune-safely for log output.
func head(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
