/* The languages the interface speaks — one list, three readers.
 *
 * It sits in a module of its own and depends on nothing: `i18n.ts` pulls
 * i18next along, and `public/routes.ts` is imported by `vite.config.ts`, which
 * runs in Node before anything is bundled. A shared list that dragged i18next
 * into the build config would be a circular favour to nobody.
 *
 * The flag is a compromise and it is worth naming: a flag is a country, a
 * language is not. English is spoken far beyond Britain, Portuguese far beyond
 * Portugal, Spanish far beyond Spain. The picker still shows one, because a
 * row of ten two-letter codes is unreadable at a glance and a visitor looks
 * for a picture, not for ISO 639. The native name stands beside it and carries
 * the actual meaning.
 */

export type Lang = "de" | "en" | "fr" | "es" | "it" | "nl" | "pl" | "pt" | "ja" | "zh";

export type LangInfo = {
  code: Lang;
  /** The name of the language IN that language — a visitor who cannot read the
      current one has to find their own. */
  name: string;
  flag: string;
  /** For `<html lang>` and screen readers. */
  bcp47: string;
};

/* German and English lead because they came first and the repository's own
   texts are written in them; the rest follows the Latin alphabet, so nobody
   has to guess at a ranking. */
export const LANG_LIST: LangInfo[] = [
  { code: "de", name: "Deutsch", flag: "🇩🇪", bcp47: "de" },
  { code: "en", name: "English", flag: "🇬🇧", bcp47: "en" },
  { code: "es", name: "Español", flag: "🇪🇸", bcp47: "es" },
  { code: "fr", name: "Français", flag: "🇫🇷", bcp47: "fr" },
  { code: "it", name: "Italiano", flag: "🇮🇹", bcp47: "it" },
  { code: "nl", name: "Nederlands", flag: "🇳🇱", bcp47: "nl" },
  { code: "pl", name: "Polski", flag: "🇵🇱", bcp47: "pl" },
  { code: "pt", name: "Português", flag: "🇵🇹", bcp47: "pt" },
  { code: "ja", name: "日本語", flag: "🇯🇵", bcp47: "ja" },
  { code: "zh", name: "中文", flag: "🇨🇳", bcp47: "zh-Hans" },
];

export const LANGS: Lang[] = LANG_LIST.map((l) => l.code);

export const LANG_BY_CODE: Record<Lang, LangInfo> = Object.fromEntries(
  LANG_LIST.map((l) => [l.code, l]),
) as Record<Lang, LangInfo>;

/** The base language: what somebody sees who has chosen nothing and whose
    browser asks for a language we do not have. */
export const BASE_LANG: Lang = "en";

/** Is this string one of ours? Whatever comes out of localStorage, a URL or a
    browser setting belongs to somebody else — a catalogue that does not exist
    would be a load error instead of a default. */
export function istLang(value: unknown): value is Lang {
  return typeof value === "string" && (LANGS as string[]).includes(value);
}

/* The URL prefix of a language, empty for German.
 *
 * German has none for the same reason English keeps translated slugs
 * (/en/sign-in, not /en/anmelden): those addresses are in bookmarks, in the
 * documentation and in the proxy's redirects. The eight that came later follow
 * one rule — prefix plus a slug in their own language, transcribed to ASCII
 * where the script is not Latin. */
export function langPrefix(lang: Lang): string {
  return lang === "de" ? "" : `/${lang}`;
}
