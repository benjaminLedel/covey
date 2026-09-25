---
slug: betrieb
title: Betrieb & Deployment
description: 'covey im Betrieb: ein Binary plus Postgres, Port 8494, Migrationen beim Start, HTTPS über Reverse-Proxy, Egress-Isolation, Sicherungen und Updates.'
faq:
  - q: Wie viel Arbeitsspeicher braucht ein covey-Server?
    a: 'Die Control Plane selbst ist genügsam — ein Go-Prozess neben Postgres. Der Bedarf entsteht in den Sandboxen: Jede läuft mit einer Node-Runtime, bei Browser-Aufgaben zusätzlich mit chromium. Planen Sie nach gleichzeitig wachen Agenten, nicht nach der Anzahl angelegter.'
  - q: Kann ich covey hinter einen Reverse-Proxy stellen?
    a: Ja, das ist der vorgesehene Weg für HTTPS. Wichtig ist nur, `COVEY_PUBLIC_URL` nicht auf die öffentliche Domain zu setzen, wenn die Sandboxen sie nicht erreichen — diese Variable zeigt nach innen.
  - q: Wie aktualisiere ich ohne Datenverlust?
    a: Binary oder Image tauschen und neu starten; die Migrationen laufen beim Start und sind gegen parallele Starts abgesichert. Vorher die Datenbank sichern, den Master-Key ohnehin aufbewahren. Danach `covey config lint` laufen lassen.
  - q: Läuft covey auch ohne Internetzugang?
    a: Die Plattform ja. Die Agenten brauchen den Modell-Endpunkt — bei harter Egress-Isolation steht der Proxy davor und lässt genau die erlaubten Hosts durch. Ein selbst betriebener Einbettungsdienst hält zusätzlich die Wiki-Suche im Haus.
---

# Betrieb & Deployment

covey ist bewusst langweilig zu betreiben: ein Prozess, eine Datenbank, ein Port. Alles, was darüber hinausgeht, ist optional.

## Was läuft

- **covey** — die Control Plane, hört auf **8494** (API, Oberfläche, Daemon-WebSocket)
- **PostgreSQL** mit `pgvector` — Zustand, Queue, Gedächtnis, Secrets
- **Docker** — für die Sandboxen, gestartet über den Socket des Hosts
- optional **covey-runner** — wenn die Data Plane auf mehrere Maschinen soll

Migrationen laufen beim Start automatisch, abgesichert über ein Advisory Lock; zwei gleichzeitig startende Instanzen migrieren nicht gegeneinander.

## Die Adressen auseinanderhalten

Zwei Variablen sehen ähnlich aus und meinen Gegenteiliges:

- `COVEY_PUBLIC_URL` zeigt **nach innen** — unter dieser Adresse erreichen die **Sandboxen** die Control Plane. Steht hier die Domain der Website, wählen die Container über das offene Netz zurück und scheitern an der Egress-Allowlist.
- `COVEY_SITE_URL` zeigt **nach außen** — die kopierbaren Webhook- und Trigger-URLs, die Adresse im herunterladbaren Skill, die Links in den Mails der Installation. Leer lassen ist der Normalfall; der Server leitet sie aus dem Request ab. Die Einstellung `site.url` (*Plattform → Einstellungen*) sagt dasselbe aus dem Produkt heraus und hat Vorrang — die Benachrichtigungsmails werden aus einer Schleife ohne Request verschickt und brauchen eine der beiden.

Beim Start warnt covey, wenn diese beiden Rollen vertauscht aussehen.

## App-Links

Nur für eine Installation, die die covey-App unter ihrem eigenen Host ausliefert — app.covey.work tut das, eine selbst betriebene Instanz normalerweise nicht. Gesetzt, liefert covey `/.well-known/apple-app-site-association` und `/.well-known/assetlinks.json` aus, und iOS und Android öffnen `/pair` (den Kopplungs-QR-Code) und `/team/<id>` (einen Verlauf) in der App statt im Browser. Ungesetzt antworten beide Dateien mit 404, und nichts ändert sich.

- `COVEY_APPLE_APP_ID` — `<TEAM-ID>.<Bundle-ID>`, z. B. `ABCDE12345.work.covey.coveyMobile`
- `COVEY_ANDROID_CERT_SHA256` — die SHA-256-Fingerabdrücke der Signaturzertifikate der App, kommagetrennt
- `COVEY_ANDROID_APP_PACKAGE` — der Paketname; Vorgabe `work.covey.covey_mobile`

## Sprachmodell

Diktat und Besprechungsnotizen der App laufen mit whisper.cpp auf dem Telefon; das Modell kommt von der Instanz. Beim Start holt covey es einmal von einer festgelegten Adresse (Hugging Face, `ggerganov/whisper.cpp`), prüft es gegen eine festgelegte SHA-256 und legt es unter `COVEY_DATA_DIR/models` ab. Die App lädt es beim ersten Diktat von `/api/v1/speech/model/file`.

- `COVEY_SPEECH_MODEL` — die Vorgabe: `tiny` (78 MB), `base` (148 MB, Vorgabe), `small` (488 MB), `medium` (1,5 GB) oder `off`
- `COVEY_SPEECH_MODELS` — die weiteren Modelle, die man in den Einstellungen der App wählen kann, kommagetrennt; Vorgabe `tiny,base,small`. Jedes wird geholt, wenn es zum ersten Mal jemand wählt. `medium` erkennt am besten, ist auf dem Prozessor eines Telefons aber langsam.

Ohne Internetzugang legen Sie `ggml-<Name>.bin` selbst nach `COVEY_DATA_DIR/models`; die Datei wird genauso geprüft, und eine mit anderer Prüfsumme wird entfernt statt ausgeliefert.

## HTTPS

Ein Reverse-Proxy davor, TLS dort terminieren, `COVEY_PUBLIC_URL` beziehungsweise `COVEY_SITE_URL` passend setzen. Das sichere Cookie schaltet sich dann von selbst ein. Für die Datenbank `sslmode=require` oder höher.

## Egress-Isolation

Zwei Stufen. **Kooperativ**: Der Datenverkehr der Sandbox läuft über einen Proxy, der die Allowlist durchsetzt. **Hart** (`COVEY_EGRESS_ISOLATION=network`): Die Sandbox hängt in einem internen Netz ohne Internet, und der Proxy-Container ist der einzige Weg hinaus — nicht mehr umgehbar. Für die harte Stufe wird ein zweites Image gebaut.

## Sicherungen

Zwei Dinge: die Postgres-Datenbank und der `COVEY_MASTER_KEY`. Ohne den Schlüssel ist ein Datenbank-Backup zwar vollständig, aber jedes Secret darin unlesbar. Die Homes der Agenten (`COVEY_DATA_DIR`) sind nützlich, aber wiederherstellbar — sie enthalten Arbeitsstände, keinen unersetzlichen Zustand.

## Updates

Neues Binary oder neues Image, Prozess neu starten; die Migrationen laufen mit. Nach einem Update lohnt ein Blick mit

```
covey config lint
```

Das ändert nichts, sondern meldet Konfigurationen, die mit der neuen Fassung nicht mehr gut zusammengehen: zu kurze Heartbeat-Takte, blockierende Aufgaben an Systemen ohne Webhook, Boards mit Spalten, die Aufgaben statt Zuständen benennen, häufige Turn-Abbrüche. Der Exit-Code ist 1, wenn es Befunde gibt — ein Upgrade-Skript kann darauf reagieren.

## Beobachten

`covey version` beantwortet, welcher Stand läuft; dieselbe Angabe steht in der Startzeile und unten in der Oberfläche. Kosten, Tokens und Läufe stehen in der Oberfläche je Agent und je Modell. Das Request-Log zeigt die HTTP-Ränder — was hereinkam, was hinausging.

## Weiter

- [Schnellstart (Docker)](../getting-started/quickstart.md) — die Installation
- [Architektur-Überblick](../introduction/architecture.md) — warum die Sandbox ein Geschwister-Container ist
- [Guard-Rails & Kontrolle](../concepts/guard-rails.md) — Not-Aus und Aufzeichnung
