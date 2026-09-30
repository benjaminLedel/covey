# App Privacy answers

The answers for App Store Connect → **App Privacy**, derived from what the code sends. Apple counts data as *collected* when it leaves the device and is kept longer than the request needs, by the developer or a partner. Two receivers matter here:

- **The covey server the app is paired with.** For a self-hosted installation that is the organisation's own server, not ours. For **app.covey.work** it is ours, so every answer below is given as if the server were ours, which is the stricter reading and the one that covers both.
- **Google (Firebase Cloud Messaging)**, for push notifications on the iPhone and iPad only. The Mac app uses no push service.

Neither receiver uses the data for tracking, advertising or analytics. The app contains no analytics SDK, no crash reporter and no advertising SDK: the only third-party SDK that talks to a network is Firebase Messaging on iOS (`mobile/ios/Runner/AppDelegate.swift`, `FirebaseMessaging` in `mobile/ios/Runner.xcodeproj/project.pbxproj`). The diagnostic log (`mobile/lib/diagnostics.dart`) is written to a file on the device when the person switches it on, holds no content, and is never sent.

## Summary

| Question | Answer |
|---|---|
| Do you or your third-party partners collect data from this app? | **Yes** |
| Used for tracking? | **No**, for every type below |
| Linked to the user's identity? | **Yes** for everything the server keeps (it belongs to the seat the API key works from); the push token is linked too, since the server stores it under the seat |
| Purpose | **App Functionality** for every type below; nothing else |

## Data types to declare

| Apple category → type | Collected | What and where in the code |
|---|---|---|
| Contact Info → **Name** | Yes, linked, App Functionality | The seat's display name lives on the server and is shown in the app (`GET /auth/me`, `mobile/lib/api.dart` `me()`). Colleagues' names come with conversations. The app does not ask for it, but the account it signs in to holds it. |
| Contact Info → **Email Address** | Yes, linked, App Functionality | As the name: the seat's e-mail (`Me.email`, `mobile/lib/models.dart`). |
| User Content → **Emails or Text Messages** | Yes, linked, App Functionality | Messages to agents and in conversations (`send`, `reply`, `postConversationMessage` in `mobile/lib/api.dart`). |
| User Content → **Photos or Videos** | Yes, linked, App Functionality | A profile photo (`setPhoto`), pictures inserted into notes (`uploadNoteMedia`), photos and videos attached to a message (`uploadToInbox`). Only what the person picks. |
| User Content → **Other User Content** | Yes, linked, App Functionality | Notes and meeting transcripts (`createNote`, `updateNote`), files attached to a message (`uploadToInbox`), and on the Mac dictated text sent for clean-up together with the name of the app it goes to (`cleanDictation`). |
| User Content → **Audio Data** | **No** | Speech is recognised on the device (sherpa-onnx, `mobile/lib/sherpa.dart`, `mobile/lib/dictation.dart`). No audio leaves the device; only the recognised text does, as a note. |
| Identifiers → **User ID** | Yes, linked, App Functionality | The API key the pairing hands out (`redeemPairing`, `mobile/lib/pairing.dart`) identifies the seat on every request, and the server's seat id comes back in `/auth/me`. |
| Identifiers → **Device ID** | Yes (iOS only), linked, App Functionality | The Firebase Cloud Messaging token, registered with the server (`registerPushDevice`, called from `mobile/lib/push.dart`) and used by the server or the relay to address the notification. Firebase Messaging also sends a Firebase installation ID to Google to issue the token. At pairing the app sends the device's name (`deviceName()`, `mobile/lib/pairing.dart`), which names the API key in the web interface. |
| Usage Data, Browsing History, Other Usage Data | **See below** (macOS only, opt-in) | The activity log. |
| Diagnostics → Crash Data, Performance Data, Other Diagnostic Data | **No** | Nothing is sent; the diagnostic log stays on the device. |
| Location, Health, Financial, Contacts, Sensitive Info, Purchases, Search History | **No** | The app reads none of these. Search in the app filters what is already loaded (`mobile/lib/screens/team_space.dart`) or asks the server for the person's own notes (`notes(q:)`), which the server does not keep as a history. |

## macOS only: the activity log

The Mac app has an activity log (`mobile/lib/activity.dart`, #363), **off unless the person switches it on**. While on, it samples every five seconds which app is in front, the window title, the page address in a browser, the focused field and an excerpt of it, and sends the merged sessions to the server every two minutes, private to the seat (`addActivity`). It never records keystrokes, screenshots, secure fields or password managers.

If the Mac App Store build ships with it, the macOS answers add, all **linked, App Functionality, not tracking**:

- Browsing History → **Browsing History** (page addresses)
- Usage Data → **Other Usage Data** (which apps and windows were in front, and for how long)
- User Content → **Other User Content** (already declared; the field excerpts fall under it)

The activity log reads other apps' windows through the Accessibility API, and dictate-anywhere pastes into other apps with a synthesised ⌘V; the current Mac app runs outside the App Sandbox for that reason (`mobile/macos/Runner/Release.entitlements`). A Mac App Store build must be sandboxed, so whether these two features ship there is open (see [README.md](README.md#mac-app-store-build)). If they do not, the macOS answers are the same as the iOS ones minus the Device ID row.

## What passes through Apple and Google without being kept by us

A push notification carries the agent's name, what it did ("Bea has a question", in the device's language), the unread count, the agent's or conversation's id and the chosen sound; with the organisation's preview switched on, also the first line of the message (`docs/en/operations/apps.md`, *What leaves the instance*). It travels through Google's FCM and Apple's APNs, and through the app.covey.work relay when an installation uses it. Apple's questionnaire does not count data passed on only to deliver it; it is named here so the privacy policy can say it.

## The privacy policy

The App Store links `https://covey.work/privacy` (English) and `https://covey.work/de/datenschutz` (German); both answer 200. As of this writing the page covers the website and the signed-in interface on app.covey.work, and says nothing about the app: not Firebase Cloud Messaging, not the push relay, not the on-device speech recognition, not the Mac's activity log. Apple requires the linked policy to describe the app's data practices, so a section on the app has to be added before submitting.
