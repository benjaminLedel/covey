#!/bin/sh
# Packs the Mac app for the release (#406): signed and notarised when the
# means are there, as it is when they are not.
#
#   cd mobile && flutter build macos --release && tool/macos_release.sh <version> <out-dir>
#
# Signing, from the environment:
#   MACOS_SIGNING_IDENTITY   a "Developer ID Application: …" identity in the
#                            keychain; empty: the app stays as the build left
#                            it, signed ad hoc, and Gatekeeper will ask
# Notarisation, only with a signature:
#   APPLE_API_KEY_PATH       the App Store Connect API key (.p8)
#   APPLE_API_KEY_ID, APPLE_API_ISSUER_ID
#
# Writes <out-dir>/covey-app_<version>_macos.zip and its .sha256.
set -eu

version="$1"
out="$2"
app="build/macos/Build/Products/Release/covey.app"
zip="$out/covey-app_${version}_macos.zip"

[ -d "$app" ] || { echo "no $app — run flutter build macos --release first" >&2; exit 1; }
mkdir -p "$out"

if [ -n "${MACOS_SIGNING_IDENTITY:-}" ]; then
  # Inside out, as codesign wants it: every library and framework the app
  # carries (Flutter, the plugins, sherpa-onnx), deepest first, then the app
  # with its entitlements. Under the hardened runtime, which notarisation
  # requires; the entitlements keep the microphone and the camera usable.
  find "$app/Contents/Frameworks" -depth \( -name '*.dylib' -o -name '*.framework' \) -print | while read -r part; do
    codesign --force --timestamp --options runtime --sign "$MACOS_SIGNING_IDENTITY" "$part"
  done
  codesign --force --timestamp --options runtime \
    --entitlements macos/Runner/Release.entitlements \
    --sign "$MACOS_SIGNING_IDENTITY" "$app"
  codesign --verify --strict --deep --verbose=2 "$app"

  if [ -n "${APPLE_API_KEY_PATH:-}" ]; then
    # Notarised as a zip, stapled to the app, and zipped again: the ticket
    # travels in the app, so it opens offline too.
    submit="$out/notarize.zip"
    ditto -c -k --keepParent "$app" "$submit"
    result="$(xcrun notarytool submit "$submit" \
      --key "$APPLE_API_KEY_PATH" --key-id "$APPLE_API_KEY_ID" --issuer "$APPLE_API_ISSUER_ID" \
      --wait --output-format json)"
    echo "$result"
    rm -f "$submit"
    id="$(printf '%s' "$result" | sed -n 's/.*"id" *: *"\([^"]*\)".*/\1/p')"
    if ! printf '%s' "$result" | grep -q '"status" *: *"Accepted"'; then
      # Apple's log says which binary it objected to; without it the red run
      # says only "Invalid".
      [ -n "$id" ] && xcrun notarytool log "$id" \
        --key "$APPLE_API_KEY_PATH" --key-id "$APPLE_API_KEY_ID" --issuer "$APPLE_API_ISSUER_ID" || true
      echo "notarisation was not accepted" >&2
      exit 1
    fi
    xcrun stapler staple "$app"
    spctl --assess --type execute --verbose=2 "$app"
  fi
fi

ditto -c -k --keepParent "$app" "$zip"
(cd "$out" && shasum -a 256 "$(basename "$zip")" > "$(basename "$zip").sha256")
cat "$zip.sha256"
