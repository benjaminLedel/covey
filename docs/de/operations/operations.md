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

Diktat und Besprechungsnotizen der App werden auf dem Gerät mit sherpa-onnx erkannt; das Modell kommt von der Instanz. Beim Start holt covey das Vorgabemodell einmal von festgelegten Adressen bei Hugging Face, prüft jede Datei gegen eine festgelegte SHA-256 und legt es unter `COVEY_DATA_DIR/models/<Name>/` ab. Die App lädt es beim ersten Diktat von `/api/v1/speech/model/file`.

- `COVEY_SPEECH_MODEL` — die Vorgabe: `parakeet` (NVIDIA Parakeet TDT 0.6B v3, 670 MB, CC-BY-4.0: 25 europäische Sprachen), `sensevoice` (FunASR SenseVoice Small, 240 MB, FunASR-Modelllizenz: Chinesisch, Kantonesisch, Japanisch, Koreanisch, Englisch) oder `off`
- `COVEY_SPEECH_MODELS` — die weiteren Modelle, die man in den Einstellungen der App wählen kann, kommagetrennt; Vorgabe `parakeet,sensevoice`. Jedes wird geholt, wenn es zum ersten Mal jemand wählt. Ist die App auf Chinesisch, Japanisch oder Koreanisch eingestellt und kein Modell gewählt, nimmt sie `sensevoice`.

Beide Modelle erkennen die Sprache selbst. Neben dem Sprachmodell bietet die Instanz zwei kleine an, die die App holt, wenn sie sie braucht, beide aus den Releases von sherpa-onnx auf GitHub und genauso festgelegt: das Sprechermodell, das die Stimmen einer Besprechung auseinanderhält (`titanet`, 40 MB), und der Sprachdetektor von Silero, der die Redebeiträge eines Anrufs schneidet (`silero`, 643 KB, MIT). Ohne Internetzugang legen Sie die Dateien des Modells selbst nach `COVEY_DATA_DIR/models/<Name>/`; sie werden genauso geprüft, und eine Datei mit anderer Prüfsumme wird entfernt statt ausgeliefert.

## Stimmen-Anbieter

Ein Anruf spricht die Antworten eines Agenten über den Stimmen-Anbieter der Organisation, die einzige Quelle der Stimmen von covey: einen beliebigen OpenAI-kompatiblen Sprachserver (`/v1/audio/speech` und `/v1/audio/transcriptions`, wenn dort auch erkannt werden soll). Eingerichtet wird er unter **Administration → Stimmen-Anbieter**: Basis-URL, Standardmodell und -stimme und der Schlüssel, der als Secret `voice_provider_key` der Organisation liegt und nie wieder angezeigt wird. **Testen** lässt den Anbieter einen Satz mit den gespeicherten Einstellungen sprechen und sagt, ob es geklappt hat; kein Ton wird aufbewahrt. Eine Organisation, die für ihre Engine bereits ein educa-AI-Token hält (`educa_seat_token` oder `educa_api_token`), spricht ohne diese Einstellung über educa AI, unter `COVEY_EDUCA_BASE_URL` in der Umgebung der Steuerebene oder beim gehosteten `https://api.educaai.de`. Ohne beides, und solange der Anbieter nicht erreichbar ist, spricht der Mac mit seiner eigenen Sprachausgabe, und der Anruf sagt das. Die Apps erreichen den Anbieter nie; die Steuerebene fragt ihn für sie an, 30 Anfragen je Minute und Platz. Anrufe beim Anbieter erkennen ist ein eigener Schalter, standardmäßig aus: eingeschaltet verlässt der Ton jedes Redebeitrags das Gerät zu diesem Server.

## Push-Mitteilungen

Jede neue Nachricht in einem Gespräch wird dessen Personen gemeldet — nicht dem, der sie geschrieben hat, nicht dem, der das Gespräch stumm geschaltet hat, und nicht dem, der schon darüber hinaus gelesen hat. Dazu gehört, was ein Agent antwortet, und was eine Aufgabe, die in diesem Gespräch entstand, zurückmeldet: ihr Ergebnis, ihren Fehler, ihre Rückfrage. Eine Aufgabe von anderswo (aus dem Backlog, von einem Webhook, aus einem Heartbeat) meldet nichts, außer einer Rückfrage, an der sie hängt: Die geht in das direkte Gespräch des Agenten mit dem Menschen, an den er berichtet, und wird diesem gemeldet. Die iPhone- und die Android-App bekommen die Mitteilungen über Firebase Cloud Messaging, das die eines iPhones an Apple weitergibt; die Mac-App zeigt sie selbst an, solange sie läuft. Die Windows-App bekommt keine. Woher die Apps kommen und wie man sie koppelt: [Die covey-App](apps.md).

Google stellt der App nur zu, wer ein Dienstkonto des Firebase-Projekts der App hat, und Firebase erreicht das iPhone nur mit dem APNs-Schlüssel, den der Herausgeber der App in dieses Projekt hochgeladen hat (Firebase-Konsole, Projekteinstellungen, Cloud Messaging, Apple-App-Konfiguration). Die Instanz selbst braucht keinen Apple-Schlüssel. Eingestellt wird das unter **Verwaltung → Plattform**, Reiter **Push**, von einer Administratorin der Installation, und eine Änderung gilt nach wenigen Sekunden, ohne Neustart:

- **Direkt**, mit eigenem Dienstkonto: Sein JSON-Schlüssel (Firebase-Konsole, Projekteinstellungen, Dienstkonten) wird auf dieser Seite hochgeladen, geprüft und mit dem Master-Key versiegelt gespeichert; die Seite nennt Projekt und Konto, nie den Schlüssel. Das Hochladen stellt den Modus nicht von selbst um — **Direkt** wählen und speichern. Das geht nur für Apps, die mit der `google-services.json` und der `GoogleService-Info.plist` dieses Projekts gebaut sind. **Prüfen** holt mit dem gespeicherten Konto ein Zugriffstoken bei Google; das belegt das Konto, nicht den APNs-Schlüssel, den erst die erste Mitteilung an ein iPhone belegt.
- **Über ein Relay**, ohne Dienstkonto: Das Relay (Vorgabe `https://app.covey.work`, das das Dienstkonto der App-Builds des Projekts hat) nimmt die Mitteilung an und gibt sie an Firebase weiter. In diesem Modus startet eine Installation, an der nichts eingestellt ist. Eine Instanz, die direkt schickt, wird mit **Für andere Installationen weiterleiten** selbst zu so einem Relay; sie nimmt nur die festen Felder einer Mitteilung an, in der Länge begrenzt und je Adresse gedrosselt.
- **Aus**: Es geht nichts hinaus; die Apps zeigen Neues, wenn sie geöffnet werden.

Für eine Installation, die als Code konfiguriert ist, gibt die Umgebung die Vorgaben, und ein auf der Seite gesetzter Wert hat Vorrang: `COVEY_FCM_CREDENTIALS_FILE` (das JSON des Dienstkontos; damit ist die Vorgabe direkt), `COVEY_PUSH_RELAY` (das Relay; `off` schaltet Push ab, solange kein Dienstkonto da ist), `COVEY_PUSH_RELAY_ACCEPT=true` (für andere weiterleiten). Dieselben Schlüssel sind Einstellungen (`push.mode`, `push.relay_url`, `push.relay_accept`, `push.fcm_credentials`) und lassen sich mit `covey settings` setzen.

Von Haus aus sagt eine Mitteilung nur, wer was getan hat („Bea hat eine Rückfrage“), in der Sprache des Geräts, und vom Inhalt verlässt nichts die Instanz: Unterwegs sind das Token des Geräts, der Name des Agenten und die Art des Ereignisses, die Zahl des Ungelesenen, die ID des Agenten und der gewählte Ton. Unter **Verwaltung → Administration**, Reiter **Profil**, Karte **Push-Mitteilungen**, kann eine Organisation die erste Zeile des Gesagten mitschicken lassen. Diese geht dann über Google, Apple und gegebenenfalls über das Relay.

## HTTPS

Ein Reverse-Proxy davor, TLS dort terminieren, `COVEY_PUBLIC_URL` beziehungsweise `COVEY_SITE_URL` passend setzen. Das sichere Cookie schaltet sich dann von selbst ein. Für die Datenbank `sslmode=require` oder höher.

## Egress-Isolation

Mit dem Docker-Provider von Haus aus an: Eine Sandbox erreicht nur die Hosts auf der Allowlist ihres Agenten — die Basisliste der Organisation (vorbelegt mit `api.anthropic.com`), die zugewiesenen Vorlagen und die eigenen Hosts des Agenten. `COVEY_EGRESS_ENFORCE=false` schaltet das ab; die Listen bleiben dann erhalten, werden aber nicht angewandt, und `covey serve` und `covey doctor` sagen das. Ein Provider, der nicht durchsetzen kann, sagt dasselbe, statt offen zu laufen. `GET /api/v1/egress` beantwortet mit `enforced`, ob es auf dieser Instanz greift.

Zwei Stufen. **Kooperativ** (die Vorgabe): Der Datenverkehr der Sandbox läuft über einen Proxy, der die Allowlist durchsetzt. **Hart** (`COVEY_EGRESS_ISOLATION=network`): Die Sandbox hängt in einem internen Netz ohne Internet, und der Proxy-Container ist der einzige Weg hinaus — nicht mehr umgehbar. Für die harte Stufe wird ein zweites Image gebaut.

Der kooperative Proxy läuft im covey-Prozess. Läuft covey selbst in einem Container, erreichen die Sandboxen ihn nur über einen veröffentlichten Port: `COVEY_EGRESS_LISTEN_ADDR` legt seine Adresse fest, und der Host muss denselben Port veröffentlichen. Die Compose-Dateien tun beides (`:8495`, `8495:8495`). Ohne das eigene Token einer Sandbox antwortet der Proxy mit 407.

Beim Update einer Installation, die mit offenem Egress lief: Vor dem Neustart die Allowlists prüfen (`GET /api/v1/egress`, die eigenen Hosts je Agent unter *Einstellungen → Egress* auf der Agentenseite) — Verkehr zu Hosts, die dort nicht stehen, wird danach abgewiesen. Wer das alte Verhalten behalten will, setzt `COVEY_EGRESS_ENFORCE=false` (in der `.env` der Compose-Installation). Einzelheiten stehen auf der englischen Seite [Upgrading](../../en/operations/upgrade.md).

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
