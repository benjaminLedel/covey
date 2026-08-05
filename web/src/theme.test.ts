import { describe, expect, it, beforeEach } from "vitest";
import { THEME_KEY, gespeichertesTheme, initTheme, merkeTheme, wendeThemeAn } from "./theme";

/* Die Farben selbst gehören dem Stylesheet (light-dark() in styles.css) und
   sind hier nicht prüfbar — jsdom rechnet sie nicht aus. Prüfbar ist der
   Schalter davor: Was wird gemerkt, und was steht danach am Wurzelelement. */

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
    // „System" heißt: kein Attribut — dann entscheidet color-scheme im
    // Stylesheet, und ein Wechsel der Systemeinstellung schlägt weiter durch.
    wendeThemeAn("system");
    expect(document.documentElement.hasAttribute("data-theme")).toBe(false);
  });

  it("wendet beim Start die gespeicherte Wahl an", () => {
    localStorage.setItem(THEME_KEY, "light");
    expect(initTheme()).toBe("light");
    expect(document.documentElement.getAttribute("data-theme")).toBe("light");
  });
});
