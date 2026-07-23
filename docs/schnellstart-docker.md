# Schnellstart mit Docker Compose

Covey in wenigen Minuten lokal ausprobieren — **ohne Go, Node oder eine
lokale Postgres-Installation**. Es genügen Docker und Docker Compose.

> Für die Entwicklung (Hot-Reload, Tests) siehe stattdessen den Abschnitt
> „Schnellstart (Entwicklung)" in der [README](../README.md).

---

## Voraussetzungen

- **Docker** mit Compose-Plugin (`docker compose version` ≥ 2.x).
- **OpenSSL** (für den Master-Key; auf macOS/Linux vorinstalliert).
- Das Repository lokal ausgecheckt (das Image wird aus dem `Dockerfile` gebaut).

---

## In vier Schritten

```bash
# 1. Konfiguration anlegen
cp .env.example .env

# 2. Master-Key erzeugen und in .env schreiben (32 Byte / 64 Hex-Zeichen)
echo "COVEY_MASTER_KEY=$(openssl rand -hex 32)" >> .env

# 3. Starten (baut das Image beim ersten Mal)
docker compose up -d --build

# 4. Öffnen
open http://localhost:8494        # oder im Browser aufrufen
```

Login: **`admin@covey.local`** / **`covey-admin`**
(änderbar in `.env` über `COVEY_ADMIN_EMAIL` / `COVEY_ADMIN_PASSWORD`).

Das war's. `docker compose` startet drei Dinge:

| Service | Aufgabe |
|---|---|
| `db` | PostgreSQL mit `pgvector` (persistent im Volume `covey-db`) |
| `bootstrap` | Legt einmalig Organisation, Admin-Login und einen Demo-Agenten an, dann beendet sich der Container (idempotent) |
| `covey` | Die Control Plane: API + Orchestrator + eingebettete Admin-UI auf Port **8494**. Migrationen laufen beim Start automatisch |

---

## Nützliche Befehle

```bash
docker compose logs -f covey      # Live-Logs der Control Plane
docker compose ps                 # Status aller Services
docker compose restart covey      # Nur die Control Plane neu starten
docker compose down               # Stoppen (Daten bleiben in den Volumes)
docker compose down -v            # Stoppen UND alle Daten löschen (frischer Start)
docker compose up -d --build      # Nach Code-Änderungen neu bauen & starten
```

Einen Befehl im covey-Container ausführen (z. B. Bootstrap manuell wiederholen):

```bash
docker compose run --rm bootstrap
```

---

## Der erste Agent

Nach dem Login ist bereits ein **Demo-Support-Agent** angelegt. Damit er
tatsächlich arbeiten kann, braucht er zwei Dinge — beides in der Admin-UI unter
dem Agenten hinterlegbar:

1. **Anthropic-Zugang** — Secret `anthropic_api_key` (API-Key) *oder*
   `claude_code_oauth_token` (Abo-Account; Token einmalig mit
   `claude setup-token` erzeugen). Ohne eines der beiden scheitern Aufgaben mit
   „Not logged in", weil die Sandbox ein eigenes, leeres `HOME` hat.
2. **Ein Zielsystem** — z. B. Zammad. Zum Ausprobieren ohne echtes Zammad gibt
   es das Double `demo/fakezammad`; für den Anschluss an eine echte Instanz das
   Runbook [`betrieb-zammad.md`](betrieb-zammad.md).

---

## Sandbox-Isolation

Das Compose-Setup nutzt den Default **`COVEY_SANDBOX_PROVIDER=local`**: Sandboxen
laufen als Subprozesse *im* covey-Container. Das ist ehrlich und ideal zum
Ausprobieren, bietet aber **nur Prozess-Isolation**.

Für echte Container-Isolation pro Agent (`COVEY_SANDBOX_PROVIDER=docker`) braucht
der covey-Container Zugriff auf einen Docker-Daemon (Socket-Mount oder
Docker-in-Docker) und das Sandbox-Image `covey-sandbox:latest`
([`Dockerfile.sandbox`](../Dockerfile.sandbox)). Das ist bewusst **nicht** Teil
dieses Einsteiger-Setups — Details dazu in
[`spec/01-architektur.md`](../spec/01-architektur.md) und der README.

---

## Für den Produktivbetrieb

Das Beispiel ist auf „schnell ausprobieren" optimiert. Vor einem echten Betrieb
mindestens:

- **Passwörter/Keys ändern:** `COVEY_ADMIN_PASSWORD`, das Postgres-Passwort und
  einen frischen `COVEY_MASTER_KEY` (der Key ver-/entschlüsselt alle Secrets —
  Verlust = alle hinterlegten Zugänge sind unlesbar; also sicher aufbewahren).
- **HTTPS davor:** `COVEY_PUBLIC_URL` auf `https://…` setzen und einen
  Reverse-Proxy (TLS-Terminierung) vorschalten. Das Secure-Cookie schaltet sich
  dann automatisch an.
- **DB-TLS:** `sslmode=require` (oder höher) in `COVEY_DATABASE_URL`.
- **Egress & Isolation:** docker-Provider + `COVEY_EGRESS_ENFORCE=true`.

Covey gibt diese Punkte beim `serve`-Start auch selbst als Warnungen aus, sobald
es nicht rein lokal (`localhost`) gebunden ist.
