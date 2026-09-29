import { describe, it, expect, beforeEach } from "vitest";
import { fireEvent, render, screen, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router";
import Voices from "./Voices";
import VoicePage from "./VoicePage";
import { mockFetch, testPrincipal, useGerman } from "../test/render";
import type { VoiceDetail } from "../api";

beforeEach(() => useGerman());

// A described voice whose draft waits: its next action is the release.
const draft: VoiceDetail = {
  id: "v1",
  name: "Kundendienst",
  language: "de",
  version: 0,
  exemplars: [],
  contrast: [],
  notes: [],
  card: "## Die Hand\n\nKurze Sätze.",
  released_card: "",
  words: 0,
  documents: 0,
  source: "described",
  purpose: "support_mail",
  description: "Wir schreiben kurz.",
  draft_exemplars: [
    { role: "opening", text: "eins" },
    { role: "evidence", text: "zwei" },
    { role: "closing", text: "drei" },
    { role: "short", text: "vier" },
  ],
  corpus: [],
  tone: "## Ton\nKurz.",
  checks: [],
  assignable: false,
  agents: [],
};

const routes = (v: VoiceDetail = draft) => ({
  "/api/v1/voices/v1/corrections": [],
  "/api/v1/voices/v1": v,
  "/api/v1/voices": [v],
  "/api/v1/assist/status": { available: false },
});

const renderAt = (route: string) =>
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })}>
      <MemoryRouter initialEntries={[route]}>
        <Routes>
          <Route path="/voices" element={<Voices me={testPrincipal()} />} />
          <Route path="/voices/:id" element={<VoicePage me={testPrincipal()} />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );

const current = () => screen.getByRole("navigation", { name: "Schritte" }).querySelector("[aria-current]");

describe("the list of voices (#466)", () => {
  it("is a list of heads, each leading to its voice and to what it waits for", async () => {
    mockFetch(routes());
    renderAt("/voices");
    expect(await screen.findByRole("link", { name: "Kundendienst" })).toHaveAttribute("href", "/voices/v1");
    expect(screen.getByRole("link", { name: "Prüfen und freigeben" })).toHaveAttribute("href", "/voices/v1?step=release");
    // No flow inside the list.
    expect(screen.queryByRole("navigation", { name: "Schritte" })).toBeNull();
  });

  it("sends an old list link to the voice's page, on its step", async () => {
    mockFetch(routes());
    renderAt("/voices?voice=v1&step=tone");
    expect(await screen.findByText("Schritt 4 von 6")).toBeInTheDocument();
    expect(current()).toHaveTextContent("Chat-Ton");
  });

  it("a new voice cannot be addressed past its first step, and says why once, at Next", async () => {
    mockFetch(routes());
    renderAt("/voices?new=1&step=release");
    expect(await screen.findByText("Schritt 1 von 6")).toBeInTheDocument();
    const pane = screen.getByText("Schritt 1 von 6").closest("section")!;
    expect(within(pane).getByRole("button", { name: "Weiter" })).toBeDisabled();
    expect(screen.getAllByText("Geben Sie der Stimme einen Namen.")).toHaveLength(1);
  });
});

describe("a voice's page (#466)", () => {
  it("opens on what needs attention and does not repeat it as the head's action", async () => {
    mockFetch(routes());
    renderAt("/voices/v1");
    expect(await screen.findByText("Schritt 6 von 6")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Prüfen und freigeben" })).toBeNull();
    // The card reads as formatted text, the passages three at a time.
    expect(screen.getByRole("heading", { name: "Die Hand" })).toBeInTheDocument();
    expect(screen.queryByText("vier")).toBeNull();
    // On another step the head leads back to it.
    fireEvent.click(within(screen.getByRole("navigation", { name: "Schritte" })).getByText("Chat-Ton"));
    fireEvent.click(await screen.findByRole("button", { name: "Prüfen und freigeben" }));
    expect(await screen.findByText("Schritt 6 von 6")).toBeInTheDocument();
  });

  it("a source set before the purpose reads as set, not as done, and the blocker is said once", async () => {
    mockFetch(routes({ ...draft, purpose: "" }));
    renderAt("/voices/v1?step=purpose");
    expect(await screen.findByText("Schritt 1 von 6")).toBeInTheDocument();
    const list = screen.getByRole("navigation", { name: "Schritte" });
    expect(within(list).getAllByText("schon gesetzt").length).toBeGreaterThan(0);
    expect(within(list).queryByText("erledigt")).toBeNull();
    expect(screen.getByText("0 von 6 Schritten erledigt")).toBeInTheDocument();
    expect(screen.getAllByText("Wählen Sie, wofür die Stimme ist.")).toHaveLength(1);
  });

  it("carries what people changed and what the agent gets in tabs", async () => {
    mockFetch(routes());
    renderAt("/voices/v1?tab=corrections");
    expect(await screen.findByText("Was Menschen geändert haben")).toBeInTheDocument();
    expect(screen.queryByRole("navigation", { name: "Schritte" })).toBeNull();
    fireEvent.click(screen.getByRole("tab", { name: "Was der Agent bekommt" }));
    expect(await screen.findByText(/Kurz\./)).toBeInTheDocument();
  });
});
