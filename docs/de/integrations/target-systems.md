---
slug: zielsysteme
title: Zielsysteme & Plugins
description: 'Zammad, Salesforce, Jira, Confluence, GitHub, GitLab, Teams, SharePoint, Nextcloud, E-Mail, Search Console, Browser und MCP: Wie covey Agenten über Plugins an Fremdsysteme anbindet.'
faq:
  - q: Kann ich ein System anbinden, für das es kein Plugin gibt?
    a: 'Ja — am schnellsten über einen MCP-Server, dessen Werkzeuge der Agent dann nutzen kann. Wer Weckereignisse, Scopes und Aktionen im Recording braucht, schreibt ein Plugin: als Manifest (JSON, ohne Neubau installierbar), als WebAssembly-Modul oder kompiliert in Go. Die mitgelieferten liegen im Plugin-Pack und sind die Vorlage.'
  - q: Braucht jedes Zielsystem einen Webhook?
    a: Nein. Ohne Webhook arbeitet der Agent über einen Heartbeat, idealerweise mit `nur-wenn:` — dann prüft die Control Plane vorab billig, ob überhaupt Arbeit anliegt, statt den Agenten ins Leere zu wecken.
  - q: Wie kommt der Agent an ein Git-Repository?
    a: 'Über das GitHub- oder GitLab-Plugin: Der Checkout passiert in der Sandbox, mit einem gebrokerten, kurzlebigen Zugang. Das Arbeitsverzeichnis bleibt im Home des Agenten und steht beim nächsten Lauf wieder bereit.'
---

# Zielsysteme & Plugins

Ein Agent wird nützlich, wenn er in den Systemen arbeitet, in denen ohnehin gearbeitet wird. In covey ist jedes davon ein **Plugin**: Es beschreibt seine Aktionen, seine Scopes und seine Weckereignisse, und der Kern kennt keine Sonderfälle.

## Was mitgeliefert wird

- **Zammad** — Tickets sichten, intern notieren, nach außen antworten; Webhook als Weckquelle
- **Salesforce Service Cloud** — Fälle mit ihrer ganzen Konversation, Antwort als Notiz, Portal-Kommentar oder Mail
- **GitHub** und **GitLab** — Issues, Pull- und Merge-Requests, Pipelines, Checkout in der Sandbox
- **Jira** — das Ticket neben dem Repository: per JQL suchen, übernehmen, durch den Workflow bewegen; Cloud und Data Center
- **Confluence** — die Dokumentation, an der beides hängt: Seiten als Markdown lesen und schreiben. Weckt niemanden — der Agent kommt her, während er an etwas anderem arbeitet
- **E-Mail (IMAP/SMTP)** — ein Postfach als Weckquelle, Antworten im Thread
- **Microsoft Teams** — Chat als Kanal zwischen Mensch und Agent
- **SharePoint** über Microsoft Graph und **Nextcloud** über WebDAV — Dateien
- **Google Search Console** — was eine Suchmaschine mit einer Seite gemacht hat, im Unterschied zu dem, was die Seite über sich behauptet: welche Adressen indexiert sind, welchen Canonical Google statt des deklarierten gewählt hat, wonach jemand gesucht hat, bevor er ankam. Lesend bis auf das Einreichen einer Sitemap — und der OAuth-Bereich wird je Aktion gewählt, ein Agent mit `scope: read` hält einen Token, der nicht schreiben kann
- **Browser** — headless Chrome für Oberflächen ohne API, mit Screenshots im Recording
- **MCP** — beliebige Model-Context-Protocol-Server als Werkzeugquelle
- **Kubernetes** — ein Cluster ausgelesen: warum ein Pod neu startet, was er vor seinem Tod gesagt hat, wohin ein Ingress zeigt
- **VulnDB** — bekannte Schwachstellen in Abhängigkeiten (npm, Composer, Dart/Flutter)

## Woher sie kommen

Die meisten sind einkompiliert und einfach da. Drei nicht: **Zammad**,
**Kubernetes** und **VulnDB** kommen als WebAssembly-Module aus dem **Katalog**
und werden einmal installiert — *Store → Katalog → Installieren*. covey prüft
dabei den Digest, auf den der Katalog das Modul festlegt.

Der Unterschied ist Absicht, nicht Geschichte: ein Plugin aus dem Katalog wird
ohne neues covey-Release aktualisiert, und ein Dritter veröffentlicht eines zu
genau denselben Bedingungen wie wir. Wer eine Installation über 0.6.0 hinweg
aktualisiert, installiert die drei danach einmal — die Zugänge der Agenten
bleiben erhalten.

## Der Aktions-Proxy

Der Agent ruft ein Zielsystem nicht direkt. Er nennt eine Aktion, die Control Plane führt sie aus und gibt das Ergebnis zurück. Drei Dinge fallen dabei ab, die man sonst nachbauen müsste: das Token bleibt draußen, die Guard-Rails greifen an einer Stelle, und die Aktion steht im Recording.

## Weckereignisse

Der beste Weg ist der Webhook: Passiert im Ticketsystem etwas, ruft es covey, und der zuständige Agent wacht auf. Wo es keinen Webhook gibt, hilft ein Heartbeat mit `nur-wenn:` — die Control Plane prüft billig, ob es Arbeit gibt, und weckt sonst niemanden.

Ereignisse tragen einen Korrelationsschlüssel, damit eine wartende Aufgabe wieder aufgenommen wird und nicht eine neue entsteht.

## Zugänge im Agenten

Welche Systeme ein Agent nutzen darf, steht in seiner `ACCESS.md`:

```
- system: zammad scope: read,write,comment
- system: gitlab scope: read,write
```

Die Werte dahinter — URL, Token — liegen bei der Organisation, nicht beim Agenten, und werden zur Laufzeit gebrokert.

## Ein eigenes System anbinden

Vier Wege, je nachdem, wie weit die Anbindung tragen soll. Am schnellsten ist ein **MCP-Server** — der Agent bekommt dessen Werkzeuge, ohne dass an covey etwas geändert wird. Ein **Manifest** beschreibt eine REST-API als JSON-Datei und wird zur Laufzeit installiert, ohne Neubau. Ein **WebAssembly-Modul** bringt eigene Logik mit und kommt ebenfalls aus dem Katalog. Und ein **kompiliertes Plugin** in Go ist der Weg, wenn die Integration übersetzen muss, was das Fremdsystem speichert — Jira und Confluence tun genau das mit ihren Dokumentformaten. Alle vier bedienen dieselbe Schnittstelle; die mitgelieferten liegen im Plugin-Pack und sind die Vorlage.

## Weiter

- [Guard-Rails & Kontrolle](../concepts/guard-rails.md) — Freigaben für kritische Aktionen
- [Identität & Secrets](../concepts/identity-and-secrets.md) — wie die Zugänge gestellt werden
- [Backlog & Lifecycle](../concepts/backlog-and-lifecycle.md) — was ein Weckereignis auslöst
