#!/bin/sh
# Covey installieren: Binary aus dem GitHub-Release holen, Prüfsumme
# vergleichen, in den Suchpfad legen.
#
#   curl -sSL https://raw.githubusercontent.com/benjaminLedel/covey/main/install.sh | sh
#
# Wer ein Skript nicht ungelesen ausführen will — eine gesunde Haltung —
# nimmt den zweistufigen Weg:
#
#   curl -sSLO https://raw.githubusercontent.com/benjaminLedel/covey/main/install.sh
#   less install.sh && sh install.sh
#
# Optionen:
#   --version v0.2.0   feste Version statt der neuesten
#   --bin-dir ~/bin    Zielverzeichnis (Standard: /usr/local/bin)
#
# Das Skript installiert NUR die Binaries. Covey braucht zusätzlich Postgres
# und Docker; was danach zu tun ist, steht am Ende der Ausgabe.
#
# Alles steckt in einer Funktion, die erst in der letzten Zeile aufgerufen
# wird. Das ist bei `curl | sh` kein Stilmittel, sondern notwendig: Reißt die
# Verbindung mitten im Download ab, führt die Shell sonst ein halbes Skript
# aus — mit einer Funktion passiert schlicht nichts.
set -eu

REPO="benjaminLedel/covey"

main() {
    version=""
    bin_dir="/usr/local/bin"

    while [ $# -gt 0 ]; do
        case "$1" in
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

    archiv="covey_${version}_${plattform}.tar.gz"
    basis="https://github.com/${REPO}/releases/download/${version}"

    tmp="$(mktemp -d)"
    trap 'rm -rf "$tmp"' EXIT INT TERM

    info "lade Covey ${version} (${plattform})"
    holen "${basis}/${archiv}" "${tmp}/${archiv}"
    holen "${basis}/SHA256SUMS" "${tmp}/SHA256SUMS"

    pruefen "$tmp" "$archiv"
    tar -xzf "${tmp}/${archiv}" -C "$tmp"

    installieren "$tmp" "$bin_dir"
    abschluss "$bin_dir"
}

usage() {
    cat <<'EOF'
Covey installieren.

  install.sh [--version <tag>] [--bin-dir <verzeichnis>]

  --version   zu installierende Version (Standard: die neueste Veröffentlichung)
  --bin-dir   Zielverzeichnis (Standard: /usr/local/bin)
EOF
}

info() { printf '  %s\n' "$*" >&2; }
fehler() { printf 'Fehler: %s\n' "$*" >&2; exit 1; }

need() {
    command -v "$1" >/dev/null 2>&1 || fehler "'$1' wird gebraucht, ist aber nicht installiert"
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
    mkdir -p "$ziel" 2>/dev/null || true
    if [ -w "$ziel" ]; then
        sudo_cmd=""
    elif command -v sudo >/dev/null 2>&1; then
        info "${ziel} gehört nicht dir — die Installation läuft über sudo"
        sudo_cmd="sudo"
    else
        fehler "keine Schreibrechte auf ${ziel} und kein sudo — mit --bin-dir ein eigenes Verzeichnis wählen"
    fi

    for bin in covey coveyd; do
        [ -f "${quelle}/${bin}" ] || continue
        $sudo_cmd install -m 0755 "${quelle}/${bin}" "${ziel}/${bin}"
        info "installiert: ${ziel}/${bin}"
    done
}

abschluss() {
    ziel="$1"
    printf '\n'
    "${ziel}/covey" version || true
    # Ein Binary im Suchpfad ist keine laufende Instanz. Was noch fehlt, gehört
    # hierhin — sonst endet die Installation in einem "command not found" oder
    # einem Server, der ohne Datenbank nicht startet.
    cat <<EOF

Covey braucht noch Postgres (mit pgvector) und Docker für die Sandboxen.

  1) Datenbank bereitstellen und COVEY_DATABASE_URL setzen
  2) Hauptschlüssel erzeugen:  export COVEY_MASTER_KEY=\$(openssl rand -hex 32)
  3) Organisation und Zugang anlegen:  covey bootstrap
  4) Starten:  covey serve

Der ausführliche Weg samt docker-compose steht in der Betriebsdoku:
https://github.com/${REPO}/blob/main/docs/betrieb-deployment.md
EOF
    case ":${PATH}:" in
        *":${ziel}:"*) ;;
        *) printf '\nHinweis: %s liegt nicht in deinem PATH.\n' "$ziel" ;;
    esac
}

main "$@"
