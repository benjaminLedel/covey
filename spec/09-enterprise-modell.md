# 09 — Enterprise-Modell (Organisation als Einheit)

Das tragende Prinzip, das Covey von den Single-User-„AI-Employee"-Apps trennt: **Coveys Einheit ist die Organisation, nicht der einzelne Nutzer.** Covey ist die Plattform, die ein *Unternehmen* betreibt, um seine gesamte Agenten-Belegschaft zu verwalten und zu governen — mit vielen menschlichen Stakeholdern, zentraler Governance und unternehmensweitem Org-Chart. Alles — Agenten, Rollen, Guard-Rails, Budgets, Audit — ist **org-scoped**.

## Warum org-level statt user-level

Die Single-User-Tools (Lindy, Relevance AI, Frontier & Co.) optimieren die Produktivität *einer Person* oder eines kleinen Teams: „dein erster KI-Mitarbeiter". Covey sitzt eine Ebene höher — es ist die **geteilte Infrastruktur, die ein Unternehmen betreibt**, so wie es Active Directory, ein SIEM oder eine Personalabteilung für die ganze Organisation betreibt, nicht für einen einzelnen Angestellten.

Konkret heißt das:

- **Agenten sind organisationseigene Ressourcen**, keine persönlichen Assistenten. Sie gehören dem Unternehmen, sind Abteilungen zugeordnet und werden zentral governt.
- **Es gibt nicht „den Nutzer", sondern viele menschliche Rollen** — IT, Team-Leads, Security/Compliance, Audit, Controlling — mit unterschiedlichen Rechten.
- **Governance ist zentral und org-weit**, nicht pro Einzelperson konfiguriert.
- **Der Org-Chart ist unternehmensweit** und umfasst Menschen *und* Agenten.

Diese Abgrenzung ist bewusst die Antwort auf die Marktlage (siehe [`08-marktumfeld.md`](08-marktumfeld.md)): Die „AI-Coworker"-Kategorie existiert, aber die reifen Angebote sind entweder Single-User/No-Cloud-SaaS oder schwergewichtige Enterprise-Suiten. Coveys Platz ist die self-hostbare Enterprise-Plattform für einen technischen Betreiber.

## Menschliche Rollen & RBAC

Die Plattform hat mehrere menschliche Stakeholder mit klar getrennten Rechten. RBAC gilt **auch für Menschen** — Least Privilege ist kein reines Agenten-Thema.

| Rolle | Verantwortung | Typische Rechte |
|---|---|---|
| **Platform-Admin / IT** | Betreibt Covey selbst: Sandbox-Infra, Runtimes, Plattform-Health | Agenten anlegen/löschen, Kill-Switch (flottenweit), Infra-Konfiguration |
| **Agent-Owner / Team-Lead** | Verantwortet einzelne Agenten einer Abteilung | `SOUL.md` & Config pflegen, Backlog priorisieren, Approval-Gates seines Agenten freigeben |
| **Security / Compliance** | Setzt die org-weiten Guard-Rails | Globale Guard-Rails definieren (von Agent-Ownern **nicht** überschreibbar), Policy-Reviews |
| **Auditor** | Prüft Verhalten und Compliance | Read-only auf Recording/Audit-Trail, Export für Inspektionen |
| **Controlling / Finance** | Kostenkontrolle | Kosten pro Agent/Abteilung/Cost-Center, Budget-Einstellungen |

Wichtig ist die **Gewaltenteilung**: Wer Guard-Rails setzt (Security/Compliance) ist nicht dieselbe Rolle wie wer Agenten betreibt (Agent-Owner) oder wer prüft (Auditor). Genau das macht die zentralen Guard-Rails aus [`06-observability-control.md`](06-observability-control.md) glaubwürdig — ein Agent-Owner kann die org-weiten Grenzen nicht aufweichen, weil er nicht die Rolle dafür hat.

## Zwei Identitäts-Ebenen

Covey trennt sauber zwischen **Mensch-Identität** und **Agent-Identität**:

- **Menschen** authentifizieren sich über den Identity-Provider der Organisation — **SSO via SAML/OIDC** (Keycloak, Entra, Okta), mit Joiner-Mover-Leaver-Lifecycle. RBAC hängt an dieser Identität.
- **Agenten** haben ihre eigene Maschinen-Identität und bekommen Zugriff über den Secrets-Broker (RFC 8693). Details in [`04-identitaet-secrets.md`](04-identitaet-secrets.md).

Beide Ebenen sind getrennt, aber verknüpft: Wenn ein Agent im Auftrag eines Menschen handelt, bleibt die Delegationskette (welcher Mensch hat welchen Agenten wofür autorisiert) im Audit-Trail erhalten.

## Mitarbeiter-Profil & Team-Verzeichnis

Jeder Mensch hat über Login und RBAC-Rolle hinaus ein **Profil**: Funktion (Job-Titel), Telefon, Zuständigkeiten (Freitext), seine **Kennungen in Zielsystemen** und die Werte der **org-weit konfigurierbaren Zusatzfelder**. Letztere definiert der Admin unter *Organisationen* frei (z. B. Standort, Abteilung, Slack-Handle; Tabelle `profile_fields`, Werte als `custom`-Map am Profil) — ein neues Feld ist reine Konfiguration und erscheint sofort in jedem Profil-Editor und im Team-Verzeichnis. Die Kennungen sind generisch als Map *System → Kennung* modelliert (`identities`, z. B. `{"gitlab": "maxm", "zammad": "max@firma.de"}`) — Zielsysteme sind Plugins ohne hartkodierte Liste, und die Profile folgen demselben Prinzip: Ein neues Plugin bekommt in der UI automatisch ein Eingabefeld, ohne Schema- oder Code-Change am Profil. Gepflegt wird das Profil vom Admin unter *Benutzer* oder per Self-Service unter *Profil*.

Der Zweck ist nicht Adressbuch-Kosmetik, sondern **Übergaben Agent → Mensch**: Die Profile werden zur Dispatch-Zeit als Abschnitt *„Team (menschliche Mitarbeiter)"* an den System-Prompt jedes Agenten gehängt (analog zur Zielsystem-Doku, damit Profil-Änderungen sofort wirken, ohne die Agent-Config neu zu kompilieren). Ein Agent wählt die Person anhand ihrer Zuständigkeit und verwendet exakt die hinterlegte Kennung — er rät niemals Benutzernamen. So weiß z. B. der GitLab-Bot, wem er ein Issue nach einem Fix zum Testen zuweist (`assign`-Aktion des GitLab-Plugins: Username → User-ID → `assignee_ids`).

## Organisationsstruktur: Abteilungen, Cost-Center, Mandanten

- **Abteilungen / Teams** — Agenten werden Organisationseinheiten zugeordnet. Der Org-Chart bildet die reale Struktur ab (Team-Lead → seine Agenten), und Guard-Rails wie Budgets können **pro Abteilung** gescopt werden (siehe Guard-Rail-Scope in [`06-observability-control.md`](06-observability-control.md)).
- **Cost-Center** — Kosten (aus dem Cost-Tracking) werden pro Agent, Abteilung und Cost-Center aggregiert, sodass Controlling sauber verrechnen kann.
- **Mandanten-Modell** — Primär **single-org self-hosted**: Ein Unternehmen betreibt eine Covey-Instanz für sich. Mehr-Mandanten-Fähigkeit (mehrere isolierte Organisationen auf einer Instanz) ist optional und eine spätere Ausbaustufe; falls relevant, muss Daten- und Policy-Isolation zwischen Mandanten von Anfang an im Datenmodell vorgesehen werden (Entscheidung offen, siehe [`07-offene-entscheidungen.md`](07-offene-entscheidungen.md)).

## Governance & Compliance

Enterprise heißt: der **Audit-Trail muss eine externe Inspektion überstehen**. Das ist in regulierten Kontexten der eigentliche Differenzierer, nicht die Feature-Liste.

- **Lückenloser, org-weiter Audit-Trail** — jede Agenten- und Menschen-Aktion nachvollziehbar, exportierbar, mit Retention-Policy (baut auf Session-Recording in [`06-observability-control.md`](06-observability-control.md) auf).
- **Rollen-getrennte Verantwortung** — Guard-Rail-Owner ≠ Agent-Owner ≠ Auditor (siehe oben).
- **EU AI Act** — Agenten, die beschäftigungs-/HR-nahe Entscheidungen berühren, fallen unter die Hochrisiko-Klassifikation (Annex III). Covey muss die Nachweise liefern können, dass ein Agent innerhalb seiner Autorität gehandelt hat — was ohne saubere Agent-Identität (siehe [`04-identitaet-secrets.md`](04-identitaet-secrets.md)) gar nicht möglich ist.
- **Datenresidenz** — Self-Hosting (z. B. auf eigener Hetzner-/Proxmox-Infra) ist hier ein Vorteil gegenüber den Cloud-SaaS-Konkurrenten.

## Abgrenzung zu Single-User-Tools

| | Single-User „AI-Employee" (Lindy, Relevance, Frontier …) | Covey |
|---|---|---|
| **Einheit** | Nutzer / kleines Team | Organisation |
| **Agent gehört** | dem Nutzer | dem Unternehmen |
| **Governance** | pro Agent-Owner, im Tool | zentral, org-weit, rollengetrennt |
| **Menschliche Rollen** | im Wesentlichen einer | mehrere, mit RBAC |
| **Deployment** | Cloud-SaaS, closed | self-hostbar (eigene Infra) |
| **Audit** | tool-intern | inspektions-fest, exportierbar |

Covey konkurriert damit nicht mit den No-Code-Produktivitäts-Apps, sondern besetzt die Lücke darüber: **die self-hostbare, runtime-agnostische Agenten-Plattform, die ein Unternehmen als Ganzes betreibt und verantwortet.**
