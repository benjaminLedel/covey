import { describe, expect, it } from "vitest";
import { exact, fmtBytes, fmtCount, fmtDelta, fmtUSD } from "./format";

/* Der Anlass stand auf der Kostenseite: 147952885 gelesene Cache-Tokens. Das
   liest niemand als hundertachtundvierzig Millionen — das zählt man ziffernweise
   nach. Daneben zeigte dieselbe Seite „3.05 M", weil die Kostenansicht ihren
   eigenen Formatierer hatte, und der Gesamtbetrag stand als „2746 $" da. Drei
   Schreibweisen für dieselbe Sorte Zahl. */

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

  // Eine Nachkommastelle sagt bei 12,3 k etwas und bei 148,0 M nichts.
  it("hängt keine Null an, die nichts trägt", () => {
    expect(fmtCount(148_000_000)).toBe("148 M");
    expect(fmtCount(20_000)).toBe("20 k");
  });

  // Die kurze Zahl ist zum Überfliegen; wer eine Rechnung prüft, braucht die
  // Ziffern — und findet sie im Tooltip daneben.
  it("hat eine Langfassung für den Tooltip", () => {
    expect(exact(147_952_885)).toBe("147.952.885");
    expect(exact(842)).toBe("842");
  });
});

describe("fmtUSD", () => {
  // Ein einzelner Lauf kostet oft Bruchteile eines Cents. Auf zwei Stellen
  // gerundet stünde auf einer ganzen Seite 0,00 $.
  it("behält die Stellen, die ein Betrag noch trägt", () => {
    expect(fmtUSD(0.0042)).toBe("0.0042 $");
    expect(fmtUSD(12.3)).toBe("12.30 $");
  });

  it("trennt Tausender, wo sie anfangen zu helfen", () => {
    expect(fmtUSD(2746)).toBe("2.746 $");
    expect(fmtUSD(463.14)).toBe("463.14 $");
    expect(fmtUSD(1_234_567)).toBe("1.234.567 $");
  });
});

/* Die beiden anderen Formatierer standen schon da und bleiben, wie sie sind —
   hier nur festgehalten, damit „einheitlich" nachprüfbar ist und nicht
   behauptet. */
describe("die übrigen Formatierer", () => {
  it("fmtBytes bleibt bei der gröbsten Einheit, die noch beschreibt", () => {
    expect(fmtBytes(812)).toBe("812 B");
    // Unter zehn Einheiten mit Nachkommastelle, darüber ohne — dieselbe Regel
    // wie bei fmtCount, nur älter.
    expect(fmtBytes(5_000)).toBe("4.9 kB");
    expect(fmtBytes(14_500)).toBe("14 kB");
  });

  it("fmtDelta ebenso", () => {
    expect(fmtDelta(42_000)).toBe("42 s");
    expect(fmtDelta(3 * 60_000)).toBe("3 min");
  });
});
