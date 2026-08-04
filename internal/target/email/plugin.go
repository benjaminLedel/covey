package email

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"covey/internal/target"
)

// System bindet ein Mail-Postfach (IMAP/SMTP) als Zielsystem-Plugin an die
// target-Registry: Posteingang lesen (IMAP), antworten und senden (SMTP),
// Ablage per Flags/Ordnern. Es gibt keinen Webhook-Eingang — Mail kennt
// keine Pushes; der Intake läuft per HEARTBEAT.md-Polling wie beim
// GitLab-Plugin.
type System struct{}

func init() {
	target.Register(target.Descriptor{
		Name:        "email",
		Label:       "E-Mail (IMAP/SMTP)",
		Description: "Ein eigenes Mail-Postfach für den Agenten: Posteingang per IMAP sichten (list_unread/get_message), Anhänge in die Sandbox laden und lesen (get_attachment), per SMTP antworten oder senden (reply/send), Ablage per mark_seen/move. Intake per HEARTBEAT.md (Polling, kein Webhook). Auth per Secrets email_url (Mailserver-Host, z. B. mail.example.com) und email_token (adresse:passwort).",
		Kind:        "builtin",
		Category:    target.CategoryComms,
		System:      System{},
		SetupDoc: `1. Beim Mail-Provider ein eigenes Postfach für den Agenten anlegen
   (z. B. support-agent@example.com) und ein App-Passwort erzeugen —
   niemals das Passwort eines menschlichen Kontos verwenden.

2. Unter Secrets hinterlegen und dem Agenten zuweisen:
   email_url   = mail.example.com          (der Mailserver-Host genügt:
                 IMAP mit TLS auf 993, SMTP mit STARTTLS auf 587)
   email_token = support-agent@example.com:app-passwort

   Abweichende Hosts, Ports oder TLS-Modi als explizite URLs:
   email_url   = imaps://imap.example.com:993 smtp://smtp.example.com:587
                 (Schemata: imaps/smtps = TLS, imap/smtp = STARTTLS;
                  weicht der Login von der Mail-Adresse ab:
                  ?from=support-agent@example.com an die SMTP-URL hängen)

3. In der ACCESS.md des Agenten freischalten:
   - system: email scope: read,write

4. Intake per Heartbeat — in der HEARTBEAT.md des Agenten:
   - alle: 5m nur-wenn: email titel: Posteingang sichten aufgabe: Hole mit
     list_unread die ungelesenen Mails, bearbeite jede einzeln (get_message,
     dann reply) und markiere Bearbeitetes mit mark_seen.
   (nur-wenn: email — die Control Plane prüft vor jedem Lauf selbst per
    IMAP, ob ungelesene Mails vorliegen, und weckt den Agenten nur dann.)

5. Optionale Prozess-Env:
   COVEY_EMAIL_SEND_DOMAINS="example.com, partner.de"   (Versand-Allowlist;
                                                         leer = alle Empfänger)
   COVEY_EMAIL_INTAKE_ADDRESSES="example.com"           (nur diese Absender
                                                         im Arbeitsvorrat)
   COVEY_EMAIL_ATTACHMENT_MAX_MB=25                     (Größenlimit je Anhang
                                                         für get_attachment;
                                                         gültig 1-1024, darüber
                                                         wird geklemmt)

6. Die IMAP-/SMTP-Hosts müssen aus der Sandbox erreichbar sein
   (Egress-Freigabe für beide Hosts).

Details: docs/ops-email.md im Repository.`,
	})
}

func (System) Name() string { return "email" }

// VerifyWebhook/ParseWebhook: Mail kennt keine Webhooks — der Intake läuft
// per Heartbeat-Polling; Antworten erscheinen als neue ungelesene Mail.
func (System) VerifyWebhook(string, []byte, http.Header) bool { return false }

func (System) ParseWebhook([]byte) (target.WebhookEvent, error) {
	return target.WebhookEvent{}, fmt.Errorf("email hat keinen webhook-eingang (intake per heartbeat)")
}

// HasWork (target.WorkChecker): billiger Vorab-Check der Control Plane für
// nur-wenn:-Heartbeats — liegt mindestens eine ungelesene Mail im INBOX-
// Arbeitsvorrat? Nutzt denselben Pfad wie list_unread, damit Echo-Schutz und
// COVEY_EMAIL_INTAKE_ADDRESSES identisch greifen: was der Agent nicht sähe,
// weckt ihn auch nicht.
func (System) HasWork(_ context.Context, cred target.Credential) (bool, error) {
	cfg, err := ParseConfig(cred)
	if err != nil {
		return false, err
	}
	msgs, err := listMessages(cfg, "INBOX", true, 100)
	if err != nil {
		return false, err
	}
	return len(msgs) > 0, nil
}

// ActionSubject: jeder SMTP-Versand verlässt die Organisation — send und
// reply sind eigene, scharf regelbare Guard-Rail-Subjekte.
func (System) ActionSubject(action string, _ json.RawMessage) string {
	return "email:" + action
}

// aktionsParams ist die Vereinigung aller Parameter, die irgendeine Aktion
// dieses Zielsystems braucht — der Agent schickt ein flaches JSON-Objekt,
// was darin fehlt, bleibt leer.
type aktionsParams struct {
	Mailbox   string   `json:"mailbox"`
	UID       uint32   `json:"uid"`
	ToMailbox string   `json:"to_mailbox"`
	Limit     int      `json:"limit"`
	To        []string `json:"to"`
	Cc        []string `json:"cc"`
	Subject   string   `json:"subject"`
	Body      string   `json:"body"`
	ReplyAll  bool     `json:"reply_all"`
	Name      string   `json:"name"`
}

// aktion fuehrt EINE Aktion aus. Frueher war jede ein Fall in einem langen
// switch; jetzt ist sie fuer sich lesbar und die Verteilung eine Tabelle.
type aktion func(ctx context.Context, cfg Config, action string, in aktionsParams) (any, error)

var aktionen = map[string]aktion{
	"list_mailboxes": func(ctx context.Context, cfg Config, action string, in aktionsParams) (any, error) {
		return listMailboxes(cfg)
	},
	"list_unread": func(ctx context.Context, cfg Config, action string, in aktionsParams) (any, error) {
		return listMessages(cfg, in.Mailbox, true, in.Limit)
	},
	"list_messages": func(ctx context.Context, cfg Config, action string, in aktionsParams) (any, error) {
		return listMessages(cfg, in.Mailbox, false, in.Limit)
	},
	"get_message": func(ctx context.Context, cfg Config, action string, in aktionsParams) (any, error) {
		if in.UID == 0 {
			return nil, fmt.Errorf("uid fehlt")
		}
		return getMessage(cfg, in.Mailbox, in.UID)
	},
	"get_attachment": func(ctx context.Context, cfg Config, action string, in aktionsParams) (any, error) {
		if in.UID == 0 {
			return nil, fmt.Errorf("uid fehlt")
		}
		if strings.TrimSpace(in.Name) == "" {
			return nil, fmt.Errorf("name fehlt")
		}
		return getAttachmentToSandbox(cfg, in.Mailbox, in.UID, in.Name, target.Workdir(ctx))
	},
	"mark_seen": func(ctx context.Context, cfg Config, action string, in aktionsParams) (any, error) {
		if in.UID == 0 {
			return nil, fmt.Errorf("uid fehlt")
		}
		if err := setSeen(cfg, in.Mailbox, in.UID, action == "mark_seen"); err != nil {
			return nil, err
		}
		return map[string]any{"uid": in.UID, "mailbox": in.Mailbox, "seen": action == "mark_seen"}, nil
	},
	"move": func(ctx context.Context, cfg Config, action string, in aktionsParams) (any, error) {
		if in.UID == 0 || strings.TrimSpace(in.ToMailbox) == "" {
			return nil, fmt.Errorf("uid oder to_mailbox fehlt")
		}
		if err := moveMessage(cfg, in.Mailbox, in.UID, in.ToMailbox); err != nil {
			return nil, err
		}
		return map[string]any{"uid": in.UID, "moved_to": in.ToMailbox}, nil
	},
	"send": func(ctx context.Context, cfg Config, action string, in aktionsParams) (any, error) {
		if len(in.To) == 0 || strings.TrimSpace(in.Subject) == "" || strings.TrimSpace(in.Body) == "" {
			return nil, fmt.Errorf("to, subject oder body fehlt")
		}
		to, err := parseAddrs("to", in.To)
		if err != nil {
			return nil, err
		}
		cc, err := parseAddrs("cc", in.Cc)
		if err != nil {
			return nil, err
		}
		if len(to) == 0 {
			return nil, fmt.Errorf("to: keine gültige adresse")
		}
		o := outgoing{From: cfg.From, To: to, Cc: cc, Subject: in.Subject, Body: in.Body}
		if err := sendMail(cfg, o); err != nil {
			return nil, err
		}
		return map[string]any{"sent_to": to, "cc": cc, "subject": in.Subject}, nil
	},
	"reply": func(ctx context.Context, cfg Config, action string, in aktionsParams) (any, error) {
		if in.UID == 0 || strings.TrimSpace(in.Body) == "" {
			return nil, fmt.Errorf("uid oder body fehlt")
		}
		orig, err := getMessage(cfg, in.Mailbox, in.UID)
		if err != nil {
			return nil, err
		}
		o, err := buildReply(cfg, orig, in.Body, in.ReplyAll)
		if err != nil {
			return nil, err
		}
		if err := sendMail(cfg, o); err != nil {
			return nil, err
		}
		// Beantwortet = bearbeitet: \Seen setzen, damit der nächste
		// Heartbeat-Lauf die Mail nicht erneut aufgreift (best effort).
		seenErr := setSeen(cfg, in.Mailbox, in.UID, true)
		return map[string]any{"replied_to": o.To, "cc": o.Cc, "subject": o.Subject,
			"marked_seen": seenErr == nil}, nil
	},
}

// Zweitnamen derselben Aktion: im switch zusammengefasste case-Label,
// in einer Tabelle ein Verweis.
func init() {
	aktionen["mark_unseen"] = aktionen["mark_seen"]
}

func (System) Execute(ctx context.Context, action string, params json.RawMessage, cred target.Credential) (any, error) {
	fn, ok := aktionen[action]
	if !ok {
		return nil, fmt.Errorf("unbekannte aktion %q", strings.TrimSpace(action))
	}
	cfg, err := ParseConfig(cred)
	if err != nil {
		return nil, err
	}

	var in aktionsParams
	if err := json.Unmarshal(params, &in); err != nil {
		return nil, fmt.Errorf("params: %w", err)
	}
	if in.Mailbox == "" {
		in.Mailbox = "INBOX"
	}
	if in.Limit <= 0 {
		in.Limit = 20
	}
	if in.Limit > 100 {
		in.Limit = 100
	}

	return fn(ctx, cfg, action, in)
}

// buildReply leitet Empfänger, Betreff und Threading-Header der Antwort aus
// der Original-Mail ab. Antworten an die eigene Adresse sind verboten —
// Echo-Schutz: der Agent darf nicht mit sich selbst korrespondieren.
func buildReply(cfg Config, orig *Message, body string, replyAll bool) (outgoing, error) {
	rcpt := orig.ReplyTo
	if rcpt == "" {
		rcpt = orig.From
	}
	if rcpt == "" {
		return outgoing{}, fmt.Errorf("original-mail ohne absender — keine antwort möglich")
	}
	if strings.EqualFold(rcpt, cfg.From) {
		return outgoing{}, fmt.Errorf("antwort an die eigene adresse %q verweigert (echo-schutz)", cfg.From)
	}
	to, err := parseAddrs("to", []string{rcpt})
	if err != nil {
		return outgoing{}, err
	}
	var cc []string
	if replyAll {
		for _, a := range append(append([]string{}, orig.To...), orig.Cc...) {
			if strings.EqualFold(a, cfg.From) || strings.EqualFold(a, rcpt) {
				continue
			}
			cc = append(cc, a)
		}
		if cc, err = parseAddrs("cc", cc); err != nil {
			return outgoing{}, err
		}
	}
	subject := orig.Subject
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(subject)), "re:") {
		subject = "Re: " + subject
	}
	return outgoing{
		From: cfg.From, To: to, Cc: cc, Subject: subject, Body: body,
		InReplyTo:  orig.MessageID,
		References: append(append([]string{}, orig.InReplyTo...), orig.MessageID),
	}, nil
}

func (System) PromptDoc() string {
	return `Available email actions (your own mailbox, IMAP/SMTP): list_mailboxes {},
   list_unread {"mailbox":"INBOX","limit":20} lists unread mail (newest first; mailbox/limit optional),
   list_messages {"mailbox":"INBOX","limit":20} lists the newest mail regardless of the read status,
   get_message {"uid":N,"mailbox":"INBOX"} returns a mail in full (sender, recipients, text,
   attachment names) — reading it sets NO read flag,
   get_attachment {"uid":N,"mailbox":"INBOX","name":"invoice.pdf"} loads ONE attachment of that mail into the
   sandbox (under attachments/) and returns its path; then look at it with the read tool (images by
   vision). The name comes from the attachment list of get_message,
   reply {"uid":N,"mailbox":"INBOX","body":"...","reply_all":true|false} answers the sender by SMTP
   (correct threading headers, subject Re: …) and marks the mail as read afterwards,
   send {"to":["a@example.com"],"cc":["..."],"subject":"...","body":"..."} sends a new mail,
   mark_seen {"uid":N,"mailbox":"..."} / mark_unseen {...} sets or clears the read flag,
   move {"uid":N,"mailbox":"INBOX","to_mailbox":"Archive"} moves a mail into another folder.
   How to work: your working set is the unread mail (list_unread). Work every mail individually:
   read get_message, answer factually by reply, after which it is marked as read automatically by reply;
   mail that needs no answer you tick off explicitly with mark_seen or file with move.
   The text (body) is plain text — no HTML, no Markdown syntax.
   NEVER answer obvious machine-generated mail (newsletters, delivery failures, out-of-office notices) —
   tick it off with mark_seen. Replies to your own sender address are blocked (echo protection).
   WAITING for an answer: email has no webhook — do NOT use the blocked status for mail threads.
   End your run regularly with done (the interim state as add_note); the answer appears at the next
   heartbeat run as new unread mail in the same subject thread.`
}
