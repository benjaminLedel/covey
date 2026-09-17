// Shared setup for all frontend tests.
import "@testing-library/jest-dom/vitest";
import i18n from "../i18n";
import de from "../locales/de.json";
import en from "../locales/en.json";
import { cleanup, configure } from "@testing-library/react";
import { afterEach, beforeEach, vi } from "vitest";

// The default for findBy* is 1000 ms, and that is no statement about the UI,
// only about the machine: on the dev laptop it is enough, on the CI runner
// with fourteen files in parallel it is not. A test that fails there and is
// green here costs more time than it measures — and whoever then "re-runs it
// once" has stopped checking.
//
// Five seconds instead of one: a UI that has rendered nothing by then is
// broken, and the test should still say that.
configure({ asyncUtilTimeout: 5000 });

// The app loads its catalogues lazily (i18n.ts) — one bundle per language, so
// that a visitor does not pay for both. In a test this only gets in the way:
// tests switch the language mid-run and expect the text to be there right
// away. So both are in place from the start here.
i18n.addResourceBundle("de", "translation", de, true, true);
i18n.addResourceBundle("en", "translation", en, true, true);

// localStorage: Node meanwhile ships its own half-finished version, which
// shadows the jsdom one here — on the first import of i18n.ts it would
// otherwise strike with `localStorage.getItem is not a function`. A simple
// store in memory is the right thing for tests anyway: it starts empty before
// each test, instead of carrying state from one test into the next.
class SpeicherImRAM implements Storage {
  private daten = new Map<string, string>();
  get length() {
    return this.daten.size;
  }
  clear() {
    this.daten.clear();
  }
  getItem(k: string) {
    return this.daten.has(k) ? this.daten.get(k)! : null;
  }
  key(i: number) {
    return [...this.daten.keys()][i] ?? null;
  }
  removeItem(k: string) {
    this.daten.delete(k);
  }
  setItem(k: string, v: string) {
    this.daten.set(k, String(v));
  }
}

const speicher = new SpeicherImRAM();
Object.defineProperty(globalThis, "localStorage", { value: speicher, writable: true });
Object.defineProperty(globalThis, "sessionStorage", { value: new SpeicherImRAM(), writable: true });

beforeEach(() => speicher.clear());

// Clear the DOM after every test — otherwise the next test finds the nodes of
// the previous one and asserts over a corpse.
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
