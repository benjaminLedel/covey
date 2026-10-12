import { readFileSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import type { Workflow } from "../../api";
import { autoLayout, durationSeconds, durationText, example, fromWorkflow, layoutOf, outY, outsOf, runPath, toSource } from "./model";

/* The editor writes what the engine parses. The fixture below is written by
   toSource and parsed by the Go tests (internal/workflow, editor_test.go);
   if either side changes the format, one of the two fails.
   UPDATE_WORKFLOW_FIXTURE=1 npm test rewrites it. */
const fixture = resolve(__dirname, "../../../../internal/workflow/testdata/from-editor.workflow");
const triggersFixture = resolve(__dirname, "../../../../internal/workflow/testdata/from-editor-triggers.workflow");

describe("the workflow pages' model", () => {
  it("writes the example as the fixture the engine's tests parse", () => {
    const src = toSource(example("en"));
    if (process.env.UPDATE_WORKFLOW_FIXTURE) writeFileSync(fixture, src);
    expect(src).toBe(readFileSync(fixture, "utf8"));
  });

  it("writes triggers and actions as the engine reads them", () => {
    const doc = example("en");
    doc.meta.mode = "steer";
    doc.blocks.t.cfg = { as: "Engineering", every: "15m", daily: "", onlyIf: "gitlab:issues:assignee:builder:acme/app", webhook: "zammad", token: true };
    doc.blocks.s = { id: "s", type: "action", title: "Close", cfg: { as: "Test", system: "zammad", do: "set_state", params: ["ticket_id: {{case.no}}", "state: closed"] } };
    doc.order = [...doc.order, "s"];
    doc.links = doc.links.map((l) => (l.from === "w" && l.port === "out" ? { ...l, to: "s" } : l));
    doc.links.push({ from: "s", port: "out", to: "e" });
    doc.positions.s = { x: 1500, y: 400 };
    const src = toSource(doc);
    if (process.env.UPDATE_WORKFLOW_FIXTURE) writeFileSync(triggersFixture, src);
    expect(src).toBe(readFileSync(triggersFixture, "utf8"));
    expect(src).toContain('    only_if: "gitlab:issues:assignee:builder:acme/app"');
    expect(src).toContain('      "state": "closed"');
  });

  it("writes canonical values and exit keys, not the words shown", () => {
    const src = toSource(example("de"));
    expect(src).toContain('    limit: "4h"');
    expect(src).toContain('    max: "14d"');
    expect(src).toContain('    on: "a.result"');
    expect(src).toContain('      "defects": b');
    expect(src).toContain('      "limit": h');
    expect(src).toContain('    not_same_as: "b"');
    expect(src).toContain("    next: r");
    expect(src).not.toContain("pick:"); // load is the default
  });

  it("reads back what it wrote, positions included", () => {
    const doc = example("en");
    doc.positions.b = { x: 777, y: 333 };
    const src = toSource(doc);
    expect(layoutOf(src).b).toEqual({ x: 777, y: 333 });
    // The API sends steps; the canvas takes the positions from the text.
    const w: Workflow = {
      id: "00000000-0000-0000-0000-000000000000",
      slug: "x", name: "Customer ticket", version: 1, mode: "watch", scope: "", owner: "admin",
      roles: doc.meta.roles, updated_at: "", runs: { open: 0, working: 0, queued: 0, waiting: 0, stuck: 0 }, source: src,
      steps: [
        { id: "t", kind: "trigger", next: { out: "b" } },
        { id: "b", kind: "agent", role: "Engineering", limit: 4 * 3600, max_parallel: 2, result: ["mr"], next: { out: "c" } },
        { id: "c", kind: "condition", on: "b.mr", max_rounds: 2, next: { yes: "e", limit: "e" } },
        { id: "e", kind: "end" },
      ],
    };
    const back = fromWorkflow(w);
    expect(back.positions.b).toEqual({ x: 777, y: 333 });
    expect(back.blocks.b.cfg.limit).toBe("4h");
    expect(back.blocks.b.cfg.cap).toBe("2");
    expect(back.blocks.c.cfg.values).toEqual(["yes"]);
    expect(outsOf(back.blocks.c).map((o) => o.key)).toEqual(["yes", "limit"]);
  });

  it("lays steps out by their distance from the trigger", () => {
    const doc = example("en");
    const p = autoLayout(doc.blocks, doc.links, doc.order);
    expect(p.t.x).toBeLessThan(p.b.x);
    expect(p.b.x).toBeLessThan(p.r.x);
    // The way back from the condition does not move the build step.
    expect(p.b.x).toBe(40 + 290);
  });

  it("turns durations into the engine's text and back", () => {
    expect(durationText(14 * 86400)).toBe("14d");
    expect(durationText(4 * 3600)).toBe("4h");
    expect(durationText(90 * 60)).toBe("90m");
    expect(durationText(0)).toBe("");
    expect(durationSeconds("14d")).toBe(14 * 86400);
    expect(durationSeconds("soon")).toBe(0);
  });

  it("puts each exit's handle on its row", () => {
    const doc = example("en");
    expect(outY(doc.blocks.b, "out")).toBe(29);
    expect(outY(doc.blocks.c, "accepted")).toBe(70);
    expect(outY(doc.blocks.c, "limit")).toBe(118);
  });

  it("follows a run's path through its log", () => {
    const p = runPath({
      id: "r", case: "#1", step: "b", state: "working", since: "",
      log: ["t", "b", "a", "c", "b"].map((step) => ({ at: "", step, text: "" })),
    });
    expect(p.count.b).toBe(2);
    expect(p.links.has("c>b")).toBe(true);
    expect(p.current).toBe("b");
  });
});
