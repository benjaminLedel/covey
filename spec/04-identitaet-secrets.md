# 04 — Identität & Secrets

Der schwierige und zugleich verteidigbarste Teil der Plattform — und er liegt genau in der bestehenden Kernkompetenz (Keycloak/OAuth aus dem Girona-Setup).

## Grundregel

**Niemals langlebige Secrets in die Sandbox backen.** Ein Agent hält keine Passwörter oder API-Keys für Zielsysteme. Stattdessen präsentiert er seine Identität und bekommt zur Laufzeit ein **kurzlebiges, gescoptes Access-Token** für das jeweilige Zielsystem.

## Komponenten

- **Keycloak** als Agent-Identity-Provider. Jeder Agent hat eine Maschinen-Identität (Client/Service-Account) im Realm.
- **Secrets-Broker** als Vermittler zwischen Agent-Identität und Zielsystem-Zugriff. Nutzt einen Secret-Store (Vault oder Infisical) für die dahinterliegenden Langzeit-Credentials/Client-Secrets, die **nie** an den Agenten gehen.

> **Zwei Identitäts-Ebenen.** Dieses Dokument behandelt die **Agent-Identität**. Davon getrennt (aber im selben IdP verwaltbar) steht die **Mensch-Identität**: Menschen melden sich per **SSO (SAML/OIDC)** an, daran hängt ihr RBAC. Details in [`09-enterprise-modell.md`](09-enterprise-modell.md). Handelt ein Agent im Auftrag eines Menschen, bleibt die Delegationskette (Mensch → Agent → Zielsystem) im Audit erhalten.

> **Pluggable — Built-in als Default.** Broker und IdP sind hinter schmalen Interfaces (`IdentityProvider`, `SecretStore`) austauschbar. Der MVP bringt eine **simple, DB-gestützte Built-in-Implementierung** mit (signierte JWTs, AES-GCM-verschlüsselte Secrets in Postgres); wer Keycloak oder Vault hat, konfiguriert stattdessen den externen Provider. Keycloak und Vault sind damit **optional, nicht Voraussetzung**. Die eine Grenze: echter RFC-8693-Token-Exchange gegen Drittsysteme läuft nur über den externen `oidc`-Provider, nicht über die Built-in-Variante. Details in [`10-architektur-stack.md`](10-architektur-stack.md).

## Token-Exchange-Flow (RFC 8693)

Der Broker setzt **RFC 8693 Token Exchange** ein — dasselbe Muster wie im Girona-Setup (zusammen mit RFC 9728 Protected Resource Metadata für die Ressourcen-Discovery).

```
1. Agent (Daemon) braucht Zugriff auf System Z
        │
        ▼  request_credential(system=Z, scope=…)
2. Control Plane / Broker prüft:
        - ist der Agent laut ACCESS.md für Z + scope berechtigt?
        - greift eine Approval-Policy?
        │  ja
        ▼
3. Broker tauscht die Agent-Identität (subject_token) gegen ein
   gescoptes Access-Token für Z (RFC 8693 token exchange bei Keycloak)
        │
        ▼  inject_credentials(token, ttl=kurz)
4. Daemon nutzt das Token für genau diesen Zugriff.
   Token läuft nach kurzer TTL ab.
```

Kernpunkte:

- Das **subject_token** ist die Identität des Agenten, nicht ein geteiltes Geheimnis.
- Das ausgestellte Token ist **auf System + Scope + kurze TTL beschränkt**.
- Der Broker entscheidet, **nie der Daemon**. Berechtigung wird zentral geprüft (gegen `ACCESS.md` + Policies).
- Langzeit-Credentials der Zielsysteme bleiben im Secret-Store, unsichtbar für den Agenten.

## Threat-Model

Agenten mit echten Zugängen sind eine Angriffsfläche. Das ist von Anfang an mitzudenken, sonst baut man eine wunderbare Exfiltrations-Maschine.

### Hauptbedrohung: Prompt-Injection → Datenexfiltration über legitime Zugänge

Ein Support-Agent hat legitimen Zugriff auf Tickets + Confluence + Teams. Wird er über **präparierten Input** (z. B. ein bösartiges Ticket, eine manipulierte Mail) prompt-injected, kann er dazu gebracht werden, Daten über seine *legitimen* Kanäle abzuziehen oder schädliche Aktionen auszuführen. Das ist ein handfester Security-Incident, kein Randfall.

### Gegenmaßnahmen

Das Leitprinzip aller Gegenmaßnahmen: **sich nicht auf die Bravheit des Agenten verlassen.** Da genau das Reasoning des Agenten kompromittiert sein kann, müssen die Schutzgrenzen *außerhalb* der Runtime greifen — als zentrale, plattform-erzwungene **Guard-Rails** (siehe [`06-observability-control.md`](06-observability-control.md)). Was in `SOUL.md` als „Grenze" steht, ist dafür nicht ausreichend; es ist Verhaltenssteuerung, keine Sicherheitsgrenze.

1. **Zentrale Guard-Rails (plattform-erzwungen).** Verbotene Systeme/Tools, Egress-Regeln, verbotene Aktionen und Approval-Pflichten werden am Broker, am Egress und im Tool-Layer durchgesetzt — vom Agenten nicht umgehbar. Fail-closed.
2. **Least Privilege / enge Scopes.** Ein Agent bekommt nur die minimal nötigen Rechte, mit möglichst engem Scope und kurzer TTL. Kein Dauer-Vollzugriff.
3. **Approval-Gates für riskante Aktionen.** Externe Mail raus, Löschen, Massen-Operationen brauchen Freigabe (siehe [`06-observability-control.md`](06-observability-control.md)).
4. **Egress-Kontrolle.** Ausgehende Kanäle des Agenten (v. a. Kommunikation nach außen) werden überwacht und ggf. gegated — nicht jeder Zieladressat ist erlaubt.
5. **Trennung von Daten und Instruktion.** Eingehender Content (Ticket-Text, Mail-Body) wird der Runtime als *Daten* präsentiert, nicht als Instruktion; Adapter/Prompt-Design müssen das durchhalten.
6. **Lückenloses Recording + Supervisor.** Jede Aktion ist nachvollziehbar; ein Supervisor-Agent flaggt anomales Verhalten (ungewöhnliche Zugriffsmuster, untypische Empfänger).
7. **Kill-Switch.** Ein aus dem Ruder laufender Agent lässt sich sofort anhalten — einzeln oder flottenweit.

### Weitere Vektoren (Kurzliste)

- **Token-Leakage** aus der Sandbox → durch kurze TTL + Scope begrenzt.
- **Privilege-Creep** über die Zeit → periodisches Access-Review gegen `ACCESS.md`.
- **Kompromittierte Sandbox** → Isolation + ephemere Compute begrenzen den Blast-Radius; kritischer Zustand liegt nicht in der Sandbox.
- **Agent-zu-Agent-Missbrauch** (ein injizierter Agent delegiert bösartig an andere) → Inter-Agent-Nachrichten unterliegen denselben Recording-/Policy-Regeln.

## Verteidigbarkeit

Sichere Agent-Identität mit Token-Exchange ist ein hartes Problem, das die meisten Plattformen nicht sauber lösen — und es liegt genau da, wo bereits tiefe Erfahrung besteht (Keycloak, RFC 8693, RFC 9728). Zusammen mit dem Knowledge-Graph-Gedächtnis (siehe [`05-gedaechtnis.md`](05-gedaechtnis.md)) sind das die zwei Bausteine, die die Plattform verteidigbar machen.
