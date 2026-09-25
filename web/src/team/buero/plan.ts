import { hash, wackel } from "./mathe";
import type { Flur, Gemein, Gruppe, Haus, Plan, Punkt, Raum, Raumnamen, Sitz } from "./typen";

export type { Etage, Flur, Gemein, Gruppe, Haus, Plan, Punkt, Raum, Raumnamen, Sitz, Trennwand } from "./typen";

/* The floor plan of the office.
 *
 * Only geometry lives here: how a workforce becomes a building. Nothing in
 * this file knows what a task is, what lights up or who walks where — which
 * is why it can be tested without starting a browser.
 *
 * THE PLAN IS COMPUTED, NOT DRAWN. A fixed template carries either eight
 * colleagues or eighty, never both: at eight it stands empty, at eighty it
 * bursts. So everything follows from the head count — how many seats a room
 * has side by side, how deep it gets, how many rooms fit in a row, how many
 * corridors are needed.
 *
 * Dimensions are thought in centimetres and computed in units of 1 cm — the
 * plan is the same as in the top-down view, only "pixel" is now "centimetre".
 */

/* Everyone used to sit cheek by jowl because the seat was exactly as wide as
   the desk. A workplace needs the desk, the chair behind it, the way between
   and the space above — and distance to the neighbour. 150 × 165 was still
   the size of the furniture, not of the place. */
export const SITZ_B = 245,
  SITZ_H = 265,
  POD_LUFT = 40,
  PAD = 40,
  PAD_OBEN = 56,
  SCHILD_H = 10;
/* The corridors used to be 130 wide — a tube in which a piece by the wall
   already reached the walking line and nothing fitted that a corridor in such
   an office otherwise carries: a runner, seating on both sides, bikes,
   lockers. 220 and 170 are the sizes at which both work. */
export const AUSSEN = 12,
  INNEN = 10,
  FLUR_H = 170,
  QUER_B = 220,
  TUER_B = 60;

/* Rooms stay roughly square: twelve seats in one row are a tube. */
export const spaltenFuer = (n: number) => Math.max(1, Math.min(5, n, Math.round(Math.sqrt(n * 1.3))));
export const podBreite = (s: number) => s * SITZ_B + (Math.ceil(s / 2) - 1) * POD_LUFT;
export const zimmerBreite = (n: number) => Math.max(430, podBreite(spaltenFuer(n)) + PAD * 2);
export const zimmerHoehe = (n: number) =>
  SCHILD_H + PAD_OBEN + PAD + (Math.ceil(n / spaltenFuer(n)) - 1) * SITZ_H + 215;

/* ── How the seats stand in a room ─────────────────────────────────────────
 * Ten arrangements, and the reason is not variety for its own sake: a
 * building in which every room shows the same grid looks computed — in the
 * bad sense, like a table with furniture. Real offices place desks
 * differently depending on whether people work side by side or together.
 *
 * Which arrangement a room gets is decided by the department's name — the
 * same room looks the same on every visit.
 *
 * `dreh` is the seat's viewing direction in radians; the furniture is rotated
 * as a group around it, the figure always stands on the point itself.
 *
 * The scatter is computed, not random: an office that stands differently on
 * every visit is worse than one that looks staged — nothing can be
 * remembered, and every background refresh would shift the furniture. So the
 * offset comes from a hash of the key: the same seat always gets the same
 * offset, and still there is no grid.
 */

/* What a seat occupies on the floor, in its own frame: the desk 150 wide,
   forward the monitor to −50, backward the chair plus the way to stand up to
   +75. The arrangements check against this size and not against
   SITZ_B × SITZ_H — that is the spacing in the grid, not the furniture, and
   along a wall or at a long table desks stand closer. */
const FUSS: readonly [number, number][] = [
  [-75, -50],
  [75, -50],
  [75, 75],
  [-75, 75],
];
const RAND = 20;

export type Kasten = { x0: number; y0: number; x1: number; y1: number };

export function sitzFuss(p: Sitz): Kasten {
  const c = Math.cos(p.dreh),
    s = Math.sin(p.dreh);
  let x0 = Infinity,
    y0 = Infinity,
    x1 = -Infinity,
    y1 = -Infinity;
  for (const [a, b] of FUSS) {
    const px = p.x + a * c - b * s,
      py = p.y + a * s + b * c;
    x0 = Math.min(x0, px);
    x1 = Math.max(x1, px);
    y0 = Math.min(y0, py);
    y1 = Math.max(y1, py);
  }
  return { x0, y0, x1, y1 };
}

/* The same area the scene keeps free in front of the door. */
export const tuerZone = (x: number, y: number, w: number, h: number, oben: boolean, tuerX: number): Kasten => ({
  x0: tuerX - TUER_B / 2 - 10,
  x1: tuerX + TUER_B / 2 + 10,
  y0: oben ? y + h - 80 : y - 10,
  y1: oben ? y + h + 10 : y + 80,
});

/** What the plan only knows while laying the room: the depth of the band,
 *  which side of the corridor the room is on and where the door sits.
 *  Without it no arrangement could stand against a wall or avoid the door. */
export type SitzRaum = { h?: number; oben?: boolean; tuerX?: number };

/** A seat before placement; `weit` scales its scatter. */
type Roh = Sitz & { weit?: number };

export function sitzMuster(
  name: string,
  x: number,
  y: number,
  w: number,
  sp: number,
  n: number,
  raum: SitzRaum = {},
): Sitz[] {
  if (!n) return [];
  const h = raum.h ?? zimmerHoehe(n),
    oben = raum.oben ?? true,
    tuerX = raum.tuerX ?? x + w / 2;
  const tuerOben = !oben;
  /* No seat stands exactly on its grid point and none exactly straight.
     Twenty centimetres and ten degrees are enough — more looks as if someone
     had bumped into it. Desks that touch do not slip individually; there only
     a fraction wobbles. */
  const streu = (i: number, px: number, py: number, dreh: number, weit = 1): Sitz => ({
    x: px + wackel(name + "x" + i, 20 * weit),
    y: py + wackel(name + "y" + i, 20 * weit),
    dreh: dreh + wackel(name + "d" + i, 0.17 * weit),
  });
  const T = y + SCHILD_H,
    B = y + h,
    L = x,
    R = x + w;
  const tuer = tuerZone(x, y, w, h, oben, tuerX);
  const schneidet = (a: Kasten, b: Kasten) => a.x0 < b.x1 && b.x0 < a.x1 && a.y0 < b.y1 && b.y0 < a.y1;

  /* An arrangement is only fit when every seat stands wholly inside the room,
     none in the door, no two on top of each other. The finished result is
     checked, scatter included — whatever fails here is never offered. */
  const taugt = (liste: Sitz[] | null): liste is Sitz[] => {
    if (!liste || liste.length !== n) return false;
    const f = liste.map(sitzFuss);
    for (const k of f) {
      if (k.x0 < L + RAND || k.x1 > R - RAND || k.y0 < T + RAND || k.y1 > B - RAND) return false;
      if (schneidet(k, tuer)) return false;
    }
    for (let i = 0; i < n; i++)
      for (let j = i + 1; j < n; j++) {
        if (Math.hypot(liste[i].x - liste[j].x, liste[i].y - liste[j].y) < 125) return false;
        const a = f[i],
          b = f[j];
        if (Math.min(a.x1, b.x1) - Math.max(a.x0, b.x0) > 10 && Math.min(a.y1, b.y1) - Math.max(a.y0, b.y0) > 10)
          return false;
      }
    return true;
  };

  /* Free arrangements are computed as a block and then placed: horizontally
     roughly centred, vertically with the larger part of the remainder on the
     door side — the entrance of an office is clear, the desks stand behind
     it. If the door is at the top, a further 90 stay free for the way in. */
  const setzen = (roh: Roh[]): Sitz[] => {
    let x0 = Infinity,
      y0 = Infinity,
      x1 = -Infinity,
      y1 = -Infinity;
    for (const p of roh) {
      const k = sitzFuss(p);
      x0 = Math.min(x0, k.x0);
      x1 = Math.max(x1, k.x1);
      y0 = Math.min(y0, k.y0);
      y1 = Math.max(y1, k.y1);
    }
    const oy0 = T + RAND + 30 + (tuerOben ? 90 : 0),
      oy1 = B - RAND - 30 - (tuerOben ? 0 : 90);
    const restX = Math.max(0, R - L - 2 * RAND - 60 - (x1 - x0));
    const restY = Math.max(0, oy1 - oy0 - (y1 - y0));
    const dx = L + RAND + 30 + restX / 2 + wackel(name + "v", restX * 0.3) - x0;
    const dy = (tuerOben ? oy1 - restY / 3 - (y1 - y0) : oy0 + restY / 3) - y0;
    return roh.map((p, i) => streu(i, p.x + dx, p.y + dy, p.dreh, p.weit ?? 1));
  };
  const podX = (sp_: number, i: number) =>
    Math.floor((i % sp_) / 2) * (2 * SITZ_B + POD_LUFT) + ((i % sp_) % 2) * SITZ_B;
  const TISCH = 165; // centre distance of two desks standing edge to edge
  const GEGEN = 136; // two seats facing each other across the monitors

  const muster: Record<string, () => Sitz[] | null> = {
    /* Rows, all facing the same way. */
    reihen: () =>
      setzen(Array.from({ length: n }, (_, i) => ({ x: podX(sp, i), y: Math.floor(i / sp) * SITZ_H, dreh: 0 }))),

    /* Facing: every two rows share an edge and look at each other across the
       monitors; between the pairs runs the aisle. */
    gegenueber: () =>
      setzen(
        Array.from({ length: n }, (_, i) => {
          const z = Math.floor(i / sp);
          return {
            x: podX(sp, i),
            y: Math.floor(z / 2) * (GEGEN + 260) + (z % 2) * GEGEN,
            dreh: z % 2 ? 0 : Math.PI,
            weit: 0.5,
          };
        }),
      ),

    /* Island: four desks to a block, two facing two. The classic in
       departments that work together rather than side by side. */
    inseln: () => {
      const inseln = Math.ceil(n / 4);
      const jeZeile = Math.max(1, Math.min(inseln, Math.floor((w - 2 * RAND - 60 - 306) / 416) + 1));
      return setzen(
        Array.from({ length: n }, (_, i) => {
          const ins = Math.floor(i / 4),
            k = i % 4;
          return {
            x: (ins % jeZeile) * 416 + (k % 2) * 156,
            y: Math.floor(ins / jeZeile) * 400 + (k >> 1) * GEGEN,
            dreh: k >> 1 ? 0 : Math.PI,
            weit: 0.3,
          };
        }),
      );
    },

    /* Staggered: every second row shifted by half a seat. The simplest remedy
       against the checkerboard — and the oldest. */
    versetzt: () =>
      setzen(
        Array.from({ length: n }, (_, i) => {
          const z = Math.floor(i / sp);
          return { x: podX(sp, i) + (z % 2 ? SITZ_B * 0.4 : 0), y: z * SITZ_H, dreh: 0 };
        }),
      ),

    /* Horseshoe: along three walls, backs to the wall, the open side towards
       the door. Teams that talk a lot sit like this — the middle stays free
       for a standing table or a whiteboard. Only from five on; below that it
       is not a horseshoe but two desks against a wall. */
    hufeisen: () => {
      if (n < 5 || w < 820) return null;
      const hy = oben ? T + RAND + 90 : B - RAND - 90; // the row against the wall opposite the door
      const capB = Math.floor((w - 500) / TISCH) + 1;
      const capS = Math.floor((h - SCHILD_H - 2 * (RAND + 90) - 2 * TISCH) / TISCH) + 1;
      if (capS < 1) return null;
      let hinten = Math.min(capB, Math.max(1, Math.round(n / 3)));
      let seite = n - hinten;
      if (Math.ceil(seite / 2) > capS) {
        hinten = n - 2 * capS;
        seite = 2 * capS;
      }
      if (hinten > capB || hinten < 1) return null;
      const liste: Sitz[] = [];
      for (let k = 0; k < hinten; k++)
        liste.push({ x: x + w / 2 + (k - (hinten - 1) / 2) * TISCH, y: hy, dreh: oben ? Math.PI : 0 });
      for (let k = 0; k < seite; k++) {
        const links = k % 2 === 0,
          reihe = Math.floor(k / 2);
        liste.push({
          x: links ? L + RAND + 90 : R - RAND - 90,
          y: hy + (oben ? 1 : -1) * (reihe + 1) * TISCH,
          dreh: links ? Math.PI / 2 : -Math.PI / 2,
        });
      }
      return liste.map((p, i) => streu(i, p.x, p.y, p.dreh, 0.4));
    },

    /* Window row: everyone against the wall opposite the door, facing the
       wall, desk beside desk. The rest of the floor stays free. Only where
       the wall is long enough for all. */
    fenster: () => {
      if (n < 2) return null;
      const cap = Math.floor((w - 2 * RAND - 44 - 150) / TISCH) + 1;
      if (n > cap) return null;
      const fy = oben ? T + RAND + 72 : B - RAND - 72;
      return Array.from({ length: n }, (_, i) =>
        streu(i, x + w / 2 + (i - (n - 1) / 2) * TISCH, fy, oben ? 0 : Math.PI, 0.4),
      );
    },

    /* Rotated: the whole grid tilted by a third of a right angle, all seats
       facing the same way. It happens where the floor plan stands askew to
       the light — and it is the only arrangement that breaks the axis of the
       walls. */
    gedreht: () => {
      const a = (hash(name + "w") % 2 ? 1 : -1) * (0.42 + (hash(name + "w") % 17) / 100);
      const c = Math.cos(a),
        s = Math.sin(a);
      for (let spalten = Math.min(n, 5); spalten >= 1; spalten--) {
        const liste = setzen(
          Array.from({ length: n }, (_, i) => {
            const gx = (i % spalten) * SITZ_B,
              gy = Math.floor(i / spalten) * SITZ_H;
            return { x: gx * c - gy * s, y: gx * s + gy * c, dreh: a, weit: 0.5 };
          }),
        );
        if (taugt(liste)) return liste;
      }
      return null;
    },

    /* Two benches: one row of desks along each of the two long walls, facing
       the wall, the aisle in the middle. On the door wall the bench has a gap
       exactly wide enough to get in. */
    baenke: () => {
      if (n < 4 || h < 520) return null;
      const hy = oben ? T + RAND + 72 : B - RAND - 72,
        hd = oben ? 0 : Math.PI;
      const vy = oben ? B - RAND - 72 : T + RAND + 72,
        vd = oben ? Math.PI : 0;
      const capB = Math.floor((w - 2 * RAND - 44 - 150) / TISCH) + 1;
      const slots: number[] = [];
      for (let sx = L + RAND + 22 + 75; sx <= R - RAND - 22 - 75; sx += TISCH)
        if (sx + 75 < tuer.x0 - 25 || sx - 75 > tuer.x1 + 25) slots.push(sx);
      slots.sort((p, q) => Math.abs(p - tuerX) - Math.abs(q - tuerX));
      let hinten = Math.min(capB, Math.ceil(n / 2)),
        vorn = n - hinten;
      if (vorn > slots.length) {
        vorn = slots.length;
        hinten = n - vorn;
      }
      if (hinten > capB) return null;
      const liste: Sitz[] = [];
      for (let k = 0; k < hinten; k++) liste.push({ x: x + w / 2 + (k - (hinten - 1) / 2) * TISCH, y: hy, dreh: hd });
      for (const sx of slots.slice(0, vorn).sort((p, q) => p - q)) liste.push({ x: sx, y: vy, dreh: vd });
      return liste.map((p, i) => streu(i, p.x, p.y, p.dreh, 0.4));
    },

    /* Long table: one long table, everyone at it, with an odd count one at
       the head. That is the department small enough to look at each other
       every morning — above eight it becomes a conference table. */
    tafel: () => {
      if (n < 3 || n > 8) return null;
      const kopf = n % 2,
        seite = (n - kopf) / 2,
        liste: Roh[] = [];
      for (let k = 0; k < seite; k++) {
        liste.push({ x: k * TISCH, y: 0, dreh: Math.PI, weit: 0.3 });
        liste.push({ x: k * TISCH, y: GEGEN, dreh: 0, weit: 0.3 });
      }
      if (kopf) liste.push({ x: -140, y: GEGEN / 2, dreh: Math.PI / 2, weit: 0.3 });
      return setzen(liste);
    },

    /* Corner: two desks at a time at a right angle, one across the other.
       That is how workplaces stand where one looks over the other's shoulder
       — onboarding, pair work. The hand of the corner changes from pair to
       pair. */
    winkel: () => {
      const paare = Math.ceil(n / 2);
      const jeZeile = Math.max(1, Math.min(paare, Math.floor((w - 2 * RAND - 60 - 280) / 390) + 1));
      return setzen(
        Array.from({ length: n }, (_, i): Roh => {
          const pr = Math.floor(i / 2),
            k = i % 2,
            gx = (pr % jeZeile) * 390,
            gy = Math.floor(pr / jeZeile) * 280;
          const spiegel = hash(name + "l" + pr) % 2 === 1;
          if (!spiegel)
            return k
              ? { x: gx + 130, y: gy + 35, dreh: -Math.PI / 2, weit: 0.3 }
              : { x: gx, y: gy, dreh: 0, weit: 0.3 };
          return k
            ? { x: gx, y: gy + 35, dreh: Math.PI / 2, weit: 0.3 }
            : { x: gx + 130, y: gy, dreh: 0, weit: 0.3 };
        }),
      );
    },
  };

  /* The name decides which arrangement a room gets — but only among those
     that fit this room. Rows always fit, because the plan sizes the room by
     them. */
  const passend: Sitz[][] = [];
  for (const k of Object.keys(muster)) {
    const l = muster[k]();
    if (taugt(l)) passend.push(l);
  }
  /* If none fits, rows stand with little scatter — the room is sized for
     them, and without the wobble the margin suffices in every case. */
  if (!passend.length)
    return setzen(
      Array.from({ length: n }, (_, i) => ({ x: podX(sp, i), y: Math.floor(i / sp) * SITZ_H, dreh: 0, weit: 0.2 })),
    );
  return passend[hash(name + "m") % passend.length];
}

/* ── The plan ────────────────────────────────────────────────────────────── */

/** One cell of a band before it is laid: a department or a common room.
 *  `schluessel` is what the lounge placement hashes — language-independent,
 *  so a translated room name does not move the lounges. */
type Zelle = {
  gruppe?: Gruppe;
  gem?: Gemein;
  name: string;
  schluessel: string;
  w: number;
  h: number;
  max?: number;
};

/** A lounge island: centre, size and number; `treffY` is the line in front
 *  of it, on the door side, where people meet. */
export type LoungeZone = { nr: number; x: number; y: number; b: number; t: number; treffY: number };

/**
 * Lays out the building.
 *
 * `maxB` is the available width; the building grows in height, never beyond
 * that width. `dichte` is how full the house stands (1 is the measure at
 * which a room looks furnished); the plan only reads it for the number of
 * islands in a lounge. It scales budgets, not recipes. `mitTreppe` says the
 * house has more than one floor, so this one needs room for the stairs.
 */
export function bauplan(
  gruppen: Gruppe[],
  maxB: number,
  namen: Raumnamen,
  dichte = 1,
  mitTreppe = false,
): Plan {
  /* The width follows the head count, but not to the centimetre: one room is
     a little generous, the next one tight, as it happened to be allocated at
     move-in. Bounded below by the rows the room has to carry. */
  const zellen: Zelle[] = gruppen.map((g) => {
    const n = g.leute.length,
      soll = zimmerBreite(n);
    const w = Math.max(430, podBreite(spaltenFuer(n)) + PAD, Math.round(soll * (1 + wackel(g.name + "b", 0.12))));
    return { gruppe: g, name: g.name, schluessel: g.name, w, h: zimmerHoehe(n), max: soll * 1.25 };
  });
  zellen.push({ gem: "besprechung", name: namen.besprechung, schluessel: "besprechung", w: 280, h: 230 });
  zellen.push({ gem: "kueche", name: namen.kueche, schluessel: "kueche", w: 230, h: 230 });
  const innenB = Math.max(900, maxB - AUSSEN * 2 - QUER_B - INNEN);
  const ges = zellen.reduce((s, z) => s + z.w + INNEN, 0) - INNEN;
  const bz = Math.max(1, Math.ceil(ges / innenB)),
    soll = ges / bz;
  let baender: Zelle[][] = [];
  let band: Zelle[] = [],
    breit = 0;
  for (const z of zellen) {
    if (band.length && (breit + z.w > innenB || (breit >= soll && baender.length < bz - 1))) {
      baender.push(band);
      band = [];
      breit = 0;
    }
    band.push(z);
    breit += z.w + INNEN;
  }
  if (band.length) baender.push(band);
  baender = baender.map((b) => zeileFuellen(b, innenB, namen.lounge));
  const x0 = AUSSEN + QUER_B + INNEN;
  const raeume: Raum[] = [],
    flure: Flur[] = [];
  let y = AUSSEN,
    lounges = 0;
  for (let gi = 0; gi < baender.length; gi += 2) {
    const paar = [baender[gi], baender[gi + 1]].filter((b): b is Zelle[] => Boolean(b)),
      fi = flure.length;
    const legen = (b: Zelle[], hoehe: number, oben: boolean) => {
      let x = x0;
      for (const z of b) {
        const w = z.w,
          g = z.gruppe,
          n = g ? g.leute.length : 0,
          sp = g ? spaltenFuer(n) : 1;
        const variante = z.gem === "lounge" ? lounges++ : undefined;
        const r: Raum = {
          id: g ? g.id : variante != null ? `lounge-${variante}` : (z.gem ?? ""),
          name: z.name,
          farbe: g ? g.farbe : "",
          leute: g ? g.leute : [],
          sitze: [],
          x,
          y,
          w,
          h: hoehe,
          spalten: sp,
          zeilen: n ? Math.ceil(n / sp) : 0,
          oben,
          flur: fi,
          flurY: 0,
          nr: 0,
          tuerX: x + w / 2,
          wand: 0,
          ri: oben ? 1 : -1,
          innen: { x: 0, y: 0 },
          aussen: { x: 0, y: 0 },
          treff: [],
        };
        if (z.gem) r.gem = z.gem;
        if (variante != null) r.variante = variante;
        r.sitze = sitzMuster(g ? g.name : "", x, y, w, sp, n, { h: hoehe, oben, tuerX: r.tuerX });
        if (n >= 10) trennwandSuchen(r);
        raeume.push(r);
        x += w + INNEN;
      }
      y += hoehe;
    };
    legen(paar[0], Math.max(...paar[0].map((z) => z.h)), true);
    flure.push({ y: y + INNEN, h: FLUR_H, mitte: y + INNEN + FLUR_H / 2 });
    y += INNEN + FLUR_H + INNEN;
    if (paar[1]) {
      legen(paar[1], Math.max(...paar[1].map((z) => z.h)), false);
      y += INNEN;
    }
  }
  const gebaut = y - INNEN + AUSSEN,
    breite = AUSSEN * 2 + QUER_B + INNEN + innenB;
  const quer = { x: AUSSEN, w: QUER_B, mitte: AUSSEN + QUER_B / 2 };
  const zaehler: Record<number, number> = {};
  for (const r of raeume) {
    const f = flure[r.flur];
    r.wand = r.oben ? r.y + r.h : r.y;
    r.flurY = f.mitte;
    r.innen = { x: r.tuerX, y: r.wand - r.ri * 26 };
    r.aussen = { x: r.tuerX, y: r.wand + r.ri * (INNEN + 26) };
    zaehler[r.flur] = (zaehler[r.flur] ?? 0) + 1;
    r.nr = zaehler[r.flur];
    if (r.gem === "lounge") {
      /* Meeting points in front of the islands, on the door side — not on
         the table-tennis table. */
      r.treff = loungeZonen(r, dichte).flatMap((z) =>
        [-1, 1].map((sx) => ({ x: z.x + sx * Math.min(60, z.b / 4), y: z.treffY })),
      );
    } else if (r.gem) {
      const cx = r.x + r.w / 2,
        cy = r.y + SCHILD_H + (r.h - SCHILD_H) / 2;
      r.treff = [
        { x: cx - 70, y: cy + 40 },
        { x: cx + 70, y: cy + 40 },
        { x: cx, y: cy + 52 },
        { x: cx - 70, y: cy - 40 },
        { x: cx + 70, y: cy - 40 },
      ];
    }
  }
  const ey = AUSSEN + 60;
  const plaetze = Array.from({ length: 5 }, (_, i) => ({ x: quer.mitte, y: ey + 230 + i * 56 }));
  /* The stairs sit at the far end of the cross corridor, opposite the
     entrance: the front desk and its queue keep the head of the corridor,
     and whoever changes floors does not walk through the queue. A floor with
     a single band of rooms is shorter than the queue plus the stairwell, so
     in a house with stairs the cross corridor runs on past the last rooms
     until the stairwell fits — the rooms stay where they are, only the
     corridor and the outer wall move. */
  const hoehe = mitTreppe ? Math.max(gebaut, plaetze[plaetze.length - 1].y + 200 + 108 + AUSSEN) : gebaut;
  return {
    breite,
    hoehe,
    flure,
    quer,
    raeume,
    tresen: { y: ey + 130, plaetze },
    treppe: { x: quer.mitte, y: hoehe - AUSSEN - 96 },
  };
}

/* ── Floors ──────────────────────────────────────────────────────────────────
 * One floor carries a good forty seats. Beyond that the plan grows in depth
 * until, seen from above, it is a tower block: six corridors at two hundred
 * people, and rooms so small that a face covers them.
 *
 * So floors, and the rule is the only one there is with flat departments:
 * FILL BY HEAD COUNT, in org-chart order, never splitting a department. A
 * department that alone is larger than the measure gets a floor of its own.
 * Every floor has its kitchen, meeting room and lounges.
 *
 * Forty-four, not thirty: two departments of twenty are the common case, and
 * with thirty each would sit alone on its floor — six storeys for a hundred
 * and forty people, each half empty. */
export const ETAGE_PLAETZE = 44;

export function etagenTeilen(gruppen: Gruppe[]): Gruppe[][] {
  const etagen: Gruppe[][] = [];
  let band: Gruppe[] = [],
    voll = 0;
  for (const g of gruppen) {
    const n = g.leute.length;
    if (band.length && voll + n > ETAGE_PLAETZE) {
      etagen.push(band);
      band = [];
      voll = 0;
    }
    band.push(g);
    voll += n;
  }
  /* A house has a ground floor even before anybody moved in: the component
     asks for the shown floor's plan while the lists are still loading. */
  if (band.length || !etagen.length) etagen.push(band);
  return etagen;
}

/** One plan per floor; `nr` is the floor's index, 0 at the entrance. All
 *  floors of a house have stairs, or none does. */
export function hausBauen(gruppen: Gruppe[], maxB: number, namen: Raumnamen, dichte = 1): Haus {
  const teile = etagenTeilen(gruppen),
    treppe = teile.length > 1;
  return {
    etagen: teile.map((g, nr) => ({ nr, gruppen: g, plan: bauplan(g, maxB, namen, dichte, treppe) })),
  };
}

/* The partition in large rooms. From ten seats on a department is two
   groups, and the wall shows it — glass or half height, so the room stays
   one. It stands only where a lane between the desks is free from top to
   bottom, and it gets shorter when a way from the door to a seat would
   otherwise pass through it. If no lane is found, the room gets no wall —
   better none than one through a desk. */
function trennwandSuchen(r: Raum): void {
  const fuss = r.sitze.map(sitzFuss).sort((a, b) => a.x0 - b.x0);
  const gassen: number[] = [];
  let rechts = r.x + RAND;
  for (const k of fuss) {
    if (k.x0 - rechts >= 40) gassen.push((rechts + k.x0) / 2);
    rechts = Math.max(rechts, k.x1);
  }
  const mitte = r.x + r.w / 2;
  const kandidaten = gassen
    .filter((gx) => gx > r.x + r.w * 0.25 && gx < r.x + r.w * 0.75 && Math.abs(gx - r.tuerX) > 60)
    .sort((a, b) => Math.abs(a - mitte) - Math.abs(b - mitte));
  if (!kandidaten.length) return;
  const gx = kandidaten[0];
  /* Measured from the wall opposite the door towards the door. */
  const hinten = r.oben ? r.y + SCHILD_H : r.y + r.h,
    ri = r.oben ? 1 : -1;
  const tiefe = r.h - SCHILD_H;
  const tuerY = r.oben ? r.y + r.h - 26 : r.y + 26;
  let laenge = tiefe * (0.5 + (hash(r.name + "t") % 20) / 100);
  for (const p of r.sitze) {
    if ((p.x - gx) * (r.tuerX - gx) >= 0) continue; // the way stays on this side
    const t = (gx - r.tuerX) / (p.x - r.tuerX),
      kreuz = tuerY + t * (p.y - tuerY);
    laenge = Math.min(laenge, (kreuz - hinten) * ri - 50);
  }
  if (laenge < tiefe * 0.35) return;
  const ende = hinten + ri * laenge;
  r.trennwand = {
    x: gx,
    y0: Math.min(hinten, ende),
    y1: Math.max(hinten, ende),
    art: hash(r.name + "g") % 3 ? "glas" : "halb",
  };
}

/* What a row leaves over.
 *
 * The remainder used to go to the rooms, and with few people that made a
 * hall: eight seats on nineteen metres. Now a department gets at most a
 * quarter more than its seats need, and whatever is still left becomes a
 * room of its own — a lounge. Below LOUNGE_MIN that is not worth it; such a
 * remainder is spread evenly as before. Above LOUNGE_MAX it becomes two,
 * otherwise the lounge would stand as a hall again — but never two side by
 * side, that would be one with a wall in it.
 * If the meeting room and the kitchen are in the row, they grow a little
 * first — a remainder of this size speaks for a more generous house. */
const LOUNGE_MIN = 380,
  LOUNGE_MAX = 1800;
/* A lounge in a row that otherwise only carries the meeting room and the
   kitchen would be as shallow as those — 230, too little for an island and
   the door in front of it. */
const LOUNGE_TIEFE = 420;

function zeileFuellen(b0: Zelle[], innenB: number, loungeName: string): Zelle[] {
  const b = b0.map((z) => ({ ...z }));
  const roh = () => b.reduce((s, z) => s + z.w + INNEN, 0) - INNEN;
  let rest = innenB - roh();
  if (rest >= LOUNGE_MIN)
    for (const z of b)
      if (z.gem) {
        const plus = Math.min(120, rest * 0.1);
        z.w += plus;
        rest -= plus;
      }
  if (rest >= LOUNGE_MIN) {
    const zahl = Math.min(Math.ceil(rest / (LOUNGE_MAX + INNEN)), b.length + 1);
    const lw = rest / zahl - INNEN;
    const schluessel = b.map((z) => z.schluessel).join("|");
    for (let k = 0; k < zahl; k++) {
      const frei: number[] = [];
      for (let i = 0; i <= b.length; i++) if (b[i - 1]?.gem !== "lounge" && b[i]?.gem !== "lounge") frei.push(i);
      if (!frei.length) break;
      b.splice(frei[hash(schluessel + "lo" + k) % frei.length], 0, {
        gem: "lounge",
        name: loungeName,
        schluessel: "lounge",
        w: lw,
        h: LOUNGE_TIEFE,
      });
    }
    return b;
  }
  /* A small remainder, evenly — but no department beyond its quarter. What
     it may not take goes to the others; only when all are at the limit do
     they get it anyway, because the row has to close flush. */
  for (let runde = 0; runde < 20 && rest > 0.5; runde++) {
    const offen = b.filter((z) => !z.max || z.w < z.max - 0.5);
    const nehmer = offen.length ? offen : b;
    const teil = rest / nehmer.length;
    for (const z of nehmer) {
      const plus = offen.length && z.max ? Math.min(teil, z.max - z.w) : teil;
      z.w += plus;
      rest -= plus;
    }
  }
  return b;
}

/* The islands of a lounge. As with the kitchen, one catalogue piece does not
 * fill the whole room — here up to four stand side by side, none narrower
 * than 330, each on its own area, so a wide lounge is not an empty hall with
 * a table-tennis table again. Towards the door a strip stays free: that is
 * where one comes in, and where the meeting points are.
 * Returns centre, size and number per island; which furnishing goes on it is
 * decided by the scene, because only it knows the catalogue. */
export function loungeZonen(r: Raum, dichte = 1): LoungeZone[] {
  const iw = r.w - PAD * 2,
    ih = r.h - SCHILD_H - PAD * 2;
  const t = Math.max(120, Math.min(ih - 90, 380));
  const zahl = Math.max(1, Math.min(4, Math.floor((iw * Math.min(1, dichte)) / 360)));
  const zb = iw / zahl;
  const cy = r.y + SCHILD_H + PAD + ih / 2 + (r.oben ? -30 : 30);
  return Array.from({ length: zahl }, (_, j) => ({
    nr: j,
    x: r.x + PAD + zb * (j + 0.5),
    y: cy,
    b: zb - 30,
    t,
    treffY: cy + (r.oben ? 1 : -1) * (t / 2 + 30),
  }));
}

/* ── Where a point is ────────────────────────────────────────────────────── */

export function raumAn(plan: Plan, p: Punkt): number | null {
  for (let i = 0; i < plan.raeume.length; i++) {
    const r = plan.raeume[i];
    if (p.x >= r.x && p.x <= r.x + r.w && p.y >= r.y && p.y <= r.y + r.h) return i;
  }
  return null;
}

export function flurAn(plan: Plan, p: Punkt): number | null {
  if (p.x < plan.quer.x + plan.quer.w) return null;
  for (let i = 0; i < plan.flure.length; i++) {
    const f = plan.flure[i];
    if (p.y >= f.y - 2 && p.y <= f.y + f.h + 2) return i;
  }
  return null;
}

/* ── Ways ────────────────────────────────────────────────────────────────────
 * In a room the way leads around the furniture (`imRaum`, supplied by the
 * pathfinding); on the corridor it stays as it was: door, centre line, cross
 * corridor. The in-room legs return the points after the start, the goal
 * included; without `imRaum` they are the straight line. */

/** The in-room leg: points after `von`, ending at `ziel`. */
export type ImRaum = (ri: number, von: Punkt, ziel: Punkt) => Punkt[];

const gerade: ImRaum = (_ri, _von, ziel) => [{ ...ziel }];

export function weg(plan: Plan, von: Punkt, ziel: Punkt, zielRaum: number | null, imRaum: ImRaum = gerade): Punkt[] {
  const raus = (ri: number, v: Punkt): Punkt[] => {
    const r = plan.raeume[ri];
    return [...imRaum(ri, v, r.innen), { ...r.aussen }, { x: r.tuerX, y: r.flurY }];
  };
  /* The point behind the door comes first, then the room's own leg from
     there; the leg may pull it onto free floor if something stands there. */
  const rein = (ri: number, z: Punkt): Punkt[] => {
    const r = plan.raeume[ri];
    return [{ x: r.tuerX, y: r.flurY }, { ...r.aussen }, { ...r.innen }, ...imRaum(ri, r.innen, z)];
  };

  const vr = raumAn(plan, von);
  if (vr != null && vr === zielRaum) return imRaum(vr, von, ziel);
  const p: Punkt[] = [];
  let fy: number | null = null;
  if (vr != null) {
    p.push(...raus(vr, von));
    fy = plan.raeume[vr].flurY;
  } else {
    const fi = flurAn(plan, von);
    if (fi != null) {
      fy = plan.flure[fi].mitte;
      p.push({ x: von.x, y: fy });
    }
  }
  if (zielRaum == null) {
    if (fy != null) p.push({ x: plan.quer.mitte, y: fy });
    p.push({ x: plan.quer.mitte, y: ziel.y }, { ...ziel });
    return p;
  }
  const b = plan.raeume[zielRaum];
  if (fy == null) p.push({ x: plan.quer.mitte, y: b.flurY });
  else if (Math.abs(fy - b.flurY) > 1) p.push({ x: plan.quer.mitte, y: fy }, { x: plan.quer.mitte, y: b.flurY });
  p.push(...rein(zielRaum, ziel));
  return p;
}

/* ── Occupancy (placement without overlap) ──────────────────────────────── */

export const RASTER = 8;

export type Belegung = {
  sperre(x: number, y: number, w: number, h: number): void;
  frei(x: number, y: number, w: number, h: number): boolean;
  r: Raum;
};

export function belegung(r: Raum): Belegung {
  const sp = Math.ceil(r.w / RASTER),
    ze = Math.ceil(r.h / RASTER);
  const feld = new Uint8Array(sp * ze);
  const zellen = (x: number, y: number, w: number, h: number) => [
    Math.max(0, Math.floor((x - r.x) / RASTER)),
    Math.max(0, Math.floor((y - r.y) / RASTER)),
    Math.min(sp - 1, Math.ceil((x + w - r.x) / RASTER) - 1),
    Math.min(ze - 1, Math.ceil((y + h - r.y) / RASTER) - 1),
  ];
  const sperre = (x: number, y: number, w: number, h: number) => {
    const [a, b, c, d] = zellen(x, y, w, h);
    for (let cy = b; cy <= d; cy++) for (let cx = a; cx <= c; cx++) feld[cy * sp + cx] = 1;
  };
  const frei = (x: number, y: number, w: number, h: number) => {
    if (x < r.x + 6 || y < r.y + 6 || x + w > r.x + r.w - 6 || y + h > r.y + r.h - 6) return false;
    const [a, b, c, d] = zellen(x, y, w, h);
    for (let cy = b; cy <= d; cy++) for (let cx = a; cx <= c; cx++) if (feld[cy * sp + cx]) return false;
    return true;
  };
  return { sperre, frei, r };
}

/** Finds the first free spot of w × h in a zone — against the wall opposite
 *  the door, in a corner, or anywhere from the door side back — and reserves
 *  it with a 4 cm margin. */
export function suche(b: Belegung, w: number, h: number, zone: "wand" | "ecke" | "frei"): [number, number] | null {
  const r = b.r,
    S = 6,
    k: [number, number][] = [];
  if (zone === "wand") {
    const y = r.oben ? r.y + SCHILD_H + 6 : r.y + r.h - h - 8;
    for (let x = r.x + 14; x < r.x + r.w - w - 14; x += S) k.push([x, y]);
  } else if (zone === "ecke") {
    for (const [ex, ey] of [
      [r.x + 12, r.y + r.h - h - 12],
      [r.x + r.w - w - 12, r.y + r.h - h - 12],
      [r.x + 12, r.y + SCHILD_H + 10],
      [r.x + r.w - w - 12, r.y + SCHILD_H + 10],
    ])
      for (let d = 0; d < 60; d += S) k.push([ex + (ex < r.x + r.w / 2 ? d : -d), ey]);
  } else {
    for (let y = r.y + r.h - h - 12; y > r.y + SCHILD_H + 8; y -= S)
      for (let x = r.x + 14; x < r.x + r.w - w - 14; x += S) k.push([x, y]);
  }
  for (const [x, y] of k)
    if (b.frei(x, y, w, h)) {
      b.sperre(x - 4, y - 4, w + 8, h + 8);
      return [x, y];
    }
  return null;
}
