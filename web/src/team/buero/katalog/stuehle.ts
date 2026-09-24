import { akzent, FARBEN, flaeche, kasten, kugel, zylinder } from "../mal";

/* Part of the furniture catalogue (see the module comment in pflanzen.ts):
 * every piece is a pure function drawing at (x, y). */

export type Stuhl = (x: number, y: number) => void;

// Chairs: the seat at the workplace and in the shared areas. There are several
// kinds, because an office does not consist of one catalogue item — the desk
// gets a swivel chair, the meeting table wooden chairs, the kitchenette stools.
// All variants sit at a seat height of 30 cm and face forward (+y): the back
// always stands on the rear side at -y.
export const STUEHLE: Stuhl[] = [

  // Office swivel chair with a cross base — gas spring, upholstered back. The castors
  // sit at the ends of the arms so that, seen obliquely from above, they stand beside the cross.
  (x, y) => {
    kasten(x, y, 44, 8, 4, FARBEN.dunkel, 2, 0);
    kasten(x, y, 8, 44, 4, FARBEN.dunkel, 2, 0);
    kugel(x - 24, y, 2.5, FARBEN.metall, 0, 1);
    kugel(x + 24, y, 2.5, FARBEN.metall, 0, 1);
    kugel(x, y - 24, 2.5, FARBEN.metall, 0, 1);
    kugel(x, y + 24, 2.5, FARBEN.metall, 0, 1);
    zylinder(x, y, 4.5, 7, FARBEN.dunkel, 4);
    zylinder(x, y, 2.5, 15, FARBEN.metall, 10);
    kasten(x, y + 1, 42, 40, 5, akzent(), 8, 25);
    kasten(x, y - 16, 38, 6, 30, akzent(), 2, 30);
  },

  // Cantilever chair — one continuous tubular frame: runner, front support, seat,
  // and behind the seat the same frame rises to the back. No rear leg.
  (x, y) => {
    kasten(x - 18, y, 4, 44, 4, FARBEN.metall, 2, 0);
    kasten(x + 18, y, 4, 44, 4, FARBEN.metall, 2, 0);
    kasten(x, y + 20, 40, 4, 4, FARBEN.metall, 2, 0);
    kasten(x - 18, y + 19, 4, 4, 26, FARBEN.metall, 2, 4);
    kasten(x + 18, y + 19, 4, 4, 26, FARBEN.metall, 2, 4);
    kasten(x, y, 40, 40, 4, FARBEN.holz, 6, 26);
    kasten(x - 17, y - 21, 4, 4, 34, FARBEN.metall, 2, 26);
    kasten(x + 17, y - 21, 4, 4, 34, FARBEN.metall, 2, 26);
    kasten(x, y - 21, 32, 5, 22, FARBEN.holz, 2, 36);
  },

  // Stool — a round seat on three legs so it stands without tipping on any floor;
  // the shelf at half height braces the legs and carries the feet.
  (x, y) => {
    for (let i = 0; i < 3; i++) {
      const winkel = (i / 3) * Math.PI * 2;
      zylinder(x + Math.cos(winkel) * 13, y + Math.sin(winkel) * 13, 2, 26, FARBEN.holzDunkel, 0);
    }
    zylinder(x, y, 14, 3, FARBEN.holzDunkel, 14);
    zylinder(x, y, 17, 4, FARBEN.holz, 26);
  },

  // Wooden chair with four legs — meeting chair; the back consists of two cross
  // rails set between the raised rear posts.
  (x, y) => {
    const bein = (bx: number, by: number) => kasten(bx, by, 4, 4, 27, FARBEN.holzDunkel, 2, 0);
    bein(x - 16, y - 16);
    bein(x + 16, y - 16);
    bein(x - 16, y + 16);
    bein(x + 16, y + 16);
    kasten(x, y, 38, 38, 4, FARBEN.holz, 4, 26);
    kasten(x - 16, y - 16, 4, 4, 28, FARBEN.holzDunkel, 2, 30);
    kasten(x + 16, y - 16, 4, 4, 28, FARBEN.holzDunkel, 2, 30);
    kasten(x, y - 16, 30, 4, 5, FARBEN.holz, 2, 40);
    kasten(x, y - 16, 30, 4, 5, FARBEN.holz, 2, 51);
  },

  // Mesh chair with a high back — a castor plate as the foot, the back surface spans
  // between two frame bars, the headrest sits above.
  (x, y) => {
    zylinder(x, y, 17, 3, FARBEN.dunkel, 0);
    for (let i = 0; i < 5; i++) {
      const winkel = (i / 5) * Math.PI * 2;
      kugel(x + Math.cos(winkel) * 19, y + Math.sin(winkel) * 19, 2.5, FARBEN.metall, 0, 1);
    }
    zylinder(x, y, 2.5, 22, FARBEN.metall, 3);
    kasten(x, y + 1, 44, 42, 5, FARBEN.dunkel, 8, 25);
    kasten(x - 19, y - 19, 4, 4, 52, FARBEN.dunkel, 2, 30);
    kasten(x + 19, y - 19, 4, 4, 52, FARBEN.dunkel, 2, 30);
    kasten(x, y - 19, 36, 4, 46, akzent(), 2, 31);
    kasten(x, y - 19, 26, 5, 8, FARBEN.dunkel, 2, 80);
  },

  // Exercise ball — no frame, just ball and mat; stands in the shared area.
  (x, y) => {
    flaeche(x, y, 42, 42, FARBEN.teppich, 0.5);
    kugel(x, y, 17, akzent(), 0, 0.9);
  },

  // Saddle stool — a split seat and a foot plate, for short sitting at a standing desk.
  (x, y) => {
    zylinder(x, y, 17, 3, FARBEN.dunkel, 0);
    zylinder(x, y, 2.5, 21, FARBEN.metall, 3);
    zylinder(x, y, 11, 2, FARBEN.metall, 13);
    kasten(x - 10, y, 18, 34, 6, akzent(), 8, 24);
    kasten(x + 10, y, 18, 34, 6, akzent(), 8, 24);
    kasten(x, y, 6, 30, 4, akzent(), 2, 24);
  },

  // Armchair — a voluminous body on short wooden feet, lounge corner.
  // Back and armrests close flush with the body, the cushion lies in front.
  (x, y) => {
    const fuss = (fx: number, fy: number) => kasten(fx, fy, 5, 5, 6, FARBEN.holzDunkel, 2, 0);
    fuss(x - 22, y - 18);
    fuss(x + 22, y - 18);
    fuss(x - 22, y + 18);
    fuss(x + 22, y + 18);
    kasten(x, y, 56, 50, 18, akzent(), 10, 6);
    kasten(x, y + 5, 42, 38, 6, akzent(), 12, 24);
    kasten(x, y - 19, 52, 12, 34, akzent(), 5, 24);
    kasten(x - 24, y + 4, 8, 40, 14, akzent(), 3, 24);
    kasten(x + 24, y + 4, 8, 40, 14, akzent(), 3, 24);
  },

  // Bench — two panel ends, two slats, one cushion; no back and twice as wide as a
  // chair, so that from above it appears as a bar and not as a square.
  (x, y) => {
    kasten(x - 40, y, 6, 30, 24, FARBEN.holzDunkel, 2, 0);
    kasten(x + 40, y, 6, 30, 24, FARBEN.holzDunkel, 2, 0);
    kasten(x, y - 8.5, 96, 15, 4, FARBEN.holz, 2, 24);
    kasten(x, y + 8.5, 96, 15, 4, FARBEN.holz, 2, 24);
    kasten(x - 18, y, 50, 28, 4, akzent(), 6, 28);
  },

  // Kneeling chair — two runners, the seat at the back tilted forward, the knee pad low in front.
  (x, y) => {
    kasten(x - 16, y, 4, 60, 5, FARBEN.holz, 2, 0);
    kasten(x + 16, y, 4, 60, 5, FARBEN.holz, 2, 0);
    kasten(x, y - 10, 36, 5, 5, FARBEN.holz, 2, 3);
    kasten(x, y - 10, 5, 5, 22, FARBEN.holz, 2, 5);
    kasten(x, y + 18, 5, 5, 10, FARBEN.holz, 2, 5);
    // The tilt is the whole point of this chair; flat, it would be a stool with a footrest.
    kasten(x, y - 10, 40, 30, 5, akzent(), 8, 27).rotation.x = 0.25;
    kasten(x, y + 18, 38, 18, 5, akzent(), 6, 14);
  },

  // Shell chair — a round seat, the back as an arc of padded rolls on the rear side.
  (x, y) => {
    for (let i = 0; i < 4; i++) {
      const winkel = (i / 4) * Math.PI * 2 + Math.PI / 4;
      zylinder(x + Math.cos(winkel) * 15, y + Math.sin(winkel) * 15, 1.8, 20, FARBEN.holzDunkel, 0);
    }
    zylinder(x, y, 23, 8, FARBEN.gemein, 18);
    // The arc runs over the rear half (-y); the seat stays open at the front.
    for (let i = 0; i < 5; i++) {
      const winkel = Math.PI + (i / 4) * Math.PI;
      zylinder(x + Math.cos(winkel) * 20, y + Math.sin(winkel) * 20, 6, 18, FARBEN.gemein, 26);
    }
    kugel(x, y + 2, 16, akzent(), 25, 0.3);
  },

  // Beanbag — no frame, a flat mass and a roll as the back.
  (x, y) => {
    kugel(x, y + 4, 26, akzent(), 0, 0.5);
    kugel(x, y - 14, 20, akzent(), 6, 0.9);
    kugel(x, y + 6, 13, akzent(1), 18, 0.35);
  },

  // Stacking chair with armrests — a coloured shell on thin tubular legs.
  (x, y) => {
    zylinder(x - 16, y - 15, 1.6, 26, FARBEN.metall);
    zylinder(x + 16, y - 15, 1.6, 26, FARBEN.metall);
    zylinder(x - 16, y + 15, 1.6, 26, FARBEN.metall);
    zylinder(x + 16, y + 15, 1.6, 26, FARBEN.metall);
    kasten(x, y, 40, 38, 4, akzent(), 10, 26);
    kasten(x, y - 19, 38, 4, 24, akzent(), 3, 30);
    // The armrests have their own supports — the thin shell alone would not hold them.
    kasten(x - 20, y, 3, 30, 3, FARBEN.metall, 1, 40);
    kasten(x + 20, y, 3, 30, 3, FARBEN.metall, 1, 40);
    kasten(x - 20, y + 14, 3, 3, 14, FARBEN.metall, 1, 26);
    kasten(x + 20, y + 14, 3, 3, 14, FARBEN.metall, 1, 26);
  },

  // Spindle-back chair — a curved row of thin spindles carrying a head rail on top.
  (x, y) => {
    zylinder(x - 15, y - 13, 2, 26, FARBEN.holzDunkel);
    zylinder(x + 15, y - 13, 2, 26, FARBEN.holzDunkel);
    zylinder(x - 15, y + 15, 2, 26, FARBEN.holzDunkel);
    zylinder(x + 15, y + 15, 2, 26, FARBEN.holzDunkel);
    kasten(x, y, 40, 38, 4, FARBEN.holz, 12, 26);
    // The spindles stand on a shallow arc; from above it is this row of dots that
    // tells the chair apart from the wooden chair with cross rails.
    for (let i = 0; i < 5; i++) {
      const sx = x - 14 + i * 7;
      zylinder(sx, y - 15 + Math.abs(i - 2) * 1.5, 1.2, 26, FARBEN.holzDunkel, 30);
    }
    kasten(x, y - 14, 38, 5, 4, FARBEN.holz, 2, 56);
  },

  // Executive chair — a wide upholstered body with armrests and a high back on a five-star base.
  (x, y) => {
    for (let i = 0; i < 5; i++) {
      const winkel = (i / 5) * Math.PI * 2 + Math.PI / 2;
      kasten(x + Math.cos(winkel) * 12, y + Math.sin(winkel) * 12, 7, 7, 3, FARBEN.dunkel, 2, 0);
      kugel(x + Math.cos(winkel) * 24, y + Math.sin(winkel) * 24, 2.5, FARBEN.metall, 0, 1);
    }
    zylinder(x, y, 3, 20, FARBEN.metall, 3);
    kasten(x, y + 2, 50, 46, 8, FARBEN.dunkel, 10, 22);
    kasten(x - 24, y + 2, 7, 38, 10, FARBEN.dunkel, 3, 30);
    kasten(x + 24, y + 2, 7, 38, 10, FARBEN.dunkel, 3, 30);
    kasten(x, y - 20, 46, 9, 50, FARBEN.dunkel, 6, 30);
  },
];
