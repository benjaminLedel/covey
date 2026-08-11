import { describe, it, expect, beforeEach } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import Improvements from "./Improvements";
import { mockFetch, renderWithProviders, testPrincipal, useGerman } from "../test/render";
import type { ImprovementItem } from "../api";

// Die Liste ist die Annahme-Oberfläche UND der Kanal (spec/21). Was sie hier
// festhält, ist die eine Entscheidung, die daran hängt: WER einen Vorschlag
// annehmen darf, entscheidet sich an den Dateien, die er anfasst — nicht
// daran, wer zuerst geklickt hat.

const punkt = (over: Partial<ImprovementItem> = {}): ImprovementItem => ({
  id: "11111111-0000-0000-0000-000000000001",
  agent_id: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
  kind: "proposal",
  title: "Teilergebnis vor dem Turn-Limit abschließen",
  rationale: "22 von 23 Läufen endeten am Turn-Limit.",
  base_version: 7,
  files: { "PLAYBOOKS.md": "## Turn-Limit\nSchließe ab." },
  status: "pending",
  decision_note: "",
  applied_version: 0,
  created_at: "2026-01-02T10:00:00Z",
  agent_slug: "qa",
  agent_name: "QA-Agent",
  current_version: 7,
  stale: false,
  needs_security: false,
  diff: [{ file: "PLAYBOOKS.md", before: "## Vorgehen\nAlt.", after: "## Turn-Limit\nSchließe ab." }],
  ...over,
});

describe("Offene Punkte", () => {
  beforeEach(() => useGerman());

  it("zeigt den Vorschlag beim Kollegen, um den es geht", async () => {
    mockFetch({ "/api/v1/improvements": [punkt()] });
    renderWithProviders(<Improvements me={testPrincipal()} />);

    expect(await screen.findByText("QA-Agent")).toBeInTheDocument();
    expect(screen.getByText(/Teilergebnis vor dem Turn-Limit/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Annehmen" })).toBeEnabled();
  });

  it("zeigt den Diff erst auf Klick, und dann beide Seiten", async () => {
    mockFetch({ "/api/v1/improvements": [punkt()] });
    renderWithProviders(<Improvements me={testPrincipal()} />);

    const toggle = await screen.findByRole("button", { name: /Änderung ansehen/ });
    expect(screen.queryByText(/− ## Vorgehen/)).not.toBeInTheDocument();
    await userEvent.click(toggle);
    expect(screen.getByText(/− ## Vorgehen/)).toBeInTheDocument();
    expect(screen.getByText(/\+ ## Turn-Limit/)).toBeInTheDocument();
  });

  it("lässt den Teamleiter einen Vorschlag auf ACCESS.md nicht annehmen", async () => {
    mockFetch({ "/api/v1/improvements": [punkt({ needs_security: true })] });
    renderWithProviders(<Improvements me={testPrincipal("agent_owner")} />);

    expect(await screen.findByText(/Security entscheidet/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Annehmen" })).toBeDisabled();
    // Ablehnen nimmt nichts weg und bleibt deshalb offen.
    expect(screen.getByRole("button", { name: "Ablehnen" })).toBeEnabled();
  });

  it("lässt Security denselben Vorschlag annehmen", async () => {
    mockFetch({ "/api/v1/improvements": [punkt({ needs_security: true })] });
    renderWithProviders(<Improvements me={testPrincipal("security")} />);

    expect(await screen.findByRole("button", { name: "Annehmen" })).toBeEnabled();
  });

  it("sperrt die Annahme, solange dieselbe Datei fremd geändert wurde", async () => {
    mockFetch({
      "/api/v1/improvements": [punkt({ stale: true, current_version: 9, conflicts: ["PLAYBOOKS.md"] })],
    });
    renderWithProviders(<Improvements me={testPrincipal() } />);

    expect(await screen.findByText(/Konflikt: PLAYBOOKS.md/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Annehmen" })).toBeDisabled();
  });

  it("meldet einen veralteten, aber konfliktfreien Vorschlag nur als Hinweis", async () => {
    mockFetch({ "/api/v1/improvements": [punkt({ stale: true, current_version: 9 })] });
    renderWithProviders(<Improvements me={testPrincipal()} />);

    expect(await screen.findByText(/Gegen Version 7 geschrieben, aktuell ist 9/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Annehmen" })).toBeEnabled();
  });

  it("schickt die Entscheidung mit der Notiz an den Server", async () => {
    const { calls } = mockFetch({
      "/api/v1/improvements": [punkt()],
      "POST /api/v1/improvements/11111111-0000-0000-0000-000000000001/decide": punkt({ status: "accepted" }),
    });
    renderWithProviders(<Improvements me={testPrincipal()} />);

    await userEvent.type(await screen.findByPlaceholderText(/Notiz/), "Gute Beobachtung.");
    await userEvent.click(screen.getByRole("button", { name: "Annehmen" }));
    await waitFor(() =>
      expect(calls.some((c) => c.startsWith("POST") && c.includes("/decide"))).toBe(true),
    );
  });

  it("zeigt einen Befund ohne Diff — und ihn hakt man ab, statt ihn anzuwenden", async () => {
    mockFetch({
      "/api/v1/improvements": [
        punkt({ kind: "finding", title: "Der Auftrag passt nicht zur Rolle", files: {}, diff: undefined }),
      ],
    });
    renderWithProviders(<Improvements me={testPrincipal()} />);

    expect(await screen.findByText("Befund")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Änderung ansehen/ })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Erledigt" })).toBeEnabled();
  });
});
