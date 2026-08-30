---
slug: was-ist-covey
title: Was ist Covey?
description: 'Covey führt KI-Agenten wie Mitarbeiter: eigene Identität, isolierte Sandbox, gebrokerte Zugänge, Backlog und Org-Chart. Ein Go-Binary, eine Postgres-Datenbank, AGPL-3.0.'
faq:
  - q: Braucht Covey eine Cloud oder ein Konto bei Ihnen?
    a: 'Nein. Covey wird selbst gehostet: ein Binary, eine Postgres-Datenbank, Docker für die Sandboxen. Ein Konto brauchen Sie nur beim Modell-Anbieter — Anthropic-API-Schlüssel oder Abo-Token, beides hinterlegen Sie in Ihrer eigenen Instanz.'
  - q: Was unterscheidet Covey von einem Agenten-Framework wie LangChain oder CrewAI?
    a: Ein Framework baut den Agenten, Covey betreibt ihn. Identität, Zugänge, Backlog, Guard-Rails, Recording und Kosten sind Betriebsfragen und liegen außerhalb der Runtime — deshalb bleibt die Runtime austauschbar. Der erste Adapter ist Claude Code headless.
  - q: Welche Lizenz hat Covey?
    a: 'AGPL-3.0. Selbst betreiben, ändern und weitergeben ist erlaubt. Die eine Pflicht, die praktisch zählt: Wer eine geänderte Fassung über ein Netz anderen anbietet, muss diesen Nutzern den geänderten Quelltext zu denselben Bedingungen zugänglich machen. Covey im eigenen Haus zu betreiben löst nichts davon aus.'
  - q: Kann ein Agent auch ohne Zielsystem arbeiten?
    a: Ja. Nach der Installation steht ein Demo-Agent bereit, der ohne angebundenes System auskommt — er arbeitet am Aufgabentext und schreibt sein Ergebnis zurück. Das reicht, um einen vollständigen Lauf mit Recording und Kosten zu sehen.
---

# Was ist Covey?

Covey führt KI-Agenten wie Mitarbeiter: Jeder Agent hat eine Identität, einen isolierten Arbeitsplatz, gebrokerte Zugänge zu Zielsystemen, ein Backlog und einen Platz im Org-Chart. Die Leitmetapher ist die IT- und HR-Abteilung für KI-Agenten — und sie ist wörtlich gemeint, bis in den Aufbau der Software hinein.

Technisch ist Covey **ein Go-Binary neben einer Postgres-Datenbank**. Die Admin-Oberfläche ist einkompiliert, die Migrationen auch. Kein nginx davor, kein separates Frontend-Hosting, kein Message-Broker daneben. Die Lizenz ist AGPL-3.0; man betreibt es selbst, auf der eigenen Maschine, mit den eigenen Schlüsseln.

## Wofür Covey gebaut ist

Für den Fall, dass ein Unternehmen mehr als einen Agenten betreibt und die Frage aufkommt, wer eigentlich zugesehen hat. Also: Wer hat dem Agenten diesen Zugang gegeben, was hat er damit letzte Woche getan, was hat es gekostet, und wer hält ihn an, wenn er Unsinn macht.

Das ist der Unterschied zu einem Agenten-Framework. Ein Framework hilft beim Bauen des Agenten. Covey verwaltet den fertigen Agenten im Betrieb — und ist deshalb gegenüber dem Framework austauschbar: die Runtime hängt an einem dünnen Adapter, der erste ist [Claude Code headless](architecture.md).

## Die Organisation ist die Einheit, nicht der Nutzer

Covey gehört keiner Person. Die Einheit ist die Organisation, und daran hängt alles Weitere: Agenten sind organisationseigene Ressourcen, ihre Secrets liegen im Tresor der Organisation, ihre Kosten laufen auf deren Konto, und mehrere Menschen mit verschiedenen Rollen schauen auf dieselbe Belegschaft — IT, Team-Lead, Security, Audit, Controlling.

Das ist die tragende Unterscheidung zu Single-User-Apps, die einen persönlichen „KI-Mitarbeiter" versprechen. Deren Modell endet an der Stelle, an der die zweite Person mitreden muss.

## Entsprechungen im Unternehmen

Fast jede Komponente hat ein Gegenstück, das jeder aus dem Arbeitsalltag kennt. Wer die linke Spalte kennt, kann die rechte lesen:

- Identität / Active Directory → Agent-Identität, optional mit eigener E-Mail-Adresse
- Arbeitsplatz / PC → isolierte Sandbox mit persistentem `/home`
- Onboarding / Rollenbeschreibung → `SOUL.md`, versioniert wie Code
- Passwort-Tresor / PAM → Secrets-Broker, kurzlebige Tokens zur Laufzeit
- Aufgabenliste / Ticket → Backlog als eigenständiges Objekt
- Betriebshandbuch / Compliance → zentrale Guard-Rails, außerhalb des Prompts erzwungen
- SIEM / EDR → Session-Recording, Kosten je Lauf, Not-Aus für die ganze Organisation

## Was Covey nicht ist

Kein Modell und kein Modell-Anbieter — Covey bringt das eigene Konto mit (Anthropic-API-Schlüssel oder Abo-Token). Kein Chat-Fenster; die Arbeit kommt aus einem Backlog, einem Webhook oder einem Heartbeat, nicht aus einer Unterhaltung. Und keine Cloud: Es gibt eine öffentliche Instanz zum Anschauen, aber der Normalfall ist die eigene.

## Weiter

- [Kernkonzepte](core-concepts.md) — die Begriffe, die überall wiederkehren
- [Architektur-Überblick](architecture.md) — Control Plane, Data Plane, Daemon-Protokoll
- [Schnellstart (Docker)](../getting-started/quickstart.md) — die Installation in fünf Zeilen
