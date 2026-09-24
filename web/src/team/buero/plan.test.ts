import { describe, expect, it } from "vitest";
import type { Agent } from "../../api";
import {
  AUSSEN,
  INNEN,
  SCHILD_H,
  TUER_B,
  bauplan,
  belegung,
  loungeZonen,
  podBreite,
  raumAn,
  sitzMuster,
  spaltenFuer,
  suche,
  weg,
  zimmerBreite,
  zimmerHoehe,
  type Gruppe,
  type Plan,
  type Punkt,
  type Raum,
  type Sitz,
} from "./plan";
import { wackel } from "./mathe";

/* The floor plan can be checked without a browser, and that is the reason it
 * is a file of its own. This is the geometry harness of the prototype,
 * ported: it holds the invariants that make the building a building.
 *
 *   1. The plan scales. Eight colleagues and a hundred and forty give the
 *      same house in two sizes — not an empty template and a burst one.
 *   2. Nobody walks through a wall. That was the prototype's bug: the way
 *      was computed from the HOME room, and whoever stood in the kitchen
 *      walked home on the straight line.
 */

const ABTEILUNGEN: [string, string][] = [
  ["Customer Support", "#6d8c5a"],
  ["Software-Entwicklung", "#8a5f7a"],
  ["Betrieb & Infrastruktur", "#4d7c74"],
  ["Finanzen", "#6e6294"],
  ["Engineering", "#5b7196"],
  ["Operations", "#8a6a3a"],
  ["Vertrieb", "#7a6b4f"],
  ["Personal & Kultur", "#5f7d8a"],
];

const agent = (g: number, i: number) =>
  ({ id: `a${g}-${i}`, slug: `agent-${g}-${i}`, display_name: `Agent ${g}.${i}` }) as Agent;

/** The prototype's workforce: about one department per six and a half
 *  people, of uneven sizes. The last one is "without a department". */
function belegschaft(n: number): Gruppe[] {
  const anzahl = Math.max(1, Math.min(ABTEILUNGEN.length, Math.round(n / 6.5)));
  const gew = Array.from({ length: anzahl }, (_, i) => 1 + ((i * 7 + 3) % 5) / 4);
  const su = gew.reduce((a, b) => a + b, 0);
  let rest = n;
  return ABTEILUNGEN.slice(0, anzahl).map(([name, farbe], i) => {
    const k = i === anzahl - 1 ? rest : Math.max(2, Math.round((n * gew[i]) / su));
    rest -= k;
    const ohne = anzahl > 1 && i === anzahl - 1;
    return {
      id: ohne ? "ohne" : `d${i}`,
      name: ohne ? "Ohne Abteilung" : name,
      farbe: ohne ? "" : farbe,
      leute: Array.from({ length: Math.max(1, k) }, (_, j) => agent(i, j)),
    };
  });
}

const NAMEN = { besprechung: "Besprechung", kueche: "Teeküche", lounge: "Lounge" };
const BREITE = 3200;
const machPlan = (n: number, breite = BREITE, dichte = 1) => bauplan(belegschaft(n), breite, NAMEN, dichte);

/** An independent footprint: desk ±75, monitor −50, chair +75, rotated. */
function fuss(p: Sitz) {
  const c = Math.cos(p.dreh),
    s = Math.sin(p.dreh);
  let x0 = 1e9,
    y0 = 1e9,
    x1 = -1e9,
    y1 = -1e9;
  for (const [a, b] of [
    [-75, -50],
    [75, -50],
    [75, 75],
    [-75, 75],
  ]) {
    const X = p.x + a * c - b * s,
      Y = p.y + a * s + b * c;
    x0 = Math.min(x0, X);
    x1 = Math.max(x1, X);
    y0 = Math.min(y0, Y);
    y1 = Math.max(y1, Y);
  }
  return { x0, y0, x1, y1 };
}

const tuerZone = (x: number, y: number, w: number, h: number, oben: boolean, tuerX: number) => ({
  x0: tuerX - TUER_B / 2 - 10,
  x1: tuerX + TUER_B / 2 + 10,
  y0: oben ? y + h - 80 : y - 10,
  y1: oben ? y + h + 10 : y + 80,
});

/** Seats inside with margin, not in the door zone, at least 120 apart. */
function sitzeFehler(
  s: Sitz[],
  x: number,
  y: number,
  w: number,
  h: number,
  oben: boolean,
  tuerX: number,
  was: string,
): string[] {
  const f: string[] = [];
  const tz = tuerZone(x, y, w, h, oben, tuerX);
  s.forEach((p, i) => {
    const k = fuss(p);
    if (!(k.x0 >= x + 20 && k.x1 <= x + w - 20 && k.y0 >= y + SCHILD_H + 20 && k.y1 <= y + h - 20))
      f.push(`${was} seat ${i} outside the room`);
    if (k.x0 < tz.x1 && tz.x0 < k.x1 && k.y0 < tz.y1 && tz.y0 < k.y1) f.push(`${was} seat ${i} in the door`);
    for (let j = i + 1; j < s.length; j++)
      if (Math.hypot(p.x - s[j].x, p.y - s[j].y) < 120) f.push(`${was} seats ${i}/${j} too close`);
  });
  return f;
}

const GROESSEN = [1, 3, 5, 8, 12, 25, 40, 60, 100, 140];

describe("floor plan", () => {
  it.each(GROESSEN)("n=%i: every seat stands in its room, clear of the door and its neighbours", (n) => {
    const plan = machPlan(n);
    const fehler: string[] = [];
    expect(plan.raeume.reduce((s, r) => s + r.leute.length, 0)).toBe(n);
    for (const r of plan.raeume) {
      expect(r.sitze).toHaveLength(r.leute.length);
      fehler.push(...sitzeFehler(r.sitze, r.x, r.y, r.w, r.h, r.oben, r.tuerX, `${r.name}`));
    }
    expect(fehler).toEqual([]);
  });

  it.each(GROESSEN)("n=%i: rooms stay inside the width and never overlap", (n) => {
    const plan = machPlan(n);
    expect(plan.breite).toBeLessThanOrEqual(BREITE);
    for (const r of plan.raeume) expect(r.x + r.w).toBeLessThanOrEqual(plan.breite - AUSSEN + 0.01);
    for (let i = 0; i < plan.raeume.length; i++)
      for (let j = i + 1; j < plan.raeume.length; j++) {
        const a = plan.raeume[i],
          b = plan.raeume[j];
        const ueber = a.x < b.x + b.w && b.x < a.x + a.w && a.y < b.y + b.h && b.y < a.y + a.h;
        expect(ueber, `${a.name} / ${b.name}`).toBe(false);
      }
  });

  it.each(GROESSEN)("n=%i: a partition never cuts a desk or a way from the door", (n) => {
    const plan = machPlan(n);
    for (const r of plan.raeume) {
      const t = r.trennwand;
      if (!t) continue;
      expect(t.x > r.x && t.x < r.x + r.w && t.y0 >= r.y && t.y1 <= r.y + r.h && t.y1 > t.y0).toBe(true);
      for (const p of r.sitze) {
        const k = fuss(p);
        expect(k.x0 < t.x + 5 && t.x - 5 < k.x1 && k.y0 < t.y1 && t.y0 < k.y1).toBe(false);
      }
      const iy = r.oben ? r.y + r.h - 26 : r.y + 26;
      for (const p of r.sitze)
        if ((p.x - t.x) * (r.tuerX - t.x) < 0) {
          const yc = iy + ((t.x - r.tuerX) / (p.x - r.tuerX)) * (p.y - iy);
          expect(yc >= t.y0 && yc <= t.y1).toBe(false);
        }
    }
  });

  it("grows with the workforce instead of bursting", () => {
    const klein = bauplan(belegschaft(8), 1400, NAMEN);
    const gross = bauplan(belegschaft(140), 1400, NAMEN);
    expect(gross.hoehe).toBeGreaterThan(klein.hoehe * 2);
    expect(gross.flure.length).toBeGreaterThan(klein.flure.length);
    /* The window gives the width — the building may grow in height, not in
       width. */
    expect(gross.breite).toBe(klein.breite);
  });

  it("keeps rooms roughly square", () => {
    expect(spaltenFuer(1)).toBe(1);
    expect(spaltenFuer(4)).toBe(2);
    expect(spaltenFuer(12)).toBe(4);
    expect(spaltenFuer(40)).toBe(5);
  });

  it("names the common rooms from the catalogue it is given", () => {
    const p = bauplan(belegschaft(40), BREITE, { besprechung: "Meeting", kueche: "Kitchen", lounge: "Lounge X" });
    expect(p.raeume.find((r) => r.gem === "besprechung")?.name).toBe("Meeting");
    expect(p.raeume.find((r) => r.gem === "kueche")?.name).toBe("Kitchen");
    for (const r of p.raeume.filter((q) => q.gem === "lounge")) expect(r.name).toBe("Lounge X");
  });

  it("treats the room without a department like any other", () => {
    const p = machPlan(40);
    const ohne = p.raeume.find((r) => r.id === "ohne");
    expect(ohne).toBeDefined();
    expect(ohne?.farbe).toBe("");
    expect(ohne?.gem).toBeUndefined();
    expect(ohne?.sitze).toHaveLength(ohne?.leute.length ?? -1);
  });

  it("is deterministic", () => {
    expect(machPlan(60)).toEqual(machPlan(60));
  });

  it("gives every room a door on its own corridor", () => {
    const p = machPlan(60);
    for (const r of p.raeume) {
      expect(r.flurY).toBe(p.flure[r.flur].mitte);
      /* The point inside lies in the room, the point outside does not. */
      expect(raumAn(p, r.innen)).toBe(p.raeume.indexOf(r));
      expect(raumAn(p, r.aussen)).toBeNull();
    }
  });
});

describe("rows and lounges", () => {
  /* The prototype's second round: every head count from 5 to 140. Rows close
     flush, departments stay within a quarter of what their seats need, and
     the remainder becomes lounges — never two side by side. */
  const plaene: [number, Plan][] = [];
  for (let n = 5; n <= 140; n++) plaene.push([n, machPlan(n)]);

  it("closes every row flush with the right wall", () => {
    const fehler: string[] = [];
    for (const [n, plan] of plaene) {
      const innenRechts = plan.breite - AUSSEN;
      const reihen = new Map<number, Raum[]>();
      for (const r of plan.raeume) reihen.set(r.y, [...(reihen.get(r.y) ?? []), r]);
      for (const [y, reihe] of reihen) {
        reihe.sort((a, b) => a.x - b.x);
        const ende = reihe[reihe.length - 1];
        if (Math.abs(ende.x + ende.w - innenRechts) > 0.5) fehler.push(`n=${n} row y=${y} ends short`);
        for (let i = 1; i < reihe.length; i++) {
          if (Math.abs(reihe[i].x - (reihe[i - 1].x + reihe[i - 1].w + INNEN)) > 0.5)
            fehler.push(`n=${n} gap before ${reihe[i].name}`);
          if (reihe[i].gem === "lounge" && reihe[i - 1].gem === "lounge") fehler.push(`n=${n} two lounges adjacent`);
        }
      }
    }
    expect(fehler).toEqual([]);
  });

  it("gives no department more than a quarter over its need", () => {
    const fehler: string[] = [];
    for (const [n, plan] of plaene)
      for (const r of plan.raeume)
        if (!r.gem && r.w > zimmerBreite(r.leute.length) * 1.25 + 0.5) fehler.push(`n=${n} ${r.name} ${r.w}`);
    expect(fehler).toEqual([]);
  });

  it("builds lounges, not halls, and keeps their islands and meeting points reachable", () => {
    const fehler: string[] = [];
    let lounges = 0;
    for (const [n, plan] of plaene)
      for (const r of plan.raeume) {
        if (r.gem !== "lounge") continue;
        lounges++;
        if (r.w < 360) fehler.push(`n=${n} lounge only ${r.w} wide`);
        if (r.w > 1800.5) fehler.push(`n=${n} lounge ${r.w} wide — a hall again`);
        if (!(r.tuerX > r.x && r.tuerX < r.x + r.w)) fehler.push(`n=${n} lounge without a door`);
        const zonen = loungeZonen(r);
        const tz = {
          x0: r.tuerX - 40,
          x1: r.tuerX + 40,
          y0: r.oben ? r.y + r.h - 90 : r.y - 10,
          y1: r.oben ? r.y + r.h + 10 : r.y + 90,
        };
        zonen.forEach((z, i) => {
          const k = { x0: z.x - z.b / 2, x1: z.x + z.b / 2, y0: z.y - z.t / 2, y1: z.y + z.t / 2 };
          if (!(k.x0 >= r.x + 20 && k.x1 <= r.x + r.w - 20 && k.y0 >= r.y + SCHILD_H + 20 && k.y1 <= r.y + r.h - 20))
            fehler.push(`n=${n} island ${i} sticks out`);
          if (k.x0 < tz.x1 && tz.x0 < k.x1 && k.y0 < tz.y1 && tz.y0 < k.y1) fehler.push(`n=${n} island ${i} blocks door`);
          for (let j = i + 1; j < zonen.length; j++)
            if (Math.abs(zonen[j].x - z.x) < (z.b + zonen[j].b) / 2) fehler.push(`n=${n} islands overlap`);
        });
        for (const t of r.treff) {
          if (!(t.x > r.x + 20 && t.x < r.x + r.w - 20 && t.y > r.y + SCHILD_H && t.y < r.y + r.h - 20))
            fehler.push(`n=${n} meeting point outside`);
          for (const z of zonen) {
            if (Math.abs(t.x - z.x) < z.b / 2 && Math.abs(t.y - z.y) < z.t / 2) fehler.push(`n=${n} meeting point on island`);
            for (let s = 0; s <= 20; s++) {
              const px = r.innen.x + ((t.x - r.innen.x) * s) / 20,
                py = r.innen.y + ((t.y - r.innen.y) * s) / 20;
              if (Math.abs(px - z.x) < z.b / 2 - 1 && Math.abs(py - z.y) < z.t / 2 - 1) {
                fehler.push(`n=${n} way to meeting point blocked by an island`);
                break;
              }
            }
          }
        }
      }
    expect(lounges).toBeGreaterThan(0);
    expect(fehler).toEqual([]);
  });

  it("reaches every meeting point from the door in a straight line", () => {
    for (const [, plan] of plaene)
      for (const [ri, r] of plan.raeume.entries())
        for (const t of r.treff)
          for (let s = 0; s <= 20; s++) {
            const p = { x: r.innen.x + ((t.x - r.innen.x) * s) / 20, y: r.innen.y + ((t.y - r.innen.y) * s) / 20 };
            expect(raumAn(plan, p)).toBe(ri);
          }
  });

  it("puts fewer lounge islands in a sparser house", () => {
    const voll = machPlan(40, BREITE, 1).raeume.filter((r) => r.gem === "lounge");
    const duenn = machPlan(40, BREITE, 0.4).raeume.filter((r) => r.gem === "lounge");
    expect(voll.length).toBeGreaterThan(0);
    const inseln = (rs: Raum[], d: number) => rs.reduce((s, r) => s + loungeZonen(r, d).length, 0);
    expect(inseln(duenn, 0.4)).toBeLessThan(inseln(voll, 1));
    expect(duenn[0].treff.length).toBe(loungeZonen(duenn[0], 0.4).length * 2);
  });
});

describe("seat patterns", () => {
  /* The prototype's stress run: the arrangements alone, many names and
     sizes, both sides of the corridor, tight and deep rooms. */
  it("keep every seat inside, out of the door and apart", () => {
    const fehler: string[] = [];
    for (let k = 0; k < 40; k++)
      for (const n of [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 12, 15, 18, 22, 26, 30])
        for (const oben of [true, false])
          for (const extraH of [0, 180]) {
            const name = "Abteilung " + k,
              sp = spaltenFuer(n);
            const w =
              Math.max(430, podBreite(sp) + 40, Math.round(zimmerBreite(n) * (1 + wackel(name + "b", 0.12)))) +
              (k % 3) * 60;
            const h = zimmerHoehe(n) + extraH,
              x = 1000,
              y = 500,
              tuerX = x + w / 2;
            const s = sitzMuster(name, x, y, w, sp, n, { h, oben, tuerX });
            if (s.length !== n) fehler.push(`${name} n=${n}: ${s.length} seats`);
            fehler.push(...sitzeFehler(s, x, y, w, h, oben, tuerX, `${name} n=${n} oben=${oben} h+${extraH}`));
          }
    expect(fehler).toEqual([]);
  });
});

describe("ways", () => {
  /** A point on each room where someone would stand: a seat, else a meeting
   *  point. */
  const stand = (r: Raum): Punkt => r.sitze[0] ?? r.treff[0] ?? { x: r.x + r.w / 2, y: r.y + r.h / 2 };

  /** Does the segment pass through a room other than the ones allowed, or
   *  through more than two areas? Two is a door (room and corridor on either
   *  side of the threshold); three means a wall was in between. */
  function segmentFehler(plan: Plan, a: Punkt, b: Punkt, erlaubt: Set<number | null>): string | null {
    const schritte = Math.max(2, Math.ceil(Math.hypot(b.x - a.x, b.y - a.y) / 3));
    const gesehen = new Set<number | null>();
    for (let i = 0; i <= schritte; i++) {
      const p = { x: a.x + ((b.x - a.x) * i) / schritte, y: a.y + ((b.y - a.y) * i) / schritte };
      const ri = raumAn(plan, p);
      if (!erlaubt.has(ri)) return `crosses room ${ri}`;
      gesehen.add(ri);
    }
    return gesehen.size > 2 ? "crosses a wall" : null;
  }

  it.each(GROESSEN)("n=%i: leaves by the door and crosses no room it does not enter", (n) => {
    const plan = machPlan(n);
    const fehler: string[] = [];
    const zimmer = plan.raeume.map((_, i) => i);
    for (const von of zimmer) {
      const start = stand(plan.raeume[von]);
      for (const nach of [...zimmer, null]) {
        const ziel = nach == null ? plan.tresen.plaetze[0] : stand(plan.raeume[nach]);
        const pfad = [start, ...weg(plan, start, ziel, nach)];
        if (nach !== von) {
          if (pfad[1].x !== plan.raeume[von].innen.x || pfad[1].y !== plan.raeume[von].innen.y)
            fehler.push(`${von}→${nach}: does not leave by the door`);
          if (nach != null) {
            const r = plan.raeume[nach];
            expect(pfad[pfad.length - 2]).toEqual(r.innen);
          }
        }
        const erlaubt = new Set<number | null>([von, nach, null]);
        for (let i = 1; i < pfad.length; i++) {
          const f = segmentFehler(plan, pfad[i - 1], pfad[i], erlaubt);
          if (f) fehler.push(`${von}→${nach} leg ${i}: ${f}`);
        }
        expect(pfad[pfad.length - 1]).toEqual(ziel);
      }
    }
    expect(fehler).toEqual([]);
  });

  it("goes from the kitchen home through both doors, not through the wall", () => {
    const p = machPlan(30);
    const kueche = p.raeume.findIndex((r) => r.gem === "kueche");
    const heim = p.raeume.findIndex((r) => r.leute.length);
    const stehtIn = p.raeume[kueche].treff[0];
    const pfad = weg(p, stehtIn, p.raeume[heim].sitze[0], heim);
    /* Not on the straight line home: first through the kitchen door. */
    expect(pfad[0]).toEqual(p.raeume[kueche].innen);
    expect(pfad[1]).toEqual(p.raeume[kueche].aussen);
    expect(pfad[pfad.length - 2]).toEqual(p.raeume[heim].innen);
  });

  it("takes the cross corridor when two rooms lie on different corridors", () => {
    const p = bauplan(belegschaft(90), 1400, NAMEN);
    expect(p.flure.length).toBeGreaterThan(1);
    const a = p.raeume.findIndex((r) => r.flur === 0 && r.leute.length);
    const b = p.raeume.findIndex((r) => r.flur === 1 && r.leute.length);
    const pfad = weg(p, p.raeume[a].sitze[0], p.raeume[b].sitze[0], b);
    expect(pfad.some((q) => Math.abs(q.x - p.quer.mitte) < 0.5)).toBe(true);
  });

  it("hands the in-room legs to the pathfinding when given", () => {
    const p = machPlan(30);
    const a = p.raeume.findIndex((r) => r.leute.length);
    const b = p.raeume.findIndex((r, i) => i !== a && r.leute.length);
    const aufrufe: [number, Punkt, Punkt][] = [];
    const umweg = (ri: number, von: Punkt, ziel: Punkt) => {
      aufrufe.push([ri, von, ziel]);
      return [{ x: -1, y: -1 }, { ...ziel }];
    };
    const pfad = weg(p, p.raeume[a].sitze[0], p.raeume[b].sitze[0], b, umweg);
    expect(aufrufe).toEqual([
      [a, p.raeume[a].sitze[0], p.raeume[a].innen],
      [b, p.raeume[b].innen, p.raeume[b].sitze[0]],
    ]);
    expect(pfad.filter((q) => q.x === -1)).toHaveLength(2);
    /* Within one room only the room's own leg. */
    aufrufe.length = 0;
    expect(weg(p, p.raeume[a].sitze[0], p.raeume[a].sitze[1] ?? p.raeume[a].innen, a, umweg)).toHaveLength(2);
    expect(aufrufe).toHaveLength(1);
  });
});

describe("occupancy", () => {
  it("finds free spots that do not overlap, and none once the room is full", () => {
    const p = machPlan(12);
    const r = p.raeume.find((q) => q.gem === "besprechung");
    expect(r).toBeDefined();
    if (!r) return;
    const b = belegung(r);
    const gefunden: [number, number][] = [];
    for (const zone of ["wand", "ecke", "frei", "frei", "frei"] as const) {
      const f = suche(b, 40, 40, zone);
      if (f) gefunden.push(f);
    }
    expect(gefunden.length).toBeGreaterThan(2);
    for (let i = 0; i < gefunden.length; i++) {
      const [x, y] = gefunden[i];
      expect(x).toBeGreaterThanOrEqual(r.x + 6);
      expect(y + 40).toBeLessThanOrEqual(r.y + r.h - 6);
      for (let j = i + 1; j < gefunden.length; j++)
        expect(Math.abs(x - gefunden[j][0]) >= 40 || Math.abs(y - gefunden[j][1]) >= 40).toBe(true);
    }
    b.sperre(r.x, r.y, r.w, r.h);
    expect(b.frei(r.x + 20, r.y + 20, 10, 10)).toBe(false);
    expect(suche(b, 40, 40, "frei")).toBeNull();
    expect(suche(belegung(r), r.w, r.h, "frei")).toBeNull();
  });
});
