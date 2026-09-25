import { FARBEN, kasten, kugel, flaeche, zylinder } from "../mal";
import type { Flaechenstueck } from "./gruppen";

/* Tea kitchens. The place where a department is currently not working: counter,
 * island, bar table, appliance corner, wall shelf, seating corner. There are
 * several kinds, because depending on the floor plan a tea kitchen clings to
 * the wall, stands free in the room or consists of no more than a board with a
 * kettle. The heights follow the scale of the scene (table 44, seat 30,
 * cabinet 76–110, ceiling 150), not real centimetres: a worktop therefore sits
 * at 52 and not at 90. */
export const KUECHEN: Flaechenstueck[] = [

  // 1 — Kitchen counter with sink, coffee machine and a wall cabinet above
  (x, y, breite, tiefe) => {
    const zeileB = Math.max(130, Math.min(breite - 12, 300));   // below 130, sink and appliances stand on top of each other
    const zeileT = Math.min(62, Math.max(46, tiefe - 40));
    const zy = y - tiefe / 2 + zeileT / 2 + 6;                  // the counter stands against the back wall
    const links = x - zeileB / 2;

    // Recessed plinth, carcass, worktop: the recess below makes the cabinets sit up rather than lie flat.
    kasten(x, zy + 3, zeileB - 10, zeileT - 12, 5, FARBEN.dunkel, 2);
    kasten(x, zy, zeileB, zeileT, 42, FARBEN.holz, 4, 5);
    kasten(x, zy, zeileB + 5, zeileT + 5, 5, FARBEN.metall, 2, 47);

    // The joints between the fronts cannot be seen from above, the handles can.
    for (let i = 0; i < 4; i++) {
      const gx = links + zeileB * (0.16 + i * 0.23);
      kasten(gx, zy + zeileT / 2 + 1, Math.min(26, zeileB / 6), 5, 5, FARBEN.metall, 2, 32);
    }

    // Sink: a metal rim, the basin a dark surface inside it and lower than the rim,
    // otherwise from above a plate lies on the counter instead of a hole in it.
    const sx = links + zeileB * 0.46;
    kasten(sx, zy, 50, 42, 4, FARBEN.metall, 2, 50);
    flaeche(sx, zy, 40, 33, FARBEN.dunkel, 52.5);
    zylinder(sx, zy, 2.5, 0.8, FARBEN.metall, 52.6);

    // Tap behind the basin, mounted on the rim and low enough for the wall cabinet.
    zylinder(sx, zy - 16, 2, 16, FARBEN.metall, 54);
    kasten(sx, zy - 8, 5, 16, 5, FARBEN.metall, 2, 65);

    // Coffee machine with water tank and brew head; the cups stand under the spout in front of the housing.
    const kx = links + zeileB * 0.13;
    kasten(kx, zy - 4, 32, 26, 20, FARBEN.dunkel, 4, 52);
    kasten(kx, zy - 8, 24, 14, 6, FARBEN.metall, 2, 72);
    kasten(kx, zy + 12, 14, 10, 5, FARBEN.dunkel, 2, 66);
    zylinder(kx - 5, zy + 13, 3.5, 5, FARBEN.papier, 52);
    zylinder(kx + 5, zy + 13, 3.5, 5, FARBEN.papier, 52);

    // Kettle on the worktop to the right, then four cups in two rows.
    const wx = links + zeileB * 0.76;
    zylinder(wx, zy, 9, 15, FARBEN.metall, 52, 7);
    kasten(wx + 11, zy, 5, 9, 10, FARBEN.dunkel, 2, 55);
    for (let i = 0; i < 4; i++) {
      zylinder(links + zeileB * 0.93 + (i % 2) * 9 - 4.5, zy - 6 + Math.floor(i / 2) * 12, 3.5, 5, FARBEN.papier, 52);
    }

    // Wall cabinet above the counter, high enough for the machine and the tap to pass underneath.
    const hb = zeileB * 0.62;
    kasten(x, y - tiefe / 2 + 22, hb, 32, 38, FARBEN.holzDunkel, 3, 88);
    kasten(x - hb / 4, y - tiefe / 2 + 37, hb / 2.6, 5, 5, FARBEN.metall, 2, 94);
    kasten(x + hb / 4, y - tiefe / 2 + 37, hb / 2.6, 5, 5, FARBEN.metall, 2, 94);
  },

  // 2 — Kitchen island with bar stools and two pendant lamps
  (x, y, breite, tiefe) => {
    const inselB = Math.max(90, Math.min(breite - 60, 190));
    const inselT = Math.max(46, Math.min(tiefe - 96, 82));
    const hockerY = Math.min(y + inselT / 2 + 24, y + tiefe / 2 - 18);   // the row of stools stays inside the room

    // The block carries an overhanging top; the overhang is what the stools have room under.
    kasten(x, y, inselB - 12, inselT - 10, 5, FARBEN.dunkel, 2);
    kasten(x, y, inselB, inselT, 42, FARBEN.holzDunkel, 4, 5);
    kasten(x, y, inselB + 16, inselT + 14, 5, FARBEN.metall, 2, 47);

    // As many stools as fit along the long side without touching: disc foot, column, cushion.
    const hocker = Math.max(2, Math.min(4, Math.floor(inselB / 40)));
    for (let i = 0; i < hocker; i++) {
      const hx = x - inselB / 2 + inselB * (i + 0.5) / hocker;
      zylinder(hx, hockerY, 15, 2, FARBEN.metall, 0);
      zylinder(hx, hockerY, 4, 31, FARBEN.metall, 2);
      zylinder(hx, hockerY, 16, 5, FARBEN.stoff, 33);
    }

    // Fruit bowl and two jugs as what always stands on an island.
    const ox = x + inselB * 0.14;
    zylinder(ox, y, 13, 3, FARBEN.papier, 52);
    kugel(ox - 5, y - 2, 4, FARBEN.laub, 55, 1);
    kugel(ox + 5, y - 2, 4, FARBEN.topf, 55, 1);
    kugel(ox, y + 5, 4, FARBEN.laub, 55, 1);
    zylinder(x - inselB * 0.28, y - 8, 7, 13, FARBEN.metall, 52, 5);
    zylinder(x - inselB * 0.28 + 17, y + 7, 5, 10, FARBEN.papier, 52, 4);

    // Pendant lamps: cable from the ceiling, a flat shade. They hang high enough that
    // nothing on the top touches them, and they do not glow — light at a desk makes a statement.
    for (let i = 0; i < 2; i++) {
      const px = x + (i === 0 ? -1 : 1) * inselB * 0.3;
      zylinder(px, y, 1.2, 32, FARBEN.metall, 118);
      kugel(px, y, 11, FARBEN.schirm, 108, 0.55);
    }
  },

  // 3 — Bar table with water dispenser
  (x, y, breite, tiefe) => {
    const halbB = breite / 2;
    const halbT = tiefe / 2;
    const tx = x - Math.min(40, halbB * 0.3);            // the table moves left, the dispenser stands on the right

    // A round disc on a column: from above the only circle in the room and therefore no desk.
    zylinder(tx, y, 27, 3, FARBEN.metall, 0);
    zylinder(tx, y, 7, 60, FARBEN.metall, 3);
    zylinder(tx, y, 38, 3, FARBEN.holz, 63);

    // Two mugs and a plate; no more fits on a bar table.
    zylinder(tx - 13, y - 8, 4, 6, FARBEN.papier, 66);
    zylinder(tx + 11, y + 6, 4, 6, FARBEN.papier, 66);
    zylinder(tx + 2, y - 15, 7, 1.5, FARBEN.papier, 66);

    // Two high stools, placed offset so the table does not look symmetrically crowded.
    const h1x = Math.max(x - halbB + 18, tx - 46);
    const h1y = y + Math.min(26, halbT - 18);
    const h2x = tx + 10;
    const h2y = y - Math.min(46, halbT - 18);
    for (let i = 0; i < 2; i++) {
      const hx = i === 0 ? h1x : h2x;
      const hy = i === 0 ? h1y : h2y;
      zylinder(hx, hy, 14, 2, FARBEN.dunkel, 0);
      zylinder(hx, hy, 4, 39, FARBEN.metall, 2);
      zylinder(hx, hy, 15, 4, FARBEN.stoff, 41);
    }

    // Water dispenser at the right edge: housing, tap, upturned bottle —
    // the neck sits down in the collar, it is wide at the top, otherwise it stands on its head.
    const wx = x + halbB - 22;
    const wy = y - Math.min(tiefe * 0.18, halbT - 20);
    kasten(wx, wy, 34, 34, 58, FARBEN.papier, 4);
    kasten(wx, wy + 16, 20, 6, 8, FARBEN.dunkel, 2, 36);
    zylinder(wx, wy, 8, 4, FARBEN.metall, 58);
    zylinder(wx, wy, 9, 22, FARBEN.schirm, 62, 14);
  },

  // 4 — Fridge, drink crates and waste separation
  (x, y, breite, tiefe) => {
    const links = x - breite / 2 + 6;
    const ry = y - tiefe / 2 + 40;                       // the appliances stand with their back to the wall
    const kx = links + 38;

    // Tall fridge in two parts: the dark gap at two thirds of the height makes the freezer readable.
    kasten(kx, ry, 72, 70, 111, FARBEN.metall, 4);
    kasten(kx, ry + 35, 68, 5, 5, FARBEN.dunkel, 2, 70);
    kasten(kx - 26, ry + 35, 5, 5, 18, FARBEN.dunkel, 2, 76);
    kasten(kx - 26, ry + 35, 5, 5, 28, FARBEN.dunkel, 2, 36);
    kasten(kx, ry, 40, 40, 6, FARBEN.papier, 2, 111);

    // Two stacked crates with bottles; the bottle necks make them a crate from above.
    const gx = links + 100;
    kasten(gx, ry + 6, 42, 32, 16, FARBEN.stoff, 3);
    kasten(gx, ry + 6, 42, 32, 16, FARBEN.stoff, 3, 16);
    for (let i = 0; i < 6; i++) {
      zylinder(gx - 14 + (i % 3) * 14, ry + (i < 3 ? -1 : 13), 5, 16, FARBEN.laub, 32, 3);
    }

    // Three bins, equally tall, different lids. If the wall is too short they do not
    // stand in the row but in front of it — otherwise they reach into the corridor.
    const reihe = breite >= 240;
    for (let i = 0; i < 3; i++) {
      const mx = reihe ? links + 140 + i * 34 : links + 22 + i * 36;
      const my = reihe ? ry + 8 : y + tiefe / 2 - 24;
      zylinder(mx, my, 15, 34, FARBEN.dunkel, 0);
      zylinder(mx, my, 16, 3, i === 0 ? FARBEN.laub : i === 1 ? FARBEN.papier : FARBEN.metall, 34);
    }
  },

  // 5 — Wall shelf with sideboard, microwave and tea rack
  (x, y, breite, tiefe) => {
    const wandY = y - tiefe / 2 + 8;
    const sbB = Math.max(120, Math.min(breite - 40, 200));
    const sy = wandY + 24;
    const links = x - sbB / 2;

    // A sideboard instead of a counter: no sink, only surface to put things on.
    kasten(x, sy, sbB, 44, 46, FARBEN.holzDunkel, 4);
    kasten(x, sy, sbB + 4, 48, 5, FARBEN.holz, 2, 46);

    // Microwave on the left, dark window to the front.
    const mx = links + sbB * 0.22;
    kasten(mx, sy, 52, 38, 18, FARBEN.metall, 3, 51);
    kasten(mx - 6, sy + 18, 32, 5, 12, FARBEN.dunkel, 2, 54);

    // Kettle and mug tree: two discs on a rod, from above a small target.
    // The three pieces sit at fractions of the width so they do not touch even on the shortest board.
    zylinder(links + sbB * 0.62, sy, 9, 14, FARBEN.dunkel, 51, 7);
    const tx = links + sbB * 0.85;
    zylinder(tx, sy, 11, 2, FARBEN.metall, 51);
    zylinder(tx, sy, 2, 26, FARBEN.metall, 53);
    zylinder(tx, sy, 9, 2, FARBEN.metall, 64);
    for (let i = 0; i < 2; i++) {
      const richtung = i === 0 ? -1 : 1;
      zylinder(tx + richtung * 7, sy + 5, 3.5, 5, FARBEN.papier, i === 0 ? 53 : 66);
      zylinder(tx - richtung * 7, sy - 5, 3.5, 5, FARBEN.papier, i === 0 ? 53 : 66);
    }

    // Two open boards on the wall, tea tins in alternating materials on them.
    // The lower one sits above the microwave and the mug tree, the upper one stays below the ceiling.
    for (let b = 0; b < 2; b++) {
      const bh = 86 + b * 24;
      kasten(x, wandY + 13, sbB * 0.8, 26, 5, FARBEN.holz, 2, bh);
      for (let i = 0; i < 6; i++) {
        const dx = x - sbB * 0.36 + i * (sbB * 0.72 / 5);
        const farbe = [FARBEN.topf, FARBEN.metall, FARBEN.papier, FARBEN.laub, FARBEN.stoff, FARBEN.holzDunkel][(i + b * 3) % 6];
        kasten(dx, wandY + 13 + (i % 2 === 0 ? -3 : 3), 11, 11, 8 + (i % 3) * 2, farbe, 2, bh + 5);
      }
    }
  },

  // 6 — Seating corner with corner bench, round table and rug
  (x, y, breite, tiefe) => {
    const bx = x - Math.min(breite * 0.1, 24);
    const by = y - Math.min(tiefe * 0.06, 12);

    // The rug sets the corner off from the corridor before any furniture stands there.
    flaeche(bx, by + 6, Math.max(120, Math.min(breite - 30 - 2 * (x - bx), 170)), Math.max(110, Math.min(tiefe - 42 - 2 * (y - by), 150)), FARBEN.teppich, 0.4);

    // Corner bench as two seat beams at a right angle, backrests on the outer sides.
    const langB = Math.max(96, Math.min(breite * 0.58, 150));
    const langT = Math.max(96, Math.min(tiefe * 0.52, 120));
    const eckX = bx - langB / 2;
    const eckY = by - langT / 2;
    kasten(bx, eckY + 24, langB, 48, 30, FARBEN.holzDunkel, 4);
    kasten(eckX + 24, by + 12, 48, langT - 48, 30, FARBEN.holzDunkel, 4);
    kasten(bx, eckY + 4, langB, 8, 25, FARBEN.holz, 2, 30);
    kasten(eckX + 4, by + 12, 8, langT - 48, 25, FARBEN.holz, 2, 30);

    // Cushions on the free stretches of both benches; the corner stays empty, that is where they meet.
    const freiB = langB - 60;
    const anzahlB = Math.max(1, Math.floor(freiB / 45));
    for (let i = 0; i < anzahlB; i++) {
      kasten(eckX + 48 + freiB * (i + 0.5) / anzahlB, eckY + 26, Math.min(34, freiB / anzahlB - 8), 34, 8, FARBEN.stoff, 3, 30);
    }
    const freiT = langT - 60;
    const anzahlT = Math.max(1, Math.floor(freiT / 45));
    for (let i = 0; i < anzahlT; i++) {
      kasten(eckX + 26, eckY + 48 + freiT * (i + 0.5) / anzahlT, 34, Math.min(34, freiT / anzahlT - 8), 8, FARBEN.stoff, 3, 30);
    }

    // Round table on a cross foot, lower than a desk.
    const tx = bx + 26;
    const ty = by + 22;                                  // far enough from the bench that the cross foot does not stand in it
    zylinder(tx, ty, 20, 2, FARBEN.metall, 0);
    zylinder(tx, ty, 6, 39, FARBEN.metall, 2);
    zylinder(tx, ty, 34, 3, FARBEN.holz, 41);
    zylinder(tx - 12, ty + 6, 4, 6, FARBEN.papier, 44);
    zylinder(tx + 10, ty - 8, 4, 6, FARBEN.papier, 44);
    flaeche(tx + 4, ty + 14, 20, 14, FARBEN.papier, 44.2);

    // Two loose chairs in front of the table, past the end of the bench — beside it they would stand inside it.
    const stuhlY = Math.max(ty + 40, eckY + langT + 12);
    for (let i = 0; i < 2; i++) {
      const cx = tx + (i === 0 ? -22 : 22);
      kasten(cx, stuhlY, 40, 40, 8, FARBEN.stoff, 3, 30);
      kasten(cx, stuhlY + 17, 38, 7, 20, FARBEN.holzDunkel, 3, 38);
      for (let b = 0; b < 4; b++) {
        zylinder(cx + (b % 2 === 0 ? -14 : 14), stuhlY + (b < 2 ? -14 : 14), 2, 30, FARBEN.metall, 0);
      }
    }

    // A plant in the open corner behind the end of the bench.
    const px = eckX + 8;
    const py = eckY + langT + 16;
    zylinder(px, py, 13, 16, FARBEN.topf, 0, 15);
    kugel(px, py, 17, FARBEN.laub, 16, 0.8);
    kugel(px - 8, py + 6, 9, FARBEN.laub, 30, 0.7);
  },
];
