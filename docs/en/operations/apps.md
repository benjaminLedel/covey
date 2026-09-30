---
slug: apps
title: The covey app
description: 'The app for iPhone, Android, Mac and Windows: where the builds come from, pairing by QR code or covey:// link, notifications and limits per platform.'
faq:
  - q: Is the covey app in the App Store or on Google Play?
    a: 'Not yet. The apps dialog in the web interface says so for both. Each release uploads the iPhone build to App Store Connect, where TestFlight hands it to the project''s internal testers; nothing is submitted for review. The release publishes no Android build. The Mac and Windows apps are attached to every GitHub release.'
  - q: How does the app connect to my instance?
    a: 'By pairing: the web interface shows a QR code that works once and for five minutes, the app scans it and exchanges it for an API key named after the device. On a desktop the same code opens the app through a covey:// link. The instance has to be reachable over HTTPS.'
  - q: Why does the Windows app show no notifications?
    a: 'The Mac app shows them itself while it runs, with macOS''s own APIs; the Windows app has no counterpart yet, and no push service reaches a desktop. Phones get their notifications through Firebase Cloud Messaging.'
---

# The covey app

One Flutter app, built from [`mobile/`](../../../mobile/README.md), for the iPhone and iPad, Android, the Mac and Windows; a Linux build is scaffolded. It is the place where a person works *with* the agents rather than the place they are run from: it pairs with an instance, shows the agents and what waits for this person, carries a message to an agent and an answer back, and dictates on the device. Configuring agents, secrets, guard rails or the platform stays in the web interface — the app has none of it, on purpose ([`spec/27-mobile-app.md`](../../../spec/27-mobile-app.md)).

**Status: a prototype.** It signs in with an API key that the pairing hands it; the device-bound badge the specification asks for does not exist yet. Writing to an agent needs the organisation's team surface switched on (**Administration → Organization**, tab **Profile**, card **Team surface**); off, the app reads along and says why it cannot write.

The app is the same for every installation: on its first start it asks which covey to work with.

## Getting the app

In the web interface, the user menu has an entry **covey app**. It lists the four platforms, links the Mac and Windows downloads, and shows the pairing code below them.

| Platform | Where it comes from |
|---|---|
| iPhone and iPad | **Not in the App Store yet.** Every release uploads the build to App Store Connect, where TestFlight hands it to the project's internal testers; nothing is submitted for review. iOS 15 or later. |
| Android | **Not on Google Play yet**, and the release publishes no Android build. Build it from `mobile/` with Flutter. |
| Mac | `covey-app_<version>_macos.zip`, attached to the GitHub release. macOS 12 or later. It updates itself through Sparkle when the release carries an update feed. |
| Windows | `covey-app_<version>_windows.zip`, attached to the GitHub release. Unpack it anywhere and start `covey_mobile.exe`; there is no installer. |
| Linux | Scaffolded, no package. It lacks its `covey://` registration (a `.desktop` file). |

The download links in the apps dialog point at the release of **the instance's own version**, so app and server match; an instance built between two tags links the latest release, and one whose source is not on GitHub gets no link rather than a wrong one.

**Mac.** The app is signed and notarised when the release was built with the project's certificate. An unsigned build says so in the release notes, and Gatekeeper then refuses the first start: right-click `covey.app` → Open, or `xattr -dr com.apple.quarantine covey.app`.

**Windows.** The app is **not code-signed**, so SmartScreen stops it on the first start: *More info*, then *Run anyway*. It does not update itself; a new version is a new download. On every start it registers `covey://` for the current user (`HKEY_CURRENT_USER\Software\Classes\covey`, no administrator rights) and points it at its own executable — after the folder was moved, the copy started last wins. There is one window: a link or a second start hands over to the running one. To remove the registration, delete that key.

## Pairing

The app does not ask for a password. It is paired from a signed-in browser:

1. In the web interface, open the user menu → **covey app** → *Pair with this covey*, or **Profile & Settings** → *Mobile app* → **Pair the mobile app**.
2. A QR code appears. It works **once and for five minutes**; the page counts down and notices when the app has used it.
3. In the app, tap **Scan QR code** and point the camera at it.

The app exchanges the code for an **API key named after the device** and keeps it in the Keychain (Apple) or the Keystore (Android). The key appears among the person's API keys under **Profile & Settings**, where it can be revoked like any other ([API keys](api-keys.md)). It carries the rights of that person's seat and nothing more.

What the QR code carries is an ordinary link to the instance, `https://<host>/pair?code=…`. On an instance whose host the app claims (app.covey.work, or an installation that ships the app under its own host, see [app links](operations.md#app-links)), the phone's camera opens the app with it directly. Everywhere else the link opens the `/pair` page, which hands the code on to the app through `covey://`.

**On a desktop** there is no camera to scan with. The pairing card has a button **Open in the app**, a `covey://pair?instance=…&code=…` link that the Mac or Windows app takes.

**HTTPS only.** The app connects to HTTPS addresses only, and the pairing page warns when it is not served over HTTPS. The one exception is a debug build against `http://localhost:8494` (`http://10.0.2.2:8494` from the Android emulator).

Without a camera or a browser at hand, the app also takes the address and an API key typed in by hand.

## Notifications

When an agent asks a question, replies in a conversation, or finishes or fails a task that came from a message, the people involved are notified — whoever wrote to that agent in the last two weeks, the person the task came from, and, for a question, the agent's human supervisor. What somebody has already read is not announced.

| Platform | How |
|---|---|
| iPhone and iPad | Firebase Cloud Messaging, which passes the notification on to Apple |
| Android | Firebase Cloud Messaging |
| Mac | The app shows them itself while it runs, from what is unread; nothing goes through a push service |
| Windows, Linux | None |

How the instance reaches Firebase — **directly** with a service account of its own, **through a relay** (by default `https://app.covey.work`, which holds the service account of the project's app builds), or **off** — is set by an installation administrator under **Administration → Platform**, tab **Push**. The [operations guide](operations.md#push-notifications) has the details, the environment variables and the settings keys.

**What leaves the instance.** A notification carries the device's token, the agent's name and what it did ("Bea has a question", in the device's language), the unread count, the agent's id so that a tap opens its thread, and the chosen sound. Nothing of what was said is in it. An organisation can switch on a preview under **Administration → Organization**, tab **Profile**, card **Push notifications**; then the first line of what was said goes along too. Either way the notification passes through Google and, for an iPhone, Apple — and through the relay, where one is used.

A notification opens the thread with the agent it is about. The app offers covey's own notification sounds, the system's sound, or none.

## Calling an agent (trial)

On the Mac, the thread with an agent has a **Call** button (⌘⇧C). The call fills the window with the agent's face, which listens, thinks and speaks; the person talks, and the agent answers aloud. It is a trial: the Mac only, switched under **Settings → Calls (trial)**, on by default, for this device alone.

The audio stays on the Mac. Silero's voice detector cuts a turn at a pause of about 1 s (set in the call settings), the speech model recognises it, and only the text goes into the conversation — as an ordinary message, marked as said in a call (`meta.via = "call"`), so the chat's own path answers it and the conversation records it like any other. The reply is spoken by the organisation's voice provider ([Operations](operations.md#voice-provider)) in the spoken voice of the agent's covey voice, streamed as it is synthesised; without a provider, or while it cannot be reached, the Mac's own speech synthesis speaks it, in the language it is written in, with a voice chosen per agent from the voices installed for that language, and the call says so; a long reply is cut to its first sentences and the rest stays in the chat. An answer to something said in the call carries a spoken form beside the written one (#502) — a few short sentences without ids or links — and the call speaks that, adding that the details are in the chat when it left some out. A task opened in the call is acknowledged aloud, and its result is spoken when it arrives while the call is still open. Speaking while the agent speaks stops it; **Mute** stops listening, **Hang up** (or Esc) ends the call, and nothing listens once it is closed.

A call works without headphones (#507). The microphone runs on the same audio engine that plays the agent's voice, with Apple's voice processing on: echo cancellation takes the agent's voice, its fillers and the call's sounds out of the microphone before the voice detector hears them, noise suppression and automatic gain clean up the rest. With it, speaking over the agent stops it after about 0.1 s instead of the tuned barge-in time — except for the first 3 s after voice processing started, while the canceller settles, when the tuned time holds (#511). While the agent's voice, a filler or the greeting plays, a moment of the microphone counts as the person only when it is at least 6 dB louder than what plays at that moment (the voice provider's audio, whose level the call knows): what of the agent's voice gets past the canceller is quieter than that. The check that drops a turn repeating what the agent just said stays as a second line. Other audio on the Mac is ducked as little as macOS allows (macOS 14 and later). While the call is open the Mac shows the microphone as in use, muted too — **Mute** stops listening, **Hang up** releases the microphone a second later.

What is not cancelled: the Mac's own speech synthesis, used when no voice provider speaks, plays outside that engine — then the tuned barge-in time holds as before. If voice processing cannot be turned on, or the call's engine does not start, the call falls back to the plain microphone and behaves as with headphones off before: short bursts do not interrupt, echoes are dropped by their text. The diagnostics log says per call which it is — a line `microphone: voice processing, echo cancellation yes|no, input "<device>" 48000 Hz → 16000 Hz, output "<device>" …` — and logs a device change mid-call. When the agent still hears itself, or what reaches it is noisy or clipped — turns recognised wrong, first words missing, a pumping background: look at that line first; check that input and output are the devices meant (a Bluetooth headset switches both, and AirPods drop to their low-quality call mode while their microphone is in use); an external microphone far from the speakers, or an external speaker the Mac cannot hear as its own output, cancels worse than the built-in pair; a loud external amplifier or a speaker volume near the top can overdrive the cancellation. Better Mac voices, for when no provider speaks, are installed in System Settings → Accessibility → Spoken Content.

## What works where

The app is one code base, but several features are built on macOS APIs and exist only there:

| | iPhone / iPad | Android | Mac | Windows |
|---|---|---|---|---|
| Pairing | QR code | QR code | `covey://` link | `covey://` link |
| Notifications | push | push | while the app runs | — |
| Dictation inside the app | yes | yes | yes | yes |
| Dictate anywhere (global shortcut) | — | — | yes | — |
| Activity log | — | — | yes | — |
| Calling an agent (trial) | — | — | yes | — |
| The computer's own audio beside the microphone in a note | — | — | yes | — |
| Updating itself | TestFlight, for testers | — | Sparkle | — |

Dictation is recognised on the device with sherpa-onnx; the speech model comes from the instance the first time somebody dictates ([speech model](operations.md#speech-model)).

## Further reading

- [`mobile/README.md`](../../../mobile/README.md) — building and working on the app, the desktop builds, the Firebase configuration
- [`spec/27-mobile-app.md`](../../../spec/27-mobile-app.md) — what the app is, what it is not, what it must never do
- [Operations](operations.md#push-notifications) — push, app links, the speech model
