/* Appearance: light, dark or what the operating system says.

   The colors themselves stand in the stylesheet — every token carries both
   variants side by side there (`light-dark()` in src/styles.css), and the
   interface follows the system setting without further help. This module only
   sets the switch for the case where someone explicitly disagrees: a
   `data-theme` attribute on the root element that pins `color-scheme`.

   Why no script in <head> that sets the color before the first paint: none is
   needed. The "System" default comes from the stylesheet, so it is already
   right in the first render pass — and our own CSP allows no inline script
   anyway. Only whoever explicitly chooses against their system briefly sees
   the system variant while loading. */

export type Theme = "system" | "light" | "dark";

export const THEME_KEY = "covey.theme";

export const THEMES: Theme[] = ["system", "light", "dark"];

function istTheme(wert: string | null): wert is Theme {
  return wert === "system" || wert === "light" || wert === "dark";
}

/* The query hangs on window, not on localStorage — as with the language choice
   (see i18n.ts): Node brings a global localStorage without methods, on which
   the prerender would otherwise break. The try/catch covers the browser that
   refuses storage (private mode, blocked third-party data). */
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
    /* Without storage the choice stays confined to this session. */
  }
}

/* "system" means: no attribute. Then `color-scheme: light dark` from the
   stylesheet takes hold and the browser decides by the system setting — also
   when it changes during the session. */
export function wendeThemeAn(theme: Theme) {
  if (typeof document === "undefined") return;
  const wurzel = document.documentElement;
  if (theme === "system") wurzel.removeAttribute("data-theme");
  else wurzel.setAttribute("data-theme", theme);
}

/* On startup apply once what is stored. */
export function initTheme(): Theme {
  const theme = gespeichertesTheme();
  wendeThemeAn(theme);
  return theme;
}

/* Whoever draws themselves instead of letting CSS draw has to notice the
   change: a canvas read its colors from the tokens while drawing and keeps
   them until it draws again — in the wrong variant that means dark lines on a
   dark ground. Two sources trigger it: the explicit choice (data-theme on the
   root element) and, while "System" holds, the system setting. Returns a
   function to unsubscribe. */
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
