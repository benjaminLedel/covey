import { describe, expect, it } from "vitest";
import { ActivityFeed, buildFeed, phasenAnteil } from "./ActivityFeed";
import type { RecordingEvent } from "../api";
import { renderWithProviders } from "../test/render";

/* The feed is the evidence for what an agent has done — and evidence that
   emits noise as events proves the wrong thing. The runtime sends three
   things under `system`: the session start, its token counter and the state
   of its background tasks. In one measured run two of 192 system lines were
   an `init`; the feed showed 192 session starts. These tests hold the
   distinction down. */

let nextID = 1;
const ev = (payload: unknown, kind = "runtime"): RecordingEvent => ({
  id: nextID++,
  agent_id: "a",
  kind,
  payload,
  created_at: "2026-08-26T18:45:00Z",
});

const texts = (items: ReturnType<typeof buildFeed>) =>
  items.filter((i) => i.kind === "evt").map((i) => (i as { text: string }).text);

describe("system-Ereignisse", () => {
  it("macht nur aus init einen Sitzungsstart", () => {
    const items = buildFeed([
      ev({ type: "system", subtype: "init", model: "opus" }),
      ev({ type: "system", subtype: "thinking_tokens", estimated_tokens: 50 }),
      ev({ type: "system", subtype: "thinking_tokens", estimated_tokens: 100 }),
      ev({ type: "system", subtype: "background_tasks_changed", tasks: [] }),
      ev({ type: "system", subtype: "task_updated", patch: { is_backgrounded: true } }),
    ]);
    const started = texts(items).filter((t) => t.includes("Session started"));
    expect(started).toHaveLength(1);
    // The token counter and the state mirrors do not show up at all.
    expect(texts(items)).toHaveLength(1);
  });

  it("nimmt eine Aufzeichnung ohne Subtyp weiterhin als Sitzungsstart", () => {
    const items = buildFeed([ev({ type: "system", model: "opus" })]);
    expect(texts(items)[0]).toContain("Session started");
  });

  it("benennt Hintergrundaufgaben, statt sie zu verwerfen", () => {
    const items = buildFeed([
      ev({ type: "system", subtype: "task_started", description: "Wait for sub-agent" }),
      ev({ type: "system", subtype: "task_notification", status: "stopped", summary: "Seed demo data" }),
    ]);
    expect(texts(items)[0]).toContain("Wait for sub-agent");
    expect(texts(items)[1]).toContain("Seed demo data");
  });
});

describe("tool_progress", () => {
  it("hängt die Laufzeit an den offenen Aufruf, statt eine Zeile zu erzeugen", () => {
    const items = buildFeed([
      ev({
        type: "assistant",
        message: {
          content: [{ type: "tool_use", id: "toolu_1", name: "Bash", input: { command: "sleep 600" } }],
        },
      }),
      ev({
        type: "tool_progress",
        heartbeat: true,
        tool_name: "Bash",
        parent_tool_use_id: "toolu_1",
        elapsed_time_seconds: 300,
      }),
    ]);
    // No event of its own — and certainly no JSON line.
    expect(texts(items)).toHaveLength(0);
    const turn = items.find((i) => i.kind === "turn") as { rows: any[] };
    expect(turn.rows[0].call.elapsedSeconds).toBe(300);
    expect(turn.rows[0].call.pending).toBe(true);
  });

  it("lässt einen bereits beantworteten Aufruf in Ruhe", () => {
    const items = buildFeed([
      ev({
        type: "assistant",
        message: { content: [{ type: "tool_use", id: "toolu_2", name: "Bash", input: {} }] },
      }),
      ev({
        type: "user",
        message: { content: [{ type: "tool_result", tool_use_id: "toolu_2", content: "fertig" }] },
      }),
      ev({ type: "tool_progress", parent_tool_use_id: "toolu_2", elapsed_time_seconds: 30 }),
    ]);
    const turn = items.find((i) => i.kind === "turn") as { rows: any[] };
    expect(turn.rows[0].call.pending).toBe(false);
    expect(turn.rows[0].call.elapsedSeconds).toBeUndefined();
  });
});

describe("unbekannte Runtime-Ereignisse", () => {
  it("werden benannt und nicht als JSON ausgeschüttet", () => {
    const items = buildFeed([ev({ type: "kommt_erst_noch", secret: "langer roher payload" })]);
    expect(texts(items)[0]).toContain("kommt_erst_noch");
    expect(texts(items)[0]).not.toContain("langer roher payload");
  });

  it("fasst gleiche Zeilen zu einer mit Zähler zusammen", () => {
    const items = buildFeed([
      ev({ type: "kommt_erst_noch" }),
      ev({ type: "kommt_erst_noch" }),
      ev({ type: "kommt_erst_noch" }),
    ]);
    const evts = items.filter((i) => i.kind === "evt") as { count?: number }[];
    expect(evts).toHaveLength(1);
    expect(evts[0].count).toBe(3);
  });
});

/* Before an agent's first move, two operations sit on a fresh host that
   together can take forty-five minutes: pulling the image and building the
   workspace. The backup hangs on behind them. The platform reports them every
   fifteen seconds — read as events that would be sixty lines for one
   operation. It is ONE line that changes. */
describe("Phasen der Plattform", () => {
  const phase = (p: Record<string, unknown>) => ev({ status: "preparing", ...p }, "lifecycle");
  const phasen = (items: ReturnType<typeof buildFeed>) => items.filter((i) => i.kind === "phase");

  it("fasst den Takt einer Phase zu einer Zeile zusammen", () => {
    const items = buildFeed([
      phase({ phase: "image", detail: "ghcr.io/covey/sandbox:main" }),
      phase({ phase: "image", detail: "ghcr.io/covey/sandbox:main", bytes: 400_000_000, bytes_total: 2_000_000_000, ms: 15_000 }),
      phase({ phase: "image", detail: "ghcr.io/covey/sandbox:main", bytes: 1_200_000_000, bytes_total: 2_000_000_000, ms: 30_000 }),
    ]);
    const p = phasen(items);
    expect(p).toHaveLength(1);
    expect(p[0]).toMatchObject({ phase: "image", bytes: 1_200_000_000, bytesTotal: 2_000_000_000, done: false });
  });

  it("hält die Anfangsmeldung fest, statt sie zu überschreiben", () => {
    // The first report carries the image and no numbers, the second numbers and
    // (for a sync) no detail. Both belong in the same line.
    const items = buildFeed([
      phase({ phase: "image", detail: "ghcr.io/covey/sandbox:main" }),
      phase({ phase: "image", bytes: 5, bytes_total: 10 }),
    ]);
    expect(phasen(items)[0]).toMatchObject({ detail: "ghcr.io/covey/sandbox:main", bytes: 5 });
  });

  it("schließt eine Phase ab, wenn sie fertig meldet", () => {
    const items = buildFeed([
      phase({ phase: "home_sync" }),
      phase({ phase: "home_sync", count: 400, bytes: 1_000, ms: 15_000 }),
      phase({ phase: "home_sync", bytes: 2_500, ms: 22_000, done: true }),
    ]);
    const p = phasen(items);
    expect(p).toHaveLength(1);
    expect(p[0]).toMatchObject({ done: true, bytes: 2_500, ms: 22_000 });
  });

  // Two operations one after the other are two lines — a completed sync does
  // not absorb the next one into itself.
  it("beginnt nach dem Abschluss eine neue Zeile", () => {
    const items = buildFeed([
      phase({ phase: "home_sync" }),
      phase({ phase: "home_sync", bytes: 10, done: true }),
      phase({ phase: "home_sync" }),
    ]);
    expect(phasen(items)).toHaveLength(2);
  });

  // Different phases do not run into one another, even when they overlap in
  // time — pulling the image and building the home are two waits.
  it("hält verschiedene Phasen auseinander", () => {
    const items = buildFeed([
      phase({ phase: "home", count: 100, count_total: 9_870 }),
      phase({ phase: "image", bytes: 5 }),
      phase({ phase: "home", count: 4_000, count_total: 9_870 }),
    ]);
    const p = phasen(items) as { phase: string; count?: number }[];
    expect(p).toHaveLength(2);
    expect(p.find((x) => x.phase === "home")?.count).toBe(4_000);
  });

  it("gibt dem Balken nur eine Länge, wenn die Phase ihr Ende kennt", () => {
    const mit = { key: "1", kind: "phase", phase: "home", time: "", done: false, count: 4_935, countTotal: 9_870 } as const;
    const ohne = { key: "2", kind: "phase", phase: "home_sync", time: "", done: false, count: 400 } as const;
    expect(phasenAnteil(mit)).toBeCloseTo(0.5);
    expect(phasenAnteil(ohne)).toBeUndefined();
  });
});

/* When the platform reconciles a state behind which nothing is left (#83),
   that has to show in the feed. Otherwise the agent goes quiet for no reason
   and the hour before, in which it read "working", stays unexplained. */
describe("aufgelöste Zustände", () => {
  it("nennt den Zustand, der aufgelöst wurde", () => {
    const items = buildFeed([ev({ status: "stale", was: "working" }, "lifecycle")]);
    const gate = items.find((i) => i.kind === "gate") as { text: string; tone: string } | undefined;
    // The other assertions in this file read the English texts; same here,
    // rather than switching the language for a single line.
    expect(gate?.text).toContain("reconciled");
    expect(gate?.text).toContain("working");
    expect(gate?.tone).toBe("warn");
  });
});

/* A sync that failed left one line in the runner's debug log and nothing
   else (#72). The UI kept showing the last successful snapshot — true and
   useless while every attempt since then failed. In the feed the failure is
   now a line like any other. */
describe("gescheiterte Phasen", () => {
  const phase = (p: Record<string, unknown>) => ev({ status: "preparing", ...p }, "lifecycle");

  it("zeigt den Grund, statt einen Balken zu füllen", () => {
    const items = buildFeed([
      phase({ phase: "home_sync" }),
      phase({ phase: "home_sync", done: true, error: "block 78a279df: 413 Request Entity Too Large" }),
    ]);
    const p = items.filter((i) => i.kind === "phase") as { error?: string; done: boolean }[];
    expect(p).toHaveLength(1);
    expect(p[0].error).toContain("413");
  });

  // A failure may not pass as success only because it reports "done".
  it("hält Fehlschlag und Abschluss auseinander", () => {
    const gut = buildFeed([phase({ phase: "home_sync", done: true, bytes: 42 })]);
    const schlecht = buildFeed([phase({ phase: "home_sync", done: true, error: "weg" })]);
    expect((gut.find((i) => i.kind === "phase") as { error?: string }).error).toBeUndefined();
    expect((schlecht.find((i) => i.kind === "phase") as { error?: string }).error).toBe("weg");
  });
});

/* What an agent writes is Markdown — and up to #225 it stood raw in the
   paragraph: the hashes of the headings, the stars of the emphasis, and a
   table as a row of pipes in one line. The report that brings numbers was
   the least readable of all because of it. */
describe("Markdown in der Stimme des Agenten", () => {
  const ergebnis = [
    "## Reichweite",
    "",
    "Der Sprung ist **kein Verdienst**.",
    "",
    "| Fenster | Klicks |",
    "|---|---:|",
    "| 08-11 … 08-24 | 1 |",
  ].join("\n");

  it("rendert das Ergebnis eines Laufs statt die Auszeichnung zu zeigen", () => {
    const { container } = renderWithProviders(
      <ActivityFeed events={[ev({ type: "result", subtype: "success", result: ergebnis })]} />,
    );
    expect(container.querySelector(".act-result .md-h")?.textContent).toBe("Reichweite");
    expect(container.querySelector(".act-result strong")?.textContent).toBe("kein Verdienst");
    expect(container.querySelectorAll(".act-result tbody tr")).toHaveLength(1);
    expect(container.textContent).not.toContain("## Reichweite");
  });

  it("rendert auch, was der Agent während des Laufs sagt", () => {
    const { container } = renderWithProviders(
      <ActivityFeed
        events={[
          ev({ type: "assistant", message: { content: [{ type: "text", text: ergebnis }] } }),
        ]}
      />,
    );
    expect(container.querySelector(".turn .voice .md-h")?.textContent).toBe("Reichweite");
    expect(container.querySelector(".turn .voice table")).not.toBeNull();
  });
});
