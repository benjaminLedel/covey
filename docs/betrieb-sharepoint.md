# Betrieb: Covey an SharePoint / Teams-Dateien anschließen

Praktisches Runbook für das `sharepoint`-Plugin: eine **per Freigabelink
bereitgestellte Dokumentbibliothek** (SharePoint-Site oder Dateien-Tab eines
Teams-Kanals), in der der Agent Dateien listet, liest, bearbeitet und ablegt.
Technische Grundlage ist die Microsoft-Graph-API; die Dateien eines
Teams-Kanals liegen ohnehin in der SharePoint-Site des Teams — derselbe
Mechanismus deckt beide Fälle ab.

> Kurzfassung: Entra-ID-App registrieren, Freigabelink kopieren, zwei
> Secrets setzen, ACCESS.md freischalten. SharePoint hat hier **keinen
> Webhook** — braucht der Agent einen Eingangsordner als Arbeitsvorrat,
> läuft der Intake per Polling über `HEARTBEAT.md`.

---

## 1. Überblick des Datenflusses

```
SharePoint /   ◄──(Microsoft Graph, HTTPS)──  Agent (Sandbox, Claude Code)
Teams-Dateien       list, read, write,          │ Aktionen über den Action-Proxy,
                    download, upload,           │ Credentials pro Aufruf gebrokert
                    mkdir, delete               │ (sharepoint_url + sharepoint_token)
Entra ID       ◄──(Client-Credentials-Flow)────┘ Bearer-Token, im Daemon gecacht
```

Der hinterlegte **Freigabelink** wird über den Graph-`/shares`-Endpoint zur
Dokumentbibliothek aufgelöst; alle Pfade in den Aktionen sind **relativ zu
dieser Wurzel**. Ein Ausbruch per `..` wird daemon-seitig abgewiesen — der
Agent sieht nur, was der Link freigibt (plus das, was die App-Berechtigung
technisch erlaubt, siehe Least Privilege unten).

---

## 2. Schritt-für-Schritt-Anleitung

### 2.1 In Entra ID: App-Registrierung für den Agenten

1. Azure-Portal → **App-Registrierungen** → neue App (z. B. `covey-agent`).
2. **API-Berechtigungen** → Microsoft Graph → **Anwendungsberechtigungen**
   (Application, nicht Delegated) → `Files.ReadWrite.All` → **Admin-Zustimmung
   erteilen**.
3. **Zertifikate & Geheimnisse** → neues **Client-Secret** erzeugen und den
   Wert sofort notieren (er ist später nicht mehr einsehbar). Ablaufdatum im
   Kalender vermerken — nach Ablauf schlagen alle Aktionen mit
   `invalid_client` fehl.
4. Notieren: **Verzeichnis-ID (Tenant)**, **Anwendungs-ID (Client)** und das
   Secret.

**Least Privilege:** `Files.ReadWrite.All` erlaubt der App technisch Zugriff
auf alle Sites des Tenants. Wer das nicht will, nimmt stattdessen
`Sites.Selected` und erteilt der App per Graph gezielt Zugriff auf die eine
Site (`POST /sites/{site-id}/permissions` mit der App als `grantedToIdentities`,
Rolle `write`) — das Plugin funktioniert mit beiden Varianten unverändert.

### 2.2 In SharePoint / Teams: Freigabelink kopieren

- **SharePoint:** den Ordner bzw. die Dokumentbibliothek öffnen →
  **Link kopieren**.
- **Teams:** im Kanal den **Dateien**-Tab öffnen (ggf. in den gewünschten
  Unterordner wechseln) → **Link kopieren**.

Der Link muss auf einen **Ordner** zeigen — ein Link auf eine einzelne Datei
wird beim Auflösen abgewiesen. Auch ein einfacher Browser-URL des Ordners
funktioniert; der Graph-`/shares`-Endpoint akzeptiert beide Formen.

### 2.3 In Covey: Secrets hinterlegen

| Secret | Wert | Zweck |
|---|---|---|
| `sharepoint_url` | der Freigabelink aus 2.2 | Wurzel der Ablage |
| `sharepoint_token` | `tenant-id:client-id:client-secret` | Client-Credentials-Tripel |

Beide Secrets dem Agenten **zuweisen** (org-weite Secrets erreichen einen
Agenten nur bei expliziter Zuweisung).

Optionale Endpunkt-Overrides in `sharepoint_url` (durch Leerzeichen
getrennt, für nationale Clouds oder Tests):

```
sharepoint_url = https://contoso.sharepoint.com/:f:/s/TeamX/AbCdEf…
                 graph=https://graph.microsoft.de login=https://login.microsoftonline.de
```

Für Demos/Tests gegen ein Graph-Double kann `sharepoint_token` statt des
Tripels auch ein **fertiger Bearer-Token** sein (jeder Wert ohne die zwei
Doppelpunkte) — dann entfällt der OAuth-Flow.

### 2.4 ACCESS.md des Agenten

```
- system: sharepoint scope: read,write
```

### 2.5 Egress-Freigabe

Aus der Sandbox müssen erreichbar sein:

- `graph.microsoft.com` (bzw. der `graph=`-Override)
- `login.microsoftonline.com` (bzw. der `login=`-Override)
- `*.sharepoint.com` — Datei-Downloads laufen über einen Redirect auf eine
  vorautorisierte SharePoint-URL

### 2.6 Optional: Intake per Heartbeat

SharePoint hat in Covey **keinen Webhook-Eingang** (Graph-Change-Notifications
brauchen eine öffentlich validierte HTTPS-Subscription — bewusst nicht im
MVP). Soll der Agent einen Eingangsordner eigenständig abarbeiten, in der
`HEARTBEAT.md`:

```
- alle: 30m titel: Ablage sichten aufgabe: Liste mit list den Ordner
  "Eingang", verarbeite neue Dateien nach Playbook und verschiebe
  Erledigtes per download + upload nach "Archiv" (Original mit delete
  entfernen).
```

Weil es keinen Webhook gibt, gilt wie beim E-Mail-Plugin: **kein
`blocked`-Status** auf SharePoint-Ereignisse — der Lauf endet regulär mit
`done`, der nächste Heartbeat sichtet erneut.

---

## 3. Die Aktionen im Überblick

| Aktion | Parameter | Wirkung |
|---|---|---|
| `list` | `path` (optional) | Dateien/Ordner listen (max. 200 pro Aufruf) |
| `read` | `path` | Textdatei direkt liefern (bis 1 MB, nur UTF-8) |
| `write` | `path`, `content` | Textdatei anlegen/überschreiben |
| `download` | `path`, `to` (optional) | Datei in die Sandbox holen (Default `sharepoint/<pfad>`) |
| `upload` | `from`, `to` (optional) | Datei aus der Sandbox ablegen (ersetzt Vorhandenes) |
| `mkdir` | `path` | Ordnerpfad anlegen (`mkdir -p`) |
| `delete` | `path` | Datei/Ordner löschen (Wurzel ist tabu) |

Binär- und Office-Dateien (docx, xlsx, pdf, …) laufen immer über
`download` → lokal bearbeiten → `upload`; `read`/`write` sind für reine
Textdateien gedacht. Simple-Uploads sind auf 250 MB begrenzt
(`COVEY_SHAREPOINT_UPLOAD_MAX_MB` im Daemon-Env übersteuert das Limit;
größere Dateien bräuchten eine Graph-Upload-Session — nicht im MVP).

---

## 4. Sicherheitsmodell

- **Kein langlebiges Secret in der Sandbox:** Tripel und Bearer-Token leben
  nur im RAM des Daemons; der Token wird mit Sicherheitsmarge gecacht und
  vor Ablauf erneuert. Die Runtime (der LLM-Prozess) sieht nie ein Secret.
- **Guard-Rails pro Aktion:** jedes Subjekt heißt `sharepoint:<aktion>` —
  `sharepoint:delete` oder `sharepoint:write` lassen sich damit gezielt auf
  Approval-Pflicht setzen, während `sharepoint:list`/`read` frei bleiben.
- **Pfad-Härtung:** Remote-Pfade werden normalisiert (`..` abgewiesen),
  lokale Pfade gegen das Sandbox-Arbeitsverzeichnis aufgelöst — kein
  Ausbruch in `/home` oder das Host-Dateisystem.

---

## 5. Typische Fehlerbilder

| Symptom | Ursache | Abhilfe |
|---|---|---|
| `invalid_client` beim Token-Holen | Client-Secret falsch oder abgelaufen | neues Secret erzeugen, `sharepoint_token` aktualisieren |
| `HTTP 403 accessDenied` beim Auflösen | Admin-Zustimmung fehlt oder `Sites.Selected` ohne Site-Grant | Zustimmung erteilen bzw. Site-Permission anlegen |
| „Freigabelink zeigt auf eine Datei" | Link auf Datei statt Ordner kopiert | Ordner-/Bibliotheks-Link hinterlegen |
| `itemNotFound` bei Aktionen | Pfad existiert nicht (relativ zur Link-Wurzel!) | mit `list` den tatsächlichen Baum prüfen |
| Download bricht ab | `*.sharepoint.com` nicht im Egress freigegeben | Egress-Freigabe ergänzen |
