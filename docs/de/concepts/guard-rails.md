---
slug: guard-rails
title: Guard-Rails & Kontrolle
description: 'Freigaben, Aufzeichnung und Not-Aus: Wie covey Grenzen für KI-Agenten außerhalb der Runtime erzwingt — fail-closed, nicht über den Prompt, und für jeden Lauf nachvollziehbar.'
faq:
  - q: Kann ein Agent seine eigenen Guard-Rails ändern?
    a: Nein. Sie liegen in der Control Plane, nicht in seiner Konfiguration, und werden außerhalb der Runtime durchgesetzt. Ändern kann sie ein Mensch mit der passenden Rolle — und diese Änderung steht im Audit-Trail.
  - q: Was ist der Unterschied zwischen Egress-Allowlist und Freigabe?
    a: Die Allowlist entscheidet, mit welchen Hosts die Sandbox überhaupt sprechen darf — technisch, im Netz. Eine Freigabe entscheidet über eine einzelne fachliche Aktion, etwa eine Antwort an einen Kunden. Das eine ist eine Mauer, das andere ein Vier-Augen-Prinzip.
  - q: Hilft das gegen Prompt-Injection aus einem Ticket?
    a: Es ist genau der Grund für den Aufbau. Ein Text aus einem Zielsystem kann das Modell in die Irre führen — er kann aber keine Regel ändern, die außerhalb des Modells liegt. Die Aktion scheitert dann an der Guard-Rail und landet als abgelehnter Versuch im Recording.
  - q: Wie lange werden Aufzeichnungen aufbewahrt?
    a: So lange, wie Sie es einstellen — es ist Ihre Datenbank. Die Aufbewahrungsdauer ist konfigurierbar, und was für Ihre Revision nötig ist, entscheiden Sie, nicht die Plattform.
---

# Guard-Rails & Kontrolle

Die Frage, die über den Einsatz von Agenten in einem Unternehmen entscheidet, ist nicht „kann er das?", sondern „was passiert, wenn er sich irrt?". covey beantwortet sie mit drei Dingen: erzwungenen Grenzen, Freigaben an den richtigen Stellen und einer Aufzeichnung, die hinterher trägt.

## Warum nicht im Prompt

„Du darfst keine Kundendaten löschen" im Prompt ist eine Bitte. Sie hilft im Normalfall und versagt genau dann, wenn es darauf ankommt: bei einer ungewöhnlichen Formulierung, bei einem Text aus einem Ticket, der wie eine Anweisung klingt, bei einem Modellwechsel.

Guard-Rails stehen deshalb **außerhalb der Runtime**. Der Agent kann sie nicht lesen, nicht überreden und nicht umgehen — er merkt nur, dass eine Aktion abgelehnt wurde. Im Zweifel wird abgelehnt, nicht durchgelassen.

## Die drei Stellen, an denen es greift

- **Secrets-Broker** — welcher Zugang, mit welchem Scope, für welchen Lauf. Ein Token, das nie in die Sandbox gelangt, kann auch nicht mitgenommen werden.
- **Egress** — wohin die Sandbox überhaupt sprechen darf. Mit `COVEY_EGRESS_ENFORCE` und harter Netz-Isolation liegt zwischen Agent und Internet ein Proxy, der die Allowlist durchsetzt.
- **Aktionsebene** — welche Aktion erlaubt ist, welche eine Freigabe braucht, welche verboten ist. Regeln greifen global, je Abteilung oder je Agent.

## Freigaben

Kritische Aktionen halten an und warten auf einen Menschen. Die Voreinstellung nach der Installation zeigt das Muster: eine Antwort nach außen zum Kunden braucht eine Freigabe, HR-Systeme sind gesperrt, alles, was `delete` heißt, ist hart verboten.

Der Agent bleibt derweil in `blocked` — er wartet, ohne Rechenzeit zu verbrauchen, und macht weiter, sobald die Freigabe da ist.

## Aufzeichnung

Jeder Lauf wird mitgeschrieben: Werkzeugaufrufe mit Argumenten, Zielsystem-Aktionen, Freigaben mit Entscheider, bei Browser-Aufgaben Screenshots, dazu Tokens und Kosten je Modell. Das ist der Teil, den Revision und Datenschutz sehen wollen — und der Teil, mit dem man einen Fehlgriff hinterher versteht.

## Not-Aus

Ein Schalter hält einen Agenten an. Ein zweiter hält die gesamte Belegschaft der Organisation an. Beide wirken sofort, laufende Sandboxen werden beendet. Es ist die Antwort auf die Frage, die in jeder Sicherheitsabnahme kommt: „Und wenn wir das alles stoppen müssen?"

## Kosten als Grenze

Ein Budget je Agent ist auch eine Guard-Rail — die gegen den Fehler, der niemandem wehtut außer der Rechnung. Ist es aufgebraucht, arbeitet der Agent nicht weiter.

## Weiter

- [Identität & Secrets](identity-and-secrets.md) — wie Zugänge gebrokert werden
- [Betrieb & Deployment](../operations/operations.md) — Egress-Isolation einschalten
- [Zielsysteme & Plugins](../integrations/target-systems.md) — welche Aktionen es überhaupt gibt
