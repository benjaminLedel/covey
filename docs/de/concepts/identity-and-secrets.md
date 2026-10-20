---
slug: identitaet-secrets
title: Identität & Secrets
description: 'Agenten-Identität, RBAC und der Secrets-Broker: Zugänge werden zur Laufzeit ausgestellt, kurzlebig und gescopt. Langlebige Secrets kommen nie in die Sandbox.'
faq:
  - q: Kann ich HashiCorp Vault statt des eingebauten Speichers verwenden?
    a: 'Der Speicher liegt hinter einem schmalen Interface, das genau dafür gezogen wurde: builtin (AES-GCM in Postgres) oder ein externer Anbieter. Der eingebaute Weg ist die Voreinstellung, damit eine Installation ohne Vault vollständig ist.'
  - q: Sieht der Agent meinen API-Schlüssel?
    a: 'Den Modell-Schlüssel bekommt die Runtime für den Lauf gestellt — ohne ihn kann sie das Modell nicht rufen. Zugänge zu Zielsystemen dagegen laufen über den Aktions-Proxy: Der Agent nennt die Aktion, den Token setzt die Control Plane ein.'
  - q: Was passiert bei einem abgelaufenen Token?
    a: 'Er wird beim Hinterlegen geprüft und im Betrieb erkannt: Ein abgelehnter Wert wird geparkt, der Agent weicht auf eine andere Kapazität aus, wenn es eine gibt, und im Recording steht der Grund.'
---

# Identität & Secrets

Die unangenehmste Frage an jede Agenten-Installation lautet: Wo liegt eigentlich das Passwort, mit dem der Agent ins Ticketsystem kommt? Die übliche Antwort — in einer Umgebungsvariable im Container — ist der Grund, warum die Sicherheitsabteilung Nein sagt.

## Der Grundsatz

**Niemals langlebige Secrets in die Sandbox.** Zugänge werden zur Laufzeit gebrokert, kurzlebig und auf den Zweck geschnitten. Was der Agent bekommt, gilt für diesen Lauf und für dieses System — nicht darüber hinaus.

## Agenten-Identität

Ein Agent ist ein eigenständiges Subjekt, kein Prozess unter einem Sammelkonto. Er hat eine Identität, an der Zugänge, Org-Chart-Position und jede Spur hängen. In Zielsystemen tritt er unter eigenem Namen auf — nachvollziehbar bis in die Kommentarspalte eines Tickets.

## Menschen und Rollen

Auf derselben Organisation arbeiten Menschen mit verschiedenen Rollen: Org-Admin, Agenten-Eigentümer, Betrachter, Revision. Wer welche Agenten sieht, wer Secrets hinterlegen darf, wer Freigaben erteilen kann — das entscheidet die Rolle, und jede administrative Handlung steht im Audit-Trail.

Gespräche sind die Ausnahme von „das entscheidet die Rolle“: Ein Gespräch lesen und schreiben seine Mitglieder, welche Rolle sie auch haben, und sonst niemand. Nur Org-Admin und Revision lesen und exportieren Gespräche, in denen sie nicht sind, in der Revision — neben den gemeinsamen Verläufen je Agent aus der Zeit, bevor Gespräche Mitglieder hatten.

Jedes Mitglied darf KI-Kollegen direkt schreiben. Wen es erreicht, ist eine Einstellung der Organisation (*Verwaltung → Wer welchem Kollegen schreibt*): jeden Kollegen oder nur die der eigenen Abteilung; wem der Kollege berichtet und der Org-Admin immer. Wird das enger, bleibt ein bestehendes Gespräch lesbar und nimmt keine neue Nachricht mehr an. Einen KI-Kollegen in eine Gruppe zu holen, bleibt den Rollen, die Arbeit von Hand übergeben dürfen, ebenso das Beantworten einer wartenden Aufgabe außerhalb des eigenen Gesprächs.

Angemeldet wird eingebaut über JWT und Argon2id. Wer ein Unternehmens-Login will, hängt einen OIDC-Anbieter an dieselbe Schnittstelle — Keycloak, Entra, was im Haus steht.

## Wie Secrets gespeichert werden

In Postgres-Spalten, verschlüsselt mit AES-GCM. Der Schlüssel ist der `COVEY_MASTER_KEY` der Instanz; er steht in der Umgebung, nicht in der Datenbank. Die Verschlüsselung bindet jeden Wert zusätzlich an seine Organisation, damit ein Ciphertext nicht in einer anderen Organisation gelesen werden kann.

Praktische Folge: Der Master-Key ist so wichtig wie das Datenbank-Passwort, und ein Verlust ist endgültig. Sichern.

## Kein Klartext in der Oberfläche

Ein hinterlegtes Secret lässt sich ersetzen, aber nicht wieder anzeigen. Das ist Absicht — ein Wert, den man auslesen kann, wandert früher oder später in eine Chat-Nachricht.

## Prüfung beim Hinterlegen

Bekannte Credentials werden beim Speichern sofort gegen ihr System geprüft. Ein abgelaufener Abo-Token fällt damit an der Stelle auf, an der man ihn eingibt — und nicht eine Stunde später als rätselhafter Fehlschlag im Lauf eines Agenten.

## Secrets in den Parametern einer Aktion

Manche Zugänge gehören zu keinem Zielsystem: ein Login für eine Staging-Umgebung, ein Token für eine private Paket-Registry. Einen solchen Wert hinterlegt man als Secret des Agenten oder als Organisations-Secret, das ihm zugewiesen ist, und schreibt den Platzhalter `{{secret:<key>}}` in die Parameter einer Aktion. Der Action-Proxy ersetzt ihn in der Sandbox, nachdem das Modell den Aufruf abgeschickt hat und bevor das Plugin läuft. Das Modell sieht nur den Platzhalter, das Audit-Log ebenso. Für einen Agenten, dem das Secret weder gehört noch zugewiesen ist, wird nichts aufgelöst.

Der häufigste Fall ist die Installation der Abhängigkeiten eines Projekts aus einer privaten Quelle. `dev:exec` führt seinen Befehl über eine Shell aus, der Wert landet also in der Umgebung genau dieses einen Prozesses, und nichts wird ins Home geschrieben:

```json
{"action": "dev:exec", "params": {"cmd": "NPM_TOKEN={{secret:npm_token}} npm ci"}}
```

```json
{"action": "dev:exec", "params": {"cmd": "COMPOSER_AUTH='{{secret:composer_auth}}' composer install"}}
```

Eine `git:`-Abhängigkeit in einem privaten Repository (in `pubspec.yaml`, `package.json` oder `composer.json`) bekommt ihren Zugang für einen git-Aufruf über gits Umgebungskonfiguration, nicht über `.gitconfig`:

```json
{"action": "dev:exec", "params": {"cmd": "GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0='url.https://oauth2:{{secret:git_deps_token}}@gitlab.example.org/.insteadOf' GIT_CONFIG_VALUE_0='https://gitlab.example.org/' flutter pub get"}}
```

Drei Grenzen:

- **Nur in Aktionen.** Der Platzhalter wird in Aktionen aufgelöst, die über den Action-Proxy laufen (`dev:exec`, `dev:start`, `browser:type`, …). Im eigenen Shell-Werkzeug der Runtime landet er wörtlich im Befehl.
- **Die Ausgabe wird nicht maskiert.** Was der Befehl ausgibt, geht an das Modell zurück. Ein Werkzeug, das seinen Zugang wiederholt, etwa eine Fehlermeldung mit der URL, trägt den Wert in den Kontext des Modells.
- **Der Prozess kann ihn lesen.** Die Install-Skripte des Repositorys laufen in derselben Umgebung und können den Token lesen. Einen Token nehmen, der nur Pakete lesen darf.

## Kapazität statt Passwort-Zettel

Mehrere Zugänge zur selben Engine — drei Abo-Sitze und ein API-Schlüssel — sind keine Sicherheitsfrage, sondern eine kaufmäntische. covey bildet sie als [Arbeitsplatz mit Merit Order](../introduction/core-concepts.md) ab: bezahltes Kontingent zuerst, Verbrauchsabrechnung als Spitzenlast.

## Weiter

- [Guard-Rails & Kontrolle](guard-rails.md) — was mit dem Zugang getan werden darf
- [Zielsysteme & Plugins](../integrations/target-systems.md) — welche Systeme Zugänge brauchen
- [Betrieb & Deployment](../operations/operations.md) — Master-Key, HTTPS, Sicherungen
