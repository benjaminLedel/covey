import { describe, it, expect, beforeEach, vi } from "vitest";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { AgentVoices, DepartmentVoicesDialog, UsedBy, WhoGetsWhat } from "./Slots";
import { mockFetch, renderWithProviders, useGerman } from "../../test/render";
import type { Department, Voice } from "../../api";

beforeEach(() => useGerman());

// Voices per occasion on screen (#471): the agent's slots with what applies
// when one is empty, the department's line with its bound, and the table of
// who gets what.

const built = (id: string, name: string): Voice => ({
  id,
  name,
  language: "de",
  version: 1,
  profile: { bands: { sentence_len: [8, 14] } },
  exemplars: [],
  contrast: [],
  notes: [],
  card: "",
  released_card: "",
  words: 100,
  documents: 3,
  source: "texts",
  purpose: "chat",
  description: "",
  draft_exemplars: [],
});
const voices = [built("v-haus", "Hausstimme"), built("v-klar", "Klar")];

const agentVoices = {
  slots: { chat: null, customers: { voice_id: "v-klar", voice: "Klar" }, publications: null },
  effective: {
    chat: { voice_id: "v-haus", voice: "Hausstimme", level: "org", reason: "org×chat" },
    customers: { voice_id: "v-klar", voice: "Klar", level: "agent", reason: "agent×customers" },
    publications: { voice_id: null, voice: "", level: "none", reason: "none×publications" },
  },
};

describe("the agent's voices", () => {
  it("shows each slot, and for an empty one what applies instead", async () => {
    mockFetch({ "/api/v1/agents/a1/voices": agentVoices, "/api/v1/voices": voices });
    renderWithProviders(<AgentVoices agent={{ id: "a1" }} editable />);
    expect(await screen.findByText("Leer: Es gilt Hausstimme (Vorgabe der Organisation).")).toBeInTheDocument();
    expect(screen.getByText("Leer: Es gilt keine Stimme.")).toBeInTheDocument();
    await waitFor(() => expect(screen.getByRole("combobox", { name: "Kunden" })).toHaveValue("v-klar"));
  });

  it("sends all three slots when one changes", async () => {
    mockFetch({ "PUT /api/v1/agents/a1/voices": agentVoices, "/api/v1/agents/a1/voices": agentVoices, "/api/v1/voices": voices });
    renderWithProviders(<AgentVoices agent={{ id: "a1" }} editable />);
    const chat = await screen.findByRole("combobox", { name: "Chat" });
    await waitFor(() => expect(chat).not.toBeDisabled());
    fireEvent.change(chat, { target: { value: "v-klar" } });
    await waitFor(() => expect(vi.mocked(fetch).mock.calls.some(([, init]) => init?.method === "PUT")).toBe(true));
    const [, init] = vi.mocked(fetch).mock.calls.find(([, i]) => i?.method === "PUT")!;
    expect(JSON.parse(String(init!.body))).toEqual({ chat: "v-klar", customers: "v-klar", publications: "" });
  });

  it("is locked for a role that may not change it", async () => {
    mockFetch({ "/api/v1/agents/a1/voices": agentVoices, "/api/v1/voices": voices });
    renderWithProviders(<AgentVoices agent={{ id: "a1" }} editable={false} />);
    expect(await screen.findByRole("combobox", { name: "Chat" })).toBeDisabled();
    expect(screen.getByText("Nur Admins und Agent-Owner ändern die Stimmen.")).toBeInTheDocument();
  });
});

describe("a department's audience and voices", () => {
  const dept: Department = {
    id: "d1",
    org_id: "o1",
    name: "Vertrieb",
    description: "",
    color: "",
    audience_note: "Kurz.",
    voices: { chat: "v-klar" },
    leads: [],
    created_at: "2026-09-01T00:00:00Z",
  };

  it("counts the note against its bound and refuses to save past it", async () => {
    mockFetch({ "/api/v1/voices": voices });
    renderWithProviders(<DepartmentVoicesDialog dept={dept} editable onClose={() => {}} />);
    expect(screen.getByText("5 / 400")).toBeInTheDocument();
    const save = screen.getByRole("button", { name: "Speichern" });
    expect(save).toBeDisabled(); // nothing changed yet
    fireEvent.change(screen.getByRole("textbox"), { target: { value: "x".repeat(401) } });
    expect(screen.getByText("401 / 400")).toBeInTheDocument();
    expect(save).toBeDisabled();
    fireEvent.change(screen.getByRole("textbox"), { target: { value: "Zahlen zuerst." } });
    expect(save).not.toBeDisabled();
  });

  it("has no save for a role that may not change it", () => {
    mockFetch({ "/api/v1/voices": voices });
    renderWithProviders(<DepartmentVoicesDialog dept={dept} editable={false} onClose={() => {}} />);
    expect(screen.queryByRole("button", { name: "Speichern" })).toBeNull();
    expect(screen.getByRole("textbox")).toBeDisabled();
  });
});

describe("who gets what", () => {
  it("lists every department and everybody without one, with the voice and where it comes from", async () => {
    mockFetch({
      "/api/v1/voices/assignments": {
        occasions: ["chat", "customers", "publications"],
        rows: [
          {
            department: { id: "d1", name: "Vertrieb" },
            audience_note: "Kurz.",
            cells: {
              chat: { voice_id: "v-klar", voice: "Klar", level: "department", reason: "department:Vertrieb×chat" },
              customers: { voice_id: "v-haus", voice: "Hausstimme", level: "org", reason: "org×customers" },
              publications: { voice_id: null, voice: "", level: "none", reason: "none×publications" },
            },
          },
          {
            department: null,
            audience_note: "",
            cells: {
              chat: { voice_id: "v-haus", voice: "Hausstimme", level: "org", reason: "org×chat" },
              customers: { voice_id: "v-haus", voice: "Hausstimme", level: "org", reason: "org×customers" },
              publications: { voice_id: null, voice: "", level: "none", reason: "none×publications" },
            },
          },
        ],
      },
      "/api/v1/agents": [],
    });
    renderWithProviders(<WhoGetsWhat />);
    const sales = (await screen.findByText("Vertrieb")).closest("tr")!;
    expect(within(sales).getByRole("link", { name: "Klar" })).toHaveAttribute("href", "/voices/v-klar");
    expect(within(sales).getByText("Abteilung Vertrieb")).toBeInTheDocument();
    expect(within(sales).getByText("keine Stimme")).toBeInTheDocument();
    const rest = screen.getByText("Kunden / ohne Abteilung").closest("tr")!;
    expect(within(rest).getAllByText("Vorgabe der Organisation")).toHaveLength(2);
  });
});

describe("where a voice is named", () => {
  it("names occasion and holder, and says so when nobody names it", () => {
    const { unmount } = renderWithProviders(
      <UsedBy
        uses={[
          { holder: "agent", id: "a1", slug: "ada", name: "Ada", occasion: "chat" },
          { holder: "department", id: "d1", name: "Vertrieb", occasion: "customers" },
          { holder: "org", name: "", occasion: "publications" },
        ]}
      />,
    );
    expect(screen.getByRole("link", { name: "Ada" })).toHaveAttribute("href", "/agents/a1");
    expect(screen.getByText("Vertrieb")).toBeInTheDocument();
    expect(screen.getByText("Veröffentlichungen")).toBeInTheDocument();
    expect(screen.getByText("Vorgabe der Organisation")).toBeInTheDocument();
    unmount();
    renderWithProviders(<UsedBy uses={[]} />);
    expect(screen.getByText("Noch nennt kein Agent, keine Abteilung und keine Vorgabe diese Stimme.")).toBeInTheDocument();
  });
});
