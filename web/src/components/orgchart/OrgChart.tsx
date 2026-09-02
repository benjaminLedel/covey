// The org chart: a layered tree on a pannable, zoomable canvas.
//
// Reading and changing are two modes. In view mode the chart is a surface to
// read and to move around; nothing on it changes anything. Edit mode puts the
// controls on the cards — department and supervisor as selects, the lead as
// a deliberate switch that asks once, rename, colour and delete on the
// department — and keeps drag and drop as the accelerator, with the name of
// the target visible while dragging. Every change reports back in one status
// line, success and failure alike; before #177 a refused move snapped the
// card back and said nothing.
//
// Nodes are HTML (links, buttons, selects stay what they are for keyboard
// and screen reader), the connectors are one SVG under them, and both share
// one transform, so they zoom together.

import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router";
import { useTranslation } from "react-i18next";
import {
  isDraft,
  createDepartment, renameDepartment, deleteDepartment, setDepartmentColor,
  setAgentDepartment, setAgentSupervisor, setHumanDepartment, setHumanManager,
  addDepartmentLead, removeDepartmentLead,
  type Department, type OrgChart as OrgChartData,
} from "../../api";
import { Avatar } from "../person";
import { ConfirmDialog } from "../Modal";
import { layoutTree, type Placed } from "./layout";
import {
  buildModel, descendantsOf, memberBoss, memberDept, memberName, memberNodeId,
  ORG, UNASSIGNED, DEPT_TONES, type ChartNode, type Member, type Sizes,
} from "./model";

/* ── Sizes ────────────────────────────────────────────────────────────── */

// Fixed card sizes make the layout deterministic: the tree is placed before
// it is drawn, and a long name is cut with an ellipsis (the full name stays
// in the title and on the profile) rather than reflowing the whole chart.
const SIZES: Record<Mode, Sizes> = {
  view: { org: { w: 272, h: 62 }, dept: { w: 248, h: 66 }, member: { w: 248, h: 60 } },
  edit: { org: { w: 272, h: 62 }, dept: { w: 272, h: 112 }, member: { w: 344, h: 104 } },
};

type Mode = "view" | "edit";
type View = { x: number; y: number; k: number };
const ZOOM_MIN = 0.25;
const ZOOM_MAX = 2;
// Edit mode never shrinks: a select scaled to 0.72 is an 8px label and a
// 19px target, under the 24px WCAG 2.2 asks for. The controls keep their
// size and the chart is panned instead.
const EDIT_MIN = 1;
const PAD = 28;

/* ── Collapse state ───────────────────────────────────────────────────── */

// From this many nodes below, a branch starts closed — an organisation with a
// few dozen people is wallpaper otherwise. Whoever opens or closes a branch
// overrides that for this browser; the default holds only where nothing is
// recorded.
const AUTO_COLLAPSE_AT = 8;
const COLLAPSE_KEY = "covey.org.collapsed";

function useCollapse() {
  const [choice, setChoice] = useState<Record<string, boolean>>(() => {
    try {
      return JSON.parse(localStorage.getItem(COLLAPSE_KEY) || "{}") as Record<string, boolean>;
    } catch {
      return {};
    }
  });
  const isOpen = useCallback((id: string, size: number) => choice[id] ?? size < AUTO_COLLAPSE_AT, [choice]);
  const toggle = (id: string, size: number) =>
    setChoice((prev) => {
      const next = { ...prev, [id]: !(prev[id] ?? size < AUTO_COLLAPSE_AT) };
      try { localStorage.setItem(COLLAPSE_KEY, JSON.stringify(next)); } catch { /* private mode */ }
      return next;
    });
  return { isOpen, toggle };
}

/* ── Icons: drawn, one stroke ─────────────────────────────────────────── */

const Ico = ({ d, size = 16 }: { d: string; size?: number }) => (
  <svg viewBox="0 0 24 24" width={size} height={size} fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    <path d={d} />
  </svg>
);
const IC = {
  chevron: "M6 9l6 6 6-6",
  plus: "M12 5v14M5 12h14",
  minus: "M5 12h14",
  fit: "M4 9V4h5M15 4h5v5M20 15v5h-5M9 20H4v-5",
  trash: "M4 7h16M10 11v6M14 11v6M6 7l1 13h10l1-13M9 7V4h6v3",
  grip: "M9 6h.01M15 6h.01M9 12h.01M15 12h.01M9 18h.01M15 18h.01",
  lead: "M12 3l2.7 5.6 6.1.9-4.4 4.3 1 6.1L12 17l-5.4 2.9 1-6.1L3.2 9.5l6.1-.9z",
  pencil: "M4 20h4l10.5-10.5a2.1 2.1 0 0 0-3-3L5 17z M13 6l3 3",
};

/* ── Component ────────────────────────────────────────────────────────── */

type Notice = { kind: "ok" | "error"; text: string; at: number };
type Pending =
  | { kind: "lead"; member: Member; dept: Department; add: boolean }
  | { kind: "delete"; dept: Department };

export function OrgChart({ chart, orgName }: { chart: OrgChartData; orgName: string }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [mode, setMode] = useState<Mode>("view");
  const collapse = useCollapse();
  const [notice, setNotice] = useState<Notice | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [pending, setPending] = useState<Pending | null>(null);
  const [dragging, setDragging] = useState<Member | null>(null);
  const [showNew, setShowNew] = useState(false);

  const model = useMemo(() => buildModel(chart, collapse.isOpen, SIZES[mode]), [chart, collapse.isOpen, mode]);
  const layout = useMemo(() => layoutTree(model.root), [model]);
  const placed = useMemo(() => new Map(layout.nodes.map((n) => [n.id, n])), [layout]);

  const invalidate = () => qc.invalidateQueries({ queryKey: ["orgchart"] });
  const ok = (text: string) => setNotice({ kind: "ok", text, at: Date.now() });
  const fail = (e: unknown) => setNotice({ kind: "error", text: t("org.chart.notSaved", { error: (e as Error).message }), at: Date.now() });

  // A confirmation fades; a failure stays until the next change.
  useEffect(() => {
    if (notice?.kind !== "ok") return;
    const h = setTimeout(() => setNotice((n) => (n === notice ? null : n)), 7000);
    return () => clearTimeout(h);
  }, [notice]);

  /* ── Mutations ──────────────────────────────────────────────────────── */

  const byId = useMemo(() => new Map(model.members.map((m) => [m.id, m])), [model]);
  const deptById = useMemo(() => new Map(chart.departments.map((d) => [d.id, d])), [chart]);
  const nameOf = (id: string | null) => (id ? memberName(byId.get(id)!) : "");
  const deptName = (id: string | null) => (id ? deptById.get(id)?.name ?? "" : t("org.diagramUnassigned"));

  // A move sets supervisor and department together, so a report lands in its
  // supervisor's department. Two calls; if the second is refused the first
  // has already happened, and the status line says which part failed.
  const moveMut = useMutation({
    mutationFn: async ({ member, deptId, supervisorId }: { member: Member; deptId: string | null; supervisorId: string | null }) => {
      setBusy(member.id);
      if (member.kind === "agent") {
        await setAgentSupervisor(member.id, supervisorId);
        await setAgentDepartment(member.id, deptId);
      } else {
        await setHumanManager(member.id, supervisorId);
        await setHumanDepartment(member.id, deptId);
      }
      return { member, deptId, supervisorId };
    },
    onSuccess: ({ member, deptId, supervisorId }) => {
      const name = memberName(member);
      if (supervisorId) ok(t("org.chart.noticeReports", { name, supervisor: nameOf(supervisorId) }));
      else if (deptId) ok(t("org.chart.noticeMember", { name, dept: deptName(deptId) }));
      else ok(t("org.chart.noticeUnassigned", { name }));
    },
    onError: fail,
    onSettled: () => { setBusy(null); invalidate(); },
  });

  const leadMut = useMutation({
    mutationFn: async ({ member, dept, add }: { member: Member; dept: Department; add: boolean }) => {
      setBusy(member.id);
      if (add) await addDepartmentLead(dept.id, member.kind, member.id);
      else await removeDepartmentLead(dept.id, member.id);
      return { member, dept, add };
    },
    onSuccess: ({ member, dept, add }) =>
      ok(t(add ? "org.chart.noticeLeadSet" : "org.chart.noticeLeadRemoved", { name: memberName(member), dept: dept.name })),
    onError: fail,
    onSettled: () => { setBusy(null); setPending(null); invalidate(); },
  });

  const renameMut = useMutation({
    mutationFn: ({ dept, name }: { dept: Department; name: string }) => renameDepartment(dept.id, name).then(() => ({ dept, name })),
    onSuccess: ({ dept, name }) => ok(t("org.chart.noticeRenamed", { from: dept.name, to: name })),
    onError: fail,
    onSettled: invalidate,
  });
  const colorMut = useMutation({
    mutationFn: ({ dept, color }: { dept: Department; color: string }) => setDepartmentColor(dept.id, color),
    onError: fail,
    onSettled: invalidate,
  });
  const deleteMut = useMutation({
    mutationFn: (dept: Department) => deleteDepartment(dept.id).then(() => dept),
    onSuccess: (dept) => ok(t("org.chart.noticeDeleted", { dept: dept.name })),
    onError: fail,
    onSettled: () => { setPending(null); invalidate(); },
  });
  const createMut = useMutation({
    mutationFn: ({ name, desc, color }: { name: string; desc: string; color: string }) => createDepartment(name, desc, color),
    onSuccess: (d) => { ok(t("org.chart.noticeCreated", { dept: d.name })); setShowNew(false); },
    onError: fail,
    onSettled: invalidate,
  });

  /* ── Drop rules ─────────────────────────────────────────────────────── */

  // Who may land where: a human reports only to a human; nobody reports to
  // itself or to anyone below it.
  const blocked = useMemo(() => (dragging ? descendantsOf(model.members, dragging.id).add(dragging.id) : new Set<string>()), [dragging, model]);
  const canDropOn = (target: Member) =>
    !!dragging && !blocked.has(target.id) && (dragging.kind === "agent" || target.kind === "human");

  const drop = (deptId: string | null, supervisorId: string | null) => {
    if (dragging) moveMut.mutate({ member: dragging, deptId, supervisorId });
    setDragging(null);
  };

  /* ── Canvas: pan, zoom, fit ─────────────────────────────────────────── */

  const canvasRef = useRef<HTMLDivElement>(null);
  const [view, setView] = useState<View>({ x: PAD, y: PAD, k: 1 });
  const [size, setSize] = useState({ w: 0, h: 0 });
  const fitted = useRef(false);

  useEffect(() => {
    const el = canvasRef.current;
    if (!el) return;
    const ro = new ResizeObserver(() => setSize({ w: el.clientWidth, h: el.clientHeight }));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  const minK = mode === "edit" ? EDIT_MIN : ZOOM_MIN;
  const fitScale = useCallback(
    () => Math.max(minK, Math.min(1, (size.w - 2 * PAD) / layout.width, (size.h - 2 * PAD) / layout.height)),
    [size, layout, minK],
  );
  const fit = useCallback(() => {
    if (size.w === 0) return;
    const k = fitScale();
    setView({ x: Math.max((size.w - layout.width * k) / 2, PAD), y: PAD, k });
  }, [size, layout, fitScale]);

  // The opening view: the whole chart if it fits, otherwise no smaller than
  // the names stay readable, with the organisation centred at the top. A
  // wide org is panned, not shrunk to a texture; the fit button is there for
  // the overview.
  const open = useCallback(() => {
    if (size.w === 0) return;
    const k = Math.max(fitScale(), 0.72);
    const root = layout.nodes.find((n) => n.id === ORG);
    const cx = root ? root.x + root.w / 2 : layout.width / 2;
    // Centred on the organisation. A wide org runs past both edges, and the
    // fades say so; starting at the left edge instead put the root card of a
    // sixty-member org a screen away from where the reading begins.
    setView({ x: layout.width * k <= size.w - 2 * PAD ? (size.w - layout.width * k) / 2 : size.w / 2 - cx * k, y: PAD, k });
  }, [size, layout, fitScale]);

  useEffect(() => {
    if (size.w === 0) return;
    if (!fitted.current) { fitted.current = true; open(); }
  }, [size, open]);
  // The cards change size with the mode: reopen so the chart does not run
  // off the edge or shrink into a corner.
  const modeRef = useRef(mode);
  useEffect(() => {
    if (modeRef.current !== mode) { modeRef.current = mode; open(); }
  }, [mode, open]);

  const zoomAt = useCallback((factor: number, cx?: number, cy?: number) => {
    setView((v) => {
      const k = Math.max(minK, Math.min(ZOOM_MAX, v.k * factor));
      const px = cx ?? size.w / 2;
      const py = cy ?? size.h / 2;
      return { k, x: px - (px - v.x) * (k / v.k), y: py - (py - v.y) * (k / v.k) };
    });
  }, [size, minK]);

  // The wheel zooms with ctrl or cmd, around the pointer. Without a modifier
  // it pans only while the canvas has focus (a click gives it that); a reader
  // scrolling down the page must not get caught in the chart on the way.
  // React registers wheel listeners passively, so this one is attached by
  // hand to be able to keep the page from scrolling underneath.
  useEffect(() => {
    const el = canvasRef.current;
    if (!el) return;
    const onWheel = (e: WheelEvent) => {
      if (e.ctrlKey || e.metaKey) {
        e.preventDefault();
        const r = el.getBoundingClientRect();
        zoomAt(Math.exp(-e.deltaY * 0.0025), e.clientX - r.left, e.clientY - r.top);
      } else if (el.contains(document.activeElement)) {
        e.preventDefault();
        setView((v) => ({ ...v, x: v.x - e.deltaX, y: v.y - e.deltaY }));
      }
    };
    el.addEventListener("wheel", onWheel, { passive: false });
    return () => el.removeEventListener("wheel", onWheel);
  }, [zoomAt]);

  const pan = useRef<{ id: number; x: number; y: number; vx: number; vy: number } | null>(null);
  const onPointerDown = (e: React.PointerEvent<HTMLDivElement>) => {
    // Only the ground pans; a card keeps its own gestures (links, drag).
    if ((e.target as HTMLElement).closest(".orgc-node")) return;
    if (e.button !== 0) return;
    pan.current = { id: e.pointerId, x: e.clientX, y: e.clientY, vx: view.x, vy: view.y };
    e.currentTarget.focus({ preventScroll: true });
    e.currentTarget.setPointerCapture(e.pointerId);
    e.currentTarget.classList.add("panning");
  };
  const onPointerMove = (e: React.PointerEvent<HTMLDivElement>) => {
    const p = pan.current;
    if (!p || p.id !== e.pointerId) return;
    setView((v) => ({ ...v, x: p.vx + (e.clientX - p.x), y: p.vy + (e.clientY - p.y) }));
  };
  const onPointerUp = (e: React.PointerEvent<HTMLDivElement>) => {
    if (pan.current?.id === e.pointerId) pan.current = null;
    e.currentTarget.classList.remove("panning");
  };

  const onKeyDown = (e: React.KeyboardEvent<HTMLDivElement>) => {
    if (e.target !== e.currentTarget) return;
    const step = 48;
    const moves: Record<string, [number, number]> = { ArrowLeft: [step, 0], ArrowRight: [-step, 0], ArrowUp: [0, step], ArrowDown: [0, -step] };
    if (moves[e.key]) { const [dx, dy] = moves[e.key]; setView((v) => ({ ...v, x: v.x + dx, y: v.y + dy })); e.preventDefault(); }
    else if (e.key === "+" || e.key === "=") { zoomAt(1.2); e.preventDefault(); }
    else if (e.key === "-") { zoomAt(1 / 1.2); e.preventDefault(); }
    else if (e.key === "0") { fit(); e.preventDefault(); }
  };

  // Tabbing through the chart must not land on a card outside the viewport:
  // when a card takes focus, the view slides until it is inside.
  const onFocusCapture = (e: React.FocusEvent<HTMLDivElement>) => {
    const card = (e.target as HTMLElement).closest<HTMLElement>(".orgc-node");
    const id = card?.dataset.id;
    const n = id ? placed.get(id) : undefined;
    if (!n) return;
    setView((v) => {
      const l = n.x * v.k + v.x, tp = n.y * v.k + v.y, r = l + n.w * v.k, b = tp + n.h * v.k;
      let { x, y } = v;
      if (l < PAD) x += PAD - l; else if (r > size.w - PAD) x -= r - (size.w - PAD);
      if (tp < PAD) y += PAD - tp; else if (b > size.h - PAD) y -= b - (size.h - PAD);
      return x === v.x && y === v.y ? v : { ...v, x, y };
    });
  };

  /* ── Render ─────────────────────────────────────────────────────────── */

  const editing = mode === "edit";
  const total = chart.humans.length + chart.agents.length;
  // Which edges the chart runs past: a clipped card should read as "more",
  // not as broken, so those edges fade.
  const more = [
    view.x < 0 ? "left" : "",
    view.x + layout.width * view.k > size.w ? "right" : "",
    view.y + layout.height * view.k > size.h ? "bottom" : "",
  ].filter(Boolean).join(" ");
  const hint = editing ? t("org.chart.hintEdit") : t("org.chart.hintView");

  return (
    <section className={`orgc${editing ? " editing" : ""}`} aria-label={t("org.title")}>
      <div className="orgc-bar">
        <div className="orgc-legend" aria-hidden="true">
          <span><span className="avatar hum orgc-mini">M</span>{t("org.legendHuman")}</span>
          <span><span className="avatar orgc-mini">A</span>{t("org.legendAgent")}</span>
        </div>
        <p className="orgc-hint muted">{hint}</p>
        <div className="orgc-tools">
          <div className="orgc-zoom" role="group" aria-label={t("org.chart.zoom")}>
            <button className="icon-btn" onClick={() => zoomAt(1 / 1.2)} disabled={view.k <= minK} title={t("org.chart.zoomOut")} aria-label={t("org.chart.zoomOut")}><Ico d={IC.minus} /></button>
            <span className="orgc-pct" aria-live="off">{Math.round(view.k * 100)}&thinsp;%</span>
            <button className="icon-btn" onClick={() => zoomAt(1.2)} title={t("org.chart.zoomIn")} aria-label={t("org.chart.zoomIn")}><Ico d={IC.plus} /></button>
            {/* Edit mode never shrinks, so "fit" could only mean 100 %: the button steps aside. */}
            {!editing && <button className="icon-btn" onClick={fit} title={t("org.chart.zoomFit")} aria-label={t("org.chart.zoomFit")}><Ico d={IC.fit} /></button>}
          </div>
          {editing && (
            <button className="btn sm" onClick={() => setShowNew((v) => !v)} aria-expanded={showNew}>
              {t("org.newDept")}
            </button>
          )}
          <button
            className={`btn sm${editing ? " primary" : ""}`}
            aria-pressed={editing}
            onClick={() => { setMode(editing ? "view" : "edit"); setShowNew(false); setDragging(null); }}
          >
            {editing ? t("org.chart.done") : t("org.chart.edit")}
          </button>
        </div>
      </div>

      {editing && showNew && (
        <NewDepartment
          pending={createMut.isPending}
          onCreate={(name, desc, color) => createMut.mutate({ name, desc, color })}
          onCancel={() => setShowNew(false)}
        />
      )}

      <div
        ref={canvasRef}
        className="orgc-canvas"
        tabIndex={0}
        role="group"
        aria-label={t("org.chart.canvasLabel")}
        data-more={more || undefined}
        onPointerDown={onPointerDown}
        onPointerMove={onPointerMove}
        onPointerUp={onPointerUp}
        onPointerCancel={onPointerUp}
        onKeyDown={onKeyDown}
        onFocusCapture={onFocusCapture}
      >
        <div className="orgc-world" style={{ transform: `translate(${view.x}px, ${view.y}px) scale(${view.k})` }}>
          <svg className="orgc-edges" width={layout.width} height={layout.height} aria-hidden="true">
            {layout.edges.map((e) => <path key={e.id} d={e.d} />)}
          </svg>
          {layout.nodes.map((p) => {
            const n = model.nodes.get(p.id);
            if (!n) return null;
            return (
              <Card key={p.id} p={p} busy={busy}>
                {n.kind === "org" && (
                  <div className="orgc-org">
                    <div className="orgc-nm">{orgName || t("org.rootOrg")}</div>
                    <div className="orgc-rl">
                      {total === 0
                        ? t("org.noPersons")
                        : [t("org.chart.humans", { count: n.humans }), t("org.chart.agents", { count: n.agents }), t("org.chart.depts", { count: n.depts })].join(" · ")}
                    </div>
                  </div>
                )}
                {n.kind === "dept" && (
                  <DeptCard
                    node={n}
                    editing={editing}
                    dragging={dragging}
                    onToggle={() => collapse.toggle(n.dept.id, n.total)}
                    onDrop={() => drop(n.dept.id, null)}
                    onRename={(name) => renameMut.mutate({ dept: n.dept, name })}
                    onColor={(color) => colorMut.mutate({ dept: n.dept, color })}
                    onDelete={() => setPending({ kind: "delete", dept: n.dept })}
                  />
                )}
                {n.kind === "unassigned" && (
                  <UnassignedCard
                    node={n}
                    dragging={dragging}
                    onToggle={() => collapse.toggle(UNASSIGNED, n.total)}
                    onDrop={() => drop(null, null)}
                  />
                )}
                {n.kind === "member" && (
                  <MemberCard
                    node={n}
                    editing={editing}
                    depts={chart.departments}
                    members={model.members}
                    dragging={dragging}
                    canDrop={canDropOn(n.member)}
                    onDragStart={() => setDragging(n.member)}
                    onDragEnd={() => setDragging(null)}
                    onDrop={() => drop(n.deptId, n.member.id)}
                    onToggle={() => collapse.toggle(n.member.id, n.below)}
                    onMove={(deptId, supervisorId) => moveMut.mutate({ member: n.member, deptId, supervisorId })}
                    onLead={(dept, add) => setPending({ kind: "lead", member: n.member, dept, add })}
                  />
                )}
              </Card>
            );
          })}
        </div>

        <div className="orgc-more-bottom" aria-hidden="true" />
        <p className={`orgc-status${notice ? ` ${notice.kind}` : ""}`} role="status" aria-live="polite">
          {notice?.text}
        </p>
      </div>

      {pending?.kind === "delete" && (
        <ConfirmDialog
          title={t("org.deleteDept")}
          confirmLabel={t("org.deleteDept")}
          onConfirm={() => deleteMut.mutate(pending.dept)}
          onClose={() => setPending(null)}
          pending={deleteMut.isPending}
        >
          {t("org.deleteDeptConfirm", { name: pending.dept.name })}
        </ConfirmDialog>
      )}
      {pending?.kind === "lead" && (
        <ConfirmDialog
          title={t("org.chart.leadTitle")}
          confirmLabel={pending.add ? t("org.chart.leadSet") : t("org.chart.leadRemove")}
          danger={false}
          onConfirm={() => leadMut.mutate(pending)}
          onClose={() => setPending(null)}
          pending={leadMut.isPending}
        >
          {t(pending.add ? "org.chart.leadConfirmAdd" : "org.chart.leadConfirmRemove", { name: memberName(pending.member), dept: pending.dept.name })}
        </ConfirmDialog>
      )}
    </section>
  );
}

/* ── Cards ────────────────────────────────────────────────────────────── */

function Card({ p, busy, children }: { p: Placed; busy: string | null; children: ReactNode }) {
  const isBusy = busy !== null && p.id === memberNodeId(busy);
  return (
    <div
      className={`orgc-node${isBusy ? " busy" : ""}`}
      data-id={p.id}
      style={{ left: p.x, top: p.y, width: p.w, height: p.h }}
      aria-busy={isBusy || undefined}
    >
      {children}
    </div>
  );
}

function Toggle({ open, count, onToggle }: { open: boolean; count: number; onToggle: () => void }) {
  const { t } = useTranslation();
  return (
    <button
      className={`orgc-toggle${open ? "" : " closed"}`}
      onClick={onToggle}
      aria-expanded={open}
      title={open ? t("org.collapse") : t("org.expand", { count })}
      aria-label={open ? t("org.collapse") : t("org.expand", { count })}
    >
      <Ico d={IC.chevron} size={14} />
      {!open && <span className="n">{count}</span>}
    </button>
  );
}

function DeptCard({ node, editing, dragging, onToggle, onDrop, onRename, onColor, onDelete }: {
  node: Extract<ChartNode, { kind: "dept" }>;
  editing: boolean;
  dragging: Member | null;
  onToggle: () => void;
  onDrop: () => void;
  onRename: (name: string) => void;
  onColor: (color: string) => void;
  onDelete: () => void;
}) {
  const { t } = useTranslation();
  const [over, setOver] = useState(false);
  const [name, setName] = useState(node.dept.name);
  useEffect(() => setName(node.dept.name), [node.dept.name]);
  const commit = () => { const v = name.trim(); if (v && v !== node.dept.name) onRename(v); else setName(node.dept.name); };
  const leads = node.leads.map(memberName).join(", ");
  const target = !!dragging;

  return (
    <div
      className={`orgc-card dept${over && target ? " over" : ""}`}
      style={{ ["--edge" as string]: node.dept.color || "var(--border-strong)" }}
      onDragOver={(e) => { if (target) { e.preventDefault(); setOver(true); } }}
      onDragLeave={(e) => { if (!e.currentTarget.contains(e.relatedTarget as Node)) setOver(false); }}
      onDrop={(e) => { e.preventDefault(); setOver(false); onDrop(); }}
    >
      <div className="orgc-row">
        {editing ? (
          <input
            className="orgc-rename"
            value={name}
            aria-label={t("org.deptNameLabel")}
            onChange={(e) => setName(e.target.value)}
            onBlur={commit}
            onKeyDown={(e) => { if (e.key === "Enter") (e.target as HTMLInputElement).blur(); if (e.key === "Escape") setName(node.dept.name); }}
          />
        ) : (
          <div className="orgc-nm" title={node.dept.description || node.dept.name}>{node.dept.name}</div>
        )}
        {node.total > 0 && <Toggle open={node.open} count={node.total} onToggle={onToggle} />}
        {editing && (
          <button className="icon-btn danger" onClick={onDelete} title={t("org.deleteDept")} aria-label={`${t("org.deleteDept")}: ${node.dept.name}`}>
            <Ico d={IC.trash} size={15} />
          </button>
        )}
      </div>
      <div className="orgc-rl">
        {target && over
          ? <span className="orgc-drophint">{t("org.chart.dropAsMember", { dept: node.dept.name })}</span>
          : <>{t("org.chart.membersCount", { count: node.total })} · {leads ? t("org.chart.leadNames", { names: leads }) : t("org.chart.noLead")}</>}
      </div>
      {editing && (
        <div className="orgc-tones" role="radiogroup" aria-label={t("org.deptColor")}>
          {["", ...DEPT_TONES].map((c) => (
            <button
              key={c || "none"}
              role="radio"
              aria-checked={(node.dept.color || "") === c}
              aria-label={c ? t("org.chart.tone", { n: DEPT_TONES.indexOf(c) + 1 }) : t("org.colorDefault")}
              className={`orgc-tone${c ? "" : " none"}${(node.dept.color || "") === c ? " sel" : ""}`}
              style={c ? { background: c } : undefined}
              onClick={() => onColor(c)}
            />
          ))}
        </div>
      )}
    </div>
  );
}

function UnassignedCard({ node, dragging, onToggle, onDrop }: {
  node: Extract<ChartNode, { kind: "unassigned" }>;
  dragging: Member | null;
  onToggle: () => void;
  onDrop: () => void;
}) {
  const { t } = useTranslation();
  const [over, setOver] = useState(false);
  const target = !!dragging && memberDept(dragging) !== null;
  return (
    <div
      className={`orgc-card dept loose${over && target ? " over" : ""}`}
      onDragOver={(e) => { if (target) { e.preventDefault(); setOver(true); } }}
      onDragLeave={(e) => { if (!e.currentTarget.contains(e.relatedTarget as Node)) setOver(false); }}
      onDrop={(e) => { e.preventDefault(); setOver(false); if (target) onDrop(); }}
    >
      <div className="orgc-row">
        <div className="orgc-nm">{t("org.diagramUnassigned")}</div>
        <Toggle open={node.open} count={node.total} onToggle={onToggle} />
      </div>
      <div className="orgc-rl">
        {target && over
          ? <span className="orgc-drophint">{t("org.chart.dropUnassign")}</span>
          : <>{t("org.chart.membersCount", { count: node.total })} · {t("org.chart.unassignedSub")}</>}
      </div>
    </div>
  );
}

function MemberCard({ node, editing, depts, members, dragging, canDrop, onDragStart, onDragEnd, onDrop, onToggle, onMove, onLead }: {
  node: Extract<ChartNode, { kind: "member" }>;
  editing: boolean;
  depts: Department[];
  members: Member[];
  dragging: Member | null;
  canDrop: boolean;
  onDragStart: () => void;
  onDragEnd: () => void;
  onDrop: () => void;
  onToggle: () => void;
  onMove: (deptId: string | null, supervisorId: string | null) => void;
  onLead: (dept: Department, add: boolean) => void;
}) {
  const { t } = useTranslation();
  const [over, setOver] = useState(false);
  const m = node.member;
  const name = memberName(m);
  const isAgent = m.kind === "agent";
  const role = isAgent ? m.agent.job_title : m.human.job_title || t(`role.${m.human.role}`, m.human.role);
  const dept = node.deptId ? depts.find((d) => d.id === node.deptId) : undefined;
  const lifted = dragging?.id === m.id;

  // Who this member may report to: someone in the same department, of a kind
  // it may report to, and not itself or anyone below it.
  const below = useMemo(() => descendantsOf(members, m.id), [members, m.id]);
  const bosses = members.filter((o) => o.id !== m.id && !below.has(o.id) && memberDept(o) === node.deptId && (isAgent || o.kind === "human"));
  const bossId = memberBoss(m);
  const bossInDept = bossId && bosses.some((b) => b.id === bossId) ? bossId : "";

  return (
    <div
      className={`orgc-card member${node.isLead ? " lead" : ""}${lifted ? " lifted" : ""}${over && canDrop ? " over" : ""}${dragging && !canDrop && !lifted ? " nodrop" : ""}`}
      draggable={editing}
      onDragStart={(e) => { e.dataTransfer.effectAllowed = "move"; onDragStart(); }}
      onDragEnd={onDragEnd}
      onDragOver={(e) => { if (canDrop) { e.preventDefault(); e.stopPropagation(); setOver(true); } }}
      onDragLeave={(e) => { if (!e.currentTarget.contains(e.relatedTarget as Node)) setOver(false); }}
      onDrop={(e) => { if (canDrop) { e.preventDefault(); e.stopPropagation(); setOver(false); onDrop(); } }}
    >
      <div className="orgc-row">
        {editing && <span className="orgc-grip" title={t("org.chart.grip")}><Ico d={IC.grip} size={16} /></span>}
        <Link to={isAgent ? `/agents/${m.id}` : `/people/${m.id}`} className="orgc-who" draggable={false} title={t("org.openProfile")} aria-label={`${name}: ${t("org.openProfile")}`}>
          <Avatar name={name} size={30} human={!isAgent} />
          <span className="orgc-text">
            <span className="orgc-nm">{name}</span>
            <span className={`orgc-rl${isAgent && !role ? " mono" : ""}`}>
              {over && canDrop ? <span className="orgc-drophint">{t("org.chart.dropUnder", { name })}</span> : role || (isAgent ? m.agent.slug : "")}
            </span>
          </span>
        </Link>
        {isAgent ? <AgentState node={node} /> : null}
        {node.isLead && !editing && <span className="orgc-leadtag" title={t("org.leadLabel")}><Ico d={IC.lead} size={11} />{t("org.leadLabel")}</span>}
        {node.below > 0 && <Toggle open={node.open} count={node.below} onToggle={onToggle} />}
      </div>
      {editing && (
        <div className="orgc-edit">
          <label className="orgc-field">
            <span>{t("org.chart.department")}</span>
            <select value={node.deptId ?? ""} onChange={(e) => onMove(e.target.value || null, null)}>
              <option value="">{t("org.chart.noDepartment")}</option>
              {depts.map((d) => <option key={d.id} value={d.id}>{d.name}</option>)}
            </select>
          </label>
          <label className="orgc-field">
            <span>{t("org.chart.reportsTo")}</span>
            <select value={bossInDept} onChange={(e) => onMove(node.deptId, e.target.value || null)}>
              <option value="">{t("org.chart.nobody")}</option>
              {bosses.map((b) => <option key={b.id} value={b.id}>{memberName(b)}</option>)}
            </select>
          </label>
          {dept && (
            <button
              className={`orgc-leadbtn${node.isLead ? " on" : ""}`}
              aria-pressed={node.isLead}
              onClick={() => onLead(dept, !node.isLead)}
              title={node.isLead ? t("org.removeLead") : t("org.chart.makeLead")}
            >
              <Ico d={IC.lead} size={13} />{t("org.leadLabel")}
            </button>
          )}
        </div>
      )}
    </div>
  );
}

function AgentState({ node }: { node: Extract<ChartNode, { kind: "member" }> }) {
  const { t } = useTranslation();
  if (node.member.kind !== "agent") return null;
  const a = node.member.agent;
  if (isDraft(a)) return <span className="badge state st-draft">{t("dashboard.draftBadge")}</span>;
  if (a.wake_trouble && !a.killed) {
    return (
      <span className="badge state st-wake-failed" title={t("agent.wakeFailedWhy", { n: a.wake_trouble.failures, err: a.wake_trouble.error ?? "" })}>
        {t("status.wakeFailed")}
      </span>
    );
  }
  const s = a.killed ? "killed" : a.status;
  return <span className={`badge state st-${s}`}>{t(`status.${s}`, s)}</span>;
}

function NewDepartment({ pending, onCreate, onCancel }: { pending: boolean; onCreate: (name: string, desc: string, color: string) => void; onCancel: () => void }) {
  const { t } = useTranslation();
  const [name, setName] = useState("");
  const [desc, setDesc] = useState("");
  const [color, setColor] = useState("");
  return (
    <form className="orgc-new" onSubmit={(e) => { e.preventDefault(); onCreate(name.trim(), desc.trim(), color); }}>
      <input value={name} onChange={(e) => setName(e.target.value)} placeholder={t("org.deptNamePlaceholder")} aria-label={t("org.deptNameLabel")} required autoFocus />
      <input value={desc} onChange={(e) => setDesc(e.target.value)} placeholder={t("org.deptDescPlaceholder")} aria-label={t("org.deptDescPlaceholder")} />
      <div className="orgc-tones" role="radiogroup" aria-label={t("org.deptColor")}>
        {["", ...DEPT_TONES].map((c) => (
          <button
            key={c || "none"}
            type="button"
            role="radio"
            aria-checked={color === c}
            aria-label={c ? t("org.chart.tone", { n: DEPT_TONES.indexOf(c) + 1 }) : t("org.colorDefault")}
            className={`orgc-tone${c ? "" : " none"}${color === c ? " sel" : ""}`}
            style={c ? { background: c } : undefined}
            onClick={() => setColor(c)}
          />
        ))}
      </div>
      <button className="btn sm primary" type="submit" disabled={pending || !name.trim()}>{t("org.createDept")}</button>
      <button className="btn sm" type="button" onClick={onCancel}>{t("modal.cancel")}</button>
    </form>
  );
}

