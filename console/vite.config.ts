import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

export default defineConfig({
  base: "/console/",
  plugins: [react(), tailwindcss()],
  build: {
    // Keep the existing Go embed boundary and plain `go build` workflow.
    outDir: "../internal/server/console_assets",
    emptyOutDir: true,
    assetsDir: "",
    rolldownOptions: {
      output: {
        entryFileNames: "app.js",
        chunkFileNames: "[name]-[hash].js",
        assetFileNames: "[name][extname]",
      },
    },
  },
});
