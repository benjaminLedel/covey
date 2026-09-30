package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"covey/internal/backlog"
	"covey/internal/chat"
	"covey/internal/identity"
	"covey/internal/push"
)

/* Conversations (#440): the chat as people and agents talking, beside the
 * backlog. Only members read a conversation and write in it; auditor and org
 * admin read all of them in the audit (auditconversations.go), and nobody
 * else does. The per-agent thread endpoints (chat.go) are aliases onto the
 * person's direct conversation with that agent.
 *
 * Membership decides who reads and writes, not the seat role. Whom a member
 * reaches directly is the organisation's setting (chat.Reach): any agent, or
 * the agents of their own departments; the agent's supervisor and the org
 * admin always. When the setting narrows, a direct conversation that exists
 * stays readable, and writing in it is refused with a sentence saying why.
 * Adding an agent to a group stays with the roles that may create a task by
 * hand. */

// darfArbeitGeben: who may add an agent to a group — whoever may create a
// task by hand, because that is what a message to an agent can do.
func darfArbeitGeben(role string) bool { return slices.Contains(manageRoles, role) }

// conversationScoped lets a request through when the conversation belongs to
// the principal's organisation and the principal is an active member of it.
// Anything else is the same answer as a conversation that does not exist:
// whether somebody else talks to an agent is not a reader's business.
func (s *Server) conversationScoped(next func(http.ResponseWriter, *http.Request, chat.Conversation)) http.Handler {
	return s.rbac(anyRoleList, func(w http.ResponseWriter, r *http.Request) {
		id, err := parseID(r)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid id")
			return
		}
		p := principalFrom(r)
		c, err := s.Chat.Get(r.Context(), id)
		if err != nil || c.OrgID != p.OrgID || !c.Has(chat.Human(p.ID)) {
			writeErr(w, http.StatusNotFound, "conversation not found")
			return
		}
		next(w, r, c)
	})
}

// anyRoleList is every seat role; the routes spell it out as anyRole.
var anyRoleList = []string{identity.RoleOrgAdmin, identity.RoleAgentOwner,
	identity.RoleSecurity, identity.RoleAuditor, identity.RoleControlling}

// handleListConversations is the person's list, newest first.
//
// For a client that refreshes it often (#447): ?since=<cursor> answers only
// the conversations that changed after it — a new message, the person's own
// read position, mute or membership — and in `left` those the person left.
// Every answer carries the cursor for the next one. Beside that the answer
// has an ETag, and If-None-Match with it is answered 304 without a body.
func (s *Server) handleListConversations(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	var since *time.Time
	if v := r.URL.Query().Get("since"); v != "" {
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "since must be the cursor of an earlier answer (RFC 3339)")
			return
		}
		since = &t
	}
	/* The cursor is the database's clock a little in the past: a message
	   whose transaction began before this read and commits after it carries
	   a created_at before now(). The next delta overlaps by that much, and
	   the client keeps a conversation by its id. */
	jetzt, err := s.Chat.Now(r.Context())
	if err != nil {
		mapErr(w, err)
		return
	}
	cursor := jetzt.Add(-deltaUeberlappung)
	list, err := s.Chat.ListSince(r.Context(), p.ID, since, 0)
	if err != nil {
		mapErr(w, err)
		return
	}
	for i := range list {
		if list[i].Last != nil {
			list[i].Last.Text = push.FirstLine(list[i].Last.Text, 140)
			list[i].Last.Report = ""
		}
	}
	inhalt := map[string]any{"conversations": list}
	if since != nil {
		left, err := s.Chat.Left(r.Context(), p.ID, *since)
		if err != nil {
			mapErr(w, err)
			return
		}
		inhalt["left"] = left
	}
	if nichtGeaendert(w, r, inhalt) {
		return
	}
	inhalt["cursor"] = cursor
	writeJSON(w, http.StatusOK, inhalt)
}

// deltaUeberlappung: how far a delta read looks back past its cursor.
const deltaUeberlappung = 2 * time.Second

/* nichtGeaendert sets an ETag over what an answer says and answers 304 when
 * the client has it already (#447). The ETag leaves out what changes on every
 * call (the cursor), so an unchanged list is recognised as one. */
func nichtGeaendert(w http.ResponseWriter, r *http.Request, v any) bool {
	roh, err := json.Marshal(v)
	if err != nil {
		return false
	}
	sum := sha256.Sum256(roh)
	etag := `"` + hex.EncodeToString(sum[:16]) + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, no-cache")
	for _, kandidat := range strings.Split(r.Header.Get("If-None-Match"), ",") {
		if strings.TrimSpace(kandidat) == etag {
			w.WriteHeader(http.StatusNotModified)
			return true
		}
	}
	return false
}

// memberIn checks that a member exists in the organisation: a seat, or an
// agent that is a colleague (hired). A draft is nobody to talk to yet.
func (s *Server) memberIn(ctx context.Context, orgID uuid.UUID, ref chat.Ref) error {
	var ok bool
	var err error
	switch ref.Kind {
	case chat.MemberHuman:
		err = s.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM humans WHERE id = $1 AND org_id = $2)`, ref.ID, orgID).Scan(&ok)
	case chat.MemberAgent:
		err = s.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM agents WHERE id = $1 AND org_id = $2)`, ref.ID, orgID).Scan(&ok)
	default:
		return errors.New("member kind must be human or agent")
	}
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("no such member in this organisation: " + ref.String())
	}
	return nil
}

// handleCreateConversation opens a direct conversation (or returns the one
// that exists) or a group.
//
//	{"kind": "direct", "member": {"kind": "agent", "id": "…"}}
//	{"kind": "group", "title": "…", "members": [{"kind": "human", "id": "…"}, …]}
func (s *Server) handleCreateConversation(w http.ResponseWriter, r *http.Request) {
	if !s.teamSurfaceAn(w, r) {
		return
	}
	p := principalFrom(r)
	var in struct {
		Kind    string     `json:"kind"`
		Title   string     `json:"title"`
		Member  *chat.Ref  `json:"member"`
		Members []chat.Ref `json:"members"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, `invalid body: expected {"kind": "direct", "member": {…}} or {"kind": "group", "title": "…", "members": […]}`)
		return
	}
	var id uuid.UUID
	switch in.Kind {
	case chat.KindDirect:
		if in.Member == nil {
			writeErr(w, http.StatusBadRequest, "a direct conversation needs a member")
			return
		}
		if in.Member.Kind == chat.MemberHuman && in.Member.ID == p.ID {
			writeErr(w, http.StatusBadRequest, "a direct conversation is with somebody else")
			return
		}
		if err := s.memberIn(r.Context(), p.OrgID, *in.Member); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if in.Member.Kind == chat.MemberAgent && !s.erreicht(w, r, in.Member.ID) {
			return
		}
		var err error
		if id, err = s.Chat.Direct(r.Context(), p.OrgID, chat.Human(p.ID), *in.Member, &p.ID); err != nil {
			mapErr(w, err)
			return
		}
	case chat.KindGroup:
		titel := strings.TrimSpace(in.Title)
		if titel == "" || len([]rune(titel)) > 120 {
			writeErr(w, http.StatusBadRequest, "a group needs a title of at most 120 characters")
			return
		}
		if len(in.Members) == 0 {
			writeErr(w, http.StatusBadRequest, "a group needs members besides the one who opens it")
			return
		}
		for _, m := range in.Members {
			if err := s.memberIn(r.Context(), p.OrgID, m); err != nil {
				writeErr(w, http.StatusBadRequest, err.Error())
				return
			}
			if m.Kind == chat.MemberAgent && !darfArbeitGeben(p.Role) {
				writeErr(w, http.StatusForbidden, "only who may create a task by hand may bring an agent into a group")
				return
			}
		}
		var err error
		if id, err = s.Chat.CreateGroup(r.Context(), p.OrgID, titel, p.ID, in.Members); err != nil {
			mapErr(w, err)
			return
		}
	default:
		writeErr(w, http.StatusBadRequest, `kind must be "direct" or "group"`)
		return
	}
	c, err := s.Chat.Get(r.Context(), id)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (s *Server) handleGetConversation(w http.ResponseWriter, r *http.Request, c chat.Conversation) {
	writeJSON(w, http.StatusOK, c)
}

// conversationPage is a page of messages.
type conversationPage struct {
	Messages []chat.Message `json:"messages"`
	// More: there are older messages; ask again with the first one's time
	// and id as before/before_id.
	More bool `json:"more"`
	// Pending: a message still waits for a triage's decision.
	Pending bool `json:"pending"`
}

// handleConversationMessages reads a page, newest last:
// ?before=<RFC 3339 time>&before_id=<id>&limit=<n ≤ 200> for older ones, or
// ?after=<message id> for only what came after that message (#447). The
// answer has an ETag like the list.
func (s *Server) handleConversationMessages(w http.ResponseWriter, r *http.Request, c chat.Conversation) {
	q := r.URL.Query()
	limit := 50
	if n := q.Get("limit"); n != "" {
		var err error
		if limit, err = strconv.Atoi(n); err != nil || limit < 1 || limit > 200 {
			writeErr(w, http.StatusBadRequest, "limit must be between 1 and 200")
			return
		}
	}
	var out conversationPage
	if a := q.Get("after"); a != "" {
		aid, err := uuid.Parse(a)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "after must be the id of a message of this conversation")
			return
		}
		bezug, err := s.Chat.Message(r.Context(), aid)
		if err != nil || bezug.ConversationID != c.ID {
			writeErr(w, http.StatusBadRequest, "after must be the id of a message of this conversation")
			return
		}
		msgs, err := s.Chat.After(r.Context(), c.ID, chat.Cursor{At: bezug.CreatedAt, ID: bezug.ID}, limit+1)
		if err != nil {
			mapErr(w, err)
			return
		}
		out.Messages = msgs
		// More here means: newer ones beyond this page; ask again after its last.
		if len(msgs) > limit {
			out.Messages, out.More = msgs[:limit], true
		}
	} else {
		var cursor *chat.Cursor
		if b := q.Get("before"); b != "" {
			at, err := time.Parse(time.RFC3339Nano, b)
			bid, ierr := uuid.Parse(q.Get("before_id"))
			if err != nil || ierr != nil {
				writeErr(w, http.StatusBadRequest, "before must be an RFC 3339 time and before_id the id of that message")
				return
			}
			cursor = &chat.Cursor{At: at, ID: bid}
		}
		msgs, err := s.Chat.Page(r.Context(), c.ID, cursor, limit+1)
		if err != nil {
			mapErr(w, err)
			return
		}
		out.Messages = msgs
		if len(msgs) > limit {
			out.Messages, out.More = msgs[1:], true
		}
	}
	s.kartenAnhaengen(r, out.Messages)
	var err error
	if out.Pending, err = s.Chat.Pending(r.Context(), c.ID); err != nil {
		mapErr(w, err)
		return
	}
	if nichtGeaendert(w, r, out) {
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handlePostConversationMessage writes a message: {"text": "…", "reply_to": "<id>"}.
func (s *Server) handlePostConversationMessage(w http.ResponseWriter, r *http.Request, c chat.Conversation) {
	if !s.teamSurfaceAn(w, r) {
		return
	}
	p := principalFrom(r)
	var in struct {
		Text    string     `json:"text"`
		ReplyTo *uuid.UUID `json:"reply_to"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, `invalid body: expected {"text": "…"}`)
		return
	}
	text := strings.TrimSpace(in.Text)
	if text == "" {
		writeErr(w, http.StatusBadRequest, "text is required")
		return
	}
	var bezug *chat.Message
	if in.ReplyTo != nil {
		m, err := s.Chat.Message(r.Context(), *in.ReplyTo)
		if err != nil || m.ConversationID != c.ID {
			writeErr(w, http.StatusBadRequest, "reply_to must be a message of this conversation")
			return
		}
		bezug = &m
	}
	// A direct conversation with a stopped agent takes no message (#414),
	// nor one with an agent the organisation's reach no longer allows.
	if adressiert := chat.Addressed(c, text, bezug); len(adressiert) > 0 && c.Kind == chat.KindDirect {
		if !s.erreicht(w, r, adressiert[0].ID) || s.gestoppt(w, r, adressiert[0].ID) {
			return
		}
	}
	a, err := s.annehmen(r.Context(), c, sprecherVon(p), text, bezug, langFrom(r))
	if err != nil {
		mapErr(w, err)
		return
	}
	status := http.StatusCreated
	if a.pending {
		status = http.StatusAccepted
	}
	msg, err := s.Chat.Message(r.Context(), a.msg.ID)
	if err != nil {
		msg = a.msg
	}
	if a.tasks == nil {
		a.tasks = []backlog.Task{}
	}
	writeJSON(w, status, map[string]any{"message": msg, "tasks": a.tasks, "pending": a.pending})
}

// handleConversationRead moves the person's point forward: {"at": <time>}.
func (s *Server) handleConversationRead(w http.ResponseWriter, r *http.Request, c chat.Conversation) {
	var in struct {
		At time.Time `json:"at"`
	}
	if err := readJSON(r, &in); err != nil || in.At.IsZero() {
		writeErr(w, http.StatusBadRequest, `expected {"at": <RFC 3339 time of the newest message shown>}`)
		return
	}
	if err := s.Chat.MarkRead(r.Context(), c.ID, principalFrom(r).ID, in.At); err != nil {
		mapErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleConversationMe sets what is the person's own about a conversation:
// {"muted": true|false}.
func (s *Server) handleConversationMe(w http.ResponseWriter, r *http.Request, c chat.Conversation) {
	var in struct {
		Muted *bool `json:"muted"`
	}
	if err := readJSON(r, &in); err != nil || in.Muted == nil {
		writeErr(w, http.StatusBadRequest, `expected {"muted": true|false}`)
		return
	}
	if err := s.Chat.SetMuted(r.Context(), c.ID, principalFrom(r).ID, *in.Muted); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"muted": *in.Muted})
}

// handleAddConversationMember takes somebody into a group: {"kind", "id"}.
// Any member may; a direct conversation has its two and no more.
func (s *Server) handleAddConversationMember(w http.ResponseWriter, r *http.Request, c chat.Conversation) {
	if c.Kind != chat.KindGroup {
		writeErr(w, http.StatusConflict, "a direct conversation has exactly two members; open a group instead")
		return
	}
	p := principalFrom(r)
	var ref chat.Ref
	if err := readJSON(r, &ref); err != nil {
		writeErr(w, http.StatusBadRequest, `invalid body: expected {"kind": "human|agent", "id": "…"}`)
		return
	}
	if err := s.memberIn(r.Context(), p.OrgID, ref); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if ref.Kind == chat.MemberAgent && !darfArbeitGeben(p.Role) {
		writeErr(w, http.StatusForbidden, "only who may create a task by hand may bring an agent into a group")
		return
	}
	if err := s.Chat.AddMember(r.Context(), c.ID, ref); err != nil {
		mapErr(w, err)
		return
	}
	s.chatEreignis(c.OrgID, uuid.Nil, c.ID, "members", nil)
	neu, err := s.Chat.Get(r.Context(), c.ID)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, neu)
}

// handleRemoveConversationMember lets somebody leave a group: oneself always,
// anybody else only by the group's owner.
func (s *Server) handleRemoveConversationMember(w http.ResponseWriter, r *http.Request, c chat.Conversation) {
	if c.Kind != chat.KindGroup {
		writeErr(w, http.StatusConflict, "nobody leaves a direct conversation")
		return
	}
	p := principalFrom(r)
	mid, err := uuid.Parse(r.PathValue("member"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid member id")
		return
	}
	ref := chat.Ref{Kind: r.PathValue("kind"), ID: mid}
	selbst := ref.Kind == chat.MemberHuman && ref.ID == p.ID
	if !selbst {
		owner := false
		for _, m := range c.Active() {
			if m.Kind == chat.MemberHuman && m.ID == p.ID && m.Role == "owner" {
				owner = true
			}
		}
		if !owner {
			writeErr(w, http.StatusForbidden, "only the group's owner removes somebody else")
			return
		}
	}
	if err := s.Chat.RemoveMember(r.Context(), c.ID, ref); err != nil {
		mapErr(w, err)
		return
	}
	s.chatEreignis(c.OrgID, uuid.Nil, c.ID, "members", nil)
	w.WriteHeader(http.StatusNoContent)
}

// handleRenameConversation renames a group: {"title": "…"}. Only its owner
// does; a direct conversation is named after the other member.
func (s *Server) handleRenameConversation(w http.ResponseWriter, r *http.Request, c chat.Conversation) {
	if c.Kind != chat.KindGroup {
		writeErr(w, http.StatusConflict, "a direct conversation has no title")
		return
	}
	p := principalFrom(r)
	owner := false
	for _, m := range c.Active() {
		if m.Kind == chat.MemberHuman && m.ID == p.ID && m.Role == "owner" {
			owner = true
		}
	}
	if !owner {
		writeErr(w, http.StatusForbidden, "only the group's owner renames it")
		return
	}
	var in struct {
		Title string `json:"title"`
	}
	titel := ""
	if err := readJSON(r, &in); err == nil {
		titel = strings.TrimSpace(in.Title)
	}
	if titel == "" || len([]rune(titel)) > 120 {
		writeErr(w, http.StatusBadRequest, "a group needs a title of at most 120 characters")
		return
	}
	if err := s.Chat.SetTitle(r.Context(), c.ID, titel); err != nil {
		mapErr(w, err)
		return
	}
	s.chatEreignis(c.OrgID, uuid.Nil, c.ID, "renamed", nil)
	neu, err := s.Chat.Get(r.Context(), c.ID)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, neu)
}

/* erreicht refuses, with a sentence, to let the principal write directly to
 * an agent the organisation's reach does not allow them. */
func (s *Server) erreicht(w http.ResponseWriter, r *http.Request, agentID uuid.UUID) bool {
	p := principalFrom(r)
	ok, err := s.Chat.Reaches(r.Context(), p.OrgID, p.ID, agentID, p.Role == identity.RoleOrgAdmin)
	if err != nil {
		mapErr(w, err)
		return false
	}
	if !ok {
		writeErr(w, http.StatusForbidden, "this organisation lets its members write directly only to the agents of their own department (or the ones they supervise); a conversation that exists stays readable")
		return false
	}
	return true
}

// handleReachableAgents answers which agents the person may write to
// directly — for the surfaces, which offer only those.
func (s *Server) handleReachableAgents(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	alle, err := s.Chat.Reachable(r.Context(), p.OrgID, p.ID, p.Role == identity.RoleOrgAdmin)
	if err != nil {
		mapErr(w, err)
		return
	}
	ids := make([]uuid.UUID, 0, len(alle))
	for id := range alle {
		ids = append(ids, id)
	}
	reach, _ := s.Chat.ReachOf(r.Context(), p.OrgID)
	writeJSON(w, http.StatusOK, map[string]any{"reach": reach, "agents": ids})
}

// handleGetReach / handleSetReach: the organisation's setting.
func (s *Server) handleGetReach(w http.ResponseWriter, r *http.Request) {
	reach, err := s.Chat.ReachOf(r.Context(), principalFrom(r).OrgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"reach": string(reach)})
}

func (s *Server) handleSetReach(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Reach string `json:"reach"`
	}
	if err := readJSON(r, &in); err != nil || (in.Reach != string(chat.ReachOrg) && in.Reach != string(chat.ReachDepartment)) {
		writeErr(w, http.StatusBadRequest, `expected {"reach": "org|department"}`)
		return
	}
	if err := s.Chat.SetReach(r.Context(), principalFrom(r).OrgID, chat.Reach(in.Reach)); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"reach": in.Reach})
}
