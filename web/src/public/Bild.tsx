/* Ein Bild der öffentlichen Website — in drei Formaten und mehreren Größen.
 *
 * Vorher stand hier ein blankes <img> auf ein JPEG: eine Datei, eine Größe,
 * für jedes Gerät dieselbe. Das Schwarm-Band war damit 334 kB, und ein Telefon
 * mit 390 px Breite lud dieselben Pixel wie ein Schreibtisch (#125).
 *
 * Die Varianten liegen fertig daneben — gebaut von web/bilder.mjs, eingecheckt,
 * nicht im Build erzeugt: Wer Covey von GitHub baut, soll dafür keine drei
 * Encoder brauchen. Das JPEG bleibt als Rückfall und ist das, was die Crawler
 * der sozialen Netze holen (og:image, siehe Head.tsx).
 *
 * `sizes` sagt dem Browser, wie breit das Bild auf der Seite wirklich steht —
 * ohne die Angabe rechnet er mit der vollen Fensterbreite und lädt regelmäßig
 * eine Nummer zu groß.
 */

type Props = {
  /* Der Pfad des JPEGs, so wie er im Markup stand. */
  src: string;
  breiten: number[];
  sizes: string;
  alt: string;
  /* Die Maße des Originals. Sie halten den Platz frei, bevor das Bild da ist —
     das Layout springt sonst, sobald es ankommt. */
  breite: number;
  hoehe: number;
  className?: string;
};

export default function Bild({ src, breiten, sizes, alt, breite, hoehe, className }: Props) {
  const stamm = src.replace(/\.jpg$/, "");
  const satz = (endung: string) =>
    breiten.map((b) => `${stamm}-${b}.${endung} ${b}w`).join(", ");

  return (
    <picture>
      <source type="image/avif" srcSet={satz("avif")} sizes={sizes} />
      <source type="image/webp" srcSet={satz("webp")} sizes={sizes} />
      <img
        src={src}
        alt={alt}
        width={breite}
        height={hoehe}
        loading="lazy"
        decoding="async"
        className={className}
      />
    </picture>
  );
}
