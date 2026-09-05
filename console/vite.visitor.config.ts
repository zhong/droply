import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

export default defineConfig({
  root: "visitor",
  base: "/_droply/ui/",
  plugins: [react(), tailwindcss()],
  build: {
    outDir: "../../internal/server/visitor_assets",
    emptyOutDir: true,
    assetsDir: "",
    rolldownOptions: {
      output: {
        entryFileNames: "visitor.js",
        assetFileNames: "visitor[extname]",
      },
    },
  },
});
