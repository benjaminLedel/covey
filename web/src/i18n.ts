import i18n from "i18next";
import { initReactI18next } from "react-i18next";

export type Lang = "de" | "en";

export const LANG_KEY = "covey.lang";

/* Startsprache. Zwei Aufrufer mit zwei Lagen:

   - Beim Vorrendern (web/prerender.mjs) gibt es weder window noch
     localStorage; dort gibt der Aufrufer den Pfad mit.
   - Im Browser hat der Pfad Vorrang vor der zuletzt gewählten Sprache: Wer
     /en/… direkt aufruft oder aus der Suche kommt, soll Englisch sehen, auch
     wenn hier einmal Deutsch gewählt wurde. Erst außerhalb der englischen
     URLs zählt die gespeicherte Wahl. */
export function langFromPath(pathname: string): Lang | null {
  return pathname === "/en" || pathname.startsWith("/en/") ? "en" : null;
}

/* Hat der Build diese Seite vorgerendert? Dann steht ihr Inhalt schon im HTML
   und der erste Rendervorgang im Browser muss ihn treffen. */
export function istVorgerendert(): boolean {
  if (typeof document === "undefined") return false;
  return document.getElementById("root")?.hasAttribute("data-prerendered") === true;
}

export function initialLang(pathname?: string): Lang {
  const path =
    pathname ?? (typeof window === "undefined" ? "/" : window.location.pathname);
  const fromPath = langFromPath(path);
  if (fromPath) return fromPath;

  /* Auf einer vorgerenderten Seite gilt, was der Server gerendert hat: alles
     außerhalb von /en/… ist deutsch. Ohne diese Zeile rendert der Browser eines
     Besuchers, der einmal auf Englisch umgeschaltet hat, die deutsche Seite
     zuerst englisch — React findet den vorgerenderten Text nicht wieder und
     wirft ihn samt Seite weg. Die gespeicherte Wahl kommt gleich danach wieder
     zum Zug: auf der öffentlichen Website über die URL (PublicSite), in der
     angemeldeten Oberfläche beim Aufbau der Shell (App.tsx). */
  if (istVorgerendert()) return "de";

  /* Englisch ist die Basissprache der angemeldeten Oberfläche — wer Deutsch
     will, wählt es einmal und bekommt es ab dann wieder. Die öffentliche
     Website bleibt davon unberührt: Sie ist vorgerendert und entscheidet eine
     Zeile höher über den Pfad. */
  return gespeicherteSprache() === "de" ? "de" : "en";
}

/* Die Abfrage hängt an window, nicht an localStorage: Node 25 bringt ein
   globales localStorage mit, das ohne --experimental-webstorage keine Methoden
   hat — die Prüfung „ist es definiert" ginge dort durch und das Vorrendern
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

/* Die Kataloge werden nachgeladen, nicht mitgeliefert. Zusammen sind sie
   262 kB — ein Fünftel des Bündels, und die Hälfte davon in einer Sprache,
   die dieser Besucher nicht liest. Als dynamischer Import wird jeder ein
   eigenes Stück, und geladen wird das eine, das gebraucht wird (#122).

   Der Aufrufer wartet darauf, bevor er rendert: Eine vorgerenderte Seite
   muss beim ersten Rendervorgang im Browser denselben Text erzeugen, den der
   Server geschrieben hat — mit leerem Katalog stünden dort die Schlüssel. */
const kataloge: Record<Lang, () => Promise<{ default: Record<string, unknown> }>> = {
  de: () => import("./locales/de.json"),
  en: () => import("./locales/en.json"),
};

export async function ladeSprache(lang: Lang): Promise<void> {
  if (!i18n.hasResourceBundle(lang, "translation")) {
    const { default: katalog } = await kataloge[lang]();
    i18n.addResourceBundle(lang, "translation", katalog, true, true);
  }
  if (i18n.language !== lang) await i18n.changeLanguage(lang);
}

i18n.use(initReactI18next).init({
  resources: {},
  lng: initialLang(),
  /* Beide Kataloge tragen dieselben Schlüssel (ein Test hält das fest), die
     Ersatzsprache greift also nur bei einem Schlüssel, den keiner von beiden
     kennt — und dann steht er selbst da, geladen oder nicht. */
  fallbackLng: "en",
  interpolation: { escapeValue: false },
});

export default i18n;
