#!/bin/sh
# Installs one PHP version with the extensions the projects use, plus Composer.
# Shared by every image that carries PHP (Dockerfile.sandbox.dev,
# Dockerfile.sandbox.dev-php, Dockerfile.sandbox.dev-php84), for the same
# reason install-flutter.sh is shared: the package list would otherwise stand in
# several Dockerfiles and drift apart at the first change. A new PHP workplace
# is one call of this script with another version, not a copied block.
#
#   install-php.sh <version> [debian|sury]
#
# The source is named, not guessed. `debian` (the default) takes the packages of
# the distribution the base image stands on — trixie ships 8.4. `sury` takes them
# from packages.sury.org, where older minor versions (8.2) are still maintained.
# Which version a workplace carries is not arbitrary: an agent that builds and
# tests against another minor version than the project's CI reports green, and
# the pipeline goes red. So each project gets the workplace whose version its CI
# runs, rather than one image deciding for all of them.
#
# Extensions: zip/gd/ldap as in the projects' CI, plus the ones composer.json
# requires (dom/json/openssl/pdo) and pdo_sqlite, because PHPUnit runs against an
# in-memory SQLite there. Plus mysql and pgsql (projects whose test database is
# MariaDB or Postgres) and pcov: without a coverage driver every
# `phpunit --coverage-*` breaks off with "No code coverage driver available" —
# the agent reads that as a broken suite and starts repairing a test environment
# that was never broken. pcov rather than xdebug, because xdebug slows every test
# run by a multiple and contributes nothing else here. As non-root the agent
# cannot install anything afterwards — what is missing here is missing for it at
# runtime.
#
# A side effect of sury one has to know about: the source brings its own
# rebuilds of some system libraries, and php8.2-gd pulls their libgd3 instead of
# the Debian one. That is unavoidable with sury (the dependency is built that
# way), but it means a third-party repository delivers more than PHP. So the
# source file goes out again in the same step — the image must not carry an
# active foreign source, and the agent never has apt anyway.
#
# Runs as root during the image build.
set -eu

version="${1:?usage: install-php.sh <version> [debian|sury]}"
source="${2:-debian}"

case "$source" in
debian) ;;
sury)
    curl -fsSL https://packages.sury.org/php/apt.gpg -o /usr/share/keyrings/sury-php.gpg
    echo "deb [signed-by=/usr/share/keyrings/sury-php.gpg] https://packages.sury.org/php/ trixie main" \
        > /etc/apt/sources.list.d/sury-php.list
    ;;
*)
    echo "install-php.sh: unknown source '$source' (debian|sury)" >&2
    exit 2
    ;;
esac

apt-get update
apt-get install -y --no-install-recommends \
    "php${version}-cli" "php${version}-xml" "php${version}-mbstring" "php${version}-curl" \
    "php${version}-zip" "php${version}-gd" "php${version}-ldap" "php${version}-sqlite3" \
    "php${version}-mysql" "php${version}-pgsql" "php${version}-bcmath" "php${version}-intl" \
    "php${version}-pcov" composer

rm -f /etc/apt/sources.list.d/sury-php.list
rm -rf /var/lib/apt/lists/*

# The version the image promises is the version `php` answers with. Composer's
# dependencies can pull a second PHP along (the distribution's default); then
# `php` would point at that one, and the agent would test against the wrong
# minor version without noticing.
update-alternatives --set php "/usr/bin/php${version}"
actual="$(php -r 'echo PHP_MAJOR_VERSION.".".PHP_MINOR_VERSION;')"
if [ "$actual" != "$version" ]; then
    echo "install-php.sh: php answers $actual, expected $version" >&2
    exit 1
fi
