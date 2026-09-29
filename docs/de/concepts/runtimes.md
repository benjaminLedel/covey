---
slug: runtimes
title: Runtimes und Engines
description: 'Die Engines, die covey mitbringt — Claude Code, Codex, SevenCode, educa AI und der Mock: was jede braucht, wie ein Agent auf eine gesetzt wird, woher die CLI kommt und was jede noch nicht kann.'
faq:
  - q: Welche Runtimes bringt covey mit?
    a: 'Im Daemon sind fünf Engines registriert: Claude Code (claude -p), Codex (codex exec), SevenCode (sevencode -p), educa AI (der Claude-Code-Harness gegen einen educa-AI-Endpunkt) und ein Mock für Tests und Demos. Claude Code tragen die Sandbox-Images, und an Claude Code wird die Plattform gemessen; Codex ist deklariert, sein Lauf aber noch nicht verifiziert.'
  - q: Wo wähle ich die Engine eines Agenten?
    a: 'Auf der Agentenseite, Reiter Einstellungen, Feld Engine — es greift beim nächsten Task-Dispatch. Dasselbe Feld ist PATCH /api/v1/agents/{id}/runtime. Modell und Denkaufwand stehen daneben; welche Werte es gibt, entscheidet die Engine.'
  - q: Was ist der Unterschied zwischen Engine und Platz?
    a: 'Die Engine ist Code: der Adapter für eine CLI. Ein Platz ist Konfiguration: eine Engine plus die Zugänge, die sie bezahlen, und ein Modell. Im einfachen Fall entsteht ein Platz von selbst, sobald ein Zugang unter Secrets liegt; wichtig wird er mit dem zweiten Zugang.'
---

# Runtimes und Engines

Die **Runtime** fährt die Modell-Schleife — sie sitzt zwischen Modell und Werkzeugaufruf und entscheidet den nächsten Schritt. covey tut das nicht selbst; es startet eine Runtime in der Sandbox und spricht mit ihr über einen dünnen Adapter im Daemon (`coveyd`). Die Oberfläche nennt so einen Adapter **Engine**. Jede Engine ist ein Plugin, das sich im Daemon selbst registriert, mit seiner Einrichtungsanleitung, den Zugängen, mit denen es laufen kann, und dem, was es kann und nicht kann; die Control Plane liest dieselbe Registry, es gibt also keine zweite Liste, die mitgezogen werden müsste.

**Infrastruktur → Engines** zeigt die Engines, die dieser Build registriert, und das ⓘ neben jeder öffnet ihre Einrichtung Schritt für Schritt.

## Engine, Platz, Agent

Drei Dinge, und sie sind mit Absicht getrennt:

- Die **Engine** ist Code — der Adapter für eine CLI (`claude-code`, `codex`, `sevencode`, `educa-ai`, `mock`).
- Ein **Platz** ist Konfiguration — eine Engine plus die Zugänge, die sie bezahlen, und optional ein Modell. Die Plätze stehen unter **Infrastruktur → Engines → Plätze**.
- Der **Agent** trägt den Namen seiner Engine und, getrennt davon, den Platz, auf dem er arbeitet.

Im einfachen Fall ist nichts einzurichten: einen Zugang unter **Secrets** unter dem Namen hinterlegen, den die Engine nennt (Tabelle unten), einen Agenten anlegen, und der Platz entsteht von selbst. Wichtig wird der Platz ab dem zweiten Zugang. Mehrere Zugänge unter einem Platz werden in fester Reihenfolge verbraucht — ein Abo wird gefüllt, bevor etwas nach Verbrauch Abgerechnetes angefasst wird, ein Agent behält seinen Zugang, solange der gesund ist (die Engine cacht ihren Prompt je Zugang), und unter gleichwertigen gewinnt der am wenigsten ausgelastete. Jeder Zugang kann ein Limit in einem gleitenden Fenster tragen (Dollar bei einem API-Schlüssel, Tokens bei einem Abo) und lässt sich von Hand pausieren.

Auf eine Engine gesetzt wird ein Agent auf der Agentenseite, Reiter **Einstellungen**, Feld **Engine**; die Änderung greift beim nächsten Task-Dispatch. **Modell** und **Denkaufwand** stehen daneben — ob eine Engine jede Modell-ID nimmt, eine feste Liste hat oder gar keinen Regler für den Denkaufwand, entscheidet die Engine, und ein Feld, das sie nicht hat, wird nicht angezeigt. Über die API:

```bash
# die Engine eines Agenten
curl -X PATCH https://covey.example.org/api/v1/agents/<id>/runtime \
  -H "Authorization: Bearer covey_…" -d '{"runtime":"codex"}'

# die Plätze, und einen Agenten auf einen setzen
curl https://covey.example.org/api/v1/runtime-instances -H "Authorization: Bearer covey_…"
curl -X POST https://covey.example.org/api/v1/agents/<id>/runtime-instance \
  -H "Authorization: Bearer covey_…" -d '{"runtime_id":"<Platz-ID>"}'
```

Ein Platz trägt die Zugänge seiner eigenen Engine. Ein Agent der einen Engine, der auf einem Platz der anderen sitzt, bekäme den falschen Zugang unter der falschen Variablen, deshalb verweigert der Lauf das mit einer Meldung, die beide Engines nennt, und die Sitzliste markiert so einen Agenten (*Sitzt woanders*) mit einem Knopf, der ihn umsetzt.

## Die Engines im Überblick

| Engine | CLI | Zugang (Secret → wie er in den Lauf kommt) | Resume | Skills | Denkaufwand | Modell |
|---|---|---|---|---|---|---|
| `claude-code` | `claude` | `anthropic_api_key` → `ANTHROPIC_API_KEY` oder `claude_code_oauth_token` → `CLAUDE_CODE_OAUTH_TOKEN` | ja | `.claude/skills` | `low` … `max` | jede ID oder jeder Alias |
| `codex` | `codex` | `openai_api_key` → `CODEX_API_KEY` oder `codex_auth_json` → die Datei `.codex/auth.json` | nein | keine | keiner | jede ID |
| `sevencode` | `sevencode` | `sevencode_api_token` → `SEVENCODE_API_KEY` | ja | `.sevencode/skills` | keiner | die der Instanz |
| `educa-ai` | `claude` | `educa_api_token` oder `educa_seat_token` → `ANTHROPIC_AUTH_TOKEN` | ja | `.claude/skills` | `low` … `xhigh` | feste Liste von dreien |
| `mock` | — | keiner | ja | `.claude/skills` | wie Claude Code | — |

Wo zwei Zugänge stehen, gewinnt der erste, der gefunden wird: Ein API-Schlüssel steht vor einem Abo, damit wer beides hat, Geld mit Absicht ausgibt und nicht aus Versehen. Der Wert wird je Wachphase vermittelt und ist kurzlebig; ein Zugang als Datei wird für den Lauf geschrieben und danach entfernt. Langlebige Secrets bleiben nicht in der Sandbox ([Identität und Secrets](identity-and-secrets.md)).

**Resume** ist das, worauf `blocked` beruht: Ein Agent, der eine Rückfrage stellt und auf die Antwort wartet, setzt dieselbe Sitzung fort, wenn die Antwort kommt ([Backlog und Lebenszyklus](backlog-and-lifecycle.md)). Eine Engine ohne Resume trägt Agenten, die in einem Lauf fertig werden, keinen, der wartet; die Sitzliste markiert sie mit *kein Resume*.

## Woher die CLI kommt

Die Engine ist eine CLI in der Sandbox, und sie kommt auf einem von drei Wegen dorthin:

1. **Das Arbeitsplatz-Image.** Jedes Sandbox-Image, das das Projekt veröffentlicht, trägt Claude Code (`npm install -g @anthropic-ai/claude-code` in [`Dockerfile.sandbox`](../../../Dockerfile.sandbox)), das auch `educa-ai` bedient. Keine andere Engine steckt in den Images.
2. **Der Engine-Katalog.** Ein JSON-Dokument hinter einer URL nennt Releases einer Engine, festgelegt über ihre Prüfsumme. Der Runner installiert ein Release beim ersten Gebrauch in sein Datenverzeichnis und hängt es schreibgeschützt in die Sandbox ein, es wird also kein Image neu gebaut. Vorgabe ist der Katalog des Projekts; `COVEY_ENGINE_CATALOG_URL` zeigt auf einen anderen, auf der Control Plane und auf jedem Runner. Der Katalog des Projekts führt SevenCode. Das ganze Modell steht in [`spec/26-engine-catalogue.md`](../../../spec/26-engine-catalogue.md).
3. **Ein eigener Pfad.** Jede Engine liest eine Variable, die ihr Binary benennt — `COVEY_CLAUDE_BIN`, `COVEY_CODEX_BIN`, `COVEY_SEVENCODE_BIN` — für eine Installation auf dem Host oder in einem eigenen Image ([Arbeitsplätze](../../en/operations/workplaces.md), englisch).

Wird ein Agent auf eine Engine gesetzt, deren CLI an keinem dieser Orte zu finden ist, antwortet die Zuweisung mit einer Warnung, statt die erste Aufgabe mit `executable file not found in $PATH` scheitern zu lassen. Sie warnt, sie verweigert nicht: Die CLI kann nachkommen.

## Claude Code

`claude-code` fährt Claude Code headless (`claude -p`, Ausgabe als `stream-json`), und an dieser Engine wird der Rest der Plattform gemessen: Resume über `--resume`, der Systemprompt über `--append-system-prompt`, Skills aus `.claude/skills`, die Aktionen der Zielsysteme als MCP-Server, der Werkzeugumfang über `--allowedTools` und der Denkaufwand über `--effort`. Sie meldet ihre Kosten selbst, und covey bucht sie unverändert, bei einem Abo zusätzlich die Auslastung des Fensters, wie der Anbieter sie meldet.

Sie braucht einen von zwei Zugängen:

- **Abo** (Pro/Max): `claude setup-token` im Terminal ergibt ein Token, das mit `sk-ant-oat…` beginnt; hinterlegt als `claude_code_oauth_token`. Es verbraucht das Kontingent des Abos.
- **API-Schlüssel** (Abrechnung nach Tokens): ein Schlüssel, der mit `sk-ant-api…` beginnt, aus der Anthropic-Konsole; hinterlegt als `anthropic_api_key`.

Beide werden beim Speichern gegen Anthropic geprüft, und ein Wert unter dem falschen Schlüssel wird mit einem Hinweis abgelehnt, unter welchen Schlüssel er gehört. Ohne einen von beiden scheitert eine Aufgabe mit „Not logged in · Please run /login“: Die Sandbox hat ihr eigenes leeres `HOME`, und ein Login auf dem eigenen Rechner ist dort nicht zu sehen. `api.anthropic.com` steht von Anfang an in der Basis-Egress-Allowlist jeder Organisation.

Einzelheiten: [`spec/12-claude-code-adapter.md`](../../../spec/12-claude-code-adapter.md). Welche eingebauten Werkzeuge von Claude Code ein Lauf hat, steuert `COVEY_RUNTIME_TOOLS` ([Deployment](../../en/operations/deployment.md), englisch).

## Codex

`codex` fährt OpenAI Codex headless (`codex exec --json`). **Der Stand ist „deklariert, Lauf nicht verifiziert“:** Der Adapter und beide Formen des Zugangs sind gebaut, der Lauf aber ist nicht gegen das Binary geprüft, und was die Dokumentation von OpenAI nicht klärt, ist weggelassen statt geraten.

- **Zugänge:** ein API-Schlüssel von der OpenAI-Plattform als `openai_api_key` (kommt als `CODEX_API_KEY` in den Lauf) oder ein Login eines ChatGPT-Plans (Plus/Pro/Team) — der Inhalt von `~/.codex/auth.json` nach `codex login` — als `codex_auth_json`, für den Lauf ins Home des Agenten geschrieben und danach entfernt. Keiner von beiden wird beim Speichern geprüft.
- **Kein Resume.** Ob `codex exec` eine Sitzung fortsetzen kann, ist nicht geklärt, also behauptet die Engine es nicht: Ein Agent auf Codex sollte in einem Lauf fertig werden und kann nicht auf eine Antwort warten.
- **Keine Skills, kein Denkaufwand, kein Turn-Limit, kein Werkzeugumfang.** Skills werden nicht geschrieben (wo Codex sie liest, ist nicht geklärt), der Regler für den Denkaufwand wird nicht angeboten, und Turn-Limit und Werkzeugliste werden nicht übergeben.
- **Kosten aus einer Preisliste.** Codex meldet Tokens und kein Geld; covey bepreist sie je Modell. Die Auslastung eines ChatGPT-Plans lässt sich aus der CLI nicht lesen, ein Limit beruht also auf dem, was covey selbst gebucht hat.
- **Die CLI steckt in keinem Image und in keinem Katalog.** `COVEY_CODEX_BIN` auf eine Installation setzen, oder ein eigenes Image oder einen eigenen Katalog verwenden.

Einzelheiten und die offenen Punkte: [`spec/19-codex-adapter.md`](../../../spec/19-codex-adapter.md).

## SevenCode

`sevencode` fährt die Coding-Agent-CLI SevenCode headless (`sevencode --json -p …`) gegen einen educa-AI-Endpunkt — eine zweite Agenten-Schleife vor demselben Gateway, das `educa-ai` über Claude Code erreicht.

- **Die 1.0-Linie, nicht das npm-Paket.** Auf den Namen hören zwei Programme. Das npm-Paket `sevencode` (`0.0.x`) ist eine andere CLI mit anderer Oberfläche; der Adapter fährt die 1.0-Linie. `sevencode --version` sagt, welche installiert ist.
- **Woher die CLI kommt:** aus dem Engine-Katalog des Projekts, als ein Node-Bundle (Node 22.13 oder neuer, das die Sandbox-Images mitbringen). Der Download liegt hinter einem Login: das Token als Secret namens `COVEY_SEVENCODE_DOWNLOAD_TOKEN` hinterlegen (das eigene des Agenten oder eines, das ihm zugewiesen ist) oder eine Variable dieses Namens auf dem Runner-Host setzen. Wird die Engine einem Agenten ohne dieses Secret zugewiesen, warnt covey. Alternativ zeigt `COVEY_SEVENCODE_BIN` auf eine Installation.
- **Zugang:** ein API-Token für die educa-AI-Instanz als `sevencode_api_token`; es kommt als `SEVENCODE_API_KEY` in den Lauf. Der Endpunkt ist `SEVENCODE_API_BASE`, gesetzt vom Katalogeintrag (`https://sevencode.app`) oder vom Host; covey erfindet keinen. Der Endpunkt muss aus der Sandbox über die Egress-Allowlist des Agenten erreichbar sein.
- **Resume:** ja. Die Sitzung wird dort gelesen, wo die CLI sie im Home des Agenten ablegt (`SEVENCODE_HOME` zeigt dorthin), und ein fortgesetzter Lauf, dessen Sitzung nicht da ist, wird verweigert, bevor die CLI startet.
- **Was sie nicht hat:** einen Schalter für den Systemprompt (die kompilierte Konfiguration steht in derselben Nachricht vor der Aufgabe), ein Turn-Limit, einen Werkzeugumfang, einen Regler für den Denkaufwand, eine Modellliste. Der Lauf startet im Modus `--auto` der CLI; was der Agent außerhalb der Sandbox erreichen darf, entscheiden Broker, Egress-Punkt und Guard-Rails, nicht die CLI.
- **Kosten in Tokens.** Die CLI meldet Tokenzahlen und keinen Preis, der Verbrauch auf dieser Engine wird also in Tokens gebucht.

Bekannte Einschränkungen, offen zum Zeitpunkt dieses Textes:

- Der Aktivitätsverlauf zeigt von einem SevenCode-Lauf fast nichts — Zeilen der Form „runtime event: …“ statt dessen, was der Agent sagt und welche Werkzeuge er aufruft. Die Aufzeichnung enthält den vollständigen Ereignisstrom ([#319](https://github.com/benjaminLedel/covey/issues/319)).
- Ein Lauf, der nach einem Turn ohne Werkzeugaufruf, ohne Statuszeile und mit leerem Ergebnis endet, wird als `done` verbucht ([#299](https://github.com/benjaminLedel/covey/issues/299)).

Einzelheiten: [`spec/25-sevencode-adapter.md`](../../../spec/25-sevencode-adapter.md).

## educa AI

`educa-ai` fährt dasselbe Claude-Code-Binary gegen den Anthropic-kompatiblen Endpunkt von educa AI Core (`https://api.educaai.de`; `COVEY_EDUCA_BASE_URL` in der Umgebung der Sandbox zeigt auf eine eigene Instanz). Weil der Harness Claude Code ist, funktionieren Resume und Skills wie dort.

- **Zugang:** ein Bearer-Token der Instanz, hinterlegt nach dem, was der Vertrag ist — `educa_api_token` (Abrechnung nach Tokens) oder `educa_seat_token` (ein Pauschal-Platz). Beide kommen als `ANTHROPIC_AUTH_TOKEN` in den Lauf.
- **Modelle:** eine feste Liste der IDs, die über den Harness eine echte mehrschrittige Aufgabe gelöst haben — `gemma-4-26B-A4B-it` (die Vorgabe), `gpt-oss-120b`, `gemma-4-E4B-it`. Andere IDs, die die Instanz listet, werden schon bei der Eingabe abgelehnt.
- **Denkaufwand:** `low` bis `xhigh`; Claude Codes `max` wird nicht angeboten.
- **Egress:** den Endpunkt (`api.educaai.de` oder den eigenen Host) in der Egress-Allowlist des Agenten erlauben, sonst erreicht die Sandbox kein Modell.

Einzelheiten: [`spec/23-educa-adapter.md`](../../../spec/23-educa-adapter.md).

## Mock

`mock` liefert eine geskriptete Antwort ohne Modell — kein Zugang, keine Kosten. Er ist für Demos da und für die Integrationstests, die mit ihm den ganzen Weg durch den Daemon fahren.

## Weiter

- [`spec/18-runtimes-capacity.md`](../../../spec/18-runtimes-capacity.md) — Engines, Plätze, Zugänge, die Merit-Order und Limits
- [`spec/26-engine-catalogue.md`](../../../spec/26-engine-catalogue.md) — woher das Binary einer Engine kommt
- [Kernbegriffe](../introduction/core-concepts.md) — Runtime, Daemon, Arbeitsplatz
