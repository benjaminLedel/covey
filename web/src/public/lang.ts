/* Auf der öffentlichen Website entscheidet die URL über die Sprache, nicht der
   localStorage: Nur was eine Adresse hat, kann indexiert, verlinkt und geteilt
   werden. In der angemeldeten Oberfläche bleibt es umgekehrt — dort ist die
   Sprache eine persönliche Einstellung und gehört nicht in den Pfad. */

import { useLocation } from "react-router";
import { langFromPath } from "../i18n";
import type { Lang } from "./seo";

export function usePublicLang(): Lang {
  const { pathname } = useLocation();
  return langFromPath(pathname) ?? "de";
}
