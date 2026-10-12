/* The right-hand column of the workflow preview (#560) outside editing:
   what stands where now, one run step by step, and the file. */
import { useTranslation } from "react-i18next";
import { BlockIcon } from "./canvas";
import { type Block, type Case, type Example, type Mode, type Run } from "./model";

export function LiveSide({
  blocks,
  live,
  mode,
  selected,
  onCase,
}: {
  blocks: Record<string, Block>;
  live: Example["live"];
  mode: Mode;
  selected: string | null;
  onCase: (no: string, block?: string) => void;
}) {
  const { t, i18n } = useTranslation();
  if (mode === "off") {
    return (
      <div>
        <Head type="agent" eyebrow={t("workflows.tab.live")} title={t("workflows.live.offTitle")} />
        <p className="wf-muted">{t("workflows.live.offText")}</p>
      </div>
    );
  }
  const row = (c: Case, block?: string) => (
    <button key={c.no + (block ?? "")} className="wf-case" onClick={() => onCase(c.no, block)}>
      <span className="wf-case-no">{c.no}</span>
      <span className="wf-case-text">
        {block ? `${blocks[block]?.title}: ${c.why}` : c.title}
        <small>
          {t("workflows.live.with", { name: c.by })}
          {!block && c.why ? ` · ${c.why}` : ""}
        </small>
      </span>
      <span className={`wf-pill ${c.level === "bad" ? "bad" : c.level === "warn" ? "warn" : "mute"}`}>{c.since}</span>
    </button>
  );
  if (selected && blocks[selected]) {
    const b = blocks[selected];
    const here = live[selected] ?? { cases: [] };
    return (
      <div>
        <Head type={b.type} eyebrow={t("workflows.live.nowAt", { type: t(`workflows.type.${b.type}`) })} title={b.title} />
        {here.note && <div className="wf-note warn">{here.note}</div>}
        {here.cases.length ? <div className="wf-cases">{here.cases.map((c) => row(c))}</div> : <p className="wf-muted">{t("workflows.live.nothingHere")}</p>}
      </div>
    );
  }
  const all = Object.values(live).flatMap((l) => l.cases);
  const stuck = all.filter((c) => c.level === "bad").length;
  const attention = Object.entries(live).flatMap(([id, l]) => l.cases.filter((c) => c.level !== "ok").map((c) => row(c, id)));
  return (
    <div>
      <Head type="agent" eyebrow={mode === "watch" ? `${t("workflows.tab.live")} · ${t("workflows.mode.watch")}` : t("workflows.tab.live")} title={t("workflows.live.title")} />
      {mode === "watch" && <div className="wf-note info">{t("workflows.live.watchNote")}</div>}
      <div className="wf-stats">
        <div className="wf-stat">
          <div className="v">{all.length}</div>
          <div className="l">{t("workflows.live.cases")}</div>
        </div>
        <div className="wf-stat">
          <div className="v danger">{stuck}</div>
          <div className="l">{t("workflows.live.stuck")}</div>
        </div>
        <div className="wf-stat">
          <div className="v">{(2.8).toLocaleString(i18n.language)}</div>
          <div className="l">{t("workflows.live.median")}</div>
        </div>
      </div>
      <p className="wf-eyebrow">{t("workflows.live.attention")}</p>
      <div className="wf-cases">{attention.length ? attention : <p className="wf-muted">{t("workflows.live.nothing")}</p>}</div>
      <p className="wf-hint">{t("workflows.live.hint")}</p>
    </div>
  );
}

export function RunSide({
  run,
  blocks,
  selected,
  waiting,
  onStep,
}: {
  run: Run;
  blocks: Record<string, Block>;
  selected: string | null;
  // The card the run waits on, as it lies in someone's chat.
  waiting?: React.ReactNode;
  onStep: (block: string) => void;
}) {
  const { t } = useTranslation();
  return (
    <div>
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
