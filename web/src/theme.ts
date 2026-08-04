/* Erscheinungsbild: hell, dunkel oder das, was das Betriebssystem sagt.

   Die Farben selbst stehen im Stylesheet — jedes Token trägt dort beide
   Fassungen nebeneinander (`light-dark()` in src/styles.css), und ohne weiteres
   Zutun folgt die Oberfläche der Systemeinstellung. Dieses Modul setzt nur den
   Schalter für den Fall, dass jemand ausdrücklich widerspricht: ein
   `data-theme`-Attribut am Wurzelelement, das `color-scheme` festnagelt.

   Warum kein Skript im <head>, das vor dem ersten Bild die Farbe setzt: Es
   bräuchte keins. Die Voreinstellung „System" kommt aus dem Stylesheet, ist
   also schon im ersten Rendervorgang richtig — und die eigene CSP erlaubt
   ohnehin kein Inline-Skript. Nur wer ausdrücklich gegen sein System wählt,
   sieht beim Laden kurz die Systemfassung. */

export type Theme = "system" | "light" | "dark";

export const THEME_KEY = "covey.theme";

export const THEMES: Theme[] = ["system", "light", "dark"];

function istTheme(wert: string | null): wert is Theme {
  return wert === "system" || wert === "light" || wert === "dark";
}

/* Die Abfrage hängt an window, nicht an localStorage — wie bei der Sprachwahl
   (siehe i18n.ts): Node bringt ein globales localStorage ohne Methoden mit, an
   dem das Vorrendern sonst zerbräche. Der try/catch fängt den Browser, der
   Speicher verweigert (Privatmodus, geblockte Drittanbieter-Daten). */
export function gespeichertesTheme(): Theme {
  if (typeof window === "undefined") return "system";
  try {
    const wert = window.localStorage.getItem(THEME_KEY);
    return istTheme(wert) ? wert : "system";
  } catch {
    return "system";
  }
}

export function merkeTheme(theme: Theme) {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(THEME_KEY, theme);
  } catch {
    /* Ohne Speicher bleibt die Wahl auf diese Sitzung beschränkt. */
  }
}

/* „system" heißt: kein Attribut. Dann greift `color-scheme: light dark` aus dem
   Stylesheet und der Browser entscheidet nach der Systemeinstellung — auch
   wenn sie sich während der Sitzung ändert. */
export function wendeThemeAn(theme: Theme) {
  if (typeof document === "undefined") return;
  const wurzel = document.documentElement;
  if (theme === "system") wurzel.removeAttribute("data-theme");
  else wurzel.setAttribute("data-theme", theme);
}

/* Beim Start einmal anwenden, was gespeichert ist. */
export function initTheme(): Theme {
  const theme = gespeichertesTheme();
  wendeThemeAn(theme);
  return theme;
}

/* Wer selbst zeichnet, statt CSS zeichnen zu lassen, muss den Wechsel
   mitbekommen: Ein Canvas hat die Farben beim Zeichnen aus den Tokens gelesen
   und behält sie, bis er neu zeichnet — in der falschen Fassung heißt das
   dunkle Linien auf dunklem Grund. Zwei Quellen lösen ihn aus: die
   ausdrückliche Wahl (data-theme am Wurzelelement) und, solange „System"
   gilt, die Systemeinstellung. Gibt eine Funktion zum Abbestellen zurück. */
export function beobachteTheme(beiWechsel: () => void): () => void {
  if (typeof window === "undefined") return () => {};

  const beobachter = new MutationObserver(beiWechsel);
  beobachter.observe(document.documentElement, { attributeFilter: ["data-theme"] });

  const abfrage = window.matchMedia("(prefers-color-scheme: dark)");
  abfrage.addEventListener("change", beiWechsel);

  return () => {
    beobachter.disconnect();
    abfrage.removeEventListener("change", beiWechsel);
  };
}
