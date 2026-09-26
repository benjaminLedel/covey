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
 *   arbeitet  — der Kopf atmet, die Augen sehen sich um, es wird geblinzelt,
 *               und der Mund ist ein konzentrierter Strich
 *   schläft   — die Augen sind zu, der Mund ein kleines o, und darüber
 *               steigen drei z auf
 *   gestoppt  — alles steht still und ist entsättigt
 *
 * Der MUND war zuerst nicht da, und das war eine Entscheidung: Ein Mund trägt
 * Gefühl, und ein Produkt, dessen Ton nüchtern ist, will von seiner Software
 * keine Stimmung vorgeführt bekommen. Er ist trotzdem gekommen, weil er der
 * beste Träger des Zustands ist, den es gibt — aber als Strich, nicht als
 * Lächeln: Er sagt „konzentriert" und „schläft", nicht „freut sich".
 *
 * Alles steht still, sobald das Betriebssystem weniger Bewegung verlangt
 * (app.css, prefers-reduced-motion).
 */

export type Zustand = "working" | "sleeping" | "killed";

/* Sechs Farbpaare, gerade kräftig genug, um zwei Kollegen nebeneinander zu
   unterscheiden, und dunkel genug, dass die Augen darauf stehen. Sie sind
   fest und nicht aus dem Hash gewürfelt: Eine frei gerechnete Farbe landet
   irgendwann auf Neongelb, und dann steht das Auge auf nichts.
   
   Sie waren einmal deutlich bunter — sechs kräftige Töne, die einzeln gut
   aussahen. Im Büro stehen aber fünfzig davon gleichzeitig auf einer Fläche,
   und dort wurde daraus eine Tüte Bonbons: sechs Farbfamilien ohne
   Verwandtschaft, jede so laut wie die nächste, auf einem Grund, der genau
   neutral sein will. Jetzt liegen alle sechs auf EINER Helligkeit und EINER
   Sättigung und unterscheiden sich nur in der Richtung des Tons — nah genug
   beieinander, dass fünfzig Köpfe eine Belegschaft ergeben, weit genug
   auseinander, dass zwei nebeneinander zwei bleiben. Berechnet in oklch
   (L 0.52 / C 0.05 hell, L 0.74 / C 0.055 dunkel), damit „gleich hell" auch
   für das Auge gilt und nicht nur für die Zahl. */
const TOENE = [
  { h: "#497367", d: "#86b7a9" },
  { h: "#566b86", d: "#94adce" },
  { h: "#826051", d: "#c9a18f" },
  { h: "#6f6280", d: "#b3a3c7" },
  { h: "#6d6b49", d: "#b0ad85" },
  { h: "#835d62", d: "#cb9da3" },
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
  blick,
  blickRef,
}: {
  /** Das Kürzel des Agenten — daraus entsteht das Gesicht. */
  schluessel: string;
  zustand?: Zustand;
  groesse?: number;
  /* Wohin geschaut wird, in Einheiten des Rasters (24 breit), etwa ±1,2.
     Gesetzt wird es dort, wo es einen Grund dafür gibt — im Büro folgen die
     Wachen dem Zeiger. Ohne diesen Wert bleibt das Gesicht bei seinem eigenen
     Umsehen, und ein Gesicht in einer Liste hat keinen Grund, jemandem
     hinterherzusehen. */
  blick?: { x: number; y: number };
  /* Derselbe Blick, nur nicht über React: Im Büro ändert er sich mit jedem
     Bild — für siebzig Gesichter wäre das siebzig Abgleiche je Bild, nur um
     zwei Pixel zu verschieben. Wer diesen Haken setzt, bekommt die Gruppe und
     schreibt ihr `transform` selbst (team/Buero.tsx). */
  blickRef?: (g: SVGGElement | null) => void;
}) {
  const g = useMemo(() => {
    const h = hash(schluessel);
    return {
      ton: TOENE[h % TOENE.length],
      /* Fünf Züge, die sich unterscheiden lassen, ohne ein Gesicht zu
         karikieren: Augenabstand, Augenhöhe, Augengröße, Mundbreite und die
         Rundung des Kopfes. Mit sechs Farben sind das 6·3·3·2·2·3 = 648
         unterscheidbare Kollegen — mehr, als eine Organisation je hat, und
         alle aus demselben Bauplan. Jede Zahl kommt aus einer anderen
         Stelle des Hashes, sonst wandern zwei Züge im Gleichschritt. */
      abstand: 3.4 + (h % 3) * 0.95,
      hoehe: 11.2 + (Math.floor(h / 3) % 3) * 0.95,
      augenR: 1.75 + (Math.floor(h / 11) % 2) * 0.35,
      mund: 4.4 + (Math.floor(h / 23) % 2) * 1.6,
      rundung: 6 + (Math.floor(h / 47) % 3) * 1.6,
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
      {/* The face in a group of its own: a stopped one is greyed, and its
          stop sign below must stay red (#414). */}
      <g className="gesicht-teile">
      <rect className="gesicht-kopf" x="1.5" y="1.5" width="21" height="21" rx={g.rundung} />
      {schlaeft || tot ? (
        <>
          {/* Zwei geschlossene Zustände, zwei Formen — und das ist kein
              Feinschliff, sondern der Unterschied zwischen „schläft" und
              „ist aus". Vorher waren beide ein waagerechter Strich, und ein
              Strich liest sich nicht als geschlossenes Auge, sondern als ein
              fehlendes: „ist da ein Bug?" ist genau die Frage, die ein
              Gesicht nicht auslösen darf.
   
              Der Schlaf bekommt deshalb den Bogen eines gesenkten Lids, der
              gestoppte Agent behält den Strich. Der Bogen ist das, was ein
              Mensch als zugefallenes Auge erkennt; der Strich daneben ist
              dann eindeutig etwas anderes — und die drei z darüber haben es
              nicht mehr allein zu tragen. */}
          {[12 - g.abstand, 12 + g.abstand].map((x) =>
            schlaeft ? (
              <path
                key={x}
                className="gesicht-lid"
                d={`M${x - g.augenR} ${augeY - 0.4} Q${x} ${augeY + 1.5} ${x + g.augenR} ${augeY - 0.4}`}
              />
            ) : (
              <path key={x} className="gesicht-lid" d={`M${x - g.augenR} ${augeY} h${g.augenR * 2}`} />
            ),
          )}
          {/* Der schlafende Mund: ein kleines o, das mit dem Atem geht. Der
              gestoppte hat keinen Atem, nur einen geraden Strich. */}
          {schlaeft ? (
            <circle className="gesicht-mund-o" cx="12" cy={augeY + 5} r="1.15" />
          ) : (
            <path className="gesicht-mund" d={`M${12 - g.mund / 2} ${augeY + 5} h${g.mund}`} />
          )}
        </>
      ) : (
        <>
          {/* Zwei Gruppen übereinander, und das mit Absicht: Die äußere trägt
              den Blick, die innere das eigene Umsehen (app.css). Beides auf
              derselben Gruppe hieße, dass die Animation den Blick jede
              Sekunde überschreibt. */}
          <g
            className="gesicht-blick"
            ref={blickRef}
            style={blick ? { transform: `translate(${blick.x}px, ${blick.y}px)` } : undefined}
          >
            <g className="gesicht-augen">
              <circle cx={12 - g.abstand} cy={augeY} r={g.augenR} />
              <circle cx={12 + g.abstand} cy={augeY} r={g.augenR} />
            </g>
          </g>
          {/* Der arbeitende Mund: ein Strich, der sich beim Nachdenken
              verkürzt. Kein Bogen — ein Bogen wäre ein Lächeln, und ein
              lächelnder Agent behauptet etwas über seine Laune. */}
          <path className="gesicht-mund" d={`M${12 - g.mund / 2} ${augeY + 5} h${g.mund}`} />
        </>
      )}
      </g>
      {/* Stopped (#414): the sign everybody reads as "no entry", on the
          corner. Grey eyes alone were close to asleep and easy to miss in a
          list — and a stopped colleague cannot be written to. */}
      {tot && (
        <g className="gesicht-stopp">
          <circle cx="19.5" cy="19.5" r="5.2" />
          <rect x="16.7" y="18.55" width="5.6" height="1.9" rx="0.5" />
        </g>
      )}
      {/* Drei z steigen auf, versetzt, und nur beim Schlafen. Sie liegen
          außerhalb des Rasters — das SVG darf dafür überlaufen (app.css). */}
      {schlaeft && (
        <g className="gesicht-zzz" aria-hidden="true">
          <text x="19" y="6" className="z1">z</text>
          <text x="21.5" y="1.5" className="z2">z</text>
          <text x="24" y="-2.5" className="z3">z</text>
        </g>
      )}
    </svg>
  );
}
