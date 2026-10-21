import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { TargetSetupWizard } from "./SetupWizard";
import { mockFetch, renderWithProviders, useGerman } from "../../test/render";

beforeEach(() => useGerman());

const AGENT = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa";

/* #654: giving an agent access in the assistant also hands it the stored
   organisation-wide credentials — without the assignment the agent was set
   up and still denied. */
describe("TargetSetupWizard", () => {
  it("weist dem Agenten beim Zugang-Geben die hinterlegten Zugangsdaten zu", async () => {
    mockFetch({
      "/api/v1/targets/productboard/setup": {
        name: "productboard",
        label: "Productboard",
        enabled: true,
        credentials: [
          { key: "productboard_url", kind: "url", stored: true, optional: false },
          { key: "productboard_token", kind: "token", stored: true, optional: false },
        ],
        scopes: ["read", "write"],
        agents: [{ id: AGENT, slug: "feedback", display_name: "Feedback", access: false, scopes: [] }],
        webhook: { supported: false, secret_set: false },
        probe: true,
      },
      [`/api/v1/agents/${AGENT}/config`]: { id: "c1", agent_id: AGENT, version: 1, files: { "SOUL.md": "# Feedback" } },
      [`PUT /api/v1/agents/${AGENT}/config`]: { ok: true },
      [`PUT /api/v1/secrets/productboard_url/agents/${AGENT}`]: { ok: true },
      [`PUT /api/v1/secrets/productboard_token/agents/${AGENT}`]: { ok: true },
    });
    renderWithProviders(<TargetSetupWizard name="productboard" onClose={() => {}} />);

    expect(await screen.findByText("Zugangsschlüssel (API-Token)")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Weiter" }));
    fireEvent.change(await screen.findByRole("combobox"), { target: { value: AGENT } });
    fireEvent.click(await screen.findByRole("button", { name: "Zugang geben" }));

    const fetchMock = vi.mocked(fetch);
    const puts = () =>
      fetchMock.mock.calls
        .filter(([, init]) => init?.method === "PUT")
        .map(([url]) => String(url));
    await waitFor(() =>
      expect(puts()).toEqual([
        `/api/v1/agents/${AGENT}/config`,
        `/api/v1/secrets/productboard_url/agents/${AGENT}`,
        `/api/v1/secrets/productboard_token/agents/${AGENT}`,
      ]),
    );
    expect(await screen.findByText(/Der Agent kann das System jetzt benutzen/)).toBeInTheDocument();
  });
});
