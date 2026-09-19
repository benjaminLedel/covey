import { useMemo } from "react";

/* Das Gesicht eines Agenten.
 *
 * Eine Kollegenliste aus Monogrammen sieht aus wie eine Tabelle, und eine
 * Belegschaft, die aussieht wie eine Tabelle, wird auch so behandelt. Dieses
 * Produkt behauptet, Agenten seien Mitarbeiter; dann darf man sie auch
 * auseinanderhalten können, ohne den Namen zu lesen.
 *
 * Was das Gesicht NICHT ist: ein Avatar, den jemand aussucht. Es wird aus dem
 * Kürzel des Agenten gerechnet, ist also für denselben Agenten immer dasselbe,
 * auf jedem Gerät und in jeder Sitzung — und niemand muss eines pflegen.
 *
 * Und es zeigt den Zustand, den die Liste sonst nur als Wort trägt:
 *
 *   arbeitet  — die Augen wandern, ganz langsam
 *   schläft   — die Augen sind zwei Striche
 *   gestoppt  — die Augen sind zu, das Gesicht ist entsättigt
 *
 * Die Bewegung ist so klein, dass sie beim Lesen nicht stört, und sie steht
 * still, sobald das Betriebssystem weniger Bewegung verlangt (app.css,
 * prefers-reduced-motion). Ein Gesicht, das zappelt, ist kein Kollege,
 * sondern ein Werbebanner.
 */

export type Zustand = "working" | "sleeping" | "killed";

/* Sechs Farbpaare, kräftig genug, um zwei Kollegen nebeneinander zu
   unterscheiden, und dunkel genug, dass die Augen darauf stehen. Sie sind
   fest und nicht aus dem Hash gewürfelt: Eine frei gerechnete Farbe landet
   irgendwann auf Neongelb, und dann steht das Auge auf nichts. */
const TOENE = [
  { h: "#2f6f5e", d: "#4fb99a" },
  { h: "#3a5b96", d: "#7fa6e8" },
  { h: "#8f3f18", d: "#e89a76" },
  { h: "#6b4a86", d: "#b394d8" },
  { h: "#8a6a12", d: "#d8b04a" },
  { h: "#96384f", d: "#e58ca0" },
];

/* Ein kleiner, stabiler Hash über das Kürzel. Kein Zufall: Derselbe Agent
   bekommt dasselbe Gesicht, immer. */
function hash(text: string): number {
  let h = 2166136261;
  for (let i = 0; i < text.length; i++) {
    h ^= text.charCodeAt(i);
    h = Math.imul(h, 16777619);
  }
  return Math.abs(h);
}

export default function Gesicht({
  schluessel,
  zustand = "working",
  groesse = 26,
}: {
  /** Das Kürzel des Agenten — daraus entsteht das Gesicht. */
  schluessel: string;
  zustand?: Zustand;
  groesse?: number;
}) {
  const g = useMemo(() => {
    const h = hash(schluessel);
    return {
      ton: TOENE[h % TOENE.length],
      /* Zwei Züge, die sich unterscheiden lassen, ohne ein Gesicht zu
         karikieren: wie weit die Augen auseinanderstehen und wie hoch sie
         sitzen. Mehr Varianz braucht es nicht — sechs Farben mal neun
         Augenstellungen sind vierundfünfzig unterscheidbare Kollegen. */
      abstand: 3.6 + (h % 3) * 0.9,
      hoehe: 11.4 + (Math.floor(h / 3) % 3) * 0.9,
      /* Damit nicht alle gleichzeitig blinzeln. */
      takt: (h % 7) * 0.9,
    };
  }, [schluessel]);

  const augeY = g.hoehe;
  const schlaeft = zustand === "sleeping";
  const tot = zustand === "killed";

  return (
    <svg
      className={`gesicht z-${zustand}`}
      style={{ ["--takt" as string]: `${g.takt}s`, ["--ton" as string]: g.ton.h, ["--ton-dunkel" as string]: g.ton.d }}
      width={groesse}
      height={groesse}
      viewBox="0 0 24 24"
      aria-hidden="true"
    >
      {/* Der Kopf: ein weiches Quadrat, keine Kugel — eine Kugel neben einer
          Kugel neben einer Kugel liest sich als Aufzählungszeichen. */}
      <rect className="gesicht-kopf" x="1.5" y="1.5" width="21" height="21" rx="7.5" />
      {schlaeft || tot ? (
        <>
          <path className="gesicht-lid" d={`M${12 - g.abstand - 1.5} ${augeY} h3`} />
          <path className="gesicht-lid" d={`M${12 + g.abstand - 1.5} ${augeY} h3`} />
        </>
      ) : (
        <g className="gesicht-augen">
          <circle cx={12 - g.abstand} cy={augeY} r="1.85" />
          <circle cx={12 + g.abstand} cy={augeY} r="1.85" />
        </g>
      )}
    </svg>
  );
}
