// A layered tree layout, computed in code rather than left to the browser.
//
// The org chart used to be a CSS tree: nested lists with pseudo-element
// connectors, a hook that re-measured where flex had wrapped, and a trunk
// that searched for a free column between cards after the fact. The column
// it found was free of departments and full of members, and the line ran
// through six drag grips (#177). Here every subtree owns a horizontal
// corridor no sibling enters, and the connectors of a level live only in the
// gap between that level and the next — a line cannot cross a card because
// no card stands where lines run.
//
// The module knows nothing about people, agents or departments. It takes a
// tree of boxes and returns positions and connector paths; the page decides
// what a box is.

export type TreeInput = {
  id: string;
  w: number;
  h: number;
  /** Visible children only — a collapsed branch passes none. */
  children: TreeInput[];
  /** False keeps this node's children in a row even when they are all
   *  leaves: departments under the organisation are a row of peers, not a
   *  list of reports. */
  stackChildren?: boolean;
};

export type Placed = {
  id: string;
  x: number;
  y: number;
  w: number;
  h: number;
  depth: number;
  /** Laid out as an indented list under its parent (see `stackAt`). */
  stacked: boolean;
};

/** One SVG path per parent, drawing the connectors to all its children. */
export type Edge = { id: string; d: string };

export type Layout = { nodes: Placed[]; edges: Edge[]; width: number; height: number };

export type Options = {
  /** Space between sibling subtrees. */
  gapX: number;
  /** Space between a parent and its children; the connector bus runs at half. */
  gapY: number;
  /** From this many children on, a parent whose children are ALL leaves gets
   *  them as a vertical list with a spine instead of a row — twelve agents
   *  under one lead are two screens wide as a row and one card wide as a
   *  list. Zero disables stacking. */
  stackAt: number;
  /** Indent of a stacked list under its parent's left edge. */
  stackIndent: number;
  /** Vertical space between stacked entries, and above the first. */
  stackGap: number;
};

export const DEFAULTS: Options = { gapX: 18, gapY: 44, stackAt: 2, stackIndent: 26, stackGap: 8 };

type Sub = { node: TreeInput; w: number; h: number; stacked: boolean; kids: Sub[] };

function measure(node: TreeInput, o: Options): Sub {
  const kids = node.children.map((c) => measure(c, o));
  if (kids.length === 0) return { node, w: node.w, h: node.h, stacked: false, kids };

  const allLeaves = kids.every((k) => k.kids.length === 0);
  if (o.stackAt > 0 && node.stackChildren !== false && allLeaves && kids.length >= o.stackAt) {
    const w = Math.max(node.w, o.stackIndent + Math.max(...kids.map((k) => k.w)));
    const h = node.h + kids.reduce((sum, k) => sum + o.stackGap + k.h, 0);
    return { node, w, h, stacked: true, kids };
  }

  const rowW = kids.reduce((sum, k) => sum + k.w, 0) + o.gapX * (kids.length - 1);
  const w = Math.max(node.w, rowW);
  const h = node.h + o.gapY + Math.max(...kids.map((k) => k.h));
  return { node, w, h, stacked: false, kids };
}

function place(sub: Sub, left: number, top: number, depth: number, o: Options, out: Layout) {
  const { node, kids } = sub;
  // A stacked parent sits flush left so its list hangs under its own edge; a
  // branching parent sits over the middle of its children.
  const x = sub.stacked ? left : left + (sub.w - node.w) / 2;
  const y = top;
  out.nodes.push({ id: node.id, x, y, w: node.w, h: node.h, depth, stacked: false });
  if (kids.length === 0) return;

  if (sub.stacked) {
    const spineX = x + o.stackIndent / 2;
    let cy = y + node.h;
    const parts: string[] = [];
    let lastMid = cy;
    for (const k of kids) {
      cy += o.stackGap;
      const kx = left + o.stackIndent;
      out.nodes.push({ id: k.node.id, x: kx, y: cy, w: k.node.w, h: k.node.h, depth: depth + 1, stacked: true });
      lastMid = cy + k.node.h / 2;
      parts.push(`M${r(spineX)} ${r(lastMid)}H${r(kx)}`);
      cy += k.node.h;
    }
    out.edges.push({ id: node.id, d: `M${r(spineX)} ${r(y + node.h)}V${r(lastMid)}` + parts.join("") });
    return;
  }

  const rowW = kids.reduce((sum, k) => sum + k.w, 0) + o.gapX * (kids.length - 1);
  let cursor = left + (sub.w - rowW) / 2;
  const childTop = y + node.h + o.gapY;
  const busY = y + node.h + o.gapY / 2;
  const centres: number[] = [];
  for (const k of kids) {
    place(k, cursor, childTop, depth + 1, o, out);
    // A stacked child's connector meets its own card, not its subtree's centre.
    centres.push(k.stacked ? cursor + k.node.w / 2 : cursor + k.w / 2);
    cursor += k.w + o.gapX;
  }
  const px = x + node.w / 2;
  let d = `M${r(px)} ${r(y + node.h)}V${r(busY)}`;
  if (centres.length > 1) d += `M${r(centres[0])} ${r(busY)}H${r(centres[centres.length - 1])}`;
  for (const cx of centres) d += `M${r(cx)} ${r(busY)}V${r(childTop)}`;
  out.edges.push({ id: node.id, d });
}

// Half-pixel alignment: a 1.5px hairline centred on a whole pixel smears over
// two; on a half pixel it sits crisp at scale 1.
const r = (n: number) => Math.round(n) + 0.5;

export function layoutTree(root: TreeInput, opts: Partial<Options> = {}): Layout {
  const o = { ...DEFAULTS, ...opts };
  const sub = measure(root, o);
  const out: Layout = { nodes: [], edges: [], width: sub.w, height: sub.h };
  place(sub, 0, 0, 0, o, out);
  return out;
}
