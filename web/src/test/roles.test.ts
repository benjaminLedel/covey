import { describe, it, expect } from "vitest";
import { ROLES } from "../api";

/* Eine Rolle, die es nicht gibt, ist in einer Bedingung nicht falsch — sie ist
   still. `me.Role === "platform_admin"` war jahrelang eine korrekte Zeile;
   seit Migration 0061 heißt die oberste Org-Rolle org_admin, und die Zeile
   sagte ab da immer nein. Sichtbar wurde das an einer Karte, die verschwand:
   die Registrierung eines Runners war für alle weg, auch für die, die den
   Endpunkt dahinter benutzen dürfen.

   Ein Compiler fängt das nicht — es ist ein Zeichenkettenvergleich. Also holt
   dieser Test die Zeichenketten aus dem Quelltext und hält sie gegen die eine
   Liste, die es gibt. */

const quellen = import.meta.glob("../**/*.{ts,tsx}", {
  query: "?raw",
  import: "default",
  eager: true,
}) as Record<string, string>;

/* Die Formen, in denen die Oberfläche eine Plattform-Rolle prüft: `me.Role`,
   ein anderes `.Role`, und die `canEdit(role)`-Helfer, die es in fast jeder
   Seite gibt.

   Bewusst NICHT jedes `role ===`: Ein Chat-Verlauf hat auch Rollen ("user",
   "assistant"), und ein Test, der die anhält, wird nach dem zweiten Fehlalarm
   abgeschaltet. Deshalb nur der Vergleich gegen einen Wert, der wie eine
   Plattform-Rolle aussieht — mit Unterstrich oder aus der Liste. */
const ROLLENVERGLEICH =
  /(?:me\.Role|\.Role|\brole)\s*[!=]==?\s*"([a-z]+_[a-z_]+|security|auditor|controlling)"/g;

describe("Rollennamen in der Oberfläche", () => {
  it("vergleicht nur gegen Rollen, die es gibt", () => {
    const erlaubt = new Set<string>(ROLES);
    const funde: string[] = [];

    for (const [datei, text] of Object.entries(quellen)) {
      if (datei.includes("/test/")) continue;
      for (const treffer of text.matchAll(ROLLENVERGLEICH)) {
        const rolle = treffer[1];
        if (!erlaubt.has(rolle)) funde.push(`${datei}: "${rolle}"`);
      }
    }

    expect(funde, "Rollen, die die Plattform nicht kennt (siehe ROLES in api.ts)").toEqual([]);
  });

  it("findet überhaupt Vergleiche — sonst prüft der Test nichts", () => {
    const anzahl = Object.values(quellen).reduce(
      (n, text) => n + [...text.matchAll(ROLLENVERGLEICH)].length,
      0,
    );
    expect(anzahl).toBeGreaterThan(5);
  });
});
