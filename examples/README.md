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
| `coding-agent.bundle.json` | `covey-dev` | Entwickler: Issues aufnehmen, Bugs am Code verifizieren, Fixes entwickeln, Merge Requests eröffnen und den Review-Loop leben. |
| `qa-agent.bundle.json` | `covey-qa` | QA/Test: fremde Merge Requests als Reviewer end-to-end abnehmen und Feedback geben; zusätzlich Bug-Reports per E-Mail annehmen und als GitLab-Ticket (`create_issue`) anlegen. |
| `delivery-lead.bundle.json` | `covey-lead` | Delivery Lead: einen GitLab-Meilenstein zur Frist führen — Tickets implementierbar machen (Abnahmekriterien, betroffene Codestellen), Reihenfolge abhängiger Tickets halten, Arbeit nach WIP-Limit an die Entwickler vergeben, Stand berichten, offene Fachfragen an den Menschen eskalieren. |
| `log-triage-agent.bundle.json` | `covey-logtriage` | Log-Triage: per E-Mail gemeldete Logs analysieren, vor dem Anlegen auf Duplikate prüfen (`list_issues search=…`, Vorfälle am bestehenden Ticket bündeln), für relevante Befunde Tickets anlegen und echte Code-Bugs per `assignee` an einen Entwickler-Agenten übergeben. |

Zusammen bilden sie das Zwei-Agenten-Setup aus
[`docs/betrieb-gitlab.md`](../docs/betrieb-gitlab.md) §2.7: Der Entwickler-Agent
trägt den QA-Agenten als `reviewer` seiner MRs ein (er findet ihn automatisch im
Prompt-Abschnitt „Team (KI-Kollegen)"), der QA-Agent testet und kommentiert, der
Entwickler arbeitet das Feedback über seinen `gitlab:mr`-Loop ein.

## Ein ganzes Vorhaben statt einzelner Tickets

Der **Delivery Lead** setzt eine Ebene darüber an. Er nimmt keine Tickets an,
sondern einen Meilenstein: Er liest die Anforderung im Original, schreibt
prüfbare Abnahmekriterien ins Ticket, hält abhängige Tickets zurück, bis ihre
Grundlage gemergt ist, und gibt erst dann Arbeit an die Entwickler-Kollegen —
nie mehr gleichzeitig, als das WIP-Limit erlaubt.

Der Grund für die eigene Rolle ist nicht das Verteilen (das kann jeder
Entwickler-Agent per `nur-wenn: gitlab:issues:assigned` selbst), sondern die
beiden Dinge, die einzeln arbeitende Agenten strukturell nicht können: aus einer
Anforderung ein implementierbares Ticket machen, und verhindern, dass mehrere
Kollegen gleichzeitig dieselbe Grundlage bauen.

Damit derselbe Agent das nächste Vorhaben führen kann, steht nichts
Vorhabensspezifisches in seiner Config — Projekt, Meilenstein, Frist, Reihenfolge
und WIP-Limit stehen in einer Wiki-Seite. Vorlage und Begründung der Felder:
[`vorhaben-steckbrief.md`](vorhaben-steckbrief.md). Der Lead braucht zusätzlich
zum GitLab-Bot-Nutzer ein **Berichtsticket** und einen menschlichen
Vorgesetzten — dorthin gehen Stand und offene Fachfragen.

**Ein Bundle trägt nur die Config-Dateien**, keine Stammdaten. Nach dem Import
noch nötig (siehe `docs/betrieb-gitlab.md` 2.2 und 2.7):

- Secrets `gitlab_token` + `gitlab_url` zuweisen, GitLab- und `dev`-Zielsystem
  aktivieren. Entwickler- und QA-Agent brauchen **je einen eigenen GitLab-Bot-
  Nutzer** (getrennte Tokens), damit der Review-Loop „Autor" von „Reviewer"
  unterscheidet.
- Im Profil jedes Agenten die **GitLab-Kennung** (`gitlab: covey-dev` bzw.
  `covey-qa`) und die **Zuständigkeit** eintragen.
- Beide Agenten **derselben Abteilung** zuordnen — dann sieht der Entwickler den
  QA-Agenten als `DEIN TEAM` und wählt ihn bevorzugt als Reviewer.
- **Für den Mail-Intake des QA-Agenten** zusätzlich ein eigenes Postfach
  einrichten und die Secrets `email_url` + `email_token` zuweisen (siehe
  `docs/betrieb-email.md`). Damit der Agent Bug-Reports dem richtigen GitLab-
  Projekt zuordnen kann, im Profil des QA-Agenten die **Produkt→Projekt-Zuordnung**
  hinterlegen (welches Postfach/Produkt gehört zu welchem GitLab-Projekt); ist die
  Zuordnung unklar, fragt er beim Melder nach, statt ins falsche Projekt zu ticketen.
