# Betrieb: Covey an Nextcloud anschließen

Praktisches Runbook für das `nextcloud`-Plugin: eine Nextcloud-Dateiablage,
in der der Agent Dateien listet, liest, bearbeitet und ablegt. Technische
Grundlage ist **WebDAV** — anders als beim SharePoint-Plugin gibt es keinen
OAuth-Flow. Der einfachste Fall ist genau das, was der Titel verspricht:
**einem Bot einen Freigabelink schicken**, den Rest macht das Plugin.

> Kurzfassung: Ordner in Nextcloud teilen (Link, „Bearbeiten erlauben",
> Passwort), zwei Secrets setzen, ACCESS.md freischalten. Nextcloud hat hier
> **keinen Webhook** — braucht der Agent einen Eingangsordner als
> Arbeitsvorrat, läuft der Intake per Polling über `HEARTBEAT.md`.

---

## 1. Überblick des Datenflusses

```
Nextcloud      ◄──(WebDAV, HTTPS)──────────  Agent (Sandbox, Claude Code)
(Dateiablage)      list, read, write,          │ Aktionen über den Action-Proxy,
                   download, upload,           │ Credentials pro Aufruf gebrokert
                   mkdir, delete               │ (nextcloud_url + nextcloud_token)
```

Zwei Betriebsarten, allein an `nextcloud_url` erkannt:

- **Freigabelink** (`https://host/s/<token>`): WebDAV über
  `/public.php/webdav/`, Basic-Auth mit dem Share-Token als Benutzer und dem
  Share-Passwort als Passwort. Der geteilte Ordner ist die Wurzel.
- **Konto-Zugang** (Server-Basis-URL): WebDAV über
  `/remote.php/dav/files/<user>/`, Basic-Auth mit Benutzer und App-Passwort.
  Das ganze Datei-Verzeichnis des Nutzers ist die Wurzel.

Alle Pfade der Aktionen sind **relativ zu dieser Wurzel**; ein Ausbruch per
`..` wird daemon-seitig abgewiesen — der Agent sieht nur, was der Link bzw.
das Konto freigibt.

---

## 2. Schritt-für-Schritt-Anleitung

### A) Öffentlicher Freigabelink (empfohlen)

1. In Nextcloud den Ordner öffnen, den der Agent bearbeiten soll →
   **Teilen → „Link teilen"**. Berechtigung **„Bearbeiten erlauben"**
   setzen (sonst darf der Agent nur lesen). Dringend empfohlen: ein
   **Passwort** auf die Freigabe legen — der Link allein ist sonst der
   ganze Zugang.
2. Freigabelink kopieren, Form `https://cloud.example.com/s/AbCdEf`.
3. Unter **Secrets** hinterlegen und dem Agenten zuweisen:
   - `nextcloud_url` = der Freigabelink aus Schritt 2
   - `nextcloud_token` = das Share-Passwort — **oder `-`**, wenn die
     Freigabe kein Passwort hat (der Broker verlangt einen Wert; `-`,
     `none`, `anonymous`, `public`, `x` gelten als „kein Passwort").

### B) Konto-Zugang (ganzes Datei-Verzeichnis)

1. In Nextcloud → **Einstellungen → Sicherheit → „App-Passwort erzeugen"**.
   Nie das Login-Passwort in Covey hinterlegen.
2. Unter **Secrets** hinterlegen und dem Agenten zuweisen:
   - `nextcloud_url` = die Server-Basis-URL, z. B. `https://cloud.example.com`
     (ein Unterverzeichnis wie `/nextcloud` bleibt erhalten)
   - `nextcloud_token` = `benutzer:app-passwort`

### Für beide Wege

3. In der **ACCESS.md** des Agenten freischalten:
   ```
   - system: nextcloud scope: read,write
   ```
4. **Egress:** den Nextcloud-Host (z. B. `cloud.example.com`) als
   Egress-Host der Org hinterlegen — sonst erreicht die Sandbox ihn nicht.
5. Optionaler **Intake per Heartbeat** — in der `HEARTBEAT.md` des Agenten:
   ```
   - alle: 30m titel: Ablage sichten aufgabe: Liste mit list den
     Eingangsordner und bearbeite neue Dateien nach Playbook.
   ```

---

## 3. Die Aktionen im Überblick

| Aktion | Parameter | Wirkung |
|---|---|---|
| `list` | `path` (optional) | Dateien/Ordner listen (max. 500 pro Aufruf) |
| `read` | `path` | Textdatei direkt liefern (bis 1 MB, nur UTF-8) |
| `write` | `path`, `content` | Textdatei anlegen/überschreiben (fehlende Ordner werden angelegt) |
| `download` | `path`, `to` (optional) | Datei in die Sandbox holen (Default `nextcloud/<pfad>`) |
| `upload` | `from`, `to` (optional) | Datei aus der Sandbox ablegen (ersetzt Vorhandenes) |
| `mkdir` | `path` | Ordnerpfad anlegen (`mkdir -p`) |
| `delete` | `path` | Datei/Ordner löschen (Wurzel ist tabu) |

Binär- und Office-Dateien (docx, xlsx, pdf, …) laufen immer über
`download` → lokal bearbeiten → `upload`; `read`/`write` sind für reine
Textdateien gedacht. Uploads sind auf 250 MB begrenzt
(`COVEY_NEXTCLOUD_UPLOAD_MAX_MB` im Daemon-Env übersteuert das Limit).
`write`/`upload` in einen noch nicht existenten Ordner legen die fehlenden
Zwischenordner automatisch an (Nextcloud tut das beim PUT nicht selbst).

---

## 4. Sicherheitsmodell

- **Kein langlebiges Secret in der Sandbox:** Share-Token/App-Passwort leben
  nur im RAM des Daemons und werden pro Aktion gebrokert. Die Runtime (der
  LLM-Prozess) sieht nie ein Secret.
- **Guard-Rails pro Aktion:** jedes Subjekt heißt `nextcloud:<aktion>` —
  `nextcloud:delete` oder `nextcloud:write` lassen sich damit gezielt auf
  Approval-Pflicht setzen, während `nextcloud:list`/`read` frei bleiben.
- **Pfad-Härtung:** Remote-Pfade werden normalisiert (`..` abgewiesen),
  lokale Pfade gegen das Sandbox-Arbeitsverzeichnis aufgelöst — kein
  Ausbruch in `/home` oder das Host-Dateisystem.
- **Least Privilege:** Der Freigabelink begrenzt den Agenten von sich aus auf
  genau einen Ordner; das ist der bevorzugte Weg gegenüber dem Konto-Zugang,
  der das ganze Datei-Verzeichnis öffnet. Freigabe-Passwort setzen.

---

## 5. Typische Fehlerbilder

| Symptom | Ursache | Abhilfe |
|---|---|---|
| `HTTP 401` bei jeder Aktion | Share-Passwort bzw. `benutzer:app-passwort` falsch | `nextcloud_token` prüfen; bei passwortloser Freigabe `-` setzen |
| `HTTP 403`/`404` beim Schreiben | Freigabe ist nur „Ansehen" (kein Bearbeiten) | Freigabe auf „Bearbeiten erlauben" umstellen |
| `HTTP 404 — Pfad nicht gefunden` | Pfad existiert nicht (relativ zur Wurzel!) | mit `list` den tatsächlichen Baum prüfen |
| Aktion hängt/Timeout | Nextcloud-Host nicht im Egress freigegeben | Egress-Host der Org ergänzen |
| Konto-Zugang findet keine Dateien | falscher Benutzer im `benutzer:app-passwort` | Benutzernamen prüfen — der Datei-Root ist `/remote.php/dav/files/<user>/` |
