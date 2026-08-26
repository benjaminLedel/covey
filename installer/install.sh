#!/bin/sh
# Install Covey: fetch the binary from the GitHub release, compare the
# checksum, put it into the search path.
#
#   curl -sSL https://raw.githubusercontent.com/benjaminLedel/covey/main/installer/install.sh | sh
#
# Every running Covey instance serves this script itself as well, then for its
# own version:
#
#   curl -sSL https://covey.example/install.sh | sh
#
# Whoever does not want to run a script unread — a healthy attitude — takes the
# two-step route:
#
#   curl -sSLO https://covey.example/install.sh
#   less install.sh && sh install.sh
#
# Options:
#   --server           only the control plane (covey)
#   --runner           only the runner (covey-runner)
#   --all              everything the release has to offer
#   --version v0.2.0   a fixed version instead of the latest
#   --bin-dir ~/bin    target directory (default: /usr/local/bin)
#
# Without an argument the script asks — but only when a human sits at a
# terminal. In a pipeline there is none, and there the default applies
# (COVEY_INSTALL_DEFAULT, otherwise "server"). An installation must not wait
# for an answer nobody can give.
#
# Everything sits in a function that is called only in the last line. With
# `curl | sh` that is not a stylistic device but a necessity: if the connection
# breaks mid-download, the shell would otherwise run half a script — with a
# function simply nothing happens.
set -eu

REPO="benjaminLedel/covey"

# These two are set as a preamble by an instance that serves the script itself.
# Loaded from GitHub they stay empty resp. at the default.
: "${COVEY_INSTALL_VERSION:=}"
: "${COVEY_INSTALL_DEFAULT:=server}"

main() {
    version="$COVEY_INSTALL_VERSION"
    bin_dir="/usr/local/bin"
    choice=""

    while [ $# -gt 0 ]; do
        case "$1" in
            --server) choice="server"; shift ;;
            --runner) choice="runner"; shift ;;
            --all) choice="all"; shift ;;
            --version) version="${2:-}"; shift 2 ;;
            --bin-dir) bin_dir="${2:-}"; shift 2 ;;
            -h|--help) usage; exit 0 ;;
            *) fail "unknown option: $1 (--help shows the options)" ;;
        esac
    done
    [ -n "$bin_dir" ] || fail "--bin-dir needs a value"

    need tar
    platform="$(detect_platform)"
    [ -n "$version" ] || version="$(latest_version)"
    base_url="https://github.com/${REPO}/releases/download/${version}"

    tmp="$(mktemp -d)"
    trap 'rm -rf "$tmp"' EXIT INT TERM

    # The checksums first: they are at the same time the table of contents of
    # the release. What is not listed there does not exist — and is therefore
    # not offered either.
    fetch "${base_url}/SHA256SUMS" "${tmp}/SHA256SUMS"
    available="$(available_components "$tmp" "$version" "$platform")"
    [ -n "$available" ] || fail "release ${version} contains nothing for ${platform}"

    [ -n "$choice" ] || choice="$(choose "$available")"
    chosen="$(resolve "$choice" "$available" "$version" "$platform")"

    for component in $chosen; do
        install_component "$component" "$version" "$platform" "$base_url" "$tmp" "$bin_dir"
    done
    next_steps "$bin_dir" "$chosen"
}

usage() {
    cat <<'EOF'
Install Covey.

  install.sh [--server|--runner|--all] [--version <tag>] [--bin-dir <directory>]

  --server    only the control plane (covey)
  --runner    only the runner (covey-runner)
  --all       everything the release has for this system
  --version   version to install (default: the latest release)
  --bin-dir   target directory (default: /usr/local/bin)

Without a selection the script asks at the terminal; in a pipeline without a
terminal the default applies.
EOF
}

info() { printf '  %s\n' "$*" >&2; }
fail() { printf 'Error: %s\n' "$*" >&2; exit 1; }

need() {
    command -v "$1" >/dev/null 2>&1 || fail "'$1' is needed but not installed"
}

# The name of a component is at the same time the name of its binary and the
# prefix of its archive. A translation table would be one more place where
# things can drift apart.
binary_for() {
    case "$1" in
        server) printf 'covey' ;;
        runner) printf 'covey-runner' ;;
        *) fail "unknown component: $1" ;;
    esac
}

# The names have to match those from .github/workflows/release.yml (GOOS and
# GOARCH), not those from uname.
detect_platform() {
    os="$(uname -s)"
    arch="$(uname -m)"
    case "$os" in
        Linux) os="linux" ;;
        Darwin) os="darwin" ;;
        *) fail "unsupported system: $os (there are binaries for Linux and macOS)" ;;
    esac
    case "$arch" in
        x86_64|amd64) arch="amd64" ;;
        aarch64|arm64) arch="arm64" ;;
        *) fail "unsupported architecture: $arch" ;;
    esac
    printf '%s_%s' "$os" "$arch"
}

# curl or wget — whichever is there. On a freshly set up server it is not
# guaranteed to be curl.
#
# The soft variant does not abort but reports the failure back: when
# determining the latest version a 404 is not a network failure but a statement
# ("there is no release yet") and deserves a message of its own.
fetch_soft() {
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL --proto '=https' --tlsv1.2 -o "$2" "$1" 2>/dev/null
    elif command -v wget >/dev/null 2>&1; then
        wget -q --https-only -O "$2" "$1" 2>/dev/null
    else
        fail "neither curl nor wget found"
    fi
}

fetch() {
    fetch_soft "$1" "$2" || fail "download failed: $1"
}

latest_version() {
    api="https://api.github.com/repos/${REPO}/releases/latest"
    response="$(mktemp)"
    v=""
    if fetch_soft "$api" "$response"; then
        v="$(sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$response" | head -1)"
    fi
    rm -f "$response"
    [ -n "$v" ] || fail "no published version found — name one with --version <tag>"
    printf '%s' "$v"
}

# Only what really sits in the release is offered. That way there is no choice
# that grabs into the void — and a new component shows up by itself as soon as
# it is published alongside.
available_components() {
    dir="$1"; v="$2"; plat="$3"
    found=""
    for k in server runner; do
        archive="$(binary_for "$k")_${v}_${plat}.tar.gz"
        if awk -v f="$archive" '$2 == f { hit = 1 } END { exit !hit }' \
                "${dir}/SHA256SUMS"; then
            found="${found}${found:+ }${k}"
        fi
    done
    printf '%s' "$found"
}

# Whether there is a controlling terminal can only be established by opening
# one. `[ -r /dev/tty ]` checks the permissions of the device file and says
# "yes" even when no terminal hangs behind it — in CI, cron, systemd and
# `docker run` without -t the opening then fails ("Device not configured"), and
# with `set -e` the script aborts halfway through. Hence the attempt itself.
have_terminal() {
    { : < /dev/tty; } 2>/dev/null
}

# With `curl | sh` stdin hangs on the script itself, not on the terminal —
# hence /dev/tty explicitly. If there is none, nothing is asked and the default
# is taken.
choose() {
    available="$1"
    count=0
    for _ in $available; do count=$((count + 1)); done
    if [ "$count" -le 1 ]; then
        printf '%s' "$available"
        return
    fi
    if ! have_terminal; then
        info "no terminal — installing the default: ${COVEY_INSTALL_DEFAULT}"
        printf '%s' "$COVEY_INSTALL_DEFAULT"
        return
    fi

    {
        printf '\nWhat should be installed?\n\n'
        printf '  1) control plane (covey) — the server\n'
        printf '  2) runner (covey-runner) — runs sandboxes for a server\n'
        printf '  3) both\n\n'
        printf 'Selection [1]: '
    } > /dev/tty
    read -r answer < /dev/tty || answer=""
    printf '\n' > /dev/tty
    case "$answer" in
        2) printf 'runner' ;;
        3) printf 'all' ;;
        *) printf 'server' ;;
    esac
}

# A missing component is almost never a typo but a version: the runner did not
# exist in earlier releases, and a server from back then does not offer one. So
# the message does not stop at the finding — whoever reads it wants to go on,
# and there are exactly two ways.
resolve() {
    wanted="$1"; available="$2"; v="$3"; plat="$4"
    if [ "$wanted" = "all" ]; then
        printf '%s' "$available"
        return
    fi
    for k in $available; do
        [ "$k" = "$wanted" ] || continue
        printf '%s' "$wanted"
        return
    done
    target="build"
    [ "$wanted" = "runner" ] && target="runner"
    fail "release ${v} carries no '${wanted}' for ${plat} — only: ${available}.
Releases before the runner do not have one. Two ways on:
  - a newer release:  install.sh --${wanted} --version <newer tag>
  - from source, which yields exactly the version this server speaks:
      git clone https://github.com/${REPO} && cd covey && make ${target}"
}

install_component() {
    component="$1"; v="$2"; plat="$3"; base="$4"; tmp_dir="$5"; dest_dir="$6"
    bin="$(binary_for "$component")"
    archive="${bin}_${v}_${plat}.tar.gz"

    info "downloading ${bin} ${v} (${plat})"
    fetch "${base}/${archive}" "${tmp_dir}/${archive}"
    verify "$tmp_dir" "$archive"
    tar -xzf "${tmp_dir}/${archive}" -C "$tmp_dir"
    place_binary "${tmp_dir}/${bin}" "$dest_dir" "$bin"
}

# Without this comparison the rest of the script would be an invitation:
# whoever can bend the connection would otherwise decide which program ends up
# here.
verify() {
    dir="$1"
    file="$2"
    if command -v sha256sum >/dev/null 2>&1; then
        got="$(cd "$dir" && sha256sum "$file" | cut -d' ' -f1)"
    elif command -v shasum >/dev/null 2>&1; then
        got="$(cd "$dir" && shasum -a 256 "$file" | cut -d' ' -f1)"
    else
        fail "neither sha256sum nor shasum found — without a checksum nothing is installed"
    fi

    want="$(awk -v f="$file" '$2 == f { print $1; exit }' "${dir}/SHA256SUMS")"
    [ -n "$want" ] || fail "no checksum for ${file} in SHA256SUMS"
    [ "$got" = "$want" ] || fail "checksum does not match (expected ${want}, got ${got}) — installation aborted"
    info "checksum matches"
}

# A normal user must not write into a system directory. Instead of blindly
# prefixing `sudo`, it is checked first — and when sudo is needed, that is
# visibly stated before it happens.
place_binary() {
    src="$1"
    dest="$2"
    name="$3"
    mkdir -p "$dest" 2>/dev/null || true
    if [ -w "$dest" ]; then
        sudo_cmd=""
    elif command -v sudo >/dev/null 2>&1; then
        info "${dest} does not belong to you — the installation goes through sudo"
        sudo_cmd="sudo"
    else
        fail "no write permission on ${dest} and no sudo — choose your own directory with --bin-dir"
    fi
    $sudo_cmd install -m 0755 "$src" "${dest}/${name}"
    info "installed: ${dest}/${name}"
}

# A new binary is not a new process. Whoever installs the runner onto a host
# that already runs one has updated nothing until the service restarts: systemd
# keeps the old image alive, the version in the runner view stays what it was,
# and the bug the update was for is still there. Reported from a real update —
# "you have to restart the service by hand afterwards" — and a step somebody has
# to know about is a step this script should take.
#
# Prints 'restarted' when it did it, 'todo' when it could only say so, and
# nothing when there is no service here (a fresh installation, the normal case
# for this script).
restart_runner_service() {
    command -v systemctl >/dev/null 2>&1 || return 0
    systemctl is-active --quiet covey-runner.service 2>/dev/null || return 0
    if [ "$(id -u)" = "0" ]; then
        sudo_cmd=""
    elif command -v sudo >/dev/null 2>&1; then
        sudo_cmd="sudo"
    else
        printf 'todo'
        return 0
    fi
    if $sudo_cmd systemctl restart covey-runner.service >/dev/null 2>&1; then
        printf 'restarted'
    else
        printf 'todo'
    fi
}

# A binary in the search path is not a running instance. What is still missing
# belongs here — and it is something different per component.
next_steps() {
    dest="$1"; chosen="$2"
    printf '\n'
    for component in $chosen; do
        case "$component" in
            server)
                cat <<EOF
The control plane still needs Postgres (with pgvector) and Docker for the sandboxes.

  1) provide a database and set COVEY_DATABASE_URL
  2) generate the master key:  export COVEY_MASTER_KEY=\$(openssl rand -hex 32)
  3) create the organization and the login:  covey bootstrap
  4) start:  covey serve

The detailed route including docker-compose is in the operations docs:
https://github.com/${REPO}/blob/main/docs/ops-deployment.md

EOF
                ;;
            runner)
                # A host that is already running one has been updated, not set
                # up: everything below it has long since happened there.
                case "$(restart_runner_service)" in
                    restarted)
                        cat <<EOF
The covey-runner service was already running here — it has been restarted and
is now on ${version}. The runner view of your instance shows the new version
within a few seconds.

EOF
                        continue
                        ;;
                    todo)
                        cat <<EOF
The covey-runner service is running here WITH THE OLD BINARY — a new file is not
a new process. Restart it, otherwise nothing has changed:

  sudo systemctl restart covey-runner.service

EOF
                        continue
                        ;;
                esac
                cat <<EOF
The runner still has to register with a Covey instance. The token for that
is issued by the runner view of the interface:

  covey-runner register --url https://covey.example --token <registration-token>

On a systemd host, run as root, that also installs and starts the service —
a runner that only runs in an open shell reports offline after the logout.
Otherwise: covey-runner run, or covey-runner install-service afterwards.

EOF
                ;;
        esac
    done
    case ":${PATH}:" in
        *":${dest}:"*) ;;
        *) printf 'Note: %s is not in your PATH.\n' "$dest" ;;
    esac
}

main "$@"
