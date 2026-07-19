# 05 — Gedächtnis

Damit ein Agent kein „Goldfisch mit Toolzugang" ist, sondern jemand, der den Laden kennt, braucht er Gedächtnis über Aufgaben hinweg — nicht nur innerhalb einer Session.

## Zwei Ebenen, die nicht verwechselt werden dürfen

| Ebene | Was | Wo |
|---|---|---|
| **Datei-Persistenz** | Dateien, Artefakte, lokale Notizen | persistentes Home der Sandbox (siehe [`02-agenten-modell.md`](02-agenten-modell.md)) |
| **Episodisches Gedächtnis** | „Mit diesem Kunden hatte ich letzte Woche zu tun, die Lösung war Y." | separate Memory-Schicht in der Control Plane |

Das persistente Home reicht für Dateien, aber **nicht** für Wissen über Aufgaben, Kunden, Kollegen und Zusammenhänge. Dafür gibt es die episodische Memory-Schicht.

## Memory-Schicht: Knowledge-Graph statt Vektorsuche

Der Agent fragt die Memory-Schicht im **`triage`**-Schritt ab (was weiß ich über diese Aufgabe / diesen Kunden?) und füttert sie im **`done`**-Schritt (was habe ich gelernt / entschieden?). Zusätzlich kann er **proaktiv mitten im Lauf** einspeisen (`covey/remember` am Action-Proxy, siehe [`01-architektur.md`](01-architektur.md)) — für allgemeingültige Erkenntnisse, die nicht bis zum Abschluss warten sollen. Die Abgrenzung zu aufgabenbezogenen Zwischenständen (Notizen an der Aufgabe, `covey/add_note`) steht in [`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md): Hilft es nur dieser Aufgabe → Notiz; hilft es auch künftigen → Gedächtnis.

Der entscheidende Design-Punkt: **ein Knowledge-Graph zahlt sich hier gegenüber platter Vektorsuche aus**, weil es genau die *Beziehungen* sind, die einen Agenten kompetent machen:

```
   Kunde ──hat──▶ Ticket ──gelöst_durch──▶ Lösung
     │                                        ▲
     └──betreut_von──▶ Kollege ──kennt────────┘
```

Platte Ähnlichkeitssuche findet „ähnliche Texte". Der Graph findet „dieser Kunde ↔ dieses Ticket ↔ diese Lösung ↔ dieser Kollege" — die vernetzte Realität, aus der sinnvolle nächste Schritte folgen.

## Technologie: Graphiti

**Graphiti** passt direkt in diese Rolle (temporal-bewusster Knowledge-Graph, auf inkrementelles Einspeisen von Episoden ausgelegt) und ist bereits in der Hand. Es extrahiert Entitäten und Beziehungen aus den Episoden, die der Agent beim `done`-Schritt einspeist, und macht sie beim `triage`-Schritt abfragbar.

> **Hinweis:** Es gibt eine bewusste Nähe zum früher explorierten „Cruu"-Konzept (E-Mail als erste Datenquelle, Graphiti zur Knowledge-Graph-Extraktion). Die Memory-Schicht hier kann konzeptionell davon erben.

## Scoping des Gedächtnisses

Offene Design-Frage: Ist das Gedächtnis **pro Agent**, **pro Team** oder **geteilt**?

- **Pro Agent** — sauberste Isolation, aber Wissen bleibt in Silos.
- **Geteilt (team-/orgweit)** — ein Kollege-Agent kann von den Erfahrungen anderer profitieren, stärkt die Organisations-Metapher; erfordert aber Berechtigungslogik auf dem Graph (nicht jeder Agent darf alles sehen).

Wahrscheinlich sinnvoll: ein pro-Agent-Kern plus ein geteilter Org-Layer mit Zugriffsregeln. Entscheidung offen — siehe [`07-offene-entscheidungen.md`](07-offene-entscheidungen.md).

## Integration in den Lifecycle

```
triage:  memory.query(kontext)   → relevante Entitäten/Beziehungen in den Prompt
working: (Aufgabe bearbeiten)
done:    memory.ingest(episode)  → neue Fakten/Beziehungen in den Graph
```

Damit wird das Gedächtnis kein separates Feature, sondern ein fester Bestandteil jedes Arbeitszyklus.

Zwei Qualitätsregeln am Ingest-Punkt:

- **Kein Noise:** Floskeln ohne Informationswert („keine neuen Erkenntnisse", „n/a") werden verworfen — der Prompt weist den Agenten an, das `memory`-Feld dann leer zu lassen; die Memory-Schicht filtert als Sicherheitsnetz zusätzlich selbst.
- **Manuelle Pflege:** Menschen mit manage-Rolle können Episoden über API und UI einspeisen, ändern und löschen (Onboarding-Wissen mitgeben, Veraltetes oder Falsches korrigieren) — das Einarbeitungsgespräch für den neuen Mitarbeiter. Manuell eingespeiste Episoden tragen `source: manual` in den Metadaten und bleiben so von Agent-Gelerntem unterscheidbar.
