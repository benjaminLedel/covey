package orchestrator

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"covey/internal/agents"
	"covey/internal/chat"
	"covey/internal/daemon"
	"covey/internal/observability"
	"covey/internal/org"
)

// messageHuman is the control-plane side of covey/message (#537): the agent
// tells a person of its organisation something, in their direct
// conversation.
//
// Until this existed an agent reached a human only by stopping — a parked
// question, one per run, resumed by the answer — or sideways, through a
// result nobody is told of. A message is neither: the run goes on, the
// person is notified like for any other message (the push notifier reads
// the agents' side of conversations from the database), and their answer
// comes back the usual way, as a message or a task.
//
// Fail-closed where it matters: the person has to be one of the
// organisation's humans, named unambiguously; an empty recipient is the
// agent's supervisor and nobody else. An agent without a supervisor and
// without a name gets a refusal that says what to do, not a guess.
func (o *Orchestrator) messageHuman(ctx context.Context, agent agents.Agent, taskID uuid.UUID, req daemon.RequestMessage) daemon.InjectMessage {
	fail := func(m string) daemon.InjectMessage {
		return daemon.InjectMessage{RequestID: req.RequestID, OK: false, Error: m}
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		return fail("text is missing")
	}
	if o.Chat == nil || o.Org == nil {
		return fail("messages to people are not available on this instance")
	}
	human, err := o.resolveHuman(ctx, agent, req.To)
	if err != nil {
		return fail(err.Error())
	}
	convID, err := o.Chat.Direct(ctx, agent.OrgID, chat.Agent(agent.ID), chat.Human(human.ID), nil)
	if err != nil {
		return fail("the conversation could not be opened: " + err.Error())
	}
	agentID := agent.ID
	m := chat.Message{
		ConversationID: convID, AuthorKind: chat.MemberAgent, AuthorID: &agentID,
		Text: text, Kind: chat.MessageText,
	}
	if taskID != uuid.Nil {
		tid := taskID
		m.TaskID = &tid
	}
	if _, _, err := o.Chat.Post(ctx, m); err != nil {
		return fail("the message was not written: " + err.Error())
	}
	_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, &taskID, observability.KindAction,
		map[string]any{"action": "covey:message", "to": human.DisplayName, "conversation_id": convID.String()})
	// The same event a chat answer publishes (httpapi.chatEreignis): the
	// surfaces reload the conversation, and the app shows it at once.
	o.events.Publish(Event{Type: "chat", AgentID: agent.ID.String(), OrgID: agent.OrgID,
		Data: map[string]string{"state": "said", "conversation_id": convID.String(), "task_id": taskID.String()}})
	return daemon.InjectMessage{RequestID: req.RequestID, OK: true, To: human.DisplayName, ConversationID: convID.String()}
}

// resolveHuman finds the person a message is for. Empty = the agent's
// supervisor. Otherwise an e-mail address or a display name of a human of the
// same organisation, matched case-insensitively; a name that fits more than
// one person is refused rather than guessed.
func (o *Orchestrator) resolveHuman(ctx context.Context, agent agents.Agent, to string) (org.Human, error) {
	to = strings.TrimSpace(to)
	if to == "" || strings.EqualFold(to, "supervisor") {
		if agent.SupervisorID == nil {
			return org.Human{}, fmt.Errorf("you have no supervisor in the org chart — name the person (e-mail or display name)")
		}
		h, err := o.Org.GetHuman(ctx, agent.OrgID, *agent.SupervisorID)
		if err != nil {
			return org.Human{}, fmt.Errorf("your supervisor is not among this organisation's people")
		}
		return h, nil
	}
	humans, err := o.Org.ListHumans(ctx, agent.OrgID)
	if err != nil {
		return org.Human{}, fmt.Errorf("the organisation's people could not be read")
	}
	var hits []org.Human
	for _, h := range humans {
		if strings.EqualFold(h.Email, to) {
			return h, nil
		}
		if strings.EqualFold(strings.TrimSpace(h.DisplayName), to) {
			hits = append(hits, h)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return org.Human{}, fmt.Errorf("no person %q in this organisation — use the e-mail address or the display name from the org chart", to)
	default:
		return org.Human{}, fmt.Errorf("%d people are called %q — use the e-mail address", len(hits), to)
	}
}
