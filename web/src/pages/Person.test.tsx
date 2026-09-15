import { describe, it, expect, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router";
import PersonPage from "./Person";
import { mockFetch, testPrincipal, useGerman } from "../test/render";

const HUMAN_ID = "99999999-8888-7777-6666-555555555555";

beforeEach(() => useGerman());

describe("Person in einer anderen Organisation", () => {
  // The same case as the agent page (#263): signing in again returns to
  // /people/<id>, and in the organisation the new session works in that person
  // answers 404. /profile leads here too, with the id of a seat.
  it("führt bei 404 zur Startseite, ohne erneut zu fragen", async () => {
    const { calls } = mockFetch({ "/api/v1/org/chart": { humans: [], agents: [], departments: [] } });
    // The client's own default retries on purpose: the page has to override it.
    const qc = new QueryClient({ defaultOptions: { queries: { gcTime: 0 } } });
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={[`/people/${HUMAN_ID}`]}>
          <Routes>
            <Route path="/people/:id" element={<PersonPage me={testPrincipal()} />} />
            <Route path="/" element={<p>Startseite</p>} />
          </Routes>
        </MemoryRouter>
      </QueryClientProvider>,
    );

    expect(await screen.findByText("Startseite")).toBeInTheDocument();
    expect(calls.filter((c) => c === `GET /api/v1/org/humans/${HUMAN_ID}`)).toHaveLength(1);
  });
});
