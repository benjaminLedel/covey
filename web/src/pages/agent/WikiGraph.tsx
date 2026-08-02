import { useState, useRef, useEffect, useCallback } from "react";
import { useTranslation } from "react-i18next";
import {
  type MemoryEntry,
} from "../../api";

export type GraphNode = { page: MemoryEntry; x: number; y: number; vx: number; vy: number; r: number; deg: number };
export type GraphEdge = { a: GraphNode; b: GraphNode };

// forceLayout ist eine kleine Kräfte-Simulation (Abstoßung zwischen allen
// Knoten, Federn entlang der Verweise, sanfte Mitte). Dependency-frei und in
// einem Rutsch gerechnet — bei ein paar hundert Seiten reicht das, und es
// erspart eine Graph-Bibliothek im Bundle.
function forceLayout(nodes: GraphNode[], edges: GraphEdge[], w: number, h: number, iters: number) {
  nodes.forEach((n, i) => {
    const a = (i / Math.max(nodes.length, 1)) * Math.PI * 2;
    n.x = w / 2 + Math.cos(a) * w * 0.3;
    n.y = h / 2 + Math.sin(a) * h * 0.3;
    n.vx = 0;
    n.vy = 0;
  });
  const k = Math.sqrt((w * h) / Math.max(nodes.length, 1)) * 0.62;
  for (let it = 0; it < iters; it++) {
    for (let i = 0; i < nodes.length; i++) {
      for (let j = i + 1; j < nodes.length; j++) {
        const a = nodes[i];
        const b = nodes[j];
        let dx = a.x - b.x;
        let dy = a.y - b.y;
        let d2 = dx * dx + dy * dy;
        if (d2 < 0.01) {
          dx = (i - j) * 0.1 + 0.05;
          dy = 0.05;
          d2 = 0.01;
        }
        const d = Math.sqrt(d2);
        const f = (k * k) / d2;
        a.vx += (dx / d) * f;
        a.vy += (dy / d) * f;
        b.vx -= (dx / d) * f;
        b.vy -= (dy / d) * f;
      }
    }
    edges.forEach((e) => {
      const dx = e.b.x - e.a.x;
      const dy = e.b.y - e.a.y;
      const d = Math.max(Math.sqrt(dx * dx + dy * dy), 0.01);
      const f = (d * d) / k / 14;
      e.a.vx += (dx / d) * f;
      e.a.vy += (dy / d) * f;
      e.b.vx -= (dx / d) * f;
      e.b.vy -= (dy / d) * f;
    });
    nodes.forEach((n) => {
      n.vx += (w / 2 - n.x) * 0.006;
      n.vy += (h / 2 - n.y) * 0.006;
      n.x += Math.max(-14, Math.min(14, n.vx));
      n.y += Math.max(-14, Math.min(14, n.vy));
      n.vx *= 0.82;
      n.vy *= 0.82;
      n.x = Math.max(18, Math.min(w - 18, n.x));
      n.y = Math.max(18, Math.min(h - 18, n.y));
    });
  }
}

export function buildGraph(pages: MemoryEntry[]) {
  const nodes: GraphNode[] = pages.map((p) => ({ page: p, x: 0, y: 0, vx: 0, vy: 0, r: 4, deg: 0 }));
  const idx = new Map(nodes.map((n) => [n.page.slug, n]));
  const edges: GraphEdge[] = [];
  pages.forEach((p) => {
    const a = idx.get(p.slug)!;
    (p.links ?? []).forEach((l) => {
      const b = idx.get(l);
      if (b && b !== a) {
        edges.push({ a, b });
        a.deg++;
        b.deg++;
      }
    });
  });
  nodes.forEach((n) => (n.r = 4 + Math.min(n.deg, 8) * 1.5));
  return { nodes, edges };
}

// WikiGraph zeichnet die Verlinkung — die Struktur, die als Liste unsichtbar
// bleibt. Canvas statt SVG: bei mehreren hundert Knoten ist das der Unterschied
// zwischen flüssig und zäh.
export function WikiGraph({
  pages,
  current,
  onOpen,
  height,
  labels = true,
}: {
  pages: MemoryEntry[];
  current?: string;
  onOpen?: (slug: string) => void;
  height: number;
  labels?: boolean;
}) {
  const { t } = useTranslation();
  const ref = useRef<HTMLCanvasElement | null>(null);
  const model = useRef<{ nodes: GraphNode[]; edges: GraphEdge[] } | null>(null);
  const [hover, setHover] = useState<GraphNode | null>(null);
  const [tip, setTip] = useState<{ x: number; y: number; text: string } | null>(null);

  const draw = useCallback(() => {
    const cv = ref.current;
    if (!cv || !cv.parentElement) return;
    const dpr = window.devicePixelRatio || 1;
    const w = cv.parentElement.clientWidth;
    if (w === 0) return;
    cv.style.height = height + "px";
    cv.width = w * dpr;
    cv.height = height * dpr;
    const c = cv.getContext("2d");
    if (!c) return;
    c.setTransform(dpr, 0, 0, dpr, 0, 0);
    if (!model.current) {
      model.current = buildGraph(pages);
      forceLayout(model.current.nodes, model.current.edges, w, height, 320);
    }
    const { nodes, edges } = model.current;
    const cs = getComputedStyle(document.documentElement);
    const cEdge = cs.getPropertyValue("--border-strong") || "#ccc";
    const cNode = cs.getPropertyValue("--text-secondary") || "#666";
    const cAcc = cs.getPropertyValue("--text-accent") || "#185fa5";
    const cMut = cs.getPropertyValue("--text-muted") || "#999";

    c.clearRect(0, 0, w, height);
    const near = new Set<GraphNode>();
    if (hover) {
      near.add(hover);
      edges.forEach((e) => {
        if (e.a === hover) near.add(e.b);
        if (e.b === hover) near.add(e.a);
      });
    }
    c.lineWidth = 1;
    edges.forEach((e) => {
      const hot = hover != null && (e.a === hover || e.b === hover);
      c.strokeStyle = hot ? cAcc : cEdge;
      c.globalAlpha = hover && !hot ? 0.25 : 1;
      c.beginPath();
      c.moveTo(e.a.x, e.a.y);
      c.lineTo(e.b.x, e.b.y);
      c.stroke();
    });
    c.globalAlpha = 1;
    nodes.forEach((n) => {
      const isSelf = n.page.slug === current;
      const isHub = n.deg >= 3;
      c.globalAlpha = hover && !near.has(n) ? 0.3 : 1;
      if (n.deg === 0) c.globalAlpha *= 0.5;
      c.fillStyle = isSelf || isHub ? cAcc : n.deg === 0 ? cMut : cNode;
      c.beginPath();
      c.arc(n.x, n.y, isSelf ? n.r + 2 : n.r, 0, 6.284);
      c.fill();
      if (labels && (isHub || isSelf || near.has(n))) {
        c.globalAlpha = 1;
        c.fillStyle = cNode;
        c.font = "11px " + (cs.getPropertyValue("--sans") || "sans-serif");
        c.textAlign = "center";
        const name = (n.page.title || n.page.slug).slice(0, 26);
        c.fillText(name, n.x, n.y - n.r - 5);
      }
    });
    c.globalAlpha = 1;
  }, [pages, current, hover, height, labels]);

  useEffect(() => {
    model.current = null;
    draw();
  }, [pages, height]); // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => {
    draw();
  }, [draw]);
  useEffect(() => {
    const onResize = () => {
      model.current = null;
      draw();
    };
    window.addEventListener("resize", onResize);
    return () => window.removeEventListener("resize", onResize);
  }, [draw]);

  const pick = (ev: React.MouseEvent<HTMLCanvasElement>): GraphNode | null => {
    const m = model.current;
    if (!m) return null;
    const r = ev.currentTarget.getBoundingClientRect();
    const x = ev.clientX - r.left;
    const y = ev.clientY - r.top;
    let best: GraphNode | null = null;
    let bd = Infinity;
    m.nodes.forEach((n) => {
      const d = (n.x - x) ** 2 + (n.y - y) ** 2;
      if (d < bd) {
        bd = d;
        best = n;
      }
    });
    return bd < 400 ? best : null;
  };

  if (pages.length === 0) {
    return <p className="muted p-4 text-[12.5px]">{t("agent.memory.graphEmpty")}</p>;
  }

  return (
    <div style={{ position: "relative" }}>
      <canvas
        ref={ref}
        style={{ display: "block", width: "100%", cursor: hover ? "pointer" : "default" }}
        onMouseMove={(ev) => {
          const n = pick(ev);
          setHover(n);
          if (n) {
            const r = ev.currentTarget.getBoundingClientRect();
            setTip({
              x: Math.min(ev.clientX - r.left + 12, r.width - 240),
              y: ev.clientY - r.top + 12,
              text: (n.page.title || n.page.slug) + " · " + t("agent.memory.refs", { count: n.deg }),
            });
          } else setTip(null);
        }}
        onMouseLeave={() => {
          setHover(null);
          setTip(null);
        }}
        onClick={(ev) => {
          const n = pick(ev);
          if (n && onOpen) onOpen(n.page.slug);
        }}
      />
      {tip && (
        <div className="wiki-tip" style={{ left: tip.x, top: tip.y }}>
          {tip.text}
        </div>
      )}
    </div>
  );
}

// Die Vorgangs-Präfixe, die die Control Plane in wiki_log.summary schreibt
// (internal/memory). Geschlossene Liste, exakt abgeglichen — eine Regel wie
// "alles bis zum ersten Doppelpunkt" schnitte mitten in Titel hinein, die
