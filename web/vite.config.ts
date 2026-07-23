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
});
