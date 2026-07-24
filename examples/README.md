# Beispiel-Agenten (Bundles)

Importierbare Agenten-Konfigurationen (`kind: covey.agent-config`). Import über
`POST /api/v1/agents/import` oder in der UI („Agent aus Bundle").

| Bundle | Slug | Rolle |
|---|---|---|
| `coding-agent.bundle.json` | `covey-dev` | Entwickler: Issues aufnehmen, Bugs am Code verifizieren, Fixes entwickeln, Merge Requests eröffnen und den Review-Loop leben. |
| `qa-agent.bundle.json` | `covey-qa` | QA/Test: fremde Merge Requests als Reviewer end-to-end abnehmen und Feedback geben. |

Zusammen bilden sie das Zwei-Agenten-Setup aus
[`docs/betrieb-gitlab.md`](../docs/betrieb-gitlab.md) §2.7: Der Entwickler-Agent
trägt den QA-Agenten als `reviewer` seiner MRs ein (er findet ihn automatisch im
Prompt-Abschnitt „Team (KI-Kollegen)"), der QA-Agent testet und kommentiert, der
Entwickler arbeitet das Feedback über seinen `gitlab:mr`-Loop ein.

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
