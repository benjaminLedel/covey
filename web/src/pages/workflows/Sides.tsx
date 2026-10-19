/* The side panels outside editing (#560, #565): what stands where now, the
   list of runs, one run step by step, and the file. They are built for many
   runs at once: counts before lists, the cases that need someone first, and
   a list one can filter rather than scroll. */
import { useState } from "react";
import { useTranslation } from "react-i18next";
import type { WorkflowRun } from "../../api";
import { BlockIcon } from "./canvas";
import { DigestCard } from "./ChatCards";
import { isOpen, type Block, type Case, type Mode } from "./model";

export type RunFilter = "all" | "attention" | "mine" | "queued";

export interface Placed {
  c: Case;
  block: string;
}

export function placed(live: Record<string, Case[]>): Placed[] {
  return Object.entries(live).flatMap(([block, cases]) => cases.map((c) => ({ c, block })));
}

const rank = { bad: 0, warn: 1, ok: 2 } as const;
// What needs someone first, then what waits on the viewer, then the oldest.
export function byAttention(a: Placed, b: Placed): number {
  return rank[a.c.level] - rank[b.c.level] || Number(!!b.c.mine) - Number(!!a.c.mine) || b.c.age - a.c.age;
}

function CaseRow({ p, blocks, showStep, onOpen }: { p: Placed; blocks: Record<string, Block>; showStep?: boolean; onOpen: (id: string) => void }) {
  const { t } = useTranslation();
  const { c } = p;
  const step = blocks[p.block];
  const detail = [showStep ? step?.title || p.block : null, c.state === "queued" || c.mine ? null : c.by, c.why].filter(Boolean).join(" · ");
  return (
    <button className="wf-case" onClick={() => onOpen(c.id)}>
      <span className="wf-case-no">{c.no}</span>
      <span className="wf-case-text">
        {c.title}
        {detail && <small>{detail}</small>}
      </span>
      {c.mine && <span className="wf-pill clay">{t("workflows.runs.mineTag")}</span>}
      <span className={`wf-pill ${c.level === "bad" ? "bad" : c.level === "warn" ? "warn" : "mute"}`}>{c.since}</span>
    </button>
  );
}

export function LiveSide({
  blocks,
  live,
  mode,
  owner,
  selected,
  onOpen,
  onList,
}: {
  blocks: Record<string, Block>;
  live: Record<string, Case[]>;
  mode: Mode;
  owner: string;
  selected: string | null;
  onOpen: (id: string) => void;
  onList: (f: RunFilter, step?: string) => void;
}) {
  const { t } = useTranslation();
  if (mode === "off") {
    return (
      <div>
        <Head type="agent" eyebrow={t("workflows.tab.live")} title={t("workflows.live.offTitle")} />
        <p className="wf-muted">{t("workflows.live.offText")}</p>
      </div>
    );
  }
  const all = placed(live);

  if (selected && blocks[selected]) {
    const b = blocks[selected];
    const here = all.filter((p) => p.block === selected);
    const group = (state: Case["state"], label: string) => {
      const xs = here.filter((p) => p.c.state === state).sort((x, y) => y.c.age - x.c.age);
      if (!xs.length) return null;
      return (
        <div className="wf-group">
          <p className="wf-eyebrow">
            {label} · {xs.length}
          </p>
          <div className="wf-cases">
            {xs.slice(0, 6).map((p) => (
              <CaseRow key={p.c.id} p={p} blocks={blocks} onOpen={onOpen} />
            ))}
          </div>
          {xs.length > 6 && (
            <button className="wf-more" onClick={() => onList(state === "queued" ? "queued" : "all", selected)}>
              {t("workflows.live.allInList", { count: xs.length })}
            </button>
          )}
        </div>
      );
    };
    return (
      <div>
        <Head type={b.type} eyebrow={t("workflows.live.nowAt", { type: t(`workflows.type.${b.type}`) })} title={b.title || b.id} />
        {here.length === 0 && <p className="wf-muted">{t("workflows.live.nothingHere")}</p>}
        {group("working", t("workflows.live.groupWorking"))}
        {group("queued", t("workflows.live.groupQueued"))}
        {group("waiting", t("workflows.live.groupWaiting"))}
      </div>
    );
  }

  const count = (f: (p: Placed) => boolean) => all.filter(f).length;
  const attention = all.filter((p) => p.c.level !== "ok" || p.c.mine).sort(byAttention);
  const queues = Object.entries(blocks)
    .map(([id, b]) => ({ b, n: all.filter((p) => p.block === id && p.c.state === "queued").length }))
    .filter((x) => x.n > 0)
    .sort((x, y) => y.n - x.n);
  return (
    <div>
      <Head type="agent" eyebrow={mode === "watch" ? `${t("workflows.tab.live")} · ${t("workflows.mode.watch")}` : t("workflows.tab.live")} title={t("workflows.live.title")} />
      {mode === "watch" && <div className="wf-note info">{t("workflows.live.watchNote")}</div>}
      <div className="wf-stats">
        <button className="wf-stat" onClick={() => onList("all")}>
          <div className="v">{count((p) => p.c.state === "working")}</div>
          <div className="l">{t("workflows.live.statWorking")}</div>
        </button>
        <button className="wf-stat" onClick={() => onList("queued")}>
          <div className="v warn">{count((p) => p.c.state === "queued")}</div>
          <div className="l">{t("workflows.live.statQueued")}</div>
        </button>
        <button className="wf-stat" onClick={() => onList("attention")}>
          <div className="v danger">{count((p) => p.c.level === "bad")}</div>
          <div className="l">{t("workflows.live.stuck")}</div>
        </button>
      </div>
      <p className="wf-eyebrow">{t("workflows.live.attention")}</p>
      <div className="wf-cases">
        {attention.length ? attention.slice(0, 5).map((p) => <CaseRow key={p.c.id} p={p} blocks={blocks} showStep onOpen={onOpen} />) : <p className="wf-muted">{t("workflows.live.nothing")}</p>}
      </div>
      {attention.length > 5 && (
        <button className="wf-more" onClick={() => onList("attention")}>
          {t("workflows.live.allInList", { count: attention.length })}
        </button>
      )}
      {owner && (
        <DigestCard
          recipient={owner}
          stuck={count((p) => p.c.level === "bad")}
          queue={queues[0] ? { n: queues[0].n, role: String(queues[0].b.cfg.role || queues[0].b.title) } : undefined}
          mine={count((p) => !!p.c.mine)}
        />
      )}
      <p className="wf-hint">{t("workflows.live.hint")}</p>
    </div>
  );
}

export function RunList({
  blocks,
  live,
  finished,
  filter,
  step,
  query,
  canStart,
  onFilter,
  onStep,
  onQuery,
  onOpen,
  onStart,
}: {
  blocks: Record<string, Block>;
  live: Record<string, Case[]>;
  finished: WorkflowRun[];
  filter: RunFilter;
  step: string | null;
  query: string;
  canStart: boolean;
  onFilter: (f: RunFilter) => void;
  onStep: (s: string | null) => void;
  onQuery: (q: string) => void;
  onOpen: (id: string) => void;
  onStart: (caseRef: string, title: string) => Promise<void>;
}) {
  const { t } = useTranslation();
  const all = placed(live);
  const tests: Record<RunFilter, (p: Placed) => boolean> = {
    all: () => true,
    attention: (p) => p.c.level !== "ok" || !!p.c.mine,
    mine: (p) => !!p.c.mine,
    queued: (p) => p.c.state === "queued",
  };
  const q = query.trim().toLowerCase();
  const shown = all
    .filter(tests[filter])
    .filter((p) => !step || p.block === step)
    .filter((p) => !q || p.c.no.toLowerCase().includes(q) || p.c.title.toLowerCase().includes(q))
    .sort(byAttention);
  const steps = Object.values(blocks).filter((b) => all.some((p) => p.block === b.id));
  return (
    <div>
      <Head type="trigger" eyebrow={t("workflows.tab.run")} title={t("workflows.runs.result", { count: shown.length })} />
      {canStart && <StartRun onStart={onStart} />}
      <input className="input wf-search" value={query} onChange={(e) => onQuery(e.target.value)} placeholder={t("workflows.runs.search")} />
      <div className="wf-filters">
        {(["all", "attention", "mine", "queued"] as RunFilter[]).map((f) => (
          <button key={f} className={filter === f ? "on" : ""} onClick={() => onFilter(f)}>
            {t(`workflows.runs.filter_${f}`)} <span>{all.filter(tests[f]).filter((p) => !step || p.block === step).length}</span>
          </button>
        ))}
      </div>
      <select className="input wf-search" value={step ?? ""} onChange={(e) => onStep(e.target.value || null)}>
        <option value="">{t("workflows.runs.allSteps")}</option>
        {steps.map((b) => (
          <option key={b.id} value={b.id}>
            {b.title || b.id}
          </option>
        ))}
      </select>
      <div className="wf-cases">
        {shown.map((p) => (
          <CaseRow key={p.c.id} p={p} blocks={blocks} showStep={!step} onOpen={onOpen} />
        ))}
        {shown.length === 0 && <p className="wf-muted">{t("workflows.runs.none")}</p>}
      </div>
      {finished.length > 0 && (
        <div className="wf-sec">
          <p className="wf-eyebrow">{t("workflows.runs.finished")}</p>
          <div className="wf-cases">
            {finished.slice(0, 10).map((r) => (
              <button key={r.id} className="wf-case" onClick={() => onOpen(r.id)}>
                <span className="wf-case-no">{r.case}</span>
                <span className="wf-case-text">{r.title || r.case}</span>
                <span className={`wf-pill ${r.state === "failed" ? "bad" : "mute"}`}>{t(`workflows.runs.state_${r.state}`)}</span>
              </button>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}

function StartRun({ onStart }: { onStart: (caseRef: string, title: string) => Promise<void> }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [caseRef, setCaseRef] = useState("");
  const [title, setTitle] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  if (!open)
    return (
      <button className="btn sm" onClick={() => setOpen(true)} style={{ marginBottom: 10 }}>
        {t("workflows.runs.start")}
      </button>
    );
  return (
    <form
      className="wf-start"
      onSubmit={async (e) => {
        e.preventDefault();
        if (!caseRef.trim()) return;
        setBusy(true);
        setError("");
        try {
          await onStart(caseRef.trim(), title.trim());
          setCaseRef("");
          setTitle("");
          setOpen(false);
        } catch (err) {
          setError((err as Error).message);
        } finally {
          setBusy(false);
        }
      }}
    >
      <input className="input" value={caseRef} onChange={(e) => setCaseRef(e.target.value)} placeholder={t("workflows.runs.startCase")} />
      <input className="input" value={title} onChange={(e) => setTitle(e.target.value)} placeholder={t("workflows.runs.startTitle")} />
      <div className="wf-row">
        <button className="btn primary sm" type="submit" disabled={busy || !caseRef.trim()}>
          {t("workflows.runs.startGo")}
        </button>
        <button className="btn sm" type="button" onClick={() => setOpen(false)}>
          {t("workflows.runs.cancel")}
        </button>
      </div>
      {error && <p className="wf-hint" style={{ color: "var(--text-danger)" }}>{error}</p>}
    </form>
  );
}

export function RunSide({
  run,
  blocks,
  selected,
  waiting,
  onStep,
  onBack,
  onEnd,
}: {
  run: WorkflowRun;
  blocks: Record<string, Block>;
  selected: string | null;
  // The card the run waits on, as it lies in someone's chat.
  waiting?: React.ReactNode;
  onStep: (block: string) => void;
  onBack: () => void;
  // Ends an open run by hand (#598); absent for whoever may not.
  onEnd?: (reason: string) => Promise<void>;
}) {
  const { t, i18n } = useTranslation();
  const log = run.log ?? [];
  const when = (at: string) => new Date(at).toLocaleString(i18n.language, { weekday: "short", hour: "2-digit", minute: "2-digit" });
  return (
    <div>
      <button className="wf-back" onClick={onBack}>
        ← {t("workflows.runs.back")}
      </button>
      <Head type="trigger" eyebrow={t("workflows.run.title", { no: run.case })} title={run.title || run.case} />
      <p className="wf-muted" style={{ marginTop: -6, marginBottom: 12 }}>
        {t(`workflows.runs.state_${run.state}`)}
        {run.assignee ? ` · ${run.assignee}` : ""}
      </p>
      <ol className="wf-timeline">
        {log.map((e, i) => {
          const last = i === log.length - 1 && isOpen(run);
          return (
            <li key={i} className={`${last ? "now" : "done"}${selected === e.step ? " sel" : ""}`} onClick={() => onStep(e.step)}>
              <div>
                <b>{blocks[e.step]?.title || e.step}</b> <span className="wf-when">{when(e.at)}</span>
              </div>
              <div>{e.text}</div>
            </li>
          );
        })}
      </ol>
      {waiting}
      {run.vars && Object.keys(run.vars).some((k) => k !== "case" && blocks[k]?.type !== "trigger") && (
        <div className="wf-sec">
          <p className="wf-eyebrow">{t("workflows.run.reported")}</p>
          <div className="wf-out">
            {Object.entries(run.vars)
              .filter(([k]) => k !== "case" && blocks[k]?.type !== "trigger")
              .flatMap(([k, fields]) =>
                Object.entries(fields).map(([f, v]) => (
                  <div key={k + f}>
                    <span className="wf-var">
                      {k}.{f}
                    </span>{" "}
                    {v}
                  </div>
                )),
              )}
          </div>
        </div>
      )}
      {onEnd && isOpen(run) && <EndRun key={run.id} onEnd={onEnd} />}
    </div>
  );
}

// EndRun asks once more in the panel before a run is ended — no browser
// dialog — and takes an optional reason for the run's log.
function EndRun({ onEnd }: { onEnd: (reason: string) => Promise<void> }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  if (!open) {
    return (
      <div className="wf-sec">
        <button className="btn sm" onClick={() => setOpen(true)}>
          {t("workflows.runs.end")}
        </button>
      </div>
    );
  }
  return (
    <form
      className="wf-sec wf-start"
      onSubmit={async (e) => {
        e.preventDefault();
        setBusy(true);
        setError(null);
        try {
          await onEnd(reason.trim());
        } catch (err) {
          setError((err as Error).message);
          setBusy(false);
        }
      }}
    >
      <p className="wf-hint">{t("workflows.runs.endHint")}</p>
      <input className="input" value={reason} onChange={(e) => setReason(e.target.value)} placeholder={t("workflows.runs.endReason")} />
      <div className="wf-row">
        <button className="btn primary sm" type="submit" disabled={busy}>
          {t("workflows.runs.endConfirm")}
        </button>
        <button className="btn sm" type="button" onClick={() => setOpen(false)} disabled={busy}>
          {t("workflows.runs.cancel")}
        </button>
      </div>
      {error && <p className="wf-hint" style={{ color: "var(--text-danger)" }}>{error}</p>}
    </form>
  );
}

export function FileSide() {
  const { t } = useTranslation();
  return (
    <div>
      <Head type="action" eyebrow={t("workflows.tab.file")} title={t("workflows.file.title")} />
      <ul className="wf-list">
        <li>{t("workflows.file.one")}</li>
        <li>{t("workflows.file.two")}</li>
        <li>{t("workflows.file.three")}</li>
      </ul>
    </div>
  );
}

function Head({ type, eyebrow, title }: { type: Block["type"]; eyebrow: string; title: string }) {
  return (
    <div className="wf-insp-head">
      <BlockIcon type={type} />
      <div className="min-w-0">
        <div className="wf-node-type">{eyebrow}</div>
        <h2>{title}</h2>
      </div>
    </div>
  );
}
