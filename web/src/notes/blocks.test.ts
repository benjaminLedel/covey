import { describe, expect, it } from "vitest";
import { parseBlocks, writeBlocks } from "./blocks";

/* The same round trips the app's test/blocks_test.dart makes: what one
   surface writes, the other reads back unchanged (#386). */
describe("note blocks", () => {
  const trip = (md: string) => writeBlocks(parseBlocks(md));

  it("keeps plain text, empty lines included", () => {
    const md = "Erste Zeile\n\nZweite Zeile\n  eingerückt";
    expect(trip(md)).toBe(md);
  });

  it("reads and writes every block kind", () => {
    const md = [
      "# Eins",
      "## Zwei",
      "### Drei",
      "- Punkt",
      "1. Erster",
      "2. Zweiter",
      "- [ ] offen",
      "- [x] erledigt",
      "> Zitat",
      "> 💡 Hinweis",
      "---",
      "![](covey-media://0b7a0d4e-8a53-4c43-9a2f-1b0f2c3d4e5f)",
    ].join("\n");
    const blocks = parseBlocks(md);
    expect(blocks.map((b) => b.kind)).toEqual([
      "heading1", "heading2", "heading3", "bullet", "numbered", "numbered", "todo", "todo",
      "quote", "callout", "divider", "image",
    ]);
    expect(blocks[7].checked).toBe(true);
    expect(blocks[9].emoji).toBe("💡");
    expect(writeBlocks(blocks)).toBe(md);
  });

  it("renumbers a numbered run from its start", () => {
    expect(trip("3. a\n7. b\n\n1. c")).toBe("1. a\n2. b\n\n1. c");
  });

  it("keeps a table, the blank line after it included", () => {
    const md = "| A | B |\n| --- | --- |\n| 1 | x\\|y |\n\nDanach";
    const blocks = parseBlocks(md);
    expect(blocks[0].kind).toBe("table");
    expect(blocks[0].rows).toEqual([["A", "B"], ["1", "x|y"]]);
    expect(blocks.length).toBe(2);
    expect(writeBlocks(blocks)).toBe(md);
  });

  it("keeps a toggle with its content", () => {
    const md = "<details>\n<summary>Mehr</summary>\n\nInnen\nZweite\n\n</details>";
    const blocks = parseBlocks(md);
    expect(blocks).toHaveLength(1);
    expect(blocks[0]).toMatchObject({ kind: "toggle", text: "Mehr", body: "Innen\nZweite" });
    expect(writeBlocks(blocks)).toBe(md);
  });

  it("gives an empty note one empty paragraph", () => {
    expect(parseBlocks("").map((b) => b.kind)).toEqual(["paragraph"]);
  });
});
