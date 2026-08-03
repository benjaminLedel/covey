/* Vorrendern der öffentlichen Website. Läuft nach `vite build` und macht aus
   der leeren Hülle echte HTML-Dateien — eine je Seite und Sprache.
 *
 * dist/index.html            → Startseite, vorgerendert
 * dist/funktion/index.html   → …
 * dist/en/…/index.html       → englische Fassung
 * dist/404.html              → Seite-nicht-gefunden (der Go-Server liefert sie
 *                              mit Status 404 aus)
 * dist/app.html              → die unveränderte Hülle für die angemeldete
 *                              Oberfläche; sie soll nicht indexiert werden und
 *                              braucht keinen vorgerenderten Inhalt
 * dist/seo.json              → Routenliste für sitemap.xml und den SPA-Handler
 *
 * Die Origin ist hier nicht bekannt (jede Installation hat eine eigene) —
 * im HTML steht ein Platzhalter, den der Go-Server beim Ausliefern ersetzt.
 */

import { mkdir, readFile, writeFile } from "node:fs/promises";
import { dirname, join } from "node:path";

const DIST = new URL("./dist/", import.meta.url).pathname;
const SSR = new URL("./dist-ssr/entry-server.js", import.meta.url).pathname;

const {
  renderPage,
  PRERENDER_URLS,
  SEO_URLS,
  APP_ROUTE_PREFIXES,
  NOT_FOUND_ROUTE,
  LANGS,
} = await import(SSR);

const template = await readFile(join(DIST, "index.html"), "utf8");

/* Die Hülle bleibt als app.html erhalten, bevor index.html überschrieben wird. */
await writeFile(join(DIST, "app.html"), template);

/* Auf Vorkommen prüfen, nicht auf Veränderung: Bei der deutschen Fassung ist
   das Ergebnis der lang-Ersetzung identisch mit der Vorlage. */
function replaceOnce(text, pattern, replacement, was) {
  if (!pattern.test(text)) throw new Error(`${was} nicht in der Hülle gefunden`);
  return text.replace(pattern, replacement);
}

function assemble(template, { html, head, lang }) {
  let out = replaceOnce(template, /<html lang="[^"]*">/, `<html lang="${lang}">`, "html-lang");

  // Der Titel der Hülle weicht dem generierten Kopf — zwei <title> im selben
  // Dokument sind kein Fehler, den ein Browser meldet, aber einer, den eine
  // Suchmaschine sieht.
  out = replaceOnce(out, /<title>.*?<\/title>/s, () => head, "<title>");

  return replaceOnce(
    out,
    /<div id="root"><\/div>/,
    () => `<div id="root" data-prerendered="">${html}</div>`,
    '<div id="root">',
  );
}

/** URL-Pfad → Datei in dist/. */
function fileFor(path) {
  if (path === "/") return join(DIST, "index.html");
  return join(DIST, path.replace(/^\//, ""), "index.html");
}

async function write(file, content) {
  await mkdir(dirname(file), { recursive: true });
  await writeFile(file, content);
}

let count = 0;
for (const { path, lang } of PRERENDER_URLS) {
  const page = await renderPage(path, lang);
  await write(fileFor(path), assemble(template, page));
  count++;
}

/* Die 404-Seite je Sprache. Sie liegt flach, nicht unter einem Pfad — sie
   gehört zu keiner Adresse, sondern zu allen falschen. */
for (const lang of LANGS) {
  const page = await renderPage(NOT_FOUND_ROUTE.path[lang], lang);
  const file = lang === "de" ? join(DIST, "404.html") : join(DIST, "en", "404.html");
  await write(file, assemble(template, page));
  count++;
}

/* Was der Go-Server über die Website wissen muss: welche Adressen es gibt
   (sitemap.xml) und welche Pfade zur angemeldeten Oberfläche gehören (dort
   greift der Fallback auf app.html statt einer 404). */
await write(
  join(DIST, "seo.json"),
  JSON.stringify(
    {
      urls: SEO_URLS.map(({ path, lang, priority, alt }) => ({ path, lang, priority, alt })),
      appPrefixes: APP_ROUTE_PREFIXES,
    },
    null,
    2,
  ),
);

console.log(`vorgerendert: ${count} Seiten, ${SEO_URLS.length} URLs in der Sitemap`);
