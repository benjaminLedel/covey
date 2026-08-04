# Operations: connecting Covey to a mailbox (IMAP/SMTP)

A practical runbook for the `email` plugin: **a mailbox of the agent's own**,
read by IMAP, answered by SMTP. No product-specific API — every mailbox with
IMAP/SMTP access works (your own mail server, Gmail/Workspace with an app
password, Office 365 with app-password auth, …).

> Short version: create a mailbox, set two secrets, unlock it in ACCESS.md,
> set up a heartbeat for the inbox. Email knows **no webhooks** — the intake
> runs exclusively by polling through `HEARTBEAT.md`.

---

## 1. Overview of the data flow

```
Mailbox  ◄──(IMAP, TLS)──────────  agent (sandbox, Claude Code)
             list_unread,            │ actions through the action proxy,
             get_message,            │ credentials brokered per call
             get_attachment,         │ (email_url + email_token)
             mark_seen, move         │
Recipient ◄──(SMTP, TLS)──────────── reply / send
```

Both directions run **outbound from the sandbox** — there is no inbound channel
into the control plane. New mail is discovered by the agent itself: the
heartbeat wakes it, `list_unread` delivers the working set.

---

## 2. Step-by-step instructions

### 2.1 At the mail provider: a mailbox + an app password

1. Create **a mailbox of its own** for the agent (e.g.
   `support-agent@example.com`). Never share a person's account — the agent
   marks mail as read and moves it around.
2. Create an **app password** (mandatory at Gmail/Office 365, since the account
   password does not apply to IMAP/SMTP basic auth there).
3. Enable IMAP and SMTP access in the account and note the mail server host
   (e.g. `mail.example.com`; both endpoints only with separate IMAP/SMTP hosts
   or unusual ports).

### 2.2 In Covey: deposit the secrets

The broker passes exactly two secrets through per system. Normally the mail
server, the address and the password suffice:

| Secret | Value | Purpose |
|---|---|---|
| `email_url` | `mail.example.com` | the mail server host |
| `email_token` | `support-agent@example.com:app-password` | the login (address `:` password) |

The short form expands into the standard setup: IMAP with TLS on port 993 and
SMTP submission with STARTTLS on port 587, both on the same host.

If IMAP and SMTP sit on separate hosts or deviate in ports/TLS modes,
`email_url` encodes **both** endpoints as URLs (separated by a space):

| Secret | Value |
|---|---|
| `email_url` | `imaps://imap.example.com:993 smtp://smtp.example.com:587` |

Schemes and default ports:

| Scheme | Transport | Default port |
|---|---|---|
| `imaps://` | TLS from the first byte | 993 |
| `imap://` | STARTTLS | 143 |
| `smtps://` | TLS from the first byte | 465 |
| `smtp://` | STARTTLS | 587 |
| `imap+insecure://`, `smtp+insecure://` | plaintext — **tests/demos only** | 143 / 25 |

If the login name deviates from the mail address (some providers demand an
account name instead of the address), append the sender address explicitly to
the SMTP URL: `smtp://smtp.example.com:587?from=support-agent@example.com`.

### 2.3 The agent's ACCESS.md

```markdown
- system: email scope: read,write
```

### 2.4 Intake by heartbeat

Email has no webhook — the inbox is polled through `HEARTBEAT.md`. With
`nur-wenn: email` ("only-if") the **control plane** itself checks by IMAP
before every run whether unread mail is present, and wakes the agent only then
— the interval can therefore be short without producing runs (and cost) with
an empty mailbox:

```markdown
- alle: 5m nur-wenn: email titel: Posteingang sichten aufgabe: Hole mit
  list_unread die ungelesenen Mails. Bearbeite jede einzeln: get_message
  lesen, sachlich per reply antworten; Mails ohne Antwortbedarf mit mark_seen
  abhaken oder mit move ablegen. Antworte nie auf Automaten-Mails (Newsletter,
  Zustellfehler, Abwesenheitsnotizen).
```

The advance check uses the same filter path as `list_unread` (echo protection,
`COVEY_EMAIL_INTAKE_ADDRESSES`) — what the agent would not see does not wake it
either. It is fail-open: if the IMAP check fails, the heartbeat fires as usual.
Without `nur-wenn:` every run fires; the agent then establishes for itself that
there is nothing there.

The read status is the working-set signal: `reply` marks the mail as read
automatically, everything else the agent ticks off explicitly. A
`get_message` sets **no** read flag (BODY.PEEK) — a mail that has only been
read, not worked on, stays in the working set at the next run.

### 2.5 Egress release

The sandbox has to be allowed to reach the IMAP and SMTP hosts — release both
hosts (including ports) in the agent's egress configuration.

---

## 3. Setting the behaviour deliberately

### 3.1 A sending allowlist (recommended)

Without a restriction the agent may send to **arbitrary** addresses. The
allowlist limits recipients daemon-side, in addition to the central guard
rails (fail-closed per address):

```bash
COVEY_EMAIL_SEND_DOMAINS="example.com, partner.de, boss@external.com"
```

Entries are domains or complete addresses; empty = no restriction.

### 3.2 Intake filter

Publicly reachable mailboxes get spam. The intake filter hides senders outside
the allowlist from `list_unread`/`list_messages`:

```bash
COVEY_EMAIL_INTAKE_ADDRESSES="example.com, customer-a.com"
```

### 3.3 Loop protection

Three mechanisms prevent mail loops:

1. `list_unread` skips mail whose sender is its own address.
2. A `reply` to its own sender address is refused (echo protection).
3. The system prompt instructs the agent not to answer machine-generated mail
   (newsletters, bounces, out-of-office notices) but to tick it off.

Two Covey agents writing mail to each other are **not** caught by this — exclude
such constellations through `COVEY_EMAIL_INTAKE_ADDRESSES` or guard rails.

### 3.4 Waiting for replies — without `blocked`

There is no webhook that could wake a blocked task. The agent should therefore
**not** hold mail threads open with the status `blocked` but end the run
regularly with `done` (the interim state as a note). The counterpart's reply
appears at the next heartbeat as new unread mail in the same subject thread;
`reply` sets the correct threading headers (`In-Reply-To`, `References`) so
that mail clients hold the thread together.

### 3.5 Reading attachments

`get_message` lists attachments by **name** only — the bytes stay out of the
context window. If the agent needs the content, it fetches exactly one
attachment into its sandbox:

```bash
get_attachment {"uid":42, "mailbox":"INBOX", "name":"invoice.pdf"}
```

The file lands under `<workspace>/attachments/<file>`; the answer names
`path`, `content_type` and `bytes`. The agent then reads it with the read
tool — images by vision, ZIP archives after `unzip` (contained in the sandbox
image). As with GitLab (`download_upload`) and Teams
(`download_attachment`), the bytes do **not** go through the control plane.

- The file name is nailed to the basename — no escape out of
  `attachments/`. If two attachments of **the same mail** carry the same name,
  the first in MIME order wins.
- **Identical names from different sources do not collide.** If another file
  of that name already sits under `attachments/`, the new one gets a
  counter: `invoice.pdf`, `invoice-2.pdf`, `invoice-3.pdf`. Only with
  byte-identical content does the existing path stay, so that a second fetch of
  the same attachment does not pile up copies. This holds across target
  systems: Teams writes into **the same** `attachments/` of the same sandbox.
- The size limit applies fail-closed before anything is written — an
  oversized attachment leaves no partial file behind:

```bash
COVEY_EMAIL_ATTACHMENT_MAX_MB=25   # default 25 MB, valid 1–1024
```

  Values above 1024 are clamped to 1024, and unreadable ones (`0`, `-3`,
  `lots`) leave the default in place. Both cases appear as `WARN` in the log —
  a silently different limit would be the worse answer.

- **Only the requested attachment** is fetched, not the whole mail: the
  BODYSTRUCTURE names the location, encoding and size of every part, so an
  oversized attachment is refused before its bytes even flow. Only when the
  server delivers no usable structure is the mail read in full — and then at
  most up to four times the limit, otherwise the action aborts. That way a mail
  with a huge attachment costs no memory as long as the agent does not request
  it.
- Like `get_message`, `get_attachment` sets **no** read flag (BODY.PEEK).
- Guard-rail subject: `email:get_attachment` — purely read-only towards the
  mailbox.

---

## 4. Action reference

| Action | Parameters | Effect |
|---|---|---|
| `list_mailboxes` | `{}` | The mailbox's folders |
| `list_unread` | `mailbox` (default `INBOX`), `limit` (default 20, max 100) | Unread mail, newest first |
| `list_messages` | as above | The newest mail regardless of status |
| `get_message` | `uid`, `mailbox` | The complete mail (text preferring `text/plain`, max. 64 KiB, attachment names) |
| `get_attachment` | `uid`, `mailbox`, `name` | Loads ONE attachment into `<workspace>/attachments/<file>` (read tool, images by vision) |
| `reply` | `uid`, `mailbox`, `body`, `reply_all` | A reply by SMTP + setting `\Seen` |
| `send` | `to[]`, `cc[]`, `subject`, `body` | A new mail by SMTP |
| `mark_seen` / `mark_unseen` | `uid`, `mailbox` | Set/clear the read flag |
| `move` | `uid`, `mailbox`, `to_mailbox` | Move mail (MOVE, otherwise a COPY+EXPUNGE fallback) |

Guard-rail subjects: `email:<action>` — sending is separately governable
through `email:send` and `email:reply` (e.g. an approval requirement for
outbound mail).

---

## 5. Limits of the current state

- **Text only when sending:** `send`/`reply` dispatch `text/plain` (UTF-8,
  quoted-printable). No HTML, no attachments when sending. Incoming
  attachments, by contrast, the agent fetches into its sandbox with
  `get_attachment` (section 3.5).
- **Inline attachments:** parts with `Content-Disposition: inline` — images
  embedded in HTML mail, for instance — do not count as attachments:
  `get_message` does not list them, `get_attachment` does not fetch them.
- **Basic auth:** login by user/password (or app password). OAuth2
  (XOAUTH2, e.g. Gmail without app passwords) is not implemented.
- **Polling latency:** the reaction time = the heartbeat interval. For
  minute-accurate reaction lower the interval — and keep an eye on the cost per
  run.
- **One mailbox per agent:** the secret pair applies per agent; several
  mailboxes require several agents.
