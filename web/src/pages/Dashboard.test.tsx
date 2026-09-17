import { describe, it, expect } from "vitest";
import { groupByDepartment, matches, stateOf } from "./Dashboard";
import type { Agent, Department } from "../api";

/* The overview used to be a wall of tiles in filing order. From about a dozen
   agents on, the question is no longer "who is here" but "who in support" and
   "where is Brunhilde" — and the order the org chart already has answers both.
   The sorting and the search are pure functions, so that exactly what someone
   searches for is what can be checked. */

const agent = (over: Partial<Agent>): Agent =>
  ({
    id: over.slug ?? "id",
    slug: "slug",
    display_name: "Name",
    job_title: "",
    status: "sleeping",
    ...over,
  }) as Agent;

const dept = (id: string, name: string, color = ""): Department =>
  ({ id, org_id: "o", name, description: "", color, leads: [], created_at: "" }) as Department;

describe("groupByDepartment", () => {
  const support = dept("d1", "Support", "#abc");
  const dev = dept("d2", "Entwicklung");
  const staff = [
    agent({ slug: "egon", display_name: "Egon Rastlos", department_id: "d2" }),
    agent({ slug: "wanda", display_name: "Wanda Wachsam", department_id: "d1" }),
    agent({ slug: "solo", display_name: "Solo Ohnehaus" }),
  ];

  it("sorts departments alphabetically and puts the unassigned last", () => {
    const groups = groupByDepartment(staff, [support, dev], "");
    expect(groups.map((g) => g.name)).toEqual(["Entwicklung", "Support", ""]);
    expect(groups[2].id).toBeNull();
    expect(groups[2].agents.map((a) => a.slug)).toEqual(["solo"]);
  });

  it("keeps the department's colour so both views look like the same order", () => {
    const groups = groupByDepartment(staff, [support, dev], "");
    expect(groups.find((g) => g.name === "Support")?.color).toBe("#abc");
  });

  // A department heading without a hit is, while searching, exactly the
  // line that costs attention.
  it("drops groups that have no hit", () => {
    const groups = groupByDepartment(staff, [support, dev], "egon");
    expect(groups).toHaveLength(1);
    expect(groups[0].name).toBe("Entwicklung");
  });

  // Whoever types "support" means the department — even when the word does
  // not appear in the person's name.
  it("finds people by their department", () => {
    const groups = groupByDepartment(staff, [support, dev], "support");
    expect(groups.map((g) => g.agents.map((a) => a.slug))).toEqual([["wanda"]]);
  });
});

describe("Zustandsfilter und Reihenfolge", () => {
  const staff = [
    agent({ slug: "schlaefer", display_name: "Zora Zuletzt", status: "sleeping" }),
    agent({ slug: "arbeiter", display_name: "Anna Aktiv", status: "working" }),
    agent({ slug: "gestoppt", display_name: "Karl Kalt", status: "sleeping", killed: true }),
  ];

  // Whoever runs stands on top. Otherwise the filing order decides, and
  // that tells nobody anything.
  it("sorts the busy ones first, the stopped ones last", () => {
    const [g] = groupByDepartment(staff, [], "");
    expect(g.agents.map((a) => a.slug)).toEqual(["arbeiter", "schlaefer", "gestoppt"]);
  });

  it("filters by state and combines with the search", () => {
    const [g] = groupByDepartment(staff, [], "", ["sleeping"]);
    expect(g.agents.map((a) => a.slug)).toEqual(["schlaefer"]);
    expect(groupByDepartment(staff, [], "anna", ["sleeping"])).toHaveLength(0);
  });

  // A stopped agent still carries its last status — the badge shows
  // "stopped" all the same, and the filter has to mean the same thing.
  it("a stopped agent is stopped, whatever its status says", () => {
    expect(stateOf(staff[2])).toBe("killed");
    const [g] = groupByDepartment(staff, [], "", ["killed"]);
    expect(g.agents.map((a) => a.slug)).toEqual(["gestoppt"]);
  });
});

describe("matches", () => {
  const a = agent({ slug: "tester-1", display_name: "Egon Rastlos", job_title: "QA", status: "working" });

  it("searches name, role, slug and state", () => {
    for (const q of ["egon", "RASTLOS", "qa", "tester", "working"]) {
      expect(matches(a, "", q)).toBe(true);
    }
    expect(matches(a, "", "brunhilde")).toBe(false);
  });

  it("an empty query keeps everybody", () => {
    expect(matches(a, "", "   ")).toBe(true);
  });
});
