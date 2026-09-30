import { beforeEach, describe, expect, it } from "vitest";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import Vorschlag from "./Vorschlag";
import Thread from "./Thread";
import type { ProposalCard } from "../api";
import { mockFetch, renderWithProviders, testPrincipal, useGerman } from "../test/render";

beforeEach(() => useGerman());

const karte = (x: Partial<ProposalCard> = {}): ProposalCard => ({
  id: "p1",
  status: "pending",
  title: "Postfach nur noch alle zwei Stunden",
  rationale: "Statt halbstündlich alle zwei Stunden, wie im Chat gewünscht.",
  diff: [{ file: "HEARTBEAT.md", before: "- alle: 30m titel: Postfach", after: "- alle: 2h titel: Postfach" }],
  can_decide: true,
  can_accept: true,
  approvers: ["Bernd Brot", "Carla Code"],
  ...x,
});

/* A configuration change drafted from the chat (#491): the card shows what
   changes and why, lets whoever may decide accept or decline it on the
   agent page's path, tells everybody else whom it waits for, and says who
   decided and when. */
describe("Vorschlag", () => {
  it("zeigt den Diff je Datei und nimmt über den Weg der Agentenseite an", async () => {
    const { calls } = mockFetch({ "POST /api/v1/improvements/p1/decide": { id: "p1", status: "accepted" } });
    renderWithProviders(<Vorschlag card={karte()} />);

    expect(screen.getByText("Postfach nur noch alle zwei Stunden")).toBeInTheDocument();
    expect(screen.getByText(/wie im Chat gewünscht/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /HEARTBEAT\.md/ }));
    expect(screen.getByText(/alle: 2h/)).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Annehmen" }));
    await waitFor(() => expect(calls).toContain("POST /api/v1/improvements/p1/decide"));
  });

  it("sagt, wenn ein Vorschlag Zugänge erweitert, und sperrt das Annehmen, wo es nicht darf", () => {
    mockFetch({});
    renderWithProviders(<Vorschlag card={karte({ widens: ["ACCESS.md"], can_accept: false })} />);
    expect(screen.getByText(/Erweitert Zugänge \(ACCESS\.md\)/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Annehmen" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Ablehnen" })).toBeEnabled();
  });

  it("zeigt wer freigeben muss, wenn man selbst nicht entscheiden darf", () => {
    mockFetch({});
    renderWithProviders(<Vorschlag card={karte({ can_decide: false, can_accept: false })} />);
    expect(screen.getByText("Wartet auf Freigabe durch Bernd Brot, Carla Code.")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Annehmen" })).toBeNull();
  });

  it("sagt nach der Entscheidung, wer wann entschieden hat", () => {
    mockFetch({});
    renderWithProviders(
      <Vorschlag card={karte({ status: "rejected", can_decide: false, decided_by: "Bernd Brot", decided_at: "2026-09-30T10:00:00Z" })} />,
    );
    expect(screen.getByText(/Abgelehnt von Bernd Brot/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Ablehnen" })).toBeNull();
  });
});

const AGENT = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa";

describe("Thread mit Vorschlag", () => {
  it("zeigt den Vorschlag als Karte im Verlauf statt als Text", async () => {
    mockFetch({
      [`/api/v1/agents/${AGENT}/thread`]: {
        entries: [
          {
            kind: "config_proposal",
            id: "m1",
            task_title: "",
            task_state: "",
            author: "agent",
            text: "Postfach nur noch alle zwei Stunden\n\nFALLBACK",
            at: "2026-09-30T10:00:00Z",
            proposal: karte(),
          },
        ],
        pending: false,
        tasks: [],
        marks: {},
      },
      [`/api/v1/agents/${AGENT}`]: { id: AGENT, slug: "demo", display_name: "Demo", status: "sleeping" },
      "/api/v1/inbox": { items: [] },
    });
    renderWithProviders(<Thread agentId={AGENT} me={testPrincipal()} />);
    expect(await screen.findByText("Konfigurationsvorschlag")).toBeInTheDocument();
    expect(screen.queryByText(/FALLBACK/)).toBeNull();
    expect(screen.getByRole("button", { name: "Annehmen" })).toBeInTheDocument();
  });
});
