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

/* Alle Kataloge tragen dieselben Schlüssel.

   Das ist die Regel aus CLAUDE.md („neue UI-Texte immer in beiden Dateien
   pflegen") — und seit die Kataloge einzeln nachgeladen werden (i18n.ts),
   hängt mehr daran als Ordnung: Vorher fing die Ersatzsprache einen
   vergessenen deutschen Schlüssel mit dem englischen Text auf, weil beide
   Kataloge im Bündel lagen. Jetzt liegt nur einer davon im Browser, und was
   fehlt, steht als Schlüssel auf dem Schirm.

   Aus zwei Katalogen sind zehn geworden. Englisch ist das Maß: Es ist die
   Basissprache (i18n.ts), und ein Schlüssel, den nur eine Übersetzung kennt,
   ist einer, den niemand liest. */
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

  /* Ein Platzhalter, der bei der Übersetzung verloren geht, ist ein Satz mit
     einem Loch: „{{count}} Einträge" wird zu „Einträge". Der Test fängt die
     Richtung, die weh tut — ein zusätzlicher Platzhalter wäre nur leer, ein
     fehlender verschluckt die Zahl. */
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
