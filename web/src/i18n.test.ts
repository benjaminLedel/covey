import { afterEach, describe, expect, it, vi } from "vitest";
import { LANG_KEY, initialLang, langFromPath } from "./i18n";
import { PUBLIC_ROUTES } from "./public/routes";

/* Which language a person gets to see.

   The rule stands in one place (initialLang): the address, else the saved
   choice, else the browser, else the base language. Both bugs this test holds
   were deviations from it — one in the path, one in the signed-in UI — and
   both were invisible, because in development `en` is stored and then every
   order gives the same
   result. */

function browserSpricht(...sprachen: string[]) {
  vi.stubGlobal("navigator", { ...window.navigator, languages: sprachen, language: sprachen[0] });
}

afterEach(() => {
  vi.unstubAllGlobals();
  window.localStorage.clear();
});

describe("langFromPath", () => {
  /* Every public address carries its language — including the German ones that
      have no prefix. Over the prefix alone /anmelden was never German: the tab
      said `Anmelden — covey`, the text below it
      followed the browser. */
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

  /* The case of the signed-in UI: it asks with `/` and without a saved choice.
     If a fixed `en` stood there, it tipped right after the sign-in out of the
     language the sign-in page had just been standing in. */
  it("nimmt ohne gespeicherte Wahl die Sprache des Browsers", () => {
    browserSpricht("fr-FR", "en-US");
    expect(initialLang("/")).toBe("fr");
  });

  it("fällt auf die Basissprache zurück, wenn wir die des Browsers nicht haben", () => {
    browserSpricht("is-IS");
    expect(initialLang("/")).toBe("en");
  });
});
