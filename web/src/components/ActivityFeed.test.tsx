import { describe, expect, it } from "vitest";
import { buildFeed } from "./ActivityFeed";
import type { RecordingEvent } from "../api";

/* Der Verlauf ist der Beleg dafür, was ein Agent getan hat — und ein Beleg,
   der Nebengeräusche als Ereignisse ausgibt, belegt das Falsche. Die Runtime
   schickt unter `system` dreierlei: den Sitzungsstart, ihren Token-Zähler und
   den Zustand ihrer Hintergrundaufgaben. In einem gemessenen Lauf waren von
   192 system-Zeilen zwei ein `init`; der Verlauf zeigte 192 Sitzungsstarts.
   Diese Tests halten die Unterscheidung fest. */

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
    // Der Token-Zähler und die Zustandsspiegel tauchen gar nicht auf.
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
    // Kein eigenes Ereignis — und schon gar keine JSON-Zeile.
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
