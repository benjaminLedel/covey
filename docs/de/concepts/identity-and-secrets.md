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

Angemeldet wird eingebaut über JWT und Argon2id. Wer ein Unternehmens-Login will, hängt einen OIDC-Anbieter an dieselbe Schnittstelle — Keycloak, Entra, was im Haus steht.

## Wie Secrets gespeichert werden

In Postgres-Spalten, verschlüsselt mit AES-GCM. Der Schlüssel ist der `COVEY_MASTER_KEY` der Instanz; er steht in der Umgebung, nicht in der Datenbank. Die Verschlüsselung bindet jeden Wert zusätzlich an seine Organisation, damit ein Ciphertext nicht in einer anderen Organisation gelesen werden kann.

Praktische Folge: Der Master-Key ist so wichtig wie das Datenbank-Passwort, und ein Verlust ist endgültig. Sichern.

## Kein Klartext in der Oberfläche

Ein hinterlegtes Secret lässt sich ersetzen, aber nicht wieder anzeigen. Das ist Absicht — ein Wert, den man auslesen kann, wandert früher oder später in eine Chat-Nachricht.

## Prüfung beim Hinterlegen

Bekannte Credentials werden beim Speichern sofort gegen ihr System geprüft. Ein abgelaufener Abo-Token fällt damit an der Stelle auf, an der man ihn eingibt — und nicht eine Stunde später als rätselhafter Fehlschlag im Lauf eines Agenten.

## Kapazität statt Passwort-Zettel

Mehrere Zugänge zur selben Engine — drei Abo-Sitze und ein API-Schlüssel — sind keine Sicherheitsfrage, sondern eine kaufmäntische. covey bildet sie als [Arbeitsplatz mit Merit Order](../introduction/core-concepts.md) ab: bezahltes Kontingent zuerst, Verbrauchsabrechnung als Spitzenlast.

## Weiter

- [Guard-Rails & Kontrolle](guard-rails.md) — was mit dem Zugang getan werden darf
- [Zielsysteme & Plugins](../integrations/target-systems.md) — welche Systeme Zugänge brauchen
- [Betrieb & Deployment](../operations/operations.md) — Master-Key, HTTPS, Sicherungen
