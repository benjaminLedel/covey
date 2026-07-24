# FR-001 — KI-Assistent zum Anpassen von Agenten („Config-Copilot")

Status: **Umgesetzt** · Stand: 2026-07-24

> Feature Requests sind Vorschläge, noch nicht festgeschriebene Spec. Wird ein
> Request angenommen, wandert sein Inhalt in das zuständige `spec/`-Dokument und
> dieses Dokument wird auf *angenommen* / *abgelehnt* gesetzt.

> **Umgesetzt** in der Control Plane (`internal/httpapi/assist.go`) und der Web-UI
> (`web/src/pages/Agent.tsx`, Komponente `ConfigAssistant`). Endpunkte:
> `GET /api/v1/assist/status` (Gating) und
> `POST /api/v1/agents/{id}/config/assist` (Dialog). Der Assistent nutzt das
> org-weite Claude-Credential serverseitig, schlägt Config-Dateien als Diff vor
> und speichert nichts selbst.

## Kurzfassung

Sobald in der Organisation ein **org-weites Claude-Credential** hinterlegt ist
(`anthropic_api_key` oder `claude_code_oauth_token` — dieselbe Zugangsdaten, die
schon die Agenten-Runtime speisen), bietet die Control Plane einen **KI-Assistenten
zum Anpassen von Agenten** an: einen Config-Copilot direkt im Agenten-Editor, der
die Config-Dateien (`SOUL.md`, `CAPABILITIES.md`, `PLAYBOOKS.md`, `ORG.md`,
`HEARTBEAT.md`) im Dialog schreibt und überarbeitet — statt dass ein Mensch die
Markdown-Dateien von Hand aus dem Nichts formuliert.

## Motivation

Ein Agent verhält sich nur so gut wie seine Config. Heute muss ein Agent-Owner
`SOUL.md` & Co. von Hand schreiben: leeres Textfeld, keine Vorlage im Kopf, keine
Rückmeldung, ob die Formulierung trägt, bis der Agent das erste Mal läuft. Das ist
die größte Reibung beim Onboarding eines neuen Agenten und der häufigste Grund für
schlechtes Verhalten (vage Rolle, fehlende Playbooks, widersprüchliche Anweisungen).

Die Plattform hat das Werkzeug, das zu lösen, bereits im Haus: das org-weite
Claude-Credential. Es liegt ohnehin vor (sonst liefe kein Agent) und lässt sich
serverseitig für einen Meta-Assistenten wiederverwenden, der beim Formulieren der
Config hilft — ein LLM, das den Menschen beim Konfigurieren der anderen LLMs
unterstützt.

## Auslösebedingung (Gating)

Der Assistent erscheint **nur**, wenn das Credential org-weit auflösbar ist
(`SecretStore` liefert `anthropic_api_key` oder `claude_code_oauth_token` für die
Org). Fehlt es, wird die Funktion in der UI gar nicht angeboten — es gäbe kein LLM,
das sie tragen könnte. Damit ist das Feature streng an eine bereits vorhandene
Fähigkeit gekoppelt und verursacht ohne sie keine tote UI und keine Kosten.

Das Credential wird **ausschließlich serverseitig** von der Control Plane für den
Assistenten-Call benutzt und nie an den Browser oder in eine Sandbox gereicht
(Designprinzip 6 — *niemals langlebige Secrets in die Sandbox*). Der Assistent läuft
in der Control Plane, nicht in einer Agenten-Sandbox.

## Was der Assistent tut

Ein Chat-Panel neben dem Config-Editor auf der Agenten-Seite. Der Mensch beschreibt
in Prosa, was der Agent tun soll oder was am Verhalten nicht stimmt; der Assistent
antwortet mit **konkreten Änderungsvorschlägen an den Config-Dateien**:

- **Neu anlegen:** „Ein Support-Agent für Rechnungsfragen, antwortet auf Deutsch,
  eskaliert an Team Buchhaltung" → Entwurf für `SOUL.md`, `CAPABILITIES.md`,
  `PLAYBOOKS.md`.
- **Überarbeiten:** „Er ist zu geschwätzig und vergisst, Tickets zu schließen" →
  gezielte Diffs an den betroffenen Abschnitten.
- **Erklären:** „Warum eskaliert der Agent hier nicht?" → liest die aktuelle Config
  und zeigt die Lücke, statt blind zu schreiben.

Der Assistent kennt beim Formulieren den **Plattform-Kontext**: das
Covey-Plattform-Protokoll (`agents.ProtocolInstructions`), die am Agenten
angebundenen Zielsysteme samt verfügbaren Aktionen, die geltenden Guard-Rails und
die Org-Einbettung. So schlägt er nur Verhalten vor, das die Plattform auch zulässt
(er erfindet keine Aktion, die der Action-Proxy gar nicht anbietet).

## Einbettung in die bestehende Architektur

- **Andockpunkt UI:** `web/src/pages/Agent.tsx` — der Config-Editor, der heute
  `files` (`SOUL.md`, `HEARTBEAT.md`, …) hält und via `PUT /agents/{id}/config`
  speichert. Der Assistent liefert Vorschläge in denselben `files`-Draft; der Mensch
  reviewt sie als Diff und speichert bewusst.
- **Andockpunkt Backend:** ein neuer Control-Plane-Endpunkt (z. B.
  `POST /api/v1/agents/{id}/config/assist`), der den org-weiten Claude-Key über den
  `SecretStore` auflöst, den Kontext (aktuelle `files`, Zielsystem-Manifeste,
  Guard-Rails, `ProtocolInstructions`) zusammenstellt und Claude befragt.
- **Config-as-Code bleibt gewahrt (Designprinzip 5):** Der Assistent **committet
  nichts selbst**. Er erzeugt Vorschläge; wirksam werden sie erst durch die reguläre,
  menschlich verantwortete Speicherung der Config — mit dem bestehenden Diff-/
  Vers-Mechanismus. Der Assistent ersetzt das Review nicht, er beschleunigt den
  Entwurf.
- **RBAC:** dieselben Rollen, die heute die Agenten-Config bearbeiten dürfen
  (Agent-Owner), dürfen den Assistenten nutzen. Keine neue Berechtigung nötig.

## Nicht-Ziele / Abgrenzung

- **Keine Autonomie über die Config.** Der Assistent schreibt keine Config live,
  löst keine Deployments aus und ändert keine Guard-Rails oder Secrets.
- **Kein zweiter Runtime-Pfad.** Er nutzt das bestehende org-Credential über einen
  einfachen Control-Plane-Call, keine Sandbox, keinen Daemon.
- **Kein Supervisor.** Abzugrenzen vom Supervisor-Agenten aus
  [`spec/06-observability-control.md`](../spec/06-observability-control.md), der
  *laufende* Agenten reviewt. Dieser Assistent hilft beim *Konfigurieren*, nicht beim
  Überwachen.

## Offene Fragen

- **Kostensicht:** Assistenten-Calls laufen auf demselben org-Credential wie die
  Agenten. Sollen sie in der Kostenkontrolle
  ([`spec/06-observability-control.md`](../spec/06-observability-control.md)) getrennt
  ausgewiesen werden (Cost-Center „Plattform/Tooling" statt Agent)?
- **Modellwahl:** festes Modell (z. B. das jeweils aktuellste Opus) oder aus der
  Runtime-Konfiguration abgeleitet?
- **Vorlagen-Kopplung:** Verhältnis zu den bestehenden Templates
  (`web/src/pages/Templates.tsx`) — startet der Assistent bevorzugt aus einem
  Template und verfeinert es, statt vom leeren Blatt?

## Akzeptanzkriterien (Definition of Done)

1. Ist kein org-weites Claude-Credential hinterlegt, erscheint der Assistent nicht;
   die Agenten-Seite verhält sich unverändert.
2. Ist eines hinterlegt, kann ein Agent-Owner im Dialog eine neue `SOUL.md` (plus
   ergänzende Dateien) entwerfen lassen und sie als Config-Draft übernehmen.
3. Vorschläge erscheinen als Diff gegen die aktuellen `files` und werden erst durch
   bewusstes Speichern wirksam — der Assistent committet nie selbst.
4. Das Claude-Credential verlässt die Control Plane nicht (nicht in den Browser,
   nicht in eine Sandbox).
5. Vorgeschlagenes Verhalten bezieht sich nur auf real angebundene Zielsysteme/
   Aktionen und respektiert die geltenden Guard-Rails.
