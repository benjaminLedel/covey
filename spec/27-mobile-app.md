# 27 — The mobile app: the chat where the person is

**Status: a first slice is built** (#329, `mobile/`): the connection screen with an API key as the interim badge, what waits (read-only), the colleagues, and the per-agent thread with message and reply. Since #440 and #441 the app is specified against conversations instead: the list of conversations and the conversation, with the people and agents to start one from, and what waits standing inside the conversations rather than in a list of its own. Decisions 1–4 below are open, so it is a prototype and called one. The same project builds the desktop app (#334): the same surfaces side by side on a wide window, pairing through a `covey://` link instead of a camera. What it builds on: the chat in covey (#298 — `internal/httpapi/chat.go`, `web/src/team/`), which since #328 has to be switched on per organisation — off, the instance refuses a message with 403, and the app reads `TeamSurface` from `/auth/me` to say so before anybody types. This document is written for the developers who build the app, and it says what they may rely on, what they have to ask for, and what the app must never do.

## Why an app

The chat made handing work over possible. It did not make the way back work. The moment that decides whether an agent is a colleague or a tool is the moment it stops and waits — *"may I answer Globex directly?"* — and that moment is not at a desk. Today it lies in a browser tab somebody opens when they happen to think of it.

The cost of that is measurable in the object: a task in `blocked` costs nothing to hold and everything to hold too long. The agent has done the work up to the question; until the answer comes, its context is parked and its sandbox is gone. Every hour there is an hour in which the ticket it came from ages.

So the app has exactly one job: **carry the question to the person and the answer back**. Everything else it does is in service of that.

The web has the same split since #298: the team surface is its own shell there, not a page in the console — same surfaces, same rule about what belongs in neither. The app inherits that division rather than inventing one.

## What the app is

Two surfaces, and a directory to start from ([`28-team-surface.md`](28-team-surface.md) defines what a conversation is):

1. **The conversations.** This person's conversations, direct and group, newest first, with what is unread. It is the app's home, not a tab beside others. There is no "What waits" list any more: what waits for this person stands in the conversations as **decidable entries** — the agent's questions, the approvals a guard rail is holding ([`06-observability-control.md`](06-observability-control.md)), the open points of a review ([`21-operations-and-improvement.md`](21-operations-and-improvement.md)) — and a conversation holding one says so in the list.
2. **The conversation.** Its members and its messages: what was handed over, what an agent answered, what it asked, what came out of a task opened here, and the entries waiting for a decision — with the decision where this person may take it, and the name of who decides where they may not. The compose box at the bottom does what the web's does: a message is handed to the agent, a reply answers the parked task. In a direct conversation the agent answers every message; in a group only when it is addressed — mentioned, or replied to on its own message. A notification opens the conversation it is about.
3. **The people and agents** this person may write to — the place a direct conversation is opened or a group started, not a surface of its own weight.

## What the app is not

It is **not the console**. Nobody configures an agent from a phone, nobody reads a secret there, nobody changes a guard rail, nobody hires. That is not a scope cut to save work; it is the point of the app. The console is the view of somebody who runs a workforce, the app is the view of somebody who works with one, and the two must not blur — a surface that can do everything gets the permissions of everything.

Concretely out of scope: agent config and its versions, secrets, guard rails and egress, runners, workplaces, the marketplace, hiring, org-chart editing, user administration, the platform level. Costs are open — see the decisions.

## The server: there is none

**The one constraint that shapes the whole app: covey is self-hosted, and there is no covey backend.** Every installation is somebody else's machine at somebody else's address. The app therefore starts at a screen that no consumer app has: *where is your covey?*

What follows from it:

- **The instance URL is part of the account.** A person may hold seats in two organisations on two hosts; profiles sit side by side and are switched like accounts, not like settings. Inside one host, organisations are switched over the existing membership endpoints.
- **The handshake is the open question.** Before a login, the only thing an instance answers without a badge is `GET /healthz` — and "ok" does not say that a covey is behind it. `GET /api/v1/version` does, and it sits behind the badge deliberately: which commit runs on somebody's instance is nobody else's business (`internal/httpapi/server.go`). So the app can check reachability before the login and the build only after it — and it then says which minimum build it needs, rather than failing feature by feature. Making version public would reverse a decision that was taken on purpose; if a pre-login handshake is wanted, it belongs to decision 1 and gets its own answer there.
- **HTTPS only.** A hostname without a dot, a `.local`, a company VPN and a self-signed certificate are all normal for a self-hosted product and all abnormal for an app store. How the app handles a certificate it cannot verify is decision 4 — it is not "just allow it".
- **No telemetry to us from the app.** The instance has its own channel to the project, with its own switch (`docs/operations/telemetry.md`). The app adds nothing to it. An app that phones home about a self-hosted product breaks the promise the product is sold on.

## Authentication: the third badge

covey has two badges today, and neither fits a phone:

| Badge | What it is | Why not here |
|---|---|---|
| Session cookie | HttpOnly, `SameSite=Strict`, set by `POST /api/v1/auth/login` | Deliberately unreachable outside a browser. That is the point of it. |
| API key | `covey_…`, carries the rights of its seat, cannot mint keys or change the password (`internal/httpapi/apikeys.go`) | A long-lived secret, typed by hand, living on a device that gets lost. |

What the app needs is a **device badge**: issued after a login on the device, bound to that device, revocable by its owner from the web interface, listed beside the sessions that are already listed there (`GET /api/v1/auth/sessions`). Short-lived access, refreshed silently, dead the moment somebody revokes it.

That endpoint does not exist yet. It is decision 1, it is covey's side of the work, and the mobile team cannot invent it — a badge is a security object and it gets designed once, in the control plane, for every client that will ever want one.

**Until it exists**, the key reaches the phone by **pairing** (#330): Profile & Settings shows a QR code carrying the instance address and a code that is good for one use and five minutes (`POST /auth/pairings`, session only); the app scans it and exchanges the code for an API key named after the device (`POST /auth/pair`, the one route without a badge — the code is the badge). The key is listed and revocable beside the others. That changes how the key gets to the phone, not what it is: it is still an API key, not the device badge.

Before pairing, a prototype could only paste an API key into the Keychain / Keystore. That is an interim, it is written here so that it is not mistaken for the design, and an app shipped that way is an app that ships a long-lived org credential on a phone.

## The API the app builds against

Paths are relative to `/api/v1`, all of them behind the badge, all scoped to the seat's organisation. The conversation endpoints are specified by #440 and not built yet — their names here are the plan, not a contract; everything else exists today.

| Purpose | Endpoint | Notes |
|---|---|---|
| handshake | `GET /version` | build and commit of the instance |
| who am I | `GET /auth/me` | includes the seat role — it decides what the app may show |
| organisations | `GET /auth/memberships`, `POST /auth/switch-org` | switching organisations inside one host |
| colleagues | `GET /agents` | filter out `status = applicant`: a draft is not a colleague |
| conversations | `GET /conversations` | this person's direct and group conversations, newest first, with the unread count and whether one holds an open entry |
| messages | `GET /conversations/{id}/messages` | a page of messages; members only |
| write | `POST /conversations/{id}/messages` | `{text}` → handed to the agent it addresses; a task it opens carries the conversation (`backlog_tasks.conversation_id`) and origin `chat:<email>` |
| mark read | `POST /conversations/{id}/read` | moves this member's read position |
| start one | `POST /conversations` | `{kind: direct, member}` returns the existing direct conversation or creates it; `{kind: group, title, members}` creates a group |
| older app versions | `GET /agents/{id}/thread`, `POST /agents/{id}/messages` | kept as aliases onto the direct conversation between this person and the agent |
| answer | `POST /tasks/{id}/reply` | `{text}` → a note, and the resume input if the task was parked |
| decide an approval | `POST /approvals/{id}/decide` | reached from the entry; needs the manage roles or `security` |
| decide an open point | `POST /improvements/{id}/decide` | reached from the entry; accept or reject with a reason, with the role check the proposal's files ask for |

`GET /inbox` is gone (#441, [`28-team-surface.md`](28-team-surface.md)): the decision endpoints stay, and the app reaches them from the entry in the conversation.

The conversation message is the shape the app renders. One line, an author and a kind:

```
author: person | agent | system   (with the person's or agent's id)
kind:   message | note | question | result | error | approval | open_point
task_id, task_title, task_state, decision_id, decidable, decided_by, text, at
```

`author` says who wrote a line — a person, an agent, or the platform writing for the agent (an outcome, an entry) — and from it the app decides which side a line stands on, and nothing else. `task_state = "blocked"` on a `question` is what makes the compose box answer instead of ask — the same rule as on the web, and it belongs in the shared behaviour, not in each client's head. An `approval` or `open_point` is a **decidable entry**: `decidable` says whether this person may decide it, and when they may not, the entry names who does and offers no button. The field names are, like the endpoints above, the plan. The per-agent thread entry (`kind: message | note | question | result | error`) stays the answer of the alias endpoints.

The reply answers with `woken: true|false`. **False is not an error.** It means nobody was waiting; the text was written to the task as a note and the agent will read it on its next run. The app says so in one line and does not retry.

## The voice provider (#497, #498)

A call speaks through the organisation's **voice provider**, the one source of covey's voices: an OpenAI-compatible speech server the control plane talks to on the app's behalf; the app never reaches it and never holds its key. How an agent sounds there is its covey voice's spoken voice ([`24`](24-voice.md)). Only while there is no provider, or when it fails before anything of a reply was heard, the Mac's `AVSpeechSynthesizer` speaks — a voice of the reply's language picked per agent, at the agent's speed — and the call view says so in one line ("Voice provider unavailable — using the Mac voice"); a provider that failed is not asked again in that call. No speech is synthesised on the device.

- **Which provider.** The organisation's own (`organizations.speech_server`, migration 0126 — the column kept the name it was introduced with: `base_url`, default `model` and `voice`, `transcribe`, `transcribe_model`; the key in the organisation secret `voice_provider_key`), set with `GET`/`PATCH /org/voice-provider` by the manage roles. Without an own base URL, an organisation that holds an educa AI token for its engine (`educa_seat_token` first, then `educa_api_token`) speaks through educa AI at `COVEY_EDUCA_BASE_URL` of the control plane's environment, default `https://api.educaai.de`. The settings answer which one is in effect (`effective.source`: `own`, `educa`, `none`) and whether an own key is stored (`key_set`), never a key. Where the provider lists its voices (educa AI's `GET {base}/api/tts/voices`), `GET /org/voice-provider/voices` offers them to every member, and an agent without a spoken voice of its own is assigned one of them ([`24`](24-voice.md), #518); the app speaks the whole call — greeting, replies, goodbye — with the `speech.voice` the conversation names.
- **Testing it.** `POST /org/voice-provider/test`, the manage roles, synthesises an English sample sentence with a ticket number, an amount, a date, a time and an emoji in it — normalised like any other text, below — with the saved settings and answers `{ok: true, ms, bytes, text}` (`text` is what the provider was given) or `{ok: false, error}`; the audio is dropped. It counts against the same per-seat limit as speaking.
- **Speaking.** `POST /speech/synthesize {text, voice, speed, language, instructions, stream, agent}`, any signed-in seat, 30 a minute per seat (429). Text 1–1000 characters, voice at most 100, instructions at most 300; the voice defaults to the organisation's, then to `DEFAULT_VOICE`; the model is always the organisation's; `speed` is held to 0.7–1.3 and left out at 0. Upstream: `POST {base}/v1/audio/speech {input, voice, model?, language?, instructions?, speed?, response_format}` with `Authorization: Bearer <key>`. Buffered, `response_format: wav` and covey answers `audio/wav` (the voice page's preview); with `stream`, `response_format: mp3, stream_format: audio` and covey passes the bytes through as they come, flushed per chunk, as `audio/mpeg` — educa AI's first bytes arrive after about half a second, so the app starts speaking before the reply is whole. The Mac decodes the MP3 incrementally (AudioFileStream and AVAudioConverter), plays each piece as it is decoded, moves the face's mouth with its level, and stops it at once on barge-in. No provider: 409; the provider failing before the first byte: 502 with a short message.
- **What the provider is given** (#501). Synthesis models do not reliably normalise text themselves ("1.250 Euro" came out as "52 Euro"), so covey hands the provider words: `internal/speech/normalise` runs on every synthesis, in the request's `language` (German or English; without one it guesses between the two from the words; another language gets only the language-free steps). Emojis and pictographs go, with their modifiers, joiners, flags and keycaps; arrows and dashes between clauses become a pause. Numbers are written out with the language's separators ("1.250" / "1,250", "2,5" / "2.5"), with their sign, percentage, currency before or after (€, $, £, EUR, USD, GBP, CHF, cents), unit (KB–TB, ms, s, min, h, km, m, kg, °C …) and multiplier (Mio., Mrd., k); times ("zehn Uhr dreißig" / "ten thirty", "two PM"), dates (`30.09.2026`, ISO, with the German ordinal's ending taken from the word before it), German ordinals before a month or after an article ("am dritten Oktober"), ranges ("zehn bis zwanzig"). Codes are spoken as codes: `#31009` as a ticket unless the word before already says what it is, `!475` and `MR !475` as one merge request, `PR #480` as a pull request, `SUP-481` letter by letter and then the number, versions part by part, long digit runs digit by digit, hashes and UUIDs left out. A short table of developer abbreviations is spoken whole (MR, PR, CI, QA, UI, API, URL, ID, SSO, DB, z. B., usw., bzw., e.g., etc. …). Markdown loses its marks, a link keeps its words, and a bare address or code is replaced once by "(Link im Chat)" / "(link in the chat)", "(Code im Chat)" / "(code in the chat)". The app cleans a reply before sending it (`mobile/lib/call/speech_text.dart`); the normaliser is idempotent, so on such text it only writes out what is left. The input is cut at 4000 characters and handled in one pass; the 1000-character limit applies to what the app sent, and a text with nothing left to speak is refused (400).
- **Models and voices** are free text: educa AI lists no speech models and no voices, and takes `DEFAULT_VOICE`.
- **Hearing.** `POST /speech/transcribe?language=` takes one turn as WAV (16 kHz mono PCM16, at most 2 MB) and forwards it to `{base}/v1/audio/transcriptions` (multipart: `file`, `model`, `language`, `response_format: json`) → `{text}`. Off unless an admin switches `transcribe` on: the audio then leaves the device. Off or no provider: 409.
- **At the voice provider** (#516). While `GET /speech/model` answers `transcribe: true`, the call sends each turn — 16 kHz mono PCM16 as the voice detector cut it, in a WAV — to `POST /speech/transcribe` with the call's language, and posts that text. The device's recogniser decodes the same turn alongside, and its text is taken when the provider fails, answers nothing, or has not answered within 4 s — so the fallback costs no wait beyond that bound. A provider that refuses (409, the organisation turned it off; 404, an instance from before it) is not asked again in that call. The diagnostics line of each turn says which recogniser was used, how long it took, and why the provider's text was not (`recogniser`, `recognise_ms`, `server_problem` in the recording). While it is on, the line at the top of the call view says "speech is recognised on your organisation's server" instead of "speech stays on this Mac".
- **Recognising a turn** (#511). SenseVoice is pinned to the call's language where it knows it (Chinese, Cantonese, Japanese, Korean, English); Parakeet takes no language and detects it itself, so a turn the Mac's language recogniser reads as another language than the call's is flagged in the diagnostics log and in the recording (`call_language`, `recognised_language`) — and sent as recognised.
- **Cleaning a turn** (#498, #511). A recognised turn goes through `POST /me/dictation/clean` with `"turn": true` before it is posted, with the conversation's names and last lines as context. A turn is only corrected, not rewritten (`dictation.CleanTurn`): spelling, punctuation, capitals, misheard names and terms, every word kept, in its language, never translated. It comes back as recognised, with `kept` saying why, when it has fewer than four words (`short` — the model is not asked; the app does not ask either), when the answer is in another language (`language`, `normalise.Language`), or when it changed more than 30 % of the turn's words (`edited`; the word-level edit distance, a word replaced by one spelled close to it counting half). A dictation outside a call keeps its fuller clean-up, and it too is returned as dictated when the answer is in another language.
- **What it leaves behind.** Nothing of the text or the audio: the log, and the recording of the agent the app names (`agent`), get lengths, durations and the model (`speech_synthesized`, `speech_transcribed`). The audit trail has the request like every other.
- `GET /speech/model` answers `synthesize` and `transcribe`, so the app knows before it asks.

### Sounds and the typing (#500, #526)

A call is never silent while something happens. Short sounds, synthesised by the project (`mobile/tool/call_sounds.dart` → `mobile/assets/sounds/`, 16-bit mono WAV at 44.1 kHz, no third-party audio), mark ringing while it connects (at most two loops), connected, the end of a turn heard (a low, soft tap, the quietest of them), a task created (an agent message naming a task the conversation did not know, in reply to a message), mute, unmute and hang-up. They play on a player of their own, mixed under the voice, and never interrupt it. On by default and quiet; the call settings switch them off and set their volume, on the device.

While the agent thinks, somebody is heard typing: 0.3 s after the end of the turn was heard — while the turn is still recognised, cleaned up and shown in its window, which together take seconds — and as long as the reply's message has not arrived, a quiet loop of muffled keystrokes — words of a few letters, a space bar, now and then a pause; 9 s, synthesised from filtered noise like the other sounds (`call_typing.wav`) — plays on the filler player beside the voice until the reply. It follows the sound setting and volume; with sounds off the wait is silent. Once the reply's message has arrived it no longer starts, even while the reply's audio is still being made (#511); one that is playing fades out over 250 ms from that moment, while the reply's audio is being made — the reply does not wait for the fade (#527); the person speaking, mute and hang-up stop it; nothing plays into a muted call; a turn that is dropped (nothing recognised, the agent's own echo) or not sent stops it. While it plays, the person's voice is told from it as from the agent's (#511). Until #526 the agent said a short filler in its own voice instead ("Hm…", "One moment…", after 8 s "Just a moment."); heard many times a call the phrases sounded canned.

**The wait after a turn** (#527). Half way into the pause that ends a turn (not sooner than 0.3 s), the turn detector hands over what the turn will be unless the person speaks again: the turn's audio is trimmed to 200 ms of trailing silence either way, so it is already the turn byte for byte. The call recognises it then, checks its language and starts its clean-up; when the turn ends with the same audio that work is used, and when the person spoke on it is dropped and the whole turn is recognised as before. The diagnostics line of a turn says when it was recognised ahead. The call reads the conversation 40 ms after a `chat` event rather than after the 250 ms other surfaces settle for. After posting a turn it opens the stream of its spoken reply (#529, [`28`](28-team-surface.md)) and speaks each sentence as it arrives — the first usually while the model still writes the rest; the wait and the typing end with it. When the reply's message comes, only what the stream did not carry is said, and the word that the details are in the chat; when the message says something else (a check dropped the spoken form), what was heard stands. The person speaking into a streamed reply drops the rest of it and its message, as a barge-in drops the rest of a reply. An instance that streams nothing answers 404, and the call speaks the message as before.

### The greeting (#506, #513, #528)

A call does not open on a quiet line: when it connects, the agent says hello — "Hi, Ada!", "Moin!", "Guten Tag, Ada Lovelace." — and nothing more (#528). A call is answered, not opened with a speech: the person called because they want something, and says it next. Until #528 the greeting also introduced the agent and asked an open question, or was written from the situation by the instance; heard at every call, two or three sentences stood between the person and what they called about.

**Written from the situation (#513), no longer asked.** `POST /conversations/{id}/greeting` stays for app versions that ask it: with `{agent_id, lang, local_time, weekday}` the instance writes one or two spoken sentences on the fast model tier (`internal/callgreeting`) from what the person could already see — the agent's name and department, the person's first name, the time of day and weekday, the titles of the agent's tasks in progress and of the one it finished last that day, and the conversation's last three messages. It follows the chat tone's address and tone ([`24`](24-voice.md)); a guard cuts the answer after its first question and refuses it without one within two sentences, over 180 characters, or with a link or an id; the rest is normalised for speech (#501). Only a member of the conversation may ask, about an agent in it (404 otherwise); `local_time` is required (400). No credential, a refused answer, an error or a turn over 3 s answers 204. Nothing is stored. The app since #528 does not ask it.

**The openings.** The app chooses one opening per language and register (`mobile/lib/call/greeting.dart`, the app's ten languages) that fits the part of the day (until noon, the afternoon, from six in the evening through the night): one to four per part of the day, each a greeting word or two and the name — none that adds a clause ("…, schön, dass du anrufst", "…, thanks for calling"). The opening said last to an agent is kept on the device, and the next is another where there is one.

The address is the chat tone's (#457), read from `GET /conversations/{id}/speech` (`address`, [`24`](24-voice.md)): Sie is formal, du or nothing informal — in English, Japanese and Chinese that is the plain friendly form. Informal greetings name the first name of the seat's display name; formal ones the full name, and only when it has a surname — whether to say "Frau" or "Herr" is not known, and a first name alone after "Guten Tag" would be too familiar. No name, or an address for one, and the greeting says none.

**Timing.** The greeting is chosen and synthesised through the voice provider as the line starts ringing, while the models load, and kept on the device (`call-fillers/`, per voice and text). After the connected sound the call waits at most 1.5 s for its audio; when it is not ready by then, the Mac's voice says it — for the greeting only, the provider speaks the rest of the call. Without a provider the Mac's voice says it. The face speaks it, it stands in the call's transcript as the agent's line, and it is not a message in the conversation. The microphone is open under it: the person speaking stops it, as it stops a reply, and speaking before it began drops it. A reply arriving meanwhile is spoken after it. The call settings switch it off, on the device; it is on by default.

### The closing (#517)

When the person ends the conversation by voice, the agent's answer is its goodbye, marked `end_call = "true"` in the message's meta ([`28`](28-team-surface.md), said in a call). The call speaks it — its spoken form, and the word that the details are in the chat when it carries one — then plays the hang-up sound and ends, as the button does; the person's turn and the goodbye stay in the conversation. The microphone stays open under the goodbye: the person speaking into it stops it, as it stops any reply, and the call stays open. A goodbye that says the agent took something on promises its report in the chat, where the task's result arrives. The call settings switch it off ("Agent may hang up", on the device, on by default); the goodbye is then spoken and the line stays open.

## Push

Nothing exists for this, and it is the hardest part of the app, not the last one.

A self-hosted instance cannot talk to APNs or FCM without credentials, and those credentials belong to whoever ships the app — not to whoever runs the instance. Three ways, and they are a real choice:

- **(a) No push.** The app fetches when it is opened and while it is open. Honest, cheap, and it fails at exactly the job the app exists for.
- **(b) The operator configures it.** APNs key / FCM credentials as instance settings; the instance talks to Apple and Google itself. Works, keeps everything on the operator's machine, and asks a self-hoster to obtain push credentials — which for APNs means an Apple developer account.
- **(c) A relay the project runs.** The instance sends an opaque wakeup ("something is waiting for device X") to a relay that holds the credentials; the payload never carries content. Easy for the operator, and it means a service we run learns which installation has somebody waiting, and when. For a product whose argument is that nothing leaves the house, that is a high price for a convenience.

**Recommendation: (b), with (a) as the default state.** An instance without push credentials is a working instance with a quieter app. The relay is decision 2 and needs an answer that is a position, not a shrug.

Whatever is chosen: **the notification carries no content.** Not the question, not the task title, not the agent's name. A lock screen is a public surface, and an agent's question can quote a customer. It says that something waits and how much; the app fetches the rest behind the lock. It goes to the conversation's members, except the author of the line and whoever muted the conversation.

## Offline, and the message typed in a tunnel

An unsent message is kept, marked as unsent, and retried. It is never silently dropped and never silently duplicated.

The second half of that needs the server: a retry after a timeout must not create two tasks. The app sends an idempotency key with every write, the instance stores it per seat and answers the repeat with the original result. That does not exist yet — decision 3.

Reads are cached so the app opens on the last conversation instead of on a spinner, and every cached view says how old it is. A stale conversation that looks live is worse than a visible "from four minutes ago", because the whole point of the screen is whether somebody is waiting right now.

## Language and wording

The interface has ten catalogues (`web/src/locales/*.json`), and the app uses **the same keys with the same wording**. Not a second set of words for the same things: an agent that "parks" in one place and "waits" in the other is two products to the person who uses both. The app follows the device language and falls back to English.

The German rule from the web holds here too and is tighter on a phone: German runs 15 to 30 % longer than the English it translates, and a button that fits on one line in English wraps in German. Layouts are checked against German, not English.

## Design

The app takes the design language of the control plane (`mockup/covey-ui-mockup.html`, `web/src/styles.css`): near-monochrome, one accent that carries links and nothing else, no second accent colour, generous space, and the state of a thing shown in words rather than in colour alone. Both appearances ship, and both are measured for contrast — WCAG 2.2 AA is binding here as it is on the web.

One thing the app adds that the web does not need: **the thumb**. The compose box and the decision buttons sit where a hand reaches without regripping, and a destructive or outward-facing decision is never a button somebody hits on the way to another.

## What the app must never do

- Never store a long-lived organisation secret. The badge is device-bound and revocable, or there is no badge.
- Never show or cache a secret, a credential or an agent's config.
- Never send content to anything but the instance. No crash reporter that ships a screen, no analytics on what was typed.
- Never put content in a notification.
- Never decide on the client what a role may do. The instance answers 403; the app hides what it knows is hidden and believes the server on everything else.

## Open decisions

| # | Decision | Whose |
|---|---|---|
| 1 | The device badge: how a phone logs in, how the token is bound and revoked, where it shows up beside the sessions — and whether anything answers "I am a covey, build X" before that login | covey |
| 2 | Push: operator-configured credentials, a project relay, or neither | product |
| 3 | The idempotency key on writes, so a retry cannot double a task | covey |
| 4 | A certificate the app cannot verify: refuse, or trust per host after an explicit dialogue | mobile + security |
| 5 | Which role may write from the app. Settled by #440 for reading and writing a conversation: membership decides, not the seat role. Still open: which seat roles may start a conversation with an agent, and the seat role that sees only the chat (decision 1 of #298) | covey |
| 6 | Whether an approval needs a biometric confirmation before it is sent | product |
| 7 | Whether costs appear at all — a figure per agent is harmless, a cost centre on a phone is a different product | product |

Decisions 1, 3 and 5 are prerequisites: an app built before them is a prototype, and it should be called one.

## Related

- [`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md) — the backlog, `blocked`, the resume input the reply becomes
- [`06-observability-control.md`](06-observability-control.md) — approvals, the recording, the kill switch
- [`09-enterprise-model.md`](09-enterprise-model.md) — the seat roles the app inherits
- [`14-companion-memory.md`](14-companion-memory.md) — the other app in this repository, and a different one: it collects material for the wiki, this one carries a decision
