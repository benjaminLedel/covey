import * as THREE from "three";
import { flaeche } from "./mal";
import { RASTER } from "./plan";
import type { Plan, Punkt, Raum } from "./typen";

/* Paths inside a room.
 *
 * The walking grid is read from what was actually BUILT, not from the
 * placement bookkeeping: the bookkeeping only knows the patches it reserved,
 * but the catalogue builds fronds, pulled-out chairs and booths that reach
 * beyond their patch. What the camera shows, the path has to walk around.
 *
 * Three levels per room. The first keeps the figure's body (≈14 cm) clear of
 * everything; in tight rooms, though, it closes the aisle between two rows of
 * desks that a person would still take sideways. Then the next, tighter level
 * is asked, rather than letting someone walk straight through the desk.
 *
 * The corridors have no grid: nothing stands in the walking line there, and
 * the plan (`weg` in ./plan) routes them on their centre lines. This file only
 * answers "how do I get from here to there inside room ri".
 */

const LAUF_STUFEN = [2, 1, 0];

/** One widening level of a room's grid.
 *    k       widening in cells
 *    zu      blocked cells (raw cells widened by k, plus a k-wide margin)
 *    haupt   cells reachable from the door on this level
 *    liste   the same cells as a list, in breadth-first order */
export type Stufe = { k: number; zu: Uint8Array; haupt: Uint8Array; liste: Int32Array };

/** The grid of one room: RASTER-sized cells over the room's inner rectangle. */
export type Zimmerfeld = { r: Raum; sp: number; ze: number; roh: Uint8Array; stufen: Stufe[] };

/** The grids of all rooms, in the order of `plan.raeume`, and what building
 *  them cost (ms: total, boxMs: reading the bounding boxes). */
export type LaufFelder = { zimmer: Zimmerfeld[]; ms: number; boxMs: number };

const jetzt = () => (typeof performance !== "undefined" ? performance.now() : Date.now());

/** Reads the built furniture out of `welt` and computes the walking grids.
 *  Call after the scene is built; the grids hold until the next build. */
export function laufFeldBauen(welt: THREE.Group, plan: Plan): LaufFelder {
  const t0 = jetzt();
  welt.updateMatrixWorld(true);
  /* The boxes are taken in welt's own frame, which is the plan's frame: the
     scene shifts welt by half the building (−breite/2, −hoehe/2) to centre it,
     and undoing welt's world matrix undoes exactly that shift — and keeps
     working if the scene ever moves welt differently. */
  const zurueck = welt.matrixWorld.clone().invert();
  const zimmer: Zimmerfeld[] = plan.raeume.map((r) => {
    const sp = Math.max(1, Math.ceil(r.w / RASTER));
    const ze = Math.max(1, Math.ceil(r.h / RASTER));
    return { r, sp, ze, roh: new Uint8Array(sp * ze), stufen: [] };
  });
  const box = new THREE.Box3();
  const eintragen = (o: THREE.Object3D) => {
    box.setFromObject(o);
    if (box.isEmpty()) return;
    box.applyMatrix4(zurueck);
    /* Rugs, floor patterns and the floor slabs themselves are flat; lamps and
       shelf boards hang above — one walks over the first and under the second. */
    if (box.min.y >= 50 || box.max.y - box.min.y <= 6) return;
    const x0 = box.min.x, x1 = box.max.x, y0 = box.min.z, y1 = box.max.z;
    for (const f of zimmer) {
      const r = f.r;
      if (x1 <= r.x || x0 >= r.x + r.w || y1 <= r.y || y0 >= r.y + r.h) continue;
      const a = Math.max(0, Math.floor((x0 - r.x) / RASTER));
      const b = Math.max(0, Math.floor((y0 - r.y) / RASTER));
      const c = Math.min(f.sp - 1, Math.ceil((x1 - r.x) / RASTER) - 1);
      const d = Math.min(f.ze - 1, Math.ceil((y1 - r.y) / RASTER) - 1);
      for (let cy = b; cy <= d; cy++) f.roh.fill(1, cy * f.sp + a, cy * f.sp + c + 1);
    }
  };
  /* One level deep: a workstation or a catalogue piece is a group, and the
     group's hull as a whole would, for a seating corner, be one block over
     sofa, table and the gap between them. Its parts one by one are precise
     enough; going deeper only costs time. */
  for (const o of welt.children) {
    if ((o as THREE.Mesh).isMesh) eintragen(o);
    else if ((o as THREE.Group).isGroup)
      for (const c of o.children) if ((c as THREE.Mesh).isMesh || (c as THREE.Group).isGroup) eintragen(c);
  }
  const boxMs = jetzt() - t0;
  for (const f of zimmer) f.stufen = LAUF_STUFEN.map((k) => laufStufe(f, k));
  return { zimmer, ms: jetzt() - t0, boxMs };
}

/* One level: the raw cells widened by k cells, the room's margin likewise
   (the wall stands behind it), and of the rest the part reachable from the
   door. Only that part is fit as a target — a free island between four desks
   that no path leads into would leave the figure standing. */
function laufStufe(f: Zimmerfeld, k: number): Stufe {
  const { sp, ze, roh } = f;
  const zu = new Uint8Array(sp * ze);
  /* Widening goes row by row: one strip per row of the disc. Widening each
     cell per neighbour would cost a multiple with sixty people. */
  const halb: [number, number][] = [];
  for (let dy = -k; dy <= k; dy++) {
    let w = 0;
    while (w < k && (w + 1) ** 2 + dy * dy <= k * k + k) w++;
    halb.push([dy, w]);
  }
  for (let y = 0; y < ze; y++)
    for (let x = 0; x < sp; x++) {
      if (x < k || y < k || x >= sp - k || y >= ze - k) {
        zu[y * sp + x] = 1;
        continue;
      }
      const i = y * sp + x;
      if (!roh[i]) continue;
      /* Inside a run of occupied cells the own column is enough — the two
         ends of the run widen sideways anyway. */
      const mitte = k > 0 && roh[i - 1] && roh[i + 1];
      for (const [dy, w] of halb) {
        const v = y + dy;
        if (v < 0 || v >= ze) continue;
        if (mitte) zu[v * sp + x] = 1;
        else zu.fill(1, v * sp + Math.max(0, x - w), v * sp + Math.min(sp - 1, x + w) + 1);
      }
    }
  const haupt = new Uint8Array(sp * ze);
  const st: Stufe = { k, zu, haupt, liste: new Int32Array(0) };
  /* The flood starts at the free cell closest to the point behind the door. */
  let s = -1;
  let best = Infinity;
  const ix = Math.floor((f.r.innen.x - f.r.x) / RASTER);
  const iy = Math.floor((f.r.innen.y - f.r.y) / RASTER);
  for (let i = 0; i < zu.length; i++)
    if (!zu[i]) {
      const d = ((i % sp) - ix) ** 2 + (Math.floor(i / sp) - iy) ** 2;
      if (d < best) {
        best = d;
        s = i;
      }
    }
  if (s < 0) return st;
  /* Four directions are enough here: diagonals are only allowed where both
     sides are free (`laufNachbarn`), so the same cells are reached around the
     corner — and half the neighbours is half the time. */
  const schlange = new Int32Array(sp * ze);
  let ende = 1;
  schlange[0] = s;
  haupt[s] = 1;
  for (let q = 0; q < ende; q++) {
    const i = schlange[q];
    const x = i % sp;
    if (x > 0 && !zu[i - 1] && !haupt[i - 1]) { haupt[i - 1] = 1; schlange[ende++] = i - 1; }
    if (x < sp - 1 && !zu[i + 1] && !haupt[i + 1]) { haupt[i + 1] = 1; schlange[ende++] = i + 1; }
    if (i >= sp && !zu[i - sp] && !haupt[i - sp]) { haupt[i - sp] = 1; schlange[ende++] = i - sp; }
    if (i + sp < zu.length && !zu[i + sp] && !haupt[i + sp]) { haupt[i + sp] = 1; schlange[ende++] = i + sp; }
  }
  st.liste = schlange.slice(0, ende);
  return st;
}

/* Eight directions, but never diagonally across a corner: between two pieces
   of furniture that only touch at an edge, nobody fits through. */
function laufNachbarn(f: Zimmerfeld, zu: Uint8Array, i: number, aus: Int32Array): number {
  const { sp, ze } = f;
  const x = i % sp;
  const y = (i - x) / sp;
  const frei = (u: number, v: number) => u >= 0 && v >= 0 && u < sp && v < ze && !zu[v * sp + u];
  let n = 0;
  for (let dy = -1; dy <= 1; dy++)
    for (let dx = -1; dx <= 1; dx++) {
      if (!dx && !dy) continue;
      if (!frei(x + dx, y + dy)) continue;
      if (dx && dy && (!frei(x + dx, y) || !frei(x, y + dy))) continue;
      aus[n++] = (y + dy) * sp + x + dx;
    }
  return n;
}

const zelleMitte = (f: Zimmerfeld, i: number): Punkt => ({
  x: f.r.x + ((i % f.sp) + 0.5) * RASTER,
  y: f.r.y + (Math.floor(i / f.sp) + 0.5) * RASTER,
});
const zelleVon = (f: Zimmerfeld, p: Punkt): number => {
  const x = Math.floor((p.x - f.r.x) / RASTER);
  const y = Math.floor((p.y - f.r.y) / RASTER);
  return x >= 0 && y >= 0 && x < f.sp && y < f.ze ? y * f.sp + x : -1;
};

/* The reachable cell closest to p, with its distance. On a tie the lower
   index wins — the same plan, the same path. */
function laufNaechste(f: Zimmerfeld, st: Stufe, p: Punkt): { i: number; d: number } {
  let best = -1;
  let bd = Infinity;
  for (const i of st.liste) {
    const m = zelleMitte(f, i);
    const d = (m.x - p.x) ** 2 + (m.y - p.y) ** 2;
    if (d < bd) {
      bd = d;
      best = i;
    }
  }
  return { i: best, d: Math.sqrt(bd) };
}

/** The widest level that still has reachable floor. */
const laufStufeVon = (f: Zimmerfeld | undefined): Stufe | undefined => f?.stufen.find((st) => st.liste.length > 0);

/** Is p on reachable free floor of room ri (on the widest level that has any)?
 *  A room without a grid or without free floor answers false. */
export function istFrei(felder: LaufFelder, ri: number, p: Punkt): boolean {
  const f = felder.zimmer[ri];
  const st = laufStufeVon(f);
  if (!f || !st) return false;
  const z = zelleVon(f, p);
  return z >= 0 && st.haupt[z] === 1;
}

/** A meeting point, or the point behind the door, that lies inside a piece
 *  of furniture is pulled onto the nearest free, reachable floor. Seats are
 *  NOT passed through here: they sit at the desk on purpose, and that is
 *  where the figure sits down. */
export function freiNahe(felder: LaufFelder, ri: number, p: Punkt): Punkt {
  const f = felder.zimmer[ri];
  const st = laufStufeVon(f);
  if (!f || !st) return { x: p.x, y: p.y };
  const z = zelleVon(f, p);
  if (z >= 0 && st.haupt[z]) return { x: p.x, y: p.y };
  return zelleMitte(f, laufNaechste(f, st, p).i);
}

/* Clear sight between two points on the grid: every cell the line crosses
   (Amanatides–Woo). If it passes exactly over a corner, both neighbours count
   — otherwise the smoothed line would cut the corner the search avoided. */
function laufSicht(f: Zimmerfeld, zu: Uint8Array, p: Punkt, q: Punkt): boolean {
  const gx0 = (p.x - f.r.x) / RASTER, gy0 = (p.y - f.r.y) / RASTER;
  const gx1 = (q.x - f.r.x) / RASTER, gy1 = (q.y - f.r.y) / RASTER;
  let x = Math.floor(gx0), y = Math.floor(gy0);
  const ex = Math.floor(gx1), ey = Math.floor(gy1);
  const dx = gx1 - gx0, dy = gy1 - gy0;
  const sx = Math.sign(dx), sy = Math.sign(dy);
  const zx = dx ? Math.abs(1 / dx) : Infinity, zy = dy ? Math.abs(1 / dy) : Infinity;
  let tx = dx > 0 ? (x + 1 - gx0) * zx : dx < 0 ? (gx0 - x) * zx : Infinity;
  let ty = dy > 0 ? (y + 1 - gy0) * zy : dy < 0 ? (gy0 - y) * zy : Infinity;
  const belegt = (u: number, v: number) => u < 0 || v < 0 || u >= f.sp || v >= f.ze || zu[v * f.sp + u] === 1;
  for (let n = 0; n < f.sp + f.ze + 4; n++) {
    if (belegt(x, y)) return false;
    if (x === ex && y === ey) return true;
    if (Math.abs(tx - ty) < 1e-9) {
      if (belegt(x + sx, y) || belegt(x, y + sy)) return false;
      x += sx; y += sy; tx += zx; ty += zy;
    } else if (tx < ty) {
      x += sx; tx += zx;
    } else {
      y += sy; ty += zy;
    }
  }
  return false;
}

/* A* over a level, from cell a to cell b. Both lie in the reachable part, so
   a path exists; null only in case that ever does not hold. */
function laufSuche(f: Zimmerfeld, zu: Uint8Array, a: number, b: number): number[] | null {
  const n = f.sp * f.ze;
  const g = new Float64Array(n).fill(Infinity);
  const von = new Int32Array(n).fill(-1);
  const fertig = new Uint8Array(n);
  const bx = b % f.sp, by = Math.floor(b / f.sp);
  const h = (i: number) => {
    const dx = Math.abs((i % f.sp) - bx), dy = Math.abs(Math.floor(i / f.sp) - by);
    return Math.max(dx, dy) + (Math.SQRT2 - 1) * Math.min(dx, dy);
  };
  /* A plain binary heap; on a tie the lower cell, so the path does not depend
     on insertion order. */
  const heap: number[] = [];
  const prio = new Float64Array(n);
  const vor = (i: number, j: number) => prio[i] < prio[j] || (prio[i] === prio[j] && i < j);
  const rein = (i: number) => {
    heap.push(i);
    let k = heap.length - 1;
    while (k) {
      const e = (k - 1) >> 1;
      if (!vor(heap[k], heap[e])) break;
      [heap[k], heap[e]] = [heap[e], heap[k]];
      k = e;
    }
  };
  const raus = () => {
    const top = heap[0];
    const last = heap.pop()!;
    if (heap.length) {
      heap[0] = last;
      let k = 0;
      for (;;) {
        const l = 2 * k + 1, r = l + 1;
        let m = k;
        if (l < heap.length && vor(heap[l], heap[m])) m = l;
        if (r < heap.length && vor(heap[r], heap[m])) m = r;
        if (m === k) break;
        [heap[k], heap[m]] = [heap[m], heap[k]];
        k = m;
      }
    }
    return top;
  };
  const nb = new Int32Array(8);
  g[a] = 0;
  prio[a] = h(a);
  rein(a);
  while (heap.length) {
    const i = raus();
    if (fertig[i]) continue;
    fertig[i] = 1;
    if (i === b) break;
    const m = laufNachbarn(f, zu, i, nb);
    for (let q = 0; q < m; q++) {
      const j = nb[q];
      const schraeg = j % f.sp !== i % f.sp && Math.floor(j / f.sp) !== Math.floor(i / f.sp);
      const ng = g[i] + (schraeg ? Math.SQRT2 : 1);
      if (ng < g[j]) {
        g[j] = ng;
        von[j] = i;
        prio[j] = ng + h(j);
        rein(j);
      }
    }
  }
  if (!fertig[b]) return null;
  const weg = [b];
  while (weg[weg.length - 1] !== a) weg.push(von[weg[weg.length - 1]]);
  return weg.reverse();
}

/** The path from `von` to `ziel` inside room ri, as plan points WITHOUT
 *  `von`. Start and goal may lie inside furniture — a seat lies in the desk's
 *  footprint, the figure sits down there at the end and starts there when it
 *  gets up. The grid path is then pulled taut along lines of sight: stair
 *  steps at eight-centimetre pitch would look like a robot. With no grid or
 *  no free floor the answer is the straight line — better through the
 *  furniture than a figure that stands still. */
export function wegImRaum(felder: LaufFelder, ri: number, von: Punkt, ziel: Punkt): Punkt[] {
  const f = felder.zimmer[ri];
  if (!f || !f.stufen.length) return [{ x: ziel.x, y: ziel.y }];
  /* The finest level says how close one gets to a point at all. A coarser
     one that leaves an end clearly further away has closed an aisle one can
     get through — then the next level. */
  const fein = f.stufen[f.stufen.length - 1];
  if (!fein.liste.length) return [{ x: ziel.x, y: ziel.y }];
  const fa = laufNaechste(f, fein, von).d;
  const fz = laufNaechste(f, fein, ziel).d;
  for (const st of f.stufen) {
    if (!st.liste.length) continue;
    const a = laufNaechste(f, st, von);
    const b = laufNaechste(f, st, ziel);
    if (st !== fein && (a.d > fa + 2 * RASTER || b.d > fz + 2 * RASTER)) continue;
    const za = zelleVon(f, von), zz = zelleVon(f, ziel);
    const vonFrei = za >= 0 && st.haupt[za] === 1;
    const zielFrei = zz >= 0 && st.haupt[zz] === 1;
    if (vonFrei) a.i = za;
    if (zielFrei) b.i = zz;
    if (a.i === b.i) return vonFrei ? [{ x: ziel.x, y: ziel.y }] : [zelleMitte(f, a.i), { x: ziel.x, y: ziel.y }];
    const zellen = laufSuche(f, st.zu, a.i, b.i);
    if (!zellen) break;
    const kette = zellen.map((i) => zelleMitte(f, i));
    if (vonFrei) kette[0] = { x: von.x, y: von.y };
    if (zielFrei) kette[kette.length - 1] = { x: ziel.x, y: ziel.y };
    const glatt = [kette[0]];
    for (let i = 0; i < kette.length - 1; ) {
      let j = kette.length - 1;
      while (j > i + 1 && !laufSicht(f, st.zu, kette[i], kette[j])) j--;
      glatt.push(kette[j]);
      i = j;
    }
    const aus = vonFrei ? glatt.slice(1) : glatt;
    if (!zielFrei) aus.push({ x: ziel.x, y: ziel.y });
    return aus;
  }
  return [{ x: ziel.x, y: ziel.y }];
}

/** A random target for wandering: uniform over the free floor reachable from
 *  the door — never a point somewhere in the rectangle that may lie inside a
 *  cupboard. Math.random on purpose: this is atmosphere, not information. */
export function laufZufall(felder: LaufFelder, ri: number): Punkt | null {
  const f = felder.zimmer[ri];
  const st = laufStufeVon(f);
  if (!f || !st) return null;
  return zelleMitte(f, st.liste[Math.floor(Math.random() * st.liste.length)]);
}

/** For inspection: draws the grid into the world being painted (`M.welt`).
 *  Strong red is what stands; pale red is what only the body margin blocks.
 *  Merged row by row, otherwise it would be ten thousand planes. */
export function laufrasterZeichnen(felder: LaufFelder, plan: Plan): void {
  const stark = new THREE.MeshBasicMaterial({ color: 0xff2020, transparent: true, opacity: 0.45, depthWrite: false });
  const blass = new THREE.MeshBasicMaterial({ color: 0xff2020, transparent: true, opacity: 0.18, depthWrite: false });
  /* Grids from a larger earlier plan would draw into rooms that no longer exist. */
  for (const f of felder.zimmer.slice(0, plan.raeume.length)) {
    const st = f.stufen[0];
    if (!st) continue;
    const art = (i: number) => (f.roh[i] ? 2 : st.zu[i] ? 1 : 0);
    for (let y = 0; y < f.ze; y++)
      for (let x = 0; x < f.sp; ) {
        const a = art(y * f.sp + x);
        let e = x + 1;
        while (e < f.sp && art(y * f.sp + e) === a) e++;
        if (a) {
          const m = flaeche(f.r.x + ((x + e) / 2) * RASTER, f.r.y + (y + 0.5) * RASTER, (e - x) * RASTER - 1, RASTER - 1, 0xff2020, 2);
          m.material = a === 2 ? stark : blass;
          m.receiveShadow = false;
        }
        x = e;
      }
  }
}
