#!/bin/sh
# Builds the iPhone app for the release (#429) and, when the means are there,
# signs it for the App Store and uploads it to App Store Connect, where
# TestFlight hands it to the testers.
#
#   cd mobile && tool/ios_release.sh <build-name> <build-number> <out-dir>
#
# Signing, from the environment:
#   APPLE_TEAM_ID        the team the app is signed for
#   IOS_PROFILE_PATH     an App Store provisioning profile (.mobileprovision)
#                        for the app, made for an "Apple Distribution"
#                        certificate whose identity is in the keychain
#   empty: the app is built without signing, and nothing is uploaded
# Upload, only with a signature:
#   APPLE_API_KEY_PATH   the App Store Connect API key (.p8)
#   APPLE_API_KEY_ID, APPLE_API_ISSUER_ID
#
# Push notifications (#431), from the checkout:
#   ios/Runner/GoogleService-Info.plist   the app's Firebase project; the
#                        release job writes it from IOS_GOOGLE_SERVICE_INFO.
#                        Without it the app is built without push.
#
# Writes <out-dir>/covey-app_<build-name>_ios.ipa when signed.
set -eu

name="$1"
number="$2"
out="$3"
mkdir -p "$out"

if [ -f ios/Runner/GoogleService-Info.plist ]; then
  firebase=yes
else
  firebase=""
  echo "no ios/Runner/GoogleService-Info.plist — this build has no push notifications"
fi

if [ -z "${APPLE_TEAM_ID:-}" ] || [ -z "${IOS_PROFILE_PATH:-}" ]; then
  echo "no signing means — building without signing, nothing is uploaded"
  flutter build ios --release --no-codesign --build-name "$name" --build-number "$number"
  exit 0
fi

# The profile where Xcode looks for it, under its UUID; both places, since
# Xcode 16 moved the folder and older tools read the old one.
plist="$(mktemp)"
security cms -D -i "$IOS_PROFILE_PATH" > "$plist"
uuid="$(/usr/libexec/PlistBuddy -c 'Print :UUID' "$plist")"
profile="$(/usr/libexec/PlistBuddy -c 'Print :Name' "$plist")"
bundle="$(/usr/libexec/PlistBuddy -c 'Print :Entitlements:application-identifier' "$plist" | sed 's/^[^.]*\.//')"
rm -f "$plist"
for dir in "$HOME/Library/MobileDevice/Provisioning Profiles" "$HOME/Library/Developer/Xcode/UserData/Provisioning Profiles"; do
  mkdir -p "$dir"
  cp "$IOS_PROFILE_PATH" "$dir/$uuid.mobileprovision"
done

# Manual signing through the xcconfig the project already reads for a
# developer's team (ios/Flutter/Signing.xcconfig, #345) — not as build
# settings on the command line, which would reach the pods as well and
# make them ask for a profile. A developer's own file is put back after.
xcconfig="ios/Flutter/Signing.xcconfig"
backup=""
if [ -f "$xcconfig" ]; then
  backup="$(mktemp)"
  cp "$xcconfig" "$backup"
fi
restore() {
  if [ -n "$backup" ]; then mv "$backup" "$xcconfig"; else rm -f "$xcconfig"; fi
}
trap restore EXIT
cat > "$xcconfig" <<EOF
DEVELOPMENT_TEAM = $APPLE_TEAM_ID
CODE_SIGN_STYLE = Manual
CODE_SIGN_IDENTITY = Apple Distribution
PROVISIONING_PROFILE_SPECIFIER = $profile
EOF

options="$(mktemp -d)/ExportOptions.plist"
cat > "$options" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>method</key><string>app-store-connect</string>
  <key>teamID</key><string>$APPLE_TEAM_ID</string>
  <key>signingStyle</key><string>manual</string>
  <key>signingCertificate</key><string>Apple Distribution</string>
  <key>provisioningProfiles</key>
  <dict><key>$bundle</key><string>$profile</string></dict>
  <key>uploadSymbols</key><true/>
</dict>
</plist>
EOF

flutter build ipa --release --build-name "$name" --build-number "$number" --export-options-plist "$options"
ipa="$out/covey-app_${name}_ios.ipa"
cp build/ios/ipa/*.ipa "$ipa"

# What Apple will check first: signed for the store, push for production.
check="$(mktemp -d)"
unzip -q "$ipa" -d "$check"
codesign -d --entitlements :- "$check"/Payload/*.app > "$check/entitlements.plist" 2>/dev/null
[ "$(plutil -extract aps-environment raw "$check/entitlements.plist" 2>/dev/null)" = production ] ||
  { echo "the app is not signed for production push" >&2; exit 1; }
# And the Firebase project it registers with, when one was given.
if [ -n "$firebase" ] && ! [ -f "$check"/Payload/*.app/GoogleService-Info.plist ]; then
  echo "GoogleService-Info.plist did not reach the app" >&2
  exit 1
fi
rm -rf "$check"

if [ -n "${APPLE_API_KEY_PATH:-}" ]; then
  # altool reads the key from a folder of its own, by its id.
  keys="$HOME/.appstoreconnect/private_keys"
  mkdir -p "$keys"
  cp "$APPLE_API_KEY_PATH" "$keys/AuthKey_${APPLE_API_KEY_ID}.p8"
  xcrun altool --upload-app --type ios --file "$ipa" \
    --apiKey "$APPLE_API_KEY_ID" --apiIssuer "$APPLE_API_ISSUER_ID"
  rm -f "$keys/AuthKey_${APPLE_API_KEY_ID}.p8"
fi
