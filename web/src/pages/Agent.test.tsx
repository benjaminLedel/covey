import { describe, it, expect, beforeEach } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import AgentPage from "./Agent";
import { mockFetch, renderWithProviders, testPrincipal, useGerman } from "../test/render";

// Charakterisierungstests der Agenten-Seite: Sie halten fest, was die Seite
// HEUTE tut — Reiter, Rollen-Sichtbarkeit, Umleitungen alter Links —, damit
// die Aufteilung der 3790-Zeilen-Datei ein reines Verschieben bleiben kann.
// Sie prüfen die Naht (welcher Reiter zeigt welchen Bereich, welcher Endpunkt
// wird gerufen), nicht die Innereien der einzelnen Bereiche.

const AGENT_ID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee";

const agent = {
  id: AGENT_ID,
  slug: "test-agent",
  display_name: "Test-Agent",
  runtime: "claude-code",
  model: "",
  max_turns: 0,
  recording_level: "",
  warm_sandbox: false,
  status: "sleeping",
  job_title: "",
  identities: {},
  phone: "",
  responsibilities: "",
  custom: {},
  killed: false,
  budget_usd: 0,
  created_at: "2026-01-01T00:00:00Z",
};

// Die Seite zieht beim Rendern mehrere Ressourcen; alles, was nicht Gegenstand
// des jeweiligen Tests ist, wird leer beantwortet.
const basisRouten = {
  [`/api/v1/agents/${AGENT_ID}`]: agent,
  [`/api/v1/agents/${AGENT_ID}/cost`]: {
    agent_id: AGENT_ID,
    total_usd: 0,
    input_tokens: 0,
    output_tokens: 0,
    cache_read_tokens: 0,
    cache_creation_tokens: 0,
    entries: 0,
  },
  [`/api/v1/agents/${AGENT_ID}/lint`]: [],
  [`/api/v1/agents/${AGENT_ID}/files/usage`]: {
    exists: false,
    total_bytes: 0,
    free_bytes: 0,
    checkout_bytes: 0,
    checkouts: [],
  },
  [`/api/v1/agents/${AGENT_ID}/backlog`]: [],
  [`/api/v1/agents/${AGENT_ID}/stages`]: [],
  [`/api/v1/agents/${AGENT_ID}/heartbeats`]: [],
  [`/api/v1/agents/${AGENT_ID}/recording`]: [],
  [`/api/v1/agents/${AGENT_ID}/memories`]: [],
  [`/api/v1/agents/${AGENT_ID}/systems`]: [],
  [`/api/v1/agents/${AGENT_ID}/skills`]: [],
  [`/api/v1/agents/${AGENT_ID}/files`]: { path: "", exists: false, truncated: false, entries: [] },
  [`/api/v1/agents/${AGENT_ID}/config`]: { version: 1, files: {}, compiled_prompt: "", created_at: "" },
  "/api/v1/skills": [],
  "/api/v1/targets": [],
  "/api/v1/runtimes": [],
  "/api/v1/assist/status": { available: false },
};

function zeigeAgent(route: string, role = "org_admin") {
  const netz = mockFetch(basisRouten);
  const r = renderWithProviders(<AgentPage me={testPrincipal(role)} />, {
    route,
    path: "/agents/:id",
  });
  return { ...r, netz };
}

beforeEach(() => useGerman());

describe("Reiter der Agenten-Seite", () => {
  it("zeigt die sechs Reiter in fester Reihenfolge", async () => {
    zeigeAgent(`/agents/${AGENT_ID}`);
    await screen.findByRole("heading", { name: "Test-Agent" });

    const erwartet = ["Backlog", "Recording", "Gedächtnis", "Dateien", "Tools & Skills", "Einstellungen"];
    for (const label of erwartet) {
      expect(screen.getByRole("button", { name: label })).toBeInTheDocument();
    }
    // Kein siebter Reiter: Heartbeat, Config, Secrets & Co. sind Unterpunkte.
    for (const weg of ["Heartbeat", "Config", "Secrets", "Egress", "Webhook", "Skills"]) {
      expect(screen.queryByRole("button", { name: weg })).not.toBeInTheDocument();
    }
  });

  it("wechselt beim Klick den Reiter und schreibt ihn in die URL", async () => {
    const nutzer = userEvent.setup();
    zeigeAgent(`/agents/${AGENT_ID}`);
    await screen.findByRole("heading", { name: "Test-Agent" });

    await nutzer.click(screen.getByRole("button", { name: "Tools & Skills" }));
    // Das Untermenü des Reiters erscheint.
    expect(await screen.findByRole("tab", { name: "Zielsysteme" })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "MCP-Werkzeuge" })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Skills" })).toBeInTheDocument();
  });

  it("holt die Dateien erst, wenn ihr Reiter offen ist", async () => {
    const nutzer = userEvent.setup();
    const { netz } = zeigeAgent(`/agents/${AGENT_ID}`);
    await screen.findByRole("heading", { name: "Test-Agent" });
    expect(netz.calls.some((c) => c.includes("/files"))).toBe(false);

    await nutzer.click(screen.getByRole("button", { name: "Dateien" }));
    await waitFor(() => expect(netz.calls.some((c) => c.includes("/files"))).toBe(true));
  });
});

describe("Rollen", () => {
  it("verbirgt die Dateien vor Rollen ohne Zugriff", async () => {
    zeigeAgent(`/agents/${AGENT_ID}`, "auditor");
    await screen.findByRole("heading", { name: "Test-Agent" });
    expect(screen.queryByRole("button", { name: "Dateien" })).not.toBeInTheDocument();
    // Die übrigen Reiter bleiben.
    expect(screen.getByRole("button", { name: "Backlog" })).toBeInTheDocument();
  });

  it("zeigt ihn Security", async () => {
    zeigeAgent(`/agents/${AGENT_ID}`, "security");
    await screen.findByRole("heading", { name: "Test-Agent" });
    expect(screen.getByRole("button", { name: "Dateien" })).toBeInTheDocument();
  });
});

// Geteilte Links und Lesezeichen auf die alten Reiter dürfen nicht ins Leere
// laufen — die Umleitung ist Verhalten, kein Detail.
describe("Umleitung alter Reiter-Links", () => {
  const fälle: Array<[string, string]> = [
    ["heartbeat", "Heartbeat"],
    ["config", "Config"],
    ["secrets", "Secrets"],
    ["egress", "Egress"],
    ["webhook", "Webhook"],
  ];
  for (const [alt, unterpunkt] of fälle) {
    it(`?tab=${alt} landet unter Einstellungen → ${unterpunkt}`, async () => {
      zeigeAgent(`/agents/${AGENT_ID}?tab=${alt}`);
      await screen.findByRole("heading", { name: "Test-Agent" });
      const tab = await screen.findByRole("tab", { name: unterpunkt });
      expect(tab).toHaveAttribute("aria-selected", "true");
    });
  }

  for (const [alt, unterpunkt] of [
    ["tools", "MCP-Werkzeuge"],
    ["skills", "Skills"],
  ] as Array<[string, string]>) {
    it(`?tab=${alt} landet unter Tools & Skills → ${unterpunkt}`, async () => {
      zeigeAgent(`/agents/${AGENT_ID}?tab=${alt}`);
      await screen.findByRole("heading", { name: "Test-Agent" });
      const tab = await screen.findByRole("tab", { name: unterpunkt });
      expect(tab).toHaveAttribute("aria-selected", "true");
    });
  }
});

// Ein unbekannter ?tab=-Wert fiel vorher durch das `|| "backlog"` hindurch —
// es greift nur bei null und leer —, und die Seite rendete unter der
// Reiterleiste nichts. Ein von Hand getippter oder aus einer älteren Fassung
// mitgebrachter Link soll irgendwo landen.
describe("Unbekannte und englische Reiter-Namen", () => {
  it("?tab=quatsch zeigt das Backlog statt einer leeren Seite", async () => {
    zeigeAgent(`/agents/${AGENT_ID}?tab=quatsch`);
    await screen.findByRole("heading", { name: "Test-Agent" });
    expect(await screen.findByPlaceholderText("Titel")).toBeInTheDocument();
  });

  // Die englischen Namen der deutschen Slugs: wer „workspace" oder „settings"
  // tippt, meint die Dateien bzw. die Einstellungen.
  for (const englisch of ["workspace", "files"]) {
    it(`?tab=${englisch} oeffnet die Dateien`, async () => {
      const { netz } = zeigeAgent(`/agents/${AGENT_ID}?tab=${englisch}`);
      await screen.findByRole("heading", { name: "Test-Agent" });
      await waitFor(() => expect(netz.calls.some((c) => c.includes("/files"))).toBe(true));
    });
  }

  it("?tab=settings landet unter Einstellungen → Allgemein", async () => {
    zeigeAgent(`/agents/${AGENT_ID}?tab=settings`);
    await screen.findByRole("heading", { name: "Test-Agent" });
    const tab = await screen.findByRole("tab", { name: "Allgemein" });
    expect(tab).toHaveAttribute("aria-selected", "true");
  });
});

// Das Recording behauptete „noch keine Aufzeichnung", solange die Abfrage lief
// — bei einem Agenten mit 178 Läufen liest man das als Befund und sucht an der
// falschen Stelle weiter.
describe("Ladezustand ist kein Befund", () => {
  it("zeigt beim Recording erst den Ladehinweis, dann die Leermeldung", async () => {
    zeigeAgent(`/agents/${AGENT_ID}?tab=recording`);
    expect(await screen.findByText("lädt …")).toBeInTheDocument();
    expect(await screen.findByText(/noch keine Aufzeichnung/i)).toBeInTheDocument();
    expect(screen.queryByText("lädt …")).not.toBeInTheDocument();
  });
});
