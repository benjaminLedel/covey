/* Macht aus docs/ das Modul, das die Website liest.
 *
 * Die Doku liegt als Markdown im Repository — englisch als Quelle unter
 * docs/en/, die Übersetzung unter demselben relativen Pfad in docs/de/. Das
 * ist die einzige Fassung: sie steht auf GitHub, nimmt einen Pull Request an
 * und wird von hier aus in die Seite gezogen (#128). Vorher lag derselbe Text
 * ein zweites Mal als 124 KB TypeScript neben der Website, und nur die Fassung,
 * die niemand ändern konnte, wurde ausgeliefert.
 *
 * Erzeugt wird src/public/docs/content.generated.ts — nicht eingecheckt,
 * sondern vor Build und Test geschrieben. Eine erzeugte Datei im Repository
 * driftet von ihrer Quelle weg, sobald jemand sie von Hand anfasst.
 *
 * Das hier ist eine Brücke auf Zeit: sobald die Website ein eigenes Repository
 * ist (#129), liest sie docs/ selbst und dieser Schritt entfällt.
 */

import { readFileSync, writeFileSync, mkdirSync, existsSync, readdirSync } from "node:fs";
import { dirname, join, normalize } from "node:path";
import { fileURLToPath } from "node:url";
import { load } from "js-yaml";

const HIER = dirname(fileURLToPath(import.meta.url));
const DOCS = join(HIER, "..", "docs");
const ZIEL = join(HIER, "src", "public", "docs", "content.generated.ts");

const LANGS = ["de", "en"];

/** Trennt YAML-Frontmatter vom Rumpf. */
function lies(datei) {
  const text = readFileSync(datei, "utf8");
  const m = /^---\n([\s\S]*?)\n---\n([\s\S]*)$/.exec(text);
  if (!m) throw new Error(`${datei}: kein Frontmatter`);
  const front = load(m[1]) ?? {};
  for (const feld of ["slug", "title", "description"]) {
    if (!front[feld]) throw new Error(`${datei}: "${feld}" fehlt im Frontmatter`);
  }
  return { ...front, body: m[2].trim() };
}

const ordnung = load(readFileSync(join(DOCS, "sections.yml"), "utf8"));

const sektionen = ordnung.sections.map((sek) => ({
  id: sek.id,
  title: sek.title,
  pages: sek.pages.map((name) => {
    /* Englisch ist die Quelle und muss da sein; Deutsch darf fehlen. Die 17
       Betriebs-Rezepte gibt es nur auf Englisch, und das bleibt so — 5.400
       Zeilen Betriebsanleitung hält niemand zweisprachig aktuell. Wo die
       Übersetzung fehlt, steht die englische Fassung unter ihrer eigenen
       Adresse; ein hreflang auf eine Seite, die es nicht gibt, wäre die
       schlechtere Antwort. */
    const en = lies(join(DOCS, "en", sek.id, `${name}.md`));
    let de;
    try {
      de = lies(join(DOCS, "de", sek.id, `${name}.md`));
    } catch (e) {
      if (e.code !== "ENOENT") throw e;
      de = en;
    }

    const seite = { slug: {}, title: {}, description: {}, body: {} };
    for (const [lang, q] of [["de", de], ["en", en]]) {
      seite.slug[lang] = q.slug;
      seite.title[lang] = q.title;
      seite.description[lang] = q.description;
      seite.body[lang] = q.body;
    }
    if (de.faq || en.faq) {
      seite.faq = { de: de.faq ?? [], en: en.faq ?? [] };
    }
    /* Nur-englische Seiten: beide Adressen sind dieselbe. Sonst gäbe es die
       Seite unter /docs/<englischer-slug> ein zweites Mal. */
    seite.nurEnglisch = de === en;
    return seite;
  }),
}));

/* Die Links zwischen den Seiten stehen im Markdown als Dateipfade
   (../concepts/memory.md), damit die Doku auch auf GitHub navigierbar ist.
   Für die Website werden sie zu Adressen — in der Sprache der lesenden Seite,
   denn /docs/gedaechtnis und /en/docs/memory sind dieselbe Seite. */
const nachSlug = new Map();
for (const sek of sektionen) {
  for (const s of sek.pages) nachSlug.set(`${sek.id}/${s.slug.en}`, s);
}

function linksAufAdressen(body, sektionID, lang) {
  return body.replace(/\]\((\.\.?\/[^)]+?)\.md(#[^)]*)?\)/g, (ganz, pfad, anker = "") => {
    const teile = `${sektionID}/${pfad}`.split("/");
    const stapel = [];
    for (const t of teile) {
      if (t === "..") stapel.pop();
      else if (t !== "." && t !== "") stapel.push(t);
    }
    const ziel = nachSlug.get(stapel.join("/"));
    if (!ziel) return ganz; // zeigt aus docs/ heraus ins Repo — bleibt, wie es ist
    const basis = lang === "en" ? "/en/docs" : "/docs";
    return `](${basis}/${ziel.slug[lang]}${anker})`;
  });
}

for (const sek of sektionen) {
  for (const s of sek.pages) {
    for (const lang of LANGS) s.body[lang] = linksAufAdressen(s.body[lang], sek.id, lang);
  }
}

/* Was den Dateibaum betrifft, prüft dieser Schritt — er ist die Stelle, die
   ihn liest. Ein Fehler hier bricht Build und Test ab, statt eine Seite still
   fehlen zu lassen. */
const klagen = [];

/** Alle Markdown-Dateien eines Sprachbaums als "<sektion>/<name>". */
const dateien = (lang) => {
  const wurzel = join(DOCS, lang);
  if (!existsSync(wurzel)) return [];
  return readdirSync(wurzel, { withFileTypes: true })
    .filter((d) => d.isDirectory())
    .flatMap((d) =>
      readdirSync(join(wurzel, d.name))
        .filter((f) => f.endsWith(".md"))
        .map((f) => `${d.name}/${f.replace(/\.md$/, "")}`),
    );
};

// Eine Seite, die in sections.yml fehlt, ist geschrieben und unsichtbar.
const geordnet = new Set(sektionen.flatMap((s) => s.pages.map((p) => `${s.id}/${p.slug.en}`)));
for (const f of dateien("en")) {
  if (!geordnet.has(f)) klagen.push(`docs/en/${f}.md steht in keiner Sektion von docs/sections.yml`);
}

/* Englisch ist die Quelle; Deutsch darf fehlen. Andersherum nicht — eine
   Übersetzung ohne Original hat keine Vorlage, gegen die sie altert. */
const originale = new Set(dateien("en"));
for (const f of dateien("de")) {
  if (!originale.has(f)) klagen.push(`docs/de/${f}.md hat kein Original unter docs/en/`);
}

// Verweise, die nicht auf eine Doku-Seite zeigen, müssen ins Repo treffen.
for (const sek of sektionen) {
  for (const s of sek.pages) {
    for (const lang of LANGS) {
      for (const [, pfad] of s.body[lang].matchAll(/\]\((\.\.?\/[^)]+?)(?:#[^)]*)?\)/g)) {
        const ziel = normalize(join(DOCS, lang, sek.id, pfad));
        if (!existsSync(ziel)) klagen.push(`${sek.id}/${s.slug.en} (${lang}): ${pfad} zeigt ins Leere`);
      }
    }
  }
}

if (klagen.length) {
  console.error(`docs: ${klagen.length} Fehler\n  ` + klagen.join("\n  "));
  process.exit(1);
}

const kopf = `/* ERZEUGT — nicht von Hand ändern.
   Quelle: docs/ (Markdown) plus docs/sections.yml, übersetzt von web/docs.mjs.
   Wer den Text ändern will, ändert die Markdown-Datei. Siehe #128. */

export type Lang = "de" | "en";
export type Localized = Record<Lang, string>;
export type Faq = { q: string; a: string };
export type DocPage = {
  slug: Localized;
  title: Localized;
  description: Localized;
  body: Localized;
  faq?: Record<Lang, Faq[]>;
  /** Seite ohne Übersetzung: beide Sprachen zeigen denselben englischen Text. */
  nurEnglisch?: boolean;
};
export type DocSection = { id: string; title: Localized; pages: DocPage[] };

export const DOC_SECTIONS: DocSection[] = `;

const fuss = `;

export const DOC_PAGES: DocPage[] = DOC_SECTIONS.flatMap((s) => s.pages);
export const FIRST_DOC = DOC_PAGES[0];
export const docLang = (l: string): Lang => (l.startsWith("en") ? "en" : "de");
`;

mkdirSync(dirname(ZIEL), { recursive: true });
writeFileSync(ZIEL, kopf + JSON.stringify(sektionen, null, 2) + fuss, "utf8");

const seiten = sektionen.reduce((n, s) => n + s.pages.length, 0);
const nurEn = sektionen.reduce((n, s) => n + s.pages.filter((p) => p.nurEnglisch).length, 0);
console.log(`docs: ${seiten} Seiten aus ${sektionen.length} Abschnitten (${nurEn} nur englisch)`);
