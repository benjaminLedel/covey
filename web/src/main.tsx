import React from "react";
import ReactDOM from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { BrowserRouter } from "react-router";
import App from "./App";
import { initialLang, istVorgerendert, ladeSprache } from "./i18n";
import { initTheme } from "./theme";

/* Inter und Lora liegen im Binary, nicht bei Google. Zwei Gründe: Die eigene
   CSP erlaubt keine fremden Stylesheet- und Font-Hosts (script-src/font-src
   'self'), das Google-Fonts-CDN war damit ohnehin blockiert und die Oberfläche
   lief auf Ersatzschriften. Und ein Aufruf an fonts.googleapis.com überträgt
   die IP jedes Besuchers an einen Dritten — für eine Software, die in
   deutschen Unternehmen läuft, keine gute Voreinstellung.
   Nur die tatsächlich benutzten Schnitte, nur die lateinischen Subsets. */
import "@fontsource/inter/latin-400.css";
import "@fontsource/inter/latin-500.css";
import "@fontsource/inter/latin-ext-400.css";
import "@fontsource/inter/latin-ext-500.css";
import "@fontsource/lora/latin-400.css";
import "@fontsource/lora/latin-400-italic.css";
import "@fontsource/lora/latin-ext-400.css";
import "@fontsource/lora/latin-ext-400-italic.css";

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

/* Vorgerenderte Seiten werden hydriert statt neu gerendert: Der Besucher sieht
   den Text sofort und behält ihn, statt ihn beim Start von React ersetzt zu
   bekommen. Alle anderen Einstiege (die angemeldete Oberfläche) rendern wie
   bisher frisch. */
/* Erst der Katalog, dann React. Die Sprache steht schon fest (i18n.ts
   entscheidet sie aus Pfad und gespeicherter Wahl) — nur ihre Texte liegen in
   einem eigenen Stück und müssen da sein, bevor gerendert wird: Auf einer
   vorgerenderten Seite muss der erste Rendervorgang den Text des Servers
   treffen, sonst verwirft React die Seite und baut sie neu auf. Sichtbar ist
   davon nichts — der Text steht ja schon im HTML. */
void ladeSprache(initialLang()).then(() => {
  if (istVorgerendert()) {
    ReactDOM.hydrateRoot(root, tree);
  } else {
    ReactDOM.createRoot(root).render(tree);
  }
});
