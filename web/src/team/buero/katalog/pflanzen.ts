import * as THREE from "three";
import { anhaengen, FARBEN, inGruppe, kasten, kugel, zylinder } from "../mal";
import { sorte } from "../mathe";

/* ── The furniture catalogue ───────────────────────────────────────────────
 * Fourteen plants, fifteen workplaces, fifteen chairs, eighteen cabinets,
 * eight seating groups, six meeting rooms, six kitchenettes, fourteen
 * corridor pieces and eight lamps — a hundred and four pieces, each built
 * from the same primitives as the house itself. Which kind stands where is
 * decided by the room's key: the same department, the same furniture, on
 * every visit.
 *
 * The number is not a collector's habit. An office with forty identical
 * desks reads as a warehouse, and a room that looks like the next one gives
 * the eye nothing to remember where it was.
 *
 * Every piece is a pure function: it draws at (x, y) and knows nothing of
 * rooms, states or time. The wrappers place and rotate it. Only the
 * workplace returns something — its screens, because they are the only
 * thing that carries a state.
 */

export type Pflanze = (cx: number, cy: number) => void;

// House plants — the only furnishing allowed to differ in every room.
// Fourteen kinds, because an office with the same bush twelve times looks like a catalogue:
// build, pot and outline from above differ, and the height ranges from the bowl on the
// sideboard to the tree that almost touches the ceiling.
export const PFLANZEN: Pflanze[] = [

  // Box ball — the basic form: round pot, short trunk, a single clipped ball. It stays
  // formal so that the looser crowns beside it read as a different plant.
  (cx, cy) => {
    zylinder(cx, cy, 12, 16, FARBEN.akzent1, 0, 13);
    kugel(cx, cy, 11, FARBEN.dunkel, 11, 0.2);
    zylinder(cx, cy, 2.5, 10, FARBEN.holzDunkel, 15);
    kugel(cx, cy, 19, FARBEN.laub, 22, 0.9);
  },

  // Palm — tall trunk in a square wooden tub, the fronds lie star-shaped around the crown.
  (cx, cy) => {
    kasten(cx, cy, 28, 28, 22, FARBEN.holzDunkel, 4);
    kugel(cx, cy, 12, FARBEN.dunkel, 17, 0.2);
    zylinder(cx, cy, 4.5, 64, FARBEN.holz, 20, 3);
    const wedel = (winkel: number, laenge: number, hoehe: number) => {
      const abstand = laenge / 2 + 3;
      const m = kasten(cx + Math.cos(winkel) * abstand, cy + Math.sin(winkel) * abstand,
        laenge, 8, 5, FARBEN.laub, 2, hoehe);
      m.rotation.y = -winkel;
      return m;
    };
    for (let i = 0; i < 7; i++) {
      const w = (i * Math.PI * 2) / 7;
      wedel(w, i % 2 ? 26 : 32, 81 + (i % 3) * 3);
    }
    kugel(cx, cy, 6, FARBEN.laub, 84, 0.8);
  },

  // Snake plant — upright blades in a slim tall pot; from above a star of strokes.
  (cx, cy) => {
    zylinder(cx, cy, 11, 32, FARBEN.papier, 0, 10);
    kugel(cx, cy, 9, FARBEN.dunkel, 28, 0.2);
    const blatt = (winkel: number, abstand: number, hoehe: number) => {
      const m = kasten(cx + Math.cos(winkel) * abstand, cy + Math.sin(winkel) * abstand,
        5, 7, hoehe, FARBEN.laub, 2, 30);
      m.rotation.y = -winkel;
      return m;
    };
    const hoehen = [52, 38, 46, 60, 34, 44, 56];
    for (let i = 0; i < hoehen.length; i++) {
      blatt((i * Math.PI * 2) / hoehen.length, 3.5 + (i % 3), hoehen[i]);
    }
  },

  // Monstera — flat wide bowl, a few large leaf discs on stems of different heights.
  (cx, cy) => {
    zylinder(cx, cy, 17, 18, FARBEN.topf, 0, 16);
    kugel(cx, cy, 15, FARBEN.dunkel, 12, 0.2);
    const blaetter = [
      [0.4, 11, 34, 13],
      [2.1, 13, 52, 15],
      [3.6, 9, 44, 12],
      [4.9, 14, 66, 14],
      [5.8, 6, 74, 11],
    ];
    for (const [winkel, abstand, hoehe, radius] of blaetter) {
      const bx = cx + Math.cos(winkel) * abstand;
      const by = cy + Math.sin(winkel) * abstand;
      zylinder(bx, by, 1.6, hoehe - 16, FARBEN.laub, 16);
      kugel(bx, by, radius, FARBEN.laub, hoehe - 4, 0.18);
    }
  },

  // Cactus — a column with two arms; the pot is small so the silhouette stands alone.
  (cx, cy) => {
    zylinder(cx, cy, 10, 15, FARBEN.akzent2, 0, 11);
    kugel(cx, cy, 9, FARBEN.grund, 10, 0.25);
    zylinder(cx, cy, 7, 46, FARBEN.laub, 14, 6);
    kugel(cx, cy, 6, FARBEN.laub, 57, 0.8);
    zylinder(cx - 9, cy + 1, 4, 20, FARBEN.laub, 32);
    kugel(cx - 9, cy + 1, 4, FARBEN.laub, 50, 0.9);
    zylinder(cx - 5, cy + 1, 4, 5, FARBEN.laub, 32);
    zylinder(cx + 8, cy - 2, 3.5, 14, FARBEN.laub, 26);
    kugel(cx + 8, cy - 2, 3.5, FARBEN.laub, 38, 0.9);
    zylinder(cx + 5, cy - 2, 3.5, 4, FARBEN.laub, 26);
    // The flower sits in the cap, not above it: y0 63 lies within the tip (57..66.6).
    kugel(cx, cy, 3, FARBEN.papier, 63, 0.9);
  },

  // Ficus — tub on castors so it can be moved around the room; the crown made of three balls.
  (cx, cy) => {
    const rollen = [[-9, -9], [9, -9], [-9, 9], [9, 9]];
    for (const [dx, dy] of rollen) kugel(cx + dx, cy + dy, 2.5, FARBEN.metall, 0, 1);
    kasten(cx, cy, 26, 26, 26, FARBEN.metall, 6, 5);
    kugel(cx, cy, 11, FARBEN.dunkel, 26, 0.2);
    zylinder(cx, cy, 3, 34, FARBEN.holzDunkel, 30, 2.5);
    kugel(cx - 6, cy + 4, 15, FARBEN.laub, 58, 0.9);
    kugel(cx + 7, cy - 3, 13, FARBEN.laub, 66, 0.9);
    kugel(cx + 1, cy + 6, 12, FARBEN.laub, 78, 0.9);
  },

  // Bamboo in a tall pot — four culms with nodes; after the tree the tallest piece in the range.
  (cx, cy) => {
    kasten(cx, cy, 24, 24, 52, FARBEN.holz, 3);
    kugel(cx, cy, 10, FARBEN.dunkel, 48, 0.2);
    const halme = [[-5, -4, 84], [4, -5, 72], [-3, 5, 90], [6, 4, 66]];
    for (const [dx, dy, hoehe] of halme) {
      const hx = cx + dx;
      const hy = cy + dy;
      zylinder(hx, hy, 1.8, hoehe, FARBEN.laub, 50);
      for (let k = 1; k * 22 < hoehe; k++) zylinder(hx, hy, 2.4, 2, FARBEN.holzDunkel, 50 + k * 22);
      for (let b = 0; b < 3; b++) {
        const w = b * 2.1 + dx;
        const m = kasten(hx + Math.cos(w) * 7, hy + Math.sin(w) * 7, 12, 6, 5, FARBEN.laub, 2,
          50 + hoehe - 12 - b * 9);
        m.rotation.y = -w;
      }
    }
  },

  // Trailing plant on a pedestal — the tendrils fall down along the column, so it stands free.
  // The links of a tendril overlap and start below the pot's base, so that one continuous
  // strand hangs rather than a row of separate balls in the air.
  (cx, cy) => {
    zylinder(cx, cy, 13, 4, FARBEN.metall, 0, 11);
    zylinder(cx, cy, 5, 52, FARBEN.metall, 4);
    zylinder(cx, cy, 14, 10, FARBEN.papier, 56, 15);
    kugel(cx, cy, 12, FARBEN.laub, 62, 0.5);
    const ranken = [0.3, 1.9, 3.4, 5.1];
    for (let r = 0; r < ranken.length; r++) {
      const w = ranken[r];
      for (let g = 0; g < 5; g++) {
        const weite = 11 + g * 1.5;
        kugel(cx + Math.cos(w) * weite, cy + Math.sin(w) * weite,
          6 - g * 0.5, FARBEN.laub, 48 - g * 5.5 - (r % 2) * 3, 0.7);
      }
    }
  },

  // Small olive tree — a loose crown of small balls over a thin trunk, clay pot.
  (cx, cy) => {
    zylinder(cx, cy, 11, 24, FARBEN.topf, 0, 15);
    zylinder(cx, cy, 15.5, 3, FARBEN.topf, 21);
    kugel(cx, cy, 13, FARBEN.dunkel, 20, 0.15);
    zylinder(cx, cy, 2.5, 40, FARBEN.holz, 22, 2);
    const ballen = [[0, 0, 74, 11], [-8, 3, 64, 8], [7, -4, 68, 9], [2, 8, 60, 7], [-4, -7, 78, 6]];
    for (const [dx, dy, hoehe, radius] of ballen) {
      kugel(cx + dx, cy + dy, radius, FARBEN.laub, hoehe, 0.75);
    }
  },

  // Grass tuft — many thin blades in a flat bowl; from above a restless circle.
  (cx, cy) => {
    zylinder(cx, cy, 18, 12, FARBEN.akzent3, 0, 19);
    kugel(cx, cy, 17, FARBEN.dunkel, 7, 0.15);
    const hoehen = [34, 46, 28, 40, 52, 30, 44, 36, 48, 26, 42, 38];
    for (let i = 0; i < hoehen.length; i++) {
      const w = (i * Math.PI * 2) / hoehen.length;
      const abstand = 4 + (i % 4) * 3.5;
      const m = kasten(cx + Math.cos(w) * abstand, cy + Math.sin(w) * abstand,
        6, 5, hoehen[i], FARBEN.laub, 2, 11);
      m.rotation.y = -w;
    }
  },

  // Succulent bowl — low enough for the sideboard: rosettes and pebbles in a wide bowl.
  (cx, cy) => {
    zylinder(cx, cy, 20, 8, FARBEN.metall, 0, 21);
    kugel(cx, cy, 19, FARBEN.dunkel, 4, 0.1);
    const rosetten = [[0, 0, 7], [-11, -4, 5], [9, -7, 4.5], [5, 9, 6], [-7, 9, 4], [13, 5, 3.5]];
    for (const [dx, dy, radius] of rosetten) {
      kugel(cx + dx, cy + dy, radius, FARBEN.laub, 8, 0.45);
      kugel(cx + dx, cy + dy, radius * 0.5, FARBEN.laub, 8 + radius * 0.6, 0.5);
    }
    kugel(cx - 3, cy - 12, 2.5, FARBEN.grund, 8, 0.5);
    kugel(cx + 15, cy - 2, 2, FARBEN.grund, 8, 0.5);
  },

  // African hemp — tiers of leaf discs in a square wooden tub, narrower towards the top.
  (cx, cy) => {
    kasten(cx, cy, 30, 30, 30, FARBEN.holz, 2);
    kasten(cx, cy, 32, 32, 5, FARBEN.holzDunkel, 2, 25);
    kasten(cx, cy, 32, 32, 5, FARBEN.holzDunkel, 2, 1);
    kugel(cx, cy, 13, FARBEN.dunkel, 28, 0.15);
    zylinder(cx, cy, 3, 62, FARBEN.holzDunkel, 30, 2.2);
    kugel(cx + 1, cy - 1, 19, FARBEN.laub, 52, 0.3);
    kugel(cx - 2, cy + 2, 15, FARBEN.laub, 68, 0.3);
    kugel(cx + 2, cy + 1, 10, FARBEN.laub, 82, 0.35);
  },

  // Large tree in a white tub — the statement piece: trunk up to 160, the crown of four
  // balls that stay within the reserved spot of 72.
  (cx, cy) => {
    zylinder(cx, cy, 20, 34, FARBEN.weiss, 0, 23);
    kugel(cx, cy, 19, FARBEN.dunkel, 30, 0.15);
    zylinder(cx, cy, 4, 110, FARBEN.holzDunkel, 32, 2.5);
    kugel(cx, cy, 30, FARBEN.laub, 112, 0.8);
    kugel(cx - 16, cy + 8, 19, FARBEN.laub, 100, 0.8);
    kugel(cx + 14, cy - 10, 20, FARBEN.laub, 116, 0.8);
    kugel(cx + 4, cy + 14, 15, FARBEN.laub, 128, 0.8);
  },

  // Monstera group in a turquoise tub — seven large leaf discs on stems at different
  // heights; from above a wreath much wider than the pot.
  (cx, cy) => {
    zylinder(cx, cy, 20, 26, FARBEN.akzent3, 0, 22);
    kugel(cx, cy, 19, FARBEN.dunkel, 22, 0.15);
    for (let i = 0; i < 7; i++) {
      const w = (i * Math.PI * 2) / 7 + 0.4;
      const abstand = 14 + (i % 3) * 4;
      const hoehe = 44 + (i % 4) * 9;
      zylinder(cx + Math.cos(w) * abstand * 0.5, cy + Math.sin(w) * abstand * 0.5, 1.4, hoehe - 26, FARBEN.laub, 26);
      kugel(cx + Math.cos(w) * abstand, cy + Math.sin(w) * abstand, 14 - (i % 3), FARBEN.laub, hoehe, 0.22);
    }
  },
];

/** The plant for a key — the same spot always gets the same plant. It is built
 *  into a group of its own that carries its key and spot in `userData`, so a
 *  click on any of its leaves can be traced back to the plant it belongs to. */
export function pflanzeBauen(cx: number, cy: number, schluessel: string | number): THREE.Group {
  const { gruppe } = inGruppe(() => sorte(PFLANZEN, schluessel)(cx, cy));
  gruppe.userData.pflanze = String(schluessel);
  gruppe.userData.punkt = { x: cx, y: cy };
  return anhaengen(gruppe);
}
