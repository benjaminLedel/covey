import { FARBEN, flaeche, glasstoff, kasten, kugel, leuchtstoff, mischen, zylinder } from "../mal";
import { hash } from "../mathe";
import { pflanzeBauen } from "./pflanzen";
import type { Flaechenstueck } from "./gruppen";

/* Lounges. The area a row leaves over does not become a wider room — a hall
 * with one row of desks in it looks as if the company had shrunk. It becomes
 * the place where you are not at your desk: table tennis, bean bags, a coffee
 * bar, phone booths for the call that does not belong in the open-plan office.
 * The accent colours are the only strong tones in the house, and they stand
 * only here. Scale as in the tea kitchens: table 44, seat 30.
 *
 * Every kind gets an island of roughly 260 to 690 wide and 240 to 380 deep and
 * fills it completely: a rug underneath, the main piece in the middle, the
 * secondary pieces along the edges and in the corners. A single piece of
 * furniture in the middle of an island was the first attempt — from above,
 * three small things in a hall. The rugs lie above 0.5 — that is the height of
 * the room's floor. The back is always −y; which wall that is in the room the
 * kind does not know, and it must not matter to it. */

const loungeAkzent = (i: number) =>
  [FARBEN.akzent1, FARBEN.akzent2, FARBEN.akzent3, FARBEN.akzent4, FARBEN.akzent5][((i % 5) + 5) % 5];
/* The colour offset hangs on the place, not on chance: the same lounge, the
   same bean bags. */
const loungeTon = (x: number, y: number, k = 0) => loungeAkzent(hash(`${Math.round(x)}|${Math.round(y)}`) + k);
/* A rug in the full accent colour shouts; mixed towards paper it stays
   readable as a surface without drowning out the furniture on it. */
const blass = (farbe: number) => mischen(farbe, FARBEN.papier, 0.35);
const loungePflanze = (x: number, y: number, k: string) => pflanzeBauen(x, y, `${k}${Math.round(x)}|${Math.round(y)}`);
/* From above a bean bag is a soft blot, from the side a slumped sphere with a
   hollow. Two bodies are enough. */
const sitzsack = (x: number, y: number, farbe: number, r = 24) => {
  kugel(x, y, r, farbe, 0, 0.5);
  kugel(x, y, r * 0.6, mischen(farbe, FARBEN.dunkel, 0.18), r * 0.42, 0.45);
};
const pouf = (x: number, y: number, farbe: number, r = 16) => zylinder(x, y, r, 22, farbe, 0, r - 2);
const barhocker = (x: number, y: number, farbe: number) => {
  zylinder(x, y, 13, 2, FARBEN.metall);
  zylinder(x, y, 3, 40, FARBEN.metall, 2);
  zylinder(x, y, 14, 5, farbe, 42);
};
const beistell = (x: number, y: number) => {
  zylinder(x, y, 2.5, 26, FARBEN.metall);
  zylinder(x, y, 14, 3, FARBEN.holz, 26);
  zylinder(x + 4, y, 3.5, 5, FARBEN.papier, 29);
};
const stehlampe = (x: number, y: number, farbe: number) => {
  zylinder(x, y, 11, 2, FARBEN.dunkel);
  zylinder(x, y, 1.5, 104, FARBEN.metall, 2);
  zylinder(x, y, 15, 16, farbe, 102, 10);
};
/* The bench stands at the back of the island and faces forward: seat, two
   side panels, a cushion in the island's colour. */
const loungeBank = (x: number, y: number, l: number, farbe: number) => {
  kasten(x, y, l, 36, 5, FARBEN.holz, 3, 22);
  for (const s of [-1, 1]) kasten(x + s * (l / 2 - 10), y, 8, 30, 22, FARBEN.holzDunkel, 2);
  kasten(x, y + 2, l - 14, 28, 4, farbe, 6, 27);
};
const fahrrad = (bx: number, by: number, farbe: number) => {
  for (const dy of [-26, 26]) {
    const rad = zylinder(bx, by + dy, 20, 3, FARBEN.dunkel, 18.5);
    rad.rotation.z = Math.PI / 2;
  }
  kasten(bx, by, 4, 52, 4, farbe, 1, 26);
  kasten(bx, by - 20, 4, 4, 20, farbe, 1, 22);
  kasten(bx, by - 20, 8, 16, 3, FARBEN.dunkel, 2, 42);
  kasten(bx, by + 24, 26, 4, 3, FARBEN.metall, 1, 44);
};

export const LOUNGES: Flaechenstueck[] = [

  // 1 — Table tennis: the table in the middle, green or blue with white lines
  //     and a net that stands out beyond the table. Bean bags in the corners
  //     for those waiting for the next game, a scoreboard on its stand and a
  //     plant at the back.
  (x, y, breite, tiefe) => {
    const m = Math.min(1, (breite - 110) / 250, (tiefe - 120) / 140);
    const L = 250 * m, B = 140 * m, ty = y + tiefe * 0.08;
    const O = y - tiefe / 2, U = y + tiefe / 2, li = x - breite / 2, re = x + breite / 2;
    flaeche(x, y, breite - 8, tiefe - 8, blass(loungeTon(x, y, 1)), 0.7);
    const platte = hash(`${Math.round(x)}tt`) % 2 ? 0x2f6f8f : 0x3f7a4c;
    for (const [sx, sy] of [[-1, -1], [1, -1], [-1, 1], [1, 1]]) zylinder(x + sx * (L / 2 - 14 * m), ty + sy * (B / 2 - 12 * m), 2.5, 40, FARBEN.metall);
    kasten(x, ty, L, B, 4, platte, 2, 40);
    flaeche(x, ty, L - 6, 2, FARBEN.papier, 44.3);                       // centre line
    for (const sx of [-1, 1]) flaeche(x + sx * (L / 2 - 2), ty, 2, B, FARBEN.papier, 44.3);
    for (const sy of [-1, 1]) flaeche(x, ty + sy * (B / 2 - 2), L, 2, FARBEN.papier, 44.3);
    kasten(x, ty, 2, B + 12, 8, FARBEN.dunkel, 1, 44);                  // net
    for (const sy of [-1, 1]) zylinder(x, ty + sy * (B / 2 + 6), 1.5, 10, FARBEN.metall, 44);
    zylinder(x - L * 0.3, ty + B * 0.2, 8, 1.5, 0xb8433a, 44);          // paddles
    zylinder(x + L * 0.28, ty - B * 0.25, 8, 1.5, 0xb8433a, 44);
    kugel(x + L * 0.1, ty + B * 0.1, 2, FARBEN.papier, 44);
    for (const [sx, sy, k] of [[-1, -1, 0], [1, -1, 2], [-1, 1, 3], [1, 1, 4]])
      sitzsack(sx < 0 ? li + 28 : re - 28, sy < 0 ? O + 28 : U - 28, loungeTon(x, y, k), 22);
    // Scoreboard: two legs, a dark board, two light digits.
    const px = x - breite * 0.18, py = O + 22;
    for (const s of [-1, 1]) zylinder(px + s * 20, py, 2, 60, FARBEN.metall);
    kasten(px, py, 50, 4, 30, FARBEN.dunkel, 1, 58);
    for (const s of [-1, 1]) kasten(px + s * 11, py + 2.5, 14, 1, 18, FARBEN.papier, 1, 64);
    loungePflanze(x + breite * 0.18, O + 30, "tt");
  },

  // 2 — A ring of bean bags on a round rug around a low table: the place for the
  //     conversation that is not a meeting. A bench at the back, beside it a
  //     side table and a floor lamp.
  (x, y, breite, tiefe) => {
    const O = y - tiefe / 2, li = x - breite / 2, re = x + breite / 2;
    flaeche(x, y, breite - 8, tiefe - 8, blass(loungeTon(x, y, 3)), 0.7);
    const cy = y + tiefe * 0.1;
    const r = Math.max(60, Math.min(breite / 2 - 30, (tiefe * 0.8) / 2 - 10));
    zylinder(x, cy, r, 0.6, loungeTon(x, y, 1), 0.8);
    zylinder(x, cy, r * 0.62, 0.6, mischen(loungeTon(x, y, 3), FARBEN.papier, 0.35), 1.4);
    zylinder(x, cy, 24, 20, FARBEN.holz, 0, 24);
    zylinder(x - 6, cy + 4, 4, 5, FARBEN.papier, 20);
    kasten(x + 8, cy - 6, 18, 13, 2, loungeTon(x, y, 4), 1, 20);
    const zahl = r > 95 ? 5 : 4;
    for (let i = 0; i < zahl; i++) {
      const w = (i / zahl) * Math.PI * 2 + 0.4;
      sitzsack(x + Math.cos(w) * r * 0.7, cy + Math.sin(w) * r * 0.7, loungeTon(x, y, i), Math.min(24, r * 0.26));
    }
    const bl = Math.min(170, breite * 0.45);
    loungeBank(x, O + 22, bl, loungeTon(x, y, 2));
    beistell(x + bl / 2 + 26, O + 26);
    stehlampe(li + 22, O + 22, loungeTon(x, y, 4));
    loungePflanze(re - 30, O + 30, "sack");
  },

  // 3 — Coffee bar: a counter at the back with espresso machine, grinder and cups,
  //     four bar stools in front of it, two pendant lamps above. A shelf with cups
  //     at the side, a low table with two poufs and a plant at the front.
  (x, y, breite, tiefe) => {
    const s = Math.min(1, breite / 340, tiefe / 260);
    const O = y - tiefe / 2, U = y + tiefe / 2, li = x - breite / 2, re = x + breite / 2;
    flaeche(x, y, breite - 8, tiefe - 8, blass(loungeTon(x, y, 2)), 0.7);
    const L = Math.max(140, Math.min(breite - 60, 300)), T = 56 * s;
    const ty = O + 8 + T / 2;
    kasten(x, ty, L - 10, T - 8, 5, FARBEN.dunkel, 2);
    kasten(x, ty, L, T, 50, loungeTon(x, y), 4, 5);
    kasten(x, ty - 2, L + 6, T + 8, 4, FARBEN.holz, 3, 55);
    kasten(x - L * 0.3, ty - 8 * s, 40, 24 * s, 22, FARBEN.metall, 4, 59);    // espresso machine
    zylinder(x - L * 0.12, ty - 8 * s, 7, 20, FARBEN.dunkel, 59, 9);          // grinder
    for (let i = 0; i < 3; i++) zylinder(x + L * 0.08 + i * 13, ty - 6 * s, 4, 6, FARBEN.papier, 59);
    zylinder(x + L * 0.36, ty - 6 * s, 11, 4, FARBEN.papier, 59);
    for (const k of [-1, 3]) kugel(x + L * 0.36 + k * 3, ty - 6 * s, 4, k > 1 ? FARBEN.topf : FARBEN.laub, 63);
    for (let i = 0; i < 4; i++) barhocker(x - L / 2 + L * (i + 0.5) / 4, ty + T / 2 + 24, loungeTon(x, y, 2));
    for (const sx of [-1, 1]) {
      zylinder(x + sx * L * 0.22, ty, 0.8, 42, FARBEN.dunkel, 108);
      zylinder(x + sx * L * 0.22, ty, 13, 8, loungeTon(x, y, 4), 100, 4);
    }
    // Shelf with cups, narrow, at the left edge, at the front.
    const rx = li + 22, ry = U - 52;
    kasten(rx, ry, 30, 80, 70, FARBEN.holzDunkel, 2);
    for (let i = 0; i < 3; i++) zylinder(rx, ry - 26 + i * 26, 4, 6, i === 1 ? loungeTon(x, y, 3) : FARBEN.papier, 70);
    // Low table with two poufs, at the front in the middle.
    const vy = y + tiefe * 0.28;
    zylinder(x, vy, 20, 22, FARBEN.holz, 0, 20);
    for (const sx of [-1, 1]) pouf(x + sx * 40, vy, loungeTon(x, y, sx < 0 ? 1 : 3), 15);
    loungePflanze(re - 28, U - 30, "bar");
  },

  // 4 — Phone booths: two or three glass boxes, a metre square, dark frame,
  //     inside a stool and a board, in a row at the back. In front a bench for
  //     those waiting, and a plant.
  (x, y, breite, tiefe) => {
    const O = y - tiefe / 2, U = y + tiefe / 2, li = x - breite / 2, re = x + breite / 2;
    flaeche(x, y, breite - 8, tiefe - 8, blass(loungeTon(x, y, 4)), 0.7);
    const zahl = breite >= 3 * 104 + 40 ? 3 : 2;
    const s = Math.min(1, (breite - 20) / (zahl * 104), (tiefe - 120) / 100);
    const zelle = (cx: number, cy: number, farbe: number) => {
      const a = 100 * s;
      kasten(cx, cy, a, a, 3, FARBEN.dunkel, 2);
      for (const [sx, sy] of [[-1, -1], [1, -1], [-1, 1], [1, 1]]) kasten(cx + sx * (a / 2 - 2), cy + sy * (a / 2 - 2), 5, 5, 86, FARBEN.dunkel, 1, 3);
      kasten(cx, cy, a, a, 3, FARBEN.dunkel, 2, 89);
      const g = kasten(cx, cy, a - 6, a - 6, 84, FARBEN.glas, 1, 4);
      g.material = glasstoff(); g.castShadow = false;
      kasten(cx, cy - a * 0.36, a * 0.7, 16, 3, FARBEN.holz, 2, 50);          // shelf on the back wall
      zylinder(cx, cy + 6, 13, 30, farbe, 3, 11);                            // stool
      kasten(cx + a * 0.34, cy + a / 2 + 1, 3, 12, 3, FARBEN.metall, 1, 44); // door handle
    };
    const zy = O + 6 + 50 * s;
    for (let i = 0; i < zahl; i++) zelle(x + (i - (zahl - 1) / 2) * 104 * s, zy, loungeTon(x, y, i));
    const bl = Math.min(160, breite * 0.42);
    loungeBank(li + 14 + bl / 2, U - 30, bl, loungeTon(x, y, 1));
    beistell(li + 14 + bl + 24, U - 32);
    loungePflanze(re - 30, U - 32, "zelle");
  },

  // 5 — Table football and arcade machine: the table football lengthwise along
  //     the left edge, the handles sticking out on both sides; at the back right
  //     a machine whose screen glows, at the front right a bar table with two stools.
  (x, y, breite, tiefe) => {
    const s = Math.min(1, breite / 340, tiefe / 260);
    const O = y - tiefe / 2, U = y + tiefe / 2, li = x - breite / 2, re = x + breite / 2;
    flaeche(x, y, breite - 8, tiefe - 8, blass(loungeTon(x, y, 3)), 0.7);
    const kx = li + 20 + 62 * s, ky = y;
    kasten(kx, ky, 70 * s, 120 * s, 34, FARBEN.holzDunkel, 3, 8);
    for (const [sx, sy] of [[-1, -1], [1, -1], [-1, 1], [1, 1]]) kasten(kx + sx * 30 * s, ky + sy * 54 * s, 6, 6, 10, FARBEN.dunkel, 1);
    flaeche(kx, ky, 60 * s, 110 * s, 0x3f7a4c, 42.4);
    flaeche(kx, ky, 60 * s, 2, FARBEN.papier, 42.6);
    for (let i = 0; i < 4; i++) {
      const sy = ky + (-40 + i * 27) * s;
      const stange = kasten(kx, sy, 112 * s, 2.5, 2.5, FARBEN.metall, 1, 46);
      stange.castShadow = false;
      kasten(kx + (i % 2 ? 58 : -58) * s, sy, 10, 5, 5, FARBEN.dunkel, 2, 45);
      kasten(kx, sy, 4, 4, 8, i < 2 ? loungeTon(x, y) : loungeTon(x, y, 3), 1, 40);
    }
    // Arcade machine at the back right.
    const ax = re - 20 - 32 * s, ay = O + 10 + 30 * s;
    kasten(ax, ay, 62 * s, 58 * s, 96, FARBEN.dunkel, 4);
    kasten(ax, ay + 26 * s, 60 * s, 12, 10, FARBEN.metall, 2, 50);
    kugel(ax - 12 * s, ay + 28 * s, 3, loungeTon(x, y, 1), 60);
    kugel(ax + 8 * s, ay + 28 * s, 3, loungeTon(x, y, 4), 60);
    const schirm = kasten(ax, ay + 28 * s, 48 * s, 3, 30, FARBEN.schirmAn, 2, 64);
    schirm.material = leuchtstoff(loungeTon(x, y, 3));
    kasten(ax, ay, 64 * s, 60 * s, 10, loungeTon(x, y, 2), 3, 96);
    // Bar table with two bar stools at the front right.
    const hx = re - 20 - 60 * s, hy = U - 36;
    zylinder(hx, hy, 16, 2, FARBEN.metall);
    zylinder(hx, hy, 3, 56, FARBEN.metall, 2);
    zylinder(hx, hy, 22, 4, FARBEN.holz, 58);
    for (const sx of [-1, 1]) barhocker(hx + sx * 40 * s, hy, loungeTon(x, y, 4));
    loungePflanze(x + breite * 0.05, O + 30, "kick");
  },

  // 6 — Reading corner: a shelf full of books at the back, in front of it a
  //     hanging chair on its arc, a pouf, a floor lamp and two large plants on a rug.
  (x, y, breite, tiefe) => {
    const O = y - tiefe / 2, U = y + tiefe / 2, li = x - breite / 2, re = x + breite / 2;
    flaeche(x, y, breite - 8, tiefe - 8, blass(loungeTon(x, y, 1)), 0.7);
    const rb = Math.max(110, Math.min(breite - 130, 240)), ry = O + 20;
    kasten(x, ry, rb, 32, 104, FARBEN.holz, 3);
    const toene = [FARBEN.papier, loungeTon(x, y), FARBEN.holzDunkel, loungeTon(x, y, 2), FARBEN.stoff, loungeTon(x, y, 4)];
    for (let f = 0; f < 3; f++) {
      kasten(x, ry + 2, rb - 8, 30, 2, FARBEN.holzDunkel, 1, 30 + f * 32);
      kasten(x - rb * 0.05, ry + 6, rb - 24, 18, 22, toene[(f * 2 + hash(`${Math.round(x)}b${f}`)) % toene.length], 1, 32 + f * 32);
    }
    // Hanging chair: heavy foot, mast, boom, chain, a basket like half an egg.
    const hx = x - breite * 0.15, hy = y + tiefe * 0.12;
    zylinder(hx, hy + 30, 22, 4, FARBEN.dunkel);
    zylinder(hx, hy + 30, 3.5, 118, FARBEN.metall, 4);
    kasten(hx, hy + 15, 5, 32, 4, FARBEN.metall, 1, 120);
    zylinder(hx, hy, 0.8, 50, FARBEN.metall, 72);
    kugel(hx, hy, 30, FARBEN.papier, 22, 0.8);
    kugel(hx, hy + 4, 22, loungeTon(x, y, 1), 32, 0.5);
    pouf(hx + 58, hy + 12, loungeTon(x, y, 3));
    stehlampe(x + breite * 0.3, y + tiefe * 0.1, loungeTon(x, y, 4));
    loungePflanze(li + 30, O + 30, "lese");
    loungePflanze(re - 30, U - 30, "lese2");
  },

  // 7 — Arriving: a round rug with a low table and six poufs, at the edge a
  //     bike rack with two bikes. Whoever comes by bike parks it inside — and
  //     the room says that this is allowed here.
  (x, y, breite, tiefe) => {
    const O = y - tiefe / 2, li = x - breite / 2, re = x + breite / 2;
    flaeche(x, y, breite - 8, tiefe - 8, blass(loungeTon(x, y, 2)), 0.7);
    const cx = x - 40, cy = y + tiefe * 0.06;
    const r = Math.max(60, Math.min(tiefe / 2 - 24, (breite - 90) / 2 - 10));
    zylinder(cx, cy, r, 0.6, loungeTon(x, y, 2), 0.8);
    zylinder(cx, cy, 26, 22, FARBEN.holz, 0, 24);
    kasten(cx - 8, cy - 5, 20, 14, 3, FARBEN.papier, 1, 22);
    zylinder(cx + 10, cy + 6, 4, 8, loungeTon(x, y), 22);
    for (let i = 0; i < 6; i++) {
      const w = (i / 6) * Math.PI * 2 + 0.3;
      pouf(cx + Math.cos(w) * r * 0.72, cy + Math.sin(w) * r * 0.72, loungeTon(x, y, i), Math.min(16, r * 0.2));
    }
    // Bike rack at the right edge: a rail, two bikes lengthwise.
    const bx = re - 44, by = y;
    kasten(bx, by + 26, 70, 8, 12, FARBEN.metall, 2);
    fahrrad(bx - 16, by, loungeTon(x, y, 1));
    fahrrad(bx + 16, by, loungeTon(x, y, 4));
    loungePflanze(li + 30, O + 30, "an");
  },
];

/* No kind by key: all lounges have the same name, they differ by their number
   in the house, and the islands of one lounge by their row. */
export function loungeBauen(z: { nr: number; x: number; y: number; b: number; t: number }, variante: number): void {
  const art = (hash("lounge") + variante * 3 + z.nr) % LOUNGES.length;
  LOUNGES[art](z.x, z.y, z.b, z.t);
}
