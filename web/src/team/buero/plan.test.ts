import { describe, expect, it } from "vitest";
import type { Agent } from "../../api";
import { AUSSEN, bauplan, raumAn, spaltenFuer, weg, type Gruppe, type Plan, type Punkt } from "./plan";

/* Der Bauplan lässt sich ohne Browser prüfen, und das ist der Grund, warum er
 * eine eigene Datei ist. Zwei Dinge sind hier wichtig genug für einen Test:
 *
 *   1. Der Plan skaliert. Acht Kollegen und hundertvierzig ergeben dasselbe
 *      Haus in zwei Größen — nicht eine leere Vorlage und eine geplatzte.
 *   2. Niemand geht durch eine Wand. Genau das war der Fehler, den der
 *      Prototyp hatte: Der Weg wurde vom HEIMATZIMMER aus gerechnet, und wer
 *      in der Teeküche stand, lief auf der Geraden nach Hause.
 */

const NAMEN = ["Customer Support", "Software-Entwicklung", "Finanzen", "Operations", "Vertrieb", "Produkt"];

function belegschaft(n: number, abteilungen = 3): Gruppe[] {
  const je = Math.ceil(n / abteilungen);
  return Array.from({ length: abteilungen }, (_, g) => ({
    id: `d${g}`,
    name: NAMEN[g % NAMEN.length],
    farbe: "",
    leute: Array.from({ length: Math.min(je, n - g * je) }, (_, i) => ({
      id: `a${g}-${i}`,
      slug: `agent-${g}-${i}`,
      display_name: `Agent ${g}.${i}`,
    })) as Agent[],
  })).filter((g) => g.leute.length > 0);
}

const machPlan = (n: number, abteilungen?: number, breite = 1148) =>
  bauplan(belegschaft(n, abteilungen), breite, { besprechung: "Besprechung", teekueche: "Teeküche" });

/** Liegt die Strecke zwischen zwei Punkten vollständig innerhalb eines Raums
 *  oder vollständig außerhalb aller Räume (Flur, Quergang)? Alles andere
 *  hieße: Sie schneidet eine Wand. */
function schneidetWand(plan: Plan, a: Punkt, b: Punkt): boolean {
  const schritte = Math.max(2, Math.ceil(Math.hypot(b.x - a.x, b.y - a.y) / 3));
  const gesehen = new Set<number | string>();
  for (let i = 0; i <= schritte; i++) {
    const p = { x: a.x + ((b.x - a.x) * i) / schritte, y: a.y + ((b.y - a.y) * i) / schritte };
    gesehen.add(raumAn(plan, p) ?? "flur");
  }
  /* Eine Strecke darf durch genau einen Bereich laufen — oder beim Durchgang
     durch eine Tür durch zwei, weil die Schwelle selbst dazwischenliegt. Drei
     Bereiche in einer Geraden gibt es nur, wenn eine Wand dazwischen war. */
  return gesehen.size > 2;
}

describe("Bauplan", () => {
  it("baut aus acht Kollegen ein Haus mit einem Flur", () => {
    const p = machPlan(8, 2);
    expect(p.flure).toHaveLength(1);
    expect(p.raeume.filter((r) => r.gem)).toHaveLength(2);
    expect(p.raeume.reduce((s, r) => s + r.leute.length, 0)).toBe(8);
  });

  it("wächst mit der Belegschaft, statt zu platzen", () => {
    const klein = machPlan(8, 2);
    const gross = machPlan(140, 6);
    expect(gross.hoehe).toBeGreaterThan(klein.hoehe * 2);
    expect(gross.flure.length).toBeGreaterThan(klein.flure.length);
    /* Die Breite gibt das Fenster vor — in die Höhe darf der Bau wachsen, in
       die Breite nicht. */
    expect(gross.breite).toBe(klein.breite);
  });

  it("hält die Zimmer annähernd quadratisch", () => {
    /* Zwölf Plätze in einer Reihe sind ein Schlauch. */
    expect(spaltenFuer(1)).toBe(1);
    expect(spaltenFuer(4)).toBe(2);
    expect(spaltenFuer(12)).toBe(4);
    expect(spaltenFuer(40)).toBe(5);
  });

  it("gibt jedem Zimmer eine Tür am eigenen Flur", () => {
    const p = machPlan(60, 5);
    for (const r of p.raeume) {
      expect(r.flurY).toBe(p.flure[r.flur].mitte);
      /* Der Punkt innen liegt im Zimmer, der Punkt außen nicht. */
      expect(raumAn(p, r.innen)).toBe(p.raeume.indexOf(r));
      expect(raumAn(p, r.aussen)).toBeNull();
    }
  });

  it("setzt alle Plätze innerhalb ihres Zimmers", () => {
    const p = machPlan(60, 5);
    for (const r of p.raeume)
      for (const s of r.sitze) {
        expect(s.x).toBeGreaterThan(r.x);
        expect(s.x).toBeLessThan(r.x + r.w);
        expect(s.y).toBeGreaterThan(r.y);
        expect(s.y).toBeLessThan(r.y + r.h);
      }
  });

  it("hält den Bau innerhalb der gemessenen Breite", () => {
    for (const n of [4, 25, 60, 140]) {
      const p = machPlan(n, Math.max(1, Math.round(n / 8)));
      expect(p.breite).toBeLessThanOrEqual(1148);
      for (const r of p.raeume) expect(r.x + r.w).toBeLessThanOrEqual(p.breite - AUSSEN + 0.5);
    }
  });
});

describe("Wege", () => {
  it("führt aus dem Zimmer erst an die Tür, dann hinaus", () => {
    const p = machPlan(30, 4);
    const von = p.raeume.findIndex((r) => r.leute.length);
    const a = p.raeume[von];
    const pfad = weg(p, a.sitze[0], { x: p.quer.mitte, y: p.tresen.plaetze[0].y }, null);
    expect(pfad[0]).toEqual(a.innen);
    expect(pfad[1]).toEqual(a.aussen);
  });

  it("führt aus der Teeküche nach Hause durch beide Türen", () => {
    const p = machPlan(30, 4);
    const kueche = p.raeume.findIndex((r) => r.gem === "teekueche");
    const heim = p.raeume.findIndex((r) => r.leute.length);
    const stehtIn = p.raeume[kueche].treffpunkte[0];
    const pfad = weg(p, stehtIn, p.raeume[heim].sitze[0], heim);
    /* Nicht auf der Geraden nach Hause: zuerst durch die Küchentür. */
    expect(pfad[0]).toEqual(p.raeume[kueche].innen);
    expect(pfad[1]).toEqual(p.raeume[kueche].aussen);
    expect(pfad[pfad.length - 2]).toEqual(p.raeume[heim].innen);
  });

  it("schneidet auf keinem Abschnitt eine Wand", () => {
    const p = machPlan(70, 6);
    const zimmer = p.raeume.map((_, i) => i);
    for (const von of zimmer) {
      const start = p.raeume[von].sitze[0] ?? p.raeume[von].treffpunkte[0];
      for (const nach of [...zimmer, null]) {
        const ziel =
          nach == null
            ? p.tresen.plaetze[0]
            : (p.raeume[nach].sitze[0] ?? p.raeume[nach].treffpunkte[0]);
        const pfad = [start, ...weg(p, start, ziel, nach)];
        for (let i = 1; i < pfad.length; i++)
          expect(
            schneidetWand(p, pfad[i - 1], pfad[i]),
            `Abschnitt ${i} von Raum ${von} nach ${nach} schneidet eine Wand`,
          ).toBe(false);
      }
    }
  });

  it("nimmt den Quergang, wenn zwei Zimmer an verschiedenen Fluren liegen", () => {
    const p = machPlan(90, 7);
    expect(p.flure.length).toBeGreaterThan(1);
    const a = p.raeume.findIndex((r) => r.flur === 0 && r.leute.length);
    const b = p.raeume.findIndex((r) => r.flur === 1 && r.leute.length);
    const pfad = weg(p, p.raeume[a].sitze[0], p.raeume[b].sitze[0], b);
    expect(pfad.some((q) => Math.abs(q.x - p.quer.mitte) < 0.5)).toBe(true);
  });
});
