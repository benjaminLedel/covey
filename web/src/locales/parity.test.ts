import { describe, it, expect } from "vitest";
import de from "./de.json";
import en from "./en.json";
import es from "./es.json";
import fr from "./fr.json";
import italienisch from "./it.json";
import nl from "./nl.json";
import pl from "./pl.json";
import pt from "./pt.json";
import ja from "./ja.json";
import zh from "./zh.json";

/* All catalogues carry the same keys.

   This is the rule from CLAUDE.md ("always maintain new UI texts in both
   files") — and since the catalogues are loaded one by one (i18n.ts),
   more rides on it than tidiness: before, the fallback language caught a
   forgotten German key with the English text, because both
   catalogues sat in the bundle. Now only one sits in the browser, and what
   is missing shows on screen as the key itself.

   Two catalogues became ten. English is the measure: it is the
   base language (i18n.ts), and a key that only one translation knows is
   one that nobody reads. */
function schluessel(obj: unknown, praefix = ""): string[] {
  if (typeof obj !== "object" || obj === null) return [praefix];
  return Object.entries(obj).flatMap(([k, v]) =>
    schluessel(v, praefix ? `${praefix}.${k}` : k),
  );
}

const kataloge: [string, unknown][] = [
  ["de", de],
  ["es", es],
  ["fr", fr],
  ["it", italienisch],
  ["nl", nl],
  ["pl", pl],
  ["pt", pt],
  ["ja", ja],
  ["zh", zh],
];

describe("Die Sprachkataloge", () => {
  const inEn = new Set(schluessel(en));

  it.each(kataloge)("tragen in %s dieselben Schlüssel wie Englisch", (_sprache, katalog) => {
    const drin = new Set(schluessel(katalog));
    expect([...drin].filter((k) => !inEn.has(k)).sort()).toEqual([]);
    expect([...inEn].filter((k) => !drin.has(k)).sort()).toEqual([]);
  });

  /* A placeholder lost in translation is a sentence with
     a hole: `{{count}} Einträge` becomes `Einträge`. The test catches the
     direction that hurts — an extra placeholder would only be empty, a
     missing one swallows the number. */
  const platzhalter = (s: unknown) => new Set(String(s).match(/\{\{\w+\}\}/g) ?? []);
  const enWerte = new Map(
    schluessel(en).map((k) => [k, k.split(".").reduce<any>((o, t) => o?.[t], en)]),
  );

  it.each(kataloge)("behalten in %s die Platzhalter", (_sprache, katalog) => {
    const fehlend: string[] = [];
    for (const [pfad, wert] of enWerte) {
      const uebersetzt = pfad.split(".").reduce<any>((o, t) => o?.[t], katalog);
      for (const p of platzhalter(wert)) {
        if (!platzhalter(uebersetzt).has(p)) fehlend.push(`${pfad}: ${p}`);
      }
    }
    expect(fehlend).toEqual([]);
  });
});
