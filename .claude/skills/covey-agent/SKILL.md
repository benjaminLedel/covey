---
name: covey-agent
description: Covey-Agenten bauen, designen, aktualisieren und teilen. Nutze diesen Skill, wenn ein Covey-Agent (Entwickler, QA, Rechercheur, Support …) neu entworfen, seine Config (SOUL/PLAYBOOKS/ACCESS/HEARTBEAT) geändert oder als Bundle exportiert/importiert werden soll. Erzeugt ein covey.agent-config-Bundle nach den Repo-Konventionen und legt den Agenten optional direkt per API an.
---

# Covey-Agenten bauen

Ein Covey-Agent ist **Config-as-Code**: sein Verhalten steckt in ein paar Markdown-Dateien,
zusammengepackt als **Bundle** (`covey.agent-config`, Version 1). Dieser Skill führt durch
Design, Anlegen, Update und Teilen. Details zum Bundle-Schema und dem Zielsystem-Katalog
stehen in [reference.md](reference.md) — dort nachschlagen, nicht raten.

**Sprache & Ton:** Die Config-Dateien sind **auf Deutsch**, nüchtern und präzise, im Stil der
mitgelieferten Vorlagen unter `examples/*.bundle.json`. Fachbegriffe englisch belassen
(*Merge Request*, *Backlog*, *Heartbeat*, *done*, *blocked*).

## Immer zuerst: an einer Vorlage orientieren

Beginne NIE bei null. Wähle die nächstliegende Vorlage aus `examples/` als Ausgangspunkt und
passe sie an:

| Vorlage | Rolle |
|---|---|
| `coding-agent.bundle.json` | Entwickler: GitLab-Issues fixen, MRs eröffnen, Review-Loop |
| `qa-agent.bundle.json` | Tester: fremde MRs abnehmen, Web-UIs im Browser prüfen, Bug-Intake per Mail |
| `web-researcher.bundle.json` | Rechercheur: im offenen Web mit echtem Browser recherchieren |
| `log-triage-agent.bundle.json` | Log-Triage: gemeldete Logs → GitLab-Tickets |
| `delivery-lead.bundle.json` | Delivery Lead: einen GitLab-Meilenstein zur Frist führen — Tickets aufbereiten, Reihenfolge halten, an Entwickler-Kollegen vergeben, Stand berichten. Vorhabensspezifisches steht im [Vorhaben-Steckbrief](../../examples/vorhaben-steckbrief.md), nicht in der Config |

Lies die gewählte Vorlage komplett, bevor du etwas änderst — sie zeigt die bewährte Struktur.

## Workflow A — Neuen Agenten designen

1. **Interview** (kurz, gezielt): Wofür ist der Agent da? Welche **Zielsysteme** braucht er
   (gitlab / email / dev / browser / mcp — siehe reference.md)? **Takt/Auslöser** (Heartbeat-
   Intervall, `nur-wenn`)? **Team** (Vorgesetzter, ggf. QA-Reviewer)? **Projekt-Scope**?
   Braucht er eine **warme Sandbox** (Dev-Server/Build zwischen Läufen halten → Test-/Entwickler-
   Agenten)?
2. **Dateien entwerfen** — die fünf Config-Dateien nach den Konventionen unten:
   `SOUL.md`, `CAPABILITIES.md`, `PLAYBOOKS.md`, `ACCESS.md`, `HEARTBEAT.md`.
3. **Bundle zusammensetzen** (Schema in reference.md), JSON validieren
   (`python3 -c "import json;json.load(open('<datei>'))"`).
4. **Anlegen** (Workflow D) oder als Datei ablegen, damit der Nutzer per UI importiert.
5. **Nacharbeit nennen:** Der Import legt Config an, aber **keine Secrets** — sag dem Nutzer,
   welche Secret-Namen der Agent braucht (aus `ACCESS.md`, z. B. `gitlab_token`, `gitlab_url`)
   und dass er sie in Covey zuweisen muss. Ebenso ggf. Egress-Allowlist (Browser/HTTP).

## Workflow B — Bestehenden Agenten aktualisieren

1. **Aktuellen Stand holen:** `GET /api/v1/agents/{id}/export` → das aktuelle Bundle. (Oder die
   Config im UI-Tab „Config" ansehen.)
2. Die betroffene(n) Datei(en) ändern — **minimal-invasiv**, Struktur/Ton erhalten.
3. **Nur die Config zurückspielen:** `POST /api/v1/agents/{id}/config/import` mit dem Bundle —
   das überschreibt SOUL/CAPABILITIES/PLAYBOOKS/ACCESS/HEARTBEAT als **neue Version** (versioniert,
   Stammdaten/Secrets/Guard-Rails bleiben unangetastet).

## Workflow C — Teilen (Export/Import durch Dritte)

- **Export:** `GET /api/v1/agents/{id}/export` liefert das Bundle als JSON-Download. Secret-**Werte**
  sind NIE enthalten (nur Namen, und nur für berechtigte Rollen). Diese Datei kann ein Dritter
  weitergeben/herunterladen.
- **Import beim Dritten:** `POST /api/v1/agents/import` mit dem Bundle-JSON legt in dessen Org
  einen neuen Agenten an. Slug-Kollision → `?slug=<neu>` hängt anders benannt an. Danach muss der
  Dritte die genannten Secrets selbst setzen (Werte reisen nie mit).

## Workflow D — Live anlegen (API)

Frage nach **Covey-Basis-URL** und **Auth** (Admin-Session-Cookie ODER Bearer-Token einer
Manage-Rolle) — hardcode nie Zugangsdaten. Dann:

```bash
curl -sS -X POST "$COVEY_URL/api/v1/agents/import" \
  -H "Authorization: Bearer $COVEY_TOKEN" \
  -H "Content-Type: application/json" \
  --data-binary @agent.bundle.json
# Slug schon vergeben? -> ...?slug=<neuer-slug>
```

Import ist RBAC-geschützt (Manage-Rolle). Die Antwort enthält ggf. Warnungen (fehlender
Supervisor, manuell nachzutragende Secrets) — dem Nutzer weitergeben.

## Konventionen & hart erkaufte Lektionen (unbedingt einhalten)

Diese Regeln stammen aus echten Fehlern — beim Entwerfen von `SOUL.md`/`PLAYBOOKS.md`/`HEARTBEAT.md`
immer berücksichtigen:

- **Immer mit `done` enden, nie `blocked`** bei Polling-Zielsystemen ohne Webhook (GitLab, E-Mail):
  der Heartbeat greift offene Arbeit erneut auf. `blocked` ist nur für echte externe Warte-Events.
- **Idempotenz gegen Loops:** Ein Heartbeat feuert wiederholt. Der Agent muss VOR dem Handeln
  prüfen, ob er die Arbeit schon erledigt hat (z. B. `list_notes`: eigener Kommentar/MR-Link
  vorhanden, kein neuer menschlicher Input) → dann **überspringen**. **Nie denselben Kommentar
  erneut posten.** (GitLab bremst identische Folge-Kommentare zwar serverseitig, aber der Prompt
  muss es trotzdem sauber tun — sonst Re-Checkout/Re-Fix-Kosten.)
- **Hand-off statt Horten:** Ist die Arbeit abgegeben (MR eröffnet), das Ticket **weiterreichen**
  (`assign` an den Vorgesetzten) und/oder `Closes #<iid>` in die MR-description, damit es beim Merge
  schließt — kein Ticket bleibt endlos beim Agenten liegen.
- **Sichtbar arbeiten, sonst weckt es erneut.** Die `nur-wenn:`-Bedingung triggert auf die **Flanke**:
  Ein Issue/MR gilt als erledigt, sobald der **letzte Nicht-System-Kommentar vom Bot** stammt. Ein
  Lauf, der etwas tut, ohne zu kommentieren, hinterlässt keine Flanke — beim nächsten Intervall gilt
  dieselbe Arbeit wieder als offen. Also: **wer arbeitet, kommentiert.**
- **Intervall an der Lauf-Dauer bemessen, nicht am Wunsch nach Reaktionszeit.** Ein Issue end-to-end
  (Checkout, Analyse, Fix, MR) dauert Minuten bis Viertelstunden — `alle: 15m` ist realistisch, alles
  unter 5m für code-anfassende Agenten falsch.
- **Zu große Aufträge zerlegen statt ins Turn-Limit laufen:** `covey/create_task` legt eine Teilaufgabe
  an (ohne `agent`) oder delegiert an einen Kollegen (`"agent":"<slug>"`). Der Playbook-Schritt lautet:
  Teilergebnis sauber abschließen, Rest als Aufgabe hinterlegen. Läuft ein Agent regelmäßig ins Limit,
  ist der Auftrag zu groß geschnitten oder `max_turns` zu klein.
- **Stages sind Zustände, keine Überschriften.** `covey/set_stage` legt fehlende Spalten automatisch an
  — im Playbook eine **feste, kleine** Menge von Spaltennamen vorgeben (z. B. `Triage`, `Analyse`,
  `Warten auf Review`). Nie den Vorgang in den Spaltennamen (`#83 CSV-Import`), nie Synonyme für
  denselben Zustand; sonst wächst das Board in Tagen auf ein Dutzend toter Spalten.
- **`ACCESS.md`-Syntax:** eine Zeile je System: `- system: <name> scope: <scope1>,<scope2>`.
  Nur freischalten, was der Agent wirklich braucht (least privilege).
- **`HEARTBEAT.md`-Syntax:** eine Zeile je Auslöser:
  `- alle: <intervall> nur-wenn: <system>:<kind> titel: <kurz> aufgabe: <was genau zu tun ist>`.
  `nur-wenn` weckt nur, wenn es Arbeit gibt (billiger Vorab-Check der Control Plane). Enge, konkrete
  `aufgabe:`-Texte — der Agent liest sie als Auftrag.
- **Warme Sandbox** (`agent.warm_sandbox: true`) nur für Agenten, die einen Dev-Server hochfahren
  oder schwere Builds/Deps haben (QA/Entwickler) — spart kalten Aufbau, belegt aber dauerhaft
  Ressourcen. Recherche-/Triage-Agenten bleiben ephemer.
- **Browser-Agenten:** Aktionen `navigate/content/screenshot/click/type`. Selektoren sind CSS plus
  `:has-text("…")` (innerster sichtbarer Text-Treffer). `screenshot` kann `highlight`+`label`, um
  einen Mangel visuell zu markieren. Erreichbarkeit hängt an der **Egress-Allowlist**.
- **Secrets nie ins Bundle:** Nur Namen (`ACCESS.md` / `secrets`-Block). Werte werden in Covey
  separat zugewiesen und zur Laufzeit gebrokert.
- **Team-Bezug:** Verweise in `SOUL.md`/`PLAYBOOKS.md` auf „das Team (KI-Kollegen)" statt auf feste
  Namen — Covey spielt das Team-Verzeichnis in den Prompt ein (z. B. wer als Reviewer eingetragen wird).

## Selbstprüfung vor dem Anlegen

- [ ] Bundle-JSON valide, `kind`/`version` gesetzt, `agent.slug` + `agent.display_name` vorhanden?
- [ ] Alle fünf Dateien vorhanden und auf Deutsch, im Vorlagen-Ton?
- [ ] `ACCESS.md` deckt genau die im `PLAYBOOKS.md` genutzten Aktionen — nicht mehr?
- [ ] `HEARTBEAT.md` endet-mit-`done`-Logik + Idempotenz/Skip beschrieben?
- [ ] Loop-Schutz (Idempotenz, kein Doppel-Kommentar, Hand-off) im Prompt verankert?
- [ ] Jeder Lauf hinterlässt eine sichtbare Spur (Kommentar) — sonst weckt die Flanke erneut?
- [ ] Intervall passt zur Lauf-Dauer (Code-Agenten ≥ 5m, realistisch 15m)?
- [ ] Feste, kleine Menge Stage-Namen im Playbook statt frei erfundener Spalten?
- [ ] `warm_sandbox` bewusst gesetzt (nur bei Dev/Test)?
- [ ] Nötige Secrets + Egress-Hosts dem Nutzer genannt?
