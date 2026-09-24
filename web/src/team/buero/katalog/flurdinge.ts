import { FARBEN, flaeche, kasten, kugel, leuchtstoff, zylinder } from "../mal";

/** A corridor piece, drawn around (x, y); its front faces −y. */
export type Flurding = (x: number, y: number) => void;

/* Corridor and reception: everything that stands between the workplaces and
 * belongs to nobody. The corridor carries a floor's recognisability — hence
 * several kinds: a reception reads differently from a bench, and both tell
 * from above where you are. None of the pieces says anything about an agent.
 *
 * At zoom 1 a centimetre is a third of a pixel. A piece shorter than a good
 * ninety, or grey on the corridor floor, vanishes there — so each one is at
 * least that long, at most fifty deep (the corridor measures 130, the figures
 * walk in the middle) and carries a fixed accent colour as its main surface.
 * Fixed per piece and not per room: the corridor belongs to no department. */
export const FLURDINGE: Flurding[] = [

  // 1 — Reception desk: white body with a coloured front, light strip, screen and plant.
  //     It is the first thing you see of the house, so it alone carries two accents.
  (x, y) => {
    flaeche(x, y, 200, 130, FARBEN.akzent3, 0.5);                  // rug in front of and behind the desk
    kasten(x, y + 10, 170, 56, 100, FARBEN.weiss, 6);              // body
    kasten(x, y - 20, 170, 6, 96, FARBEN.akzent1, 3, 4);           // front towards the visitor
    kasten(x, y - 24, 120, 2, 6, FARBEN.akzent2, 1, 70).material = leuchtstoff(FARBEN.akzent2); // light strip
    kasten(x, y - 6, 184, 50, 6, FARBEN.weiss, 4, 104);            // top, cantilevers towards the visitor
    kasten(x + 30, y + 10, 44, 4, 28, FARBEN.dunkel, 2, 112);      // screen, turned towards the staff
    kasten(x + 30, y + 15, 12, 8, 8, FARBEN.metall, 2, 110);       // its foot
    kasten(x - 36, y - 14, 26, 18, 3, FARBEN.papier, 2, 110);      // forms
    zylinder(x - 72, y + 4, 10, 16, FARBEN.akzent2, 110, 12);      // pot
    kugel(x - 72, y + 4, 16, FARBEN.laub, 124, 0.8);               // plant
  },

  // 2 — Cloakroom: a coloured wall panel, hook rail, coats in four colours, a bench below.
  (x, y) => {
    kasten(x, y + 18, 170, 4, 110, FARBEN.akzent5, 3, 30);         // wall panel
    kasten(x, y + 10, 170, 20, 4, FARBEN.weiss, 3, 136);           // hat shelf
    kasten(x, y + 12, 160, 4, 4, FARBEN.metall, 2, 116);           // hook rail
    const maentel = [FARBEN.akzent1, FARBEN.akzent2, FARBEN.dunkel, FARBEN.akzent4];
    maentel.forEach((farbe, i) => kasten(x - 60 + i * 40, y + 4, 30, 16, 66, farbe, 8, 50));
    kasten(x, y - 4, 170, 34, 5, FARBEN.holzHell, 3, 24);          // bench, the coats hang above it
    kasten(x - 78, y - 4, 6, 30, 24, FARBEN.dunkel, 2);
    kasten(x + 78, y - 4, 6, 30, 24, FARBEN.dunkel, 2);
    kugel(x + 50, y + 10, 9, FARBEN.akzent3, 140, 0.5);            // cap on the shelf
  },

  // 3 — Copier station: a white machine on a blue base cabinet, boxes of paper beside it.
  (x, y) => {
    kasten(x - 20, y + 2, 96, 50, 50, FARBEN.akzent4, 4);          // base cabinet
    kasten(x - 20, y - 23, 88, 3, 38, FARBEN.weiss, 2, 6);         // paper drawer
    kasten(x - 20, y + 2, 88, 48, 30, FARBEN.weiss, 5, 50);        // print unit
    kasten(x - 26, y + 6, 70, 36, 5, FARBEN.dunkel, 3, 80);        // scanner lid
    kasten(x + 12, y - 16, 16, 8, 4, FARBEN.akzent2, 2, 80);       // control panel
    kasten(x - 30, y - 22, 50, 16, 3, FARBEN.papier, 2, 58);       // output tray
    kasten(x + 52, y + 4, 40, 34, 22, FARBEN.akzent2, 2);          // paper boxes, stacked
    kasten(x + 52, y + 4, 36, 32, 22, FARBEN.akzent2, 2, 22);
    kasten(x + 52, y - 13, 20, 1, 8, FARBEN.weiss, 1, 30);         // label
  },

  // 4 — Water bar: turquoise base cabinet, the dispenser with a blue bottle, glasses and a flask.
  (x, y) => {
    kasten(x, y + 2, 120, 44, 80, FARBEN.akzent3, 4);              // base cabinet
    kasten(x, y + 2, 126, 48, 4, FARBEN.weiss, 3, 80);             // top
    kasten(x - 34, y + 8, 34, 30, 44, FARBEN.weiss, 5, 84);        // dispenser
    kasten(x - 34, y - 8, 18, 3, 12, FARBEN.metall, 2, 100);       // taps
    zylinder(x - 34, y + 8, 14, 32, FARBEN.akzent4, 128, 12);      // bottle
    for (let i = 0; i < 3; i++) zylinder(x + 4 + i * 14, y - 6, 5, 10, FARBEN.weiss, 84);
    zylinder(x + 46, y + 8, 7, 26, FARBEN.akzent4, 84);            // flask
  },

  // 5 — Emergency station: fire extinguisher and first-aid kit on a panel, the floor
  //     marking in front. The palette knows no red; coral is the closest to it.
  (x, y) => {
    flaeche(x, y - 8, 110, 40, FARBEN.akzent1, 0.6);               // floor marking
    kasten(x, y + 18, 100, 4, 70, FARBEN.weiss, 3, 40);            // panel
    zylinder(x - 26, y + 4, 10, 52, FARBEN.akzent1, 0);            // fire extinguisher
    kugel(x - 26, y + 4, 10, FARBEN.akzent1, 47, 0.6);
    zylinder(x - 26, y + 4, 3, 8, FARBEN.metall, 53);
    kasten(x - 26, y - 2, 6, 14, 3, FARBEN.dunkel, 1, 58);         // handle
    kasten(x + 24, y + 10, 40, 14, 36, FARBEN.akzent3, 3, 70);     // first-aid kit
    kasten(x + 24, y + 2, 16, 2, 5, FARBEN.weiss, 1, 86);          // cross, horizontal
    kasten(x + 24, y + 2, 5, 2, 16, FARBEN.weiss, 1, 81);          // cross, vertical
  },

  // 6 — Bench in coral: seat cushion and backrest on dark side panels, two cushions.
  (x, y) => {
    kasten(x - 64, y, 8, 44, 26, FARBEN.dunkel, 2);                // side panel left
    kasten(x + 64, y, 8, 44, 26, FARBEN.dunkel, 2);                // side panel right
    kasten(x, y, 160, 48, 8, FARBEN.akzent1, 6, 26);               // seat cushion
    kasten(x, y + 19, 160, 10, 36, FARBEN.akzent1, 5, 30);         // backrest, stands on the seat
    kasten(x - 40, y - 2, 40, 28, 8, FARBEN.akzent2, 8, 34);       // cushion
    kasten(x + 44, y - 2, 36, 28, 8, FARBEN.weiss, 8, 34);
  },

  // 7 — Art on the wall: three white-framed canvases in accents, a gallery bench
  //     in front — it gives the piece from above the surface the pictures lack.
  (x, y) => {
    kasten(x, y + 20, 170, 3, 4, FARBEN.metall, 1, 140);           // rail
    const bilder = [[-58, 40, 50, FARBEN.akzent2], [0, 56, 70, FARBEN.akzent5], [58, 40, 50, FARBEN.akzent4]];
    for (const [dx, b, h, farbe] of bilder) {
      kasten(x + dx, y + 18, b, 5, h, FARBEN.weiss, 2, 130 - h);   // frame
      kasten(x + dx, y + 15, b - 8, 2, h - 8, farbe, 1, 134 - h);  // canvas
    }
    kasten(x, y - 6, 140, 26, 34, FARBEN.holzHell, 3);             // gallery bench
  },

  // 8 — Planter as a room divider: a mustard trough, five plants at three heights so the
  //     silhouette from above does not become a wall.
  (x, y) => {
    kasten(x, y, 160, 40, 40, FARBEN.akzent2, 8);                  // trough
    kasten(x, y, 164, 44, 4, FARBEN.weiss, 4, 38);                 // edge strip
    flaeche(x, y, 150, 30, FARBEN.dunkel, 42.2);                   // soil, lies above the strip
    const pflanzen = [[-62, 3, 34, 14], [-30, -5, 22, 11], [2, 4, 40, 16], [34, -4, 26, 12], [64, 3, 30, 13]];
    for (const [dx, dy, hoehe, blatt] of pflanzen) {
      zylinder(x + dx, y + dy, 2.5, hoehe, FARBEN.holzDunkel, 40);
      kugel(x + dx, y + dy, blatt, FARBEN.laub, 40 + hoehe, 0.7);
      kugel(x + dx + blatt * 0.4, y + dy + 4, blatt * 0.6, FARBEN.laub, 38 + hoehe, 0.6);
    }
  },

  // 9 — Waste separation: three coloured bins in front of a white board; the colour is the kind.
  (x, y) => {
    kasten(x, y + 20, 140, 5, 90, FARBEN.weiss, 3);                // back board
    const tonnen = [[-46, FARBEN.akzent3], [0, FARBEN.akzent2], [46, FARBEN.akzent4]];
    for (const [dx, farbe] of tonnen) {
      zylinder(x + dx, y - 2, 19, 56, farbe, 0);                   // bin
      kugel(x + dx, y - 2, 18, FARBEN.weiss, 56, 0.3);             // domed lid
      kasten(x + dx, y - 2, 18, 3, 3, FARBEN.dunkel, 1, 61);       // slot
      kasten(x + dx, y + 16, 26, 2, 12, farbe, 2, 70);             // sign on the board
    }
  },

  // 10 — Bike rack: three bikes parallel to the wall, the middle one set back so the
  //      handlebars do not catch each other. From above they read by frame and handlebar.
  (x, y) => {
    kasten(x, y + 20, 190, 4, 4, FARBEN.metall, 2);                // floor rail
    kasten(x, y + 20, 190, 4, 4, FARBEN.metall, 2, 50);            // leaning bar on the wall
    const rad = (rx: number, ry: number, farbe: number) => {
      for (const dx of [-24, 24]) zylinder(rx + dx, ry, 14, 3, FARBEN.dunkel, 12.5).rotation.x = Math.PI / 2;
      kasten(rx, ry, 44, 4, 4, farbe, 2, 30);                      // top tube
      kasten(rx - 4, ry, 36, 4, 4, farbe, 2, 16);                  // down tube
      kasten(rx - 16, ry, 16, 7, 3, FARBEN.dunkel, 2, 38);         // saddle
      kasten(rx + 22, ry, 4, 22, 3, FARBEN.dunkel, 2, 40);         // handlebar, crosswise
    };
    rad(x - 56, y - 10, FARBEN.akzent1);
    rad(x, y + 8, FARBEN.akzent3);
    rad(x + 56, y - 10, FARBEN.akzent5);
  },

  // 11 — Locker wall: six columns of lockers, each column in its own colour.
  (x, y) => {
    kasten(x, y, 172, 40, 6, FARBEN.dunkel, 2);                    // plinth
    kasten(x, y, 172, 40, 116, FARBEN.weiss, 3, 6);                // body
    const farben = [FARBEN.akzent1, FARBEN.akzent2, FARBEN.akzent3, FARBEN.akzent4, FARBEN.akzent5, FARBEN.akzent1];
    const tb = 166 / farben.length;
    for (let sp = 0; sp < farben.length; sp++) {
      for (let r = 0; r < 2; r++) {
        kasten(x - 83 + tb * (sp + 0.5), y - 20, tb - 3, 3, 52, farben[sp], 2, 10 + r * 56);
      }
    }
    kasten(x, y, 176, 44, 4, FARBEN.weiss, 2, 122);                // cover
  },

  // 12 — Upholstered bench against the wall: wooden base, blue cushion, back cushions in four colours.
  (x, y) => {
    kasten(x, y + 2, 186, 42, 24, FARBEN.holzHell, 4);             // base
    kasten(x, y + 2, 186, 42, 8, FARBEN.akzent4, 6, 24);           // seat cushion
    kasten(x, y + 19, 186, 10, 40, FARBEN.akzent4, 5, 30);         // back cushion against the wall
    const kissen = [FARBEN.akzent1, FARBEN.akzent2, FARBEN.weiss, FARBEN.akzent5];
    kissen.forEach((farbe, i) => kasten(x - 66 + i * 44, y + 8, 30, 12, 26, farbe, 6, 32));
  },

  // 13 — Trellis with hanging plants: a tall wooden frame above a trough, three pots
  //      hanging at different heights. The tallest corridor piece, from above a green strip.
  (x, y) => {
    kasten(x, y + 4, 150, 34, 30, FARBEN.akzent3, 6);              // trough
    flaeche(x, y + 4, 140, 24, FARBEN.dunkel, 30.2);               // soil
    kasten(x - 72, y + 14, 5, 5, 176, FARBEN.holzHell, 2);         // posts
    kasten(x + 72, y + 14, 5, 5, 176, FARBEN.holzHell, 2);
    kasten(x, y + 14, 150, 5, 5, FARBEN.holzHell, 2, 172);         // cross beam
    for (const [dx, h] of [[-44, 132], [4, 110], [48, 140]]) {
      kasten(x + dx, y + 12, 1.5, 1.5, 172 - h - 10, FARBEN.metall, 0.5, h + 10); // hanger
      zylinder(x + dx, y + 12, 8, 10, FARBEN.weiss, h);            // pot
      kugel(x + dx, y + 10, 13, FARBEN.laub, h - 6, 0.8);          // foliage, hangs over the pot
    }
    kugel(x - 40, y + 4, 16, FARBEN.laub, 26, 0.7);
    kugel(x + 30, y + 2, 18, FARBEN.laub, 26, 0.8);
  },

  // 14 — Mural: a violet wall surface with three simple shapes, a neon lettering bar
  //      above. The colour strip on the floor is what you see of it from above.
  (x, y) => {
    flaeche(x, y - 8, 180, 26, FARBEN.akzent5, 0.6);               // colour strip on the floor
    kasten(x, y + 18, 190, 4, 120, FARBEN.akzent5, 2, 10);         // wall surface
    zylinder(x - 50, y + 15, 28, 3, FARBEN.akzent1, 68.5).rotation.x = Math.PI / 2; // circle
    kasten(x + 10, y + 15, 36, 3, 90, FARBEN.akzent2, 2, 20);      // bar
    kasten(x + 60, y + 15, 50, 3, 50, FARBEN.akzent3, 2, 70);      // square
    kasten(x, y + 14, 150, 3, 6, FARBEN.weiss, 3, 138).material = leuchtstoff(FARBEN.akzent1); // neon lettering
  },
];

/** Everything except the reception desk: the pieces that stand against a corridor wall. */
export const FLUR_WAND = FLURDINGE.slice(1);
