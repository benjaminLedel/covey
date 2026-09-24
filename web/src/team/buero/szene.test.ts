import * as THREE from "three";
import { describe, expect, it } from "vitest";
import type { Agent } from "../../api";
import { AUSSEN, INNEN, bauplan } from "./plan";
import { AUSSEN_VOLL, SOCKEL_H, START_DREHUNG, WAND_VOLL, amAussen, kameraSeiten, szeneBauen, wandHoch, type Lage } from "./szene";
import type { Gruppe, Plan, Raum, Zustand } from "./typen";

/* The scene runs in node with a real three.js and no renderer. What is worth
 * a test here is what breaks silently in a browser: an exception half-way
 * through the build leaves a half-furnished house; a body count that runs
 * away makes the page crawl at sixty people; a rebuild that does not replace
 * the old world stacks two houses on top of each other; and a cutaway that
 * cuts one side or three leaves rooms hidden or the building open. */

const NAMEN = ["Customer Support", "Software-Entwicklung", "Finanzen", "Operations", "Vertrieb", "Produkt"];

function belegschaft(n: number, abteilungen = 3): Gruppe[] {
  const je = Math.ceil(n / abteilungen);
  return Array.from({ length: abteilungen }, (_, g) => ({
    id: `d${g}`,
    name: NAMEN[g % NAMEN.length],
    farbe: ["#6d8c5a", "#b0603a", ""][g % 3],
    leute: Array.from({ length: Math.max(0, Math.min(je, n - g * je)) }, (_, i) => ({
      id: `a${g}-${i}`,
      slug: `agent-${g}-${i}`,
      display_name: `Agent ${g}.${i}`,
    })) as Agent[],
  })).filter((g) => g.leute.length > 0);
}

const machPlan = (n: number): Plan => {
  const plan = bauplan(belegschaft(n, Math.min(6, Math.max(2, Math.round(n / 8)))), 3200,
    { besprechung: "Besprechung", kueche: "Teeküche", lounge: "Lounge" });
  /* Until bauplan sets the stairwell itself: where the prototype puts it. */
  plan.treppe ??= { x: plan.quer.mitte, y: plan.hoehe - AUSSEN - 96 };
  return plan;
};

const lage = (zustand: Zustand, nacht = false, etage = 0, etagen = 1): Lage =>
  ({ zustand: () => zustand, nacht, dichte: 1, drehung: START_DREHUNG, etage, etagen });

/** Top-level bodies whose world position lies inside a box on the plan. */
const koerperIn = (welt: THREE.Group, x0: number, x1: number, y0: number, y1: number) =>
  welt.children.filter((o) => o.position.x >= x0 && o.position.x <= x1 && o.position.z >= y0 && o.position.z <= y1);

describe("szeneBauen", () => {
  for (const n of [8, 25, 60]) {
    it(`builds a house for ${n} colleagues`, () => {
      const szene = new THREE.Scene();
      const bau = szeneBauen(szene, null, machPlan(n), lage("arbeitet"));
      expect(szene.children).toContain(bau.welt);
      expect(bau.koerper).toBe(bau.welt.children.length);
      expect(bau.koerper).toBeGreaterThan(50);
      expect(bau.koerper).toBeLessThan(4000);
    });
  }

  it("stays within a sane body count for 25 colleagues", () => {
    const bau = szeneBauen(new THREE.Scene(), null, machPlan(25), lage("schlaeft"));
    expect(bau.koerper).toBeGreaterThanOrEqual(500);
    expect(bau.koerper).toBeLessThanOrEqual(1200);
  });

  it("lights working rooms only at night", () => {
    const plan = machPlan(25);
    const tag = szeneBauen(new THREE.Scene(), null, plan, lage("arbeitet"));
    expect(tag.lampen).toHaveLength(0);
    expect(tag.decken).toHaveLength(0);
    const nacht = szeneBauen(new THREE.Scene(), null, plan, lage("arbeitet", true));
    expect(nacht.lampen.length).toBeGreaterThan(0);
    expect(nacht.lampen.length).toBeLessThanOrEqual(14);
    expect(nacht.decken.length).toBeGreaterThan(0);
    expect(nacht.decken.length).toBeLessThanOrEqual(8);
    const schlaf = szeneBauen(new THREE.Scene(), null, plan, lage("schlaeft", true));
    expect(schlaf.lampen).toHaveLength(0);
    expect(schlaf.decken).toHaveLength(0);
    expect(plan.raeume.every((r) => !r.hell)).toBe(true);
  });

  it("replaces the old world on a rebuild", () => {
    const szene = new THREE.Scene();
    const plan = machPlan(25);
    const erst = szeneBauen(szene, null, plan, lage("schlaeft"));
    let entsorgt = 0;
    erst.welt.traverse((o) => {
      const m = o as THREE.Mesh;
      if (m.isMesh) m.geometry.addEventListener("dispose", () => entsorgt++);
    });
    const dann = szeneBauen(szene, erst.welt, plan, { ...lage("arbeitet"), drehung: START_DREHUNG + Math.PI / 2 });
    expect(szene.children).not.toContain(erst.welt);
    expect(szene.children).toContain(dann.welt);
    expect(szene.children.filter((o) => o.name === "welt")).toHaveLength(1);
    expect(entsorgt).toBeGreaterThan(0);
    /* The two lights are found again, not added a second time. */
    expect(szene.children.filter((o) => o.name === "licht")).toHaveLength(1);
    expect(szene.children.filter((o) => o.name === "himmel")).toHaveLength(1);
  });
});

describe("floors", () => {
  it("builds a stairwell only when the house has several floors", () => {
    const plan = machPlan(25);
    const t = plan.treppe;
    const box = (w: THREE.Group) => koerperIn(w, plan.quer.x, plan.quer.x + plan.quer.w, t.y - 50, t.y + 120).length;
    const eins = szeneBauen(new THREE.Scene(), null, plan, lage("schlaeft", false, 0, 1));
    const zwei = szeneBauen(new THREE.Scene(), null, plan, lage("schlaeft", false, 0, 2));
    /* Nine steps, a landing, eight posts, two rails and the sign. */
    expect(box(zwei.welt) - box(eins.welt)).toBeGreaterThanOrEqual(20);
  });

  it("puts the reception on the ground floor only", () => {
    const plan = machPlan(25);
    const tresen = (w: THREE.Group) =>
      w.children.filter((o) => Math.abs(o.position.z - plan.tresen.y) < 1 && Math.abs(o.position.x - plan.quer.mitte) < 1);
    const unten = szeneBauen(new THREE.Scene(), null, plan, lage("schlaeft", false, 0, 2));
    const oben = szeneBauen(new THREE.Scene(), null, plan, lage("schlaeft", false, 1, 2));
    expect(tresen(unten.welt)).toHaveLength(1);
    expect(tresen(oben.welt)).toHaveLength(0);
  });

  it("keeps the corridor pieces clear of the stairs", () => {
    const plan = machPlan(25);
    const zwei = szeneBauen(new THREE.Scene(), null, plan, lage("schlaeft", false, 1, 2));
    /* Groups placed by flurdingBauen along the cross corridor walls. */
    const imGang = zwei.welt.children.filter((o) => o instanceof THREE.Group
      && o.position.x > plan.quer.x && o.position.x < plan.quer.x + plan.quer.w
      && o.position.z > plan.treppe.y - 120);
    expect(imGang).toHaveLength(0);
  });
});

describe("the cutaway", () => {
  it("cuts exactly two sides in each of the four quarter views", () => {
    const gesehen = new Set<string>();
    for (let k = -4; k < 8; k++) {
      const s = kameraSeiten(START_DREHUNG + (k * Math.PI) / 2);
      const offen = Object.entries(s).filter(([, v]) => v).map(([n]) => n);
      expect(offen).toHaveLength(2);
      expect(s.oben && s.unten).toBe(false);
      expect(s.links && s.rechts).toBe(false);
      gesehen.add(offen.sort().join("+"));
    }
    expect(gesehen.size).toBe(4);
  });

  it("does not flicker during a turn", () => {
    const vor = kameraSeiten(START_DREHUNG);
    expect(kameraSeiten(START_DREHUNG + 0.6)).toEqual(vor);
    expect(kameraSeiten(START_DREHUNG - 0.6)).toEqual(vor);
  });

  /* Two rooms side by side in a hand-made plan, independent of bauplan. */
  const raum = (x: number, y: number, w: number, h: number, name: string): Raum => ({
    id: name, name, farbe: "", leute: [], sitze: [], x, y, w, h, spalten: 1, zeilen: 1, oben: true,
    flur: 0, flurY: 0, nr: 1, tuerX: x + w / 2, wand: y + h, ri: 1,
    innen: { x: 0, y: 0 }, aussen: { x: 0, y: 0 }, treff: [],
  });
  const links = raum(AUSSEN, AUSSEN, 400, 300, "links");
  const rechts = raum(AUSSEN + 400 + INNEN, AUSSEN, 400, 300, "rechts");
  const plan = { breite: AUSSEN + 400 + INNEN + 400 + AUSSEN, hoehe: 400, raeume: [links, rechts] };

  it("knows which rooms touch the outer wall", () => {
    expect(amAussen(links, plan)).toEqual({ oben: true, unten: false, links: true, rechts: false });
    expect(amAussen(rechts, plan)).toEqual({ oben: true, unten: false, links: false, rechts: true });
  });

  it("keeps the far walls full and cuts the near ones to the plinth", () => {
    /* At the start view the camera looks from below right: those sides are cut. */
    const d = START_DREHUNG;
    expect(wandHoch(links, "oben", plan, d)).toBe(AUSSEN_VOLL);
    expect(wandHoch(links, "links", plan, d)).toBe(AUSSEN_VOLL);
    expect(wandHoch(links, "unten", plan, d)).toBe(SOCKEL_H);
    /* The shared wall faces the camera from the left room's side. */
    expect(wandHoch(links, "rechts", plan, d)).toBe(SOCKEL_H);
    expect(wandHoch(rechts, "links", plan, d)).toBe(SOCKEL_H);
    /* Turned half-way round, the far sides swap. */
    const h = START_DREHUNG + Math.PI;
    expect(wandHoch(links, "oben", plan, h)).toBe(SOCKEL_H);
    expect(wandHoch(rechts, "unten", plan, h)).toBe(WAND_VOLL);
  });
});
