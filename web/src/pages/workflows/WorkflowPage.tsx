/* One workflow (#560, #565): edit it on a canvas, watch it live, follow its
   runs, read and write it as text. The definition comes from the API and
   goes back as the text the engine parses; runs are read as they change. */
import { useCallback, useEffect, useMemo, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Link, useNavigate, useParams, useSearchParams } from "react-router";
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
import {
  api,
  cancelWorkflowRun,
  checkWorkflow,
  decideWorkflowRun,
  deleteWorkflow,
  getWorkflow,
  getWorkflowRun,
  isNotFound,
  listWorkflowRuns,
  saveWorkflow,
  startWorkflowRun,
  type Agent,
  type Principal,
  type TargetPlugin,
  type WorkflowProblem,
  type WorkflowRun,
} from "../../api";
import { fmtDelta } from "../../format";
import { ConfirmDialog } from "../../components/Modal";
import { canManage } from "../agent/roles";
import { BlockIcon, CanvasContext, edgeTypes, nodeTypes, unstaffed, type CanvasState, type Tab } from "./canvas";
import { DecisionCard } from "./ChatCards";
import Inspector from "./Inspector";
import { FileSide, LiveSide, RunList, RunSide, type RunFilter } from "./Sides";
import {
  BLOCK_TYPES,
  EXIT_ELSE,
  EXIT_LIMIT,
  EXIT_OUT,
  EXIT_TIMEOUT,
  NODE_W,
  fromWorkflow,
  heightOf,
  isOpen,
  outsOf,
  runPath,
  toSource,
  type Block,
  type BlockType,
  type Case,
  type Cfg,
  type Doc,
  type Meta,
  type Mode,
} from "./model";

const TABS: Tab[] = ["edit", "live", "run", "file"];
const MODES: Mode[] = ["off", "watch", "steer"];
// Room for what floats on the canvas: the tag on top, the rail on the left.
const FIT_PADDING = { top: "56px", left: "72px", right: "24px", bottom: "56px" } as const;

export default function WorkflowPage({ me }: { me: Principal }) {
  return (
    <ReactFlowProvider>
      <Editor me={me} />
    </ReactFlowProvider>
  );
}

/* An approval names its person by e-mail, by the part of it before the @,
   or by display name — the API decides the same way. */
function namesMe(who: unknown, me: Principal): boolean {
  const w = String(who ?? "").trim().toLowerCase();
  if (!w) return false;
  const email = me.Email.toLowerCase();
  return w === email || w === email.split("@")[0] || w === me.DisplayName.trim().toLowerCase();
}

function blankBlock(type: BlockType, id: string, title: string): Block {
  const cfg: Cfg = {
    agent: { role: "", pick: "load", sticky: true, cap: "", notSame: "", optional: false, task: "", output: [], ask: "", limit: "" },
    ai: { task: "", output: [] },
    condition: { field: "", rounds: 0, values: [] },
    action: { as: "", system: "", do: "", params: [] },
    approval: { who: "", ask: "", wait: "", options: [] },
    wait: { until: "", max: "" },
    notify: { to: "", via: "chat", text: "" },
    trigger: { as: "", every: "", daily: "", onlyIf: "", webhook: "", token: false },
    end: {},
  }[type];
  return { id, type, title, cfg };
}

function Editor({ me }: { me: Principal }) {
  const { t } = useTranslation();
  // The address names the workflow by id (#580); the API takes it wherever
  // it takes the slug.
  const { id: ref = "" } = useParams();
  const [params] = useSearchParams();
  const qc = useQueryClient();
  const flow = useReactFlow();
  const manage = canManage(me.Role);
  const navigate = useNavigate();
  const [deleting, setDeleting] = useState<"ask" | "busy" | null>(null);
  const [deleteError, setDeleteError] = useState("");

  const wfQ = useQuery({ queryKey: ["workflow", ref], queryFn: () => getWorkflow(ref), retry: (n, e) => !isNotFound(e) && n < 2 });
  const runsQ = useQuery({ queryKey: ["workflow-runs", ref], queryFn: () => listWorkflowRuns(ref), refetchInterval: 5000, enabled: wfQ.isSuccess });
  const agentsQ = useQuery({ queryKey: ["agents"], queryFn: () => api<Agent[] | null>("/agents"), staleTime: 60_000 });
  const agents = useMemo(() => agentsQ.data ?? [], [agentsQ.data]);
  const targetsQ = useQuery({ queryKey: ["targets"], queryFn: () => api<TargetPlugin[] | null>("/targets"), staleTime: 60_000 });
  const systems = useMemo(() => (targetsQ.data ?? []).filter((p) => p.enabled).map((p) => p.name).sort(), [targetsQ.data]);

  const [doc, setDoc] = useState<Doc | null>(null);
  const [nodes, setNodes] = useState<Node[]>([]);
  const [dirty, setDirty] = useState(false);
  const [loadedVersion, setLoadedVersion] = useState(0);
  const [problems, setProblems] = useState<WorkflowProblem[]>([]);
  const [saving, setSaving] = useState(false);
  const [message, setMessage] = useState("");
  const [textDraft, setTextDraft] = useState<string | null>(null);

  const linked = params.get("run");
  const [tab, setTab] = useState<Tab>(linked ? "run" : "edit");
  const [panel, setPanel] = useState(tab === "run");
  const [selected, setSelected] = useState<string | null>(null);
  const [selectedLink, setSelectedLink] = useState<number | null>(null);
  const [run, setRun] = useState<string | null>(linked);
  const [runFilter, setRunFilter] = useState<RunFilter>("attention");
  const [runStep, setRunStep] = useState<string | null>(null);
  const [runQuery, setRunQuery] = useState("");

  // A new version from the server replaces what is shown — unless someone is
  // in the middle of changing it.
  useEffect(() => {
    const w = wfQ.data;
    if (!w || dirty || w.version === loadedVersion) return;
    const d = fromWorkflow(w);
    setDoc(d);
    setNodes(d.order.map((id) => ({ id, type: "block", position: d.positions[id], data: {} })));
    setLoadedVersion(w.version);
    setProblems([]);
  }, [wfQ.data, dirty, loadedVersion]);

  const positions = useMemo(() => Object.fromEntries(nodes.map((n) => [n.id, n.position])), [nodes]);
  const source = useMemo(() => (doc ? toSource({ ...doc, positions }) : ""), [doc, positions]);

  // While the definition is changed, the server checks it as it would on save.
  useEffect(() => {
    if (!dirty || !source) return;
    const id = setTimeout(() => {
      checkWorkflow(source)
        .then((r) => setProblems(r.problems))
        .catch(() => undefined);
    }, 400);
    return () => clearTimeout(id);
  }, [dirty, source]);

  const change = (f: (d: Doc) => Doc) => {
    setDoc((d) => (d ? f(d) : d));
    setDirty(true);
    setMessage("");
  };

  const save = async (text?: string) => {
    setSaving(true);
    setMessage("");
    try {
      const res = await saveWorkflow(ref, text ?? source);
      if (!res.ok) {
        setProblems(res.problems);
        setMessage(res.error);
        return false;
      }
      setDirty(false);
      setLoadedVersion(0); // take up the saved version as the server parsed it
      setTextDraft(null);
      setMessage(res.warning ?? t("workflows.saved", { n: res.version }));
      await qc.invalidateQueries({ queryKey: ["workflow", ref] });
      await qc.invalidateQueries({ queryKey: ["workflows"] });
      return true;
    } catch (e) {
      setMessage((e as Error).message);
      return false;
    } finally {
      setSaving(false);
    }
  };

  const runs = useMemo(() => runsQ.data ?? [], [runsQ.data]);
  const runQ = useQuery({
    queryKey: ["workflow-run", ref, run],
    queryFn: () => getWorkflowRun(ref, run!),
    enabled: !!run,
    refetchInterval: 5000,
  });
  const path = useMemo(() => runPath(tab === "run" && run ? runQ.data : undefined), [tab, run, runQ.data]);

  const exitLabel = useCallback(
    (key: string) =>
      key === EXIT_LIMIT ? t("workflows.exit.limit") : key === EXIT_TIMEOUT ? t("workflows.exit.timeout") : key === EXIT_ELSE ? t("workflows.exit.else") : key === EXIT_OUT ? "" : key,
    [t],
  );
  const available = useCallback(
    (name: string) => {
      const a = agents.find((x) => x.slug === name);
      return !!a && !!a.hired_at && !a.killed;
    },
    [agents],
  );

  // The open runs, as cases at their step.
  const live = useMemo(() => {
    const out: Record<string, Case[]> = {};
    const now = Date.now();
    for (const r of runs) {
      if (!isOpen(r)) continue;
      const step = doc?.blocks[r.step];
      const age = Math.max(0, now - new Date(r.since).getTime());
      const mine = step?.type === "approval" && r.state === "waiting" && namesMe(step.cfg.who, me);
      // A colleague who parked the step on a question waits on a person, it
      // is not hung (#656) — unless the question reaches nobody.
      const parked = r.parked;
      const c: Case = {
        id: r.id,
        no: r.case,
        title: r.title || r.case,
        by: r.assignee ?? "",
        since: fmtDelta(age),
        age,
        state: parked ? "waiting" : r.state === "working" ? "working" : r.state === "queued" ? "queued" : "waiting",
        level: parked ? (parked.asked ? "warn" : "bad") : r.stuck || r.unstaffed ? "bad" : mine ? "warn" : "ok",
        why: parked
          ? parked.asked
            ? t("workflows.live.whyAsked", { name: parked.asked })
            : t("workflows.live.whyAskedNobody")
          : r.stuck
            ? t("workflows.live.whyStuck")
            : r.unstaffed
              ? t("workflows.live.whyUnstaffed")
              : undefined,
        mine,
      };
      (out[r.step] ??= []).push(c);
    }
    return out;
  }, [runs, doc, me, t]);

  const edges: Edge[] = useMemo(() => {
    if (!doc) return [];
    return doc.links.flatMap((l, i) => {
      const a = doc.blocks[l.from];
      const b = doc.blocks[l.to];
      const pa = positions[l.from];
      const pb = positions[l.to];
      if (!a || !b || !pa || !pb) return [];
      const back = pb.x < pa.x + NODE_W + 20;
      const cls: string[] = [];
      const ctx = { roles: doc.meta.roles, available };
      if (unstaffed(ctx, a) || unstaffed(ctx, b)) cls.push("dashed");
      if (tab === "run" && run) cls.push(path.links.has(`${l.from}>${l.to}`) ? "on" : "dim");
      if (tab === "edit" && selectedLink === i) cls.push("sel");
      const label = exitLabel(l.port);
      const low = Math.max(pa.y + heightOf(a), pb.y + heightOf(b)) + (a.type === "approval" ? 40 : 70);
      return [
        {
          id: `l${i}`,
          source: l.from,
          sourceHandle: l.port,
          target: l.to,
          type: back ? "loop" : "default",
          data: { low },
          label: back && label ? label : undefined,
          className: cls.join(" "),
          markerEnd: { type: MarkerType.ArrowClosed, width: 16, height: 16 },
        },
      ];
    });
  }, [doc, positions, tab, run, path, selectedLink, available, exitLabel]);

  const onNodesChange = useCallback((changes: NodeChange[]) => {
    const kept = changes.filter((c) => c.type === "position" || c.type === "dimensions");
    setNodes((ns) => applyNodeChanges(kept, ns));
    // A block put down somewhere else is a change to keep.
    if (kept.some((c) => c.type === "position" && c.dragging === false)) {
      setDirty(true);
      setMessage("");
    }
  }, []);

  const onConnect = useCallback((c: Connection) => {
    if (!c.source || !c.target || c.source === c.target) return;
    const port = c.sourceHandle ?? EXIT_OUT;
    setDoc((d) => {
      if (!d || d.blocks[c.target!]?.type === "trigger") return d;
      return { ...d, links: [...d.links.filter((l) => !(l.from === c.source && l.port === port)), { from: c.source!, port, to: c.target! }] };
    });
    setDirty(true);
  }, []);

  const addBlock = (type: BlockType) => {
    if (!doc) return;
    const box = document.querySelector(".wf-canvas")?.getBoundingClientRect();
    const at = box ? flow.screenToFlowPosition({ x: box.left + box.width / 2 - NODE_W / 2, y: box.top + box.height / 2 - 30 }) : { x: 200, y: 200 };
    let n = 1;
    while (doc.blocks[`${type[0]}${n}`]) n++;
    const id = `${type[0]}${n}`;
    change((d) => ({ ...d, order: [...d.order, id], blocks: { ...d.blocks, [id]: blankBlock(type, id, t(`workflows.type.${type}`)) } }));
    setNodes((ns) => [...ns, { id, type: "block", position: { x: Math.round(at.x / 10) * 10, y: Math.round(at.y / 10) * 10 }, data: {} }]);
    setSelected(id);
    setSelectedLink(null);
    setPanel(true);
  };

  const removeBlock = (id: string) => {
    change((d) => {
      const blocks = { ...d.blocks };
      delete blocks[id];
      return { ...d, blocks, order: d.order.filter((x) => x !== id), links: d.links.filter((l) => l.from !== id && l.to !== id) };
    });
    setNodes((ns) => ns.filter((n) => n.id !== id));
    setSelected(null);
  };

  const switchTab = (next: Tab) => {
    if (next === "file") setTextDraft(null);
    setTab(next);
    if (next !== "run") setSelected(null);
    setSelectedLink(null);
    setPanel(next === "live" || next === "run");
  };
  const openRun = (id: string) => {
    setRun(id);
    setSelected(null);
    setTab("run");
    setPanel(true);
  };
  const refreshRuns = async () => {
    await qc.invalidateQueries({ queryKey: ["workflow-runs", ref] });
    await qc.invalidateQueries({ queryKey: ["workflow-run", ref] });
    await qc.invalidateQueries({ queryKey: ["workflows-mine"] });
  };

  if (wfQ.isError) {
    return (
      <div className="wf-page wf-center">
        <p className="wf-muted">{isNotFound(wfQ.error) ? t("workflows.notFound") : (wfQ.error as Error).message}</p>
        <Link to="/workflows">{t("nav.workflows")}</Link>
      </div>
    );
  }
  if (!doc || !wfQ.data) return <div className="wf-page" />;

  const ctx: CanvasState = {
    blocks: doc.blocks,
    roles: doc.meta.roles,
    tab,
    mode: doc.meta.mode,
    selected: tab === "run" && !run ? runStep : selected,
    live,
    path,
    available,
    exitLabel,
  };
  const stuck = runs.filter((r) => isOpen(r) && (r.stuck || r.unstaffed) && !r.parked?.asked).length;

  // Where a run waits on a person, the card it puts into their chat.
  let waiting: React.ReactNode = null;
  const current: WorkflowRun | undefined = runQ.data;
  const at = current ? doc.blocks[current.step] : undefined;
  if (current && at?.type === "approval" && current.state === "waiting") {
    const may = manage || namesMe(at.cfg.who, me);
    waiting = (
      <DecisionCard
        recipient={String(at.cfg.who || doc.meta.owner)}
        kind={`${t("workflows.chat.kindApproval")} · ${current.case}`}
        question={renderAsk(String(at.cfg.ask ?? ""), current)}
        options={outsOf(at)
          .filter((o) => o.key !== EXIT_TIMEOUT)
          .map((o) => ({ key: o.key, label: o.key }))}
        onDecide={
          may
            ? async (option) => {
                await decideWorkflowRun(ref, current.id, option);
                await refreshRuns();
              }
            : undefined
        }
      />
    );
  } else if (current?.parked) {
    // The colleague's question, answered in the direct conversation with them.
    const who = agents.find((a) => a.slug === current.assignee)?.display_name || current.assignee || "";
    const kind = `${t("workflows.chat.kindQuestion", { name: who })} · ${current.case}`;
    waiting = current.parked.asked ? (
      <DecisionCard recipient={current.parked.asked} kind={kind} question={current.parked.question} options={[]} />
    ) : (
      <div className="wf-chatcard">
        <div className="wf-card">
          <div className="wf-card-kind">{kind}</div>
          <div className="wf-card-q">{current.parked.question}</div>
          <div className="wf-card-ctx">{t("workflows.live.whyAskedNobody")}</div>
        </div>
      </div>
    );
  }

  let side: React.ReactNode;
  if (tab === "edit")
    side = (
      <Inspector
        doc={doc}
        systems={systems}
        slug={ref}
        workflowId={wfQ.data.id}
        selected={selected}
        selectedLink={selectedLink}
        agents={agents}
        problems={problems}
        exitLabel={exitLabel}
        onBlock={(id, patch) => change((d) => ({ ...d, blocks: { ...d.blocks, [id]: { ...d.blocks[id], ...patch } } }))}
        onMeta={(patch: Partial<Meta>) => change((d) => ({ ...d, meta: { ...d.meta, ...patch } }))}
        onRemoveBlock={removeBlock}
        onRemoveLink={(i) => {
          change((d) => ({ ...d, links: d.links.filter((_, j) => j !== i) }));
          setSelectedLink(null);
        }}
        save={{ dirty, saving, version: wfQ.data.version, message, onSave: () => void save() }}
      />
    );
  else if (tab === "live")
    side = (
      <LiveSide
        blocks={doc.blocks}
        live={live}
        mode={doc.meta.mode}
        owner={doc.meta.owner}
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
      run && current ? (
        <RunSide
          run={current}
          blocks={doc.blocks}
          selected={selected}
          waiting={waiting}
          onStep={setSelected}
          onBack={() => {
            setRun(null);
            setSelected(null);
          }}
          onEnd={
            manage
              ? async (reason) => {
                  await cancelWorkflowRun(ref, current.id, reason);
                  await refreshRuns();
                }
              : undefined
          }
        />
      ) : (
        <RunList
          blocks={doc.blocks}
          live={live}
          finished={runs.filter((r) => !isOpen(r))}
          filter={runFilter}
          step={runStep}
          query={runQuery}
          canStart={manage && doc.meta.mode !== "off"}
          onFilter={setRunFilter}
          onStep={setRunStep}
          onQuery={setRunQuery}
          onOpen={openRun}
          onStart={async (caseRef, title) => {
            const r = await startWorkflowRun(ref, caseRef, title ? { title } : {});
            await refreshRuns();
            openRun(r.id);
          }}
        />
      );

  return (
    <div className="wf-page">
      <div className="wf-editor-head">
        <div className="wf-crumb">
          <Link to="/workflows">{t("nav.workflows")}</Link>
          <span>/</span>
          <b>{doc.meta.name}</b>
          <span className="wf-pill mute">{t("workflows.versionN", { n: wfQ.data.version })}</span>
          {dirty && (
            <button className="wf-pill clay wf-unsaved" onClick={() => void save()} disabled={saving || problems.length > 0 || !manage} title={problems[0]?.text}>
              {problems.length > 0 ? t("workflows.unsavedProblems", { count: problems.length }) : t("workflows.unsaved")}
            </button>
          )}
          {manage && !dirty && wfQ.data.mode === "off" && (
            <button className="wf-pill mute wf-delete" onClick={() => setDeleting("ask")}>
              {t("workflows.delete")}
            </button>
          )}
        </div>
        <div className="wf-seg mode" role="group" aria-label={t("workflows.modeLabel")}>
          {MODES.map((m) => (
            <button key={m} data-m={m} aria-pressed={doc.meta.mode === m} disabled={!manage} onClick={() => change((d) => ({ ...d, meta: { ...d.meta, mode: m } }))}>
              {t(`workflows.modeButton.${m}`)}
            </button>
          ))}
        </div>
        <div className="wf-seg wf-tabs" role="tablist" aria-label={t("workflows.viewLabel")}>
          {TABS.map((x) => (
            <button key={x} role="tab" aria-selected={tab === x} onClick={() => switchTab(x)}>
              {t(`workflows.tab.${x}`)}
              {x === "live" && stuck > 0 && doc.meta.mode !== "off" && <span className="wf-count">{stuck}</span>}
            </button>
          ))}
        </div>
      </div>
      <p className="wf-modebar">{t(`workflows.modeText.${doc.meta.mode}`)}</p>
      {deleting && (
        <ConfirmDialog
          title={t("workflows.deleteTitle", { name: doc.meta.name })}
          confirmLabel={t("workflows.delete")}
          pending={deleting === "busy"}
          onClose={() => {
            setDeleting(null);
            setDeleteError("");
          }}
          onConfirm={async () => {
            setDeleting("busy");
            try {
              await deleteWorkflow(ref);
              await qc.invalidateQueries({ queryKey: ["workflows"] });
              navigate("/workflows");
            } catch (e) {
              setDeleteError((e as Error).message);
              setDeleting("ask");
            }
          }}
        >
          <p>{t("workflows.deleteText")}</p>
          {deleteError && <p className="wf-error">{deleteError}</p>}
        </ConfirmDialog>
      )}

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
              nodesDraggable={tab === "edit" && manage}
              nodesConnectable={tab === "edit" && manage}
              elementsSelectable={false}
              deleteKeyCode={null}
              snapToGrid
              snapGrid={[10, 10]}
              fitView
              fitViewOptions={{ padding: FIT_PADDING }}
              minZoom={0.25}
              maxZoom={2}
              proOptions={{ hideAttribution: true }}
            >
              <Background variant={BackgroundVariant.Dots} gap={20} size={1} />
              <Controls showInteractive={false} position="bottom-right" />
              <FitOnTab tab={tab} />
            </ReactFlow>
          </CanvasContext.Provider>

          {tab === "edit" && manage && (
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
            <span />
            {tab !== "file" && !panel && (
              <button className="wf-details-btn" onClick={() => setPanel(true)}>
                {t("workflows.details")}
              </button>
            )}
          </div>
          {tab !== "file" && <div className="wf-legend">{t(`workflows.legend.${tab === "run" && !run ? "runs" : tab}`, { no: current?.case ?? "" })}</div>}
          {tab === "file" && (
            <div className="wf-file">
              <div className="wf-file-head">
                <span className="wf-pill info">{wfQ.data.slug}.workflow</span>
                <span>{t("workflows.file.head")}</span>
              </div>
              <div className="wf-file-grid">
                <div>
                  <textarea
                    className="wf-source"
                    spellCheck={false}
                    readOnly={!manage}
                    value={textDraft ?? source}
                    onChange={(e) => {
                      setTextDraft(e.target.value);
                      checkWorkflow(e.target.value)
                        .then((r) => setProblems(r.problems))
                        .catch(() => undefined);
                    }}
                  />
                </div>
                <div className="wf-file-side">
                  <FileSide />
                  {problems.length > 0 && (
                    <ul className="wf-list wf-sec">
                      {problems.map((pr, i) => (
                        <li key={i}>
                          <span className="wf-pill warn">{pr.step || t("workflows.workflow")}</span>
                          <span>{pr.text}</span>
                        </li>
                      ))}
                    </ul>
                  )}
                  {manage && (
                    <div className="wf-sec">
                      <button className="btn primary sm" disabled={saving || textDraft === null || problems.length > 0} onClick={() => void save(textDraft ?? undefined)}>
                        {t("workflows.file.save")}
                      </button>
                      {message && <p className="wf-hint">{message}</p>}
                    </div>
                  )}
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
    </div>
  );
}

// The question as it was asked: the run's reports fill its placeholders.
function renderAsk(text: string, run: WorkflowRun): string {
  return text.replace(/\{\{\s*([A-Za-z0-9_-]+)\.([A-Za-z0-9_-]+)\s*\}\}/g, (m, step: string, field: string) => run.vars?.[step]?.[field] ?? m);
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
