package chat

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Attachments (#586): files a message carries, in both directions — what an
// agent hands a person through covey/message, and what a person attaches for
// an agent. They are part of what was said, so they live beside the message
// and go with it.

// The limits of one message's attachments. The total is what keeps an
// agent's files, base64-encoded in a single request_message frame, inside the
// daemon WebSocket's 16 MiB read limit (httpapi/daemonws.go): 10 MiB become
// 13.4 MiB of base64, and the rest of the frame is the text.
const (
	MaxAttachments     = 10
	MaxAttachmentBytes = 10 << 20
)

// ErrAttachmentTooLarge and ErrTooManyAttachments are the two limits, said.
var (
	ErrAttachmentTooLarge = fmt.Errorf("attachments too large (max. %d MiB together)", MaxAttachmentBytes>>20)
	ErrTooManyAttachments = fmt.Errorf("too many attachments (max. %d)", MaxAttachments)
)

// ErrAttachmentNotFound: no such attachment, or none the reader may see.
var ErrAttachmentNotFound = errors.New("attachment not found")

// Attachment is one file of a message. Data travels only into the store and
// out of the download; a message read with its attachments carries the rest.
type Attachment struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
	MIME string    `json:"mime"`
	Size int64     `json:"size"`
	// HomePath: where the file was put in the home of the agents a person's
	// message addressed. Empty on an agent's own file.
	HomePath string `json:"home_path,omitempty"`
	Data     []byte `json:"-"`
}

// StoredAttachment is an attachment as the download reads it: with the bytes
// and the conversation that decides who may read it.
type StoredAttachment struct {
	Attachment
	ConversationID uuid.UUID
	OrgID          uuid.UUID
	MessageID      uuid.UUID
	CreatedAt      time.Time
}

// CheckAttachments holds a set of attachments to the limits. Both directions
// call it — the daemon before it reads a byte, the control plane again
// before it writes one, because the sandbox is not trusted to have.
func CheckAttachments(n int, total int64) error {
	if n > MaxAttachments {
		return ErrTooManyAttachments
	}
	if total > MaxAttachmentBytes {
		return ErrAttachmentTooLarge
	}
	return nil
}

// AttachmentName makes a file name safe to store and to serve: the base name
// only, no control characters, never empty.
func AttachmentName(name string) string {
	name = path.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == "/" || name == ".." {
		return "file"
	}
	if len(name) > 200 {
		ext := path.Ext(name)
		if len(ext) > 20 {
			ext = ""
		}
		name = name[:200-len(ext)] + ext
	}
	return name
}

// AttachmentMIME names a file's type: by its extension, else by its first
// bytes. What it says decides nothing about how the file is served — the
// download keeps its own allowlist (httpapi) — it is what a surface shows.
func AttachmentMIME(name string, data []byte) string {
	if t := mime.TypeByExtension(strings.ToLower(path.Ext(name))); t != "" {
		if mt, _, err := mime.ParseMediaType(t); err == nil {
			return mt
		}
	}
	t := http.DetectContentType(data)
	if mt, _, err := mime.ParseMediaType(t); err == nil {
		return mt
	}
	return "application/octet-stream"
}

// AttachmentNote is the line an agent reads below a person's message: where
// the attached files lie in its home. The message itself stays what the
// person wrote; the note is added where an agent reads it (ForAgent).
func AttachmentNote(as []Attachment) string {
	var paths []string
	for _, a := range as {
		if a.HomePath != "" {
			paths = append(paths, a.HomePath)
		}
	}
	if len(paths) == 0 {
		return ""
	}
	return "(Attached, in your home: " + strings.Join(paths, ", ") + ")"
}

// ForAgent is the message as an agent reads it: the text, and below it where
// the files attached to it lie.
func (m Message) ForAgent() string {
	note := AttachmentNote(m.Attachments)
	switch {
	case note == "":
		return m.Text
	case strings.TrimSpace(m.Text) == "":
		return note
	default:
		return m.Text + "\n\n" + note
	}
}

// insertAttachments writes a message's attachments in the transaction that
// writes the message.
func insertAttachments(ctx context.Context, tx pgx.Tx, m Message) error {
	for i := range m.Attachments {
		a := &m.Attachments[i]
		if a.ID == uuid.Nil {
			a.ID = uuid.New()
		}
		if _, err := tx.Exec(ctx, `INSERT INTO conversation_attachments
			(id, message_id, conversation_id, org_id, name, mime, size, home_path, bytes, position)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			a.ID, m.ID, m.ConversationID, m.OrgID, a.Name, a.MIME, int64(len(a.Data)), a.HomePath, a.Data, i); err != nil {
			return err
		}
		a.Size = int64(len(a.Data))
	}
	return nil
}

// attachmentsCol reads a message's attachments without their bytes, as one
// JSON column beside the message — one index lookup per message read.
const attachmentsCol = `coalesce((SELECT json_agg(json_build_object('id', ca.id, 'name', ca.name, 'mime', ca.mime,
		'size', ca.size, 'home_path', ca.home_path) ORDER BY ca.position)
	FROM conversation_attachments ca WHERE ca.message_id = m.id), '[]') AS attachments`

// Attachment reads one attachment with its bytes.
func (s *Store) Attachment(ctx context.Context, id uuid.UUID) (StoredAttachment, error) {
	var a StoredAttachment
	err := s.pool.QueryRow(ctx, `SELECT id, message_id, conversation_id, org_id, name, mime, size, home_path, bytes, created_at
		FROM conversation_attachments WHERE id = $1`, id).
		Scan(&a.ID, &a.MessageID, &a.ConversationID, &a.OrgID, &a.Name, &a.MIME, &a.Size, &a.HomePath, &a.Data, &a.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, ErrAttachmentNotFound
	}
	return a, err
}
