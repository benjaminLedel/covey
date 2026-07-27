# 14 — Companion: Brain-Dump & Kontext für Agenten

Covey behandelt Agenten wie Mitarbeiter — mit Identität, Arbeitsplatz und **Gedächtnis** ([`05-gedaechtnis.md`](05-gedaechtnis.md)). Den Agenten fehlt der Kontext aus dem Wissen ihrer Menschen: Mails, unterwegs gesprochene Ideen, Whiteboard-Fotos, Bildschirmaufnahmen, quergelesene PDFs. Dieses Wissen liegt verstreut und geht verloren.

Die **Companion** ist eine eigene App zusätzlich zur Covey-Web-UI, ein eigenes Erfassungs-Produkt. Ihr Zweck: den Brain-Dump eines Menschen an einem Ort sammeln und ihn als Kontext für seine Agenten bereitstellen. Der Mensch lädt roh ab, die Plattform verdichtet das zu strukturiertem Wissen, die Agenten konsumieren es. Anders als ein privates Notiz-Tool ist das Teilen mit Agenten hier der eigentliche Zweck, nicht ein Zusatz.

Technisch ist die Companion ein separater Client auf Coveys Backend: die Daten leben in der Control Plane (Postgres, Memory — [`10-architektur-stack.md`](10-architektur-stack.md)), es gibt eine Quelle der Wahrheit, und die Agenten lesen direkt. Kein zweiter Store.

## Leitprinzipien

1. **Ein universeller Trichter.** Jede Quelle, jedes Format darf rein — Audio, Text, E-Mail, Screen-Recording, Foto, Link, Word, PDF, beliebige Datei. Der Mensch entscheidet nicht beim Erfassen über Struktur; er lädt ab.
2. **Text-Repräsentation als gemeinsamer Nenner.** Jede Erfassung wird auf einen **Text** reduziert (Transkript / OCR / Vision-Caption / extrahierter Dokumenttext), der Retrieval und Embedding trägt. Das Original bleibt als **Medium** erhalten und verlinkt. Der Text dient dem Auffinden, das Medium der Treue. So bleibt beliebiger Input sofort für Agenten nutzbar.
3. **Gleiche Infrastruktur, anderer Owner.** Das strukturierte Ergebnis ist das Wiki aus [`05-gedaechtnis.md`](05-gedaechtnis.md) — dieselbe Seite als Einheit, dieselben `[[Wikilinks]]`, derselbe Retrieval- und Konsolidierungs-Pass. Owner-Spalte `human_id` statt `agent_id`. Kein zweites Gedächtnis-Modell.
4. **Privat by default, Freigabe zentral erzwungen** (Prinzip 7). Der Brain-Dump gehört dem Menschen. Was als Kontext an die Agenten geht, entscheidet er — und die Control Plane setzt es durch, fail-closed. Siehe „Datenschutz & Governance".
5. **Reibungslose Erfassung.** Erfassung muss schnell und unterwegs möglich sein, sonst landet das Material nicht in der App.

## Erfassung — der universelle Trichter

Der Mensch produziert Rohmaterial; die Companion nimmt es in **jedem** Format und legt es als **Capture** in einen Posteingang. Jede Capture bekommt ihre Text-Repräsentation (für Retrieval) und behält ihr Original (als Anhang):

| Quelle | Verarbeitung → Text-Repräsentation | Original |
|---|---|---|
| **Audio-Idee** | Speech-to-Text (on-device, siehe Datenschutz) | Audio *(optional, sonst verworfen)* |
| **Text / Quick-Capture** | direkt | — |
| **E-Mail** | Postfach anbinden / weiterleiten → Betreff + Body (nutzt ggf. das bestehende IMAP-Plugin) | Original-Mail |
| **Screen-Recording** | Transkript + Keyframe-OCR / Vision | Video |
| **Foto / Screenshot** | OCR + Vision-Caption | Bild |
| **Link / URL** | Titel- und Textextraktion | URL + Snapshot |
| **Dokument (Word, PDF, …)** | Textextraktion | Datei |

Der **MVP** trägt **Audio (mit Transkription) und Text**; alle weiteren Quellen folgen demselben Muster (roher Input → Text-Repräsentation + Anhang → Ingest) und sind additiv. Screen-Recording und E-Mail-Import ziehen **Desktop** als Fläche mit herein (siehe „Flächen").

## Verarbeitung — der Memory-Kurator

Die reine Cosine-Zuordnung des Ingest ([`05`](05-gedaechtnis.md)) trägt knappe Einträge, aber nicht einen rohen Strom aus Mails, Memos und PDFs, in dem Personen, Projekte und Entscheidungen durcheinanderlaufen. Diesen **Schnitt** macht ein LLM-Schritt — und der ist in Covey konsequent selbst **ein Agent**: der **Memory-Kurator**.

Statt einen LLM-Aufruf in die Control Plane zu verdrahten, ist die Triage ein org-eigener Agent mit eigenem `SOUL.md`. Damit erbt sie alles, was Agenten ohnehin haben: **Config-as-Code** (die Kurations-Regeln sind versioniert und per PR änderbar; der Mensch definiert, wie sein Brain-Dump geschnitten wird), die Runtime-Abstraktion, Kostenzählung, Guard-Rails und das gemeinsame LLM-Abo (den „globalen Token") als Credential. Der globale Token bestimmt nur, wie der Kurator ans Modell kommt, nicht was er tut.

Ablauf: neue Captures wecken den Kurator (Wake-Quelle „offene Captures für Mensch H"). Er liest Posteingang (inkl. der Text-Repräsentationen der Medien) + den Wiki-Index des Menschen und entscheidet wie im Agenten-`done`-Schritt: neue Seite vs. bestehende ergänzen, Wikilinks setzen, Kern extrahieren, **Medien in die passende Seite einbetten**, Floskeln verwerfen. Geschrieben wird über ein **auf genau diesen Menschen gescoptes** Tool ins `human_wiki` — der einzige Fall, in dem ein Agent in fremdes (menschliches) Gedächtnis schreibt: im Auftrag und Eigentum des Menschen, vollständig auditiert. Die rein mechanische Cosine-Ingest bleibt als LLM-freier Fallback.

Scope offen (siehe „Offene Entscheidungen"): ein Kurator **pro Mensch** (persönlich, aber viele Agenten) vs. einer **pro Org mit Menschen-Scoping** (sparsam). Als Template/Rolle ausgeführt, damit beides trägt.

## Das Wiki mit Medien

Das strukturierte Ergebnis ist das Wiki aus [`05`](05-gedaechtnis.md) — aber die Seiten sind **Markdown mit Medien**, nicht reiner Text:

- Eine Seite bettet Medien per Standard-Markdown ein (`![Whiteboard](covey-media://<id>)`) bzw. listet Anhänge im Frontmatter. Die Wikilinks tragen weiter den Graphen.
- **Blob-Speicherung als Port.** Medien liegen in einem `BlobStore` — „batteries included, but swappable" ([`10-architektur-stack.md`](10-architektur-stack.md)): builtin (Dateisystem/Postgres), swappable (S3-kompatibel). Die Seite referenziert nur die Blob-ID; der Vektor-Index bettet die **Text-Repräsentation** ein, nicht das Binär-Medium.
- **Retrieval bleibt text-getragen.** Gefunden werden Seiten über die eingebettete Text-Repräsentation; das Medium hängt an der gefundenen Seite. So funktioniert die semantische Suche über einen Bild-/Audio-/PDF-Bestand, ohne multimodale Embeddings vorauszusetzen.

## Kontext für Agenten

Die Agenten konsumieren das **kuratierte Wiki** (nicht den rohen Posteingang) — die verdichtete Schicht. Der Weg dahin ist die **Freigabe**:

- **Privat by default; Freigabe explizit.** Jede Seite ist `privat` oder `geteilt mit meinen Agenten`. Nur geteilte Seiten kommen in Frage.
- **Bindung an die Supervision.** Empfänger sind nur die vom Menschen beaufsichtigten Agenten (`supervisor_id`, [`02-agenten-modell.md`](02-agenten-modell.md)) — kein org-weiter Durchgriff (das wäre der Org-Scope, D5 in [`07-offene-entscheidungen.md`](07-offene-entscheidungen.md)).
- **Ein Embedding-Raum.** Menschen- und Agenten-Seiten teilen Vektorraum und Embedder; geteilte Seiten sind damit direkt in die Agenten-Suche mischbar. Beim `triage` erweitert die Control Plane die Wiki-Query um die geteilten Seiten des Vorgesetzten, klar getrennt im Prompt ausgewiesen („## Aus dem Gedächtnis deines Vorgesetzten").
- **Zentral, fail-closed, read-only, auditiert** (Prinzip 7). Der Agent liest nie selbst — die Control Plane wählt aus und injiziert. Kein Token aufs Menschen-Gedächtnis. Jede Einblendung wird protokolliert ([`06-observability-control.md`](06-observability-control.md)); der Mensch sieht, welcher Agent wann was gesehen hat. Widerruf entzieht sofort (Referenz zur Laufzeit, kein Kopieren).

**Medien für Agenten — der Ausbaupfad.** Heute triagiert der Agent über die **Text-Repräsentation** einer Seite; damit ist er sofort nutzbar. Damit ein Agent Medien direkt sieht oder hört, wird die **hybride Home-Speicherung** aus [`05`](05-gedaechtnis.md) erweitert: sie materialisiert die geteilten Seiten ohnehin als `~/wiki/*.md` in die Sandbox — zusätzlich werden die verlinkten Medien nach `~/wiki/media/` materialisiert, und der Agent öffnet sie mit **normalen Datei-Tools** (Claude Code liest lokale Bilder multimodal, [`12-claude-code-adapter.md`](12-claude-code-adapter.md)). Multimodale Tiefe hängt dann an der Runtime, nicht an einer Sonder-API. Umfang offen (siehe unten).

## Aufgaben an Agenten

Nicht jeder erfasste Gedanke ist Gedächtnis; mancher ist ein Auftrag. Dieselbe Capture-Oberfläche pusht darum auch **Aufgaben in den Backlog eines Agenten** ([`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md)): „mach aus diesem Memo eine Aufgabe für Agent X". Der Mensch spricht/tippt, wählt einen seiner beaufsichtigten Agenten, die App legt ein Backlog-Item an (Titel/Body aus der Text-Repräsentation) — gegen die bestehende Control-Plane-API, per Bearer-Auth, RBAC-gescopt auf die Agenten, die der Mensch verantwortet (`supervisor_id` / Agent-Owner, [`09-enterprise-modell.md`](09-enterprise-modell.md)).

Damit dient die Companion auch zur mobilen Steuerung der eigenen Agenten: unterwegs abladen, entweder ins eigene Gedächtnis oder als Auftrag an einen Agenten. Diese Weiche trifft der Mensch — oder der Memory-Kurator schlägt sie vor („klingt nach einer Aufgabe für Agent X"). MVP: Weiche manuell; automatische Vorschläge sind additiv.

## Datenschutz & Governance

Der gesamte Brain-Load eines Mitarbeiters auf Firmen-Infrastruktur ist heikel — arbeitsrechtlich (Mitbestimmung/Betriebsrat), datenschutzrechtlich (DSGVO: Zweckbindung, Datenminimierung, Betroffenenrechte) und für die Akzeptanz. Grundlage ist eine klare Zusage: **der Brain-Dump gehört dem Menschen, nicht dem Arbeitgeber.**

- **Kein Monitoring-Werkzeug.** Der persönliche Ablade-Ort dient der Produktivität des Mitarbeiters, nicht seiner Überwachung. Die Plattform, die *Agenten* überwacht ([`06-observability-control.md`](06-observability-control.md)), überwacht damit **nicht die Menschen** — eine harte Zusage, keine Einstellung.
- **Kein Arbeitgeber-Durchgriff by default.** Kein Admin, kein Vorgesetzter liest den persönlichen Dump. Ein Platform-Admin sieht Existenz/Metadaten (Offboarding), nie Inhalte. Inhaltszugriff nur über einen definierten, protokollierten Compliance-Prozess (Legal Hold), idealerweise im Vier-Augen-Prinzip — nie beiläufig.
- **Verschlüsselung at rest.** Text-Repräsentationen *und* Medien-Blobs werden verschlüsselt (AES-GCM, dasselbe Muster wie die Secret-Spalten, [`04-identitaet-secrets.md`](04-identitaet-secrets.md)), Schlüssel so gescopt, dass beiläufiges Mitlesen technisch — nicht nur per Policy — ausgeschlossen ist.
- **Datensparsame Erfassung.** On-device-STT: das Roh-Audio verlässt das Gerät nie, nur das Transkript wird gespeichert (Audio danach optional verworfen). Analog wählt der Mensch, ob ein Original-Medium (Video, Mail) überhaupt behalten wird oder nur seine Text-Repräsentation.
- **Transparenz für den Menschen.** Jeder Zugriff — der Kurator-Agent, jede Einblendung einer geteilten Seite in einen Agenten — ist für den Menschen sichtbar. Kein stiller Zugriff.
- **Freigabe überschreitet eine Datenschutz-Grenze — sichtbar gemacht.** Sobald der Mensch eine Seite an seine Agenten freigibt, kann dieser Inhalt in der **überwachten** Agenten-Aktivität auftauchen (Session-Recording, sichtbar für Security/Audit). Freigabe ist also der bewusste Schritt aus dem privaten in den governten Raum — die UI muss diese Konsequenz klar benennen, nicht verstecken.
- **Betroffenenrechte.** Löschen jederzeit (Capture/Seite/Medium); Offboarding kaskadiert (`ON DELETE CASCADE` an `humans`) und entzieht damit auch zuvor geteilten Kontext; Export für Auskunft/Portabilität.

## Flächen

Zwei Oberflächen auf derselben Control-Plane-API:

- **Companion-App (mobil, Flutter).** Die primäre Erfassungsfläche unterwegs: Audio/Text/Foto/Link abladen, durchsuchen, Seiten lesen, freigeben, Aufgaben pushen. Authentifiziert per Bearer-Token (siehe „Technische Umsetzung").
- **Desktop.** Für **Screen-Recording** und **E-Mail-/Datei-Import** braucht es eine Rechner-Fläche — offen, ob als Desktop-Build derselben Flutter-App oder als schlanker Begleiter. *Ausbaustufe.*
- **Web (Person-Seite).** Die bestehende React-SPA zeigt den eigenen Brain-Dump auf der Person-Seite: Seiten (mit Quelle, Medien, Wikilinks), das Protokoll, manuelle Pflege, Freigabe-Verwaltung und der Audit-Einblick „wer hat was gesehen".

## Technische Umsetzung (Referenz)

Bewusst dünn — die Mechanik steht in [`05`](05-gedaechtnis.md), hier nur die Deltas:

- **Schema.** `human_wiki_pages` / `human_wiki_log` als Spiegel von `wiki_pages` / `wiki_log`, Owner `human_id UUID REFERENCES humans(id) ON DELETE CASCADE`, Sichtbarkeit (`visibility: private | shared`). Dazu `captures` (Posteingang: Quelle, Status, Text-Repräsentation, Blob-Ref) und eine Anhang-/Blob-Tabelle. Neue Migration, Bestehendes nie editieren.
- **Store owner-agnostisch.** `internal/memory.Store` wird um Tabellen- und Owner-Spaltennamen parametrisiert (`NewStore` = Agenten-Verhalten unverändert, `NewOwnerStore` für Menschen). Ingest, Query, Consolidate, Log — identischer Code, anderer Owner. Keine Duplizierung.
- **BlobStore-Port.** Medien-Ablage hinter einem schmalen Interface (builtin Dateisystem/Postgres, swappable S3), analog zu `SecretStore`. Seiten referenzieren Blob-IDs.
- **Extraktions-Pipeline.** Pro Quelle ein Extraktor (STT, OCR/Vision, Dokument-Text, HTML→Text) → Text-Repräsentation. Als Registry/Plugin-Muster wie die Ziel-Systeme ([`13-zammad-integration.md`](13-zammad-integration.md)).
- **Endpunkte am eingeloggten Menschen.** `/api/v1/me/captures` (POST Upload/Capture), `/me/memories` (GET/POST), `/me/wiki/log`, `/me/wiki/consolidate`, `/me/memories/{id}` (PATCH/DELETE) — gescopt über den authentifizierten Principal, kein Agenten-Umweg.
- **Mobile-Auth.** Bestehende Session-Infrastruktur (`http_sessions`, [`04-identitaet-secrets.md`](04-identitaet-secrets.md)) wiederverwendet: der Login gibt den Session-Token zusätzlich im Body zurück, die `auth`-Middleware akzeptiert ihn per `Authorization: Bearer <token>`. Kein neuer JWT-Pfad für Menschen.
- **Memory-Kurator.** Agenten-Template mit `SOUL.md`; ein auf den jeweiligen Menschen gescoptes Schreib-Tool (`covey/human_wiki_write`) am Action-Proxy, geweckt über die Wake-Quelle „offene Captures". Nutzt den globalen LLM-Token wie jeder Agent.
- **Medien in die Sandbox.** Die hybride Home-Materialisierung ([`05`](05-gedaechtnis.md)) wird um `~/wiki/media/` erweitert, damit Agenten Medien mit normalen Datei-Tools lesen.
- **Aufgaben-Push.** Wiederverwendung der bestehenden Backlog-API ([`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md)), RBAC-gescopt auf `supervisor_id`/Agent-Owner.
- **Konsolidierung.** Der getaktete Wartungs-Job der Control Plane läuft auch über die Menschen-Wikis.

## Offene Entscheidungen

Ergänzend zu [`07-offene-entscheidungen.md`](07-offene-entscheidungen.md):

- **Multimodale Agenten-Nutzung.** Wie tief Agenten Medien wirklich verwerten (nur Text-Repräsentation vs. Bilder/Audio via materialisierte Dateien vs. multimodale Embeddings) — Umfang und Reihenfolge offen; hängt an der Runtime ([`12-claude-code-adapter.md`](12-claude-code-adapter.md)).
- **Medien-Storage-Backend.** Builtin (Dateisystem/Postgres) reicht für den Durchstich; ab welchem Volumen S3-kompatibel? Dedup identischer Medien, Größen-/Retention-Grenzen.
- **Retention des Roh-Mediums.** Was wird nach der Extraktion behalten (nur Text, Text + Original, zeitlich begrenzt)? Datensparsamkeit vs. Nachvollziehbarkeit.
- **Extraktions-Qualität.** STT-, OCR- und Dokument-Extraktion bestimmen, wie gut der Kurator schneidet. On-device vs. Server, Modell-Wahl.
- **Embedder-Qualität.** Der `HashEmbedder` trägt den Durchstich, liefert aber keine echte semantische Nähe — für die freieren Texte eines Brain-Dumps besonders relevant. Priorität für den Swap auf ein echtes API-Embedding.
- **Isolation geteilten Kontexts.** Zu verhindern, dass ein Agent freigegebenen Vorgesetzten-Kontext in seine *eigene* Wiki-Seite übernimmt (und ihn nach Widerruf behält), ist heute nur prompt-seitig adressierbar. Sauberer Enforcement-Punkt offen.
- **Kurator-Scope.** Ein Kurator pro Mensch vs. pro Org mit Menschen-Scoping vs. pro Mensch konfigurierbar. MVP-Tendenz: Org-Default, pro Mensch übersteuerbar.
- **Freigabe-Granularität.** Pro Seite (einfach) vs. pro Bereich/Projekt/„Space" (ergonomischer) vs. pro Tag. MVP: pro Seite.
- **Aufgaben-Weiche automatisch.** Ob der Kurator „Gedächtnis vs. Aufgabe an Agent X" nur vorschlägt oder (mit Bestätigung) selbst auslöst.
- **Arbeitgeber-Zugriffsmodell.** Der Legal-Hold-Prozess (wer darf im Compliance-Fall Inhalte sehen, unter welchem Vier-Augen-Verfahren) muss mit [`09-enterprise-modell.md`](09-enterprise-modell.md) und [`06-observability-control.md`](06-observability-control.md) formal austariert werden.
- **Desktop-Fläche.** Desktop-Build der Flutter-App vs. schlanker Begleiter für Screen-Recording und Mail-/Datei-Import.
- **Rückkanal Agent → Mensch.** Ein Agent hebt etwas Wichtiges hervor → landet im Posteingang seines Supervisors. Spiegelbild der Freigabe, hier zunächst ausgeklammert.
- **Produktname.** Die Companion als eigenes Produkt braucht einen Namen (Arbeitstitel „Companion").
- **Org-Scope (D5).** Ein gemeinsames Org-Gedächtnis, in das Menschen *und* Agenten einspeisen, ist die nächste Ausbaustufe über die persönliche Freigabe hinaus.
