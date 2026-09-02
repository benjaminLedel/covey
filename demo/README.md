# Demo tooling

Programs that exist for demonstrating and documenting covey — not part of the
product, not shipped in the binary.

| Directory | What it is |
|---|---|
| [`seed/`](seed/) | Fills a fresh instance with a believable example organisation |
| [`tour/`](tour/) | Drives the admin UI and records the screenshots and the GIF in the README |
| [`fakezammad/`](fakezammad/) | Minimal Zammad double: tickets, webhook, replies |
| [`faketeams/`](faketeams/) | Minimal Microsoft Teams double |
| [`fakemail/`](fakemail/) | Minimal IMAP/SMTP mailbox |

## Re-recording the README images

The screenshots in [`README.md`](../README.md) go stale with every change to the
interface. They are not taken by hand — this recreates all of them, in both
languages, in about two minutes.

**Never run this against an instance you care about.** The seed deletes agents,
people and departments before it writes its own. Use a throwaway database.

```bash
# 1. A throwaway Postgres and a covey on its own port
docker run -d --name covey-demo-pg \
  -e POSTGRES_USER=covey -e POSTGRES_PASSWORD=covey -e POSTGRES_DB=covey \
  -p 5434:5432 pgvector/pgvector:pg16

export COVEY_DATABASE_URL="postgres://covey:covey@localhost:5434/covey?sslmode=disable"
export COVEY_MASTER_KEY=$(cat .covey.key)
export COVEY_LISTEN_ADDR=":8495" COVEY_PUBLIC_URL="http://localhost:8495"

make build && ./covey bootstrap && ./covey serve &

# 2. The example organisation
go run ./demo/seed -database "$COVEY_DATABASE_URL"

# 3. Record, once per language, and build the images
go run ./demo/tour -url http://localhost:8495 -out /tmp/tour
go run ./demo/tour -url http://localhost:8495 -lang de -out /tmp/tour-de
python3 demo/tour/build.py /tmp/tour
python3 demo/tour/build.py /tmp/tour-de .de

# 4. Clean up
docker rm -f covey-demo-pg
```

The results land in `web/public/shots/`: `<view>.jpg` and `<view>.de.jpg` for the
two READMEs, plus `tour.gif` and `tour.de.gif`.

**Seed after the server is up, and record both languages in one go.** The
control plane requeues every `in_progress` task to `open` when it starts
(`RequeueOrphaned` — a task in progress without a live session is an orphan
after a restart). Seed before that and the board is reset before you photograph
it; the dispatcher then picks the open tasks up and the agents show as woken. So:
start `covey serve`, *then* seed, then record. The heartbeats in the seeded
configs fire on an interval, which gives you a comfortable window — but not an
unlimited one.

## Two things that will surprise you

**No task in the seed is `open`.** A running control plane claims every open task
immediately (`ClaimNext`), starts a sandbox and begins a run — the data set would
be a different one ten seconds later, and without a sandbox image the runs fail
and leave error noise in the recording. The seeded organisation is therefore a
snapshot taken mid-work: tasks are in progress, waiting or done, and the column
names say the same thing as the states.

**No agent is `working`.** An agent marked as working without a live daemon
session gets put back to sleep by the control plane, correctly. Sleeping agents
are the honest picture anyway — idle is supposed to mean idle.

## Adding a station to the tour

Stations are the `stops` slice in [`tour/main.go`](tour/main.go): a path to
navigate to or a tab to click, a selector to wait for, optionally something to
click and a distance to scroll, and how long the frame stands still in the GIF.
Set `inREADME` when the view should also be written as a JPEG. Tab labels are
translated — if you add one, give it the German label too.
