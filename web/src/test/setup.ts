// Gemeinsame Vorbereitung aller Frontend-Tests.
import "@testing-library/jest-dom/vitest";
import i18n from "../i18n";
import de from "../locales/de.json";
import en from "../locales/en.json";
import { cleanup, configure } from "@testing-library/react";
import { afterEach, beforeEach, vi } from "vitest";

// Die Voreinstellung von findBy* ist 1000 ms, und das ist keine Aussage über
// die Oberfläche, sondern über die Maschine: Auf dem Entwicklungsrechner reicht
// es, auf dem CI-Runner mit vierzehn Dateien parallel nicht. Ein Test, der dort
// scheitert und hier grün ist, kostet mehr Zeit als er misst — und wer ihn dann
// „nur einmal wiederholt", hat sich die Prüfung abgewöhnt.
//
// Fünf Sekunden statt einer: Eine Oberfläche, die dann noch nichts gerendert
// hat, ist kaputt, und das soll der Test weiterhin sagen.
configure({ asyncUtilTimeout: 5000 });

// Die Anwendung lädt ihre Kataloge nach (i18n.ts) — ein Bündel je Sprache,
// damit ein Besucher nicht beide bezahlt. Im Test ist das nur im Weg: Die
// Prüfungen wechseln die Sprache mitten im Ablauf und rechnen damit, dass der
// Text sofort da ist. Hier liegen deshalb beide von Anfang an bereit.
i18n.addResourceBundle("de", "translation", de, true, true);
i18n.addResourceBundle("en", "translation", en, true, true);

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
