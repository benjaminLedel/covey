import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// Dev: Vite-Dev-Server proxyt /api zum Go-Binary; Prod: dist/ wird via
// //go:embed ins Binary gebacken (spec/10).
export default defineConfig({
  plugins: [react(), tailwindcss()],
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
