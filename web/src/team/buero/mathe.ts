/* The computed scatter.
 *
 * "Random" would be wrong in the office: a building that stands differently
 * on every visit is worse than one that looks staged — nothing can be
 * remembered, and every background refetch would move the furniture. So
 * everything that is meant to look random comes from a hash of the key: the
 * same offset for the same seat every time, and still no grid. The same
 * function sits in components/Gesicht.tsx; the faces and the house must draw
 * the same number from the same key.
 */

/** FNV-1a over the text, as a positive number. */
export function hash(text: string): number {
  let h = 2166136261;
  for (let i = 0; i < text.length; i++) {
    h ^= text.charCodeAt(i);
    h = Math.imul(h, 16777619);
  }
  return Math.abs(h);
}

/** A number between −weite and +weite, fixed per key. */
export function wackel(schluessel: string, weite: number): number {
  return ((hash(schluessel) % 2000) / 1000 - 1) * weite;
}

/** One entry of a list, fixed per key — this is how the catalogue chooses. */
export function sorte<T>(liste: readonly T[], schluessel: string | number): T {
  return liste[hash(String(schluessel)) % liste.length];
}
