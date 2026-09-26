import { beforeEach, describe, expect, it } from "vitest";
import { fireEvent, screen } from "@testing-library/react";
import Thread from "./Thread";
import { mockFetch, renderWithProviders, testPrincipal, useGerman } from "../test/render";

beforeEach(() => useGerman());

const AGENT = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa";

/* A told result (#411): the conversation shows what the agent said about the
   run, and the report the run wrote stays one click away. */
describe("Thread", () => {
  it("zeigt, was der Agent zum Ergebnis sagt, und den Bericht auf Klick", async () => {
    mockFetch({
      [`/api/v1/agents/${AGENT}/thread`]: {
        entries: [
          {
            kind: "result",
            id: "t1",
            task_id: "t1",
            task_title: "Rechnung prüfen",
            task_state: "done",
            author: "agent",
            text: "## Ergebnis\n- Rechnung 4711 doppelt gebucht",
            at: "2026-09-26T10:00:00Z",
            said: "Hab nachgesehen: Die Rechnung war doppelt gebucht.",
          },
        ],
        pending: false,
        tasks: [],
        marks: {},
      },
      [`/api/v1/agents/${AGENT}`]: { id: AGENT, slug: "ada", display_name: "Ada", status: "sleeping" },
    });
    renderWithProviders(<Thread agentId={AGENT} me={testPrincipal()} />);

    expect(await screen.findByText("Hab nachgesehen: Die Rechnung war doppelt gebucht.")).toBeInTheDocument();
    const bericht = screen.getByText("Ganzer Bericht");
    const details = bericht.closest("details")!;
    expect(details.open).toBe(false);
    fireEvent.click(bericht);
    expect(details.open).toBe(true);
    expect(screen.getByText("Rechnung 4711 doppelt gebucht")).toBeInTheDocument();
  });

  it("nimmt bei einem gestoppten Agenten keine Nachricht an und sagt warum (#414)", async () => {
    mockFetch({
      [`/api/v1/agents/${AGENT}/thread`]: { entries: [], pending: false, tasks: [], marks: {} },
      [`/api/v1/agents/${AGENT}`]: { id: AGENT, slug: "ada", display_name: "Ada", status: "idle", killed: true },
    });
    const { container } = renderWithProviders(<Thread agentId={AGENT} me={testPrincipal()} />);
    expect(await screen.findByText(/Ada ist gestoppt/)).toBeInTheDocument();
    expect((container.querySelector(".tm-eingabe") as HTMLElement).hidden).toBe(true);
  });
});
