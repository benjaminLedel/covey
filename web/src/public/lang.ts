/* Auf den beiden offenen Seiten entscheidet die Adresse über die Sprache, in
   der angemeldeten Oberfläche die persönliche Einstellung. Der Grund ist
   geblieben, auch wenn die Website ausgezogen ist (#130): /anmelden und
   /en/sign-in sind zwei Adressen, die jemand teilen und verlinken kann — und
   die der Proxy vor der Anwendung getrennt weiterleitet. */

import { useLocation } from "react-router";
import { initialLang } from "../i18n";
import type { Lang } from "./routes";

/* Der Pfad hat Vorrang; wo keiner eine Sprache trägt (die Weiterleitung von
   „/" auf die Anmeldung), zählt die gespeicherte Wahl und danach der Browser
   — dieselbe Reihenfolge wie beim Start (i18n.ts). Vor den zehn Sprachen
   stand hier ein festes „de": mit nur zwei Katalogen war das die eine
   Alternative zu /en/…, mit zehn wäre es eine Behauptung. */
export function usePublicLang(): Lang {
  const { pathname } = useLocation();
  return initialLang(pathname);
}
