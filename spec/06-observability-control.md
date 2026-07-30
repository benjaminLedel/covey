# 06 — Guard-Rails, Observability & Control

Dies ist die **Vertrauensschicht** — der Teil, der am Ende über Adoption entscheidet. Der Anspruch: nicht Logging, sondern **EDR/SIEM für Agenten**. Ein IT-Admin muss zentral definieren dürfen, was Agenten *nicht* tun (Guard-Rails), sehen können, was sie tun (Recording), riskante Aktionen freigeben (Gates) und im Notfall stoppen (Kill-Switch).

Drei Wirkzeitpunkte, die zusammenspielen:

- **präventiv** — Guard-Rails verhindern verbotene Aktionen, bevor sie passieren,
- **interaktiv** — Approval-Gates halten riskante Aktionen an und holen eine Freigabe,
- **retrospektiv** — Session-Recording macht alles nachvollziehbar, Supervisor + Kill-Switch reagieren.

## Guard-Rails (zentral, plattform-erzwungen)

Der Kern: **Grenzen werden nicht dem Agenten überlassen.** Was in `SOUL.md` unter „Grenzen" steht, ist Selbstbindung über den Prompt — wertvoll zur Verhaltenssteuerung, aber **keine Sicherheitsgrenze**, weil ein Prompt umgangen oder per Injection ausgehebelt werden kann (siehe Threat-Model in [`04-identitaet-secrets.md`](04-identitaet-secrets.md)). Die *harten* Grenzen sind **Guard-Rails**: zentral definiert, versioniert und **außerhalb der Runtime erzwungen**, an den Stellen, an denen die Plattform ohnehin im Datenfluss sitzt. Ein kompromittierter Agent kann sie nicht umgehen, weil sie nicht in seinem Reasoning liegen.

### Enforcement-Punkte

Guard-Rails greifen genau dort, wo die Control Plane und der Daemon den Fluss kontrollieren:

| Punkt | Was hier durchgesetzt wird |
|---|---|
| **Secrets-Broker** | Auf welche Systeme und Scopes ein Agent überhaupt ein Token bekommt (siehe [`04-identitaet-secrets.md`](04-identitaet-secrets.md)). |
| **Egress** | Ausgehende Kommunikation: erlaubte Empfänger/Domains, Blocklisten, Freigabepflicht bei externen Adressaten. |
| **Tool-/Action-Layer** (im Daemon) | Welche Tools und Kommandos erlaubt sind; destruktive Operationen; Dateizugriffe außerhalb des Home. |
| **Approval-Queue** | Aktionen, die nicht verboten, aber freigabepflichtig sind (siehe unten). |
| **Rate- & Cost-Limits** | Frequenz von Aktionen, Budget-Deckel (siehe Kostenkontrolle). |
| **Content-Filter** | Ein-/ausgehende Inhalte: PII-Redaction, verbotene Inhaltsklassen. |

### Typen von Guard-Rails

- **Allow/Deny für Systeme & Tools** — z. B. „darf nie auf das Personalsystem zugreifen", „kein Shell-Zugriff".
- **Egress-Regeln** — z. B. „Mail nur an interne Domains ohne Freigabe", „keine ausgehenden Requests an unbekannte Hosts".
- **Verbotene Aktionen** — harte No-Gos, die auch mit Freigabe nicht gehen.
- **Approval-Pflichten** — Aktionen, die nur mit menschlicher Freigabe laufen.
- **Rate-/Cost-Limits** — Obergrenzen pro Zeitfenster / pro Aufgabe / pro Agent.
- **Content-/PII-Regeln** — Redaction, Klassifikation, Blockieren sensibler Inhalte.

### Scope & Vererbung

Guard-Rails werden **zentral verwaltet** und wirken auf drei Ebenen, die sich überlagern:

1. **Global** — gelten für *alle* Agenten (z. B. „kein Agent mailt ohne Freigabe an externe Adressen"). Nicht durch Agent-Config überschreibbar.
2. **Rolle / Team** — gelten für eine Klasse von Agenten (z. B. alle Support-Agenten).
3. **Pro Agent** — zusätzliche, engere Regeln für einen einzelnen Agenten.

Regeln sind **additiv-restriktiv**: Eine engere Ebene kann verschärfen, aber eine globale Deny-Regel nie aufweichen. Der Default ist **fail-closed** — was nicht erlaubt ist, ist verboten; im Zweifel wird geblockt oder gegated.

### Verwaltung

Guard-Rails sind selbst **Config-as-Code**: zentral, versioniert, per Review geändert (analog zur Agent-Config in [`02-agenten-modell.md`](02-agenten-modell.md)). Sie werden von der Rolle **Security/Compliance** verwaltet — bewusst getrennt von den Agent-Ownern, damit ein einzelner Team-Lead die org-weiten Grenzen nicht aufweichen kann (siehe Rollen & RBAC in [`09-enterprise-modell.md`](09-enterprise-modell.md)). Jede Auslösung einer Guard-Rail (geblockt/gegated) fließt ins Recording und kann Alerts erzeugen. So sind sie nicht nur Schutz, sondern auch Signal: Häufig anschlagende Rails deuten auf einen fehlkonfigurierten — oder kompromittierten — Agenten hin.

Zwei Werkzeuge machen die Verwaltung praktikabel:

- **Pausieren statt Löschen** — eine Regel lässt sich deaktivieren, ohne sie zu verlieren. Das hält die Regel-Historie intakt und macht Experimente reversibel; eine pausierte Regel greift nicht, bleibt aber sicht- und reaktivierbar.
- **Regel-Tester (Dry-Run)** — ein Subjekt (System oder `system:aktion`, optional im Kontext eines Agenten) wird trocken gegen die aktuellen Regeln ausgewertet: Ergebnis ist die Entscheidung (allow / deny / require_approval) samt auslösender Regel und anwendbarem Budget-Deckel. So lässt sich eine Policy verifizieren, *bevor* ein Agent hineinläuft — ausgeführt wird nichts.

## Session-Recording

Lückenlose Aufzeichnung jeder Agenten-Aktivität, gespeist aus den `event`-Nachrichten des Daemons (siehe [`01-architektur.md`](01-architektur.md)):

- **jeder LLM-Call** (Prompt, Antwort, Modell, Kosten),
- **jeder Tool-Call** (welches Tool, welche Parameter, welches Ergebnis),
- **jedes Kommando in der Sandbox** (Shell, Dateioperationen),
- **jeder Credential-Request** (welches System, welcher Scope, gewährt/verweigert),
- **jede Inter-Agent-Nachricht**.

Recording ist die Grundlage für Audit, Debugging, Kostenanalyse und die Supervisor-Auswertung. Es ist unveränderlich und pro Agent/Aufgabe zeitlich navigierbar.

## Request-Log (Diagnose der Zielsystem-Anbindung)

Das Recording sagt, **was** ein Agent getan hat — welche Aktion mit welchen Parametern und ob sie glückte. Beim Anbinden eines Zielsystems ist die drängendere Frage aber, **was über die Leitung ging**: der Bot-Connector-Call nach Teams samt Antwort, der eingehende Webhook, der an der Signaturprüfung scheiterte. Genau das hält das **Request-Log** fest (Tabelle `request_log`, Sicht *Plattform → Requests*):

- **ausgehend** — jeder Request eines Zielsystem-Plugins. Die Plugins bauen ihren HTTP-Client über `reqlog.Client(...)`; ob und wohin protokolliert wird, entscheidet eine Senke, nicht das Plugin. Aus der Sandbox reist der Eintrag als `event(kind=http)` über das Daemon-Protokoll und bekommt in der Control Plane Org-, Agenten- und Aufgabenbezug; Requests, die die Control Plane selbst stellt (Work-Checks der Heartbeat-Bedingung `nur-wenn:`, JWKS-Abruf), laufen über die Default-Senke.
- **eingehend** — jeder Webhook und jeder generische Trigger, **auch der abgelehnte**. Ein Webhook, der an der Signatur, am Slug oder am nicht aktivierten Zielsystem scheitert, hinterlässt sonst keine Spur — und ist der häufigste Fehlerfall bei der Einrichtung.

Abgrenzung zum Recording, bewusst als eigene Tabelle: Das Request-Log ist **Diagnose, kein Audit-Trail**. Es hat sein eigenes, kurzes Aufbewahrungsfenster (`COVEY_REQUEST_LOG_RETENTION`, Default 72 h, dazu eine harte Zeilen-Obergrenze), es bläht die Agenten-Timeline nicht auf, und es ist ganz abschaltbar (`COVEY_REQUEST_LOG=false`). Geschrieben wird asynchron über einen gepufferten Kanal — ein Request-Pfad hängt nie an der Diagnose; läuft der Puffer voll, werden Einträge verworfen und gezählt.

Zugangsdaten gehören nicht hinein: Header werden gar nicht erst gespeichert (dort steckt der Bearer), verdächtige Query-Parameter und Body-Felder (`token`, `secret`, `password`, `client_secret`, …) werden ersetzt, Bodies bei 8 KiB gekappt. Wer auch die Nutzinhalte (Chat-Nachrichten, Ticket-Texte) aus der Diagnose-Tabelle heraushalten will, setzt `COVEY_REQUEST_LOG_BODIES=false` — dann bleiben nur Metadaten. Die Sicht ist den Rollen `platform_admin` und `security` vorbehalten.

## Approval-Gates

Approval-Gates sind der **interaktive** Guard-Rail-Typ: Riskante Aktionen laufen nicht durch, sondern warten auf **Freigabe**. Der Daemon meldet `request_approval`; die Control Plane hält die Aktion an, bis ein Mensch (oder eine Policy) `approve`/`deny` liefert.

Typische gate-pflichtige Aktionen:

- externe Mail raus (v. a. an nicht-interne Empfänger),
- Löschen / destruktive Operationen,
- Massen-Operationen,
- Zugriff auf besonders sensible Systeme/Scopes,
- alles, was eine Policy als riskant markiert.

Gates sind konfigurierbar pro Agent und pro Aktionstyp. Der Default ist konservativ — im Zweifel gaten.

## Supervisor-Agent (optional, KI-gestützt)

Ein optionaler Agent, der die Aktivität anderer Agenten **reviewt und Anomalien flaggt** — die KI-gestützte Überwachungskomponente, nach der auch aus IT-Admin-Sicht gefragt war. Er sieht die Recording-Streams und schlägt an bei:

- ungewöhnlichen Zugriffsmustern (untypische Systeme, Scopes, Frequenz),
- verdächtigen Empfängern ausgehender Kommunikation,
- Verhalten, das auf Prompt-Injection hindeutet (siehe Threat-Model in [`04-identitaet-secrets.md`](04-identitaet-secrets.md)),
- Abweichungen vom in `SOUL.md`/`CAPABILITIES.md` definierten Auftrag.

Der Supervisor entscheidet nicht autonom über harte Eingriffe; er flaggt und eskaliert an den Menschen bzw. löst Approval-Gates aus.

## Kill-Switch

Ein Agent aus dem Ruder? **Sofort anhalten** — `pause`/`kill` an den Daemon (siehe [`01-architektur.md`](01-architektur.md)). Zwei Granularitäten:

- **einzeln** — ein bestimmter Agent wird gestoppt,
- **flottenweit** — Notaus für alle Agenten (z. B. bei einem vermuteten systemischen Injection-Angriff).

## Kostenkontrolle

Nicht optional, sondern Grundvoraussetzung für die always-on-Ökonomie (siehe [`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md)):

- **Cost-Tracking pro Agent** — aus den `cost`-Events (Tokens/Compute je LLM-Call); aggregiert bis auf Abteilungs-/Cost-Center-Ebene für das Controlling (siehe [`09-enterprise-modell.md`](09-enterprise-modell.md)).
- **Budget pro Agent** — konfigurierbares Deckel; bei Überschreitung wird der Agent gedrosselt oder pausiert.
- **Idle ist idle** — der billige Dispatch-Loop verbraucht (nahezu) nichts; teure Runtime läuft nur in `working`. Sandboxen hibernieren in `sleeping`/`blocked`.

Ohne diese drei Mechanismen skaliert die Rechnung beim zehnten Agenten weg.

## Admin-Sicht

Die Plattform-Sichten sind **rollen-gescopt** (siehe RBAC in [`09-enterprise-modell.md`](09-enterprise-modell.md)): Ein Agent-Owner sieht seine Agenten, Security/Compliance die Guard-Rails, der Auditor read-only den Audit-Trail, Controlling die Kosten. In der jeweils zuständigen Sicht bekommt der Mensch:

- **Live-Status** aller Agenten (wer schläft, wer arbeitet, wer blockiert ist),
- **Backlog-Einblick** (was liegt in wessen Liste),
- **Recording-Timeline** pro Agent/Aufgabe,
- **Request-Log** der Zielsystem-Anbindung (Plattform → Requests),
- **Alerts** vom Supervisor und aus ausgelösten Guard-Rails,
- **Kosten-Dashboard** pro Agent und aggregiert,
- **Guard-Rail-Verwaltung** (global / Rolle / Agent, versioniert),
- **Kontrollen**: Approval-Queue, Kill-Switch, Budget-Einstellungen.

Diese Sicht ist das, was aus einer Sammlung von Agenten eine *führbare Organisation* macht.
