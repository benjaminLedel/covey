import { beforeEach, describe, expect, it } from "vitest";
import i18n from "../i18n";
import { anteil, dauer, phaseZahlen } from "./PhaseBadge";
import type { AgentPhase } from "../api";

/* The phase readout answers a question the status has no answer to: `triggered`
   sits on a fresh host for three quarters of an hour and in that time says
   nothing about the image that is currently being pulled. */

// The separators follow the language; a test that does not pin them checks the
// environment instead of the readout.
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

  // A sync only knows afterwards how much it was. A bar that asserts a number
  // nobody has is worse than no bar.
  it("bleibt ohne Gesamtgröße ohne Länge", () => {
    expect(anteil(ph({ phase: "home_sync", count: 400, bytes: 12_000 }))).toBeUndefined();
  });

  // Docker reports the total size only gradually — as long as not every layer
  // has started, the sum can exceed the supposed total. 130 % would be a
  // readout that nobody believes any more.
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

  // fmtBytes computes in powers of two — the same unit as everywhere else in
  // the UI. Docker writes decimal; that is a difference of seven percent and
  // is not quietly switched here to make a readout work.
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
