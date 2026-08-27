import { beforeEach, describe, expect, it } from "vitest";
import i18n from "../i18n";
import { anteil, dauer, phaseZahlen } from "./PhaseBadge";
import type { AgentPhase } from "../api";

/* Die Phasenanzeige beantwortet eine Frage, auf die der Status keine Antwort
   hat: „triggered" steht auf einem frischen Host eine Dreiviertelstunde lang da
   und sagt in dieser Zeit nichts über das Image, das gerade geholt wird. */

// Die Trenner folgen der Sprache; ein Test, der sie nicht festlegt, prüft die
// Umgebung statt der Anzeige.
beforeEach(async () => {
  await i18n.changeLanguage("de");
});

const ph = (p: Partial<AgentPhase>): AgentPhase => ({
  phase: "image",
  since: "2026-08-27T10:00:00Z",
  updated: "2026-08-27T10:04:00Z",
  ...p,
});

describe("Anteil", () => {
  it("rechnet in Bytes, wenn die Phase ihre Größe kennt", () => {
    expect(anteil(ph({ bytes: 1_200_000_000, bytes_total: 2_400_000_000 }))).toBeCloseTo(0.5);
  });

  it("rechnet in Dateien, wo Dateien gezählt werden", () => {
    expect(anteil(ph({ phase: "home", count: 4_935, count_total: 9_870 }))).toBeCloseTo(0.5);
  });

  // Ein Sync weiß erst hinterher, wie viel es war. Ein Balken, der eine Zahl
  // behauptet, die niemand hat, ist schlimmer als keiner.
  it("bleibt ohne Gesamtgröße ohne Länge", () => {
    expect(anteil(ph({ phase: "home_sync", count: 400, bytes: 12_000 }))).toBeUndefined();
  });

  // Docker meldet die Gesamtgröße erst nach und nach — solange nicht jede
  // Schicht begonnen hat, kann die Summe die vermeintliche Gesamtgröße
  // übersteigen. 130 % wäre eine Anzeige, die niemand mehr glaubt.
  it("geht nicht über voll hinaus", () => {
    expect(anteil(ph({ bytes: 13, bytes_total: 10 }))).toBe(1);
  });
});

describe("Dauer", () => {
  it("zählt ab dem Beginn der Phase, nicht ab dem letzten Lebenszeichen", () => {
    const jetzt = Date.parse("2026-08-27T10:05:00Z");
    expect(dauer(ph({}), jetzt)).toBe(5 * 60_000);
  });

  it("verträgt einen Zeitstempel, den es nicht lesen kann", () => {
    expect(dauer(ph({ since: "" }), Date.now())).toBe(0);
  });
});

describe("Zahlen", () => {
  const t = (k: string, o?: Record<string, unknown>) =>
    k === "activity.phase.filesOf" ? `${o?.count} von ${o?.total} Dateien` : `${o?.count} Dateien`;

  // fmtBytes rechnet in Zweierpotenzen — dieselbe Einheit wie überall sonst in
  // der Oberfläche. Docker schreibt dezimal; das ist ein Unterschied von sieben
  // Prozent und wird hier nicht heimlich für eine Anzeige umgestellt.
  it("nennt beide Zahlen, wo es beide gibt", () => {
    expect(phaseZahlen(ph({ bytes: 1_200_000_000, bytes_total: 2_400_000_000 }), t)).toBe("1.1 GB / 2.2 GB");
  });

  it("nennt die eine, wo es nur eine gibt", () => {
    expect(phaseZahlen(ph({ phase: "home_sync", bytes: 14_500 }), t)).toBe("14 kB");
  });

  it("zählt Dateien, wo Dateien gezählt werden", () => {
    expect(phaseZahlen(ph({ phase: "home", count: 4_000, count_total: 9_870 }), t)).toBe("4.000 von 9.870 Dateien");
  });

  it("sagt nichts, wo es nichts zu sagen gibt", () => {
    expect(phaseZahlen(ph({}), t)).toBe("");
  });
});
