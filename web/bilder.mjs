/* Building the responsive variants of the website's images.
 *
 * Not part of `npm run build`, and deliberately so: it needs cwebp, avifenc and
 * sips, and whoever installs Covey from GitHub must not have to find three
 * encoders before the frontend builds. The results are committed instead; this
 * script is what produced them, and what reproduces them when an image changes.
 *
 *   node web/bilder.mjs            (from the repository root, or from web/)
 *
 * What it makes, per source image and per width: an AVIF and a WebP. The JPEG
 * stays as it is — it is the fallback, and it is what the social-media crawlers
 * fetch (og:image, see public/Head.tsx).
 *
 * The widths follow the place the image is used, not the file it comes from: a
 * thumbnail 120 px wide had no business loading 1280 px of screenshot, and the
 * hero was 1400 px for a phone that shows 390.
 */

import { execFile } from "node:child_process";
import { mkdtemp, rm, stat } from "node:fs/promises";
import { promisify } from "node:util";
import { tmpdir } from "node:os";
import { join } from "node:path";

const lauf = promisify(execFile);
const PUBLIC = new URL("./public/", import.meta.url).pathname;

const BILDER = [
  // The wide swarm band on the start page: full width, a fixed height, cropped.
  // A grainy photograph is the most expensive thing on this page; it is also
  // decoration behind a caption, and it drifts (band-zoom). Hence the lower
  // quality — measured against the screenshots beside it, where a lower one
  // would blur the very thing the picture is there to show.
  { datei: "landing/murmuration.jpg", breiten: [700, 1400], q: 48 },
  // "How Covey works": beside the three steps, at most 320 px high — the
  // source is 1600 × 1600.
  { datei: "landing/formation.jpg", breiten: [480, 960] },
  // The screenshot gallery: one large frame, five thumbnails.
  ...["agents", "org", "backlog", "memory", "costs"].map((n) => ({
    datei: `shots/${n}.jpg`,
    breiten: [320, 1280],
  })),
];

/* Quality: chosen by looking, not by rule. AVIF holds up further down than
   WebP does, which is why the numbers differ. */
const AVIF_Q = 58;
const WEBP_Q = 78;
/* WebP needs roughly twenty points more than AVIF for the same result. */
const WEBP_ABSTAND = 20;

const kb = (n) => `${Math.round(n / 1024)} kB`;

async function groesse(pfad) {
  try {
    return (await stat(pfad)).size;
  } catch {
    return 0;
  }
}

const arbeit = await mkdtemp(join(tmpdir(), "covey-bilder-"));
try {
  for (const { datei, breiten, q = AVIF_Q } of BILDER) {
    const quelle = PUBLIC + datei;
    const stamm = quelle.replace(/\.jpg$/, "");
    const zeile = [`${datei}  ${kb(await groesse(quelle))} jpg`];

    for (const breite of breiten) {
      /* sips scales, the encoders encode — avifenc cannot resize, and giving
         both the same scaled input keeps the two formats comparable. */
      const zwischen = join(arbeit, `${breite}.jpg`);
      await lauf("sips", ["-Z", String(breite), quelle, "--out", zwischen]);

      const avif = `${stamm}-${breite}.avif`;
      const webp = `${stamm}-${breite}.webp`;
      await lauf("avifenc", ["-q", String(q), "-s", "4", zwischen, avif]);
      const qWebp = q === AVIF_Q ? WEBP_Q : q + WEBP_ABSTAND;
      await lauf("cwebp", ["-q", String(qWebp), "-quiet", zwischen, "-o", webp]);

      zeile.push(`${breite}w: ${kb(await groesse(avif))} avif / ${kb(await groesse(webp))} webp`);
    }
    console.log(zeile.join("  ·  "));
  }
} finally {
  await rm(arbeit, { recursive: true, force: true });
}
