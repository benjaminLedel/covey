import { describe, expect, it } from "vitest";
import { collapse, diffLines } from "./diff";

const kinds = (before: string, after: string) => diffLines(before, after).map((l) => l.kind).join("");

describe("diffLines", () => {
  it("zeigt eine neue Datei als lauter Zusätze", () => {
    expect(kinds("", "eins\nzwei")).toBe("addadd");
  });

  it("lässt Unverändertes unverändert", () => {
    expect(kinds("a\nb\nc", "a\nb\nc")).toBe("samesamesame");
  });

  it("erkennt die eine geänderte Zeile in der Mitte", () => {
    const d = diffLines("a\nb\nc", "a\nB\nc");
    expect(d.map((l) => l.kind)).toEqual(["same", "del", "add", "same"]);
    expect(d[1].text).toBe("b");
    expect(d[2].text).toBe("B");
  });

  it("hält eine eingefügte Zeile für einen Zusatz und nicht für einen Austausch", () => {
    expect(kinds("a\nc", "a\nb\nc")).toBe("sameaddsame");
  });
});

describe("collapse", () => {
  it("kürzt lange unveränderte Strecken und meldet, wie viel fehlt", () => {
    const before = Array.from({ length: 20 }, (_, i) => `z${i}`).join("\n");
    const after = before.replace("z10", "GEÄNDERT");
    const chunks = collapse(diffLines(before, after), 2);

    const skips = chunks.filter((c) => c.kind === "skip");
    expect(skips.length).toBe(2);
    // Nichts geht verloren: gezeigte Zeilen plus übersprungene ergeben den Diff.
    const shown = chunks.filter((c) => c.kind !== "skip").length;
    const hidden = skips.reduce((n, c) => n + (c as { skipped: number }).skipped, 0);
    expect(shown + hidden).toBe(diffLines(before, after).length);
  });

  it("lässt einen kurzen Diff ganz stehen", () => {
    const chunks = collapse(diffLines("a\nb", "a\nB"), 3);
    expect(chunks.some((c) => c.kind === "skip")).toBe(false);
  });
});
