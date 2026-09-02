---
slug: mail
title: Mail from the installation
description: 'Configuring the SMTP server an instance sends its own mail through: the settings, the test mail, and why registration stays closed until one has arrived.'
---

Two different things send mail in covey, and they have nothing to do with each
other:

- **An agent's mail.** Its own mailbox, read by IMAP, answered by SMTP,
  brokered per call. That is the [email target
  system](../integrations/email.md) and it is configured per organisation.
- **The installation's mail** — this page. A confirmation link, a password
  reset, a notification that something is waiting for a decision. From the
  instance itself, to one person, no mailbox of its own.

> Short version: *Platform → E-mail*, enter the server, save, send a test mail.
> Until that test arrives, `signup.mode` cannot leave `off`.

## 1. What you need

A mailbox the instance may send from — a mail server of your own, or a hosted
one with SMTP submission. Not an inbox: nothing is read here, only sent. An
address like `no-reply@example.com` is the usual shape.

At providers that require it (Gmail/Workspace, Office 365) this is an **app
password**, not the account password.

## 2. The settings

*Platform → E-mail*, visible to a `system_admin`. The page writes into the
instance's own settings table, not into the environment — a wrong SMTP host is
corrected here and takes effect on the next send, with no redeploy and no
restart.

| Setting | Meaning |
|---|---|
| `mail.smtp_host` | the server, **without a scheme and without a port** (`mail.example.com`) |
| `mail.smtp_port` | `587` for STARTTLS, `465` for TLS from the first byte |
| `mail.security` | `starttls` \| `tls` \| `none` |
| `mail.smtp_user` | the login name — often the sender address, but not always |
| `mail.smtp_password` | sealed with the master key, **never returned**: the field shows whether one is stored, and an empty save clears it |
| `mail.from` | the sender address |
| `mail.from_name` | the name in front of it; empty falls back to `site.name` |

`none` sends in plain text and exists for a local double, not for a mailbox.

Two more settings decide what the mails contain, under *Platform → Settings*:

| Setting | Meaning |
|---|---|
| `site.url` | the address this installation is reachable at from outside (`https://covey.example.com`) — every link in a mail is built from it. It takes precedence over `COVEY_SITE_URL`; empty means the variable, and for notification mails no links at all |
| `notify.window` | how long notification events are collected before a mail goes out — a duration such as `5m` or `1h`, at most `24h`; `0s` sends on the sender's next pass, which runs once a minute |
| `notify.decision`, `notify.task`, `notify.cost`, `notify.ops` | the installation's master switch per notification class, `on` or `off`. `off` overrides every account's own switch: nothing of that class is written down for anybody, and rows still waiting when the switch is flipped are retired rather than sent |

## 3. The test mail

The button sends to the address of the administrator pressing it, over exactly
the path a real mail takes: the same sender, the same stored settings, only the
body differs. A test that travelled its own route could pass while every real
mail failed.

It sends the settings **as stored**, not what stands in the form — save first,
then test, so that what was proven is what will run. The button stays disabled
while something is unsaved.

The result is kept (`mail.last_test_at`, `mail.last_test_error`) and shown under
the card. Whoever opens registration a week later can see when this last worked
instead of having to remember.

### When it fails

The SMTP server's answer is passed through verbatim, because it is more precise
than any sentence that could replace it:

| What it says | Where to look |
|---|---|
| `connection refused` | port or host — 587 and 465 are not interchangeable |
| `smtp starttls: …` | the server wants TLS from the first byte: `mail.security = tls`, port 465 |
| `535 5.7.8 authentication failed` | user or password; at Gmail/Office 365 an app password is required |
| `550 … not permitted to send` | the mailbox may not send under this `mail.from` |
| a timeout after 15 seconds | a firewall between instance and mail server that swallows the packets |

## 4. What the installation sends

Four messages, all of them in the language the sender page was in — the
catalogues are the interface's own (`web/src/locales/*.json`), so a mail
arrives in the language somebody registered in. Each goes out as
`multipart/alternative`: a plain-text part, and beside it the same content as
HTML in the interface's design language. The HTML loads nothing — no font, no
image, no stylesheet from anywhere — so a client that blocks remote content
shows the same mail as one that does not, and the text part is the whole mail
for a client that shows no HTML.

| Mail | Trigger | Lifetime of its link |
|---|---|---|
| Confirmation | a registration, and *send a new link* on `/verify` | 24 hours, one use |
| Password reset | *forgotten your password?* on the sign-in page | one hour, one use |
| Test mail | the button on this page | — |
| Notifications | something needs a person: a waiting decision, a finished task, a budget cap, a runner that left | — |

Notification mails are grouped: an event is sent after the `notify.window`
has passed, together with whatever joined it in that window, and what has been
dealt with by then produces no mail at all. Every person chooses per class on
their own account page which of them they want; the installation's
`notify.<class>` switches stand above that choice, and a class switched off
there shows greyed out on the account page.

**Set `site.url` on a public instance.** The links in those mails need a host.
The notification sender has no request to derive one from and leaves the links
out while neither the setting nor `COVEY_SITE_URL` is set; the confirmation and
reset mails fall back to the request — which means to the `Host` header. Behind
a proxy that passes the origin through, that is right; where anybody can set
the header, it lets a stranger decide which domain a confirmation link points
at, and the mail would carry the token there. The setting closes both gaps
from the same page the mail server is configured on.

## 5. Why registration hangs on it

`signup.mode` cannot leave `off` while no test mail has ever gone through. That
is deliberate: an instance that opens self-registration without a working mailer
produces accounts whose confirmation link is never sent — and the people
affected cannot report it, because reporting it would require an account.

A filled-in host is not evidence. A delivered message is.

## 6. Trying it out locally

`demo/fakemail` (`go run ./demo/fakemail`) is a mail double: it accepts
everything and keeps a log of what arrived. For a development instance:

```
mail.smtp_host      127.0.0.1
mail.smtp_port      1025
mail.security       none
mail.from           covey@example.test
```

What arrived is then listed by `curl http://localhost:8025/mails` — and the
test mail travels the same path a real server would see, TLS aside.
