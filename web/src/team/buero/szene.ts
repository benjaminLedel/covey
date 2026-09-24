import * as THREE from "three";
import {
  FARBEN, M, akzentFuer, anhaengen, dunkler, flaeche, glasstoff, inGruppe, kasten, leuchtstoff,
  malenBeginnen, mischen, muster,
  LICHT_FAKTOR, punktlicht,
} from "./mal";
import { hash, sorte, wackel } from "./mathe";
import type { Plan, Punkt, Raum, Sitz, Zustand } from "./typen";
import {
  AUSSEN, INNEN, PAD, SCHILD_H, SITZ_B, SITZ_H, TUER_B, belegung, loungeZonen, suche,
} from "./plan";
import { charakterEinrichten } from "./charakter";
import { pflanzeBauen } from "./katalog/pflanzen";
import { PLAETZE } from "./katalog/plaetze";
import { STUEHLE } from "./katalog/stuehle";
import { SCHRAENKE } from "./katalog/schraenke";
import { BESPRECHUNGEN } from "./katalog/besprechungen";
import { KUECHEN } from "./katalog/kuechen";
import { loungeBauen } from "./katalog/lounges";
import { FLURDINGE, FLUR_WAND } from "./katalog/flurdinge";
import { KLEINKRAM, STEHLEUCHTEN, WANDLEUCHTEN } from "./katalog/leuchten";

/* The scene: turns a floor plan into a three.js world.
 *
 * This file builds; it does not live. It knows nothing about figures, signs
 * or the card — the component renders those over the canvas — and it does
 * not read agent status itself. What it needs to know about the people it
 * asks `lage.zustand`, because the only two things in the building that say
 * anything about an agent are the screen on the desk and the light in the
 * room. Everything else hangs on names and stays put when somebody falls
 * asleep.
 *
 * No DOM access here: the file runs under vitest with a bare THREE.Scene.
 */

/** What the scene is told from outside: status per agent, time of day,
 *  furnishing density and the current rotation (for the cutaway). */
export type Lage = {
  zustand: (agentId: string) => Zustand;
  nacht: boolean;
  dichte: number;
  drehung: number;
  namen?: unknown;
};

/** What a build leaves behind: the world group, the lights that belong to
 *  working people (desk lamps, room lights) and the body count. */
export type Bau = {
  welt: THREE.Group;
  lampen: THREE.PointLight[];
  decken: THREE.PointLight[];
  koerper: number;
};

type Seite = "oben" | "unten" | "links" | "rechts";
type Seiten = Record<Seite, boolean>;
type Leuchte = (x: number, y: number, an: boolean) => void;
type Flurding = (x: number, y: number) => void;
type Zone = "wand" | "ecke" | "frei";

/** The start view. Mirrors kamera.ts; the cutaway snaps to quarter views
 *  counted from here. */
export const START_DREHUNG = 0.38;

/* ── The shells ───────────────────────────────────────────────────────────
 * The catalogue draws, the shell places: it picks the kind from a key,
 * builds into a group of its own when the piece has to be turned, and passes
 * on whatever carries a state. */

/* The corridor is not a room, and its pieces do not stand in it like
 * furniture. Two things set it apart:
 *
 *   First, the path runs right through the middle. Figures walk the centre
 *   line of the cross aisle to the desk and back — an umbrella stand there
 *   is not furniture but an obstacle that gets walked through.
 *
 *   Second, most of these pieces need a wall at their back. A row of
 *   pictures, a coat rack with a back board, a bin station: set freely into
 *   the aisle they float and face the wrong way.
 *
 * So everything stands against one of the two aisle walls, facing the path.
 * Where exactly the back lies is measured, not guessed — every piece has a
 * different depth, and a fixed offset would pull the flat picture off the
 * wall and push the deep bench into it. */
function flurdingBauen(plan: Plan, bauen: Flurding, z: number, seite: "links" | "rechts" | "quer"): void {
  const { gruppe: gr } = inGruppe(() => bauen(0, 0));
  /* A piece's front points to −y in its own frame, i.e. to −z unrotated. A
     quarter turn to the left brings it to +x. */
  gr.rotation.y = seite === "links" ? -Math.PI / 2 : seite === "rechts" ? Math.PI / 2 : Math.PI;
  gr.updateMatrixWorld(true);
  const box = new THREE.Box3().setFromObject(gr);
  const links = plan.quer.x + 3, rechts = plan.quer.x + plan.quer.w - 3;
  if (seite === "quer") {
    /* A piece across the aisle is as wide as the aisle allows. The reception
       desk measures 180 and the aisle less — unsquashed it would stand in both
       walls. Only the width is squashed: depth and height stay, otherwise the
       reception turns into a model of one. */
    gr.scale.x = Math.min(1, (plan.quer.w - 14) / Math.max(1, box.max.x - box.min.x));
    gr.updateMatrixWorld(true);
  }
  gr.position.set(
    seite === "links" ? links - box.min.x : seite === "rechts" ? rechts - box.max.x : plan.quer.mitte,
    0, z);
  anhaengen(gr);
}

/* The same for the long corridors between the rows of rooms: the piece
   stands against the upper or lower corridor wall, facing the walking line,
   and its x is given. Where exactly the back lies is measured again. */
function flurdingWaag(bauen: Flurding, x: number, seite: "oben" | "unten", f: { y: number; h: number }): void {
  const { gruppe: gr } = inGruppe(() => bauen(0, 0));
  /* The front points to −y in its own frame, i.e. to −z. At the upper wall
     it has to point to +z: half a turn. */
  gr.rotation.y = seite === "oben" ? Math.PI : 0;
  gr.updateMatrixWorld(true);
  const box = new THREE.Box3().setFromObject(gr);
  gr.position.set(x, 0, seite === "oben" ? f.y + 3 - box.min.z : f.y + f.h - 3 - box.max.z);
  anhaengen(gr);
}

/* Lights are the only pieces that know the time of day — and the only thing
   there may not be arbitrarily many of at night: every point light costs the
   GPU a pass per surface. Hence a budget, reset at the start of each build.
   The selection (floor and wall lights only; the house has no ceiling one
   could hang a pendant from, one looks into it from above) lives in the
   catalogue as STEHLEUCHTEN / WANDLEUCHTEN / KLEINKRAM. */
let lichtBudget = 0;
let nachtBau = false;

/** Builds one light from `liste`, lit when it is night and the budget holds.
 *  Handed to `charakterEinrichten` as its `leuchte`. */
export function leuchteBauen(liste: readonly Leuchte[], x: number, y: number, k: string | number): void {
  const an = nachtBau && lichtBudget > 0;
  if (an) lichtBudget--;
  sorte(liste, k)(x, y, an);
}

/* The workplace comes axis-aligned from the catalogue and is placed and
   turned as a whole — hence its own group. Returns the screens, the only
   parts that carry a state. */
function arbeitsplatzBauen(p: Sitz, an: boolean, k: string): THREE.Mesh[] {
  const { gruppe: gr, ergebnis } = inGruppe(() => sorte(PLAETZE, k)({ x: 0, y: 0 }, an) || []);
  gr.position.set(p.x, 0, p.y);
  gr.rotation.y = -(p.dreh || 0);
  anhaengen(gr);
  return ergebnis;
}

/* ── Floors, windows, department colour ──────────────────────────────────
 * As long as every room had the same slab and the same wall, the plan was a
 * table: one found a room only by its sign. A floor of its own, a strip in
 * the department's colour and windows where the house meets the world give
 * every room something to recognise it by — without drowning the state the
 * floor reports.
 *
 * Everything hangs on the room's name, nothing on chance: the same room has
 * the same parquet and the same windows on every visit. */
const BODEN_ARTEN = ["boden", "parkett", "nadelfilz", "linoleum", "beton", "terrazzo"] as const;
type Rechteck = [number, number, number, number];

/* At most three bodies per floor: the slab and up to two patterns. */
function bodenLegen(r: Raum, hell: boolean): void {
  const art = BODEN_ARTEN[hash(r.name + "b") % BODEN_ARTEN.length];
  const grund = FARBEN[hell ? (`${art}Hell` as const) : art];
  flaeche(r.x + r.w / 2, r.y + r.h / 2, r.w, r.h, grund);
  const k = r.name + art;
  const rr: Rechteck[] = [];
  if (art === "parkett") {
    /* Boards run along the room, joints staggered — a grid of equally long
       boards would read as a table again. */
    const laengs = r.w >= r.h, quer = laengs ? r.h : r.w, lang = laengs ? r.w : r.h;
    const n = Math.max(4, Math.min(16, Math.round(quer / 48))), d = quer / n;
    for (let i = 0; i < n; i++) {
      const a = i * d;
      if (i) rr.push(laengs ? [r.x, r.y + a - 0.8, r.w, 1.6] : [r.x + a - 0.8, r.y, 1.6, r.h]);
      for (let s = 90 + (hash(k + i) % 6) * 30; s < lang - 20; s += 180)
        rr.push(laengs ? [r.x + s - 0.8, r.y + a, 1.6, d] : [r.x + a, r.y + s - 0.8, d, 1.6]);
    }
    muster(rr, dunkler(grund, 0.11));
  } else if (art === "nadelfilz") {
    /* Large carpet tiles, the second tone just beside the first: visible
       when one looks, quiet when not. */
    const T = 100;
    for (let iy = 0; iy * T < r.h; iy++) for (let ix = 0; ix * T < r.w; ix++)
      if ((ix + iy) % 2) rr.push([r.x + ix * T, r.y + iy * T, Math.min(T, r.w - ix * T), Math.min(T, r.h - iy * T)]);
    muster(rr, dunkler(grund, 0.05));
  } else if (art === "beton") {
    /* A screed has control joints or it cracks — one every three metres. */
    for (let x = 300; x < r.w - 40; x += 300) rr.push([r.x + x - 0.7, r.y, 1.4, r.h]);
    for (let y = 300; y < r.h - 40; y += 300) rr.push([r.x, r.y + y - 0.7, r.w, 1.4]);
    muster(rr, dunkler(grund, 0.09));
  } else if (art === "terrazzo") {
    const hier: Rechteck[] = [], dort: Rechteck[] = [];
    for (let i = 0; i < 14; i++) {
      const g = 5 + (hash(k + "g" + i) % 5);
      const px = r.x + r.w / 2 + wackel(k + "x" + i, r.w / 2 - 20), py = r.y + r.h / 2 + wackel(k + "y" + i, r.h / 2 - 20);
      (i % 3 ? hier : dort).push([px - g / 2, py - g / 2, g, g]);
    }
    muster(hier, dunkler(grund, 0.2));
    muster(dort, mischen(grund, FARBEN.holz, 0.45));
  }
}

/** Where a room touches the outer wall. The tolerance swallows the inner
 *  wall the plan might leave between room and outer wall. */
export function amAussen(r: Pick<Raum, "x" | "y" | "w" | "h">, plan: Pick<Plan, "breite" | "hoehe">): Seiten {
  const tol = INNEN + 2;
  return {
    oben: Math.abs(r.y - AUSSEN) <= tol,
    unten: Math.abs(r.y + r.h - (plan.hoehe - AUSSEN)) <= tol,
    links: Math.abs(r.x - AUSSEN) <= tol,
    rechts: Math.abs(r.x + r.w - (plan.breite - AUSSEN)) <= tol,
  };
}

/* One to three windows on a stretch of room, equally wide, each in its own
   bay. The ends stay closed so that neither corner nor partition wall stands
   in an opening. */
function fensterIn(a: number, laenge: number, schluessel: string): [number, number][] {
  const rand = 45, platz = laenge - rand * 2, soll = 110;
  if (platz < soll) return [];
  const n = Math.min(1 + (hash(schluessel) % 3), Math.floor((platz + 40) / (soll + 40)));
  const feld = platz / n, b = Math.min(soll + wackel(schluessel + "b", 20), feld - 30);
  return Array.from({ length: n }, (_, i) => {
    const m = a + rand + feld * (i + 0.5) + wackel(schluessel + i, Math.max(0, (feld - b) / 2 - 15));
    return [m - b / 2, m + b / 2] as [number, number];
  });
}

/* ── The cutaway ──────────────────────────────────────────────────────────
 * The camera looks at the building from about forty degrees, and a wall of
 * height h hides roughly 1.2·h of floor behind it. At 92 inside and 120
 * outside that swallowed the first row of desks in every room whose wall
 * faces the camera. Glass everywhere solved it but looked like an aquarium.
 * So, as in a section drawing: the walls on the two sides facing the camera
 * end at the plinth; the ones facing away stand full height and give the
 * room its back. The plinth stays so the floor plan still reads as a line.
 *
 * Which sides those are depends on the rotation — rounded to the nearest
 * quarter view, so a running turn does not make the cut flicker. It is read
 * afresh on every build. */
export const WAND_VOLL = 92, AUSSEN_VOLL = 120, SOCKEL_H = 30;

/** The two sides of the building that face the camera at `drehung`. */
export function kameraSeiten(drehung: number): Seiten {
  const k = Math.round((drehung - START_DREHUNG) / (Math.PI / 2));
  const d = START_DREHUNG + (k * Math.PI) / 2;
  const sx = Math.sin(d), sy = Math.cos(d);
  return { unten: sy > 0, oben: sy < 0, rechts: sx > 0, links: sx < 0 };
}

/** Two adjoining rooms would both draw their common wall. The wall therefore
 *  belongs to the room on the left, or to the band above — the band above a
 *  room spans the same width, so its lower edge is continuous. `unten` /
 *  `rechts` say whether there is a room on the other side; the cutaway
 *  needs that. */
export function geteilt(r: Raum, plan: Pick<Plan, "raeume">): Seiten {
  const neben = (q: Raum) => q !== r && Math.abs(q.y - r.y) < 1;
  return {
    oben: plan.raeume.some((q) => Math.abs(q.y + q.h + INNEN - r.y) < 1),
    unten: plan.raeume.some((q) => Math.abs(r.y + r.h + INNEN - q.y) < 1),
    links: plan.raeume.some((q) => neben(q) && Math.abs(q.x + q.w + INNEN - r.x) < 1),
    rechts: plan.raeume.some((q) => neben(q) && Math.abs(r.x + r.w + INNEN - q.x) < 1),
  };
}

const GEGEN: Record<Seite, Seite> = { oben: "unten", unten: "oben", links: "rechts", rechts: "links" };

/** How high a room's wall stands on one side. A wall between two rooms is,
 *  for one of them, always the side towards the camera — it hides that
 *  room's floor — and so it stays a plinth, whoever owns it. Also for other
 *  code that wants to know whether something can hang on a wall. */
export function wandHoch(r: Raum, seite: Seite, plan: Pick<Plan, "breite" | "hoehe" | "raeume">, drehung: number): number {
  const cam = kameraSeiten(drehung);
  if (amAussen(r, plan)[seite]) return cam[seite] ? SOCKEL_H : AUSSEN_VOLL;
  return cam[seite] || (geteilt(r, plan)[seite] && cam[GEGEN[seite]]) ? SOCKEL_H : WAND_VOLL;
}

/* The outer wall in runs: closed where there is no window; below a window a
   parapet, above it glass up to the top edge. The glass casts no shadow —
   otherwise no light would fall through the window.

   NO LINTEL. The first draft had one, as a house does — and from the height
   one looks at this building from, the wall is mostly its top edge. A
   lintel lets that edge run through, and the window becomes a lighter patch
   on the face that one takes for a bug. In a plan a window is read because
   the thick wall line gets thin there: so glass up to the top, a dark frame,
   a mullion — from above a fine line between two pieces of masonry, from the
   front a window. */
function aussenwandZiehen(plan: Plan, hoch: number, cam: Seiten): void {
  const BRUEST = 30, RAHMEN = 4, A = AUSSEN, B = plan.breite, H = plan.hoehe;
  const oeff: Record<Seite, [number, number][]> = { oben: [], unten: [], links: [], rechts: [] };
  for (const r of plan.raeume) {
    const am = amAussen(r, plan);
    for (const s of ["oben", "unten"] as const) if (am[s]) oeff[s].push(...fensterIn(r.x, r.w, r.name + s));
    for (const s of ["links", "rechts"] as const) if (am[s]) oeff[s].push(...fensterIn(r.y, r.h, r.name + s));
  }
  const laeufe: [Seite, boolean, number, number][] = [
    ["oben", true, B, 0], ["unten", true, B, H - A], ["links", false, H, 0], ["rechts", false, H, B - A],
  ];
  for (const [seite, waag, bis, fest] of laeufe) {
    const teil = (a: number, l: number, h: number, y0: number, dicke = A, farbe = FARBEN.wand) => waag
      ? kasten(a + l / 2, fest + A / 2, l, dicke, h, farbe, 1, y0)
      : kasten(fest + A / 2, a + l / 2, dicke, l, h, farbe, 1, y0);
    /* Towards the camera only the parapet, running into the corners: a
       window in a wall that is no longer there would be a mere frame. */
    if (cam[seite]) { teil(0, bis, SOCKEL_H, 0); continue; }
    let x = 0;
    for (const [o0, o1] of oeff[seite].sort((p, q) => p[0] - q[0])) {
      if (o0 - x > 1) teil(x, o0 - x, hoch, 0);
      const w = o1 - o0;
      teil(o0, w, BRUEST, 0);
      /* The frame: two posts, a rail on top, a mullion in the middle when the
         window is wide enough. Dark, to stand off the wall; as thick as the
         wall, so from above it continues the wall's line. */
      teil(o0, RAHMEN, hoch - BRUEST, BRUEST, A - 2, FARBEN.dunkel);
      teil(o1 - RAHMEN, RAHMEN, hoch - BRUEST, BRUEST, A - 2, FARBEN.dunkel);
      teil(o0, w, RAHMEN, hoch - RAHMEN, A - 2, FARBEN.dunkel);
      if (w > 90) teil(o0 + w / 2 - RAHMEN / 2, RAHMEN, hoch - BRUEST, BRUEST, A - 2, FARBEN.dunkel);
      const g = teil(o0, w, hoch - BRUEST - RAHMEN, BRUEST, 3);
      g.material = glasstoff();
      g.castShadow = false;
      x = o1;
    }
    if (bis - x > 1) teil(x, bis - x, hoch, 0);
  }
}

const TECHNIK = /tech|entwick|dev|it\b|infra|ops|daten|data|engin|plattform|platform|system|sicher|secur|qa|test|backend|frontend|netz|server|cloud/i;

/* The department colour as door frame and skirting, mixed towards the wall:
   it should assign the room, not paint it. The picture on the side wall
   only where the name does not sound technical — a server department with a
   watercolour would be the wrong message. */
function zimmerZeichen(r: Raum, am: Seiten, wandH: number, hoch: (s: Seite) => number): void {
  if (r.gem || !/^#[0-9a-f]{6}$/i.test(r.farbe || "")) return;
  const farbe = mischen(parseInt(r.farbe.slice(1), 16), FARBEN.wand, 0.45);
  const wy = r.oben ? r.y + r.h + INNEN / 2 : r.y - INNEN / 2;
  for (const sx of [-1, 1]) kasten(r.tuerX + sx * (TUER_B / 2 - 1), wy, 5, INNEN + 4, wandH + 3, farbe, 1);
  kasten(r.x + r.w / 2, r.oben ? r.y + 2 : r.y + r.h - 2, r.w - 8, 3, 9, farbe, 1);
  if (TECHNIK.test(r.name)) return;
  const links = am.rechts || hash(r.name + "bild") % 2 === 0;
  if (links && am.links) return;
  if (hoch(links ? "links" : "rechts") < WAND_VOLL) return;
  const bx = links ? r.x + 2 : r.x + r.w - 2, by = r.y + r.h * (0.45 + wackel(r.name + "bi", 0.08));
  const lang = 44 + (hash(r.name + "bl") % 3) * 10;
  const bunt = [FARBEN.laub, FARBEN.topf, FARBEN.stoff, farbe][hash(r.name + "bf") % 4];
  kasten(bx, by, 2, lang, 32, FARBEN.holzDunkel, 1, 54);
  kasten(bx + (links ? 0.6 : -0.6), by, 2, lang - 8, 24, mischen(bunt, FARBEN.papier, 0.25), 1, 58);
}

/** Removes an old world from the scene and frees what the GPU holds for it.
 *  Materials go too: `malenBeginnen` empties the material cache, so nothing
 *  in the new build can still share one of them. */
function entsorgen(szene: THREE.Scene, alt: THREE.Group): void {
  szene.remove(alt);
  const stoffe = new Set<THREE.Material>();
  alt.traverse((o) => {
    const m = o as THREE.Mesh;
    if (m.geometry) m.geometry.dispose();
    if (m.material) for (const s of Array.isArray(m.material) ? m.material : [m.material]) stoffe.add(s);
    if ((o as THREE.Light).isLight) (o as THREE.Light).dispose();
  });
  for (const s of stoffe) s.dispose();
}

/** Builds the world for `plan` into `szene`, replacing `alt`. */
export function szeneBauen(szene: THREE.Scene, alt: THREE.Group | null, plan: Plan, lage: Lage): Bau {
  if (alt) entsorgen(szene, alt);
  const lampen: THREE.PointLight[] = [], decken: THREE.PointLight[] = [];
  const nacht = lage.nacht, dichte = lage.dichte;
  lichtBudget = 18;
  nachtBau = nacht;
  const welt = new THREE.Group();
  welt.name = "welt";
  malenBeginnen(welt, nacht);
  szene.add(welt);
  /* The origin to the middle of the building — then the camera turns around
     the house. `aufSchirm` undoes this offset. */
  welt.position.set(-plan.breite / 2, 0, -plan.hoehe / 2);
  const cam = kameraSeiten(lage.drehung);

  /* Ground, floors, walls. */
  flaeche(plan.breite / 2, plan.hoehe / 2, plan.breite + 150, plan.hoehe + 150, FARBEN.grund, -2);
  flaeche(plan.quer.x + plan.quer.w / 2, plan.hoehe / 2, plan.quer.w, plan.hoehe - AUSSEN * 2, FARBEN.flur);
  for (const f of plan.flure)
    flaeche(plan.quer.x + (plan.breite - plan.quer.x - AUSSEN) / 2, f.y + f.h / 2,
      plan.breite - plan.quer.x - AUSSEN, f.h, FARBEN.flur);
  /* The runner: a band in one colour on every walking line, with a darker
     hem. It turns the corridor into a path — before, it was a surface in the
     same colour as everything else, and nobody read it as a corridor. One
     colour for the whole house, because the corridor belongs to no
     department. */
  const laeufer = mischen(FARBEN.akzent3, FARBEN.papier, 0.5), saum = mischen(laeufer, FARBEN.dunkel, 0.35);
  /* Narrower than the aisle would allow: the pieces along the walls are up to
     70 deep, and a runner lying under them turns the path back into a
     surface. 76 in the cross aisle, 56 in the long corridors leave both sides
     their strip. */
  const LQ = 76, LF = 56;
  flaeche(plan.quer.mitte, plan.hoehe / 2, LQ + 8, plan.hoehe - AUSSEN * 2 - 60, saum, 0.8);
  flaeche(plan.quer.mitte, plan.hoehe / 2, LQ, plan.hoehe - AUSSEN * 2 - 68, laeufer, 1.0);
  for (const f of plan.flure) {
    const x0 = plan.quer.mitte, x1 = plan.breite - AUSSEN - 40;
    flaeche((x0 + x1) / 2, f.mitte, x1 - x0, LF + 8, saum, 0.8);
    flaeche((x0 + x1) / 2 + 4, f.mitte, x1 - x0 - 8, LF, laeufer, 1.0);
  }

  /* The wall height follows the tallest piece of furniture, not the other way
     round. A catalogue shelf measures 108 — against a wall of 52 it would
     stand like a wardrobe in a sandpit. 92 inside, 120 outside — but only on
     the sides facing away from the camera; towards it `wandHoch` cuts them
     down to the plinth. */
  const wandStueck = (x: number, y: number, b: number, t: number, h: number) =>
    kasten(x + b / 2, y + t / 2, b, t, h, FARBEN.wand, 2);
  aussenwandZiehen(plan, AUSSEN_VOLL, cam);

  for (const r of plan.raeume) {
    const arbeitet = r.leute.some((a) => lage.zustand(a.id) === "arbeitet");
    r.hell = arbeitet;
    /* The room's accent: one per room, from its name, so that chair, rug and
       sofa go together and not every piece looks separately rolled. The
       corridor resets it further down. */
    M.akzent = akzentFuer(r.name);
    /* Where somebody works, the room light burns at night — and only there.
       A room where everyone sleeps stays dark; that is the same message as
       by day, only at night nobody can miss it. */
    if (nacht && arbeitet && decken.length < 8) {
      const D = punktlicht(0xffd9ac, 1.7, Math.max(r.w, r.h) * 1.5);
      D.position.set(r.x + r.w / 2, 135, r.y + r.h / 2);
      welt.add(D);
      decken.push(D);
    }
    if (r.gem) flaeche(r.x + r.w / 2, r.y + r.h / 2, r.w, r.h, FARBEN.gemein);
    else bodenLegen(r, arbeitet);

    /* Walls with a gap for the door. */
    const t = INNEN;
    const luecke = (x0: number, laenge: number, tuer: boolean): [number, number][] => tuer
      ? [[x0, r.tuerX - TUER_B / 2 - x0], [r.tuerX + TUER_B / 2, x0 + laenge - (r.tuerX + TUER_B / 2)]]
      : [[x0, laenge]];
    /* Where the room lies against the outer wall, its own wall is dropped: it
       would stand in the middle of the windows. A shared wall is drawn by its
       owner only. */
    const am = amAussen(r, plan), ge = geteilt(r, plan);
    const hoch = (s: Seite) => wandHoch(r, s, plan, lage.drehung);
    if (!am.oben && !ge.oben) for (const [ax, aw] of luecke(r.x - t, r.w + 2 * t, !r.oben)) if (aw > 2) wandStueck(ax, r.y - t, aw, t, hoch("oben"));
    if (!am.unten) for (const [ax, aw] of luecke(r.x - t, r.w + 2 * t, r.oben)) if (aw > 2) wandStueck(ax, r.y + r.h, aw, t, hoch("unten"));
    if (!am.links && !ge.links) wandStueck(r.x - t, r.y, t, r.h, hoch("links"));
    if (!am.rechts) wandStueck(r.x + r.w, r.y, t, r.h, hoch("rechts"));
    zimmerZeichen(r, am, hoch(r.oben ? "unten" : "oben"), hoch);

    const b = belegung(r);
    b.sperre(r.tuerX - TUER_B / 2 - 10, r.oben ? r.y + r.h - 80 : r.y - 10, TUER_B + 20, 90);

    /* Which kind of workplace and which chair: one choice per room, so a
       department looks furnished and not thrown together. */
    const platzArt = r.name + "p";
    const stuhlArt = r.name + "s";
    r.sitze.forEach((p, i) => {
      b.sperre(p.x - SITZ_B / 2, p.y - SITZ_H / 2, SITZ_B, SITZ_H);
      const agent = r.leute[i];
      const slug = agent?.slug ?? r.name + i;
      const an = !!agent && lage.zustand(agent.id) === "arbeitet";
      for (const schirm of arbeitsplatzBauen(p, an, platzArt)) {
        if (!an) continue;
        schirm.material = leuchtstoff(FARBEN.schirmAn);
        schirm.castShadow = false;
      }
      if (an && nacht && lampen.length < 14) {
        /* The desk lamp: at night, the thing that lights the room. */
        const L = punktlicht(0xffb266, 2.8, 460);
        L.position.set(p.x, 78, p.y + 16);
        welt.add(L);
        lampen.push(L);
      }
      /* The chair belongs to the workplace and turns with it — and it never
         stands neatly at it. It is pulled out and twisted, because that is
         what tells a used office from a catalogue photo. It stands BEHIND
         the seat point, the desk top in front: p is where the figure
         stands, and the top lies at −10 in front of it. */
      const { gruppe } = inGruppe(() =>
        sorte(STUEHLE, stuhlArt)(wackel(slug + "sx", 26), 34 + wackel(slug + "sy", 16)));
      gruppe.position.set(p.x, 0, p.y);
      gruppe.rotation.y = -(p.dreh || 0) + wackel(slug + "sd", 0.5);
      welt.add(gruppe);
    });

    /* The catalogue plants are wider than the old bushes — a palm spans a
       good seventy across its fronds. The reserved spot grows with it,
       otherwise the frond stands in the cupboard door. */
    let nr = 0;
    const pflanze = (zone: Zone, k = r.name) => {
      const p = suche(b, 72, 72, zone);
      if (p) pflanzeBauen(p[0] + 36, p[1] + 36, k + "f" + nr++);
    };
    const schrank = (w: number, d: number, zone: Zone) => {
      const p = suche(b, w, d, zone);
      if (p) sorte(SCHRAENKE, r.name + "k" + nr++)(p[0] + w / 2, p[1] + d / 2, w, d);
    };
    const leuchte = (liste: readonly Leuchte[], w: number, d: number, zone: Zone) => {
      const p = suche(b, w, d, zone);
      if (p) leuchteBauen(liste, p[0] + w / 2, p[1] + d / 2, r.name + "l" + nr++);
    };

    /* The shared rooms furnish themselves as a whole: the catalogue knows
       several meeting rooms and kitchens and fits each to the size it gets.
       So no furnishing stands here any more, only the area it may fill. */
    const cx = r.x + r.w / 2, cy = r.y + SCHILD_H + (r.h - SCHILD_H) / 2;
    const iw = r.w - PAD * 2, ih = r.h - SCHILD_H - PAD * 2;
    if (r.gem === "lounge") {
      /* The lounge furnishes itself in islands (see `loungeZonen`); the edge
         gets plants and light but no cupboards — nothing is put away here.
         The keys carry the number, otherwise every lounge would have the same
         palm in the same corner. */
      const zonen = loungeZonen(r, dichte);
      for (const z of zonen) {
        b.sperre(z.x - z.b / 2, z.y - z.t / 2, z.b, z.t);
        loungeBauen(z, r.variante ?? 0);
      }
      /* Sparingly: the islands carry their own plants. Only where a single
         island stands does the edge get one more. */
      if (zonen.length < 2) pflanze("ecke", "lounge" + r.variante);
      leuchte(STEHLEUCHTEN, 56, 56, "ecke");
    } else if (r.gem) {
      /* The catalogue furnishes the middle, but it does not fill a hall: a
         table does not become six metres long because the room allows it.
         So it gets a capped island, and the edge is furnished as in any other
         room — otherwise a kitchen stands in a hall. */
      const mw = Math.min(iw, 460), mh = Math.min(ih, 380);
      b.sperre(cx - mw / 2, cy - mh / 2, mw, mh);
      sorte(r.gem === "besprechung" ? BESPRECHUNGEN : KUECHEN, r.name + r.gem[0])(cx, cy, mw, mh);
      schrank(130, 38, "wand");
      if (iw > 520) schrank(90, 38, "wand");
      pflanze("ecke");
      pflanze("ecke");
      if (iw > 420) pflanze("ecke");
      leuchte(STEHLEUCHTEN, 56, 56, "ecke");
      leuchte(WANDLEUCHTEN, 40, 30, "wand");
      if (r.gem === "besprechung" && iw > 520) leuchte(KLEINKRAM, 190, 70, "frei");
    } else {
      /* Every department after its character — see charakter.ts. */
      charakterEinrichten(r, b, dichte, leuchteBauen);
    }
  }

  /* Reception and corridor. The desk is the first piece of the corridor
     catalogue — the rest is spread over the cross aisle so the way from the
     door to the room does not lead through an empty lane. None of them says
     anything about an agent: the corridor is the place without state.
     The desk is the only piece set across the aisle — it closes off the head
     of the corridor and faces those waiting below it. */
  M.akzent = 0;
  flurdingBauen(plan, FLURDINGE[0], plan.tresen.y, "quer");
  pflanzeBauen(plan.quer.x + 34, plan.tresen.y - 50, "empfang");

  /* Below it lies the queue (plan.tresen.plaetze) — the aisle stays free
     there. Only behind it does the row along the walls begin, alternating
     left and right, so the path between stays open. */
  const warte: Punkt[] = plan.tresen.plaetze;
  const ab = (warte.length ? warte[warte.length - 1].y : plan.tresen.y) + 90;
  const bahn = plan.hoehe - 60 - ab;
  /* Both sides: a piece every 190, sides alternating, so the pieces on one
     side stand 380 apart. Denser was a flea market — a corridor carries less
     than a room, it is for walking through. The mouths of the long corridors
     stay free, otherwise a locker would stand in the bend. */
  const muendungen = plan.flure.map((f) => f.mitte);
  const stuecke = Math.max(0, Math.min(12, Math.floor((bahn * dichte) / 190)));
  for (let i = 0; i < stuecke; i++) {
    const z = ab + ((i + 0.5) * bahn) / stuecke;
    if (muendungen.some((m) => Math.abs(m - z) < 150)) continue;
    flurdingBauen(plan, sorte(FLUR_WAND, "flur" + i), z, i % 2 === 0 ? "links" : "rechts");
  }
  /* The long corridors: pieces on both walls, every 360, never in front of
     a door and not in the mouth. A floor lamp at the blind end of each
     corridor — no path leads there; before, it stood on the crossing, in
     the middle of the walking line. */
  const schritt = 360 / Math.max(0.1, dichte);
  plan.flure.forEach((f, fi) => {
    const tueren = plan.raeume.filter((r) => Math.abs(r.flurY - f.mitte) < 2)
      .map((r) => ({ x: r.tuerX, seite: r.oben ? "oben" : "unten" }));
    const x0 = plan.quer.x + plan.quer.w + 90, x1 = plan.breite - AUSSEN - 90;
    let k = 0;
    for (let x = x0; x < x1; x += schritt, k++) {
      const seite = k % 2 === 0 ? "unten" : "oben";
      const xs = x + wackel("flurx" + fi + k, 40);
      if (tueren.some((t) => t.seite === seite && Math.abs(t.x - xs) < 130)) continue;
      flurdingWaag(sorte(FLUR_WAND, "lang" + fi + k), xs, seite, f);
    }
    leuchteBauen(STEHLEUCHTEN, plan.breite - AUSSEN - 40, f.mitte, "flurlicht" + f.y);
  });

  const bau: Bau = { welt, lampen, decken, koerper: welt.children.length };
  lichtStellen(szene, plan, nacht, bau);
  return bau;
}

/* ── Light ─────────────────────────────────────────────────────────────────
 * The factor and the falloff live in mal.ts, next to the palette: the
 * catalogue's lamps use the same two numbers. */

/** Two lights, no more: one from above for the base brightness, one at an
 *  angle for the shadows. Those two carry the picture. They are created on
 *  first use and found by name afterwards. */
function lichter(szene: THREE.Scene): { himmel: THREE.HemisphereLight; licht: THREE.DirectionalLight } {
  let himmel = szene.getObjectByName("himmel") as THREE.HemisphereLight | undefined;
  if (!himmel) {
    himmel = new THREE.HemisphereLight(0xf4f7ff, 0xcfc6b4, 0.4 * LICHT_FAKTOR);
    himmel.name = "himmel";
    szene.add(himmel);
  }
  let licht = szene.getObjectByName("licht") as THREE.DirectionalLight | undefined;
  if (!licht) {
    licht = new THREE.DirectionalLight(0xfff4e2, 0.95 * LICHT_FAKTOR);
    licht.name = "licht";
    licht.castShadow = true;
    licht.shadow.mapSize.set(2048, 2048);
    licht.shadow.bias = -0.0006;
    licht.shadow.radius = 2;
    szene.add(licht, licht.target);
  }
  return { himmel, licht };
}

/** Sun (or moon), sky and background for day or night. The prototype also
 *  had an evening; the product knows only day and night, from the theme. */
export function lichtStellen(szene: THREE.Scene, plan: Plan, nacht: boolean, bau?: Bau): void {
  const { himmel, licht } = lichter(szene);
  const d = Math.max(plan.breite, plan.hoehe);
  licht.position.set(nacht ? -d * 0.5 : d * 0.45, d * (nacht ? 0.8 : 0.95), nacht ? d * 0.35 : -d * 0.3);
  licht.target.position.set(0, 0, 0);
  /* At night the moon stands: almost no brightness, but the shadows stay —
     without them the house would be a black surface. */
  licht.intensity = (nacht ? 0.16 : 0.95) * LICHT_FAKTOR;
  licht.color.setHex(nacht ? 0xaebfe0 : 0xfff4e2);
  himmel.intensity = (nacht ? 0.055 : 0.4) * LICHT_FAKTOR;
  himmel.color.setHex(nacht ? 0x2e3a56 : 0xf4f7ff);
  himmel.groundColor.setHex(nacht ? 0x14120f : 0xcfc6b4);
  const c = licht.shadow.camera;
  c.left = -d * 0.75; c.right = d * 0.75; c.top = d * 0.75; c.bottom = -d * 0.75;
  c.near = 1; c.far = d * 3;
  c.updateProjectionMatrix();
  szene.background = new THREE.Color(nacht ? 0x15161a : 0xe4e8dc);
  /* The working lights exist only in a night build; should a day light be
     applied to a night build (theme switched before the rebuild), they go
     dark rather than glow in daylight. */
  if (bau) for (const l of [...bau.lampen, ...bau.decken]) l.visible = nacht;
}

/* ── Plan point → pixel ────────────────────────────────────────────────── */
const _v = new THREE.Vector3();

/** Projects a plan point (x, y in plan centimetres, `hoehe` above the floor)
 *  to stage pixels. The world is offset by −breite/2, −hoehe/2, which is
 *  why the plan comes along. */
export function aufSchirm(
  kamera: THREE.Camera, plan: Pick<Plan, "breite" | "hoehe">, buehneW: number, buehneH: number,
  x: number, y: number, hoehe: number,
): [number, number] {
  _v.set(x - plan.breite / 2, hoehe, y - plan.hoehe / 2).project(kamera);
  return [(_v.x * 0.5 + 0.5) * buehneW, (-_v.y * 0.5 + 0.5) * buehneH];
}
