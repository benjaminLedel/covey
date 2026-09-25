import { describe, expect, it } from "vitest";
import * as THREE from "three";
import { M, malenBeginnen } from "./mal";
import { belegung } from "./plan";
import type { Raum, Sitz } from "./typen";
import { charakterEinrichten, charakterVon, type Charakter, type LeuchteBauen } from "./charakter";

/* The character furnishing runs against the real catalogue and the real
 * occupancy grid; only the room is made up. What is checked is what would be
 * visible as a fault in the office: an exception, an empty room, a piece outside
 * the walls, and a density switch that does nothing. */

const X = 100, Y = 50, W = 1200, H = 850;

function raum(name: string, oben: boolean): Raum {
  /* Two rows of four seats, the door in the wall towards the corridor: the
     lower wall for a room above the corridor, the upper one otherwise. */
  const sitze: Sitz[] = [];
  for (let zeile = 0; zeile < 2; zeile++)
    for (let s = 0; s < 4; s++)
      sitze.push({ x: X + 200 + s * 260, y: Y + 300 + zeile * 280, dreh: 0 });
  const tuerX = X + 120;
  const wand = oben ? Y + H : Y;
  const ri = oben ? 1 : -1;
  return {
    id: name, name, farbe: "", leute: [], sitze,
    x: X, y: Y, w: W, h: H, spalten: 4, zeilen: 2, oben,
    flur: 0, flurY: oben ? Y + H + 80 : Y - 80, nr: 1,
    tuerX, wand, ri,
    innen: { x: tuerX, y: oben ? Y + H - 30 : Y + 30 },
    aussen: { x: tuerX, y: oben ? Y + H + 30 : Y - 30 },
    treff: [],
  };
}

/* The scene blocks the door and the desks before the character sees the room. */
function belegt(r: Raum) {
  const b = belegung(r);
  b.sperre(r.tuerX - 40, r.oben ? r.y + r.h - 60 : r.y, 80, 60);
  for (const p of r.sitze) b.sperre(p.x - 100, p.y - 110, 200, 220);
  return b;
}

const NAMEN: Record<Charakter, string> = {
  callcenter: "Customer Support",
  kontor: "Vertrieb",
  labor: "Operations",
  werkstatt: "Software-Entwicklung",
  kanzlei: "Finanzen",
  startup: "Irgendwas",
  atelier: "Redaktion",
  studio: "Kultur",
  bibliothek: "Research",
  loft: "Geschäftsführung",
  archiv: "Logistik",
};

const leuchte: LeuchteBauen = (liste, x, y) => liste[0](x, y, false);

function einrichten(name: string, oben: boolean, dichte: number) {
  const welt = new THREE.Group();
  malenBeginnen(welt, false);
  M.welt = welt;
  const r = raum(name, oben);
  charakterEinrichten(r, belegt(r), dichte, leuchte);
  let meshes = 0;
  welt.traverse((o) => { if ((o as THREE.Mesh).isMesh) meshes++; });
  return { welt, r, meshes };
}

describe("charakterVon", () => {
  it("maps each sample name to its character", () => {
    for (const [art, name] of Object.entries(NAMEN)) expect(charakterVon(name)).toBe(art);
  });
  it("takes the first match: Customer Support Engineering is support", () => {
    expect(charakterVon("Customer Support Engineering")).toBe("callcenter");
  });
});

describe("charakterEinrichten", () => {
  for (const [art, name] of Object.entries(NAMEN))
    for (const oben of [false, true])
      it(`furnishes ${art} (oben=${oben}) inside the room`, () => {
        const { welt, r, meshes } = einrichten(name, oben, 1);
        expect(meshes).toBeGreaterThan(10);
        welt.updateMatrixWorld(true);
        for (const kind of welt.children) {
          const box = new THREE.Box3().setFromObject(kind);
          if (box.isEmpty()) continue;
          expect(box.min.x).toBeGreaterThanOrEqual(r.x - 40);
          expect(box.max.x).toBeLessThanOrEqual(r.x + r.w + 40);
          expect(box.min.z).toBeGreaterThanOrEqual(r.y - 40);
          expect(box.max.z).toBeLessThanOrEqual(r.y + r.h + 40);
        }
      });

  it("is deterministic", () => {
    expect(einrichten("Finanzen", false, 1).meshes).toBe(einrichten("Finanzen", false, 1).meshes);
  });

  it("adds less at low density than at high density", () => {
    for (const name of Object.values(NAMEN))
      expect(einrichten(name, false, 0.45).meshes).toBeLessThan(einrichten(name, false, 1.7).meshes);
  });
});
