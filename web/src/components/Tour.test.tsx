import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import i18n from "../i18n";
import Tour, { TEAM_TOUR, tourGesehen } from "./Tour";

/* The tour (#402) points at the real thing, can be left at any step, and
   comes once: whoever skipped it or saw it through is not shown it again on
   the next page. */

beforeEach(async () => {
  localStorage.clear();
  await i18n.changeLanguage("de");
});
afterEach(cleanup);

const schritte = [{ id: "willkommen" }, { id: "team", ziel: "team" }, { id: "entscheiden", ziel: "entscheiden" }];

describe("Tour", () => {
  it("goes forward and back, and names the step it is on", () => {
    render(<Tour schritte={schritte} onEnde={() => {}} />);
    expect(screen.getByRole("dialog")).toBeTruthy();
    expect(screen.getByText("Willkommen bei covey")).toBeTruthy();
    expect(screen.getByText("1 von 3")).toBeTruthy();
    fireEvent.click(screen.getByText("Weiter"));
    expect(screen.getByText("Das Team")).toBeTruthy();
    fireEvent.click(screen.getByText("Zurück"));
    expect(screen.getByText("Willkommen bei covey")).toBeTruthy();
  });

  it("lights the target when it is on screen, and stands in the middle when it is not", () => {
    const ziel = document.createElement("button");
    ziel.setAttribute("data-tour", "team");
    ziel.getBoundingClientRect = () => ({ left: 10, top: 100, width: 40, height: 40, right: 50, bottom: 140, x: 10, y: 100, toJSON() {} });
    document.body.appendChild(ziel);
    const { container } = render(<Tour schritte={schritte} onEnde={() => {}} />);
    fireEvent.click(screen.getByText("Weiter"));
    expect(container.querySelector(".tour-licht")).toBeTruthy();
    // The decision cards only exist in an open conversation: here there are none.
    fireEvent.click(screen.getByText("Weiter"));
    expect(screen.getByText("Wenn jemand Sie braucht")).toBeTruthy();
    expect(container.querySelector(".tour-licht")).toBeNull();
    expect(container.querySelector(".tour-dunkel")).toBeTruthy();
    ziel.remove();
  });

  it("is remembered once it is skipped", () => {
    const ende = vi.fn();
    expect(tourGesehen()).toBe(false);
    render(<Tour schritte={schritte} onEnde={ende} />);
    fireEvent.click(screen.getByText("Überspringen"));
    expect(ende).toHaveBeenCalledOnce();
    expect(tourGesehen()).toBe(true);
  });

  it("is remembered once it is seen through, and Escape leaves it too", () => {
    const ende = vi.fn();
    const erste = render(<Tour schritte={schritte} onEnde={ende} />);
    fireEvent.click(screen.getByText("Weiter"));
    fireEvent.click(screen.getByText("Weiter"));
    fireEvent.click(screen.getByText("Los geht's"));
    expect(tourGesehen()).toBe(true);
    // The rail takes it away once it has ended; here the test does.
    erste.unmount();
    localStorage.clear();
    render(<Tour schritte={schritte} onEnde={ende} />);
    fireEvent.keyDown(window, { key: "Escape" });
    expect(ende).toHaveBeenCalledTimes(2);
    expect(tourGesehen()).toBe(true);
  });

  it("has a title and a text for every step of the team tour in every language", async () => {
    for (const lang of ["de", "en", "es", "fr", "it", "nl", "pl", "pt", "ja", "zh"]) {
      await i18n.changeLanguage(lang);
      for (const s of TEAM_TOUR) {
        for (const teil of ["titel", "text"]) {
          const key = `tour.${s.id}.${teil}`;
          expect(i18n.exists(key), `${lang}: ${key}`).toBe(true);
        }
      }
    }
  });
});
