package httpapi

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"

	"covey/internal/chat"
)

/* Die Suche der Triage (#416): Der Zug sieht nur das Ende des Gesprächs, die
 * letzten Vorgänge und das Organigramm, wie es ist. Wonach er fragt, sucht
 * covey im ganzen Gespräch, im Backlog des Agenten (#440) und im Organigramm
 * — dort mit einem Tippfehler Spielraum, denn „Vorrauschauend" ist Volker
 * Vorausschauend, und ein Zug, der den Namen nicht erkennt, kann ihn auch
 * nicht zuordnen. */

// suchTreffer: so viele Stellen aus dem Gespräch gehen an den zweiten Zug,
// und noch einmal so viele aus dem Backlog.
const suchTreffer = 8

func (s *Server) suchen(ctx context.Context, agentID, convID uuid.UUID, organisation, anfrage string) []string {
	woerter := suchWoerter(anfrage)
	var out []string
	out = append(out, orgTreffer(organisation, woerter)...)
	if treffer, err := s.Chat.Search(ctx, convID, woerter, suchTreffer); err == nil {
		for _, m := range treffer {
			b := beitrag(m, agentID)
			out = append(out, fmt.Sprintf("conversation, %s, %s: %s", m.CreatedAt.Format("2006-01-02"), b.Wer, m.Text))
		}
	} else {
		s.Log.Warn("triage search: the conversation could not be searched", "agent", agentID, "err", err)
	}
	out = append(out, s.backlogTreffer(ctx, agentID, woerter)...)
	return out
}

/* backlogTreffer: the agent's tasks whose title, result or error carries a
 * word, newest first — also archived ones, which the lists leave out, since
 * "what came of the Globex invoice in spring?" asks exactly for those. It is
 * the agent's own backlog, as the lists are; nothing of another agent. */
func (s *Server) backlogTreffer(ctx context.Context, agentID uuid.UUID, woerter []string) []string {
	muster := chat.Muster(woerter)
	if len(muster) == 0 || s.Pool == nil {
		return nil
	}
	rows, err := s.Pool.Query(ctx, `SELECT title, state, updated_at,
		       CASE WHEN state = 'failed' THEN coalesce(error, '') ELSE coalesce(result, '') END
		  FROM backlog_tasks
		 WHERE agent_id = $1
		   AND (title ILIKE ANY($2::text[]) OR coalesce(result, '') ILIKE ANY($2::text[])
		        OR coalesce(error, '') ILIKE ANY($2::text[]))
		 ORDER BY updated_at DESC LIMIT $3`, agentID, muster, suchTreffer)
	if err != nil {
		s.Log.Warn("triage search: the backlog could not be searched", "agent", agentID, "err", err)
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var titel, zustand, ausgang string
		var wann time.Time
		if rows.Scan(&titel, &zustand, &wann, &ausgang) != nil {
			continue
		}
		zeile := fmt.Sprintf("your task, %s, %s: %s", wann.Format("2006-01-02"), zustand, titel)
		if ausgang = strings.TrimSpace(ausgang); ausgang != "" {
			zeile += " — outcome: " + ausgang
		}
		out = append(out, zeile)
	}
	return out
}

// suchWoerter: die Wörter einer Anfrage, klein, ab drei Zeichen.
func suchWoerter(anfrage string) []string {
	var out []string
	for _, w := range strings.FieldsFunc(strings.ToLower(anfrage), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if len([]rune(w)) >= 3 {
			out = append(out, w)
		}
	}
	return out
}

// orgTreffer: die Zeilen des Organigramms, in denen ein Wort vorkommt — oder
// ein Wort, das bis auf einen Tippfehler gleich ist (ab fünf Zeichen, bis zu
// zwei Änderungen).
func orgTreffer(organisation string, woerter []string) []string {
	var out []string
	for _, zeile := range strings.Split(organisation, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(zeile), "- ") {
			continue
		}
		klein := strings.ToLower(zeile)
		passt := false
		for _, w := range woerter {
			if strings.Contains(klein, w) {
				passt = true
				break
			}
			if len([]rune(w)) < 5 {
				continue
			}
			for _, z := range strings.FieldsFunc(klein, func(r rune) bool { return !unicode.IsLetter(r) }) {
				if abstand(w, z) <= 2 {
					passt = true
					break
				}
			}
			if passt {
				break
			}
		}
		if passt {
			out = append(out, "org chart: "+strings.TrimPrefix(strings.TrimSpace(zeile), "- "))
		}
		if len(out) >= 6 {
			break
		}
	}
	return out
}

// abstand ist die Levenshtein-Distanz zweier Wörter, in Zeichen gezählt.
func abstand(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	if d := len(ra) - len(rb); d > 2 || d < -2 {
		return 3
	}
	vorher := make([]int, len(rb)+1)
	jetzt := make([]int, len(rb)+1)
	for j := range vorher {
		vorher[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		jetzt[0] = i
		for j := 1; j <= len(rb); j++ {
			kosten := 1
			if ra[i-1] == rb[j-1] {
				kosten = 0
			}
			jetzt[j] = min(vorher[j]+1, jetzt[j-1]+1, vorher[j-1]+kosten)
		}
		vorher, jetzt = jetzt, vorher
	}
	return vorher[len(rb)]
}
