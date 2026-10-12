import { describe, expect, it } from "vitest";
import { checks, definitionText, example, outY, runPath } from "./model";

describe("the workflow preview's model", () => {
  const ex = example("en");

  it("says the example's loop ends with a person and that the review role is unfilled", () => {
    const keys = checks(ex, ex.blocks, ex.links).map((c) => c.key);
    expect(keys).toContain("checkLoopOk");
    expect(keys).toContain("checkRoleEmptySkip");
    expect(keys).not.toContain("checkLoopNoLimit");
    expect(keys).not.toContain("checkNoOut");
  });

  it("refuses a loop without a round limit", () => {
    const blocks = { ...ex.blocks, c: { ...ex.blocks.c, cfg: { ...ex.blocks.c.cfg, rounds: 0 } } };
    expect(checks(ex, blocks, ex.links).map((c) => c.key)).toContain("checkLoopNoLimit");
  });

  it("names an exit that leads nowhere", () => {
    const links = ex.links.filter((l) => !(l.from === "c" && l.port === "limit"));
    const found = checks(ex, ex.blocks, links).filter((c) => c.key === "checkNoOut");
    expect(found.map((c) => c.params.step)).toEqual(["Accepted? (after 3 rounds)"]);
  });

  it("counts the rounds of a run and finds where it stands", () => {
    const p = runPath(ex.runs["#384"]);
    expect(p.count.b).toBe(3);
    expect(p.current).toBe("h");
    expect(p.links.has("c>b")).toBe(true);
    expect(p.links.has("c>s")).toBe(false);
  });

  it("puts each exit's handle on its row", () => {
    expect(outY(ex.blocks.b, "out")).toBe(29);
    expect(outY(ex.blocks.c, "yes")).toBe(70);
    expect(outY(ex.blocks.c, "limit")).toBe(118);
  });

  it("writes the definition with the file's own keys", () => {
    const text = definitionText(ex, "watch", ex.blocks, ex.links, ex.positions);
    expect(text).toContain("mode: watch");
    expect(text).toContain("    max_rounds: 3");
    expect(text).toContain('      "defects": b');
    expect(text).toContain("  c: [1200, 182]");
  });
});
