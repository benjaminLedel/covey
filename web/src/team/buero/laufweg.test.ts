import * as THREE from "three";
import { describe, expect, it } from "vitest";
import { freiNahe, istFrei, laufFeldBauen, laufrasterZeichnen, laufZufall, wegImRaum, type LaufFelder } from "./laufweg";
import { flaeche, inGruppe, kasten, malenBeginnen, M } from "./mal";
import { RASTER } from "./plan";
import type { Plan, Punkt, Raum } from "./typen";

/* A hand-made room, built with the real three and the real painting
   primitives: 1200 × 850, the door in the lower wall at x = 800, two desks
   and a phone booth standing right in front of the door. */

function testPlan(): Plan {
  const x = 200, y = 40, w = 1200, h = 850, tuerX = 800;
  const raum: Raum = {
    id: "a", name: "Abteilung", farbe: "", leute: [],
    sitze: [
      { x: 500, y: 430, dreh: 0 },  /* inside the left desk's footprint */
      { x: 1000, y: 430, dreh: 0 }, /* inside the right desk's footprint */
      { x: 700, y: 250, dreh: 0 },  /* on open floor */
    ],
    x, y, w, h, spalten: 2, zeilen: 1, oben: true, flur: 0, flurY: 942, nr: 1,
    tuerX, wand: y + h, ri: 1,
    innen: { x: tuerX, y: y + h - 26 },
    aussen: { x: tuerX, y: y + h + 38 },
    treff: [],
  };
  return {
    breite: 1420, hoehe: 1000, raeume: [raum],
    flure: [{ y: 902, h: 80, mitte: 942 }],
    quer: { x: 12, w: 150, mitte: 87 },
    tresen: { y: 160, plaetze: Array.from({ length: 5 }, (_, i) => ({ x: 87, y: 260 + i * 56 })) },
  };
}

/** Builds the scene of the test room; `voll` fills the whole room instead. */
function testWelt(plan: Plan, voll = false): THREE.Group {
  const welt = new THREE.Group();
  /* As the scene does: welt is centred by half the building. */
  welt.position.set(-plan.breite / 2, 0, -plan.hoehe / 2);
  malenBeginnen(welt, false);
  const r = plan.raeume[0];
  flaeche(r.x + r.w / 2, r.y + r.h / 2, r.w, r.h, 0xeeeeee); /* the floor: flat, walked over */
  kasten(1300, 150, 60, 60, 10, 0x888888, 5, 180);           /* a lamp: hangs above, walked under */
  if (voll) {
    kasten(r.x + r.w / 2, r.y + r.h / 2, r.w, r.h, 40, 0x999999);
    return welt;
  }
  kasten(500, 400, 140, 70, 40, 0xbd9d72);                     /* desk left */
  /* desk right, as a group one level down — the way workstations are built */
  const { gruppe } = inGruppe(() => kasten(0, 0, 140, 70, 40, 0xbd9d72));
  gruppe.position.set(1000, 0, 400);
  M.welt!.add(gruppe);
  kasten(800, 700, 100, 100, 92, 0x6d8c5a);                    /* the booth in front of the door */
  return welt;
}

const zelle = (f: LaufFelder, p: Punkt) => {
  const z = f.zimmer[0];
  const x = Math.floor((p.x - z.r.x) / RASTER), y = Math.floor((p.y - z.r.y) / RASTER);
  return x >= 0 && y >= 0 && x < z.sp && y < z.ze ? y * z.sp + x : -1;
};
/** Does something really stand at p? */
const imMoebel = (f: LaufFelder, p: Punkt) => {
  const i = zelle(f, p);
  return i >= 0 && f.zimmer[0].roh[i] === 1;
};
/** Samples the leg a→b every 2 cm and counts the samples inside furniture. */
const treffer = (f: LaufFelder, a: Punkt, b: Punkt) => {
  const k = Math.max(1, Math.ceil(Math.hypot(b.x - a.x, b.y - a.y) / 2));
  let n = 0;
  for (let t = 0; t <= k; t++) if (imMoebel(f, { x: a.x + ((b.x - a.x) * t) / k, y: a.y + ((b.y - a.y) * t) / k })) n++;
  return n;
};
/** Only the first leg (getting up) and the last (sitting down) may cross
 *  furniture, and only if that end lies inside it. */
function pruefen(f: LaufFelder, von: Punkt, ziel: Punkt): Punkt[] {
  const w = wegImRaum(f, 0, von, ziel);
  expect(w.length).toBeGreaterThan(0);
  expect(w[w.length - 1]).toEqual(ziel);
  const pts = [von, ...w];
  for (let i = 0; i < pts.length - 1; i++) {
    const erlaubt = (i === 0 && !istFrei(f, 0, von)) || (i === pts.length - 2 && !istFrei(f, 0, ziel));
    if (erlaubt) continue;
    expect(treffer(f, pts[i], pts[i + 1]), `leg ${i} ${JSON.stringify([pts[i], pts[i + 1]])}`).toBe(0);
  }
  return w;
}

describe("laufweg", () => {
  const plan = testPlan();
  const felder = laufFeldBauen(testWelt(plan), plan);
  const r = plan.raeume[0];

  it("reads the built furniture, not the floor or the lamp", () => {
    expect(imMoebel(felder, { x: 500, y: 400 })).toBe(true);
    expect(imMoebel(felder, { x: 1000, y: 400 })).toBe(true);
    expect(imMoebel(felder, { x: 800, y: 700 })).toBe(true);
    expect(imMoebel(felder, { x: 1300, y: 150 })).toBe(false);
    expect(imMoebel(felder, { x: 700, y: 250 })).toBe(false);
    expect(felder.ms).toBeGreaterThanOrEqual(0);
    expect(felder.boxMs).toBeLessThanOrEqual(felder.ms);
  });

  it("walks from the door to a seat inside a desk in few, clear legs", () => {
    const tuer = freiNahe(felder, 0, r.innen);
    expect(istFrei(felder, 0, tuer)).toBe(true);
    for (const { x, y } of r.sitze) {
      const s = { x, y };
      const w = pruefen(felder, tuer, s);
      expect(w.length).toBeLessThanOrEqual(6);
      pruefen(felder, s, tuer);
    }
  });

  it("goes around the booth in front of the door", () => {
    const w = pruefen(felder, r.innen, { x: 800, y: 600 });
    expect(w.length).toBeGreaterThanOrEqual(2);
    expect(treffer(felder, r.innen, { x: 800, y: 600 })).toBeGreaterThan(0);
  });

  it("still reaches a goal inside the booth", () => {
    const w = pruefen(felder, r.innen, { x: 800, y: 700 });
    expect(w[w.length - 1]).toEqual({ x: 800, y: 700 });
  });

  it("falls back to the straight line in a room without free floor", () => {
    const leer = laufFeldBauen(testWelt(plan, true), plan);
    const sitz = { x: r.sitze[0].x, y: r.sitze[0].y };
    expect(wegImRaum(leer, 0, r.innen, sitz)).toEqual([sitz]);
    expect(laufZufall(leer, 0)).toBeNull();
    expect(freiNahe(leer, 0, { x: 1, y: 2 })).toEqual({ x: 1, y: 2 });
    /* and a room index without a grid as well */
    expect(wegImRaum(felder, 7, r.innen, sitz)).toEqual([sitz]);
  });

  it("is deterministic", () => {
    const zweite = laufFeldBauen(testWelt(plan), plan);
    for (const s of r.sitze) expect(wegImRaum(felder, 0, r.innen, s)).toEqual(wegImRaum(zweite, 0, r.innen, s));
  });

  it("wanders only to reachable free floor", () => {
    const st = felder.zimmer[0].stufen.find((s) => s.liste.length)!;
    for (let k = 0; k < 300; k++) {
      const p = laufZufall(felder, 0)!;
      expect(istFrei(felder, 0, p)).toBe(true);
      expect(st.haupt[zelle(felder, p)]).toBe(1);
      expect(imMoebel(felder, p)).toBe(false);
      if (k < 40) pruefen(felder, p, laufZufall(felder, 0)!);
    }
  });

  it("draws the debug overlay", () => {
    const welt = new THREE.Group();
    malenBeginnen(welt, false);
    laufrasterZeichnen(felder, plan);
    const stark = welt.children.filter((o) => ((o as THREE.Mesh).material as THREE.Material).opacity === 0.45);
    expect(stark.length).toBeGreaterThan(0);
  });
});
