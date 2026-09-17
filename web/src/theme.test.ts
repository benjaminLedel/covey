import { describe, expect, it, beforeEach } from "vitest";
import { THEME_KEY, gespeichertesTheme, initTheme, merkeTheme, wendeThemeAn } from "./theme";

/* The colors themselves belong to the stylesheet (light-dark() in styles.css)
   and are not testable here — jsdom does not compute them. Testable is the
   switch before them: what gets stored, and what then stands on the root element. */

describe("theme", () => {
  beforeEach(() => {
    localStorage.clear();
    document.documentElement.removeAttribute("data-theme");
  });

  it("ohne Wahl gilt System", () => {
    expect(gespeichertesTheme()).toBe("system");
  });

  it("unbekannter Wert im Speicher gilt als keine Wahl", () => {
    localStorage.setItem(THEME_KEY, "sepia");
    expect(gespeichertesTheme()).toBe("system");
  });

  it("merkt die Wahl und liest sie zurück", () => {
    merkeTheme("dark");
    expect(localStorage.getItem(THEME_KEY)).toBe("dark");
    expect(gespeichertesTheme()).toBe("dark");
  });

  it("setzt data-theme nur bei ausdrücklicher Wahl", () => {
    wendeThemeAn("dark");
    expect(document.documentElement.getAttribute("data-theme")).toBe("dark");
    wendeThemeAn("light");
    expect(document.documentElement.getAttribute("data-theme")).toBe("light");
    // "system" means: no attribute — then color-scheme in the stylesheet
    // decides, and a change of the system setting keeps breaking through.
    wendeThemeAn("system");
    expect(document.documentElement.hasAttribute("data-theme")).toBe(false);
  });

  it("wendet beim Start die gespeicherte Wahl an", () => {
    localStorage.setItem(THEME_KEY, "light");
    expect(initTheme()).toBe("light");
    expect(document.documentElement.getAttribute("data-theme")).toBe("light");
  });
});
