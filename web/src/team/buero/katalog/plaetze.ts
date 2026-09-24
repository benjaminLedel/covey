import type * as THREE from "three";
import { akzent, FARBEN, flaeche, kasten, kugel, zylinder } from "../mal";
import type { Punkt } from "../typen";

/* Part of the furniture catalogue (see the module comment in pflanzen.ts):
 * every piece is a pure function drawing at a point. */

export type Platz = (p: Punkt, an: boolean) => THREE.Mesh[];

// Workplaces: the desk and everything on it. There are several kinds, because an
// office does not consist of a single catalogue item — build, outline and material
// keep the places apart, even seen obliquely from above.
// State is carried by the screen alone, so every variant returns its panes.
export const PLAETZE: Platz[] = [

  // Straight desk: top on four tubular legs, one screen — the base case.
  (p, an) => {
    kasten(p.x, p.y - 10, 140, 70, 4, FARBEN.weiss, 2, 40);
    zylinder(p.x - 64, p.y - 38, 2.5, 40, FARBEN.metall);
    zylinder(p.x + 64, p.y - 38, 2.5, 40, FARBEN.metall);
    zylinder(p.x - 64, p.y + 18, 2.5, 40, FARBEN.metall);
    zylinder(p.x + 64, p.y + 18, 2.5, 40, FARBEN.metall);
    // Monitor on a plate foot and column, the pane sits in the dark frame.
    kasten(p.x, p.y - 34, 24, 14, 4, FARBEN.dunkel, 2, 44);
    zylinder(p.x, p.y - 34, 2.5, 14, FARBEN.metall, 48);
    kasten(p.x, p.y - 34, 50, 4, 32, FARBEN.dunkel, 2, 60);
    const scheibe = kasten(p.x, p.y - 31, 44, 4, 26, an ? FARBEN.schirmAn : FARBEN.schirm, 2, 63);
    kasten(p.x, p.y + 2, 44, 16, 4, FARBEN.papier, 2, 44);
    kugel(p.x + 34, p.y + 4, 4, FARBEN.dunkel, 44, 0.6);
    zylinder(p.x + 52, p.y - 16, 4, 9, FARBEN.gemein, 44, 4.5);
    return [scheibe];
  },

  // Desk with mobile pedestal: panel ends instead of legs, beside it a drawer unit on castors.
  (p, an) => {
    kasten(p.x, p.y - 10, 140, 70, 4, FARBEN.weiss, 2, 40);
    kasten(p.x - 66, p.y - 10, 5, 64, 40, FARBEN.holzDunkel, 2, 0);
    kasten(p.x + 66, p.y - 10, 5, 64, 40, FARBEN.holzDunkel, 2, 0);
    // The pedestal stands free under the top so it can roll out.
    kasten(p.x + 34, p.y - 10, 40, 50, 30, FARBEN.grund, 2, 7);
    kasten(p.x + 34, p.y + 14, 34, 5, 7, FARBEN.papier, 2, 10);
    kasten(p.x + 34, p.y + 14, 34, 5, 7, FARBEN.papier, 2, 19);
    kasten(p.x + 34, p.y + 14, 34, 5, 7, FARBEN.papier, 2, 28);
    // The castors reach up to the underside of the body; it stands on them.
    kugel(p.x + 20, p.y - 28, 3.5, FARBEN.dunkel, 0, 1);
    kugel(p.x + 48, p.y - 28, 3.5, FARBEN.dunkel, 0, 1);
    kugel(p.x + 20, p.y + 8, 3.5, FARBEN.dunkel, 0, 1);
    kugel(p.x + 48, p.y + 8, 3.5, FARBEN.dunkel, 0, 1);
    kasten(p.x - 28, p.y - 34, 26, 16, 4, FARBEN.dunkel, 2, 44);
    zylinder(p.x - 28, p.y - 34, 3, 12, FARBEN.metall, 48);
    kasten(p.x - 28, p.y - 34, 54, 4, 30, FARBEN.dunkel, 2, 58);
    const scheibe = kasten(p.x - 28, p.y - 31, 48, 4, 24, an ? FARBEN.schirmAn : FARBEN.schirm, 2, 61);
    kasten(p.x - 28, p.y + 4, 42, 16, 4, FARBEN.papier, 2, 44);
    return [scheibe];
  },

  // Corner desk: two tops at an angle, the short one carries the tray.
  (p, an) => {
    kasten(p.x - 5, p.y - 45, 130, 66, 4, FARBEN.holzHell, 2, 40);
    kasten(p.x + 35, p.y + 18, 60, 60, 4, FARBEN.holzHell, 2, 40);
    zylinder(p.x - 63, p.y - 70, 2.5, 40, FARBEN.metall);
    zylinder(p.x - 63, p.y - 20, 2.5, 40, FARBEN.metall);
    zylinder(p.x + 53, p.y - 70, 2.5, 40, FARBEN.metall);
    zylinder(p.x + 58, p.y + 42, 2.5, 40, FARBEN.metall);
    zylinder(p.x + 12, p.y + 42, 2.5, 40, FARBEN.metall);
    // The monitor stands at the back edge of the long top so the corner stays free.
    kasten(p.x + 30, p.y - 60, 26, 16, 4, FARBEN.dunkel, 2, 44);
    zylinder(p.x + 30, p.y - 60, 3, 14, FARBEN.metall, 48);
    kasten(p.x + 30, p.y - 60, 50, 4, 30, FARBEN.dunkel, 2, 60);
    const scheibe = kasten(p.x + 30, p.y - 57, 44, 4, 24, an ? FARBEN.schirmAn : FARBEN.schirm, 2, 63);
    kasten(p.x + 2, p.y - 24, 42, 16, 4, FARBEN.papier, 2, 44);
    // The tray lies on the short top: paper, pen cup, a pot.
    kasten(p.x + 46, p.y + 20, 28, 38, 5, FARBEN.papier, 2, 44);
    zylinder(p.x + 16, p.y + 8, 4, 10, FARBEN.metall, 44);
    zylinder(p.x + 18, p.y + 34, 7, 9, FARBEN.topf, 44, 8);
    kugel(p.x + 18, p.y + 34, 9, FARBEN.laub, 53, 0.7);
    return [scheibe];
  },

  // Sit-stand desk: a lifting column on a double-T foot, the top at standing height.
  (p, an) => {
    kasten(p.x, p.y - 8, 26, 66, 6, FARBEN.dunkel, 2, 0);
    kasten(p.x, p.y - 38, 90, 14, 6, FARBEN.dunkel, 2, 0);
    kasten(p.x, p.y + 20, 90, 14, 6, FARBEN.dunkel, 2, 0);
    zylinder(p.x, p.y - 8, 9, 34, FARBEN.metall, 6);
    zylinder(p.x, p.y - 8, 6.5, 26, FARBEN.grund, 40);
    kasten(p.x, p.y - 8, 130, 68, 5, FARBEN.weiss, 2, 62);
    // The control button sits at the front, right under the edge, otherwise nobody standing reaches it.
    kasten(p.x + 48, p.y + 24, 14, 5, 6, FARBEN.metall, 2, 56);
    kasten(p.x, p.y - 30, 24, 14, 4, FARBEN.dunkel, 2, 67);
    zylinder(p.x, p.y - 30, 2.5, 12, FARBEN.metall, 71);
    kasten(p.x, p.y - 30, 50, 4, 30, FARBEN.dunkel, 2, 81);
    const scheibe = kasten(p.x, p.y - 27, 44, 4, 24, an ? FARBEN.schirmAn : FARBEN.schirm, 2, 84);
    kasten(p.x, p.y + 6, 44, 16, 4, FARBEN.papier, 2, 67);
    kugel(p.x + 34, p.y + 8, 4, FARBEN.dunkel, 67, 0.6);
    return [scheibe];
  },

  // Dual monitor: two panes on one crossbar, a single foot carries both.
  (p, an) => {
    kasten(p.x, p.y - 10, 140, 72, 4, FARBEN.weiss, 2, 40);
    kasten(p.x - 64, p.y - 10, 6, 66, 40, FARBEN.metall, 2, 0);
    kasten(p.x + 64, p.y - 10, 6, 66, 40, FARBEN.metall, 2, 0);
    kasten(p.x, p.y - 36, 30, 18, 4, FARBEN.dunkel, 2, 44);
    zylinder(p.x, p.y - 36, 3, 32, FARBEN.metall, 48);
    kasten(p.x, p.y - 36, 116, 5, 5, FARBEN.metall, 2, 80);
    // Two identical housings hang left and right on the bar, either side of the column.
    kasten(p.x - 30, p.y - 36, 52, 4, 30, FARBEN.dunkel, 2, 50);
    kasten(p.x + 30, p.y - 36, 52, 4, 30, FARBEN.dunkel, 2, 50);
    const links = kasten(p.x - 30, p.y - 33, 46, 4, 24, an ? FARBEN.schirmAn : FARBEN.schirm, 2, 53);
    const rechts = kasten(p.x + 30, p.y - 33, 46, 4, 24, an ? FARBEN.schirmAn : FARBEN.schirm, 2, 53);
    flaeche(p.x, p.y + 6, 70, 34, FARBEN.stoff, 44.5);
    kasten(p.x, p.y + 4, 46, 16, 4, FARBEN.papier, 2, 45);
    kugel(p.x + 30, p.y + 6, 4, FARBEN.dunkel, 45, 0.6);
    return [links, rechts];
  },

  // Laptop on a stand: the device sits raised, keyboard and mouse lie in front of it.
  (p, an) => {
    kasten(p.x, p.y - 10, 130, 68, 4, FARBEN.holzHell, 2, 40);
    zylinder(p.x - 58, p.y - 36, 2.5, 40, FARBEN.metall);
    zylinder(p.x + 58, p.y - 36, 2.5, 40, FARBEN.metall);
    zylinder(p.x - 58, p.y + 16, 2.5, 40, FARBEN.metall);
    zylinder(p.x + 58, p.y + 16, 2.5, 40, FARBEN.metall);
    // The stand is a plate on two supports, with room for storage underneath.
    zylinder(p.x - 14, p.y - 26, 2, 10, FARBEN.metall, 44);
    zylinder(p.x + 14, p.y - 26, 2, 10, FARBEN.metall, 44);
    kasten(p.x, p.y - 26, 40, 30, 4, FARBEN.metall, 2, 54);
    kasten(p.x, p.y - 24, 36, 26, 4, FARBEN.grund, 2, 58);
    kasten(p.x, p.y - 36, 36, 4, 24, FARBEN.dunkel, 2, 60);
    const scheibe = kasten(p.x, p.y - 33, 31, 4, 20, an ? FARBEN.schirmAn : FARBEN.schirm, 2, 62);
    kasten(p.x - 4, p.y + 8, 42, 16, 4, FARBEN.papier, 2, 44);
    kugel(p.x + 32, p.y + 10, 4, FARBEN.dunkel, 44, 0.6);
    kasten(p.x + 44, p.y - 22, 22, 28, 5, FARBEN.papier, 2, 44);
    zylinder(p.x - 46, p.y - 18, 4, 10, FARBEN.gemein, 44, 4.5);
    return [scheibe];
  },

  // Desk with privacy screen: a fabric panel at the back edge, the monitor in front of it.
  (p, an) => {
    kasten(p.x, p.y - 10, 140, 70, 4, FARBEN.weiss, 2, 40);
    kasten(p.x - 64, p.y - 10, 5, 64, 40, FARBEN.grund, 2, 0);
    kasten(p.x + 64, p.y - 10, 5, 64, 40, FARBEN.grund, 2, 0);
    // The screen stands on two clamps above the top and shields only towards the back.
    kasten(p.x - 50, p.y - 44, 6, 8, 12, FARBEN.metall, 2, 32);
    kasten(p.x + 50, p.y - 44, 6, 8, 12, FARBEN.metall, 2, 32);
    kasten(p.x, p.y - 44, 140, 6, 34, akzent(), 2, 44);
    kasten(p.x + 46, p.y - 36, 34, 14, 4, FARBEN.holzDunkel, 2, 62);
    zylinder(p.x + 46, p.y - 36, 6, 8, FARBEN.topf, 66, 7);
    kugel(p.x + 46, p.y - 36, 8, FARBEN.laub, 74, 0.7);
    kasten(p.x - 18, p.y - 34, 22, 14, 4, FARBEN.dunkel, 2, 44);
    zylinder(p.x - 18, p.y - 34, 2.5, 12, FARBEN.metall, 48);
    kasten(p.x - 18, p.y - 34, 48, 4, 28, FARBEN.dunkel, 2, 58);
    const scheibe = kasten(p.x - 18, p.y - 31, 42, 4, 22, an ? FARBEN.schirmAn : FARBEN.schirm, 2, 61);
    kasten(p.x - 18, p.y + 4, 42, 16, 4, FARBEN.papier, 2, 44);
    kugel(p.x + 14, p.y + 6, 4, FARBEN.dunkel, 44, 0.6);
    return [scheibe];
  },

  // Bench: one continuous deep top on two panel ends. The spine in the middle carries
  // the screen and separates the working half from the storage behind it.
  (p, an) => {
    kasten(p.x, p.y, 140, 150, 5, FARBEN.weiss, 2, 39);
    kasten(p.x - 64, p.y, 6, 130, 39, FARBEN.metall, 2, 0);
    kasten(p.x + 64, p.y, 6, 130, 39, FARBEN.metall, 2, 0);
    kasten(p.x, p.y, 128, 6, 12, FARBEN.metall, 2, 24);
    kasten(p.x, p.y, 136, 6, 24, akzent(), 2, 44);
    kasten(p.x, p.y + 5, 52, 4, 30, FARBEN.dunkel, 2, 56);
    const scheibe = kasten(p.x, p.y + 8, 46, 4, 24, an ? FARBEN.schirmAn : FARBEN.schirm, 2, 59);
    kasten(p.x - 6, p.y + 44, 44, 16, 4, FARBEN.papier, 2, 44);
    kugel(p.x + 30, p.y + 46, 4, FARBEN.dunkel, 44, 0.6);
    zylinder(p.x - 52, p.y + 30, 4, 9, FARBEN.gemein, 44, 4.5);
    // Behind the spine lies the half nobody needs for working: stacks and a pot.
    kasten(p.x + 18, p.y - 40, 40, 30, 6, FARBEN.papier, 2, 44);
    kasten(p.x - 30, p.y - 36, 34, 26, 8, FARBEN.papier, 2, 44);
    zylinder(p.x + 52, p.y - 50, 8, 11, FARBEN.topf, 44, 9);
    kugel(p.x + 52, p.y - 50, 10, FARBEN.laub, 55, 0.7);
    return [scheibe];
  },

  // Drawing table: a tilted top on two trestles, a drawing tablet instead of a monitor.
  (p, an) => {
    // The trestles stand at the sides so there is knee room under the sloping top.
    kasten(p.x - 60, p.y - 10, 8, 60, 4, FARBEN.holzDunkel, 2, 0);
    kasten(p.x + 60, p.y - 10, 8, 60, 4, FARBEN.holzDunkel, 2, 0);
    kasten(p.x - 60, p.y - 14, 5, 8, 44, FARBEN.holzDunkel, 2, 0);
    kasten(p.x + 60, p.y - 14, 5, 8, 44, FARBEN.holzDunkel, 2, 0);
    // Top, sheet and tablet share the same tilt — otherwise the paper floats.
    const neigung = 0.22;
    kasten(p.x, p.y - 10, 136, 70, 4, FARBEN.holzHell, 2, 44).rotation.x = neigung;
    kasten(p.x - 24, p.y - 10, 60, 44, 1, FARBEN.papier, 1, 48).rotation.x = neigung;
    const scheibe = kasten(p.x + 34, p.y - 12, 40, 28, 2, an ? FARBEN.schirmAn : FARBEN.schirm, 2, 48);
    scheibe.rotation.x = neigung;
    // The ledge at the lower edge holds pen and sheet, otherwise both slide off.
    kasten(p.x, p.y + 25, 130, 4, 4, FARBEN.holzDunkel, 2, 37);
    zylinder(p.x + 64, p.y - 40, 1.8, 48, FARBEN.metall, 40);
    kasten(p.x + 50, p.y - 40, 30, 3, 3, FARBEN.metall, 1, 86);
    zylinder(p.x + 36, p.y - 40, 8, 9, FARBEN.dunkel, 78, 3);
    return [scheibe];
  },

  // Desk with return: the main top on the left, on the right a cabinet with the printer on it.
  (p, an) => {
    kasten(p.x - 12, p.y - 10, 116, 70, 4, FARBEN.weiss, 2, 40);
    kasten(p.x - 66, p.y - 10, 5, 64, 40, FARBEN.holzDunkel, 2, 0);
    // The return runs out towards the front so the printer stands beside the seat, not behind it.
    kasten(p.x + 70, p.y + 4, 44, 100, 40, FARBEN.grund, 3, 0);
    kasten(p.x + 70, p.y + 4, 48, 104, 4, FARBEN.weiss, 2, 40);
    kasten(p.x + 70, p.y + 18, 38, 34, 14, FARBEN.metall, 3, 44);
    kasten(p.x + 70, p.y + 36, 26, 10, 3, FARBEN.papier, 1, 48);
    kasten(p.x + 70, p.y + 14, 26, 18, 1, FARBEN.papier, 1, 58);
    kasten(p.x + 70, p.y - 26, 32, 22, 8, FARBEN.papier, 2, 44);
    kasten(p.x - 16, p.y - 34, 24, 14, 4, FARBEN.dunkel, 2, 44);
    zylinder(p.x - 16, p.y - 34, 2.5, 14, FARBEN.metall, 48);
    kasten(p.x - 16, p.y - 34, 50, 4, 30, FARBEN.dunkel, 2, 60);
    const scheibe = kasten(p.x - 16, p.y - 31, 44, 4, 24, an ? FARBEN.schirmAn : FARBEN.schirm, 2, 63);
    kasten(p.x - 16, p.y + 4, 44, 16, 4, FARBEN.papier, 2, 44);
    kugel(p.x + 16, p.y + 6, 4, FARBEN.dunkel, 44, 0.6);
    return [scheibe];
  },

  // Wide desk with a curved monitor: three pane segments, the wings turned towards the seat.
  (p, an) => {
    kasten(p.x, p.y - 10, 160, 70, 4, FARBEN.weiss, 2, 40);
    kasten(p.x - 72, p.y - 10, 60, 6, 4, FARBEN.dunkel, 2, 0);
    kasten(p.x + 72, p.y - 10, 60, 6, 4, FARBEN.dunkel, 2, 0);
    kasten(p.x - 72, p.y - 10, 5, 5, 40, FARBEN.dunkel, 2, 0);
    kasten(p.x + 72, p.y - 10, 5, 5, 40, FARBEN.dunkel, 2, 0);
    kasten(p.x, p.y - 38, 30, 16, 4, FARBEN.dunkel, 2, 44);
    zylinder(p.x, p.y - 38, 3, 14, FARBEN.metall, 48);
    // A straight box would look like any other screen from above; only the angled
    // wings make the curve visible.
    const mitte = kasten(p.x, p.y - 34, 44, 4, 26, an ? FARBEN.schirmAn : FARBEN.schirm, 2, 60);
    const links = kasten(p.x - 36, p.y - 30, 32, 4, 26, an ? FARBEN.schirmAn : FARBEN.schirm, 2, 60);
    const rechts = kasten(p.x + 36, p.y - 30, 32, 4, 26, an ? FARBEN.schirmAn : FARBEN.schirm, 2, 60);
    links.rotation.y = 0.35;
    rechts.rotation.y = -0.35;
    kasten(p.x, p.y - 38, 100, 5, 6, FARBEN.dunkel, 2, 70);
    flaeche(p.x, p.y + 4, 90, 36, FARBEN.dunkel, 44.5);
    kasten(p.x, p.y + 4, 46, 16, 4, FARBEN.papier, 2, 45);
    kugel(p.x + 34, p.y + 6, 4, FARBEN.dunkel, 45, 0.6);
    return [mitte, links, rechts];
  },

  // Small laptop desk with lamp: a narrow top, the device lies flat, the light beside it.
  (p, an) => {
    kasten(p.x, p.y - 14, 100, 54, 4, FARBEN.holzHell, 3, 40);
    zylinder(p.x - 44, p.y - 36, 2, 40, FARBEN.holzDunkel);
    zylinder(p.x + 44, p.y - 36, 2, 40, FARBEN.holzDunkel);
    zylinder(p.x - 44, p.y + 8, 2, 40, FARBEN.holzDunkel);
    zylinder(p.x + 44, p.y + 8, 2, 40, FARBEN.holzDunkel);
    kasten(p.x - 6, p.y - 8, 34, 22, 2, FARBEN.metall, 2, 44);
    kasten(p.x - 6, p.y - 20, 34, 2, 22, FARBEN.metall, 1, 46);
    const scheibe = kasten(p.x - 6, p.y - 19, 30, 2, 18, an ? FARBEN.schirmAn : FARBEN.schirm, 1, 48);
    // The lamp stands at the back left so its shade reaches over the top and not over the seat.
    zylinder(p.x - 36, p.y - 30, 7, 2, FARBEN.dunkel, 44);
    zylinder(p.x - 36, p.y - 30, 1.5, 30, FARBEN.metall, 46);
    kasten(p.x - 28, p.y - 26, 18, 3, 3, FARBEN.metall, 1, 74);
    zylinder(p.x - 20, p.y - 24, 8, 8, FARBEN.dunkel, 68, 3);
    zylinder(p.x + 32, p.y - 20, 4, 8, FARBEN.papier, 44);
    kasten(p.x + 30, p.y + 2, 20, 26, 2, FARBEN.stoff, 2, 44);
    return [scheibe];
  },

  // Workbench: a thick top, a pegboard at the back, the screen on a wall arm.
  (p, an) => {
    kasten(p.x, p.y - 10, 150, 72, 6, FARBEN.holzDunkel, 2, 38);
    kasten(p.x - 68, p.y - 40, 6, 6, 38, FARBEN.metall, 2, 0);
    kasten(p.x + 68, p.y - 40, 6, 6, 38, FARBEN.metall, 2, 0);
    kasten(p.x - 68, p.y + 20, 6, 6, 38, FARBEN.metall, 2, 0);
    kasten(p.x + 68, p.y + 20, 6, 6, 38, FARBEN.metall, 2, 0);
    kasten(p.x, p.y - 10, 136, 60, 3, FARBEN.holz, 2, 10);
    // The pegboard stands on the back edge: from above, together with the tools in
    // front of it, it makes a row of teeth no other place has.
    kasten(p.x, p.y - 44, 150, 4, 50, FARBEN.holz, 2, 44);
    kasten(p.x - 56, p.y - 40, 8, 3, 20, FARBEN.metall, 1, 62);
    kasten(p.x - 44, p.y - 40, 6, 3, 24, FARBEN.dunkel, 1, 60);
    kasten(p.x - 30, p.y - 40, 12, 3, 14, FARBEN.topf, 1, 70);
    kasten(p.x + 22, p.y - 38, 50, 4, 30, FARBEN.dunkel, 2, 58);
    const scheibe = kasten(p.x + 22, p.y - 35, 44, 4, 24, an ? FARBEN.schirmAn : FARBEN.schirm, 2, 61);
    kasten(p.x - 36, p.y - 12, 34, 24, 10, FARBEN.grund, 2, 44);
    zylinder(p.x - 60, p.y + 6, 5, 6, FARBEN.dunkel, 44);
    kasten(p.x + 12, p.y + 6, 44, 16, 4, FARBEN.papier, 2, 44);
    return [scheibe];
  },

  // Standing lectern: a column on a round foot, on it a small top with the laptop.
  (p, an) => {
    // The mat lies where one stands — it gives the place away without a chair.
    flaeche(p.x, p.y + 6, 70, 44, FARBEN.teppich, 0.5);
    zylinder(p.x, p.y - 26, 24, 3, FARBEN.dunkel, 0);
    zylinder(p.x, p.y - 26, 4.5, 62, FARBEN.metall, 3);
    kasten(p.x, p.y - 22, 76, 48, 4, FARBEN.weiss, 3, 65);
    kasten(p.x, p.y + 1, 72, 3, 3, FARBEN.holzDunkel, 1, 69);
    kasten(p.x - 4, p.y - 20, 32, 22, 2, FARBEN.metall, 2, 69);
    kasten(p.x - 4, p.y - 32, 32, 2, 22, FARBEN.metall, 1, 71);
    const scheibe = kasten(p.x - 4, p.y - 31, 28, 2, 18, an ? FARBEN.schirmAn : FARBEN.schirm, 1, 73);
    zylinder(p.x + 28, p.y - 36, 3.5, 16, FARBEN.laub, 69);
    return [scheibe];
  },

  // Round table as a workplace: a central foot, laptop, notebook and cup.
  (p, an) => {
    zylinder(p.x, p.y - 24, 22, 3, FARBEN.metall, 0);
    zylinder(p.x, p.y - 24, 4, 37, FARBEN.metall, 3);
    // The only circle among the places — from above the clearest distinguishing mark.
    zylinder(p.x, p.y - 24, 48, 4, FARBEN.holzHell, 40);
    kasten(p.x, p.y - 14, 34, 22, 2, FARBEN.dunkel, 2, 44);
    kasten(p.x, p.y - 26, 34, 2, 22, FARBEN.dunkel, 1, 46);
    const scheibe = kasten(p.x, p.y - 25, 30, 2, 18, an ? FARBEN.schirmAn : FARBEN.schirm, 1, 48);
    kasten(p.x - 30, p.y - 30, 18, 24, 2, FARBEN.stoff, 2, 44);
    zylinder(p.x + 28, p.y - 36, 4, 8, FARBEN.papier, 44);
    kugel(p.x + 24, p.y - 8, 4, FARBEN.dunkel, 44, 0.6);
    return [scheibe];
  },

];
