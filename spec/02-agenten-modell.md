# 02 — Agenten-Modell

Ein Agent ist die zentrale Entität der Plattform: eine konfigurierte, persistente „Person" mit vier Bausteinen — Identität, Arbeitsplatz, Zugänge und Persönlichkeit. Alle vier werden an einem Ort angelegt und konfiguriert.

Ein Agent ist dabei eine **organisationseigene Ressource**, kein persönlicher Assistent eines Nutzers: Er gehört dem Unternehmen, ist einer Abteilung zugeordnet und wird von einem **Agent-Owner** (meist Team-Lead) verantwortet, während er zentraler Governance unterliegt. Das Zusammenspiel der menschlichen Rollen steht in [`09-enterprise-modell.md`](09-enterprise-modell.md).

## Identität

Jeder Agent hat:

- eine **eindeutige ID** in der Registry,
- einen **Platz im Org-Chart** mit Vorgesetzten- und ggf. Kollegen-Beziehungen,
- eine **Maschinen-Identität** beim Identity-Provider (Keycloak), über die sämtliche Zugriffe laufen,
- **optional eine eigene E-Mail-Adresse** (z. B. `support-agent@…`).

**Die E-Mail ist optional, keine Pflicht.** Die verbindliche Identität eines Agenten ist seine Maschinen-Identität in Keycloak — darüber laufen Zugriffe und Zuordnung. Eine E-Mail-Adresse ist ein *zusätzlicher Kanal*, den man einem Agenten gibt, wenn seine Rolle sie braucht. Wo sie vorhanden ist, ist sie doppelt nützlich: als Bus für Inter-Agent-Kommunikation und Mensch↔Agent-Eskalation (siehe unten) und als natürlicher Wake-Trigger (neue Mail → Agent wacht auf). Ein reiner Automatisierungs-Agent, der nur auf Ticket-Events reagiert, braucht keine.

### Identitätsmodell: echter User vs. Service-Account

Zwei Optionen, mit Konsequenzen fürs ganze System:

| | (a) Echter User pro Agent | (b) Service-Account mit Delegation |
|---|---|---|
| **Permissions/Audit** | greifen die bestehenden Systeme direkt (Confluence, Teams) | müssen plattformseitig nachgebaut werden |
| **Org-Chart** | wird real (Agent ist echter Nutzer) | bleibt plattform-intern |
| **Kosten** | pro Agent eine Lizenz (M365 etc.) | keine zusätzlichen Lizenzen |
| **Provisioning** | Overhead pro Agent | schlanker |

Empfehlung als Default: **(a) für Systeme, in denen echte Identität + natives Audit zählen** (Mail, Teams, Confluence), **(b) für rein technische Zugriffe**. Egal welches Modell — Zugriff läuft nie über langlebige, in die Sandbox gebackene Secrets, sondern über den Broker (siehe [`04-identitaet-secrets.md`](04-identitaet-secrets.md)).

## Arbeitsplatz (Sandbox)

Der „PC des Mitarbeiters": eine isolierte Sandbox mit **persistentem Home-Verzeichnis**, das über Sessions hinweg überlebt. Dateien, angesammelte Artefakte, lokale Notizen bleiben erhalten. Compute ist ephemer, das Volume persistent (siehe [`01-architektur.md`](01-architektur.md)).

Das persistente Home deckt Dateien ab — **nicht** das episodische Gedächtnis über Aufgaben hinweg. Dafür gibt es eine separate Memory-Schicht (siehe [`05-gedaechtnis.md`](05-gedaechtnis.md)).

> **Weiche vs. harte Grenzen.** Die `## Grenzen` in `SOUL.md` sind **Selbstbindung** — sie leiten das Verhalten des Agenten über den Prompt. Sie sind wertvoll, aber **nicht** die Sicherheitsgrenze: Ein Prompt lässt sich umgehen oder per Injection aushebeln. Die *harten* Grenzen kommen aus den zentralen, plattform-erzwungenen **Guard-Rails** (siehe [`06-observability-control.md`](06-observability-control.md)), die außerhalb der Runtime greifen. Beide Schichten zusammen = Defense in Depth.

## Zugänge

Ein Agent bekommt gezielt Zugänge zu den Systemen, die seine Rolle braucht. Beispiel Support-Agent: Ticketsystem, Confluence, Teams. Diese Zugänge werden **nicht** als Credentials in der Sandbox hinterlegt, sondern als **Berechtigungen** in der Plattform konfiguriert. Zur Laufzeit fordert der Agent über den Daemon ein Token an, der Broker prüft die Berechtigung und stellt ein kurzlebiges, gescoptes Token aus.

> **Sicherheitshinweis:** Ein Support-Agent mit Zugriff auf Tickets + Confluence + Teams, der über ein präpariertes Ticket prompt-injected wird, ist ein handfester Security-Incident (Datenexfiltration über *legitime* Zugänge). Das Threat-Model dazu steht in [`04-identitaet-secrets.md`](04-identitaet-secrets.md) und ist von Anfang an mitzudenken.

## Persönlichkeit: Config-as-Code

Das Verhalten eines Agenten ist als Satz von Markdown-Dateien in Git definiert — **eine Source of Truth pro Agent** (ein Repo oder Verzeichnis je Agent). Die Plattform kompiliert daraus System-Prompt + Runtime-Config. Vorteile: Versionierung, Rollback, Review. Änderungen am Agentenverhalten laufen über PR, nicht über Deploy — GitOps für Agenten, Audit fällt gratis ab.

### Config-Dateien

| Datei | Zweck |
|---|---|
| `SOUL.md` | Kern-Persönlichkeit: Rolle, Auftrag, Ton, Werte, Grenzen. Der Charakter des Agenten. |
| `CAPABILITIES.md` | Was der Agent darf/soll: Aufgabentypen, Zuständigkeiten, Nicht-Zuständigkeiten. |
| `PLAYBOOKS.md` | Konkrete Abläufe für wiederkehrende Aufgaben (Schritt-für-Schritt-Anleitungen). |
| `ACCESS.md` | **Referenzen** auf benötigte Zugänge (Systemnamen + Scopes) — niemals Secrets selbst. |
| `ORG.md` | Position im Org-Chart, Vorgesetzter, an wen eskaliert wird. |

Die genaue Dateiliste ist bewusst erweiterbar — weitere sinnvolle MD-Dateien (z. B. `TONE.md`, `ESCALATION.md`) können hinzukommen. Kernregel: **`ACCESS.md` enthält Referenzen, keine Geheimnisse.**

### Beispiel: `SOUL.md` (Skizze)

```markdown
# Support-Agent

## Rolle
First-Level-Support für Kundenanfragen im Ticketsystem.

## Auftrag
Tickets triagieren, lösbare Fälle selbst beantworten,
komplexe Fälle an den zuständigen Menschen eskalieren.

## Ton
Freundlich, knapp, lösungsorientiert. Deutsch, Sie-Form.

## Grenzen
- Keine Zusagen zu Preisen oder Verträgen.
- Bei rechtlichen Fragen immer eskalieren.
- Keine Aktionen ohne Freigabe, die Kundendaten löschen.
```

### Beispiel: `ACCESS.md` (Skizze)

```markdown
# Zugänge

- system: ticketing      scope: read,write,comment
- system: confluence     scope: read
- system: teams          scope: send-message
```

## Org-Chart & Inter-Agent-Kommunikation

Der Org-Chart ist mehr als Deko: Er strukturiert Eskalation und Delegation. **Für Agenten mit E-Mail-Adresse** ist E-Mail als Message-Bus überraschend elegant — async, auditierbar, menschenlesbar. Ein Support-Agent kann an einen „Senior"-Agent oder einen echten Kollegen eskalieren, ohne dass ein Sonderprotokoll nötig ist. Wo keine E-Mail vorhanden ist (oder für strukturiertere Delegation), läuft die Kommunikation über eine plattform-interne Nachrichtenschicht bzw. **A2A/MCP**.

Wo die E-Mail doppelt genutzt wird (Bus + Wake-Trigger), macht das die Event-Korrelation zum Kernthema — die aber generell kanalunabhängig gedacht wird (auch Ticket-Updates, Webhooks). Siehe [`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md).
