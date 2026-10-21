import { beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, screen, waitFor } from "@testing-library/react";
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
  it("schickt eine angehängte Datei mit der Nachricht ab (#647)", async () => {
    mockFetch({
      [`/api/v1/agents/${AGENT}/thread`]: { entries: [], pending: false, tasks: [], marks: {} },
      [`/api/v1/agents/${AGENT}`]: { id: AGENT, slug: "ada", display_name: "Ada", status: "sleeping" },
      [`POST /api/v1/agents/${AGENT}/messages`]: { message: { id: "m1" } },
    });
    const { container } = renderWithProviders(<Thread agentId={AGENT} me={testPrincipal()} />);
    await screen.findByText(/Noch nichts mit Ada besprochen/);
    const datei = new File(["id,title\n1,Login"], "export.csv", { type: "text/csv" });
    fireEvent.change(container.querySelector('input[type="file"]')!, { target: { files: [datei] } });
    fireEvent.change(container.querySelector(".tm-eingabe textarea")!, { target: { value: "Hier der Export." } });
    fireEvent.click(screen.getByRole("button", { name: "Senden" }));

    const fetchMock = vi.mocked(fetch);
    const gesendet = () => fetchMock.mock.calls.find(([url, init]) => String(url).endsWith("/messages") && init?.method === "POST");
    await waitFor(() => expect(gesendet()).toBeDefined());
    const [, init] = gesendet()!;
    expect(init?.body).toBeInstanceOf(FormData);
    const form = init!.body as FormData;
    expect(form.get("text")).toBe("Hier der Export.");
    expect((form.get("file") as File).name).toBe("export.csv");
  });
  it("nimmt eine gewählte Datei auch an, wenn React die Änderung später ausführt (#652)", async () => {
    mockFetch({
      [`/api/v1/agents/${AGENT}/thread`]: { entries: [], pending: false, tasks: [], marks: {} },
      [`/api/v1/agents/${AGENT}`]: { id: AGENT, slug: "ada", display_name: "Ada", status: "sleeping" },
    });
    const { container } = renderWithProviders(<Thread agentId={AGENT} me={testPrincipal()} />);
    await screen.findByText(/Noch nichts mit Ada besprochen/);
    const eingabe = container.querySelector('input[type="file"]') as HTMLInputElement;
    // As in a browser: clearing the value empties the file list.
    const datei = new File(["a,b"], "export.csv", { type: "text/csv" });
    let geleert = false;
    Object.defineProperty(eingabe, "files", { configurable: true, get: () => (geleert ? [] : [datei]) });
    Object.defineProperty(eingabe, "value", { configurable: true, get: () => "", set: () => { geleert = true; } });
    // A state update already pending in the same batch: React then runs the
    // file updater later instead of at once — the page's normal case.
    act(() => {
      fireEvent.change(container.querySelector(".tm-eingabe textarea")!, { target: { value: "Hier der Export." } });
      fireEvent.change(eingabe);
    });
    expect(await screen.findByText("export.csv")).toBeInTheDocument();
  });
});
