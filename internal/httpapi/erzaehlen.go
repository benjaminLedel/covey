package httpapi

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	"covey/internal/chat"
)

/* Erzählen: was ein Agent im Chat sagt, wenn eine Aufgabe aus dem Chat fertig
 * ist (#411).
 *
 * Ein Lauf endet mit einem Ergebnis, geschrieben für die Akte — Überschriften,
 * Aufzählungen, der Ton eines Protokolls —, und das stand bisher als die
 * Antwort des Agenten im Gespräch. Ein Kollege erzählt, was herauskam. Das tut
 * ein zweiter billiger Zug in der Control Plane, mit denselben Grenzen wie die
 * Triage: kein Zielsystem, keine Zugangsdaten, nur das Ergebnis und das
 * Gespräch. Was er sagt, steht an der Aufgabe (`said`); das Ergebnis bleibt,
 * einen Tipp entfernt, damit niemand dem Satz glauben muss.
 *
 * Eine Schleife statt eines Hakens am Abschluss: Ein Lauf endet im
 * Orchestrator, und ein Neustart zwischen Ende und Erzählen darf den Satz
 * nicht verlieren. Was noch nicht erzählt ist, steht in der Datenbank.
 */

// erzaehlTakt: wie oft nachgesehen wird. Ein Ergebnis, das zehn Sekunden
// später erzählt wird, ist nicht spät — der Lauf davor dauerte Minuten.
const erzaehlTakt = 5 * time.Second

// erzaehlFenster: Wer die Triage einschaltet, bekommt nicht die Geschichte
// der letzten Monate nacherzählt.
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

type zuErzaehlen struct {
	id, orgID, agentID uuid.UUID
	titel, rumpf       string
	origin             string
	zustand, ergebnis  string
}

// Nacherzaehlen erzählt, was fertig und noch nicht erzählt ist. Öffentlich,
// damit ein Test nicht auf den Takt warten muss.
func (s *Server) Nacherzaehlen(ctx context.Context) {
	rows, err := s.Pool.Query(ctx, `
		SELECT t.id, t.org_id, t.agent_id, t.title, coalesce(t.body, ''), t.origin, t.state,
		       CASE WHEN t.state = 'done' THEN coalesce(t.result, '') ELSE coalesce(t.error, '') END
		  FROM backlog_tasks t JOIN organizations o ON o.id = t.org_id
		 WHERE t.origin LIKE 'chat:%' AND t.state IN ('done', 'failed')
		   AND t.said_at IS NULL AND t.archived_at IS NULL
		   AND o.chat_triage = $1
		   AND t.updated_at > now() - make_interval(secs => $2)
		 ORDER BY t.updated_at
		 LIMIT 10`, string(chat.TriageOn), erzaehlFenster.Seconds())
	if err != nil {
		s.Log.Warn("narration: could not look for finished chat tasks", "err", err)
		return
	}
	var liste []zuErzaehlen
	for rows.Next() {
		var z zuErzaehlen
		if rows.Scan(&z.id, &z.orgID, &z.agentID, &z.titel, &z.rumpf, &z.origin, &z.zustand, &z.ergebnis) == nil {
			liste = append(liste, z)
		}
	}
	rows.Close()
	for _, z := range liste {
		s.erzaehle(ctx, z)
	}
}

// erzaehle sagt eine Aufgabe an. Ohne Modell, ohne Ergebnis oder bei einem
// Fehler bleibt der Satz leer — und das wird trotzdem vermerkt: Die
// Mitteilung wartet auf said_at und geht dann mit dem Bericht hinaus, wie
// vor #411.
func (s *Server) erzaehle(ctx context.Context, z zuErzaehlen) {
	gesagt := ""
	if z.ergebnis != "" {
		zctx, abbrechen := context.WithTimeout(ctx, erzaehlFrist)
		gesagt = s.erzaehlText(zctx, z)
		abbrechen()
	}
	tag, err := s.Pool.Exec(ctx, `UPDATE backlog_tasks SET said = $2, said_at = now()
		WHERE id = $1 AND said_at IS NULL`, z.id, gesagt)
	if err != nil {
		s.Log.Warn("narration: not stored", "task", z.id, "err", err)
		return
	}
	if tag.RowsAffected() == 1 && gesagt != "" {
		s.chatEreignis(z.orgID, z.agentID, "said", map[string]string{"task_id": z.id.String()})
	}
}

func (s *Server) erzaehlText(ctx context.Context, z zuErzaehlen) string {
	provider, err := s.resolveOrgLLM(ctx, z.orgID)
	if err != nil {
		return ""
	}
	verlauf, err := s.Chat.Gespraech(ctx, z.agentID, triageKontext)
	if err != nil {
		verlauf = nil
	}
	auftrag := z.titel
	if z.rumpf != "" && z.rumpf != z.titel {
		auftrag = z.rumpf
	}
	// The person the task came from (origin chat:<email>), #412.
	gegenueber := s.gegenueberVon(ctx, z.orgID, strings.TrimPrefix(z.origin, "chat:"))
	text, err := chat.Erzaehlen(ctx, provider, s.rolleVon(ctx, z.agentID), s.seeleVon(ctx, z.agentID), gegenueber,
		verlauf, auftrag, z.zustand, z.ergebnis)
	if err != nil {
		s.Log.Warn("narration failed — the report stands on its own", "task", z.id, "err", err)
		return ""
	}
	return text
}
