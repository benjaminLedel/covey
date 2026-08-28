# Workplaces — which image an agent works in

Every agent works in a **workplace**: an image the control plane starts a
container from at each wake, with the agent's persistent home mounted into it.
The model is in [`spec/16-runner.md`](../spec/16-runner.md), "Sandbox images per
agent"; this page is the operational part — which ones there are, how one picks
them, and what a choice costs.

## What is published

| Profile | Image | On top of base | Size | For whom |
|---|---|---|---|---|
| `base` | `covey-sandbox:latest` | — | 1.54 GB | support, mail, QA, research |
| `dev` | `covey-sandbox-dev:latest` | PHP, JDK, `fvm`, `uv`, node-gyp, MariaDB | 2.53 GB | developer agents without a settled field |
| `dev-flutter` | `covey-sandbox-dev-flutter:latest` | Flutter SDK, `fvm`, JDK | 3.21 GB | Flutter agents |
| `dev-php` | `covey-sandbox-dev-php:latest` | PHP 8.2, Composer, MariaDB, node-gyp | 2.12 GB | PHP/Laravel agents |
| `dev-web` | `covey-sandbox-dev-web:latest` | node-gyp toolchain | 1.82 GB | Node/TypeScript agents |

Sizes measured with `docker images` on arm64 — uncompressed, so a pull moves
less, and a host that already has `base` moves only the difference. Read them as
a comparison, not as a download figure.

`dev` is the **union** of the developer toolchains and stays the answer for an
agent that may get a PHP ticket today and a Flutter one tomorrow. The role
workplaces are for an agent whose field is settled — it carries the same
toolchain and not the three others beside it. Which one an agent gets is a
statement about the agent, so nothing decides it automatically: agent →
settings → *workplace*.

The choice takes effect **at the next cold start**. A running or warm-parked
sandbox keeps the image it was started with.

### Why the Flutter SDK is in the image and the PHP version is not

`fvm` and `uv` are in the images, their SDKs are not: which Flutter or Python
version a project wants is the project's business, so the version manager
fetches it into the persistent home, once. `dev-flutter` reverses that for its
baseline version — for a *Flutter* agent the version question is settled, and
the home is the most expensive storage the platform has: it is walked at every
wake and written back after every run. A 1.3 GB SDK that is identical for every
Flutter agent has no business lying there once per agent.

That is why `dev-flutter` is the one workplace that is *bigger* than `dev`
(3.21 against 2.53 GB) and still the cheaper one for a Flutter agent: the image
is pulled once per runner, the home is carried on every single run.

`fvm` stays installed in `dev-flutter`. A project that pins another version
still gets it, into the home, for that one deviation. An installation whose
projects all run an older version is better off rebuilding the image:

```bash
docker build -f Dockerfile.sandbox.dev-flutter \
  --build-arg BASE_IMAGE=covey-sandbox:latest \
  --build-arg FLUTTER_VERSION=3.44.5 \
  -t covey-sandbox-dev-flutter:latest .
```

## Getting the images

The project builds and publishes every workplace for amd64 and arm64 on each
push and each release, as one package with the variant as a tag prefix
(`ghcr.io/benjaminledel/covey-sandbox:dev-flutter-latest`,
`…:base-v0.4.0`, …). Take the tag your binary is: the image carries the `coveyd`
that talks to this control plane.

```bash
make sandbox-images-pull                              # all of them
make sandbox-images-pull SANDBOX_PROFILES="base dev-flutter"
make sandbox-image-dev-flutter                        # or build it here
```

Pull what your agents actually stand in. Building all workplaces locally is
several gigabytes and the better part of an hour; pulling one is minutes.

**As a container** there is no checkout and no `make`. Then the images come from
the published catalogue on their own — an installation sets nothing. Where it
has to (an air-gapped instance, an own registry), one variable per profile wins
over the catalogue:

```bash
COVEY_SANDBOX_IMAGE=…              # base, the name from before the split
COVEY_SANDBOX_IMAGE_DEV_FLUTTER=…  # COVEY_SANDBOX_IMAGE_<PROFILE>, hyphens as _
```

An image that lies on no runner of the organisation is named before it hurts:
`covey doctor` says so before the restart, with how many agents are waiting on
it, and the agent's settings say it at the moment of choosing. A runner that
does not have the image pulls it at the first wake — that is slow, not broken.

## A workplace of your own

Anything the published ones do not cover is an **own workplace**: an image you
build and host, registered once and then chosen like any other (`kind: own` in
the list, agent → settings → *workplace*).

```bash
curl -X POST https://covey.example.com/api/v1/workplaces \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"name":"dev-flutter-internal","label":"Flutter (internal CA)",
       "description":"dev-flutter plus our certificate",
       "image":"registry.example.com/team/sandbox-flutter:2026-08"}'
```

The name belongs to exactly one workplace per organisation, and the names of the
published profiles are taken. A workplace an agent points at cannot be deleted —
the agent would wake into nothing.

**Give your image a self-description.** An agent is told what its workplace
contains, and that text comes from the image itself, not from the control plane:

```dockerfile
COPY my-workplace.json /etc/covey/workplace.json
```

The format is one of the published ones
([`internal/sandbox/workplaces/dev-flutter.json`](../internal/sandbox/workplaces/dev-flutter.json)):
`summary`, `tools`, `sdk_dirs`, `notes`. Without it the agent gets no paragraph
about its workplace — which is honest, and expensive: an agent that does not
know a tool is there fetches its own copy into its home, and the home is written
back after every run. The measured case was 2.7 GB of JDKs and a Flutter SDK in
one home, all of which the image had been carrying for weeks.

Say in `notes` that the agent is not root and has no apt. It is the sentence
that turns "I will build myself a way around this" into a request for a tool.
