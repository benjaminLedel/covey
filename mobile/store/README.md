# App Store listing

Everything the App Store listing of the covey app needs, for the iPhone and iPad and for the Mac, in English (primary, `en-US`) and German (`de-DE`). The App Store Connect record is "covey: AI agents at work", bundle id `work.covey.coveyMobile`; the Mac app has the same bundle id, so adding the macOS platform to that record makes it one universal purchase.

The layout is the one `fastlane deliver` reads, so the texts could be uploaded with it later; fastlane itself is not a dependency of this repository.

| Path | What |
|---|---|
| `metadata/<locale>/` | iOS texts: `name`, `subtitle`, `description`, `keywords`, `promotional_text`, `release_notes`, `support_url`, `marketing_url`, `privacy_url` (`.txt`) |
| `metadata/primary_category.txt`, `secondary_category.txt` | `BUSINESS`, `PRODUCTIVITY` |
| `metadata-macos/` | the same for macOS; subtitle, keywords, promotional text and description differ (pairing by link, notifications while the app runs, keyboard, the Mac's audio in meetings) |
| `screenshots/<device>/<locale>/NN_name.png` | the screenshots, see below |
| [`review_notes.md`](review_notes.md) | sign-in and notes for App Review, with placeholders for the demo account |
| [`app_privacy.md`](app_privacy.md) | the App Privacy answers, with the code each one rests on |
| [`age_rating.md`](age_rating.md) | category and age rating answers |

## Apple's limits

| Field | Limit | Here |
|---|---|---|
| Name | 30 characters | 24 |
| Subtitle | 30 characters | 26–28 |
| Keywords | 100 characters, comma-separated, no spaces after commas; words from the name count already and are not repeated | 93–99 |
| Promotional text | 170 characters, changeable without a new version | 143–153 |
| Description | 4000 characters | 2350–3150 |
| What's New | 4000 characters; not asked for on a platform's first version | ~520 |
| Screenshots | 1–10 per device size and locale, PNG or JPEG, **no alpha channel** | 4–6 |

A quick check after editing:

```sh
cd mobile/store
for f in metadata*/*/{name,subtitle,keywords,promotional_text}.txt; do printf '%4d %s\n' "$(tr -d '\n' < "$f" | wc -m)" "$f"; done
```

The URLs answer 200 as of this writing: `https://covey.work` and `/de`, `https://covey.work/contact` and `/de/kontakt`, `https://covey.work/privacy` and `/de/datenschutz`. The product page `https://covey.work/product/companion` describes the app and would also do as the marketing URL.

## Screenshots

Drawn by [`test/store_screenshots_test.dart`](../test/store_screenshots_test.dart) from the app's real screens, fed by a fake instance with made-up data (Ada Berger, Jonas Weber, Lena Hoffmann; the agents Mira, Paul, Theo, Emil, Nora and Ida; `example.org` addresses). Nothing in them comes from a real installation. The test is skipped unless asked for:

```sh
cd mobile
STORE_SHOTS=1 flutter test test/store_screenshots_test.dart
```

It writes to `store/screenshots/`, or to `STORE_SHOTS_DIR`. Times are relative to the moment the test runs, so a new run changes the clock in the pictures and nothing else.

| Device folder | Pixels | App Store Connect size | Shots |
|---|---|---|---|
| `iphone-6.9` | 1320 × 2868 | iPhone 6.9" (required) | team, thread with an agent, group, notes, dictation, pairing |
| `ipad-13` | 2752 × 2064, landscape | iPad 13" (required, the app runs on iPad) | team beside a thread, group, notes beside a meeting note, pairing |
| `mac` | 2880 × 1800 | Mac, 16:10 | the same four, in the Mac's window with its traffic lights |

The app draws the iPhone and iPad screenshots with the platform set to iOS and the Mac ones with it set to macOS (`Host.debugOverride`, `mobile/lib/host.dart`), whatever machine runs the test. The status bar is left empty. The PNGs are written without an alpha channel, which App Store Connect refuses.

What the pictures leave out on purpose:

- **The office view.** It is built but switched off in the app (`officeSpace = false` in `mobile/lib/screens/home.dart`); a screenshot must show what the app does.
- **Dictation on the iPad and the Mac.** There a new note opens as a page over the whole window, and the picture is mostly empty; the open meeting note shows the Dictate button instead.
- **Settings.** The screen reads as a list of technical values (role `org_admin`, the speech model's size) and says little about the app.

`fastlane deliver` takes screenshots as `screenshots/<locale>/*.png` and tells iPhone from iPad by their size. To upload with it, copy `iphone-6.9/<locale>` and `ipad-13/<locale>` into one `<locale>` folder for iOS, and `mac/<locale>` into another tree for `--platform osx`.

## Checklist in App Store Connect

- [ ] **Add the macOS platform** to the app record (the platforms in the sidebar of the app). Same bundle id, so it becomes a universal purchase.
- [ ] **Privacy policy:** add a section on the app to `https://covey.work/privacy` and `/de/datenschutz` (Firebase Cloud Messaging, the push relay, on-device speech recognition, the Mac's activity log). The page does not mention the app yet; see [`app_privacy.md`](app_privacy.md).
- [ ] **App Information:** name, subtitle, category (`Business`, `Productivity`), privacy policy URL, content rights (the app shows no third-party content it has no rights to), age rating from [`age_rating.md`](age_rating.md).
- [ ] **Copyright** line for each version (not kept here).
- [ ] **Per locale and platform:** description, keywords, promotional text, support and marketing URL from `metadata/` and `metadata-macos/`; What's New from the second version on.
- [ ] **Screenshots:** iPhone 6.9", iPad 13" and Mac from `screenshots/`, in order.
- [ ] **App Privacy:** the answers in [`app_privacy.md`](app_privacy.md); publish them.
- [ ] **Demo organisation** on app.covey.work, set up as in [`review_notes.md`](review_notes.md); put the demo e-mail, password and notes into App Review Information. Keep the credentials out of this repository.
- [ ] **Export compliance:** `ITSAppUsesNonExemptEncryption` is `false` in the iOS and the macOS `Info.plist` (HTTPS only), so no upload asks.
- [ ] **Pricing and availability:** free, no in-app purchases.
- [ ] **Build:** pick the build the release uploaded, for iOS and for macOS (below).
- [ ] **Submit for review**, iOS and macOS as separate submissions of the same app.

## Mac App Store build

The release workflow (`.github/workflows/release.yml`) builds the Mac app twice:

- **`mac-app`**, the direct download: `flutter build macos --release`, signed with a *Developer ID Application* certificate, notarised, stapled and zipped by `mobile/tool/macos_release.sh`, attached to the GitHub release with a Sparkle appcast. Outside the App Sandbox (`macos/Runner/Release.entitlements`), with dictate-anywhere and the activity log.
- **`mac-store`**, for the Mac App Store (#486): `mobile/tool/macos_store_release.sh`, signed with an *Apple Distribution* certificate and a Mac App Store provisioning profile, packed as an installer package with `productbuild`, and on a tag uploaded to App Store Connect, where it appears under the macOS platform of this record. Nothing is submitted for review.

What makes the store build different:

1. **Chosen at build time.** `--dart-define=COVEY_STORE=true` sets `Host.appStore` (`mobile/lib/host.dart`), and the script writes `macos/Runner/Configs/Store.xcconfig` for the length of the build, which points the Release configuration's `CODE_SIGN_ENTITLEMENTS` at `macos/Runner/Store.entitlements` (through `COVEY_RELEASE_ENTITLEMENTS` in `AppInfo.xcconfig`). Without both, the build is the direct download, unchanged.
2. **App Sandbox.** `Store.entitlements`: `app-sandbox`, `network.client`, `files.user-selected.read-write`, `device.audio-input`, `device.camera`. The Mac's own audio in a meeting (a Core Audio tap) asks under `NSAudioCaptureUsageDescription`. Communication notifications (#420) are added at signing time when the profile grants them.
3. **Without the two features that reach into other apps.** *Dictate anywhere* (`mobile/lib/anywhere.dart`, a synthesised ⌘V) and the *activity log* (`mobile/lib/activity.dart`, the Accessibility API) report `supported = false` with `Host.appStore`, so neither starts nor shows in the settings. The texts in `metadata-macos/` leave them out, and [`app_privacy.md`](app_privacy.md#macos-only-the-activity-log) answers for a build without them.
4. **No self-update.** `Updater.supported` (`mobile/lib/updater.dart`) is false with `Host.appStore`, and the job sets no `COVEY_UPDATE_FEED`. The script also takes Sparkle's helpers (`Autoupdate`, `Updater.app`, `XPCServices`) out of the bundle and the `SU…` keys out of its `Info.plist`; the framework itself stays, since the update plugin links against it.
5. **Category and export compliance.** `LSApplicationCategoryType` = `public.app-category.business` and `ITSAppUsesNonExemptEncryption` = `false` in `mobile/macos/Runner/Info.plist`, for both builds.
6. **Signing and package.** The script signs the frameworks and then the app with the Apple Distribution identity and the sandbox entitlements (plus the application and team identifier the profile names), embeds the profile as `Contents/embedded.provisionprofile`, and builds `covey-app_<version>_macos-store.pkg` signed with the *Mac Installer Distribution* identity. No notarisation: App Store Connect reviews the app itself.
7. **Upload.** `xcrun altool --upload-app --type macos` with the App Store Connect API key the iPhone app is uploaded with. Build numbers are the workflow's run number, as for iOS.

The job's secrets; without the first five (a fork, a rehearsal) the store build is made and signed ad hoc, and nothing is packed or uploaded:

| Secret | What |
|---|---|
| `MACOS_APPSTORE_DISTRIBUTION_P12` | an *Apple Distribution* certificate with its private key, base64 (may be the same one as `IOS_DISTRIBUTION_P12`) |
| `MACOS_APPSTORE_DISTRIBUTION_P12_PASSWORD` | its password |
| `MACOS_INSTALLER_P12` | a *Mac Installer Distribution* certificate ("3rd Party Mac Developer Installer") with its private key, base64 |
| `MACOS_INSTALLER_P12_PASSWORD` | its password |
| `MACOS_APPSTORE_PROVISIONING_PROFILE` | a *Mac App Store* distribution profile for `work.covey.coveyMobile`, base64 |
| `APPLE_TEAM_ID` | shared with the iPhone app |
| `APPLE_API_KEY_P8`, `APPLE_API_KEY_ID`, `APPLE_API_ISSUER_ID` | shared with the iPhone app and the notarisation; the upload happens only on a tag |

Locally, unsigned:

```sh
cd mobile
tool/macos_store_release.sh 0.0.0 1 /tmp/store
codesign -d --entitlements - build/macos/Build/Products/Release/covey.app   # app-sandbox: true
```

A store build started by hand runs in its own container (`~/Library/Containers/work.covey.coveyMobile`), so it does not see the direct download's sign-in.
