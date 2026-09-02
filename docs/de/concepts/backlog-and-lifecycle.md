---
slug: backlog-lifecycle
title: Backlog & Lifecycle
description: 'Wie Arbeit zu einem Agenten kommt: Backlog als eigenständiges Objekt, Weckquellen über Webhook und Heartbeat, die Zustände open, in_progress, blocked, done.'
faq:
  - q: Wie lange kann eine Aufgabe blockiert warten?
    a: Unbegrenzt, und ohne laufende Kosten. Der Agent schläft; erst das passende Ereignis — eine Kundenantwort, ein Kommentar, eine Freigabe — weckt genau diese Aufgabe wieder auf.
  - q: Was passiert, wenn ein Lauf mitten in einer Aufgabe abbricht?
    a: Die Aufgabe geht zurück in den Backlog und wird beim nächsten Wecken erneut aufgenommen. Beim Start der Control Plane werden verwaiste Aufgaben eingesammelt, damit nichts liegen bleibt, weil ein Prozess neu gestartet wurde.
  - q: Kann ich Aufgaben von außen anlegen?
    a: 'Ja, über die API oder über ein Zielsystem: Ein Ticket, eine E-Mail oder ein Issue kann eine Aufgabe erzeugen. Genau das ist der Normalfall — von Hand angelegte Aufgaben sind eher die Ausnahme.'
---

# Backlog & Lifecycle

Arbeit kommt bei covey nicht aus einem Chat-Fenster, sondern aus einem Backlog. Das ist keine Geschmacksfrage: Eine Aufgabe, die ein eigenes Objekt ist, hat einen Zustand, einen Verantwortlichen, eine Historie und ein Ende — ein Chat-Verlauf hat nichts davon.

## Die Zustände

- `open` — angelegt, wartet auf Bearbeitung
- `in_progress` — der Agent arbeitet gerade daran
- `blocked` — wartet auf eine Antwort von außen
- `done`, `failed`, `cancelled` — abgeschlossen, gescheitert, zurückgezogen

Auf dem Board sind die Spalten frei konfigurierbar; darunter bleiben es diese Zustände.

## Der Lauf

Geweckt wird ein Agent von einer neuen Aufgabe, einem Webhook, einem Heartbeat oder von Hand. Dann läuft immer dieselbe Kette: Sandbox hoch → Aufgabe aufnehmen (dabei im Gedächtnis nachschlagen) → arbeiten → Ergebnis festhalten und ins Gedächtnis ablegen → Sandbox runter, Agent schläft.

Die Kette ist bewusst kurz. Was länger dauert als ein Lauf, wird nicht durch Warten gelöst, sondern durch `blocked`.

## Warum blocked der interessante Zustand ist

Ein Agent, der auf eine Kundenantwort wartet, darf keine Rechenzeit verbrauchen — und er darf die Antwort auch nicht verpassen. Also wird die Aufgabe geparkt und mit einem Korrelationsschlüssel versehen, etwa `zammad:ticket:4711`.

Trifft später ein Ereignis mit demselben Schlüssel ein, weckt es genau diese Aufgabe wieder, mit ihrem Kontext. Zwischen Frage und Antwort können Minuten liegen oder drei Tage; die Kosten dafür sind null.

## Weckquellen

- **Webhook** — das Zielsystem ruft covey, wenn dort etwas passiert. Der schnellste Weg, und der einzige ohne Leerlauf.
- **Heartbeat** — ein Takt aus der `HEARTBEAT.md`, etwa `- alle: 30m titel: Posteingang aufgabe: Neue Tickets sichten.` Mit `nur-wenn:` fragt die Control Plane vorher billig nach, ob es überhaupt etwas zu tun gibt, und lässt den Agenten sonst schlafen.
- **Von Hand** — „Wecken" in der Oberfläche, oder ein API-Aufruf.

## Turn-Limit und Budget

Ein Lauf hat eine Obergrenze an Schritten (`max_turns`). Wird sie erreicht, bricht der Lauf kontrolliert ab, statt sich im Kreis zu drehen — meist ein Zeichen, dass die Aufgabe zu groß geschnitten ist. Häufige Abbrüche dieser Art meldet auch die Konfigurationsprüfung.

## Weiter

- [Zielsysteme & Plugins](../integrations/target-systems.md) — woher die Weckereignisse kommen
- [Das Agenten-Modell](agent-model.md) — was zwischen zwei Läufen bleibt
- [Guard-Rails & Kontrolle](guard-rails.md) — wo ein Lauf anhält
