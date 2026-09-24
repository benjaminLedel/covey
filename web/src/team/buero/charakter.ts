import * as THREE from "three";
import { FARBEN, M, akzentFuer, anhaengen, flaeche, inGruppe, kasten, mat, zylinder } from "./mal";
import { hash, sorte } from "./mathe";
import type { Raum } from "./typen";
import { SCHILD_H, suche, type Belegung } from "./plan";
import { PFLANZEN } from "./katalog/pflanzen";
import { SCHRAENKE } from "./katalog/schraenke";
import { GRUPPEN } from "./katalog/gruppen";
import { LEUCHTEN } from "./katalog/leuchten";

/* ── The character of a department room ─────────────────────────────────────
 * Every department room gets its furniture from its NAME: a support team sits
 * among phone booths and pin boards, finance among folders, operations among
 * racks. The keyword table below maps a name to one of eleven characters; each
 * character is a recipe.
 *
 * The first attempt furnished only the core and left the rest empty. A room for
 * eight on sixteen metres then had two grey cabinets against the wall and
 * otherwise floor — that does not read as calm, it reads as unoccupied. Hence a
 * second stage, the filling: after the core the room measures how much floor is
 * still free and places pieces from the character's ranked list until it is used
 * up — standing table, sofa corner, phone booth, plant island, table football.
 *
 * What the character is NOT stays important: information about the agents. A
 * lab does not mean something is running there, and a full lounge does not mean
 * it is break time. State is carried by the screen and the room light alone; the
 * furniture hangs on the name, and the name does not change when someone falls
 * asleep. */

/* Order matters: "Customer Support Engineering" is support first. The words come
   from real org charts, German and English mixed, because that is how
   departments are named. */
const CHARAKTER_WORTE = [
  [/support|kundenservice|kundendienst|service ?desk|helpdesk|customer|hotline/, "callcenter"],
  [/vertrieb|sales|verkauf|key account|account manage|akquise|business dev/, "kontor"],
  [/betrieb|infrastruktur|operations|(^|[^a-z])ops|plattform|platform|rechenzentrum|netzwerk|network|security|sicherheit|devops|sre/, "labor"],
  [/entwicklung|engineering|software|develop|technik|tech|(^|[^a-z])it([^a-z]|$)|qualität|qualitaet|(^|[^a-z])qa([^a-z]|$)|test/, "werkstatt"],
  [/finanz|finance|controlling|buchhaltung|accounting|recht|legal|compliance|datenschutz|privacy|steuer|tax|revision|audit|einkauf|procurement/, "kanzlei"],
  [/produkt|product|growth|marketing|design|people|innovation|venture|startup/, "startup"],
  [/kreativ|creative|grafik|content|redaktion|editorial|(^|[^a-z])ux([^a-z]|$)/, "atelier"],
  [/kultur|culture|personal|(^|[^a-z])hr([^a-z]|$)|human|talent|kommunikation|communication|brand|events?/, "studio"],
  [/forschung|research|wissen|knowledge|doku|documentation|wiki|bibliothek|library|akademie|academy|schulung|training|analyse|analytics|daten|data/, "bibliothek"],
  [/geschäftsführung|geschaeftsfuehrung|management|vorstand|leitung|board|executive|strategie|strategy|office of/, "loft"],
  [/lager|logistik|logistics|archiv|archive|verwaltung|administration|backoffice|back office|poststelle|facility/, "archiv"],
] as const satisfies readonly (readonly [RegExp, string])[];

/** A light from the catalogue: builds itself at (x, y), lit when `an`. */
export type Leuchte = (x: number, y: number, an: boolean) => void;

/** How a light is placed while respecting the night budget — supplied by the
 *  scene, which owns the budget. */
export type LeuchteBauen = (liste: Leuchte[], x: number, y: number, k: string) => void;

/* Without a match: the startup. A name that gives nothing away gets the office
   that claims the least. */
export function charakterVon(name: string): Charakter {
  const n = String(name || "").toLowerCase();
  for (const [muster, art] of CHARAKTER_WORTE) if (muster.test(n)) return art;
  return "startup";
}

/* The colours. The room's own accent comes from `M.akzent` (0 means "no room",
   then the calm fabric tone stands in); neighbouring accents come from the name. */
const zimmerAkzent = () => M.akzent || FARBEN.stoff;
const nebenAkzent = (key: string) => akzentFuer(key);
const hellHolz = () => FARBEN.holzHell ?? FARBEN.holz;
/* Lighten without THREE.Color: a rug in the accent, but mixed towards the floor
   so that it lies there and does not glow. */
function aufhellen(c: number, zu: number, t: number): number {
  const k = (v: number, s: number) => (v >> s) & 255;
  const m = (s: number) => Math.round(k(c, s) + (k(zu, s) - k(c, s)) * t) << s;
  return m(16) | m(8) | m(0);
}

/* Glass for the booths: the same translucent material as the windows, but more
   transparent and without shadow, otherwise a grey block would stand on the
   floor. Cached in `M.mat`, so a switch between day and night discards it with
   the other materials. */
function kabinenGlas(): THREE.Material {
  return M.mat.kabine || (M.mat.kabine = new THREE.MeshLambertMaterial({
    color: FARBEN.glas ?? 0xd3e3e8, transparent: true, opacity: 0.32, depthWrite: false,
  }));
}

/* Build a catalogue piece in its own group and recolour it on the way. The
   catalogue knows only the one upholstery fabric and the one terracotta pot — a
   sofa in the room accent is the same sofa with a different cover, not a new
   piece of furniture. The material is swapped, not the geometry: `mat()` creates
   exactly one per colour and shape, so a comparison suffices. On request the
   group turns round, for pieces against the upper wall. */
function gefaerbt(x: number, y: number, tausch: [number, number][], bauen: (x: number, y: number) => void, drehen = false): THREE.Group {
  const { gruppe } = inGruppe(() => bauen(0, 0));
  if (tausch.length) gruppe.traverse((o) => {
    if (!(o instanceof THREE.Mesh)) return;
    for (const [von, nach] of tausch) for (const p of "fzkp")
      if (M.mat[p + von] && o.material === M.mat[p + von]) o.material = mat(p + nach, nach);
  });
  gruppe.position.set(x, 0, y);
  if (drehen) gruppe.rotation.y = Math.PI;
  return anhaengen(gruppe);
}

/* The pieces the catalogue lacks, from the same primitives. Above the plinth the
   walls are glass; what used to hang on the wall now stands on the floor, on feet
   or on a ledge. The front faces −y, as with the cabinets. */
const STUECKE = {
  /* Pin board on two feet, the felt in the accent. */
  pinnwand: (x: number, y: number, farbe: number) => {
    for (const s of [-1, 1]) {
      kasten(x + s * 58, y, 6, 34, 3, FARBEN.metall, 2);
      kasten(x + s * 58, y + 2, 4, 4, 122, FARBEN.metall, 2, 3);
    }
    kasten(x, y + 2, 112, 4, 70, farbe, 2, 48);
    for (let i = 0; i < 3; i++)
      kasten(x - 34 + i * 34, y - 1, 22, 2, 18, FARBEN.papier, 1, 62 + (i % 2) * 24);
  },
  /* Acoustic wall: free-standing felt panels in alternating accents. In a real
     office they swallow the noise; here they give the glass wall a colour without
     hanging on it. */
  akustik: (x: number, y: number, farben: number[]) => {
    const n = farben.length, bw = 54;
    farben.forEach((f, i) => {
      const px = x + (i - (n - 1) / 2) * (bw + 4);
      kasten(px, y, bw, 8, 104 - (i % 2) * 18, f, 3, 4);
      kasten(px, y, 30, 22, 4, FARBEN.dunkel, 2);
    });
  },
  /* Picture ledge: a low board at plinth height, the pictures stand on it and
     lean against the glass — where the wall is still wall. */
  bilder: (x: number, y: number, farben: number[]) => {
    kasten(x, y + 4, 172, 26, 36, hellHolz(), 3);
    ([[-56, 38, 46], [0, 56, 62], [56, 38, 46]] as const).forEach(([dx, bw, bh], i) => {
      kasten(x + dx, y + 10, bw, 3, bh, FARBEN.holzDunkel, 2, 36);
      kasten(x + dx, y + 8, bw - 8, 2, bh - 8, farben[i % farben.length], 1, 40);
    });
  },
  werkbank: (x: number, y: number) => {
    kasten(x - 70, y, 6, 58, 86, FARBEN.metall, 2);
    kasten(x + 70, y, 6, 58, 86, FARBEN.metall, 2);
    kasten(x, y, 158, 66, 5, hellHolz(), 3, 86);
    kasten(x + 38, y - 6, 34, 24, 12, FARBEN.dunkel, 3, 91);
  },
  planschrank: (x: number, y: number) => {
    kasten(x, y, 150, 62, 64, FARBEN.holz, 4);
    for (let i = 0; i < 3; i++) kasten(x, y - 32, 140, 2, 14, FARBEN.holzDunkel, 1, 8 + i * 19);
    kasten(x - 20, y + 4, 90, 50, 3, FARBEN.papier, 1, 64);
  },
};

/* Pieces for the free floor. Each measures what it occupies and builds around
   its centre. */
function stehtischBauen(x: number, y: number, sitze: number, farbe: number) {
  zylinder(x, y, 24, 2, FARBEN.dunkel);
  zylinder(x, y, 3.5, 100, FARBEN.metall, 2);
  zylinder(x, y, 40, 4, hellHolz(), 102);
  for (let i = 0; i < sitze; i++) {
    const w = (i / sitze) * Math.PI * 2 + 0.6, hx = x + Math.cos(w) * 58, hy = y + Math.sin(w) * 58;
    zylinder(hx, hy, 13, 2, FARBEN.dunkel);
    zylinder(hx, hy, 2, 68, FARBEN.metall, 2);
    zylinder(hx, hy, 15, 6, farbe, 70);
  }
}
/* The phone booth: the room for the conversation that does not belong in the
   open-plan office. Frame dark, roof in the accent, the panes transparent so
   that from above one sees the stool inside. */
function kabineBauen(x: number, y: number, farbe: number) {
  kasten(x, y, 100, 100, 4, FARBEN.dunkel, 3);
  for (const [sx, sy] of [[-1, -1], [1, -1], [-1, 1], [1, 1]])
    kasten(x + sx * 47, y + sy * 47, 6, 6, 116, FARBEN.dunkel, 2, 4);
  const glas = kasten(x, y, 92, 92, 112, FARBEN.glas ?? FARBEN.metall, 3, 5);
  glas.material = kabinenGlas();
  glas.castShadow = false;
  kasten(x, y, 104, 104, 6, farbe, 4, 120);
  zylinder(x - 14, y + 16, 15, 44, farbe, 4);
  kasten(x + 20, y - 34, 44, 22, 3, hellHolz(), 2, 88);
}
function kickerBauen(x: number, y: number) {
  for (const [sx, sy] of [[-1, -1], [1, -1], [-1, 1], [1, 1]])
    kasten(x + sx * 28, y + sy * 52, 6, 6, 62, FARBEN.dunkel, 2);
  kasten(x, y, 72, 124, 20, FARBEN.holzDunkel, 4, 62);
  flaeche(x, y, 62, 112, FARBEN.laub, 82.6);
  for (let i = 0; i < 8; i++) kasten(x, y - 49 + i * 14, 118, 2, 2, FARBEN.metall, 1, 80);
}
/* A bicycle leaning against the wall. The wheels are flat discs stood on their
   edge — from above a stroke, from the side a wheel. */
function fahrradBauen(x: number, y: number, farbe: number) {
  for (const s of [-1, 1]) {
    const rad = zylinder(x, y + s * 50, 32, 3, FARBEN.dunkel, 30.5);
    rad.rotation.z = Math.PI / 2;
  }
  kasten(x, y, 3, 92, 3, farbe, 1, 60);
  kasten(x, y + 12, 3, 64, 3, farbe, 1, 40);
  kasten(x, y - 30, 16, 24, 4, FARBEN.dunkel, 2, 76);
  kasten(x, y + 44, 46, 3, 3, FARBEN.dunkel, 1, 88);
}
function bankBauen(x: number, y: number) {
  kasten(x - 72, y, 8, 40, 40, FARBEN.metall, 2);
  kasten(x + 72, y, 8, 40, 40, FARBEN.metall, 2);
  kasten(x, y, 176, 44, 6, hellHolz(), 3, 40);
}
function lesetischBauen(x: number, y: number) {
  zylinder(x, y, 26, 2, FARBEN.metall);
  zylinder(x, y, 3, 70, FARBEN.metall, 2);
  zylinder(x, y, 48, 4, hellHolz(), 72);
  kasten(x + 12, y - 6, 34, 26, 5, FARBEN.papier, 1, 76);
}
function arbeitstischBauen(x: number, y: number, b: number, t: number) {
  for (const [dx, dy] of [[-1, -1], [1, -1], [-1, 1], [1, 1]])
    kasten(x + dx * (b / 2 - 8), y + dy * (t / 2 - 8), 6, 6, 88, FARBEN.metall, 2);
  kasten(x, y, b, t, 5, hellHolz(), 4, 88);
  kasten(x - b / 5, y + 4, b / 3, t / 2, 2, FARBEN.papier, 1, 93);
}

/* A spot along a long wall. First the wall opposite the door, as in `suche` —
   but the seats often stand in the upper part of the room, and in a room above
   the corridor that is exactly this wall: there the desks' reservation reaches
   the wall. Then the wall with the door remains, left and right of it. Returned
   are the centre and whether the piece stands against the upper wall. */
function wandSuche(b: Belegung, w: number, d: number): { x: number; y: number; oben: boolean } | null {
  const r = b.r, S = 6;
  const obenY = r.y + SCHILD_H + 6, untenY = r.y + r.h - d - 8;
  for (const y of r.oben ? [obenY, untenY] : [untenY, obenY])
    for (let x = r.x + 14; x < r.x + r.w - w - 14; x += S)
      if (b.frei(x, y, w, d)) {
        b.sperre(x - 4, y - 4, w + 8, d + 8);
        return { x: x + w / 2, y: y + d / 2, oben: y === obenY };
      }
  return null;
}

/* How much floor is still free, in square metres: a coarse grid of metres,
   every cell counts that `b.frei` reports entirely free. Inexact at the edges,
   but it only has to say when a room is full enough. */
function freieMeter(b: Belegung): number {
  const r = b.r;
  let n = 0;
  for (let y = r.y + SCHILD_H + 8; y + 100 < r.y + r.h - 6; y += 100)
    for (let x = r.x + 8; x + 100 < r.x + r.w - 6; x += 100) if (b.frei(x, y, 100, 100)) n++;
  return n;
}

const INSEL_PFLANZEN = [0, 2, 4, 5, 8, 11];

type Zone = "wand" | "ecke" | "frei";
type Bauen = (x: number, y: number, w: number, d: number) => void;

/** What a recipe may do in a room — see `charakterEinrichten`. */
type Einrichter = {
  bahn: number;
  n: number;
  wand(w: number, d: number, bauen: Bauen): boolean;
  frei(w: number, d: number, bauen: Bauen): boolean;
  schrank(liste: number[], w: number, d: number, zone: Zone, fest?: string | null): boolean;
  pflanze(liste: number[] | null, zone: Zone, gr?: number): boolean;
  sofa(liste: number[], teppich: boolean, w?: number, d?: number): boolean;
  pinnwand(): boolean;
  akustik(n: number): boolean;
  bilder(): boolean;
  kabine(): boolean;
  licht(i: number, w: number, d: number, zone: Zone): boolean;
};

type Fueller =
  | "stehtisch" | "sofa" | "sitzsaecke" | "kabine" | "pflanzeninsel" | "tafel"
  | "regal" | "teppich" | "fahrrad" | "kicker" | "werkbank" | "lesetisch";

type Rezept = { kern(z: Einrichter): void; fuellung: Fueller[] };

/* The recipes. `kern` is what makes the character and goes against the wall
   first; `fuellung` is the ranked list for the free floor. Every character has
   at least one coloured piece in its core and gets at least two plants at the
   end — even the accounts department of a startup has a sofa. `bahn` counts the
   wall sections of a good four metres, `n` the seats. */
const CHARAKTERE = {
  /* Startup: boards, coloured acoustic walls, a sofa corner in the accent, a
     booth for phone calls. The fallback for every name without a match. */
  startup: {
    kern(z) {
      z.schrank([11], 150, 50, "wand");
      z.akustik(3);
      z.sofa([0, 4], true);
      z.kabine();
      if (z.bahn > 2) z.akustik(3);
    },
    fuellung: ["stehtisch", "pflanzeninsel", "sitzsaecke", "kicker", "tafel", "teppich", "fahrrad", "kabine", "regal"],
  },
  /* Workshop: boards on castors, a rolling shelf, a workbench, table football. */
  werkstatt: {
    kern(z) {
      z.schrank([11], 150, 50, "wand");
      z.schrank([5, 16], 110, 42, "wand");
      z.pinnwand();
      if (z.bahn > 2) z.schrank([11], 150, 50, "wand");
    },
    fuellung: ["stehtisch", "werkbank", "kicker", "pflanzeninsel", "sitzsaecke", "tafel", "kabine", "fahrrad"],
  },
  /* Lab: racks in a row, cable trays, a glass wall; the colour comes from the
     acoustic walls that really do stand in a server room. Nothing gets soft
     here. */
  labor: {
    kern(z) {
      const racks = 1 + Math.min(2, z.bahn);
      for (let i = 0; i < racks; i++) z.schrank([3], 64, 76, "wand", "rack");
      z.licht(6, 36, 160, "frei");
      if (z.bahn > 1) z.licht(7, 22, 112, "frei");
      z.akustik(2);
    },
    fuellung: ["stehtisch", "pflanzeninsel", "tafel", "kabine", "kicker", "fahrrad", "regal"],
  },
  /* Chambers: still folders, but only half as many; the other half of the wall
     has been given a sofa corner. */
  kanzlei: {
    kern(z) {
      z.schrank([0, 4], 110, 42, "wand");
      if (z.bahn > 1) z.schrank([0, 4], 90, 42, "wand");
      z.sofa([1, 0], true);
      z.schrank([9], 130, 46, "wand");
    },
    fuellung: ["pflanzeninsel", "stehtisch", "kabine", "teppich", "regal", "sofa", "pflanzeninsel"],
  },
  /* Library: a wall of books, a reading chair in the accent, lots of green. */
  bibliothek: {
    kern(z) {
      /* The bookcase is the most expensive piece in the catalogue — seventy
         bodies. One of them carries the character; what follows is lighter. */
      z.schrank([6], 150, 40, "wand");
      if (z.bahn > 1) z.schrank([16, 1], 120, 40, "wand");
      z.sofa([6], true, 200, 200);
    },
    fuellung: ["pflanzeninsel", "teppich", "lesetisch", "sofa", "kabine", "regal", "pflanzeninsel"],
  },
  /* Studio: pictures on the ledge, a large sofa corner on a rug. */
  studio: {
    kern(z) {
      z.bilder();
      z.sofa([0, 4, 1], true);
      z.schrank([13, 2], 140, 46, "wand");
    },
    fuellung: ["sitzsaecke", "pflanzeninsel", "teppich", "stehtisch", "kicker", "kabine", "fahrrad"],
  },
  /* Call centre: pin board, lockers, and above all booths — whoever is on the
     phone all day needs a place without neighbours. */
  callcenter: {
    kern(z) {
      z.pinnwand();
      z.schrank([8], 90, 44, "wand");
      z.kabine();
    },
    fuellung: ["kabine", "stehtisch", "pflanzeninsel", "sitzsaecke", "tafel", "teppich"],
  },
  /* Counting house: sales. Pin board, a seating corner for the conversation. */
  kontor: {
    kern(z) {
      z.pinnwand();
      z.schrank([14, 2], 120, 44, "wand");
      z.sofa([5, 1], true, 200, 180);
    },
    fuellung: ["kabine", "stehtisch", "pflanzeninsel", "tafel", "fahrrad", "teppich"],
  },
  /* Loft: a big tree, a bench, a sofa corner — set wide apart. The filling keeps
     to green and rugs so that it stays a loft. */
  loft: {
    kern(z) {
      z.pflanze([1, 6, 8], "frei", 86);
      z.frei(186, 50, (x, y) => bankBauen(x, y));
      z.sofa([4, 0], true);
    },
    fuellung: ["pflanzeninsel", "teppich", "stehtisch", "pflanzeninsel", "regal"],
  },
  /* Archive: rows of shelves in the room, lockers, colour in between. */
  archiv: {
    kern(z) {
      const reihen = Math.min(3, 1 + z.bahn);
      for (let i = 0; i < reihen; i++) z.schrank([5, 5, 1], 130, 44, "frei", i ? "reihe" : null);
      z.schrank([8, 15], 110, 44, "wand");
      z.akustik(2);
    },
    fuellung: ["pflanzeninsel", "stehtisch", "teppich", "kabine", "regal"],
  },
  /* Studio for makers: a big table to spread things out, a plan chest, pictures. */
  atelier: {
    kern(z) {
      z.frei(z.bahn > 1 ? 200 : 150, 100, (x, y, b, t) => arbeitstischBauen(x, y, b - 10, t - 10));
      z.wand(160, 70, (x, y) => STUECKE.planschrank(x, y));
      z.bilder();
    },
    fuellung: ["pflanzeninsel", "sitzsaecke", "teppich", "tafel", "stehtisch", "fahrrad", "kabine"],
  },
} satisfies Record<string, Rezept>;

export type Charakter = keyof typeof CHARAKTERE;

/* Budget per filler: roughly how many bodies it adds. A piece that would break
   the room's cap is skipped. */
const KOSTEN: Record<Fueller, number> = {
  stehtisch: 12, sofa: 18, sitzsaecke: 14, kabine: 11, pflanzeninsel: 32, tafel: 15,
  regal: 45, teppich: 3, fahrrad: 7, kicker: 15, werkbank: 4, lesetisch: 4,
};

/* Furnishes a department room after its character. `b` is the occupancy in
   which the door and the seats are already blocked; what finds no room is
   dropped — better one piece too few than one inside a desk.

   `dichte` is how full the house stands (1 = a room looks furnished); it scales
   the budgets, not the recipes. `leuchte` places a light against the scene's
   night budget. */
export function charakterEinrichten(r: Raum, b: Belegung, dichte: number, leuchte: LeuchteBauen): void {
  const welt = M.welt;
  if (!welt) throw new Error("charakterEinrichten: M.welt is not set");
  const d = Math.max(dichte, 0.05);
  const rezept: Rezept = CHARAKTERE[charakterVon(r.name)];
  const bahn = Math.max(1, Math.min(4, Math.floor(r.w / 400)));
  const ak = zimmerAkzent();
  let nr = 0, pflanzen = 0;
  const wahl = (liste: number[], s: string) => liste[hash(r.name + s) % liste.length];
  const neben = (i: string | number) => nebenAkzent(r.name + "a" + i);

  /* Keep the paths clear before anything stands. The figures walk from the door
     straight to their seat; a full floor without these lanes would mean they
     walk through the table football. Plus the partition, if the room has one. */
  if (r.innen) for (const p of r.sitze) {
    const dx = p.x - r.innen.x, dy = p.y - r.innen.y, l = Math.hypot(dx, dy);
    for (let t = 0; t <= l; t += 24) b.sperre(r.innen.x + (dx * t) / l - 24, r.innen.y + (dy * t) / l - 24, 48, 48);
  }
  if (r.trennwand) b.sperre(r.trennwand.x - 20, r.trennwand.y0, 40, r.trennwand.y1 - r.trennwand.y0);

  /* How much this room may carry: a two-person office needs less than a hall,
     and the whole building has to stay drawable with sixty people. What is
     counted are the bodies that arise here, not an estimate. */
  const deckel = Math.round(Math.min(220, 70 + 12 * r.sitze.length + 2 * freieMeter(b)) * d);
  const start = welt.children.length;
  const koerper = () => {
    let n = 0;
    for (let i = start; i < welt.children.length; i++) welt.children[i].traverse((o) => { if ((o as THREE.Mesh).isMesh) n++; });
    return n;
  };

  const z: Einrichter = {
    bahn, n: r.sitze.length,
    wand(w, dd, bauen) {
      const p = wandSuche(b, w, dd);
      nr++;
      if (p) gefaerbt(p.x, p.y, [], (x, y) => bauen(x, y, w, dd), p.oben);
      return !!p;
    },
    frei(w, dd, bauen) {
      const p = suche(b, w, dd, "frei");
      nr++;
      if (p) bauen(p[0] + w / 2, p[1] + dd / 2, w, dd);
      return !!p;
    },
    /* A cabinet from a choice of kinds. The key `fest` keeps the kind the same
       for all pieces of that sort in a room — a row of racks is a row, not a
       sample case. */
    schrank(liste, w, dd, zone, fest) {
      const s = SCHRAENKE[wahl(liste, fest || "k" + nr)];
      nr++;
      if (zone === "wand") return z.wand(w, dd, (x, y) => s(x, y, w, dd));
      const p = suche(b, w, dd, zone);
      if (p) s(p[0] + w / 2, p[1] + dd / 2, w, dd);
      return !!p;
    },
    /* A plant in a coloured pot. If it finds no corner it may go into the room —
       in full rooms corners are the first thing missing. */
    pflanze(liste, zone, gr = 72) {
      const s = liste ? PFLANZEN[wahl(liste, "f" + nr)] : sorte(PFLANZEN, r.name + "f" + nr);
      const topf = neben(nr);
      nr++;
      for (const g of gr > 72 ? [gr, 72] : [gr]) {
        const p = suche(b, g, g, zone) || (zone === "ecke" ? suche(b, g, g, "frei") : null);
        if (p) {
          gefaerbt(p[0] + g / 2, p[1] + g / 2, [[FARBEN.topf, topf]], (x, y) => s(x, y));
          pflanzen++;
          return true;
        }
      }
      return false;
    },
    /* A seating group in the room accent, on request on a rug. The rug lies above
       the floor patterns (0.8), otherwise it flickers with them. */
    sofa(liste, teppich, w = 260, dd = 210) {
      const p = suche(b, w, dd, "frei");
      const s = GRUPPEN[wahl(liste, "g" + nr)];
      nr++;
      if (!p) return false;
      const x = p[0] + w / 2, y = p[1] + dd / 2;
      if (teppich) flaeche(x, y, w - 16, dd - 16, aufhellen(neben("t"), FARBEN.boden, 0.35), 1.4);
      gefaerbt(x, y, [[FARBEN.stoff, ak], [FARBEN.teppich, aufhellen(ak, FARBEN.boden, 0.5)]], (a, c) => s(a, c, w, dd));
      return true;
    },
    pinnwand() { return z.wand(130, 36, (x, y) => STUECKE.pinnwand(x, y, ak)); },
    akustik(n) { return z.wand(n * 58 + 8, 24, (x, y) => STUECKE.akustik(x, y, Array.from({ length: n }, (_, i) => (i % 2 ? neben("p" + i) : ak)))); },
    bilder() { return z.wand(176, 30, (x, y) => STUECKE.bilder(x, y, [ak, neben("b1"), neben("b2")])); },
    kabine() { return z.frei(110, 110, (x, y) => kabineBauen(x, y, ak)); },
    /* Lights go through `leuchte`, so that the night budget applies here too.
       Cable tray and glass wall (6 and 7) sit in the same catalogue but carry
       no light — they must not use up the budget. */
    licht(i, w, dd, zone) {
      const p = suche(b, w, dd, zone);
      nr++;
      if (!p) return false;
      if (i >= 6) LEUCHTEN[i](p[0] + w / 2, p[1] + dd / 2, false);
      else leuchte([LEUCHTEN[i]], p[0] + w / 2, p[1] + dd / 2, r.name + "l" + nr);
      return true;
    },
  };

  /* The filling, by name. Each returns whether it found room. */
  const FUELLER: Record<Fueller, () => boolean> = {
    stehtisch: () => z.frei(170, 170, (x, y) => stehtischBauen(x, y, 2 + (hash(r.name + nr) % 2), neben("s" + nr))),
    sofa: () => z.sofa([1, 5, 0], false, 220, 190),
    sitzsaecke: () => z.sofa([3, 7], true, 220, 200),
    kabine: () => z.kabine(),
    pflanzeninsel: () => z.frei(170, 150, (x, y) => {
      /* Three pots at three heights: the back one on a tall plinth, the middle
         one on a low one, the front one on the floor. Only the light kinds —
         bamboo and the hanging plant cost thirty bodies each, and an island is
         three of them. */
      ([[-40, -28, 34], [36, -20, 16], [0, 34, 0]] as const).forEach(([dx, dy, h], i) => {
        const topf = neben("i" + nr + i);
        if (h) zylinder(x + dx, y + dy, 26, h, topf);
        const gr = gefaerbt(x + dx, y + dy, [[FARBEN.topf, topf]], (a, c) => PFLANZEN[sorte(INSEL_PFLANZEN, r.name + "i" + nr + i)](a, c));
        gr.position.y = h;
        pflanzen++;
      });
    }),
    tafel: () => z.frei(150, 50, (x, y) => SCHRAENKE[11](x, y, 150, 50)),
    regal: () => z.schrank([16, 1, 13], 120, 44, "frei"),
    teppich: () => z.frei(200, 160, (x, y) => {
      flaeche(x, y, 190, 150, aufhellen(neben("r" + nr), FARBEN.boden, 0.3), 1.4);
      zylinder(x - 40, y + 10, 22, 34, ak);
      zylinder(x + 42, y - 14, 20, 30, neben("q" + nr));
    }),
    fahrrad: () => z.wand(40, 150, (x, y) => fahrradBauen(x, y, neben("v" + nr))) || z.frei(40, 150, (x, y) => fahrradBauen(x, y, neben("v" + nr))),
    kicker: () => z.frei(140, 160, (x, y) => kickerBauen(x, y)),
    werkbank: () => z.frei(170, 74, (x, y) => STUECKE.werkbank(x, y)),
    lesetisch: () => z.frei(110, 110, (x, y) => lesetischBauen(x, y)),
  };

  rezept.kern(z);
  z.licht(2, 56, 56, "ecke");

  /* Fill until the floor is used up, the list is at its end or the room has its
     measure of bodies. Three square metres may stay free — a room without a
     single empty spot is not an office but a storeroom. A piece that would break
     the measure is skipped, not the whole room ended: after an expensive shelf a
     rug still fits. When the list is through and floor is left, the cheap pieces
     repeat — so that a very wide room does not stay half empty either. */
  const genug = () => freieMeter(b) < 3 / d || koerper() >= deckel;
  const versuche = (name: Fueller) => koerper() + KOSTEN[name] <= deckel && FUELLER[name]();
  for (const name of rezept.fuellung) {
    if (genug()) break;
    versuche(name);
  }
  const billig: Fueller[] = ["teppich", "stehtisch", "kabine"];
  for (let i = 0, fehl = 0; i < 12 && fehl < 3 && !genug(); i++)
    fehl = versuche(billig[i % 3]) ? 0 : fehl + 1;
  /* Whatever is still free gets green — and every room at least two plants,
     the lab included. */
  for (let i = 0; i < 8 && (pflanzen < 2 || (!genug() && koerper() + 30 <= deckel)); i++)
    if (!z.pflanze(null, i % 2 ? "frei" : "ecke") && pflanzen >= 2) break;
}
