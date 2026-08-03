# Betrieb: Automatisches Deployment (main → Host)

Jeder Push auf `main` rollt Covey automatisch auf einen Zielhost aus. Dieses
Runbook beschreibt die einmalige Einrichtung und was im laufenden Betrieb
passiert.

> Für lokales Ausprobieren ohne CI siehe stattdessen
> [`schnellstart-docker.md`](schnellstart-docker.md).

---

## Wie es funktioniert

Die Pipeline (`.gitlab-ci.yml`) hat drei Stages: `test → build → deploy`.

1. **build** baut zwei Docker-Images und pusht sie in die Registry, u. a. auf
   den **unveränderlichen Commit-Tag** `:$CI_COMMIT_SHORT_SHA` — das ist der
   „spezielle Tag", auf den das Deployment pinnt:
   - `…/covey` (Control Plane, [`Dockerfile`](../Dockerfile)),
   - `…/covey/sandbox` (coveyd + Claude-Code-Runtime,
     [`Dockerfile.sandbox`](../Dockerfile.sandbox)).
2. **deploy** läuft auf einem **Shell-Runner direkt auf dem Zielhost**
   (Runner-Tag `covey-deploy`). Der Job:
   - kopiert [`docker-compose.deploy.yml`](../docker-compose.deploy.yml) nach
     `$DEPLOY_DIR/docker-compose.yml` (Default `/opt/covey`),
   - erzeugt beim **ersten** Deploy einmalig eine `.env` mit zufälligem
     Master-Key + Passwörtern (danach nie wieder angefasst),
   - setzt `COVEY_IMAGE` auf `…/covey:$CI_COMMIT_SHORT_SHA`, pullt das
     Sandbox-Image auf den Host und pinnt es via `COVEY_SANDBOX_IMAGE`,
   - `docker compose pull && docker compose up -d`.

### Sandbox-Isolation im Deployment

Das Deployment nutzt den **docker-SandboxProvider**: pro Agenten-Wake startet
die Control Plane einen Sibling-Container aus dem Sandbox-Image. Dafür ist im
Compose der Docker-Socket des Hosts in den covey-Container gemountet, und das
Datenverzeichnis (`COVEY_DATA_DIR`, Default `/opt/covey/data`) liegt als
Bind-Mount unter **identischem Pfad** auf Host und im Container — nur so
stimmen die `-v`-Pfade der Agenten-Homes, die der covey-Container an den
Host-Daemon übergibt. Das Sandbox-Image wird im Deploy-Job gepullt (dort
existiert der Registry-Login); die Control Plane selbst pullt nie.

Migrationen laufen beim `serve`-Start automatisch (Advisory-Lock). Der
`bootstrap`-Service legt Organisation/Admin idempotent an und blockiert den
Start der Control Plane, bis er durch ist.

Deploy-Compose vs. lokales Compose:

| Datei | Zweck | Image |
|---|---|---|
| `docker-compose.yml` | lokal ausprobieren | `build: .` (baut lokal) |
| `docker-compose.deploy.yml` | Host-Deployment via CI | `image: ${COVEY_IMAGE}` (aus Registry) |

### Was ins Sandbox-Image gehört — und was nicht

Ein Entwickler-Agent arbeitet an mehreren Projekten mit unterschiedlichen
Technologien und Versionen. Die Sandbox hängt aber am **Agenten**, nicht am
Projekt: Der Container startet beim Wake, bevor feststeht, welches Ticket aus
welchem Projekt drankommt. Ein Image pro Projekt ist deshalb kein gangbarer Weg.
Stattdessen gilt:

> **Version → Home, Toolchain → Image.**

| Schicht | Inhalt | Lebensdauer |
|---|---|---|
| Image | Systempakete (PHP, JDK) und die Versionsmanager selbst | pro Build |
| Home `/home/agent` | die SDK-Versionen, die das Projekt pinnt | persistent je Agent |
| Checkout | `vendor/`, `node_modules/`, Gradle-Cache | persistent je Agent |

Das Image bringt deshalb `fvm` (Flutter/Dart) und `uv` (Python) mit, aber
**keine SDKs**: Die zieht sich jeder Agent beim ersten Bedarf in sein Home,
gesteuert durch die Version, die im Projekt-Repo steht (`.fvmrc`,
`.python-version`, `gradle-wrapper.properties`). Weil das Home persistent ist,
passiert das einmal und nicht bei jedem Lauf.

**Beim Erweitern des Images beachten:** Die Control Plane mountet das Home über
`/home/agent`. Alles, was ein Installer zur Build-Zeit dorthin schreibt, ist zur
Laufzeit maskiert und unsichtbar — Werkzeuge gehören in einen Systempfad
(`/usr/local/bin`, `/opt`), ihre Caches ins Home. Aus demselben Grund lässt sich
SDKMAN nicht vorinstallieren; es liegt konstruktionsbedingt in `$HOME/.sdkman`
und wird bei Bedarf vom Agenten selbst installiert (bleibt dann liegen).

**Nachinstallieren zur Laufzeit ist kein Weg.** Der Agent läuft als Nicht-Root,
und ein Paketmanager auf der Egress-Allowlist wäre ein generischer
Code-Ausführungskanal. Die Begründung steht als **D11** in
[`spec/07-offene-entscheidungen.md`](../spec/07-offene-entscheidungen.md).

### Egress für Entwickler-Agenten

Im Isolationsmodus `network` erreicht die Sandbox nur, was auf der Allowlist
steht — ohne die passenden Templates scheitern `composer install`,
`fvm install` und `gradlew`. Der eingebaute Katalog
(`internal/egress/builtin.go`) hält dafür fertige Host-Sets bereit; einem
Entwickler-Agenten weist man üblicherweise zu:

| Template | Wofür |
|---|---|
| GitHub | Git-Klone und Release-Downloads. **Praktisch immer nötig:** `fvm`, Composer-VCS-Pakete, `uv`s CPython, der Gradle-Wrapper und die JDK-Toolchains landen alle auf GitHub Releases |
| PHP / Composer | `packagist.org` (die dist-Archive kommen über GitHub) |
| Dart / Flutter | `pub.dev` und die SDK-Artefakte |
| Maven / Gradle | Maven Central, Plugin Portal, Wrapper-Distributionen, JDK-Toolchains |
| Android / Google Maven | Android-Abhängigkeiten — für Gradle- **und** Flutter-Builds |
| Node.js / npm | `registry.npmjs.org` |

Dazu das eigene GitLab bzw. Zammad als org-eigenes Template — die stehen
bewusst nicht im Katalog.

Zwei Dinge, die beim Zuschneiden der Allowlist regelmäßig Zeit kosten:

- **Der Proxy folgt keinem Redirect.** Er sieht nur den CONNECT-Host der
  jeweiligen Verbindung, und er ist fail-closed. Leitet ein Dienst auf einen
  anderen Host um, muss auch das Ziel auf der Liste stehen — deshalb liegt
  `plugins-artifacts.gradle.org` neben `plugins.gradle.org`, und deshalb ist
  das GitHub-Template praktisch Pflicht.
- **`storage.googleapis.com` im Flutter-Template ist breit.** Der Proxy
  terminiert TLS nicht, kann also nicht auf Pfade filtern — der Eintrag öffnet
  jeden öffentlich lesbaren GCS-Bucket, nicht nur die Flutter-Artefakte. Für
  Agenten, bei denen das nicht tragbar ist, führt der Weg über einen eigenen
  Spiegel und `FLUTTER_STORAGE_BASE_URL` in der Sandbox statt über dieses
  Template.

---

## Einmalige Einrichtung

### 1. Shell-Runner auf dem Zielhost

Ein GitLab-Runner mit **Executor `shell`** muss auf dem Zielhost laufen und mit
dem Tag `covey-deploy` registriert sein. Auf dem Host müssen installiert sein:

- **Docker** + **Compose-Plugin** (`docker compose version`),
- der Runner-User muss Docker ausführen dürfen (Gruppe `docker`),
- **OpenSSL** (für die einmalige `.env`-Erzeugung).

```bash
# Beispiel-Registrierung (auf dem Host):
gitlab-runner register \
  --url https://gitlab.lapco.legal \
  --registration-token <TOKEN> \
  --executor shell \
  --tag-list covey-deploy \
  --description "covey-host"
```

### 2. CI/CD-Variablen (optional)

In GitLab unter **Settings → CI/CD → Variables**:

| Variable | Default | Zweck |
|---|---|---|
| `DEPLOY_DIR` | `/opt/covey` | Zielordner auf dem Host |
| `COVEY_PUBLIC_URL` | `http://localhost:8494` | öffentliche URL — nur beim **ersten** Deploy in die `.env` geschrieben |

`COVEY_PUBLIC_URL` ist mehr als Kosmetik: Aus ihr baut das Binary die Adressen
in `sitemap.xml`, `robots.txt` und den `canonical`/`hreflang`-Angaben der
öffentlichen Website. Ist sie leer, leitet der Server sie aus dem Host-Header
ab — das funktioniert hinter einem sauber konfigurierten Reverse-Proxy, ist aber
die schwächere Grundlage. Wer die Website in Suchmaschinen sehen will, setzt sie
auf die tatsächliche `https://`-Adresse.

Registry-Login läuft automatisch über die eingebauten `$CI_REGISTRY_*`.

### 3. Erster Push auf main

Beim ersten Deploy erzeugt der Job `$DEPLOY_DIR/.env` mit zufälligem Master-Key,
Postgres- und Admin-Passwort. **Diese `.env` sofort sichern** — der Master-Key
ver-/entschlüsselt alle hinterlegten Secrets; geht er verloren, sind alle
gebrokerten Zugänge unlesbar. Das erzeugte Admin-Passwort steht ebenfalls dort:

```bash
sudo cat /opt/covey/.env      # Admin-Passwort + Master-Key ablesen / sichern
```

---

## Laufender Betrieb

```bash
cd /opt/covey
docker compose ps                 # Status
docker compose logs -f covey      # Live-Logs
docker compose down               # Stoppen (Daten bleiben in den Volumes)
```

Jeder weitere main-Push zieht die neuen Images und startet neu. Das DB-Volume
(`covey-db`), die Agenten-Homes (`/opt/covey/data/homes/…`) und die `.env`
bleiben erhalten.

### Passwort-Notfall-Reset

Eigenes Passwort ändert man in der UI (Account-Einstellungen), fremde setzt
der Platform-Admin über die Nutzer-Seite zurück. Ist der **Admin selbst**
ausgesperrt, hilft `covey passwd` direkt auf dem Host — es setzt das Passwort
in der DB neu und invalidiert alle laufenden Sessions des Nutzers:

```bash
cd /opt/covey
docker compose run --rm covey passwd admin@covey.local
# → fragt das neue Passwort interaktiv ab (ohne Echo);
#   nicht-interaktiv: echo 'neues-passwort' | docker compose run --rm -T covey passwd admin@covey.local
```

### Rollback

Auf einen früheren Commit-Tag zurück (Tags stehen in der Registry):

```bash
cd /opt/covey
COVEY_IMAGE=<registry>/covey:<älterer-sha> docker compose up -d
```

---

## Wiki-Gedächtnis: Embedding wählen

Die Wiki-Suche der Agenten (siehe [`spec/05`](../spec/05-gedaechtnis.md)) liegt
auf einem Vektor-Index. Welches Embedding ihn füllt, entscheidet
`COVEY_EMBEDDING_PROVIDER` in der `.env` des Deploy-Ordners.

Der Default `builtin` braucht nichts und kann wenig: er misst Wortüberlappung,
keine Bedeutung. „Die Pipeline ist rot" und „Der CI-Build schlägt fehl" haben
für ihn nichts miteinander zu tun. Ein Agent findet seine eigene Seite damit
nicht wieder, sobald er anders formuliert — und legt eine zweite an. Für echten
Betrieb ist das zu wenig.

**Selbst betreiben** (empfohlen — die Wiki-Inhalte verlassen das Haus nicht):

```bash
# in /opt/covey/.env
COMPOSE_PROFILES=embeddings
COVEY_EMBEDDING_PROVIDER=ollama
```

Das startet zwei zusätzliche Container: einen Embedding-Server und einen
einmaligen Job, der das Modell lädt. Default ist EmbeddingGemma — 308M
Parameter, mehrsprachig, läuft auf der CPU, kein Schlüssel, kein Egress zu
Dritten. Ein anderes Modell über `COVEY_EMBEDDING_MODEL`; es muss die vom
Schema erwarteten 256 Dimensionen liefern können (Matryoshka-trainierte Modelle
tun das, sonst wird gekürzt und neu normalisiert).

**Fremden Dienst nutzen:**

```bash
COVEY_EMBEDDING_PROVIDER=voyage      # oder openai
COVEY_EMBEDDING_API_KEY=…
```

Danach `docker compose up -d`. Beim Start bettet die Control Plane den
vorhandenen Bestand automatisch neu ein — Vektoren verschiedener Modelle sind
nicht vergleichbar, deshalb trägt jede Seite den Fingerabdruck ihres Modells.
Im Log:

```
wiki-embedding aktiv                      modell=ollama:embeddinggemma:256
wiki-embedding: Bestand wird nachgezogen  seiten=52
wiki-embedding: Bestand nachgezogen       seiten=52
```

Bis das durchgelaufen ist, findet die Suche die betroffenen Seiten nicht; sie
gehen aber nicht verloren. Ist der Embedding-Dienst beim Start noch nicht
bereit (er lädt beim ersten Mal das Modell), fasst die Control Plane im
Minutentakt nach.

### Aufräum-Heartbeat

`COVEY_WIKI_CLEANUP` (Default `03:00`) legt jedem Agenten täglich eine
Wartungsaufgabe an: ähnliche Seiten zusammenführen, tote `[[Verweise]]`
korrigieren, Widersprüche glätten. Das kostet **einen LLM-Lauf pro Agent und
Tag** — bei vielen Agenten ein spürbarer Posten. Abschalten mit einem leeren
Wert, ein anderer Takt über `HH:MM` oder ein Intervall wie `12h`. Einzelne
Agenten überschreiben den Eintrag über einen gleichnamigen Punkt in ihrer
`HEARTBEAT.md`.

---

## Vor echtem Produktivbetrieb

Das Setup ist bewusst schlank. Für echten Betrieb zusätzlich (vgl.
[`schnellstart-docker.md`](schnellstart-docker.md#für-den-produktivbetrieb)):

- **HTTPS davor:** Reverse-Proxy mit TLS-Terminierung, `COVEY_PUBLIC_URL` auf
  `https://…` — das Secure-Cookie schaltet sich dann automatisch an.
- **DB-TLS:** `sslmode=require` in der DB-URL (dann in `docker-compose.deploy.yml`
  bzw. per eigener DB-Instanz).
- **Egress:** `COVEY_EGRESS_ENFORCE=true` (der docker-Sandbox-Provider ist
  bereits aktiv), damit Sandboxen nur Allowlist-Hosts erreichen.
- Admin-Passwort aus der generierten `.env` durch ein eigenes ersetzen.
