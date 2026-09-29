import { describe, it, expect, beforeEach } from "vitest";
import { fireEvent, screen, within } from "@testing-library/react";
import Voices from "./Voices";
import { mockFetch, renderWithProviders, testPrincipal, useGerman } from "../test/render";
import type { VoiceDetail } from "../api";

beforeEach(() => useGerman());

// A described voice whose draft waits: the page's one next action is the
// release, and the review is where it opens (#464).
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
  tone: "",
  checks: [],
  assignable: false,
  agents: [],
};

const routes = {
  "/api/v1/voices/v1/corrections": [],
  "/api/v1/voices/v1": draft,
  "/api/v1/voices": [draft],
  "/api/v1/assist/status": { available: false },
};

const current = () => screen.getByRole("navigation", { name: "Schritte" }).querySelector("[aria-current]");

describe("the voice page (#464)", () => {
  it("opens a voice on the step the address names", async () => {
    mockFetch(routes);
    renderWithProviders(<Voices me={testPrincipal()} />, { route: "/voices?voice=v1&step=tone" });
    expect(await screen.findByText("Schritt 4 von 6")).toBeInTheDocument();
    expect(current()).toHaveTextContent("Chat-Ton");
  });

  it("without an address, opens on what needs attention and names it in the head", async () => {
    mockFetch(routes);
    renderWithProviders(<Voices me={testPrincipal()} />, { route: "/voices" });
    const action = await screen.findByRole("button", { name: "Prüfen und freigeben" });
    fireEvent.click(action);
    expect(await screen.findByText("Schritt 6 von 6")).toBeInTheDocument();
    // The card reads as formatted text, the passages three at a time.
    expect(screen.getByRole("heading", { name: "Die Hand" })).toBeInTheDocument();
    expect(screen.getByText("drei")).toBeInTheDocument();
    expect(screen.queryByText("vier")).toBeNull();
    expect(screen.getByRole("button", { name: "Alle 4 zeigen" })).toBeInTheDocument();
  });

  it("a new voice cannot be addressed past its first step", async () => {
    mockFetch(routes);
    renderWithProviders(<Voices me={testPrincipal()} />, { route: "/voices?new=1&step=release" });
    expect(await screen.findByText("Schritt 1 von 6")).toBeInTheDocument();
    const nav = screen.getByText("Schritt 1 von 6").closest("section")!;
    expect(within(nav).getByRole("button", { name: "Weiter" })).toBeDisabled();
    expect(within(nav).getByText("Geben Sie der Stimme einen Namen.")).toBeInTheDocument();
  });
});
