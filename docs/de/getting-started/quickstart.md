---
slug: schnellstart
title: Schnellstart (Docker)
description: 'Covey selbst hosten: Repository klonen, Master-Key erzeugen, Sandbox-Image bauen, docker compose up — danach läuft die Plattform auf Port 8494.'
faq:
  - q: Welche Ports braucht Covey?
    a: 'Einen: **8494** für API und Oberfläche. Postgres läuft im Compose-Setup auf dem internen Netz und muss nicht nach außen. Die Sandboxen brauchen keinen eingehenden Port — sie rufen die Control Plane von sich aus.'
  - q: Warum dauert der Sandbox-Image-Build so lange?
    a: 'Weil in dem Image der Arbeitsplatz eines Agenten steckt: Claude Code, git, ripgrep, chromium für den Browser, dazu eine Node- und Java-Toolchain und Build-Werkzeuge für native npm-Pakete. Das sind einige Gigabyte, und sie werden einmal gebaut, nicht bei jedem Lauf.'
  - q: Kann ich Covey ohne den Image-Build ausprobieren?
    a: 'Ja — die Plattform startet, die Oberfläche funktioniert, Agenten und Konfigurationen lassen sich anlegen. Nur der erste Lauf scheitert dann, und zwar mit einer Meldung, die genau das sagt: beim Start im Log und in der Checkliste auf der Agenten-Übersicht.'
  - q: Was passiert, wenn ich den COVEY_MASTER_KEY verliere?
    a: Dann ist jedes hinterlegte Secret unlesbar — der Schlüssel ver- und entschlüsselt sie mit AES-GCM. Es gibt keine Hintertür. Sichern Sie die `.env`, und behandeln Sie den Schlüssel wie ein Datenbank-Passwort.
  - q: Läuft Covey auf einem Raspberry Pi oder auf ARM?
    a: 'Die Binaries gibt es für linux/amd64, linux/arm64 sowie macOS in beiden Architekturen. Entscheidend ist weniger die Architektur als der Arbeitsspeicher: In jeder Sandbox laufen eine Node-Runtime und je nach Aufgabe ein chromium.'
---

# Schnellstart (Docker)

Covey läuft als einzelnes Binary neben einer Postgres-Datenbank. Ohne Go, ohne Node, ohne lokale Postgres — Docker genügt.

## Voraussetzungen

- **Docker** mit Compose-Plugin (`docker compose version` ≥ 2.x)
- **OpenSSL** für den Master-Key (auf macOS/Linux vorinstalliert)

## Installieren

```
git clone https://github.com/benjaminLedel/covey.git && cd covey
cp .env.example .env
echo "COVEY_MASTER_KEY=$(openssl rand -hex 32)" >> .env

docker build -f Dockerfile.sandbox -t covey-sandbox:latest .
docker compose up -d --build
```

Dann **http://localhost:8494** öffnen, Login `admin@covey.local` / `covey-admin` (änderbar in der `.env` über `COVEY_ADMIN_EMAIL` / `COVEY_ADMIN_PASSWORD`).

Der `COVEY_MASTER_KEY` ver- und entschlüsselt alle hinterlegten Secrets. Geht er verloren, ist jedes hinterlegte Credential unlesbar — die `.env` gehört gesichert und nicht ins Git.

## Warum der Image-Build eine eigene Zeile ist

Alles außer `docker build` dauert Sekunden. Diese Zeile baut den Container, **in dem ein Agent arbeitet** (Claude Code, chromium, eine Node- und Java-Toolchain) und braucht ein paar Minuten.

Zum Umsehen kann man sie weglassen: Die Plattform startet, die Oberfläche funktioniert, Agenten und Configs lassen sich anlegen. Erst der erste Lauf scheitert dann — und sagt auch, woran. Covey prüft das beim Start und meldet es in den **ersten Schritten** auf der Agenten-Übersicht.

## Was `docker compose` startet

- `db` — PostgreSQL mit `pgvector`.
- `bootstrap` — legt einmalig Organisation, Admin, Demo-Agent und dessen Arbeitsplatz an (idempotent) und beendet sich.
- `covey` — die Control Plane: API + Orchestrator + eingebettete Admin-UI auf Port 8494. Migrationen laufen beim Start automatisch.

Zwei Dinge tragen dabei die Sandbox-Isolation und gehen beim Anpassen leicht verloren: der **Docker-Socket** im covey-Container (er startet jede Sandbox als Geschwister-Container) und ein **Datenverzeichnis unter identischem Pfad** auf Host und im Container (die Agenten-Homes werden vom Host-Daemon gemountet).

## Der erste Agent

Nach dem Login führt die **Einrichtung** durch drei Fragen, jede überspringbar: die Engine und ihr Zugang — bei Claude Code das Secret `anthropic_api_key` (API-Schlüssel) oder `claude_code_oauth_token` (Abo-Konto, Token einmalig mit `claude setup-token` erzeugen), geprüft bevor er gespeichert wird —, dann drei Sätze darüber, was Ihr Unternehmen macht, und zuletzt die **Personalabteilung**: ein Agent, dessen Aufgabe es ist, die anderen zu entwerfen.

Danach ist *Neuer Agent → Ausschreibung* der kürzeste Weg zur ersten Kollegin: in ein paar Sätzen beschreiben, was sie tun soll. Was dabei herauskommt, ist ein **Entwurf** — er arbeitet erst, wenn Sie ihn einstellen. Der Demo-Agent, der nach dem Login bereitsteht, ist bereits eingestellt und läuft, sobald der Zugang steht.

Die Checkliste **Erste Schritte** auf der Agenten-Übersicht führt den Weg zu Ende; sie liest den tatsächlichen Zustand der Organisation und verschwindet, wenn alles erledigt ist.

## Lieber das blanke Binary?

```
curl -sSL https://raw.githubusercontent.com/benjaminLedel/covey/main/installer/install.sh | sh
```

Holt das passende Binary aus dem neuesten Release und prüft die SHA-256-Summe. Die Control Plane braucht dann noch PostgreSQL (mit pgvector) und Docker für die Sandboxen; das Skript nennt die verbleibenden Schritte.

## Weiter

- [Den ersten Agenten anlegen](first-agent.md) — vom Credential zum ersten Lauf
- [Betrieb & Deployment](../operations/operations.md) — HTTPS, Sicherungen, Updates
