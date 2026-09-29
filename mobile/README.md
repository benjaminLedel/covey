# covey mobile

The covey app for iOS and Android — and, from the same code, for macOS, Windows
and Linux (#334): the chat where the person is. What it is,
what it is not and what it must never do is in
[`spec/27-mobile-app.md`](../spec/27-mobile-app.md); this file only says how to
work on it.

**Status: a first slice, and a prototype.** It signs in with an API key, which
spec/27 names as an interim and not as the design — the device badge
(decision 1) replaces it. There is no idempotency key on writes yet
(decision 3).

## What it does

- **Where is your covey?** Scan the QR code under Profile & Settings → Mobile
  app in the web interface (#330): the app exchanges its one-time code for an
  API key named after the device, kept in the Keychain / Keystore. The
  fallback is typing the address — `app.covey.work` by default — and a key.
- **What waits** — the open points of the inbox, most urgent first. Read-only:
  decisions are made in the web interface for now.
- **Colleagues** — the agents, grouped by department, without applicants.
- **The thread** — a message hands work over; an answer is chosen at the
  question it answers. `woken: false` is said in one line, not retried.

The organisation must have the team surface switched on (Admin →
Organization → Team surface, #328). Off, the app reads along and says why it
cannot write.

On a desktop the app has no scanner: it pairs through “Open in the app” on the
web's pairing card (a `covey://` link), and a wide window shows the list and
the thread side by side. macOS and Windows are built with every release;
Linux is scaffolded and needs its `covey://` registration (a `.desktop` file)
once there is a package.

## The desktop apps

Both are built by `.github/workflows/release.yml` on a `v*` tag and attached
to that release, which is where the web's apps dialog links to (the release
of the instance's own version). Started by hand, the workflow is a rehearsal:
it builds and keeps the files on the run, and publishes nothing.

- **Mac** (#406): `covey-app_<version>_macos.zip`, signed and notarised when
  the repository holds the certificate, and updating itself through Sparkle
  (#421). `tool/macos_release.sh` packs it.
- **Windows** (#436): `covey-app_<version>_windows.zip`, the contents of
  `build/windows/x64/runner/Release` — `covey_mobile.exe` with its DLLs and
  `data\`. It needs no installer: unpacked anywhere, it runs. It is **not
  code-signed**, so SmartScreen asks on the first start (More info → Run
  anyway), and it does not update itself; a new version is a new download.
  Built locally with `flutter build windows --release` on a Windows machine
  with Visual Studio's C++ workload.

On every start the Windows app checks that `covey://` is registered for the
current user (`HKEY_CURRENT_USER\Software\Classes\covey`, no administrator
rights) and points at its own executable, and writes the entry when it is
missing or points at another copy — after the folder was moved, the copy
started last wins (`windows/runner/url_scheme.cpp`). A link opens a second
process that hands it to the running window and ends (the app_links plugin's
`SendAppLinkToInstance`, `windows/runner/main.cpp`), so there is one window;
a plain second start brings that window forward. To remove the registration,
delete that key.

What the Windows app does not have, since each is built on macOS APIs:

- notifications — the Mac app shows them itself while it runs (#379); on
  Windows there are none
- dictate anywhere, the global shortcut (#355)
- the activity log (#363)
- recording the computer's own audio beside the microphone in a note (#364)
- updating itself (#421)
- the unified title bar (#356); Windows keeps its own

Dictation inside the app uses sherpa-onnx and `record`, both of which ship
for Windows.

## Working on it

```sh
cd mobile
flutter pub get
flutter test          # or: make test-mobile, from the repository root
flutter run           # against a device or simulator
```

Against a local `make run`, a debug build accepts `http://localhost:8494`
(`http://10.0.2.2:8494` from the Android emulator); every other address must be
HTTPS. The iOS deployment target is 15.0.

A debug build can skip the connect screen:

```sh
flutter run --dart-define=COVEY_INSTANCE=http://localhost:8494 --dart-define=COVEY_KEY=covey_…
```

Release builds ignore both.

Push notifications (#379, #424, #431) come through the app's Firebase project,
on both platforms, and its configuration belongs to whoever ships the app; it
is not in the repository (and is ignored), and a build without it works and
has no push. On Android that is the project's `google-services.json`, placed
at `android/app/google-services.json`; on the iPhone its
`GoogleService-Info.plist`, placed at `ios/Runner/GoogleService-Info.plist`,
which a build phase copies into the app when it is there (the release job
writes it from the secret `IOS_GOOGLE_SERVICE_INFO`). The iPhone app hands
Firebase its APNs token and registers the FCM token with the instance, so the
project also needs the app's APNs key (Firebase console, Project settings,
Cloud Messaging, Apple app configuration). The instance then needs that
project's service account, uploaded under Platform → Push, or a relay that
holds it — see [the operations guide](../docs/en/operations/operations.md#push-notifications). The notification sounds are
synthesised by `sounds/synth.py`, which writes the iOS/macOS `.caf` files and
the Android copies in `android/app/src/main/res/raw`.

## Wording

The app uses the web's keys with the web's wording — it has no catalogue of its
own. `assets/locales/*.json` is a copy of exactly the keys `lib/` uses, taken
from `web/src/locales`. Sentences only the app needs live under `mobile.*` in
the web catalogues, in all ten languages.

After using a new key, or when the wording changed on the web:

```sh
dart run tool/sync_locales.dart    # or: make mobile-locales
```

`test/locales_test.dart` fails until the copy is current.
