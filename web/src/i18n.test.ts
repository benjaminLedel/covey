import { afterEach, describe, expect, it, vi } from "vitest";
import { LANG_KEY, initialLang, langFromPath } from "./i18n";
import { PUBLIC_ROUTES } from "./public/routes";

/* Welche Sprache jemand zu sehen bekommt.

   Die Regel steht an einer Stelle (initialLang): die Adresse, sonst die
   gespeicherte Wahl, sonst der Browser, sonst die Basissprache. Beide Fehler,
   die dieser Test festhält, waren Abweichungen davon — eine im Pfad, eine in
   der angemeldeten Oberfläche — und beide waren unsichtbar, weil in der
   Entwicklung „en" gespeichert ist und dann jede Reihenfolge dasselbe
   ergibt. */

function browserSpricht(...sprachen: string[]) {
  vi.stubGlobal("navigator", { ...window.navigator, languages: sprachen, language: sprachen[0] });
}

afterEach(() => {
  vi.unstubAllGlobals();
  window.localStorage.clear();
});

describe("langFromPath", () => {
  /* Jede offene Adresse trägt ihre Sprache — auch die deutschen, die kein
      Präfix haben. Über das Präfix allein war /anmelden nie deutsch: der
      Reiter sagte „Anmelden — covey", der Text darunter richtete sich nach
      dem Browser. */
  for (const route of PUBLIC_ROUTES) {
    for (const [lang, pfad] of Object.entries(route.path)) {
      it(`erkennt ${pfad} als ${lang}`, () => {
        expect(langFromPath(pfad)).toBe(lang);
      });
    }
  }

  it("erkennt eine Adresse unterhalb eines Präfixes", () => {
    expect(langFromPath("/fr/irgendwas")).toBe("fr");
  });

  it("gibt für eine App-Adresse nichts zurück — sie trägt keine Sprache", () => {
    expect(langFromPath("/agents/1234")).toBeNull();
    expect(langFromPath("/")).toBeNull();
  });
});

describe("initialLang", () => {
  it("nimmt die Sprache der Adresse, auch gegen eine gespeicherte Wahl", () => {
    window.localStorage.setItem(LANG_KEY, "de");
    expect(initialLang("/fr/connexion")).toBe("fr");
  });

  it("nimmt die gespeicherte Wahl, wo die Adresse keine trägt", () => {
    window.localStorage.setItem(LANG_KEY, "pl");
    browserSpricht("fr-FR");
    expect(initialLang("/")).toBe("pl");
  });

  /* Der Fall der angemeldeten Oberfläche: sie fragt mit „/" und ohne
     gespeicherte Wahl. Stand dort ein festes „en", kippte sie gleich nach der
     Anmeldung von der Sprache, in der die Anmeldeseite gerade noch stand. */
  it("nimmt ohne gespeicherte Wahl die Sprache des Browsers", () => {
    browserSpricht("fr-FR", "en-US");
    expect(initialLang("/")).toBe("fr");
  });

  it("fällt auf die Basissprache zurück, wenn wir die des Browsers nicht haben", () => {
    browserSpricht("is-IS");
    expect(initialLang("/")).toBe("en");
  });
});
