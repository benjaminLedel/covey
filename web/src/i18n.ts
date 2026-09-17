import i18n from "i18next";
import { initReactI18next } from "react-i18next";

import { BASE_LANG, LANG_BY_CODE, LANGS, istLang, langPrefix, type Lang } from "./langs";
import { matchRoute } from "./public/routes";

export type { Lang };
export { LANG_BY_CODE, LANG_LIST, LANGS, istLang } from "./langs";

export const LANG_KEY = "covey.lang";

/* Start language. Two cases:

   - Before sign-in the address decides: /fr/connexion is an address that
     someone can share and link to, and the proxy forwards it separately.
     Whoever opens it should see French, even if German was chosen here
     once.
   - Otherwise the stored choice counts, and where there is none, the
     language of the browser.

   Until #130 a third case stood here: a prerendered page had to hit the same
   text on the first render pass that the server had written. The website
   moved out, the application starts empty (main.tsx). */
export function langFromPath(pathname: string): Lang | null {
  /* First the addresses themselves, then their prefixes. German carries none
     (langPrefix returns "", langs.ts), and by the prefix alone /anmelden would
     never have been German for this function — the oldest address of the
     application would have been the only one not to decide its own language,
     while the tab above it already said `Anmelden — covey`. */
  const treffer = matchRoute(pathname);
  if (treffer) return treffer.lang;

  for (const lang of LANGS) {
    const prefix = langPrefix(lang);
    if (prefix && (pathname === prefix || pathname.startsWith(prefix + "/"))) return lang;
  }
  return null;
}

export function initialLang(pathname?: string): Lang {
  const path =
    pathname ?? (typeof window === "undefined" ? "/" : window.location.pathname);
  const fromPath = langFromPath(path);
  if (fromPath) return fromPath;

  const gespeichert = gespeicherteSprache();
  if (istLang(gespeichert)) return gespeichert;

  return sprachePerBrowser() ?? BASE_LANG;
}

/* Whoever chose nothing gets what their browser asks for — as long as we have
   the language. The header of a browser is a list with regions ("de-AT",
   "pt-BR"); the part before the dash is what interests us, because our
   catalogues are cut by language, not by country. If nothing is left, the base
   language applies: an interface in a language that nobody chose would be
   worse than one in a language that everyone can read. */
function sprachePerBrowser(): Lang | null {
  if (typeof navigator === "undefined") return null;
  const wuensche = navigator.languages?.length ? navigator.languages : [navigator.language];
  for (const wunsch of wuensche) {
    const basis = (wunsch || "").toLowerCase().split("-")[0];
    if (istLang(basis)) return basis;
  }
  return null;
}

/* The query hangs on window, not on localStorage: Node 25 ships a global
   localStorage with no methods without --experimental-webstorage — the check
   "is it defined" would pass there and the test run would break. The
   try/catch also catches the browser that refuses storage (private mode,
   blocked third-party data). */
export function gespeicherteSprache(): string | null {
  if (typeof window === "undefined") return null;
  try {
    return window.localStorage.getItem(LANG_KEY);
  } catch {
    return null;
  }
}

export function merkeSprache(lang: Lang) {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(LANG_KEY, lang);
  } catch {
    /* Without storage the choice stays limited to this session. */
  }
}

/* The catalogues are loaded afterwards, not shipped along. All ten of them are
   over a megabyte — a multiple of the bundle, and nine tenths of that in
   languages this visitor does not read. As a dynamic import each becomes its
   own chunk, and loaded is the one that is needed (#122).

   The caller waits for it before it renders: with an empty catalogue the
   screen would show the keys instead of the sentences. */
const kataloge: Record<Lang, () => Promise<{ default: Record<string, unknown> }>> = {
  de: () => import("./locales/de.json"),
  en: () => import("./locales/en.json"),
  es: () => import("./locales/es.json"),
  fr: () => import("./locales/fr.json"),
  it: () => import("./locales/it.json"),
  nl: () => import("./locales/nl.json"),
  pl: () => import("./locales/pl.json"),
  pt: () => import("./locales/pt.json"),
  ja: () => import("./locales/ja.json"),
  zh: () => import("./locales/zh.json"),
};

export async function ladeSprache(lang: Lang): Promise<void> {
  if (!i18n.hasResourceBundle(lang, "translation")) {
    const { default: katalog } = await kataloge[lang]();
    i18n.addResourceBundle(lang, "translation", katalog, true, true);
  }
  if (i18n.language !== lang) await i18n.changeLanguage(lang);
  setzeDokumentSprache(lang);
}

/* The lang attribute on <html> is no cosmetics: screen readers pick their
   pronunciation by it, and the browser its hyphenation. It stands "de" in the
   markup and has to travel along when the language changes. */
function setzeDokumentSprache(lang: Lang) {
  if (typeof document === "undefined") return;
  document.documentElement.setAttribute("lang", LANG_BY_CODE[lang].bcp47);
}

i18n.use(initReactI18next).init({
  resources: {},
  lng: initialLang(),
  /* All catalogues carry the same keys (a test covers that), so the fallback
     language only kicks in for a key that none of them knows — and then that
     key itself stands there, loaded or not. */
  fallbackLng: BASE_LANG,
  interpolation: { escapeValue: false },
});

export default i18n;
