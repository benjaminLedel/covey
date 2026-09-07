---
slug: telemetrie
title: Telemetrie und der Kanal zum Projekt
description: 'Was eine covey-Installation einmal am Tag an das Projekt schickt, was sie nie schickt, wie man es abschaltet — und wie ein Plattformfehler in den Tracker kommt.'
faq:
  - q: Ist Telemetrie standardmäßig an?
    a: 'Ja. Eine Einstellung schaltet sie ab (Plattform → Einstellungen → telemetry.mode = off), eine leere telemetry.url tut dasselbe, und COVEY_TELEMETRY=off in der Umgebung steht über beidem — das wirkt schon vor dem ersten Start.'
  - q: Was genau wird geschickt?
    a: 'Zahlen und Versionen: wie viele Organisationen, Menschen und Agenten, wie viele Aufgaben am letzten Tag liefen und wie sie endeten, welche Runtimes und welche Zielsysteme eingeschaltet sind, ob ein Runner verbunden ist. Nie ein Aufgabentitel, ein Agenten-Slug, ein Prompt, ein Ergebnis, eine Adresse oder ein Organisationsname.'
  - q: Kann jemand erkennen, welche Installation meine ist?
    a: 'Aus den Daten nicht. Die Kennung ist eine UUID, die sich die Installation selbst gegeben hat; sie verbindet die Zahlen von gestern mit denen von heute und trägt keinen Namen, keine Domain, keine Adresse. Wer die Zeile löscht, ist danach eine neue Installation.'
  - q: Gehen Fehlerberichte meiner Agenten automatisch hinaus?
    a: 'Nein. Nur, was covey Doctor mit covey/create_issue bewusst einreicht, und nur, wenn das Ziel das Repository des Projekts ist. Eine Organisation, die in ihr eigenes GitLab meldet, benutzt diesen Kanal nie.'
---

covey spricht an zwei Stellen mit dem Projekt, aus dem es stammt — und beide
gehen durch denselben Kanal mit demselben Schalter davor.

## 1. Telemetrie: eine Handvoll Zahlen, einmal am Tag

**Standardmäßig an**, und das ist eine Entscheidung und kein Versehen: Ohne sie
weiß das Projekt nichts darüber, wie covey tatsächlich betrieben wird — auf
welcher Version Installationen stehen, ob eine Auslieferung überhaupt
angenommen wird, wie viele Agenten eine übliche Instanz hält — und jede Antwort
darauf wird zur Vermutung.

**Was hinausgeht**, vollständig:

| | |
|---|---|
| `version`, `commit` | welcher Stand das hier ist |
| `orgs`, `humans`, `agents`, `agents_hired` | wie viele es davon gibt |
| `tasks_24h`, `tasks_done_24h`, `tasks_failed_24h`, `tasks_blocked` | wie viel am letzten Tag lief und wie es endete |
| `runtimes` | welche Engines die Agenten benutzen, und wie viele je |
| `targets` | welche Zielsysteme eingeschaltet sind, beim Namen |
| `runners` | wie viele Runner angemeldet sind |

**Was nie hinausgeht:** ein Aufgabentitel, ein Agenten-Slug, ein Prompt, ein
Ergebnis, eine Mailadresse, ein Repository, eine URL, ein Organisationsname,
ein Kundenname. Nichts, was ein Agent erzeugt hat, und nichts, was jemand
eingetippt hat. Zusammengestellt wird es in einer kurzen Funktion —
`internal/telemetry`, `Zahlen` — und der Test daneben
(`TestTelemetrieSchicktNurZahlen`) nimmt einen echten Aufgabentitel und einen
echten Agenten-Slug aus einer laufenden Instanz und schlägt fehl, wenn eines
davon im Gesendeten auftaucht.

**Die Kennung** ist eine UUID, die sich die Installation beim ersten Mal selbst
gibt (`telemetry.id`). Es gibt sie, damit die Zahlen von gestern und heute
dieselbe Installation sind. Sie trägt keinen Namen und keine Adresse.

### Abschalten

Drei Wege, und jeder heißt: *nichts verlässt die Maschine*.

```bash
# 1. Die Einstellung, unter Plattform → Einstellungen
telemetry.mode = off

# 2. Kein Ziel
telemetry.url = (leer)

# 3. Die Umgebung — wirkt schon vor dem ersten Start
COVEY_TELEMETRY=off
```

## 2. Der Kanal für einen Plattformfehler

Zu covey Doctors Arbeit gehört der Befund, den keine Konfiguration behebt: Drei
Agenten sind diese Woche am Zugbegrenzer gestorben, und keiner von ihnen war
falsch konfiguriert. Das ist ein Fehlerbericht, und er gehört in den Tracker des
Repositories, in dem covey gepflegt wird.

Zwei Wege führen dorthin, und die Plattform nimmt den, der da ist:

- **Das eigene Konto.** Den Token des Zielsystems als Secret der Organisation
  hinterlegen (`github_token`, `gitlab_token`, …) und *Organisation → Quelle
  dieser Plattform* setzen. Die Steuerebene reicht unter diesem Konto ein. Die
  Installation verlässt nichts außer dem Bericht, den der Agent geschrieben hat.
- **Der Kanal zum Projekt**, wenn das Ziel das Repository des Projekts ist und
  kein eigenes Konto hinterlegt ist. Der Bericht geht an covey.work, wo ihn ein
  Mensch liest, bevor daraus ein Issue wird — Installationen, die das Projekt
  kennt, gehen sofort durch, alle anderen warten. In einen öffentlichen Tracker
  gelangt nichts ungelesen.

So oder so sieht der Agent nie ein Credential, das Ziel ist Stammdatum und kein
Parameter, und derselbe Bericht steht in Ihrem eigenen Posteingang, damit Sie
sehen, was hinausgegangen ist.

Eine Organisation, die in ihr eigenes GitLab meldet (eigenes Zielsystem,
eigenes Projekt), benutzt den Kanal zum Projekt nie: Ihre Befunde bleiben im
Haus, und dafür ist die Einstellung da.

### Was in so einem Bericht steht

Was covey Doctor geschrieben hat: ein Titel und ein Text mit seinen Belegen —
welche Agenten, welche Läufe, was es gekostet hat, und was er im Quelltext
gefunden hat, wo er ihn lesen darf. Es ist ein Text, den ein Agent über die
Plattform verfasst hat. Wenn Ihre Instanz Material verarbeitet, das sie nicht
verlassen darf, hinterlegen Sie ein eigenes Konto für Ihren eigenen Tracker —
oder schalten Sie die Schicht mit `-` unter *Organisation → Quelle dieser
Plattform* ganz ab.
