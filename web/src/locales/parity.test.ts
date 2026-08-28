import { describe, it, expect } from "vitest";
import de from "./de.json";
import en from "./en.json";

/* Beide Kataloge tragen dieselben Schlüssel.
   
   Das ist die Regel aus CLAUDE.md („neue UI-Texte immer in beiden Dateien
   pflegen") — und seit die Kataloge einzeln nachgeladen werden (i18n.ts),
   hängt mehr daran als Ordnung: Vorher fing die Ersatzsprache einen
   vergessenen deutschen Schlüssel mit dem englischen Text auf, weil beide
   Kataloge im Bündel lagen. Jetzt liegt nur einer davon im Browser, und was
   fehlt, steht als Schlüssel auf dem Schirm. */
function schluessel(obj: unknown, praefix = ""): string[] {
  if (typeof obj !== "object" || obj === null) return [praefix];
  return Object.entries(obj).flatMap(([k, v]) =>
    schluessel(v, praefix ? `${praefix}.${k}` : k),
  );
}

describe("Die Sprachkataloge", () => {
  it("tragen dieselben Schlüssel", () => {
    const inDe = new Set(schluessel(de));
    const inEn = new Set(schluessel(en));
    expect([...inDe].filter((k) => !inEn.has(k)).sort()).toEqual([]);
    expect([...inEn].filter((k) => !inDe.has(k)).sort()).toEqual([]);
  });
});
