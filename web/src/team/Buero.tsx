import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router";
import * as THREE from "three";
import { post, type Agent, type Department, type Laufend, type Principal } from "../api";
import { canManage } from "../pages/agent/roles";
import Gesicht from "../components/Gesicht";
import Dauer from "../components/Dauer";
import { bauplan } from "./buero/plan";
import { aufSchirm, szeneBauen, type Bau } from "./buero/szene";
import { erschaffeKamera, erschaffeMaler, groesse, type Steuerung } from "./buero/kamera";
import { laufFeldBauen, type LaufFelder } from "./buero/laufweg";
import { erschaffeLeben, type Leben } from "./buero/leben";
import Riss from "./buero/risse";
import type { Gruppe, Plan, Punkt, Zustand } from "./buero/typen";

/* The office: the workforce as a building, seen from above at an angle.
 *
 * A list says that something is running. A room shows it. covey already has
 * the vocabulary for that room — an agent HAS a workplace, belongs to a
 * department, sleeps when nothing is pending and waits for a human when it
 * cannot go on. This is not decoration but the same information in a form
 * that is read at a glance instead of in four lines.
 *
 * THE PLAN IS COMPUTED (buero/plan.ts): how many seats a room has abreast,
 * how deep it is, how many rooms fit a row and whether a second corridor is
 * needed follows from the headcount. THE HOUSE IS BUILT (buero/szene.ts) from
 * primitives; every department room gets a character from its name
 * (buero/charakter.ts) and one of five accent colours. THE WALLS THAT FACE
 * THE CAMERA ARE CUT DOWN to a plinth, the others stand full with windows —
 * an architectural cutaway, so nothing hides a desk; a quarter turn rebuilds
 * it. THE LIGHT IS THE STATE: a screen is dark while nothing runs and lit as
 * soon as something does, and a room with a lit desk is a lit room.
 *
 * WHAT CARRIES INFORMATION and what is only atmosphere is kept apart in
 * buero/leben.ts — and that separation is a condition, not taste. A movement
 * from which something could be read that no record holds would be a lie
 * with charm.
 *
 * WHY HTML FACES OVER A CANVAS: the faces exist as an SVG component with
 * states, animations and appearance. Painted onto the canvas they would be
 * built a second time, a click on a colleague would be arithmetic instead of
 * a button, and the keyboard would have nowhere to go. So the scene is a
 * canvas and the people, the signs and the card are an overlay positioned
 * from the camera every frame — with `transform`, one write per figure.
 */

/** How far the eyes swing, in the face's own grid (24 wide). */
const BLICK_WEITE = 1.25;
/** The building's maximum width in centimetres; the camera fits it to the stage. */
const BAU_BREITE = 3200;

const DICHTE_STUFEN = [
  { key: "sparsam", wert: 0.45 },
  { key: "normal", wert: 1 },
  { key: "ueppig", wert: 1.7 },
] as const;
const DICHTE_SCHLUESSEL = "covey.buero.dichte";
function gespeicherteDichte(): number {
  try {
    const w = Number(localStorage.getItem(DICHTE_SCHLUESSEL));
    return DICHTE_STUFEN.some((s) => s.wert === w) ? w : 1;
  } catch {
    return 1;
  }
}

/* Day or night follows the interface theme: whoever sets the surface dark
   means the office too. The theme is an attribute on the document root
   (theme.ts) or, when unset, the system preference. */
function useNacht(): boolean {
  const lesen = () => {
    if (typeof document === "undefined") return false;
    const t = document.documentElement.getAttribute("data-theme");
    if (t === "dark") return true;
    if (t === "light") return false;
    return window.matchMedia?.("(prefers-color-scheme: dark)").matches ?? false;
  };
  const [nacht, setNacht] = useState(lesen);
  useEffect(() => {
    const neu = () => setNacht(lesen());
    const beobachter = new MutationObserver(neu);
    beobachter.observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme"] });
    const medium = window.matchMedia?.("(prefers-color-scheme: dark)");
    medium?.addEventListener("change", neu);
    return () => {
      beobachter.disconnect();
      medium?.removeEventListener("change", neu);
    };
  }, []);
  return nacht;
}

export default function Buero({
  agents,
  departments,
  laufend,
  wartetBei,
  me,
}: {
  agents: Agent[];
  departments: Department[];
  laufend: Laufend[];
  wartetBei: Set<string>;
  me: Principal;
}) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const ruhig = useMemo(() => window.matchMedia?.("(prefers-reduced-motion: reduce)").matches ?? false, []);
  const nacht = useNacht();
  const [dichte, setDichte] = useState(gespeicherteDichte);
  const [webgl, setWebgl] = useState(true);

  /* The building depends on what REALLY changes it: who works here and in
     which department. Not on whether the shell happened to allocate a new
     array — the overview refetches running tasks every ten seconds, and a
     plan hung on array identity was rebuilt every ten seconds, with the life
     inside it thrown away (#312). */
  const kern = agents.map((a) => `${a.id}:${a.department_id ?? ""}:${a.slug}`).join("|");
  const abtKern = departments.map((d) => `${d.id}:${d.name}:${d.color}`).join("|");
  const ohneName = t("team.ohneAbteilung");
  const gruppen = useMemo<Gruppe[]>(() => {
    const ohne = agents.filter((a) => !departments.some((d) => d.id === a.department_id));
    return [
      ...departments
        .map((d) => ({ id: d.id, name: d.name, farbe: d.color, leute: agents.filter((a) => a.department_id === d.id) }))
        .filter((g) => g.leute.length > 0),
      ...(ohne.length > 0 ? [{ id: "ohne", name: ohneName, farbe: "", leute: ohne }] : []),
    ];
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [kern, abtKern, ohneName]);

  const namen = useMemo(
    () => ({ besprechung: t("team.raumBesprechung"), kueche: t("team.raumTeekueche"), lounge: t("team.raumLounge") }),
    [t],
  );
  const plan = useMemo(() => bauplan(gruppen, BAU_BREITE, namen, dichte), [gruppen, namen, dichte]);

  /* State from the data. "arbeitet" means: has a running task — the status
     alone only said that the agent is awake, and an awake agent without a
     task is not information but a state. */
  const laufendVon = useMemo(() => new Map(laufend.map((l) => [l.agent_id, l])), [laufend]);
  const zustandVon = useCallback(
    (a: Agent): Zustand =>
      a.killed ? "gestoppt" : wartetBei.has(a.id) ? "wartet" : laufendVon.has(a.id) ? "arbeitet" : a.status === "sleeping" ? "schlaeft" : "frei",
    [laufendVon, wartetBei],
  );
  /* The scene is rebuilt only when a state actually changes — a lit screen is
     geometry here — not on every poll that returns the same picture. */
  const zustandKern = agents.map((a) => `${a.id}:${zustandVon(a)}`).join("|");
  const zustandRef = useRef(zustandVon);
  zustandRef.current = zustandVon;

  const [gewaehlt, setGewaehlt] = useState<string | null>(null);

  /* ── The stage ──────────────────────────────────────────────────────────
     Renderer, camera, scene and life live in refs, not in state: seventy
     figures per frame through React would be seventy reconciliations per
     frame. React draws the buttons; the frame loop moves them. */
  const buehne = useRef<HTMLDivElement>(null);
  const maler = useRef<THREE.WebGLRenderer | null>(null);
  const szene = useRef<THREE.Scene | null>(null);
  const steuerung = useRef<Steuerung | null>(null);
  const bau = useRef<Bau | null>(null);
  const felder = useRef<LaufFelder | null>(null);
  const leben = useRef<Leben | null>(null);
  const planImLeben = useRef<Plan | null>(null);
  const knoepfe = useRef(new Map<string, HTMLButtonElement>());
  const blicke = useRef(new Map<string, SVGGElement>());
  const schilder = useRef(new Map<string, HTMLDivElement>());
  const masten = useRef<SVGSVGElement>(null);
  const karteHuelle = useRef<HTMLDivElement>(null);
  const zeiger = useRef<Punkt | null>(null);
  const gastRefs = useRef(new Map<number, HTMLElement>());
  /* The stage exists only once there is somebody to show. The renderer is
     created when it appears — on the first render the lists are usually
     still loading, and an effect that ran once against an empty page would
     never run again. */
  const sichtbar = plan.raeume.some((r) => r.leute.length > 0) && webgl;
  const bauenRef = useRef<() => void>(() => {});
  /* The guest list lives in the life; React only learns that it changed and
     draws the sprites, the frame loop moves them. */
  const [gaesteTakt, setGaesteTakt] = useState(0);
  const haken = useMemo(() => ({ gaeste: () => setGaesteTakt((n) => n + 1) }), []);

  useLayoutEffect(() => {
    const el = buehne.current;
    if (!el || !sichtbar) return;
    const m = erschaffeMaler(el);
    if (!m) {
      setWebgl(false);
      return;
    }
    maler.current = m;
    szene.current = new THREE.Scene();
    const s = erschaffeKamera(el, ruhig);
    steuerung.current = s;
    const loesen = s.anbinden(() => setGewaehlt(null));
    const messen = () => {
      groesse(m, s.kamera, el);
      s.groesse();
    };
    messen();
    const beobachter = new ResizeObserver(messen);
    beobachter.observe(el);
    bauenRef.current();
    return () => {
      beobachter.disconnect();
      loesen();
      m.dispose();
      m.domElement.remove();
      maler.current = null;
      steuerung.current = null;
      szene.current = null;
      bau.current = null;
    };
  }, [ruhig, sichtbar]);

  /* Build or rebuild the house. Afterwards the walk grid is read from what
     was actually built, and the life gets the new grid — the figures keep
     their positions and routes. A new PLAN (other people, other rooms) is
     the one case where the life starts over: the geometry beneath it is a
     different one. */
  const bauen = useCallback(() => {
    const sz = szene.current, st = steuerung.current;
    if (!sz || !st) return;
    st.planSetzen(plan);
    bau.current = szeneBauen(sz, bau.current?.welt ?? null, plan, {
      zustand: (id) => {
        const a = agents.find((x) => x.id === id);
        return a ? zustandRef.current(a) : "frei";
      },
      nacht,
      dichte,
      drehung: st.drehung,
    });
    felder.current = laufFeldBauen(bau.current.welt, plan);
    if (planImLeben.current !== plan || !leben.current) {
      planImLeben.current = plan;
      leben.current = erschaffeLeben(
        plan,
        felder.current,
        plan.raeume.flatMap((r, ri) => r.leute.map((a, i) => ({ id: a.id, slug: a.slug, ri, i, zustand: zustandRef.current(a) }))),
        ruhig,
        haken,
      );
      setGaesteTakt((n) => n + 1);
    } else {
      leben.current.felderSetzen(felder.current);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [plan, nacht, dichte, ruhig, kern, haken]);
  bauenRef.current = bauen;
  useEffect(() => {
    bauen();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [bauen, zustandKern]);

  /* States are taken over as they come from the data; positions stay. */
  useEffect(() => {
    const l = leben.current;
    if (!l) return;
    for (const a of agents) l.zustandSetzen(a.id, zustandVon(a), laufendVon.get(a.id)?.step ?? null);
  }, [agents, zustandVon, laufendVon, zustandKern]);

  useEffect(() => {
    leben.current?.ansprechen(gewaehlt);
  }, [gewaehlt]);

  /* The frame: the camera's ride, the life's step, then every overlay is set
     from the camera. With "reduced motion" the life does not run at all —
     everyone sits at their place, the light stays on; the information stays,
     only the movement goes. */
  useEffect(() => {
    let laeuft = true;
    let letzte = 0;
    const bild = (t0: number) => {
      if (!laeuft) return;
      const roh = Math.min(64, t0 - letzte) / 1000;
      letzte = t0;
      const m = maler.current, sz = szene.current, st = steuerung.current, el = buehne.current, l = leben.current;
      if (m && sz && st && el) {
        if (st.tick(roh)) bauen();
        if (l && !ruhig) l.tick(roh);
        const w = el.clientWidth, h = el.clientHeight;
        if (l) {
          for (const f of l.figuren) {
            const kn = knoepfe.current.get(f.id);
            if (!kn) continue;
            const bob = f.geht ? Math.sin(f.phase * 0.16) * 3 : 0;
            const [x, y] = aufSchirm(st.kamera, plan, w, h, f.pos.x, f.pos.y, 44 + bob);
            kn.style.transform = `translate3d(${x}px, ${y}px, 0)`;
            kn.classList.toggle("geht", f.geht);
            /* The gaze follows the pointer, in screen space — where the
               person looks is a matter of the picture, not of the plan. */
            const b = blicke.current.get(f.id);
            if (b) {
              const z = zeiger.current;
              if (z && f.zustand !== "schlaeft" && f.zustand !== "gestoppt") {
                const dx = z.x - x, dy = z.y - y, d = Math.max(1, Math.hypot(dx, dy));
                const k = Math.min(1, d / 160);
                b.style.transform = `translate(${(dx / d) * k * BLICK_WEITE}px, ${(dy / d) * k * BLICK_WEITE}px)`;
              } else b.style.transform = "";
            }
            if (f.id === gewaehlt && karteHuelle.current) {
              const hoch = karteHuelle.current.offsetHeight || 170;
              karteHuelle.current.style.left = `${Math.min(Math.max(x, 140), w - 140)}px`;
              karteHuelle.current.style.top = `${Math.min(Math.max(y - hoch - 34, 8), h - hoch - 8)}px`;
            }
          }
        }
        if (l)
          for (const g of l.gaeste) {
            const el = gastRefs.current.get(g.id);
            if (!el) continue;
            const [x, y] = aufSchirm(st.kamera, plan, w, h, g.pos.x, g.pos.y, g.art === "vogel" ? 124 : g.art === "flieger" ? 90 : 8);
            el.style.transform = `translate3d(${x}px, ${y}px, 0)`;
          }
        schilderStellen(st, w, h);
        m.render(sz, st.kamera);
      }
      requestAnimationFrame(bild);
    };
    const id = requestAnimationFrame((t0) => {
      letzte = t0;
      requestAnimationFrame(bild);
    });
    return () => {
      laeuft = false;
      cancelAnimationFrame(id);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [plan, ruhig, gewaehlt, bauen]);

  /* Signs are pushed apart in screen space, like on a map: a sign that sits
     on another sign is two signs nobody can read. */
  const schilderStellen = (st: Steuerung, w: number, h: number) => {
    const k = plan.raeume.map((r) => {
      const d = schilder.current.get(r.id + r.nr);
      const [x, y] = aufSchirm(st.kamera, plan, w, h, r.x + r.w / 2, r.y + 30, 60);
      const b = d?.offsetWidth || 110, hh = d?.offsetHeight || 20;
      return { d, ax: x, ay: y, w: b, h: hh, x, y: y - 26 };
    });
    k.sort((a, b) => a.ay - b.ay);
    for (let runde = 0; runde < 8; runde++) {
      let bewegt = false;
      for (let i = 0; i < k.length; i++)
        for (let j = i + 1; j < k.length; j++) {
          const a = k[i], c = k[j];
          if (Math.abs(a.x - c.x) - (a.w + c.w) / 2 - 6 >= 0) continue;
          const dy = Math.abs(a.y - c.y) - (a.h + c.h) / 2 - 4;
          if (dy >= 0) continue;
          const schub = Math.min(-dy, 14) / 2;
          (a.ay <= c.ay ? a : c).y -= schub;
          (a.ay <= c.ay ? c : a).y += schub * 0.35;
          bewegt = true;
        }
      if (!bewegt) break;
    }
    const svg = masten.current;
    let pfad = "";
    for (const s of k) {
      if (s.d) {
        s.d.style.left = `${s.x}px`;
        s.d.style.top = `${s.y}px`;
      }
      pfad += `M${s.x.toFixed(1)} ${(s.y + 2).toFixed(1)}L${s.ax.toFixed(1)} ${s.ay.toFixed(1)}M${(s.ax - 1.8).toFixed(1)} ${s.ay.toFixed(1)}a1.8 1.8 0 1 0 3.6 0a1.8 1.8 0 1 0 -3.6 0`;
    }
    svg?.firstElementChild?.setAttribute("d", pfad);
  };

  /* The pointer, decoupled from the event: mouse moves come more often than
     frames, and handling each would mean computing several times per frame. */
  const aufZeiger = (e: React.PointerEvent<HTMLDivElement>) => {
    if (ruhig) return;
    const r = e.currentTarget.getBoundingClientRect();
    zeiger.current = { x: e.clientX - r.left, y: e.clientY - r.top };
  };

  /* A click on the floor closes the card — and a click on a plant waters it.
     The plant is found by casting into the scene: the catalogue builds each
     plant into a group that carries its key. */
  const strahl = useRef(new THREE.Raycaster());
  const aufKlick = (e: React.MouseEvent<HTMLDivElement>) => {
    setGewaehlt(null);
    const st = steuerung.current, b = bau.current, l = leben.current, el = buehne.current;
    if (!st || !b || !l || !el) return;
    const r = el.getBoundingClientRect();
    strahl.current.setFromCamera(new THREE.Vector2(((e.clientX - r.left) / r.width) * 2 - 1, -((e.clientY - r.top) / r.height) * 2 + 1), st.kamera);
    const treffer = strahl.current.intersectObjects(b.welt.children, true);
    for (const tr of treffer) {
      let o: THREE.Object3D | null = tr.object;
      while (o && !o.userData.pflanze) o = o.parent;
      if (o?.userData.pflanze) {
        l.giessen(String(o.userData.pflanze), o.userData.punkt as Punkt);
        return;
      }
    }
  };

  useEffect(() => {
    if (!gewaehlt) return;
    const zu = (e: KeyboardEvent) => {
      if (e.key === "Escape") setGewaehlt(null);
    };
    window.addEventListener("keydown", zu);
    return () => window.removeEventListener("keydown", zu);
  }, [gewaehlt]);

  const dichteWaehlen = (wert: number) => {
    setDichte(wert);
    try {
      localStorage.setItem(DICHTE_SCHLUESSEL, String(wert));
    } catch {
      /* a browser without storage forgets the choice, nothing else */
    }
  };

  /* The building is fitted to the stage whatever its headcount, so the faces
     have to give way: seventy heads at thirty pixels cover the desks they
     sit at. Smaller from forty on, and the name goes with the head. */
  const gesichtGroesse = agents.length > 40 ? 24 : 30;
  void gaesteTakt; // the guest list is a ref; the tick triggers the redraw
  const gaeste = leben.current?.gaeste ?? [];
  const gewaehlterAgent = agents.find((a) => a.id === gewaehlt);
  const anwaehlen = (a: Agent) => setGewaehlt(gewaehlt === a.id ? null : a.id);

  if (!plan.raeume.some((r) => r.leute.length > 0)) return null;
  if (!webgl) return <p className="bu-legende">{t("team.keinWebgl")}</p>;

  return (
    <div className="bu">
      <div
        className={`bu-buehne${ruhig ? " ruht" : ""}`}
        ref={buehne}
        onPointerMove={aufZeiger}
        onPointerLeave={() => {
          zeiger.current = null;
        }}
        onClick={aufKlick}
      >
        <div className="bu-leute">
          <svg className="bu-masten" ref={masten} aria-hidden="true">
            <path d="" />
          </svg>
          {plan.raeume.map((r) => (
            <div
              key={r.id + r.nr}
              className="bu-schild-3d"
              ref={(el) => {
                if (el) schilder.current.set(r.id + r.nr, el);
                else schilder.current.delete(r.id + r.nr);
              }}
            >
              {r.farbe && <span className="punkt" style={{ background: r.farbe }} />}
              {r.name}
              {!r.gem && <span className="klein">{`${r.flur + 1}.${String(r.nr).padStart(2, "0")}`}</span>}
            </div>
          ))}

          {plan.raeume.map((r) =>
            r.leute.map((a) => {
              const laeuft = laufendVon.get(a.id);
              const zustand = zustandVon(a);
              return (
                <button
                  key={a.id}
                  ref={(el) => {
                    if (el) knoepfe.current.set(a.id, el);
                    else knoepfe.current.delete(a.id);
                  }}
                  data-wer={a.id}
                  className={`bu-wer${laeuft ? " arbeitet" : ""}${zustand === "gestoppt" ? " gestoppt" : ""}${gewaehlt === a.id ? " gewaehlt" : ""}`}
                  onClick={(e) => {
                    e.stopPropagation();
                    anwaehlen(a);
                  }}
                  onPointerDown={(e) => e.stopPropagation()}
                  aria-expanded={gewaehlt === a.id}
                  title={laeuft ? `${a.display_name}: ${laeuft.title}` : a.display_name}
                >
                  {/* The thought bubble: the last recorded step, above the head
                      of whoever is doing it. Keyed by the step itself, so the
                      reveal runs again when it changes. */}
                  {laeuft?.step && (
                    <span key={laeuft.step} className="bu-denkt">
                      {t(`team.schritt.${laeuft.step}`, laeuft.step)}
                    </span>
                  )}
                  {zustand === "wartet" && !laeuft?.step && <span className="bu-denkt warten">{t("team.wartet")}</span>}
                  <span className="bu-kopf">
                    <Gesicht
                      schluessel={a.slug}
                      zustand={zustand === "gestoppt" ? "killed" : zustand === "schlaeft" ? "sleeping" : "working"}
                      groesse={gesichtGroesse}
                      blickRef={(g) => {
                        if (g) blicke.current.set(a.id, g);
                        else blicke.current.delete(a.id);
                      }}
                    />
                  </span>
                  <span className="bu-name">{a.display_name.split(/\s+/)[0]}</span>
                </button>
              );
            }),
          )}

          {/* What rarely passes by. Nothing of it carries information. The
              sprites are the same sketches the flat plan used; they hover
              over the scene, positioned like the faces. */}
          {gaeste.map((g) => (
            <span
              key={g.id}
              className={`bu-gast g-${g.art}`}
              ref={(el) => {
                if (el) gastRefs.current.set(g.id, el);
                else gastRefs.current.delete(g.id);
              }}
              style={g.art === "flieger" ? { ["--weit" as string]: `${g.weit * 0.3}px` } : undefined}
            >
              <Riss art={g.art} x={0} y={0} w={g.art === "katze" ? 22 : g.art === "kuchen" ? 22 : g.art === "flieger" ? 22 : 13} h={g.art === "katze" ? 26 : g.art === "kuchen" ? 20 : 12} />
            </span>
          ))}

          {gewaehlterAgent && (
            <div className="bu-karte-huelle" ref={karteHuelle}>
              <BuKarte
                agent={gewaehlterAgent}
                laeuft={laufendVon.get(gewaehlterAgent.id)}
                wartet={wartetBei.has(gewaehlterAgent.id)}
                me={me}
                onOeffnen={() => navigate(`/team/${gewaehlterAgent.id}`)}
                onSchliessen={() => setGewaehlt(null)}
              />
            </div>
          )}
        </div>

        {/* The controls are the visible promise that the handles exist.
            Without them one has to guess whether a view can be moved — and
            guesses wrong. */}
        <div className="bu-steuer" onPointerDown={(e) => e.stopPropagation()} onClick={(e) => e.stopPropagation()}>
          <button type="button" onClick={() => steuerung.current?.zoomen(1.16)} title={`${t("team.naeher")} (+)`} aria-label={t("team.naeher")}>+</button>
          <button type="button" onClick={() => steuerung.current?.zoomen(1 / 1.16)} title={`${t("team.weiter")} (−)`} aria-label={t("team.weiter")}>−</button>
          <button type="button" onClick={() => steuerung.current?.drehen(1)} title={`${t("team.drehenLinks")} (Q)`} aria-label={t("team.drehenLinks")}>↺</button>
          <button type="button" onClick={() => steuerung.current?.drehen(-1)} title={`${t("team.drehenRechts")} (E)`} aria-label={t("team.drehenRechts")}>↻</button>
          <button type="button" onClick={() => steuerung.current?.zurueck()} title={`${t("team.ansichtZurueck")} (0)`} aria-label={t("team.ansichtZurueck")}>⌂</button>
        </div>
      </div>

      <div className="bu-fuss">
        <p className="bu-legende">{t("team.bueroLegende")}</p>
        <div className="bu-dichte" role="group" aria-label={t("team.ausstattung")}>
          <span className="bu-dichte-marke">{t("team.ausstattung")}</span>
          {DICHTE_STUFEN.map((s) => (
            <button key={s.key} type="button" aria-pressed={dichte === s.wert} onClick={() => dichteWaehlen(s.wert)}>
              {t(`team.ausstattung${s.key[0].toUpperCase()}${s.key.slice(1)}`)}
            </button>
          ))}
        </div>
      </div>
    </div>
  );
}

/* The card at the desk.
 *
 * It does not replace the jump into the conversation, it comes before it: in
 * nine cases out of ten one wants to know what somebody is working on, and in
 * the tenth one wants to tell them a sentence. Having both here means the
 * office is a working surface and not a picture of one. Whoever is addressed
 * stops walking, by the way — a person does that too.
 *
 * The input is the same door as in the conversation — same address, same
 * triage. It only does not answer: what the colleague makes of it stands in
 * the conversation, and the link below leads there.
 */
function BuKarte({
  agent,
  laeuft,
  wartet,
  me,
  onOeffnen,
  onSchliessen,
}: {
  agent: Agent;
  laeuft?: Laufend;
  wartet: boolean;
  me: Principal;
  onOeffnen: () => void;
  onSchliessen: () => void;
}) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [text, setText] = useState("");
  const [ab, setAb] = useState(false);
  const karte = useRef<HTMLDivElement>(null);
  useEffect(() => {
    karte.current?.focus({ preventScroll: true });
  }, []);
  const schicken = useMutation({
    mutationFn: (nachricht: string) => post(`/agents/${agent.id}/messages`, { text: nachricht }),
    onSuccess: () => {
      setText("");
      setAb(true);
      qc.invalidateQueries({ queryKey: ["thread", agent.id] });
      qc.invalidateQueries({ queryKey: ["org-running"] });
    },
  });
  const darfSchreiben = canManage(me.Role);
  return (
    <div
      className="bu-karte"
      role="dialog"
      aria-label={agent.display_name}
      ref={karte}
      tabIndex={-1}
      onPointerDown={(e) => e.stopPropagation()}
      onClick={(e) => e.stopPropagation()}
    >
      <header className="bu-karte-kopf">
        <Gesicht schluessel={agent.slug} zustand={agent.killed ? "killed" : agent.status === "sleeping" ? "sleeping" : "working"} groesse={28} />
        <span className="bu-karte-wer">
          <strong>{agent.display_name}</strong>
          <span className="bu-karte-rolle">{agent.job_title || agent.slug}</span>
        </span>
        <button className="bu-karte-zu" onClick={onSchliessen} aria-label={t("team.abbrechen")}>
          ×
        </button>
      </header>
      {laeuft ? (
        <p className="bu-karte-lage">
          <span className="bu-karte-titel">{laeuft.title}</span>
          <span className="bu-karte-unten">
            {laeuft.step && <>{t(`team.schritt.${laeuft.step}`, laeuft.step)} · </>}
            <Dauer seit={laeuft.since} />
          </span>
        </p>
      ) : (
        <p className="bu-karte-lage">
          <span className="bu-karte-unten">{wartet ? t("team.wartet") : agent.status === "sleeping" ? t("team.schlaeft") : t("team.bereit")}</span>
        </p>
      )}
      {darfSchreiben && (
        <form
          className="bu-karte-sagen"
          onSubmit={(e) => {
            e.preventDefault();
            const n = text.trim();
            if (n && !schicken.isPending) schicken.mutate(n);
          }}
        >
          <input
            value={text}
            onChange={(e) => {
              setText(e.target.value);
              setAb(false);
            }}
            placeholder={t("chat.placeholder")}
            aria-label={t("chat.placeholder")}
          />
          <button type="submit" disabled={!text.trim() || schicken.isPending} aria-label={t("team.senden")}>
            ↑
          </button>
        </form>
      )}
      {/* Accepted, not answered: what the colleague makes of it stands in the
          conversation, and the link below leads there. Claiming it was done
          here would be exactly the untruth #304 removed. */}
      {ab && <p className="bu-karte-ab">{t("team.angenommen")}</p>}
      <button className="bu-karte-hin" onClick={onOeffnen}>
        {t("team.gespraech")}
      </button>
    </div>
  );
}
