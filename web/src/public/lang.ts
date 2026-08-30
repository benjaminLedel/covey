/* Auf den beiden offenen Seiten entscheidet die Adresse über die Sprache, in
   der angemeldeten Oberfläche die persönliche Einstellung. Der Grund ist
   geblieben, auch wenn die Website ausgezogen ist (#130): /anmelden und
   /en/sign-in sind zwei Adressen, die jemand teilen und verlinken kann — und
   die der Proxy vor der Anwendung getrennt weiterleitet. */

import { useLocation } from "react-router";
import { langFromPath } from "../i18n";
import type { Lang } from "./routes";

export function usePublicLang(): Lang {
  const { pathname } = useLocation();
  return langFromPath(pathname) ?? "de";
}
