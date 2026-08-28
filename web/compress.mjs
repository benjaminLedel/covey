/* Precompressing the built website. Runs after prerender.mjs and writes a .br
 * and a .gz beside every file that benefits from it.
 *
 * Why at build time and not in the request: internal/httpapi/compression.go
 * compresses on the fly, at gzip's fastest setting — which is right for what it
 * was written for, answers that are computed per request and never twice the
 * same. The bundle is the opposite case. It is built once, it is identical for
 * every visitor until the next release, and it carries a content hash in its
 * name. Compressing it again on every request pays CPU for a result that could
 * have been computed once, at the best setting — and brotli, which no
 * on-the-fly compressor here speaks, saves another fifth on top of gzip.
 *
 * Not the HTML: it is assembled per request (the address of the installation
 * goes in, see internal/httpapi/spa.go), so a precompressed copy would be a
 * copy of something that does not exist yet.
 */

import { readdir, readFile, stat, writeFile } from "node:fs/promises";
import { join, extname } from "node:path";
import { constants, brotliCompress, gzip } from "node:zlib";
import { promisify } from "node:util";

const br = promisify(brotliCompress);
const gz = promisify(gzip);

const DIST = new URL("./dist/", import.meta.url).pathname;

/* An allowlist, for the same reason as in compression.go: a format that lands
 * here and is already compressed (woff2 carries brotli inside, jpg, png, gif,
 * ico) would cost build time and disk for nothing. */
const LOHNT = new Set([".js", ".css", ".svg", ".json", ".webmanifest", ".xml", ".txt", ".map"]);

/* Below this the header of the compressed form is most of the answer. */
const KLEINSTES = 512;

async function* dateien(dir) {
  for (const eintrag of await readdir(dir, { withFileTypes: true })) {
    const pfad = join(dir, eintrag.name);
    if (eintrag.isDirectory()) yield* dateien(pfad);
    else yield pfad;
  }
}

let dateienGezaehlt = 0;
let roh = 0;
let brotli = 0;

for await (const pfad of dateien(DIST)) {
  const ext = extname(pfad);
  if (!LOHNT.has(ext)) continue;
  if (pfad.endsWith(".br") || pfad.endsWith(".gz")) continue;
  const { size } = await stat(pfad);
  if (size < KLEINSTES) continue;

  const daten = await readFile(pfad);
  const [b, g] = await Promise.all([
    br(daten, {
      params: {
        [constants.BROTLI_PARAM_QUALITY]: constants.BROTLI_MAX_QUALITY,
        [constants.BROTLI_PARAM_SIZE_HINT]: daten.length,
      },
    }),
    gz(daten, { level: 9 }),
  ]);

  /* A compressed form that is not smaller is one the handler would serve
     instead of the original. */
  if (b.length < daten.length) await writeFile(pfad + ".br", b);
  if (g.length < daten.length) await writeFile(pfad + ".gz", g);

  dateienGezaehlt++;
  roh += daten.length;
  brotli += Math.min(b.length, daten.length);
}

const kb = (n) => `${Math.round(n / 1024)} kB`;
console.log(
  `compress: ${dateienGezaehlt} files, ${kb(roh)} → ${kb(brotli)} brotli ` +
    `(${Math.round((1 - brotli / roh) * 100)} %)`,
);
