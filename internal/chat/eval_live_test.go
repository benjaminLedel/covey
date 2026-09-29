package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"covey/internal/llm"
	secbuiltin "covey/internal/secrets/builtin"
)

/* The live half of the evaluation (make eval-chat).
 *
 * The same scenarios, through the real Triagieren and Erzaehlen against the
 * real model, with the same hard checks — and a judge. What a check cannot
 * see is whether a sentence sounds like a colleague; a second model, on the
 * cheap tier, grades that from 1 to 5 with a one-line reason. Its score is a
 * number to compare two prompt versions by, not a gate: it fails nothing.
 *
 * It costs money and is off by default. The credential is looked for in this
 * order: ANTHROPIC_API_KEY, CLAUDE_CODE_OAUTH_TOKEN, or an organisation of a
 * covey database (COVEY_EVAL_ORG, with COVEY_MASTER_KEY and
 * COVEY_DATABASE_URL) through llm.Resolve — the path the control plane itself
 * takes. Without any, the test skips and says why. */

const evalDefaultDB = "postgres://covey:covey@localhost:5433/covey?sslmode=disable"

func evalProvider(ctx context.Context) (llm.Provider, string, error) {
	if k := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")); k != "" {
		return evalAnthropic(k, false), "ANTHROPIC_API_KEY", nil
	}
	if k := strings.TrimSpace(os.Getenv("CLAUDE_CODE_OAUTH_TOKEN")); k != "" {
		return evalAnthropic(k, true), "CLAUDE_CODE_OAUTH_TOKEN", nil
	}
	org := strings.TrimSpace(os.Getenv("COVEY_EVAL_ORG"))
	if org == "" {
		return nil, "", fmt.Errorf("no credential: set ANTHROPIC_API_KEY or CLAUDE_CODE_OAUTH_TOKEN, " +
			"or COVEY_EVAL_ORG (an organisation id) with COVEY_MASTER_KEY and COVEY_DATABASE_URL")
	}
	orgID, err := uuid.Parse(org)
	if err != nil {
		return nil, "", fmt.Errorf("COVEY_EVAL_ORG: %w", err)
	}
	url := os.Getenv("COVEY_DATABASE_URL")
	if url == "" {
		url = evalDefaultDB
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, "", err
	}
	store, err := secbuiltin.New(pool, os.Getenv("COVEY_MASTER_KEY"))
	if err != nil {
		return nil, "", fmt.Errorf("secret store: %w", err)
	}
	p, err := llm.Resolve(ctx, store, orgID)
	if err != nil {
		return nil, "", err
	}
	return p, "organisation " + org, nil
}

// live runs one scenario the way covey runs it: a turn, once more on
// failure, one search with the scenario's hits, and a task when it still
// fails (the fallback in internal/httpapi).
func live(ctx context.Context, p llm.Provider, sc evalScenario) evalAusgabe {
	switch sc.Kind {
	case "addressing":
		return evalAusgabe{Angesprochen: angesprochen(sc)}
	case "narration":
		auftrag := sc.Task.Title
		if sc.Task.Body != "" {
			auftrag = sc.Task.Body
		}
		text, err := evalErzaehlen(ctx, p, sc.rahmen(), sc.verlauf(), auftrag, sc.Task.State, sc.Task.Result)
		return evalAusgabe{Text: text, Fehler: err}
	}
	zug := func(suche *Suche) (Entscheidung, error) {
		e, err := evalTriagieren(ctx, p, sc.rahmen(), sc.OrgChart, sc.offen(), sc.fertig(), sc.verlauf(), sc.Message, suche)
		if err != nil {
			e, err = evalTriagieren(ctx, p, sc.rahmen(), sc.OrgChart, sc.offen(), sc.fertig(), sc.verlauf(), sc.Message, suche)
		}
		return e, err
	}
	e, err := zug(nil)
	if err != nil {
		return evalAusgabe{Fehler: fmt.Errorf("triage failed, covey would open a task: %w", err)}
	}
	a := evalAusgabe{Aktion: e.Aktion, Endgueltig: e.Aktion, Text: e.Text}
	if e.Aktion == AktionSuche {
		e2, err := zug(&Suche{Anfrage: e.Anfrage, Treffer: sc.SearchHits})
		if err != nil {
			a.Fehler = err
			return a
		}
		a.Endgueltig, a.Text = e2.Aktion, e2.Text
	}
	return a
}

const richterSystem = `You grade one message that an AI agent wrote in a team chat at work, where it works as a colleague of the people in it.

Score from 1 to 5 how much it sounds like a good colleague in a work chat:
5 — exactly what a helpful colleague would write here: natural, fits the message and the person, the right length.
4 — good, one small thing off.
3 — acceptable but stiff, generic or a bit long.
2 — sounds like a system, a report or a service desk.
1 — talks about its own process ("I have answered …", "the task", "the run"), is wrong for the situation, or is not a chat message at all.

Answer with ONE JSON object and nothing else: {"score": <1-5>, "reason": "<one short line>"}`

type urteil struct {
	Score  int    `json:"score"`
	Reason string `json:"reason"`
}

func richten(ctx context.Context, p llm.Provider, sc evalScenario, a evalAusgabe) (urteil, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "Situation: %s\n", sc.About)
	fmt.Fprintf(&b, "Conversation: %s", sc.Conversation.Kind)
	if sc.gruppe() {
		var wer []string
		for _, m := range sc.Conversation.Members {
			wer = append(wer, m.Name)
		}
		fmt.Fprintf(&b, " with %s", strings.Join(wer, ", "))
	}
	fmt.Fprintf(&b, "\nThe person: %s\nThe agent: %s\n", sc.gegenueber(), sc.Agent.Role)
	if t := evalTon(sc.Tone); t != "" {
		fmt.Fprintf(&b, "Tone the organisation set: %s\n", t)
	}
	for _, h := range sc.History {
		fmt.Fprintf(&b, "Earlier — %s: %s\n", h.Who, h.Text)
	}
	if sc.Kind == "narration" {
		fmt.Fprintf(&b, "The agent did this work: %s\nIt ended %s with: %s\n", sc.Task.Title, sc.Task.State, sc.Task.Result)
		b.WriteString("Now the agent tells the conversation what came out:\n")
	} else {
		fmt.Fprintf(&b, "New message from %s: %s\n", sc.Person.Name, sc.Message)
		if a.Endgueltig == AktionAufgabe {
			b.WriteString("The agent takes this on as work and first says in the chat:\n")
		} else {
			b.WriteString("The agent answers in the chat:\n")
		}
	}
	b.WriteString(a.Text)
	roh, err := p.Complete(ctx, llm.Request{
		Tier: llm.TierFast, MaxTokens: 200, NoThinking: true,
		System: richterSystem, Messages: []llm.Message{{Role: "user", Content: b.String()}},
	})
	if err != nil {
		return urteil{}, err
	}
	s := roh
	if i := strings.Index(s, "{"); i >= 0 {
		s = s[i:]
	}
	if j := strings.LastIndex(s, "}"); j >= 0 {
		s = s[:j+1]
	}
	var u urteil
	if err := json.Unmarshal([]byte(s), &u); err != nil {
		return urteil{}, fmt.Errorf("judge: %w", err)
	}
	return u, nil
}

// TestEvalLive runs the set against the real model (COVEY_EVAL_LLM=1).
func TestEvalLive(t *testing.T) {
	if os.Getenv("COVEY_EVAL_LLM") != "1" {
		t.Skip("the live chat evaluation runs with COVEY_EVAL_LLM=1 (make eval-chat)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	p, wo, err := evalProvider(ctx)
	if err != nil {
		t.Skipf("live chat evaluation skipped: %v", err)
	}

	var rep strings.Builder
	fmt.Fprintf(&rep, "# Chat evaluation — %s\n\nProvider: %s (%s)\n\n", time.Now().Format("2006-01-02 15:04"), p.Name(), wo)
	rep.WriteString("| scenario | action | checks | judge | said |\n|---|---|---|---|---|\n")
	var ok, aktionOK, triagen, summe, gerichtet int
	szenarien := ladeSzenarien(t)
	for _, sc := range szenarien {
		a := live(ctx, p, sc)
		befunde := pruefen(sc, a)
		aktion := "–"
		if sc.Kind == "triage" {
			triagen++
			aktion = string(a.Aktion)
			if a.Endgueltig != a.Aktion {
				aktion += "→" + string(a.Endgueltig)
			}
			if a.Fehler == nil && !enthaelt(befunde, "action") {
				aktionOK++
				aktion += " ✓"
			} else {
				aktion += " ✗"
			}
		}
		pruef := "ok"
		if len(befunde) == 0 {
			ok++
		} else {
			var teile []string
			for _, b := range befunde {
				teile = append(teile, b.String())
			}
			pruef = strings.Join(teile, "; ")
			t.Errorf("%s: %s", sc.Name, pruef)
		}
		richt := "–"
		// Only what is said in the chat is judged: a note goes onto the task.
		gesprochen := sc.Kind == "narration" || a.Endgueltig == AktionAntwort || a.Endgueltig == AktionAufgabe
		if a.Fehler == nil && gesprochen && strings.TrimSpace(a.Text) != "" {
			if u, err := richten(ctx, p, sc, a); err == nil {
				richt = fmt.Sprintf("%d — %s", u.Score, u.Reason)
				summe += u.Score
				gerichtet++
			} else {
				richt = "judge failed: " + err.Error()
			}
		}
		gesagt := a.Text
		if a.Fehler != nil {
			gesagt = "error: " + a.Fehler.Error()
		}
		if sc.Kind == "addressing" && a.Angesprochen != nil {
			gesagt = fmt.Sprintf("addressed: %v", *a.Angesprochen)
		}
		fmt.Fprintf(&rep, "| %s | %s | %s | %s | %s |\n", sc.Name, aktion, zelle(pruef), zelle(richt), zelle(gesagt))
	}
	schnitt := 0.0
	if gerichtet > 0 {
		schnitt = float64(summe) / float64(gerichtet)
	}
	fmt.Fprintf(&rep, "\n**%d/%d scenarios pass all checks · triage action %d/%d · judge %.2f over %d answers**\n",
		ok, len(szenarien), aktionOK, triagen, schnitt, gerichtet)

	ziel := os.Getenv("COVEY_EVAL_REPORT")
	if ziel == "" {
		ziel = filepath.Join(evalDir, "last-report.md")
	}
	if err := os.WriteFile(ziel, []byte(rep.String()), 0o644); err != nil {
		t.Logf("report not written: %v", err)
	}
	fmt.Println(rep.String())
	fmt.Println("report:", ziel)
}

func enthaelt(befunde []befund, check string) bool {
	for _, b := range befunde {
		if b.Check == check {
			return true
		}
	}
	return false
}

// zelle keeps a text inside one table cell.
func zelle(s string) string {
	return strings.ReplaceAll(strings.Join(strings.Fields(s), " "), "|", "\\|")
}
