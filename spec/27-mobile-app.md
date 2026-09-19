# 27 — The mobile app: the chat where the person is

**Status: specification, nothing built.** What it builds on exists: the chat is in covey (#298 — `internal/httpapi/chat.go`, `web/src/workspace/`). This document is written for the developers who build the app, and it says what they may rely on, what they have to ask for, and what the app must never do.

## Why an app

The chat made handing work over possible. It did not make the way back work. The moment that decides whether an agent is a colleague or a tool is the moment it stops and waits — *"may I answer Globex directly?"* — and that moment is not at a desk. Today it lies in a browser tab somebody opens when they happen to think of it.

The cost of that is measurable in the object: a task in `blocked` costs nothing to hold and everything to hold too long. The agent has done the work up to the question; until the answer comes, its context is parked and its sandbox is gone. Every hour there is an hour in which the ticket it came from ages.

So the app has exactly one job: **carry the question to the person and the answer back**. Everything else it does is in service of that.

The web has the same split since #298: the workspace is its own shell there, not a page in the console — same three surfaces, same rule about what belongs in neither. The app inherits that division rather than inventing one.

## What the app is

Three surfaces, in this order of weight:

1. **What waits.** The open points of the inbox, filtered to what this person may decide: the agent's questions and the approvals a guard rail is holding ([`06-observability-control.md`](06-observability-control.md)). This is the screen a notification opens. It is the app's home, not a tab beside others.
2. **The thread.** One agent, the conversation with it: what was handed over, what it wrote down on the way, what it asked, what came out. The compose box at the bottom does what the web chat's does — a new message opens a task, a reply answers the parked one.
3. **The colleagues.** The agents this person may write to, as a message list.

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

**Until it exists**, a prototype may paste an API key into the Keychain / Keystore. That is an interim, it is written here so that it is not mistaken for the design, and an app shipped that way is an app that ships a long-lived org credential on a phone.

## The API the app builds against

All of this exists today. Paths are relative to `/api/v1`, all of them behind the badge, all scoped to the seat's organisation.

| Purpose | Endpoint | Notes |
|---|---|---|
| handshake | `GET /version` | build and commit of the instance |
| who am I | `GET /auth/me` | includes the seat role — it decides what the app may show |
| organisations | `GET /auth/memberships`, `POST /auth/switch-org` | switching organisations inside one host |
| colleagues | `GET /agents` | filter out `status = applicant`: a draft is not a colleague |
| the thread | `GET /agents/{id}/thread` | the last 20 tasks as one chronological list |
| hand over work | `POST /agents/{id}/messages` | `{text}` → a task with origin `chat:<email>` |
| answer | `POST /tasks/{id}/reply` | `{text}` → a note, and the resume input if the task was parked |
| what waits | `GET /inbox?status=open&sort=urgent` | approvals and open points in one list |
| decide | `POST /approvals/{id}/decide` | needs the manage roles or `security` |

The thread entry is the shape the app renders. One line, five kinds:

```
kind: message | note | question | result | error
task_id, task_title, task_state, author, text, at
```

`author` carries the origin of a message (`chat:someone@example.org`) or the author of a note (`agent`, `human:someone@example.org`). From it the app decides which side of the thread a line stands on, and nothing else. `task_state = "blocked"` on a `question` is what makes the compose box answer instead of ask — the same rule as on the web, and it belongs in the shared behaviour, not in each client's head.

The reply answers with `woken: true|false`. **False is not an error.** It means nobody was waiting; the text was written to the task as a note and the agent will read it on its next run. The app says so in one line and does not retry.

## Push

Nothing exists for this, and it is the hardest part of the app, not the last one.

A self-hosted instance cannot talk to APNs or FCM without credentials, and those credentials belong to whoever ships the app — not to whoever runs the instance. Three ways, and they are a real choice:

- **(a) No push.** The app fetches when it is opened and while it is open. Honest, cheap, and it fails at exactly the job the app exists for.
- **(b) The operator configures it.** APNs key / FCM credentials as instance settings; the instance talks to Apple and Google itself. Works, keeps everything on the operator's machine, and asks a self-hoster to obtain push credentials — which for APNs means an Apple developer account.
- **(c) A relay the project runs.** The instance sends an opaque wakeup ("something is waiting for device X") to a relay that holds the credentials; the payload never carries content. Easy for the operator, and it means a service we run learns which installation has somebody waiting, and when. For a product whose argument is that nothing leaves the house, that is a high price for a convenience.

**Recommendation: (b), with (a) as the default state.** An instance without push credentials is a working instance with a quieter app. The relay is decision 2 and needs an answer that is a position, not a shrug.

Whatever is chosen: **the notification carries no content.** Not the question, not the task title, not the agent's name. A lock screen is a public surface, and an agent's question can quote a customer. It says that something waits and how much; the app fetches the rest behind the lock.

## Offline, and the message typed in a tunnel

An unsent message is kept, marked as unsent, and retried. It is never silently dropped and never silently duplicated.

The second half of that needs the server: a retry after a timeout must not create two tasks. The app sends an idempotency key with every write, the instance stores it per seat and answers the repeat with the original result. That does not exist yet — decision 3.

Reads are cached so the app opens on the last thread instead of on a spinner, and every cached view says how old it is. A stale thread that looks live is worse than a visible "from four minutes ago", because the whole point of the screen is whether somebody is waiting right now.

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
| 5 | Which role may write from the app. Today the chat needs the manage roles; the seat role that sees only the chat is decision 1 of #298 and this app is the reason it matters | covey |
| 6 | Whether an approval needs a biometric confirmation before it is sent | product |
| 7 | Whether costs appear at all — a figure per agent is harmless, a cost centre on a phone is a different product | product |

Decisions 1, 3 and 5 are prerequisites: an app built before them is a prototype, and it should be called one.

## Related

- [`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md) — the backlog, `blocked`, the resume input the reply becomes
- [`06-observability-control.md`](06-observability-control.md) — approvals, the recording, the kill switch
- [`09-enterprise-model.md`](09-enterprise-model.md) — the seat roles the app inherits
- [`14-companion-memory.md`](14-companion-memory.md) — the other app in this repository, and a different one: it collects material for the wiki, this one carries a decision
