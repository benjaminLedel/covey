import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { GitHubAppPanel } from "./GitHubApp";
import { mockFetch, renderWithProviders, useGerman } from "../../test/render";

beforeEach(() => useGerman());

describe("GitHubAppPanel", () => {
  it("bietet die gemeinsame App an und startet die Installation", async () => {
    mockFetch({
      "/api/v1/integrations/github": { shared_available: true, shared_slug: "covey", can_create: true, installed: false },
      "POST /api/v1/integrations/github/connect": { url: "https://github.com/apps/covey/installations/new?state=x" },
    });
    const assign = vi.fn();
    Object.defineProperty(window, "location", { value: { ...window.location, set href(v: string) { assign(v); } }, writable: true });
    renderWithProviders(<GitHubAppPanel onChanged={() => {}} />);

    fireEvent.click(await screen.findByRole("button", { name: "Mit GitHub verbinden" }));
    await waitFor(() => expect(assign).toHaveBeenCalledWith("https://github.com/apps/covey/installations/new?state=x"));
    expect(screen.getByText(/deine Agenten erscheinen als covey\[bot\]/)).toBeInTheDocument();
  });

  it("zeigt eine verbundene App mit ihrem Bot", async () => {
    mockFetch({
      "/api/v1/integrations/github": {
        shared_available: false, can_create: true, installed: true, bot_login: "acme-agents[bot]",
        connection: { source: "own", app_slug: "acme-agents", installation_id: 9, account_login: "acme" },
      },
    });
    renderWithProviders(<GitHubAppPanel onChanged={() => {}} />);
    expect(await screen.findByText("Verbunden als acme-agents[bot] · acme")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Mit GitHub verbinden" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Trennen" })).toBeInTheDocument();
  });

  it("ohne Seitenadresse kein Start", async () => {
    mockFetch({ "/api/v1/integrations/github": { shared_available: true, shared_slug: "covey", can_create: false, installed: false } });
    renderWithProviders(<GitHubAppPanel onChanged={() => {}} />);
    expect(await screen.findByText(/Braucht eine Seitenadresse/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Mit GitHub verbinden" })).not.toBeInTheDocument();
  });
});
