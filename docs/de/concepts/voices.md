---
slug: stimmen
title: Stimmen
description: 'Wie ein covey-Agent schreibt wie ein Mensch: eine Stimme, aus Texten gemessen, in Worten beschrieben oder von einem Agenten im Chat entworfen — Karte, Passagen, Chat-Ton und, wenn gemessen, ein Profil, an dem die Stilprüfung misst.'
faq:
  - q: Wir haben keine Texte zum Hochladen. Bekommt ein Agent trotzdem eine Stimme?
    a: Ja. Beschreiben Sie in eigenen Worten, wie Sie schreiben; ein Modellaufruf schreibt die Karte, fünf bis acht Beispielpassagen und einen vorgeschlagenen Chat-Ton. Eine solche Stimme ist beschrieben, nicht gemessen — die Stilprüfung misst nicht an ihr, bis Texte hinzukommen und sie gebaut wird.
  - q: Wirkt etwas, das ein Modell geschrieben hat, ohne dass es jemand gelesen hat?
    a: Nein. Die Karte und die Passagen einer beschriebenen Stimme sind Entwürfe, bis ein Mensch sie auf der Seite der Stimmen freigibt, und eine Stimme lässt sich erst für einen Anlass nennen, wenn an ihr etwas freigegeben oder gemessen ist.
  - q: Was kostet es, eine Stimme zu bauen?
    a: Texte messen kostet nichts. Die Karte, eine Stimme aus einer Beschreibung, eine Probe und eine Überarbeitung sind je ein begrenzter Modellaufruf mit dem Zugang der Organisation; die Seite sagt das neben jedem Knopf.
---

# Stimmen

Ein Stilprofil sagt, wie *weit* ein Text von einem Korpus entfernt ist; es kann ihn nicht wie den Autor des Korpus klingen lassen. Eine **Stimme** kann das. Sie gehört der Organisation, wird je Anlass — Chat, Kunden, Veröffentlichungen — einem Agenten, einer Abteilung oder der ganzen Organisation zugewiesen und in dem Moment gewählt, in dem der Agent schreibt, danach, an wen er schreibt ([unten](#welche-stimme-gilt-je-anlass-und-je-leser)).

Eine Stimme trägt:

- eine **Karte** — 250 bis 400 Wörter darüber, wie der Autor einsteigt, argumentiert, Fakten einbringt, Sätze baut und was er nie tut;
- **Passagen** — fünf bis acht Absätze, die die Hand zeigen; sie stehen im Prompt;
- einen **Chat-Ton** — Anrede, Ton und Emoji im Team-Chat;
- wenn sie aus Texten gemessen ist: ein **Profil**, an dem die Stilprüfung jeden ausgehenden Text misst, und einen **Kontrast** zu KI-Text, wenn eine Referenz hochgeladen wurde.

## Welche Stimme gilt: je Anlass und je Leser

Ein Agent trägt nicht eine Stimme für alles. Eine Stimme wird je **Anlass** gewählt:

- **Chat** — der Team-Chat und die Arbeit, die ein Agent für eine Bitte aus einem Gespräch erledigt;
- **Kunden** — Mails, Tickets und Antworten in Zielsystemen; jede andere Aufgabe;
- **Veröffentlichungen** — Blogbeiträge, Dokumentation, Angebote; eine Aufgabe sagt das mit einer Zeile `occasion: publications` (auch ein `aufgabe:` in der `HEARTBEAT.md` kann sie tragen).

Für jeden Anlass gewinnt die erste von drei Stellen, die eine Stimme nennt, und zwar in dem Moment, in dem der Agent schreibt:

1. die **Abteilung** der Person, der der Agent antwortet — im Bearbeitungsmodus des Organigramms unter **Publikum und Stimmen**;
2. der **Agent** — in seinen Einstellungen unter **Stimmen**, ein Platz je Anlass; ein leerer Platz sagt, was stattdessen gilt und woher;
3. die **Organisation** — **Administration → Standardstimmen**.

Kunden haben keine Abteilung; für sie zählen nur die Stimmen des Agenten und der Organisation. In einem Gruppengespräch mit Menschen aus mehreren Abteilungen passt keine einzelne Abteilungsstimme für alle, deshalb gilt die des Agenten. Ist nichts genannt, schreibt der Agent wie bisher: mit der `TONE.md` seiner Konfiguration.

**So sprechen Sie mit uns.** Eine Abteilung kann eine Zeile hinterlegen — höchstens 400 Zeichen, etwa „Zahlen zuerst, keine Ticketnummern“ —, die alles begleitet, was ein Agent an jemanden aus dieser Abteilung schreibt, gleich welche Stimme gilt. In einer Gruppe kommen die Zeilen aller beteiligten Abteilungen zusammen mit (höchstens vier).

**Wer bekommt was.** Die Liste der Stimmen zeigt eine Tabelle: jede Abteilung und alle ohne Abteilung gegen die drei Anlässe, mit der geltenden Stimme und ihrer Herkunft; wählt man einen Agenten, zählen auch seine Plätze. Die Seite einer Stimme nennt, wer sie wofür nennt, und ihre Probe lässt sich an jemanden aus einer Abteilung schreiben. Jede Chat-Antwort zeigt die Stimme, in der sie gesprochen hat („Stimme: X · für Vertrieb“), und jeder Lauf hält Stimme und Grund fest (`agent×customers`, `department:Vertrieb×chat`, `none×chat`).

Einen Platz zu setzen schreibt keine Konfigurationsversion: Die Stimme wirkt so, wie sie steht, wenn der Agent schreibt, und die Aufzeichnung sagt, welche es war. Die Stilprüfung, `covey/style_check` und `covey/style_apply` messen an der Stimme des Anlasses nach außen — Kunden, oder Veröffentlichungen, wenn die Aufgabe es sagt.

**Nach dem Upgrade** (Migration 0122) steht die eine Stimme eines Agenten in seinen Plätzen **Kunden** und **Veröffentlichungen**, und **Chat** ist leer. Der Team-Chat nimmt dann die Chat-Standardstimme der Organisation, wenn eine gesetzt ist, sonst keine Stimme; der Chat-Ton (du/Sie, Emoji) kommt weiter aus der Kundenstimme, bis eine Chat-Stimme genannt ist. Wie ein Agent mit Kollegen redet, ändert sich nicht von selbst.

## Eine Stimme bauen

*Stimmen* in der Bibliothek hat einen Knopf **Neue Stimme**, der durch sechs Schritte führt; eine bestehende Stimme zeigt dieselben Schritte als Abschnitte ihrer Seite. Eine Leiste oben markiert jeden Schritt als erledigt, offen, optional oder noch nicht möglich, und die Zeile darunter nennt, was den nächsten aufhält.

1. **Zweck** — Blog, Support-Mail, Chat, Angebote oder Anderes. Er steht in jedem Prompt, der in der Stimme schreibt, und bestimmt, welche Art Probe die Vorschau schreibt.
2. **Quelle** — eine von dreien:
   - **Aus Texten.** Laden Sie hoch, was jemand geschrieben hat (Dateien oder eingefügter Text). Schon beim Hochladen sagt die Seite, was den Texten fehlt: wie viele Texte mit 150 Wörtern oder mehr noch für belastbare Bänder nötig sind (vier insgesamt), welche Texte zu kurz sind, um ein Band zu tragen, welche ihre Leser anders ansprechen als die übrigen („diese Texte wirken wie ein anderes Register“), welche in einer anderen Sprache sind und in welchen die Sätze viel länger oder kürzer laufen als in den anderen. **Bauen** misst sie; die Karte ist ein Modellaufruf.
   - **Aus einer Beschreibung.** Sagen Sie, wie Sie schreiben — so, wie Sie es einer neuen Kollegin erklären würden: wie ein Text beginnt, wie Fakten hineinkommen, wie lang die Sätze sind, was Sie nie schreiben, du oder Sie. Ein Modellaufruf schreibt Karte, Passagen und einen vorgeschlagenen Chat-Ton. Die Aussagen der Karte stammen aus Ihrer Beschreibung, nicht aus zitierten Passagen, und die Seite kennzeichnet die Stimme als **beschrieben, nicht gemessen**.
   - **Aus dem Chat.** Bitten Sie einen Agenten im Team-Chat, eine Stimme zu entwerfen. Er befragt Sie in ein paar Fragen oder übernimmt Texte, die Sie ihm geben, und legt mit `covey/voice_draft` einen Entwurf an. Der Entwurf erscheint auf der Seite der Stimmen, mit dem Namen des Agenten.
3. **Material** — die Texte gebaut oder die Beschreibung geschrieben.
4. **Chat-Ton** — optional; eine beschriebene Stimme bringt einen Vorschlag mit, der gilt, sobald er gespeichert ist.
5. **Probe** — optional; eine kurze Probe zu einem Thema Ihrer Wahl, als Absatz, Mail oder Chat-Nachricht. Wird nicht gespeichert. Steht neben einer freigegebenen Karte ein neuer Entwurf, lassen sich beide hören.
6. **Freigabe** — lesen Sie die Karte, korrigieren Sie sie oder **überarbeiten Sie sie mit einer Anweisung** („weniger förmlich, mehr konkrete Zahlen“): Die Überarbeitung ist ein Entwurf, der neben dem Text davor steht. Nichts davon wirkt vor der Freigabe; eine freigegebene Karte überschreibt kein späterer Bau.

## Beschrieben oder gemessen, und die Stilprüfung

Eine Stimme aus einer Beschreibung hat **kein Profil**. Gemessen wurde nichts, also misst die Stilprüfung nicht an ihr: Sie hält fest, dass sie nicht angewandt wurde, und lässt den Text durch. Die Stimme wirkt trotzdem dort, wo es am meisten zählt — im Prompt, über Karte und Passagen. Fügen Sie Texte hinzu und bauen Sie, dann ist sie gemessen: Die Passagen kommen aus den Texten, die Prüfung greift, und die freigegebene Karte bleibt.

## Ein Agent entwirft eine Stimme

`covey/voice_draft {"name": "…", "purpose": "support_mail", "description": "…"}` — oder `"texts": [{"name": "…", "text": "…"}]` statt einer Beschreibung — legt die Stimme als Entwurf an. Die Aktion hat einen eigenen Leitplanken-Gegenstand, `covey:voice_draft`, sodass eine Organisation sie an eine Freigabe binden oder verbieten kann, und jeder Entwurf steht in der Aufzeichnung. Freigeben, bauen oder zuweisen kann der Agent nicht: Das bleibt bei denen, die Stimmen verwalten.

## Wer was darf

Jede Rolle liest Stimmen. Anlegen, ändern, bauen, beschreiben, Proben schreiben, überarbeiten und freigeben dürfen `org_admin` und `agent_owner` — die Probe ändert nichts, kostet aber einen Modellaufruf. Dieselben beiden Rollen setzen die Stimmen-Plätze von Agenten, Abteilungen und Organisation und die Zeile einer Abteilung; alle anderen sehen sie gesperrt.
