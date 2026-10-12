/* The canvas of the workflow preview (#560): one node kind for every block,
   one edge kind for the way back. React Flow does panning, zooming,
   dragging and connecting; what a block shows comes from the page through
   a context, so the nodes need no copy of the page's state. */
import { createContext, memo, useContext, type JSX } from "react";
import { useTranslation } from "react-i18next";
import { BaseEdge, EdgeLabelRenderer, Handle, Position, type EdgeProps, type NodeProps } from "@xyflow/react";
import { HEAD_H, NODE_W, ROW_H, heightOf, outY, outsOf, roleEmpty, type Block, type BlockType, type Example, type Mode } from "./model";

export type Tab = "edit" | "live" | "run" | "file" | "chat";

export interface CanvasState {
  ex: Example;
  blocks: Record<string, Block>;
  tab: Tab;
  mode: Mode;
  selected: string | null;
  live: Example["live"];
  path: { blocks: Set<string>; count: Record<string, number>; current: string | null };
}

export const CanvasContext = createContext<CanvasState | null>(null);

const ICONS: Record<BlockType, JSX.Element> = {
  trigger: <path d="M13 2 3 14h9l-1 8 10-12h-9l1-8z" />,
  agent: (
    <>
      <path d="M20 21v-2a4 4 0 0 0-4-4H8a4 4 0 0 0-4 4v2" />
      <circle cx="12" cy="7" r="4" />
    </>
  ),
  ai: (
    <>
      <path d="M12 3l1.8 5.2L19 10l-5.2 1.8L12 17l-1.8-5.2L5 10l5.2-1.8z" />
      <path d="M19 15l.7 2.3L22 18l-2.3.7L19 21l-.7-2.3L16 18l2.3-.7z" />
    </>
  ),
  condition: (
    <>
      <circle cx="6" cy="6" r="2.5" />
      <circle cx="6" cy="18" r="2.5" />
      <circle cx="18" cy="8" r="2.5" />
      <path d="M6 8.5v7M18 10.5a8 8 0 0 1-8 7" />
    </>
  ),
  action: <path d="M14.7 6.3a4 4 0 0 0-5.4 5.4L3 18l3 3 6.3-6.3a4 4 0 0 0 5.4-5.4l-2.6 2.6-2.4-.6-.6-2.4z" />,
  approval: (
    <>
      <path d="M22 11.1V12a10 10 0 1 1-5.9-9.1" />
      <path d="M22 4 12 14l-3-3" />
    </>
  ),
  wait: (
    <>
      <circle cx="12" cy="12" r="9" />
      <path d="M12 7v5l3 2" />
    </>
  ),
  notify: (
    <>
      <path d="M18 8A6 6 0 0 0 6 8c0 7-3 9-3 9h18s-3-2-3-9" />
      <path d="M13.7 21a2 2 0 0 1-3.4 0" />
    </>
  ),
  end: (
    <>
      <path d="M4 15s1-1 4-1 5 2 8 2 4-1 4-1V3s-1 1-4 1-5-2-8-2-4 1-4 1z" />
      <path d="M4 22v-7" />
    </>
  ),
};

export function BlockIcon({ type, small }: { type: BlockType; small?: boolean }) {
  return (
    <span className={`wf-icon k-${type}${small ? " small" : ""}`} aria-hidden="true">
      <svg viewBox="0 0 24 24">{ICONS[type]}</svg>
    </span>
  );
}

// The line under a block's title: what it does, in a few words.
export function useBlockSummary() {
  const { t } = useTranslation();
  return (ex: Example, b: Block): string => {
    const c = b.cfg;
    switch (b.type) {
      case "trigger":
        return String(c.source);
      case "agent": {
        const members = ex.roles[String(c.role)] ?? [];
        const free = members.filter((p) => ex.people[p]?.state === "ok");
        return members.length > 1
          ? t("workflows.sumPool", { role: c.role, free: free.length, all: members.length })
          : t("workflows.sumRole", { role: c.role, name: members[0] ?? t("workflows.unfilled") });
      }
      case "ai":
        return t("workflows.sumAi", { model: t(`workflows.ai.model_${c.model}`) });
      case "condition":
        return t("workflows.sumCondition", { field: c.field });
      case "action":
        return `${c.system}: ${t(`workflows.ops.${c.op}`)}`;
      case "approval":
        return t("workflows.sumApproval", { who: c.who });
      case "wait":
        return t("workflows.sumWait", { until: c.until });
      case "notify":
        return t("workflows.sumNotify", { to: c.to, via: c.via });
      default:
        return t("workflows.sumEnd");
    }
  };
}

function BlockNodeImpl({ id }: NodeProps) {
  const { t } = useTranslation();
  const summary = useBlockSummary();
  const s = useContext(CanvasContext);
  if (!s) return null;
  const b = s.blocks[id];
  if (!b) return null;
  const outs = outsOf(b);
  const empty = roleEmpty(s.ex, b);
  const cls = ["wf-node"];
  const badges: JSX.Element[] = [];
  if ((s.tab === "edit" || s.tab === "live") && s.selected === id) cls.push("sel");
  if (empty) cls.push("skip");
  if (s.tab === "edit" && empty) {
    badges.push(<span key="e" className="wf-pill warn">{b.cfg.optional ? t("workflows.unfilledSkipped") : t("workflows.unfilled")}</span>);
  }
  if (s.tab === "live" && s.mode !== "off") {
    const cases = s.live[id]?.cases ?? [];
    if (cases.length) {
      const bad = cases.filter((c) => c.level === "bad").length;
      const warn = cases.filter((c) => c.level === "warn").length;
      if (bad) cls.push("bad");
      else if (warn) cls.push("warnb");
      badges.push(<span key="n" className={`wf-pill ${bad ? "bad" : warn ? "warn" : "info"}`}>{t("workflows.live.count", { count: cases.length })}</span>);
      if (bad) badges.push(<span key="b" className="wf-pill bad">{t("workflows.live.stuckBadge", { count: bad })}</span>);
    } else if (empty) {
      badges.push(<span key="s" className="wf-pill warn">{t("workflows.live.skipped")}</span>);
    }
  }
  if (s.tab === "run") {
    if (!s.path.blocks.has(id)) cls.push("dim");
    if (s.path.current === id) cls.push("cur");
    if ((s.path.count[id] ?? 0) > 1) badges.push(<span key="x" className="wf-pill info">{t("workflows.run.times", { n: s.path.count[id] })}</span>);
    if (s.path.current === id) badges.push(<span key="h" className="wf-pill info">{t("workflows.run.here")}</span>);
  }
  return (
    <div className={cls.join(" ")} style={{ width: NODE_W, height: heightOf(b) }}>
      <div className="wf-badges">{badges}</div>
      <div className="wf-node-head" style={{ height: HEAD_H }}>
        <BlockIcon type={b.type} />
        <div className="wf-node-text">
          <div className="wf-node-type">
            {t(`workflows.type.${b.type}`)}
            {b.type === "agent" && b.cfg.optional ? ` · ${t("workflows.optionalShort")}` : ""}
          </div>
          <div className="wf-node-title">{b.title}</div>
          <div className="wf-node-sub">{summary(s.ex, b)}</div>
        </div>
      </div>
      {outs.length > 1 &&
        outs.map((o) => (
          <div key={o.key} className="wf-node-row" style={{ height: ROW_H }}>
            {o.label}
          </div>
        ))}
      {b.type !== "trigger" && <Handle type="target" position={Position.Left} style={{ top: HEAD_H / 2 }} />}
      {outs.map((o) => (
        <Handle key={o.key} id={o.key} type="source" position={Position.Right} style={{ top: outY(b, o.key) }} />
      ))}
    </div>
  );
}

export const BlockNode = memo(BlockNodeImpl);

/* The way back of a loop: out to the right, down below both blocks, across
   and up into the target. A bezier between the two handles would cut
   straight through the blocks in between. */
export function LoopEdge({ sourceX, sourceY, targetX, targetY, data, markerEnd, style, label }: EdgeProps) {
  const low = (data?.low as number) ?? Math.max(sourceY, targetY) + 80;
  const path = `M${sourceX} ${sourceY} C${sourceX + 70} ${sourceY},${sourceX + 70} ${low},${sourceX} ${low} L${targetX} ${low} C${targetX - 70} ${low},${targetX - 70} ${targetY},${targetX} ${targetY}`;
  return (
    <>
      <BaseEdge path={path} markerEnd={markerEnd} style={style} />
      {label && (
        <EdgeLabelRenderer>
          <div className="wf-edge-label" style={{ transform: `translate(-50%, -100%) translate(${(sourceX + targetX) / 2}px, ${low - 4}px)` }}>
            {label}
          </div>
        </EdgeLabelRenderer>
      )}
    </>
  );
}

export const nodeTypes = { block: BlockNode };
export const edgeTypes = { loop: LoopEdge };
