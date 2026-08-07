import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";

/* Geteilte Bausteine der öffentlichen Website (Chrome, Animationen, Icons).
   Aus der früheren Einseiten-Landing (pages/Login.tsx) extrahiert, damit
   Home, Funktion und die Produktseiten dieselbe Design-Sprache teilen. */

/* Covey = ein Schwarm — drei Vögel in Flugformation als Wortmarke. */
export function BirdMark({ size = 84 }: { size?: number }) {
  return (
    <span
      aria-hidden="true"
      style={{
        width: size,
        height: size,
        borderRadius: size * 0.32,
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        background:
          "linear-gradient(140deg, #d98a6a 0%, var(--clay) 55%, #b25f41 100%)",
        boxShadow:
          "0 10px 28px rgba(178, 95, 65, 0.34), 0 1px 2px rgba(30, 28, 23, 0.2), inset 0 1px 0 rgba(255, 255, 255, 0.28)",
      }}
    >
      <svg
        viewBox="0 0 24 24"
        width={size * 0.62}
        height={size * 0.62}
        fill="none"
        stroke="#fff"
        strokeWidth={1.7}
        strokeLinecap="round"
        strokeLinejoin="round"
      >
        <path d="M7 15 Q9.75 11.8 12.5 15 Q15.25 11.8 18 15" />
        <path d="M3.5 10 Q5.5 7.7 7.5 10 Q9.5 7.7 11.5 10" />
        <path d="M13 8 Q14.5 6.3 16 8 Q17.5 6.3 19 8" />
      </svg>
    </span>
  );
}

/* Ein einzelner Vogel-Glyph für den Hintergrund-Schwarm. */
export function Bird({
  top,
  left,
  size,
  delay,
}: {
  top: string;
  left: string;
  size: number;
  delay: string;
}) {
  return (
    <svg
      viewBox="0 0 18 6"
      width={size}
      height={size / 3}
      style={{ top, left, animationDelay: delay }}
    >
      <path d="M1 5 Q4.5 0.5 8 5 Q11.5 0.5 15 5" />
    </svg>
  );
}

/* Lebender Schwarm (Boids) auf Canvas: Kohäsion, Ausrichtung, Abstand —
   und sanftes Ausweichen vor dem Mauszeiger. Läuft nicht bei
   prefers-reduced-motion. */
// Der Ref-Typ trägt seit React 19 das null aus useRef(null) mit — der Body
// prüft es ohnehin ab, bevor er auf das Canvas geht.
export function useBoids(ref: React.RefObject<HTMLCanvasElement | null>) {
  useEffect(() => {
    const canvas = ref.current;
    if (!canvas) return;
    if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) return;
    const ctx = canvas.getContext("2d");
    if (!ctx) return;

    const DPR = Math.min(window.devicePixelRatio || 1, 2);
    let w = 0;
    let h = 0;
    const resize = () => {
      w = canvas.width = window.innerWidth * DPR;
      h = canvas.height = window.innerHeight * DPR;
    };
    resize();
    window.addEventListener("resize", resize);

    const N = window.innerWidth < 700 ? 55 : 110;
    const boids = Array.from({ length: N }, () => ({
      x: Math.random() * window.innerWidth * DPR,
      y: Math.random() * window.innerHeight * DPR,
      vx: (Math.random() - 0.5) * 2 * DPR,
      vy: (Math.random() - 0.5) * 2 * DPR,
    }));
    const mouse = { x: -1e9, y: -1e9 };
    const onMove = (e: PointerEvent) => {
      mouse.x = e.clientX * DPR;
      mouse.y = e.clientY * DPR;
    };
    const onLeave = () => {
      mouse.x = -1e9;
      mouse.y = -1e9;
    };
    window.addEventListener("pointermove", onMove);
    window.addEventListener("pointerleave", onLeave);

    const SEE = (85 * DPR) ** 2;
    const NEAR = (24 * DPR) ** 2;
    let raf = 0;
    const step = () => {
      ctx.clearRect(0, 0, w, h);
      for (const b of boids) {
        let cx = 0, cy = 0, ax = 0, ay = 0, sx = 0, sy = 0, n = 0;
        for (const o of boids) {
          if (o === b) continue;
          const dx = o.x - b.x;
          const dy = o.y - b.y;
          const d2 = dx * dx + dy * dy;
          if (d2 < SEE) {
            cx += o.x; cy += o.y; ax += o.vx; ay += o.vy; n++;
            if (d2 < NEAR) { sx -= dx; sy -= dy; }
          }
        }
        if (n) {
          b.vx += (cx / n - b.x) * 0.0004 + (ax / n - b.vx) * 0.045 + sx * 0.004;
          b.vy += (cy / n - b.y) * 0.0004 + (ay / n - b.vy) * 0.045 + sy * 0.004;
        }
        const mdx = b.x - mouse.x;
        const mdy = b.y - mouse.y;
        const md2 = mdx * mdx + mdy * mdy;
        if (md2 < (150 * DPR) ** 2) {
          const md = Math.sqrt(md2) || 1;
          b.vx += (mdx / md) * 0.5 * DPR;
          b.vy += (mdy / md) * 0.5 * DPR;
        }
        const sp = Math.hypot(b.vx, b.vy) || 1;
        const max = 1.7 * DPR;
        const min = 0.6 * DPR;
        if (sp > max) { b.vx = (b.vx / sp) * max; b.vy = (b.vy / sp) * max; }
        else if (sp < min) { b.vx = (b.vx / sp) * min; b.vy = (b.vy / sp) * min; }
        b.x += b.vx;
        b.y += b.vy;
        const m = 24 * DPR;
        if (b.x < -m) b.x = w + m;
        if (b.x > w + m) b.x = -m;
        if (b.y < -m) b.y = h + m;
        if (b.y > h + m) b.y = -m;

        const ang = Math.atan2(b.vy, b.vx);
        const s = 3.4 * DPR;
        ctx.save();
        ctx.translate(b.x, b.y);
        ctx.rotate(ang);
        ctx.strokeStyle = "rgba(178, 95, 65, 0.30)";
        ctx.lineWidth = 1.1 * DPR;
        ctx.lineCap = "round";
        ctx.beginPath();
        ctx.moveTo(-s, -s * 0.65);
        ctx.lineTo(0, 0);
        ctx.lineTo(-s, s * 0.65);
        ctx.stroke();
        ctx.restore();
      }
      raf = requestAnimationFrame(step);
    };
    raf = requestAnimationFrame(step);

    return () => {
      cancelAnimationFrame(raf);
      window.removeEventListener("resize", resize);
      window.removeEventListener("pointermove", onMove);
      window.removeEventListener("pointerleave", onLeave);
    };
  }, [ref]);
}

/* Fixer Hintergrund für alle öffentlichen Seiten: Aurora + Boid-Schwarm +
   ruhende Vogel-Glyphen. Einmal im PublicSite gemountet, seitenübergreifend. */
export function PublicBackground() {
  const boidsRef = useRef<HTMLCanvasElement>(null);
  useBoids(boidsRef);
  return (
    <>
      <div className="aurora" aria-hidden="true">
        <i /><i /><i />
      </div>
      <canvas ref={boidsRef} className="boids" aria-hidden="true" />
      <div className="login-flock" aria-hidden="true">
        <Bird top="10%" left="12%" size={44} delay="0s" />
        <Bird top="18%" left="78%" size={62} delay="1.4s" />
        <Bird top="42%" left="6%" size={54} delay="0.7s" />
        <Bird top="52%" left="88%" size={38} delay="2.1s" />
        <Bird top="30%" left="92%" size={30} delay="1.1s" />
      </div>
    </>
  );
}

/* Hero-Org-Chart: baut sich selbst auf — der Team-Lead und seine Agenten
   poppen gestaffelt ins Bild, die Verbindungslinien zeichnen sich, ein
   Aufgaben-Punkt fließt vom Lead zu einem Agenten, Status-Dots pulsieren.
   Endlos-Loop; bei prefers-reduced-motion statisch aufgebaut. */
export function HeroOrg() {
  // Menschen und Agenten mit Namen — eine zusammenarbeitende Organisation,
  // nicht ein einzelner Nutzer.
  const HUMANS = [
    { x: 45, name: "Mara", role: "Team-Lead", delay: "0s" },
    { x: 215, name: "Jonas", role: "Security", delay: "0.12s" },
  ];
  const AGENTS = [
    { cx: 70, name: "Ada", sys: "Zammad", delay: "0.62s" },
    { cx: 190, name: "Kilo", sys: "GitLab", delay: "0.74s" },
    { cx: 310, name: "Nova", sys: "E-Mail", delay: "0.86s" },
  ];
  const links = [
    { d: "M105 74 V140", delay: "0.28s" },
    { d: "M275 74 V140", delay: "0.32s" },
    { d: "M70 140 H310", delay: "0.44s" },
    { d: "M70 140 V200", delay: "0.55s" },
    { d: "M190 140 V200", delay: "0.6s" },
    { d: "M310 140 V200", delay: "0.65s" },
  ];
  // Zusammenarbeit: Peer-Linie zwischen den Menschen, Übergaben im Agenten-Team.
  const colinks = [
    { d: "M165 52 H215", delay: "0.9s" },
    { d: "M118 229 H142", delay: "1s" },
    { d: "M238 229 H262", delay: "1.05s" },
  ];
  return (
    <svg className="hero-org" viewBox="0 0 380 300" preserveAspectRatio="xMidYMid meet" aria-hidden="true">
      <g className="org-links">
        {links.map((l) => (
          <path key={l.d} className="org-link" d={l.d} pathLength={1} style={{ animationDelay: l.delay }} />
        ))}
      </g>
      <g className="org-colinks">
        {colinks.map((l) => (
          <path key={l.d} className="org-colink" d={l.d} style={{ animationDelay: l.delay }} />
        ))}
      </g>

      {/* Menschen */}
      {HUMANS.map((h) => (
        <g key={h.name} className="org-node org-lead" style={{ animationDelay: h.delay }}>
          <rect x={h.x} y="30" width="120" height="44" rx="12" />
          <circle className="org-avatar" cx={h.x + 24} cy="52" r="8" />
          <path
            className="org-avatar-glyph"
            d={`M${h.x + 24} 49a2.3 2.3 0 1 0 0 4.6 2.3 2.3 0 0 0 0-4.6M${h.x + 20} 56.6a4 4 0 0 1 8 0`}
          />
          <text className="org-label" x={h.x + 42} y="49">{h.name}</text>
          <text className="org-sub" x={h.x + 42} y="60">{h.role}</text>
        </g>
      ))}

      {/* Agenten */}
      {AGENTS.map((a) => (
        <g key={a.name} className="org-node" style={{ animationDelay: a.delay }}>
          <rect x={a.cx - 48} y="200" width="96" height="58" rx="12" />
          <path className="org-bird" d={`M${a.cx - 34} 221 q4.5 -4.6 9 0 q4.5 -4.6 9 0`} />
          <circle className="org-status" cx={a.cx + 34} cy="217" r="3.2" />
          <text className="org-label" x={a.cx} y="241" textAnchor="middle">{a.name}</text>
          <text className="org-sub" x={a.cx} y="251.5" textAnchor="middle">{a.sys}</text>
        </g>
      ))}

      {/* Aufgaben fließen: Delegation nach unten, Übergabe quer durchs Team. */}
      <circle className="org-token" r="3.4" cx="0" cy="0" />
      <circle className="org-token2" r="3.4" cx="0" cy="0" />
    </svg>
  );
}

/* Rückwärtskompatibler Alias. */
export const HeroFlock = HeroOrg;

/* Rotierendes Wort im Hero — bei jedem Wechsel remountet der key die
   CSS-Einblendanimation. */
export function RotatingWord() {
  const { t } = useTranslation();
  const words = [1, 2, 3, 4, 5].map((n) => t(`landing.rot${n}`));
  const [i, setI] = useState(0);
  useEffect(() => {
    const id = setInterval(() => setI((v) => (v + 1) % 5), 2400);
    return () => clearInterval(id);
  }, []);
  return (
    <span className="rot-word" key={i}>
      {words[i]}
    </span>
  );
}

/* Scroll-Reveal: Elemente mit .reveal blenden ein, sobald sie sichtbar
   werden. dep (Pfad) sorgt dafür, dass beim Seitenwechsel neu beobachtet
   wird. Bei prefers-reduced-motion greift die CSS-Abschaltung. */
export function useReveal(dep?: unknown) {
  useEffect(() => {
    const els = document.querySelectorAll<HTMLElement>(".reveal:not(.in)");
    const io = new IntersectionObserver(
      (entries) => {
        for (const e of entries) {
          if (e.isIntersecting) {
            (e.target as HTMLElement).classList.add("in");
            io.unobserve(e.target);
          }
        }
      },
      { threshold: 0.12 },
    );
    els.forEach((el) => io.observe(el));
    return () => io.disconnect();
  }, [dep]);
}

/* Icon-Pfade im Stil der Sidebar-Nav (App.tsx / Mockup). Zentral, damit
   Home, Funktion und Produktseiten dieselben Glyphen teilen. */
export const icons: Record<string, React.JSX.Element> = {
  sitemap: (
    <>
      <rect x="9" y="3" width="6" height="5" rx="1" />
      <rect x="3" y="16" width="6" height="5" rx="1" />
      <rect x="15" y="16" width="6" height="5" rx="1" />
      <path d="M12 8v4M6 16v-2h12v2M12 12v2" />
    </>
  ),
  box: (
    <>
      <path d="M12 3l8 4.5v9L12 21l-8-4.5v-9z" />
      <path d="M4 7.5l8 4.5l8-4.5M12 12v9" />
    </>
  ),
  key: (
    <>
      <circle cx="8" cy="8" r="3.5" />
      <path d="M10.5 10.5L20 20M17 17l2-2M14 14l2 2" />
    </>
  ),
  shield: <path d="M12 3l7 3v5c0 5-3 8-7 10c-4-2-7-5-7-10V6z" />,
  list: (
    <>
      <path d="M9 6h11M9 12h11M9 18h11" />
      <path d="M4 6h.01M4 12h.01M4 18h.01" />
    </>
  ),
  eye: (
    <>
      <path d="M2.5 12S6 5.5 12 5.5 21.5 12 21.5 12 18 18.5 12 18.5 2.5 12 2.5 12z" />
      <circle cx="12" cy="12" r="3" />
    </>
  ),
  plug: (
    <>
      <path d="M9 3v5M15 3v5" />
      <path d="M7 8h10v3a5 5 0 0 1-10 0z" />
      <path d="M12 16v5" />
    </>
  ),
  brain: (
    <>
      <path d="M12 5a3 3 0 0 0-6 0 3 3 0 0 0-2 5 3 3 0 0 0 2 5 3 3 0 0 0 6 0z" />
      <path d="M12 5a3 3 0 0 1 6 0 3 3 0 0 1 2 5 3 3 0 0 1-2 5 3 3 0 0 1-6 0" />
      <path d="M12 5v14" />
    </>
  ),
  mail: (
    <>
      <rect x="3" y="5" width="18" height="14" rx="2" />
      <path d="M4 7l8 6 8-6" />
    </>
  ),
  mic: (
    <>
      <rect x="9" y="3" width="6" height="11" rx="3" />
      <path d="M6 11a6 6 0 0 0 12 0M12 17v4M9 21h6" />
    </>
  ),
  chart: (
    <>
      <path d="M4 20V4M4 20h16" />
      <path d="M8 20v-6M13 20V8M18 20v-9" />
    </>
  ),
  coins: (
    <>
      <ellipse cx="12" cy="6" rx="7" ry="3" />
      <path d="M5 6v6c0 1.7 3.1 3 7 3s7-1.3 7-3V6M5 12v6c0 1.7 3.1 3 7 3s7-1.3 7-3v-6" />
    </>
  ),
  net: (
    <>
      <circle cx="12" cy="12" r="9" />
      <path d="M3 12h18M12 3c2.5 2.6 2.5 15.4 0 18M12 3c-2.5 2.6-2.5 15.4 0 18" />
    </>
  ),
  spark: (
    <>
      <path d="M12 3v4M12 17v4M3 12h4M17 12h4" />
      <path d="M6.5 6.5l2.5 2.5M15 15l2.5 2.5M17.5 6.5L15 9M9 15l-2.5 2.5" />
    </>
  ),
  bolt: <path d="M13 2 4 14h6l-1 8 9-12h-6z" />,
  users: (
    <>
      <circle cx="9" cy="8" r="3.2" />
      <path d="M3.5 20a5.5 5.5 0 0 1 11 0" />
      <path d="M16 5.5a3 3 0 0 1 0 5.8M17 20a5.5 5.5 0 0 0-3-4.9" />
    </>
  ),
  git: (
    <>
      <circle cx="6" cy="6" r="2.4" />
      <circle cx="6" cy="18" r="2.4" />
      <circle cx="17" cy="9" r="2.4" />
      <path d="M6 8.4v7.2M6 12h6a3 3 0 0 0 3-3" />
    </>
  ),
  lock: (
    <>
      <rect x="4.5" y="10" width="15" height="10" rx="2" />
      <path d="M8 10V7a4 4 0 0 1 8 0v3" />
      <circle cx="12" cy="15" r="1.3" />
    </>
  ),
};
