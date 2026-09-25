import { akzent, FARBEN, flaeche, kasten, kugel, zylinder } from "../mal";

/* Part of the furniture catalogue (see the module comment in pflanzen.ts):
 * every piece is a pure function drawing into a footprint of breite × tiefe
 * centred on (x, y). */

export type Schrank = (x: number, y: number, b: number, t: number) => void;

// Cabinets and shelves are the standing furniture at the edge of a room: they close off
// the wall and give the room its use. There are several kinds, because a filing corridor
// looks different from a library and a server room different again — the silhouette
// from above tells them apart: closed block, open compartments, legs, castors.
// All get the same footprint and turn their front to -y, towards the room.
export const SCHRAENKE: Schrank[] = [

  // 1 — Filing cabinet: four drawers on a recessed plinth, so that the body reads as one
  // block from above and as a stack from the front.
  (x, y, breite, tiefe) => {
    const hoehe = 98;
    kasten(x, y, breite - 8, tiefe - 6, 5, FARBEN.dunkel, 2);
    kasten(x, y, breite, tiefe, hoehe, FARBEN.metall, 4, 5);
    const fach = (hoehe - 8) / 4;
    for (let i = 0; i < 4; i++) {
      const unten = 9 + i * fach;
      kasten(x, y - tiefe / 2 + 1, breite - 8, 3, fach - 3, FARBEN.grund, 2, unten);
      kasten(x, y - tiefe / 2 - 1, breite * 0.34, 3, 3, FARBEN.metall, 2, unten + fach - 12);
    }
    kasten(x, y, breite + 3, tiefe + 3, 3, FARBEN.holzDunkel, 2, 5 + hoehe);
  },

  // 2 — Open shelf with binders: two side panels, three shelves, coloured spines between.
  // The shelf spacing of 34 is the height of a binder plus room to grip, otherwise the row
  // would stand in the shelf above; the colour sequence is fixed so that the same shelf
  // always carries the same binders.
  (x, y, breite, tiefe) => {
    const hoehe = 108;
    const rueckenFarben = [akzent(), FARBEN.laub, FARBEN.weiss, akzent(1), FARBEN.topf];
    kasten(x - breite / 2 + 2, y, 4, tiefe, hoehe, FARBEN.holz, 2);
    kasten(x + breite / 2 - 2, y, 4, tiefe, hoehe, FARBEN.holz, 2);
    kasten(x, y + tiefe / 2 - 1.5, breite, 3, hoehe, FARBEN.holzDunkel, 2);
    const boeden = 3;
    const abstand = (hoehe - 6) / boeden;
    for (let b = 0; b < boeden; b++) {
      const bodenY = b * abstand;
      kasten(x, y, breite - 8, tiefe - 6, 3, FARBEN.holz, 2, bodenY);
      const platz = breite - 14;
      const anzahl = Math.max(3, Math.floor(platz / 7));
      for (let i = 0; i < anzahl; i++) {
        const ordnerHoehe = 22 + ((b * 5 + i * 3) % 4) * 2;
        kasten(
          x - platz / 2 + 3.5 + i * (platz / anzahl),
          y + 1,
          5,
          tiefe - 8,
          ordnerHoehe,
          rueckenFarben[(b + i) % rueckenFarben.length],
          2,
          bodenY + 3
        );
      }
    }
    kasten(x, y, breite, tiefe, 4, FARBEN.holzDunkel, 2, hoehe - 4);
  },

  // 3 — Sideboard: low, on slender legs, with an overhanging top — stands free in the
  // room and lets the floor run through underneath.
  (x, y, breite, tiefe) => {
    const beinHoehe = 16;
    const korpus = 52;
    const bx = breite / 2 - 6;
    const bt = tiefe / 2 - 5;
    zylinder(x - bx, y - bt, 2.2, beinHoehe, FARBEN.metall);
    zylinder(x + bx, y - bt, 2.2, beinHoehe, FARBEN.metall);
    zylinder(x - bx, y + bt, 2.2, beinHoehe, FARBEN.metall);
    zylinder(x + bx, y + bt, 2.2, beinHoehe, FARBEN.metall);
    kasten(x, y, breite - 6, tiefe - 4, korpus, FARBEN.holz, 4, beinHoehe);
    for (let i = 0; i < 2; i++) {
      const tuerBreite = (breite - 12) / 2;
      kasten(
        x - tuerBreite / 2 - 1 + i * (tuerBreite + 2),
        y - tiefe / 2 + 3,
        tuerBreite,
        3,
        korpus - 8,
        FARBEN.grund,
        2,
        beinHoehe + 4
      );
    }
    kasten(x, y - tiefe / 2 + 1.5, 3, 3, 12, FARBEN.metall, 2, beinHoehe + korpus / 2 - 6);
    kasten(x, y, breite, tiefe, 3, FARBEN.holzDunkel, 3, beinHoehe + korpus);
    kasten(x - breite / 4, y, 18, 13, 5, FARBEN.papier, 2, beinHoehe + korpus + 3);
  },

  // 4 — Server rack: a dark steel body, perforated panels in a grid, a vented roof on top.
  // The tight horizontal stripe pattern makes it unmistakable from above.
  (x, y, breite, tiefe) => {
    const hoehe = 110;
    kasten(x, y, breite, tiefe, 6, FARBEN.dunkel, 2);
    kasten(x, y, breite, tiefe, hoehe - 12, FARBEN.dunkel, 3, 6);
    kasten(x - breite / 2 + 2, y - tiefe / 2 + 2, 4, 4, hoehe - 12, FARBEN.metall, 2, 6);
    kasten(x + breite / 2 - 2, y - tiefe / 2 + 2, 4, 4, hoehe - 12, FARBEN.metall, 2, 6);
    const blenden = 7;
    const raster = (hoehe - 24) / blenden;
    for (let i = 0; i < blenden; i++) {
      kasten(x, y - tiefe / 2 + 1, breite - 10, 3, raster - 3, FARBEN.metall, 2, 12 + i * raster);
      kasten(x, y - tiefe / 2 - 1, breite - 22, 3, 3, FARBEN.grund, 2, 12 + i * raster + 2);
    }
    kasten(x, y, breite + 2, tiefe + 2, 6, FARBEN.metall, 2, hoehe - 6);
  },

  // 5 — Tall cupboard with doors: two full leaves, vertical handle bars, a cornice as the
  // top. The closed surface is the quietest counterpart to the open shelf.
  (x, y, breite, tiefe) => {
    const hoehe = 110;
    kasten(x, y, breite, tiefe, hoehe - 6, FARBEN.holz, 3, 3);
    kasten(x, y, breite - 10, tiefe - 6, 3, FARBEN.dunkel, 2);
    const tuerBreite = (breite - 7) / 2;
    for (let i = 0; i < 2; i++) {
      const tx = x - tuerBreite / 2 - 1.5 + i * (tuerBreite + 3);
      kasten(tx, y - tiefe / 2 + 1, tuerBreite, 3, hoehe - 16, FARBEN.grund, 2, 8);
      kasten(
        tx + (i === 0 ? tuerBreite / 2 - 4 : -tuerBreite / 2 + 4),
        y - tiefe / 2 - 1,
        3,
        3,
        26,
        FARBEN.metall,
        2,
        hoehe / 2 - 8
      );
    }
    kasten(x, y, breite + 4, tiefe + 4, 5, FARBEN.holzDunkel, 2, hoehe - 3);
  },

  // 6 — Rolling shelf: three shelves between four posts on castors, deeper than tall. The
  // shelves end at the posts so the rug beneath stays visible; the rug keeps to the
  // footprint and does not reach into the neighbouring spot.
  (x, y, breite, tiefe) => {
    const hoehe = 76;
    flaeche(x, y, breite, tiefe, FARBEN.teppich);
    const px = breite / 2 - 4;
    const pt = tiefe / 2 - 4;
    const rollenHoehe = 6;
    kugel(x - px, y - pt, 4, FARBEN.dunkel, 0, 0.75);
    kugel(x + px, y - pt, 4, FARBEN.dunkel, 0, 0.75);
    kugel(x - px, y + pt, 4, FARBEN.dunkel, 0, 0.75);
    kugel(x + px, y + pt, 4, FARBEN.dunkel, 0, 0.75);
    zylinder(x - px, y - pt, 2, hoehe, FARBEN.metall, rollenHoehe);
    zylinder(x + px, y - pt, 2, hoehe, FARBEN.metall, rollenHoehe);
    zylinder(x - px, y + pt, 2, hoehe, FARBEN.metall, rollenHoehe);
    zylinder(x + px, y + pt, 2, hoehe, FARBEN.metall, rollenHoehe);
    for (let i = 0; i < 3; i++) {
      const bodenY = rollenHoehe + 6 + i * ((hoehe - 12) / 3);
      kasten(x, y, breite - 12, tiefe - 12, 3, FARBEN.holz, 2, bodenY);
      if (i < 2) {
        kasten(x - breite / 5, y, breite / 3, tiefe - 18, 14, akzent(), 2, bodenY + 3);
        kasten(x + breite / 4, y, breite / 4, tiefe - 20, 9, FARBEN.papier, 2, bodenY + 3);
      }
    }
    kasten(x, y - pt, breite - 8, 4, 4, FARBEN.metall, 2, rollenHoehe + hoehe - 8);
  },

  // 7 — Bookcase wall: wide, divided into compartments, every compartment filled. The book
  // heights follow a fixed pattern so the wall looks alive but is the same on every visit.
  (x, y, breite, tiefe) => {
    const hoehe = 112;
    const muster = [26, 22, 28, 24, 20, 27, 23, 25];
    const buchFarben = [akzent(), FARBEN.holzDunkel, akzent(1), FARBEN.laub, akzent(2), FARBEN.papier];
    kasten(x, y + tiefe / 2 - 1.5, breite, 3, hoehe, FARBEN.holzDunkel, 2);
    const faecher = Math.max(2, Math.round(breite / 42));
    const fachBreite = breite / faecher;
    for (let f = 0; f <= faecher; f++) {
      kasten(x - breite / 2 + f * fachBreite, y, 3, tiefe, hoehe, FARBEN.holz, 2);
    }
    const boeden = 3;
    for (let b = 0; b < boeden; b++) {
      const bodenY = 4 + b * ((hoehe - 8) / boeden);
      kasten(x, y, breite, tiefe - 2, 3, FARBEN.holz, 2, bodenY);
      for (let f = 0; f < faecher; f++) {
        const mitte = x - breite / 2 + fachBreite * (f + 0.5);
        const platz = fachBreite - 8;
        const anzahl = Math.max(4, Math.floor(platz / 5));
        for (let i = 0; i < anzahl; i++) {
          const k = b * 3 + f * 2 + i;
          kasten(
            mitte - platz / 2 + 2 + i * (platz / anzahl),
            y - 1,
            4,
            tiefe - 9,
            muster[k % muster.length],
            buchFarben[k % buchFarben.length],
            2,
            bodenY + 3
          );
        }
      }
    }
    kasten(x, y, breite + 3, tiefe + 2, 4, FARBEN.holzDunkel, 2, hoehe);
  },

  // 8 — Wardrobe: closed side sections, in the middle an open niche with a rail, coats and a
  // hat shelf — recognisable from above by the gap in the front. The niche floor runs all
  // the way down to the ground; it carries the box.
  (x, y, breite, tiefe) => {
    const hoehe = 104;
    const seite = breite * 0.26;
    kasten(x - breite / 2 + seite / 2, y, seite, tiefe, hoehe, FARBEN.holz, 3);
    kasten(x + breite / 2 - seite / 2, y, seite, tiefe, hoehe, FARBEN.holz, 3);
    kasten(x, y + tiefe / 2 - 1.5, breite, 3, hoehe, FARBEN.holzDunkel, 2);
    kasten(x, y, breite, tiefe, 6, FARBEN.holzDunkel, 2, hoehe - 6);
    const nische = breite - 2 * seite;
    kasten(x, y, nische, tiefe - 4, 10, FARBEN.holz, 2);
    kasten(x, y, nische, tiefe - 4, 3, FARBEN.holz, 2, hoehe - 26);
    kasten(x, y, nische - 4, 3, 3, FARBEN.metall, 2, hoehe - 34);
    for (let i = 0; i < 3; i++) {
      const mx = x - nische / 2 + nische * (i + 1) / 4;
      kasten(mx, y, 11, tiefe - 12, 44, [akzent(), akzent(1), FARBEN.dunkel][i], 3, hoehe - 80);
      kasten(mx, y, 4, 4, 6, FARBEN.metall, 2, hoehe - 38);
    }
    kugel(x - nische / 4, y, 8, akzent(), hoehe - 23, 0.45);
    kasten(x + nische / 5, y, 16, 9, 7, FARBEN.holzDunkel, 2, 10);
  },

  // 9 — Lockers: eight identical doors in a grid of two, each with a handle and a label.
  // The floor marking in front keeps clear the area into which the doors swing.
  (x, y, breite, tiefe) => {
    const hoehe = 108;
    const tuerFarben = [akzent(), FARBEN.weiss, akzent(), FARBEN.weiss];
    flaeche(x, y - tiefe / 2 - 12, breite, 22, FARBEN.flur);
    kasten(x, y, breite, tiefe, 5, FARBEN.dunkel, 2);
    kasten(x, y, breite, tiefe, hoehe - 5, FARBEN.metall, 2, 5);
    const spalten = 2;
    const reihen = 4;
    const tb = (breite - 6) / spalten;
    const th = (hoehe - 12) / reihen;
    for (let s = 0; s < spalten; s++) {
      for (let r = 0; r < reihen; r++) {
        const tx = x - breite / 2 + 3 + tb * (s + 0.5);
        const ty = 8 + r * th;
        kasten(tx, y - tiefe / 2 + 1, tb - 3, 3, th - 3, tuerFarben[(s + r) % tuerFarben.length], 2, ty);
        kasten(tx + tb / 2 - 6, y - tiefe / 2 - 1, 3, 3, 8, FARBEN.metall, 2, ty + th / 2 - 8);
        kasten(tx - tb / 4, y - tiefe / 2 - 1, tb / 3, 3, 4, FARBEN.papier, 2, ty + th - 12);
      }
    }
    kasten(x, y, breite + 2, tiefe + 2, 4, FARBEN.dunkel, 2, hoehe - 4);
  },

  // 10 — Low cabinet with a plant: sliding doors on two planes, a pot with foliage on top.
  // The silhouette from above is a rectangle plus a round bush.
  (x, y, breite, tiefe) => {
    const hoehe = 80;
    kasten(x, y, breite - 6, tiefe - 6, 4, FARBEN.dunkel, 2);
    kasten(x, y, breite, tiefe, hoehe - 8, FARBEN.holzDunkel, 3, 4);
    const tuerBreite = (breite - 6) / 2;
    kasten(x - tuerBreite / 2, y - tiefe / 2 + 2.5, tuerBreite, 3, hoehe - 20, FARBEN.holz, 2, 10);
    kasten(x + tuerBreite / 2 - 2, y - tiefe / 2 + 0.5, tuerBreite, 3, hoehe - 20, FARBEN.grund, 2, 10);
    kasten(x + tuerBreite / 2 - 2, y - tiefe / 2 - 1, tuerBreite / 3, 3, 4, FARBEN.metall, 2, hoehe / 2 - 2);
    kasten(x, y, breite + 4, tiefe + 4, 4, FARBEN.holz, 2, hoehe - 4);
    const topfX = x + breite / 2 - 16;
    zylinder(topfX, y, 9, 14, FARBEN.topf, hoehe, 11);
    zylinder(topfX, y, 1.6, 12, FARBEN.holzDunkel, hoehe + 14);
    kugel(topfX, y, 11, FARBEN.laub, hoehe + 20, 0.8);
    kugel(topfX - 7, y + 3, 6, FARBEN.laub, hoehe + 16, 0.7);
    kugel(topfX + 6, y - 4, 5, FARBEN.laub, hoehe + 18, 0.7);
  },

  // 11 — Copier: a print engine on the paper-tray base, on top the lid with the control
  // panel. Whatever width is left over is filled with paper boxes.
  (x, y, breite, tiefe) => {
    const w = Math.min(breite - 6, 78);
    const kx = x - breite / 2 + w / 2 + 3;
    kasten(kx, y, w, tiefe - 2, 8, FARBEN.dunkel, 2);
    kasten(kx, y, w, tiefe - 2, 42, FARBEN.metall, 3, 8);
    for (let i = 0; i < 2; i++) {
      kasten(kx, y - tiefe / 2 + 2, w - 8, 3, 16, FARBEN.gemein, 2, 11 + i * 19);
    }
    kasten(kx, y, w, tiefe - 2, 14, FARBEN.gemein, 3, 50);
    kasten(kx - w * 0.1, y + 2, w * 0.6, tiefe - 10, 3, FARBEN.dunkel, 2, 64);
    kasten(kx + w / 2 - 12, y - tiefe / 2 + 7, 16, 9, 4, FARBEN.schirm, 2, 64);
    kasten(kx - w * 0.1, y, w * 0.4, tiefe - 14, 1, FARBEN.papier, 1, 50);
    const rest = breite - w - 8;
    if (rest > 24) {
      const rx = x + breite / 2 - rest / 2 - 2;
      kasten(rx, y, rest - 2, tiefe - 6, 18, FARBEN.holzDunkel, 2);
      kasten(rx, y, rest - 6, tiefe - 8, 18, FARBEN.holzDunkel, 2, 18);
      kasten(rx, y - tiefe / 2 + 3, rest / 2, 1, 6, FARBEN.papier, 1, 24);
    }
  },

  // 12 — Whiteboard on castors: the board stands upright on two feet. From above a stroke
  // between two crossbars remains — the emptiest silhouette in the catalogue.
  (x, y, breite, tiefe) => {
    const fx = breite / 2 - 5;
    const ft = tiefe / 2 - 4;
    for (const s of [-1, 1]) {
      kasten(x + s * fx, y, 5, tiefe - 4, 4, FARBEN.metall, 2, 4);
      kugel(x + s * fx, y - ft, 3, FARBEN.dunkel, 0, 1);
      kugel(x + s * fx, y + ft, 3, FARBEN.dunkel, 0, 1);
      kasten(x + s * fx, y, 4, 4, 100, FARBEN.metall, 2, 8);
    }
    kasten(x, y, breite - 14, 3, 62, FARBEN.weiss, 2, 40);
    kasten(x, y, breite - 12, 5, 3, FARBEN.metall, 2, 102);
    // The pen tray sticks out to the front so that it shows beside the board from above.
    kasten(x, y - 5, breite * 0.4, 7, 2, FARBEN.metall, 1, 40);
    kasten(x - breite * 0.2, y - 2, breite * 0.25, 1, 3, akzent(), 1, 80);
    kasten(x + breite * 0.1, y - 2, breite * 0.3, 1, 3, akzent(1), 1, 70);
    kasten(x - breite * 0.05, y - 2, breite * 0.2, 1, 3, akzent(2), 1, 58);
  },

  // 13 — Open coat rack: a hook rail on the wall, coats beneath it and a bench with shoes.
  // Unlike the wardrobe it has no sides; everything hangs free.
  (x, y, breite, tiefe) => {
    const hinten = y + tiefe / 2;
    kasten(x, hinten - 2, breite - 4, 4, 40, FARBEN.holz, 2, 66);
    kasten(x, hinten - 12, breite - 4, 20, 3, FARBEN.holz, 2, 104);
    kasten(x, hinten - 6, breite - 10, 3, 3, FARBEN.metall, 1, 96);
    const farben = [akzent(), FARBEN.dunkel, akzent(1), akzent(2)];
    const anzahl = Math.max(2, Math.floor(breite / 30));
    for (let i = 0; i < anzahl; i++) {
      const mx = x - breite / 2 + breite * (i + 0.5) / anzahl;
      kasten(mx, hinten - 11, 18, 14, 50, farben[i % farben.length], 6, 46);
    }
    kasten(x - breite / 2 + 6, y, 4, tiefe - 4, 22, FARBEN.holzDunkel, 2);
    kasten(x + breite / 2 - 6, y, 4, tiefe - 4, 22, FARBEN.holzDunkel, 2);
    kasten(x, y, breite - 4, tiefe - 4, 4, FARBEN.holz, 2, 22);
    kasten(x - breite / 6, y - 4, 12, 22, 5, FARBEN.dunkel, 4, 0);
    kasten(x + breite / 8, y - 4, 12, 22, 5, FARBEN.dunkel, 4, 0);
    kugel(x + breite / 3, hinten - 12, 7, FARBEN.dunkel, 107, 0.5);
  },

  // 14 — Plant shelf: three shelves full of pots, the front leaves hang over the edge.
  // From above a row of green circles no other cabinet has.
  (x, y, breite, tiefe) => {
    const hoehe = 96;
    kasten(x - breite / 2 + 2, y, 4, tiefe, hoehe, FARBEN.holzDunkel, 2);
    kasten(x + breite / 2 - 2, y, 4, tiefe, hoehe, FARBEN.holzDunkel, 2);
    const anzahl = Math.max(2, Math.floor(breite / 38));
    for (let b = 0; b < 3; b++) {
      const bodenY = 4 + b * 34;
      kasten(x, y, breite - 4, tiefe - 2, 3, FARBEN.holz, 2, bodenY);
      for (let i = 0; i < anzahl; i++) {
        const px = x - breite / 2 + breite * (i + 0.5) / anzahl + ((b + i) % 2 ? 4 : -4);
        zylinder(px, y + 2, 8, 12, FARBEN.topf, bodenY + 3, 9);
        kugel(px, y - 2, 11, FARBEN.laub, bodenY + 13, 0.6 + ((b + i) % 3) * 0.2);
      }
    }
  },

  // 15 — Sideboard with coffee machine: a base cabinet, on it the machine, cups and a tin.
  // People stop here, which is why it is lower than any filing cabinet.
  (x, y, breite, tiefe) => {
    const korpus = 48;
    kasten(x, y, breite - 6, tiefe - 6, 4, FARBEN.dunkel, 2);
    kasten(x, y, breite, tiefe, korpus - 4, FARBEN.gemein, 3, 4);
    const tuerBreite = (breite - 8) / 2;
    kasten(x - tuerBreite / 2 - 1, y - tiefe / 2 + 1, tuerBreite, 3, korpus - 12, FARBEN.holz, 2, 8);
    kasten(x + tuerBreite / 2 + 1, y - tiefe / 2 + 1, tuerBreite, 3, korpus - 12, FARBEN.holz, 2, 8);
    kasten(x, y, breite + 2, tiefe + 2, 3, FARBEN.holzDunkel, 2, korpus);
    const mx = x - breite / 2 + 20;
    kasten(mx, y + 3, 26, 26, 34, FARBEN.dunkel, 4, korpus + 3);
    kasten(mx, y - 9, 18, 6, 3, FARBEN.metall, 1, korpus + 3);
    zylinder(mx, y + 6, 7, 8, FARBEN.dunkel, korpus + 37, 5);
    // The cups stand in a row beside the machine, not scattered — whoever fetches one
    // always reaches for the same spot.
    for (let i = 0; i < 3; i++) {
      zylinder(mx + 26 + i * 11, y - 6, 4, 7, FARBEN.papier, korpus + 3);
    }
    zylinder(x + breite / 2 - 14, y + 8, 7, 16, FARBEN.metall, korpus + 3);
  },

  // 16 — Drawer wall: a grid of small fronts in alternating tones. The rhythm makes it a
  // grid rather than a surface, from the side as from above.
  (x, y, breite, tiefe) => {
    const hoehe = 96;
    kasten(x, y, breite - 6, tiefe - 6, 5, FARBEN.dunkel, 2);
    kasten(x, y, breite, tiefe, hoehe - 5, FARBEN.holzDunkel, 3, 5);
    const spalten = Math.max(3, Math.round(breite / 26));
    const reihen = 4;
    const fb = (breite - 6) / spalten;
    const fh = (hoehe - 12) / reihen;
    const toene = [FARBEN.holzHell, FARBEN.weiss, akzent(), FARBEN.holzHell, akzent(1)];
    for (let s = 0; s < spalten; s++) {
      for (let r = 0; r < reihen; r++) {
        kasten(x - breite / 2 + 3 + fb * (s + 0.5), y - tiefe / 2 + 1, fb - 3, 3, fh - 3,
          toene[(s * 2 + r * 3) % toene.length], 2, 8 + r * fh);
      }
    }
    kasten(x, y, breite + 2, tiefe + 2, 4, FARBEN.holz, 2, hoehe);
  },

  // 17 — Cube shelf with boxes: an open grid, some compartments with fabric boxes, others
  // empty. The gaps are deliberate — a fully stocked grid reads as a block.
  (x, y, breite, tiefe) => {
    const hoehe = 102;
    const spalten = Math.max(2, Math.round(breite / 42));
    const reihen = 3;
    const zb = breite / spalten;
    const zh = (hoehe - 3) / reihen;
    for (let s = 0; s <= spalten; s++) {
      kasten(Math.min(x + breite / 2 - 1.5, Math.max(x - breite / 2 + 1.5, x - breite / 2 + s * zb)),
        y, 3, tiefe, hoehe, FARBEN.holz, 1);
    }
    for (let r = 0; r <= reihen; r++) {
      kasten(x, y, breite, tiefe, 3, FARBEN.holz, 1, r * zh);
    }
    const boxen = [akzent(), FARBEN.weiss, akzent(1)];
    for (let s = 0; s < spalten; s++) {
      for (let r = 0; r < reihen; r++) {
        if ((s + r * 2) % 3 === 1) continue;
        const bx = x - breite / 2 + zb * (s + 0.5);
        kasten(bx, y, zb - 8, tiefe - 6, zh - 8, boxen[(s + r) % boxen.length], 3, r * zh + 3);
      }
    }
  },

  // 18 — Pinboard on a stand: a cork surface between two feet, on it a map and notes in
  // four colours. It stands free, which is why it carries its own feet.
  (x, y, breite, tiefe) => {
    const fx = breite / 2 - 5;
    kasten(x - fx, y, 5, tiefe - 4, 4, FARBEN.dunkel, 2);
    kasten(x + fx, y, 5, tiefe - 4, 4, FARBEN.dunkel, 2);
    kasten(x - fx, y, 4, 4, 104, FARBEN.dunkel, 2, 4);
    kasten(x + fx, y, 4, 4, 104, FARBEN.dunkel, 2, 4);
    kasten(x, y, breite - 14, 4, 60, akzent(), 2, 44);
    kasten(x, y, breite - 12, 5, 3, FARBEN.dunkel, 2, 104);
    kasten(x - breite * 0.14, y - 2.5, breite * 0.4, 1, 34, FARBEN.bodenHell, 1, 58);
    const zettel = [FARBEN.papier, akzent(1), akzent(2), FARBEN.weiss];
    for (let i = 0; i < 4; i++) {
      kasten(x + breite * 0.18 + (i % 2) * 14, y - 2.5, 11, 1, 11, zettel[i], 1, 52 + Math.floor(i / 2) * 22 + (i % 2) * 4);
    }
  },
];
