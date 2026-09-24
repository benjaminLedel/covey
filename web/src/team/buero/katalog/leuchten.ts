import { FARBEN, akzent, anhaengen, flaeche, kasten, kugel, zylinder, punktlicht } from "../mal";

/** A lamp at (x, y); `an` says whether it is lit — only then does it carry light. */
export type Leuchte = (x: number, y: number, an: boolean) => void;

/* Lamps and small fittings: everything that lights or orders a room without
 * being a workplace itself. There are several kinds, because an open-plan
 * office is lit differently from a corner — ceiling for the area, floor and
 * desk lamp for the single seat. When lit, every lamp additionally carries
 * light; the last two entries are fittings and are not affected by it. */
export const LEUCHTEN: Leuchte[] = [
  // Flat ceiling lamp — a square panel flush under the ceiling, readable from above as a bright box.
  (x, y, an) => {
    kasten(x, y, 62, 62, 5, FARBEN.metall, 4, 145);
    kasten(x, y, 52, 52, 2, an ? FARBEN.schirmAn : FARBEN.schirm, 3, 143);
    if (an) {
      const licht = punktlicht(FARBEN.schirmAn, 0.85, 320);
      licht.position.set(x, 138, y);
      anhaengen(licht);
    }
  },

  // Pendant lamp — cable plus conical shade, hangs low enough to take in a table.
  (x, y, an) => {
    zylinder(x, y, 0.9, 42, FARBEN.dunkel, 108);
    zylinder(x, y, 17, 15, akzent(), 95, 5);
    kugel(x, y, 14, an ? FARBEN.schirmAn : FARBEN.papier, 94, 0.22);
    if (an) {
      const licht = punktlicht(FARBEN.schirmAn, 0.8, 200);
      licht.position.set(x, 90, y);
      anhaengen(licht);
    }
  },

  // Floor lamp — heavy base, thin shaft, drum shade just above head height;
  // it stays below the ceiling lamps so the two do not intersect.
  (x, y, an) => {
    zylinder(x, y, 15, 2.5, FARBEN.metall, 0);
    zylinder(x, y, 1.8, 84, FARBEN.metall, 2.5);
    zylinder(x, y, 19, 24, akzent(), 86, 15);
    kugel(x, y, 16, an ? FARBEN.schirmAn : FARBEN.papier, 85, 0.18);
    if (an) {
      const licht = punktlicht(FARBEN.schirmAn, 0.7, 170);
      licht.position.set(x, 80, y);
      anhaengen(licht);
    }
  },

  // Desk lamp — stands on the desktop, the arm reaches forward, the head points down.
  (x, y, an) => {
    zylinder(x, y, 7, 1.5, FARBEN.metall, 44);
    zylinder(x, y, 1.3, 26, FARBEN.metall, 45.5);
    kasten(x, y + 7, 4, 18, 3, FARBEN.metall, 2, 70);
    zylinder(x, y + 14, 6, 8, FARBEN.schirm, 62, 3);
    kugel(x, y + 14, 5, an ? FARBEN.schirmAn : FARBEN.papier, 61, 0.16);
    if (an) {
      const licht = punktlicht(FARBEN.schirmAn, 0.45, 90);
      licht.position.set(x, 58, y + 14);
      anhaengen(licht);
    }
  },

  // Light strip — a continuous line on the ceiling that traces a corridor lengthwise.
  // Two sources, because a single point source leaves the ends of the 180 cm dark.
  (x, y, an) => {
    kasten(x, y, 13, 180, 5, FARBEN.metall, 2, 145);
    kasten(x, y, 8, 172, 2, an ? FARBEN.schirmAn : FARBEN.schirm, 2, 143);
    if (an) {
      const vorn = punktlicht(FARBEN.schirmAn, 0.5, 220);
      vorn.position.set(x, 138, y - 55);
      anhaengen(vorn);
      const hinten = punktlicht(FARBEN.schirmAn, 0.5, 220);
      hinten.position.set(x, 138, y + 55);
      anhaengen(hinten);
    }
  },

  // Wall lamp — a short bracket with a flat hood, sits above head height on a wall.
  (x, y, an) => {
    kasten(x, y, 10, 5, 20, FARBEN.metall, 2, 102);
    kugel(x, y + 6, 9, FARBEN.schirm, 110, 0.6);
    kugel(x, y + 6, 7, an ? FARBEN.schirmAn : FARBEN.papier, 108, 0.14);
    if (an) {
      const licht = punktlicht(FARBEN.schirmAn, 0.4, 120);
      licht.position.set(x, 112, y + 8);
      anhaengen(licht);
    }
  },

  // Floor cable tray — two rounded channels on a strip, so cables have a visible
  // track instead of running across the room. The clamps sit on top of the
  // channels, like a clip. Carries no light and knows no night.
  (x, y) => {
    flaeche(x, y, 30, 160, FARBEN.flur, 0.4);
    kasten(x - 6, y, 9, 152, 4, FARBEN.dunkel, 4, 0.6);
    kasten(x + 6, y, 9, 152, 4, FARBEN.dunkel, 4, 0.6);
    kasten(x, y - 50, 26, 5, 2, FARBEN.metall, 2, 4.6);
    kasten(x, y, 26, 5, 2, FARBEN.metall, 2, 4.6);
    kasten(x, y + 50, 26, 5, 2, FARBEN.metall, 2, 4.6);
  },

  // Glass partition — frosted glass between two feet, divides the room without
  // darkening it; the band at seat height is the marking. Carries no light and
  // knows no night.
  (x, y) => {
    kasten(x, y - 46, 16, 12, 4, FARBEN.metall, 2, 0);
    kasten(x, y + 46, 16, 12, 4, FARBEN.metall, 2, 0);
    kasten(x, y - 48, 5, 5, 116, FARBEN.metall, 2, 4);
    kasten(x, y + 48, 5, 5, 116, FARBEN.metall, 2, 4);
    kasten(x, y, 4, 96, 112, FARBEN.bodenHell, 2, 5);
    kasten(x, y, 6, 96, 9, FARBEN.papier, 2, 62);
  },
];

/* And a selection. The catalogue knows eight lamps, four of which hang from a
 * ceiling — which this house does not have, since you look into it from above.
 * A pendant lamp over an open room would hang from nothing. So only the ones
 * with a floor or a wall beneath them are listed here; the last two entries
 * are fittings anyway and carry no light at all. */
export const STEHLEUCHTEN: Leuchte[] = [LEUCHTEN[2]];
export const WANDLEUCHTEN: Leuchte[] = [LEUCHTEN[5]];
export const KLEINKRAM: Leuchte[] = [LEUCHTEN[6], LEUCHTEN[7]];
