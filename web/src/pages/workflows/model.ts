/* The workflow pages' model (#560, #565): blocks and links on the canvas,
   built from the steps the API sends, and written back as the text the
   engine parses (internal/workflow). The text is the one definition; the
   canvas is a view of it.

   Values are kept canonical — durations as "4h" or "14d", exits by their
   key, other steps by id, conditions as "step.field" — and only turned into
   words where they are shown. */
import type { Workflow, WorkflowRun, WorkflowStep } from "../../api";

export type BlockType = WorkflowStep["kind"];
export const BLOCK_TYPES: BlockType[] = ["trigger", "agent", "ai", "condition", "action", "approval", "wait", "notify", "end"];

export type Mode = Workflow["mode"];
export type Level = "ok" | "warn" | "bad";
export type Cfg = Record<string, string | number | boolean | string[]>;

export interface Out {
  key: string;
}

export interface Block {
  id: string;
  type: BlockType;
  title: string;
  cfg: Cfg;
}

export interface Link {
  from: string;
  port: string;
  to: string;
}

export interface Meta {
  name: string;
  scope: string;
  owner: string;
  mode: Mode;
  roles: Record<string, string[]>;
}

export interface Doc {
  meta: Meta;
  order: string[];
  blocks: Record<string, Block>;
  links: Link[];
  positions: Record<string, { x: number; y: number }>;
}

/* Heights follow from the number of exits, so the handles sit where the rows
   are drawn without measuring anything. */
export const NODE_W = 220;
export const HEAD_H = 58;
export const ROW_H = 24;

// Exits with a meaning of their own, as the engine names them.
export const EXIT_OUT = "out";
export const EXIT_ELSE = "else";
export const EXIT_LIMIT = "limit";
export const EXIT_TIMEOUT = "timeout";

const str = (v: unknown) => (typeof v === "string" ? v : v == null ? "" : String(v));
const list = (v: unknown) => (Array.isArray(v) ? v.map(String) : []);

/* A block's exits follow from its settings: a condition's values, and its
   limit when it counts rounds; an approval's options, and its timeout when
   it has a deadline; a wait's timeout when it has a maximum. */
export function outsOf(b: Block): Out[] {
  switch (b.type) {
    case "end":
      return [];
    case "condition": {
      const outs = list(b.cfg.values).map((key) => ({ key }));
      if (Number(b.cfg.rounds) > 0) outs.push({ key: EXIT_LIMIT });
      return outs;
    }
    case "approval": {
      const outs = list(b.cfg.options).map((key) => ({ key }));
      if (str(b.cfg.wait)) outs.push({ key: EXIT_TIMEOUT });
      return outs;
    }
    case "wait":
      return str(b.cfg.max) && str(b.cfg.until) ? [{ key: EXIT_OUT }, { key: EXIT_TIMEOUT }] : [{ key: EXIT_OUT }];
    default:
      return [{ key: EXIT_OUT }];
  }
}

export function heightOf(b: Block): number {
  const o = outsOf(b);
  return o.length > 1 ? HEAD_H + o.length * ROW_H : HEAD_H;
}

// Where an exit's handle sits, measured from the block's top edge.
export function outY(b: Block, key: string): number {
  const o = outsOf(b);
  if (o.length <= 1) return HEAD_H / 2;
  return HEAD_H + o.findIndex((x) => x.key === key) * ROW_H + ROW_H / 2;
}

/* ---------- durations ---------- */

// seconds → the shortest text the engine reads back: "14d", "4h", "30m".
export function durationText(seconds: number | undefined): string {
  const s = Math.max(0, Math.round(seconds ?? 0));
  if (!s) return "";
  if (s % 86400 === 0) return `${s / 86400}d`;
  if (s % 3600 === 0) return `${s / 3600}h`;
  if (s % 60 === 0) return `${s / 60}m`;
  return `${s}s`;
}

export function durationSeconds(text: string): number {
  const m = /^(\d+)([dhms])$/.exec(text.trim());
  if (!m) return 0;
  return Number(m[1]) * { d: 86400, h: 3600, m: 60, s: 1 }[m[2] as "d" | "h" | "m" | "s"];
}

/* ---------- from the API ---------- */

function blockOf(s: WorkflowStep): Block {
  const next = s.next ?? {};
  const b: Block = { id: s.id, type: s.kind, title: s.title ?? "", cfg: {} };
  switch (s.kind) {
    case "trigger":
      b.cfg = { as: s.as ?? "", every: durationText(s.every), daily: s.daily ?? "", onlyIf: s.only_if ?? "", webhook: s.webhook ?? "", token: !!s.token };
      break;
    case "agent":
      b.cfg = {
        role: s.role ?? "",
        pick: s.pick ?? "load",
        sticky: !!s.sticky,
        cap: s.max_parallel ? String(s.max_parallel) : "",
        notSame: s.not_same_as ?? "",
        optional: !!s.optional,
        task: s.task ?? "",
        output: s.result ?? [],
        ask: s.questions_to ?? "",
        limit: durationText(s.limit),
      };
      break;
    case "ai":
      b.cfg = { task: s.task ?? "", output: s.result ?? [] };
      break;
    case "condition":
      b.cfg = { field: s.on ?? "", rounds: s.max_rounds ?? 0, values: Object.keys(next).filter((k) => k !== EXIT_LIMIT) };
      break;
    case "action":
      b.cfg = { as: s.as ?? "", system: s.system ?? "", do: s.do ?? "", params: paramLines(s.params ?? {}) };
      break;
    case "approval":
      b.cfg = { who: s.who ?? "", ask: s.ask ?? "", wait: durationText(s.wait), options: s.options ?? [] };
      break;
    case "wait":
      b.cfg = { until: s.until ?? "", max: durationText(s.max) };
      break;
    case "notify":
      b.cfg = { to: s.to ?? "", via: s.via ?? "", text: s.text ?? "" };
      break;
  }
  return b;
}

export function fromWorkflow(w: Workflow): Doc {
  const order = (w.steps ?? []).map((s) => s.id);
  const blocks: Record<string, Block> = {};
  const links: Link[] = [];
  for (const s of w.steps ?? []) {
    blocks[s.id] = blockOf(s);
    for (const [port, to] of Object.entries(s.next ?? {})) links.push({ from: s.id, port, to });
  }
  const saved = layoutOf(w.source ?? "");
  const auto = autoLayout(blocks, links, order);
  const positions: Doc["positions"] = {};
  for (const id of order) positions[id] = saved[id] ?? auto[id];
  return {
    meta: { name: w.name, scope: w.scope, owner: w.owner, mode: w.mode, roles: w.roles ?? {} },
    order,
    blocks,
    links,
    positions,
  };
}

/* An action's parameters, one "name: value" per line — the way they read in
   the text, and editable as one field. */
export function paramLines(params: Record<string, string>): string[] {
  return Object.keys(params)
    .sort()
    .map((k) => `${k}: ${params[k]}`);
}

export function paramsOf(lines: string[]): Record<string, string> {
  const out: Record<string, string> = {};
  for (const line of lines) {
    const at = line.indexOf(":");
    if (at <= 0) continue;
    const k = line.slice(0, at).trim();
    if (k) out[k] = line.slice(at + 1).trim();
  }
  return out;
}

/* The canvas keeps its positions in the text, under `layout:` — a section
   the engine does not read. */
export function layoutOf(source: string): Record<string, { x: number; y: number }> {
  const out: Record<string, { x: number; y: number }> = {};
  const at = source.search(/^layout:\s*$/m);
  if (at < 0) return out;
  for (const line of source.slice(at).split("\n").slice(1)) {
    if (!/^\s/.test(line) && line.trim()) break;
    const m = /^\s+([A-Za-z0-9_-]+):\s*\[\s*(-?\d+)\s*,\s*(-?\d+)\s*\]/.exec(line);
    if (m) out[m[1]] = { x: Number(m[2]), y: Number(m[3]) };
  }
  return out;
}

/* Left to right by distance from the trigger; steps at the same distance
   stacked. A way back does not pull a step further right. */
export function autoLayout(blocks: Record<string, Block>, links: Link[], order: string[]): Record<string, { x: number; y: number }> {
  const start = order.find((id) => blocks[id]?.type === "trigger") ?? order[0];
  const depth: Record<string, number> = {};
  const queue = start ? [start] : [];
  if (start) depth[start] = 0;
  while (queue.length) {
    const id = queue.shift()!;
    for (const l of links) {
      if (l.from === id && blocks[l.to] && depth[l.to] === undefined) {
        depth[l.to] = depth[id] + 1;
        queue.push(l.to);
      }
    }
  }
  let deepest = Math.max(0, ...Object.values(depth));
  for (const id of order) if (depth[id] === undefined) depth[id] = ++deepest;
  const row: Record<number, number> = {};
  const out: Record<string, { x: number; y: number }> = {};
  for (const id of order) {
    const d = depth[id];
    const r = row[d] ?? 0;
    row[d] = r + 1;
    out[id] = { x: 40 + d * 290, y: 80 + r * 170 };
  }
  return out;
}

/* ---------- back to text ---------- */

const q = (v: string) => JSON.stringify(v);
const words = (xs: string[]) => `[${xs.map((x) => (/^[A-Za-z0-9_.-]+$/.test(x) ? x : q(x))).join(", ")}]`;

/* The definition as the engine parses it. Keys are the file format's and
   stay English; strings are written double-quoted, which YAML reads as
   written. */
export function toSource(doc: Doc): string {
  const { meta } = doc;
  const L: string[] = [`workflow: ${q(meta.name)}`];
  if (meta.scope) L.push(`scope: ${q(meta.scope)}`);
  if (meta.owner) L.push(`owner: ${q(meta.owner)}`);
  L.push(`mode: ${meta.mode}`, "");
  const roles = Object.entries(meta.roles);
  if (roles.length) {
    L.push("roles:");
    for (const [r, m] of roles) L.push(`  ${q(r)}: ${words(m)}`);
    L.push("");
  }
  L.push("steps:");
  const ids = [...doc.order.filter((id) => doc.blocks[id]), ...Object.keys(doc.blocks).filter((id) => !doc.order.includes(id))];
  for (const id of ids) {
    const b = doc.blocks[id];
    const c = b.cfg;
    L.push(`  ${id}:`, `    type: ${b.type}`);
    if (b.title) L.push(`    title: ${q(b.title)}`);
    const field = (name: string, v: unknown) => {
      if (v === undefined || v === "" || v === false || v === 0 || (Array.isArray(v) && v.length === 0)) return;
      if (Array.isArray(v)) L.push(`    ${name}: ${words(v.map(String))}`);
      else if (typeof v === "string") L.push(`    ${name}: ${q(v)}`);
      else L.push(`    ${name}: ${v}`);
    };
    switch (b.type) {
      case "trigger":
        field("as", c.as);
        field("every", c.every);
        field("daily", c.daily);
        field("only_if", c.onlyIf);
        field("webhook", c.webhook);
        field("token", c.token);
        break;
      case "agent":
        field("role", c.role);
        if (str(c.pick) && c.pick !== "load") field("pick", c.pick);
        field("sticky", c.sticky);
        if (str(c.cap)) field("max_parallel", Number(c.cap));
        field("not_same_as", c.notSame);
        field("optional", c.optional);
        field("task", c.task);
        field("result", list(c.output));
        field("questions_to", c.ask);
        field("limit", c.limit);
        break;
      case "ai":
        field("task", c.task);
        field("result", list(c.output));
        break;
      case "condition":
        field("on", c.field);
        field("max_rounds", Number(c.rounds) || 0);
        break;
      case "action": {
        field("as", c.as);
        field("system", c.system);
        field("do", c.do);
        const params = paramsOf(list(c.params));
        const keys = Object.keys(params).sort();
        if (keys.length) {
          L.push("    params:");
          for (const k of keys) L.push(`      ${q(k)}: ${q(params[k])}`);
        }
        break;
      }
      case "approval":
        field("who", c.who);
        field("ask", c.ask);
        field("wait", c.wait);
        break;
      case "wait":
        field("until", c.until);
        field("max", c.max);
        break;
      case "notify":
        field("to", c.to);
        field("via", c.via);
        field("text", c.text);
        break;
    }
    const outs = outsOf(b);
    const exits = outs.map((o) => [o.key, doc.links.find((l) => l.from === id && l.port === o.key)?.to] as const).filter(([, to]) => to);
    const single = outs.length === 1 && outs[0].key === EXIT_OUT;
    if (single && exits.length === 1) L.push(`    next: ${exits[0][1]}`);
    else if (exits.length) {
      L.push("    next:");
      for (const [k, to] of exits) L.push(`      ${q(k)}: ${to}`);
    }
  }
  L.push("", "layout:");
  for (const id of ids) {
    const p = doc.positions[id];
    if (p) L.push(`  ${id}: [${Math.round(p.x)}, ${Math.round(p.y)}]`);
  }
  return L.join("\n") + "\n";
}

/* ---------- runs ---------- */

export type CaseState = "working" | "queued" | "waiting";

export interface Case {
  id: string;
  no: string;
  title: string;
  by: string;
  since: string;
  level: Level;
  why?: string;
  state: CaseState;
  // Milliseconds at this step, for sorting; `since` is how it reads.
  age: number;
  // Waits on the person looking: an approval that names them.
  mine?: boolean;
}

export const isOpen = (r: WorkflowRun) => r.state !== "done" && r.state !== "failed";

/* The run's path, from its log: which blocks it touched, which links it
   walked, how often it came back to each, and where it stands now. */
export function runPath(r: WorkflowRun | undefined) {
  const blocks = new Set<string>();
  const links = new Set<string>();
  const count: Record<string, number> = {};
  let prev: string | null = null;
  for (const e of r?.log ?? []) {
    if (prev !== e.step) {
      count[e.step] = (count[e.step] ?? 0) + 1;
      if (prev) links.add(`${prev}>${e.step}`);
    }
    blocks.add(e.step);
    prev = e.step;
  }
  return { blocks, links, count, current: r && isOpen(r) ? r.step : null };
}

/* ---------- the example ---------- */

/* The example a new organisation can start from: a ticket from assignment
   to the customer's test. Its roles start empty; the organisation fills
   them with its agents. It starts in observe mode, so nothing is handed out
   before someone looked. */
export function example(lang: string): Doc {
  const de = lang.startsWith("de");
  const L = (d: string, e: string) => (de ? d : e);
  const eng = L("Entwicklung", "Engineering");
  const blocks: Record<string, Block> = {
    t: { id: "t", type: "trigger", title: L("Ticket zugewiesen", "Ticket assigned"), cfg: {} },
    b: { id: "b", type: "agent", title: L("Umsetzen", "Build"), cfg: { role: eng, pick: "load", sticky: true, cap: "2", notSame: "", optional: false, task: L("Setz {{case.title}} um. Frühere Mängel: {{a.defects}}", "Implement {{case.title}}. Earlier defects: {{a.defects}}"), output: ["mr"], ask: "", limit: "4h" } },
    r: { id: "r", type: "agent", title: L("Code prüfen", "Review code"), cfg: { role: "Code-Review", pick: "load", sticky: false, cap: "", notSame: "b", optional: true, task: L("Prüf {{b.mr}}.", "Review {{b.mr}}."), output: ["verdict"], ask: "", limit: "2h" } },
    a: { id: "a", type: "agent", title: L("Abnahme", "Acceptance"), cfg: { role: "Test", pick: "load", sticky: true, cap: "3", notSame: "b", optional: false, task: L("Teste {{b.mr}} gegen {{case.title}}. Melde result: accepted oder defects.", "Test {{b.mr}} against {{case.title}}. Report result: accepted or defects."), output: ["result", "defects"], ask: "", limit: "4h" } },
    c: { id: "c", type: "condition", title: L("Abgenommen?", "Accepted?"), cfg: { field: "a.result", rounds: 3, values: ["accepted", "defects"] } },
    h: { id: "h", type: "approval", title: L("Wie weiter?", "What next?"), cfg: { who: "admin", ask: L("Drei Runden ohne Abnahme bei {{case.no}}. Zuletzt bemängelt: {{a.defects}}", "Three rounds without acceptance on {{case.no}}. Last defects: {{a.defects}}"), wait: "", options: ["retry", "stop"] } },
    w: { id: "w", type: "wait", title: L("Kundentest", "Customer test"), cfg: { until: "label:customer-ok", max: "14d" } },
    e: { id: "e", type: "end", title: L("Fertig", "Done"), cfg: {} },
  };
  const links: Link[] = [
    { from: "t", port: EXIT_OUT, to: "b" }, { from: "b", port: EXIT_OUT, to: "r" }, { from: "r", port: EXIT_OUT, to: "a" },
    { from: "a", port: EXIT_OUT, to: "c" }, { from: "c", port: "accepted", to: "w" }, { from: "c", port: "defects", to: "b" },
    { from: "c", port: EXIT_LIMIT, to: "h" }, { from: "h", port: "retry", to: "b" }, { from: "h", port: "stop", to: "e" },
    { from: "w", port: EXIT_OUT, to: "e" }, { from: "w", port: EXIT_TIMEOUT, to: "e" },
  ];
  const order = ["t", "b", "r", "a", "c", "h", "w", "e"];
  return {
    meta: { name: L("Kundenticket", "Customer ticket"), scope: "", owner: "admin", mode: "watch", roles: { [eng]: [], "Code-Review": [], Test: [] } },
    order,
    blocks,
    links,
    positions: autoLayout(blocks, links, order),
  };
}
