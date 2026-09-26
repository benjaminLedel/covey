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
    expect(screen.getByDisplayValue("Weekly")).toBeInTheDocument();
    // The transcript is the page's body, as blocks (#386).
    expect(screen.getByText("Ada: Angebot bis Freitag.")).toBeInTheDocument();
    // Summarising sits in the page's menu.
    fireEvent.click(screen.getByLabelText("Weitere Aktionen"));
    expect(screen.getByText("Neu zusammenfassen")).toBeInTheDocument();
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

describe("Bilder in Notizen (#344)", () => {
  it("zeigt covey-Bilder über den eigenen Endpunkt und lädt keine fremden", async () => {
    const id = "11111111-2222-3333-4444-555555555555";
    mockFetch({
      "/api/v1/me/notes": {
        summarize: false,
        notes: [
          {
            id: "n1",
            kind: "text",
            title: "Tafel",
            body: `Vorher\n![Tafel](covey-media://${id})\n![](https://tracker.example/p.gif)\n> ein Zitat\n---`,
            summary: "",
            duration_seconds: 0,
            created_at: new Date().toISOString(),
            updated_at: new Date().toISOString(),
          },
        ],
      },
    });
    renderNotes();
    fireEvent.click(await screen.findByText("Tafel"));
    // Only the note's own picture is loaded; a foreign one stays its address.
    const imgs = document.querySelectorAll("img");
    expect(imgs).toHaveLength(1);
    expect(imgs[0].getAttribute("src")).toBe(`/api/v1/me/notes/media/${id}`);
    expect(screen.getByText("https://tracker.example/p.gif")).toBeInTheDocument();
    expect(screen.getByText("ein Zitat").closest(".nb-quote")).not.toBeNull();
  });

  it("lässt Bilder als Text, wo keine Seite sie erlaubt", () => {
    render(<Markdown text={"![x](https://tracker.example/p.gif)"} />);
    expect(screen.queryByRole("img")).not.toBeInTheDocument();
  });
});
