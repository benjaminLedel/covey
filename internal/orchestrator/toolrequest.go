package orchestrator

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"covey/internal/agents"
	"covey/internal/daemon"
)

// toolRequest nimmt die Bitte um ein Werkzeug entgegen (covey/request_tool).
//
// Ein Agent, dem ein Paket fehlt, hatte keinen Weg, das zu sagen. Er ist
// nirgends root, apt ist nicht für ihn, und der Arbeitsplatz steht fest, bis
// jemand ein Image neu baut. Was er stattdessen tat, lag in seinem Home:
// ~/aptroot mit sources.list, aufgelösten Paket-URIs und entpackten .debs —
// unreproduzierbar, unaufgeschrieben, und bei jedem Sync mitgetragen.
//
// Die Plattform beschafft hier nichts. Sie schreibt die Bitte dorthin, wo ein
// Mensch sie sieht, mit dem Beleg daneben: an welcher Aufgabe es gefehlt hat.
// Entschieden wird von dem, der das Dockerfile verantwortet, und die Antwort
// gilt dann für alle Agenten des Profils statt für dieses eine Home.
func (o *Orchestrator) toolRequest(ctx context.Context, agent agents.Agent, taskID uuid.UUID, req daemon.RequestTool) daemon.InjectTool {
	fail := func(msg string) daemon.InjectTool {
		return daemon.InjectTool{RequestID: req.RequestID, OK: false, Error: msg}
	}
	// Der Proxy prüft das schon (actionproxy.go), und trotzdem steht es hier:
	// Diese Funktion hängt an einer Protokollnachricht, und eine Nachricht kann
	// von etwas anderem kommen als von dem Weg, den wir uns gedacht haben. Ein
	// offener Punkt mit dem Titel „Werkzeug fehlt: " wäre für niemanden zu
	// entscheiden.
	werkzeug := strings.TrimSpace(req.Tool)
	if werkzeug == "" {
		return fail("tool missing")
	}
	if o.Registry == nil {
		return fail("no registry")
	}

	item := agents.ImprovementItem{
		OrgID:   agent.OrgID,
		AgentID: agent.ID,
		Kind:    agents.KindToolRequest,
		// Der Titel ist die Liste, die ein Betreuer überfliegt: Was fehlt, wem.
		Title:         "Werkzeug fehlt: " + werkzeug,
		Rationale:     belegen(agent, req),
		AuthorAgentID: &agent.ID,
	}
	if taskID != uuid.Nil {
		id := taskID
		item.TaskID = &id
	}
	angelegt, err := o.Registry.CreateImprovement(ctx, item)
	if err != nil {
		o.Log.Warn("tool request not filed", "agent", agent.ID, "tool", werkzeug, "err", err)
		return fail(err.Error())
	}
	o.Log.Info("tool request filed", "agent", agent.ID, "tool", werkzeug, "item", angelegt.ID)
	o.notifyImprovement(ctx, angelegt)
	return daemon.InjectTool{RequestID: req.RequestID, OK: true, ID: angelegt.ID.String()}
}

// belegen schreibt die Begründung so, dass sie ohne den Agenten lesbar bleibt:
// wer, in welchem Arbeitsplatz, wofür. Der Arbeitsplatz gehört dazu, weil die
// Antwort eine Zeile in SEINEM Dockerfile ist — und ein Werkzeug, das im
// falschen Profil landet, wiegt für alle anderen mit.
func belegen(agent agents.Agent, req daemon.RequestTool) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(req.Why))
	if b.Len() > 0 {
		b.WriteString("\n\n")
	}
	arbeitsplatz := agent.SandboxImage
	if arbeitsplatz == "" {
		arbeitsplatz = "(die Voreinstellung der Instanz)"
	}
	b.WriteString("Arbeitsplatz: " + arbeitsplatz)
	return b.String()
}
