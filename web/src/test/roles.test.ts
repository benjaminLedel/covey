import { describe, it, expect } from "vitest";
import { ROLES } from "../api";

/* A role that does not exist is not wrong in a condition — it is silent.
   `me.Role === "platform_admin"` was a correct line for years; since
   migration 0061 the top-level org role is called org_admin, and from then on
   the line always said no. What it showed up in was a card that disappeared:
   registering a runner was gone for everyone, including those who may use the
   endpoint behind it.

   A compiler does not catch this — it is a string comparison. So this test
   pulls the strings out of the source code and holds them against the one
   list that exists. */

const quellen = import.meta.glob("../**/*.{ts,tsx}", {
  query: "?raw",
  import: "default",
  eager: true,
}) as Record<string, string>;

/* The forms in which the UI checks a platform role: `me.Role`, another
   `.Role`, and the `canEdit(role)` helpers that exist in almost every page.

   Deliberately NOT every `role ===`: a chat transcript also has roles ("user",
   "assistant"), and a test that holds those gets switched off after the
   second false alarm. Hence only the comparison against a value
   that looks like a platform role — with an underscore
   or from the list. */
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
