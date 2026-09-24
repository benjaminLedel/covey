import { FARBEN, kasten, kugel, flaeche, zylinder } from "../mal";
import type { Flaechenstueck } from "./gruppen";

/* Meeting rooms: the table around which several seats come together, plus the
 * surface to show things on (whiteboard, flipchart, screen). There are several
 * kinds, because a department meets differently in twos than in twelves — and
 * because the silhouette from above (oval, round, long, U, tall) already names
 * the room. x/y is the centre of the assigned area, breite/tiefe its extent. */
export const BESPRECHUNGEN: Flaechenstueck[] = [

  // 1 — Oval conference table with eight chairs: the classic department round;
  //     the rounded ends take the corners out of the way.
  (x, y, breite, tiefe) => {
    const laenge = Math.max(150, Math.min(breite - 120, 280));
    const tief = Math.max(70, Math.min(tiefe - 170, 120));
    const kern = laenge - tief;

    flaeche(x, y, Math.min(breite - 30, laenge + 130), Math.min(tiefe - 30, tief + 150), FARBEN.teppich, 0.4);

    // Top: a rounded middle section, a half-round cylinder at each end.
    kasten(x, y, kern, tief, 5, FARBEN.holz, 16, 39);
    zylinder(x - kern / 2, y, tief / 2, 5, FARBEN.holz, 39);
    zylinder(x + kern / 2, y, tief / 2, 5, FARBEN.holz, 39);
    // Two panels instead of four legs, so there is room for knees under the table.
    kasten(x - kern / 4, y, 18, tief - 30, 39, FARBEN.holzDunkel, 4);
    kasten(x + kern / 4, y, 18, tief - 30, 39, FARBEN.holzDunkel, 4);

    const stuhl = (sx: number, sy: number, rx: number, ry: number) => {
      zylinder(sx, sy, 19, 3, FARBEN.metall);
      zylinder(sx, sy, 5, 28, FARBEN.metall, 3);
      kasten(sx, sy, 38, 38, 4, FARBEN.stoff, 10, 28);
      const quer = Math.abs(rx) > Math.abs(ry);
      kasten(sx + rx * 17, sy + ry * 17, quer ? 6 : 34, quer ? 34 : 6, 32, FARBEN.stoff, 3, 32);
    };

    const abstand = tief / 2 + 34;
    // A seat needs a chair's width: with a short middle section the lower bound
    // keeps the three chairs per long side apart.
    const versatz = Math.max(46, kern * 0.34);
    [-versatz, 0, versatz].forEach((dx) => {
      stuhl(x + dx, y - abstand, 0, -1);
      stuhl(x + dx, y + abstand, 0, 1);
    });
    stuhl(x - laenge / 2 - 34, y, -1, 0);
    stuhl(x + laenge / 2 + 34, y, 1, 0);

    // Conference speaker in the middle, two pads and two mugs beside it.
    kugel(x, y, 9, FARBEN.dunkel, 44, 0.4);
    kasten(x - versatz, y - tief / 4, 16, 22, 1, FARBEN.papier, 2, 44);
    kasten(x + versatz, y + tief / 4, 16, 22, 1, FARBEN.papier, 2, 44);
    zylinder(x - versatz + 20, y - tief / 4, 4, 9, FARBEN.papier, 44);
    zylinder(x + versatz - 20, y + tief / 4, 4, 9, FARBEN.papier, 44);

    // Whiteboard on the back wall, with a tray and two markers; the top edge
    // stays below the ceiling, otherwise the frame sticks to it.
    const wy = y - tiefe / 2 + 5;
    const tafel = Math.min(breite - 80, 190);
    kasten(x, wy, tafel + 8, 6, 70, FARBEN.metall, 2, 70);
    kasten(x, wy + 4, tafel, 4, 64, FARBEN.papier, 2, 73);
    kasten(x, wy + 7, tafel * 0.6, 5, 3, FARBEN.metall, 2, 70);
    kasten(x - 20, wy + 8, 12, 4, 4, FARBEN.dunkel, 2, 73);
    kasten(x + 4, wy + 8, 12, 4, 4, FARBEN.dunkel, 2, 73);
  },

  // 2 — Round table with six chairs and a round rug: no head of the table,
  //     so this is where the meeting without rank sits.
  (x, y, breite, tiefe) => {
    const r = Math.max(50, Math.min(Math.min(breite, tiefe) * 0.24, 85));

    zylinder(x, y, r + 72, 0.6, FARBEN.teppich, 0.2);

    // Centre column with a disc foot — otherwise six chairs have six legs in the way.
    zylinder(x, y, r * 0.5, 4, FARBEN.holzDunkel);
    zylinder(x, y, 10, 35, FARBEN.metall, 4);
    zylinder(x, y, r, 5, FARBEN.holz, 39);

    const stuhl = (sx: number, sy: number, rx: number, ry: number) => {
      zylinder(sx, sy, 18, 3, FARBEN.metall);
      zylinder(sx, sy, 5, 27, FARBEN.metall, 3);
      zylinder(sx, sy, 20, 4, FARBEN.stoff, 27);
      const quer = Math.abs(rx) > Math.abs(ry);
      kasten(sx + rx * 16, sy + ry * 16, quer ? 6 : 32, quer ? 32 : 6, 30, FARBEN.stoff, 3, 31);
    };

    for (let i = 0; i < 6; i++) {
      const w = (i * Math.PI) / 3;
      const rx = Math.cos(w);
      const ry = Math.sin(w);
      stuhl(x + rx * (r + 36), y + ry * (r + 36), rx, ry);
    }

    // Water jug and glasses, set evenly instead of scattered at random.
    zylinder(x, y, 7, 22, FARBEN.metall, 44);
    for (let i = 0; i < 3; i++) {
      const w = (i * 2 * Math.PI) / 3 + 0.5;
      zylinder(x + Math.cos(w) * (r * 0.55), y + Math.sin(w) * (r * 0.55), 4, 10, FARBEN.papier, 44);
    }

    // Flipchart in the corner: a tripod with a paper pad; the pad ends below the
    // leg tips and well below the ceiling.
    const fx = x - Math.min(breite, tiefe) * 0.32;
    const fy = y - tiefe / 2 + 40;
    zylinder(fx - 14, fy + 12, 3, 138, FARBEN.metall);
    zylinder(fx + 14, fy + 12, 3, 138, FARBEN.metall);
    zylinder(fx, fy - 14, 3, 138, FARBEN.metall);
    kasten(fx, fy, 62, 6, 70, FARBEN.papier, 2, 64);
    kasten(fx, fy, 70, 10, 5, FARBEN.metall, 2, 130);
  },

  // 3 — Long table with projector and screen: the room where someone at the front
  //     shows something. The screen hangs on the long wall, the opposite row
  //     faces it directly, the other one turns towards it.
  (x, y, breite, tiefe) => {
    const laenge = Math.max(180, Math.min(breite - 130, 320));
    const tief = Math.max(80, Math.min(tiefe - 200, 110));
    const ty = y + 25;

    flaeche(x, ty, Math.min(breite - 30, laenge + 120), Math.min(tiefe - 60, tief + 140), FARBEN.teppich, 0.4);

    kasten(x, ty, laenge, tief, 5, FARBEN.holz, 6, 39);
    kasten(x - laenge / 2 + 25, ty, 10, tief - 20, 39, FARBEN.metall, 2);
    kasten(x + laenge / 2 - 25, ty, 10, tief - 20, 39, FARBEN.metall, 2);
    // Cable channel down the middle of the table, set off in dark, with two sockets.
    kasten(x, ty, laenge - 80, 14, 1, FARBEN.dunkel, 2, 44);
    kasten(x - laenge * 0.18, ty, 20, 12, 3, FARBEN.metall, 2, 44);
    kasten(x + laenge * 0.18, ty, 20, 12, 3, FARBEN.metall, 2, 44);

    const stuhl = (sx: number, sy: number, ry: number) => {
      zylinder(sx, sy, 19, 3, FARBEN.metall);
      zylinder(sx, sy, 5, 28, FARBEN.metall, 3);
      kasten(sx, sy, 38, 38, 4, FARBEN.stoff, 10, 28);
      kasten(sx, sy + ry * 17, 34, 6, 32, FARBEN.stoff, 3, 32);
    };

    const abstand = tief / 2 + 34;
    for (let i = 0; i < 4; i++) {
      const dx = (i - 1.5) * (laenge / 4.4);
      stuhl(x + dx, ty - abstand, -1);
      stuhl(x + dx, ty + abstand, 1);
    }

    // Screen on the wall: cassette below the ceiling, cloth down to above head height.
    const wy = y - tiefe / 2 + 6;
    const tuch = Math.min(breite - 90, 200);
    kasten(x, wy, tuch + 10, 10, 8, FARBEN.metall, 2, 140);
    kasten(x, wy + 2, tuch, 4, 78, FARBEN.papier, 2, 62);
    // Projector under the ceiling, on a short pipe, lens towards the screen.
    zylinder(x, ty - 20, 3, 14, FARBEN.metall, 136);
    kasten(x, ty - 20, 28, 36, 14, FARBEN.dunkel, 4, 122);
    zylinder(x, ty - 36, 5, 6, FARBEN.schirm, 126);
  },

  // 4 — U shape with whiteboard: three rows of tables open towards the board,
  //     nobody sits with their back to it, and the middle leaves room to stand.
  (x, y, breite, tiefe) => {
    const spanne = Math.max(160, Math.min(breite - 140, 300));
    const laenge = Math.max(120, Math.min(tiefe - 180, 220));
    const br = 65;
    const hinten = y + laenge / 2 - br / 2;
    // A chair needs 36 cm: a short leg carries two seats, a long one three.
    const reihen = laenge >= 190 ? 3 : 2;
    const schritt = (laenge - 60) / (reihen - 1);
    const platzY = (i: number) => y - laenge / 2 + 30 + i * schritt;

    flaeche(x, y, Math.min(breite - 30, spanne + 130), Math.min(tiefe - 40, laenge + 120), FARBEN.teppich, 0.4);

    // Two legs and the connection at the back; the opening faces the board.
    kasten(x - spanne / 2 + br / 2, y, br, laenge, 5, FARBEN.holz, 6, 39);
    kasten(x + spanne / 2 - br / 2, y, br, laenge, 5, FARBEN.holz, 6, 39);
    kasten(x, hinten, spanne - br * 2, br, 5, FARBEN.holz, 6, 39);
    kasten(x - spanne / 2 + br / 2, y - laenge / 2 + 30, 14, 14, 39, FARBEN.metall, 3);
    kasten(x - spanne / 2 + br / 2, hinten, 14, 14, 39, FARBEN.metall, 3);
    kasten(x + spanne / 2 - br / 2, y - laenge / 2 + 30, 14, 14, 39, FARBEN.metall, 3);
    kasten(x + spanne / 2 - br / 2, hinten, 14, 14, 39, FARBEN.metall, 3);

    const stuhl = (sx: number, sy: number, rx: number, ry: number) => {
      zylinder(sx, sy, 18, 3, FARBEN.metall);
      zylinder(sx, sy, 5, 28, FARBEN.metall, 3);
      kasten(sx, sy, 36, 36, 4, FARBEN.stoff, 10, 28);
      const quer = Math.abs(rx) > Math.abs(ry);
      kasten(sx + rx * 16, sy + ry * 16, quer ? 6 : 32, quer ? 32 : 6, 30, FARBEN.stoff, 3, 32);
    };

    for (let i = 0; i < reihen; i++) {
      const dy = platzY(i);
      stuhl(x - spanne / 2 + br / 2 - 52, dy, -1, 0);
      stuhl(x + spanne / 2 - br / 2 + 52, dy, 1, 0);
    }
    stuhl(x - 50, hinten + 52, 0, 1);
    stuhl(x + 50, hinten + 52, 0, 1);

    // Papers at every seat, so the rows read as a seating order.
    for (let i = 0; i < reihen; i++) {
      const dy = platzY(i);
      kasten(x - spanne / 2 + br / 2 - 8, dy, 22, 16, 1, FARBEN.papier, 2, 44);
      kasten(x + spanne / 2 - br / 2 + 8, dy, 22, 16, 1, FARBEN.papier, 2, 44);
    }

    // Whiteboard on castors at the open side: two posts carry the frame,
    // otherwise the board stands in the air above its castors.
    const wy = y - tiefe / 2 + 20;
    const tafel = Math.min(breite - 60, 150);
    zylinder(x - tafel / 2 + 20, wy, 4, 10, FARBEN.metall);
    zylinder(x + tafel / 2 - 20, wy, 4, 10, FARBEN.metall);
    zylinder(x - tafel / 2 + 20, wy, 4, 22, FARBEN.metall, 8);
    zylinder(x + tafel / 2 - 20, wy, 4, 22, FARBEN.metall, 8);
    kasten(x, wy, tafel, 8, 82, FARBEN.metall, 2, 30);
    kasten(x, wy + 5, tafel - 8, 4, 72, FARBEN.papier, 2, 35);
  },

  // 5 — Stand-up meeting: a high table without chairs, because a round held
  //     standing stays short; two stools for those who stay longer after all.
  (x, y, breite, tiefe) => {
    const r = Math.max(42, Math.min(Math.min(breite, tiefe) * 0.18, 60));

    zylinder(x, y, r + 60, 0.6, FARBEN.teppich, 0.2);

    // Bar table: top at 110, slim column, heavy foot against tipping.
    zylinder(x, y, r * 0.62, 5, FARBEN.metall);
    zylinder(x, y, 11, 105, FARBEN.metall, 5);
    zylinder(x, y, r, 5, FARBEN.holz, 105);

    const hocker = (sx: number, sy: number) => {
      zylinder(sx, sy, 17, 3, FARBEN.metall);
      zylinder(sx, sy, 5, 72, FARBEN.metall, 3);
      zylinder(sx, sy, 16, 3, FARBEN.metall, 26);
      zylinder(sx, sy, 18, 5, FARBEN.stoff, 72);
    };
    hocker(x - (r + 34), y - 18);
    hocker(x + (r + 34), y + 18);

    // Mugs and a notepad at standing height, set evenly around the top.
    for (let i = 0; i < 4; i++) {
      const w = (i * Math.PI) / 2 + 0.4;
      zylinder(x + Math.cos(w) * (r * 0.6), y + Math.sin(w) * (r * 0.6), 4, 9, FARBEN.papier, 110);
    }
    kasten(x, y, 26, 18, 1, FARBEN.papier, 2, 110);

    // Narrow wall board at standing height: for writing on, not for putting things down.
    const wy = y - tiefe / 2 + 5;
    const board = Math.min(breite - 70, 170);
    kasten(x, wy, board, 5, 56, FARBEN.papier, 2, 86);
    kasten(x, wy + 4, board, 4, 4, FARBEN.metall, 2, 82);

    // A tall plant as a counterweight to the empty middle of the table, in the
    // corner no stool takes.
    const px = x - Math.min(breite, tiefe) * 0.3;
    const py = y + tiefe * 0.26;
    zylinder(px, py, 16, 30, FARBEN.topf, 0, 13);
    zylinder(px, py, 3, 40, FARBEN.laub, 30);
    kugel(px, py, 26, FARBEN.laub, 60, 0.6);
  },

  // 6 — Small meeting table for four: square, a sideboard against the wall,
  //     because in small rooms the storage otherwise ends up on the table.
  (x, y, breite, tiefe) => {
    // The depth limits it too, otherwise the rear row of chairs stands in the sideboard.
    const seite = Math.max(90, Math.min(breite * 0.38, tiefe * 0.3, 120));
    const ty = y + 24;

    flaeche(x, ty, Math.min(breite - 30, seite + 130), Math.min(tiefe - 110, seite + 130), FARBEN.teppich, 0.4);

    kasten(x, ty, seite, seite, 5, FARBEN.holz, 10, 39);
    const bein = seite / 2 - 14;
    [[-1, -1], [1, -1], [-1, 1], [1, 1]].forEach(([sx, sy]) => {
      zylinder(x + sx * bein, ty + sy * bein, 4, 39, FARBEN.holzDunkel);
    });

    const stuhl = (sx: number, sy: number, rx: number, ry: number) => {
      zylinder(sx, sy, 18, 3, FARBEN.metall);
      zylinder(sx, sy, 5, 28, FARBEN.metall, 3);
      kasten(sx, sy, 36, 36, 4, FARBEN.stoff, 10, 28);
      const quer = Math.abs(rx) > Math.abs(ry);
      kasten(sx + rx * 16, sy + ry * 16, quer ? 6 : 32, quer ? 32 : 6, 30, FARBEN.stoff, 3, 32);
    };
    const d = seite / 2 + 34;
    stuhl(x, ty - d, 0, -1);
    stuhl(x, ty + d, 0, 1);
    stuhl(x - d, ty, -1, 0);
    stuhl(x + d, ty, 1, 0);

    // Four mugs and a pad: the middle of the table stays free for things spread out.
    kasten(x, ty, 30, 22, 1, FARBEN.papier, 2, 44);
    [[-1, -1], [1, -1], [-1, 1], [1, 1]].forEach(([sx, sy]) => {
      zylinder(x + sx * (seite * 0.3), ty + sy * (seite * 0.3), 4, 9, FARBEN.papier, 44);
    });

    // Sideboard on the back wall, handle rail towards the room, on it a plant and
    // a stack of files; the plant stands at the front so it leaves the pinboard free.
    const wy = y - tiefe / 2 + 22;
    const board = Math.min(breite - 80, 150);
    kasten(x, wy, board, 38, 76, FARBEN.holzDunkel, 4);
    kasten(x, wy + 20, board - 12, 4, 3, FARBEN.metall, 2, 50);
    zylinder(x - board / 2 + 26, wy + 4, 11, 16, FARBEN.topf, 76, 9);
    kugel(x - board / 2 + 26, wy + 4, 15, FARBEN.laub, 92, 0.7);
    kasten(x + board / 2 - 32, wy, 26, 20, 5, FARBEN.papier, 2, 76);
    kasten(x + board / 2 - 32, wy, 22, 16, 4, FARBEN.papier, 2, 81);

    // Pinboard above it, narrow, with three notes at equal spacing.
    kasten(x, y - tiefe / 2 + 5, board, 5, 46, FARBEN.grund, 2, 92);
    for (let i = 0; i < 3; i++) {
      kasten(x + (i - 1) * 34, y - tiefe / 2 + 9, 22, 4, 22, FARBEN.papier, 2, 104);
    }
  },
];
