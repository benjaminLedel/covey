---
slug: architektur
title: Architektur-Überblick
description: Control Plane als einzelnes Go-Binary, Data Plane aus ephemeren Docker-Sandboxen, dazwischen ein WebSocket-Protokoll. Postgres trägt Zustand, Queue und Vektorsuche.
faq:
  - q: Braucht covey Redis, RabbitMQ oder Kafka?
    a: Nein. Die Warteschlange ist `SELECT … FOR UPDATE SKIP LOCKED`, die Benachrichtigung `LISTEN/NOTIFY`, die Vektorsuche `pgvector` — alles in derselben Postgres-Instanz. Ein Broker wäre ein zweiter Dienst mit eigenem Ausfallverhalten für einen Nutzen, den die Datenbank schon hat.
  - q: Kann ich eine andere Runtime als Claude Code verwenden?
    a: 'Vom Aufbau her ja: Runtimes hängen als Plugins an einer Registry und sprechen über einen dünnen Adapter das Daemon-Protokoll. Ausgeliefert ist derzeit Claude Code headless, dazu eine Mock-Runtime für Tests und Demos ohne Modellkosten.'
  - q: Warum läuft die Sandbox in Docker und nicht als Unterprozess?
    a: Weil Prozess-Isolation keine ist, sobald ein Agent Werkzeuge ausführt. Der Container gibt Namespaces, ein eigenes Dateisystem und einen kontrollierbaren Netzausgang. Einen local-Provider gab es früher; er ist entfernt, damit niemand versehentlich ohne Isolation produktiv geht.
  - q: Wie skaliert covey über eine Maschine hinaus?
    a: 'Über Runner: eigenständige Prozesse, die sich bei der Control Plane registrieren und Sandboxen ausführen. Die Control Plane bleibt eine, der Zustand bleibt in Postgres, die Homes liegen zentral.'
---

# Architektur-Überblick

covey zerfällt in zwei Hälften, die sich sehr unterschiedlich verhalten: eine **Control Plane**, die den Zustand führt und immer läuft, und eine **Data Plane** aus Sandboxen, die entstehen und wieder verschwinden. Dazwischen liegt ein Protokoll, und genau an dieser Naht wird die Plattform gegenüber der Runtime austauschbar.

## Control Plane — ein Prozess, ein Binary

Ein einzelnes Go-Binary vereint Scheduler und Dispatcher, Agent-Registry und Org-Chart, Backlog-Store, Identitäts- und Secrets-Broker, Guard-Rail-Engine, Observability und die HTTP-API. Die React-Oberfläche ist über `//go:embed` einkompiliert, die SQL-Migrationen ebenso; beim Start migriert die Instanz sich selbst, abgesichert über ein `pg_advisory_lock`.

Der Grund für den einen Prozess ist nicht Sparsamkeit, sondern Betreibbarkeit: Wer covey installiert, soll eine Datei kopieren und sie starten. Alles, was sonst als zweiter Dienst danebenstünde — Queue, Pub/Sub, Vektorindex —, liegt in Postgres.

## Data Plane — dumm und ersetzbar

Pro Wecken startet die Control Plane einen Container aus dem Sandbox-Image und darin `coveyd`. Der Container erbt nichts von der Umgebung des Hosts: Was er sieht, hat die Control Plane hineingelegt. Persistent ist allein das Home des Agenten, das als Volume eingehängt wird.

Daraus folgt die Betriebsregel: Geht eine Sandbox verloren, wird sie aus Config und Home neu gebaut. Ein Agent, der beim nächsten Lauf einen frischen Container bekommt, verliert nichts, was ihm gehört.

## Daemon-Protokoll — die stabile Naht

Zwischen Control Plane und Sandbox läuft ein bidirektionales Protokoll über WebSocket. Es transportiert den Auftrag hinein, Werkzeugaufrufe und Ergebnisse heraus, Freigabe-Anfragen in beide Richtungen und am Ende Tokens und Kosten.

Die Runtime dahinter ist austauschbar, weil sie das Protokoll nicht kennt — dazwischen sitzt ein dünner Adapter. Der erste ist Claude Code headless (`claude -p`); ein weiterer ändert an der Plattform nichts, solange er dieselben Nachrichten spricht.

## Warum die Sandbox ein Geschwister-Container ist

Die Control Plane startet Sandboxen über den Docker-Socket des Hosts — sie laufen also **neben** ihr, nicht in ihr. Das hat eine praktische Folge, über die viele beim Anpassen des Compose-Setups stolpern: Der `-v`-Pfad eines Agenten-Homes wird vom Docker-Daemon des **Hosts** aufgelöst. Deshalb muss das Datenverzeichnis auf Host und Container denselben Pfad haben; ein benanntes Volume ginge ins Leere.

## Postgres als Anker

Ein Datenspeicher trägt fast alles:

- **Zustand** — Agenten, Organisationen, Rollen, Konfigurationsversionen
- **Queue** — `SELECT … FOR UPDATE SKIP LOCKED` statt eines Brokers
- **Pub/Sub** — `LISTEN/NOTIFY` weckt den Dispatcher, ohne dass jemand pollt
- **Gedächtnis** — `pgvector` für die Suche über das Wiki der Agenten
- **Secrets** — AES-GCM-verschlüsselte Spalten, gebunden an die Organisation

## Batteries included, but swappable

Jede Fähigkeit hat eine eingebaute, datenbankgestützte Voreinstellung und ein schmales Interface für einen externen Anbieter: `IdentityProvider` builtin (JWT/Argon2id) ↔ OIDC, `SecretStore` builtin (AES-GCM) ↔ Vault. Der Normalbetrieb läuft mit `builtin` überall — Keycloak, Vault und Redis sind Optionen, nie Voraussetzungen.

## Verteilte Data Plane

Reicht eine Maschine nicht mehr, registrieren sich **Runner** bei der Control Plane und übernehmen Sandboxen; die Homes liegen dann zentral. Der Aufbau bleibt derselbe, nur der Ort der Container ändert sich.

## Weiter

- [Betrieb & Deployment](../operations/operations.md) — Ports, Umgebungsvariablen, Egress, Updates
- [Identität & Secrets](../concepts/identity-and-secrets.md) — was in die Sandbox darf und was nicht
- [Zielsysteme & Plugins](../integrations/target-systems.md) — wie ein Agent an fremde Systeme kommt
