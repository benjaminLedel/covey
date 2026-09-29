---
slug: stimmen
title: Stimmen
description: 'Wie ein covey-Agent schreibt wie ein Mensch: eine Stimme, aus Texten gemessen, in Worten beschrieben oder von einem Agenten im Chat entworfen — Karte, Passagen, Chat-Ton und, wenn gemessen, ein Profil, an dem die Stilprüfung misst.'
faq:
  - q: Wir haben keine Texte zum Hochladen. Bekommt ein Agent trotzdem eine Stimme?
    a: Ja. Beschreiben Sie in eigenen Worten, wie Sie schreiben; ein Modellaufruf schreibt die Karte, fünf bis acht Beispielpassagen und einen vorgeschlagenen Chat-Ton. Eine solche Stimme ist beschrieben, nicht gemessen — die Stilprüfung misst nicht an ihr, bis Texte hinzukommen und sie gebaut wird.
  - q: Wirkt etwas, das ein Modell geschrieben hat, ohne dass es jemand gelesen hat?
    a: Nein. Die Karte und die Passagen einer beschriebenen Stimme sind Entwürfe, bis ein Mensch sie auf der Seite der Stimmen freigibt, und ein Agent lässt sich nur auf eine Stimme setzen, an der etwas freigegeben oder gemessen ist.
  - q: Was kostet es, eine Stimme zu bauen?
    a: Texte messen kostet nichts. Die Karte, eine Stimme aus einer Beschreibung, eine Probe und eine Überarbeitung sind je ein begrenzter Modellaufruf mit dem Zugang der Organisation; die Seite sagt das neben jedem Knopf.
---

# Stimmen

Ein Stilprofil sagt, wie *weit* ein Text von einem Korpus entfernt ist; es kann ihn nicht wie den Autor des Korpus klingen lassen. Eine **Stimme** kann das. Sie gehört der Organisation, wird Agenten in ihren Einstellungen zugewiesen, und die Zuweisung schreibt die `TONE.md` des Agenten — eine Konfigurationsversion wie jede andere.

Eine Stimme trägt:

- eine **Karte** — 250 bis 400 Wörter darüber, wie der Autor einsteigt, argumentiert, Fakten einbringt, Sätze baut und was er nie tut;
- **Passagen** — fünf bis acht Absätze, die die Hand zeigen; sie stehen im Prompt;
- einen **Chat-Ton** — Anrede, Ton und Emoji im Team-Chat;
- wenn sie aus Texten gemessen ist: ein **Profil**, an dem die Stilprüfung jeden ausgehenden Text misst, und einen **Kontrast** zu KI-Text, wenn eine Referenz hochgeladen wurde.

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

Jede Rolle liest Stimmen. Anlegen, ändern, bauen, beschreiben, Proben schreiben, überarbeiten und freigeben dürfen `org_admin` und `agent_owner` — die Probe ändert nichts, kostet aber einen Modellaufruf.
