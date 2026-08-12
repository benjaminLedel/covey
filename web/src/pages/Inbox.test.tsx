import { describe, it, expect, beforeEach } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import Inbox from "./Inbox";
import { mockFetch, renderWithProviders, testPrincipal, useGerman } from "../test/render";
import type { ImprovementItem, InboxEntry } from "../api";

/* Der Posteingang trägt zwei Sorten, die sich nicht gleich anfühlen dürfen:
   bei einer Freigabe wartet ein Agent, bei einem offenen Punkt wartet niemand.
   Was die Tests festhalten, ist genau das — die Reihenfolge oben, die Trennung
   unten und die Rollengrenze, die an den Dateien eines Vorschlags hängt. */

const vorschlag: ImprovementItem = {
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
};

const eintragVorschlag = (over: Partial<ImprovementItem> = {}): InboxEntry => ({
  type: "proposal",
  id: vorschlag.id,
  agent_id: vorschlag.agent_id,
  agent_slug: "qa",
  agent_name: "QA-Agent",
  title: vorschlag.title,
  status: over.status ?? "pending",
  pending: (over.status ?? "pending") === "pending",
  created_at: vorschlag.created_at,
  item: { ...vorschlag, ...over },
});

const eintragFreigabe = (): InboxEntry => ({
  type: "approval",
  id: "22222222-0000-0000-0000-000000000002",
  agent_id: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
  agent_slug: "qa",
  agent_name: "QA-Agent",
  title: "zammad:reply_external",
  status: "pending",
  pending: true,
  created_at: "2026-01-01T09:00:00Z",
  approval: {
    id: "22222222-0000-0000-0000-000000000002",
    agent_id: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
    action: "zammad:reply_external",
    params: { ticket: 4711 },
    status: "pending",
    requested_at: "2026-01-01T09:00:00Z",
  },
});

// Der Server liefert je Abfrage eine Seite. Der Test antwortet nach dem
// type-Parameter, damit die Gruppen unten dasselbe zeigen wie in echt.
const seiten = (items: InboxEntry[]) => {
  const von = (typ?: string) => {
    const gefiltert = typ ? items.filter((i) => i.type === typ) : items;
    return { items: gefiltert, total: gefiltert.length, pending: gefiltert.filter((i) => i.pending).length };
  };
  return {
    "/api/v1/agents": [],
    "/api/v1/inbox?type=approval": von("approval"),
    "/api/v1/inbox?type=proposal": von("proposal"),
    "/api/v1/inbox?type=finding": von("finding"),
    "/api/v1/inbox?type=issue": von("issue"),
    "/api/v1/inbox": von(),
  };
};

describe("Posteingang", () => {
  beforeEach(() => useGerman());

  it("stellt oben, was zu entscheiden ist — die Freigabe zuerst", async () => {
    // Die Reihenfolge kommt vom Server (sort=urgent); der Test hält fest, dass
    // die Seite sie nicht wieder umsortiert.
    mockFetch(seiten([eintragFreigabe(), eintragVorschlag()]));
    renderWithProviders(<Inbox me={testPrincipal()} />);

    // Der Kopf ist statisch — gewartet wird auf die Karten, nicht auf ihn.
    await waitFor(() => expect(document.querySelectorAll(".card").length).toBe(2));
    const karten = document.querySelectorAll(".card");
    expect(within(karten[0] as HTMLElement).getByText("Freigabe")).toBeInTheDocument();
    expect(within(karten[1] as HTMLElement).getByText("Vorschlag")).toBeInTheDocument();
  });

  it("sagt bei einer Freigabe, dass ein Agent wartet — beim Vorschlag nicht", async () => {
    mockFetch(seiten([eintragFreigabe()]));
    renderWithProviders(<Inbox me={testPrincipal()} />);
    expect(await screen.findByText(/Aufgabe steht still/)).toBeInTheDocument();
    expect(await screen.findByRole("button", { name: "Freigeben" })).toBeEnabled();
  });

  it("gruppiert die Auflistung nach Sorte", async () => {
    mockFetch(seiten([eintragFreigabe(), eintragVorschlag()]));
    renderWithProviders(<Inbox me={testPrincipal()} />);

    await screen.findByText("Alle Vorgänge");
    // Je Sorte eine Überschrift mit der Zahl aus dem Bestand des Servers.
    expect(await screen.findByRole("heading", { name: /Freigabe \(1\)/ })).toBeInTheDocument();
    expect(await screen.findByRole("heading", { name: /Vorschlag \(1\)/ })).toBeInTheDocument();
    // Und keine leere Gruppe.
    expect(screen.queryByRole("heading", { name: /Befund/ })).not.toBeInTheDocument();
  });

  it("lässt den Teamleiter einen Vorschlag auf ACCESS.md nicht annehmen", async () => {
    mockFetch(seiten([eintragVorschlag({ needs_security: true })]));
    renderWithProviders(<Inbox me={testPrincipal("agent_owner")} />);

    expect(await screen.findByText(/Security entscheidet/)).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: "Annehmen" })[0]).toBeDisabled();
    // Ablehnen nimmt nichts weg und bleibt offen.
    expect(screen.getAllByRole("button", { name: "Ablehnen" })[0]).toBeEnabled();
  });

  it("sperrt die Annahme, solange dieselbe Datei fremd geändert wurde", async () => {
    mockFetch(seiten([eintragVorschlag({ stale: true, current_version: 9, conflicts: ["PLAYBOOKS.md"] })]));
    renderWithProviders(<Inbox me={testPrincipal()} />);

    expect(await screen.findByText(/Konflikt: PLAYBOOKS.md/)).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: "Annehmen" })[0]).toBeDisabled();
  });

  it("zeigt den Diff erst auf Klick", async () => {
    mockFetch(seiten([eintragVorschlag()]));
    renderWithProviders(<Inbox me={testPrincipal()} />);

    const toggle = (await screen.findAllByRole("button", { name: /Änderung ansehen/ }))[0];
    expect(screen.queryByText(/− ## Vorgehen/)).not.toBeInTheDocument();
    await userEvent.click(toggle);
    expect(screen.getByText(/− ## Vorgehen/)).toBeInTheDocument();
  });

  it("schickt die Entscheidung mit der Notiz an den Server", async () => {
    const { calls } = mockFetch({
      ...seiten([eintragVorschlag()]),
      [`POST /api/v1/improvements/${vorschlag.id}/decide`]: vorschlag,
    });
    renderWithProviders(<Inbox me={testPrincipal()} />);

    await userEvent.type((await screen.findAllByPlaceholderText(/Notiz/))[0], "Gute Beobachtung.");
    await userEvent.click(screen.getAllByRole("button", { name: "Annehmen" })[0]);
    await waitFor(() => expect(calls.some((c) => c.startsWith("POST") && c.includes("/decide"))).toBe(true));
  });

  it("hält Controlling von den Arbeitsakten fern und zeigt ihm die Freigaben", async () => {
    mockFetch(seiten([eintragFreigabe()]));
    renderWithProviders(<Inbox me={testPrincipal("controlling")} />);

    expect(await screen.findByRole("heading", { name: /Freigabe \(1\)/ })).toBeInTheDocument();
    // Die Sorten der Arbeitsakte werden gar nicht erst abgefragt.
    expect(screen.queryByRole("heading", { name: /Vorschlag/ })).not.toBeInTheDocument();
  });
});
