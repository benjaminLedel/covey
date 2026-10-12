/* The right-hand column of the workflow preview (#560) while editing: the
   settings of the selected block, of a connection, or of the workflow. */
import { useRef } from "react";
import { useTranslation } from "react-i18next";
import { BlockIcon } from "./canvas";
import { DecisionCard } from "./ChatCards";
import { checks, outsOf, available, type Block, type Example, type Link } from "./model";

type Cfg = Block["cfg"];

interface Props {
  ex: Example;
  blocks: Record<string, Block>;
  links: Link[];
  selected: string | null;
  selectedLink: number | null;
  onBlock: (id: string, patch: { title?: string; cfg?: Cfg }) => void;
  onRemoveBlock: (id: string) => void;
  onRemoveLink: (i: number) => void;
}

const VARS = ["{{ticket.no}}", "{{ticket.title}}", "{{build.mr}}", "{{accept.result}}", "{{accept.defects}}"];

function Field({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
  return (
    <label className="wf-field">
      <span className="wf-field-label">{label}</span>
      {children}
      {hint && <span className="wf-hint">{hint}</span>}
    </label>
  );
}

export default function Inspector(p: Props) {
  const { t } = useTranslation();
  const task = useRef<HTMLTextAreaElement>(null);

  if (p.selectedLink !== null && p.links[p.selectedLink]) {
    const l = p.links[p.selectedLink];
    const from = p.blocks[l.from];
    const to = p.blocks[l.to];
    const label = outsOf(from).find((o) => o.key === l.port)?.label;
    return (
      <div>
        <div className="wf-insp-head">
          <div>
            <div className="wf-node-type">{t("workflows.link.title")}</div>
            <h2>
              {from.title} → {to.title}
            </h2>
          </div>
        </div>
        {label && <p className="wf-muted">{t("workflows.link.branch", { label })}</p>}
        {from.type === "condition" && l.port === "no" && <div className="wf-note info">{t("workflows.link.back")}</div>}
        <button className="btn sm danger" onClick={() => p.onRemoveLink(p.selectedLink!)}>
          {t("workflows.link.remove")}
        </button>
      </div>
    );
  }

  const b = p.selected ? p.blocks[p.selected] : null;
  if (!b) return <Overview {...p} />;

  const c = b.cfg;
  const set = (k: string, v: Cfg[string]) => p.onBlock(b.id, { cfg: { ...c, [k]: v } });
  const text = (k: string) => <input className="input" value={String(c[k] ?? "")} onChange={(e) => set(k, e.target.value)} />;
  const area = (k: string, ref?: React.Ref<HTMLTextAreaElement>) => (
    <textarea ref={ref} className="input wf-area" value={String(c[k] ?? "")} onChange={(e) => set(k, e.target.value)} />
  );
  const select = (k: string, options: [string, string][]) => (
    <select className="input" value={String(c[k] ?? "")} onChange={(e) => set(k, e.target.value)}>
      {options.map(([v, l]) => (
        <option key={v} value={v}>
          {l}
        </option>
      ))}
    </select>
  );
  const same = (xs: string[]): [string, string][] => xs.map((x) => [x, x]);
  const check = (k: string, label: string) => (
    <label className="wf-check">
      <input type="checkbox" checked={Boolean(c[k])} onChange={(e) => set(k, e.target.checked)} />
      <span>{label}</span>
    </label>
  );

  let body: React.ReactNode = null;
  switch (b.type) {
    case "trigger":
      body = (
        <>
          <Field label={t("workflows.trigger.source")}>{select("source", same(p.ex.options.sources))}</Field>
          <Field label={t("workflows.trigger.event")}>{text("event")}</Field>
          <Field label={t("workflows.trigger.filter")} hint={t("workflows.trigger.filterHint")}>
            {text("filter")}
          </Field>
          <div className="wf-note info">{t("workflows.trigger.chatNote")}</div>
        </>
      );
      break;
    case "agent": {
      const role = String(c.role);
      const members = p.ex.roles[role] ?? [];
      const free = available(p.ex, role);
      const others = Object.values(p.blocks).filter((x) => x.type === "agent" && x.id !== b.id);
      body = (
        <>
          {free.length === 0 && (
            <div className="wf-note warn">
              {t("workflows.agent.empty", { role })} {c.optional ? t("workflows.agent.emptySkip") : t("workflows.agent.emptyOwner")}
            </div>
          )}
          <Field label={t("workflows.agent.role")} hint={t("workflows.agent.roleHint")}>
            {select("role", same(Object.keys(p.ex.roles)))}
          </Field>
          <div className="wf-members">
            {members.map((m) => {
              const person = p.ex.people[m];
              const st = person.state;
              return (
                <div key={m} className="wf-member">
                  <span className="wf-avatar">{m.slice(0, 2).toUpperCase()}</span>
                  {m}
                  <span className={`wf-pill ${st === "ok" ? "ok" : st === "stopped" ? "warn" : "mute"}`}>
                    {st === "ok" ? t("workflows.agent.open", { count: person.load }) : t(`workflows.agent.state_${st}`)}
                  </span>
                </div>
              );
            })}
            <button className="btn sm" type="button">
              {t("workflows.agent.addMember")}
            </button>
          </div>
          {members.length > 1 && (
            <>
              <Field label={t("workflows.agent.pick")}>
                {select("pick", [
                  ["load", t("workflows.agent.pick_load")],
                  ["round", t("workflows.agent.pick_round")],
                  ["skill", t("workflows.agent.pick_skill")],
                ])}
              </Field>
              <Field label={t("workflows.agent.cap")}>{select("cap", [["1", "1"], ["2", "2"], ["3", "3"], ["", t("workflows.agent.capNone")]])}</Field>
            </>
          )}
          {check("sticky", t("workflows.agent.sticky"))}
          {others.length > 0 && (
            <Field label={t("workflows.agent.notSame")} hint={t("workflows.agent.notSameHint")}>
              {select("notSame", [["", "–"], ...others.map((x): [string, string] => [x.id, x.title])])}
            </Field>
          )}
          {check("optional", t("workflows.agent.optional"))}
          <Field label={t("workflows.agent.task")} hint={t("workflows.agent.taskHint")}>
            {area("task", task)}
          </Field>
          <div className="wf-vars">
            {VARS.map((v) => (
              <button
                key={v}
                type="button"
                className="wf-var"
                onClick={() => {
                  const el = task.current;
                  if (!el) return;
                  const at = el.selectionStart ?? el.value.length;
                  set("task", el.value.slice(0, at) + v + el.value.slice(el.selectionEnd ?? at));
                }}
              >
                {v}
              </button>
            ))}
          </div>
          <Field label={t("workflows.agent.result")} hint={t("workflows.agent.resultHint")}>
            <input
              className="input"
              value={(c.output as string[]).join(", ")}
              onChange={(e) => set("output", e.target.value.split(",").map((s) => s.trim()).filter(Boolean))}
            />
          </Field>
          <Field label={t("workflows.agent.ask")} hint={t("workflows.agent.askHint")}>
            {select("ask", same(p.ex.options.askTo))}
          </Field>
          <Field label={t("workflows.agent.limit")} hint={t("workflows.agent.limitHint")}>
            {select("limit", same(p.ex.options.limits))}
          </Field>
        </>
      );
      break;
    }
    case "ai":
      body = (
        <>
          <div className="wf-note info">{t("workflows.ai.note")}</div>
          <Field label={t("workflows.ai.prompt")}>{area("prompt")}</Field>
          <Field label={t("workflows.agent.result")}>
            <input
              className="input"
              value={(c.output as string[]).join(", ")}
              onChange={(e) => set("output", e.target.value.split(",").map((s) => s.trim()).filter(Boolean))}
            />
          </Field>
          <Field label={t("workflows.ai.model")}>
            {select("model", [
              ["small", t("workflows.ai.model_small")],
              ["large", t("workflows.ai.model_large")],
            ])}
          </Field>
        </>
      );
      break;
    case "condition":
      body = (
        <>
          <Field label={t("workflows.condition.field")}>{text("field")}</Field>
          <div className="wf-chips">
            {outsOf(b).map((o) => (
              <span key={o.key} className="wf-pill mute">
                {o.label}
              </span>
            ))}
          </div>
          <Field label={t("workflows.condition.rounds")} hint={t("workflows.condition.roundsHint")}>
            <select className="input" value={String(c.rounds)} onChange={(e) => set("rounds", Number(e.target.value))}>
              {["0", "2", "3", "5"].map((n) => (
                <option key={n} value={n}>
                  {n === "0" ? "–" : n}
                </option>
              ))}
            </select>
          </Field>
        </>
      );
      break;
    case "action":
      body = (
        <>
          <div className="wf-note info">{t("workflows.action.note")}</div>
          <Field label={t("workflows.action.system")}>{select("system", same(["GitLab", "Zammad", "Jira", "E-Mail"]))}</Field>
          <Field label={t("workflows.action.op")}>
            {select(
              "op",
              ["assign", "reviewer", "label", "comment", "merge"].map((k): [string, string] => [k, t(`workflows.ops.${k}`)]),
            )}
          </Field>
          <Field label={t("workflows.action.target")}>{text("target")}</Field>
          <Field label={t("workflows.action.value")}>{text("value")}</Field>
          <Field label={t("workflows.action.comment")}>{area("comment")}</Field>
        </>
      );
      break;
    case "approval":
      body = (
        <>
          <Field label={t("workflows.approval.who")}>{select("who", same(p.ex.options.approvers))}</Field>
          <Field label={t("workflows.approval.question")} hint={t("workflows.approval.questionHint")}>
            {area("question")}
          </Field>
          <Field label={t("workflows.approval.wait")}>{select("wait", same(p.ex.options.waits))}</Field>
          <DecisionCard recipient={String(c.who)} kind={t("workflows.chat.kindApproval")} question={String(c.question)} options={outsOf(b)} />
        </>
      );
      break;
    case "wait":
      body = (
        <>
          <Field label={t("workflows.wait.until")}>{text("until")}</Field>
          <Field label={t("workflows.wait.max")}>{select("max", same(p.ex.options.maxes))}</Field>
          <Field label={t("workflows.wait.then")}>{text("then")}</Field>
        </>
      );
      break;
    case "notify":
      body = (
        <>
          <Field label={t("workflows.notify.to")}>{text("to")}</Field>
          <Field label={t("workflows.notify.via")}>{select("via", same(p.ex.options.channels))}</Field>
          <Field label={t("workflows.notify.text")}>{area("text")}</Field>
          <div className="wf-note info">{t("workflows.notify.note")}</div>
        </>
      );
      break;
    default:
      body = <p className="wf-muted">{t("workflows.end.text")}</p>;
  }

  return (
    <div>
      <div className="wf-insp-head">
        <BlockIcon type={b.type} />
        <div className="min-w-0">
          <div className="wf-node-type">{t(`workflows.type.${b.type}`)}</div>
          <h2>{b.title}</h2>
        </div>
      </div>
      <Field label={t("workflows.name")}>
        <input className="input" value={b.title} onChange={(e) => p.onBlock(b.id, { title: e.target.value })} />
      </Field>
      {body}
      <div className="wf-sec">
        <button className="btn sm danger" type="button" onClick={() => p.onRemoveBlock(b.id)}>
          {t("workflows.removeBlock")}
        </button>
      </div>
    </div>
  );
}

function Overview(p: Props) {
  const { t } = useTranslation();
  const found = checks(p.ex, p.blocks, p.links);
  const blocking = found.some((c) => c.key === "checkLoopNoLimit");
  return (
    <div>
      <div className="wf-insp-head">
        <BlockIcon type="trigger" />
        <div>
          <div className="wf-node-type">{t("workflows.workflow")}</div>
          <h2>{p.ex.name}</h2>
        </div>
      </div>
      <Field label={t("workflows.col.scope")}>
        <input className="input" value={p.ex.options.sources[0]} readOnly />
      </Field>
      <Field label={t("workflows.owner")} hint={t("workflows.ownerHint")}>
        <input className="input" value={p.ex.owner} readOnly />
      </Field>
      <div className="wf-sec">
        <p className="wf-eyebrow">{t("workflows.rails")}</p>
        <ul className="wf-list">
          {p.ex.rails.map((r) => (
            <li key={r.text}>
              <span className="wf-pill mute">{r.who}</span>
              <span>{r.text}</span>
            </li>
          ))}
        </ul>
        <p className="wf-hint">{t("workflows.railsNote")}</p>
      </div>
      <div className="wf-sec">
        <p className="wf-eyebrow">{t("workflows.checks")}</p>
        <ul className="wf-list">
          {found.map((c, i) => (
            <li key={i}>
              <span className={`wf-pill ${c.level}`}>{c.level === "ok" ? t("workflows.checkOk") : t("workflows.checkHint")}</span>
              <span>{t(`workflows.${c.key}`, c.params)}</span>
            </li>
          ))}
        </ul>
      </div>
      <div className="wf-sec">
        <button className="btn primary sm" type="button" disabled={blocking}>
          {t("workflows.save", { n: 5 })}
        </button>
      </div>
    </div>
  );
}
