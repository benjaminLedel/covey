import * as THREE from "three";
import { afterEach, describe, expect, it, vi } from "vitest";
import { istFrei, laufFeldBauen } from "./laufweg";
import { erschaffeLeben, type Kollege, type Leben } from "./leben";
import { inGruppe, kasten, malenBeginnen, M } from "./mal";
import { raumAn } from "./plan";
import type { Plan, Punkt, Raum } from "./typen";

/* The same hand-made room as in laufweg.test.ts: 1200 × 850, door at
   x = 800 in the lower wall, two desks and a booth in front of the door; the
   corridor below it, the cross corridor with the front-desk queue on the left. */
function testPlan(): Plan {
  const x = 200, y = 40, w = 1200, h = 850, tuerX = 800;
  const raum: Raum = {
    id: "a", name: "Abteilung", farbe: "", leute: [],
    sitze: [{ x: 500, y: 430, dreh: 0 }, { x: 1000, y: 430, dreh: 0 }, { x: 700, y: 250, dreh: 0 }],
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
function testWelt(plan: Plan): THREE.Group {
  const welt = new THREE.Group();
  welt.position.set(-plan.breite / 2, 0, -plan.hoehe / 2);
  malenBeginnen(welt, false);
  kasten(500, 400, 140, 70, 40, 0xbd9d72);
  const { gruppe } = inGruppe(() => kasten(0, 0, 140, 70, 40, 0xbd9d72));
  gruppe.position.set(1000, 0, 400);
  M.welt!.add(gruppe);
  kasten(800, 700, 100, 100, 92, 0x6d8c5a);
  return welt;
}

const plan = testPlan();
const felder = laufFeldBauen(testWelt(plan), plan);
const kollegen = (z: Kollege["zustand"][]): Kollege[] =>
  z.map((zustand, i) => ({ id: "k" + i, slug: "kollege-" + i, ri: 0, i, zustand }));
const figur = (l: Leben, id: string) => l.figuren.find((f) => f.id === id)!;
/** Ticks until the figure has arrived (or a minute of simulated time passed). */
function laufen(l: Leben, id: string, dt = 0.05) {
  for (let t = 0; t < 1200 * 5 && figur(l, id).route.length; t++) l.tick(dt);
  expect(figur(l, id).route).toEqual([]);
}
const nah = (a: Punkt, b: Punkt) => Math.hypot(a.x - b.x, a.y - b.y) < 1e-6;

afterEach(() => vi.restoreAllMocks());

describe("leben", () => {
  it("lives 3000 ticks without error", () => {
    const l = erschaffeLeben(plan, felder, kollegen(["frei", "arbeitet", "frei"]), false);
    for (const f of l.figuren) f.pause = 0;
    for (let t = 0; t < 3000; t++) {
      l.tick(0.05);
      if (t === 500) l.zustandSetzen("k1", "wartet", "braucht eine Entscheidung");
      if (t === 1500) l.zustandSetzen("k1", "arbeitet", "macht weiter");
      if (t === 2000) l.felderSetzen(laufFeldBauen(testWelt(plan), plan));
      for (const e of ["katze", "besprechung", "flieger", "kuchen", "vogel", "aufmerksam"] as const) if (t === 100) l.ausloesen(e);
    }
    for (const f of l.figuren) expect(Number.isFinite(f.pos.x) && Number.isFinite(f.pos.y)).toBe(true);
  });

  it("walks to the front-desk queue when waiting and back home when decided", () => {
    const l = erschaffeLeben(plan, felder, kollegen(["arbeitet", "arbeitet", "schlaeft"]), false);
    l.zustandSetzen("k0", "wartet", "braucht eine Entscheidung");
    expect(figur(l, "k0").schritt).toBe("braucht eine Entscheidung");
    l.zustandSetzen("k1", "wartet");
    laufen(l, "k0");
    laufen(l, "k1");
    expect(figur(l, "k0").pos).toEqual(plan.tresen.plaetze[0]);
    expect(figur(l, "k1").pos).toEqual(plan.tresen.plaetze[1]);
    /* The first is decided: back to the seat — and the second moves up. */
    l.zustandSetzen("k0", "arbeitet", "macht weiter");
    laufen(l, "k0");
    laufen(l, "k1");
    expect(nah(figur(l, "k0").pos, plan.raeume[0].sitze[0])).toBe(true);
    expect(figur(l, "k1").pos).toEqual(plan.tresen.plaetze[0]);
  });

  it("starts a waiting colleague in the queue, and moves instantly with reduced motion", () => {
    const l = erschaffeLeben(plan, felder, kollegen(["wartet", "arbeitet"]), true);
    expect(figur(l, "k0").pos).toEqual(plan.tresen.plaetze[0]);
    l.zustandSetzen("k1", "wartet");
    expect(figur(l, "k1").route).toEqual([]);
    expect(figur(l, "k1").pos).toEqual(plan.tresen.plaetze[1]);
    l.zustandSetzen("k0", "arbeitet");
    expect(nah(figur(l, "k0").pos, plan.raeume[0].sitze[0])).toBe(true);
    expect(figur(l, "k1").pos).toEqual(plan.tresen.plaetze[0]);
  });

  it("never wanders to a blocked cell", () => {
    const l = erschaffeLeben(plan, felder, kollegen(["frei", "frei", "frei"]), false);
    let ziele = 0;
    const vorher = new Map<string, Punkt | null>();
    for (let t = 0; t < 6000; t++) {
      l.tick(0.05);
      for (const f of l.figuren) {
        const ziel = f.route.length ? f.route[f.route.length - 1] : null;
        const alt = vorher.get(f.id);
        if (ziel && (!alt || !nah(alt, ziel))) {
          ziele++;
          const daheim = nah(ziel, f.sitz);
          const ri = raumAn(plan, ziel);
          expect(daheim || (ri != null && istFrei(felder, ri, ziel)), JSON.stringify(ziel)).toBe(true);
        }
        vorher.set(f.id, ziel);
      }
    }
    expect(ziele).toBeGreaterThan(10);
  });

  it("waters with the visitor's can when nobody is free for three seconds", () => {
    const gegossen = vi.fn();
    const l = erschaffeLeben(plan, felder, kollegen(["arbeitet", "schlaeft", "gestoppt"]), false, { gegossen });
    expect(l.giessen("pflanze-1", { x: 1300, y: 100 })).toBe("selbst");
    expect(l.istDurstig("pflanze-1")).toBe(true);
    for (let t = 0; t < 58; t++) l.tick(0.05);
    expect(gegossen).not.toHaveBeenCalled();
    for (let t = 0; t < 4; t++) l.tick(0.05);
    expect(gegossen).toHaveBeenCalledWith("pflanze-1", "selbst");
    expect(l.istDurstig("pflanze-1")).toBe(false);
  });

  it("sends a free colleague to water, who stands beside the plant", () => {
    const gegossen = vi.fn();
    const l = erschaffeLeben(plan, felder, kollegen(["arbeitet", "frei"]), false, { gegossen });
    expect(l.giessen("pflanze-2", { x: 800, y: 700 })).toBe("kollege");
    const f = figur(l, "k1");
    expect(f.giesst).toBe("pflanze-2");
    const ziel = f.route[f.route.length - 1];
    expect(istFrei(felder, 0, ziel)).toBe(true);
    laufen(l, "k1");
    expect(gegossen).toHaveBeenCalledWith("pflanze-2", "kollege");
  });

  it("stops a clicked colleague's stroll and follows the pointer with the eyes", () => {
    const l = erschaffeLeben(plan, felder, kollegen(["frei", "arbeitet"]), false);
    const f = figur(l, "k0");
    f.pause = 0;
    vi.spyOn(Math, "random").mockReturnValue(0.9); /* a stroll somewhere in the room */
    l.tick(0.05);
    expect(f.route.length).toBeGreaterThan(0);
    l.ansprechen("k0", true);
    const da = { ...f.pos };
    for (let t = 0; t < 20; t++) l.tick(0.05);
    expect(f.pos).toEqual(da);
    l.ansprechen(null); /* the selection form: nobody selected */
    expect(f.angesprochen).toBe(false);
    l.tick(0.05);
    expect(f.pos).not.toEqual(da);
    l.blickAuf({ x: figur(l, "k1").pos.x + 100, y: figur(l, "k1").pos.y });
    expect(figur(l, "k1").blick).toEqual({ x: 1, y: 0 });
  });
});
