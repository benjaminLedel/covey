// From the chart the API delivers to the tree the layout draws.
//
// Departments are flat, and a reporting line is resolved only inside a
// department: a member whose supervisor sits elsewhere is a root of its own
// department. That is the shape the data has (spec/02, spec/09), and the
// tree follows it — organisation, then departments, then the reporting
// chain within each.

import type { Agent, Department, Human, OrgChart } from "../../api";
import type { TreeInput } from "./layout";

export type Member =
  | { kind: "human"; id: string; human: Human }
  | { kind: "agent"; id: string; agent: Agent };

export const memberName = (m: Member) => (m.kind === "human" ? m.human.display_name : m.agent.display_name);
export const memberDept = (m: Member): string | null =>
  (m.kind === "human" ? m.human.department_id : m.agent.department_id) ?? null;
export const memberBoss = (m: Member): string | null =>
  (m.kind === "human" ? m.human.manager_id : m.agent.supervisor_id) ?? null;

// Five tones an org may give a department, each a hue held to the paper's
// luminance so it reads as an edge on both grounds. The colour is user data
// (stored as hex); it marks the department's left edge and nothing else.
export const DEPT_TONES = ["#6b7a99", "#7a8f6f", "#b08a4a", "#9a6f8a", "#5f8f8f"];

export const UNASSIGNED = "unassigned";
export const ORG = "org";
export const deptNodeId = (id: string) => `dept:${id}`;
export const memberNodeId = (id: string) => `m:${id}`;

export type ChartNode =
  | { kind: "org"; id: typeof ORG; humans: number; agents: number; depts: number }
  | { kind: "dept"; id: string; dept: Department; total: number; leads: Member[]; open: boolean }
  | { kind: "unassigned"; id: typeof UNASSIGNED; total: number; open: boolean }
  | {
      kind: "member";
      id: string;
      member: Member;
      deptId: string | null;
      isLead: boolean;
      /** Members reporting to this one, transitively, inside its department. */
      below: number;
      open: boolean;
    };

export type Size = { w: number; h: number };
export type Sizes = { org: Size; dept: Size; member: Size };
export type IsOpen = (id: string, size: number) => boolean;

export type Model = { root: TreeInput; nodes: Map<string, ChartNode>; members: Member[] };

export function allMembers(chart: OrgChart): Member[] {
  return [
    ...chart.humans.map((h): Member => ({ kind: "human", id: h.id, human: h })),
    ...chart.agents.map((a): Member => ({ kind: "agent", id: a.id, agent: a })),
  ];
}

/** Everyone reporting to `id`, transitively, within `pool`. Used to keep a
 *  member from being placed under its own report — the server would answer
 *  409, but a choice that cannot be right should not be offered. */
export function descendantsOf(pool: Member[], id: string): Set<string> {
  const out = new Set<string>();
  const walk = (parent: string) => {
    for (const m of pool) {
      if (memberBoss(m) === parent && !out.has(m.id)) {
        out.add(m.id);
        walk(m.id);
      }
    }
  };
  walk(id);
  return out;
}

export function buildModel(chart: OrgChart, isOpen: IsOpen, sizes: Sizes): Model {
  const members = allMembers(chart);
  const nodes = new Map<string, ChartNode>();
  const byId = new Map(members.map((m) => [m.id, m]));

  const inDept = (deptId: string | null) => members.filter((m) => memberDept(m) === deptId);

  // Leads are referenced org-wide: a lead need not be a member of its
  // department. Inside, it heads the tree; outside, it is named on the card.
  const leadsOf = (d: Department) => d.leads.map((l) => byId.get(l.id)).filter((m): m is Member => !!m);

  const branch = (pool: Member[], parentId: string | null, leadIds: Set<string>, seen: Set<string>, deptId: string | null): TreeInput[] => {
    const ids = new Set(pool.map((m) => m.id));
    const kids = pool.filter((m) => {
      if (seen.has(m.id)) return false;
      const boss = memberBoss(m);
      // Root level: no boss, or a boss outside this department.
      return parentId === null ? !(boss && ids.has(boss)) : boss === parentId;
    });
    // Leads first at the top: they are the head of the department.
    if (parentId === null) kids.sort((a, b) => Number(leadIds.has(b.id)) - Number(leadIds.has(a.id)));
    return kids.map((m) => {
      const next = new Set(seen).add(m.id);
      const below = descendantsOf(pool, m.id).size;
      const open = below === 0 || isOpen(m.id, below);
      const id = memberNodeId(m.id);
      nodes.set(id, { kind: "member", id, member: m, deptId, isLead: leadIds.has(m.id), below, open });
      return { id, ...sizes.member, children: open ? branch(pool, m.id, leadIds, next, deptId) : [] };
    });
  };

  const deptChildren: TreeInput[] = chart.departments.map((d) => {
    const pool = inDept(d.id);
    const leadIds = new Set(d.leads.map((l) => l.id));
    const open = isOpen(d.id, pool.length);
    const id = deptNodeId(d.id);
    nodes.set(id, { kind: "dept", id, dept: d, total: pool.length, leads: leadsOf(d), open });
    return { id, ...sizes.dept, children: open ? branch(pool, null, leadIds, new Set(), d.id) : [] };
  });

  const loose = inDept(null);
  if (loose.length > 0) {
    const open = isOpen(UNASSIGNED, loose.length);
    nodes.set(UNASSIGNED, { kind: "unassigned", id: UNASSIGNED, total: loose.length, open });
    deptChildren.push({ id: UNASSIGNED, ...sizes.dept, children: open ? branch(loose, null, new Set(), new Set(), null) : [] });
  }

  nodes.set(ORG, { kind: "org", id: ORG, humans: chart.humans.length, agents: chart.agents.length, depts: chart.departments.length });
  // Departments are peers in a row, never a list under the organisation.
  return { root: { id: ORG, ...sizes.org, children: deptChildren, stackChildren: false }, nodes, members };
}
