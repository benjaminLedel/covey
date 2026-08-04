#!/bin/sh
# Covey installieren: Binary aus dem GitHub-Release holen, Prüfsumme
# vergleichen, in den Suchpfad legen.
#
#   curl -sSL https://raw.githubusercontent.com/benjaminLedel/covey/main/installer/install.sh | sh
#
# Jede laufende Covey-Instanz liefert dieses Skript auch selbst aus, dann für
# ihre eigene Version:
#
#   curl -sSL https://covey.example/install.sh | sh
#
# Wer ein Skript nicht ungelesen ausführen will — eine gesunde Haltung —
# nimmt den zweistufigen Weg:
#
#   curl -sSLO https://covey.example/install.sh
#   less install.sh && sh install.sh
#
# Optionen:
#   --server           nur die Control Plane (covey)
#   --runner           nur den Runner (covey-runner)
#   --all              alles, was das Release hergibt
#   --version v0.2.0   feste Version statt der neuesten
#   --bin-dir ~/bin    Zielverzeichnis (Standard: /usr/local/bin)
#
# Ohne Angabe fragt das Skript — aber nur, wenn ein Mensch am Terminal sitzt.
# In einer Pipeline gibt es keins, dort gilt die Voreinstellung
# (COVEY_INSTALL_DEFAULT, sonst "server"). Eine Installation darf nicht auf
# eine Antwort warten, die niemand geben kann.
#
# Alles steckt in einer Funktion, die erst in der letzten Zeile aufgerufen
# wird. Das ist bei `curl | sh` kein Stilmittel, sondern notwendig: Reißt die
# Verbindung mitten im Download ab, führt die Shell sonst ein halbes Skript
# aus — mit einer Funktion passiert schlicht nichts.
set -eu

REPO="benjaminLedel/covey"

# Diese beiden setzt eine Instanz, die das Skript selbst ausliefert, als
# Vorspann. Von GitHub geladen bleiben sie leer bzw. beim Standard.
: "${COVEY_INSTALL_VERSION:=}"
: "${COVEY_INSTALL_DEFAULT:=server}"

main() {
    version="$COVEY_INSTALL_VERSION"
    bin_dir="/usr/local/bin"
    wahl=""

    while [ $# -gt 0 ]; do
        case "$1" in
            --server) wahl="server"; shift ;;
            --runner) wahl="runner"; shift ;;
            --all) wahl="all"; shift ;;
            --version) version="${2:-}"; shift 2 ;;
            --bin-dir) bin_dir="${2:-}"; shift 2 ;;
            -h|--help) usage; exit 0 ;;
            *) fehler "unbekannte Option: $1 (--help zeigt die Optionen)" ;;
        esac
    done
    [ -n "$bin_dir" ] || fehler "--bin-dir braucht einen Wert"

    need tar
    plattform="$(plattform_ermitteln)"
    [ -n "$version" ] || version="$(neueste_version)"
    basis="https://github.com/${REPO}/releases/download/${version}"

    tmp="$(mktemp -d)"
    trap 'rm -rf "$tmp"' EXIT INT TERM

    # Erst die Prüfsummen: Sie sind zugleich das Inhaltsverzeichnis des
    # Releases. Was dort nicht steht, gibt es nicht — und wird deshalb auch
    # nicht angeboten.
    holen "${basis}/SHA256SUMS" "${tmp}/SHA256SUMS"
    verfuegbar="$(komponenten_ermitteln "$tmp" "$version" "$plattform")"
    [ -n "$verfuegbar" ] || fehler "Release ${version} enthält nichts für ${plattform}"

    [ -n "$wahl" ] || wahl="$(auswaehlen "$verfuegbar")"
    gewaehlt="$(aufloesen "$wahl" "$verfuegbar")"

    for komponente in $gewaehlt; do
        installiere_komponente "$komponente" "$version" "$plattform" "$basis" "$tmp" "$bin_dir"
    done
    abschluss "$bin_dir" "$gewaehlt"
}

usage() {
    cat <<'EOF'
Covey installieren.

  install.sh [--server|--runner|--all] [--version <tag>] [--bin-dir <verzeichnis>]

  --server    nur die Control Plane (covey)
  --runner    nur den Runner (covey-runner)
  --all       alles, was das Release für dieses System hergibt
  --version   zu installierende Version (Standard: die neueste Veröffentlichung)
  --bin-dir   Zielverzeichnis (Standard: /usr/local/bin)

Ohne Auswahl fragt das Skript am Terminal nach; in einer Pipeline ohne
Terminal gilt die Voreinstellung.
EOF
}

info() { printf '  %s\n' "$*" >&2; }
fehler() { printf 'Fehler: %s\n' "$*" >&2; exit 1; }

need() {
    command -v "$1" >/dev/null 2>&1 || fehler "'$1' wird gebraucht, ist aber nicht installiert"
}

# Der Name einer Komponente ist zugleich der Name ihres Binaries und der
# Präfix ihres Archivs. Eine Übersetzungstabelle wäre eine Stelle mehr, an der
# etwas auseinanderlaufen kann.
binary_von() {
    case "$1" in
        server) printf 'covey' ;;
        runner) printf 'covey-runner' ;;
        *) fehler "unbekannte Komponente: $1" ;;
    esac
}

# Die Namen müssen zu denen aus .github/workflows/release.yml passen (GOOS und
# GOARCH), nicht zu denen von uname.
plattform_ermitteln() {
    os="$(uname -s)"
    arch="$(uname -m)"
    case "$os" in
        Linux) os="linux" ;;
        Darwin) os="darwin" ;;
        *) fehler "nicht unterstütztes System: $os (es gibt Binaries für Linux und macOS)" ;;
    esac
    case "$arch" in
        x86_64|amd64) arch="amd64" ;;
        aarch64|arm64) arch="arm64" ;;
        *) fehler "nicht unterstützte Architektur: $arch" ;;
    esac
    printf '%s_%s' "$os" "$arch"
}

# curl oder wget — welches da ist. Auf einem frisch aufgesetzten Server ist
# nicht garantiert, dass es curl ist.
#
# Die weiche Fassung bricht nicht ab, sondern meldet den Fehlschlag zurück:
# Beim Ermitteln der neuesten Version ist ein 404 kein Fehler des Netzes,
# sondern eine Aussage („es gibt noch kein Release") und verdient eine eigene
# Meldung.
holen_weich() {
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL --proto '=https' --tlsv1.2 -o "$2" "$1" 2>/dev/null
    elif command -v wget >/dev/null 2>&1; then
        wget -q --https-only -O "$2" "$1" 2>/dev/null
    else
        fehler "weder curl noch wget gefunden"
    fi
}

holen() {
    holen_weich "$1" "$2" || fehler "Download fehlgeschlagen: $1"
}

neueste_version() {
    api="https://api.github.com/repos/${REPO}/releases/latest"
    antwort="$(mktemp)"
    v=""
    if holen_weich "$api" "$antwort"; then
        v="$(sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$antwort" | head -1)"
    fi
    rm -f "$antwort"
    [ -n "$v" ] || fehler "keine veröffentlichte Version gefunden — mit --version <tag> eine angeben"
    printf '%s' "$v"
}

# Angeboten wird nur, was wirklich im Release liegt. Damit gibt es keine
# Auswahl, die ins Leere greift — und eine neue Komponente erscheint von
# selbst, sobald sie mitveröffentlicht wird.
komponenten_ermitteln() {
    verzeichnis="$1"; v="$2"; plat="$3"
    gefunden=""
    for k in server runner; do
        archiv="$(binary_von "$k")_${v}_${plat}.tar.gz"
        if awk -v f="$archiv" '$2 == f { treffer = 1 } END { exit !treffer }' \
                "${verzeichnis}/SHA256SUMS"; then
            gefunden="${gefunden}${gefunden:+ }${k}"
        fi
    done
    printf '%s' "$gefunden"
}

# Ob es ein steuerndes Terminal gibt, lässt sich nur durch Öffnen feststellen.
# `[ -r /dev/tty ]` prüft die Rechte der Gerätedatei und sagt auch dann "ja",
# wenn kein Terminal dahinter hängt — in CI, cron, systemd und `docker run`
# ohne -t scheitert dann erst das Öffnen ("Device not configured"), und mit
# `set -e` bricht das Skript mittendrin ab. Deshalb hier der Versuch selbst.
terminal_vorhanden() {
    { : < /dev/tty; } 2>/dev/null
}

# Stdin hängt bei `curl | sh` am Skript selbst, nicht am Terminal — deshalb
# ausdrücklich /dev/tty. Gibt es keins, wird nicht gefragt, sondern die
# Voreinstellung genommen.
auswaehlen() {
    verfuegbar="$1"
    anzahl=0
    for _ in $verfuegbar; do anzahl=$((anzahl + 1)); done
    if [ "$anzahl" -le 1 ]; then
        printf '%s' "$verfuegbar"
        return
    fi
    if ! terminal_vorhanden; then
        info "kein Terminal — installiere die Voreinstellung: ${COVEY_INSTALL_DEFAULT}"
        printf '%s' "$COVEY_INSTALL_DEFAULT"
        return
    fi

    {
        printf '\nWas soll installiert werden?\n\n'
        printf '  1) Control Plane (covey) — der Server\n'
        printf '  2) Runner (covey-runner) — faehrt Sandboxen fuer einen Server\n'
        printf '  3) beides\n\n'
        printf 'Auswahl [1]: '
    } > /dev/tty
    read -r antwort < /dev/tty || antwort=""
    printf '\n' > /dev/tty
    case "$antwort" in
        2) printf 'runner' ;;
        3) printf 'all' ;;
        *) printf 'server' ;;
    esac
}

aufloesen() {
    wunsch="$1"; verfuegbar="$2"
    if [ "$wunsch" = "all" ]; then
        printf '%s' "$verfuegbar"
        return
    fi
    for k in $verfuegbar; do
        [ "$k" = "$wunsch" ] || continue
        printf '%s' "$wunsch"
        return
    done
    fehler "'${wunsch}' ist in diesem Release fuer diese Plattform nicht enthalten (vorhanden: ${verfuegbar})"
}

installiere_komponente() {
    komponente="$1"; v="$2"; plat="$3"; basis_url="$4"; tmp_dir="$5"; ziel_dir="$6"
    bin="$(binary_von "$komponente")"
    archiv="${bin}_${v}_${plat}.tar.gz"

    info "lade ${bin} ${v} (${plat})"
    holen "${basis_url}/${archiv}" "${tmp_dir}/${archiv}"
    pruefen "$tmp_dir" "$archiv"
    tar -xzf "${tmp_dir}/${archiv}" -C "$tmp_dir"
    installieren "${tmp_dir}/${bin}" "$ziel_dir" "$bin"
}

# Ohne diesen Vergleich wäre der Rest des Skripts eine Einladung: Wer die
# Verbindung umbiegen kann, bestimmt sonst, welches Programm hier landet.
pruefen() {
    verzeichnis="$1"
    datei="$2"
    if command -v sha256sum >/dev/null 2>&1; then
        ist="$(cd "$verzeichnis" && sha256sum "$datei" | cut -d' ' -f1)"
    elif command -v shasum >/dev/null 2>&1; then
        ist="$(cd "$verzeichnis" && shasum -a 256 "$datei" | cut -d' ' -f1)"
    else
        fehler "weder sha256sum noch shasum gefunden — ohne Prüfsumme wird nicht installiert"
    fi

    soll="$(awk -v f="$datei" '$2 == f { print $1; exit }' "${verzeichnis}/SHA256SUMS")"
    [ -n "$soll" ] || fehler "keine Prüfsumme für ${datei} in SHA256SUMS"
    [ "$ist" = "$soll" ] || fehler "Prüfsumme stimmt nicht (erwartet ${soll}, erhalten ${ist}) — Installation abgebrochen"
    info "Prüfsumme stimmt"
}

# In ein Systemverzeichnis darf ein normaler Nutzer nicht schreiben. Statt
# blind `sudo` voranzustellen, wird erst geprüft — und wenn es sudo braucht,
# steht das sichtbar da, bevor es passiert.
installieren() {
    quelle="$1"
    ziel="$2"
    name="$3"
    mkdir -p "$ziel" 2>/dev/null || true
    if [ -w "$ziel" ]; then
        sudo_cmd=""
    elif command -v sudo >/dev/null 2>&1; then
        info "${ziel} gehört nicht dir — die Installation läuft über sudo"
        sudo_cmd="sudo"
    else
        fehler "keine Schreibrechte auf ${ziel} und kein sudo — mit --bin-dir ein eigenes Verzeichnis wählen"
    fi
    $sudo_cmd install -m 0755 "$quelle" "${ziel}/${name}"
    info "installiert: ${ziel}/${name}"
}

# Ein Binary im Suchpfad ist keine laufende Instanz. Was noch fehlt, gehört
# hierhin — und es ist je Komponente etwas anderes.
abschluss() {
    ziel="$1"; gewaehlt="$2"
    printf '\n'
    for komponente in $gewaehlt; do
        case "$komponente" in
            server)
                cat <<EOF
Die Control Plane braucht noch Postgres (mit pgvector) und Docker fuer die Sandboxen.

  1) Datenbank bereitstellen und COVEY_DATABASE_URL setzen
  2) Hauptschluessel erzeugen:  export COVEY_MASTER_KEY=\$(openssl rand -hex 32)
  3) Organisation und Zugang anlegen:  covey bootstrap
  4) Starten:  covey serve

Der ausfuehrliche Weg samt docker-compose steht in der Betriebsdoku:
https://github.com/${REPO}/blob/main/docs/betrieb-deployment.md

EOF
                ;;
            runner)
                cat <<EOF
Der Runner meldet sich noch bei einer Covey-Instanz an. Das Token dafuer
erzeugt die Runner-Ansicht der Oberflaeche:

  covey-runner register --url https://covey.example --token <registration-token>
  covey-runner run

EOF
                ;;
        esac
    done
    case ":${PATH}:" in
        *":${ziel}:"*) ;;
        *) printf 'Hinweis: %s liegt nicht in deinem PATH.\n' "$ziel" ;;
    esac
}

main "$@"
