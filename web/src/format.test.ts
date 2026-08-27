import { beforeEach, describe, expect, it } from "vitest";
import i18n from "./i18n";
import { exact, fmtBytes, fmtCount, fmtDelta, fmtUSD } from "./format";

/* Der Anlass stand auf der Kostenseite: 147952885 gelesene Cache-Tokens. Das
   liest niemand als hundertachtundvierzig Millionen — das zählt man ziffernweise
   nach. Daneben zeigte dieselbe Seite „3.05 M", weil die Kostenansicht ihren
   eigenen Formatierer hatte, und der Gesamtbetrag stand als „2746 $" da. Drei
   Schreibweisen für dieselbe Sorte Zahl. */

// Die Trenner folgen der Sprache der Oberfläche (siehe format.ts), also muss
// jeder Test sagen, in welcher er läuft. Vorher hing das Ergebnis daran, was
// die Umgebung zuletzt gesetzt hatte.
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

  // Über der Million hört M auf zu helfen: „2500 M" zählt man wieder
  // ziffernweise nach. Der gemessene Anlass stand über einem Agenten auf
  // covey.work — 2.499.833.356 Eingabe-Tokens.
  it("hat eine Stufe für Milliarden, und die kennt die Sprache", async () => {
    const i18n = (await import("./i18n")).default;
    await i18n.changeLanguage("de");
    expect(fmtCount(2_499_833_356)).toBe("2,5 Mrd");
    expect(fmtCount(999_999_999)).toBe("1000 M");
    await i18n.changeLanguage("en");
    // „2,5 B" läse sich im Deutschen als Byte, „2.5 Mrd" im Englischen als
    // nichts — das eine Zeichen dieser Datei, das die Sprache kennen muss.
    expect(fmtCount(2_499_833_356)).toBe("2.5 B");
    await i18n.changeLanguage("de");
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
  // Auf Deutsch mit Komma — hier stand einmal „12.30 $" neben „2.746 $", und
  // derselbe Punkt hieß in zwei Zeilen zweierlei.
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

/* Dieselben Zahlen auf der englischen Oberfläche. Der Anlass stand im Kopf
   eines Agenten auf covey.work: „2.847 $" — auf Deutsch zweitausendachthundert,
   auf Englisch zwei Dollar fünfundachtzig. Dieselbe Zeichenkette, zwei Zahlen. */
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

  // Die beiden hängen aneinander: „1.234,5" gegen „1,234.5" — wer nur einen
  // umstellt, erzeugt eine Schreibweise, die in keiner Sprache richtig ist.
  it("mischt die beiden nie", () => {
    expect(fmtCount(9999)).toBe("9,999");
    expect(fmtUSD(1_234_567)).toBe("1,234,567 $");
  });
});
