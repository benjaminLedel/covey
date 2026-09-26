#!/bin/sh
# Writes the Mac app's appcast (#421): the feed Sparkle reads to learn of a
# new version, with the Ed25519 signature Sparkle checks before it installs.
#
#   tool/appcast.sh <tag> <version> <build-number> <zip> <repository> > appcast.xml
#
#   <version>       the CFBundleShortVersionString the app was built with (0.9.1)
#   <build-number>  its CFBundleVersion — what Sparkle compares
#   <repository>    owner/name on GitHub; the zip is that release's asset
#
# Signs with the PEM in SPARKLE_ED_KEY_PATH, through OpenSSL 3 (OPENSSL, or
# openssl on the path) — the key whose public half is SUPublicEDKey.
set -eu

tag="$1"
version="$2"
build="$3"
zip="$4"
repo="$5"
openssl="${OPENSSL:-openssl}"

[ -n "${SPARKLE_ED_KEY_PATH:-}" ] || { echo "SPARKLE_ED_KEY_PATH is not set" >&2; exit 1; }
signature="$("$openssl" pkeyutl -sign -inkey "$SPARKLE_ED_KEY_PATH" -rawin -in "$zip" | base64 | tr -d '\n')"
length="$(wc -c < "$zip" | tr -d ' ')"
date="$(LC_ALL=C date -u '+%a, %d %b %Y %H:%M:%S +0000')"
file="$(basename "$zip")"

cat <<XML
<?xml version="1.0" encoding="utf-8"?>
<rss version="2.0" xmlns:sparkle="http://www.andymatuschak.org/xml-namespaces/sparkle">
  <channel>
    <title>covey</title>
    <item>
      <title>covey ${version}</title>
      <pubDate>${date}</pubDate>
      <sparkle:version>${build}</sparkle:version>
      <sparkle:shortVersionString>${version}</sparkle:shortVersionString>
      <sparkle:minimumSystemVersion>12.0</sparkle:minimumSystemVersion>
      <sparkle:releaseNotesLink>https://github.com/${repo}/releases/tag/${tag}</sparkle:releaseNotesLink>
      <enclosure url="https://github.com/${repo}/releases/download/${tag}/${file}" length="${length}" type="application/octet-stream" sparkle:edSignature="${signature}"/>
    </item>
  </channel>
</rss>
XML
