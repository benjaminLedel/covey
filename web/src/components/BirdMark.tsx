/* The signet: three birds in flight formation on a clay tile.
 *
 * One component, because there were three before — sidebar, setup and
 * the sign-in area each drew the same SVG themselves, with their own radii,
 * their own shadows and in the sign-in area a gradient of their own. Three
 * copies of a mark drift apart, and that is exactly what had happened.
 *
 * The tile sits INSIDE the SVG and not in a box around it: that way this
 * markup is character for character the same as on the website and in
 * public/favicon.svg, and it needs no style attribute. Radius 14 on 64
 * is the same proportion as in the favicon — the tile in the UI and the
 * one in the browser tab are thereby the same shape.
 *
 * Flat, without a gradient and without a gloss edge: what is a soft gradient
 * at 512px becomes a dirty smudge at 16px — which is why the favicon up to
 * #131 carried a quite different version than the UI. The colors stand as
 * literals, not as tokens: a mark does not change with the appearance. */
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
