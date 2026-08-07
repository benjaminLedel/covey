import { describe, it, expect, beforeEach } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import Secrets from "./Secrets";
import { mockFetch, renderWithProviders, testPrincipal, useGerman } from "../test/render";

// Die Secrets-Seite verwaltet WERTE — verschlüsselt, org-gebunden, mehrere je
// Schlüssel. Was mit ihnen geschieht (wer darauf sitzt, was sie verbrauchen
// dürfen) steht bei den Arbeitsplätzen; diese Trennung ist das, was die Tests
// hier festhalten.

const AGENT_ID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee";
const agents = [{ id: AGENT_ID, slug: "alice", display_name: "Alice Beispiel" }];

const value = (slot: number, prefix = "sk-a") => ({
  slot,
  prefix,
  sensitive: true,
  updated_at: "2026-01-01T00:00:00Z",
});

const previews = [
  {
    key: "zammad_token",
    prefix: "demo",
    sensitive: true,
    agent_ids: [AGENT_ID],
    values: [value(0, "demo")],
  },
  {
    key: "claude_code_oauth_token",
    prefix: "sk-a",
    sensitive: true,
    agent_ids: [AGENT_ID],
    values: [value(0), value(1)],
  },
];

const routen = { "/api/v1/secrets": previews, "/api/v1/agents": agents };

describe("Secrets — mehrere Werte je Schlüssel", () => {
  beforeEach(() => useGerman());

  it("klappt einen Schlüssel mit einem Wert zu und einen mit mehreren auf", async () => {
    mockFetch(routen);
    renderWithProviders(<Secrets me={testPrincipal()} />);

    // Der Normalfall bleibt leise: ein Wert, eingeklappt.
    expect(await screen.findByRole("button", { name: /1 Wert$/ })).toBeInTheDocument();
    // Mehrere zeigen sich von selbst — sonst müsste man erst suchen, was man hat.
    expect(await screen.findByRole("button", { name: /2 Werte$/ })).toBeInTheDocument();
  });

  it("verweist für Auslastung und Sitzbelegung auf die Arbeitsplätze", async () => {
    // Die Trennung soll der Nutzer nicht raten müssen: die Seite sagt selbst,
    // wo das Übrige steht.
    mockFetch(routen);
    renderWithProviders(<Secrets me={testPrincipal()} />);
    expect(await screen.findByText(/Arbeitsplätzen/)).toBeInTheDocument();
  });

  it("zeigt sensible Werte nur als Präfix, nie im Klartext", async () => {
    mockFetch(routen);
    renderWithProviders(<Secrets me={testPrincipal()} />);
    const masked = await screen.findAllByText(/sk-a••••••••/);
    expect(masked.length).toBeGreaterThan(0);
  });

  it("legt einen weiteren Wert an", async () => {
    const { calls } = mockFetch({
      ...routen,
      "POST /api/v1/secrets/claude_code_oauth_token/values": {
        ok: true,
        slot: 2,
        check: { checked: false, valid: false },
      },
    });
    renderWithProviders(<Secrets me={testPrincipal()} />);

    await userEvent.click((await screen.findAllByRole("button", { name: "Wert hinzufügen" }))[0]);
    await userEvent.type(screen.getByLabelText("Neuer Wert"), "sk-ant-oat-neu");
    await userEvent.click(screen.getByRole("button", { name: "Hinzufügen" }));

    await waitFor(() =>
      expect(calls).toContain("POST /api/v1/secrets/claude_code_oauth_token/values"),
    );
  });

  it("verbirgt die Bearbeitung vor Rollen ohne Schreibrecht", async () => {
    mockFetch(routen);
    renderWithProviders(<Secrets me={testPrincipal("auditor")} />);

    await screen.findByRole("button", { name: /2 Werte$/ });
    expect(screen.queryByRole("button", { name: "Wert hinzufügen" })).not.toBeInTheDocument();
  });
});
