import { describe, it, expect } from "vitest";
import { groupByDepartment, matches } from "./Dashboard";
import type { Agent, Department } from "../api";

/* Die Übersicht war eine Kachelwand in Anlegereihenfolge. Ab etwa einem Dutzend
   Agenten ist die Frage nicht mehr „wer ist da", sondern „wer im Support" und
   „wo ist Brunhilde" — und beides beantwortet die Ordnung, die das Organigramm
   längst hat. Die Sortierung und die Suche sind reine Funktionen, damit genau
   das prüfbar ist, wonach jemand sucht. */

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

  // Eine Abteilungsüberschrift ohne Treffer ist bei aktiver Suche genau die
  // Zeile, die den Blick kostet.
  it("drops groups that have no hit", () => {
    const groups = groupByDepartment(staff, [support, dev], "egon");
    expect(groups).toHaveLength(1);
    expect(groups[0].name).toBe("Entwicklung");
  });

  // Wer „support" tippt, meint die Abteilung — auch wenn das Wort im Namen der
  // Person nicht vorkommt.
  it("finds people by their department", () => {
    const groups = groupByDepartment(staff, [support, dev], "support");
    expect(groups.map((g) => g.agents.map((a) => a.slug))).toEqual([["wanda"]]);
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
