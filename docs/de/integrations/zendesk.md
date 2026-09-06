---
slug: zendesk
title: Zendesk Support
description: 'Eine Zendesk-Warteschlange als covey-Wake-Quelle: die vier Credential-Formen, das Ticket als Arbeitseinheit, das Gespräch aus dem Audit-Trail neu aufgebaut, und was ein Agent zurückschreiben darf.'
---

Ein Runbook für eine **Zendesk-Support**-Warteschlange mit einem covey-Agenten.
Das Plugin kommt im [Plugin-Pack](https://github.com/benjaminLedel/covey-plugin-pack)
als `zendesk/`; der covey-Standardbinary importiert es, es gibt also nichts zu bauen
oder zu installieren — Zielsystem aktivieren, Credential hinterlegen, Wake-Quelle
wählen.

> Kurzversion: ein Credential im SecretStore (`zendesk_url` + `zendesk_token`), vier
> Auth-Formen, die der Tokenwert selbst auswählt, und zwei Wake-Quellen (Heartbeat
> ganz ohne Zendesk-Einrichtung, oder ein signierter Webhook). Das Ticket ist die
> Arbeitseinheit. Der Agent liest über `/tickets/<id>/audits.json` statt über
> `/comments`, sieht damit seine eigenen internen Notizen und kann den Thread nicht
> in der falschen Reihenfolge lesen.

Wer das andere Helpdesk in diesem Pack sucht: Zammad ist in
[`zammad.md`](../../en/integrations/zammad.md) dokumentiert (bisher nur englisch).
Beide erledigen dieselbe Arbeit und unterscheiden sich an drei Stellen — das
Credential, wie ein Gespräch gelesen werden muss, und was „eskalieren" heißt.

---

## 1. Der Datenfluss

```
Zendesk ──(Trigger-Webhook, signiert)──────►  covey  /api/webhooks/zendesk/<agent-slug>
   │                                             │  Signatur prüfen → Wake-Regel → Backlog-Aufgabe
   │                                             ▼
   │                                           Agent (Sandbox)
   │                                             │  Aktionen über den Action-Proxy
Zendesk ◄──(REST /api/v2, gebrokertes Token)──────┘  list_tickets, list_messages, reply, escalate
   ▲
   └─(Heartbeat, keine Zendesk-Einrichtung)── die Vorprüfung fragt: wartet etwas auf uns?
```

Zwei Richtungen, zwei Credentials:

- **Raus** (covey → Zendesk): REST gegen genau einen Host, die Konten-URL,
  authentisiert mit einem gebrokerten Credential, das nie in der Sandbox landet.
- **Rein** (Zendesk → covey): ein Trigger-Webhook mit Signing-Key — oder gar kein
  Eingang, wenn der Agent die Warteschlange per Heartbeat arbeitet.

---

## 2. Einrichtung

### 2.1 In Zendesk: eine Identität mit minimalen Rechten

Legen Sie einen eigenen Nutzer an („covey-Agent"), statt Ihren eigenen zu nehmen:

- **Agent-Rolle**, keine Admin-Rechte. Ein Token mit `read:messages` würde seinem
  Inhaber erlauben, als jeder Endnutzer zu schreiben; genau das soll dieser eine
  nicht können.
- Setzen Sie ihn in **genau die Gruppe(n)**, die der Agent arbeiten soll. Auf den
  Plänen Team und Growth gibt es Gruppenbeschränkungen nicht — dazu Abschnitt 7.
- Wenn der Agent Tickets in eine Eskalationsgruppe schieben soll, braucht er einen
  zweiten Trigger, und diese Gruppe muss ein echtes Ziel sein.

### 2.2 In covey: das Credential

Zwei Secrets pro Agent im SecretStore:

| Secret | Wert |
|---|---|
| `zendesk_url` | `https://acme.zendesk.com` — die Konten-URL, **ohne** `/api/v2` |
| `zendesk_token` | eine der vier Formen unten |

Welche Auth-Form gilt, steht im Tokenwert selbst; das Plugin erkennt sie am Wert:

| Form | Wert | Anmerkung |
|---|---|---|
| API-Token | `<mail>/<token>` | Der einfache Fall. Das Plugin schickt Basic, braucht also die Adresse, der das Token gehört — ein nacktes Token wird abgelehnt statt geraten |
| OAuth-Client | `client:<id>/<secret>` | Mintet ein Token bei Bedarf und erneuert es selbst. Für eine Installation, die laufen bleiben soll, die richtige Form |
| OAuth-Refresh-Token | `refresh:<refresh-token>/<client-id>/<client-secret>` | Funktioniert, aber jedes Erneuern verbrennt den alten Wert, und zurückschreiben ins SecretStore kann das Plugin nicht — Abschnitt 6 |
| Access-Token | `<token>` | Ein anderswo gemintetes Token, oder ein Test. Das Plugin benutzt es unverändert und erneuert es nie |

Ein Agent, der nur **eine** Gruppe betreuen soll, nennt sie in der URL:

```
zendesk_url = https://acme.zendesk.com queue="Support L1"
```

Der Name ist der, den `list_groups` meldet, und die Anführungszeichen zählen —
Gruppennamen haben Leerzeichen. Das ist eine **Grenze, kein Default**: Der Agent
sieht die Tickets dieser Gruppe und keine anderen, auch dann nicht, wenn ein Kunde
eine Ticketnummer nennt, und auch beim Schreiben nicht. Die Heartbeat-Vorprüfung
erbt die Grenze, der Agent wird für eine fremde Gruppe also nicht einmal geweckt.
Anders als `COVEY_ZENDESK_INTAKE_GROUPS` (Abschnitt 3) gehört das zum Agenten und
nicht zur Installation: Welche Gruppe meine ist, ist eine Eigenschaft des
Mitarbeitenden, nicht der Maschine, auf der er läuft.

### 2.3 Zugriff und Guard-Rails

Die `ACCESS.md` des Agenten nennt das System und seine Scopes
(`system: zendesk scope: read,write,comment`). Schreibaktionen, die nach außen an
einen Kunden gehen, sind geteilt gegengeprüft von denen, die im Ticket bleiben, und
Eskalation ist noch einmal ein eigenes Gate — die Tabelle in Abschnitt 5.

### 2.4 Wake — Heartbeat, Webhook, oder beide

**Per Heartbeat, ganz ohne Zendesk-Einrichtung.** In `HEARTBEAT.md`:

```yaml
alle: 15m
nur-wenn: zendesk
titel: Look after the support queue
aufgabe: Check the open tickets (list_tickets) for ones waiting for an answer,
  read the conversation (list_messages) and reply.
```

`nur-wenn: zendesk` stellt eine Frage — *wartet in meinem Bereich ein Ticket auf
uns?* Sie kostet einen Listenlesen plus ein Lesen pro Ticket, das warten könnte,
höchstens `COVEY_ZENDESK_PROBE_TICKETS` viele (Standard 10). Die Liste wird **ohne
Statusfilter** gelesen — der Endpunkt nimmt genau einen, und „wartet auf uns" sind
new, open, pending und hold zusammen; clientseitig zu filtern kostet einen Aufruf
statt vier und landet auf denselben Tickets. Ein Ticket, dessen letzter öffentlicher
Kommentar von unserer eigenen Identität stammt, zählt nicht: Ein Agent, der
geantwortet hat, wird durch seine eigene Antwort nicht an dasselbe Ticket zurückgeholt.
Eine Kundenantwort erzeugt einen neuen öffentlichen Kommentar, also wird er wieder
geweckt. Die Prüfung gibt außerdem einen Fingerprint davon zurück, *was* wartet — ein
Agent, der ein Ticket gelesen hat und beschließt, nichts zu schreiben, wird eine
Minute später nicht durch denselben Stand erneut gestartet.

**Per Webhook**, wenn ein Ticket sofort aufgenommen werden soll. Im Admin Center
(*Apps and extensions → Trigger and automation webhooks*):

- **Endpoint URL**: `https://covey.example.com/api/webhooks/zendesk/<agent-slug>` —
  der Slug des zuständigen Agenten; die Agenten-ID (die UUID in der URL der
  Agentenseite) geht ebenfalls und ist auf einer Installation mit mehreren
  Organisationen die richtige Wahl, weil ein Slug nur innerhalb einer eindeutig ist.
- **Request method** POST, Content-Type `application/json`.
- **Signing: einschalten**, und als Signing-Key den Wert von
  `COVEY_ZENDESK_WEBHOOK_SECRET`.

Dann abonnieren: entweder ein Event-Trigger auf *Ticket create* / *Ticket update*
(das Konto postet dann ein Ticket-Event, Subjekt `zen:ticket:<id>`), oder ein
Trigger/Automation, das das Ticket selbst postet. **Beide Payload-Formen werden
verstanden**, und welche ankommt, entscheidet das Abonnement, nicht eine
Einstellung hier.

> **Die Signatur ist das, was diese Zustellung vertrauenswürdig macht.** Zendesk
> postet `x-zendesk-webhook-signature: t=<unix-sekunden>,v1=<hex>` über ein
> HMAC-SHA256 auf `t + "." + body`, und das Plugin rechnet es mit dem Secret nach,
> in Hex. Ein Zeitstempel, der mehr als fünf Minuten von der Uhr der Plattform
> abweicht, wird abgelehnt — eine abgefangene Zustellung lässt sich nicht wiederverwenden.
> Das Schiefzeitfenster ist bewusst dieselbe Konvention wie das `WEBHOOK_MAX_SKEW`
> der Plattform, wer einem Zeitstempel nach so langer Zeit nicht mehr trauen will,
> hat also einen Knopf dafür, und er gehört zur Plattform.
>
> Ein **leeres** `COVEY_ZENDESK_WEBHOOK_SECRET` schaltet die Prüfung aus — so wird
> eine Entwicklungsumgebung markiert, genau wie bei Zammad. Produktiv ist das Secret
> gesetzt, ein unsigned eintreffender Webhook wird dann abgelehnt statt ausgeführt.
> Eine lokale Instanz, die nicht signieren kann, muss stattdessen per Heartbeat
> gearbeitet werden.

### 2.5 Prozess-Umgebung

```bash
COVEY_PUBLIC_URL=https://covey.example.com          # von Zendesk aus erreichbar, nicht localhost
COVEY_ZENDESK_WEBHOOK_SECRET=<lang-zufaellige-zeichenkette>   # identisch mit dem Signing-Key
```

### 2.6 Testen

1. In der Zielgruppe ein Ticket anlegen — **als Kunde**.
2. Taucht eine Backlog-Aufgabe beim Agenten auf? → Recording lesen.
3. Wenn der Agent geantwortet hat: Ist die Antwort **für den Kunden sichtbar**
   (Abschnitt 5)?
4. Als Kunde nachfragen: Wacht der Agent wieder auf, und taucht die frühere interne
   Notiz in dem auf, was er liest?
5. `escalate`: Ist das Ticket in die Eskalationsgruppe gewandert *und* haben seine
   Tags überlebt?

---

## 3. Welche Tickets der Agent aufnimmt

Drei Filter, von der Quelle nach innen. Jeder beantwortet eine andere Frage.

| Stufe | Wo | Frage |
|---|---|---|
| Trigger | Zendesk | Welche Ereignisse überhaupt zugestellt werden — Gruppe, Priorität, Kanal, Tag |
| `COVEY_ZENDESK_INTAKE_GROUPS` | covey, pro Installation | Welche Gruppen diese Installation bearbeitet. Leer = jede Gruppe |
| `queue=` in `zendesk_url` | covey, pro Agent | Welche Gruppe **dieser Agent** hat — eine Grenze, auch für Reads per ID und für alles Schreiben |

Der sauberste Filter sitzt an der Quelle: Was ein Trigger nicht zustellt, erreicht
covey gar nicht. Die Umgebungsvariable ist das Auffangnetz für einen zu weit
gefassten Trigger — und sie existiert hier, anders als ihr Zammad-Pendant, weil die
Heartbeat-Vorprüfung dieselbe Einschränkung ohne jeden Webhook mittragen muss.

Der Gruppenfilter gilt für **Tickets mit Gruppe**. Ein Ticket ohne eine ist nicht
„in jeder Gruppe" — es gehört in keine, und ein Warteschlangen-Agent hat damit nichts
zu tun. Beim Heartbeat gilt dasselbe: Tickets ohne Gruppe wecken einen Agenten, der
nach Gruppe arbeitet, nicht. Wer solche Tickets mitbearbeiten will, muss sie in eine
Gruppe leiten.

---

## 4. Was der Agent liest

**Das Gespräch wird aus dem Audit-Trail neu aufgebaut**
(`/tickets/<id>/audits.json`), nicht aus `/comments`. Drei Gründe, alles Dinge, die
sonst passieren:

1. Interne Notizen sind echte Antworten. Im Audit-Trail sind sie gleichwertige
   Ereignisse, und ein Agent kann eine ältere Antwort nicht übersehen, weil er nur
   die letzten drei Kommentare gelesen hat.
2. Audits kommen neueste zuerst. Ein Thread, neueste-first gelesen, bringt einen
   Agenten dazu, die falsche Frage zu beantworten. Das Plugin dreht die Ereignisse
   um, bevor der Agent sie sieht.
3. Es ist der einzige Ort, an dem überhaupt zu sehen ist, wann etwas versteckt
   wurde und von wem. Redigierte Kommentare werden als redigiert gemeldet, sie
   fehlen nicht stillschweigend.

Kommentare, die über das `comments[]`-Array des Tickets selbst geschrieben wurden
(der übliche Weg), erscheinen nicht als Audit-Ereignisse. Das Plugin faltet sie aus
dem Ticketkörper in die Zeitlinie ein, wenn es die Kommentar-ID kennt, damit die
Reihenfolge stimmt.

**Namen.** Der Agent arbeitet in Namen, nicht in Nummern: Gruppen, Anfragende,
Bearbeitende und Beteiligte kommen aufgelöst zurück, aus einem gebündelten
`/users/show_many.json` pro Liste. Eine Person, die für dieses Credential nicht
sichtbar ist, wird per ID gemeldet statt als geratener Name. Wenn `/users/me` nicht
lesbar ist, scheitert nichts: Der Read liefert die Namen, die er bekommen hat, und
der Heartbeat-Test *„ist das unsere eigene Antwort?"* fällt auf die Rolle des
Autors zurück — der schwächere der beiden Tests, und das Plugin weiß das.

**Anhänge.** `list_attachments` gibt ID, Name, Typ, Größe und Autor — und keine
Download-URL, denn die sind signiert und kurzlebig. `download_attachment` holt die
Datei und gibt einen Pfad in der Sandbox zurück
(`attachments/<anhang-id>-<name>`, damit zwei gleichnamige Dateien in einem Ticket
sich nicht gegenseitig überschreiben), wo der Vision-Schritt der Runtime sie ansehen
kann. Eine Datei von einem fremden Host wird abgelehnt, und ein `file://`-Pfad in
einer Antwort ebenso: die eine Adresse, die der Agent nicht abrufen darf, ist eine
lokale Datei.

---

## 5. Was der Agent schreibt

| Aktion | Geht raus | Guard-Rail-Subjekt |
|---|---|---|
| `reply` (`internal:true`, der Default) | privater Kommentar | `zendesk:reply_internal` |
| `reply` (`internal:false`) | öffentlicher Kommentar — der Kunde sieht ihn | `zendesk:reply_external` |
| `update_ticket` | die benannten Felder: Status, Priorität, Tags, Bearbeiter, Gruppe, Custom Fields | `zendesk:update_ticket` |
| `set_status` | ein Feld, damit „Ticket weiterbewegen" nicht heißt, ein ganzes Ticket zu benennen | `zendesk:set_status` |
| `escalate` | interne Notiz mit dem Grund, die Eskalationsgruppe wenn gesetzt, das Tag `covey-escalated` | `zendesk:escalate` |
| `create_ticket` | ein neues Ticket, sein Text als erster Kommentar | `zendesk:create_ticket` |
| `attach_file` | eine Datei aus der Sandbox und den Kommentar, der sie trägt | `zendesk:attach_file` |
| `merge_tickets` | das Duplikat, gefaltet in das überlebende Ticket | `zendesk:merge_tickets` |

Das Scope-Vokabular dieses Systems ist `read`, `write`, `comment`; die Guard-Rails
greifen auf das **Subjekt** oben zu, denn das ist, was ein Lauf protokolliert und
wonach die Control Plane später fragt. Schreiben, das die Seite verlässt, und
Schreiben, das im Ticket bleibt, sind deshalb zwei verschiedene Gates, und
Eskalation ist ein drittes.

Der Default für `reply` ist **intern**. Eine falsche Antwort ist dann eine Notiz,
nicht eine Behauptung gegenüber einem Kunden — ein Agent, der antworten will,
schreibt ausdrücklich `"internal": false`.

**Eine Antwort ändert den Status nicht, solange Sie es nicht anordnen.**
`COVEY_ZENDESK_REPLY_STATUS=pending` macht, dass eine Antwort nach außen das Ticket
auf diesen Status setzt; ohne Angabe bleibt der Status, wie er ist, und die Antwort
sagt das auch. Der Status eines laufenden Tickets gehört dem Workflow, und ein Agent,
der ihn still weiterbewegt, versteckt eine Rückfrage. Wenn das Setzen scheitert —
eine Automation hat schon bewegt, der Workflow verbietet den Sprung — meldet sich die
Antwort trotzdem als versendet und trägt `status_warning` mit dem, was das Konto
gesagt hat: Die Antwort ist raus, und so zu tun, als nicht, würde einen Agenten dazu
bringen, sie noch einmal zu schicken.

**`escalate`** schreibt eine interne Notiz mit dem Grund, schiebt das Ticket in
`COVEY_ZENDESK_ESCALATION_GROUP` wenn eine gesetzt ist (leer = es behält seine
Gruppe), und fügt das Tag `covey-escalated` hinzu. Die Priorität bleibt unangetastet:
Ein Mensch, der ein Ticket für eilig hält, soll derjenige bleiben, der das gesagt
hat. Das Tag wird zusammengführt statt als Liste geschrieben, weil ein Tag-Update die
Liste *ersetzt* — ein Ticket eskalieren, indem still die Tags gelöscht werden, die
eine andere Abteilung draufgesetzt hat, wäre eine schlechte Art von Hilfe.

**Ein blockierter Agent.** Das vorgesehene Paar, wie bei Zammad: Der Agent antwortet
mit einer Rückfrage und setzt den Status auf `pending`. Die Kundenantwort kommt als
neuer öffentlicher Kommentar, der Trigger feuert, covey korreliert über die
Ticket-ID und setzt die Session fort. Ein Trigger nur auf *ticket created* heißt,
dass der Agent nie wieder aufwacht — abonnieren Sie auch Updates.

---

## 6. Credentials, die ablaufen

Die OAuth-Formen minten ein Token mit Ablauf. Der Probe — die Identitätszeile, die
die Credential-Oberfläche zeigt — meldet das Ablaufdatum, das das Konto beim Minten
genannt hat. Für ein Token, das dieser Prozess nicht gemintet hat, gibt es nichts zu
melden, und das Plugin sagt das, statt ein Datum zu raten.

**Client Credentials** (`client:`) ist die Form für alles, was bleiben soll: Das
Plugin mintet bei Bedarf, behält nichts, und es gibt nichts zu rotieren.

**Refresh-Token** (`refresh:`) verbrennt den alten Wert bei jedem Erneuern. Das
Plugin behält den neuen im Speicher, solange der Prozess lebt, und kann ihn aus einem
Aufruf heraus nicht ins SecretStore zurückschreiben. Rotiert wird über die
Plattform: `Rotate` macht genau ein Erneuern und **gibt** das neue Credential zurück,
und die Control Plane speichert das Ergebnis. Ein Prozessneustart mit verbranntem
Refresh-Token ist ein Credential, das nicht mehr funktioniert — deshalb verweist das
Setup-Doc in `zendesk/plugin.go` für langlebige Setups auf Client Credentials.

---

## 7. Umgebungsreferenz

| Variable | Default | Bedeutung |
|---|---|---|
| `COVEY_PUBLIC_URL` | `http://localhost:8494` | Die Basis-URL, an die Zendesk den Webhook zustellt |
| `COVEY_ZENDESK_WEBHOOK_SECRET` | *(leer = Prüfung aus, nur Entwicklung)* | Signing-Key, identisch mit dem Signing-Key des Webhooks |
| `COVEY_ZENDESK_INTAKE_GROUPS` | *(leer = jede Gruppe)* | Welche Gruppen diese Installation arbeitet, per Name |
| `COVEY_ZENDESK_ESCALATION_GROUP` | *(leer = Gruppe behalten)* | Wohin `escalate` ein Ticket schiebt, per Name |
| `COVEY_ZENDESK_REPLY_STATUS` | *(leer = Status unangetastet)* | Eine von `new open pending hold solved closed canceled` — der Status, den eine Antwort nach außen setzt |
| `COVEY_ZENDESK_ATTACHMENT_MAX_MB` | `25` | Pro Datei, 1…50 — Zendesks eigene Obergrenze |
| `COVEY_ZENDESK_PROBE_TICKETS` | `10` | Wie viele Tickets die Heartbeat-Vorprüfung liest |

Jede warteschlangenartige Einstellung wird **per Name** gesetzt, und die Namen sind
die, die `list_groups` meldet — diese Aktion einmal ausführen, statt die Zeichenketten
aus dem Admin-Interface abzutippen.

**Egress.** Die Konten-URL ist der einzige Host, den das Plugin je anspricht, und
OAuth-Token werden bei demselben Host gemintet (`https://<subdomain>.zendesk.com`),
niemals bei einem generischen Authorisierungshost. Pro Agent also ein
Allowlist-Eintrag:

```bash
COVEY_EGRESS_ALLOW="acme.zendesk.com"
```

> **`zendesk_url` ist https**, und reines http wird nur auf einer
> Loopback-Adresse akzeptiert (127.0.0.1, localhost, ::1) — so laufen eine lokale
> Instanz und der skriptierte Live-Test. Alles, was kein Laptop ist, wird beim
> Credential abgelehnt, bevor ein Client-Paar im Klartext hinausgehen könnte.

---

## 8. Bekannte Grenzen

- **Audit-Pagination liest alle Seiten.** `/tickets/<id>/audits.json` hat kein
  serverseitiges Fenster, ein Ticket mit hunderten Audits kostet also einen Read pro
  100 Ereignisse. So lange Threads sind im Support selten, und das Plugin tut nicht
  so, als wäre es anders; `limit` beschneidet, was beim Agenten ankommt, nicht was
  gelesen wird.
- **`update_time` in Suchtreffern** kommt im Index als Unix-Sekunden und sonst überall
  als RFC 3339. Zeiten werden nachsichtig gelesen und als der String behalten, der
  angekommen ist.
- **Team- und Growth-Pläne** haben keine Gruppenbeschränkungen: Das Credential sieht
  jedes Ticket. `queue=` hält dann den *Agenten* ehrlich, ist aber nicht durchsetzbar
  — auf diesen Plänen keine Sicherheitsgrenze daraus machen.
- **Ein Ticket ohne Gruppe** wird von einem warteschlangenartigen Wake weder geweckt
  noch gelistet. „Ohne Bearbeiter und ohne Gruppe" ist ein Zustand, den manche Konten
  für Spam benutzen; wenn Arbeit daran hängt, in eine Gruppe leiten.
- **Das Wake-Bucket ist pro Aktion, nicht pro Ticket.** Das Subjekt, das ein Lauf
  protokolliert, ist `zendesk:<aktion>` (`reply` aufgeteilt in internal und
  external), und genau das ist die Zeichenkette, nach der `WritesWorkSignature` später
  gefragt wird. Zwei Agenten, die zwei verschiedene Tickets beantworten, teilen sich
  damit ein Bucket und können an einem Watermark hängenbleiben. Ein Bucket pro Ticket
  bräuchte eine Signaturfrage, die die ID mitführt, und das Subjekt, mit dem dieses
  Plugin antwortet, ist auf die Aktion hin geformt — dieselbe Wahl wie bei Zammad,
  aus demselben Grund.
- **Redigierte Kommentare** werden als redigiert gemeldet. Ihr Text ist an der Quelle
  weg, nicht vom Plugin versteckt.
