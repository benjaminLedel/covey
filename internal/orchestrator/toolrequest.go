package orchestrator

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"covey/internal/agents"
	"covey/internal/daemon"
)

// toolRequest takes the request for a tool (covey/request_tool).
//
// An agent that lacks a package had no way to say so. It is root nowhere,
// apt is not for it, and the workspace stands until someone rebuilds the
// image. What it did instead sat in its home: ~/aptroot with sources.list,
// resolved package URIs and unpacked .debs — unreproducible, unrecorded,
// and carried along on every sync.
//
// The platform fetches nothing here. It writes the request where a human
// sees it, with the evidence beside it: which task it was missing for. The
// decision is made by whoever owns the Dockerfile, and the answer then
// holds for every agent of the profile instead of this one home.
func (o *Orchestrator) toolRequest(ctx context.Context, agent agents.Agent, taskID uuid.UUID, req daemon.RequestTool) daemon.InjectTool {
	fail := func(msg string) daemon.InjectTool {
		return daemon.InjectTool{RequestID: req.RequestID, OK: false, Error: msg}
	}
	// The proxy already checks this (actionproxy.go), and it stands here all
	// the same: this function hangs off a protocol message, and a message can
	// come from something other than the path we had in mind. An open item
	// titled `Werkzeug fehlt: ` is a decision nobody could reach from
	// the list alone.
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
		// The title is the list a maintainer skims: what is missing, for whom.
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

// belegen writes the rationale so that it stays readable without the agent:
// who, in which workspace, for what. The workspace belongs in it, because the
// answer is a line in ITS Dockerfile — and a tool that lands in the wrong
// profile weighs on all the others.
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
