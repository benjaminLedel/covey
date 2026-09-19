/* Das Beiwerk, gezeichnet.
 *
 * Jedes Möbelstück ist ein Pfad in seinem eigenen kleinen Raster. Gezeichnet
 * und nicht aus einer Schrift geliehen: Ein Zeichen aus dem Unicode-Vorrat
 * trägt die Strichstärke SEINER Schrift, nicht die dieses Plans, und in einer
 * Reihe mit gezeichneten Türschwüngen fällt das sofort auf.
 *
 * NICHTS HIER TRÄGT AUSKUNFT. Das ist keine Nachlässigkeit, sondern die
 * Bedingung: Ein Aktenschrank, aus dem sich etwas ablesen ließe, das in
 * keiner Aufzeichnung steht, wäre eine Lüge mit Charme. Was Zustand trägt —
 * Bildschirm, Zimmerlicht, Lidbogen, Weg zum Tresen, leerer Stuhl — steht in
 * Buero.tsx und nicht hier.
 */

export type Art =
  | "pflanze"
  | "monstera"
  | "schrank"
  | "regal"
  | "server"
  | "pinnwand"
  | "whiteboard"
  | "uhr"
  | "sofa"
  | "sessel"
  | "teppich"
  | "telefon"
  | "zweitschirm"
  | "lampe"
  | "papierkorb"
  | "tasse"
  | "mappe"
  | "drucker"
  | "spender"
  | "garderobe"
  | "giesskanne"
  | "feuerloescher"
  | "flipchart"
  | "kaffeemaschine"
  | "stehlampe"
  | "bild"
  | "tastatur"
  | "katze"
  | "flieger"
  | "paket"
  | "kuchen"
  | "blatt"
  | "vogel";

/* `voll` nennt die Pfade, die eine Fläche sind und keine Kante — sie bekommen
   eine Füllung aus demselben Ton. Reine Umrisse lasen sich als Drahtmodell:
   Ein Sofa ohne Sitzfläche ist ein Rechteck mit einer Linie darin. */
type Bild = { vb: string; d: string[]; voll?: number[] };

const BILDER: Record<Art, Bild> = {
  /* Eine Topfpflanze von oben: Stiel, zwei Blätter, Topf. */
  pflanze: {
    vb: "0 0 24 30",
    d: [
      "M12 22V13",
      "M12 15c-5-1-7-5-6.5-9C9 6.5 11.5 10 12 15Z",
      "M12 17c5-1.5 6.5-5.5 6-9.5-3.5.5-6 4-6 9.5Z",
      "M7 22h10l-1.2 6.5a1 1 0 0 1-1 .5H9.2a1 1 0 0 1-1-.5Z",
    ],
    voll: [1, 2, 3],
  },
  /* Die große: ein runder Topf und drei Blätter, die darüber hinausragen. */
  monstera: {
    vb: "0 0 26 26",
    d: [
      "M17 17.5a4 4 0 1 1-8 0 4 4 0 0 1 8 0Z",
      "M12.5 13.5c-1.5-4-5-6-9-5 .5 4.5 4 7 9 5Z",
      "M13.5 13c2.5-3.5 2.5-7.5 0-10.5-2.5 3-2.5 7 0 10.5Z",
      "M14 14.5c4-.5 7-3.5 7.5-7.5-4 .5-7 3.5-7.5 7.5Z",
    ],
    voll: [0, 1, 2, 3],
  },
  schrank: { vb: "0 0 34 13", d: ["M.6 .6h32.8v11.8H.6Z", "M17 .6v11.8", "M8 7h4", "M22 7h4"], voll: [0] },
  /* Ein Regal: Fächer, und in zweien stehen Ordner. */
  regal: {
    vb: "0 0 40 12",
    d: [
      "M.6 .6h38.8v10.8H.6Z",
      "M13.7 .6v10.8",
      "M26.3 .6v10.8",
      "M3 3.4v5.6M5 3.4v5.6M7 3.4v5.6",
      "M29 3.4v5.6M31 3.4v5.6",
    ],
    voll: [0],
  },
  server: {
    vb: "0 0 16 31",
    d: ["M.7 .7h14.6v29.6H.7Z", "M3 5h10", "M3 10h10", "M3 15h10", "M3 20h10", "M3 25h10"],
    voll: [0],
  },
  pinnwand: {
    vb: "0 0 34 8",
    d: ["M.7 .7h32.6v6.6H.7Z", "M6 4h.01", "M14 2.6h.01", "M21 5h.01", "M28 3.4h.01"],
    voll: [0],
  },
  whiteboard: { vb: "0 0 45 8", d: ["M.7 .7h43.6v6.6H.7Z", "M6 4h16", "M6 5.8h9"], voll: [0] },
  uhr: { vb: "0 0 24 14", d: ["M12 1.2a5.8 5.8 0 1 1 0 11.6 5.8 5.8 0 0 1 0-11.6Z", "M12 4v3l2.4 1.6"], voll: [0] },
  sofa: {
    vb: "0 0 44 24",
    d: [
      "M2 7a4.4 4.4 0 0 1 4.4-4.4h31.2A4.4 4.4 0 0 1 42 7v12.6a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2Z",
      "M2 8.6h40",
      "M22 8.6v13",
    ],
    voll: [0],
  },
  sessel: {
    vb: "0 0 22 22",
    d: ["M2 7a4 4 0 0 1 4-4h10a4 4 0 0 1 4 4v10.6a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2Z", "M2 8.4h20"],
    voll: [0],
  },
  teppich: {
    vb: "0 0 70 46",
    d: [
      "M3 .7h64a2.3 2.3 0 0 1 2.3 2.3v40a2.3 2.3 0 0 1-2.3 2.3H3A2.3 2.3 0 0 1 .7 43V3A2.3 2.3 0 0 1 3 .7Z",
      "M5.5 5.5h59v34h-59Z",
    ],
  },
  telefon: { vb: "0 0 12 9", d: ["M.7 1.7h10.6v6.6H.7Z", "M2.6 .8c1.4-.8 4.4-.8 5.8 0"], voll: [0] },
  zweitschirm: { vb: "0 0 14 7", d: ["M.7 .7h12.6v5.6H.7Z"], voll: [0] },
  lampe: { vb: "0 0 12 12", d: ["M6 11V5", "M2.4 5 6 .9 9.6 5Z", "M3 11.4h6"], voll: [1] },
  papierkorb: { vb: "0 0 10 10", d: ["M1.4 1.6h7.2l-.9 7a1 1 0 0 1-1 .8H3.3a1 1 0 0 1-1-.8Z"], voll: [0] },
  tasse: { vb: "0 0 14 12", d: ["M2 3h8v5a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2Z", "M10 4.5h1.5a1.5 1.5 0 0 1 0 3H10"], voll: [0] },
  mappe: { vb: "0 0 14 11", d: ["M4 1.5h9v6.5", "M1.5 2.5h10v7.5h-10Z"], voll: [1] },
  drucker: { vb: "0 0 24 18", d: ["M6 0h12v4H6Z", "M2 4h20v9H2Z", "M6 13h12v5H6Z", "M18 7h2"], voll: [1] },
  spender: { vb: "0 0 18 21", d: ["M4 6h10v12a3 3 0 0 1-3 3H7a3 3 0 0 1-3-3Z", "M5 0h8l-1 6H6Z"], voll: [0, 1] },
  garderobe: { vb: "0 0 30 7", d: ["M1 2h28", "M5 2v4.4", "M12 2v4.4", "M19 2v4.4", "M26 2v4.4"] },
  giesskanne: { vb: "0 0 16 12", d: ["M2 4h7v6a1.6 1.6 0 0 1-1.6 1.6H3.6A1.6 1.6 0 0 1 2 10Z", "M9 5.4 15 2", "M3.4 4V2.4h4.2V4"] },
  /* Der Feuerlöscher an der Flurwand — in einem Plan steht er immer irgendwo,
     und man sieht ihn erst, wenn man ihn sucht. */
  feuerloescher: { vb: "0 0 9 14", d: ["M1.6 3.4h5.8v9.2a1 1 0 0 1-1 1H2.6a1 1 0 0 1-1-1Z", "M3.2 3.4V1.4h2.6v2", "M7.4 5.4h1.2"], voll: [0] },
  flipchart: { vb: "0 0 22 16", d: ["M2.6 .7h16.8v9.6H2.6Z", "M11 10.3v5", "M4 15.3 11 10.3l7 5", "M6 4h8", "M6 6.6h5"], voll: [0] },
  kaffeemaschine: { vb: "0 0 14 16", d: ["M1.6 .7h10.8v6.6H1.6Z", "M3.6 7.3v4.4h6.8V7.3", "M2 15.3h10", "M5 11.7v3.6", "M9 11.7v3.6"], voll: [0] },
  stehlampe: { vb: "0 0 14 16", d: ["M3.4 5 7 .9 10.6 5Z", "M7 5v9.4", "M3.6 15.3h6.8"], voll: [0] },
  bild: { vb: "0 0 18 13", d: ["M.7 .7h16.6v11.6H.7Z", "M3.4 9.4 7 5.4l2.6 2.8L12 6l2.6 3.4Z"], voll: [0, 1] },
  /* Die Tastatur: der Strich, an dem man einen Schreibtisch erkennt. */
  tastatur: { vb: "0 0 22 6", d: ["M.7 .7h20.6v4.6H.7Z", "M4 3h14"], voll: [0] },
  /* Die Katze von oben: Rücken, zwei Ohren, ein Schwanz. */
  katze: {
    vb: "0 0 22 26",
    d: ["M11 7a5 7 0 0 1 0 14 5 7 0 0 1 0-14Z", "M7.5 8.5 6 4.5l3.5 2Z", "M14.5 8.5 16 4.5l-3.5 2Z", "M11 21c0 4 3 5 6 4"],
    voll: [0, 1, 2],
  },
  flieger: { vb: "0 0 22 12", d: ["M0 6 22 0l-8 12-3-5Z", "M11 7 22 0"], voll: [0] },
  paket: { vb: "0 0 18 18", d: ["M1 3.4h16v13.2H1Z", "M9 3.4v13.2", "M1 8.2h16"], voll: [0] },
  kuchen: { vb: "0 0 22 20", d: ["M11 3.4a7.6 7.6 0 1 1 0 15.2 7.6 7.6 0 0 1 0-15.2Z", "M11 11 17 7.6", "M11 11v7.6", "M11 3.4V.8"], voll: [0] },
  blatt: { vb: "0 0 10 13", d: ["M.7 .7h8.6v11.6H.7Z", "M2.6 4h4.8", "M2.6 6.4h4.8", "M2.6 8.8h3"], voll: [0] },
  vogel: { vb: "0 0 13 12", d: ["M3 6a3 3 0 0 1 6 0c0 2-1.5 3.4-3 3.4S3 8 3 6Z", "M9 5.2 12 4", "M3.4 5 .6 3.4", "M6 9.4v1.6"], voll: [0] },
};

/** Die Maße, in denen ein Stück gezeichnet wird — ohne sie steht jedes an vier Orten. */
export const MASS: Partial<Record<Art, [number, number]>> = {
  pflanze: [20, 25],
  monstera: [26, 26],
  schrank: [34, 13],
  regal: [40, 12],
  server: [16, 31],
  pinnwand: [34, 8],
  whiteboard: [45, 8],
  uhr: [24, 14],
  sofa: [44, 24],
  sessel: [22, 22],
  teppich: [70, 46],
  telefon: [12, 9],
  zweitschirm: [14, 7],
  lampe: [12, 12],
  papierkorb: [10, 10],
  feuerloescher: [9, 14],
  flipchart: [22, 16],
  kaffeemaschine: [14, 16],
  stehlampe: [14, 16],
  bild: [18, 13],
  tastatur: [22, 6],
};

export default function Riss({
  art,
  x,
  y,
  w,
  h,
  className = "",
  onClick,
  titel,
}: {
  art: Art;
  x: number;
  y: number;
  w?: number;
  h?: number;
  className?: string;
  onClick?: () => void;
  titel?: string;
}) {
  const bild = BILDER[art];
  const [mw, mh] = MASS[art] ?? [w ?? 20, h ?? 20];
  const breite = w ?? mw;
  const hoehe = h ?? mh;
  const gemeinsam = {
    className: `bu-riss-stueck r-${art} ${className}`.trim(),
    viewBox: bild.vb,
    width: breite,
    height: hoehe,
    style: { left: x, top: y },
  };
  const inhalt = bild.d.map((d, i) => <path key={i} d={d} className={bild.voll?.includes(i) ? "voll" : undefined} />);

  /* Anklickbar ist genau ein Stück — die Pflanze. Dann muss es auch ein
     Knopf sein: Ein <svg> mit einem Klickhörer erreicht die Tastatur nicht. */
  if (!onClick) {
    return (
      <svg {...gemeinsam} aria-hidden="true">
        {inhalt}
      </svg>
    );
  }
  return (
    <button type="button" className="bu-riss-knopf" style={{ left: x, top: y }} onClick={onClick} title={titel}>
      <svg {...gemeinsam} style={undefined} aria-hidden="true">
        {inhalt}
      </svg>
      <span className="bu-nur-lesegeraet">{titel}</span>
    </button>
  );
}
