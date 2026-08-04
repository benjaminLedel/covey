package email

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	gomessage "github.com/emersion/go-message"
	"github.com/emersion/go-message/charset" // also registers the charset decoders (ISO-8859-*, …)
	gomail "github.com/emersion/go-message/mail"
)

// IMAP side of the plugin: read the mailbox, set flags, move mail. Every action
// opens a fresh connection (login → work → logout) — actions are rare, short
// accesses; a connection pool would not pay off and would hold credentials in
// memory longer than necessary.

// maxBodyBytes limits the text that reaches the agent's session out of one mail
// — mail can be arbitrarily large, the context window cannot.
const maxBodyBytes = 64 << 10

// MessageSummary is the list view of a mail (list_unread/list_messages).
type MessageSummary struct {
	UID       uint32 `json:"uid"`
	Mailbox   string `json:"mailbox"`
	From      string `json:"from"`
	Subject   string `json:"subject"`
	Date      string `json:"date,omitempty"`
	Seen      bool   `json:"seen"`
	MessageID string `json:"message_id,omitempty"`
}

// Message is the detail view (get_message) including the extracted text.
type Message struct {
	MessageSummary
	To          []string `json:"to,omitempty"`
	Cc          []string `json:"cc,omitempty"`
	ReplyTo     string   `json:"reply_to,omitempty"`
	InReplyTo   []string `json:"in_reply_to,omitempty"`
	Body        string   `json:"body"`
	BodyIsHTML  bool     `json:"body_is_html,omitempty"`
	Truncated   bool     `json:"truncated,omitempty"`
	Attachments []string `json:"attachments,omitempty"`
}

func dialIMAP(cfg Config) (*imapclient.Client, error) {
	opts := &imapclient.Options{
		WordDecoder: &mime.WordDecoder{CharsetReader: charset.Reader},
	}
	switch cfg.IMAPTLS {
	case tlsImplicit:
		return imapclient.DialTLS(cfg.IMAPAddr, opts)
	case tlsStartTLS:
		return imapclient.DialStartTLS(cfg.IMAPAddr, opts)
	default:
		return imapclient.DialInsecure(cfg.IMAPAddr, opts)
	}
}

// withIMAP wraps connection + login + logout around one mailbox operation.
func withIMAP(cfg Config, fn func(c *imapclient.Client) error) error {
	c, err := dialIMAP(cfg)
	if err != nil {
		return fmt.Errorf("imap connection %s: %w", cfg.IMAPAddr, err)
	}
	defer c.Close()
	if err := c.Login(cfg.Username, cfg.Password).Wait(); err != nil {
		return fmt.Errorf("imap login as %s: %w", cfg.Username, err)
	}
	err = fn(c)
	_ = c.Logout().Wait()
	return err
}

// listMailboxes returns the folder names of the mailbox.
func listMailboxes(cfg Config) ([]string, error) {
	var names []string
	err := withIMAP(cfg, func(c *imapclient.Client) error {
		boxes, err := c.List("", "*", nil).Collect()
		if err != nil {
			return err
		}
		for _, b := range boxes {
			names = append(names, b.Mailbox)
		}
		sort.Strings(names)
		return nil
	})
	return names, err
}

// listMessages returns the newest mail of a folder (newest first). unseenOnly
// restricts it to unread mail; own mail (sender = own address) is skipped then
// — echo protection against processing oneself. Senders outside
// COVEY_EMAIL_INTAKE_ADDRESSES are hidden.
func listMessages(cfg Config, mailbox string, unseenOnly bool, limit int) ([]MessageSummary, error) {
	out := []MessageSummary{}
	err := withIMAP(cfg, func(c *imapclient.Client) error {
		if _, err := c.Select(mailbox, &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
			return fmt.Errorf("mailbox %q: %w", mailbox, err)
		}
		criteria := &imap.SearchCriteria{}
		if unseenOnly {
			criteria.NotFlag = []imap.Flag{imap.FlagSeen}
		}
		data, err := c.UIDSearch(criteria, nil).Wait()
		if err != nil {
			return err
		}
		uids := data.AllUIDs()
		if len(uids) == 0 {
			return nil
		}
		// Fetch only the most recent slice — UIDs rise chronologically.
		if len(uids) > limit {
			uids = uids[len(uids)-limit:]
		}
		msgs, err := c.Fetch(imap.UIDSetNum(uids...),
			&imap.FetchOptions{Envelope: true, Flags: true, UID: true}).Collect()
		if err != nil {
			return err
		}
		for _, m := range msgs {
			s := summarize(m, mailbox)
			if unseenOnly && strings.EqualFold(s.From, cfg.From) {
				continue
			}
			if !senderInScope(s.From) {
				continue
			}
			out = append(out, s)
		}
		sort.Slice(out, func(i, j int) bool { return out[i].UID > out[j].UID })
		return nil
	})
	return out, err
}

// getMessage fetches a mail in full (header + extracted text). BODY.PEEK leaves
// the \Seen flag untouched — the agent marks a mail as read explicitly with
// mark_seen once it has processed it.
func getMessage(cfg Config, mailbox string, uid uint32) (*Message, error) {
	var msg *Message
	err := withIMAP(cfg, func(c *imapclient.Client) error {
		if _, err := c.Select(mailbox, &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
			return fmt.Errorf("mailbox %q: %w", mailbox, err)
		}
		section := &imap.FetchItemBodySection{Peek: true}
		msgs, err := c.Fetch(imap.UIDSetNum(imap.UID(uid)), &imap.FetchOptions{
			Envelope: true, Flags: true, UID: true,
			BodySection: []*imap.FetchItemBodySection{section},
		}).Collect()
		if err != nil {
			return err
		}
		if len(msgs) == 0 {
			return fmt.Errorf("no mail with uid %d in %q", uid, mailbox)
		}
		m := msgs[0]
		msg = &Message{MessageSummary: summarize(m, mailbox)}
		if env := m.Envelope; env != nil {
			msg.To = addrList(env.To)
			msg.Cc = addrList(env.Cc)
			for _, id := range env.InReplyTo {
				msg.InReplyTo = append(msg.InReplyTo, ensureAngles(id))
			}
			if len(env.ReplyTo) > 0 {
				msg.ReplyTo = env.ReplyTo[0].Addr()
			}
		}
		var raw []byte
		if len(m.BodySection) > 0 {
			raw = m.BodySection[0].Bytes
		}
		extractBody(raw, msg)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return msg, nil
}

// getAttachment fetches the bytes of ONE attachment of a mail. BODY.PEEK as in
// getMessage — loading an attachment does not set a \Seen flag either.
// Delivered are the actual file name out of the mail, the content type and the
// (transfer-decoded) bytes.
//
// Two ways, so that fetching one attachment does not put the whole mail into
// memory: the normal case is the BODYSTRUCTURE — it names file name, encoding
// and size of every part, so only the wanted part is fetched, and one that is
// too large is not fetched at all. If the name is not found there (server
// without extended BODYSTRUCTURE, RFC-2231-encoded file names), the parser over
// the whole mail decides as before — there RFC822.SIZE caps the memory.
func getAttachment(cfg Config, mailbox string, uid uint32, name string, limit int64) (string, string, []byte, error) {
	// Memory budget for what may go over the wire raw for one attachment.
	// Encoded, a part is never smaller than its content (base64 ≈ +37 %,
	// quoted-printable at most a good three times) — whatever exceeds four
	// times the limit raw is certainly too large decoded as well.
	rawLimit := 4 * limit
	var (
		filename    string
		contentType string
		data        []byte
	)
	err := withIMAP(cfg, func(c *imapclient.Client) error {
		if _, err := c.Select(mailbox, &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
			return fmt.Errorf("mailbox %q: %w", mailbox, err)
		}
		uids := imap.UIDSetNum(imap.UID(uid))
		msgs, err := c.Fetch(uids, &imap.FetchOptions{
			UID: true, RFC822Size: true,
			BodyStructure: &imap.FetchItemBodyStructure{Extended: true},
		}).Collect()
		if err != nil {
			return err
		}
		if len(msgs) == 0 {
			return fmt.Errorf("no mail with uid %d in %q", uid, mailbox)
		}

		if part := findAttachmentPart(msgs[0].BodyStructure, name); part != nil {
			if int64(part.size) > rawLimit {
				return fmt.Errorf("attachment %q larger than %d MB — aborted", part.filename, limit>>20)
			}
			filename, contentType, data, err = fetchAttachmentPart(c, uids, part, limit)
			return err
		}

		// Fallback over the whole mail — but only as long as it fits into the
		// memory budget.
		if msgs[0].RFC822Size > rawLimit {
			return fmt.Errorf("the mail with uid %d is %d MB — too large to load it in full for one attachment",
				uid, msgs[0].RFC822Size>>20)
		}
		section := &imap.FetchItemBodySection{Peek: true}
		full, err := c.Fetch(uids, &imap.FetchOptions{
			UID: true, BodySection: []*imap.FetchItemBodySection{section},
		}).Collect()
		if err != nil {
			return err
		}
		if len(full) == 0 {
			return fmt.Errorf("no mail with uid %d in %q", uid, mailbox)
		}
		filename, contentType, data, err = findAttachment(full[0].FindBodySection(section), name, limit)
		return err
	})
	if err != nil {
		return "", "", nil, err
	}
	return filename, contentType, data, nil
}

// attachmentPart is an attachment found through the BODYSTRUCTURE: the IMAP
// path of the part, its file name and its size in encoded bytes.
type attachmentPart struct {
	path     []int
	filename string
	size     uint32
}

// findAttachmentPart looks for the attachment called name in the BODYSTRUCTURE.
// What counts as an attachment and how the file name comes about follows the
// same rule as when parsing the whole mail (go-message/mail) — otherwise this
// shortcut would find parts that get_message never listed. No hit therefore
// does not mean "does not exist", only "not decidable here": the caller then
// falls back to the whole mail, which delivers the names authoritatively.
func findAttachmentPart(bs imap.BodyStructure, name string) *attachmentPart {
	if bs == nil {
		return nil
	}
	want := filepath.Base(strings.TrimSpace(name))
	dec := mime.WordDecoder{CharsetReader: charset.Reader}
	var found *attachmentPart
	bs.Walk(func(path []int, part imap.BodyStructure) bool {
		sp, ok := part.(*imap.BodyStructureSinglePart)
		if !ok || found != nil || !isAttachmentPart(sp) {
			return true
		}
		fname := sp.Filename()
		if decoded, err := dec.DecodeHeader(fname); err == nil {
			fname = decoded
		}
		if fname == "" || !strings.EqualFold(filepath.Base(fname), want) {
			return true
		}
		// The first hit in MIME order wins — the same rule as in
		// findAttachment, so that both ways deliver the same attachment.
		found = &attachmentPart{path: append([]int(nil), path...), filename: fname, size: sp.Size}
		return true
	})
	return found
}

// isAttachmentPart mirrors the classification from go-message/mail: an
// attachment is everything that is not inline and is either explicitly declared
// as attachment or is not text.
func isAttachmentPart(sp *imap.BodyStructureSinglePart) bool {
	var disp string
	if d := sp.Disposition(); d != nil {
		disp = strings.ToLower(d.Value)
	}
	if disp == "inline" {
		return false
	}
	return disp == "attachment" || !strings.EqualFold(sp.Type, "text")
}

// fetchAttachmentPart fetches exactly one MIME part — its MIME header and body
// — and lets go-message decode it: the same decoding as when parsing the whole
// mail, only without transferring the rest of the mail.
func fetchAttachmentPart(c *imapclient.Client, uids imap.UIDSet, part *attachmentPart, limit int64) (string, string, []byte, error) {
	head := &imap.FetchItemBodySection{Part: part.path, Specifier: imap.PartSpecifierMIME, Peek: true}
	body := &imap.FetchItemBodySection{Part: part.path, Peek: true}
	msgs, err := c.Fetch(uids, &imap.FetchOptions{
		UID: true, BodySection: []*imap.FetchItemBodySection{head, body},
	}).Collect()
	if err != nil {
		return "", "", nil, err
	}
	if len(msgs) == 0 {
		return "", "", nil, fmt.Errorf("attachment %q no longer retrievable", part.filename)
	}
	rawHead := msgs[0].FindBodySection(head)
	if len(rawHead) == 0 {
		return "", "", nil, fmt.Errorf("attachment %q: the server did not deliver the MIME part", part.filename)
	}
	rawBody := msgs[0].FindBodySection(body)
	// The pre-check against the BODYSTRUCTURE (see caller) hangs on what the
	// server states. body-fld-octets is mandatory, but a faulty or malicious
	// server reporting 0 there would defeat the budget: at this point Collect()
	// already holds the encoded part in memory in full, and the LimitReader
	// below only takes effect on the DECODED stream. Hence check once more
	// here, against what was actually read.
	if int64(len(rawBody)) > 4*limit {
		return "", "", nil, fmt.Errorf("attachment %q larger than %d MB — aborted", part.filename, limit>>20)
	}
	ent, err := gomessage.Read(io.MultiReader(bytes.NewReader(rawHead), bytes.NewReader(rawBody)))
	if err != nil && !gomessage.IsUnknownEncoding(err) {
		return "", "", nil, fmt.Errorf("attachment %q not parsable: %w", part.filename, err)
	}
	// Read 1 byte beyond the limit in order to detect an overrun reliably.
	data, err := io.ReadAll(io.LimitReader(ent.Body, limit+1))
	if err != nil {
		return "", "", nil, fmt.Errorf("reading attachment %q: %w", part.filename, err)
	}
	if int64(len(data)) > limit {
		return "", "", nil, fmt.Errorf("attachment %q larger than %d MB — aborted", part.filename, limit>>20)
	}
	ct, _, _ := ent.Header.ContentType()
	return part.filename, ct, data, nil
}

// findAttachment looks for the attachment called name in a raw mail and returns
// file name, content type and bytes. Compared is the basename (case
// insensitively); with several attachments of the same name the first one in
// MIME order wins — the selection stays deterministic that way. The bytes stay
// in memory in full: only once they are below the limit does the caller write
// them into the sandbox.
func findAttachment(raw []byte, name string, limit int64) (string, string, []byte, error) {
	want := filepath.Base(strings.TrimSpace(name))
	if len(raw) == 0 {
		return "", "", nil, fmt.Errorf("mail without content — no attachment readable")
	}
	mr, err := gomail.CreateReader(bytes.NewReader(raw))
	if err != nil {
		return "", "", nil, fmt.Errorf("mail not parsable: %w", err)
	}
	var names []string
	for {
		p, err := mr.NextPart()
		if err != nil {
			break
		}
		h, ok := p.Header.(*gomail.AttachmentHeader)
		if !ok {
			continue
		}
		fname, err := h.Filename()
		if err != nil || fname == "" {
			continue
		}
		names = append(names, fname)
		if !strings.EqualFold(filepath.Base(fname), want) {
			continue
		}
		ct, _, _ := h.ContentType()
		// Read 1 byte beyond the limit in order to detect an overrun reliably.
		data, err := io.ReadAll(io.LimitReader(p.Body, limit+1))
		if err != nil {
			return "", "", nil, fmt.Errorf("reading attachment %q: %w", fname, err)
		}
		if int64(len(data)) > limit {
			return "", "", nil, fmt.Errorf("attachment %q larger than %d MB — aborted", fname, limit>>20)
		}
		return fname, ct, data, nil
	}
	if len(names) == 0 {
		return "", "", nil, fmt.Errorf("this mail has no attachments")
	}
	return "", "", nil, fmt.Errorf("no attachment %q on this mail (available: %s)", name, strings.Join(names, ", "))
}

// setSeen sets resp. clears the \Seen flag of a mail.
func setSeen(cfg Config, mailbox string, uid uint32, seen bool) error {
	return withIMAP(cfg, func(c *imapclient.Client) error {
		if _, err := c.Select(mailbox, nil).Wait(); err != nil {
			return fmt.Errorf("mailbox %q: %w", mailbox, err)
		}
		op := imap.StoreFlagsAdd
		if !seen {
			op = imap.StoreFlagsDel
		}
		return c.Store(imap.UIDSetNum(imap.UID(uid)), &imap.StoreFlags{
			Op: op, Silent: true, Flags: []imap.Flag{imap.FlagSeen},
		}, nil).Close()
	})
}

// moveMessage moves a mail into another folder (MOVE resp. the COPY+EXPUNGE
// fallback of the client library for servers without the MOVE capability).
func moveMessage(cfg Config, mailbox string, uid uint32, dest string) error {
	return withIMAP(cfg, func(c *imapclient.Client) error {
		if _, err := c.Select(mailbox, nil).Wait(); err != nil {
			return fmt.Errorf("mailbox %q: %w", mailbox, err)
		}
		if _, err := c.Move(imap.UIDSetNum(imap.UID(uid)), dest).Wait(); err != nil {
			return fmt.Errorf("moving to %q: %w", dest, err)
		}
		return nil
	})
}

// ensureAngles normalizes a message ID to the header form <id> — some servers
// deliver the envelope ID without the angle brackets.
func ensureAngles(id string) string {
	id = strings.TrimSpace(id)
	if id == "" || strings.HasPrefix(id, "<") {
		return id
	}
	return "<" + id + ">"
}

// addrList reduces envelope addresses to "mailbox@host" strings.
func addrList(addrs []imap.Address) []string {
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		if addr := a.Addr(); addr != "" {
			out = append(out, addr)
		}
	}
	return out
}

// summarize builds the list view out of a fetch result.
func summarize(m *imapclient.FetchMessageBuffer, mailbox string) MessageSummary {
	s := MessageSummary{UID: uint32(m.UID), Mailbox: mailbox}
	for _, f := range m.Flags {
		if f == imap.FlagSeen {
			s.Seen = true
		}
	}
	if env := m.Envelope; env != nil {
		s.Subject = env.Subject
		s.MessageID = ensureAngles(env.MessageID)
		if len(env.From) > 0 {
			s.From = env.From[0].Addr()
		}
		if !env.Date.IsZero() {
			s.Date = env.Date.Format(time.RFC3339)
		}
	}
	return s
}

// extractBody pulls the readable text out of the raw mail: text/plain
// preferred, otherwise text/html (marked as such); attachments by name only.
// Parse errors are no abort — whatever was extracted is delivered.
func extractBody(raw []byte, msg *Message) {
	if len(raw) == 0 {
		return
	}
	mr, err := gomail.CreateReader(bytes.NewReader(raw))
	if err != nil {
		return
	}
	var plain, html string
	for {
		p, err := mr.NextPart()
		if err != nil {
			break
		}
		switch h := p.Header.(type) {
		case *gomail.InlineHeader:
			ct, _, _ := h.ContentType()
			b, _ := io.ReadAll(io.LimitReader(p.Body, maxBodyBytes+1))
			switch {
			case ct == "text/plain" && plain == "":
				plain = string(b)
			case ct == "text/html" && html == "":
				html = string(b)
			}
		case *gomail.AttachmentHeader:
			if name, err := h.Filename(); err == nil && name != "" {
				msg.Attachments = append(msg.Attachments, name)
			}
		}
	}
	body := plain
	if body == "" && html != "" {
		body, msg.BodyIsHTML = html, true
	}
	if len(body) > maxBodyBytes {
		body, msg.Truncated = body[:maxBodyBytes], true
	}
	msg.Body = body
}
