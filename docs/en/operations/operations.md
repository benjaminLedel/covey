---
slug: operations
title: Operations & deployment
description: 'Running covey: one binary plus Postgres, port 8494, migrations at startup, HTTPS through a reverse proxy, egress isolation, backups and updates without ceremony.'
faq:
  - q: How much memory does a covey server need?
    a: 'The control plane itself is frugal — a Go process next to Postgres. The demand comes from the sandboxes: each runs a Node runtime, plus chromium for browser work. Size by the number of agents awake at once, not by the number created.'
  - q: Can I put covey behind a reverse proxy?
    a: Yes, that is the intended route for HTTPS. The one thing to get right is not setting `COVEY_PUBLIC_URL` to the public domain when the sandboxes cannot reach it — that variable points inwards.
  - q: How do I update without losing data?
    a: Swap the binary or image and restart; migrations run at startup and are guarded against concurrent starts. Back up the database first, and keep the master key anyway. Afterwards run `covey config lint`.
  - q: Does covey run without internet access?
    a: The platform does. The agents need the model endpoint — with hard egress isolation the proxy sits in front and lets exactly the allowed hosts through. A self-hosted embedding service additionally keeps wiki search in the building.
---

# Operations & deployment

covey is deliberately boring to run: one process, one database, one port. Everything beyond that is optional.

## What runs

- **covey** — the control plane, listening on **8494** (API, interface, daemon WebSocket)
- **PostgreSQL** with `pgvector` — state, queue, memory, secrets
- **Docker** — for the sandboxes, started through the host's socket
- optionally **covey-runner** — when the data plane should span several machines

Migrations run automatically at startup, guarded by an advisory lock; two instances starting at once do not migrate against each other.

## Keeping the addresses apart

Two variables look alike and mean opposite things:

- `COVEY_PUBLIC_URL` points **inwards** — the address at which the **sandboxes** reach the control plane. Put the website's domain here and the containers dial back over the open network and fail at the egress allowlist.
- `COVEY_SITE_URL` points **outwards** — the copyable webhook and trigger URLs, the address in the downloadable skill, the links in the mails the installation sends. Leaving it empty is the normal case; the server derives it from the request. The `site.url` setting (*Platform → Settings*) says the same thing from inside the product and takes precedence — the mails sent from the notification loop have no request to fall back on and need one of the two ([mail](mail.md)).

At startup covey warns when these two roles look swapped.

## App links

Only for an installation that ships the covey app under its own host — app.covey.work does, a self-hosted instance normally does not. When set, covey serves `/.well-known/apple-app-site-association` and `/.well-known/assetlinks.json`, and iOS and Android open `/pair` (the pairing QR code) and `/team/<id>` (a thread) in the app instead of the browser. Unset, both files answer 404 and nothing changes.

- `COVEY_APPLE_APP_ID` — `<TEAM ID>.<bundle id>`, e.g. `ABCDE12345.work.covey.coveyMobile`
- `COVEY_ANDROID_CERT_SHA256` — the SHA-256 fingerprints of the app's signing certificates, comma-separated
- `COVEY_ANDROID_APP_PACKAGE` — the package name; default `work.covey.covey_mobile`

## Speech model

The app's dictation and meeting notes are recognised on the device with sherpa-onnx; the model comes from the instance. At startup covey fetches the default model once from pinned addresses on Hugging Face, verifies every file against a pinned SHA-256 and keeps it under `COVEY_DATA_DIR/models/<name>/`. Apps download it from `/api/v1/speech/model/file` the first time somebody dictates.

- `COVEY_SPEECH_MODEL` — the default: `parakeet` (NVIDIA Parakeet TDT 0.6B v3, 670 MB, CC-BY-4.0: 25 European languages), `sensevoice` (FunASR SenseVoice Small, 240 MB, FunASR model licence: Chinese, Cantonese, Japanese, Korean, English) or `off`
- `COVEY_SPEECH_MODELS` — the further models a person may pick in the app's settings, comma-separated; default `parakeet,sensevoice`. Each is fetched the first time somebody picks it. With the app in Chinese, Japanese or Korean and no model picked, the app takes `sensevoice`.

Both models detect the language themselves. Without internet access, place the model's files in `COVEY_DATA_DIR/models/<name>/` yourself; they are verified the same way, and a file with another digest is removed rather than served.

## Push notifications

Every new message in a conversation is announced to the conversation's people — not to whoever wrote it, not to whoever muted the conversation, and not to anybody who has already read past it. That covers what an agent answers, and what a task opened in that conversation reports back: its result, its error, its question. A task from anywhere else (the backlog, a webhook, a heartbeat) announces nothing, except a question it parks on, which goes into the direct conversation of the agent with its human supervisor and is announced to them. The iPhone and Android apps receive them through Firebase Cloud Messaging, which passes an iPhone's notification on to Apple; the Mac app shows them itself while it runs. The Windows app gets none. Where each app comes from and how it is paired: [The covey app](apps.md).

Google delivers to the app only for whoever holds a service account of the app's Firebase project, and Firebase reaches the iPhone only with the APNs key that whoever ships the app has uploaded to that project (Firebase console, Project settings, Cloud Messaging, Apple app configuration). The instance itself needs no Apple key. It is configured under **Administration → Platform**, tab **Push**, by an installation administrator, and a change applies within seconds, without a restart:

- **Directly**, with a service account of your own: its JSON key (Firebase console, Project settings, Service accounts) is uploaded on that page, checked, and stored sealed with the master key; the page names its project and account, never the key. Uploading does not switch the mode by itself — choose **Directly** and save. This only works for apps built with that project's `google-services.json` and `GoogleService-Info.plist`. **Test** asks Google for an access token with the stored account; it proves the account, not the APNs key, which only the first notification to an iPhone does.
- **Through a relay**, without an account: the relay (default `https://app.covey.work`, which holds the service account of the project's app builds) receives the notification and passes it to Firebase. This is the mode an installation starts in when nothing is configured. An instance that sends directly becomes such a relay for others with **Relay for other installations**; it accepts only the notification's fixed fields, bounded in length and limited per address.
- **Off**: nothing is sent; the apps show what is new when they are opened.

For an installation configured as code, the environment gives the defaults, and a value set on the page wins: `COVEY_FCM_CREDENTIALS_FILE` (the service account's JSON; with it the default is direct), `COVEY_PUSH_RELAY` (the relay; `off` switches push off unless there is an account), `COVEY_PUSH_RELAY_ACCEPT=true` (relay for others). The same keys are settings (`push.mode`, `push.relay_url`, `push.relay_accept`, `push.fcm_credentials`) and can be set with `covey settings`.

By default a notification says only who did what ("Bea has a question"), in the device's language, and nothing of the content leaves the instance: what travels is the device's token, the agent's name and the kind of event, the unread count, the agent's id and the chosen sound. Under **Administration → Organization**, tab **Profile**, card **Push notifications**, an organisation can include the first line of what was said. It then passes through Google, Apple and, where used, the relay.

## HTTPS

A reverse proxy in front, TLS terminated there, `COVEY_PUBLIC_URL` and `COVEY_SITE_URL` set accordingly. The secure cookie then switches itself on. For the database, `sslmode=require` or higher.

## Egress isolation

On by default with the docker provider: a sandbox reaches only the hosts on its agent's allowlist — the organisation's base list (seeded with `api.anthropic.com`), the assigned templates and the agent's own hosts. `COVEY_EGRESS_ENFORCE=false` switches it off; the lists are then kept but not applied, and `covey serve` and `covey doctor` say so. A provider that cannot enforce says the same instead of running open. `GET /api/v1/egress` answers with `enforced` whether it applies on this instance.

Two levels. **Cooperative** (the default): the sandbox's traffic goes through a proxy that enforces the allowlist. **Hard** (`COVEY_EGRESS_ISOLATION=network`): the sandbox sits on an internal network without internet, and the proxy container is the only way out — no longer bypassable. The hard level needs a second image built.

The cooperative proxy runs inside the covey process. When covey itself runs in a container, the sandboxes reach it only through a published port: `COVEY_EGRESS_LISTEN_ADDR` fixes its address, and the host has to publish the same port. The compose files do both (`:8495`, `8495:8495`). Without a sandbox's own token the proxy answers 407.

Upgrading an installation that ran with open egress: see [Upgrading](upgrade.md).

## Backups

Two things: the Postgres database and the `COVEY_MASTER_KEY`. Without the key a database backup is complete but every secret in it is unreadable. The agents' homes (`COVEY_DATA_DIR`) are useful but reproducible — they hold work in progress, not irreplaceable state.

## Updates

New binary or new image, restart the process; the migrations come along. After an update it is worth a look with

```
covey config lint
```

It changes nothing and reports configurations that no longer sit well with the new version: heartbeat intervals that are too short, blocking tasks on systems without a webhook, boards with columns naming tasks instead of states, frequent turn-limit aborts. The exit code is 1 when there are findings — an upgrade script can react to it.

## Observing

`covey version` answers which build is running; the same information appears in the startup line and at the bottom of the interface. Cost, tokens and runs are in the interface per agent and per model. The request log shows the HTTP edges — what came in, what went out.

## Next

- [Quick start (Docker)](../getting-started/quickstart.md) — the installation
- [Architecture overview](../introduction/architecture.md) — why the sandbox is a sibling container
- [Guard-rails & control](../concepts/guard-rails.md) — kill switch and recording
