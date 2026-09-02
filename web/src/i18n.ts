import i18n from "i18next";
import { initReactI18next } from "react-i18next";

import { BASE_LANG, LANG_BY_CODE, LANGS, istLang, langPrefix, type Lang } from "./langs";
import { matchRoute } from "./public/routes";

export type { Lang };
export { LANG_BY_CODE, LANG_LIST, LANGS, istLang } from "./langs";

export const LANG_KEY = "covey.lang";

/* Startsprache. Zwei Lagen:

   - Vor der Anmeldung entscheidet die Adresse: /fr/connexion ist eine Adresse,
     die jemand teilen und verlinken kann, und der Proxy leitet sie getrennt
     weiter. Wer sie aufruft, soll Französisch sehen, auch wenn hier einmal
     Deutsch gewählt wurde.
   - Sonst zählt die gespeicherte Wahl, und wo es keine gibt, die Sprache des
     Browsers.

   Bis #130 stand hier ein dritter Fall: eine vorgerenderte Seite musste beim
   ersten Rendervorgang denselben Text treffen, den der Server geschrieben
   hatte. Die Website ist ausgezogen, die Anwendung startet leer (main.tsx). */
export function langFromPath(pathname: string): Lang | null {
  /* Zuerst die Adressen selbst, dann ihre Präfixe. Deutsch trägt keines
     (langPrefix gibt "" zurück, langs.ts), und über das Präfix allein wäre
     /anmelden für diese Funktion nie deutsch gewesen — die älteste Adresse
     der Anwendung hätte als einzige nicht über ihre Sprache entschieden,
     während der Reiter darüber schon „Anmelden — covey" sagte. */
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

/* Wer nichts gewählt hat, bekommt, was sein Browser verlangt — solange wir die
   Sprache haben. Die Kopfzeile eines Browsers ist eine Liste mit Regionen
   ("de-AT", "pt-BR"); uns interessiert der Teil davor, denn unsere Kataloge
   sind nach Sprache geschnitten, nicht nach Land. Bleibt nichts übrig, gilt
   die Basissprache: eine Oberfläche in einer Sprache, die keiner gewählt hat,
   wäre schlechter als eine in der, die alle lesen können. */
function sprachePerBrowser(): Lang | null {
  if (typeof navigator === "undefined") return null;
  const wuensche = navigator.languages?.length ? navigator.languages : [navigator.language];
  for (const wunsch of wuensche) {
    const basis = (wunsch || "").toLowerCase().split("-")[0];
    if (istLang(basis)) return basis;
  }
  return null;
}

/* Die Abfrage hängt an window, nicht an localStorage: Node 25 bringt ein
   globales localStorage mit, das ohne --experimental-webstorage keine Methoden
   hat — die Prüfung „ist es definiert" ginge dort durch und der Testlauf
   bräche. Der try/catch fängt außerdem den Browser, der Speicher verweigert
   (Privatmodus, geblockte Drittanbieter-Daten). */
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
    /* Ohne Speicher bleibt die Wahl auf diese Sitzung beschränkt. */
  }
}

/* Die Kataloge werden nachgeladen, nicht mitgeliefert. Zu zehnt sind sie über
   ein Megabyte — ein Vielfaches des Bündels, und neun Zehntel davon in
   Sprachen, die dieser Besucher nicht liest. Als dynamischer Import wird jeder
   ein eigenes Stück, und geladen wird das eine, das gebraucht wird (#122).

   Der Aufrufer wartet darauf, bevor er rendert: mit leerem Katalog stünden auf
   dem Schirm die Schlüssel statt der Sätze. */
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

/* Das lang-Attribut am <html> ist keine Kosmetik: Vorleseprogramme wählen
   danach ihre Aussprache, und der Browser seine Silbentrennung. Es steht im
   Markup auf „de" und muss mitwandern, wenn die Sprache wechselt. */
function setzeDokumentSprache(lang: Lang) {
  if (typeof document === "undefined") return;
  document.documentElement.setAttribute("lang", LANG_BY_CODE[lang].bcp47);
}

i18n.use(initReactI18next).init({
  resources: {},
  lng: initialLang(),
  /* Alle Kataloge tragen dieselben Schlüssel (ein Test hält das fest), die
     Ersatzsprache greift also nur bei einem Schlüssel, den keiner von ihnen
     kennt — und dann steht er selbst da, geladen oder nicht. */
  fallbackLng: BASE_LANG,
  interpolation: { escapeValue: false },
});

export default i18n;
