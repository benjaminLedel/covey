import React from "react";
import ReactDOM from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { BrowserRouter } from "react-router";
import App from "./App";
import { initialLang, ladeSprache } from "./i18n";
import { initTheme } from "./theme";

/* The fonts: see fonts.css — they stand there on their own so that they can
   carry a unicode-range. */
import "./fonts.css";

import "./styles.css";

const queryClient = new QueryClient({
  defaultOptions: { queries: { retry: 1, refetchOnWindowFocus: false, staleTime: 3000 } },
});

const root = document.getElementById("root")!;

/* .js on the root element: everything that only becomes visible through
   JavaScript (the scroll reveals) stays visible without JavaScript. That
   concerns not only visitors with the script switched off, but above all the
   crawlers that run no JavaScript — they should find no text sitting at
   opacity: 0. */
document.documentElement.classList.add("js");

/* Create an explicit choice for the appearance before the first render pass,
   so that it does not only take effect after the interface is built. Without
   a choice nothing happens here — then the stylesheet decides by the system
   setting (see src/theme.ts). */
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

/* First the catalogue, then React: the language is already settled (i18n.ts
   decides it from path and stored choice), only its texts lie in a chunk of
   their own and should be there before anything renders — otherwise the key
   flashes up instead of the sentence.

   Rendering is fresh, not hydrated. Until #130 there was both: the prerendered
   pages of the website had to be hydrated so that React would not throw away
   the text of the server. The website lives elsewhere now, and an application
   starts empty. */
void ladeSprache(initialLang()).then(() => {
  ReactDOM.createRoot(root).render(tree);
});
