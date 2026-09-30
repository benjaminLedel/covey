package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"covey/internal/callgreeting"
	"covey/internal/chat"
)

// handleCallGreeting writes the sentence an agent opens a call with
// (#513): from the caller's clock, what the agent is working on and what
// the two last spoke about, in the conversation's language and the chat
// tone's address. The app asks while the line rings; whatever goes wrong —
// no credential, a slow or unusable answer — is 204, and the app greets
// from its templates. Nothing is stored: the greeting is part of the call,
// not a message.
func (s *Server) handleCallGreeting(w http.ResponseWriter, r *http.Request, c chat.Conversation) {
	var in struct {
		AgentID   string `json:"agent_id"`
		Lang      string `json:"lang"`
		LocalTime string `json:"local_time"`
		Weekday   string `json:"weekday"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	agentID, err := uuid.Parse(in.AgentID)
	if err != nil || !c.Has(chat.Ref{Kind: chat.MemberAgent, ID: agentID}) {
		writeErr(w, http.StatusNotFound, "no such agent in this conversation")
		return
	}
	local, err := time.Parse(time.RFC3339, strings.TrimSpace(in.LocalTime))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "local_time must be the caller's time in RFC 3339, with its offset")
		return
	}
	if len(in.Lang) > 35 || len(in.Weekday) > 40 {
		writeErr(w, http.StatusBadRequest, "lang at most 35 characters, weekday at most 40")
		return
	}
	ctx := r.Context()
	p := principalFrom(r)
	provider, err := s.resolveOrgLLM(ctx, p.OrgID)
	if err != nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	g := s.callGreetingContext(ctx, r, c, agentID, local)
	g.Language = strings.TrimSpace(in.Lang)
	g.Weekday = strings.TrimSpace(in.Weekday)
	wait := s.CallGreetingTimeout
	if wait <= 0 {
		wait = callgreeting.Timeout
	}
	tctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	text, err := callgreeting.Write(tctx, provider, g)
	if err != nil {
		if s.Log != nil {
			s.Log.Info("call greeting from the app's templates", "agent", agentID, "reason", err)
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"text": text})
}

// callGreetingContext gathers what the greeting is written from. A part
// that cannot be read is left out; the greeting is written without it.
func (s *Server) callGreetingContext(ctx context.Context, r *http.Request, c chat.Conversation, agentID uuid.UUID, local time.Time) callgreeting.Context {
	p := principalFrom(r)
	g := callgreeting.Context{
		Local:     local,
		FullName:  strings.TrimSpace(p.DisplayName),
		FirstName: callgreeting.FirstName(p.DisplayName),
	}
	if a, err := s.Registry.Get(ctx, agentID); err == nil && a.OrgID == c.OrgID {
		g.AgentName = a.DisplayName
		if a.DepartmentID != nil && s.Org != nil {
			if d, err := s.Org.GetDepartment(ctx, c.OrgID, *a.DepartmentID); err == nil {
				g.Department = d.Name
			}
		}
	}
	if s.Voices != nil {
		_, tone := s.callVoice(r, c, agentID)
		g.Address, g.Tone, g.ToneNote = tone.SpokenAddress(), tone.Tone, tone.Note
	}
	if s.Backlog != nil {
		if titles, err := s.Backlog.InProgressTitles(ctx, agentID, 3); err == nil {
			g.InProgress = titles
		}
		y, m, d := local.Date()
		if t, err := s.Backlog.LastDoneSince(ctx, agentID, time.Date(y, m, d, 0, 0, 0, 0, local.Location())); err == nil {
			g.DoneToday = t
		}
	}
	// The conversation's own last lines: what the caller has read already.
	if msgs, err := s.Chat.Page(ctx, c.ID, nil, 3); err == nil {
		for _, m := range msgs {
			if m.Kind == chat.MessageConfigProposal || strings.TrimSpace(m.Text) == "" {
				continue
			}
			g.Recent = append(g.Recent, callgreeting.Line{
				Agent:  m.AuthorKind == "agent",
				Author: m.AuthorName,
				Text:   m.Text,
				At:     m.CreatedAt,
			})
		}
	}
	return g
}
