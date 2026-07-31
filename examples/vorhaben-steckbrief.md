# Vorlage: Vorhaben-Steckbrief

Der [Delivery Lead](delivery-lead.bundle.json) trägt nichts Vorhabensspezifisches
in seiner Config. Alles, was ihn an ein konkretes Vorhaben bindet, steht in einer
**Wiki-Seite** — die liest er zu Beginn jedes Laufs (`covey/wiki_search` →
`covey/wiki_read`) und schreibt Gelerntes dorthin zurück.

Damit ist ein zweites Vorhaben eine zweite Wiki-Seite, kein zweiter Agent. Führt
ein Lead mehrere Vorhaben, unterscheidet er sie am Meilenstein-Titel.

**Anlegen:** als Aufgabe an den Lead („Lege den Steckbrief für <Meilenstein> an:
…"), oder direkt über die Gedächtnis-Ansicht des Agenten im UI. Titel der Seite:
`Vorhaben-Steckbrief <Meilenstein-Titel>` — das macht ihn auffindbar.

Die Felder unter „Pflicht" braucht der Lead, sonst arbeitet er nicht los.

---

## Vorlage

```markdown
# Vorhaben-Steckbrief <Meilenstein-Titel>

## Pflicht
- **Projekt-ID:** <numerische GitLab-Projekt-id, nicht der Pfad>
- **Meilenstein-Titel:** <exakt wie in GitLab — der Filter matcht wörtlich>
- **Ziel-Branch:** <Branch, gegen den entwickelt wird>
- **Frist:** <Datum> — <woraus sie sich ergibt>

## Anforderungen im Original
- **Pfad im Repository:** <z. B. docs/anforderungen/kriterien.md>
- **Was maßgeblich ist:** <welches Dokument gewinnt bei Widerspruch zum Ticket-Text>

## Steuerung
- **WIP-Limit:** <Tickets gleichzeitig je Entwickler; ohne Angabe gilt 1>
- **Berichtsticket:** Projekt <id> / #<iid> — dorthin schreibt der Lead den
  Tagesstand. Muss dem Lead zugewiesen und dauerhaft offen sein.
- **Zuständiger Mensch:** <Name>, GitLab-Kennung <username> — bekommt offene
  Fragen und den Bericht. Ohne hinterlegte GitLab-Kennung scheitert `assign`.

## Reihenfolge und Abhängigkeiten
- #<iid> vor #<iid>, #<iid>, … — <Begründung: gemeinsame Grundlage>
- <weitere Ketten>

## Entscheidungen
<Was der Auftraggeber oder der zuständige Mensch festgelegt hat, mit Datum.
Der Lead schreibt hier jede beantwortete Rückfrage hinein — sonst stellt er
sie beim nächsten Ticket erneut.>

## Offene Fragen
<Was noch niemand beantwortet hat, mit Ticket und Wartedauer. Führt der Lead
selbst; der Tagesbericht liest sich von hier.>
```

---

## Warum diese Felder

- **Projekt-ID und Meilenstein-Titel** sind der Schnitt des Arbeitsvorrats
  (`list_issues {"project_id":N,"milestone":"…"}`). Ohne sie greift der Lead
  entweder nichts oder das ganze Projekt.
- **Der Pfad zu den Anforderungen** ist der Unterschied zwischen einem Lead, der
  Tickets sortiert, und einem, der sie implementierbar macht. Ein Ticket-Text ist
  eine Zusammenfassung; die Abnahmekriterien müssen aus dem Original kommen. Die
  Dokumente gehören deshalb ins Repository (versioniert, für alle Kollegen
  lesbar), nicht in einen Anhang.
  Stimmt der Pfad nicht — Datei doch nicht committet, falscher Branch, Tippfehler
  —, sucht der Lead sie einmal per `list_tree` und korrigiert den Steckbrief
  selbst. Findet er sie nicht, **bricht er die Aufbereitung ab** und meldet es
  einmal im Berichtsticket, statt Abnahmekriterien aus Ticket-Titeln zu raten.
  Ein Vorhaben, das nicht anläuft, ist deshalb zuerst hier zu prüfen.
- **Das WIP-Limit** ist die Bremse gegen den häufigsten Fehler: fünf Agenten
  arbeiten gleichzeitig an Tickets, die sich dieselbe Grundlage teilen, und
  erzeugen fünf widersprüchliche Implementierungen auf einem Branch. Im Zweifel
  niedriger ansetzen. Es wirkt allerdings nur, wenn die Entwickler-Agenten auf
  `nur-wenn: gitlab:issues:assigned` stehen — greifen sie frei nach offenen
  Issues, umgehen sie den Lead vollständig.
- **Die Reihenfolge** ist dasselbe Problem in explizit: Was eine gemeinsame
  Grundlage hat, wird nacheinander gebaut. Der Lead hält abhängige Tickets
  zurück, bis der Vorgänger gemergt ist.
- **Entscheidungen** verhindern, dass eine einmal beantwortete Frage in jedem
  weiteren Ticket erneut gestellt wird. Der Kommentarverlauf eines einzelnen
  Tickets ist dafür der falsche Ort — er ist beim nächsten Ticket nicht in Sicht.
