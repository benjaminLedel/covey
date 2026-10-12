/* One workflow in the preview (#560): edit it on a canvas, watch it live,
   follow one run, read it as a file, see how it reaches people in the chat.
   All state lives in this component; nothing is saved. */
import { useCallback, useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import {
  Background,
  BackgroundVariant,
  Controls,
  MarkerType,
  ReactFlow,
  ReactFlowProvider,
  applyNodeChanges,
  useReactFlow,
  type Connection,
  type Edge,
  type Node,
  type NodeChange,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import "./workflows.css";
import { BlockIcon, CanvasContext, edgeTypes, nodeTypes, type CanvasState, type Tab } from "./canvas";
import { DecisionCard, QuestionCard } from "./ChatCards";
import Inspector from "./Inspector";
import { FileSide, LiveSide, RunList, RunSide, type RunFilter } from "./Sides";
import { BLOCK_TYPES, NODE_W, definitionText, example, heightOf, outsOf, roleEmpty, runPath, type Block, type BlockType, type Example, type Link as WfLink, type Mode, type Step } from "./model";

const TABS: Tab[] = ["edit", "live", "run", "file"];
const MODES: Mode[] = ["off", "watch", "steer"];
// Room for what floats on the canvas: the tag on top, the rail on the left.
const FIT_PADDING = { top: "56px", left: "72px", right: "24px", bottom: "56px" } as const;

export default function WorkflowPage() {
  return (
    <ReactFlowProvider>
      <Editor />
    </ReactFlowProvider>
  );
}

function newBlock(type: BlockType, ex: Example, t: (k: string) => string): Omit<Block, "id"> {
  const title = t(`workflows.type.${type}`);
  switch (type) {
    case "trigger":
      return { type, title, cfg: { source: ex.options.sources[4], event: "", filter: "" } };
    case "agent":
      return { type, title, cfg: { role: Object.keys(ex.roles)[2], pick: "load", sticky: true, cap: "2", notSame: "", optional: false, task: "", output: ["result"], ask: ex.owner, limit: ex.options.limits[1] } };
    case "ai":
      return { type, title, cfg: { prompt: "", output: ["kind"], model: "small" } };
    case "condition":
      return { type, title, cfg: { field: "{{…}}", rounds: 0 }, outs: [{ key: "yes", label: t("workflows.yes") }, { key: "no", label: t("workflows.no") }] };
    case "action":
      return { type, title, cfg: { system: "GitLab", op: "label", target: "{{ticket.no}}", value: "", comment: "" } };
    case "approval":
      return { type, title, cfg: { who: ex.owner, question: "", wait: ex.options.waits[1] }, outs: [{ key: "ok", label: t("workflows.approved") }, { key: "no", label: t("workflows.rejected") }] };
    case "wait":
      return { type, title, cfg: { until: "", max: ex.options.maxes[1], then: "" } };
    case "notify":
      return { type, title, cfg: { to: ex.owner, via: "Chat", text: "" } };
    default:
      return { type, title, cfg: {} };
  }
}

function Editor() {
  const { t, i18n } = useTranslation();
  const ex = useMemo(() => example(i18n.language), [i18n.language]);
  const flow = useReactFlow();

  const [blocks, setBlocks] = useState(ex.blocks);
  const [links, setLinks] = useState<WfLink[]>(ex.links);
  const [nodes, setNodes] = useState<Node[]>(() =>
    Object.keys(ex.blocks).map((id) => ({ id, type: "block", position: ex.positions[id], data: {} })),
  );
  const [mode, setMode] = useState<Mode>("steer");
  const [tab, setTab] = useState<Tab>("edit");
  /* The details float over the canvas instead of taking a column from it:
     while editing they open with a click on a block and close again; in
     the live view and a run they carry the summary and start open. */
  const [panel, setPanel] = useState(false);
  const [selected, setSelected] = useState<string | null>(null);
  const [selectedLink, setSelectedLink] = useState<number | null>(null);
  const [live, setLive] = useState(ex.live);
  const [runs, setRuns] = useState(ex.runs);
  // No run chosen shows the list of runs; a chosen one its path and steps.
  const [run, setRun] = useState<string | null>(null);
  const [runFilter, setRunFilter] = useState<RunFilter>("attention");
  const [runStep, setRunStep] = useState<string | null>(null);
  const [runQuery, setRunQuery] = useState("");
  const [decided, setDecided] = useState<Record<string, string>>({});
  const [replied, setReplied] = useState<string | null>(null);

  const path = useMemo(() => runPath(run ? runs[run] : undefined), [runs, run]);
  const positions = useMemo(() => Object.fromEntries(nodes.map((n) => [n.id, n.position])), [nodes]);

  const edges: Edge[] = useMemo(
    () =>
      links.flatMap((l, i) => {
        const a = blocks[l.from];
        const b = blocks[l.to];
        const pa = positions[l.from];
        const pb = positions[l.to];
        if (!a || !b || !pa || !pb) return [];
        const back = pb.x < pa.x + NODE_W + 20;
        const cls: string[] = [];
        if (roleEmpty(ex, a) || roleEmpty(ex, b)) cls.push("dashed");
        if (tab === "run" && run) cls.push(path.links.has(`${l.from}>${l.to}`) ? "on" : "dim");
        if (tab === "edit" && selectedLink === i) cls.push("sel");
        const label = outsOf(a).find((o) => o.key === l.port)?.label ?? "";
        const low = Math.max(pa.y + heightOf(a), pb.y + heightOf(b)) + (a.type === "approval" ? 40 : 70);
        return [
          {
            id: `l${i}`,
            source: l.from,
            sourceHandle: l.port,
            target: l.to,
            type: back ? "loop" : "default",
            data: { low },
            label: back && label ? (a.type === "condition" ? `${label} · ${t("workflows.roundPlus")}` : label) : undefined,
            className: cls.join(" "),
            markerEnd: { type: MarkerType.ArrowClosed, width: 16, height: 16 },
          },
        ];
      }),
    [links, blocks, positions, tab, run, path, selectedLink, ex, t],
  );

  const ctx: CanvasState = { ex, blocks, tab, mode, selected: tab === "run" && !run ? runStep : selected, live, path };

  const onNodesChange = useCallback(
    (changes: NodeChange[]) => setNodes((ns) => applyNodeChanges(changes.filter((c) => c.type === "position" || c.type === "dimensions"), ns)),
    [],
  );

  const onConnect = useCallback(
    (c: Connection) => {
      if (!c.source || !c.target || c.source === c.target || blocks[c.target]?.type === "trigger") return;
      const port = c.sourceHandle ?? "out";
      setLinks((ls) => [...ls.filter((l) => !(l.from === c.source && l.port === port)), { from: c.source, port, to: c.target }]);
    },
    [blocks],
  );

  const addBlock = (type: BlockType) => {
    const box = document.querySelector(".wf-canvas")?.getBoundingClientRect();
    const at = box
      ? flow.screenToFlowPosition({ x: box.left + box.width / 2 - NODE_W / 2, y: box.top + box.height / 2 - 30 })
      : { x: 200, y: 200 };
    const id = `${type[0]}${Math.random().toString(36).slice(2, 6)}`;
    setBlocks((bs) => ({ ...bs, [id]: { id, ...newBlock(type, ex, t) } }));
    setNodes((ns) => [...ns, { id, type: "block", position: { x: Math.round(at.x / 10) * 10, y: Math.round(at.y / 10) * 10 }, data: {} }]);
    setSelected(id);
    setSelectedLink(null);
  };

  const removeBlock = (id: string) => {
    setBlocks((bs) => {
      const next = { ...bs };
      delete next[id];
      return next;
    });
    setNodes((ns) => ns.filter((n) => n.id !== id));
    setLinks((ls) => ls.filter((l) => l.from !== id && l.to !== id));
    setSelected(null);
  };

  const switchTab = (next: Tab) => {
    setTab(next);
    if (next !== "run") setSelected(null);
    setSelectedLink(null);
    setPanel(next === "live" || next === "run");
  };

  const openRun = (no: string) => {
    setRun(no);
    setSelected(null);
    switchTab("run");
  };

  const now = t("workflows.chat.now");
  const decide = (no: string, d: string) => {
    setDecided((ds) => ({ ...ds, [no]: d }));
    const waiting = live.h?.cases.find((c) => c.no === no);
    const choice = outsOf(blocks.h ?? ex.blocks.h).find((o) => o.key === d)?.label ?? d;
    setLive((lv) => ({
      ...lv,
      h: { cases: lv.h.cases.filter((c) => c.no !== no) },
      // Another round goes back to the person who built it; it may have to queue.
      b:
        waiting && d === "retry"
          ? { ...lv.b, cases: [...lv.b.cases, { ...waiting, by: "Kim", since: t("workflows.chat.justNowRound", { n: 4 }), level: "ok", why: undefined, state: "queued", age: 0, mine: false }] }
          : lv.b,
    }));
    setRuns((rs) => {
      const r = rs[no];
      const steps: Step[] = r.steps.map((s) => ({ ...s, now: false }));
      steps.push({ block: "h", when: now, text: t("workflows.chat.stepDecided", { owner: ex.owner, choice }), chat: true });
      if (d === "retry") steps.push({ block: "b", when: now, text: t("workflows.chat.stepRound", { n: 4, name: "Kim" }), now: true });
      else steps.push({ block: "n", when: now, text: t("workflows.type.notify") }, { block: "e", when: now, text: t("workflows.chat.stepEnded", { owner: ex.owner }) });
      return { ...rs, [no]: { ...r, steps } };
    });
  };
  const reply = (text: string) => {
    setReplied(text);
    const no = ex.chat.caseNo;
    setLive((lv) => ({ ...lv, b: { ...lv.b, cases: lv.b.cases.map((c) => (c.no === no ? { ...c, level: "ok", why: undefined, since: now, mine: false } : c)) } }));
    setRuns((rs) => {
      const r = rs[no];
      const steps: Step[] = r.steps.map((s) => ({ ...s, now: false }));
      steps.push(
        { block: "b", when: now, text: t("workflows.chat.stepAnswered", { owner: ex.owner, text }), chat: true },
        { block: "b", when: now, text: t("workflows.chat.stepContinues", { name: ex.chat.colleague }), now: true },
      );
      return { ...rs, [no]: { ...r, steps } };
    });
  };

  const stuck = Object.values(live).flatMap((l) => l.cases).filter((c) => c.level === "bad").length;
  /* Where a run waits on a person, the card it put into their chat. Once
     answered it stays, showing the answer, so the step that follows reads. */
  let waiting: React.ReactNode = null;
  const atApproval = run && (live.h?.cases.some((c) => c.no === run) || decided[run]);
  if (run && atApproval) {
    const defects = runs[run].steps.find((s) => s.out?.defects)?.out?.defects;
    waiting = (
      <DecisionCard
        recipient={ex.owner}
        kind={`${t("workflows.chat.kindApproval")} · ${run}`}
        question={t("workflows.chat.cardQuestion")}
        context={defects ? t("workflows.chat.cardContext", { defects }) : undefined}
        options={outsOf(blocks.h ?? ex.blocks.h)}
        decided={decided[run] ?? null}
        onDecide={(d) => decide(run, d)}
      />
    );
  } else if (run === ex.chat.caseNo) {
    waiting = (
      <QuestionCard
        recipient={ex.owner}
        colleague={ex.chat.colleague}
        question={ex.chat.question}
        answered={replied}
        thanks={ex.chat.thanks}
        onAnswer={reply}
      />
    );
  }

  let side: React.ReactNode;
  if (tab === "edit")
    side = (
      <Inspector
        ex={ex}
        blocks={blocks}
        links={links}
        selected={selected}
        selectedLink={selectedLink}
        onBlock={(id, patch) => setBlocks((bs) => ({ ...bs, [id]: { ...bs[id], ...patch } }))}
        onRemoveBlock={removeBlock}
        onRemoveLink={(i) => {
          setLinks((ls) => ls.filter((_, j) => j !== i));
          setSelectedLink(null);
        }}
      />
    );
  else if (tab === "live")
    side = (
      <LiveSide
        blocks={blocks}
        live={live}
        mode={mode}
        owner={ex.owner}
        selected={selected}
        onOpen={openRun}
        onList={(f, step) => {
          setRunFilter(f);
          setRunStep(step ?? null);
          setRunQuery("");
          setRun(null);
          switchTab("run");
        }}
      />
    );
  else if (tab === "run")
    side =
      run && runs[run] ? (
        <RunSide
          run={runs[run]}
          blocks={blocks}
          selected={selected}
          waiting={waiting}
          onStep={setSelected}
          onBack={() => {
            setRun(null);
            setSelected(null);
          }}
        />
      ) : (
        <RunList
          blocks={blocks}
          live={live}
          filter={runFilter}
          step={runStep}
          query={runQuery}
          onFilter={setRunFilter}
          onStep={setRunStep}
          onQuery={setRunQuery}
          onOpen={openRun}
        />
      );
  else side = null;

  return (
    <div className="wf-page">
      <div className="wf-editor-head">
        <div className="wf-crumb">
          <Link to="/workflows">{t("nav.workflows")}</Link>
          <span>/</span>
          <b>{ex.name}</b>
          <span className="wf-pill clay">{t("workflows.preview")}</span>
        </div>
        <div className="wf-seg mode" role="group" aria-label={t("workflows.modeLabel")}>
          {MODES.map((m) => (
            <button key={m} data-m={m} aria-pressed={mode === m} onClick={() => setMode(m)}>
              {t(`workflows.modeButton.${m}`)}
            </button>
          ))}
        </div>
        <div className="wf-seg wf-tabs" role="tablist" aria-label={t("workflows.viewLabel")}>
          {TABS.map((x) => (
            <button key={x} role="tab" aria-selected={tab === x} onClick={() => switchTab(x)}>
              {t(`workflows.tab.${x}`)}
              {x === "live" && stuck > 0 && mode !== "off" && <span className="wf-count">{stuck}</span>}
            </button>
          ))}
        </div>
      </div>
      <p className="wf-modebar">{t(`workflows.modeText.${mode}`)}</p>

      {(
        <div className="wf-body">
          <div className={`wf-canvas${panel && tab !== "file" ? " has-panel" : ""}`}>
            <CanvasContext.Provider value={ctx}>
              <ReactFlow
                nodes={nodes}
                edges={edges}
                nodeTypes={nodeTypes}
                edgeTypes={edgeTypes}
                onNodesChange={onNodesChange}
                onConnect={onConnect}
                onNodeClick={(_, n) => {
                  if (tab === "run" && !run) {
                    setRunStep(n.id);
                    setPanel(true);
                    return;
                  }
                  setSelected(n.id);
                  setSelectedLink(null);
                  setPanel(true);
                }}
                onEdgeClick={(_, e) => {
                  if (tab !== "edit") return;
                  setSelectedLink(Number(e.id.slice(1)));
                  setSelected(null);
                  setPanel(true);
                }}
                onPaneClick={() => {
                  setSelected(null);
                  setSelectedLink(null);
                  if (tab === "edit") setPanel(false);
                }}
                nodesDraggable={tab === "edit"}
                nodesConnectable={tab === "edit"}
                elementsSelectable={false}
                deleteKeyCode={null}
                snapToGrid
                snapGrid={[10, 10]}
                fitView
                fitViewOptions={{ padding: FIT_PADDING }}
                minZoom={0.25}
                maxZoom={2}
              >
                <Background variant={BackgroundVariant.Dots} gap={20} size={1} />
                <Controls showInteractive={false} position="bottom-right" />
                <FitOnTab tab={tab} />
              </ReactFlow>
            </CanvasContext.Provider>

            {tab === "edit" && (
              <div className="wf-rail" role="toolbar" aria-label={t("workflows.blocks")}>
                {BLOCK_TYPES.map((type) => (
                  <button key={type} className="wf-rail-item" onClick={() => addBlock(type)} aria-label={t(`workflows.type.${type}`)}>
                    <BlockIcon type={type} />
                    <span className="wf-rail-tip" role="tooltip">
                      <b>{t(`workflows.type.${type}`)}</b>
                      <small>{t(`workflows.typeDesc.${type}`)}</small>
                    </span>
                  </button>
                ))}
              </div>
            )}
            <div className="wf-overlay-top">
              <span className="wf-tag">{t("workflows.preview")}</span>
              {tab !== "file" && !panel && (
                <button className="wf-details-btn" onClick={() => setPanel(true)}>
                  {t("workflows.details")}
                </button>
              )}
            </div>
            {tab !== "file" && <div className="wf-legend">{t(`workflows.legend.${tab === "run" && !run ? "runs" : tab}`, { no: run })}</div>}
            {tab === "file" && (
              <div className="wf-file">
                <div className="wf-file-head">
                  <span className="wf-pill info">workflows/customer-ticket.workflow</span>
                  <span>{t("workflows.file.head")}</span>
                </div>
                <div className="wf-file-grid">
                  <pre>{definitionText(ex, mode, blocks, links, positions)}</pre>
                  <div className="wf-file-side">
                    <FileSide />
                  </div>
                </div>
              </div>
            )}
            {panel && tab !== "file" && (
              <aside className="wf-panel" aria-label={t("workflows.details")}>
                <button
                  className="wf-panel-close"
                  aria-label={t("workflows.closeDetails")}
                  onClick={() => {
                    setPanel(false);
                    setSelected(null);
                    setSelectedLink(null);
                  }}
                >
                  ×
                </button>
                {side}
              </aside>
            )}
          </div>
        </div>
      )}
    </div>
  );
}

// Each view starts with the whole workflow in sight.
function FitOnTab({ tab }: { tab: Tab }) {
  const flow = useReactFlow();
  useEffect(() => {
    const id = requestAnimationFrame(() => flow.fitView({ padding: FIT_PADDING }));
    return () => cancelAnimationFrame(id);
  }, [tab, flow]);
  return null;
}
