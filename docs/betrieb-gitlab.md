# Betrieb: Covey an ein GitLab anschließen

Praktisches Runbook für das Zielsystem **GitLab** (`internal/target/gitlab`).
Aufbau und Datenfluss folgen dem Zammad-Adapter
([`betrieb-zammad.md`](betrieb-zammad.md)) — die Einheit der Arbeit ist hier das
**Issue** statt des Tickets.

> Kurzfassung: Token-Auth gegen die REST-API (`/api/v4`). Der Agent findet
> seine Issues **selbst** (`list_issues`), getrieben von einem
> `HEARTBEAT.md`-Eintrag — GitLab nimmt Arbeit **rein per Polling** auf, es
> gibt keinen Webhook (bewusst: mit mehreren Agenten pro Projekt ist ein
> Webhook-Setup aufwendig und fehleranfällig). Bug-Reports beantwortet er
> **code-basiert**: `checkout` holt den Quelltext in die Sandbox, bestätigt
> wird nur mit Fundstelle (Abschnitt 4). Es sind **Konfigurationsschritte**,
> kein Umbau.

---

## 1. Überblick des Datenflusses

**Intake ausschließlich per Heartbeat (Polling).** Ein `HEARTBEAT.md`-Eintrag
legt dem Agenten periodisch die Aufgabe „Issues sichten" ins Backlog. Der Agent
findet seinen Arbeitsvorrat selbst über die Discovery-Aktionen:

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

> **Kein Webhook für GitLab.** Anders als Zammad hat das GitLab-Plugin
> **keinen** Webhook-Eingang (`/api/webhooks/gitlab/…` antwortet mit 404). Der
> Grund: Ein Webhook müsste pro Zielprojekt eingerichtet werden und einen
> einzelnen Agenten adressieren — mit mehreren Agenten auf denselben Projekten
> wird das schnell mehrdeutig und schwer zu warten. GitLab wird deshalb
> vollständig per Polling betrieben; der Review-Loop (Abschnitt 2.6) läuft
> ebenfalls über den Heartbeat statt über `blocked`+Wake. (Der generische,
> per-Agent-Trigger `/api/trigger/<token>` für Fremdsysteme bleibt davon
> unberührt — er ist kein Zielsystem-Webhook.)

Auth (nur outbound, Covey → GitLab): REST mit einem gebrokerten API-Token
(Secret `gitlab_token`), das nie in der Sandbox persistiert wird.

---

## 2. Schritt-für-Schritt-Anleitung

### 2.1 In GitLab: Nutzer + Token anlegen

1. Einen **eigenen Nutzer** für den Covey-Agenten anlegen (z. B. `covey-bot`) —
   nicht das Token eines Menschen verwenden. Über diesen Nutzer laufen alle
   Kommentare und Commits des Agenten; per `list_notes` erkennt er beim
   nächsten Lauf seinen eigenen letzten Stand und bearbeitet nichts doppelt.
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

### 2.4 Intake per Heartbeat einrichten

In der `HEARTBEAT.md` des Agenten einen Sichtungs-Eintrag anlegen:

```
- alle: 15m nur-wenn: gitlab titel: GitLab-Issues sichten aufgabe: Finde offene Issues (list_issues state=opened), bearbeite neue und prüfe per list_notes, ob auf deine Rückfragen geantwortet wurde. Bei Bugs: Code per checkout holen und die Behauptung am Quelltext verifizieren. Prüfe außerdem deine offenen Merge Requests (list_merge_requests state=opened) auf neues Review-Feedback (list_mr_notes) und schließe die Aufgabe ab, sobald ein MR gemergt ist.
```

**Ein einziger Heartbeat deckt Issues UND den Review-Loop ab.** Weil der Agent
nach `create_merge_request` **nicht blockt**, sondern mit `done` endet, muss ein
Heartbeat seine offenen MRs erneut aufgreifen — die MR-Prüfung ist deshalb Teil
der Sichtungs-Aufgabe (letzter Satz oben). Ein zweiter, MR-eigener Heartbeat ist
**nicht** nötig.

`nur-wenn: gitlab` verhindert, dass dieser Heartbeat den (teuren) Agenten-Lauf
in jedem Intervall auslöst, auch wenn nichts zu tun ist: Die Control Plane prüft
vorab per API und weckt nur, wenn **eines** zutrifft —

- es gibt ein offenes Issue im Intake-Scope, **oder**
- einer der vom Bot selbst eröffneten, offenen Merge Requests hat
  **unbeantwortetes Review-Feedback** (der letzte Nicht-System-Kommentar im
  Thread stammt nicht vom Bot).

Der Merge-Abschluss braucht keinen eigenen Auslöser: Ist das zugehörige Issue
noch offen, weckt es über den Issue-Zweig; wurde es beim Merge automatisch
geschlossen, gibt es nichts mehr zu tun. Beachte die Semantik: anders als das
Gelesen-Flag bei E-Mail bleiben offene Issues und offene MR-Threads „Arbeit",
bis sie geschlossen bzw. beantwortet sind — die Bedingung spart die
Leerlauf-Phasen ganz ohne offene Arbeit, nicht die Läufe, in denen ein Issue
auf eine Kundenantwort oder ein MR auf deine Nacharbeit wartet.

Der Vorab-Check ist billig (wenige REST-Aufrufe: offene Issues, die eigenen
offenen MRs und deren Notes) — verglichen mit einem LLM-Turn vernachlässigbar.

Der Agent entdeckt seinen Arbeitsvorrat dann selbst: `list_projects` liefert
die Projekte, in denen der Bot-Nutzer Mitglied ist, `list_issues` die offenen
Issues (ohne `project_id`: alle, die das Token sehen darf). Damit
wiederkehrende Läufe nichts doppelt bearbeiten, prüft der Agent per
`list_notes`, ob sein eigener Kommentar bereits der letzte Stand ist — die
Prompt-Doku des Plugins weist ihn darauf hin.

Optionaler Projekt-Filter (greift für `list_issues`/`list_projects` und den
`nur-wenn:`-Vorabcheck):

```bash
COVEY_GITLAB_INTAKE_PROJECTS="gruppe/support"   # leer = alle Projekte
```

### 2.5 Testen

1. Im Zielprojekt ein Issue anlegen → beim nächsten Heartbeat-Lauf nimmt der
   Agent es auf.
2. Antwortet der Agent, muss sein Kommentar unter dem `covey-bot`-Nutzer
   erscheinen.
3. Bei einer Rückfrage geht der Agent **nicht** `blocked`: Er stellt die
   Rückfrage als Kommentar, schließt seinen Lauf mit `done` ab und prüft beim
   nächsten Heartbeat per `list_notes`, ob eine Antwort da ist.

### 2.6 Der Review-Loop: der Agent als Entwickler

Behebt der Agent einen Bug selbst, arbeitet er wie ein Entwickler aus
Fleisch und Blut — das Warten auf das Review läuft aber **per Polling**, nicht
über `blocked`:

1. `checkout` des Projekts in die Sandbox, **Projekt aufsetzen** (Dependencies
   installieren, Build und Tests einmal im Ausgangszustand laufen lassen —
   die nötigen Paket-Registries gibt der Egress über die Built-in-Templates
   frei, z. B. npm/PyPI/Go).
2. Fix entwickeln, Tests ausführen, per `commit` auf einen Feature-Branch
   pushen, `create_merge_request` an den Vorgesetzten, Link im Issue
   kommentieren.
3. Der Agent beendet seinen Lauf mit `done` — **kein `blocked`**. (Ein
   `blocked` würde ohne Webhook nie geweckt und die Heartbeat-Aufgabe dauerhaft
   belegen, sodass keine neuen „Issues sichten"-Läufe mehr entstünden.)
4. Beim nächsten Heartbeat-Lauf prüft der Agent seine offenen MRs
   (`list_merge_requests state=opened` → `list_mr_notes`) auf neues
   Review-Feedback. Verlangt es Änderungen, checkt er den Source-Branch erneut
   aus, arbeitet die Punkte ein, lässt die Tests laufen, pusht auf denselben
   Branch und antwortet per `comment_mr` — danach wieder `done`.
5. Ist ein MR **gemergt** (`list_merge_requests state=merged` / `get_merge_request`),
   kommentiert der Agent das Ergebnis im Issue; wurde er ohne Merge
   **geschlossen**, prüft er per `list_mr_notes` warum und eskaliert, wenn unklar.

Damit dieser Loop zuverlässig läuft, muss die Sichtungs-Aufgabe in der
`HEARTBEAT.md` (Abschnitt 2.4) auch die offenen MRs prüfen. Der
`nur-wenn: gitlab`-Vorabcheck weckt den Agenten dafür auch dann, wenn kein
Issue offen ist, aber einer seiner MRs unbeantwortetes Review-Feedback hat —
ein separater Heartbeat ist nicht nötig.

---

## 3. Welche Issues nimmt der Agent auf?

Der Agent entscheidet selbst: `list_issues` liefert nur offene Issues, und die
Projekt-Allowlist (3.2) filtert die Ergebnisse von `list_issues`/`list_projects`
serverseitig. Soll der Agent **nur direkt ihm zugewiesene Issues** bearbeiten,
gibt es `list_issues {"assigned":true}` (GitLab-`scope=assigned_to_me`, bezogen
auf den Bot-Nutzer des Tokens) — die Regel selbst gehört in
`PLAYBOOKS.md`/`HEARTBEAT.md` des Agenten; zusätzlich liefert jedes Issue seine
`assignees` mit, sodass der Agent die Zuweisung auch im Einzelfall prüfen kann.

### 3.1 Kein Doppelbearbeiten

Weil der Intake per Polling läuft, sieht der Agent bei jedem Lauf denselben
offenen Arbeitsvorrat erneut. Damit wiederkehrende Läufe nichts doppelt
bearbeiten, prüft er per `list_notes` (bzw. `list_mr_notes` bei MRs), ob sein
eigener Kommentar schon der letzte Stand ist, und reagiert nur auf seither neu
hinzugekommene Antworten. Die Prompt-Doku des Plugins verpflichtet ihn darauf.

### 3.2 Projekt-Allowlist

```bash
COVEY_GITLAB_INTAKE_PROJECTS="gruppe/support, 42"
```

Ist die Variable gesetzt, liefern `list_issues`/`list_projects` und der
`nur-wenn:`-Vorabcheck nur Treffer aus diesen Projekten (Projektpfad
`path_with_namespace` case-insensitiv oder numerische Projekt-id).
Leer/ungesetzt → keine Einschränkung. Primärer Filter bleibt trotzdem das
GitLab-seitige Setup: den Bot-Nutzer nur in den Zielprojekten eintragen.

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
| `COVEY_GITLAB_INTAKE_PROJECTS` | *(leer = alle)* | Allowlist der Projekte (Pfad oder id) — filtert `list_issues`/`list_projects` und den `nur-wenn:`-Vorabcheck |
| `COVEY_GITLAB_CHECKOUT_MAX_MB` | `512` | Obergrenze der entpackten Größe eines `checkout` (Abschnitt 4) |
| `COVEY_EGRESS_ALLOW` | *(leer)* | zusätzliche erlaubte Egress-Hosts, z. B. das GitLab-Host |

GitLab hat keinen Webhook-Eingang — die früheren Variablen `COVEY_PUBLIC_URL`
(nur für GitLab), `COVEY_GITLAB_WEBHOOK_SECRET` und `COVEY_GITLAB_AGENT_USERNAMES`
entfallen für dieses Plugin.

Die allgemeinen Variablen (Egress, Daemon-Token-TTL, …) stehen in
[`betrieb-zammad.md`](betrieb-zammad.md), Abschnitt 6.
