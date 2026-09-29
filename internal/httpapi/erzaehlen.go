package httpapi

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"covey/internal/chat"
)

/* Erzählen: was ein Agent im Gespräch sagt, wenn eine Aufgabe aus dem Gespräch
 * fertig ist (#411), und wohin sich eine Aufgabe überhaupt meldet (#440).
 *
 * Ein Lauf endet mit einem Ergebnis, geschrieben für die Akte — Überschriften,
 * Aufzählungen, der Ton eines Protokolls. Ein Kollege erzählt, was herauskam.
 * Das tut ein zweiter billiger Zug in der Control Plane, mit denselben Grenzen
 * wie die Triage: kein Zielsystem, keine Zugangsdaten, nur das Ergebnis und
 * das Gespräch. Was er sagt, steht an der Aufgabe (`said`) und als Nachricht
 * im Gespräch; das Ergebnis bleibt, einen Tipp entfernt, damit niemand dem
 * Satz glauben muss.
 *
 * Zurückgemeldet wird nur, was aus einem Gespräch kam, und nur dorthin
 * (chat/report.go). Die Frage einer Aufgabe von anderswo geht an den
 * Vorgesetzten des Agenten, in ihr direktes Gespräch.
 *
 * Eine Schleife statt eines Hakens am Abschluss: Ein Lauf endet im
 * Orchestrator, und ein Neustart zwischen Ende und Meldung darf sie nicht
 * verlieren. Was noch nicht gemeldet ist, steht in der Datenbank.
 */

// erzaehlTakt: wie oft nachgesehen wird. Ein Ergebnis, das zehn Sekunden
// später erzählt wird, ist nicht spät — der Lauf davor dauerte Minuten.
const erzaehlTakt = 5 * time.Second

// erzaehlFenster: Was länger als einen Tag zurückliegt, wird nicht mehr
// gemeldet — wer die Triage einschaltet, bekommt nicht die Geschichte der
// letzten Monate nacherzählt.
const erzaehlFenster = 24 * time.Hour

// erzaehlFrist: ein Zug, der länger braucht, hat nichts gesagt.
const erzaehlFrist = 60 * time.Second

// ErzaehlSchleife läuft, bis ctx endet.
func (s *Server) ErzaehlSchleife(ctx context.Context) {
	if s.Chat == nil || s.Pool == nil {
		return
	}
	t := time.NewTicker(erzaehlTakt)
	defer t.Stop()
	for {
		s.Nacherzaehlen(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Nacherzaehlen meldet, was fertig oder geparkt und noch nicht gemeldet ist.
// Öffentlich, damit ein Test nicht auf den Takt warten muss.
func (s *Server) Nacherzaehlen(ctx context.Context) {
	berichte, err := s.Chat.DueReports(ctx, erzaehlFenster, 10)
	if err != nil {
		s.Log.Warn("narration: could not look for finished tasks of conversations", "err", err)
	}
	for _, b := range berichte {
		s.melden(ctx, b)
	}
	fragen, err := s.Chat.DueQuestions(ctx, erzaehlFenster, 20)
	if err != nil {
		s.Log.Warn("narration: could not look for parked questions", "err", err)
	}
	for _, f := range fragen {
		s.frageZustellen(ctx, f)
	}
}

/* melden writes a task's outcome into its conversation. With the triage on,
 * the agent tells it first (erzaehle); without a model, without the triage
 * or when telling failed, the report itself is the message, as before #411. */
func (s *Server) melden(ctx context.Context, b chat.Report) {
	text := b.Said
	if b.Triage && !b.Told {
		text = s.erzaehle(ctx, b)
	}
	if strings.TrimSpace(text) == "" {
		text = b.Outcome
	}
	_, neu, err := s.Chat.Post(ctx, chat.Message{
		ID: chat.ReportID(b.TaskID, b.Kind()), ConversationID: b.ConversationID,
		AuthorKind: chat.MemberAgent, AuthorID: &b.AgentID, Text: text, Kind: b.Kind(), TaskID: &b.TaskID,
	})
	if err != nil {
		s.Log.Warn("narration: the report was not written", "task", b.TaskID, "err", err)
		return
	}
	if neu {
		s.chatEreignis(b.OrgID, b.AgentID, b.ConversationID, "said", map[string]string{"task_id": b.TaskID.String()})
	}
}

/* frageZustellen delivers a parked task's question: into the conversation the
 * task came from, or, for a task from anywhere else, into the direct
 * conversation between the agent and its human supervisor — opened for it
 * the first time. The message's id is derived from the blocked edge, so the
 * question is delivered once. */
func (s *Server) frageZustellen(ctx context.Context, f chat.Question) {
	var convID uuid.UUID
	switch {
	case f.ConversationID != nil:
		convID = *f.ConversationID
	case f.Supervisor != nil:
		id, err := s.Chat.Direct(ctx, f.OrgID, chat.Agent(f.AgentID), chat.Human(*f.Supervisor), nil)
		if err != nil {
			s.Log.Warn("question: the supervisor's conversation could not be opened", "task", f.TaskID, "err", err)
			return
		}
		convID = id
	default:
		return
	}
	_, neu, err := s.Chat.Post(ctx, chat.Message{
		ID: chat.QuestionID(f.TransitionID), ConversationID: convID, AuthorKind: chat.MemberAgent, AuthorID: &f.AgentID,
		Text: f.Text, Kind: chat.MessageQuestion, TaskID: &f.TaskID,
		Meta: map[string]string{"transition": strconv.FormatInt(f.TransitionID, 10)},
	})
	if err != nil {
		s.Log.Warn("question: not delivered", "task", f.TaskID, "err", err)
		return
	}
	if neu {
		s.chatEreignis(f.OrgID, f.AgentID, convID, "question", map[string]string{"task_id": f.TaskID.String()})
	}
}

// erzaehle sagt eine Aufgabe an. Ohne Modell, ohne Ergebnis oder bei einem
// Fehler bleibt der Satz leer — und das wird trotzdem vermerkt: Dann steht der
// Bericht selbst im Gespräch, wie vor #411.
func (s *Server) erzaehle(ctx context.Context, b chat.Report) string {
	gesagt := ""
	if b.Outcome != "" {
		zctx, abbrechen := context.WithTimeout(ctx, erzaehlFrist)
		gesagt = s.erzaehlText(zctx, b)
		abbrechen()
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE backlog_tasks SET said = $2, said_at = now()
		WHERE id = $1 AND said_at IS NULL`, b.TaskID, gesagt); err != nil {
		s.Log.Warn("narration: not stored", "task", b.TaskID, "err", err)
	}
	return gesagt
}

func (s *Server) erzaehlText(ctx context.Context, b chat.Report) string {
	provider, err := s.resolveOrgLLM(ctx, b.OrgID)
	if err != nil {
		return ""
	}
	verlauf, err := s.verlaufFuer(ctx, b.ConversationID, b.AgentID, uuid.Nil, triageKontext)
	if err != nil {
		verlauf = nil
	}
	auftrag := b.Title
	if b.Body != "" && b.Body != b.Title {
		auftrag = b.Body
	}
	// The person the task came from (origin chat:<email>), #412.
	gegenueber := s.gegenueberVon(ctx, b.OrgID, strings.TrimPrefix(b.Origin, "chat:"))
	rahmen := chat.Rahmen{Rolle: s.rolleVon(ctx, b.AgentID), Seele: s.seeleVon(ctx, b.AgentID),
		Gegenueber: gegenueber, Ton: s.tonVon(ctx, b.AgentID)}
	// In a group everybody reads the retelling, not only who asked (#457).
	if conv, err := s.Chat.Get(ctx, b.ConversationID); err == nil {
		rahmen.Raum = chat.Raum(conv, b.AgentID, false)
	}
	text, err := chat.Erzaehlen(ctx, provider, rahmen, verlauf, auftrag, b.State, b.Outcome)
	if err != nil {
		s.Log.Warn("narration failed — the report stands on its own", "task", b.TaskID, "err", err)
		return ""
	}
	return text
}
