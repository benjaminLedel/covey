import { describe, expect, it } from "vitest";
// Der Quelltext von App.tsx als Text — Vite löst ?raw auf, damit der Test ohne
// Node-Typen auskommt (tsc -b prüft die Tests mit).
import appQuelltext from "../App.tsx?raw";
import { DOC_PAGES } from "./docs/docsContent";
import {
  APP_ROUTE_PREFIXES,
  LANGS,
  PUBLIC_ROUTES,
  SEO_URLS,
  altPath,
  descriptionFromMarkdown,
  matchRoute,
  pathOf,
} from "./seo";

describe("Routen-Tabelle", () => {
  it("vergibt jeden Pfad nur einmal", () => {
    const alle = PUBLIC_ROUTES.flatMap((r) => LANGS.map((l) => r.path[l]));
    expect(new Set(alle).size).toBe(alle.length);
  });

  it("trennt die Sprachen sauber: englisch unter /en, deutsch nicht", () => {
    for (const r of PUBLIC_ROUTES) {
      expect(r.path.en === "/en" || r.path.en.startsWith("/en/")).toBe(true);
      expect(r.path.de.startsWith("/en")).toBe(false);
    }
  });

  it("gibt jeder Seite einen eigenen Titel und eine Description", () => {
    const titel = PUBLIC_ROUTES.flatMap((r) => LANGS.map((l) => r.title[l]));
    expect(new Set(titel).size).toBe(titel.length);

    for (const r of PUBLIC_ROUTES) {
      for (const l of LANGS) {
        // Was länger ist, kürzt Google im Ergebnis ab; was fehlt, ersetzt es
        // durch selbst gewählten Text von der Seite.
        expect(r.title[l].length, `Titel ${r.id}/${l}`).toBeLessThanOrEqual(70);
        expect(r.description[l].length, `Description ${r.id}/${l}`).toBeGreaterThan(40);
        expect(r.description[l].length, `Description ${r.id}/${l}`).toBeLessThanOrEqual(175);
      }
    }
  });

  it("hält die Anmeldeseite aus der Sitemap heraus", () => {
    expect(SEO_URLS.some((u) => u.path.includes("anmelden"))).toBe(false);
    expect(SEO_URLS.some((u) => u.path.includes("sign-in"))).toBe(false);
  });

  it("findet zu jedem Pfad die Route und das Sprachgegenstück", () => {
    expect(matchRoute("/funktion")?.route.id).toBe("funktion");
    expect(matchRoute("/en/how-it-works")?.lang).toBe("en");
    // Mit und ohne abschließenden Schrägstrich dieselbe Seite.
    expect(matchRoute("/funktion/")?.route.id).toBe("funktion");
    expect(matchRoute("/gibt-es-nicht")).toBeNull();

    expect(altPath("/funktion", "en")).toBe("/en/how-it-works");
    expect(altPath("/en/how-it-works", "de")).toBe("/funktion");
    // Unbekanntes landet auf der Startseite der Zielsprache, nicht im Nichts.
    expect(altPath("/gibt-es-nicht", "en")).toBe("/en");

    expect(pathOf("docs", "en")).toBe("/en/docs");
  });
});

describe("Description aus Markdown", () => {
  it("überspringt Überschriften und entfernt Auszeichnung", () => {
    const md = "# Titel\n\nEin **wichtiger** Satz mit [Link](/x) und `Code`.";
    expect(descriptionFromMarkdown(md)).toBe("Ein wichtiger Satz mit Link und Code.");
  });

  it("kürzt an der Wortgrenze", () => {
    const lang = descriptionFromMarkdown("wort ".repeat(60), 40);
    expect(lang.length).toBeLessThanOrEqual(42);
    expect(lang.endsWith("…")).toBe(true);
  });
});

/* Die Präfixe der angemeldeten Oberfläche stehen in seo.ts, die Routen in
   App.tsx. Der Go-Handler entscheidet anhand der Präfixe, ob ein Pfad die
   SPA-Hülle bekommt oder eine 404 — eine vergessene Route wäre damit ab sofort
   nicht mehr erreichbar. Deshalb hier der Abgleich. */
describe("App-Präfixe", () => {
  const pfade = [...appQuelltext.matchAll(/<Route\s+path="([^"]+)"/g)]
    .map((m) => m[1])
    .filter((p) => p !== "/" && p !== "*");

  it("deckt jede Route aus App.tsx ab", () => {
    expect(pfade.length).toBeGreaterThan(10);
    for (const p of pfade) {
      const prefix = "/" + p.split("/")[1];
      expect(APP_ROUTE_PREFIXES, `Route ${p} fehlt in APP_ROUTE_PREFIXES`).toContain(prefix);
    }
  });

  it("führt kein Präfix, das es in App.tsx nicht gibt", () => {
    const vorhanden = new Set(pfade.map((p) => "/" + p.split("/")[1]));
    for (const prefix of APP_ROUTE_PREFIXES) {
      expect(vorhanden, `${prefix} hat keine Route in App.tsx`).toContain(prefix);
    }
  });

  it("kollidiert nicht mit den öffentlichen Pfaden", () => {
    const oeffentlich = PUBLIC_ROUTES.flatMap((r) => LANGS.map((l) => r.path[l]));
    for (const prefix of APP_ROUTE_PREFIXES) {
      expect(oeffentlich).not.toContain(prefix);
    }
  });
});

/* Die Docs-Seiten tragen ihre Auffindbarkeit selbst: eigener Slug je Sprache,
   geschriebene Description, Fragen in beiden Fassungen. Das hier hält fest,
   dass eine neue Seite das nicht vergessen kann. */
describe("Docs-Seiten", () => {
  it("gibt jeder Sprache einen eigenen Slug-Raum ohne Dubletten", () => {
    for (const l of LANGS) {
      const slugs = DOC_PAGES.map((p) => p.slug[l]);
      expect(new Set(slugs).size, `Slugs ${l}`).toBe(slugs.length);
    }
  });

  it("schreibt englische Slugs englisch", () => {
    // Ein paar Wörter, die im Deutschen wie im Englischen gleich aussehen
    // (guard-rails, companion), sind erlaubt — Umlaut-Transkriptionen und
    // deutsche Wörter nicht: /en/docs/gedaechtnis war genau der Fehler.
    const deutsch = /(gedaechtnis|schnellstart|zielsysteme|betrieb|identitaet|kernkonzepte|ersten)/;
    for (const p of DOC_PAGES) {
      expect(p.slug.en, `englischer Slug von ${p.slug.de}`).not.toMatch(deutsch);
    }
  });

  it("gibt jeder Seite eine geschriebene Description", () => {
    for (const p of DOC_PAGES) {
      for (const l of LANGS) {
        expect(p.description[l].length, `Description ${p.slug.de}/${l}`).toBeGreaterThan(60);
      }
    }
  });

  it("hält die Fragen in beiden Sprachen gleich lang", () => {
    // Eine FAQ, die es nur auf Deutsch gibt, wäre in der englischen Fassung
    // eine ausgezeichnete Frage ohne sichtbare Antwort.
    for (const p of DOC_PAGES) {
      if (!p.faq) continue;
      expect(p.faq.en.length, `FAQ-Anzahl ${p.slug.de}`).toBe(p.faq.de.length);
      for (const l of LANGS) {
        for (const f of p.faq[l]) {
          expect(f.q.length, `Frage ${p.slug.de}/${l}`).toBeGreaterThan(10);
          expect(f.a.length, `Antwort ${p.slug.de}/${l}`).toBeGreaterThan(40);
        }
      }
    }
  });
});
