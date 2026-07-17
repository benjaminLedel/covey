# Betrieb: Covey an ein GitLab anschließen

Praktisches Runbook für das Zielsystem **GitLab** (`internal/target/gitlab`).
Aufbau und Datenfluss folgen dem Zammad-Adapter
([`betrieb-zammad.md`](betrieb-zammad.md)) — die Einheit der Arbeit ist hier das
**Issue** statt des Tickets.

> Kurzfassung: Token-Auth gegen die REST-API (`/api/v4`), Webhook-Verifikation
> über `X-Gitlab-Token` (GitLab signiert nicht per HMAC, sondern schickt das
> konfigurierte Secret mit — Vergleich in konstanter Zeit). Es sind
> **Konfigurationsschritte**, kein Umbau.

---

## 1. Überblick des Datenflusses

```
GitLab  ──(Issue-/Note-Hook, X-Gitlab-Token)──►  Covey  /api/webhooks/gitlab/<agent-slug>
                                                   │  Token prüfen → Intake-Filter → Backlog-Task
                                                   ▼
                                                 Agent (Sandbox, Claude Code)
                                                   │  Aktionen über Action-Proxy
GitLab  ◄──(REST /api/v4, PRIVATE-TOKEN)───────────┘  get_issue, comment, set_state, escalate
```

Zwei Richtungen, zwei Auth-Wege:

- **Inbound** (GitLab → Covey): Webhook, verifiziert per Secret-Token
  (`COVEY_GITLAB_WEBHOOK_SECRET` ↔ „Secret token" des GitLab-Webhooks).
- **Outbound** (Covey → GitLab): REST mit einem gebrokerten API-Token
  (Secret `gitlab_token`), das nie in der Sandbox persistiert wird.

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

### 2.4 In Covey: Prozess-Env setzen

```bash
COVEY_PUBLIC_URL=https://covey.example.com        # von GitLab erreichbar, NICHT localhost
COVEY_GITLAB_WEBHOOK_SECRET=<langes-zufalls-secret>
COVEY_GITLAB_AGENT_USERNAMES="covey-bot"
# optional, siehe Abschnitt 3:
COVEY_GITLAB_INTAKE_PROJECTS="gruppe/support"
```

### 2.5 In GitLab: Webhook einrichten

Pro Zielprojekt (*Settings → Webhooks*):

- URL: `https://covey.example.com/api/webhooks/gitlab/<agent-slug>`
  (`<agent-slug>` = der Slug des zuständigen Agenten; die URL ordnet das Issue
  dem Agenten zu — analog Zammad, Abschnitt 3.3 dort).
- Secret token: **derselbe Wert** wie `COVEY_GITLAB_WEBHOOK_SECRET`.
- Trigger: **Issues events** und **Comments** (Note Hooks) ankreuzen. Andere
  Ereignisse (Push, MR, …) verwirft der Adapter, schaden aber nicht.

### 2.6 Testen

1. Im Zielprojekt ein Issue anlegen → in Covey erscheint eine Backlog-Aufgabe.
2. Antwortet der Agent, muss sein Kommentar unter dem `covey-bot`-Nutzer
   erscheinen — und darf **keinen neuen Wake** auslösen (2.4 gesetzt?).
3. Bei einer Rückfrage: Agent `blocked` → Kommentar eines Menschen am Issue →
   Agent wird über den Korrelations-Key `gitlab:issue:<project_id>:<iid>`
   geweckt und via `claude -p --resume` fortgesetzt.

---

## 3. Welche Issues nimmt der Agent auf?

Die Aufnahme-Entscheidung (`ShouldWake` in `internal/target/gitlab/webhook.go`):

### 3.1 Wake-Ereignisse

- **Issue Hook** mit `action` `open` oder `reopen` → neue Aufgabe. `update`
  (Label-/Assignee-Änderungen) und `close` wecken nicht.
- **Note Hook** auf ein Issue → weckt eine geblockte Aufgabe bzw. legt eine
  neue an. Kommentare auf Merge Requests o. ä. wecken nicht.

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
Leer/ungesetzt → keine Einschränkung. Primärer Filter bleibt aber die
Webhook-Konfiguration selbst: Webhooks nur in den Zielprojekten anlegen.

---

## 4. Interne vs. öffentliche Kommentare

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

## 5. Env-Referenz (GitLab-relevant)

| Variable | Default | Bedeutung |
|---|---|---|
| `COVEY_PUBLIC_URL` | `http://localhost:8494` | Basis-URL, die GitLab für den Webhook erreicht |
| `COVEY_GITLAB_WEBHOOK_SECRET` | *(leer = Prüfung aus)* | Secret-Token, identisch zum „Secret token" des GitLab-Webhooks |
| `COVEY_GITLAB_AGENT_USERNAMES` | *(leer)* | GitLab-Nutzernamen der Agenten — deren Kommentare wecken nicht |
| `COVEY_GITLAB_INTAKE_PROJECTS` | *(leer = alle)* | Allowlist der Projekte (Pfad oder id), die Issues aufnehmen |
| `COVEY_EGRESS_ALLOW` | *(leer)* | zusätzliche erlaubte Egress-Hosts, z. B. das GitLab-Host |

Die allgemeinen Variablen (Egress, Daemon-Token-TTL, …) stehen in
[`betrieb-zammad.md`](betrieb-zammad.md), Abschnitt 6.
