import { describe, it, expect, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import Notes from "./Notes";
import { Markdown } from "../components/Markdown";
import { mockFetch, useGerman } from "../test/render";

beforeEach(() => useGerman());

const renderNotes = () =>
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })}>
      <Notes />
    </QueryClientProvider>,
  );

describe("Notizen im Web (#342)", () => {
  it("zeigt die eigenen Notizen und öffnet eine mit Zusammenfassung", async () => {
    mockFetch({
      "/api/v1/me/notes": {
        summarize: true,
        notes: [
          {
            id: "n1",
            kind: "meeting",
            title: "Weekly",
            body: "Ada: Angebot bis Freitag.",
            summary: "## Aufgaben\n- [ ] Ada schickt das Angebot",
            duration_seconds: 1805,
            created_at: new Date().toISOString(),
            updated_at: new Date().toISOString(),
          },
        ],
      },
    });
    renderNotes();

    fireEvent.click(await screen.findByText("Weekly"));
    expect(screen.getByText("Ada schickt das Angebot")).toBeInTheDocument();
    expect(screen.getByRole("checkbox")).not.toBeChecked();
    expect(screen.getByText("Neu zusammenfassen")).toBeInTheDocument();
    expect(screen.getByText("Ada: Angebot bis Freitag.")).toBeInTheDocument();
  });

  it("sagt, dass Aufnahmen in der App entstehen", async () => {
    mockFetch({ "/api/v1/me/notes": { summarize: false, notes: [] } });
    renderNotes();
    expect(await screen.findByText(/Noch keine Notizen/)).toBeInTheDocument();
    expect(screen.getByText(/in der covey-App auf/)).toBeInTheDocument();
  });
});

describe("Markdown-Aufgabenlisten", () => {
  it("rendert - [ ] und - [x] als Kästchen statt als Text", () => {
    render(<Markdown text={"- [ ] offen\n- [x] erledigt\n- normal"} />);
    const boxes = screen.getAllByRole("checkbox");
    expect(boxes).toHaveLength(2);
    expect(boxes[1]).toBeChecked();
    expect(screen.queryByText(/\[ \]/)).not.toBeInTheDocument();
    expect(screen.getByText("normal")).toBeInTheDocument();
  });
});
