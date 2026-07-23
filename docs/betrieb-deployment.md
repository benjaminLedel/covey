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

### Rollback

Auf einen früheren Commit-Tag zurück (Tags stehen in der Registry):

```bash
cd /opt/covey
COVEY_IMAGE=<registry>/covey:<älterer-sha> docker compose up -d
```

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
