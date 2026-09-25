import * as THREE from "three";
import { beforeEach, describe, expect, it } from "vitest";
import { FARBEN, M, malenBeginnen } from "../mal";
import { PFLANZEN, pflanzeBauen } from "./pflanzen";
import { PLAETZE } from "./plaetze";
import { SCHRAENKE } from "./schraenke";
import { STUEHLE } from "./stuehle";

/* Every catalogue piece is called once against real three.js in node. What
 * this catches: a palette key that does not exist (a mesh with an undefined
 * colour), a workplace that forgets to return its screens, and a cabinet that
 * grows out of the footprint it was given — the cabinets stand side by side
 * along a wall, and one that sticks out cuts into its neighbour. */

/** How far a cabinet may stick out of its footprint, measured on the drawn
 *  sizes: handles and mouldings sit proud of the front by up to 2.5. The
 *  bevel `kasten` rounds its edges with is taken off before comparing — it
 *  adds up to another 2.5 on every side of every box and would otherwise
 *  hide a real overhang behind a generous number. */
const TOLERANZ = 3;

/** The footprint a mesh actually claims, in the xz plane. */
function grundriss(m: THREE.Mesh): { x: number; z: number } {
  const box = new THREE.Box3().setFromObject(m);
  const g = m.geometry as THREE.BufferGeometry & { parameters?: { options?: { bevelSize?: number } } };
  const fase = g.type === "ExtrudeGeometry" ? g.parameters?.options?.bevelSize ?? 0 : 0;
  return {
    x: Math.max(Math.abs(box.min.x), Math.abs(box.max.x)) - fase,
    z: Math.max(Math.abs(box.min.z), Math.abs(box.max.z)) - fase,
  };
}

let welt: THREE.Group;

function meshes(): THREE.Mesh[] {
  const liste: THREE.Mesh[] = [];
  welt.traverse((o) => {
    if ((o as THREE.Mesh).isMesh) liste.push(o as THREE.Mesh);
  });
  return liste;
}

function farbenGesetzt(): void {
  for (const m of meshes()) {
    const stoff = m.material as THREE.MeshLambertMaterial;
    expect(stoff.color, "material without colour").toBeDefined();
    expect(Number.isNaN(stoff.color.r)).toBe(false);
  }
}

function neu(nacht = false): void {
  welt = new THREE.Group();
  malenBeginnen(welt, nacht);
}

describe("catalogue: plants, workplaces, chairs, cabinets", () => {
  beforeEach(() => neu());

  it("has the prototype's counts", () => {
    expect(PFLANZEN).toHaveLength(14);
    expect(PLAETZE).toHaveLength(15);
    expect(STUEHLE).toHaveLength(15);
    expect(SCHRAENKE).toHaveLength(18);
  });

  it("builds every plant", () => {
    PFLANZEN.forEach((f, i) => {
      neu();
      f(0, 0);
      expect(meshes().length, `plant ${i}`).toBeGreaterThan(0);
      farbenGesetzt();
    });
  });

  it("builds a plant into its own group that knows its key and spot", () => {
    const gruppe = pflanzeBauen(40, -20, "Support");
    expect(gruppe.parent).toBe(welt);
    expect(gruppe.userData.pflanze).toBe("Support");
    expect(gruppe.userData.punkt).toEqual({ x: 40, y: -20 });
    expect(gruppe.children.length).toBeGreaterThan(0);
    expect(M.ziel).toBeNull();
  });

  it("returns the screens of every workplace, on and off", () => {
    PLAETZE.forEach((f, i) => {
      for (const an of [false, true]) {
        neu();
        const scheiben = f({ x: 0, y: 0 }, an);
        expect(scheiben.length, `workplace ${i}`).toBeGreaterThan(0);
        for (const s of scheiben) {
          expect(s).toBeInstanceOf(THREE.Mesh);
          expect(s.parent).toBe(welt);
          const farbe = (s.material as THREE.MeshLambertMaterial).color.getHex();
          expect(farbe).toBe(an ? FARBEN.schirmAn : FARBEN.schirm);
        }
        farbenGesetzt();
      }
    });
  });

  it("builds every chair", () => {
    STUEHLE.forEach((f, i) => {
      neu();
      f(0, 0);
      expect(meshes().length, `chair ${i}`).toBeGreaterThan(0);
      farbenGesetzt();
    });
  });

  it("keeps every cabinet within its footprint", () => {
    SCHRAENKE.forEach((f, i) => {
      for (const [b, t] of [[130, 38], [90, 38]]) {
        neu();
        f(0, 0, b, t);
        welt.updateMatrixWorld(true);
        const teile = meshes();
        expect(teile.length, `cabinet ${i}`).toBeGreaterThan(0);
        farbenGesetzt();
        for (const m of teile) {
          // A floor marking (the lockers' swing zone) lies in front of the
          // footprint on purpose; it is flat and cannot cut into a neighbour.
          if (m.geometry.type === "PlaneGeometry") continue;
          const { x, z } = grundriss(m);
          expect(x, `cabinet ${i} at ${b}×${t}: x`).toBeLessThanOrEqual(b / 2 + TOLERANZ);
          expect(z, `cabinet ${i} at ${b}×${t}: z`).toBeLessThanOrEqual(t / 2 + TOLERANZ);
        }
      }
    });
  });

  it("builds everything at night and inside a room's accent", () => {
    for (const nacht of [false, true]) {
      neu(nacht);
      M.akzent = FARBEN.akzent1;
      expect(() => {
        PFLANZEN.forEach((f) => f(0, 0));
        PLAETZE.forEach((f) => f({ x: 0, y: 0 }, true));
        STUEHLE.forEach((f) => f(0, 0));
        SCHRAENKE.forEach((f) => f(0, 0, 130, 38));
      }).not.toThrow();
      farbenGesetzt();
    }
  });
});
