package httpapi

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"covey/internal/chat"
	"covey/internal/identity"
)

/* Conversations in the audit (#440). Nobody reads a conversation they are
 * not in — except the auditor and the org admin, here, like the rest of what
 * an agent did. The shared threads from before #440 (chat_messages) are
 * listed beside them and are readable only here.
 *
 * Every read here passes the audit middleware like any other request, so the
 * trail records who looked at whose conversation. */

// auditConversationRoles: the two roles that read conversations they are
// not in. Security reads the audit trail but not what people said.
func auditConversationRoles() []string { return []string{identity.RoleOrgAdmin, identity.RoleAuditor} }

// handleAuditConversations lists the organisation's conversations and the
// shared threads from before: ?agent_id= or ?human_id= narrow it to those an
// agent or a person is or was in.
func (s *Server) handleAuditConversations(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	var member *chat.Ref
	var agentID *uuid.UUID
	if v := r.URL.Query().Get("agent_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid agent_id")
			return
		}
		member, agentID = &chat.Ref{Kind: chat.MemberAgent, ID: id}, &id
	}
	if v := r.URL.Query().Get("human_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid human_id")
			return
		}
		member = &chat.Ref{Kind: chat.MemberHuman, ID: id}
	}
	list, err := s.Chat.AuditList(r.Context(), p.OrgID, member, 0)
	if err != nil {
		mapErr(w, err)
		return
	}
	/* The shared threads belong to no person; narrowed to a person there are
	   none of them. */
	legacy := []chat.LegacyThread{}
	if member == nil || member.Kind == chat.MemberAgent {
		if legacy, err = s.Chat.LegacyThreads(r.Context(), p.OrgID, agentID); err != nil {
			mapErr(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"conversations": list, "legacy": legacy})
}

// handleAuditConversation reads one conversation whole. ?format=csv or
// ?format=json hands it over as a file.
func (s *Server) handleAuditConversation(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	c, err := s.Chat.Get(r.Context(), id)
	if err != nil || c.OrgID != principalFrom(r).OrgID {
		writeErr(w, http.StatusNotFound, "conversation not found")
		return
	}
	msgs, err := s.Chat.All(r.Context(), id)
	if err != nil {
		mapErr(w, err)
		return
	}
	name := "conversation-" + id.String()
	switch r.URL.Query().Get("format") {
	case "csv":
		auditCSV(w, name, func(cw *csv.Writer) {
			for _, m := range msgs {
				wer := m.AuthorName
				if m.AuthorID != nil {
					wer += " (" + m.AuthorID.String() + ")"
				}
				task := ""
				if m.TaskID != nil {
					task = m.TaskID.String()
				}
				_ = cw.Write([]string{m.CreatedAt.UTC().Format(time.RFC3339Nano), m.AuthorKind, zelle(wer), m.Kind, task, zelle(m.Text)})
			}
		})
	case "json":
		attachment(w, name+".json")
		writeJSON(w, http.StatusOK, map[string]any{"conversation": c, "messages": msgs})
	default:
		writeJSON(w, http.StatusOK, map[string]any{"conversation": c, "messages": msgs})
	}
}

// handleAuditLegacyThread reads an agent's shared thread from before #440,
// with the same ?format= as a conversation.
func (s *Server) handleAuditLegacyThread(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	msgs, err := s.Chat.LegacyMessages(r.Context(), principalFrom(r).OrgID, id)
	if err != nil {
		mapErr(w, err)
		return
	}
	name := "shared-thread-" + id.String()
	switch r.URL.Query().Get("format") {
	case "csv":
		auditCSV(w, name, func(cw *csv.Writer) {
			for _, m := range msgs {
				task := ""
				if m.TaskID != nil {
					task = m.TaskID.String()
				}
				kind := "human"
				if m.Author == "agent" {
					kind = "agent"
				}
				_ = cw.Write([]string{m.CreatedAt.UTC().Format(time.RFC3339Nano), kind, zelle(m.Author), "text", task, zelle(m.Text)})
			}
		})
	case "json":
		attachment(w, name+".json")
		writeJSON(w, http.StatusOK, map[string]any{"agent_id": id, "legacy": true, "messages": msgs})
	default:
		writeJSON(w, http.StatusOK, map[string]any{"agent_id": id, "legacy": true, "messages": msgs})
	}
}

func attachment(w http.ResponseWriter, name string) {
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
}

// auditCSV writes one table: a line per message, the same columns for a
// conversation and a shared thread.
func auditCSV(w http.ResponseWriter, name string, rows func(*csv.Writer)) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	attachment(w, name+".csv")
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"time", "author_kind", "author", "kind", "task_id", "text"})
	rows(cw)
	cw.Flush()
}

// zelle keeps a spreadsheet from reading a message as a formula: what
// somebody typed in a chat must not compute when the export is opened.
func zelle(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}
