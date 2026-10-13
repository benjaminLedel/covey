package httpapi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
	"slices"
	"strings"

	"github.com/google/uuid"

	"covey/internal/chat"
	"covey/internal/orchestrator"
	"covey/internal/sandboxfs"
)

// Files in chat (#586). A person attaches files to a message; they are
// stored with it, and put into the home of every agent the message addresses,
// where a run can open them — a file an agent cannot read does not help it.
// An agent hands files to a person through covey/message
// (orchestrator/message.go). Both kinds come out through one download route.

// chatFilesDir is where a person's attachments land in an agent's home: one
// folder per message, so that two files of the same name sent on two days
// do not replace each other.
const chatFilesDir = "chat"

// chatForm is a message sent as multipart/form-data: the text, the message it
// replies to, and the files.
type chatForm struct {
	Text    string
	ReplyTo string
	Files   []chat.Attachment
}

// isMultipart says whether the request carries a form with files rather than
// the JSON body both message routes have always taken.
func isMultipart(r *http.Request) bool {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && mt == "multipart/form-data"
}

// readChatForm reads a message with files: fields "text" and "reply_to", and
// "file" as often as there are files. The body is held to the attachment
// limit plus room for the text, and refused past it before it is buffered. A
// message may be files and no text; nothing at all is refused.
func readChatForm(w http.ResponseWriter, r *http.Request) (chatForm, bool) {
	var out chatForm
	r.Body = http.MaxBytesReader(w, r.Body, chat.MaxAttachmentBytes+(1<<20))
	mr, err := r.MultipartReader()
	if err != nil {
		writeErr(w, http.StatusBadRequest, "not a multipart form")
		return out, false
	}
	var total int64
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				writeErr(w, http.StatusRequestEntityTooLarge, chat.ErrAttachmentTooLarge.Error())
				return out, false
			}
			writeErr(w, http.StatusBadRequest, "form unreadable: "+err.Error())
			return out, false
		}
		switch part.FormName() {
		case "text", "reply_to":
			b, err := io.ReadAll(io.LimitReader(part, 1<<20))
			part.Close()
			if err != nil {
				writeErr(w, http.StatusBadRequest, "form unreadable: "+err.Error())
				return out, false
			}
			if part.FormName() == "text" {
				out.Text = strings.TrimSpace(string(b))
			} else {
				out.ReplyTo = strings.TrimSpace(string(b))
			}
		case "file":
			// One byte past what is left: reading it says the limit was
			// crossed without holding the rest of the file.
			var buf bytes.Buffer
			n, err := io.Copy(&buf, io.LimitReader(part, chat.MaxAttachmentBytes-total+1))
			part.Close()
			if err != nil {
				var tooLarge *http.MaxBytesError
				if errors.As(err, &tooLarge) {
					writeErr(w, http.StatusRequestEntityTooLarge, chat.ErrAttachmentTooLarge.Error())
					return out, false
				}
				writeErr(w, http.StatusBadRequest, "upload unreadable: "+err.Error())
				return out, false
			}
			total += n
			if err := chat.CheckAttachments(len(out.Files)+1, total); err != nil {
				writeErr(w, http.StatusRequestEntityTooLarge, err.Error())
				return out, false
			}
			name := chat.AttachmentName(part.FileName())
			out.Files = append(out.Files, chat.Attachment{Name: name, MIME: chat.AttachmentMIME(name, buf.Bytes()), Data: buf.Bytes()})
		default:
			part.Close()
		}
	}
	if out.Text == "" && len(out.Files) == 0 {
		writeErr(w, http.StatusBadRequest, "text or a file is required")
		return out, false
	}
	return out, true
}

// attachError is a message refused because of its files — the status says
// whose side the fault is on.
type attachError struct {
	status int
	msg    string
}

func (e attachError) Error() string { return e.msg }

// writeChatErr answers a failed message: a refusal over its files with its
// own status, everything else the usual way.
func writeChatErr(w http.ResponseWriter, err error) {
	var ae attachError
	if errors.As(err, &ae) {
		writeErr(w, ae.status, ae.msg)
		return
	}
	mapErr(w, err)
}

// uniqueNames gives every attachment of one message its own name: a second
// "image.png" becomes "image (2).png", in the store as in the home.
func uniqueNames(files []chat.Attachment) {
	seen := map[string]bool{}
	for i := range files {
		name := files[i].Name
		ext := path.Ext(name)
		stem := strings.TrimSuffix(name, ext)
		for n := 2; seen[strings.ToLower(name)]; n++ {
			name = fmt.Sprintf("%s (%d)%s", stem, n, ext)
		}
		seen[strings.ToLower(name)] = true
		files[i].Name = name
	}
}

// placeInHomes puts a person's attachments into the home of each agent the
// message addresses, under chat/<message>/<name>, and records each write
// like a file put there through the Files tab. A home that cannot be
// written refuses the message: a note pointing the agent at a file that is
// not there would be worse than no message.
func (s *Server) placeInHomes(ctx context.Context, orgID uuid.UUID, wer sprecher, msgID uuid.UUID, agenten []uuid.UUID, files []chat.Attachment) error {
	if len(files) == 0 || len(agenten) == 0 {
		return nil
	}
	if s.Orch == nil {
		return attachError{http.StatusServiceUnavailable, "files cannot be handed to an agent on this instance"}
	}
	dir := path.Join(chatFilesDir, msgID.String()[:8])
	for i := range files {
		files[i].HomePath = path.Join(dir, files[i].Name)
	}
	for _, agentID := range agenten {
		fs, err := s.Orch.AgentFiles(agentID)
		if errors.Is(err, orchestrator.ErrNoFileAccess) {
			return attachError{http.StatusServiceUnavailable, "the configured sandbox provider has no reachable home — files cannot be handed to the agent"}
		}
		if err != nil {
			return err
		}
		for _, f := range files {
			e, err := fs.Write(f.HomePath, bytes.NewReader(f.Data))
			if err != nil {
				var readOnly *sandboxfs.ReadOnlyError
				if errors.As(err, &readOnly) {
					return attachError{http.StatusConflict, "the agent's home cannot be written right now: " + readOnly.Reason}
				}
				return attachError{http.StatusInternalServerError, "the file could not be put into the agent's home: " + err.Error()}
			}
			if err := s.Obs.Record(ctx, orgID, agentID, nil, "file", map[string]any{
				"op": "chat", "path": e.Path, "size": e.Size, "actor": wer.email, "actor_id": wer.id,
			}); err != nil {
				s.Log.Warn("chat attachment not written to the recording", "agent", agentID, "path", e.Path, "err", err)
			}
		}
	}
	return nil
}

// chatInlineTypes: what the download shows in place rather than handing out
// as a file — the Files preview's allowlist (sandboxfs.InlineType), and the
// two video containers a browser plays in a <video> element.
var chatInlineTypes = map[string]string{".mp4": "video/mp4", ".webm": "video/webm"}

func chatInlineType(name string) string {
	if t := sandboxfs.InlineType(name); t != "" {
		return t
	}
	return chatInlineTypes[strings.ToLower(path.Ext(name))]
}

// handleChatAttachment: GET /api/v1/chat/attachments/{id}[?download=1] — one
// file of a message, for the members of its conversation and for the roles
// that read every conversation of the organisation (the audit). Anybody else
// gets the 404 an unknown id gets.
//
// Shown in place only from the allowlist, and under the same headers as the
// Files preview (files.go, handlePreviewFile): foreign bytes on the covey
// origin, kept from becoming a document that runs script.
func (s *Server) handleChatAttachment(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	p := principalFrom(r)
	a, err := s.Chat.Attachment(r.Context(), id)
	if err != nil || a.OrgID != p.OrgID {
		writeErr(w, http.StatusNotFound, "attachment not found")
		return
	}
	if !slices.Contains(auditConversationRoles(), p.Role) {
		c, err := s.Chat.Get(r.Context(), a.ConversationID)
		if err != nil || !c.Has(chat.Human(p.ID)) {
			writeErr(w, http.StatusNotFound, "attachment not found")
			return
		}
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", fmt.Sprint(len(a.Data)))
	// An attachment never changes under its id.
	w.Header().Set("Cache-Control", "private, max-age=86400, immutable")
	ctype := chatInlineType(a.Name)
	if ctype == "" || r.URL.Query().Get("download") != "" {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": a.Name}))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(a.Data)
		return
	}
	csp := "default-src 'none'; sandbox"
	if ctype == "application/pdf" {
		csp = "default-src 'none'"
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Security-Policy", csp)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": a.Name}))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(a.Data)
}
