import { describe, it, expect } from "vitest";
import { layoutTree, type TreeInput, type Placed } from "./layout";

const box = (id: string, children: TreeInput[] = [], w = 200, h = 50): TreeInput => ({ id, w, h, children });

const overlaps = (a: Placed, b: Placed) =>
  a.x < b.x + b.w && b.x < a.x + a.w && a.y < b.y + b.h && b.y < a.y + a.h;

function byId(nodes: Placed[]) {
  return Object.fromEntries(nodes.map((n) => [n.id, n]));
}

describe("layoutTree", () => {
  it("centres a parent over its children and keeps siblings apart", () => {
    const l = layoutTree(box("root", [box("a"), box("b"), box("c")]), { stackAt: 4 });
    const n = byId(l.nodes);
    expect(n.a.x).toBe(0);
    expect(n.b.x).toBe(200 + 18);
    expect(n.c.x).toBe(2 * (200 + 18));
    // Root sits over the middle of the row of three.
    expect(n.root.x + n.root.w / 2).toBeCloseTo((n.a.x + n.c.x + n.c.w) / 2);
    expect(l.width).toBe(3 * 200 + 2 * 18);
  });

  it("never lets two boxes overlap, however uneven the subtrees", () => {
    /* The failure the old CSS tree had: a wide department beside a narrow
       one, and the connector of the second running through the members of
       the first. Here a subtree's corridor is its own. */
    const tree = box("org", [
      box("d1", [box("lead1", [box("m1"), box("m2"), box("m3")])]),
      box("d2", [box("lead2")]),
      box("d3", [box("lead3", [box("m4", [box("m5")])])]),
    ]);
    const l = layoutTree(tree);
    for (const a of l.nodes) for (const b of l.nodes) {
      if (a.id < b.id) expect(overlaps(a, b), `${a.id} overlaps ${b.id}`).toBe(false);
    }
  });

  it("stacks many leaves under one parent as an indented list", () => {
    const l = layoutTree(box("lead", [box("a"), box("b"), box("c"), box("d"), box("e")]), { stackAt: 4 });
    const n = byId(l.nodes);
    expect(n.lead.x).toBe(0);
    for (const id of ["a", "b", "c", "d", "e"]) {
      expect(n[id].stacked).toBe(true);
      expect(n[id].x).toBe(26);
    }
    expect(n.b.y).toBe(n.a.y + 50 + 8);
    // One card wide plus the indent, not five cards wide.
    expect(l.width).toBe(26 + 200);
  });

  it("stacks leaves from two on, so a list always means members and a row always means reports", () => {
    const l = layoutTree(box("lead", [box("a"), box("b")]));
    expect(l.nodes.filter((n) => n.stacked).map((n) => n.id)).toEqual(["a", "b"]);
    expect(l.width).toBe(26 + 200);
  });

  it("does not stack when one child has children of its own", () => {
    const l = layoutTree(box("lead", [box("a"), box("b"), box("c"), box("d", [box("x")])]));
    expect(l.nodes.every((n) => !n.stacked)).toBe(true);
  });

  it("draws one connector path per parent and none for leaves", () => {
    const l = layoutTree(box("org", [box("d1", [box("m1")]), box("d2")]));
    expect(l.edges.map((e) => e.id).sort()).toEqual(["d1", "org"]);
    // The bus of `org` lies in the gap between the levels, below the parent
    // and above the children.
    const n = byId(l.nodes);
    const bus = Number(/V([\d.]+)/.exec(l.edges.find((e) => e.id === "org")!.d)![1]);
    expect(bus).toBeGreaterThan(n.org.y + n.org.h);
    expect(bus).toBeLessThan(n.d1.y);
  });

  it("keeps a row where the parent asks for one, however leafy the children", () => {
    const l = layoutTree({ ...box("org", [box("d1"), box("d2")]), stackChildren: false });
    expect(l.nodes.every((n) => !n.stacked)).toBe(true);
    expect(l.width).toBe(2 * 200 + 18);
  });

  it("gives a collapsed branch (no children passed) the width of its own card", () => {
    const l = layoutTree({ ...box("org", [box("d1"), box("d2")]), stackChildren: false });
    expect(l.width).toBe(2 * 200 + 18);
    expect(l.height).toBe(50 + 44 + 50);
  });
});
