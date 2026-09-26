/* Writes test/fixtures/office_plan.json: what the web's plan computes for a
 * handful of workforces, so that test/office_plan_test.dart can hold the
 * Dart port to the same numbers (#398). The office is the same building in
 * both places or it is two products.
 *
 * Run from mobile/ after a change to web/src/team/buero/plan.ts or mathe.ts:
 *
 *   node --import ./tool/office/resolve.mjs tool/office/fixture.ts
 */
import { writeFileSync } from "node:fs";
import { hash, wackel } from "../../../web/src/team/buero/mathe";
import { hausBauen, sitzMuster } from "../../../web/src/team/buero/plan";
import type { Gruppe } from "../../../web/src/team/buero/typen";

const namen = { besprechung: "Meeting room", kueche: "Kitchen", lounge: "Lounge" };

/* Made-up departments; the names matter, because every arrangement and
   every scatter hangs on a hash of them. */
const abteilung = (name: string, n: number, farbe = ""): Gruppe => ({
  id: "d-" + name.toLowerCase().replace(/\W+/g, "-"),
  name,
  farbe,
  leute: Array.from({ length: n }, (_, i) => ({ id: `${name}-${i}`, slug: `${name.toLowerCase()}-${i}` }) as never),
});

const faelle: { name: string; gruppen: Gruppe[]; maxB: number; dichte: number }[] = [
  { name: "leer", gruppen: [], maxB: 3200, dichte: 1 },
  { name: "einer", gruppen: [abteilung("Support", 1, "#d95f4a")], maxB: 3200, dichte: 1 },
  {
    name: "klein",
    gruppen: [abteilung("Entwicklung", 4, "#1f8a82"), abteilung("Vertrieb", 3, "#d09a22"), abteilung("Ohne Abteilung", 2)],
    maxB: 3200,
    dichte: 1,
  },
  {
    name: "mittel",
    gruppen: [
      abteilung("Engineering", 12, "#3f8ccb"),
      abteilung("Kundendienst", 7, "#7a57b8"),
      abteilung("Qualität & Test", 5),
      abteilung("Marketing", 6, "#d95f4a"),
    ],
    maxB: 3200,
    dichte: 1.7,
  },
  {
    name: "gross",
    gruppen: [
      abteilung("Plattform", 20),
      abteilung("Daten", 18),
      abteilung("Büro", 9),
      abteilung("Einkauf", 3),
      abteilung("Recht", 50),
      abteilung("Sales", 11),
    ],
    maxB: 3200,
    dichte: 0.45,
  },
  { name: "schmal", gruppen: [abteilung("Research", 8), abteilung("Design", 4)], maxB: 1400, dichte: 1 },
];

const muster: unknown[] = [];
for (const name of ["Entwicklung", "Support", "QA", "Büro", "Vertrieb", "Plattform", "x"])
  for (let n = 1; n <= 24; n++)
    for (const oben of [true, false]) {
      const w = 430 + (hash(name + n) % 900);
      muster.push({ name, n, oben, w, sitze: sitzMuster(name, 100, 50, w, Math.min(5, n), n, { oben }) });
    }

const texte = ["", "a", "Entwicklung", "Qualität & Test", "日本語", "Büro" + "x3", "lounge|kueche|lo0"];
writeFileSync(
  new URL("../../test/fixtures/office_plan.json", import.meta.url),
  JSON.stringify(
    {
      hash: texte.map((t) => ({ t, h: hash(t), w: wackel(t, 20) })),
      muster,
      haeuser: faelle.map((f) => ({
        name: f.name,
        maxB: f.maxB,
        dichte: f.dichte,
        gruppen: f.gruppen.map((g) => ({ id: g.id, name: g.name, farbe: g.farbe, leute: g.leute.map((a) => a.id) })),
        haus: hausBauen(f.gruppen, f.maxB, namen, f.dichte).etagen.map((e) => ({
          nr: e.nr,
          gruppen: e.gruppen.map((g) => g.id),
          plan: { ...e.plan, raeume: e.plan.raeume.map((r) => ({ ...r, leute: r.leute.map((a) => a.id) })) },
        })),
      })),
    },
  ),
);
