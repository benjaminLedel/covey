---
slug: ersten-agenten
title: Den ersten Agenten anlegen
description: 'In fünf Schritten vom leeren Covey zum arbeitenden KI-Agenten: einrichten, ausschreiben, Entwurf durchsehen und einstellen, Aufgabe stellen, Lauf im Recording ansehen.'
faq:
  - q: Wie viel kostet ein Agentenlauf?
    a: So viel wie die Tokens, die das Modell dabei verbraucht — Covey selbst rechnet nichts ab. Jeder Lauf wird mit Tokens und Kosten aufgezeichnet, aufgeschlüsselt nach Agent und Modell. Pro Agent lässt sich ein Budget setzen; ist es erreicht, arbeitet er nicht weiter.
  - q: Was gehört in die SOUL.md und was in die PLAYBOOKS.md?
    a: 'In die `SOUL.md` gehört, wer der Agent ist: Rolle, Zuständigkeit, Ton, Grenzen. In die `PLAYBOOKS.md` gehört, wie er vorgeht: die Schrittfolge für einen wiederkehrenden Fall. Faustregel — die SOUL ändert sich selten, ein Playbook ändert sich, sobald sich der Ablauf ändert.'
  - q: Kann ich einen Agenten aus einem anderen Covey übernehmen?
    a: Ja. Die Konfiguration lässt sich als Bündel exportieren und in einer anderen Instanz importieren — mitsamt Playbooks, Zugriffswünschen und Heartbeat. Die Secrets bleiben, wo sie waren; sie gehören der Organisation, nicht dem Bündel.
  - q: Warum tut mein Agent nichts, obwohl eine Aufgabe im Backlog liegt?
    a: 'Die drei häufigsten Gründe: Es liegt kein Credential vor (dann sagt es die Checkliste), das Sandbox-Image fehlt (dann sagt es der Start-Log), oder der Agent wurde per Not-Aus angehalten. Im Recording steht in allen drei Fällen, woran es lag.'
---

# Den ersten Agenten anlegen

Einen Agenten anzulegen ist ein Onboarding: Identität, Rolle, Zugänge, Vorgesetzter. Der Unterschied zum Menschen ist, dass die Rollenbeschreibung diesmal gelesen wird — sie ist der Prompt.

Fünf Schritte, und die Checkliste **Erste Schritte** auf der Agenten-Übersicht hakt sie mit, während Sie sie tun. Sie liest den echten Zustand Ihrer Organisation, nicht Ihren Fortschritt in einer Tour — was einmal steht, steht.

## 1. Einrichtung

Die Seite *Einrichtung* stellt drei Fragen, jede überspringbar:

- **Motor und Zugang.** Welche Engine Ihre Agenten denken lässt, und das Credential dazu — bei Claude Code ein API-Schlüssel (Abrechnung nach Verbrauch) oder ein Abo-Token (einmalig im Terminal mit `claude setup-token` erzeugt), bei Codex ein API-Schlüssel oder der Inhalt von `~/.codex/auth.json`. Der Wert wird gegen den Anbieter geprüft, **bevor** er gespeichert wird — besser hier als eine Stunde später im Lauf eines Agenten. Der Arbeitsplatz entsteht dabei von selbst; ein zweiter Token wird automatisch zu weiterer Kapazität.
- **Was Ihr Unternehmen macht.** Drei bis fünf Sätze. Sie bleiben an der Organisation und gehen von da an in jede Ausschreibung, in die Konfiguration neu entworfener Agenten und in den Config-Assistenten ein.
- **Ihre Personalabteilung.** Ein Agent, dessen Aufgabe es ist, die anderen zu entwerfen.

Die Seite läuft für sich, ohne Navigation daneben, und verschwindet aus dem Menü, sobald die drei Karten stehen. Alles davon geht auch später von Hand (Secrets, Runtimes, Vorlagenbibliothek). Was die Einrichtung kauft, ist die Reihenfolge: ohne Zugang kann nichts laufen, was die Oberfläche anbietet.

## 2. Die Ausschreibung

*Neuer Agent* bietet vier Wege. Der kürzeste ist die **Ausschreibung**: ein Freitextfeld — was soll die neue Kollegin tun? — plus Abteilung und Vorgesetzter. Daraus wird ein Auftrag an die Personalabteilung, und die Oberfläche zeigt danach das laufende Einstellungsgespräch. Ist die Beschreibung zu dünn, fragt der Agent zurück, statt zu raten.

Daneben bleiben die anderen drei Wege: eine **Vorlage** aus der Bibliothek, das **manuelle** Formular für den, der genau weiß, was er will, und der **Bundle-Import** aus `examples/` (Coding-Agent, QA-Agent, Web-Rechercheur, Log-Triage).

## 3. Durchsehen und einstellen

Was dabei herauskommt, ist ein **Entwurf** — bei jedem der vier Wege. Er steht auf der Agenten-Übersicht im Feld *Bewerbungen*: angelegt, ansehbar, änderbar, und er arbeitet nicht. Kein Dispatch, kein Heartbeat, kein scharfer Webhook, keine Sandbox, keine Kosten.

Sehen Sie sich seine Konfiguration an — das ist der eigentliche Sinn des Zustands:

- `SOUL.md` — die Rollenbeschreibung: Wer bist du, wofür bist du zuständig, in welchem Ton antwortest du, wo hörst du auf. Sie wird bei jedem Lauf in den Prompt kompiliert, ist versioniert und lässt sich mit Verlauf zurückrollen.
- `PLAYBOOKS.md` — Arbeitsabläufe, Schritt für Schritt
- `CAPABILITIES.md` — wofür der Agent zuständig ist und wofür nicht
- `ACCESS.md` — die Zugänge, in der Form `- system: zammad scope: read,write,comment`
- `HEARTBEAT.md` — wiederkehrende Aufgaben, etwa `- alle: 30m titel: Posteingang aufgabe: Neue Tickets sichten.`
- `ORG.md` — der Vorgesetzte, an den eskaliert wird

Ein guter erster Satz in der `SOUL.md` ist konkreter, als man denkt: „Du beantwortest Fragen zum Abrechnungssystem" trägt weiter als „Du bist ein hilfreicher Assistent".

Die Entwürfe stehen auf der Agenten-Übersicht in einem eigenen Feld **Bewerbungen**, abgesetzt von der Belegschaft darunter. **Einstellen** zeigt vorher eine Zusammenfassung — Rolle, angefragte Zielsysteme mit Scopes, Vorgesetzter, Runtime, Budgetdeckel — und gibt danach die wartenden Aufgaben frei. Passt der Entwurf nicht, verwirft ihn **Ablehnen**; er hat nie gearbeitet, es gibt nichts aufzuräumen.

## 4. Erste Aufgabe

Im Backlog des Agenten eine Aufgabe anlegen. „Wecken" startet die Abarbeitung sofort, statt auf den nächsten Auslöser zu warten. Danach ist der Agent wieder `sleeping` — das ist der Normalzustand, und er kostet nichts.

## 5. Zusehen

Das Recording zeigt den Lauf von innen: jeden Werkzeugaufruf, jede Zielsystem-Aktion, jede Freigabe, bei Browser-Aufgaben die Screenshots, am Ende Tokens und Kosten. Wer wissen will, warum ein Agent etwas getan hat, liest hier nach und nicht im Modell.

## Danach: Zuständigkeit statt Aufgaben

Ein Agent, der nur auf angelegte Aufgaben wartet, ist ein teures Kommandozeilenwerkzeug. Interessant wird er mit einer **Weckquelle**: ein Webhook aus dem Ticketsystem, ein Heartbeat im Minutentakt, eine eingehende E-Mail. Dann arbeitet er, wenn Arbeit da ist, und schläft, wenn keine da ist.

## Weiter

- [Das Agenten-Modell](../concepts/agent-model.md) — was einen Agenten technisch ausmacht
- [Zielsysteme & Plugins](../integrations/target-systems.md) — Zammad, GitLab, E-Mail, Teams anbinden
- [Guard-Rails & Kontrolle](../concepts/guard-rails.md) — Freigaben, Not-Aus, Aufzeichnung
