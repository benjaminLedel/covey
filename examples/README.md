# Beispiel-Agenten (Bundles)

Importierbare Agenten-Konfigurationen (`kind: covey.agent-config`). Import über
`POST /api/v1/agents/import` oder in der UI („Agent aus Bundle").

Diese Bundles sind zugleich die **mitgelieferte Vorlagen-Bibliothek**: `builtin.go`
zieht sie per `//go:embed` ins Binary, sodass sie in der UI unter **Vorlagen**
erscheinen (schreibgeschützt, org-übergreifend) und dort direkt instanziiert
werden können — ohne vorher etwas zu importieren. Ein neues Beispiel-Bundle wird
zur Vorlage, indem man es hier ablegt und im `manifest` in `builtin.go` einträgt.

| Bundle | Slug | Rolle |
|---|---|---|
| `coding-agent.bundle.json` | `covey-dev` | Entwickler: **ihm zugewiesene** Issues aufnehmen (`nur-wenn: gitlab:issues:assigned`), Bugs am Code verifizieren, Fixes entwickeln, Merge Requests eröffnen und den Review-Loop leben. |
| `qa-agent.bundle.json` | `covey-qa` | QA/Test: fremde Merge Requests als Reviewer end-to-end abnehmen und Feedback geben; zusätzlich Bug-Reports per E-Mail annehmen und als GitLab-Ticket (`create_issue`) anlegen. |
| `delivery-lead.bundle.json` | `covey-lead` | Delivery Lead: einen GitLab-Meilenstein zur Frist führen — Tickets implementierbar machen (Abnahmekriterien, betroffene Codestellen), Reihenfolge abhängiger Tickets halten, Arbeit nach WIP-Limit an die Entwickler vergeben, Stand berichten, offene Fachfragen an den Menschen eskalieren. |
| `log-triage-agent.bundle.json` | `covey-logtriage` | Log-Triage: per E-Mail gemeldete Logs analysieren, vor dem Anlegen auf Duplikate prüfen (`list_issues search=…`, Vorfälle am bestehenden Ticket bündeln), für relevante Befunde Tickets anlegen und echte Code-Bugs per `assignee` an einen Entwickler-Agenten übergeben. |

Zusammen bilden sie das Zwei-Agenten-Setup aus
[`docs/ops-gitlab.md`](../docs/ops-gitlab.md) §2.7: Der Entwickler-Agent
trägt den QA-Agenten als `reviewer` seiner MRs ein (er findet ihn automatisch im
Prompt-Abschnitt „Team (KI-Kollegen)"), der QA-Agent testet und kommentiert, der
Entwickler arbeitet das Feedback über seinen `gitlab:mr`-Loop ein.

## Ein ganzes Vorhaben statt einzelner Tickets

Der **Delivery Lead** setzt eine Ebene darüber an. Er nimmt keine Tickets an,
sondern einen Meilenstein: Er liest die Anforderung im Original, schreibt
prüfbare Abnahmekriterien ins Ticket, hält abhängige Tickets zurück, bis ihre
Grundlage gemergt ist, und gibt erst dann Arbeit an die Entwickler-Kollegen —
nie mehr gleichzeitig, als das WIP-Limit erlaubt.

Der Grund für die eigene Rolle ist nicht das Verteilen — das trägt GitLab
selbst, sobald die Entwickler nur ihre Zuweisungen bearbeiten. Es sind die
beiden Dinge, die einzeln arbeitende Agenten strukturell nicht können: aus einer
Anforderung ein implementierbares Ticket machen, und verhindern, dass mehrere
Kollegen gleichzeitig dieselbe Grundlage bauen.

Damit das trägt, ist der Entwickler-Agent auf `nur-wenn: gitlab:issues:assigned`
geschnitten: **Er arbeitet ausschließlich an Issues, die ihm zugewiesen sind.**
Das ist die Naht zwischen den beiden Vorlagen — griffe er wie früher nach jedem
offenen Issue, würde ihn schon der Aufbereitungs-Kommentar des Leads auslösen,
und WIP-Limit wie Reihenfolge wären wirkungslos. Der Preis: Ein Entwickler-Agent
**ohne** Lead braucht eine Zuweisung, um loszulaufen — entweder von einem
Menschen oder über `assign` durch einen anderen Agenten. Wer die Vorlage solo
mit freiem Intake nutzen will, ändert die erste Heartbeat-Zeile zurück auf
`nur-wenn: gitlab:issues` und den Playbook-Schritt 0 entsprechend.

Findet der Lead im Ticket eine Aufbereitung vor (Abschnitte „Anforderung",
„Abnahmekriterien", „Betroffen", „Nicht dazu"), gilt sie dem Entwickler als
Auftrag — nicht der Ticket-Titel.

Damit derselbe Agent das nächste Vorhaben führen kann, steht nichts
Vorhabensspezifisches in seiner Config — Projekt, Meilenstein, Frist, Reihenfolge
und WIP-Limit stehen in einer Wiki-Seite, dem Vorhaben-Steckbrief. Vorlage und
Begründung der Felder: [`docs/ops-gitlab.md`](../docs/ops-gitlab.md)
§2.9.1. **Ein Lead führt genau ein Vorhaben**; für ein zweites wird ein zweiter
Lead angelegt (dieselbe Config, eigener Steckbrief), weil seine Heartbeats keinen
Meilenstein nennen und er zwei Steckbriefe nicht auseinanderhalten könnte.

**Ein Bundle trägt nur die Config-Dateien**, keine Stammdaten. Nach dem Import
noch nötig (siehe `docs/ops-gitlab.md` 2.2, 2.7 und — für den Lead — 2.9):

- Secrets `gitlab_token` + `gitlab_url` zuweisen, GitLab- und `dev`-Zielsystem
  aktivieren. Entwickler-, QA- und Lead-Agent brauchen **je einen eigenen
  GitLab-Bot-Nutzer** (getrennte Tokens) — nicht nur, damit der Review-Loop
  „Autor" von „Reviewer" unterscheidet: Die Wach-Logik entscheidet daran, ob der
  letzte Kommentar vom eigenen Bot stammt. Bei geteiltem Token stellen sich zwei
  Agenten gegenseitig den Wecker ab.
- Im Profil jedes Agenten die **GitLab-Kennung** (`gitlab: covey-dev` bzw.
  `covey-qa`, `covey-lead`) und die **Zuständigkeit** eintragen.
- Alle Agenten **derselben Abteilung** zuordnen — dann sieht der Entwickler den
  QA-Agenten als `DEIN TEAM` und wählt ihn bevorzugt als Reviewer, und der Lead
  findet seine Entwickler.
- **Für den Mail-Intake des QA-Agenten** zusätzlich ein eigenes Postfach
  einrichten und die Secrets `email_url` + `email_token` zuweisen (siehe
  `docs/ops-email.md`). Damit der Agent Bug-Reports dem richtigen GitLab-
  Projekt zuordnen kann, im Profil des QA-Agenten die **Produkt→Projekt-Zuordnung**
  hinterlegen (welches Postfach/Produkt gehört zu welchem GitLab-Projekt); ist die
  Zuordnung unklar, fragt er beim Melder nach, statt ins falsche Projekt zu ticketen.

Zusätzlich nur für den **Delivery Lead**:

- Ein **Berichtsticket** im Meilenstein anlegen, dem Lead zuweisen und dauerhaft
  offen lassen — dorthin schreibt er den Tagesstand. Seine IID (mit Projekt-ID)
  gehört in den Steckbrief.
- Einen **menschlichen Vorgesetzten** eintragen, **dessen GitLab-Kennung im
  Profil hinterlegt sein muss**. Ohne sie scheitert `assign` bei jeder offenen
  Fachfrage — also genau in dem Pfad, der Menschen einbindet.
- Die Wiki-Seite mit dem Steckbrief anlegen (Vorlage: `docs/ops-gitlab.md`
  §2.9.1), bevor der erste Heartbeat feuert.
