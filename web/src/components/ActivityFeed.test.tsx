import { describe, expect, it } from "vitest";
import { buildFeed, phasenAnteil } from "./ActivityFeed";
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

/* Vor dem ersten Zug eines Agenten liegen auf einem frischen Host zwei
   Vorgänge, die zusammen eine Dreiviertelstunde dauern können: das Image holen
   und den Arbeitsplatz herstellen. Hinten dran hängt das Sichern. Die Plattform
   meldet sie im Fünfzehn-Sekunden-Takt — als Ereignisse gelesen wären das
   sechzig Zeilen für einen Vorgang. Es ist EINE Zeile, die sich ändert. */
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
    // Die erste Meldung trägt das Image und keine Zahlen, die zweite Zahlen und
    // (bei einem Sync) kein Detail. Beides gehört in dieselbe Zeile.
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

  // Zwei Vorgänge nacheinander sind zwei Zeilen — ein abgeschlossener Sync
  // nimmt den nächsten nicht mehr in sich auf.
  it("beginnt nach dem Abschluss eine neue Zeile", () => {
    const items = buildFeed([
      phase({ phase: "home_sync" }),
      phase({ phase: "home_sync", bytes: 10, done: true }),
      phase({ phase: "home_sync" }),
    ]);
    expect(phasen(items)).toHaveLength(2);
  });

  // Verschiedene Phasen laufen nicht ineinander, auch wenn sie sich zeitlich
  // überschneiden — Image holen und Home herstellen sind zwei Wartezeiten.
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

/* Wenn die Plattform einen Zustand auflöst, hinter dem nichts mehr steht (#83),
   muss das im Verlauf stehen. Sonst schläft der Agent „einfach so", und die
   Stunde davor, in der er auf „arbeitet" stand, bleibt unerklärt. */
describe("aufgelöste Zustände", () => {
  it("nennt den Zustand, der aufgelöst wurde", () => {
    const items = buildFeed([ev({ status: "stale", was: "working" }, "lifecycle")]);
    const gate = items.find((i) => i.kind === "gate") as { text: string; tone: string } | undefined;
    // Die übrigen Prüfungen dieser Datei lesen die englischen Texte; hier
    // ebenso, statt für eine Zeile die Sprache umzustellen.
    expect(gate?.text).toContain("reconciled");
    expect(gate?.text).toContain("working");
    expect(gate?.tone).toBe("warn");
  });
});
