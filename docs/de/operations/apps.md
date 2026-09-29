---
slug: apps
title: Die covey-App
description: 'Eine App für iPhone, Android, Mac und Windows: woher jeder Build kommt, das Koppeln mit einer Instanz per QR-Code oder covey://-Link, Mitteilungen, und was auf welcher Plattform nicht geht.'
faq:
  - q: Gibt es die covey-App im App Store oder bei Google Play?
    a: 'Noch nicht. Der App-Dialog der Weboberfläche sagt das für beide. Jedes Release lädt den iPhone-Build zu App Store Connect hoch, wo TestFlight ihn den internen Testern des Projekts gibt; zur Prüfung eingereicht wird nichts. Einen Android-Build veröffentlicht das Release nicht. Die Mac- und die Windows-App hängen an jedem GitHub-Release.'
  - q: Wie verbindet sich die App mit meiner Instanz?
    a: 'Durch Koppeln: Die Weboberfläche zeigt einen QR-Code, der einmal und fünf Minuten lang gilt, die App scannt ihn und tauscht ihn gegen einen API-Schlüssel, benannt nach dem Gerät. Auf einem Desktop öffnet derselbe Code die App über einen covey://-Link. Die Instanz muss über HTTPS erreichbar sein.'
  - q: Warum zeigt die Windows-App keine Mitteilungen?
    a: 'Die Mac-App zeigt sie selbst an, solange sie läuft, mit macOS-eigenen Schnittstellen; für Windows gibt es dazu noch kein Gegenstück, und einen Desktop erreicht kein Push-Dienst. Telefone bekommen ihre Mitteilungen über Firebase Cloud Messaging.'
---

# Die covey-App

Eine Flutter-App, gebaut aus [`mobile/`](../../../mobile/README.md), für iPhone und iPad, Android, den Mac und Windows; ein Linux-Build ist angelegt. Sie ist der Ort, an dem ein Mensch *mit* den Agenten arbeitet, nicht der, von dem aus sie betrieben werden: Sie koppelt sich mit einer Instanz, zeigt die Agenten und was auf diesen Menschen wartet, bringt eine Nachricht zu einem Agenten und eine Antwort zurück und diktiert auf dem Gerät. Agenten, Secrets, Guard-Rails oder die Plattform einrichten bleibt Sache der Weboberfläche — die App hat nichts davon, mit Absicht ([`spec/27-mobile-app.md`](../../../spec/27-mobile-app.md)).

**Stand: ein Prototyp.** Sie meldet sich mit einem API-Schlüssel an, den ihr das Koppeln gibt; die gerätegebundene Anmeldung, die die Spezifikation verlangt, gibt es noch nicht. Einem Agenten schreiben setzt voraus, dass die Team-Oberfläche der Organisation eingeschaltet ist (**Verwaltung → Administration**, Reiter **Profil**, Karte **Team-Oberfläche**); ausgeschaltet liest die App mit und sagt, warum sie nicht schreiben kann.

Die App ist für jede Installation dieselbe: Beim ersten Start fragt sie, mit welcher covey sie arbeiten soll.

## Die App bekommen

In der Weboberfläche hat das Benutzermenü einen Eintrag **covey-App**. Er listet die vier Plattformen, verlinkt die Downloads für Mac und Windows und zeigt darunter den Kopplungscode.

| Plattform | Woher sie kommt |
|---|---|
| iPhone und iPad | **Noch nicht im App Store.** Jedes Release lädt den Build zu App Store Connect hoch, wo TestFlight ihn den internen Testern des Projekts gibt; zur Prüfung eingereicht wird nichts. iOS 15 oder neuer. |
| Android | **Noch nicht bei Google Play**, und das Release veröffentlicht keinen Android-Build. Mit Flutter aus `mobile/` bauen. |
| Mac | `covey-app_<Version>_macos.zip`, am GitHub-Release. macOS 12 oder neuer. Sie aktualisiert sich selbst über Sparkle, wenn das Release einen Update-Feed trägt. |
| Windows | `covey-app_<Version>_windows.zip`, am GitHub-Release. Irgendwohin entpacken und `covey_mobile.exe` starten; einen Installer gibt es nicht. |
| Linux | Angelegt, kein Paket. Es fehlt die Registrierung von `covey://` (eine `.desktop`-Datei). |

Die Download-Links im App-Dialog zeigen auf das Release **der eigenen Version der Instanz**, damit App und Server zusammenpassen; eine Instanz, die zwischen zwei Tags gebaut ist, verlinkt das neueste Release, und eine, deren Quelle nicht auf GitHub liegt, bekommt keinen Link statt eines falschen.

**Mac.** Die App ist signiert und notarisiert, wenn das Release mit dem Zertifikat des Projekts gebaut wurde. Ein unsignierter Build sagt das in den Release-Notizen, und Gatekeeper verweigert dann den ersten Start: Rechtsklick auf `covey.app` → Öffnen, oder `xattr -dr com.apple.quarantine covey.app`.

**Windows.** Die App ist **nicht code-signiert**, deshalb hält SmartScreen sie beim ersten Start an: *Weitere Informationen*, dann *Trotzdem ausführen*. Sie aktualisiert sich nicht selbst; eine neue Version ist ein neuer Download. Bei jedem Start registriert sie `covey://` für den aktuellen Benutzer (`HKEY_CURRENT_USER\Software\Classes\covey`, ohne Administratorrechte) und lässt es auf ihre eigene ausführbare Datei zeigen — nachdem der Ordner verschoben wurde, gewinnt die zuletzt gestartete Kopie. Es gibt ein Fenster: Ein Link oder ein zweiter Start übergibt an das laufende. Wer die Registrierung loswerden will, löscht diesen Schlüssel.

## Koppeln

Die App fragt nach keinem Passwort. Gekoppelt wird aus einem angemeldeten Browser:

1. In der Weboberfläche das Benutzermenü öffnen → **covey-App** → *Mit dieser covey koppeln*, oder **Profil & Einstellungen** → *Mobile App* → **Mobile App koppeln**.
2. Ein QR-Code erscheint. Er gilt **einmal und fünf Minuten lang**; die Seite zählt herunter und merkt, wenn die App ihn benutzt hat.
3. In der App auf **QR-Code scannen** tippen und die Kamera darauf richten.

Die App tauscht den Code gegen einen **API-Schlüssel, benannt nach dem Gerät**, und legt ihn im Schlüsselbund (Apple) bzw. im Keystore (Android) ab. Der Schlüssel erscheint unter den API-Schlüsseln der Person in **Profil & Einstellungen** und lässt sich dort widerrufen wie jeder andere ([API keys](../../en/operations/api-keys.md), englisch). Er trägt die Rechte des Platzes dieser Person und nicht mehr.

Der QR-Code trägt einen gewöhnlichen Link zur Instanz, `https://<Host>/pair?code=…`. Bei einer Instanz, deren Host die App beansprucht (app.covey.work oder eine Installation, die die App unter ihrem eigenen Host ausliefert, siehe [App-Links](operations.md#app-links)), öffnet die Kamera des Telefons damit direkt die App. Überall sonst öffnet der Link die Seite `/pair`, die den Code über `covey://` an die App weiterreicht.

**Auf einem Desktop** gibt es keine Kamera zum Scannen. Die Kopplungskarte hat einen Knopf **In der App öffnen**, einen Link `covey://pair?instance=…&code=…`, den die Mac- oder Windows-App annimmt.

**Nur HTTPS.** Die App verbindet sich nur mit HTTPS-Adressen, und die Kopplungsseite warnt, wenn sie nicht über HTTPS ausgeliefert wird. Die eine Ausnahme ist ein Debug-Build gegen `http://localhost:8494` (`http://10.0.2.2:8494` aus dem Android-Emulator).

Ohne Kamera oder Browser in Reichweite nimmt die App auch die Adresse und einen von Hand eingegebenen API-Schlüssel.

## Mitteilungen

Stellt ein Agent eine Rückfrage, antwortet er in einem Gespräch oder erledigt er eine Aufgabe, die aus einer Nachricht kam (oder scheitert daran), bekommen die Beteiligten eine Mitteilung — wer diesem Agenten in den letzten zwei Wochen geschrieben hat, wer die Aufgabe gestellt hat und bei einer Rückfrage der Mensch, an den der Agent berichtet. Was jemand schon gelesen hat, wird nicht gemeldet.

| Plattform | Wie |
|---|---|
| iPhone und iPad | Firebase Cloud Messaging, das die Mitteilung an Apple weitergibt |
| Android | Firebase Cloud Messaging |
| Mac | Die App zeigt sie selbst an, solange sie läuft, aus dem Ungelesenen; über einen Push-Dienst geht nichts |
| Windows, Linux | keine |

Wie die Instanz Firebase erreicht — **direkt** mit einem eigenen Dienstkonto, **über ein Relay** (Vorgabe `https://app.covey.work`, das das Dienstkonto der App-Builds des Projekts hat) oder **aus** —, stellt eine Administratorin der Installation unter **Verwaltung → Plattform**, Reiter **Push**, ein. Die Einzelheiten, die Umgebungsvariablen und die Einstellungsschlüssel stehen im [Betriebshandbuch](operations.md#push-mitteilungen).

**Was die Instanz verlässt.** Eine Mitteilung trägt das Token des Geräts, den Namen des Agenten und was er getan hat („Bea hat eine Rückfrage“, in der Sprache des Geräts), die Zahl des Ungelesenen, die ID des Agenten, damit ein Tippen seinen Verlauf öffnet, und den gewählten Ton. Vom Gesagten steht nichts darin. Eine Organisation kann unter **Verwaltung → Administration**, Reiter **Profil**, Karte **Push-Mitteilungen** eine Vorschau einschalten; dann geht die erste Zeile des Gesagten mit. So oder so läuft die Mitteilung über Google und, für ein iPhone, über Apple — und über das Relay, wo eines benutzt wird.

Eine Mitteilung öffnet den Verlauf mit dem Agenten, um den es geht. Die App bietet covey-eigene Mitteilungstöne, den Ton des Systems oder keinen.

## Was wo geht

Die App ist eine Codebasis, aber mehrere Funktionen beruhen auf macOS-Schnittstellen und gibt es nur dort:

| | iPhone / iPad | Android | Mac | Windows |
|---|---|---|---|---|
| Koppeln | QR-Code | QR-Code | `covey://`-Link | `covey://`-Link |
| Mitteilungen | Push | Push | solange die App läuft | — |
| Diktat in der App | ja | ja | ja | ja |
| Überall diktieren (globales Tastenkürzel) | — | — | ja | — |
| Aktivitätsprotokoll | — | — | ja | — |
| Der eigene Ton des Computers neben dem Mikrofon in einer Notiz | — | — | ja | — |
| Sich selbst aktualisieren | TestFlight, für Tester | — | Sparkle | — |

Das Diktat wird auf dem Gerät mit sherpa-onnx erkannt; das Sprachmodell kommt von der Instanz, wenn zum ersten Mal jemand diktiert ([Sprachmodell](operations.md#sprachmodell)).

## Weiter

- [`mobile/README.md`](../../../mobile/README.md) — die App bauen und an ihr arbeiten, die Desktop-Builds, die Firebase-Konfiguration
- [`spec/27-mobile-app.md`](../../../spec/27-mobile-app.md) — was die App ist, was sie nicht ist, was sie nie tun darf
- [Betrieb](operations.md#push-mitteilungen) — Push, App-Links, das Sprachmodell
