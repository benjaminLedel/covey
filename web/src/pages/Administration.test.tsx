import { describe, it, expect, beforeEach } from "vitest";
import { screen } from "@testing-library/react";
import Administration from "./Administration";
import { mockFetch, renderWithProviders, testPrincipal, useGerman } from "../test/render";

// The administration panel manages THIS organisation. The test pins the
// boundary to the platform panel, and does so where you notice it: the
// numbers are those of your own organisation, and the header says which
// one that is.

const ORG = {
  id: "22222222-2222-2222-2222-222222222222",
  name: "Northgate Systems",
  description: "Bestell- und Abrechnungsplattform",
  platform_repo_system: "",
  platform_repo_project: "",
  fleet_killed: false,
  human_count: 2,
  agent_count: 3,
  created_at: "2026-01-01T00:00:00Z",
};

const AGENTEN = [
  { id: "a1", slug: "ada", display_name: "Ada", status: "working" },
  { id: "a2", slug: "kilo", display_name: "Kilo", status: "sleeping" },
  { id: "a3", slug: "nova", display_name: "Nova", status: "killed" },
];

const MENSCHEN = [
  { id: "h1", email: "chefin@northgate.de", display_name: "Mara", role: "org_admin" },
  { id: "h2", email: "owner@northgate.de", display_name: "Jonas", role: "agent_owner" },
];

const routen = {
  "/api/v1/org": ORG,
  "/api/v1/agents": AGENTEN,
  "/api/v1/users": MENSCHEN,
  "/api/v1/cost/org?days=30": { total_usd: 12.5, entries: 4, bucket: "day", series: null, agents: null, models: null, credentials: null },
  "/api/v1/org/profile-fields": [],
  "/api/v1/targets": [],
};

describe("Administrations-Panel", () => {
  beforeEach(() => useGerman());

  it("nennt im Kopf die Organisation, um die es geht", async () => {
    mockFetch(routen);
    renderWithProviders(<Administration me={testPrincipal()} />, { route: "/administration", path: "/administration/*" });

    // Twice: in the panel header and on the master-data card it shares with
    // the org chart.
    expect(await screen.findAllByText("Northgate Systems")).toHaveLength(2);
    expect(screen.getByRole("heading", { name: "Administration" })).toBeInTheDocument();
  });

  it("zählt beschäftigte Agenten ohne die schlafenden und gestoppten", async () => {
    mockFetch(routen);
    renderWithProviders(<Administration me={testPrincipal()} />, { route: "/administration/usage", path: "/administration/*" });

    // 3 agents, one of them working: sleeping does not count, killed neither —
    // a killed agent consumes nothing.
    //
    // The amount is in the notation the whole UI uses (fmtUSD): symbol after
    // the number. This used to read `$12.50`, and the page was thus one of
    // three notations for the same kind of number.
    expect(await screen.findByText("12,50 $")).toBeInTheDocument();
    const werte = document.querySelectorAll(".stat .v");
    expect([...werte].map((e) => e.textContent)).toEqual(["2", "3", "1", "12,50 $"]);
  });

  it("zeigt die Mitgliederverwaltung ohne zweite Überschrift", async () => {
    mockFetch(routen);
    renderWithProviders(<Administration me={testPrincipal()} />, { route: "/administration/members", path: "/administration/*" });

    expect(await screen.findByText("Mara")).toBeInTheDocument();
    // Exactly one h1: the panel's. The embedded page brings no
    // second one.
    expect(screen.getAllByRole("heading", { level: 1 })).toHaveLength(1);
  });
});
