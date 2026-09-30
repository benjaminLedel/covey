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
- [ ] **Export compliance:** `ITSAppUsesNonExemptEncryption` is `false` in the iOS `Info.plist` (HTTPS only); add the same key to the macOS `Info.plist` or answer the question on each upload.
- [ ] **Pricing and availability:** free, no in-app purchases.
- [ ] **Build:** pick the TestFlight build the release uploaded (iOS), and a Mac App Store build once there is one (below).
- [ ] **Submit for review**, iOS and macOS as separate submissions of the same app.

## Mac App Store build

The release workflow's `mac-app` job (`.github/workflows/release.yml`) builds a **Developer ID** app for direct download: `flutter build macos --release`, signed with a *Developer ID Application* certificate, notarised with `notarytool`, stapled and zipped by `mobile/tool/macos_release.sh`, then attached to the GitHub release with a Sparkle appcast. The Mac App Store takes none of that; it needs a second path, not built yet:

1. **Certificates.** An *Apple Distribution* certificate (signs the app) and a *Mac Installer Distribution* certificate (signs the installer package), both from the team's developer account, stored as secrets like the Developer ID one.
2. **Provisioning profile.** A *Mac App Store* distribution profile for `work.covey.coveyMobile`, embedded in the app as `Contents/embedded.provisionprofile`. With a profile the restricted entitlements the release leaves out today (communication notifications, see `Release.entitlements`) become possible too.
3. **App Sandbox.** Required on the Mac App Store; `com.apple.security.app-sandbox` is `false` today (`mobile/macos/Runner/Release.entitlements`). Two features depend on running outside it:
   - *dictate anywhere* (`mobile/lib/anywhere.dart`) posts a synthesised ⌘V to the app in front,
   - the *activity log* (`mobile/lib/activity.dart`) reads other apps' windows and fields through the Accessibility API.

   Both need to be switched off in a sandboxed build, or reworked; the store texts in `metadata-macos/` already leave them out. If they ship after all, add a paragraph under ON THE MAC and the extra privacy answers in [`app_privacy.md`](app_privacy.md#macos-only-the-activity-log). Everything else the app does (network client, microphone, camera, user-selected files, notifications, the Mac's audio for meetings) has sandbox entitlements.
4. **No self-update.** Sparkle must not be in a store build: build without `COVEY_UPDATE_FEED`, and `Updater.supported` (`mobile/lib/updater.dart`) stays off.
5. **Category.** `LSApplicationCategoryType` = `public.app-category.business` in `mobile/macos/Runner/Info.plist`.
6. **Package.** Sign the app with the Apple Distribution identity and the sandbox entitlements, then build a signed installer:
   `productbuild --component covey.app /Applications --sign "3rd Party Mac Developer Installer: …" covey.pkg`
   No notarisation: App Store Connect does its own review.
7. **Upload.** `xcrun altool --upload-app -t macos -f covey.pkg --apiKey … --apiIssuer …` with the App Store Connect API key the release already holds, or the Transporter app by hand. The build then appears under the macOS platform of the same record.
