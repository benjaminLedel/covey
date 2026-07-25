# 05 — Gedächtnis

Damit ein Agent kein „Goldfisch mit Toolzugang" ist, sondern jemand, der den Laden kennt, braucht er Gedächtnis über Aufgaben hinweg — nicht nur innerhalb einer Session.

## Zwei Ebenen, die nicht verwechselt werden dürfen

| Ebene | Was | Wo |
|---|---|---|
| **Datei-Persistenz** | Dateien, Artefakte, flüchtige Arbeitsnotizen einer Aufgabe | persistentes Home der Sandbox (siehe [`02-agenten-modell.md`](02-agenten-modell.md)) |
| **Semantisches Gedächtnis** | „Kunde X löst man über Y, betreut von Kollegin Z." — vernetztes Wissen über Aufgaben, Kunden, Kollegen, Systeme | **das Wiki**, geführt in der Control Plane |

Das persistente Home reicht für Dateien, aber **nicht** für Wissen, das über die einzelne Aufgabe hinaus trägt. Dafür gibt es das Wiki.

## Das Modell: ein LLM-gepflegtes Wiki

Der entscheidende Design-Punkt: **es sind die *Beziehungen*, die einen Agenten kompetent machen**, nicht Textähnlichkeit. Eine platte Ähnlichkeitssuche über angesammelte Freitext-Schnipsel findet „ähnliche Sätze"; sie häuft Duplikate an, verwaltet Widersprüche nicht und kennt keine Struktur. Was der Agent braucht, ist die vernetzte Realität:

```
   Kunde ──hat──▶ Ticket ──gelöst_durch──▶ Lösung
     │                                        ▲
     └──betreut_von──▶ Kollege ──kennt────────┘
```

Statt diese Struktur in einen separaten Graph-Store zu extrahieren, pflegt der Agent sie als das, was er ohnehin am besten kann: **vernetzte Markdown-Seiten**. Eine Seite pro Entität — Kunde, Projekt, Kollege, System, wiederkehrendes Problem — mit expliziten Querverweisen `[[Andere-Seite]]`. Die Wikilinks *sind* der Graph: traversierbar, aber ohne eigene Graph-Datenbank und ohne LLM→Graph-Extraktionspipeline.

Das Wiki hat eine feste Grundstruktur:

| Datei | Inhalt |
|---|---|
| **Entitäts-/Themenseiten** | je eine Seite pro verlinkbarer Entität; Frontmatter (`type`, `tags`, `scope`, `source`, `updated_at`) + Fließtext mit `[[Wikilinks]]` |
| **`index.md`** | navigierbarer Katalog: eine Zeile pro Seite mit Einzeiler-Zusammenfassung, nach Kategorie gruppiert. Wird bei jedem Ingest aktualisiert |
| **`log.md`** | append-only, chronologisch (`## [2026-07-25] ingest | Titel`) — Protokoll aller Ingests, Queries und Lint-Läufe |

**Kernregel** (steht auch im Wiki-Schema, das dem Agenten mitgegeben wird): *neue Seite, wenn es eine eigenständige, von anderswo verlinkbare Entität ist; bestehende Seite editieren, wenn es ein Attribut oder Update von etwas Vorhandenem ist.* Der Agent **erfindet nichts** — er kompiliert nur aus dem, was er in Aufgaben tatsächlich gelernt hat.

## Retrieval: Wiki + Vektor-Index

Die Seiten sind die Quelle der Wahrheit. Für das Auffinden liegt **über den Seiten weiterhin ein Vektor-Index** (`pgvector`, siehe [`10-architektur-stack.md`](10-architektur-stack.md)) — der Agent findet also nicht mehr zufällige Schnipsel, sondern die *relevanten Seiten*. Zwei Wege ergänzen sich:

- **Vektorsuche** über Seiten-Chunks → die thematisch nächsten Seiten.
- **Struktur-Navigation** über `index.md` und `[[Wikilinks]]` → von einer Treffer-Seite einen Hop weiter zu den verbundenen Entitäten (Kunde → Kollege → dessen offene Themen).

So bleibt gutes Retrieval erhalten, und die Beziehungen, die den Graphiti-Plan motiviert haben, entstehen pragmatisch aus der Verlinkung.

## Speicherung: hybrid (Home-Arbeitskopie ↔ Control-Plane-Quelle)

Das Wiki lebt zweifach, mit klarer Rollenteilung:

- **Sandbox-Home — Arbeitskopie.** Die Seiten liegen als echte `.md`-Dateien im persistenten Home. Der Agent liest und schreibt sie mit *normalen Datei-Tools* — keine Spezial-API, kein Reibungsverlust. Das ist die natürliche Schreibfläche.
- **Control Plane — Quelle der Wahrheit.** Bei Aufgabenabschluss werden geänderte Seiten in die Control Plane synchronisiert (Postgres); von dort wird der Vektor-Index gepflegt und das Wiki bei jedem Sandbox-Neubau ins Home materialisiert. Damit überlebt das Wissen den Verlust einer „dummen und ersetzbaren" Sandbox (siehe [`01-architektur.md`](01-architektur.md)), ist gebrokert, org-weit abfragbar und zentraler Governance zugänglich.

Diese Trennung löst die Spannung „Dateien vs. Kontrollebene": die Datei-Ergonomie fürs Schreiben, die Kontrollebene für Persistenz, Retrieval und Zugriffsregeln. Der Sync läuft über das Daemon-Protokoll, dieselbe stabile Naht wie der übrige Config-/Home-Austausch.

## Drei Operationen

**Ingest** — im **`done`**-Schritt (was habe ich gelernt / entschieden?) und proaktiv mitten im Lauf über `covey/remember` am Action-Proxy (siehe [`01-architektur.md`](01-architektur.md)), für allgemeingültige Erkenntnisse, die nicht bis zum Abschluss warten sollen. Der Agent ordnet die Erkenntnis der richtigen Seite zu (neu vs. editieren), setzt Wikilinks und aktualisiert `index.md` + `log.md`. Abgrenzung zu aufgabenbezogenen Zwischenständen (`covey/add_note`, siehe [`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md)): Hilft es nur dieser Aufgabe → Notiz; hilft es auch künftigen → Wiki.

**Query** — im **`triage`**-Schritt (was weiß ich über diese Aufgabe / diesen Kunden?): Vektorsuche liefert die relevanten Seiten, optional einen Wikilink-Hop erweitert, als Kontextblock in den Prompt.

**Lint / Konsolidierung** — periodisch (geplanter Job, nicht an eine einzelne Aufgabe gebunden): Duplikat-Seiten mergen, Widersprüche zwischen Seiten auflösen, veraltete Aussagen kennzeichnen, verwaiste Seiten und fehlende Querverweise finden. **Das ist die Pflege-Mechanik, die dem flachen Schnipsel-Store gefehlt hat** — sie hält das Wiki widerspruchsarm und lässt das Wissen mit jeder Aufgabe *verdichten* statt nur *anwachsen*.

## Integration in den Lifecycle

```
triage:  wiki.query(kontext)     → relevante Seiten (+ 1 Wikilink-Hop) in den Prompt
working: (Aufgabe bearbeiten; Seiten im Home direkt les-/schreibbar)
done:    wiki.ingest(erkenntnis) → Seite anlegen/editieren, Links, index.md, log.md; Sync in die Control Plane
─────────
lint:    wiki.consolidate()      → periodisch, aufgabenunabhängig
```

Damit wird das Gedächtnis kein separates Feature, sondern ein fester Bestandteil jedes Arbeitszyklus.

Zwei Qualitätsregeln am Ingest-Punkt:

- **Kein Noise:** Floskeln ohne Informationswert („keine neuen Erkenntnisse", „n/a") werden verworfen — der Prompt weist den Agenten an, das Wiki dann nicht anzufassen; die Memory-Schicht filtert als Sicherheitsnetz zusätzlich selbst.
- **Manuelle Pflege:** Menschen mit manage-Rolle können Seiten über API und UI anlegen, ändern und löschen (Onboarding-Wissen mitgeben, Veraltetes oder Falsches korrigieren) — das Einarbeitungsgespräch für den neuen Mitarbeiter. Manuell gepflegte Seiten tragen `source: manual` im Frontmatter und bleiben so von Agent-Gelerntem unterscheidbar.

## Scoping des Gedächtnisses

Offene Design-Frage (siehe [`07-offene-entscheidungen.md`](07-offene-entscheidungen.md), D5): Ist das Wiki **pro Agent**, **pro Team** oder **geteilt**?

- **Pro Agent** — sauberste Isolation, aber Wissen bleibt in Silos.
- **Geteilt (team-/orgweit)** — ein Kollege-Agent profitiert von den Erfahrungen anderer, stärkt die Organisations-Metapher; erfordert aber Zugriffsregeln auf Seitenebene (nicht jeder Agent darf jede Seite sehen).

Wahrscheinlich sinnvoll: ein pro-Agent-Kern plus ein geteilter Org-Layer. Das `scope`-Frontmatter jeder Seite trägt diese Entscheidung; beim Query filtert die Control Plane die sichtbaren Seiten. Entscheidung offen.

## Umsetzungsstand & Bezug zu Graphiti

Der MVP (M7) hat mit einem **flachen `pgvector`-Schnipsel-Store** begonnen (query@triage, ingest@done) — die ehrliche, dünne Durchstich-Variante. Das **Wiki-Modell ist implementiert** (Migration `0031_wiki`, `internal/memory`): die Einheit ist die **verlinkte Seite** (`wiki_pages`) statt des losen Schnipsels, mit `pgvector`-Index fürs Retrieval, Wikilink-Extraktion aus dem Body, Konsolidierungs-Pass (`Consolidate`) und den Agenten-Tools `covey/wiki_search|read|write` über den Action-Proxy. Der alte flache Store (`memories`) bleibt als übernommene Bestandsdaten. Auch die **hybride Speicherung ist umgesetzt** (`internal/daemon/wikilocal.go`): die Seiten werden zu Aufgabenbeginn als `~/wiki/*.md` ins Home materialisiert und am Aufgabenende zurückgesynct — die Control Plane bleibt Quelle der Wahrheit.

Damit **ersetzen die Wikilinks den früher geplanten Graphiti-Knowledge-Graph**: die Beziehungen leben in der Verlinkung, nicht in einem separaten temporalen Graph-Store. Braucht es später doch echtes temporales Reasoning, kann Graphiti über dasselbe Interface-Muster hinter dem Wiki nachrüsten — der Lifecycle-Kontrakt (query/ingest/consolidate) bleibt gleich.

> **Hinweis:** Es gibt eine bewusste Nähe zum früher explorierten „Cruu"-Konzept (E-Mail als erste Datenquelle, Wissensextraktion aus dem laufenden Betrieb). Das Wiki hier kann konzeptionell davon erben.
