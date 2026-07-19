# Betrieb: Covey an ein GitLab anschließen

Praktisches Runbook für das Zielsystem **GitLab** (`internal/target/gitlab`).
Aufbau und Datenfluss folgen dem Zammad-Adapter
([`betrieb-zammad.md`](betrieb-zammad.md)) — die Einheit der Arbeit ist hier das
**Issue** statt des Tickets.

> Kurzfassung: Token-Auth gegen die REST-API (`/api/v4`). Der Agent findet
> seine Issues **selbst** (`list_issues`), getrieben von einem
> `HEARTBEAT.md`-Eintrag — ein Webhook ist optional und nur für sofortige
> Wakes nötig. Bug-Reports beantwortet er **code-basiert**: `checkout` holt
> den Quelltext in die Sandbox, bestätigt wird nur mit Fundstelle
> (Abschnitt 4). Es sind **Konfigurationsschritte**, kein Umbau.

---

## 1. Überblick des Datenflusses

Es gibt **zwei Intake-Wege**, kombinierbar:

**A) Heartbeat (empfohlen, Standard):** Ein `HEARTBEAT.md`-Eintrag legt dem
Agenten periodisch die Aufgabe „Issues sichten" ins Backlog. Der Agent findet
seinen Arbeitsvorrat selbst über die Discovery-Aktionen:

```
Covey  ──(Heartbeat-Tick)──►  Backlog-Task „GitLab-Issues sichten"
                                 │
                                 ▼
                              Agent (Sandbox, Claude Code)
                                 │  Aktionen über Action-Proxy
GitLab  ◄──(REST /api/v4)────────┘  list_issues → get_issue/list_notes → checkout → comment/set_state/escalate
```

Kein eingehender Traffic, keine öffentliche URL, kein Webhook-Secret —
funktioniert auch, wenn Covey hinter NAT/Firewall läuft.

**B) Webhook (optional):** Issue-/Note-Hooks wecken sofort — und nur dieser
Weg weckt eine `blocked`-Aufgabe automatisch über den Korrelations-Key:

```
GitLab  ──(Issue-/Note-Hook, X-Gitlab-Token)──►  Covey  /api/webhooks/gitlab/<agent-slug>
                                                   │  Token prüfen → Intake-Filter → Backlog-Task/Wake
```

Zwei Richtungen, zwei Auth-Wege:

- **Outbound** (Covey → GitLab): REST mit einem gebrokerten API-Token
  (Secret `gitlab_token`), das nie in der Sandbox persistiert wird.
- **Inbound** (GitLab → Covey, nur Weg B): Webhook, verifiziert per
  Secret-Token (`COVEY_GITLAB_WEBHOOK_SECRET` ↔ „Secret token" des Webhooks).

---

## 2. Schritt-für-Schritt-Anleitung

### 2.1 In GitLab: Nutzer + Token anlegen

1. Einen **eigenen Nutzer** für den Covey-Agenten anlegen (z. B. `covey-bot`) —
   nicht das Token eines Menschen verwenden. Der Nutzername kommt später in
   `COVEY_GITLAB_AGENT_USERNAMES` (Echo-Schutz, Abschnitt 3.2).
2. Den Nutzer den Zielprojekten mit **Reporter**-Rolle hinzufügen (reicht für
   Kommentare inkl. interner Notizen; zum Schließen von Issues je nach
   Projekt-Setup Developer).
3. Als dieser Nutzer ein **Personal Access Token** mit Scope `api` erzeugen —
   oder pro Projekt ein **Project Access Token** (Least Privilege). Token
   notieren (wird nur einmal angezeigt).

### 2.2 In Covey: Secrets hinterlegen

Pro Agent im SecretStore setzen (UI: Agenten-Seite → Secrets, oder via API):

| Secret | Wert | Zweck |
|---|---|---|
| `gitlab_url` | `https://gitlab.example.com` | **ohne** `/api/v4` — der Client hängt das an |
| `gitlab_token` | das Token aus 2.1 | Outbound-Auth (`PRIVATE-TOKEN`-Header) |
| `anthropic_api_key` *oder* `claude_code_oauth_token` | API-Key bzw. `claude setup-token` | die Runtime in der Sandbox |

### 2.3 In Covey: Zielsystem aktivieren

Das Zielsystem `gitlab` muss für die Org **aktiviert** sein (UI: Zielsysteme).
Zusätzlich muss der Agent laut `ACCESS.md` auf `gitlab` zugreifen dürfen, und
die Guard-Rails dürfen `gitlab` / `gitlab:comment_external` nicht verbieten.

### 2.4 Intake per Heartbeat einrichten (empfohlen)

In der `HEARTBEAT.md` des Agenten einen Sichtungs-Eintrag anlegen:

```
- alle: 15m titel: GitLab-Issues sichten aufgabe: Finde offene Issues (list_issues state=opened), bearbeite neue und prüfe per list_notes, ob auf deine Rückfragen geantwortet wurde. Bei Bugs: Code per checkout holen und die Behauptung am Quelltext verifizieren.
```

Der Agent entdeckt seinen Arbeitsvorrat dann selbst: `list_projects` liefert
die Projekte, in denen der Bot-Nutzer Mitglied ist, `list_issues` die offenen
Issues (ohne `project_id`: alle, die das Token sehen darf). Damit
wiederkehrende Läufe nichts doppelt bearbeiten, prüft der Agent per
`list_notes`, ob sein eigener Kommentar bereits der letzte Stand ist — die
Prompt-Doku des Plugins weist ihn darauf hin.

Optionaler Projekt-Filter (greift für **beide** Intake-Wege, auch für
`list_issues`/`list_projects`):

```bash
COVEY_GITLAB_INTAKE_PROJECTS="gruppe/support"   # leer = alle Projekte
```

### 2.5 Optional: Webhook für sofortige Wakes

Nur nötig, wenn Issues ohne Heartbeat-Wartezeit aufgenommen und geblockte
Aufgaben **automatisch** geweckt werden sollen (Abschnitt 2.6, Punkt 3).

Prozess-Env:

```bash
COVEY_PUBLIC_URL=https://covey.example.com        # von GitLab erreichbar, NICHT localhost
COVEY_GITLAB_WEBHOOK_SECRET=<langes-zufalls-secret>
COVEY_GITLAB_AGENT_USERNAMES="covey-bot"          # Echo-Schutz, Abschnitt 3.2
```

Pro Zielprojekt (*Settings → Webhooks*):

- URL: `https://covey.example.com/api/webhooks/gitlab/<agent-slug>`
  (`<agent-slug>` = der Slug des zuständigen Agenten; die URL ordnet das Issue
  dem Agenten zu — analog Zammad, Abschnitt 3.3 dort).
- Secret token: **derselbe Wert** wie `COVEY_GITLAB_WEBHOOK_SECRET`.
- Trigger: **Issues events**, **Comments** (Note Hooks) und **Merge request
  events** ankreuzen. Die MR-Ereignisse tragen den Review-Loop des
  Entwickler-Workflows (Abschnitt 2.7); andere Ereignisse (Push, Pipeline, …)
  verwirft der Adapter, schaden aber nicht.

### 2.6 Testen

1. Im Zielprojekt ein Issue anlegen → beim nächsten Heartbeat-Lauf nimmt der
   Agent es auf (bzw. sofort, wenn ein Webhook eingerichtet ist).
2. Antwortet der Agent, muss sein Kommentar unter dem `covey-bot`-Nutzer
   erscheinen — und darf bei Webhook-Betrieb **keinen neuen Wake** auslösen
   (`COVEY_GITLAB_AGENT_USERNAMES` gesetzt?).
3. Bei einer Rückfrage: **Mit Webhook** geht der Agent `blocked`, ein
   Kommentar eines Menschen weckt ihn über den Korrelations-Key
   `gitlab:issue:<project_id>:<iid>` und er wird via `claude -p --resume`
   fortgesetzt. **Ohne Webhook** gibt es diesen Wake nicht — der Agent stellt
   die Rückfrage als Kommentar, schließt seinen Lauf ab und prüft beim
   nächsten Heartbeat per `list_notes`, ob eine Antwort da ist.

### 2.7 Der Review-Loop: der Agent als Entwickler

Behebt der Agent einen Bug selbst, arbeitet er wie ein Entwickler aus
Fleisch und Blut — inklusive Warten auf Review:

1. `checkout` des Projekts in die Sandbox, **Projekt aufsetzen** (Dependencies
   installieren, Build und Tests einmal im Ausgangszustand laufen lassen —
   die nötigen Paket-Registries gibt der Egress über die Built-in-Templates
   frei, z. B. npm/PyPI/Go).
2. Fix entwickeln, Tests ausführen, per `commit` auf einen Feature-Branch
   pushen, `create_merge_request` an den Vorgesetzten.
3. Der Agent geht `blocked` auf den Korrelations-Key
   `gitlab:mr:<project_id>:<mr_iid>` — er **wartet auf Review**.
4. Ein **Review-Kommentar** (Note Hook auf den MR) weckt ihn mit dem
   Feedback: Er checkt den Source-Branch erneut aus, arbeitet die Punkte ein,
   lässt die Tests laufen, pusht auf denselben Branch, antwortet per
   `comment_mr` und blockt wieder.
5. **Merge oder Close** des MR (Merge-Request-Hook, `action=merge|close`)
   weckt ihn ein letztes Mal: Er kommentiert das Ergebnis im Issue und
   schließt seine Aufgabe ab. Diese Hooks sind *correlate-only* — wartet
   keine geblockte Aufgabe auf den MR, entsteht auch keine neue.

Ohne Webhook funktioniert der Workflow ebenfalls, nur langsamer: Der Agent
prüft dann beim nächsten Heartbeat per `list_mr_notes`/`get_merge_request`,
ob Feedback oder der Merge vorliegt.

---

## 3. Welche Issues nimmt der Agent auf?

Beim **Heartbeat-Weg** entscheidet der Agent selbst: `list_issues` liefert nur
offene Issues, und die Projekt-Allowlist (3.3) filtert die Ergebnisse von
`list_issues`/`list_projects` serverseitig. Soll der Agent **nur direkt ihm
zugewiesene Issues** bearbeiten, gibt es `list_issues {"assigned":true}`
(GitLab-`scope=assigned_to_me`, bezogen auf den Bot-Nutzer des Tokens) — die
Regel selbst gehört in `PLAYBOOKS.md`/`HEARTBEAT.md` des Agenten; zusätzlich
liefert jedes Issue seine `assignees` mit, sodass der Agent die Zuweisung auch
im Einzelfall prüfen kann. Beim **Webhook-Weg** gilt die
Aufnahme-Entscheidung (`ShouldWake` in `internal/target/gitlab/webhook.go`):

### 3.1 Wake-Ereignisse

- **Issue Hook** mit `action` `open` oder `reopen` → neue Aufgabe. `update`
  (Label-/Assignee-Änderungen) und `close` wecken nicht.
- **Note Hook** auf ein Issue → weckt eine geblockte Aufgabe bzw. legt eine
  neue an.
- **Note Hook** auf einen Merge Request → Review-Feedback: weckt die auf
  `gitlab:mr:…` geblockte Aufgabe des MR-Autors bzw. legt eine neue an
  (Abschnitt 2.7).
- **Merge-Request-Hook** mit `action` `merge` oder `close` → weckt
  ausschließlich eine geblockte Aufgabe (*correlate-only*); `open`/`update`
  wecken nicht.

### 3.2 Echo-Schutz

Kommentare der Nutzer aus `COVEY_GITLAB_AGENT_USERNAMES` (die Agenten selbst)
wecken nicht — sonst würde die eigene Antwort des Agenten einen neuen
Wake-Zyklus auslösen. Anders als Zammad (`article.sender`) kennzeichnet GitLab
Bot-Kommentare im Payload nicht zuverlässig; deshalb die explizite Liste.

### 3.3 Projekt-Allowlist

```bash
COVEY_GITLAB_INTAKE_PROJECTS="gruppe/support, 42"
```

Ist die Variable gesetzt, wird nur ein Issue aus diesen Projekten aufgenommen
(Projektpfad `path_with_namespace` case-insensitiv oder numerische Projekt-id).
Leer/ungesetzt → keine Einschränkung. Der Filter greift an beiden
Intake-Wegen: Webhook-Payloads außerhalb der Allowlist wecken nicht, und
`list_issues`/`list_projects` liefern nur Treffer aus der Allowlist. Primärer
Filter bleibt trotzdem das GitLab-seitige Setup: den Bot-Nutzer (und ggf.
Webhooks) nur in den Zielprojekten eintragen.

---

## 4. Code-basierte Antworten: `checkout`

Der Agent soll Bug-Reports nicht „aus dem Gedächtnis" beantworten, sondern die
Behauptung **am Quelltext** prüfen. Dafür gibt es die Aktion

```
checkout {"project_id":15, "ref":"main"}     # ref optional, Default: Default-Branch
```

Ablauf und Sicherheitsmodell:

- Der **Daemon** (nicht die Runtime) lädt das Repository-Archiv über die API
  (`GET /projects/:id/repository/archive.tar.gz`) mit dem gebrokerten Token —
  das Token bleibt im Daemon-RAM und landet **nie** im Dateisystem der Sandbox
  (anders als bei einem `git clone` mit Credential-Remote, das das Token in
  `.git/config` persistieren würde).
- Entpackt wird nach `<home>/repos/<projekt>-<ref>-<sha>/`; die Aktion liefert
  den Pfad zurück, der Agent arbeitet dann lokal mit Grep/Read/Bash. Ein
  erneuter Checkout ersetzt den alten Stand (immer frischer Code).
- Schutzmaßnahmen: Pfad-Traversal wird abgelehnt, Symlinks werden
  übersprungen, die entpackte Größe ist begrenzt (Default 512 MB,
  `COVEY_GITLAB_CHECKOUT_MAX_MB`).
- Guard-Rail-Subjekte: `gitlab:checkout`, `gitlab:list_tree`,
  `gitlab:read_file` (alle read-only gegenüber GitLab; wer sie einschränken
  will, legt Regeln darauf).

**Große Repos:** Sprengt das Archiv das Limit, gibt es zwei Auswege — beide
stehen auch in der Fehlermeldung, die der Agent sieht:

- **Teil-Checkout**: `checkout {"project_id":N, "path":"web/upload"}` lädt nur
  das Unterverzeichnis (der `path`-Parameter der GitLab-Archiv-API).
- **Browsen ohne Checkout**: `list_tree {"project_id":N, "path":"...",
  "recursive":true}` listet den Repository-Baum (max. 100 Einträge pro
  Aufruf), `read_file {"project_id":N, "file_path":"pfad/zur/datei"}` liest
  eine einzelne Datei (bis 512 KB, darüber `truncated:true`).

**Historie und MRs — „ist das schon gefixt?":** Ein Checkout ist ein Archiv
ohne `.git` — Historie sieht der Agent darüber nicht. Dafür gibt es vier
weitere read-only-Aktionen (Guard-Rail-Subjekte `gitlab:list_commits`,
`gitlab:get_commit`, `gitlab:list_merge_requests`, `gitlab:list_branches`):

```
list_branches       {"project_id":N, "search":"..."}          # Default-Branch ist markiert
list_commits        {"project_id":N, "ref":"...", "path":"...", "since":"2026-07-15T00:00:00Z"}
get_commit          {"project_id":N, "sha":"..."}             # Diff, pro Datei auf 16 KB gekürzt
list_merge_requests {"project_id":N, "state":"merged", "search":"...", "target_branch":"..."}
```

Die Prompt-Doku des Plugins verpflichtet den Agenten auf diese Arbeitsweise:
**Zuerst** prüfen, ob der gemeldete Fehler seit Erstellung des Issues bereits
behoben wurde (`list_commits` mit `since`, `list_merge_requests`; verdächtige
Commits per `get_commit` verifizieren) — ist das der Fall, meldet er genau
das mit Commit-Referenz, statt den Bug erneut zu bestätigen. Erst danach:
Bug **bestätigen** nur mit Fundstelle (Datei:Zeile) im ausgecheckten Code,
und nur, nachdem der gemeldete Weg vollständig verfolgt wurde (UI →
Endpoint → Verarbeitung — der Fehler kann im Frontend liegen, auch wenn das
Backend verdächtig aussieht); findet er die Stelle nicht, beschreibt er, was
er geprüft hat, und stellt eine gezielte Rückfrage. Antworten ohne Code-Beleg
sind nur bei rein organisatorischen Issues zulässig. Voraussetzung: Das Token
aus 2.1 braucht Lesezugriff aufs Repository (Scope `api` deckt das ab;
Reporter-Rolle genügt bei privaten Projekten für den Archiv-Download).

---

## 5. Interne vs. öffentliche Kommentare

Der Adapter unterscheidet — analog `reply` bei Zammad:

- **intern** (`comment` mit `internal:true`, Default) → GitLab „internal note",
  nur für Projektmitglieder ab Reporter sichtbar.
- **extern** (`comment` mit `internal:false`) → öffentlicher Kommentar, auch für
  externe Reporter sichtbar. Guard-Rail-Subjekt: `gitlab:comment_external` —
  hier greift typischerweise eine Approval-Regel.

`escalate` setzt eine interne Notiz und entfernt die Zuweisung des Issues,
damit ein Mensch übernimmt. `set_state` kennt `close` und `reopen`
(GitLab-`state_event`).

---

## 6. Env-Referenz (GitLab-relevant)

| Variable | Default | Bedeutung |
|---|---|---|
| `COVEY_GITLAB_INTAKE_PROJECTS` | *(leer = alle)* | Allowlist der Projekte (Pfad oder id) — filtert Webhook-Intake **und** `list_issues`/`list_projects` |
| `COVEY_GITLAB_CHECKOUT_MAX_MB` | `512` | Obergrenze der entpackten Größe eines `checkout` (Abschnitt 4) |
| `COVEY_EGRESS_ALLOW` | *(leer)* | zusätzliche erlaubte Egress-Hosts, z. B. das GitLab-Host |
| `COVEY_PUBLIC_URL` | `http://localhost:8494` | nur Webhook-Betrieb: Basis-URL, die GitLab für den Webhook erreicht |
| `COVEY_GITLAB_WEBHOOK_SECRET` | *(leer = Prüfung aus)* | nur Webhook-Betrieb: Secret-Token, identisch zum „Secret token" des GitLab-Webhooks |
| `COVEY_GITLAB_AGENT_USERNAMES` | *(leer)* | nur Webhook-Betrieb: GitLab-Nutzernamen der Agenten — deren Kommentare wecken nicht (Echo-Schutz) |

Die allgemeinen Variablen (Egress, Daemon-Token-TTL, …) stehen in
[`betrieb-zammad.md`](betrieb-zammad.md), Abschnitt 6.
