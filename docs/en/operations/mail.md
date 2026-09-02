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

## 4. Why registration hangs on it

`signup.mode` cannot leave `off` while no test mail has ever gone through. That
is deliberate: an instance that opens self-registration without a working mailer
produces accounts whose confirmation link is never sent — and the people
affected cannot report it, because reporting it would require an account.

A filled-in host is not evidence. A delivered message is.

## 5. Trying it out locally

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
