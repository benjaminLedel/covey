import { describe, it, expect, beforeEach } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import Chat from "./Chat";
import { mockFetch, renderWithProviders, testPrincipal, useGerman } from "../test/render";
import type { Agent, ChatEntry } from "../api";

/* What the chat has to get right is one decision: where the typed text goes.

   Normally it opens a task. But when the agent stands still and waits, the
   same field has to answer THAT task — otherwise a new piece of work is
   created next to one that is parked, and the agent goes on waiting. That is
   the whole difference between an inbound channel and a second backlog. */

const agent = (over: Partial<Agent> = {}) =>
  ({
    id: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
    slug: "ada",
    display_name: "Ada",
    job_title: "Support",
    status: "hired",
  }) as Agent;

const nachricht: ChatEntry = {
  kind: "message",
  task_id: "11111111-0000-0000-0000-000000000001",
  task_title: "Rechnung von Globex prüfen",
  task_state: "blocked",
  author: "chat:test@covey.local",
  text: "Rechnung von Globex prüfen",
  at: "2026-01-02T10:00:00Z",
};

const frage: ChatEntry = {
  kind: "question",
  task_id: "11111111-0000-0000-0000-000000000001",
  task_title: "Rechnung von Globex prüfen",
  task_state: "blocked",
  author: "agent",
  text: "Darf ich Globex direkt antworten?",
  at: "2026-01-02T10:05:00Z",
};

describe("Chat", () => {
  beforeEach(() => useGerman());

  it("macht aus einer Nachricht eine Aufgabe", async () => {
    const { calls } = mockFetch({
      "/agents": [agent()],
      [`/agents/${agent().id}/thread`]: { entries: [] },
      [`POST /agents/${agent().id}/messages`]: { id: "neu" },
    });
    renderWithProviders(<Chat me={testPrincipal()} />);

    await screen.findByText("Ada");
    await userEvent.type(
      await screen.findByLabelText("Was soll erledigt werden?"),
      "Bitte die Rechnung prüfen",
    );
    await userEvent.click(screen.getByRole("button", { name: "Senden" }));

    await waitFor(() =>
      expect(calls.some((c) => c.startsWith(`POST /api/v1/agents/${agent().id}/messages`))).toBe(true),
    );
  });

  it("antwortet der wartenden Aufgabe statt eine neue anzulegen", async () => {
    const { calls } = mockFetch({
      "/agents": [agent()],
      [`/agents/${agent().id}/thread`]: { entries: [nachricht, frage] },
      [`POST /tasks/${frage.task_id}/reply`]: { woken: true, note: {} },
    });
    renderWithProviders(<Chat me={testPrincipal()} />);

    // Die Frage steht im Verlauf, und das Feld sagt, wem es antwortet.
    await screen.findByText("Darf ich Globex direkt antworten?");
    expect(screen.getByText(/Antwort auf: Rechnung von Globex prüfen/)).toBeTruthy();

    await userEvent.type(screen.getByLabelText("Was soll erledigt werden?"), "Ja, bitte.");
    await userEvent.click(screen.getByRole("button", { name: "Antworten" }));

    await waitFor(() =>
      expect(calls.some((c) => c === `POST /api/v1/tasks/${frage.task_id}/reply`)).toBe(true),
    );
    // Und eben KEINE neue Aufgabe daneben.
    expect(calls.some((c) => c.includes("/messages"))).toBe(false);
  });

  it("lässt eine lesende Rolle mitlesen, aber nicht schreiben", async () => {
    mockFetch({
      "/agents": [agent()],
      [`/agents/${agent().id}/thread`]: { entries: [nachricht] },
    });
    renderWithProviders(<Chat me={testPrincipal("auditor")} />);

    // Zweimal im Bild: als Titel der Aufgabe und als Text der Nachricht.
    expect((await screen.findAllByText("Rechnung von Globex prüfen")).length).toBeGreaterThan(0);
    expect(screen.getByLabelText("Was soll erledigt werden?").hasAttribute("disabled")).toBe(true);
  });
});
