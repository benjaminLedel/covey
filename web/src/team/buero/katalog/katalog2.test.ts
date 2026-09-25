import * as THREE from "three";
import { describe, expect, it } from "vitest";
import { M, akzentFuer, malenBeginnen } from "../mal";
import { BESPRECHUNGEN } from "./besprechungen";
import { FLURDINGE, FLUR_WAND } from "./flurdinge";
import { GRUPPEN, type Flaechenstueck } from "./gruppen";
import { KUECHEN } from "./kuechen";
import { KLEINKRAM, LEUCHTEN, STEHLEUCHTEN, WANDLEUCHTEN } from "./leuchten";
import { LOUNGES, loungeBauen } from "./lounges";

/* The area pieces, the corridor and the lamps run in node against real three:
 * nothing throws, each piece draws something, and a lounge island stays on
 * its island — it is the only kind the plan packs edge to edge. */

function neueWelt(nacht = false): THREE.Group {
  const welt = new THREE.Group();
  malenBeginnen(welt, nacht);
  return welt;
}

function meshes(welt: THREE.Object3D): THREE.Mesh[] {
  const liste: THREE.Mesh[] = [];
  welt.traverse((o) => {
    if ((o as THREE.Mesh).isMesh) liste.push(o as THREE.Mesh);
  });
  return liste;
}

/** The footprint (x, z) of everything drawn, in world coordinates. A plant
 *  counts as its nominal disc of radius 28 around its spot, as in the
 *  prototype's harness: its leaves may hang over the edge of an island, its
 *  pot may not. */
function grundriss(welt: THREE.Object3D): THREE.Box3 {
  welt.updateMatrixWorld(true);
  const box = new THREE.Box3();
  const besuchen = (o: THREE.Object3D) => {
    const punkt = o.userData.punkt as { x: number; y: number } | undefined;
    if (o.userData.pflanze !== undefined && punkt) {
      box.expandByPoint(new THREE.Vector3(punkt.x - 28, 0, punkt.y - 28));
      box.expandByPoint(new THREE.Vector3(punkt.x + 28, 0, punkt.y + 28));
      return;
    }
    if ((o as THREE.Mesh).isMesh) box.union(new THREE.Box3().setFromObject(o));
    for (const kind of o.children) besuchen(kind);
  };
  besuchen(welt);
  return box;
}

const ALLE_FLAECHEN: [string, Flaechenstueck[], [number, number][]][] = [
  ["GRUPPEN", GRUPPEN, [[260, 210]]],
  ["BESPRECHUNGEN", BESPRECHUNGEN, [[460, 380], [280, 230]]],
  ["KUECHEN", KUECHEN, [[460, 380], [280, 230]]],
];

const INSELN: [number, number][] = [[260, 240], [330, 240], [386, 380], [690, 380], [300, 300]];

describe("area pieces", () => {
  for (const [name, liste, masse] of ALLE_FLAECHEN) {
    for (const [b, t] of masse) {
      it(`${name} draws every kind at ${b}×${t}`, () => {
        liste.forEach((stueck) => {
          const welt = neueWelt();
          stueck(0, 0, b, t);
          expect(meshes(welt).length).toBeGreaterThan(0);
        });
      });
    }
  }
});

describe("lounges", () => {
  it("has seven kinds", () => {
    expect(LOUNGES).toHaveLength(7);
  });

  for (const [b, t] of INSELN) {
    it(`every kind stays inside a ${b}×${t} island`, () => {
      LOUNGES.forEach((stueck, i) => {
        const welt = neueWelt();
        stueck(0, 0, b, t);
        const zahl = meshes(welt).length;
        expect(zahl, `kind ${i + 1}: ${zahl} meshes`).toBeGreaterThanOrEqual(15);
        expect(zahl, `kind ${i + 1}: ${zahl} meshes`).toBeLessThanOrEqual(90);
        const box = grundriss(welt);
        expect(box.min.x, `kind ${i + 1} left`).toBeGreaterThanOrEqual(-b / 2 - 3);
        expect(box.max.x, `kind ${i + 1} right`).toBeLessThanOrEqual(b / 2 + 3);
        expect(box.min.z, `kind ${i + 1} back`).toBeGreaterThanOrEqual(-t / 2 - 3);
        expect(box.max.z, `kind ${i + 1} front`).toBeLessThanOrEqual(t / 2 + 3);
      });
    });
  }

  it("loungeBauen picks a kind by number", () => {
    const welt = neueWelt();
    loungeBauen({ nr: 3, x: 0, y: 0, b: 330, t: 240 }, 1);
    expect(meshes(welt).length).toBeGreaterThan(0);
  });
});

describe("corridor pieces", () => {
  it("has fourteen, the reception desk first", () => {
    expect(FLURDINGE).toHaveLength(14);
    expect(FLUR_WAND).toHaveLength(13);
    expect(FLUR_WAND[0]).toBe(FLURDINGE[1]);
  });

  it("every piece is long enough to be seen from above", () => {
    FLURDINGE.forEach((stueck, i) => {
      const welt = neueWelt();
      stueck(0, 0);
      expect(meshes(welt).length).toBeGreaterThanOrEqual(1);
      const box = grundriss(welt);
      const lang = Math.max(box.max.x - box.min.x, box.max.z - box.min.z);
      expect(lang, `piece ${i + 1}`).toBeGreaterThanOrEqual(90);
    });
  });
});

describe("lamps", () => {
  it("draws every lamp dark and lit", () => {
    for (const an of [false, true]) {
      LEUCHTEN.forEach((leuchte) => {
        const welt = neueWelt();
        leuchte(0, 0, an);
        expect(meshes(welt).length).toBeGreaterThan(0);
      });
    }
  });

  it("only the lit lamps carry light", () => {
    const lichter = (an: boolean) => {
      const welt = neueWelt();
      LEUCHTEN.forEach((l) => l(0, 0, an));
      let n = 0;
      welt.traverse((o) => { if ((o as THREE.Light).isLight) n++; });
      return n;
    };
    expect(lichter(false)).toBe(0);
    expect(lichter(true)).toBe(7);
  });

  it("the selections are entries of the catalogue", () => {
    expect(STEHLEUCHTEN).toEqual([LEUCHTEN[2]]);
    expect(WANDLEUCHTEN).toEqual([LEUCHTEN[5]]);
    expect(KLEINKRAM).toEqual([LEUCHTEN[6], LEUCHTEN[7]]);
  });
});

describe("at night with a room accent", () => {
  it("nothing throws", () => {
    const welt = neueWelt(true);
    M.akzent = akzentFuer("Customer Support");
    expect(() => {
      for (const [, liste, masse] of ALLE_FLAECHEN) for (const s of liste) s(0, 0, ...masse[0]);
      for (const [b, t] of INSELN) for (const s of LOUNGES) s(0, 0, b, t);
      for (const s of FLURDINGE) s(0, 0);
      for (const l of LEUCHTEN) l(0, 0, true);
    }).not.toThrow();
    expect(meshes(welt).length).toBeGreaterThan(0);
  });
});
