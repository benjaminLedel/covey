# Betrieb: Covey-Agenten einen Browser geben

Praktisches Runbook für das `browser`-Plugin: ein **vollwertiger headless
Chrome** als universeller Adapter für Web-Anwendungen, die kein eigenes
Zielsystem-Plugin haben. Der Agent navigiert, liest Seiteninhalt, macht
Screenshots, klickt und tippt — wie ein Nutzer, nur ohne sichtbares Fenster.
Getrieben wird ein echter Chromium über das DevTools-Protokoll (chromedp,
reines Go); „headless" heißt gleiche Engine, volles Rendering.

> Kurzfassung: Plugin aktivieren, in `ACCESS.md` freischalten, die Ziel-Hosts
> in die **Egress-Allowlist** aufnehmen. Keine Secrets nötig — der Browser
> läuft lokal in der Sandbox.

---

## 1. Wann der Browser, wann ein Plugin?

- **Eigenes Zielsystem-Plugin** (Zammad, Nextcloud, GitLab …) für Systeme, die
  eine erstklassige, fein-guard-railte Integration wert sind: stabile
  Aktionen, saubere `system:aktion`-Guard-Rails, gebrokerte Credentials.
- **Browser** für den **langen Schwanz**: interne Web-Tools, Portale und
  Dashboards, für die sich kein Plugin lohnt. Anwendungs-agnostisch — was ein
  Mensch im Browser bedienen kann, kann der Agent auch, ohne API oder Plugin.

Der Preis: pro-Aktion-Guard-Rails greifen im Browser gröber (ein Klick ist
opak). Die **Egress-Allowlist** ist hier die harte, tragende Grenze.

## 2. Die Aktionen im Überblick

| Aktion | Parameter | Wirkung |
|---|---|---|
| `navigate` | `url` | Seite laden, liefert Titel + finale URL (nur http/https) |
| `content` | `selector` (optional) | Sichtbaren Text liefern (ganze Seite oder per CSS-Selektor, bis 20k Zeichen) |
| `screenshot` | `to` (optional), `full` (optional) | PNG in die Sandbox schreiben (Default `browser/shot-N.png`); `full=true` = ganze scrollbare Seite |
| `click` | `selector` | Element klicken |
| `type` | `selector`, `text` | Text in ein Feld tippen |

Die Browser-Sitzung **bleibt über mehrere Aktionen bestehen** (Cookies/Login
bleiben erhalten) und wird beim Einschlafen der Sandbox beendet. Screenshots
liest der Agent anschließend als Bild — so „sieht" er die Seite.

## 3. Einrichtung

1. Plugin in der Org aktivieren (keine Secrets).
2. In der `ACCESS.md` des Agenten:
   ```
   - system: browser scope: navigate,content,screenshot,click,type
   ```
3. **Egress:** jeden Host, den der Agent aufrufen soll, in die
   Egress-Allowlist der Org aufnehmen. Ohne Freigabe lädt keine Seite —
   fehlgeschlagene Navigation ist fast immer eine fehlende Egress-Regel.
4. **Sandbox-Image:** muss `chromium` enthalten (im mitgelieferten
   `Dockerfile.sandbox` bereits ergänzt). `COVEY_BROWSER_CHROME_PATH`
   übersteuert den Browser-Pfad, falls nötig.

## 4. Sicherheitsmodell

- **Kein langlebiges Secret in der Sandbox:** Der Browser bekommt keine
  gebrokerten Credentials automatisch. Muss sich der Agent bei einer Web-App
  anmelden, kommen die Zugangsdaten kurzlebig und gescopt über den Broker
  (z. B. als hinterlegtes Login, das der Agent in ein Formular tippt) — nie
  dauerhaft im Browser-Profil.
- **Egress ist die harte Grenze:** Der Browser ist das mächtigste
  Egress-Werkzeug überhaupt; die Sandbox-Netzsperre gatet jeden Request.
- **Guard-Rails pro Aktion:** Subjekte `browser:navigate`, `browser:click`,
  `browser:type`, `browser:content`, `browser:screenshot` — z. B.
  `browser:type` approval-pflichtig setzen, während `browser:navigate`/
  `content` frei bleiben.
- **Kein lokaler Dateizugriff:** `navigate` erlaubt nur `http`/`https` —
  `file://` & Co. werden abgewiesen.
- **`--no-sandbox`:** Chromiums eigener Setuid-Sandbox braucht Kernel-Caps,
  die der Container nicht hat; da der Container selbst die Isolationsgrenze
  ist, läuft Chromium mit `--no-sandbox`. Die harte Grenze bleibt die
  Sandbox, nicht der browsereigene Sandbox.

## 5. Grenzen & Ausbau

- **Anti-Bot/CAPTCHA:** Manche Seiten blocken Automatisierung. Die
  DOM-Steuerung über CDP stößt dort an Grenzen; die spätere Pixel-Ebene
  (Computer-Use-Runtime, `spec/16`) fährt da besser, weil sie wie ein echter
  Nutzer aussieht.
- **Sichtbares Fenster / Live-View:** `COVEY_BROWSER_HEADFUL` startet Chromium
  im sichtbaren Modus — braucht dann einen X-Server/Xvfb im Image. Das ist die
  Brücke zur Live-View/Takeover-Ausbaustufe (noVNC).
- **Timeout:** `COVEY_BROWSER_TIMEOUT_SECS` deckelt eine einzelne Aktion
  (Default 45 s).

## 6. Typische Fehlerbilder

| Symptom | Ursache | Abhilfe |
|---|---|---|
| „chromium start … " | Chromium fehlt im Image / falscher Pfad | `chromium` installieren bzw. `COVEY_BROWSER_CHROME_PATH` setzen |
| Navigation hängt/timeout | Ziel-Host nicht in der Egress-Allowlist | Egress-Regel ergänzen |
| `nur http/https erlaubt` | `file://`/`javascript:`-URL | echte Web-URL verwenden |
| Klick findet nichts | Selektor unsichtbar/nicht vorhanden | mit `content`/`screenshot` prüfen, Selektor korrigieren |
