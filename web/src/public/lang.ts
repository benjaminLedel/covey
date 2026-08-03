/* Auf der öffentlichen Website entscheidet die URL über die Sprache, nicht der
   localStorage: Nur was eine Adresse hat, kann indexiert, verlinkt und geteilt
   werden. In der angemeldeten Oberfläche bleibt es umgekehrt — dort ist die
   Sprache eine persönliche Einstellung und gehört nicht in den Pfad. */

import { useEffect, useRef } from "react";
import { useLocation, useNavigate } from "react-router";
import { gespeicherteSprache, langFromPath } from "../i18n";
import { altPath, matchRoute, type Lang } from "./seo";

export function usePublicLang(): Lang {
  const { pathname } = useLocation();
  return langFromPath(pathname) ?? "de";
}

/* Wer die Sprache einmal umgeschaltet hat, soll beim nächsten Besuch nicht
   wieder in der anderen landen. Der Server kann das nicht lösen: Dieselbe
   Adresse je nach Accept-Language mal deutsch, mal englisch auszuliefern hieße,
   Google widersprüchlichen Inhalt unter einer URL zu zeigen — genau das Modell,
   das die eigenen Adressen je Sprache vermeiden. Also wird nicht anders
   gerendert, sondern auf die richtige Adresse gewechselt.

   Drei Einschränkungen, damit daraus keine Bevormundung wird:

   - Nur beim Einstieg. Wer im Verlauf des Besuchs auf eine Seite klickt, will
     diese Seite.
   - Nur aus dem Deutschen ins Englische, nie zurück. Die deutschen Pfade sind
     die unmarkierte Voreinstellung, auf der jeder Aufruf der blanken Domain
     landet; eine /en-Adresse hat dagegen immer jemand bewusst erzeugt — sei es
     die Suchmaschine über hreflang oder ein Kollege, der sie weitergibt. Ein
     geteilter Link wiegt schwerer als eine alte Wahl im eigenen Browser.
   - Nur auf eine bekannte Seite. Eine 404 bliebe sonst keine 404, sondern
     würde zur Startseite.

   Crawler haben keinen localStorage und sehen von alldem nichts — die
   Indexierung bleibt unberührt. Erstbesucher führt die Suchmaschine über
   hreflang ohnehin direkt auf die passende Fassung. */
export function useSprachwahlFolgen() {
  const { pathname } = useLocation();
  const navigate = useNavigate();
  const erledigt = useRef(false);

  useEffect(() => {
    if (erledigt.current) return;
    erledigt.current = true;

    if (gespeicherteSprache() !== "en") return;
    if (langFromPath(pathname) === "en") return;
    if (!matchRoute(pathname)) return;

    navigate(altPath(pathname, "en"), { replace: true });
  }, [pathname, navigate]);
}
