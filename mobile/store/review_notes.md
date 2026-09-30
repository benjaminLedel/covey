# Notes for App Review

What goes into App Store Connect → the version → **App Review Information** (sign-in information and notes), for the iOS and the macOS version alike. Replace the placeholders before pasting; nothing real is stored in this repository.

| Field | Value |
|---|---|
| Sign-in required | Yes |
| User name | `<demo email>` |
| Password | `<demo password>` |
| Contact | the person who submits, from their own App Store Connect account |

## Before submitting: the demo organisation

The reviewer signs in to a demo organisation on app.covey.work, not to a real one. Set it up once and keep it for later reviews:

1. Create an organisation on app.covey.work for review only, with a made-up name.
2. Create a seat with `<demo email>` / `<demo password>`, role *org_admin* or *member*, so that it may chat.
3. Switch on the team surface: **Administration → Organization**, tab **Profile**, card **Team surface**. Without it the app shows the notes only.
4. Hire at least one agent that answers in a conversation, and write to it once from the web interface, so the reviewer sees a conversation with an answer in it.
5. Start one group conversation with that agent and a second seat, with a few lines in it.
6. Create an API key for the demo seat under **Profile & Settings → API keys**, named `app-review`. It is the `<demo API key>` below. Revoke it after the review.
7. Check that the instance serves a speech model (`COVEY_SPEECH_MODEL` not `off`, see [the speech model](../../docs/en/operations/operations.md#speech-model)), so dictation works.

## Notes (paste into the "Notes" field)

```
covey is the companion app to a covey server: an open-source platform (AGPL-3.0,
github.com/benjaminLedel/covey) on which an organisation runs AI agents with a
role, a backlog and a workplace each. The app is where the people of that
organisation talk to the agents and to each other. It has no account creation
of its own and no purchases; an organisation runs covey on its own server or
signs up on app.covey.work.

HOW TO SIGN IN
The app does not ask for a password. It is paired with a signed-in browser.
We prepared a demo organisation on app.covey.work for you.

Way 1, with a second device or a computer (recommended):
1. In a browser, open https://app.covey.work and sign in with
   <demo email> / <demo password>.
2. Open Profile & Settings → Mobile app → "Pair the mobile app". A QR code
   appears; it works once and for five minutes.
3. In the app, tap "Scan QR code" and point the camera at it.
   (Mac: click "Open in the app" on that page instead; the covey:// link
   pairs the Mac app.)

Way 2, on the device alone:
1. In the app, tap "Connect with an address and an API key instead"
   (on the Mac the fields are shown at once).
2. Address: app.covey.work
3. API key: <demo API key>
4. Tap "Connect".

WHAT TO TRY
- Team: the agent's conversation and the group. Write to the agent; it answers
  within a minute or two, as a colleague would.
- Notes: the + button creates a note; the microphone dictates into it.
  Recognition runs on the device; the speech model (several hundred MB) is
  fetched from the server the first time.

PERMISSIONS
- Camera (iPhone, iPad): only to read the pairing QR code, and to take a
  profile photo when the person asks for one in the settings.
- Camera (Mac): only to take a profile photo when asked for in the settings.
- Microphone: dictating notes and recording meetings. Speech is recognised on
  the device; no audio is stored or sent.
- Photo library (iPhone, iPad): photos and videos the person picks, attached to
  a message for an agent or used as the profile photo.
- Local network (iPhone, iPad): an organisation may run covey on a machine in
  its local network; the app connects to it when pointed there.
- System audio (Mac): in a meeting the person records, the other side of a
  call is transcribed on the Mac; no audio is stored or sent.
- Notifications: an agent's question, answer or finished task. They carry the
  agent's name and what happened, not the text of the message.

The app connects to HTTPS addresses only and uses no encryption beyond the
operating system's HTTPS.
```

## What the permission strings say

The notes above follow the usage strings in the app's `Info.plist` files. When one of them changes, change the notes with it.

| Key | iOS (`mobile/ios/Runner/Info.plist`) | macOS (`mobile/macos/Runner/Info.plist`) |
|---|---|---|
| `NSCameraUsageDescription` | The camera reads the QR code covey shows to pair this app, and takes your profile photo when you ask for it. | The camera takes your profile photo, when you ask for it in the settings. |
| `NSMicrophoneUsageDescription` | The microphone is used to dictate notes and record meetings. Speech is recognised on this device with a model from your covey instance; no audio is stored or sent. | same |
| `NSPhotoLibraryUsageDescription` | Photos and videos you choose are attached to a message for an agent, or become your profile photo. | — |
| `NSLocalNetworkUsageDescription` | covey on a machine in your local network — used when you connect the app to it. | — |
| `NSAudioCaptureUsageDescription` | — | In a meeting you record, covey transcribes what the Mac plays — the other side of a call — on this Mac. No audio is stored or sent. |

## Questions App Review tends to ask

- **"The app requires an account we cannot create."** The account comes from the organisation's covey server, the way a company mail app needs a company mailbox. covey is open source and anybody can run a server, or sign up on app.covey.work; the demo organisation above is ready.
- **Account deletion (guideline 5.1.1(v)).** The app creates no accounts. Seats are created and removed in the web interface by the organisation, and the app's own key can be revoked there.
- **User-generated content (1.2).** Messages and notes are visible only to members of the same organisation, which administers its own seats: an administrator removes a seat in the web interface and revokes any key there. Nothing is published outside the organisation.
- **Purchases.** None. The app sells nothing and links to no purchase.
- **Minimum functionality (4.2).** Without a server the app shows the pairing screen only; with one it is the team's chat, the notes and the dictation described above.
