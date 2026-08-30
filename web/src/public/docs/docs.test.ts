/* Die Doku liegt als Markdown unter docs/ und wird von web/docs.mjs eingelesen
   (#128). Was den Dateibaum betrifft — fehlende Originale, Seiten, die in
   sections.yml nicht vorkommen, tote Verweise —, prüft der Generator selbst
   und bricht ab; er ist die Stelle, die das Dateisystem liest. Hier steht, was
   am Ergebnis stimmen muss, damit eine Seite eine Adresse bekommt. */

import { describe, expect, it } from "vitest";
import { DOC_SECTIONS, DOC_PAGES } from "./content.generated";

describe("Doku-Bestand", () => {
  it("gibt jeder Seite Frontmatter, das ihre Adresse trägt", () => {
    for (const s of DOC_SECTIONS) {
      for (const p of s.pages) {
        for (const l of ["de", "en"] as const) {
          expect(p.slug[l], `${s.id}/${p.slug.en}`).toMatch(/^[a-z0-9-]+$/);
          expect(p.title[l].length, `${s.id}/${p.slug.en}`).toBeGreaterThan(0);
          // Die Description ist der einzige Fließtext der Seite im Suchergebnis.
          expect(p.description[l].length, `${s.id}/${p.slug.en}`).toBeGreaterThan(40);
          expect(p.body[l].length, `${s.id}/${p.slug.en}`).toBeGreaterThan(200);
        }
      }
    }
  });

  it("vergibt jeden Slug nur einmal", () => {
    for (const l of ["de", "en"] as const) {
      const slugs = DOC_PAGES.map((p) => p.slug[l]);
      expect(new Set(slugs).size, `doppelter Slug in ${l}`).toBe(slugs.length);
    }
  });

  it("lässt keinen Dateipfad im ausgelieferten Text stehen", () => {
    /* Im Markdown stehen Verweise zwischen Doku-Seiten als Dateipfade, damit
       die Doku auf GitHub navigierbar ist; docs.mjs macht daraus Adressen.
       Bleibt einer stehen, zeigt er im Browser ins Leere. Verweise aus docs/
       heraus ins Repo (../../../spec/…) sind etwas anderes und dürfen. */
    for (const p of DOC_PAGES) {
      for (const l of ["de", "en"] as const) {
        for (const [ganz, href] of p.body[l].matchAll(/\]\((\.\.?\/[^)]+)\)/g)) {
          expect(
            /^(\.\.\/){3}/.test(href),
            `${p.slug.en} (${l}): ${ganz} — nicht aufgelöst`,
          ).toBe(true);
        }
      }
    }
  });

  it("hält die Reihenfolge, in der gelesen wird", () => {
    // Wer neu ist, fängt bei "Was ist Covey?" an, nicht bei "Architektur".
    expect(DOC_SECTIONS[0].id).toBe("introduction");
    expect(DOC_PAGES[0].slug.en).toBe("what-is-covey");
  });

  it("übersetzt die Abschnittstitel", () => {
    for (const s of DOC_SECTIONS) {
      expect(s.title.de.length, s.id).toBeGreaterThan(0);
      expect(s.title.en.length, s.id).toBeGreaterThan(0);
    }
  });
});
