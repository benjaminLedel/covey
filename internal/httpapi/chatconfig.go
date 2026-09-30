package httpapi

// Configuration changes from the chat (#491), a trial behind an organisation
// setting.
//
// A person who tells an agent "check the inbox only every two hours" meant a
// change to its HEARTBEAT.md, and got a task the agent could not carry out:
// an ordinary agent may not touch its own configuration. With the setting
// on, the triage recognises such a wish (chat.AktionKonfig) and says in
// plain words what to change; this file drafts it with the config assistant
// (assist.go, the same turn the agent page's dialogue takes), stores it as
// a proposal that is NOT in effect (the row covey/propose_agent_config
// writes, improvements.go), and shows it in the conversation as a card.
//
// Nothing here puts a configuration in effect. Accepting goes through
// POST /api/v1/improvements/{id}/decide, the same path as the agent page's
// open points: a new version with the person as author, the lint on
// HEARTBEAT.md and KPIS.md, the write-through of ACCESS.md and EGRESS.md,
// and the rule that only org_admin or security accept a change to access.
// Whoever wrote the wish is not thereby whoever may accept it.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"

	"covey/internal/agents"
	"covey/internal/chat"
	"covey/internal/identity"
	"covey/internal/llm"
)

// handleGetChatConfigProposals says whether the trial is on.
func (s *Server) handleGetChatConfigProposals(w http.ResponseWriter, r *http.Request) {
	on, err := s.Chat.ConfigProposals(r.Context(), principalFrom(r).OrgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": on})
}

// handleSetChatConfigProposals switches it: {"enabled": true|false}.
func (s *Server) handleSetChatConfigProposals(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if err := readJSON(r, &in); err != nil || in.Enabled == nil {
		writeErr(w, http.StatusBadRequest, `invalid body: expected {"enabled": true|false}`)
		return
	}
	if err := s.Chat.SetConfigProposals(r.Context(), principalFrom(r).OrgID, *in.Enabled); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": *in.Enabled})
}

// konfigTimeout bounds drafting and storing one proposal. Its own, and not
// the triage turn's: the assistant asks the best tier for complete files,
// which takes longer than a triage turn is given.
const konfigTimeout = 2 * assistTimeout

/*
konfigVorschlagen drafts the change the triage recognised, stores it as a

	proposal, and says so in the conversation: a card (a config_proposal
	message naming the proposal) and a short line in the agent's words.

	A draft that fails is answered plainly in the conversation — never a task:
	the run of a task could not change the configuration either, and a
	message that disappears into a task the agent cannot do is what #491 is
	about.
*/
func (s *Server) konfigVorschlagen(conv chat.Conversation, agentID uuid.UUID, msg chat.Message,
	e chat.Entscheidung, lang string) (string, map[string]string, error) {

	ctx, cancel := context.WithTimeout(context.Background(), konfigTimeout)
	defer cancel()

	sagen := func(text string, kind string, meta map[string]string) error {
		_, _, err := s.Chat.Post(ctx, chat.Message{
			ConversationID: conv.ID, AuthorKind: chat.MemberAgent, AuthorID: &agentID,
			Text: text, Kind: kind, ReplyTo: &msg.ID, Meta: meta,
		})
		return err
	}
	scheitern := func(grund string) (string, map[string]string, error) {
		s.Log.Warn("chat: config proposal not drafted", "agent", agentID, "message", msg.ID, "reason", grund)
		if err := sagen(fmt.Sprintf(konfigFehlschlag(lang), grund), chat.MessageText, e.Meta); err != nil {
			return "", nil, err
		}
		return "answered", map[string]string{"proposal_failed": "true"}, nil
	}

	item, grund, err := s.konfigEntwerfen(ctx, conv.OrgID, agentID, msg, e)
	if err != nil {
		return "", nil, err
	}
	if grund != "" {
		return scheitern(grund)
	}

	meta := map[string]string{"proposal_id": item.ID.String()}
	karte := item.Title
	if r := strings.TrimSpace(item.Rationale); r != "" {
		karte += "\n\n" + r
	}
	if err := sagen(karte, chat.MessageConfigProposal, meta); err != nil {
		return "", nil, err
	}
	/* The agent's own line, with the names of those who may accept put in
	   where the turn left the placeholder: who they are depends on the
	   files the draft touches, which the turn could not know. */
	cur, _ := s.Registry.CurrentConfig(ctx, agentID)
	restricted := len(agents.RestrictedChanges(cur.Files, item.Files)) > 0
	namen := s.annehmende(ctx, conv.OrgID, agentID, restricted)
	text := strings.TrimSpace(e.Text)
	if text == "" {
		text = konfigStandard(lang)
	}
	text = strings.ReplaceAll(text, chat.ApproversPlatzhalter, aufzaehlen(namen, lang))
	if err := sagen(text, chat.MessageText, e.Meta); err != nil {
		s.Log.Warn("chat: the line beside a config proposal was not written", "agent", agentID, "err", err)
	}
	if s.Orch != nil {
		s.Orch.NotifyImprovement(ctx, item)
	}
	return "proposed", meta, nil
}

/*
konfigEntwerfen asks the config assistant for the change and stores it.

	A non-empty reason is a draft that did not come about — said to the
	person; an error is a fault of the platform.
*/
func (s *Server) konfigEntwerfen(ctx context.Context, orgID, agentID uuid.UUID, msg chat.Message,
	e chat.Entscheidung) (agents.ImprovementItem, string, error) {

	a, err := s.Registry.Get(ctx, agentID)
	if err != nil {
		return agents.ImprovementItem{}, "", err
	}
	cur, err := s.Registry.CurrentConfig(ctx, agentID)
	if err != nil && !errors.Is(err, agents.ErrNotFound) {
		return agents.ImprovementItem{}, "", err
	}
	if cur.Files == nil {
		cur.Files = map[string]string{}
	}
	provider, err := s.resolveOrgLLM(ctx, orgID)
	if err != nil {
		return agents.ImprovementItem{}, "no model is configured for the control plane", nil
	}
	auftrag := "A person asked this agent, in the team chat, to change its own configuration.\n\n" +
		"What to change: " + strings.TrimSpace(e.Aenderung) + "\n\n" +
		"Their message: " + strings.TrimSpace(msg.Text) + "\n\n" +
		"Propose exactly this change and nothing else: change only the files it needs and keep everything " +
		"else in them as it is. In \"reply\", write for the person who will accept or decline it — what changes " +
		"and why, in two or three sentences, without greeting."
	entwurf, err := s.assistDraft(ctx, provider, orgID, a, cur.Files, []llm.Message{{Role: "user", Content: auftrag}})
	if err != nil {
		return agents.ImprovementItem{}, "the configuration assistant could not be reached (" + err.Error() + ")", nil
	}
	files := map[string]string{}
	for _, p := range entwurf.Proposals {
		files[p.File] = p.Content
	}
	// Only what really changes: a file sent back as it is changes nothing,
	// and would decide who may accept.
	geaendert := agents.ChangedFiles(cur.Files, files)
	if len(geaendert) == 0 {
		return agents.ImprovementItem{}, "the draft changed nothing", nil
	}
	nur := make(map[string]string, len(geaendert))
	for _, name := range geaendert {
		nur[name] = files[name]
	}
	/* The checks the accept path runs, run here first: a proposal that
	   could never be accepted is furniture on the card. */
	merged := agents.MergeConfig(cur.Files, nur)
	if _, err := agents.ParseHeartbeat(merged["HEARTBEAT.md"]); err != nil {
		return agents.ImprovementItem{}, "the drafted HEARTBEAT.md does not parse (" + err.Error() + ")", nil
	}
	/* The rule of review.go for every proposal: who may reach the platform
	   itself is decided by a person, never proposed. */
	if acc, ok := nur["ACCESS.md"]; ok {
		for _, sa := range agents.ParseAccess(acc) {
			if sa.System == "covey" {
				return agents.ImprovementItem{}, "a proposal may not give an agent the system covey", nil
			}
		}
	}
	titel := strings.TrimSpace(e.Titel)
	if titel == "" {
		titel = chatTitle(e.Aenderung)
	}
	begruendung := strings.TrimSpace(entwurf.Reply)
	if begruendung == "" {
		begruendung = strings.TrimSpace(e.Aenderung)
	}
	msgID := msg.ID
	item, err := s.Registry.CreateImprovement(ctx, agents.ImprovementItem{
		OrgID: orgID, AgentID: agentID, Kind: agents.KindProposal,
		Title: titel, Rationale: begruendung, Files: nur,
		OriginMessageID: &msgID, RequestedBy: msg.AuthorID,
	})
	if err != nil {
		return agents.ImprovementItem{}, "", err
	}
	return item, "", nil
}

// maxAnnehmende: so many names the line and the card give.
const maxAnnehmende = 3

/*
annehmende are the people who may accept a proposal about this agent, by

	name, as the decide endpoint lets them: the agent's owner and the org
	admins — and when access widens only org admins and security.
*/
func (s *Server) annehmende(ctx context.Context, orgID, agentID uuid.UUID, restricted bool) []string {
	rollen := []string{identity.RoleOrgAdmin, identity.LegacyRoleOrgAdmin}
	if restricted {
		rollen = append(rollen, identity.RoleSecurity)
	}
	var owner *uuid.UUID
	if a, err := s.Registry.Get(ctx, agentID); err == nil {
		owner = a.OwnerID
	}
	/* The owner first, when their seat may decide: that is who the agent
	   belongs to. Then the rest, in a stable order. */
	rows, err := s.Pool.Query(ctx, `SELECT display_name FROM humans
		 WHERE org_id = $1 AND (role = ANY($2) OR (id = $3 AND role = ANY($4)))
		 ORDER BY (id = $3) DESC NULLS LAST, display_name`,
		orgID, rollen, owner, s.entscheidendeRollen(restricted))
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if rows.Scan(&n) == nil && n != "" && len(out) < maxAnnehmende {
			out = append(out, n)
		}
	}
	return out
}

// entscheidendeRollen are the seat roles that may accept: the decide route's
// (manage and security), narrowed to org_admin and security when access
// widens (improvements.go).
func (s *Server) entscheidendeRollen(restricted bool) []string {
	if restricted {
		return []string{identity.RoleOrgAdmin, identity.LegacyRoleOrgAdmin, identity.RoleSecurity}
	}
	return append(append([]string{}, manageRoles...), identity.RoleSecurity, identity.LegacyRoleOrgAdmin)
}

/*
kartenAnhaengen puts the proposal onto every config_proposal message, as

	the reader may see it (#491). Read with the conversation, so that the
	card is current wherever the conversation is: after a decision on the
	agent page too, and — the answer's ETag covering it — without a second
	request per card.
*/
func (s *Server) kartenAnhaengen(r *http.Request, msgs []chat.Message) {
	for i := range msgs {
		if msgs[i].Kind != chat.MessageConfigProposal {
			continue
		}
		msgs[i].Proposal = s.vorschlagsKarte(r, msgs[i].Meta["proposal_id"])
	}
}

// vorschlagsKarte is one proposal as the reader sees it; nil when it is gone
// or not of the reader's organisation.
func (s *Server) vorschlagsKarte(r *http.Request, roh string) *chat.ProposalCard {
	id, err := uuid.Parse(roh)
	if err != nil {
		return nil
	}
	p := principalFrom(r)
	item, err := s.Registry.GetImprovement(r.Context(), id)
	if err != nil || item.OrgID != p.OrgID || item.Kind != agents.KindProposal {
		return nil
	}
	v := s.improvementViews(r, []agents.ImprovementItem{item})[0]
	k := &chat.ProposalCard{
		ID: item.ID, Status: item.Status, Title: item.Title, Rationale: item.Rationale,
		Conflicts: v.Conflicts, AppliedVersion: item.AppliedVersion, DecidedAt: item.DecidedAt,
		Diff: []chat.ProposalFile{},
	}
	/* The diff against the running state while it is open; once decided,
	   the files it wrote, against nothing that changed since — the card
	   then records what was decided. */
	for _, d := range v.Diff {
		k.Diff = append(k.Diff, chat.ProposalFile{File: d.File, Before: d.Before, After: d.After})
	}
	if item.Status == agents.ImprovementPending {
		cur, _ := s.Registry.CurrentConfig(r.Context(), item.AgentID)
		k.Widens = agents.RestrictedChanges(cur.Files, item.Files)
	} else {
		for _, name := range agents.RestrictedConfigFiles {
			if _, ok := item.Files[name]; ok {
				k.Widens = append(k.Widens, name)
			}
		}
	}
	restricted := len(k.Widens) > 0
	k.CanDecide = item.Status == agents.ImprovementPending && slices.Contains(s.entscheidendeRollen(false), p.Role)
	k.CanAccept = k.CanDecide && len(k.Conflicts) == 0 && (!restricted || slices.Contains(s.entscheidendeRollen(true), p.Role))
	k.Approvers = s.annehmende(r.Context(), p.OrgID, item.AgentID, restricted)
	if k.Approvers == nil {
		k.Approvers = []string{}
	}
	k.RequestedBy = s.menschName(r.Context(), item.RequestedBy)
	k.DecidedBy = s.menschName(r.Context(), item.DecidedBy)
	return k
}

func (s *Server) menschName(ctx context.Context, id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	var n string
	_ = s.Pool.QueryRow(ctx, `SELECT display_name FROM humans WHERE id = $1`, *id).Scan(&n)
	return n
}

/*
vorschlagEntschieden tells the conversation a proposal came from that it was

	decided (#491) — wherever that happened, on the card or on the agent page.
	The event only says that the conversation moved; the card reads the rest.
*/
func (s *Server) vorschlagEntschieden(ctx context.Context, item agents.ImprovementItem) {
	if item.OriginMessageID == nil || s.Chat == nil {
		return
	}
	m, err := s.Chat.Message(ctx, *item.OriginMessageID)
	if err != nil {
		return
	}
	s.chatEreignis(item.OrgID, item.AgentID, m.ConversationID, "proposal_decided",
		map[string]string{"proposal_id": item.ID.String(), "status": item.Status})
}

// aufzaehlen joins names the way the language of the request says "or".
func aufzaehlen(namen []string, lang string) string {
	oder := map[string]string{"de": "oder", "en": "or", "es": "o", "fr": "ou", "it": "o",
		"nl": "of", "pl": "lub", "pt": "ou", "ja": "または", "zh": "或"}[sprachKuerzel(lang)]
	if oder == "" {
		oder = "or"
	}
	switch len(namen) {
	case 0:
		if sprachKuerzel(lang) == "de" {
			return "ein Admin"
		}
		return "an admin"
	case 1:
		return namen[0]
	}
	return strings.Join(namen[:len(namen)-1], ", ") + " " + oder + " " + namen[len(namen)-1]
}

// sprachKuerzel reads the first language tag of ?lang= or Accept-Language.
func sprachKuerzel(lang string) string {
	l := strings.ToLower(strings.TrimSpace(lang))
	if i := strings.IndexAny(l, ",;-_"); i >= 0 {
		l = l[:i]
	}
	return l
}

// konfigStandard is the line when the turn gave none.
func konfigStandard(lang string) string {
	if sprachKuerzel(lang) == "de" {
		return "Hab's entworfen — " + chat.ApproversPlatzhalter + " kann es annehmen."
	}
	return "I've drafted it — " + chat.ApproversPlatzhalter + " can accept it."
}

// konfigFehlschlag is the plain answer when a draft did not come about; %s
// is the reason, as the platform says it.
func konfigFehlschlag(lang string) string {
	if sprachKuerzel(lang) == "de" {
		return "Den Entwurf hab ich nicht hinbekommen: %s. Auf meiner Seite unter Konfiguration lässt es sich von Hand ändern."
	}
	return "I couldn't draft that change: %s. It can be changed by hand on my page, under Configuration."
}
