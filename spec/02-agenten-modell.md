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

### Der Arbeitsplatz im Webinterface

Das Home ist im Webinterface als **Dateibrowser** offen (Reiter *Arbeitsplatz* am Agenten): durchsehen, öffnen, Textdateien ändern, hoch- und herunterladen, Ordner anlegen, umbenennen, löschen. Damit ist „was liegt bei dem Agenten eigentlich rum?" eine Frage der Oberfläche und nicht mehr eine Shell auf dem Host — und der Weg, einem Agenten Material mitzugeben (eine Vorlage, eine Preisliste, einen Datensatz), führt nicht mehr über den Umweg eines Zielsystems.

Drei Festlegungen tragen das:

- **Am Daemon vorbei, direkt aufs Home.** Der Zugriff läuft über den `FileAccess`-Port des Sandbox-Providers auf das Home-Verzeichnis, nicht über das Daemon-Protokoll. Sonst gäbe es den Arbeitsplatz nur, während die Sandbox läuft — und laufen tut sie im Normalfall nicht (siehe [`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md)). Ein Provider ohne erreichbares Home hat kein Feature, statt eines geratenen.
- **Kein Weg aus dem Home heraus.** Jeder Pfad wird normalisiert und gegen den tiefsten existierenden Vorfahren geprüft; ein Symlink, der hinauszeigt, wird angezeigt, aber nicht geöffnet. Der Dateibrowser einer Control Plane, der sich zum Dateibrowser des Hosts ausweiten lässt, wäre die teuerste Bequemlichkeit der Plattform.
- **Jede Änderung ins Recording.** Schreibende Zugriffe landen als Ereignis (`kind: file`) in derselben Spur wie die Aktionen des Agenten, mit dem handelnden Menschen. Wer über Nacht eine Datei im Home austauscht, ändert das Verhalten des Agenten; für den, der den Lauf später liest, ist das dieselbe Art von Ereignis wie ein Tool-Aufruf.

**Rollen:** Lesen dürfen die Verwalter des Agenten und Security — wer einen Agenten untersucht, muss sehen, was bei ihm liegt. Schreiben bleibt bei den Verwaltern: eine Datei im Home ist Konfiguration des Agenten, kein Audit-Vorgang.

Die Wiki-Arbeitskopie unter `~/wiki/` ist sichtbar, aber kein Bearbeitungsort: Quelle der Wahrheit ist die Control Plane, und der nächste Lauf materialisiert sie neu (siehe [`05-gedaechtnis.md`](05-gedaechtnis.md)). Die Oberfläche sagt das an Ort und Stelle.

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
| `ACCESS.md` | **Referenzen** auf benötigte Zugänge (Systemnamen + Scopes) und die Tool-Allowlist je System (`tools:`-Attribut) — niemals Secrets selbst. |
| `ORG.md` | Position im Org-Chart, Vorgesetzter, an wen eskaliert wird. |
| `EGRESS.md` | Egress-Konfiguration des Agenten: zugewiesene Templates + eigene Hosts. |
| `HEARTBEAT.md` | Wiederkehrende Aufgaben nach Zeitplan (Intervall oder feste Tageszeit) — die Control Plane legt sie automatisch ins Backlog, siehe [`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md). |

Die genaue Dateiliste ist bewusst erweiterbar — weitere sinnvolle MD-Dateien (z. B. `TONE.md`, `ESCALATION.md`) können hinzukommen. Kernregel: **`ACCESS.md` enthält Referenzen, keine Geheimnisse.** Neben diesen Dateien, die jeder Lauf vollständig mitträgt, gibt es **Skills** für Prozeduren, die nur bei Bedarf laden (siehe unten).

`ACCESS.md` und `EGRESS.md` sind die **Text-Sicht auf Zustand, der auch über die Oberfläche gepflegt wird** (*Tools & Skills* bzw. *Einstellungen → Egress*). Damit Text- und UI-Config nie divergieren, gibt es jede Datei genau einmal und beide Richtungen schreiben denselben Store: Lesen rendert die Datei live aus der Datenbank, Speichern parst sie und wendet sie an (Write-Through). Text-Edits an Tools/Egress unterliegen derselben RBAC wie die Oberfläche (nur `platform_admin`/`security`); in den System-Prompt kompiliert werden beide Dateien nicht.

Was ein Agent in einem angebundenen Zielsystem **tun** kann, zeigt derselbe Reiter unter *Zielsysteme*: die Aktionsliste im **Wortlaut seines System-Prompts** (`PromptDoc` des Plugins, bei MCP auf die zugewiesenen Werkzeuge gefiltert), dazu der Zugang aus `ACCESS.md` und die org-weite Aktivierung. Eine geglättete Zweitfassung wäre eine zweite Wahrheit, die irgendwann von der ersten abweicht — die Frage „warum schließt der Agent das Ticket nicht?" beantwortet nur der Text, den er wirklich liest.

Auch `HEARTBEAT.md` ist Plattform-Config, kein Prompt-Material: Sie wird beim Speichern geparst und materialisiert (Tabelle `agent_heartbeats`), die Aufgabe selbst erreicht den Agenten als reguläre Backlog-Aufgabe. Format und Zeitplan-Semantik stehen in [`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md).

### Skills: Fähigkeiten, die erst bei Bedarf laden

Alle oben genannten Prompt-Dateien stehen in **jedem** Lauf vollständig im System-Prompt. Für Identität und Grenzen ist das richtig — sie gelten immer. Für Prozeduren ist es Verschwendung: Ein Agent mit fünf Playbooks zahlt alle fünf auch dann, wenn der Lauf nach drei Turns feststellt, dass nichts zu tun ist.

Ein **Skill** kehrt das um. Er ist ein Verzeichnis mit einer `SKILL.md` und beliebigem Beiwerk (Referenztabellen, Vorlagen, Checklisten). Dauerhaft im Kontext steht nur seine **Beschreibung** — ein Satz, der sagt, *wann* der Skill zu ziehen ist; Rumpf und Zusatzdateien liest die Runtime erst, wenn sie ihn zieht. Der Daemon materialisiert die Skills eines Agenten vor jedem Lauf nach `<home>/.claude/skills/<name>/`; weil der Lauf mit `HOME=<Agenten-Home>` startet, sind es damit die **persönlichen Skills genau dieses Agenten**, ohne dass am Prompt etwas geändert werden müsste.

Zwei Ebenen, dem Secrets-Modell nachgebaut ([`04-identitaet-secrets.md`](04-identitaet-secrets.md)):

- **Agent-eigene Skills** gehören genau einem Agenten.
- **Skills der Org-Bibliothek** stehen allen zur Verfügung, erreichen aber **nur nach ausdrücklicher Verlinkung** einen Agenten. Diese Opt-in-Regel ist hier nicht nur Least Privilege, sondern auch eine Kostenfrage: Jede verfügbare Beschreibung liegt dauerhaft im Kontext jedes Laufs, und ein Recherche-Agent braucht die Deploy-Checkliste nicht.

Bei Namensgleichheit gewinnt der agent-eigene Skill — sonst überschriebe eine Änderung an der Bibliothek eine bewusste lokale Abweichung, und auf Platte könnten zwei Skills nicht in dasselbe Verzeichnis. Der Name ist zugleich Verzeichnisname und `/slash-command` und deshalb unveränderlich; Umbenennen hieße, Verweise aus Playbooks und anderen Skills still ins Leere laufen zu lassen.

Skills sind **zentral verwaltete Config**, kein Gedächtnis: Es gibt keinen Rückweg aus der Sandbox. Ein Lauf, der sich selbst neue Fähigkeiten schreiben könnte, hebelte die Kontrolle aus, die das Feature erst rechtfertigt. Umgekehrt räumt der Daemon entzogene Skills beim nächsten Lauf ab — das Home überlebt den Lauf, ein gelöschter oder abgehängter Skill bliebe sonst für immer wirksam.

Faustregel für den Schnitt: In `PLAYBOOKS.md` bleibt der Standardablauf, den fast jeder Lauf braucht. In einen Skill wandert, was selten greift, aber dann ausführlich ist.

### Was zur Config gehört — und was zum Binary

Der System-Prompt eines Laufs hat zwei Herkünfte, und die Trennung entscheidet, wie Änderungen einen produktiven Bestand erreichen:

- **Agenten-Anteil** (`SOUL.md`, `CAPABILITIES.md`, `PLAYBOOKS.md`, `ORG.md` …) — das ist Config. Er wird versioniert, per PR/UI geändert, ist rollback-fähig. Eine Änderung hier betrifft genau einen Agenten und ist eine bewusste Entscheidung eines Menschen.
- **Plattform-Anteil** (Abschluss-Protokoll, die `covey/`-Meta-Aktionen, Stage-Regeln — `agents.ProtocolInstructions`) — das ist **Code**. Er beschreibt den Vertrag zwischen Agent und Plattform und ändert sich mit dem Binary, nicht mit einer Agenten-Config.

Deshalb wird der Prompt **zur Dispatch-Zeit** aus den Config-Dateien plus dem aktuellen Plattform-Anteil zusammengesetzt, nicht aus der beim Speichern eingefrorenen Fassung. Sonst kennte ein Agent, den seit dem letzten Deploy niemand angefasst hat, die neu hinzugekommenen Aktionen nie — jedes Plattform-Update bräuchte einen manuellen Nachzug über den gesamten Bestand, und wer ihn vergisst, betreibt Agenten mit einem Vertrag, den die Plattform nicht mehr erfüllt.

Dasselbe Prinzip trägt bereits die Zielsystem-Doku (spiegelt die aktuell aktivierten Plugins der Organisation) und das Team-Verzeichnis (spiegelt den aktuellen Org-Chart). Der gespeicherte `compiled_prompt` bleibt als **Momentaufnahme** für Audit und Anzeige erhalten.

Praktische Folge für den Betrieb: **Ein Deploy rollt Plattform-Änderungen selbst aus.** Nachziehen muss man nur, was wirklich Config ist — etwa wenn ein Playbook eine neue Aktion nutzen soll.

### Export & Import

Die komplette Konfiguration eines Agenten ist als **JSON-Bundle** portabel (`GET /api/v1/agents/{id}/export`, `POST /api/v1/agents/import`, in der UI als *Export*-Button am Agenten und *Import* auf der Agenten-Übersicht). Das Bundle (`kind: covey.agent-config`, versioniert) umfasst Stammdaten (Runtime, Modell, Turn-Limit, Budget, Vorgesetzter per E-Mail), alle Config-Dateien inklusive der live gerenderten `ACCESS.md`/`EGRESS.md`, Board-Spalten, agent-scoped Guard-Rails, die zugewiesenen Egress-Templates **samt Definition** (fehlende legt der Import an), die **Skills** des Agenten mit vollem Inhalt und Herkunftsvermerk (`origin: agent|library`) und die **Namen** zugewiesener Secrets.

Skills reisen bewusst vollständig mit: Anders als bei Secrets gibt es an ihnen nichts Geheimes, und ein Bundle, das nur Namen nennte, ergäbe beim Import einen Agenten ohne seine Prozeduren — was nicht beim Import auffiele, sondern erst im Lauf. Beim Import gilt dieselbe Trennung der beiden Ebenen: Agent-eigene Skills legt er an, bei Bibliotheks-Skills verlinkt er eine **bereits vorhandene gleichnamige Fassung** statt sie zu überschreiben (sie kann anderen Agenten gehören) und meldet das als Warnung.

Grenzen bleiben dabei erhalten: **Secret-Werte und Webhook-Tokens verlassen die Plattform nie** — der Import weist vorhandene Org-Secrets per Name neu zu, meldet fehlende als Warnung und erzeugt bei aktiviertem Webhook ein frisches Token. Der Import legt stets einen **neuen** Agenten an (Slug-Kollision → `409`, `?slug=` überschreibt) und unterliegt derselben RBAC wie die Einzel-Endpunkte: Bundles mit Guard-Rails, Egress oder Tool-Allowlists importiert nur `platform_admin`/`security` (fail-closed).

Neben dem Neu-Anlegen gibt es das **Überschreiben eines bestehenden Agenten aus einem Bundle**, das **die Config-Dateien und die Skills** übernimmt (`POST /api/v1/agents/{id}/config/import`, in der UI der Button *Bundle importieren (nur Config)* am Config-Tab). Die Skills gehören dazu, seit Prozeduren aus `PLAYBOOKS.md` dorthin wandern — eine reine Datei-Übernahme wäre sonst ein halber Import; sie wirkt additiv, vorhandene Skills, die das Bundle nicht kennt, bleiben stehen. Alles andere im Bundle — Stammdaten, Board-Spalten, Guard-Rails, Egress-Templates, Secret-Zuordnungen — wird ignoriert; der Ziel-Agent behält Identität, Secrets und Zuweisungen. Speicher- und Write-Through-Pfad sind identisch zu `PUT /config` (neue Config-Version, dieselbe Security-Rollen-Grenze für Tool-Allowlists/Egress). So verteilt man eine gemeinsame Basis-Config auf mehrere bestehende Agenten, ohne sie neu anzulegen.

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

- system: ticketing      scope: read,write,comment   tools: alle
- system: confluence     scope: read                  tools: search, get_page
- system: teams          scope: send-message
```

`tools:` ist die Tool-Allowlist des Agenten für das System: fehlt das Attribut oder steht dort
`alle`, sind alle Tools erlaubt; eine Liste schaltet fail-closed nur die genannten frei
(Enforcement zentral in der Control Plane, siehe [`01-architektur.md`](01-architektur.md)).

## Org-Chart & Inter-Agent-Kommunikation

Der Org-Chart ist mehr als Deko: Er strukturiert Eskalation und Delegation. **Für Agenten mit E-Mail-Adresse** ist E-Mail als Message-Bus überraschend elegant — async, auditierbar, menschenlesbar. Ein Support-Agent kann an einen „Senior"-Agent oder einen echten Kollegen eskalieren, ohne dass ein Sonderprotokoll nötig ist. Wo keine E-Mail vorhanden ist (oder für strukturiertere Delegation), läuft die Kommunikation über eine plattform-interne Nachrichtenschicht bzw. **A2A/MCP**.

Wo die E-Mail doppelt genutzt wird (Bus + Wake-Trigger), macht das die Event-Korrelation zum Kernthema — die aber generell kanalunabhängig gedacht wird (auch Ticket-Updates, Webhooks). Siehe [`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md).
