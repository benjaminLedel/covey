import React from "react";
import ReactDOM from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { BrowserRouter } from "react-router";
import App from "./App";
import { initialLang, ladeSprache } from "./i18n";
import { initTheme } from "./theme";

/* Die Schriften: siehe fonts.css — sie stehen dort selbst, damit sie einen
   unicode-range tragen können. */
import "./fonts.css";

import "./styles.css";

const queryClient = new QueryClient({
  defaultOptions: { queries: { retry: 1, refetchOnWindowFocus: false, staleTime: 3000 } },
});

const root = document.getElementById("root")!;

/* .js am Wurzelelement: Alles, was erst durch JavaScript sichtbar wird
   (die Scroll-Reveals), bleibt ohne JavaScript sichtbar. Das betrifft nicht
   nur Besucher mit abgeschaltetem Skript, sondern vor allem die Crawler, die
   kein JavaScript ausführen — sie sollen keinen Text vorfinden, der auf
   opacity: 0 steht. */
document.documentElement.classList.add("js");

/* Eine ausdrückliche Wahl fürs Erscheinungsbild vor dem ersten Rendervorgang
   anlegen, damit sie nicht erst nach dem Aufbau der Oberfläche durchschlägt.
   Ohne Wahl passiert hier nichts — dann entscheidet das Stylesheet nach der
   Systemeinstellung (siehe src/theme.ts). */
initTheme();

const tree = (
  <React.StrictMode>
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <App />
      </BrowserRouter>
    </QueryClientProvider>
  </React.StrictMode>
);

/* Erst der Katalog, dann React: Die Sprache steht schon fest (i18n.ts
   entscheidet sie aus Pfad und gespeicherter Wahl), nur ihre Texte liegen in
   einem eigenen Stück und sollen da sein, bevor gerendert wird — sonst blitzt
   der Schlüssel statt des Satzes auf.

   Gerendert wird frisch, nicht hydriert. Bis #130 gab es beides: Die
   vorgerenderten Seiten der Website mussten hydriert werden, damit React den
   Text des Servers nicht wegwirft. Die Website liegt jetzt woanders, und eine
   Anwendung startet leer. */
void ladeSprache(initialLang()).then(() => {
  ReactDOM.createRoot(root).render(tree);
});
