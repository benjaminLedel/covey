# Referenz: Bundle-Schema & Zielsysteme

## Bundle-Schema (`covey.agent-config`, Version 1)

Ein Agent-Bundle ist eine JSON-Datei. Pflicht sind `kind`, `version`, `agent.slug`,
`agent.display_name` und `files`. Alles Weitere ist optional.

```json
{
  "kind": "covey.agent-config",
  "version": 1,
  "agent": {
    "slug": "covey-support",              // eindeutig pro Org, [a-z0-9-]
    "display_name": "Covey Support",
    "runtime": "claude-code",             // aktuell einzige Runtime
    "model": "",                          // optional, leer = Runtime-Default
    "max_turns": 0,                       // optional, 0 = Default (30)
    "budget_usd": 0,                      // optional, 0 = kein Deckel (Kill-Switch bei Überschreitung)
    "supervisor_email": "",               // optional, ordnet den Vorgesetzten zu
    "webhook_enabled": false,             // optional, erzeugt beim Import ein frisches Token
    "warm_sandbox": false                 // optional, hält Sandbox zwischen Läufen live (nur Dev/Test)
  },
  "files": {
    "SOUL.md": "...",                     // Rolle, Auftrag, Ton, Grenzen
    "CAPABILITIES.md": "...",             // was er kann / nicht zuständig
    "PLAYBOOKS.md": "...",                // Schritt-für-Schritt je Auftrag
    "ACCESS.md": "...",                   // Zugänge (system + scope)
    "HEARTBEAT.md": "..."                 // Auslöser (Intervall, nur-wenn, aufgabe)
  },
  "stages": [ { "name": "In Arbeit", "color": "#..." } ],        // optional: Backlog-Spalten
  "guardrails": [ ... ],                                          // optional: Policy-Regeln
  "egress_templates": [ { "name": "...", "hosts": [ ... ] } ],    // optional: erlaubte Hosts
  "secrets": { "org_keys": [], "agent_keys": [] }                // optional: NUR NAMEN, nie Werte
}
```

**Nur die `files` sind für ein reines Config-Update nötig** (`POST /agents/{id}/config/import`);
die restlichen Felder greifen nur beim Neu-Anlegen (`POST /agents/import`).

### Die fünf Config-Dateien

- **SOUL.md** — Identität und Auftrag. Struktur der Vorlagen: `## Rolle`, `## Auftrag`,
  `## Ton`, `## Grenzen`. Hier gehören die Verhaltensregeln hinein (done-nicht-blocked,
  Idempotenz, sichtbare Spur je Lauf, Zerlegen statt Turn-Limit, Hand-off).
- **CAPABILITIES.md** — knappe Fähigkeitenliste + `## Nicht zuständig`.
- **PLAYBOOKS.md** — nummerierte Abläufe je Auftrag; die konkreten Aktionsaufrufe.
- **ACCESS.md** — Zugänge, eine Zeile je System: `- system: <name> scope: <a>,<b>`.
- **HEARTBEAT.md** — Auslöser, eine Zeile je Trigger:
  `- alle: <intervall> nur-wenn: <system>:<kind> titel: <kurz> aufgabe: <konkret>`.

## Zielsystem-Katalog

Registrierte Built-in-Systeme (Stand Repo): `gitlab`, `email`, `dev`, `browser`, `teams`,
`zammad`, `sharepoint`, `mcp`. **Autoritativ** sind die `SetupDoc()`/`PromptDoc()` im jeweiligen
`internal/target/<name>/plugin.go` — dort stehen die exakten Scopes und Aktionen. Häufig genutzt:

| System | Scopes (ACCESS) | Wozu / Kern-Aktionen |
|---|---|---|
| `gitlab` | `read,write,comment` | Issues/MRs: list_issues (auch `milestone`), get_issue, checkout, read_file, create_issue, comment, list_notes, assign, set_labels, set_state, commit, create_merge_request, comment_mr, approve_mr, upload, download_upload |
| `email` | `read,write` | IMAP/SMTP: list_unread, get_message, reply, mark_seen |
| `dev` | `exec,processes` | Sandbox-Shell: exec, start/stop/logs/list (Dev-Server hochfahren) |
| `browser` | `navigate,content,screenshot,click,type` | headless Chrome; CSS + `:has-text("…")`; screenshot mit `highlight`+`label` |
| `teams` | s. SetupDoc | Microsoft Teams |
| `zammad` | s. SetupDoc | Zammad-Tickets (erstes Built-in, spec/13) |
| `sharepoint` | s. SetupDoc | SharePoint / Teams-Dateien |
| `mcp` | s. SetupDoc | generischer MCP-Adapter |

Secrets, die ein System braucht (z. B. `gitlab_token` + `gitlab_url`, IMAP/SMTP-Zugang),
stehen in dessen `SetupDoc()` — dem Nutzer nach dem Import zum Zuweisen nennen; Werte reisen
nie im Bundle.

## Plattform-Aktionen (`covey/…`)

Neben den Zielsystemen kennt der Action-Proxy das Pseudo-System `covey` — Aktionen an die
Control Plane selbst. Sie brauchen **keinen `ACCESS.md`-Eintrag** (kein Credential, kein
Egress) und stehen jedem Agenten offen; der kompilierte System-Prompt erklärt sie ihm.
Im `PLAYBOOKS.md` lohnt es trotzdem, sie an den richtigen Stellen zu verankern:

| Aktion | Params | Wofür |
|---|---|---|
| `set_stage` | `{"stage":"<name>"}` | Kanban-Spalte der laufenden Aufgabe (rein anzeigend) |
| `add_note` | `{"content":"<text>"}` | Zwischenstand an der Aufgabe |
| `remember` | `{"content":"<fakt>"}` | Einzelfakt ins Gedächtnis |
| `wiki_search/read/write/delete` | s. Prompt | verlinktes Langzeit-Gedächtnis (spec/05) |
| `org_chart` | `{}` | Zuständigkeiten/Eskalationswege zur Laufzeit nachschlagen |
| `create_task` | `{"title":…,"body":…,"agent":"<slug>","priority":1..9}` | Teilaufgabe (ohne `agent`) oder Delegation an einen Kollegen |

Zwei davon brauchen Sorgfalt beim Entwerfen:

- **`create_task`** ist der Ausweg aus zu großen Aufträgen: Teilergebnis abschließen, Rest als
  Aufgabe hinterlegen — statt bis zum Turn-Limit weiterzumachen. Als einzige `covey`-Aktion
  läuft sie durch die Guard-Rails (`covey:create_task`, bei Delegation
  `covey:create_task:foreign`), kann also `denied`/`pending` liefern. Die Plattform lehnt
  Dubletten gleichen Titels, zu tiefe Ketten und zu viele Aufgaben pro Lauf ab — das Playbook
  sollte gar nicht erst dagegenlaufen.
- **`set_stage`** legt fehlende Spalten automatisch an. Gib im Playbook eine **feste, kleine**
  Menge von Namen vor, die Arbeits*zustände* benennen (`Triage`, `Analyse`, `Warten auf
  Review`) — nie den Vorgang (`#83 CSV-Import`), nie Synonyme für denselben Zustand. Sonst
  wächst das Board in Tagen auf ein Dutzend toter Spalten.

## API-Endpunkte

| Zweck | Call |
|---|---|
| Neu anlegen | `POST /api/v1/agents/import` (Body = Bundle; `?slug=` überschreibt bei Kollision) |
| Config updaten | `POST /api/v1/agents/{id}/config/import` (Body = Bundle; nur `files` wirken) |
| Exportieren/Teilen | `GET /api/v1/agents/{id}/export` (Bundle-Download, ohne Secret-Werte) |
| Diagnose (Laufzeit) | `GET /api/v1/agents/{id}/diagnostics` (voller Zustand inkl. Recording) |

Alle sind RBAC-geschützt (Manage-Rolle; Export/Diagnose auch Security). Auth per Admin-Session
oder Bearer-Token. Basis-URL und Auth immer vom Nutzer erfragen, nie hardcoden.
