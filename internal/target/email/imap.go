package email

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"sort"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-message/charset" // registriert zudem die Charset-Decoder (ISO-8859-*, …)
	gomail "github.com/emersion/go-message/mail"
)

// IMAP-Seite des Plugins: Postfach lesen, Flags setzen, Mails verschieben.
// Jede Aktion öffnet eine frische Verbindung (Login → Arbeit → Logout) —
// Aktionen sind seltene, kurze Zugriffe; ein Verbindungspool lohnt nicht und
// hielte Credentials länger als nötig im Speicher.

// maxBodyBytes begrenzt den Text, der aus einer Mail in die Session des
// Agenten gelangt — Mails können beliebig groß sein, das Kontextfenster nicht.
const maxBodyBytes = 64 << 10

// MessageSummary ist die Listen-Ansicht einer Mail (list_unread/list_messages).
type MessageSummary struct {
	UID       uint32 `json:"uid"`
	Mailbox   string `json:"mailbox"`
	From      string `json:"from"`
	Subject   string `json:"subject"`
	Date      string `json:"date,omitempty"`
	Seen      bool   `json:"seen"`
	MessageID string `json:"message_id,omitempty"`
}

// Message ist die Detail-Ansicht (get_message) inkl. extrahiertem Text.
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

// withIMAP kapselt Verbindung + Login + Logout um eine Postfach-Operation.
func withIMAP(cfg Config, fn func(c *imapclient.Client) error) error {
	c, err := dialIMAP(cfg)
	if err != nil {
		return fmt.Errorf("imap-verbindung %s: %w", cfg.IMAPAddr, err)
	}
	defer c.Close()
	if err := c.Login(cfg.Username, cfg.Password).Wait(); err != nil {
		return fmt.Errorf("imap-login als %s: %w", cfg.Username, err)
	}
	err = fn(c)
	_ = c.Logout().Wait()
	return err
}

// listMailboxes liefert die Ordner-Namen des Postfachs.
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

// listMessages liefert die neuesten Mails eines Ordners (neueste zuerst).
// unseenOnly beschränkt auf ungelesene; eigene Mails (Absender = eigene
// Adresse) werden dann übersprungen — Echo-Schutz gegen Selbst-Bearbeitung.
// Absender außerhalb von COVEY_EMAIL_INTAKE_ADDRESSES werden ausgeblendet.
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
		// Nur den jüngsten Ausschnitt holen — UIDs steigen chronologisch.
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

// getMessage holt eine Mail vollständig (Header + extrahierter Text).
// BODY.PEEK lässt das \Seen-Flag unangetastet — gelesen markiert der Agent
// explizit mit mark_seen, wenn er die Mail bearbeitet hat.
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
			return fmt.Errorf("keine mail mit uid %d in %q", uid, mailbox)
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

// setSeen setzt bzw. löscht das \Seen-Flag einer Mail.
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

// moveMessage verschiebt eine Mail in einen anderen Ordner (MOVE bzw. der
// COPY+EXPUNGE-Fallback der Client-Library für Server ohne MOVE-Capability).
func moveMessage(cfg Config, mailbox string, uid uint32, dest string) error {
	return withIMAP(cfg, func(c *imapclient.Client) error {
		if _, err := c.Select(mailbox, nil).Wait(); err != nil {
			return fmt.Errorf("mailbox %q: %w", mailbox, err)
		}
		if _, err := c.Move(imap.UIDSetNum(imap.UID(uid)), dest).Wait(); err != nil {
			return fmt.Errorf("verschieben nach %q: %w", dest, err)
		}
		return nil
	})
}

// ensureAngles normalisiert eine Message-ID auf die Header-Form <id> —
// manche Server liefern die Envelope-ID ohne die spitzen Klammern.
func ensureAngles(id string) string {
	id = strings.TrimSpace(id)
	if id == "" || strings.HasPrefix(id, "<") {
		return id
	}
	return "<" + id + ">"
}

// addrList reduziert Envelope-Adressen auf "mailbox@host"-Strings.
func addrList(addrs []imap.Address) []string {
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		if addr := a.Addr(); addr != "" {
			out = append(out, addr)
		}
	}
	return out
}

// summarize baut die Listen-Ansicht aus einem Fetch-Ergebnis.
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

// extractBody zieht den lesbaren Text aus der rohen Mail: bevorzugt
// text/plain, sonst text/html (gekennzeichnet); Attachments nur dem Namen
// nach. Parse-Fehler sind kein Abbruch — was extrahiert wurde, wird geliefert.
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
