import { flurAn, raumAn, streu, weg, type Plan, type Punkt } from "./plan";

/* Was die Kollegen im Büro tun.
 *
 * Diese Datei kennt keine Pixel-Klassen und kein React — sie führt Buch über
 * Positionen und rechnet je Bild einen Schritt weiter. Wer sie liest, soll
 * das Verhalten verstehen können, ohne die Darstellung zu kennen; wer die
 * Darstellung ändert, soll das Verhalten nicht anfassen müssen.
 *
 * DIE TRENNUNG, die alles andere trägt:
 *
 *   Auskunft   — Zustand (schläft, arbeitet, wartet, gestoppt), der Weg zum
 *                Tresen, der leere Stuhl. Kommt aus den Daten, immer.
 *   Atmosphäre — Aufstehen, Herumgehen, Zusammenstehen, der Gang in die
 *                Teeküche, die Katze, der Kuchen, der Vogel. Kommt von hier
 *                und behauptet NICHTS, was in einer Aufzeichnung stünde.
 *
 * Eine Bewegung, aus der sich etwas ablesen ließe, das nirgends aufgezeichnet
 * ist, wäre eine Lüge mit Charme. Deshalb wacht hier niemand von selbst auf,
 * beginnt niemand von selbst einen Vorgang, und geht niemand von selbst an
 * den Tresen.
 */

export type Zustand = "schlaeft" | "arbeitet" | "frei" | "wartet" | "gestoppt";

export type Figur = {
  id: string;
  slug: string;
  /** Index des Heimatzimmers und des eigenen Platzes darin. */
  ri: number;
  i: number;
  sitz: Punkt;
  pos: Punkt;
  zustand: Zustand;
  route: Punkt[];
  /** Millisekunden, bis der nächste Einfall kommt. */
  pause: number;
  /** Schrittgeschwindigkeit in Pixeln je Sekunde, aus dem Kürzel. */
  tempo: number;
  phase: number;
  geht: boolean;
  /** Steht noch ein Rückweg aus? */
  heimkehr: boolean;
  /** Angeklickt: Wer angesprochen wird, bleibt stehen. */
  angesprochen: boolean;
  giesst: string | null;
  holt: boolean;
  tasseHolen: boolean;
  hatTasse: boolean;
  /** Blickrichtung, −1 … 1, wird von der Darstellung gelesen. */
  blick: Punkt;
};

/** Was sich sonst noch im Bau bewegt — alles selten, alles bedeutungslos. */
export type Gast =
  | { art: "katze"; id: number; pos: Punkt; route: Punkt[]; ruht: number; zi: number; schub: number }
  | { art: "blatt"; id: number; pos: Punkt; flur: number }
  | { art: "kuchen"; id: number; pos: Punkt; bis: number }
  | { art: "vogel"; id: number; pos: Punkt; bis: number }
  | { art: "flieger"; id: number; pos: Punkt; weit: number; bis: number };

export type Ereignis = "katze" | "besprechung" | "flieger" | "drucker" | "kuchen" | "vogel" | "aufmerksam";

export type Leben = ReturnType<typeof erschaffeLeben>;

/* Wie oft etwas Seltenes passiert, je Sekunde. Die Zahlen sind mit Absicht
   klein: Was jede Minute geschieht, ist keine Entdeckung mehr, sondern
   Ausstattung — und Ausstattung, die sich bewegt, ist Unruhe. */
const HAEUFIG = {
  katze: 0.0022,
  besprechung: 0.0018,
  flieger: 0.0012,
  drucker: 0.0025,
  kuchen: 0.00035,
  vogel: 0.0016,
};

export function erschaffeLeben(
  plan: Plan,
  haken: {
    /** Eine Tasse steht (oder steht nicht mehr) auf dem Tisch dieses Kollegen. */
    tasse: (id: string, da: boolean) => void;
    /** Diese Pflanze ist gegossen — sie blüht von jetzt an. */
    gegossen: (pflanze: string) => void;
    /** Die Gästeliste hat sich geändert; die Darstellung muss neu zeichnen. */
    gaeste: () => void;
    /** Alle Wachen sehen einen Moment lang in die Mitte. */
    aufmerksam: () => void;
  },
) {
  const figuren = new Map<string, Figur>();
  let gaeste: Gast[] = [];
  let naechsteId = 1;
  const durstig: { id: string; punkt: Punkt }[] = [];

  const raumVon = (f: Figur) => plan.raeume[f.ri];
  const gemIndex = (art: "besprechung" | "teekueche") =>
    plan.raeume.findIndex((r) => r.gem === art);

  /* ── Übernahme aus den Daten ────────────────────────────────────────────
     Positionen bleiben erhalten, Zustände kommen von außen. Wer neu ist,
     beginnt an seinem Platz; wer verschwunden ist, verschwindet. */
  function uebernehmen(
    stand: { id: string; slug: string; ri: number; i: number; sitz: Punkt; zustand: Zustand }[],
  ) {
    const gesehen = new Set<string>();
    for (const s of stand) {
      gesehen.add(s.id);
      const alt = figuren.get(s.id);
      if (!alt) {
        figuren.set(s.id, {
          ...s,
          pos: { ...s.sitz },
          route: [],
          pause: 1200 + (streu(s.slug) % 6000),
          tempo: 30 + (streu(s.slug + "v") % 14),
          phase: streu(s.slug) % 100,
          geht: false,
          heimkehr: false,
          angesprochen: false,
          giesst: null,
          holt: false,
          tasseHolen: false,
          hatTasse: false,
          blick: { x: 0, y: 0 },
        });
        continue;
      }
      /* Der Platz kann sich verschoben haben (neuer Kollege, neuer Plan).
         Dann rückt auch die Figur nach, aber ohne zu springen: Sie geht. */
      const umgezogen = alt.ri !== s.ri || alt.i !== s.i;
      alt.ri = s.ri;
      alt.i = s.i;
      alt.sitz = s.sitz;
      alt.slug = s.slug;
      if (umgezogen) alt.pos = { ...s.sitz };

      if (alt.zustand !== s.zustand) {
        const vorher = alt.zustand;
        alt.zustand = s.zustand;
        alt.angesprochen = false;
        if (s.zustand === "wartet") {
          /* Wer nicht weiterkommt, geht wirklich nach vorn. */
          const belegt = [...figuren.values()].filter((x) => x !== alt && x.zustand === "wartet").length;
          const ziel = plan.tresen.plaetze[Math.min(belegt, plan.tresen.plaetze.length - 1)];
          alt.route = weg(plan, alt.pos, ziel, null);
          alt.heimkehr = false;
        } else if (vorher === "wartet" || s.zustand === "arbeitet" || s.zustand === "schlaeft") {
          /* Zurück an den Platz — jeder Zustand außer „frei" gehört dorthin. */
          alt.route = weg(plan, alt.pos, { ...alt.sitz }, alt.ri);
          alt.heimkehr = false;
          alt.giesst = null;
          alt.holt = false;
        }
        if (s.zustand === "arbeitet" && alt.hatTasse) {
          alt.hatTasse = false;
          haken.tasse(alt.id, false);
        }
      }
    }
    for (const id of [...figuren.keys()]) if (!gesehen.has(id)) figuren.delete(id);
  }

  /* ── Der Schritt ────────────────────────────────────────────────────────── */
  function schritt(f: Figur, dt: number) {
    if (f.route.length) {
      f.geht = true;
      const z = f.route[0];
      const dx = z.x - f.pos.x;
      const dy = z.y - f.pos.y;
      const d = Math.hypot(dx, dy);
      const s = f.tempo * dt;
      if (d <= s) {
        f.pos.x = z.x;
        f.pos.y = z.y;
        f.route.shift();
      } else {
        f.pos.x += (dx / d) * s;
        f.pos.y += (dy / d) * s;
        f.phase += dt * 60;
        f.blick = { x: dx / d, y: dy / d };
      }
      if (!f.route.length) {
        f.geht = false;
        f.blick = { x: 0, y: 0 };
        angekommen(f);
      }
      return;
    }
    if (f.zustand !== "frei" || f.angesprochen) return;
    f.pause -= dt * 1000;
    if (f.pause > 0) return;
    einfall(f);
  }

  function angekommen(f: Figur) {
    if (f.holt) {
      const i = gaeste.findIndex((g) => g.art === "blatt");
      if (i >= 0) {
        gaeste.splice(i, 1);
        haken.gaeste();
      }
      f.holt = false;
      f.heimkehr = true;
      f.pause = 1400;
      return;
    }
    if (f.giesst) {
      haken.gegossen(f.giesst);
      f.giesst = null;
      f.heimkehr = true;
      f.pause = 1200;
      return;
    }
    if (f.tasseHolen) {
      f.tasseHolen = false;
      f.hatTasse = true;
      return;
    }
    if (f.hatTasse && Math.hypot(f.pos.x - f.sitz.x, f.pos.y - f.sitz.y) < 8) haken.tasse(f.id, true);
  }

  /* Wer wach ist und nichts zu tun hat: zurück an den Platz, zu einem
     Kollegen, irgendwo im Zimmer hin — oder in die Teeküche. Nicht mehr: Ein
     Büro, in dem alle ständig unterwegs sind, ist ein Bildschirmschoner. */
  function einfall(f: Figur) {
    const r = raumVon(f);
    if (f.heimkehr) {
      f.route = weg(plan, f.pos, { ...f.sitz }, f.ri);
      f.heimkehr = false;
      f.pause = 2600 + Math.random() * 5000;
      return;
    }
    const w = Math.random();
    if (w < 0.13) {
      const gi = gemIndex(Math.random() < 0.6 ? "teekueche" : "besprechung");
      if (gi >= 0) {
        const g = plan.raeume[gi];
        f.route = weg(plan, f.pos, g.treffpunkte[Math.floor(Math.random() * g.treffpunkte.length)], gi);
        f.heimkehr = true;
        if (g.gem === "teekueche") f.tasseHolen = true;
        f.pause = 8000 + Math.random() * 7000;
        return;
      }
    }
    const andere = [...figuren.values()].filter(
      (x) => x.ri === f.ri && x !== f && x.zustand === "frei" && !x.route.length,
    );
    if (andere.length && w < 0.4) {
      /* Zwei, die beieinanderstehen. Das ist kein Gespräch, das covey kennt —
         es ist der Unterschied zwischen einem Raum und einem Wartezimmer, in
         dem alle einzeln an ihrem Stuhl kleben. */
      const a = andere[Math.floor(Math.random() * andere.length)];
      const links = a.pos.x - r.x > r.w / 2;
      f.route = [
        {
          x: Math.min(Math.max(a.pos.x + (links ? -40 : 40), r.x + 24), r.x + r.w - 24),
          y: a.pos.y,
        },
      ];
      f.pause = 3000 + Math.random() * 6000;
      return;
    }
    if (w < 0.66 || !r.gaenge.length) {
      f.route = [{ ...f.sitz }];
      f.pause = 3000 + Math.random() * 7000;
      return;
    }
    f.route = [r.gaenge[Math.floor(Math.random() * r.gaenge.length)]];
    f.pause = 2600 + Math.random() * 6000;
  }

  /* Wer steht, steht nicht IN jemandem. Bei siebzig Figuren sind das im
     schlimmsten Fall ein paar tausend Vergleiche je Bild — und es betrifft
     nur die, die gerade nicht laufen. */
  function ausweichen(dt: number) {
    const steher = [...figuren.values()].filter(
      (f) => !f.route.length && (f.zustand === "frei" || f.zustand === "wartet"),
    );
    for (let i = 0; i < steher.length; i++)
      for (let j = i + 1; j < steher.length; j++) {
        const a = steher[i];
        const b = steher[j];
        const dx = b.pos.x - a.pos.x;
        const dy = b.pos.y - a.pos.y;
        const d = Math.hypot(dx, dy);
        if (d > 34 || d < 0.01) continue;
        const k = ((34 - d) / 2) * Math.min(1, dt * 6);
        a.pos.x -= (dx / d) * k;
        a.pos.y -= (dy / d) * k;
        b.pos.x += (dx / d) * k;
        b.pos.y += (dy / d) * k;
        halten(a);
        halten(b);
      }
  }
  /* Auch beim Ausweichen bleibt jeder in dem Raum, in dem er steht. */
  function halten(f: Figur) {
    if (f.zustand === "wartet") {
      f.pos.x = Math.min(Math.max(f.pos.x, plan.quer.x + 22), plan.quer.x + plan.quer.w - 22);
      return;
    }
    const r = plan.raeume[raumAn(plan, f.pos) ?? f.ri];
    f.pos.x = Math.min(Math.max(f.pos.x, r.x + 20), r.x + r.w - 20);
    f.pos.y = Math.min(Math.max(f.pos.y, r.y + 30), r.y + r.h - 16);
  }

  /* ── Was selten passiert ────────────────────────────────────────────────── */

  /** Wen man losschicken kann, ohne über seinen Zustand zu lügen. */
  const freieHand = () =>
    [...figuren.values()].filter(
      (f) => f.zustand === "frei" && !f.route.length && !f.giesst && !f.holt && !f.angesprochen,
    );

  function katzeLos() {
    if (gaeste.some((g) => g.art === "katze")) return;
    const warm = plan.raeume
      .map((r, i) => i)
      .filter((i) => [...figuren.values()].some((f) => f.ri === i && f.zustand === "arbeitet"));
    if (!warm.length) return;
    const zi = warm[Math.floor(Math.random() * warm.length)];
    const ziel = plan.raeume[zi];
    const arbeiter = [...figuren.values()].find((f) => f.ri === zi && f.zustand === "arbeitet");
    const platz = arbeiter
      ? { x: arbeiter.sitz.x, y: arbeiter.sitz.y + 28 }
      : { x: ziel.x + ziel.w / 2, y: ziel.y + ziel.h - 30 };
    const start = { x: -20, y: plan.flure[ziel.flur].mitte };
    gaeste.push({
      art: "katze",
      id: naechsteId++,
      pos: start,
      route: [
        { x: plan.quer.mitte, y: plan.flure[ziel.flur].mitte },
        ...weg(plan, { x: plan.quer.mitte, y: plan.flure[ziel.flur].mitte }, platz, zi),
      ],
      ruht: 0,
      zi,
      schub: 4000 + Math.random() * 8000,
    });
    haken.gaeste();
  }

  function katzeSchritt(g: Extract<Gast, { art: "katze" }>, dt: number) {
    if (g.route.length) {
      const z = g.route[0];
      const dx = z.x - g.pos.x;
      const dy = z.y - g.pos.y;
      const d = Math.hypot(dx, dy);
      const s = 48 * dt;
      if (d <= s) {
        g.pos = { ...z };
        g.route.shift();
        if (!g.route.length) g.ruht = 18000;
      } else {
        g.pos.x += (dx / d) * s;
        g.pos.y += (dy / d) * s;
      }
      return;
    }
    g.ruht -= dt * 1000;
    /* Und wenn sie eine Tasse findet, schiebt sie sie herunter. Eine Katze
       tut das; ein Zustand dieser Plattform ist es nicht. */
    if (g.schub > 0 && (g.schub -= dt * 1000) <= 0) {
      const mit = [...figuren.values()].find((f) => f.ri === g.zi && f.hatTasse);
      if (mit) {
        mit.hatTasse = false;
        haken.tasse(mit.id, false);
      }
    }
    if (g.ruht > 0) return;
    if (g.ruht > -4000) {
      /* Der Rückweg: durch die Tür hinaus und zum Eingang. */
      g.route = [
        ...weg(plan, g.pos, { x: plan.quer.mitte, y: plan.flure[plan.raeume[g.zi].flur].mitte }, null),
        { x: -30, y: plan.flure[plan.raeume[g.zi].flur].mitte },
      ];
      g.ruht = -9999;
      return;
    }
    weg_gast(g);
  }

  const weg_gast = (g: Gast) => {
    gaeste = gaeste.filter((x) => x !== g);
    haken.gaeste();
  };

  function besprechungLos() {
    const gi = gemIndex("besprechung");
    if (gi < 0) return;
    const g = plan.raeume[gi];
    const wer = freieHand()
      .filter((f) => !f.heimkehr)
      .sort(() => Math.random() - 0.5)
      .slice(0, 3 + Math.floor(Math.random() * 3));
    if (wer.length < 2) return;
    wer.forEach((f, i) => {
      f.route = weg(plan, f.pos, g.treffpunkte[i % g.treffpunkte.length], gi);
      f.heimkehr = true;
      f.pause = 14000 + Math.random() * 9000;
    });
  }

  function druckerLos() {
    if (!plan.drucker.length || gaeste.some((g) => g.art === "blatt")) return;
    const i = Math.floor(Math.random() * plan.drucker.length);
    const d = plan.drucker[i];
    gaeste.push({ art: "blatt", id: naechsteId++, pos: { x: d.blattX, y: d.blattY }, flur: i });
    haken.gaeste();
    holerSchicken();
  }

  function holerSchicken() {
    const blatt = gaeste.find((g) => g.art === "blatt") as Extract<Gast, { art: "blatt" }> | undefined;
    if (!blatt) return;
    const d = plan.drucker[blatt.flur];
    const frei = freieHand();
    if (!frei.length) return;
    const f = frei
      .map((k) => ({ k, s: Math.hypot(k.pos.x - d.x, k.pos.y - d.y) }))
      .sort((a, b) => a.s - b.s)[0].k;
    f.holt = true;
    /* Der Drucker steht im Flur. Der Weg dorthin endet an ihm, nicht am
       Tresen — deshalb der Pfad in den Quergang, ohne die letzten zwei
       Schritte, und dann quer zum Gerät. */
    const bis = weg(plan, f.pos, { x: d.x, y: d.y }, null);
    f.route = [...bis.slice(0, Math.max(1, bis.length - 2)), { x: d.x, y: d.y }];
    f.pause = 3000;
  }

  function kuchenLos() {
    const gi = gemIndex("teekueche");
    if (gi < 0 || gaeste.some((g) => g.art === "kuchen")) return;
    const g = plan.raeume[gi];
    gaeste.push({
      art: "kuchen",
      id: naechsteId++,
      pos: { x: g.x + g.w / 2 - 11, y: g.y + 20 + (g.h - 20) / 2 + 12 },
      bis: 26000,
    });
    haken.gaeste();
    freieHand()
      .filter((f) => !f.heimkehr)
      .sort(() => Math.random() - 0.5)
      .slice(0, 6)
      .forEach((f, i) => {
        f.route = weg(plan, f.pos, g.treffpunkte[i % g.treffpunkte.length], gi);
        f.heimkehr = true;
        f.tasseHolen = true;
        f.pause = 16000 + Math.random() * 9000;
      });
  }

  function vogelLos() {
    if (!plan.fenster.length || gaeste.some((g) => g.art === "vogel")) return;
    const f = plan.fenster[Math.floor(Math.random() * plan.fenster.length)];
    gaeste.push({ art: "vogel", id: naechsteId++, pos: { x: f.x + f.w / 2 - 6, y: 0 }, bis: 7000 });
    haken.gaeste();
  }

  function fliegerLos() {
    if (!plan.flure.length || gaeste.some((g) => g.art === "flieger")) return;
    const f = plan.flure[Math.floor(Math.random() * plan.flure.length)];
    gaeste.push({
      art: "flieger",
      id: naechsteId++,
      pos: { x: plan.quer.x + plan.quer.w, y: f.y + 14 },
      weit: plan.breite - plan.quer.x - plan.quer.w - 60,
      bis: 5400,
    });
    haken.gaeste();
  }

  /** Eine Pflanze anklicken: Sie merkt sich den Durst, bis jemand Zeit hat. */
  function giessen(id: string, punkt: Punkt) {
    if (durstig.some((d) => d.id === id)) return;
    durstig.push({ id, punkt });
    giessdienst();
  }
  function giessdienst() {
    if (!durstig.length) return;
    const frei = freieHand();
    if (!frei.length) return;
    const d = durstig[0];
    const zi = raumAn(plan, d.punkt);
    const f = frei
      .map((k) => ({ k, s: Math.hypot(k.pos.x - d.punkt.x, k.pos.y - d.punkt.y) }))
      .sort((a, b) => a.s - b.s)[0].k;
    durstig.shift();
    f.giesst = d.id;
    f.route = weg(plan, f.pos, { x: d.punkt.x + 26, y: d.punkt.y }, zi);
    f.pause = 3600;
  }
  const istDurstig = (id: string) => durstig.some((d) => d.id === id);

  function ausloesen(e: Ereignis) {
    if (e === "katze") katzeLos();
    else if (e === "besprechung") besprechungLos();
    else if (e === "flieger") fliegerLos();
    else if (e === "drucker") druckerLos();
    else if (e === "kuchen") kuchenLos();
    else if (e === "vogel") vogelLos();
    else if (e === "aufmerksam") haken.aufmerksam();
  }

  let seit = 0;
  function tick(dt: number) {
    for (const f of figuren.values()) schritt(f, dt);
    ausweichen(dt);

    let geaendert = false;
    for (const g of [...gaeste]) {
      if (g.art === "katze") katzeSchritt(g, dt);
      else if (g.art !== "blatt") {
        g.bis -= dt * 1000;
        if (g.bis <= 0) {
          gaeste = gaeste.filter((x) => x !== g);
          geaendert = true;
        }
      }
    }
    if (geaendert) haken.gaeste();

    seit += dt * 1000;
    if (seit < 1000) return;
    seit = 0;
    giessdienst();
    holerSchicken();
    for (const [name, p] of Object.entries(HAEUFIG)) if (Math.random() < p) ausloesen(name as Ereignis);
  }

  return {
    figuren,
    tick,
    uebernehmen,
    ausloesen,
    giessen,
    istDurstig,
    gast: () => gaeste,
    waehlen(id: string | null) {
      for (const f of figuren.values()) f.angesprochen = f.id === id;
    },
    /** Alle Wachen sehen in die Mitte — das Osterei zum Wort „covey". */
    hinsehen() {
      const mx = plan.breite / 2;
      const my = plan.hoehe / 2;
      for (const f of figuren.values()) {
        if (f.zustand === "schlaeft" || f.zustand === "gestoppt" || f.route.length) continue;
        const dx = mx - f.pos.x;
        const dy = my - f.pos.y;
        const d = Math.hypot(dx, dy) || 1;
        f.blick = { x: dx / d, y: dy / d };
      }
    },
    /** Der Zeiger: Die Wachen sehen ihm nach, solange sie stehen. */
    zeiger(p: Punkt | null) {
      for (const f of figuren.values()) {
        if (f.route.length || f.zustand === "schlaeft" || f.zustand === "gestoppt") continue;
        if (!p) {
          f.blick = { x: 0, y: 0 };
          continue;
        }
        const dx = p.x - f.pos.x;
        const dy = p.y - f.pos.y;
        const d = Math.hypot(dx, dy);
        f.blick = d < 1 ? { x: 0, y: 0 } : { x: dx / d, y: dy / d };
      }
    },
    /** Nur für die Prüfung: steht dieser Punkt im Flur? */
    imFlur: (p: Punkt) => flurAn(plan, p),
  };
}
