import { freiNahe, istFrei, laufZufall, wegImRaum, type LaufFelder } from "./laufweg";
import { hash } from "./mathe";
import { raumAn, weg } from "./plan";
import type { Gemein, Plan, Punkt, Zustand } from "./typen";

export type { Zustand } from "./typen";

/* What the colleagues do in the office.
 *
 * This file knows no scene, no DOM and no React — it keeps the books on
 * positions and computes one step further per frame. Whoever reads it should
 * understand the behaviour without knowing the rendering; whoever changes the
 * rendering should not have to touch the behaviour. Positions are plan
 * coordinates in centimetres.
 *
 * THE SEPARATION that carries everything else:
 *
 *   Information — the state (asleep, working, waiting, stopped), the walk to
 *                 the front desk, the empty chair. Comes from the data, always.
 *   Atmosphere  — getting up, walking about, standing together, the trip to
 *                 the kitchen, the cat, the cake, the bird. Comes from here and
 *                 claims NOTHING that would be in a recording.
 *
 * A movement from which one could read something that is recorded nowhere
 * would be a lie with charm. That is why nobody here wakes up on their own,
 * nobody starts a task on their own, and nobody goes to the front desk on
 * their own: `zustandSetzen` is the only way into "wartet", and it is called
 * with what the data says.
 *
 * Paths inside rooms go around the furniture that was actually built (the
 * grids from ./laufweg); on the corridors they follow the centre lines of the
 * plan. After every rebuild of the scene the grids are new — `felderSetzen` —
 * and the figures keep where they are and where they were going.
 */

export type Figur = {
  id: string;
  slug: string;
  /** Index of the home room and of the own seat in it. */
  ri: number;
  i: number;
  sitz: Punkt;
  pos: Punkt;
  zustand: Zustand;
  /** The current step from the data ("braucht eine Entscheidung"), if any. */
  schritt?: string | null;
  route: Punkt[];
  /** Milliseconds until the next idea comes. */
  pause: number;
  /** Walking speed in cm per second, from the slug. */
  tempo: number;
  phase: number;
  geht: boolean;
  /** Is a way back home still due? */
  heimkehr: boolean;
  /** Clicked: whoever is being spoken to stops. */
  angesprochen: boolean;
  /** The plant this figure is on the way to water. */
  giesst: string | null;
  /** Fetching a printout. The plan has no printer any more, so this stays
   *  false; it is kept so the rendering's shape does not change (see Gast). */
  holt: boolean;
  tasseHolen: boolean;
  hatTasse: boolean;
  /** Gaze direction, −1 … 1, read by the rendering. */
  blick: Punkt;
};

/** What else moves in the building — all of it rare, all of it meaningless.
 *
 *  Dropped from the flat office: the printout ("blatt") and whoever fetched
 *  it — the new plan has no printer to put it on. The bird no longer sits on
 *  a window from the plan (the plan has no windows any more; the scene draws
 *  them): it lands on the outer wall of a room in the top row. */
export type Gast =
  | { art: "katze"; id: number; pos: Punkt; route: Punkt[]; ruht: number; zi: number; schub: number }
  | { art: "kuchen"; id: number; pos: Punkt; bis: number }
  | { art: "vogel"; id: number; pos: Punkt; bis: number }
  | { art: "flieger"; id: number; pos: Punkt; weit: number; bis: number };

export type Ereignis = "katze" | "besprechung" | "flieger" | "kuchen" | "vogel" | "aufmerksam";

/** Who waters a clicked plant: a colleague who has nothing to do, or — if
 *  nobody is free — the visitor's own watering can. */
export type Giesser = "kollege" | "selbst";

/** What the rendering wants to hear about. All optional. */
export type Haken = {
  /** A cup is (or no longer is) on this colleague's desk. */
  tasse?: (id: string, da: boolean) => void;
  /** This plant is watered — it blooms from now on. */
  gegossen?: (pflanze: string, wer: Giesser) => void;
  /** The guest list changed; the rendering has to redraw. */
  gaeste?: () => void;
  /** A plant now waits for water (or no longer does). */
  durst?: () => void;
  /** All awake colleagues look to the middle for a moment. */
  aufmerksam?: () => void;
};

export type Kollege = { id: string; slug: string; ri: number; i: number; zustand: Zustand; schritt?: string | null };

export type Leben = {
  figuren: Figur[];
  /** Live list: mutated in place, never replaced. */
  gaeste: Gast[];
  tick(dtSekunden: number): void;
  /** From the data. A change re-routes: waiting → the queue at the front
   *  desk; any other state than "frei" → back to the seat. */
  zustandSetzen(id: string, zustand: Zustand, schritt?: string | null): void;
  /** With `an`: this one is (no longer) spoken to. Without: the selection —
   *  exactly this one is spoken to, everyone else released (null: nobody). */
  ansprechen(id: string | null, an?: boolean): void;
  /** A click on a plant. "kollege": someone free is on the way now.
   *  "selbst": nobody is free right now — the plant waits up to three seconds
   *  for someone to become free, then it is the visitor's can. Either way
   *  `haken.gegossen` says when and by whom it was watered. */
  giessen(pflanzeId: string, punkt: Punkt): Giesser;
  istDurstig(pflanzeId: string): boolean;
  /** After a rebuild the grids are new; figures keep pos and route. */
  felderSetzen(felder: LaufFelder): void;
  /** The pointer: the awake ones look after it while they stand. */
  blickAuf(zeiger: Punkt | null): void;
  /** All awake ones look to the middle — the easter egg for the word "covey". */
  hinsehen(): void;
  ausloesen(e: Ereignis): void;
};

/* How often something rare happens, per second. The numbers are small on
   purpose: what happens every minute is no discovery any more but fittings —
   and fittings that move are unrest. */
const HAEUFIG: Partial<Record<Ereignis, number>> = {
  katze: 0.0022,
  besprechung: 0.0018,
  flieger: 0.0012,
  kuchen: 0.00035,
  vogel: 0.0016,
};

/** How long a clicked plant waits for a free colleague before the visitor's
 *  own can does it. */
const GIESS_GEDULD = 3000;
/** Two standing figures closer than this (cm) step apart. */
const ABSTAND = 30;

export function erschaffeLeben(
  plan: Plan,
  felder: LaufFelder,
  kollegen: Kollege[],
  ruhig: boolean,
  haken: Haken = {},
): Leben {
  let raster = felder;
  const imRaum = (ri: number, von: Punkt, ziel: Punkt) => wegImRaum(raster, ri, von, ziel);
  const route = (von: Punkt, ziel: Punkt, zielRaum: number | null) => weg(plan, von, ziel, zielRaum, imRaum);

  const figuren: Figur[] = [];
  const gaeste: Gast[] = [];
  let naechsteId = 1;
  const durstig: { id: string; punkt: Punkt; seit: number }[] = [];
  /** The queue at the front desk, in order of arrival. */
  const schlange: string[] = [];

  const gaesteGeaendert = () => haken.gaeste?.();
  const zufall = <T>(liste: readonly T[]): T => liste[Math.floor(Math.random() * liste.length)];

  /* With reduced motion nobody walks: a route is taken in one step. The
     information stays — whoever waits still stands at the front desk. */
  function losschicken(f: Figur, weg: Punkt[]) {
    f.route = weg;
    if (ruhig && weg.length) {
      f.pos = { ...weg[weg.length - 1] };
      f.route = [];
    }
  }

  const heimweg = (f: Figur) => route(f.pos, { ...f.sitz }, f.ri);

  function warteplatz(id: string): Punkt {
    const n = Math.max(0, schlange.indexOf(id));
    const plaetze = plan.tresen.plaetze;
    if (!plaetze.length) return { x: plan.quer.mitte, y: plan.tresen.y + 90 };
    return { ...plaetze[Math.min(n, plaetze.length - 1)] };
  }

  /* ── From the data ──────────────────────────────────────────────────────
     Whoever starts out waiting stands in the queue already; everybody else at
     the seat. Nobody walks in on the first frame. */
  for (const k of kollegen) {
    const s = plan.raeume[k.ri]?.sitze[k.i];
    if (!s) continue;
    const f: Figur = {
      id: k.id,
      slug: k.slug,
      ri: k.ri,
      i: k.i,
      sitz: { x: s.x, y: s.y },
      pos: { x: s.x, y: s.y },
      zustand: k.zustand,
      schritt: k.schritt ?? null,
      route: [],
      pause: 1200 + (hash(k.slug) % 6000),
      tempo: 42 + (hash(k.slug + "v") % 18),
      phase: hash(k.slug) % 100,
      geht: false,
      heimkehr: false,
      angesprochen: false,
      giesst: null,
      holt: false,
      tasseHolen: false,
      hatTasse: false,
      blick: { x: 0, y: 0 },
    };
    if (k.zustand === "wartet") {
      schlange.push(f.id);
      f.pos = warteplatz(f.id);
    }
    figuren.push(f);
  }

  /* Whoever stands behind someone who left moves up. */
  function aufruecken() {
    for (const id of schlange) {
      const f = figuren.find((x) => x.id === id);
      if (!f) continue;
      const ziel = warteplatz(id);
      const jetzt = f.route.length ? f.route[f.route.length - 1] : f.pos;
      if (Math.hypot(jetzt.x - ziel.x, jetzt.y - ziel.y) < 1) continue;
      losschicken(f, route(f.pos, ziel, null));
    }
  }

  function zustandSetzen(id: string, zustand: Zustand, schritt?: string | null) {
    const f = figuren.find((x) => x.id === id);
    if (!f) return;
    if (schritt !== undefined) f.schritt = schritt;
    if (f.zustand === zustand) return;
    const vorher = f.zustand;
    f.zustand = zustand;
    f.angesprochen = false;
    f.heimkehr = false;
    f.giesst = null;
    f.holt = false;
    f.tasseHolen = false;
    if (zustand === "wartet") {
      /* Whoever cannot go on really walks to the front: out of the door,
         along the corridor, into the queue. */
      if (!schlange.includes(id)) schlange.push(id);
      losschicken(f, route(f.pos, warteplatz(id), null));
    } else {
      if (vorher === "wartet") {
        schlange.splice(schlange.indexOf(id), 1);
        aufruecken();
      }
      if (zustand === "frei") {
        /* A task ended: the colleague stays awake and will get up shortly. */
        f.pause = 400;
        if (vorher === "wartet") losschicken(f, heimweg(f));
      } else {
        /* Every state except "frei" belongs at the seat — the decided one
           walks the same way back, and the screen comes on there. */
        losschicken(f, heimweg(f));
      }
    }
    if (zustand === "arbeitet" && f.hatTasse) {
      f.hatTasse = false;
      haken.tasse?.(f.id, false);
    }
  }

  /* ── The step ───────────────────────────────────────────────────────────── */
  function schritt(f: Figur, dt: number) {
    if (f.route.length) {
      /* Being spoken to stops the stroll — not the walk the data asked for:
         whoever waits for a decision still gets to the front desk. */
      if (f.angesprochen && f.zustand === "frei") {
        f.geht = false;
        return;
      }
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
    if (f.giesst) {
      haken.gegossen?.(f.giesst, "kollege");
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
    if (f.hatTasse && Math.hypot(f.pos.x - f.sitz.x, f.pos.y - f.sitz.y) < 8) haken.tasse?.(f.id, true);
  }

  const gemRaeume = (art: Gemein) => plan.raeume.map((r, i) => (r.gem === art ? i : -1)).filter((i) => i >= 0);

  /** A place to stand in a shared room: one of its meeting points, pulled
   *  onto free floor; without meeting points anywhere free in it. */
  function treffpunkt(gi: number, n?: number): Punkt | null {
    const g = plan.raeume[gi];
    if (g.treff.length) return freiNahe(raster, gi, n == null ? zufall(g.treff) : g.treff[n % g.treff.length]);
    return laufZufall(raster, gi);
  }

  /* Whoever is awake and has nothing to do: back to the seat, over to a
     colleague, somewhere in the room — or to the kitchen, the meeting room or
     a lounge. No more: an office in which everyone is always on the move is a
     screensaver. */
  function einfall(f: Figur) {
    if (f.heimkehr) {
      losschicken(f, heimweg(f));
      f.heimkehr = false;
      f.pause = 3000 + Math.random() * 5000;
      return;
    }
    const w = Math.random();
    if (w < 0.16) {
      /* Where to: kitchen, meeting room or one of the lounges — with several
         lounges not always the same one, or the first stands full and the
         second empty. */
      const art: Gemein = Math.random() < 0.45 ? "kueche" : Math.random() < 0.4 ? "besprechung" : "lounge";
      const ziele = gemRaeume(art);
      const gi = ziele.length ? zufall(ziele) : -1;
      const hin = gi >= 0 ? treffpunkt(gi) : null;
      if (hin) {
        losschicken(f, route(f.pos, hin, gi));
        f.heimkehr = true;
        if (art === "kueche") f.tasseHolen = true;
        f.pause = 9000 + Math.random() * 7000;
        return;
      }
    }
    const andere = figuren.filter(
      (x) => x !== f && x.ri === f.ri && x.zustand === "frei" && !x.route.length && raumAn(plan, x.pos) === f.ri,
    );
    if (andere.length && w < 0.4) {
      /* Two standing together. That is no conversation covey knows about —
         it is the difference between a room and a waiting room in which
         everyone sticks to their own chair. */
      const a = zufall(andere);
      const r = plan.raeume[f.ri];
      const links = a.pos.x - r.x > r.w / 2;
      const hin = freiNahe(raster, f.ri, { x: a.pos.x + (links ? -40 : 40), y: a.pos.y });
      losschicken(f, route(f.pos, hin, f.ri));
      f.pause = 3000 + Math.random() * 6000;
      return;
    }
    const hin = w < 0.62 ? null : laufZufall(raster, f.ri);
    losschicken(f, hin ? route(f.pos, hin, f.ri) : heimweg(f));
    f.pause = hin ? 2600 + Math.random() * 6000 : 3000 + Math.random() * 6000;
  }

  /* Whoever stands does not stand INSIDE someone. With seventy figures that
     is a few thousand comparisons per frame at worst — and it only concerns
     those who are standing, not sitting and not walking. A push that would
     move someone into furniture or out of the corridor is not made. */
  function ausweichen(dt: number) {
    const steher = figuren.filter(
      (f) =>
        !f.route.length &&
        (f.zustand === "frei" || f.zustand === "wartet") &&
        Math.hypot(f.pos.x - f.sitz.x, f.pos.y - f.sitz.y) > 4,
    );
    for (let i = 0; i < steher.length; i++)
      for (let j = i + 1; j < steher.length; j++) {
        const a = steher[i];
        const b = steher[j];
        const dx = b.pos.x - a.pos.x;
        const dy = b.pos.y - a.pos.y;
        const d = Math.hypot(dx, dy);
        if (d > ABSTAND || d < 0.01) continue;
        const k = ((ABSTAND - d) / 2) * Math.min(1, dt * 6);
        schieben(a, -(dx / d) * k, -(dy / d) * k);
        schieben(b, (dx / d) * k, (dy / d) * k);
      }
  }
  function schieben(f: Figur, dx: number, dy: number) {
    const p = { x: f.pos.x + dx, y: f.pos.y + dy };
    if (f.zustand === "wartet") {
      /* The queue stays in the cross corridor. */
      p.x = Math.min(Math.max(p.x, plan.quer.x + 22), plan.quer.x + plan.quer.w - 22);
      f.pos = p;
      return;
    }
    const ri = raumAn(plan, p);
    if (ri != null && istFrei(raster, ri, p)) f.pos = p;
  }

  /* ── What rarely happens ────────────────────────────────────────────────── */

  /** Whom one can send off without lying about their state. */
  const freieHand = () =>
    figuren.filter((f) => f.zustand === "frei" && !f.route.length && !f.giesst && !f.angesprochen);

  /* The cat comes in at the far end of a corridor, walks to someone who is
     working, lies down beside them and leaves the same way. */
  function katzeLos() {
    if (gaeste.some((g) => g.art === "katze")) return;
    const arbeiter = figuren.filter((f) => f.zustand === "arbeitet" && raumAn(plan, f.pos) === f.ri);
    if (!arbeiter.length) return;
    const a = zufall(arbeiter);
    const zi = a.ri;
    const start = { x: plan.breite - 30, y: plan.raeume[zi].flurY };
    const platz = freiNahe(raster, zi, a.sitz);
    gaeste.push({
      art: "katze",
      id: naechsteId++,
      pos: start,
      route: route(start, platz, zi),
      ruht: 0,
      zi,
      schub: 4000 + Math.random() * 8000,
    });
    gaesteGeaendert();
  }

  function katzeSchritt(g: Extract<Gast, { art: "katze" }>, dt: number) {
    if (g.route.length) {
      const z = g.route[0];
      const dx = z.x - g.pos.x;
      const dy = z.y - g.pos.y;
      const d = Math.hypot(dx, dy);
      const s = 60 * dt;
      if (d <= s) {
        g.pos = { ...z };
        g.route.shift();
        if (!g.route.length && g.ruht === 0) g.ruht = 18000;
      } else {
        g.pos.x += (dx / d) * s;
        g.pos.y += (dy / d) * s;
      }
      return;
    }
    if (g.ruht === -9999) {
      /* Back at the end of the corridor: gone. */
      gaeste.splice(gaeste.indexOf(g), 1);
      gaesteGeaendert();
      return;
    }
    g.ruht -= dt * 1000;
    /* And if she finds a cup, she pushes it off. A cat does that; a state of
       this platform it is not. */
    if (g.schub > 0 && (g.schub -= dt * 1000) <= 0) {
      const mit = figuren.find((f) => f.ri === g.zi && f.hatTasse);
      if (mit) {
        mit.hatTasse = false;
        haken.tasse?.(mit.id, false);
      }
    }
    if (g.ruht > 0) return;
    /* The way back: out of the door and down the corridor to where she came in. */
    const r = plan.raeume[g.zi];
    g.route = [
      ...wegImRaum(raster, g.zi, g.pos, r.innen),
      { ...r.aussen },
      { x: r.tuerX, y: r.flurY },
      { x: plan.breite - 30, y: r.flurY },
    ];
    g.ruht = -9999;
  }

  function besprechungLos() {
    const ziele = gemRaeume("besprechung");
    if (!ziele.length) return;
    const gi = zufall(ziele);
    const wer = freieHand()
      .filter((f) => !f.heimkehr)
      .sort(() => Math.random() - 0.5)
      .slice(0, 3 + Math.floor(Math.random() * 3));
    if (wer.length < 2) return;
    wer.forEach((f, i) => {
      const hin = treffpunkt(gi, i);
      if (!hin) return;
      losschicken(f, route(f.pos, hin, gi));
      f.heimkehr = true;
      f.pause = 14000 + Math.random() * 9000;
    });
  }

  function kuchenLos() {
    const ziele = gemRaeume("kueche");
    if (!ziele.length || gaeste.some((g) => g.art === "kuchen")) return;
    const gi = ziele[0];
    const g = plan.raeume[gi];
    gaeste.push({ art: "kuchen", id: naechsteId++, pos: { x: g.x + g.w / 2, y: g.y + g.h / 2 }, bis: 26000 });
    gaesteGeaendert();
    freieHand()
      .filter((f) => !f.heimkehr)
      .sort(() => Math.random() - 0.5)
      .slice(0, 6)
      .forEach((f, i) => {
        const hin = treffpunkt(gi, i);
        if (!hin) return;
        losschicken(f, route(f.pos, hin, gi));
        f.heimkehr = true;
        f.tasseHolen = true;
        f.pause = 16000 + Math.random() * 9000;
      });
  }

  /* The bird lands on the outer wall of a room in the top row — the only
     walls whose outside is the sky. */
  function vogelLos() {
    if (!plan.raeume.length || gaeste.some((g) => g.art === "vogel")) return;
    const oben = Math.min(...plan.raeume.map((r) => r.y));
    const reihe = plan.raeume.filter((r) => r.y - oben < 1);
    const r = zufall(reihe);
    gaeste.push({ art: "vogel", id: naechsteId++, pos: { x: r.x + r.w * (0.25 + Math.random() * 0.5), y: r.y }, bis: 7000 });
    gaesteGeaendert();
  }

  /* A paper plane along a corridor, from the cross corridor to the far end. */
  function fliegerLos() {
    if (!plan.flure.length || gaeste.some((g) => g.art === "flieger")) return;
    const f = zufall(plan.flure);
    const x = plan.quer.x + plan.quer.w;
    gaeste.push({ art: "flieger", id: naechsteId++, pos: { x, y: f.mitte }, weit: plan.breite - x - 60, bis: 5400 });
    gaesteGeaendert();
  }

  /* Clicking a plant.
   *
   * A first attempt did nothing whenever nobody happened to be free — and
   * since most colleagues are asleep most of the time, that was the normal
   * case. Now the plant remembers its thirst, the next one who has nothing to
   * do goes over, and if nobody has come after three seconds, it was the
   * visitor's own watering can. Waking a sleeper for it would be a lie about
   * their state; a plant that does not react to a click is a broken button. */
  function giessen(id: string, punkt: Punkt): Giesser {
    if (ruhig) {
      haken.gegossen?.(id, "selbst");
      return "selbst";
    }
    if (!durstig.some((d) => d.id === id)) {
      durstig.push({ id, punkt, seit: 0 });
      haken.durst?.();
    }
    return giessdienst() ? "kollege" : "selbst";
  }

  /** Sends the nearest free colleague to the first thirsty plant; true if
   *  someone went. Without anyone free the plant's patience runs down. */
  function giessdienst(dt = 0): boolean {
    if (!durstig.length) return false;
    const d = durstig[0];
    const frei = freieHand();
    if (!frei.length) {
      d.seit += dt * 1000;
      if (d.seit >= GIESS_GEDULD) {
        durstig.shift();
        haken.gegossen?.(d.id, "selbst");
        haken.durst?.();
      }
      return false;
    }
    const zi = raumAn(plan, d.punkt);
    const f = frei
      .map((k) => ({ k, s: Math.hypot(k.pos.x - d.punkt.x, k.pos.y - d.punkt.y) }))
      .sort((a, b) => a.s - b.s)[0].k;
    durstig.shift();
    haken.durst?.();
    f.giesst = d.id;
    /* The plant is furniture: the figure stands on the free floor beside it. */
    const hin = zi != null ? freiNahe(raster, zi, d.punkt) : { ...d.punkt };
    losschicken(f, route(f.pos, hin, zi));
    f.pause = 3600;
    if (!f.route.length) angekommen(f);
    return true;
  }

  function ausloesen(e: Ereignis) {
    if (e === "aufmerksam") {
      hinsehen();
      haken.aufmerksam?.();
      return;
    }
    if (ruhig) return;
    if (e === "katze") katzeLos();
    else if (e === "besprechung") besprechungLos();
    else if (e === "flieger") fliegerLos();
    else if (e === "kuchen") kuchenLos();
    else if (e === "vogel") vogelLos();
  }

  const wach = (f: Figur) => f.zustand !== "schlaeft" && f.zustand !== "gestoppt";

  function hinsehen() {
    const mx = plan.breite / 2;
    const my = plan.hoehe / 2;
    for (const f of figuren) {
      if (!wach(f) || f.route.length) continue;
      const dx = mx - f.pos.x;
      const dy = my - f.pos.y;
      const d = Math.hypot(dx, dy) || 1;
      f.blick = { x: dx / d, y: dy / d };
    }
  }

  let seit = 0;
  function tick(dt: number) {
    if (ruhig || !(dt > 0)) return;
    for (const f of figuren) schritt(f, dt);
    ausweichen(dt);

    let geaendert = false;
    for (const g of [...gaeste]) {
      if (g.art === "katze") katzeSchritt(g, dt);
      else {
        g.bis -= dt * 1000;
        if (g.bis <= 0) {
          gaeste.splice(gaeste.indexOf(g), 1);
          geaendert = true;
        }
      }
    }
    if (geaendert) gaesteGeaendert();

    giessdienst(dt);
    seit += dt * 1000;
    if (seit < 1000) return;
    seit = 0;
    for (const [name, p] of Object.entries(HAEUFIG)) if (Math.random() < (p ?? 0)) ausloesen(name as Ereignis);
  }

  return {
    figuren,
    gaeste,
    tick,
    zustandSetzen,
    ansprechen(id, an) {
      for (const f of figuren) {
        if (an === undefined) f.angesprochen = f.id === id;
        else if (f.id === id) f.angesprochen = an;
      }
    },
    giessen,
    istDurstig: (id) => durstig.some((d) => d.id === id),
    felderSetzen(neu) {
      raster = neu;
    },
    blickAuf(p) {
      for (const f of figuren) {
        if (f.route.length || !wach(f)) continue;
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
    hinsehen,
    ausloesen,
  };
}
