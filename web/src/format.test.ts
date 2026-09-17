import { beforeEach, describe, expect, it } from "vitest";
import i18n from "./i18n";
import { exact, fmtBytes, fmtCount, fmtDelta, fmtUSD } from "./format";

/* The reason stood on the cost side: 147952885 read cache tokens. Nobody reads
   that as a hundred forty-eight million — you count it through digit by digit.
   Next to it the same page showed `3.05 M`, because the cost view had its own
   formatter, and the total stood there as `2746 $`. Three spellings for the
   same kind of number. */

// The separators follow the language of the UI (see format.ts), so every test
// has to say which one it runs in. Before, the result hung on what the
// environment had last been set to.
beforeEach(async () => {
  await i18n.changeLanguage("de");
});

describe("fmtCount", () => {
  it("zeigt kleine Anzahlen ganz, mit Tausendertrennung", () => {
    expect(fmtCount(0)).toBe("0");
    expect(fmtCount(842)).toBe("842");
    expect(fmtCount(9999)).toBe("9.999");
  });

  it("kürzt ab, wo das Zählen anfängt", () => {
    expect(fmtCount(10_000)).toBe("10 k");
    expect(fmtCount(12_345)).toBe("12,3 k");
    expect(fmtCount(999_999)).toBe("1000 k");
    expect(fmtCount(1_500_000)).toBe("1,5 M");
    expect(fmtCount(147_952_885)).toBe("148 M");
  });

  // Above a million M stops helping: `2500 M` you count through digit by
  // digit again. The measured reason stood above an agent on covey.work —
  // `2.499.833.356` input tokens.
  it("hat eine Stufe für Milliarden, und die kennt die Sprache", async () => {
    const i18n = (await import("./i18n")).default;
    await i18n.changeLanguage("de");
    expect(fmtCount(2_499_833_356)).toBe("2,5 Mrd");
    expect(fmtCount(999_999_999)).toBe("1000 M");
    await i18n.changeLanguage("en");
    // `2,5 B` would read as bytes in German, `2.5 Mrd` as nothing in English —
    // the one character of this file that has to know the language.
    expect(fmtCount(2_499_833_356)).toBe("2.5 B");
    await i18n.changeLanguage("de");
  });

  // One decimal digit says something at 12,3 k and nothing at 148,0 M.
  it("hängt keine Null an, die nichts trägt", () => {
    expect(fmtCount(148_000_000)).toBe("148 M");
    expect(fmtCount(20_000)).toBe("20 k");
  });

  // The short number is for skimming; whoever checks a bill needs the digits —
  // and finds them in the tooltip next to it.
  it("hat eine Langfassung für den Tooltip", () => {
    expect(exact(147_952_885)).toBe("147.952.885");
    expect(exact(842)).toBe("842");
  });
});

describe("fmtUSD", () => {
  // A single run often costs fractions of a cent. Rounded to two digits a
  // whole page would read 0,00 $.
  // With a comma in German — once `12.30 $` stood here next to `2.746 $`, and
  // the same point meant two things in two lines.
  it("behält die Stellen, die ein Betrag noch trägt", () => {
    expect(fmtUSD(0.0042)).toBe("0,0042 $");
    expect(fmtUSD(12.3)).toBe("12,30 $");
  });

  it("trennt Tausender, wo sie anfangen zu helfen", () => {
    expect(fmtUSD(2746)).toBe("2.746 $");
    expect(fmtUSD(463.14)).toBe("463,14 $");
    expect(fmtUSD(1_234_567)).toBe("1.234.567 $");
  });
});

/* The other two formatters stood there already and stay as they are — held
   down here only so that consistency is checkable and not
   claimed. */
describe("die übrigen Formatierer", () => {
  it("fmtBytes bleibt bei der gröbsten Einheit, die noch beschreibt", () => {
    expect(fmtBytes(812)).toBe("812 B");
    // Below ten units with a decimal digit, above without — the same rule as
    // in fmtCount, only older.
    expect(fmtBytes(5_000)).toBe("4.9 kB");
    expect(fmtBytes(14_500)).toBe("14 kB");
  });

  it("fmtDelta ebenso", () => {
    expect(fmtDelta(42_000)).toBe("42 s");
    expect(fmtDelta(3 * 60_000)).toBe("3 min");
  });
});

/* The same numbers on the English UI. The reason stood in the head of an agent
   on covey.work: `2.847 $` — in German two thousand eight hundred, in English
   two dollars fifty-five. The same string, two numbers. */
describe("englische Schreibweise", () => {
  beforeEach(async () => {
    await i18n.changeLanguage("en");
  });

  it("dreht Tausender- und Dezimaltrenner um", () => {
    expect(fmtUSD(2746)).toBe("2,746 $");
    expect(fmtUSD(12.3)).toBe("12.30 $");
    expect(fmtUSD(0.0042)).toBe("0.0042 $");
    expect(exact(147_952_885)).toBe("147,952,885");
    expect(fmtCount(12_345)).toBe("12.3 k");
    expect(fmtCount(2_499_833_356)).toBe("2.5 B");
  });

  // The two hang together: `1.234,5` against `1,234.5` — whoever changes only
  // one produces a spelling that is right in neither language.
  it("mischt die beiden nie", () => {
    expect(fmtCount(9999)).toBe("9,999");
    expect(fmtUSD(1_234_567)).toBe("1,234,567 $");
  });
});
