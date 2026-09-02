/* Das Signet: drei Vögel in Flugformation auf einer Lehm-Kachel.
 *
 * Eine Komponente, weil es vorher drei gab — Sidebar, Einrichtung und
 * Anmeldebereich zeichneten dasselbe SVG jeweils selbst, mit eigenen Radien,
 * eigenen Schatten und im Anmeldebereich einem eigenen Farbverlauf. Drei
 * Kopien einer Marke driften auseinander, und genau das war passiert.
 *
 * Die Kachel steckt IM SVG und nicht in einem Kasten darum: So ist dieses
 * Markup Zeichen für Zeichen dasselbe wie auf der Website und in
 * public/favicon.svg, und es braucht kein style-Attribut. Radius 14 auf 64
 * ist derselbe Anteil wie im Favicon — die Kachel in der Oberfläche und die
 * im Browser-Reiter sind damit dieselbe Form.
 *
 * Flach, ohne Verlauf und ohne Glanzkante: Was bei 512px ein weicher Verlauf
 * ist, wird bei 16px ein schmutziger Fleck — deshalb trug das Favicon bis
 * #131 eine ganz andere Fassung als die Oberfläche. Die Farben stehen als
 * Literale, nicht als Token: eine Marke dreht nicht mit dem Erscheinungsbild. */
export function BirdMark({ size = 26 }: { size?: number }) {
  return (
    <svg className="signet" aria-hidden="true" viewBox="0 0 64 64" width={size} height={size}>
      <rect width="64" height="64" rx="14" fill="#cc7a5b" />
      <g
        transform="translate(-0.7,1.1) scale(2.9)"
        fill="none"
        stroke="#ffffff"
        strokeWidth={2.3}
        strokeLinecap="round"
        strokeLinejoin="round"
      >
        <path d="M7 15 Q9.75 11.8 12.5 15 Q15.25 11.8 18 15" />
        <path d="M3.5 10 Q5.5 7.7 7.5 10 Q9.5 7.7 11.5 10" />
        <path d="M13 8 Q14.5 6.3 16 8 Q17.5 6.3 19 8" />
      </g>
    </svg>
  );
}
