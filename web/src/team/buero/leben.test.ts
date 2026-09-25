import * as THREE from "three";
import { afterEach, describe, expect, it, vi } from "vitest";
import { istFrei, laufFeldBauen } from "./laufweg";
import { erschaffeLeben, type Kollege, type Leben } from "./leben";
import { inGruppe, kasten, malenBeginnen, M } from "./mal";
import { raumAn } from "./plan";
import type { Haus, Plan, Punkt, Raum } from "./typen";

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
    treppe: { x: 87, y: 960 },
  };
}

/* The upper floor: a narrower office on the left, a lounge beside it. The
   same corridors and the stairs on the same spot; no grid (never shown). */
function obenPlan(): Plan {
  const p = testPlan();
  const buero = p.raeume[0];
  const zimmer = (x: number, w: number, extra: Partial<Raum>): Raum => ({
    ...buero,
    x, w, tuerX: x + w / 2,
    innen: { x: x + w / 2, y: buero.innen.y },
    aussen: { x: x + w / 2, y: buero.aussen.y },
    ...extra,
  });
  p.raeume = [
    zimmer(200, 560, { id: "b", sitze: [{ x: 350, y: 300, dreh: 0 }, { x: 600, y: 300, dreh: 0 }] }),
    zimmer(780, 620, { id: "lounge", gem: "lounge", sitze: [], treff: [{ x: 1000, y: 600 }, { x: 1200, y: 600 }] }),
  ];
  return p;
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
const haus: Haus = { etagen: [{ nr: 0, gruppen: [], plan }] };
const zweiStock: Haus = { etagen: [{ nr: 0, gruppen: [], plan }, { nr: 1, gruppen: [], plan: obenPlan() }] };
const kollegen = (z: Kollege["zustand"][]): Kollege[] =>
  z.map((zustand, i) => ({ id: "k" + i, slug: "kollege-" + i, etage: 0, ri: 0, i, zustand }));
/** Two below (seats 0 and 1 on floor 0), two above (seats 0 and 1 on floor 1). */
const vierLeute = (z: Kollege["zustand"][]): Kollege[] =>
  z.map((zustand, n) => ({ id: "k" + n, slug: "kollege-" + n, etage: n < 2 ? 0 : 1, ri: 0, i: n % 2, zustand }));
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
    const l = erschaffeLeben(haus, [felder], kollegen(["frei", "arbeitet", "frei"]), false);
    for (const f of l.figuren) f.pause = 0;
    for (let t = 0; t < 3000; t++) {
      l.tick(0.05);
      if (t === 500) l.zustandSetzen("k1", "wartet", "braucht eine Entscheidung");
      if (t === 1500) l.zustandSetzen("k1", "arbeitet", "macht weiter");
      if (t === 2000) l.felderSetzen(0, laufFeldBauen(testWelt(plan), plan));
      for (const e of ["katze", "besprechung", "flieger", "kuchen", "vogel", "aufmerksam"] as const) if (t === 100) l.ausloesen(e);
    }
    for (const f of l.figuren) expect(Number.isFinite(f.pos.x) && Number.isFinite(f.pos.y)).toBe(true);
  });

  it("walks to the front-desk queue when waiting and back home when decided", () => {
    const l = erschaffeLeben(haus, [felder], kollegen(["arbeitet", "arbeitet", "schlaeft"]), false);
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
    const l = erschaffeLeben(haus, [felder], kollegen(["wartet", "arbeitet"]), true);
    expect(figur(l, "k0").pos).toEqual(plan.tresen.plaetze[0]);
    l.zustandSetzen("k1", "wartet");
    expect(figur(l, "k1").route).toEqual([]);
    expect(figur(l, "k1").pos).toEqual(plan.tresen.plaetze[1]);
    l.zustandSetzen("k0", "arbeitet");
    expect(nah(figur(l, "k0").pos, plan.raeume[0].sitze[0])).toBe(true);
    expect(figur(l, "k1").pos).toEqual(plan.tresen.plaetze[0]);
  });

  it("never wanders to a blocked cell", () => {
    const l = erschaffeLeben(haus, [felder], kollegen(["frei", "frei", "frei"]), false);
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
    const l = erschaffeLeben(haus, [felder], kollegen(["arbeitet", "schlaeft", "gestoppt"]), false, { gegossen });
    expect(l.giessen(0, "pflanze-1", { x: 1300, y: 100 })).toBe("selbst");
    expect(l.istDurstig("pflanze-1")).toBe(true);
    for (let t = 0; t < 58; t++) l.tick(0.05);
    expect(gegossen).not.toHaveBeenCalled();
    for (let t = 0; t < 4; t++) l.tick(0.05);
    expect(gegossen).toHaveBeenCalledWith("pflanze-1", "selbst");
    expect(l.istDurstig("pflanze-1")).toBe(false);
  });

  it("sends a free colleague to water, who stands beside the plant", () => {
    const gegossen = vi.fn();
    const l = erschaffeLeben(haus, [felder], kollegen(["arbeitet", "frei"]), false, { gegossen });
    expect(l.giessen(0, "pflanze-2", { x: 800, y: 700 })).toBe("kollege");
    const f = figur(l, "k1");
    expect(f.giesst).toBe("pflanze-2");
    const ziel = f.route[f.route.length - 1];
    expect(istFrei(felder, 0, ziel)).toBe(true);
    laufen(l, "k1");
    expect(gegossen).toHaveBeenCalledWith("pflanze-2", "kollege");
  });

  it("stops a clicked colleague's stroll and follows the pointer with the eyes", () => {
    const l = erschaffeLeben(haus, [felder], kollegen(["frei", "arbeitet"]), false);
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

  it("takes the stairs to the lounge upstairs and comes home over them", () => {
    const wechsel = vi.fn();
    const l = erschaffeLeben(zweiStock, [felder, null], vierLeute(["frei", "arbeitet", "schlaeft", "schlaeft"]), false, { etage: wechsel });
    const f = figur(l, "k0");
    f.pause = 0;
    /* The idea: a shared room (0.1), not the kitchen (0.9), not the meeting
       room (0.9) — the lounge; another floor (0.1), the only other one. */
    const folge = [0.1, 0.9, 0.9, 0.1, 0];
    vi.spyOn(Math, "random").mockImplementation(() => folge.shift() ?? 0.5);
    l.tick(0.05);
    expect(f.treppe).toEqual({ etage: 1, gem: "lounge" });
    expect(f.route[f.route.length - 1]).toEqual(plan.treppe);
    laufen(l, "k0");
    expect(f.hier).toBe(1);
    expect(wechsel).toHaveBeenCalledWith("k0", 0, 1);
    expect(l.sichtbar(1)).toContain(f);
    expect(l.sichtbar(0)).not.toContain(f);
    l.tick(0.05); /* upstairs, the target is decided with that floor's plan */
    laufen(l, "k0");
    const oben = zweiStock.etagen[1].plan;
    expect(raumAn(oben, f.pos)).toBe(1);
    expect(oben.raeume[1].gem).toBe("lounge");
    /* After a while: back over the stairs to the own seat. */
    let daheim = false;
    for (let t = 0; t < 20 * 60 * 5 && !daheim; t++) {
      l.tick(0.05);
      daheim = f.hier === 0 && !f.route.length && nah(f.pos, f.sitz);
    }
    expect(daheim).toBe(true);
    expect(wechsel).toHaveBeenCalledWith("k0", 1, 0);
  });

  it("comes down from the upper floor to wait at the front desk, and goes back up", () => {
    const l = erschaffeLeben(zweiStock, [felder, null], vierLeute(["arbeitet", "arbeitet", "arbeitet", "arbeitet"]), false);
    const f = figur(l, "k2");
    expect(f.etage).toBe(1);
    l.zustandSetzen("k2", "wartet", "braucht eine Entscheidung");
    expect(f.treppe).toEqual({ etage: 0, warten: true });
    laufen(l, "k2");
    expect(f.hier).toBe(0);
    l.tick(0.05);
    laufen(l, "k2");
    expect(f.pos).toEqual(plan.tresen.plaetze[0]);
    l.zustandSetzen("k2", "arbeitet", "macht weiter");
    laufen(l, "k2");
    expect(f.hier).toBe(1);
    l.tick(0.05);
    laufen(l, "k2");
    expect(f.hier).toBe(1);
    expect(nah(f.pos, f.sitz)).toBe(true);
  });

  it("with reduced motion, the waiting one from upstairs stands in the queue at once", () => {
    const l = erschaffeLeben(zweiStock, [felder, null], vierLeute(["arbeitet", "arbeitet", "wartet", "arbeitet"]), true);
    expect(figur(l, "k2").hier).toBe(0);
    expect(figur(l, "k2").pos).toEqual(plan.tresen.plaetze[0]);
    l.zustandSetzen("k3", "wartet");
    expect(figur(l, "k3").hier).toBe(0);
    expect(figur(l, "k3").pos).toEqual(plan.tresen.plaetze[1]);
    l.zustandSetzen("k2", "arbeitet");
    expect(figur(l, "k2").hier).toBe(1);
    expect(nah(figur(l, "k2").pos, figur(l, "k2").sitz)).toBe(true);
    expect(figur(l, "k3").pos).toEqual(plan.tresen.plaetze[0]);
  });

  it("moves everybody on both floors, and wanders only onto free floor where there is a grid", () => {
    const l = erschaffeLeben(zweiStock, [felder, null], vierLeute(["frei", "frei", "frei", "frei"]), false);
    const start = new Map(l.figuren.map((f) => [f.id, { ...f.pos }]));
    const bewegt = new Set<string>();
    const vorher = new Map<string, Punkt | null>();
    for (let t = 0; t < 3000; t++) {
      l.tick(0.05);
      for (const f of l.figuren) {
        if (!nah(f.pos, start.get(f.id)!)) bewegt.add(f.id);
        const ziel = f.route.length ? f.route[f.route.length - 1] : null;
        const alt = vorher.get(f.id);
        if (ziel && f.hier === 0 && (!alt || !nah(alt, ziel))) {
          const ri = raumAn(plan, ziel);
          const erlaubt = nah(ziel, f.sitz) || nah(ziel, plan.treppe) || (ri != null && istFrei(felder, ri, ziel));
          expect(erlaubt, JSON.stringify(ziel)).toBe(true);
        }
        vorher.set(f.id, ziel);
      }
    }
    expect([...bewegt].sort()).toEqual(["k0", "k1", "k2", "k3"]);
  });
});
