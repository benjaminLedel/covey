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
		Description: "Ein eigenes Mail-Postfach für den Agenten: Posteingang per IMAP sichten (list_unread/get_message), per SMTP antworten oder senden (reply/send), Ablage per mark_seen/move. Intake per HEARTBEAT.md (Polling, kein Webhook). Auth per Secrets email_url (IMAP- + SMTP-Endpoint) und email_token (benutzer:passwort).",
		Kind:        "builtin",
		System:      System{},
		SetupDoc: `1. Beim Mail-Provider ein eigenes Postfach für den Agenten anlegen
   (z. B. support-agent@example.com) und ein App-Passwort erzeugen —
   niemals das Passwort eines menschlichen Kontos verwenden.

2. Unter Secrets hinterlegen und dem Agenten zuweisen:
   email_url   = imaps://imap.example.com:993 smtp://smtp.example.com:587
                 (Schemata: imaps/smtps = TLS, imap/smtp = STARTTLS;
                  weicht der Login von der Mail-Adresse ab:
                  ?from=support-agent@example.com an die SMTP-URL hängen)
   email_token = support-agent@example.com:app-passwort

3. In der ACCESS.md des Agenten freischalten:
   - system: email scope: read,write

4. Intake per Heartbeat — in der HEARTBEAT.md des Agenten:
   - alle: 15m titel: Posteingang sichten aufgabe: Hole mit list_unread die
     ungelesenen Mails, bearbeite jede einzeln (get_message, dann reply)
     und markiere Bearbeitetes mit mark_seen.

5. Optionale Prozess-Env:
   COVEY_EMAIL_SEND_DOMAINS="example.com, partner.de"   (Versand-Allowlist;
                                                         leer = alle Empfänger)
   COVEY_EMAIL_INTAKE_ADDRESSES="example.com"           (nur diese Absender
                                                         im Arbeitsvorrat)

6. Die IMAP-/SMTP-Hosts müssen aus der Sandbox erreichbar sein
   (Egress-Freigabe für beide Hosts).

Details: docs/betrieb-email.md im Repository.`,
	})
}

func (System) Name() string { return "email" }

// VerifyWebhook/ParseWebhook: Mail kennt keine Webhooks — der Intake läuft
// per Heartbeat-Polling; Antworten erscheinen als neue ungelesene Mail.
func (System) VerifyWebhook(string, []byte, http.Header) bool { return false }

func (System) ParseWebhook([]byte) (target.WebhookEvent, error) {
	return target.WebhookEvent{}, fmt.Errorf("email hat keinen webhook-eingang (intake per heartbeat)")
}

// ActionSubject: jeder SMTP-Versand verlässt die Organisation — send und
// reply sind eigene, scharf regelbare Guard-Rail-Subjekte.
func (System) ActionSubject(action string, _ json.RawMessage) string {
	return "email:" + action
}

func (System) Execute(ctx context.Context, action string, params json.RawMessage, cred target.Credential) (any, error) {
	cfg, err := ParseConfig(cred)
	if err != nil {
		return nil, err
	}

	var in struct {
		Mailbox   string   `json:"mailbox"`
		UID       uint32   `json:"uid"`
		ToMailbox string   `json:"to_mailbox"`
		Limit     int      `json:"limit"`
		To        []string `json:"to"`
		Cc        []string `json:"cc"`
		Subject   string   `json:"subject"`
		Body      string   `json:"body"`
		ReplyAll  bool     `json:"reply_all"`
	}
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

	switch action {
	case "list_mailboxes":
		return listMailboxes(cfg)
	case "list_unread":
		return listMessages(cfg, in.Mailbox, true, in.Limit)
	case "list_messages":
		return listMessages(cfg, in.Mailbox, false, in.Limit)
	case "get_message":
		if in.UID == 0 {
			return nil, fmt.Errorf("uid fehlt")
		}
		return getMessage(cfg, in.Mailbox, in.UID)
	case "mark_seen", "mark_unseen":
		if in.UID == 0 {
			return nil, fmt.Errorf("uid fehlt")
		}
		if err := setSeen(cfg, in.Mailbox, in.UID, action == "mark_seen"); err != nil {
			return nil, err
		}
		return map[string]any{"uid": in.UID, "mailbox": in.Mailbox, "seen": action == "mark_seen"}, nil
	case "move":
		if in.UID == 0 || strings.TrimSpace(in.ToMailbox) == "" {
			return nil, fmt.Errorf("uid oder to_mailbox fehlt")
		}
		if err := moveMessage(cfg, in.Mailbox, in.UID, in.ToMailbox); err != nil {
			return nil, err
		}
		return map[string]any{"uid": in.UID, "moved_to": in.ToMailbox}, nil
	case "send":
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
	case "reply":
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
	default:
		return nil, fmt.Errorf("unbekannte aktion %q", strings.TrimSpace(action))
	}
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
	return `Verfügbare E-Mail-Aktionen (dein eigenes Postfach, IMAP/SMTP): list_mailboxes {},
   list_unread {"mailbox":"INBOX","limit":20} listet ungelesene Mails (neueste zuerst; mailbox/limit optional),
   list_messages {"mailbox":"INBOX","limit":20} listet die neuesten Mails unabhängig vom Gelesen-Status,
   get_message {"uid":N,"mailbox":"INBOX"} liefert eine Mail vollständig (Absender, Empfänger, Text,
   Attachment-Namen) — das Lesen setzt KEIN Gelesen-Flag,
   reply {"uid":N,"mailbox":"INBOX","body":"...","reply_all":true|false} antwortet dem Absender per SMTP
   (korrekte Threading-Header, Betreff Re: …) und markiert die Mail danach als gelesen,
   send {"to":["a@example.com"],"cc":["..."],"subject":"...","body":"..."} sendet eine neue Mail,
   mark_seen {"uid":N,"mailbox":"..."} / mark_unseen {...} setzt bzw. löscht das Gelesen-Flag,
   move {"uid":N,"mailbox":"INBOX","to_mailbox":"Archiv"} verschiebt eine Mail in einen anderen Ordner.
   Arbeitsweise: Dein Arbeitsvorrat sind die ungelesenen Mails (list_unread). Bearbeite jede Mail einzeln:
   get_message lesen, sachlich per reply antworten, danach ist sie durch reply automatisch als gelesen
   markiert; Mails, die keine Antwort brauchen, explizit mit mark_seen abhaken oder mit move ablegen.
   Der Text (body) ist reiner Text — kein HTML, keine Markdown-Syntax.
   Antworte NIE auf offensichtliche Automaten-Mails (Newsletter, Zustellfehler, Abwesenheitsnotizen) —
   hake sie mit mark_seen ab. Antworten an deine eigene Absender-Adresse sind gesperrt (Echo-Schutz).
   WARTEN auf eine Antwort: E-Mail hat keinen Webhook — nutze KEINEN blocked-Status für Mail-Threads.
   Beende deinen Lauf regulär mit done (Zwischenstand als add_note); die Antwort erscheint beim nächsten
   Heartbeat-Lauf als neue ungelesene Mail im selben Betreff-Thread.`
}
