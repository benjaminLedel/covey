import type { Agent } from "../../api";

/* Der Bauplan des Büros.
 *
 * Hier steht ausschließlich Geometrie: wie aus einer Belegschaft ein Haus
 * wird. Nichts in dieser Datei weiß, was ein Vorgang ist, was leuchtet oder
 * wer wohin läuft — das ist der Grund, warum sie sich prüfen lässt, ohne
 * einen Browser zu starten.
 *
 * DER PLAN WIRD GERECHNET, NICHT GEZEICHNET. Eine feste Vorlage trägt
 * entweder acht Kollegen oder achtzig, nie beides: Bei acht steht sie leer,
 * bei achtzig platzt sie. Also folgt alles aus der Kopfzahl — wie viele
 * Plätze ein Zimmer nebeneinander hat, wie tief es wird, wie viele Zimmer in
 * eine Zeile passen, wie viele Flure es braucht und ob ein Quergang sie
 * verbinden muss.
 *
 * Und die WAND IST DIE MASSE. In einem Grundriss ist geschnittenes Mauerwerk
 * gefüllt, und der Raum ist das, was ausgespart bleibt; umgekehrt gezeichnet
 * — helle Kästen mit einem Rahmen — bleibt es ein Diagramm, und zwei Rahmen
 * nebeneinander ergeben eine doppelte Linie, die es in keinem Haus gibt.
 * Deshalb liefert der Plan Böden, keine Zimmerrahmen: Die Fläche darunter ist
 * durchgehend Wand, und jede Wandstärke ist der Abstand zwischen zwei Böden.
 */

export type Punkt = { x: number; y: number };
export type Gemein = "besprechung" | "teekueche";

/** Ein Platz am Schreibtisch, im Koordinatensystem des Baus. */
export type Sitz = { punkt: Punkt; agent: Agent };

export type Raum = {
  /** Abteilungs-ID, "" für „ohne Abteilung", "gem:…" für Gemeinschaftsräume. */
  id: string;
  name: string;
  farbe: string;
  /** Gesetzt, wenn dieser Raum niemandem gehört. */
  gem?: Gemein;
  leute: Agent[];
  x: number;
  y: number;
  w: number;
  h: number;
  spalten: number;
  zeilen: number;
  /** Liegt das Zimmer ÜBER seinem Flur? Davon hängt ab, wo die Tür sitzt. */
  obenDrueber: boolean;
  flur: number;
  /** Zimmernummer innerhalb des Geschosses, für das Schild. */
  nr: number;
  tuerX: number;
  /** Die Wandmitte, auf der die Tür sitzt. */
  wand: number;
  /** +1, wenn die Tür nach unten aufgeht, sonst −1. */
  ri: 1 | -1;
  /** Der Punkt an der Tür, INNEN im Zimmer. */
  innen: Punkt;
  /** Der Punkt an der Tür, im Flur. */
  aussen: Punkt;
  /** Die Laufhöhe des zugehörigen Flurs. */
  flurY: number;
  sitze: Punkt[];
  /** Wo man im Zimmer stehen darf, ohne auf einer Tischplatte zu stehen. */
  gaenge: Punkt[];
  /** Nur in Gemeinschaftsräumen: wo man sich hinstellt. */
  treffpunkte: Punkt[];
};

export type Plan = {
  breite: number;
  hoehe: number;
  flure: { y: number; h: number; mitte: number }[];
  quer: { x: number; w: number; mitte: number };
  raeume: Raum[];
  eingang: { y: number; hoehe: number };
  tresen: { y: number; plaetze: Punkt[] };
  drucker: { x: number; y: number; blattX: number; blattY: number }[];
  fenster: { x: number; y: number; w: number }[];
};

/* ── Maße ──────────────────────────────────────────────────────────────────
   Ein Platz ist 70 breit, weil darunter der Name steht und „Incident-Erst­
   responder" bei weniger in drei Punkten endet. Der Maßstab ist gesetzt: 38
   Pixel je Meter — eine Tischplatte ist damit 1,60 m breit, und ein Zimmer
   für neun kommt auf gut achtzig Quadratmeter. Die Zahl steht hier, weil das
   Schild am Zimmer sie ausweist; ein Plan ohne Maßstab ist eine Zeichnung. */
export const SITZ_B = 70;
export const SITZ_H = 88;
export const POD_LUFT = 16;
export const PAD = 13;
/* Zwei Zeilen: der Name und darunter Nummer und Fläche. Auf einer Zeile
   nebeneinander schreibt eine lange Abteilung die Nummer zu — und die Nummer
   ist die eine Angabe, die den Plan als Plan ausweist. */
export const SCHILD_H = 30;
export const AUSSEN = 9;
export const INNEN = 6;
export const FLUR_H = 66;
export const QUER_B = 108;
export const TUER_B = 42;
export const SCHWUNG = 32;
export const TUER_TIEF = 17;
export const PX_JE_METER = 38;

/** Wie viel Höhe die letzte Reihe unter dem Kopf noch braucht: Name und Platte. */
const REIHE_REST = 46;

/** Ein kleiner, stabiler Hash. Derselbe Name, dieselbe Einrichtung, immer. */
export function streu(text: string): number {
  let h = 2166136261;
  for (let i = 0; i < text.length; i++) {
    h ^= text.charCodeAt(i);
    h = Math.imul(h, 16777619);
  }
  return Math.abs(h);
}

/* Ein Zimmer soll annähernd quadratisch werden. Zwölf Plätze in einer Reihe
   sind ein Schlauch, zwölf untereinander eine Säule; √(n·1,3) trifft die
   Mitte und deckelt bei fünf, weil ein Zimmer irgendwann breiter wäre als
   die Zeile, in der es steht. */
export const spaltenFuer = (n: number) => Math.max(1, Math.min(5, n, Math.round(Math.sqrt(n * 1.3))));

/* Zwei Plätze eines Pods teilen sich die Rückkante — eine Bank. Zwischen den
   Pods bleibt ein Gang. Ein Raster aus gleich weit entfernten Einzeltischen
   liest sich als Formular, nicht als Büro. */
export const podBreite = (spalten: number) =>
  spalten * SITZ_B + (Math.ceil(spalten / 2) - 1) * POD_LUFT;

const zimmerBreite = (n: number) => Math.max(136, podBreite(spaltenFuer(n)) + PAD * 2);
const zimmerHoehe = (n: number) =>
  SCHILD_H + PAD * 2 + (Math.ceil(n / spaltenFuer(n)) - 1) * SITZ_H + REIHE_REST + 32;

/** Was in ein Zimmer gestellt wird. Siehe `ausstattungFuer`. */
export type Ausstattung = {
  wand: ("pinnwand" | "whiteboard" | "server" | "regal" | "uhr" | "schrank" | "bild")[];
  ecke: "sofa" | "sessel" | "regal" | "schrank";
  tisch: "telefon" | "zweitschirm" | "lampe" | null;
  /** Was in der Sitzecke noch steht, wenn Platz dafür ist. */
  neben: "stehlampe" | "flipchart" | "papierkorb";
};

const AUSSTATTUNGEN: Record<string, Ausstattung> = {
  support: { wand: ["pinnwand", "uhr"], ecke: "sofa", tisch: "telefon", neben: "stehlampe" },
  technik: { wand: ["whiteboard", "server"], ecke: "sessel", tisch: "zweitschirm", neben: "flipchart" },
  betrieb: { wand: ["server", "uhr"], ecke: "regal", tisch: "zweitschirm", neben: "papierkorb" },
  akten: { wand: ["schrank", "schrank"], ecke: "regal", tisch: "lampe", neben: "papierkorb" },
  werkstatt: { wand: ["whiteboard", "regal"], ecke: "sessel", tisch: "zweitschirm", neben: "flipchart" },
  aufenthalt: { wand: ["bild", "pinnwand"], ecke: "sofa", tisch: "lampe", neben: "stehlampe" },
  buero: { wand: ["pinnwand", "bild"], ecke: "regal", tisch: "lampe", neben: "stehlampe" },
};

/* Abteilungen heißen in covey, wie die Organisation sie nennt — eine feste
   Tabelle nach Namen träfe deshalb nur die eigenen Beispiele. Also zuerst ein
   Blick auf das Wort selbst, in beiden Sprachen der Oberfläche, und sonst ein
   Griff aus dem Hash: Falsch liegen kann das nicht, weil nichts davon eine
   Auskunft trägt. Ein Serverschrank in der Buchhaltung ist kein Fehler, er
   ist ein Möbelstück. */
const WORTE: [RegExp, string][] = [
  [/support|service|kunde|customer|helpdesk|ticket/i, "support"],
  [/entwickl|engineer|software|develop|platform|architek/i, "technik"],
  [/betrieb|infra|operation|ops|sre|netz|hosting/i, "betrieb"],
  [/finan|buchhalt|controll|account|rechnung|einkauf|procure/i, "akten"],
  [/recht|legal|complian|datenschutz|privacy|audit|archiv/i, "akten"],
  [/personal|people|hr|kultur|culture/i, "aufenthalt"],
  [/qualit|test|quality|forschung|research|labor|produkt|product|design/i, "werkstatt"],
];

export function ausstattungFuer(name: string): Ausstattung {
  for (const [muster, art] of WORTE) if (muster.test(name)) return AUSSTATTUNGEN[art];
  const schluessel = Object.keys(AUSSTATTUNGEN);
  return AUSSTATTUNGEN[schluessel[streu(name) % schluessel.length]];
}

export type Gruppe = { id: string; name: string; farbe: string; leute: Agent[] };

/** Die Sitzpunkte eines Zimmers, in Koordinaten des Baus. */
function sitze(x: number, y: number, w: number, spalten: number, anzahl: number): Punkt[] {
  const basis = x + (w - podBreite(spalten)) / 2;
  return Array.from({ length: anzahl }, (_, i) => {
    const sp = i % spalten;
    const pod = Math.floor(sp / 2);
    return {
      x: basis + pod * (2 * SITZ_B + POD_LUFT) + (sp % 2) * SITZ_B + SITZ_B / 2,
      y: y + SCHILD_H + PAD + Math.floor(i / spalten) * SITZ_H + 32,
    };
  });
}

/* Wo man gehen darf: in den Gängen zwischen den Bänken, am Rand und im
   Streifen unter der letzten Reihe. Ohne diese Liste liefen die Wachen über
   die Tischplatten, und ein Kollege, der über seinen Schreibtisch spaziert,
   ist die eine Bewegung, die alles andere unglaubwürdig macht. */
function gaengeVon(r: Omit<Raum, "gaenge" | "treffpunkte">): Punkt[] {
  if (r.gem) return [];
  const basis = r.x + (r.w - podBreite(r.spalten)) / 2;
  const xs = [r.x + 22, r.x + r.w - 22];
  for (let p = 1; p < Math.ceil(r.spalten / 2); p++)
    xs.push(basis + p * (2 * SITZ_B + POD_LUFT) - POD_LUFT / 2);
  const ys: number[] = [];
  for (let z = 0; z < r.zeilen; z++) ys.push(r.y + SCHILD_H + PAD + z * SITZ_H + 32);
  ys.push(r.y + r.h - 24);
  const punkte: Punkt[] = [];
  for (const x of xs) for (const y of ys) punkte.push({ x, y });
  return punkte;
}

/**
 * Baut aus Abteilungen und der verfügbaren Breite ein Haus.
 *
 * @param gruppen  Abteilungen mit ihren Kollegen, in fester Reihenfolge — ein
 *                 Büro, in dem die Zimmer von Besuch zu Besuch wandern, ist
 *                 kein Büro.
 * @param maxB     Wie breit der Bau werden darf.
 * @param namen    Die übersetzten Namen der zwei Gemeinschaftsräume.
 */
export function bauplan(
  gruppen: Gruppe[],
  maxB: number,
  namen: { besprechung: string; teekueche: string },
): Plan {
  type Zelle = { gruppe?: Gruppe; gem?: Gemein; name: string; farbe: string; w: number; h: number };

  const zellen: Zelle[] = gruppen.map((g) => ({
    gruppe: g,
    name: g.name,
    farbe: g.farbe,
    w: zimmerBreite(g.leute.length),
    h: zimmerHoehe(g.leute.length),
  }));
  /* Zwei Räume, die niemandem gehören. Sie tragen keinen Zustand — sie sind
     das, was den Flur zu einem Weg macht statt zu einem Streifen, und sie
     geben den Wachen ein Ziel außerhalb des eigenen Zimmers. */
  zellen.push({ gem: "besprechung", name: namen.besprechung, farbe: "", w: 168, h: 150 });
  zellen.push({ gem: "teekueche", name: namen.teekueche, farbe: "", w: 144, h: 150 });

  const innenB = Math.max(300, maxB - AUSSEN * 2 - QUER_B - INNEN);

  /* In Bänder füllen — aber ausgewogen. Gierig gefüllt landen alle
     Arbeitszimmer im ersten Band und die zwei Gemeinschaftsräume allein im
     letzten, wo sie sich den ganzen Rest der Zeile teilen und zu Sälen
     werden. Also erst zählen, wie viele Bänder es braucht, dann jedes auf
     denselben Sollwert füllen. */
  const gesamt = zellen.reduce((s, z) => s + z.w + INNEN, 0) - INNEN;
  const bandZahl = Math.max(1, Math.ceil(gesamt / innenB));
  const soll = gesamt / bandZahl;
  const baender: Zelle[][] = [];
  let band: Zelle[] = [];
  let breit = 0;
  for (const z of zellen) {
    if (band.length && (breit + z.w > innenB || (breit >= soll && baender.length < bandZahl - 1))) {
      baender.push(band);
      band = [];
      breit = 0;
    }
    band.push(z);
    breit += z.w + INNEN;
  }
  if (band.length) baender.push(band);

  /* Je zwei Bänder teilen sich einen Flur — ein Flur, an dem auf beiden
     Seiten Zimmer liegen, ist der einzige Grundriss, der Fläche nicht
     verschwendet. Ab dem zweiten Flur verbindet der Quergang links sie alle
     mit dem Eingang. */
  const x0 = AUSSEN + QUER_B + INNEN;
  const roh = (b: Zelle[]) => b.reduce((s, z) => s + z.w + INNEN, 0) - INNEN;
  const raeume: Raum[] = [];
  const flure: { y: number; h: number; mitte: number }[] = [];
  let y = AUSSEN;

  for (let gi = 0; gi < baender.length; gi += 2) {
    const paar = [baender[gi], baender[gi + 1]].filter(Boolean);
    /* Beide Bänder eines Paares gehören demselben Flur — und das steht fest,
       BEVOR er angelegt wird. Zeigt das untere Band auf den Flur des
       nächsten Paares, führt der Weg aus seinen Zimmern ins Leere. */
    const flurIndex = flure.length;

    const legen = (b: Zelle[], hoehe: number, obenDrueber: boolean) => {
      const rest = (innenB - roh(b)) / b.length;
      let x = x0;
      for (const z of b) {
        const w = z.w + rest;
        const spalten = z.gruppe ? spaltenFuer(z.gruppe.leute.length) : 1;
        const anzahl = z.gruppe ? z.gruppe.leute.length : 0;
        raeume.push({
          id: z.gem ? `gem:${z.gem}` : z.gruppe!.id,
          name: z.name,
          farbe: z.farbe,
          gem: z.gem,
          leute: z.gruppe?.leute ?? [],
          x,
          y,
          w,
          h: hoehe,
          spalten,
          zeilen: anzahl ? Math.ceil(anzahl / spalten) : 0,
          obenDrueber,
          flur: flurIndex,
          nr: 0,
          tuerX: x + w / 2,
          wand: 0,
          ri: obenDrueber ? 1 : -1,
          innen: { x: 0, y: 0 },
          aussen: { x: 0, y: 0 },
          flurY: 0,
          sitze: z.gruppe ? sitze(x, y, w, spalten, anzahl) : [],
          gaenge: [],
          treffpunkte: [],
        });
        x += w + INNEN;
      }
      y += hoehe;
    };

    legen(paar[0], Math.max(...paar[0].map((z) => z.h)), true);
    flure.push({ y: y + INNEN, h: FLUR_H, mitte: y + INNEN + 40 });
    y += INNEN + FLUR_H + INNEN;
    if (paar[1]) {
      legen(paar[1], Math.max(...paar[1].map((z) => z.h)), false);
      y += INNEN;
    }
  }

  const hoehe = y - INNEN + AUSSEN;
  const breite = AUSSEN * 2 + QUER_B + INNEN + innenB;
  const quer = { x: AUSSEN, w: QUER_B, mitte: AUSSEN + QUER_B / 2 };

  /* Türen: der Punkt INNEN im Zimmer und der Punkt DRAUSSEN im Flur, getrennt.
     Genau hier lag der Fehler des ersten Anlaufs — ein Weg, dessen erster
     Punkt schon im Flur liegt, schneidet auf der Geraden dorthin die Wand. */
  const zaehler: Record<number, number> = {};
  for (const r of raeume) {
    const f = flure[r.flur];
    r.wand = r.obenDrueber ? r.y + r.h : r.y;
    r.flurY = f.mitte;
    r.innen = { x: r.tuerX, y: r.wand - r.ri * TUER_TIEF };
    r.aussen = { x: r.tuerX, y: r.wand + r.ri * (INNEN + TUER_TIEF) };
    zaehler[r.flur] = (zaehler[r.flur] ?? 0) + 1;
    r.nr = zaehler[r.flur];
    r.gaenge = gaengeVon(r);
    if (r.gem) {
      const cx = r.x + r.w / 2;
      const cy = r.y + SCHILD_H + (r.h - SCHILD_H) / 2;
      r.treffpunkte = [
        { x: cx - 46, y: cy + 26 },
        { x: cx + 46, y: cy + 26 },
        { x: cx, y: cy + 34 },
        { x: cx - 46, y: cy - 26 },
        { x: cx + 46, y: cy - 26 },
        { x: cx, y: cy - 34 },
      ];
    }
  }

  /* Der Eingang liegt oben links in der Außenwand, der Tresen im Quergang
     dahinter: die Stelle, an der man vorbeikommt, nicht eine, die man
     aufsucht. Wer auf eine Entscheidung wartet, steht dort. */
  const eingangY = AUSSEN + 34;
  const tresenY = eingangY + 86;

  return {
    breite,
    hoehe,
    flure,
    quer,
    raeume,
    eingang: { y: eingangY, hoehe: 46 },
    tresen: {
      y: tresenY,
      plaetze: Array.from({ length: 6 }, (_, i) => ({ x: quer.mitte, y: tresenY + 74 + i * 40 })),
    },
    drucker: flure.map((f) => ({
      x: quer.x + quer.w + 81,
      y: f.mitte,
      blattX: quer.x + quer.w + 74,
      blattY: f.y + f.h - 36,
    })),
    fenster: ([
      [quer.x + 180, 130],
      [quer.x + 400, 100],
      [breite - 280, 160],
    ] as [number, number][])
      .filter(([x, w]) => x + w < breite - AUSSEN)
      .map(([x, w]) => ({ x, y: 0, w })),
  };
}

/** In welchem Zimmer liegt dieser Punkt — oder in keinem (Flur, Quergang)? */
export function raumAn(plan: Plan, p: Punkt): number | null {
  for (let i = 0; i < plan.raeume.length; i++) {
    const r = plan.raeume[i];
    if (p.x >= r.x && p.x <= r.x + r.w && p.y >= r.y && p.y <= r.y + r.h) return i;
  }
  return null;
}

/** In welchem Flur — oder im Quergang (null)? */
export function flurAn(plan: Plan, p: Punkt): number | null {
  if (p.x < plan.quer.x + plan.quer.w) return null;
  for (let i = 0; i < plan.flure.length; i++) {
    const f = plan.flure[i];
    if (p.y >= f.y - 2 && p.y <= f.y + f.h + 2) return i;
  }
  return null;
}

/* ── Wege ──────────────────────────────────────────────────────────────────
   Niemand geht durch eine Wand. Hinaus: erst an die Tür VON INNEN, dann durch
   die Schwelle nach draußen, dann erst den Flur entlang. Hinein in
   umgekehrter Reihenfolge. Zwischen zwei Fluren über den Quergang.

   Das ist der ganze Wegfinder, und mehr braucht ein Haus aus Rechtecken auch
   nicht: Jeder Raum ist konvex, also ist die Gerade zwischen zwei Punkten
   desselben Raums immer frei, und die einzigen Übergänge sind Türen. */
const raus = (r: Raum): Punkt[] => [{ ...r.innen }, { ...r.aussen }, { x: r.tuerX, y: r.flurY }];
const rein = (r: Raum, ziel: Punkt): Punkt[] => [
  { x: r.tuerX, y: r.flurY },
  { ...r.aussen },
  { ...r.innen },
  ziel,
];

/**
 * Der Weg von `von` nach `ziel`.
 *
 * @param zielRaum Index des Zielzimmers, oder `null` für den Quergang (Tresen).
 *
 * Gerechnet wird ab dem Raum, in dem der Punkt WIRKLICH liegt — nicht ab dem
 * Heimatzimmer. Wer in der Teeküche steht, muss durch die Küchentür hinaus,
 * nicht durch seine eigene.
 */
export function weg(plan: Plan, von: Punkt, ziel: Punkt, zielRaum: number | null): Punkt[] {
  const vonRaum = raumAn(plan, von);
  if (vonRaum != null && vonRaum === zielRaum) return [ziel];

  const pfad: Punkt[] = [];
  let flurY: number | null = null;
  if (vonRaum != null) {
    pfad.push(...raus(plan.raeume[vonRaum]));
    flurY = plan.raeume[vonRaum].flurY;
  } else {
    const fi = flurAn(plan, von);
    if (fi != null) {
      flurY = plan.flure[fi].mitte;
      pfad.push({ x: von.x, y: flurY });
    }
  }

  if (zielRaum == null) {
    if (flurY != null) pfad.push({ x: plan.quer.mitte, y: flurY });
    pfad.push({ x: plan.quer.mitte, y: ziel.y }, ziel);
    return pfad;
  }

  const b = plan.raeume[zielRaum];
  if (flurY == null) pfad.push({ x: plan.quer.mitte, y: b.flurY });
  else if (Math.abs(flurY - b.flurY) > 1)
    pfad.push({ x: plan.quer.mitte, y: flurY }, { x: plan.quer.mitte, y: b.flurY });
  pfad.push(...rein(b, ziel));
  return pfad;
}
