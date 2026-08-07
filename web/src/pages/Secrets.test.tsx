import { describe, it, expect, beforeEach } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import Secrets from "./Secrets";
import { mockFetch, renderWithProviders, testPrincipal, useGerman } from "../test/render";

// Die Pool-Verwaltung auf der Secrets-Seite: mehrere Werte unter einem
// Schlüssel, ihre Auslastung, geparkte Werte.
//
// Geprüft wird, was die Seite AUSSAGT — die Zahl gegen das Limit, der geparkte
// Zustand, die Sitzbelegung. Das sind die Stellen, an denen ein Fehler nicht
// auffällt, weil eine falsche Zahl genauso plausibel aussieht wie eine richtige.

const AGENT_ID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee";

const agents = [{ id: AGENT_ID, slug: "alice", display_name: "Alice Beispiel" }];

// Ein Schlüssel mit einem Wert (der Normalfall) und einer mit dreien.
const previews = [
  {
    key: "zammad_token",
    prefix: "demo",
    sensitive: true,
    agent_ids: [AGENT_ID],
    values: [{ slot: 0, label: "", prefix: "demo", sensitive: true, limit: { amount: 0, unit: "usd", window_secs: 0 }, updated_at: "2026-01-01T00:00:00Z" }],
  },
  {
    key: "claude_code_oauth_token",
    prefix: "sk-a",
    sensitive: true,
    agent_ids: [AGENT_ID],
    values: [
      { slot: 0, label: "Abo Ben", prefix: "sk-a", sensitive: true, limit: { amount: 0, unit: "usd", window_secs: 0 }, updated_at: "2026-01-01T00:00:00Z" },
      { slot: 1, label: "Abo Team", prefix: "sk-a", sensitive: true, limit: { amount: 0, unit: "usd", window_secs: 0 }, updated_at: "2026-01-01T00:00:00Z" },
    ],
  },
];

const pool = {
  key: "claude_code_oauth_token",
  values: [
    {
      slot: 0,
      label: "Abo Ben",
      prefix: "sk-a",
      sensitive: true,
      limit: { amount: 10, unit: "usd", window_secs: 18000 },
      updated_at: "2026-01-01T00:00:00Z",
      usage: { slot: 0, usd: 2.5, tokens: 1000, runs: 3 },
      window_secs: 18000,
    },
    {
      slot: 1,
      label: "Abo Team",
      prefix: "sk-a",
      sensitive: true,
      cooldown_until: "2099-01-01T00:00:00Z",
      cooldown_reason: "error",
      limit: { amount: 0, unit: "usd", window_secs: 0 },
      updated_at: "2026-01-01T00:00:00Z",
      usage: { slot: 1, usd: 0, tokens: 0, runs: 0 },
      window_secs: 86400,
    },
  ],
  bindings: [{ agent_id: AGENT_ID, slot: 0, reason: "initial", bound_at: "2026-01-01T00:00:00Z" }],
};

const routen = {
  "/api/v1/secrets": previews,
  "/api/v1/agents": agents,
  "/api/v1/secrets/claude_code_oauth_token/pool": pool,
};

describe("Secrets — Pool", () => {
  beforeEach(() => useGerman());

  it("klappt einen Schlüssel mit einem Wert zu und einen mit mehreren auf", async () => {
    mockFetch(routen);
    renderWithProviders(<Secrets me={testPrincipal()} />);

    // Der Normalfall bleibt leise: ein Wert, eingeklappt.
    expect(await screen.findByRole("button", { name: /1 Wert$/ })).toBeInTheDocument();
    // Der Pool zeigt sich von selbst — sonst müsste man erst suchen, was man hat.
    expect(await screen.findByRole("button", { name: /2 Werte$/ })).toBeInTheDocument();
    expect(await screen.findByText("Abo Ben")).toBeInTheDocument();
    expect(await screen.findByText("Abo Team")).toBeInTheDocument();
  });

  it("zeigt den Verbrauch gegen das Limit und ohne Limit den nackten Verbrauch", async () => {
    mockFetch(routen);
    renderWithProviders(<Secrets me={testPrincipal()} />);

    // Mit Limit: gegen den Höchstwert, im Fenster des Werts (5 h).
    expect(await screen.findByText("$2.50 von $10.00 in 5 h")).toBeInTheDocument();
    // Ohne Limit: keine erfundene Prozentzahl, sondern der Verbrauch im
    // Anzeigefenster (1 d).
    expect(await screen.findByText(/kein Limit gesetzt/)).toBeInTheDocument();
  });

  it("kennzeichnet einen geparkten Wert und bietet ihn zur Freigabe an", async () => {
    mockFetch(routen);
    renderWithProviders(<Secrets me={testPrincipal()} />);

    expect(await screen.findByText(/geparkt bis/)).toBeInTheDocument();
    expect(await screen.findByRole("button", { name: "freigeben" })).toBeInTheDocument();
  });

  it("zeigt, wer auf welchem Wert sitzt", async () => {
    mockFetch(routen);
    renderWithProviders(<Secrets me={testPrincipal()} />);

    // Alice sitzt auf Slot 0 — der Name steht am Wert, nicht nur am Schlüssel.
    await screen.findByText("Abo Ben");
    const sitze = await screen.findAllByText("Alice Beispiel");
    expect(sitze.length).toBeGreaterThan(0);
  });

  it("legt einen weiteren Wert über den Pool-Endpunkt an", async () => {
    const { calls } = mockFetch({
      ...routen,
      "POST /api/v1/secrets/claude_code_oauth_token/values": { ok: true, slot: 2, check: { checked: false, valid: false } },
    });
    renderWithProviders(<Secrets me={testPrincipal()} />);

    await userEvent.click(await screen.findByRole("button", { name: "Wert hinzufügen" }));
    await userEvent.type(screen.getByLabelText("Neuer Wert"), "sk-ant-oat-neu");
    await userEvent.type(screen.getByLabelText("Bezeichnung"), "Abo Drittes");
    await userEvent.click(screen.getByRole("button", { name: "Hinzufügen" }));

    await waitFor(() =>
      expect(calls).toContain("POST /api/v1/secrets/claude_code_oauth_token/values"),
    );
  });

  it("verbirgt die Pool-Bearbeitung vor Rollen ohne Schreibrecht", async () => {
    mockFetch(routen);
    renderWithProviders(<Secrets me={testPrincipal("auditor")} />);

    await screen.findByRole("button", { name: /2 Werte$/ });
    expect(screen.queryByRole("button", { name: "Wert hinzufügen" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "freigeben" })).not.toBeInTheDocument();
  });
});
