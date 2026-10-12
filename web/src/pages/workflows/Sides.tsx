/* The side panels of the workflow preview (#560) outside editing: what
   stands where now, the list of runs, one run step by step, and the file.
   They are built for many runs at once: counts before lists, the cases that
   need someone first, and a list one can filter rather than scroll. */
import { useTranslation } from "react-i18next";
import { BlockIcon } from "./canvas";
import { DigestCard } from "./ChatCards";
import { type Block, type Case, type Example, type Mode, type Run } from "./model";

export type RunFilter = "all" | "attention" | "mine" | "queued";

export interface Placed {
  c: Case;
  block: string;
}

export function placed(live: Example["live"]): Placed[] {
  return Object.entries(live).flatMap(([block, l]) => l.cases.map((c) => ({ c, block })));
}

const rank = { bad: 0, warn: 1, ok: 2 } as const;
// What needs someone first, then what waits on the viewer, then the oldest.
export function byAttention(a: Placed, b: Placed): number {
  return rank[a.c.level] - rank[b.c.level] || Number(!!b.c.mine) - Number(!!a.c.mine) || b.c.age - a.c.age;
}

function CaseRow({ p, blocks, showStep, onOpen }: { p: Placed; blocks: Record<string, Block>; showStep?: boolean; onOpen: (no: string) => void }) {
  const { t } = useTranslation();
  const { c } = p;
  const detail = [showStep ? blocks[p.block]?.title : null, c.state === "queued" || c.mine ? null : c.by, c.why].filter(Boolean).join(" · ");
  return (
    <button className="wf-case" onClick={() => onOpen(c.no)}>
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
  live: Example["live"];
  mode: Mode;
  owner: string;
  selected: string | null;
  onOpen: (no: string) => void;
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
              <CaseRow key={p.c.no} p={p} blocks={blocks} onOpen={onOpen} />
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
        <Head type={b.type} eyebrow={t("workflows.live.nowAt", { type: t(`workflows.type.${b.type}`) })} title={b.title} />
        {live[selected]?.note && <div className="wf-note warn">{live[selected].note}</div>}
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
        {attention.length ? attention.slice(0, 5).map((p) => <CaseRow key={p.c.no} p={p} blocks={blocks} showStep onOpen={onOpen} />) : <p className="wf-muted">{t("workflows.live.nothing")}</p>}
      </div>
      {attention.length > 5 && (
        <button className="wf-more" onClick={() => onList("attention")}>
          {t("workflows.live.allInList", { count: attention.length })}
        </button>
      )}
      <DigestCard
        recipient={owner}
        stuck={count((p) => p.c.level === "bad")}
        queue={queues[0] ? { n: queues[0].n, role: String(queues[0].b.cfg.role ?? queues[0].b.title) } : undefined}
        mine={count((p) => !!p.c.mine)}
      />
      <p className="wf-hint">{t("workflows.live.hint")}</p>
    </div>
  );
}

export function RunList({
  blocks,
  live,
  filter,
  step,
  query,
  onFilter,
  onStep,
  onQuery,
  onOpen,
}: {
  blocks: Record<string, Block>;
  live: Example["live"];
  filter: RunFilter;
  step: string | null;
  query: string;
  onFilter: (f: RunFilter) => void;
  onStep: (s: string | null) => void;
  onQuery: (q: string) => void;
  onOpen: (no: string) => void;
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
    .filter((p) => !q || p.c.no.includes(q) || p.c.title.toLowerCase().includes(q))
    .sort(byAttention);
  const steps = Object.values(blocks).filter((b) => all.some((p) => p.block === b.id));
  return (
    <div>
      <Head type="trigger" eyebrow={t("workflows.tab.run")} title={t("workflows.runs.result", { count: shown.length })} />
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
            {b.title}
          </option>
        ))}
      </select>
      <div className="wf-cases">
        {shown.map((p) => (
          <CaseRow key={p.c.no} p={p} blocks={blocks} showStep={!step} onOpen={onOpen} />
        ))}
        {shown.length === 0 && <p className="wf-muted">{t("workflows.runs.none")}</p>}
      </div>
    </div>
  );
}

export function RunSide({
  run,
  blocks,
  selected,
  waiting,
  onStep,
  onBack,
}: {
  run: Run;
  blocks: Record<string, Block>;
  selected: string | null;
  // The card the run waits on, as it lies in someone's chat.
  waiting?: React.ReactNode;
  onStep: (block: string) => void;
  onBack: () => void;
}) {
  const { t } = useTranslation();
  return (
    <div>
      <button className="wf-back" onClick={onBack}>
        ← {t("workflows.runs.back")}
      </button>
      <Head type="trigger" eyebrow={t("workflows.run.title", { no: run.no })} title={run.title} />
      <ol className="wf-timeline">
        {run.steps.map((s, i) => (
          <li
            key={i}
            className={`${s.now ? "now" : s.skip ? "skip" : s.chat ? "chat" : "done"}${selected === s.block ? " sel" : ""}`}
            onClick={() => onStep(s.block)}
          >
            <div>
              <b>{blocks[s.block]?.title ?? s.block}</b> <span className="wf-when">{s.when}</span>
              {s.chat && <span className="wf-pill warn">{t("workflows.run.chatTag")}</span>}
            </div>
            <div>{s.text}</div>
            {s.out && (
              <div className="wf-out">
                {Object.entries(s.out).map(([k, v]) => (
                  <div key={k}>
                    <span className="wf-var">{k}</span> {v}
                  </div>
                ))}
              </div>
            )}
          </li>
        ))}
      </ol>
      {waiting}
      <div className="wf-sec wf-row">
        <button className="btn sm">{t("workflows.run.reassign")}</button>
        <button className="btn sm">{t("workflows.run.pause")}</button>
      </div>
    </div>
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
      <div className="wf-sec wf-row">
        <button className="btn sm">{t("workflows.file.export")}</button>
        <button className="btn sm">{t("workflows.file.import")}</button>
      </div>
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
