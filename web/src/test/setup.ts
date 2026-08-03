// Gemeinsame Vorbereitung aller Frontend-Tests.
import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";
import { afterEach, beforeEach, vi } from "vitest";

// localStorage: Node bringt inzwischen eine eigene, halbfertige Fassung mit,
// die die von jsdom hier verdeckt — beim ersten Import von i18n.ts schlägt
// sonst `localStorage.getItem is not a function` zu. Ein simpler Speicher im
// Arbeitsspeicher ist für Tests ohnehin das Richtige: Er startet vor jedem
// Test leer, statt Zustand von einem Test in den nächsten zu tragen.
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

// Nach jedem Test das DOM abräumen — sonst findet der nächste Test die
// Knoten des vorherigen und prüft an einer Leiche.
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
