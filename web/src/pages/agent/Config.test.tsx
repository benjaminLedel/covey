import { describe, it, expect, beforeEach } from "vitest";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Config } from "./Config";
import { mockFetch, renderWithProviders, useGerman } from "../../test/render";

// The config page (#745): the texts a team lead writes under plain names,
// the text views of other stores folded away, nothing saved without asking.

const AGENT = "11111111-1111-1111-1111-111111111111";
const CFG = {
  version: 3,
  files: {
    "SOUL.md": "# Ada\n\nBeantwortet Rechnungsfragen.",
    "ACCESS.md": "# Zugänge\n",
    "EGRESS.md": "templates: keine\n",
    "HEARTBEAT.md": "",
  },
};

describe("Config", () => {
  beforeEach(() => useGerman());

  const render = () =>
    renderWithProviders(<Config agentId={AGENT} slug="ada" displayName="Ada" canManage canExport />);

  it("zeigt die Rolle als Text und faltet ACCESS/EGRESS weg", async () => {
    mockFetch({ [`/api/v1/agents/${AGENT}/config`]: CFG, "/api/v1/assist/status": { available: false } });
    render();
    const soul = (await screen.findByRole("heading", { name: /Rolle und Auftrag/ })).closest("section")!;
    expect(within(soul).getByText("Beantwortet Rechnungsfragen.")).toBeInTheDocument();
    expect(screen.queryByRole("textbox", { name: "Rolle und Auftrag" })).toBeNull();
    const more = screen.getByText("Weitere Dateien").closest("details")!;
    expect(more).not.toHaveAttribute("open");
    expect(within(more).getByText("ACCESS.md")).toBeInTheDocument();
    // An empty way of working is offered, not shown as an empty box.
    const playbooks = screen.getByRole("heading", { name: /Arbeitsweise/ }).closest("section")!;
    expect(within(playbooks).getByRole("button", { name: "Hinzufügen" })).toBeInTheDocument();
  });

  it("speichert erst über die Leiste, als neue Version", async () => {
    const { calls } = mockFetch({
      [`/api/v1/agents/${AGENT}/config`]: CFG,
      "/api/v1/assist/status": { available: false },
      [`PUT /api/v1/agents/${AGENT}/config`]: CFG,
    });
    render();
    const soul = (await screen.findByRole("heading", { name: /Rolle und Auftrag/ })).closest("section")!;
    await userEvent.click(within(soul).getByRole("button", { name: "Bearbeiten" }));
    await userEvent.type(screen.getByRole("textbox", { name: "Rolle und Auftrag" }), " Duzt.");
    expect(calls.some((c) => c.startsWith("PUT"))).toBe(false);
    expect(screen.getByText("Ungespeicherte Änderungen")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Als Version 4 speichern" }));
    expect(calls).toContain(`PUT /api/v1/agents/${AGENT}/config`);
  });
});
