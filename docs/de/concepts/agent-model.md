---
slug: agenten-modell
title: Das Agenten-Modell
description: 'Was einen Covey-Agenten ausmacht: eigene Identität, isolierte Sandbox mit persistentem Home, versioniertes Verhalten in SOUL.md und Rechenzeit nur bei Bedarf.'
faq:
  - q: Behält ein Agent seine Dateien zwischen zwei Läufen?
    a: Ja. Der Container ist ephemer, das Home nicht — es wird als Volume eingehängt und überlebt jeden Neustart. Ein geklontes Repository, eine heruntergeladene Datei oder eine Notiz ist beim nächsten Wecken noch da.
  - q: Kann ein Agent einen anderen beauftragen?
    a: 'Über den Org-Chart und den Backlog: Ein Agent kann einem anderen eine Aufgabe anlegen und auf deren Ergebnis warten, statt selbst zu tun, wofür ein anderer zuständig ist. Die Eskalation nach oben geht an einen Menschen.'
  - q: Was ist eine warme Sandbox?
    a: Ein Container, der zwischen zwei Weckungen stehen bleibt, damit der nächste Lauf nicht kalt startet. Sinnvoll bei Agenten, die im Minutentakt geweckt werden; unnötig bei einem, der dreimal am Tag arbeitet.
---

# Das Agenten-Modell

Ein Agent ist kein Prompt und kein Chat-Verlauf. Er ist eine Entität, die bestehen bleibt, wenn niemand hinschaut: mit Identität, Arbeitsplatz, Zugängen, Gedächtnis und einem Verhalten, das als Code versioniert ist.

## Identität

Jeder Agent hat eine eigene Identität in der Plattform — nicht die des Menschen, der ihn angelegt hat. Daran hängen seine Zugänge, sein Platz im Org-Chart und jede Spur, die er hinterlässt. Optional bekommt er eine eigene E-Mail-Adresse, und dann sieht ihn ein Zielsystem als das, was er ist: als Absender mit Namen.

Das ist der Unterschied zu einem Skript, das unter dem Konto eines Administrators läuft. Wenn hinterher jemand fragt, wer diesen Kommentar geschrieben hat, gibt es eine Antwort.

## Der erste Tag

Ein Agent trägt den Zeitpunkt, an dem er eingestellt wurde. Fehlt er, ist er ein **Entwurf**: angelegt, konfigurierbar, im Org-Chart sichtbar — und er arbeitet nicht. Kein Dispatch, kein Heartbeat, kein scharfer Webhook, keine Sandbox, keine Kosten. Aufgaben dürfen trotzdem in seinem Backlog liegen und starten am ersten Tag.

Der Kill-Switch hätte technisch gereicht, würde aber zwei verschiedene Tatsachen in dasselbe Feld legen: „der wurde gestoppt" und „der hat noch nicht angefangen". Und das Einstellen ist keine Fahne, sondern ein Zeitpunkt — er steht später im Mitarbeiterprofil neben dem eines Menschen.

So entsteht jeder Agent, den niemand von Hand geschrieben hat: aus einer Vorlage, aus einem Bundle-Import und vor allem dort, wo ein *Agent* den Agenten entwirft. Einstellen bleibt ein menschlicher Akt; es gibt dafür keine Aktion, die ein Agent aufrufen könnte.

## Sandbox mit persistentem Home

Gearbeitet wird in einem Container, der zum Wecken entsteht und danach verschwindet. Was bleibt, ist `/home/agent`: geklonte Repositories, heruntergeladene Anhänge, Notizen, die Wiki-Seiten des Agenten. Beim nächsten Lauf findet er seinen Arbeitsplatz so vor, wie er ihn verlassen hat — die Maschine darum herum ist neu.

Der Arbeitsplatz ist im Browser einsehbar: durchsehen, eine Vorlage hineinlegen, eine Datei ändern, eine Auswahl als ZIP herausziehen. Auch während der Agent schläft, denn das Home liegt auf der Platte und nicht im Container.

## Verhalten als Code

`SOUL.md`, `PLAYBOOKS.md`, `CAPABILITIES.md`, `ACCESS.md`, `HEARTBEAT.md`, `ORG.md` — sechs Dateien, versioniert, mit Verlauf und Rücksprung. Eine Verhaltensänderung ist damit ein Vorgang, den man lesen, prüfen und zurücknehmen kann, und kein Deployment.

## Seriell vor parallel

Ein Agent bearbeitet eine Aufgabe zur Zeit. Das ist eine Entscheidung, keine Beschränkung: Zwei gleichzeitige Läufe desselben Agenten würden sich um dasselbe Home und denselben Zustand streiten, und der Fehler wäre nicht reproduzierbar. Mehr Durchsatz kommt über mehr Agenten.

## Immer erreichbar, Rechenzeit nur bei Bedarf

„Always-on" ist eine Eigenschaft der Bedienung, nicht der Rechnung. Ein billiger Dispatch-Loop hält den Agenten erreichbar; der teure Teil — Container, Modell — läuft nur, wenn Arbeit anliegt. Ein Agent im Zustand `sleeping` kostet nichts außer einer Zeile in der Datenbank.

Für Aufgaben, bei denen der Start ins Gewicht fällt, lässt sich eine **warme Sandbox** einschalten: Der Container bleibt zwischen zwei Weckungen stehen. Das kostet Speicher und spart Sekunden — eine bewusste Abwägung, keine Voreinstellung.

## Grenzen, die der Agent nicht verschieben kann

`max_turns` begrenzt, wie viele Schritte ein Lauf machen darf, ein Budget begrenzt, was er kosten darf, die Guard-Rails begrenzen, was er tun darf. Alle drei liegen außerhalb des Modells — sie stehen nicht im Prompt und lassen sich nicht wegargumentieren.

## Weiter

- [Backlog & Lifecycle](backlog-and-lifecycle.md) — die Zustände und wer sie ändert
- [Das Wiki-Gedächtnis](memory.md) — was der Agent behält
- [Guard-Rails & Kontrolle](guard-rails.md) — wo die Plattform eingreift
