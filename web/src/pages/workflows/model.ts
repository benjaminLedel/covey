/* The workflow preview (#560): the model and its example data.

   Nothing here talks to the server. The page exists to shape the
   specification (spec/29) against something one can click, so the data is
   invented and lives in the browser. Names, projects and tickets are made up
   on purpose — the repository is public. */

export type BlockType = "trigger" | "agent" | "ai" | "condition" | "action" | "approval" | "wait" | "notify" | "end";
export const BLOCK_TYPES: BlockType[] = ["trigger", "agent", "ai", "condition", "action", "approval", "wait", "notify", "end"];

export type Mode = "off" | "watch" | "steer";
export type Level = "ok" | "warn" | "bad";

export interface Out {
  key: string;
  label: string;
}

export interface Block {
  id: string;
  type: BlockType;
  title: string;
  // Each type keeps its own settings; the inspector knows which ones.
  cfg: Record<string, string | number | boolean | string[]>;
  outs?: Out[];
}

export interface Link {
  from: string;
  port: string;
  to: string;
}

export interface Person {
  name: string;
  dept: string;
  job: string;
  state: "ok" | "stopped" | "draft";
  load: number;
}

export interface Case {
  no: string;
  title: string;
  by: string;
  since: string;
  level: Level;
  why?: string;
}

export interface Step {
  block: string;
  when: string;
  text: string;
  out?: Record<string, string>;
  now?: boolean;
  skip?: boolean;
  chat?: boolean;
}

export interface Run {
  no: string;
  title: string;
  steps: Step[];
}

export interface WorkflowRow {
  id: string;
  name: string;
  desc: string;
  scope: string;
  chain: string[];
  mode: Mode;
  active: number;
  stuck: number;
}

export interface Example {
  owner: string;
  scope: string;
  name: string;
  people: Record<string, Person>;
  roles: Record<string, string[]>;
  blocks: Record<string, Block>;
  positions: Record<string, { x: number; y: number }>;
  links: Link[];
  live: Record<string, { cases: Case[]; note?: string }>;
  runs: Record<string, Run>;
  workflows: WorkflowRow[];
  rails: { who: string; text: string }[];
  options: {
    sources: string[];
    limits: string[];
    waits: string[];
    maxes: string[];
    channels: string[];
    approvers: string[];
    askTo: string[];
  };
  // What the preview's chat shows: who asks, about which case.
  chat: { colleague: string; caseNo: string; request: string; answer: string; question: string; thanks: string };
}

/* Heights follow from the number of exits, so the handles sit where the rows
   are drawn without measuring anything. */
export const NODE_W = 220;
export const HEAD_H = 58;
export const ROW_H = 24;

export function outsOf(b: Block): Out[] {
  if (b.outs) return b.outs;
  return b.type === "end" ? [] : [{ key: "out", label: "" }];
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

export function available(ex: Pick<Example, "roles" | "people">, role: string): string[] {
  return (ex.roles[role] ?? []).filter((p) => ex.people[p]?.state === "ok");
}

export function roleEmpty(ex: Pick<Example, "roles" | "people">, b: Block): boolean {
  return b.type === "agent" && available(ex, String(b.cfg.role)).length === 0;
}

export interface Check {
  level: "ok" | "warn";
  key: string;
  params: Record<string, string | number>;
}

/* What the editor says before one saves. A loop without a round limit is
   the one finding that blocks saving — it is how a run circles forever. */
export function checks(ex: Pick<Example, "roles" | "people">, blocks: Record<string, Block>, links: Link[]): Check[] {
  const out: Check[] = [];
  const list = Object.values(blocks);
  for (const b of list) {
    if (roleEmpty(ex, b)) out.push({ level: "warn", key: b.cfg.optional ? "checkRoleEmptySkip" : "checkRoleEmpty", params: { step: b.title, role: String(b.cfg.role) } });
  }
  for (const b of list) {
    if (b.type !== "trigger" && !links.some((l) => l.to === b.id)) out.push({ level: "warn", key: "checkNoIn", params: { step: b.title } });
  }
  for (const b of list) {
    for (const o of outsOf(b)) {
      if (!links.some((l) => l.from === b.id && l.port === o.key)) {
        out.push({ level: "warn", key: "checkNoOut", params: { step: o.label ? `${b.title} (${o.label})` : b.title } });
      }
    }
  }
  for (const b of list) {
    if (b.type !== "condition") continue;
    const loops = links.some((l) => l.from === b.id && loopsBack(blocks, links, l.to, b.id));
    if (!loops) continue;
    if (Number(b.cfg.rounds) > 0) out.push({ level: "ok", key: "checkLoopOk", params: { step: b.title, n: Number(b.cfg.rounds) } });
    else out.push({ level: "warn", key: "checkLoopNoLimit", params: { step: b.title } });
  }
  for (const b of list) {
    if (b.type === "agent" && b.cfg.notSame) {
      const other = blocks[String(b.cfg.notSame)];
      if (other) out.push({ level: "ok", key: "checkFourEyes", params: { step: b.title, other: other.title } });
    }
  }
  return out;
}

// Whether following the links from `start` comes back to `target`.
export function loopsBack(blocks: Record<string, Block>, links: Link[], start: string, target: string): boolean {
  const seen = new Set<string>();
  const stack = [start];
  while (stack.length) {
    const id = stack.pop()!;
    if (id === target) return true;
    if (seen.has(id) || !blocks[id]) continue;
    seen.add(id);
    for (const l of links) if (l.from === id) stack.push(l.to);
  }
  return false;
}

/* The run's path: which blocks it touched, which links it walked, how often
   it stood at each block, and where it stands now. */
export function runPath(r: Run | undefined) {
  const blocks = new Set<string>();
  const links = new Set<string>();
  const count: Record<string, number> = {};
  let prev: string | null = null;
  let current: string | null = null;
  for (const s of r?.steps ?? []) {
    if (prev !== s.block) {
      count[s.block] = (count[s.block] ?? 0) + 1;
      if (prev) links.add(`${prev}>${s.block}`);
    }
    blocks.add(s.block);
    prev = s.block;
    if (s.now) current = s.block;
  }
  return { blocks, links, count, current };
}

/* The same definition as text. The keys are the file format a parser would
   read, so they stay English whatever the interface speaks. */
export function definitionText(
  ex: Pick<Example, "roles" | "name" | "scope" | "owner">,
  mode: Mode,
  blocks: Record<string, Block>,
  links: Link[],
  positions: Record<string, { x: number; y: number }>,
): string {
  const q = (v: unknown) => JSON.stringify(String(v));
  const lines = [
    `workflow: ${q(ex.name)}`,
    `scope: ${ex.scope}`,
    `owner: ${ex.owner.toLowerCase()}`,
    `mode: ${mode}`,
    "",
    "roles:",
    ...Object.entries(ex.roles).map(([r, m]) => `  ${q(r)}: [${m.map((x) => x.toLowerCase()).join(", ")}]`),
    "",
    "steps:",
  ];
  const fields: Record<BlockType, [string, string][]> = {
    trigger: [["source", "source"], ["event", "event"], ["filter", "filter"]],
    agent: [["role", "role"], ["pick", "pick"], ["sticky", "sticky"], ["cap", "max_parallel"], ["notSame", "not_same_as"], ["optional", "optional"], ["task", "task"], ["output", "result"], ["ask", "questions_to"], ["limit", "limit"]],
    ai: [["prompt", "prompt"], ["output", "result"], ["model", "model"]],
    condition: [["field", "on"], ["rounds", "max_rounds"]],
    action: [["system", "system"], ["op", "do"], ["target", "target"], ["value", "value"], ["comment", "comment"]],
    approval: [["who", "who"], ["question", "ask"], ["wait", "wait"]],
    wait: [["until", "until"], ["max", "max"], ["then", "then"]],
    notify: [["to", "to"], ["via", "via"], ["text", "text"]],
    end: [],
  };
  for (const b of Object.values(blocks)) {
    lines.push(`  ${b.id}:`, `    type: ${b.type}`, `    title: ${q(b.title)}`);
    for (const [k, name] of fields[b.type]) {
      const v = b.cfg[k];
      if (v === undefined || v === "" || (Array.isArray(v) && v.length === 0)) continue;
      if (Array.isArray(v)) lines.push(`    ${name}: [${v.join(", ")}]`);
      else if (typeof v === "string") lines.push(`    ${name}: ${q(v)}`);
      else lines.push(`    ${name}: ${v}`);
    }
    const out = links.filter((l) => l.from === b.id);
    if (out.length) {
      lines.push("    next:");
      for (const l of out) {
        const label = outsOf(b).find((o) => o.key === l.port)?.label;
        lines.push(`      ${label ? q(label) : "-"}: ${l.to}`);
      }
    }
  }
  lines.push("", "layout:");
  for (const b of Object.values(blocks)) {
    const p = positions[b.id];
    if (p) lines.push(`  ${b.id}: [${Math.round(p.x)}, ${Math.round(p.y)}]`);
  }
  return lines.join("\n");
}

/* ---------- example data ---------- */

export function example(lang: string): Example {
  const de = lang.startsWith("de");
  const L = (d: string, e: string) => (de ? d : e);
  const owner = "Alex";

  const people: Record<string, Person> = {
    Ada: { name: "Ada", dept: L("Entwicklung", "Engineering"), job: "Engineer", state: "ok", load: 2 },
    Kim: { name: "Kim", dept: L("Entwicklung", "Engineering"), job: "Engineer", state: "ok", load: 1 },
    Ole: { name: "Ole", dept: L("Entwicklung", "Engineering"), job: "Engineer", state: "draft", load: 0 },
    Rosa: { name: "Rosa", dept: L("Qualität", "Quality"), job: L("Testerin", "Tester"), state: "ok", load: 2 },
    Theo: { name: "Theo", dept: L("Qualität", "Quality"), job: "Code-Review", state: "stopped", load: 0 },
    Mia: { name: "Mia", dept: "Support", job: L("Ticket-Sichtung", "Ticket triage"), state: "ok", load: 0 },
  };
  const rEng = L("Entwicklung", "Engineering");
  const rRev = "Code-Review";
  const rTest = "Test";
  const roles = { [rEng]: ["Ada", "Kim", "Ole"], [rRev]: ["Theo"], [rTest]: ["Rosa"] };

  const blocks: Record<string, Block> = {
    t: { id: "t", type: "trigger", title: L("Ticket zugewiesen", "Ticket assigned"), cfg: { source: "GitLab · acme/timetable", event: L("Ticket wird der Stelle Entwicklung zugewiesen, oder jemand gibt es im Chat in Auftrag", "A ticket is assigned to the Engineering role, or someone asks for it in the chat"), filter: L("Label: Kunde", "Label: customer") } },
    b: { id: "b", type: "agent", title: L("Umsetzen", "Build"), cfg: { role: rEng, pick: "load", sticky: true, cap: "2", notSame: "", optional: false, task: L("Setz {{ticket.title}} um. Lies den Verlauf, auch frühere Mängel aus {{accept.defects}}. Öffne einen Merge Request mit „Closes #{{ticket.no}}“.", "Implement {{ticket.title}}. Read the history, including earlier defects from {{accept.defects}}. Open a merge request with “Closes #{{ticket.no}}”."), output: ["mr", "summary"], ask: owner, limit: L("4 Std.", "4 h") } },
    r: { id: "r", type: "agent", title: L("Code prüfen", "Review code"), cfg: { role: rRev, pick: "load", sticky: false, cap: "3", notSame: "b", optional: true, task: L("Prüf {{build.mr}} auf Lesbarkeit, Tests und Seiteneffekte.", "Review {{build.mr}} for readability, tests and side effects."), output: ["verdict", "notes"], ask: owner, limit: L("2 Std.", "2 h") } },
    a: { id: "a", type: "agent", title: L("Abnahme", "Acceptance"), cfg: { role: rTest, pick: "load", sticky: true, cap: "3", notSame: "b", optional: false, task: L("Teste {{build.mr}} gegen das Ticket auf der Testumgebung. Melde abgenommen oder die Mängel als Liste.", "Test {{build.mr}} against the ticket on the test system. Report accepted, or the defects as a list."), output: ["result", "defects"], ask: owner, limit: L("4 Std.", "4 h") } },
    c: { id: "c", type: "condition", title: L("Abgenommen?", "Accepted?"), cfg: { field: "{{accept.result}}", rounds: 3 }, outs: [{ key: "yes", label: L("abgenommen", "accepted") }, { key: "no", label: L("Mängel", "defects") }, { key: "limit", label: L("nach 3 Runden", "after 3 rounds") }] },
    s: { id: "s", type: "action", title: L(`Ticket an ${owner}`, `Ticket to ${owner}`), cfg: { system: "GitLab", op: "assign", target: "{{ticket.no}}", value: owner, comment: L("Abgenommen, MR {{build.mr}}. Bitte Kundentest anstoßen.", "Accepted, MR {{build.mr}}. Please start the customer test.") } },
    w: { id: "w", type: "wait", title: L("Kundentest", "Customer test"), cfg: { until: L("Label „kunde-ok“ am Ticket", "Label “customer-ok” on the ticket"), max: L("14 Tage", "14 days"), then: L(`Erinnerung an ${owner}`, `Reminder to ${owner}`) } },
    e: { id: "e", type: "end", title: L("Fertig", "Done"), cfg: {} },
    h: { id: "h", type: "approval", title: L(`${owner} entscheidet`, `${owner} decides`), cfg: { who: owner, question: L("Drei Runden ohne Abnahme bei #{{ticket.no}}. Letzte Mängel: {{accept.defects}}", "Three rounds without acceptance on #{{ticket.no}}. Last defects: {{accept.defects}}"), wait: L("unbegrenzt", "no limit") }, outs: [{ key: "retry", label: L("neue Runde", "another round") }, { key: "self", label: L("übernehme ich", "I'll take it") }] },
    n: { id: "n", type: "notify", title: L("Kurz Bescheid", "Let them know"), cfg: { to: owner, via: "Chat", text: L("#{{ticket.no}} liegt jetzt bei dir.", "#{{ticket.no}} is with you now.") } },
  };
  const positions = {
    t: { x: 40, y: 200 }, b: { x: 330, y: 200 }, r: { x: 620, y: 200 }, a: { x: 910, y: 200 }, c: { x: 1200, y: 182 },
    s: { x: 1500, y: 70 }, w: { x: 1790, y: 70 }, e: { x: 2080, y: 70 }, h: { x: 1500, y: 380 }, n: { x: 1790, y: 420 },
  };
  const links: Link[] = [
    { from: "t", port: "out", to: "b" }, { from: "b", port: "out", to: "r" }, { from: "r", port: "out", to: "a" }, { from: "a", port: "out", to: "c" },
    { from: "c", port: "yes", to: "s" }, { from: "c", port: "no", to: "b" }, { from: "c", port: "limit", to: "h" },
    { from: "s", port: "out", to: "w" }, { from: "w", port: "out", to: "e" },
    { from: "h", port: "retry", to: "b" }, { from: "h", port: "self", to: "n" }, { from: "n", port: "out", to: "e" },
  ];

  const live: Example["live"] = {
    b: { cases: [
      { no: "#431", title: L("Stundenplan als PDF exportieren", "Export the timetable as PDF"), by: "Ada", since: L("35 Min.", "35 min"), level: "warn", why: L(`Rückfrage an ${owner} offen`, `question to ${owner} open`) },
      { no: "#427", title: L("Raumwechsel ohne Benachrichtigung", "Room change sends no notice"), by: "Ada", since: L("1 Std. · Runde 2", "1 h · round 2"), level: "ok" },
      { no: "#409", title: L("Doppelstunden über die Pause", "Double lessons across the break"), by: "Kim", since: L("6 Std.", "6 h"), level: "bad", why: L("Zeitlimit 4 Std. überschritten", "over the 4 h limit") },
    ] },
    r: { cases: [], note: L("Theo ist gestoppt und niemand sonst hat die Stelle. Alle Läufe überspringen den Schritt.", "Theo is stopped and nobody else holds the role. Every run skips the step.") },
    a: { cases: [
      { no: "#425", title: L("Vertretungsplan zeigt falsche Woche", "Cover plan shows the wrong week"), by: "Rosa", since: L("50 Min.", "50 min"), level: "ok" },
      { no: "#418", title: L("Kursliste filtert Ehemalige nicht", "Course list keeps former pupils"), by: "Rosa", since: L("2 Std.", "2 h"), level: "ok" },
    ] },
    w: { cases: [
      { no: "#402", title: L("Import aus Excel bricht ab", "Excel import stops"), by: owner, since: L("2 Tage", "2 days"), level: "ok" },
      { no: "#398", title: L("Feiertage fehlen", "Public holidays missing"), by: owner, since: L("4 Tage", "4 days"), level: "ok" },
      { no: "#377", title: L("Anwesenheit: Export leer", "Attendance export is empty"), by: owner, since: L("9 Tage", "9 days"), level: "warn", why: L("Erinnerung in 5 Tagen", "reminder in 5 days") },
    ] },
    h: { cases: [
      { no: "#384", title: L("Import mit Umlauten", "Import with umlauts"), by: owner, since: L("2 Tage", "2 days"), level: "bad", why: L("wartet auf deine Entscheidung im Chat", "waits for your decision in the chat") },
    ] },
  };

  const runs: Record<string, Run> = {
    "#431": { no: "#431", title: live.b.cases[0].title, steps: [
      { block: "t", when: L("heute 10:05", "today 10:05"), text: L("Im Chat an Ada: „schau dir bitte #431 an“. Liegt in acme/timetable, läuft deshalb über den Ablauf.", "In the chat to Ada: “please look at #431”. It lies in acme/timetable, so it runs through the workflow."), chat: true },
      { block: "b", when: L("heute 10:06", "today 10:06"), text: L("Ada wurde im Chat direkt angesprochen und übernimmt, obwohl Kim weniger offen hat.", "Ada was asked directly in the chat and takes it, although Kim has less open."), out: { by: "Ada" } },
      { block: "b", when: L("heute 10:31 – jetzt", "today 10:31 – now"), text: L(`Rückfrage an ${owner} im Chat: „Sollen Vertretungen mit in den Export?“`, `Question to ${owner} in the chat: “Should cover lessons go into the export?”`), now: true, chat: true },
    ] },
    "#427": { no: "#427", title: live.b.cases[1].title, steps: [
      { block: "t", when: L("Mo 09:12", "Mon 09:12"), text: L("Ticket der Stelle Entwicklung zugewiesen", "Ticket assigned to the Engineering role"), out: { ticket: "#427" } },
      { block: "b", when: L("Mo 09:13 – 10:02", "Mon 09:13 – 10:02"), text: L("Ada (am wenigsten ausgelastet): MR !212", "Ada (least busy): MR !212"), out: { mr: "!212", summary: L("Benachrichtigung beim Raumwechsel ergänzt", "Notice on room change added") } },
      { block: "r", when: L("Mo 10:02", "Mon 10:02"), text: L("übersprungen: niemand in der Stelle Code-Review verfügbar", "skipped: nobody available in the Code-Review role"), skip: true },
      { block: "a", when: L("Mo 10:03 – 10:41", "Mon 10:03 – 10:41"), text: L("Rosa: Mängel", "Rosa: defects"), out: { result: L("Mängel", "defects"), defects: L("Mail geht auch bei Raumwechseln in der Vergangenheit raus", "Mail also goes out for room changes in the past") } },
      { block: "c", when: L("Mo 10:41", "Mon 10:41"), text: L("Mängel, Runde 1 von 3 → zurück, bleibt bei Ada", "defects, round 1 of 3 → back, stays with Ada") },
      { block: "b", when: L("Mo 10:42 – jetzt", "Mon 10:42 – now"), text: L("Ada arbeitet an Runde 2", "Ada works on round 2"), now: true },
    ] },
    "#402": { no: "#402", title: live.w.cases[0].title, steps: [
      { block: "t", when: L("Do 14:20", "Thu 14:20"), text: L("Ticket der Stelle Entwicklung zugewiesen", "Ticket assigned to the Engineering role") },
      { block: "b", when: L("Do 14:21 – 15:30", "Thu 14:21 – 15:30"), text: "Kim: MR !205", out: { mr: "!205", summary: L("Leere Zeilen am Tabellenende übersprungen", "Empty rows at the end of the sheet skipped") } },
      { block: "r", when: L("Do 15:30", "Thu 15:30"), text: L("übersprungen: Stelle Code-Review unbesetzt", "skipped: Code-Review role unfilled"), skip: true },
      { block: "a", when: L("Do 15:31 – 16:05", "Thu 15:31 – 16:05"), text: L("Rosa: abgenommen", "Rosa: accepted"), out: { result: L("abgenommen", "accepted") } },
      { block: "c", when: L("Do 16:05", "Thu 16:05"), text: L("abgenommen → weiter", "accepted → on") },
      { block: "s", when: L("Do 16:05", "Thu 16:05"), text: L(`covey hat das Ticket ${owner} zugewiesen und kommentiert`, `covey assigned the ticket to ${owner} and commented`), out: { action: `assign #402 → ${owner}` } },
      { block: "w", when: L("Do 16:05 – jetzt", "Thu 16:05 – now"), text: L("wartet auf das Label „kunde-ok“", "waits for the label “customer-ok”"), now: true },
    ] },
    "#384": { no: "#384", title: live.h.cases[0].title, steps: [
      { block: "t", when: L("Mi 08:02", "Wed 08:02"), text: L("Ticket der Stelle Entwicklung zugewiesen", "Ticket assigned to the Engineering role") },
      { block: "b", when: L("Mi 08:03", "Wed 08:03"), text: L("Runde 1 bei Kim: MR !198", "Round 1 with Kim: MR !198") },
      { block: "a", when: L("Mi 09:10", "Wed 09:10"), text: L("Rosa: Mängel", "Rosa: defects") },
      { block: "c", when: L("Mi 09:10", "Wed 09:10"), text: L("Runde 1 von 3", "round 1 of 3") },
      { block: "b", when: L("Mi 09:11", "Wed 09:11"), text: L("Runde 2, bleibt bei Kim", "Round 2, stays with Kim") },
      { block: "a", when: L("Mi 10:40", "Wed 10:40"), text: L("Rosa: Mängel", "Rosa: defects") },
      { block: "c", when: L("Mi 10:40", "Wed 10:40"), text: L("Runde 2 von 3", "round 2 of 3") },
      { block: "b", when: L("Mi 10:41", "Wed 10:41"), text: L("Runde 3, bleibt bei Kim", "Round 3, stays with Kim") },
      { block: "a", when: L("Mi 12:15", "Wed 12:15"), text: L("Rosa: Mängel", "Rosa: defects"), out: { defects: L("„ß“ wird beim Import zu „ss“", "“ß” turns into “ss” on import") } },
      { block: "c", when: L("Mi 12:15", "Wed 12:15"), text: L(`3 Runden erreicht → an ${owner}`, `3 rounds reached → to ${owner}`) },
      { block: "h", when: L("Mi 12:15 – jetzt", "Wed 12:15 – now"), text: L(`Entscheidungskarte liegt in ${owner}s Chat`, `The decision card lies in ${owner}'s chat`), now: true, chat: true },
    ] },
  };

  const workflows: WorkflowRow[] = [
    { id: "ticket", name: L("Kundenticket", "Customer ticket"), desc: L("Ticket bis zum Kundentest", "Ticket to customer test"), scope: "GitLab · acme/timetable", chain: [rEng, rRev, rTest, owner], mode: "steer", active: 11, stuck: 2 },
    { id: "support", name: L("Supportanfrage", "Support request"), desc: L("Kundenmail bis zur Antwort, Fehler an die Entwicklung", "Customer mail to answer, bugs to engineering"), scope: "Zammad → GitLab", chain: [L("KI: einordnen", "AI: classify"), "Support", rEng, "Support"], mode: "watch", active: 7, stuck: 0 },
    { id: "blog", name: L("Blogartikel", "Blog post"), desc: L("Thema bis zur Veröffentlichung", "Topic to publication"), scope: "CMS", chain: [L("Redaktion", "Writing"), L("Lektorat", "Editing"), L("Freigabe", "Approval")], mode: "steer", active: 2, stuck: 0 },
    { id: "hire", name: L("Neue Kollegin einstellen", "Hire a colleague"), desc: L("Anfrage bis zum ersten Lauf", "Request to first run"), scope: "covey", chain: [L("KI: Entwurf", "AI: draft"), L("Freigabe", "Approval"), "Secrets"], mode: "off", active: 0, stuck: 0 },
  ];

  return {
    owner,
    scope: "gitlab acme/timetable",
    name: workflows[0].name,
    people,
    roles,
    blocks,
    positions,
    links,
    live,
    runs,
    workflows,
    rails: [
      { who: L("Stelle Test", "Test role"), text: L("darf im Projekt lesen und zuweisen, nie kommentieren", "may read and assign in the project, never comment") },
      { who: L("alle", "all"), text: L("kein Merge ohne Freigabe", "no merge without approval") },
      { who: L("alle", "all"), text: L("gestoppte Kollegen werden nicht angesprochen", "stopped colleagues are not addressed") },
    ],
    options: {
      sources: ["GitLab · acme/timetable", "GitLab · acme/shop", "Zammad", L("E-Mail-Postfach", "Mailbox"), L("Zeitplan", "Schedule"), L("Formular", "Form"), L("Chat-Nachricht", "Chat message")],
      limits: L("1 Std.|2 Std.|4 Std.|1 Tag", "1 h|2 h|4 h|1 day").split("|"),
      waits: L("1 Tag|2 Tage|unbegrenzt", "1 day|2 days|no limit").split("|"),
      maxes: L("1 Tag|7 Tage|14 Tage|30 Tage", "1 day|7 days|14 days|30 days").split("|"),
      channels: ["Chat", L("E-Mail", "Email"), "Teams"],
      approvers: [owner, L("Teamleitung Entwicklung", "Engineering lead")],
      askTo: [owner, L("wer den Vorgang im Chat gegeben hat", "whoever asked for it in the chat"), L("Betreuer:in des Kollegen", "the colleague's supervisor")],
    },
    chat: {
      colleague: "Ada",
      caseNo: "#431",
      request: L("Schau dir bitte #431 an, den PDF-Export vom Stundenplan.", "Please look at #431, the timetable's PDF export."),
      answer: L("Mach ich. #431 liegt in acme/timetable und läuft deshalb über den Ablauf Kundenticket. Ich bin beim Umsetzen, danach geht es an die Abnahme.", "Will do. #431 lies in acme/timetable, so it runs through the Customer ticket workflow. I'm on the build step; acceptance comes next."),
      question: L("Sollen die Vertretungen mit in den Export oder nur der reguläre Plan? Im Ticket steht dazu nichts.", "Should cover lessons go into the export, or only the regular plan? The ticket says nothing about it."),
      thanks: L("Danke, baue ich so ein.", "Thanks, I'll build it that way."),
    },
  };
}
