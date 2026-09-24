import * as THREE from "three";
import type { Plan } from "./typen";
import { START_DREHUNG } from "./szene";

/* The camera and its controls: orthographic, so it stays a plan.
 *
 * This file touches the DOM only through the stage element it is given and
 * the window listeners it attaches in `anbinden` — and removes again with
 * the function `anbinden` returns.
 */

/* The start view. Forty-five degrees was the obvious number and the wrong
   one: the building lay as a diamond on the stage, two corners of the stage
   stayed empty, and the rooms were small. A flat rotation lays the long side
   almost horizontal so the building uses the width, and a slightly steeper
   tilt lets the front walls hide less — it stays a plan one reads, not a
   model one admires. */
export { START_DREHUNG };
export const START_NEIGUNG = 0.7;

export type Steuerung = {
  kamera: THREE.OrthographicCamera;
  planSetzen(plan: Plan): void;
  stellen(): void;
  drehen(richtung: 1 | -1): void;
  schieben(dx: number, dy: number): void;
  zoomen(faktor: number, px?: number, py?: number): void;
  zurueck(): void;
  /** Eases the rotation towards its target. Returns true in the frame a
   *  quarter turn completes — the caller then rebuilds the scene so the
   *  cutaway follows the new view. */
  tick(rohSekunden: number): boolean;
  readonly drehung: number;
  anbinden(onKarteZu: () => void): () => void;
  groesse(): void;
};

/** Creates the camera for `buehne`. `ruhig` (prefers-reduced-motion) makes a
 *  turn snap instead of travel. */
export function erschaffeKamera(buehne: HTMLElement, ruhig = false): Steuerung {
  const kamera = new THREE.OrthographicCamera(-1, 1, 1, -1, -1000, 1000);
  let plan: Pick<Plan, "breite" | "hoehe"> = { breite: 1000, hoehe: 1000 };
  let drehung = START_DREHUNG, neigung = START_NEIGUNG, zoom = 1;
  /* Where the rotation is headed. Turning happens only in quarter turns and
     only through the buttons and keys: a plan has four sides one looks at it
     from, not three hundred and sixty. Free rotation on the pointer meant
     every grab at the building tilted it first — one wanted to move it and
     had turned it. */
  let drehZiel = drehung;
  /* Where the camera looks. With a fixed origin one could turn and zoom but
     not move the view, and every corner of a large building was reachable
     only by way of a rotation. */
  const blick = new THREE.Vector3(0, 0, 0);

  const breite = () => Math.max(1, buehne.clientWidth);
  const hoehe = () => Math.max(1, buehne.clientHeight);

  /* How large the building stands on the stage must not hang on a guessed
   * number. Eight colleagues make a different house than a hundred and
   * forty, and both are seen from another angle by the same camera — a fixed
   * extent lets one float and crops the other.
   *
   * So it is measured: the eight corners of the building are turned into
   * the viewing direction, and the camera gets exactly the extent the widest
   * and the highest point need, plus a hand's breadth of air. `zoom` is then
   * what it should be — the wheel under the finger, not the base setting. */
  function stellen(): void {
    const a = breite() / hoehe();
    const richtung = new THREE.Vector3(
      Math.sin(drehung) * Math.cos(neigung),
      Math.sin(neigung),
      Math.cos(drehung) * Math.cos(neigung));
    const grob = Math.max(plan.breite, plan.hoehe) * 3;
    kamera.position.copy(richtung).multiplyScalar(grob);
    kamera.lookAt(0, 0, 0);
    kamera.updateMatrixWorld(true);

    /* The corners in world coordinates: the building stands around its own
       middle because the world is offset accordingly. 150 is the height of
       the outer wall plus whatever sticks out above it. */
    const hx = plan.breite / 2, hz = plan.hoehe / 2;
    let mx = 1, my = 1;
    const sicht = new THREE.Matrix4().copy(kamera.matrixWorld).invert();
    const e = new THREE.Vector3();
    for (const x of [-hx, hx]) for (const y of [0, 150]) for (const z of [-hz, hz]) {
      e.set(x, y, z).applyMatrix4(sicht);
      mx = Math.max(mx, Math.abs(e.x));
      my = Math.max(my, Math.abs(e.y));
    }
    const d = (Math.max(mx / a, my) * 1.06) / zoom;
    kamera.left = -d * a; kamera.right = d * a; kamera.top = d; kamera.bottom = -d;
    const abstand = Math.max(plan.breite, plan.hoehe) * 3;
    kamera.near = -abstand * 3; kamera.far = abstand * 3;
    /* Measured around the building, looking at `blick`. Mixing the two would
       mean: whoever pans sideways changes the scale. */
    kamera.position.copy(richtung).multiplyScalar(abstand).add(blick);
    kamera.lookAt(blick);
    kamera.updateProjectionMatrix();
    kamera.updateMatrixWorld(true);
  }

  /* Panning means moving the look target along the axes that lie horizontal
   * and vertical on the screen — otherwise the building slides away at an
   * angle under the finger as soon as one has turned. How much world a pixel
   * is: the height of the view divided by the height of the stage.
   *
   * And it is bounded. A view that can be pushed into the void is not a tool
   * but a trap: one cannot find the building again and does not know which
   * way to look. */
  const _re = new THREE.Vector3(), _ho = new THREE.Vector3(), _vo = new THREE.Vector3();
  function schieben(dx: number, dy: number): void {
    kamera.matrixWorld.extractBasis(_re, _ho, _vo);
    const jePixel = (kamera.top - kamera.bottom) / hoehe();
    blick.addScaledVector(_re, -dx * jePixel).addScaledVector(_ho, dy * jePixel);
    zaeumen();
    stellen();
  }
  function zaeumen(): void {
    const hx = plan.breite / 2, hz = plan.hoehe / 2;
    blick.x = Math.max(-hx, Math.min(hx, blick.x));
    blick.z = Math.max(-hz, Math.min(hz, blick.z));
    blick.y = Math.max(0, Math.min(200, blick.y));
  }

  /* Zooming onto a point instead of the middle: what lies under the pointer
     should stay there. Without it one zooms past the spot one wants to see. */
  function zoomen(faktor: number, px?: number, py?: number): void {
    const vorher = zoom;
    zoom = Math.max(0.6, Math.min(4.5, zoom * faktor));
    if (zoom === vorher) return;
    if (px != null && py != null) {
      const r = buehne.getBoundingClientRect();
      const mx = px - r.left - r.width / 2, my = py - r.top - r.height / 2;
      stellen();
      /* For the point under the pointer to stay put, the look target has to
         follow by exactly the fraction by which the scale grew. */
      const k = zoom / vorher - 1;
      schieben(-mx * k, -my * k);
      return;
    }
    stellen();
  }

  function drehen(richtung: 1 | -1): void {
    drehZiel += (richtung * Math.PI) / 2;
  }

  function zurueck(): void {
    const warGedreht = drehung !== START_DREHUNG;
    drehung = drehZiel = START_DREHUNG;
    neigung = START_NEIGUNG;
    zoom = 1;
    blick.set(0, 0, 0);
    stellen();
    /* A reset across a quarter view changes which walls face the camera; the
       next tick reports it like a completed turn. */
    if (warGedreht) zurueckGedreht = true;
  }
  let zurueckGedreht = false;

  /* The ride to the next side: exponential, not linear, so it sets off fast
     and arrives softly — and it runs on wall-clock time, not on the pace of
     the life in the office, otherwise the camera would stand still when that
     is paused. */
  function tick(roh: number): boolean {
    if (zurueckGedreht) { zurueckGedreht = false; return true; }
    if (drehung === drehZiel) return false;
    const rest = drehZiel - drehung;
    drehung = Math.abs(rest) < 0.0015 || ruhig ? drehZiel : drehung + rest * Math.min(1, roh * 9);
    stellen();
    /* Arrived: the walls now facing the camera are cut down, the others grow
       back — the building is rebuilt once for that. During the ride it stays
       as it was; a rebuild per frame would flicker, and the ride takes half a
       second. */
    return drehung === drehZiel;
  }

  function planSetzen(p: Plan): void {
    plan = { breite: p.breite, hoehe: p.hoehe };
    zaeumen();
    stellen();
  }

  /* ── The controls ─────────────────────────────────────────────────────────
   * Three movements, and each must be reachable in more than one way — who
   * does not find one of them takes the view for nailed down.
   *
   *   Pan     drag · two fingers · wheel · arrow keys · double-click on the
   *           spot
   *   Scale   ⌘/Ctrl+wheel · + and − · the two buttons
   *   Turn    ↺ ↻ · Q/E — in quarter turns, never on the pointer
   *
   * Dragging pans, always. The first draft turned on drag and panned only
   * with Shift or the right button — the gesture everyone makes first did
   * what one wants least, and tilted the building too. A floor plan is a
   * map: what one grabs stays under the finger.
   *
   * The wheel does not pan and zoom at once, because both would sit on the
   * same event and the pointer would do one thing one time and the other the
   * next. It pans — the gesture a trackpad gives — and with Cmd or Ctrl held
   * it zooms, as everywhere else.
   *
   * Dragging starts only after four pixels. Before, every click on a face
   * was a camera drag — nobody COULD be clicked, because the listener sits on
   * the whole stage and the figures above it got no events. */
  function anbinden(onKarteZu: () => void): () => void {
    const zeiger = new Map<number, { x: number; y: number }>();
    let zieht: { weit: number; faengt?: boolean } | null = null;
    let kneif = { weite: 0, x: 0, y: 0 };
    const vorRad = new THREE.Vector3();
    const schiebt = (an: boolean) => buehne.classList.toggle("schiebt", an);

    const runter = (e: PointerEvent) => {
      onKarteZu();
      zeiger.set(e.pointerId, { x: e.clientX, y: e.clientY });
      if (zeiger.size === 2) { zieht = null; schiebt(true); return; }
      if (zeiger.size > 2) return;
      zieht = { weit: 0 };
    };

    const bewegt = (e: PointerEvent) => {
      const alt = zeiger.get(e.pointerId);
      if (!alt) return;
      const vorX = alt.x, vorY = alt.y;
      alt.x = e.clientX; alt.y = e.clientY;

      /* Two fingers: the distance makes the scale, the midpoint pans. */
      if (zeiger.size === 2) {
        const [a, b] = [...zeiger.values()];
        const weite = Math.hypot(a.x - b.x, a.y - b.y);
        const mitteX = (a.x + b.x) / 2, mitteY = (a.y + b.y) / 2;
        if (kneif.weite) {
          schieben(mitteX - kneif.x, mitteY - kneif.y);
          if (weite > 8 && kneif.weite > 8) zoomen(weite / kneif.weite, mitteX, mitteY);
        }
        kneif = { weite, x: mitteX, y: mitteY };
        return;
      }

      if (!zieht) return;
      const dx = e.clientX - vorX, dy = e.clientY - vorY;
      zieht.weit += Math.abs(dx) + Math.abs(dy);
      if (zieht.weit < 4) return;
      if (!zieht.faengt) {
        zieht.faengt = true;
        /* The capture holds the drag when the pointer leaves the stage. It
           can fail when the pointer no longer belongs to the browser — that
           must not end the drag. */
        try { buehne.setPointerCapture(e.pointerId); } catch { /* not needed */ }
        schiebt(true);
      }
      schieben(dx, dy);
    };

    const loslassen = (e: PointerEvent) => {
      zeiger.delete(e.pointerId);
      if (zeiger.size < 2) kneif = { weite: 0, x: 0, y: 0 };
      if (zeiger.size === 0) { zieht = null; schiebt(false); }
    };

    /* Dragging with the right button must not open a menu. */
    const menue = (e: MouseEvent) => e.preventDefault();

    const rad = (e: WheelEvent) => {
      if (e.ctrlKey || e.metaKey) {
        /* A trackpad pinch reports fractions, a mouse wheel reports 120 at
           once. Uncapped, a single notch turns the house into one room. */
        e.preventDefault();
        const d = Math.max(-40, Math.min(40, e.deltaY));
        zoomen(Math.pow(0.99, d), e.clientX, e.clientY);
        return;
      }
      /* Pan — but the page is not locked in. The stage is almost as tall as
         the window; whoever wants to scroll past it would have no way down
         left and would first have to move the pointer out. So: as long as
         panning moves something, the wheel belongs to the stage. At the stop
         it hands the wheel back and the page scrolls on — the same handover a
         text field makes at the end of its content. */
      vorRad.copy(blick);
      schieben(-e.deltaX, -e.deltaY);
      if (!blick.equals(vorRad)) e.preventDefault();
    };

    /* A double-click moves the clicked spot to the middle. For a building
       longer than the stage is wide that is the shortest way into a corner —
       panning would take several strokes. */
    const strahl = new THREE.Raycaster(), boden = new THREE.Plane(new THREE.Vector3(0, 1, 0), 0);
    const doppel = (e: MouseEvent) => {
      const r = buehne.getBoundingClientRect();
      strahl.setFromCamera(new THREE.Vector2(
        ((e.clientX - r.left) / r.width) * 2 - 1, -((e.clientY - r.top) / r.height) * 2 + 1), kamera);
      const treffer = new THREE.Vector3();
      if (!strahl.ray.intersectPlane(boden, treffer)) return;
      blick.copy(treffer);
      zaeumen();
      stellen();
    };

    /* The keyboard is the way for everyone who cannot or will not drag. It
       acts only while nobody is typing in a field. */
    const taste = (e: KeyboardEvent) => {
      if (e.key === "Escape") { onKarteZu(); return; }
      if (e.metaKey || e.ctrlKey || e.altKey) return;
      const z = e.target as HTMLElement | null;
      if (z && (z.tagName === "INPUT" || z.tagName === "TEXTAREA" || z.tagName === "SELECT" || z.isContentEditable)) return;
      const weit = e.shiftKey ? 160 : 55;
      const taten: Record<string, () => void> = {
        ArrowLeft: () => schieben(weit, 0), ArrowRight: () => schieben(-weit, 0),
        ArrowUp: () => schieben(0, weit), ArrowDown: () => schieben(0, -weit),
        q: () => drehen(1), e: () => drehen(-1),
        "+": () => zoomen(1.16), "=": () => zoomen(1.16), "-": () => zoomen(1 / 1.16),
        "0": zurueck,
      };
      const t = taten[e.key] || taten[e.key.toLowerCase()];
      if (t) { e.preventDefault(); t(); }
    };

    buehne.addEventListener("pointerdown", runter);
    buehne.addEventListener("pointermove", bewegt);
    buehne.addEventListener("contextmenu", menue);
    buehne.addEventListener("wheel", rad, { passive: false });
    buehne.addEventListener("dblclick", doppel);
    window.addEventListener("pointerup", loslassen);
    window.addEventListener("pointercancel", loslassen);
    window.addEventListener("keydown", taste);
    return () => {
      buehne.removeEventListener("pointerdown", runter);
      buehne.removeEventListener("pointermove", bewegt);
      buehne.removeEventListener("contextmenu", menue);
      buehne.removeEventListener("wheel", rad);
      buehne.removeEventListener("dblclick", doppel);
      window.removeEventListener("pointerup", loslassen);
      window.removeEventListener("pointercancel", loslassen);
      window.removeEventListener("keydown", taste);
      schiebt(false);
    };
  }

  stellen();
  return {
    kamera,
    planSetzen,
    stellen,
    drehen,
    schieben,
    zoomen,
    zurueck,
    tick,
    get drehung() { return drehung; },
    anbinden,
    groesse: stellen,
  };
}

/* ── The renderer ──────────────────────────────────────────────────────── */

/** Creates the WebGL renderer and puts its canvas first into `buehne`.
 *  Returns null when WebGL is unavailable; the component shows a fallback. */
export function erschaffeMaler(buehne: HTMLElement): THREE.WebGLRenderer | null {
  let maler: THREE.WebGLRenderer;
  try {
    maler = new THREE.WebGLRenderer({ antialias: true, alpha: false });
  } catch {
    return null;
  }
  /* The palette in mal.ts was tuned on three r128, where a hex colour went
     into the shader as it was written and only the output was encoded to
     sRGB. Since r152 colour management converts every hex colour to linear
     first, which renders the same palette darker and more saturated. Turning
     it off restores the look the palette was mixed for. It is a global
     switch; the office is the only three.js user in the app. */
  THREE.ColorManagement.enabled = false;
  maler.outputColorSpace = THREE.SRGBColorSpace;
  maler.setPixelRatio(Math.min(2, window.devicePixelRatio || 1));
  maler.shadowMap.enabled = true;
  maler.shadowMap.type = THREE.PCFSoftShadowMap;
  buehne.prepend(maler.domElement);
  return maler;
}

/** On resize: the canvas follows the stage. The camera's extent is
 *  `Steuerung.groesse`, called beside this — the renderer does not own it. */
export function groesse(maler: THREE.WebGLRenderer, _kamera: THREE.Camera, buehne: HTMLElement): void {
  maler.setSize(Math.max(1, buehne.clientWidth), Math.max(1, buehne.clientHeight), false);
}
