import i18n from "i18next";
import { initReactI18next } from "react-i18next";
import de from "./locales/de.json";
import en from "./locales/en.json";

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

  return gespeicherteSprache() === "en" ? "en" : "de";
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

i18n.use(initReactI18next).init({
  resources: { de: { translation: de }, en: { translation: en } },
  lng: initialLang(),
  fallbackLng: "de",
  interpolation: { escapeValue: false },
});

export default i18n;
