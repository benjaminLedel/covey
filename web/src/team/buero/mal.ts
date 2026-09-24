import * as THREE from "three";
import { hash } from "./mathe";

/* The drawing context: palette, materials and the primitives everything in
 * the house is built from — walls, floors, every piece of furniture.
 *
 * WHY A CONTEXT AND NOT ARGUMENTS. A chair is a pure function of (x, y).
 * Where it is hung, which palette applies and which accent the room carries
 * it reads from `M` instead of being handed them: lengthening the signature
 * of each of the hundred catalogue pieces by three arguments, only so they
 * can pass something on, would mean touching every wrapper.
 *
 * `FARBEN` is an object that is REFILLED, not a reference that changes: the
 * catalogue writes `FARBEN.holz`, and that has to stay the same line after
 * the switch to night.
 */

export type Palette = {
  boden: number; bodenHell: number; flur: number; gemein: number;
  wand: number; grund: number;
  holz: number; holzDunkel: number; stoff: number; metall: number;
  schirm: number; schirmAn: number; topf: number; laub: number;
  teppich: number; papier: number; dunkel: number;
  parkett: number; parkettHell: number; nadelfilz: number; nadelfilzHell: number;
  linoleum: number; linoleumHell: number; beton: number; betonHell: number;
  terrazzo: number; terrazzoHell: number; glas: number;
  weiss: number; holzHell: number;
  akzent1: number; akzent2: number; akzent3: number; akzent4: number; akzent5: number;
};

/* The tones must lie apart, otherwise everything is one shade of cream and
   the light has nothing to show itself on. The floor light, the corridor a
   step deeper, the wood rich enough to read as warm — and the green outside
   clearly different, so the building has an edge to the world.

   Every floor covering has a resting and a working tone; the second is
   always warmer and lighter, so that the information "somebody works here"
   does not depend on which covering happens to lie in the room.

   White desk tops and light wood carry the surfaces, the five accents carry
   what one touches. The accents are richer than anything else, but not
   loud: they are to tell one room from the next, not to turn the house into
   a playground. Coral, mustard, teal, sky blue, violet — five tones far
   enough apart that two neighbours never look the same. They stand a step
   darker than one would mix them: sun and sky together lift every surface,
   and a middle tone would arrive as pastel. */
export const TAG: Palette = {
  boden: 0xf4eee3, bodenHell: 0xfae0c6, flur: 0xd9cdb9, gemein: 0xe7e6e0,
  wand: 0xdcd2c4, grund: 0xbccab0,
  holz: 0xbd9d72, holzDunkel: 0xa89076, stoff: 0x8b9ab4, metall: 0xb4bac2,
  schirm: 0x4a4d52, schirmAn: 0xffb268, topf: 0xc07a4e, laub: 0x6ba37c,
  teppich: 0xcbbca2, papier: 0xf7f4ed, dunkel: 0x5c5751,
  parkett: 0xe9d9bf, parkettHell: 0xf6d9b2, nadelfilz: 0xdddbd2, nadelfilzHell: 0xf3dcc0,
  linoleum: 0xe6eadf, linoleumHell: 0xf8e5c8, beton: 0xdcdddb, betonHell: 0xefe0ca,
  terrazzo: 0xf0ece5, terrazzoHell: 0xfbe6cc, glas: 0xd3e3e8,
  weiss: 0xf6f5f1, holzHell: 0xdcc39b,
  akzent1: 0xd95f4a, akzent2: 0xd09a22, akzent3: 0x1f8a82, akzent4: 0x3f8ccb, akzent5: 0x7a57b8,
};

/* At night the surfaces are not simply darker but cooler and desaturated —
   what glows warm at night is the light itself, not the wall. The accents
   stay recognisable in hue: a chair that turns grey at night loses the room
   it belongs to. */
export const NACHT: Palette = {
  boden: 0x2b2c30, bodenHell: 0x3a3229, flur: 0x222327, gemein: 0x2a2c31,
  wand: 0x34343a, grund: 0x1d221c,
  holz: 0x4a4038, holzDunkel: 0x332c26, stoff: 0x3a4152, metall: 0x474c54,
  schirm: 0x24262a, schirmAn: 0xffb268, topf: 0x5a3f2e, laub: 0x2f5340,
  teppich: 0x38342c, papier: 0x4a4a48, dunkel: 0x25262a,
  parkett: 0x2f2b27, parkettHell: 0x41352a, nadelfilz: 0x2a2b2f, nadelfilzHell: 0x3b3229,
  linoleum: 0x292d2b, linoleumHell: 0x3a3329, beton: 0x2c2d30, betonHell: 0x3b342d,
  terrazzo: 0x303032, terrazzoHell: 0x3f3629, glas: 0x2d3540,
  weiss: 0x505154, holzHell: 0x5c5040,
  akzent1: 0x8a4539, akzent2: 0x86682b, akzent3: 0x1d5e59, akzent4: 0x355f82, akzent5: 0x55437c,
};

/* The prototype was tuned on three r128 with legacy lighting. Since r155
   lights are physically based: ambient, hemisphere and directional lights no
   longer carry the implicit factor π, and a point light with decay falls off
   with the inverse square of the distance — in centimetres, that is dark
   after a hand's breadth. So every intensity is scaled by π and every point
   light uses decay 0 inside its cut-off distance, which comes close to the
   old soft falloff. The catalogue's lamps and the scene's lights share these
   two numbers, so a lamp in a lounge is as bright as one at a desk. */
export const LICHT_FAKTOR = Math.PI;
export const LICHT_ABFALL = 0;

/** A point light with the two corrections applied; the catalogue and the
 *  scene create every light through this, so none is forgotten. */
export function punktlicht(farbe: number, staerke: number, weite: number): THREE.PointLight {
  return new THREE.PointLight(farbe, staerke * LICHT_FAKTOR, weite, LICHT_ABFALL);
}

/** The palette in force. Refilled, never replaced. */
export const FARBEN: Palette = { ...TAG };

/** The state of drawing: where things are hung, which accent applies. */
export const M = {
  /** The world everything goes into. The scene sets it before building. */
  welt: null as THREE.Group | null,
  /** A group built into for the moment (a workplace that gets rotated). */
  ziel: null as THREE.Group | null,
  /** The accent of the room being built; 0 means "no room". */
  akzent: 0,
  nacht: false,
  /** Materials, one per colour — a hundred desks share one material. */
  mat: {} as Record<string, THREE.Material>,
};

/** Before every build: refill the palette, drop the materials, set the world. */
export function malenBeginnen(welt: THREE.Group, nacht: boolean): void {
  Object.assign(FARBEN, nacht ? NACHT : TAG);
  M.nacht = nacht;
  M.welt = welt;
  M.ziel = null;
  M.akzent = 0;
  for (const k of Object.keys(M.mat)) delete M.mat[k];
}

const AKZENT_REIHE = ["akzent1", "akzent2", "akzent3", "akzent4", "akzent5"] as const;

/** A room's accent, from its name. */
export function akzentFuer(schluessel: string | number): number {
  return FARBEN[AKZENT_REIHE[hash(String(schluessel)) % AKZENT_REIHE.length]];
}

/** The room's accent and its neighbours. The second tone lies two steps
 *  further along the row, not one — coral next to mustard would be too close
 *  for a cushion on the sofa. Without a room: the quiet fabric tone. */
export function akzent(versatz = 0): number {
  const i = AKZENT_REIHE.findIndex((k) => FARBEN[k] === M.akzent);
  if (i < 0 && versatz === 0) return FARBEN.stoff;
  return FARBEN[AKZENT_REIHE[(Math.max(i, 0) + versatz * 2) % AKZENT_REIHE.length]];
}

export function mischen(a: number, b: number, t: number): number {
  return new THREE.Color(a).lerp(new THREE.Color(b), t).getHex();
}

/** At night the tones lie closer together; a seam that suffices by day
 *  would vanish there. So darken harder at night. */
export function dunkler(c: number, t: number): number {
  return mischen(c, 0x000000, M.nacht ? t * 1.7 : t);
}

function stoff(farbe: number): THREE.Material {
  return new THREE.MeshLambertMaterial({ color: farbe });
}
export function mat(name: string, farbe: number): THREE.Material {
  return M.mat[name] || (M.mat[name] = stoff(farbe));
}
/** A screen that is on glows by itself — it is not lit. Otherwise it would
 *  be the one thing in the room that stays dark at night. */
export function leuchtstoff(farbe: number): THREE.Material {
  return M.mat["l" + farbe] || (M.mat["l" + farbe] = new THREE.MeshBasicMaterial({ color: farbe }));
}
/** Glass: transparent, casts no shadow, does not write depth — otherwise
 *  panes behind each other cut holes into one another. */
export function glasstoff(): THREE.Material {
  return M.mat.glas || (M.mat.glas = new THREE.MeshLambertMaterial({ color: FARBEN.glas, transparent: true, opacity: 0.45, depthWrite: false }));
}

/* A rounded box. The rounding is half the reason a rendered piece of
   furniture looks soft: a sharp edge catches no light, a rounded one draws
   a highlight. */
export function rundQuader(b: number, h: number, t: number, r = 6): THREE.BufferGeometry {
  const form = new THREE.Shape();
  const rr = Math.min(r, b / 2 - 0.1, t / 2 - 0.1);
  form.moveTo(-b / 2 + rr, -t / 2);
  form.lineTo(b / 2 - rr, -t / 2);
  form.quadraticCurveTo(b / 2, -t / 2, b / 2, -t / 2 + rr);
  form.lineTo(b / 2, t / 2 - rr);
  form.quadraticCurveTo(b / 2, t / 2, b / 2 - rr, t / 2);
  form.lineTo(-b / 2 + rr, t / 2);
  form.quadraticCurveTo(-b / 2, t / 2, -b / 2, t / 2 - rr);
  form.lineTo(-b / 2, -t / 2 + rr);
  form.quadraticCurveTo(-b / 2, -t / 2, -b / 2, -t / 2 + rr);
  const bevel = Math.min(2.5, h / 4);
  const g = new THREE.ExtrudeGeometry(form, {
    depth: h - bevel * 2, bevelEnabled: true, bevelThickness: bevel, bevelSize: bevel, bevelSegments: 2, curveSegments: 4,
  });
  g.rotateX(-Math.PI / 2);
  g.translate(0, bevel, 0);
  return g;
}

/** Where furniture is hung: into the target group, otherwise the world. */
export function anhaengen<T extends THREE.Object3D>(m: T): T {
  (M.ziel || M.welt)!.add(m);
  return m;
}

/** Builds `bauen` into a group of its own and returns it — for everything
 *  that is placed and rotated as a whole: a workplace, a piece in the corridor. */
export function inGruppe<T>(bauen: () => T): { gruppe: THREE.Group; ergebnis: T } {
  const gruppe = new THREE.Group();
  const vorher = M.ziel;
  M.ziel = gruppe;
  let ergebnis: T;
  try {
    ergebnis = bauen();
  } finally {
    M.ziel = vorher;
  }
  return { gruppe, ergebnis };
}

export function zylinder(x: number, y: number, r: number, h: number, farbe: number, y0 = 0, rOben: number | null = null): THREE.Mesh {
  const g = new THREE.CylinderGeometry(rOben ?? r, r, h, 14);
  const m = new THREE.Mesh(g, mat("z" + farbe, farbe));
  m.position.set(x, y0 + h / 2, y);
  m.castShadow = true;
  m.receiveShadow = true;
  return anhaengen(m);
}
export function kugel(x: number, y: number, r: number, farbe: number, y0 = 0, flach = 1): THREE.Mesh {
  const m = new THREE.Mesh(new THREE.IcosahedronGeometry(r, 1), mat("k" + farbe, farbe));
  m.position.set(x, y0 + r * flach, y);
  m.scale.set(1, flach, 1);
  m.castShadow = true;
  m.receiveShadow = true;
  return anhaengen(m);
}
export function kasten(x: number, y: number, b: number, t: number, h: number, farbe: number, rund = 5, y0 = 0): THREE.Mesh {
  const m = new THREE.Mesh(rundQuader(b, h, t, rund), mat("f" + farbe, farbe));
  m.position.set(x, y0, y);
  m.castShadow = true;
  m.receiveShadow = true;
  return anhaengen(m);
}
export function flaeche(x: number, y: number, b: number, t: number, farbe: number, y0 = 0.5): THREE.Mesh {
  const g = new THREE.PlaneGeometry(b, t);
  g.rotateX(-Math.PI / 2);
  const m = new THREE.Mesh(g, mat("p" + farbe, farbe));
  m.position.set(x, y0, y);
  m.receiveShadow = true;
  return anhaengen(m);
}

/** Many small rectangles [x, y, b, t] as one single body. A parquet seam as
 *  a mesh of its own cost a dozen draw calls per room; this way a pattern
 *  stays one call, however fine it is. */
export function muster(rechtecke: [number, number, number, number][], farbe: number, y0 = 0.8): THREE.Mesh | null {
  if (!rechtecke.length) return null;
  const pos = new Float32Array(rechtecke.length * 18);
  const nor = new Float32Array(rechtecke.length * 18);
  rechtecke.forEach(([x, y, b, t], i) => {
    const x1 = x + b, y1 = y + t;
    pos.set([x, y0, y, x, y0, y1, x1, y0, y, x1, y0, y, x, y0, y1, x1, y0, y1], i * 18);
    for (let k = 0; k < 6; k++) nor[i * 18 + k * 3 + 1] = 1;
  });
  const g = new THREE.BufferGeometry();
  g.setAttribute("position", new THREE.BufferAttribute(pos, 3));
  g.setAttribute("normal", new THREE.BufferAttribute(nor, 3));
  const m = new THREE.Mesh(g, mat("p" + farbe, farbe));
  m.receiveShadow = true;
  return anhaengen(m);
}
