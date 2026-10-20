#!/bin/sh
# Builds the Mac app for the Mac App Store (#486) and, when the means are
# there, signs it for the store, packs it as an installer package and
# uploads it to App Store Connect. Beside the direct download
# (tool/macos_release.sh), not instead of it.
#
#   cd mobile && tool/macos_store_release.sh <build-name> <build-number> <out-dir>
#
# The store build differs from the direct download in three ways:
#   - COVEY_STORE=true: dictate-anywhere, the activity log and Sparkle are
#     off in the app (lib/host.dart, Host.appStore);
#   - it is signed with macos/Runner/Store.entitlements, in the App Sandbox;
#   - Sparkle's helpers (the installer, the updater app, its XPC services)
#     are taken out of the bundle, and its keys out of Info.plist. The
#     framework itself stays: the update plugin links against it.
#
# Signing, from the environment:
#   APPLE_TEAM_ID                    the team the app is signed for
#   MACOS_STORE_PROFILE_PATH         a Mac App Store provisioning profile
#                                    (.provisionprofile) for the app
#   MACOS_STORE_APP_IDENTITY         an "Apple Distribution: …" (or "3rd Party
#                                    Mac Developer Application: …") identity
#                                    in the keychain
#   MACOS_STORE_INSTALLER_IDENTITY   a "3rd Party Mac Developer Installer: …"
#                                    (Mac Installer Distribution) identity
#   any of them empty: the app is built and signed ad hoc, no package is
#   made, nothing is uploaded
# Upload, only with a signature:
#   APPLE_API_KEY_PATH               the App Store Connect API key (.p8)
#   APPLE_API_KEY_ID, APPLE_API_ISSUER_ID
#
# Writes <out-dir>/covey-app_<build-name>_macos-store.pkg when signed.
set -eu

name="$1"
number="$2"
out="$3"
mkdir -p "$out"
app="build/macos/Build/Products/Release/covey.app"
entitlements="macos/Runner/Store.entitlements"

# The store's entitlements through the xcconfig the Release configuration
# reads (macos/Runner/Configs/AppInfo.xcconfig), for the length of the build.
store_xcconfig="macos/Runner/Configs/Store.xcconfig"
work="$(mktemp -d)"
cleanup() {
  rm -f "$store_xcconfig"
  rm -rf "$work"
}
trap cleanup EXIT
echo "COVEY_RELEASE_ENTITLEMENTS = Runner/Store.entitlements" > "$store_xcconfig"

flutter build macos --release --build-name "$name" --build-number "$number" \
  --dart-define=COVEY_STORE=true
rm -f "$store_xcconfig"

# No self-update in a store app: Sparkle's helpers out, its keys out.
sparkle="$app/Contents/Frameworks/Sparkle.framework"
if [ -d "$sparkle" ]; then
  for part in Autoupdate Updater.app XPCServices; do
    rm -rf "$sparkle/$part" "$sparkle"/Versions/*/"$part"
  done
fi
for key in SUPublicEDKey SUEnableAutomaticChecks SUScheduledCheckInterval SUFeedURL; do
  /usr/libexec/PlistBuddy -c "Delete :$key" "$app/Contents/Info.plist" 2>/dev/null || true
done

# Every library and framework the app carries, deepest first, then the app
# with its entitlements — as codesign wants it.
sign() {
  identity="$1"
  app_entitlements="$2"
  find "$app/Contents/Frameworks" -depth \( -name '*.dylib' -o -name '*.framework' \) -print | while read -r part; do
    codesign --force --sign "$identity" "$part"
  done
  codesign --force --entitlements "$app_entitlements" --sign "$identity" "$app"
  codesign --verify --strict --deep --verbose=2 "$app"
}

if [ -z "${APPLE_TEAM_ID:-}" ] || [ -z "${MACOS_STORE_PROFILE_PATH:-}" ] ||
   [ -z "${MACOS_STORE_APP_IDENTITY:-}" ] || [ -z "${MACOS_STORE_INSTALLER_IDENTITY:-}" ]; then
  echo "no signing means — the store build is signed ad hoc, no package is made, nothing is uploaded"
  # Signed again after Sparkle's helpers went, so the bundle stays valid
  # and runs, sandboxed, for a look at it.
  sign - "$entitlements"
  exit 0
fi

# The profile: which app and team it is for, and whether it grants
# communication notifications (#420).
profile_plist="$work/profile.plist"
security cms -D -i "$MACOS_STORE_PROFILE_PATH" > "$profile_plist"
app_id="$(/usr/libexec/PlistBuddy -c 'Print :Entitlements:com.apple.application-identifier' "$profile_plist")"
bundle="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' "$app/Contents/Info.plist")"
[ "$app_id" = "$APPLE_TEAM_ID.$bundle" ] ||
  { echo "the profile is for $app_id, the app is $APPLE_TEAM_ID.$bundle" >&2; exit 1; }

# The entitlements the app is signed with: the sandbox's, plus the app and
# team identifier Xcode would take from the profile.
signed="$work/Store.entitlements"
cp "$entitlements" "$signed"
/usr/libexec/PlistBuddy -c "Add :com.apple.application-identifier string $app_id" "$signed"
/usr/libexec/PlistBuddy -c "Add :com.apple.developer.team-identifier string $APPLE_TEAM_ID" "$signed"
if [ "$(/usr/libexec/PlistBuddy -c 'Print :Entitlements:com.apple.developer.usernotifications.communication' "$profile_plist" 2>/dev/null)" = true ]; then
  /usr/libexec/PlistBuddy -c "Add :com.apple.developer.usernotifications.communication bool true" "$signed"
else
  echo "the profile does not grant communication notifications — the app is signed without them"
fi

cp "$MACOS_STORE_PROFILE_PATH" "$app/Contents/embedded.provisionprofile"
sign "$MACOS_STORE_APP_IDENTITY" "$signed"
codesign -d --entitlements :- "$app" > "$work/check.plist" 2>/dev/null
# PlistBuddy, not plutil: plutil reads the dots of the key as a path (#629).
[ "$(/usr/libexec/PlistBuddy -c 'Print :com.apple.security.app-sandbox' "$work/check.plist" 2>/dev/null)" = true ] ||
  { echo "the app is not signed with the App Sandbox" >&2; exit 1; }

# The installer package App Store Connect takes; no notarisation, the store
# reviews the app itself.
pkg="$out/covey-app_${name}_macos-store.pkg"
productbuild --component "$app" /Applications --sign "$MACOS_STORE_INSTALLER_IDENTITY" "$pkg"
pkgutil --check-signature "$pkg"

if [ -n "${APPLE_API_KEY_PATH:-}" ]; then
  # altool reads the key from a folder of its own, by its id.
  keys="$HOME/.appstoreconnect/private_keys"
  mkdir -p "$keys"
  cp "$APPLE_API_KEY_PATH" "$keys/AuthKey_${APPLE_API_KEY_ID}.p8"
  xcrun altool --upload-app --type macos --file "$pkg" \
    --apiKey "$APPLE_API_KEY_ID" --apiIssuer "$APPLE_API_ISSUER_ID"
  rm -f "$keys/AuthKey_${APPLE_API_KEY_ID}.p8"
fi
