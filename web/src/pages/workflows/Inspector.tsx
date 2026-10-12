/* The details panel while editing (#560, #565): the settings of the selected
   block, of a connection, or of the workflow. Values are kept as the engine
   reads them; durations are chosen from a few and shown in words. */
import { useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { isDraft, type Agent, type WorkflowProblem } from "../../api";
import { fmtDelta } from "../../format";
import { BlockIcon } from "./canvas";
import { DecisionCard } from "./ChatCards";
import { durationSeconds, outsOf, type Cfg, type Doc, type Meta } from "./model";

interface Props {
  doc: Doc;
  selected: string | null;
  selectedLink: number | null;
  agents: Agent[];
  problems: WorkflowProblem[];
  exitLabel: (key: string) => string;
  onBlock: (id: string, patch: { title?: string; cfg?: Cfg }) => void;
  onMeta: (patch: Partial<Meta>) => void;
  onRemoveBlock: (id: string) => void;
  onRemoveLink: (i: number) => void;
  save: { dirty: boolean; saving: boolean; version: number; message: string; onSave: () => void };
}

function Field({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
  return (
    <label className="wf-field">
      <span className="wf-field-label">{label}</span>
      {children}
      {hint && <span className="wf-hint">{hint}</span>}
    </label>
  );
}

const durationLabel = (text: string, none: string) => (text ? fmtDelta(durationSeconds(text) * 1000) : none);

/* A list of words the definition branches on: a condition's values, an
   approval's options. */
function Words({ values, onChange, placeholder }: { values: string[]; onChange: (v: string[]) => void; placeholder: string }) {
  const { t } = useTranslation();
  const [draft, setDraft] = useState("");
  const add = () => {
    const w = draft.trim();
    if (w && !values.includes(w)) onChange([...values, w]);
    setDraft("");
  };
  return (
    <div className="wf-words">
      {values.map((v) => (
        <span key={v} className="wf-pill mute">
          {v}
          <button type="button" aria-label={t("workflows.removeWord", { word: v })} onClick={() => onChange(values.filter((x) => x !== v))}>
            ×
          </button>
        </span>
      ))}
      <input
        className="input"
        value={draft}
        placeholder={placeholder}
        onChange={(e) => setDraft(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            e.preventDefault();
            add();
          }
        }}
        onBlur={add}
      />
    </div>
  );
}

export default function Inspector(p: Props) {
  const { t } = useTranslation();
  const task = useRef<HTMLTextAreaElement>(null);
  const { doc } = p;

  if (p.selectedLink !== null && doc.links[p.selectedLink]) {
    const l = doc.links[p.selectedLink];
    const from = doc.blocks[l.from];
    const to = doc.blocks[l.to];
    return (
      <div>
        <div className="wf-insp-head">
          <div>
            <div className="wf-node-type">{t("workflows.link.title")}</div>
            <h2>
              {from?.title || l.from} → {to?.title || l.to}
            </h2>
          </div>
        </div>
        {l.port !== "out" && <p className="wf-muted">{t("workflows.link.branch", { label: p.exitLabel(l.port) })}</p>}
        <button className="btn sm danger" onClick={() => p.onRemoveLink(p.selectedLink!)}>
          {t("workflows.link.remove")}
        </button>
      </div>
    );
  }

  const b = p.selected ? doc.blocks[p.selected] : null;
  if (!b) return <Overview {...p} />;

  const c = b.cfg;
  const set = (k: string, v: Cfg[string]) => p.onBlock(b.id, { cfg: { ...c, [k]: v } });
  const text = (k: string, placeholder?: string) => (
    <input className="input" value={String(c[k] ?? "")} placeholder={placeholder} onChange={(e) => set(k, e.target.value)} />
  );
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
  const durations = (k: string, values: string[]) =>
    select(k, values.map((v): [string, string] => [v, durationLabel(v, t("workflows.none"))]));
  const check = (k: string, label: string) => (
    <label className="wf-check">
      <input type="checkbox" checked={Boolean(c[k])} onChange={(e) => set(k, e.target.checked)} />
      <span>{label}</span>
    </label>
  );
  const fields = (k: string) => (
    <input
      className="input"
      value={(Array.isArray(c[k]) ? (c[k] as string[]) : []).join(", ")}
      onChange={(e) =>
        set(
          k,
          e.target.value
            .split(",")
            .map((s) => s.trim())
            .filter(Boolean),
        )
      }
    />
  );

  // The values a task can name: the case, and what earlier colleague steps report.
  const vars = ["{{case.title}}", "{{case.no}}"];
  for (const other of Object.values(doc.blocks)) {
    if (other.id === b.id || (other.type !== "agent" && other.type !== "ai")) continue;
    for (const f of Array.isArray(other.cfg.output) ? (other.cfg.output as string[]) : []) vars.push(`{{${other.id}.${f}}}`);
  }

  let body: React.ReactNode = null;
  switch (b.type) {
    case "trigger":
      body = <div className="wf-note info">{t("workflows.trigger.note")}</div>;
      break;
    case "agent": {
      const role = String(c.role ?? "");
      const members = doc.meta.roles[role] ?? [];
      const others = Object.values(doc.blocks).filter((x) => x.type === "agent" && x.id !== b.id);
      const free = members.filter((m) => {
        const a = p.agents.find((x) => x.slug === m);
        return a && !isDraft(a) && !a.killed;
      });
      body = (
        <>
          {free.length === 0 && (
            <div className="wf-note warn">
              {t("workflows.agent.empty", { role: role || "–" })} {c.optional ? t("workflows.agent.emptySkip") : t("workflows.agent.emptyOwner")}
            </div>
          )}
          <Field label={t("workflows.agent.role")} hint={t("workflows.agent.roleHint")}>
            {select("role", [["", "–"], ...Object.keys(doc.meta.roles).map((r): [string, string] => [r, r])])}
          </Field>
          <Members role={role} doc={doc} agents={p.agents} onMeta={p.onMeta} />
          {members.length > 1 && (
            <>
              <Field label={t("workflows.agent.pick")}>
                {select("pick", [
                  ["load", t("workflows.agent.pick_load")],
                  ["round", t("workflows.agent.pick_round")],
                ])}
              </Field>
              <Field label={t("workflows.agent.cap")}>{select("cap", [["", t("workflows.agent.capNone")], ["1", "1"], ["2", "2"], ["3", "3"]])}</Field>
            </>
          )}
          {check("sticky", t("workflows.agent.sticky"))}
          {others.length > 0 && (
            <Field label={t("workflows.agent.notSame")} hint={t("workflows.agent.notSameHint")}>
              {select("notSame", [["", "–"], ...others.map((x): [string, string] => [x.id, x.title || x.id])])}
            </Field>
          )}
          {check("optional", t("workflows.agent.optional"))}
          <Field label={t("workflows.agent.task")} hint={t("workflows.agent.taskHint")}>
            {area("task", task)}
          </Field>
          <div className="wf-vars">
            {vars.map((v) => (
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
            {fields("output")}
          </Field>
          <Field label={t("workflows.agent.limit")} hint={t("workflows.agent.limitHint")}>
            {durations("limit", ["", "1h", "2h", "4h", "1d"])}
          </Field>
        </>
      );
      break;
    }
    case "ai":
      body = (
        <>
          <div className="wf-note warn">{t("workflows.ai.notYet")}</div>
          <Field label={t("workflows.ai.prompt")}>{area("task")}</Field>
          <Field label={t("workflows.agent.result")}>{fields("output")}</Field>
        </>
      );
      break;
    case "condition":
      body = (
        <>
          <Field label={t("workflows.condition.field")} hint={t("workflows.condition.fieldHint")}>
            {text("field", "a.result")}
          </Field>
          <Field label={t("workflows.condition.values")} hint={t("workflows.condition.valuesHint")}>
            <Words values={Array.isArray(c.values) ? (c.values as string[]) : []} onChange={(v) => set("values", v)} placeholder={t("workflows.condition.addValue")} />
          </Field>
          <Field label={t("workflows.condition.rounds")} hint={t("workflows.condition.roundsHint")}>
            <select className="input" value={String(c.rounds ?? 0)} onChange={(e) => set("rounds", Number(e.target.value))}>
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
          <div className="wf-note warn">{t("workflows.action.notYet")}</div>
          <Field label={t("workflows.action.system")}>{text("system", "gitlab")}</Field>
          <Field label={t("workflows.action.op")}>{text("do", "assign")}</Field>
          <Field label={t("workflows.action.target")}>{text("target", "{{case.no}}")}</Field>
          <Field label={t("workflows.action.value")}>{text("value")}</Field>
          <Field label={t("workflows.action.comment")}>{area("comment")}</Field>
        </>
      );
      break;
    case "approval":
      body = (
        <>
          <Field label={t("workflows.approval.who")} hint={t("workflows.approval.whoHint")}>
            {text("who")}
          </Field>
          <Field label={t("workflows.approval.question")} hint={t("workflows.approval.questionHint")}>
            {area("ask")}
          </Field>
          <Field label={t("workflows.approval.options")}>
            <Words values={Array.isArray(c.options) ? (c.options as string[]) : []} onChange={(v) => set("options", v)} placeholder={t("workflows.approval.addOption")} />
          </Field>
          <Field label={t("workflows.approval.wait")}>{durations("wait", ["", "1d", "2d", "7d"])}</Field>
          <DecisionCard recipient={String(c.who || "–")} kind={t("workflows.chat.kindApproval")} question={String(c.ask)} options={outsOf(b).filter((o) => o.key !== "timeout").map((o) => ({ key: o.key, label: o.key }))} />
        </>
      );
      break;
    case "wait":
      body = (
        <>
          <Field label={t("workflows.wait.until")} hint={t("workflows.wait.untilHint")}>
            {text("until", "label:customer-ok")}
          </Field>
          <Field label={t("workflows.wait.max")}>{durations("max", ["", "1d", "7d", "14d", "30d"])}</Field>
        </>
      );
      break;
    case "notify":
      body = (
        <>
          <div className="wf-note warn">{t("workflows.notify.notYet")}</div>
          <Field label={t("workflows.notify.to")}>{text("to")}</Field>
          <Field label={t("workflows.notify.text")}>{area("text")}</Field>
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
          <div className="wf-node-type">
            {t(`workflows.type.${b.type}`)} · {b.id}
          </div>
          <h2>{b.title || b.id}</h2>
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

/* Who holds a role: the organisation's agents by slug, each with whether it
   can take work now. A role is part of the workflow, not of an agent. */
function Members({ role, doc, agents, onMeta }: { role: string; doc: Doc; agents: Agent[]; onMeta: Props["onMeta"] }) {
  const { t } = useTranslation();
  if (!role) return null;
  const members = doc.meta.roles[role] ?? [];
  const setMembers = (m: string[]) => onMeta({ roles: { ...doc.meta.roles, [role]: m } });
  const candidates = agents.filter((a) => !members.includes(a.slug));
  return (
    <div className="wf-members">
      {members.map((m) => {
        const a = agents.find((x) => x.slug === m);
        const state = !a ? "unknown" : isDraft(a) ? "draft" : a.killed ? "stopped" : "ok";
        return (
          <div key={m} className="wf-member">
            <span className="wf-avatar">{(a?.display_name ?? m).slice(0, 2).toUpperCase()}</span>
            {a?.display_name ?? m}
            <span className={`wf-pill ${state === "ok" ? "ok" : state === "unknown" ? "bad" : "warn"}`}>{t(`workflows.agent.state_${state}`)}</span>
            <button type="button" className="wf-member-x" aria-label={t("workflows.removeWord", { word: m })} onClick={() => setMembers(members.filter((x) => x !== m))}>
              ×
            </button>
          </div>
        );
      })}
      {candidates.length > 0 && (
        <select className="input" value="" onChange={(e) => e.target.value && setMembers([...members, e.target.value])}>
          <option value="">{t("workflows.agent.addMember")}</option>
          {candidates.map((a) => (
            <option key={a.slug} value={a.slug}>
              {a.display_name} ({a.slug})
            </option>
          ))}
        </select>
      )}
    </div>
  );
}

function Overview(p: Props) {
  const { t } = useTranslation();
  const { meta } = p.doc;
  const [newRole, setNewRole] = useState("");
  return (
    <div>
      <div className="wf-insp-head">
        <BlockIcon type="trigger" />
        <div>
          <div className="wf-node-type">
            {t("workflows.workflow")} · {t("workflows.versionN", { n: p.save.version })}
          </div>
          <h2>{meta.name}</h2>
        </div>
      </div>
      <Field label={t("workflows.name")}>
        <input className="input" value={meta.name} onChange={(e) => p.onMeta({ name: e.target.value })} />
      </Field>
      <Field label={t("workflows.owner")} hint={t("workflows.ownerHint")}>
        <input className="input" value={meta.owner} onChange={(e) => p.onMeta({ owner: e.target.value })} />
      </Field>
      <div className="wf-sec">
        <p className="wf-eyebrow">{t("workflows.roles")}</p>
        <ul className="wf-list">
          {Object.entries(meta.roles).map(([r, m]) => (
            <li key={r}>
              <span className="wf-pill mute">{r}</span>
              <span>{m.length ? m.join(", ") : t("workflows.unfilled")}</span>
            </li>
          ))}
        </ul>
        <div className="wf-row" style={{ marginTop: 8 }}>
          <input className="input" value={newRole} placeholder={t("workflows.newRole")} onChange={(e) => setNewRole(e.target.value)} />
          <button
            className="btn sm"
            type="button"
            disabled={!newRole.trim() || newRole.trim() in meta.roles}
            onClick={() => {
              p.onMeta({ roles: { ...meta.roles, [newRole.trim()]: [] } });
              setNewRole("");
            }}
          >
            {t("workflows.addRole")}
          </button>
        </div>
      </div>
      <div className="wf-sec">
        <p className="wf-eyebrow">{t("workflows.checks")}</p>
        {p.problems.length === 0 ? (
          <p className="wf-muted">{t("workflows.checksClean")}</p>
        ) : (
          <ul className="wf-list">
            {p.problems.map((pr, i) => (
              <li key={i}>
                <span className="wf-pill warn">{pr.step || t("workflows.workflow")}</span>
                <span>{pr.text}</span>
              </li>
            ))}
          </ul>
        )}
      </div>
      <div className="wf-sec">
        <button className="btn primary sm" type="button" disabled={!p.save.dirty || p.save.saving || p.problems.length > 0} onClick={p.save.onSave}>
          {t("workflows.save", { n: p.save.version + 1 })}
        </button>
        {p.save.message && <p className="wf-hint">{p.save.message}</p>}
      </div>
    </div>
  );
}
