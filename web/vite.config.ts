import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

import { APP_ROUTE_PREFIXES, LANGS, MAIL_LINK_PATHS, PUBLIC_ROUTES } from "./src/public/routes";

/* The route list for the Go handler. It has to tell apart what belongs to the
   UI (and falls back to the shell) from what is a typo (and deserves a 404),
   but the browser code is the one that knows the addresses.

   So the build writes it out: one source in src/public/routes.ts, two
   consumers. Before #130 the file came from prerender.mjs and was called
   seo.json; back then it also carried the list for pre-rendering. */
function appRouten() {
  return {
    name: "covey-app-routes",
    generateBundle() {
      this.emitFile({
        type: "asset",
        fileName: "app-routes.json",
        source: JSON.stringify(
          {
            appPrefixes: APP_ROUTE_PREFIXES,
            publicPaths: [
              ...PUBLIC_ROUTES.flatMap((r) => LANGS.map((l) => r.path[l])),
              ...MAIL_LINK_PATHS,
            ],
          },
          null,
          2,
        ),
      });
    },
  };
}

// Dev: the Vite dev server proxies /api to the Go binary; prod: dist/ is
// baked into the binary via //go:embed (spec/10).
export default defineConfig({
  plugins: [react(), tailwindcss(), appRouten()],
  server: {
    proxy: {
      "/api": { target: "http://localhost:8494", changeOrigin: false },
    },
  },
  build: { outDir: "dist" },
  // Tests run in the same tool as the build, so the same resolution of
  // imports and aliases applies to them as to the application, and not the
  // one of a second configuration sitting beside it.
  test: {
    environment: "jsdom",
    setupFiles: ["./src/test/setup.ts"],
    // The rest of web/ is application code; tests sit beside their counterpart.
    include: ["src/**/*.test.{ts,tsx}"],
    // The office tests build the real scene in node — sixty colleagues with
    // furniture take a few seconds when thirty files run at once. The default
    // five seconds made them fail under load and pass alone.
    testTimeout: 30_000,
  },
});
