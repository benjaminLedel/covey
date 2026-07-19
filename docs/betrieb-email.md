# Betrieb: Covey an ein Mail-Postfach anschließen (IMAP/SMTP)

Praktisches Runbook für das `email`-Plugin: ein **eigenes Postfach für den
Agenten**, gelesen per IMAP, beantwortet per SMTP. Kein produktspezifisches
API — jedes Postfach mit IMAP/SMTP-Zugang funktioniert (eigener Mailserver,
Gmail/Workspace mit App-Passwort, Office 365 mit Auth per App-Passwort, …).

> Kurzfassung: Postfach anlegen, zwei Secrets setzen, ACCESS.md freischalten,
> Heartbeat für den Posteingang einrichten. E-Mail kennt **keine Webhooks** —
> der Intake läuft ausschließlich per Polling über `HEARTBEAT.md`.

---

## 1. Überblick des Datenflusses

```
Postfach ◄──(IMAP, TLS)──────────  Agent (Sandbox, Claude Code)
             list_unread,            │ Aktionen über den Action-Proxy,
             get_message,            │ Credentials pro Aufruf gebrokert
             mark_seen, move         │ (email_url + email_token)
Empfänger ◄──(SMTP, TLS)──────────── reply / send
```

Beide Richtungen laufen **outbound aus der Sandbox** — es gibt keinen
Inbound-Kanal in die Control Plane. Neue Mails werden vom Agenten selbst
entdeckt: der Heartbeat weckt ihn, `list_unread` liefert den Arbeitsvorrat.

---

## 2. Schritt-für-Schritt-Anleitung

### 2.1 Beim Mail-Provider: Postfach + App-Passwort

1. Ein **eigenes Postfach** für den Agenten anlegen (z. B.
   `support-agent@example.com`). Niemals das Konto eines Menschen mitnutzen —
   der Agent markiert Mails als gelesen und verschiebt sie.
2. Ein **App-Passwort** erzeugen (bei Gmail/Office 365 Pflicht, da das
   Kontopasswort dort nicht für IMAP/SMTP-Basic-Auth gilt).
3. IMAP- und SMTP-Zugang im Konto aktivieren, Endpunkte notieren
   (z. B. `imap.example.com:993` und `smtp.example.com:587`).

### 2.2 In Covey: Secrets hinterlegen

Der Broker reicht pro System genau zwei Secrets durch — `email_url` kodiert
deshalb **beide** Endpunkte als URLs (durch Leerzeichen getrennt):

| Secret | Wert | Zweck |
|---|---|---|
| `email_url` | `imaps://imap.example.com:993 smtp://smtp.example.com:587` | beide Endpunkte |
| `email_token` | `support-agent@example.com:app-passwort` | Login (Benutzer `:` Passwort) |

Schemata und Default-Ports:

| Schema | Transport | Default-Port |
|---|---|---|
| `imaps://` | TLS ab dem ersten Byte | 993 |
| `imap://` | STARTTLS | 143 |
| `smtps://` | TLS ab dem ersten Byte | 465 |
| `smtp://` | STARTTLS | 587 |
| `imap+insecure://`, `smtp+insecure://` | Klartext — **nur Tests/Demos** | 143 / 25 |

Weicht der Login-Name von der Mail-Adresse ab (manche Provider verlangen
Kontonamen statt Adresse), die Absender-Adresse explizit an die SMTP-URL
hängen: `smtp://smtp.example.com:587?from=support-agent@example.com`.

### 2.3 ACCESS.md des Agenten

```markdown
- system: email scope: read,write
```

### 2.4 Intake per Heartbeat

E-Mail hat keinen Webhook — der Posteingang wird per `HEARTBEAT.md` gepollt:

```markdown
- alle: 15m titel: Posteingang sichten aufgabe: Hole mit list_unread die
  ungelesenen Mails. Bearbeite jede einzeln: get_message lesen, sachlich per
  reply antworten; Mails ohne Antwortbedarf mit mark_seen abhaken oder mit
  move ablegen. Antworte nie auf Automaten-Mails (Newsletter, Zustellfehler,
  Abwesenheitsnotizen).
```

Der Gelesen-Status ist das Arbeitsvorrats-Signal: `reply` markiert die Mail
automatisch als gelesen, alles andere hakt der Agent explizit ab. Ein
`get_message` setzt **kein** Gelesen-Flag (BODY.PEEK) — eine nur gelesene,
nicht bearbeitete Mail bleibt im nächsten Lauf im Vorrat.

### 2.5 Egress-Freigabe

Die Sandbox muss die IMAP- und SMTP-Hosts erreichen dürfen — beide Hosts
(inkl. Ports) in der Egress-Konfiguration des Agenten freigeben.

---

## 3. Verhalten bewusst einstellen

### 3.1 Versand-Allowlist (empfohlen)

Ohne Einschränkung darf der Agent an **beliebige** Adressen senden. Die
Allowlist begrenzt Empfänger daemon-seitig, zusätzlich zu den zentralen
Guard-Rails (fail-closed pro Adresse):

```bash
COVEY_EMAIL_SEND_DOMAINS="example.com, partner.de, chef@extern.de"
```

Einträge sind Domains oder vollständige Adressen; leer = keine Einschränkung.

### 3.2 Intake-Filter

Öffentlich erreichbare Postfächer bekommen Spam. Der Intake-Filter blendet
Absender außerhalb der Allowlist aus `list_unread`/`list_messages` aus:

```bash
COVEY_EMAIL_INTAKE_ADDRESSES="example.com, kunde-a.de"
```

### 3.3 Schleifenschutz

Drei Mechanismen verhindern Mail-Schleifen:

1. `list_unread` überspringt Mails, deren Absender die eigene Adresse ist.
2. `reply` an die eigene Absender-Adresse wird verweigert (Echo-Schutz).
3. Der System-Prompt weist den Agenten an, Automaten-Mails (Newsletter,
   Bounces, Abwesenheitsnotizen) nicht zu beantworten, sondern abzuhaken.

Zwei Covey-Agenten, die sich gegenseitig Mails schreiben, fängt das **nicht**
ab — solche Konstellationen über `COVEY_EMAIL_INTAKE_ADDRESSES` bzw.
Guard-Rails ausschließen.

### 3.4 Warten auf Antworten — ohne `blocked`

Es gibt keinen Webhook, der eine geblockte Aufgabe wecken könnte. Der Agent
soll Mail-Threads deshalb **nicht** mit Status `blocked` offenhalten, sondern
den Lauf regulär mit `done` beenden (Zwischenstand als Notiz). Die Antwort des
Gegenübers erscheint beim nächsten Heartbeat als neue ungelesene Mail im
selben Betreff-Thread; `reply` setzt die korrekten Threading-Header
(`In-Reply-To`, `References`), damit Mail-Clients den Thread zusammenhalten.

---

## 4. Aktions-Referenz

| Aktion | Parameter | Wirkung |
|---|---|---|
| `list_mailboxes` | `{}` | Ordner des Postfachs |
| `list_unread` | `mailbox` (Default `INBOX`), `limit` (Default 20, max 100) | ungelesene Mails, neueste zuerst |
| `list_messages` | wie oben | neueste Mails unabhängig vom Status |
| `get_message` | `uid`, `mailbox` | vollständige Mail (Text bevorzugt `text/plain`, max. 64 KiB, Attachment-Namen) |
| `reply` | `uid`, `mailbox`, `body`, `reply_all` | Antwort per SMTP + `\Seen` setzen |
| `send` | `to[]`, `cc[]`, `subject`, `body` | neue Mail per SMTP |
| `mark_seen` / `mark_unseen` | `uid`, `mailbox` | Gelesen-Flag setzen/löschen |
| `move` | `uid`, `mailbox`, `to_mailbox` | Mail verschieben (MOVE, sonst COPY+EXPUNGE-Fallback) |

Guard-Rail-Subjekte: `email:<aktion>` — Versand ist über `email:send` und
`email:reply` separat regelbar (z. B. Approval-Pflicht für ausgehende Mails).

---

## 5. Grenzen des aktuellen Stands

- **Nur Text:** `send`/`reply` verschicken `text/plain` (UTF-8,
  quoted-printable). Kein HTML, keine Attachments im Versand; eingehende
  Attachments werden nur dem Namen nach gelistet, nicht heruntergeladen.
- **Basic-Auth:** Login per Benutzer/Passwort (bzw. App-Passwort). OAuth2
  (XOAUTH2, z. B. Gmail ohne App-Passwörter) ist nicht implementiert.
- **Polling-Latenz:** Reaktionszeit = Heartbeat-Intervall. Für Minuten-genaue
  Reaktion das Intervall senken — und die Kosten pro Lauf im Blick behalten.
- **Ein Postfach pro Agent:** Das Secret-Paar gilt pro Agent; mehrere
  Postfächer erfordern mehrere Agenten.
