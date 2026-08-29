---
slug: gedaechtnis
title: Das Wiki-Gedächtnis
description: 'Wie ein KI-Agent Wissen behält: verlinkte Markdown-Seiten mit pgvector-Index statt flacher Schnipsel — lesbar, korrigierbar und über Läufe hinweg verdichtet.'
faq:
  - q: Wo liegen die Gedächtnisdaten meiner Agenten?
    a: In Ihrer Postgres-Datenbank und im Home der jeweiligen Sandbox. Nichts davon verlässt Ihre Installation — außer Sie richten bewusst einen fremden Einbettungsdienst ein. Wer das vermeiden will, betreibt die Einbettung selbst über Ollama.
  - q: Kann ich einen Agenten gezielt etwas vergessen lassen?
    a: Ja. Die Wiki-Seiten sind in der Oberfläche einsehbar und löschbar. Weil das Gedächtnis aus lesbaren Seiten besteht und nicht aus Vektoren allein, kann man auch einzelne Sätze korrigieren, statt alles wegzuwerfen.
  - q: Wächst das Gedächtnis unbegrenzt?
    a: 'Es wird verdichtet: Ein getakteter Wartungslauf führt Duplikate zusammen und repariert tote Verweise. Zusätzlich lässt sich ein plattformweiter Aufräum-Heartbeat einschalten, bei dem jeder Agent sein eigenes Wiki pflegt.'
---

# Das Wiki-Gedächtnis

Ein Agent, der nach jeder Aufgabe vergisst, was er gelernt hat, wiederholt jeden Fehler und jede Recherche. Covey gibt ihm dafür kein Protokoll und keine Schnipselhalde, sondern ein **Wiki**: verlinkte Markdown-Seiten, eine je Sache, durchsuchbar über einen `pgvector`-Index.

## Warum ein Wiki und kein Schnipsel-Speicher

Der übliche Aufbau legt jede Erkenntnis als Textstück ab und sucht später nach Ähnlichkeit. Das findet Formulierungen, keine Zusammenhänge — und genau die machen den Unterschied zwischen einem Agenten, der Fakten wiedergibt, und einem, der eine Sache kennt: Kunde → Ticket → Lösung, Kunde → zuständiger Kollege, Projekt → Repository → offene Baustelle.

Im Wiki hat jede Sache ihre Seite, und die Seiten verweisen mit `[[Wikilinks]]` aufeinander. Diese Verweise sind der Graph. Nebenbei ist das Ergebnis für Menschen lesbar, was der zweite Grund ist: Ein Gedächtnis, in das niemand hineinschauen kann, ist im Zweifel nicht korrigierbar.

## Drei Operationen

- **Ablegen** — am Ende einer Aufgabe ordnet der Agent seine Erkenntnisse den richtigen Seiten zu, statt eine neue anzulegen.
- **Nachschlagen** — beim Aufnehmen einer Aufgabe liefert die Vektorsuche die passenden Seiten, dazu den kompakten Index des ganzen Wikis: Er weiß dadurch auch, was er *nicht* weiß.
- **Verdichten** — ein getakteter Lauf führt Duplikate zusammen und repariert tote Verweise, damit das Wissen dichter wird und nicht nur mehr.

## Hybride Speicherung

Die Seiten liegen zweifach: als `.md`-Dateien im Home der Sandbox, wo der Agent damit arbeitet wie mit Dateien, und in der Control Plane als maßgebliche Fassung mit Index. Geht die Sandbox verloren, geht kein Wissen verloren.

## Einbettung: eingebaut oder extern

Ohne weitere Konfiguration läuft eine eingebaute Einbettung, die Wortüberlappung misst — das reicht zum Ausprobieren, findet aber die eigene Seite nicht wieder, sobald der Agent anders formuliert. Für echte semantische Suche setzt man `COVEY_EMBEDDING_PROVIDER` auf einen Dienst (Voyage, OpenAI) oder betreibt ihn selbst über Ollama; der Bestand wird beim nächsten Start automatisch neu eingebettet.

## Von Hand eingreifen

Das Gedächtnis ist in der Oberfläche sichtbar und änderbar: eine Seite lesen, etwas beitragen, das ein Agent nie erfahren konnte, oder gezielt löschen, was falsch ist. Das ist der praktische Teil des Versprechens — ein Agent, dem man etwas beibringen kann, ohne seinen Prompt anzufassen.

## Auch für Menschen

Derselbe Apparat trägt das Gedächtnis von Menschen: Die [Companion-App](../companion/companion.md) nutzt dieselben Seiten und denselben Index, nur mit einem Menschen als Eigentümer.

## Weiter

- [Das Agenten-Modell](agent-model.md) — wo das Gedächtnis im Lauf auftaucht
- [Backlog & Lifecycle](backlog-and-lifecycle.md) — wann abgelegt und nachgeschlagen wird
