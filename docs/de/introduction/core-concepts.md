---
slug: kernkonzepte
title: Kernkonzepte
description: 'Das Glossar der Plattform: Agent, Control Plane, Data Plane, Runtime, Backlog, Guard-Rail, Secrets-Broker, Arbeitsplatz und Wiki-Gedächtnis — kurz erklärt.'
faq:
  - q: Was ist der Unterschied zwischen Runtime und Agent?
    a: Der Agent ist die dauerhafte Identität mit Konfiguration, Backlog und Gedächtnis. Die Runtime ist die austauschbare Maschinerie, die während eines Laufs das Modell fährt. Derselbe Agent kann auf eine andere Runtime umgestellt werden, ohne seine Geschichte zu verlieren.
  - q: Warum arbeitet ein Agent seriell und nicht parallel?
    a: Weil parallele Läufe desselben Agenten sich um dasselbe Home, dieselbe Aufgabe und denselben Zustand streiten würden. Mehr Durchsatz erreicht man über mehr Agenten, nicht über mehr Gleichzeitigkeit in einem.
  - q: Was passiert, wenn eine Sandbox abstürzt?
    a: Nichts, was sich nicht wiederherstellen ließe. Der Zustand liegt in der Control Plane, das Home auf der Platte. Die Sandbox wird beim nächsten Wecken neu gebaut; eine unterbrochene Aufgabe geht zurück in den Backlog.
---

# Kernkonzepte

Zwölf Begriffe, die in der Oberfläche, in der Spezifikation und in diesen Seiten immer wieder auftauchen. Wer sie einmal liest, versteht den Rest ohne Rückfragen.

## Agent

Eine konfigurierte, dauerhaft bestehende Entität mit Identität, Sandbox, Zugängen und Backlog — das Gegenstück zum Mitarbeiter. Ein Agent ist kein Prozess, der läuft: Der Normalzustand ist `sleeping`. Geweckt wird er von einer Aufgabe, einem Webhook oder einem Heartbeat, arbeitet seriell und schläft wieder ein. Eine eigene E-Mail-Adresse ist optional.

## Control Plane

Der zentrale, zustandsführende Dienst — ein Go-Binary: Scheduler und Dispatcher, Agent-Registry, Org-Chart, Backlog-Store, Identitäts- und Secrets-Broker, Guard-Rail-Engine, Observability. Sie kennt den Zustand jedes Agenten. **Die Control Plane ist das Produkt**; Sandboxen sind Verbrauchsmaterial, Runtimes austauschbar.

## Data Plane

Die Gesamtheit der Sandboxen, in denen tatsächlich gearbeitet wird. Sie sind bewusst dumm und ersetzbar: Geht eine verloren, wird sie aus Config und Home neu gebaut. Persistent ist nur das Home des Agenten.

## Runtime und Daemon

Die **Runtime** ist das Agenten-Framework, das die Modell-Schleife fährt — erster Adapter ist Claude Code headless über `claude -p`. Der **Daemon** (`coveyd`) ist der schlanke Prozess in der Sandbox, der das Plattform-Protokoll spricht und die Runtime startet. Der Adapter dazwischen ist dünn, damit ein Wechsel der Runtime kein Umbau der Plattform wird.

## Arbeitsplatz (Runtime-Vertrag)

Auf wessen Vertrag ein Agent arbeitet. Ein Arbeitsplatz trägt die Engine und ihre Kapazität — ein Abo-Sitz, ein API-Schlüssel, mehrere davon. Die Reihenfolge der Credentials ist die Merit Order: Bezahltes Kontingent wird vor Bezahltem-nach-Verbrauch gefahren. Bei einer einzigen Anmeldung merkt man davon nichts; die Plattform legt den Arbeitsplatz selbst an.

## Backlog

Die persistente Aufgabenliste eines Agenten, als eigenständiges Objekt und nicht als Chat-Verlauf. Zustände: `open`, `in_progress`, `blocked`, `done`, `failed`, `cancelled`. `blocked` ist der interessante: Der Agent wartet auf eine Antwort — von einem Menschen oder aus einem Zielsystem — und wird geweckt, wenn sie eintrifft.

## Guard-Rail

Eine zentral definierte Grenze für Agentenverhalten: Freigabepflicht für eine Aktion, verbotenes System, gesperrtes Tool, Egress-Regel. Erzwungen wird sie **außerhalb der Runtime** und fail-closed — ein Agent kann sie nicht wegreden, weil sie nicht in seinem Prompt steht.

## Secrets-Broker

Der Dienst, der Zugänge zur Laufzeit ausstellt, kurzlebig und gescopt. Langlebige Secrets kommen nicht in die Sandbox. Gespeichert wird mit AES-GCM in Postgres-Spalten, verschlüsselt mit dem `COVEY_MASTER_KEY` der Instanz.

## Zielsystem

Ein angebundenes Fremdsystem — Zammad, Salesforce, Jira, Confluence, GitHub, GitLab, Microsoft Teams, SharePoint, Nextcloud, E-Mail, ein Headless-Browser, ein MCP-Server. Jedes ist ein Plugin — kompiliert, als Manifest, als WebAssembly-Modul oder als MCP-Server; der Kern kennt keine Sonderfälle. Aktionen laufen über einen Proxy, damit Guard-Rails und Aufzeichnung greifen.

## Wiki-Gedächtnis

Was der Agent behält: verlinkte Markdown-Seiten mit einem `pgvector`-Index, nicht eine Halde flacher Schnipsel. Lesbar und von Hand korrigierbar — man kann einem Agenten etwas beibringen und ihn gezielt etwas vergessen lassen.

## Config-as-Code

Das Verhalten eines Agenten steht in Dateien: `SOUL.md` (Rolle und Ton), `PLAYBOOKS.md` (Arbeitsabläufe), `CAPABILITIES.md` (Zuständigkeit), `ACCESS.md` (Zugänge), `HEARTBEAT.md` (wiederkehrende Aufgaben), `ORG.md` (Vorgesetzte). Versioniert, mit Verlauf, änderbar über die Oberfläche oder die API.

## Recording und Not-Aus

Jeder Lauf wird aufgezeichnet: Werkzeugaufrufe, Zielsystem-Aktionen, Freigaben, Screenshots des Browsers, Tokens und Kosten. Der Not-Aus hält einen Agenten an — oder die gesamte Belegschaft der Organisation auf einen Griff.

## Weiter

- [Architektur-Überblick](architecture.md) — wie diese Teile zusammenhängen
- [Das Agenten-Modell](../concepts/agent-model.md) — was einen Agenten ausmacht
- [Backlog & Lifecycle](../concepts/backlog-and-lifecycle.md) — die Zustände im Detail
