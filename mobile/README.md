# covey mobile

The covey app for iOS and Android: the chat where the person is. What it is,
what it is not and what it must never do is in
[`spec/27-mobile-app.md`](../spec/27-mobile-app.md); this file only says how to
work on it.

**Status: a first slice, and a prototype.** It signs in with an API key, which
spec/27 names as an interim and not as the design — the device badge
(decision 1) replaces it. There is no push yet (decision 2) and no idempotency
key on writes (decision 3).

## What it does

- **Where is your covey?** Instance address (HTTPS only) and an API key, kept
  in the Keychain / Keystore.
- **What waits** — the open points of the inbox, most urgent first. Read-only:
  decisions are made in the web interface for now.
- **Colleagues** — the agents, grouped by department, without applicants.
- **The thread** — a message hands work over; an answer is chosen at the
  question it answers. `woken: false` is said in one line, not retried.

The organisation must have the team surface switched on (Admin →
Organization → Team surface, #328). Off, the app reads along and says why it
cannot write.

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
