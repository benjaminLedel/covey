package teamsapp

import (
	"context"
	"covey/internal/teamsapp/teamsys"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"github.com/benjaminLedel/covey-plugin-pack/teams"
	"github.com/benjaminLedel/covey-plugin-sdk/target"
	"github.com/google/uuid"

	"covey/internal/agents"
)

// Inbound is what the router decided for one delivery: an agent and the event
// to hand it, or text the bot answers itself, or nothing.
type Inbound struct {
	OrgID uuid.UUID
	Agent *agents.Agent
	Event target.WebhookEvent
	// Answer is set when no agent takes the message: the bot says why.
	Answer string
	// SignIn: the agent works as the writer, who has not signed in yet. The
	// message is kept; the bot asks for single sign-on.
	SignIn bool
	// Tenant and Person name the writer (aadObjectId), for the task record.
	Tenant, Person string
}

// Route takes a verified activity to an organisation and one of its agents.
//
// The order is what a person writing in Teams would expect: a name at the
// start of the message wins (that is how one switches colleagues), then the
// colleague the conversation is already with, then the channel's default,
// then — if the organisation has exactly one — that one. Only agents whose
// ACCESS.md grants teamsapp are candidates; an organisation that has not
// switched the system on reaches nobody.
func (s *Service) Route(ctx context.Context, a teams.Activity, raw json.RawMessage) (Inbound, error) {
	tenant := a.TenantID()
	if tenant == "" {
		return Inbound{}, nil
	}
	org, err := s.Store.OrgOfTenant(ctx, tenant)
	if err != nil {
		if err == ErrNotLinked && isMessage(a) {
			return Inbound{Answer: "This Microsoft 365 organisation is not connected to covey yet. " +
				"An administrator connects it under Settings → Microsoft Teams in covey."}, nil
		}
		return Inbound{}, nilIfNotLinked(err)
	}
	in := Inbound{OrgID: org}

	// The install event: remember the team so its channels can be listed and
	// posted to. Nothing to route.
	if team := a.ChannelData.Team; team != nil && team.ID != "" {
		if err := s.Store.RememberTeam(ctx, Team{TenantID: tenant, TeamID: team.ID, Name: team.Name, ServiceURL: a.ServiceURL}); err != nil {
			return in, err
		}
	}
	if !isMessage(a) {
		return in, nil
	}
	if s.Enabled != nil && !s.Enabled(ctx, org) {
		in.Answer = "Microsoft Teams is not switched on for this organisation in covey."
		return in, nil
	}

	candidates, err := s.candidates(ctx, org)
	if err != nil {
		return in, err
	}
	if len(candidates) == 0 {
		in.Answer = "No colleague in this organisation works in Teams yet (ACCESS.md: `- system: teamsapp`)."
		return in, nil
	}

	text := a.CleanText()
	agent, rest := named(candidates, text)
	if agent != nil {
		text = rest
	}
	existing, convErr := s.Store.conversationIn(ctx, tenant, a.Conversation.ID)
	if agent == nil && convErr == nil && existing.AgentID != nil {
		agent = byID(candidates, *existing.AgentID)
	}
	channelID := ""
	if ch := a.ChannelData.Channel; ch != nil {
		channelID = ch.ID
	}
	if agent == nil && channelID != "" {
		if id, ok := s.Store.ChannelAgent(ctx, tenant, channelID); ok {
			agent = byID(candidates, id)
		}
	}
	if agent == nil && len(candidates) == 1 {
		agent = &candidates[0]
	}
	if agent == nil {
		in.Answer = chooser(candidates)
		return in, nil
	}

	conv := Conversation{
		TenantID: tenant, ConversationID: a.Conversation.ID, Kind: kindOf(a),
		ChannelID: channelID, AgentID: &agent.ID, ServiceURL: a.ServiceURL,
	}
	if team := a.ChannelData.Team; team != nil {
		conv.TeamID = team.ID
	}
	if err := s.Store.Bind(ctx, conv); err != nil {
		return in, err
	}
	in.Tenant, in.Person = tenant, a.From.AADObjectID
	if s.People != nil && s.People.SSOEnabled() && s.ActsAsPerson != nil && s.ActsAsPerson(ctx, *agent) {
		if in.Person == "" {
			in.Answer = "This colleague works with your own Teams access, and Teams did not say who you are. Write from a Teams account of your organisation."
			return in, nil
		}
		if !s.People.HasPerson(ctx, tenant, in.Person) {
			if err := s.People.KeepPending(ctx, tenant, in.Person, raw); err != nil {
				return in, err
			}
			in.SignIn = true
			return in, nil
		}
	}
	in.Agent = agent
	in.Event = event(a, text)
	return in, nil
}

func nilIfNotLinked(err error) error {
	if err == ErrNotLinked {
		return nil
	}
	return err
}

// isMessage: a person's message with something in it — the only kind of
// activity that becomes work. The bot's own echo never is.
func isMessage(a teams.Activity) bool {
	return strings.EqualFold(a.Type, "message") && a.From.ID != "" && !a.IsEcho() &&
		(a.CleanText() != "" || len(a.Files()) > 0)
}

func kindOf(a teams.Activity) string {
	switch k := a.Conversation.ConversationType; k {
	case "personal", "groupChat", "channel":
		return k
	}
	if a.ChannelData.Channel != nil {
		return "channel"
	}
	return "personal"
}

func (s *Service) candidates(ctx context.Context, org uuid.UUID) ([]agents.Agent, error) {
	all, err := s.Registry.List(ctx, org)
	if err != nil {
		return nil, err
	}
	var out []agents.Agent
	for _, ag := range all {
		if ag.Killed || ag.OrgID != org {
			continue
		}
		if ok, err := s.Registry.HasAccess(ctx, ag.ID, teamsys.Name, "read"); err == nil && ok {
			out = append(out, ag)
		}
	}
	return out, nil
}

func byID(list []agents.Agent, id uuid.UUID) *agents.Agent {
	for i := range list {
		if list[i].ID == id {
			return &list[i]
		}
	}
	return nil
}

// named finds the colleague a message starts with — "Vera, …", "vera: …",
// "@Vera …" — by display name, first name or slug. The longest match wins, so
// "Vera Brandt" beats "Vera". Returns the rest of the message without the name.
func named(list []agents.Agent, text string) (*agents.Agent, string) {
	t := strings.TrimLeft(strings.TrimSpace(text), "@")
	lower := strings.ToLower(t)
	var best *agents.Agent
	bestLen := 0
	for i := range list {
		for _, n := range namesOf(list[i]) {
			n = strings.ToLower(n)
			if len(n) <= bestLen || !strings.HasPrefix(lower, n) {
				continue
			}
			if len(lower) > len(n) {
				if r := rune(lower[len(n)]); unicode.IsLetter(r) || unicode.IsDigit(r) {
					continue // "Veronika" does not name "Vera"
				}
			}
			best, bestLen = &list[i], len(n)
		}
	}
	if best == nil {
		return nil, text
	}
	rest := strings.TrimLeftFunc(t[bestLen:], func(r rune) bool {
		return unicode.IsSpace(r) || r == ',' || r == ':' || r == '-' || r == '—'
	})
	return best, rest
}

func namesOf(a agents.Agent) []string {
	out := []string{a.Slug}
	if d := strings.TrimSpace(a.DisplayName); d != "" {
		out = append(out, d)
		if first, _, ok := strings.Cut(d, " "); ok && len(first) >= 3 {
			out = append(out, first)
		}
	}
	return out
}

func chooser(list []agents.Agent) string {
	names := make([]string, 0, len(list))
	for _, a := range list {
		n := a.DisplayName
		if n == "" {
			n = a.Slug
		}
		names = append(names, "**"+n+"**")
	}
	return "Which colleague is this for? Start your message with the name: " + strings.Join(names, ", ") + "."
}

// CorrelationKey is the key a parked task waits on: the conversation (for a
// channel, the thread).
func CorrelationKey(conversationID string) string {
	return "teamsapp:conversation:" + conversationID
}

func event(a teams.Activity, text string) target.WebhookEvent {
	from := a.From.Name
	if from == "" {
		from = "a Teams user"
	}
	if text == "" {
		text = "(no text)"
	}
	where := "a direct chat"
	switch kindOf(a) {
	case "channel":
		where = "a channel"
		if ch := a.ChannelData.Channel; ch != nil && ch.Name != "" {
			where = "the channel " + ch.Name
		}
	case "groupChat":
		where = "a group chat"
	}
	files := ""
	if fs := a.Files(); len(fs) > 0 {
		names := make([]string, 0, len(fs))
		for _, f := range fs {
			names = append(names, f.Filename())
		}
		files = fmt.Sprintf("\n\nAttached in Teams (not fetched through the covey app): %s", strings.Join(names, ", "))
	}
	call := fmt.Sprintf(`reply {"conversation_id":%q,"reply_to_activity_id":%q,"text":"…"}`, a.Conversation.ID, a.ID)
	return target.WebhookEvent{
		DedupKey:       "teamsapp:" + a.DedupKey(),
		CorrelationKey: CorrelationKey(a.Conversation.ID),
		Title:          "Teams message from " + from,
		TaskBody: fmt.Sprintf("%s wrote to you in %s in Microsoft Teams:\n\n%s%s\n\nAnswer through the action proxy (system teamsapp):\n%s",
			from, where, text, files, call),
		ResumeInput: fmt.Sprintf("Reply from %s in Teams:\n%s%s", from, text, files),
		Wake:        true,
	}
}
