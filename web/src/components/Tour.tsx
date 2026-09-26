import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";

/* The tour (#402): what the team surface can do, shown once to whoever
 * arrives, in a handful of steps.
 *
 * The help drawer explains the console to an administrator. A person who
 * was only invited sees an icon rail and a list of names, and nothing tells
 * them that writing to an agent gives it work, that a lit desk means a
 * running task or that a question waits above the input. So the tour points
 * at the real thing — the rail item, the menu — instead of describing a
 * screenshot of it.
 *
 * A step names its target by `data-tour`. What only exists inside an open
 * conversation (the decision cards) is pointed at when it is on screen, and
 * otherwise the step stands in the middle: the tour must not open a
 * conversation to have something to show, and it must not skip what a person
 * most needs to know because it is not on screen yet.
 *
 * Seen is remembered in this browser only. The tour is a convenience, not a
 * record; whoever wants it again finds it in the person's menu. */

export type TourSchritt = {
  /** The key under `tour.` for title and text. */
  id: string;
  /** The `data-tour` value of the element to point at; none: in the middle. */
  ziel?: string;
};

export const TEAM_TOUR: TourSchritt[] = [
  { id: "willkommen" },
  { id: "buero", ziel: "office" },
  { id: "team", ziel: "team" },
  { id: "gespraech", ziel: "eingabe" },
  { id: "entscheiden", ziel: "entscheiden" },
  { id: "notizen", ziel: "notes" },
  { id: "suche", ziel: "search" },
  { id: "person", ziel: "person" },
];

const GESEHEN = "covey.tour.team";

export function tourGesehen(): boolean {
  try {
    return localStorage.getItem(GESEHEN) === "1";
  } catch {
    // Without storage the tour would come back on every page; better never.
    return true;
  }
}

function merken() {
  try {
    localStorage.setItem(GESEHEN, "1");
  } catch {
    /* nothing to remember in */
  }
}

type Rahmen = { x: number; y: number; w: number; h: number };

const RAND = 6; // the air around the highlighted element
const KARTE_B = 320;
const ABSTAND = 14;

function zielRahmen(ziel?: string): Rahmen | null {
  if (!ziel) return null;
  const el = document.querySelector<HTMLElement>(`[data-tour="${ziel}"]`);
  if (!el) return null;
  const r = el.getBoundingClientRect();
  if (r.width === 0 && r.height === 0) return null;
  return { x: r.left - RAND, y: r.top - RAND, w: r.width + 2 * RAND, h: r.height + 2 * RAND };
}

/* Where the card stands: right of the target (the rail is on the left),
   below it when the window is too narrow for that, and always inside the
   window. Without a target in the middle. */
function kartenOrt(z: Rahmen | null, kh: number): { left: number; top: number } {
  const vw = window.innerWidth,
    vh = window.innerHeight;
  const b = Math.min(KARTE_B, vw - 32);
  if (!z) return { left: (vw - b) / 2, top: Math.max(16, (vh - kh) / 2) };
  const klemme = (v: number, lo: number, hi: number) => Math.max(lo, Math.min(hi, v));
  if (z.x + z.w + ABSTAND + b <= vw - 16) {
    return { left: z.x + z.w + ABSTAND, top: klemme(z.y + z.h / 2 - kh / 2, 16, vh - kh - 16) };
  }
  const unten = z.y + z.h + ABSTAND + kh <= vh - 16;
  return {
    left: klemme(z.x + z.w / 2 - b / 2, 16, vw - b - 16),
    top: unten ? z.y + z.h + ABSTAND : klemme(z.y - ABSTAND - kh, 16, vh - kh - 16),
  };
}

export default function Tour({ schritte, onEnde }: { schritte: TourSchritt[]; onEnde: () => void }) {
  const { t } = useTranslation();
  const [i, setI] = useState(0);
  const [rahmen, setRahmen] = useState<Rahmen | null>(null);
  const [kh, setKh] = useState(180);
  const karte = useRef<HTMLDivElement>(null);
  const weiterKnopf = useRef<HTMLButtonElement>(null);
  const schritt = schritte[i];
  const letzter = i === schritte.length - 1;

  const ende = useCallback(() => {
    merken();
    onEnde();
  }, [onEnde]);

  // The target is measured again whenever the window changes under it.
  useLayoutEffect(() => {
    const messen = () => setRahmen(zielRahmen(schritt.ziel));
    messen();
    window.addEventListener("resize", messen);
    window.addEventListener("scroll", messen, true);
    return () => {
      window.removeEventListener("resize", messen);
      window.removeEventListener("scroll", messen, true);
    };
  }, [schritt.ziel]);

  useLayoutEffect(() => {
    if (karte.current) setKh(karte.current.offsetHeight);
  }, [i, rahmen]);

  useEffect(() => {
    weiterKnopf.current?.focus();
  }, [i]);

  useEffect(() => {
    const taste = (e: KeyboardEvent) => {
      if (e.key === "Escape") ende();
      else if (e.key === "ArrowRight") setI((n) => Math.min(n + 1, schritte.length - 1));
      else if (e.key === "ArrowLeft") setI((n) => Math.max(n - 1, 0));
    };
    window.addEventListener("keydown", taste);
    return () => window.removeEventListener("keydown", taste);
  }, [ende, schritte.length]);

  const ort = kartenOrt(rahmen, kh);
  return (
    <div className="tour" role="dialog" aria-modal="true" aria-labelledby="tour-titel" aria-describedby="tour-text">
      {rahmen ? (
        <div
          className="tour-licht"
          style={{ left: rahmen.x, top: rahmen.y, width: rahmen.w, height: rahmen.h }}
          aria-hidden="true"
        />
      ) : (
        <div className="tour-dunkel" aria-hidden="true" />
      )}
      <div ref={karte} className="tour-karte" style={{ left: ort.left, top: ort.top, width: Math.min(KARTE_B, window.innerWidth - 32) }}>
        <p className="tour-zaehler">{t("tour.schritt", { n: i + 1, m: schritte.length })}</p>
        <h2 id="tour-titel">{t(`tour.${schritt.id}.titel`)}</h2>
        <p id="tour-text">{t(`tour.${schritt.id}.text`)}</p>
        <div className="tour-knoepfe">
          {!letzter && (
            <button className="btn sm tour-weg" onClick={ende}>
              {t("tour.ueberspringen")}
            </button>
          )}
          <span className="tour-luft" />
          {i > 0 && (
            <button className="btn sm" onClick={() => setI(i - 1)}>
              {t("tour.zurueck")}
            </button>
          )}
          <button ref={weiterKnopf} className="btn sm primary" onClick={() => (letzter ? ende() : setI(i + 1))}>
            {letzter ? t("tour.fertig") : t("tour.weiter")}
          </button>
        </div>
      </div>
    </div>
  );
}
