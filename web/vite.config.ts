import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

import { APP_ROUTE_PREFIXES, LANGS, PUBLIC_ROUTES } from "./src/public/routes";

/* Die Routenliste für den Go-Handler. Er muss unterscheiden können, was zur
   Oberfläche gehört (und auf die Hülle fällt) und was ein Tippfehler ist (und
   eine 404 verdient) — kennen tut die Adressen aber der Browser-Code.

   Deshalb schreibt der Build sie mit: eine Quelle in src/public/routes.ts,
   zwei Verbraucher. Vor #130 kam die Datei aus prerender.mjs und hieß
   seo.json; sie trug damals auch die Liste fürs Vorrendern. */
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
            publicPaths: PUBLIC_ROUTES.flatMap((r) => LANGS.map((l) => r.path[l])),
          },
          null,
          2,
        ),
      });
    },
  };
}

// Dev: Vite-Dev-Server proxyt /api zum Go-Binary; Prod: dist/ wird via
// //go:embed ins Binary gebacken (spec/10).
export default defineConfig({
  plugins: [react(), tailwindcss(), appRouten()],
  server: {
    proxy: {
      "/api": { target: "http://localhost:8494", changeOrigin: false },
    },
  },
  build: { outDir: "dist" },
  // Tests laufen im selben Werkzeug wie der Build — damit gilt für sie
  // dieselbe Auflösung von Importen und Aliassen wie für die Anwendung und
  // nicht die einer zweiten, danebenstehenden Konfiguration.
  test: {
    environment: "jsdom",
    setupFiles: ["./src/test/setup.ts"],
    // Der Rest von web/ ist Anwendungscode; Tests liegen neben ihrem Gegenstück.
    include: ["src/**/*.test.{ts,tsx}"],
  },
});
