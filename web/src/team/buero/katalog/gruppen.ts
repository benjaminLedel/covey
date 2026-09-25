import { FARBEN, akzent, flaeche, kasten, kugel, zylinder, punktlicht } from "../mal";

/** A catalogue piece that fills an area: centre (x, y), extent breite × tiefe. */
export type Flaechenstueck = (x: number, y: number, breite: number, tiefe: number) => void;

/* Seating groups: a rug plus furniture that means staying, not working. They
 * fill the areas where no workplace stands and give the room a second tone.
 * Eight kinds, because eight identical sofas make a waiting hall, not an
 * office. The factor m only shrinks the footprint; seat height 30 and table
 * height 44 stay, otherwise the scale tips over. */
export const GRUPPEN: Flaechenstueck[] = [

  // 1 — Sofa with side table
  (x, y, breite, tiefe) => {
    const m = Math.min(breite / 260, tiefe / 200, 1);
    flaeche(x, y, 240 * m, 170 * m, FARBEN.teppich, 0.4);
    // Three-seater along the back edge: one seat block, one continuous backrest, two arms.
    const sx = x - 26 * m, sy = y - 42 * m;
    kasten(sx, sy, 190 * m, 78 * m, 26, akzent(), 8);
    kasten(sx, sy - 34 * m, 190 * m, 16, 48, akzent(), 4, 26);
    kasten(sx - 88 * m, sy + 4 * m, 14, 70 * m, 18, akzent(), 4, 26);
    kasten(sx + 88 * m, sy + 4 * m, 14, 70 * m, 18, akzent(), 4, 26);
    kasten(sx - 44 * m, sy + 6 * m, 66 * m, 56 * m, 9, akzent(1), 3, 26);
    kasten(sx + 44 * m, sy + 6 * m, 66 * m, 56 * m, 9, FARBEN.weiss, 3, 26);
    // Side table on a single foot; it stands just beside the armrest, not inside it.
    const bx = x + 96 * m, by = y - 14 * m;
    zylinder(bx, by, 16 * m, 2, FARBEN.metall);
    zylinder(bx, by, 4, 36, FARBEN.metall, 2);
    zylinder(bx, by, 24 * m, 4, FARBEN.holz, 38);
    zylinder(bx, by, 6, 9, FARBEN.papier, 42);
  },

  // 2 — Two armchairs with a coffee table
  (x, y, breite, tiefe) => {
    const m = Math.min(breite / 240, tiefe / 220, 1);
    flaeche(x, y, 215 * m, 200 * m, FARBEN.bodenHell, 0.4);
    // Two single armchairs facing each other: backrests point outwards, the table sits between.
    const sessel = (cx: number, cy: number, richtung: number, kissen: number) => {
      kasten(cx, cy, 68 * m, 62 * m, 26, akzent(), 8);
      kasten(cx, cy + 27 * m * richtung, 68 * m, 14, 42, akzent(), 4, 26);
      kasten(cx - 29 * m, cy - 4 * m * richtung, 12, 50 * m, 15, akzent(), 4, 26);
      kasten(cx + 29 * m, cy - 4 * m * richtung, 12, 50 * m, 15, akzent(), 4, 26);
      // The cushion stays narrower than the seat so it does not stick into the armrests.
      kasten(cx, cy - 3 * m * richtung, 42 * m, 46 * m, 9, kissen, 3, 26);
    };
    sessel(x - 4 * m, y - 68 * m, -1, akzent(1));
    sessel(x - 4 * m, y + 68 * m, 1, akzent(2));
    // Low coffee table on four legs: flat enough to look over.
    kasten(x, y, 96 * m, 54 * m, 5, FARBEN.holz, 2, 33);
    kasten(x, y, 82 * m, 40 * m, 5, FARBEN.holzDunkel, 2, 17);
    zylinder(x - 42 * m, y - 21 * m, 3, 33, FARBEN.holzDunkel);
    zylinder(x + 42 * m, y - 21 * m, 3, 33, FARBEN.holzDunkel);
    zylinder(x - 42 * m, y + 21 * m, 3, 33, FARBEN.holzDunkel);
    zylinder(x + 42 * m, y + 21 * m, 3, 33, FARBEN.holzDunkel);
    kasten(x + 22 * m, y + 2 * m, 20 * m, 15 * m, 4, FARBEN.papier, 2, 38);
  },

  // 3 — Benches at a table
  (x, y, breite, tiefe) => {
    const m = Math.min(breite / 280, tiefe / 200, 1);
    flaeche(x, y, 258 * m, 178 * m, FARBEN.gemein, 0.4);
    // Long table on two trestles with a stretcher, the canteen measure: many people, short time.
    // The stretcher reaches into both trestles, otherwise it would hang in the air between them.
    kasten(x, y, 176 * m, 74 * m, 5, FARBEN.holz, 2, 39);
    kasten(x - 72 * m, y, 6, 62 * m, 39, FARBEN.holzDunkel, 2);
    kasten(x + 72 * m, y, 6, 62 * m, 39, FARBEN.holzDunkel, 2);
    kasten(x, y, 146 * m, 6, 6, FARBEN.holzDunkel, 2, 15);
    // Two benches without backrest, one on each side — you sit down from anywhere and get up quickly.
    const bank = (by: number) => {
      kasten(x, by, 176 * m, 30 * m, 6, FARBEN.holz, 2, 24);
      kasten(x - 66 * m, by, 5, 26 * m, 24, FARBEN.holzDunkel, 2);
      kasten(x + 66 * m, by, 5, 26 * m, 24, FARBEN.holzDunkel, 2);
    };
    bank(y - 60 * m);
    bank(y + 60 * m);
    zylinder(x - 40 * m, y, 9, 11, FARBEN.topf, 44);
    kugel(x - 40 * m, y, 11, FARBEN.laub, 52, 0.8);
    kasten(x + 42 * m, y - 6 * m, 22 * m, 16 * m, 4, FARBEN.papier, 2, 44);
  },

  // 4 — Bean bags
  (x, y, breite, tiefe) => {
    const m = Math.min(breite / 220, tiefe / 200, 1);
    // Round rug as a flat disc: the loose corner does without edges.
    zylinder(x, y, 100 * m, 1, FARBEN.teppich, 0.4);
    // A bag is a squashed sphere as the seat with a smaller bulge behind it as the back.
    const sack = (sx: number, sy: number, r: number, farbe: number, hinten: number) => {
      kugel(sx, sy, r * m, farbe, 0, 0.62);
      kugel(sx, sy + hinten * m, r * 0.66 * m, farbe, 6, 0.85);
    };
    // The three bags stand in a triangle around the tray. The spacing is larger than the
    // sum of the radii, because both reach down to the floor and would otherwise intersect.
    sack(x - 58 * m, y - 21 * m, 34, akzent(), -20);
    sack(x + 54 * m, y - 31 * m, 30, akzent(1), -18);
    sack(x + 10 * m, y + 57 * m, 32, akzent(2), 19);
    // Conical drum in the middle: meant as a tray, hence narrower than a seat.
    zylinder(x, y, 17 * m, 24, FARBEN.holzDunkel, 0, 20 * m);
    zylinder(x, y, 23 * m, 3, FARBEN.holz, 24);
    zylinder(x, y, 5, 8, FARBEN.papier, 27);
  },

  // 5 — Corner sofa
  (x, y, breite, tiefe) => {
    const m = Math.min(breite / 260, tiefe / 240, 1);
    flaeche(x, y, 244 * m, 222 * m, FARBEN.teppich, 0.4);
    // L shape: the long leg lies along the back edge, the short one bends forward.
    // Both seat blocks end on the same edge on the left, otherwise the corner has a step.
    kasten(x + 12 * m, y - 58 * m, 176 * m, 76 * m, 26, akzent(), 8);
    kasten(x - 38 * m, y + 26 * m, 76 * m, 92 * m, 26, akzent(), 8);
    // The backrests stand on the rear seat edge and meet in the corner;
    // neither sticks out beyond a seat block, otherwise it would float.
    kasten(x + 20 * m, y - 88 * m, 160 * m, 16, 46, akzent(), 4, 26);
    kasten(x - 68 * m, y - 12 * m, 16, 168 * m, 46, akzent(), 4, 26);
    // The arms close the open ends of both legs.
    kasten(x + 92 * m, y - 50 * m, 16, 60 * m, 16, akzent(), 4, 26);
    kasten(x - 30 * m, y + 64 * m, 60 * m, 16, 16, akzent(), 4, 26);
    kasten(x - 24 * m, y - 50 * m, 66 * m, 54 * m, 9, akzent(1), 3, 26);
    kasten(x + 48 * m, y - 50 * m, 66 * m, 54 * m, 9, FARBEN.weiss, 3, 26);
    kasten(x - 30 * m, y + 16 * m, 56 * m, 56 * m, 9, akzent(1), 3, 26);
    // Square table inside the angle of the corner, reachable from both legs.
    kasten(x + 36 * m, y + 30 * m, 62 * m, 62 * m, 5, FARBEN.holz, 2, 34);
    zylinder(x + 9 * m, y + 3 * m, 3, 34, FARBEN.metall);
    zylinder(x + 63 * m, y + 3 * m, 3, 34, FARBEN.metall);
    zylinder(x + 9 * m, y + 57 * m, 3, 34, FARBEN.metall);
    zylinder(x + 63 * m, y + 57 * m, 3, 34, FARBEN.metall);
  },

  // 6 — Two stools and a round table
  (x, y, breite, tiefe) => {
    const m = Math.min(breite / 180, tiefe / 180, 1);
    zylinder(x, y, 84 * m, 1, akzent(1), 0.4);
    // Bar-table silhouette at seat scale: heavy disc foot, thin column, round top.
    // The top ends at 44, the cushions at 30 — the measure of the house.
    zylinder(x, y, 26 * m, 3, FARBEN.metall);
    zylinder(x, y, 5, 37, FARBEN.metall, 3);
    zylinder(x, y, 34 * m, 4, FARBEN.holz, 40);
    zylinder(x - 12 * m, y - 6 * m, 4, 9, FARBEN.papier, 44);
    zylinder(x + 10 * m, y + 8 * m, 4, 9, FARBEN.papier, 44);
    // Two stools: a padded disc on a drum, no back — for short conversations.
    const hocker = (hx: number, hy: number) => {
      zylinder(hx, hy, 15 * m, 24, FARBEN.holzDunkel, 0, 17 * m);
      zylinder(hx, hy, 19 * m, 6, akzent(), 24);
    };
    hocker(x - 58 * m, y + 14 * m);
    hocker(x + 58 * m, y - 14 * m);
  },

  // 7 — Reading chair with floor lamp
  (x, y, breite, tiefe) => {
    const m = Math.min(breite / 200, tiefe / 200, 1);
    flaeche(x, y, 180 * m, 170 * m, FARBEN.teppich, 0.4);
    // Wing chair: high back, side wings, a footstool in front — a place for one person.
    // Wing and armrest meet, the cushion stays between the armrests.
    const sx = x - 14 * m, sy = y - 6 * m;
    kasten(sx, sy, 76 * m, 70 * m, 28, akzent(), 10);
    kasten(sx, sy - 30 * m, 76 * m, 16, 62, akzent(), 4, 28);
    kasten(sx - 32 * m, sy - 15 * m, 14, 22 * m, 40, akzent(), 4, 28);
    kasten(sx + 32 * m, sy - 15 * m, 14, 22 * m, 40, akzent(), 4, 28);
    kasten(sx - 32 * m, sy + 15 * m, 14, 38 * m, 18, akzent(), 4, 28);
    kasten(sx + 32 * m, sy + 15 * m, 14, 38 * m, 18, akzent(), 4, 28);
    kasten(sx, sy + 6 * m, 46 * m, 48 * m, 9, akzent(1), 3, 28);
    kasten(sx, sy + 58 * m, 52 * m, 32 * m, 26, akzent(), 6);
    kasten(sx + 4 * m, sy + 58 * m, 22 * m, 16 * m, 5, FARBEN.papier, 2, 26);
    // Floor lamp behind the backrest; the light sits in the shade and therefore hangs from the shade.
    const lx = x + 62 * m, ly = y - 46 * m;
    zylinder(lx, ly, 14 * m, 2, FARBEN.metall);
    zylinder(lx, ly, 3, 112, FARBEN.metall, 2);
    const schirm = zylinder(lx, ly, 19 * m, 26, FARBEN.schirm, 110, 13 * m);
    schirm.add(punktlicht(FARBEN.schirmAn, 0.5, 170));
  },

  // 8 — Platform with cushions
  (x, y, breite, tiefe) => {
    const m = Math.min(breite / 240, tiefe / 200, 1);
    flaeche(x, y, 230 * m, 186 * m, FARBEN.bodenHell, 0.4);
    // A platform you can step on instead of furniture: recessed base, the top above it.
    kasten(x - 14 * m, y, 156 * m, 100 * m, 6, FARBEN.holzDunkel, 2);
    kasten(x - 14 * m, y, 170 * m, 112 * m, 18, FARBEN.holz, 6, 6);
    // Cushions as loose seats, behind them a continuous bolster as the backrest.
    kasten(x - 14 * m, y - 46 * m, 148 * m, 16, 32, akzent(), 4, 24);
    kasten(x - 62 * m, y - 8 * m, 46 * m, 44 * m, 13, akzent(1), 4, 24);
    kasten(x - 10 * m, y - 2 * m, 42 * m, 42 * m, 12, FARBEN.weiss, 4, 24);
    zylinder(x + 42 * m, y + 22 * m, 24 * m, 11, akzent(2), 24);
    kasten(x - 48 * m, y + 34 * m, 24 * m, 18 * m, 4, FARBEN.papier, 2, 24);
    // A block beside the platform at table height: the shelf the platform itself lacks.
    // The plant stands in a pot, otherwise it reads as a ball.
    kasten(x + 96 * m, y + 44 * m, 34 * m, 34 * m, 40, FARBEN.holzDunkel, 4);
    zylinder(x + 96 * m, y + 44 * m, 9, 11, FARBEN.topf, 40);
    kugel(x + 96 * m, y + 44 * m, 11, FARBEN.laub, 48, 0.9);
  },
];
